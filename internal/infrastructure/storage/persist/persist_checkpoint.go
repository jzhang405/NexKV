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

// PersistCheckpoint is a KVStore decorator that periodically flushes dirty BTree
// pages to AO chunk files (cf. Lealone's BTreeStorage.save()).
type PersistCheckpoint struct {
	service.KVStore

	enumerateFn func(checkpoint.PageRef) ([]checkpoint.PageFlushItem, error)
	rootFn      func() checkpoint.PageRef
	chunkMgr    service.ChunkManager
	ckptInterval int64
	setCount     atomic.Uint64
	saving       atomic.Bool
	saveMu       sync.Mutex
	stats        CkptStats
}

// CkptStats holds runtime checkpoint statistics for benchmark output.
type CkptStats struct {
	SaveCount  int64
	PageCount  int64
	LastSaveMs int64
	TotalSaves int64
}

// NewPersistCheckpoint creates a Checkpoint decorator.
//
//	tree      — KVStore backend (e.g. BTree)
//	rootFn    — returns the current root page (e.g. btree.RootPage)
//	enumFn    — enumerates dirty pages from a root (e.g. btree.EnumeratePages)
//	chunkMgr  — AO chunk storage
//	interval  — how many Set() ops between async saves (0 = default 10K)
func NewPersistCheckpoint(
	tree service.KVStore,
	rootFn func() checkpoint.PageRef,
	enumFn func(checkpoint.PageRef) ([]checkpoint.PageFlushItem, error),
	cm service.ChunkManager,
	interval int64,
) *PersistCheckpoint {
	if interval <= 0 {
		interval = 10000
	}
	return &PersistCheckpoint{
		KVStore:      tree,
		rootFn:       rootFn,
		enumerateFn:  enumFn,
		chunkMgr:     cm,
		ckptInterval: interval,
	}
}

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

	elapsed := time.Since(start)
	atomic.StoreInt64(&p.stats.LastSaveMs, elapsed.Milliseconds())
	atomic.AddInt64(&p.stats.TotalSaves, 1)
	atomic.StoreInt64(&p.stats.PageCount, pageCount)
	return nil
}

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
	return p.KVStore.Close()
}

// String returns a human-readable description.
func (p *PersistCheckpoint) String() string {
	return fmt.Sprintf("PersistCheckpoint(interval=%d sets=%d saves=%d)",
		p.ckptInterval, p.setCount.Load(), atomic.LoadInt64(&p.stats.TotalSaves))
}
