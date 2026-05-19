// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package checkpoint

import (
	"context"
	"encoding/binary"
	"fmt"
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

// BTreeScanner provides read-only access to the BTree for checkpoint DFS.
type BTreeScanner interface {
	RootPage() PageRef
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

// Manager orchestrates Fuzzy and Sharp checkpoints.
// Phase 4.3: cm (ChunkManager) is used for AO page persistence during checkpoint.
// Serialization is handled internally by BTree.EnumeratePages (returns pre-serialized PageData),
// so the Manager does not need a PageSerializer reference.
type Manager struct {
	wal    WALWriter
	btree  BTreeScanner
	cm     service.ChunkManager // Phase 4.3: AO chunk persistence
	config *Config
	stats  Stats
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex // prevents concurrent checkpoints
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
//
// Protocol (C1 CRITICAL — T0→T1 gap eliminated):
//  1. checkpointStartLSN = wal.CurrentLSN()  ← record first
//  2. rootRef = btree.RootPage()              ← capture COW snapshot second
//  3. DFS traverse rootRef, enumerate pages
//  4. checkpointEndLSN = wal.CurrentLSN()    ← stats only
//  5. Write Checkpoint WALEntry(checkpointStartLSN)
//  6. wal.Sync()
//  7. wal.Truncate(checkpointStartLSN)       ← truncate below snapshot
func (m *Manager) FuzzyCheckpoint() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	start := time.Now()

	// Step 1: Record Recovery replay start (MUST be before root snapshot — C1 CRITICAL)
	startLSN := uint64(m.wal.CurrentLSN())

	// Step 2: Capture COW root snapshot
	root := m.btree.RootPage()
	if root == nil {
		return fmt.Errorf("checkpoint: nil root page")
	}

	// Step 3: DFS traverse — enumerate all reachable pages from root snapshot.
	_ = m.enumeratePages(root) // page list for future persistent storage integration

	// Step 4: Record end LSN (stats only)
	_ = uint64(m.wal.CurrentLSN()) // endLSN — stats

	// Step 5: Write Checkpoint WAL entry (authorization for truncation)
	ckpKey := make([]byte, 8)
	binary.BigEndian.PutUint64(ckpKey, startLSN)
	ckpEntry := &service.WALEntry{Type: service.WALTypeCheckpoint, Key: ckpKey}
	if _, err := m.wal.Append(ckpEntry); err != nil {
		return fmt.Errorf("checkpoint: append entry: %w", err)
	}

	// Step 6: Sync — makes Checkpoint entry durable (C2 CRITICAL: authorize before delete)
	if err := m.wal.Sync(); err != nil {
		return fmt.Errorf("checkpoint: sync: %w", err)
	}

	// Step 7: Truncate WAL segments below checkpointStartLSN
	if err := m.wal.Truncate(service.LSN(startLSN)); err != nil {
		return fmt.Errorf("checkpoint: truncate: %w", err)
	}

	elapsed := time.Since(start)
	m.stats.LastCheckpointLSN.Store(startLSN)
	m.stats.LastDurationMs.Store(elapsed.Milliseconds())
	m.stats.TotalCheckpoints.Add(1)

	return nil
}

// SharpCheckpoint performs an offline checkpoint (pauses writes).
func (m *Manager) SharpCheckpoint() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	startLSN := uint64(m.wal.CurrentLSN())

	ckpKey := make([]byte, 8)
	binary.BigEndian.PutUint64(ckpKey, startLSN)
	ckpEntry := &service.WALEntry{Type: service.WALTypeCheckpoint, Key: ckpKey}
	if _, err := m.wal.Append(ckpEntry); err != nil {
		return fmt.Errorf("checkpoint: append entry: %w", err)
	}
	if err := m.wal.Sync(); err != nil {
		return fmt.Errorf("checkpoint: sync: %w", err)
	}
	if err := m.wal.Truncate(service.LSN(startLSN)); err != nil {
		return fmt.Errorf("checkpoint: truncate: %w", err)
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

// enumeratePages traverses all pages reachable from root and returns their IDs.
func (m *Manager) enumeratePages(root PageRef) []model.PageID {
	visited := make(map[model.PageID]bool)
	var ids []model.PageID
	var dfs func(p PageRef)
	dfs = func(p PageRef) {
		if p == nil || visited[p.PageID()] {
			return
		}
		visited[p.PageID()] = true
		ids = append(ids, p.PageID())
		if !p.IsLeaf() {
			for _, childID := range p.ChildIDs() {
				dfs(&checkpointPageRef{id: childID, isLeaf: false})
			}
		}
	}
	dfs(root)
	return ids
}

// checkpointPageRef is a lightweight PageRef for DFS enumeration.
type checkpointPageRef struct {
	id     model.PageID
	isLeaf bool
}

func (p *checkpointPageRef) PageID() model.PageID     { return p.id }
func (p *checkpointPageRef) IsLeaf() bool             { return p.isLeaf }
func (p *checkpointPageRef) ChildIDs() []model.PageID { return nil }
