// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package checkpoint

import (
	"context"
	"encoding/binary"
	"fmt"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// WALWriter is the minimal WAL interface needed for checkpoint (interface segregation).
type WALWriter interface {
	Append(entry *service.WALEntry) (service.LSN, error)
	Sync() error
	Truncate(lsn service.LSN) error
	CurrentLSN() service.LSN
}

// PageFlushItem encapsulates the data needed to flush a single page to AO during checkpoint.
type PageFlushItem struct {
	PageID   model.PageID        // logical page ID
	PageType uint8               // 0=index, 1=leaf
	PageData []byte              // pre-serialized page data; nil = already persisted
	ChunkPos model.ChunkPosition // current AO position (0 = dirty, needs Alloc+Write)
}

// BTreeScanner provides read-only access to the BTree for checkpoint DFS.
type BTreeScanner interface {
	RootPage() PageRef
	// EnumeratePages performs post-order DFS from root, returning page flush items.
	EnumeratePages(root PageRef) ([]PageFlushItem, error)
}

// PageRef is a BTree page reference for checkpoint traversal.
type PageRef interface {
	PageID() model.PageID
	IsLeaf() bool
	ChildIDs() []model.PageID
}

// Stats tracks checkpoint metrics.
type Stats struct {
	LastCheckpointLSN atomic.Uint64
	LastDurationMs    atomic.Int64
	TotalCheckpoints  atomic.Uint64
}

// Compactor triggers chunk space reclamation after checkpoint.
type Compactor interface {
	NeedCompaction() bool
	Compact() error
}

// Manager orchestrates Fuzzy and Sharp checkpoints.
type Manager struct {
	wal       WALWriter
	btree     BTreeScanner
	cm        service.ChunkManager // Phase 4.3: AO chunk persistence
	compactor Compactor            // Phase 4.4: chunk space reclamation
	config    *Config
	stats     Stats
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex // prevents concurrent checkpoints
}

// SetCompactor injects the chunk compactor for post-checkpoint space reclamation.
func (m *Manager) SetCompactor(c Compactor) {
	m.compactor = c
}

// NewManager creates a checkpoint manager.
func NewManager(wal WALWriter, btree BTreeScanner, cm service.ChunkManager, config *Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		wal:    wal,
		btree:  btree,
		cm:     cm,
		config: config,
		ctx:    ctx,
		cancel: cancel,
	}
}

// FuzzyCheckpoint performs an online checkpoint without pausing writes.
func (m *Manager) FuzzyCheckpoint() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	start := time.Now()

	// Step 1: Record checkpoint start LSN (MUST be before root snapshot)
	startLSN := uint64(m.wal.CurrentLSN())

	// Step 2: Capture COW root snapshot
	root := m.btree.RootPage()
	if root == nil {
		return errpkg.Wrap(errpkg.ErrCheckpointNilRoot, "checkpoint: nil root page")
	}

	// Step 3: Enumerate pages + flush dirty pages to AO (Phase 4.3)
	var pageLocs map[model.PageID]model.ChunkPosition
	if m.cm != nil {
		items, err := m.btree.EnumeratePages(root)
		if err != nil {
			return errpkg.Wrap(err, "checkpoint: enumerate pages")
		}
		pageLocs = make(map[model.PageID]model.ChunkPosition, len(items))
		for _, item := range items {
			pageLocs[item.PageID] = item.ChunkPos
			if item.ChunkPos == 0 && item.PageData != nil {
				pos, err := m.cm.Allocate(len(item.PageData), item.PageType)
				if err != nil {
					return errpkg.Wrap(err, fmt.Sprintf("checkpoint: allocate page %d", item.PageID))
				}
				if err := m.cm.WritePage(pos, item.PageData); err != nil {
					return errpkg.Wrap(err, fmt.Sprintf("checkpoint: write page %d", item.PageID))
				}
				pageLocs[item.PageID] = pos
			}
		}
		if err := m.cm.Sync(); err != nil {
			return errpkg.Wrap(err, "checkpoint: sync chunks")
		}
	}

	// Step 4: Write Checkpoint WAL entry
	ckpKey := encodeCheckpointKey(startLSN, pageLocs)
	ckpEntry := &service.WALEntry{Type: service.WALTypeCheckpoint, Key: ckpKey}
	if _, err := m.wal.Append(ckpEntry); err != nil {
		return errpkg.Wrap(err, "checkpoint: append entry")
	}

	// Step 5: Sync WAL
	if err := m.wal.Sync(); err != nil {
		return errpkg.Wrap(err, "checkpoint: sync")
	}

	// Step 6: Truncate WAL segments below checkpointStartLSN
	if err := m.wal.Truncate(service.LSN(startLSN)); err != nil {
		return errpkg.Wrap(err, "checkpoint: truncate")
	}

	elapsed := time.Since(start)
	m.stats.LastCheckpointLSN.Store(startLSN)
	m.stats.LastDurationMs.Store(elapsed.Milliseconds())
	m.stats.TotalCheckpoints.Add(1)

	// Phase 4.4: Async chunk compaction (does not block checkpoint)
	if m.compactor != nil && m.compactor.NeedCompaction() {
		go func() { _ = m.compactor.Compact() }()
	}

	// Phase 6.5 (G5): Async BTree tombstone compaction (does not block checkpoint)
	if tc, ok := m.btree.(interface{ TryCompact() error }); ok {
		go func() { _ = tc.TryCompact() }()
	}

	return nil
}

// encodeCheckpointKey encodes the checkpoint key.
// Key format: [startLSN:8][PageCount:4][(PageID:8,ChunkPos:8)*N]
// PageCount=0 means no pageLocs (equivalent to old Phase 3 checkpoint;
// e.g., SharpCheckpoint without ChunkManager).
func encodeCheckpointKey(startLSN uint64, pageLocs map[model.PageID]model.ChunkPosition) []byte {
	n := 8 + 4 + len(pageLocs)*16
	key := make([]byte, n)
	binary.BigEndian.PutUint64(key[0:8], startLSN)
	binary.BigEndian.PutUint32(key[8:12], uint32(len(pageLocs)))
	offset := 12
	for pageID, pos := range pageLocs {
		binary.BigEndian.PutUint64(key[offset:offset+8], uint64(pageID))
		binary.BigEndian.PutUint64(key[offset+8:offset+16], uint64(pos))
		offset += 16
	}
	return key
}

// SharpCheckpoint performs an offline checkpoint (pauses writes).
func (m *Manager) SharpCheckpoint() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	startLSN := uint64(m.wal.CurrentLSN())

	ckpKey := encodeCheckpointKey(startLSN, nil)
	ckpEntry := &service.WALEntry{Type: service.WALTypeCheckpoint, Key: ckpKey}
	if _, err := m.wal.Append(ckpEntry); err != nil {
		return errpkg.Wrap(err, "checkpoint: append entry")
	}
	if err := m.wal.Sync(); err != nil {
		return errpkg.Wrap(err, "checkpoint: sync")
	}
	if err := m.wal.Truncate(service.LSN(startLSN)); err != nil {
		return errpkg.Wrap(err, "checkpoint: truncate")
	}

	m.stats.LastCheckpointLSN.Store(startLSN)
	m.stats.TotalCheckpoints.Add(1)
	return nil
}

// Run starts the background Fuzzy Checkpoint goroutine.
func (m *Manager) Run() {
	go func() {
		ticker := time.NewTicker(m.config.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = m.FuzzyCheckpoint()
			case <-m.ctx.Done():
				return
			}
		}
	}()
}

// Shutdown gracefully stops the background checkpoint goroutine.
func (m *Manager) Shutdown() error {
	m.cancel()
	return m.SharpCheckpoint()
}
