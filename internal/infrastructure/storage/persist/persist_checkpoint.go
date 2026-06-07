// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package persist

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/checkpoint"
)

const maxDirtyBytesPerSave = 256 * 1024 * 1024 // 256MB (Lealone MAX_CHUNK_SIZE equiv)

// dirtyBytesReader exposes the Lealone collectDirtyMemory() equivalent.
type dirtyBytesReader interface{ DirtyBytes() uint64 }

// resetDirtyBytesWriter allows resetting the dirty byte counter after save.
type resetDirtyBytesWriter interface{ ResetDirtyBytes() }

// ---------------------------------------------------------------------------
// CheckpointOption — Functional Options (Lealone-style tuning)
// ---------------------------------------------------------------------------

// CheckpointOption configures PersistCheckpoint behaviour.
type CheckpointOption func(*checkpointConfig)

type checkpointConfig struct {
	ckptInterval         int64
	maxIdleDuration      time.Duration
	maxDirtyBytesPerSave int64
}

func defaultCheckpointConfig() *checkpointConfig {
	return &checkpointConfig{
		ckptInterval:         10000,
		maxIdleDuration:      3 * time.Second,
		maxDirtyBytesPerSave: maxDirtyBytesPerSave,
	}
}

// WithCkptInterval sets the number of Set() ops between async checkpoints.
func WithCkptInterval(n int64) CheckpointOption {
	return func(c *checkpointConfig) { c.ckptInterval = n }
}

// WithMaxIdleDuration sets the maximum idle time before a forced save.
func WithMaxIdleDuration(d time.Duration) CheckpointOption {
	return func(c *checkpointConfig) { c.maxIdleDuration = d }
}

// WithMaxDirtyBytes sets the dirty byte threshold for warning logs.
func WithMaxDirtyBytes(n int64) CheckpointOption {
	return func(c *checkpointConfig) { c.maxDirtyBytesPerSave = n }
}

// ---------------------------------------------------------------------------
// PersistCheckpoint — KVStore decorator (Lealone BTreeStorage.save() equiv)
// ---------------------------------------------------------------------------

// PersistCheckpoint is a KVStore decorator that periodically flushes dirty BTree
// pages to AO chunk files (cf. Lealone's BTreeStorage.save()).
//
// Three trigger dimensions (Lealone LogSyncService equivalent):
//  1. count:  setCount % ckptInterval == 0 → asyncSave
//  2. time:   idle > maxIdleDuration → forced save (background goroutine)
//  3. memory: dirtyBytes > maxDirtyBytesPerSave → warning log
type PersistCheckpoint struct {
	service.KVStore

	enumerateFn          func(checkpoint.PageRef) ([]checkpoint.PageFlushItem, error)
	rootFn               func() checkpoint.PageRef
	chunkMgr             service.ChunkManager
	dirtyReader          dirtyBytesReader
	dirtyReset           resetDirtyBytesWriter
	ckptInterval         int64
	maxIdleDuration      time.Duration
	maxDirtyBytesPerSave int64
	setCount             atomic.Uint64
	lastSavedCount       atomic.Uint64 // saved-at count (prevents idle-check false positive)
	saving               atomic.Bool
	dirtyWarned          atomic.Bool // suppresses duplicate dirty-byte warnings
	saveMu               sync.Mutex
	stats                CkptStats

	// idle goroutine (Lealone LogSyncService.run() equiv)
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// CkptStats holds runtime checkpoint statistics for benchmark output.
type CkptStats struct {
	SaveCount  int64
	PageCount  int64
	LastSaveMs int64
	TotalSaves int64
}

// NewPersistCheckpoint creates a Checkpoint decorator.
func NewPersistCheckpoint(
	tree service.KVStore,
	rootFn func() checkpoint.PageRef,
	enumFn func(checkpoint.PageRef) ([]checkpoint.PageFlushItem, error),
	cm service.ChunkManager,
	opts ...CheckpointOption,
) *PersistCheckpoint {
	cfg := defaultCheckpointConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	dr, _ := tree.(dirtyBytesReader)
	dw, _ := tree.(resetDirtyBytesWriter)

	p := &PersistCheckpoint{
		KVStore:              tree,
		rootFn:               rootFn,
		enumerateFn:          enumFn,
		chunkMgr:             cm,
		dirtyReader:          dr,
		dirtyReset:           dw,
		ckptInterval:         cfg.ckptInterval,
		maxIdleDuration:      cfg.maxIdleDuration,
		maxDirtyBytesPerSave: cfg.maxDirtyBytesPerSave,
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())

	// Start background idle-check goroutine (Lealone LogSyncService.run() equiv)
	p.wg.Add(1)
	go p.runIdleCheckLoop()
	return p
}

// ---------------------------------------------------------------------------
// KVStore interceptors
// ---------------------------------------------------------------------------

// Set intercepts KVStore.Set to count writes and trigger async checkpoint.
func (p *PersistCheckpoint) Set(ctx context.Context, key, value []byte) error {
	if err := p.KVStore.Set(ctx, key, value); err != nil {
		return err
	}
	p.maybeTriggerCkpt(p.setCount.Add(1))
	return nil
}

// SetBatch intercepts batch writes.
func (p *PersistCheckpoint) SetBatch(ctx context.Context, pairs []service.KVPair) error {
	if err := p.KVStore.SetBatch(ctx, pairs); err != nil {
		return err
	}
	p.maybeTriggerCkpt(p.setCount.Add(uint64(len(pairs))))
	return nil
}

func (p *PersistCheckpoint) maybeTriggerCkpt(count uint64) {
	if count%uint64(p.ckptInterval) == 0 && p.saving.CompareAndSwap(false, true) {
		go func() {
			defer p.saving.Store(false)
			p.asyncSave()
		}()
	}
}

// Save triggers a synchronous checkpoint.
func (p *PersistCheckpoint) Save() error {
	p.saveMu.Lock()
	defer p.saveMu.Unlock()
	return p.saveInternal()
}

func (p *PersistCheckpoint) asyncSave() {
	p.saveMu.Lock()
	defer p.saveMu.Unlock()
	_ = p.saveInternal()
}

// ---------------------------------------------------------------------------
// idle checkpoint goroutine (Lealone LogSyncService.run() equiv)
// ---------------------------------------------------------------------------

func (p *PersistCheckpoint) runIdleCheckLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.maxIdleDuration)
	defer ticker.Stop()

	lastTickCount := p.setCount.Load()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			cur := p.setCount.Load()
			// Trigger forced save if:
			//   cur == lastTickCount  → no new writes since last tick
			//   cur > lastSavedCount  → there is unpersisted data
			//   CAS succeeds          → no save already in progress
			if cur == lastTickCount && cur > p.lastSavedCount.Load() &&
				p.saving.CompareAndSwap(false, true) {
				go func() {
					defer p.saving.Store(false)
					p.asyncSave()
				}()
			}
			lastTickCount = cur
		}
	}
}

// ---------------------------------------------------------------------------
// save core
// ---------------------------------------------------------------------------

func (p *PersistCheckpoint) saveInternal() error {
	start := time.Now()
	root := p.rootFn()
	if root == nil {
		return errpkg.Wrap(errpkg.ErrCheckpointNilRoot, "checkpoint: nil root")
	}

	items, err := p.enumerateFn(root)
	if err != nil {
		log.Printf("persist checkpoint: enumerate failed: %v", err)
		return err
	}

	// Dirty-byte threshold check (O(1) atomic read, Lealone collectDirtyMemory equiv)
	if p.dirtyReader != nil {
		totalBytes := int64(p.dirtyReader.DirtyBytes())
		if totalBytes > p.maxDirtyBytesPerSave && p.dirtyWarned.CompareAndSwap(false, true) {
			log.Printf("persist checkpoint: dirty bytes %d > max %d, "+
				"consider reducing ckptInterval (current=%d)", totalBytes,
				p.maxDirtyBytesPerSave, p.ckptInterval)
		} else if totalBytes <= p.maxDirtyBytesPerSave {
			p.dirtyWarned.Store(false)
		}
	}

	var pageCount int64
	for _, item := range items {
		if item.ChunkPos != 0 || item.PageData == nil {
			continue
		}
		pos, err := p.chunkMgr.Allocate(len(item.PageData), item.PageType)
		if err != nil {
			log.Printf("persist checkpoint: allocate page %d: %v", item.PageID, err)
			_ = p.chunkMgr.RollbackLastBatch()
			return err
		}
		if err := p.chunkMgr.WritePage(pos, item.PageData); err != nil {
			log.Printf("persist checkpoint: write page %d: %v", item.PageID, err)
			_ = p.chunkMgr.RollbackLastBatch()
			return err
		}
		pageCount++
	}

	if err := p.chunkMgr.Sync(); err != nil {
		log.Printf("persist checkpoint: sync: %v", err)
		return err
	}

	// Reset dirty counter after successful save (Lealone reset dirtyMemory equiv)
	if p.dirtyReset != nil {
		p.dirtyReset.ResetDirtyBytes()
	}
	p.lastSavedCount.Store(p.setCount.Load())

	elapsed := time.Since(start)
	atomic.StoreInt64(&p.stats.LastSaveMs, elapsed.Milliseconds())
	atomic.AddInt64(&p.stats.TotalSaves, 1)
	atomic.StoreInt64(&p.stats.PageCount, pageCount)
	return nil
}

// ---------------------------------------------------------------------------
// stats / lifecycle
// ---------------------------------------------------------------------------

// CkptStats returns checkpoint statistics.
func (p *PersistCheckpoint) CkptStats() CkptStats {
	return CkptStats{
		SaveCount:  atomic.LoadInt64(&p.stats.TotalSaves),
		PageCount:  atomic.LoadInt64(&p.stats.PageCount),
		LastSaveMs: atomic.LoadInt64(&p.stats.LastSaveMs),
		TotalSaves: atomic.LoadInt64(&p.stats.TotalSaves),
	}
}

// Close performs a final sync checkpoint with timeout.
func (p *PersistCheckpoint) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		p.saveMu.Lock()
		_ = p.saveInternal()
		p.saveMu.Unlock()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		log.Printf("persist checkpoint: close timeout: %v", ctx.Err())
	}

	p.cancel()
	p.wg.Wait()
	return p.KVStore.Close()
}

// String returns a human-readable description.
func (p *PersistCheckpoint) String() string {
	return fmt.Sprintf("PersistCheckpoint(interval=%d sets=%d saves=%d idle=%v)",
		p.ckptInterval, p.setCount.Load(), atomic.LoadInt64(&p.stats.TotalSaves),
		p.maxIdleDuration)
}
