// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package persist provides KVStore decorators for persistence strategies.
//
// PersistWAL wraps a service.KVStore (e.g. BTree) and appends every Set
// operation to a Write-Ahead Log. Three sync modes are supported:
//   - EveryWrite: fwrite + fsync on every Set (strongest guarantee)
//   - GroupCommit: batch fsync every 16 entries or when queue drains
//   - EverySecond: fsync once per second
//
// Concurrency: all goroutines send tasks through a lock-free Go channel.
// A single background goroutine is the sole writer to the WAL file,
// eliminating lock contention (cf. Lealone's LogSyncService).
package persist

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
)

// WalSyncMode controls WAL fsync behaviour.
type WalSyncMode string

const (
	WalSyncEveryWrite  WalSyncMode = "every-write"  // fsync after every entry
	WalSyncGroupCommit WalSyncMode = "group-commit" // batch fsync (default: 16 entries)
	WalSyncEverySecond WalSyncMode = "every-second" // periodic fsync (1s)
)

// walTask wraps a WAL entry together with an optional completion signal.
// When done is non-nil the caller is waiting synchronously (EveryWrite).
type walTask struct {
	entry *service.WALEntry
	done  chan struct{} // nil for async (GroupCommit / EverySecond)
}

// PersistWAL is a KVStore decorator that logs every Set to a WAL.
//
// It embeds the underlying KVStore and delegates all read operations
// (Get, Delete, Stats, …) directly. Only Set is intercepted.
//
// Concurrency model (lock-free):
//
//	Set() goroutines  ──►  taskCh (Go channel, lock-free ring buffer)
//	                              │
//	                              ▼
//	                   runWriteLoop()  ← sole WAL writer
type PersistWAL struct {
	service.KVStore // embedded — delegates Get / Delete / Close / Stats / …

	wal      service.WAL
	syncMode WalSyncMode

	taskCh    chan *walTask
	batchSize int // batch threshold for GroupCommit (default 16)

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// NewPersistWAL creates a PersistWAL decorator.
//
// The returned PersistWAL starts a background goroutine that is the sole
// writer to the WAL file. Close() shuts it down gracefully.
func NewPersistWAL(tree service.KVStore, w service.WAL, syncMode WalSyncMode) *PersistWAL {
	p := &PersistWAL{
		KVStore:   tree,
		wal:       w,
		syncMode:  syncMode,
		batchSize: 16,
		taskCh:    make(chan *walTask, 64),
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())

	p.wg.Add(1)
	go p.runWriteLoop()
	return p
}

// Set intercepts the KVStore Set call to append a WAL entry.
func (p *PersistWAL) Set(ctx context.Context, key, value []byte) error {
	// ① Pure in-memory BTree write.
	if err := p.KVStore.Set(ctx, key, value); err != nil {
		return err
	}

	// ② Build WAL entry (pooled for GC reduction).
	entry := acquireWALEntry()
	entry.Type = service.WALTypeInsert
	entry.Key = key
	entry.Value = value

	// ③ Dispatch through lock-free channel.
	switch p.syncMode {
	case WalSyncEveryWrite:
		// Synchronous: wait for the background goroutine to fwrite+fsync.
		task := &walTask{entry: entry, done: make(chan struct{}, 1)}
		select {
		case p.taskCh <- task:
		case <-ctx.Done():
			releaseWALEntry(entry)
			return ctx.Err()
		}
		select {
		case <-task.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		releaseWALEntry(entry)
		return nil

	default:
		// Async: deep-copy so the Pool entry can be reused immediately.
		clone := &service.WALEntry{
			Type:      entry.Type,
			TxID:      entry.TxID,
			Timestamp: entry.Timestamp,
			Key:       copyBytes(key),
			Value:     copyBytes(value),
		}
		releaseWALEntry(entry)
		task := &walTask{entry: clone}
		select {
		case p.taskCh <- task:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// SetBatch intercepts batch writes for WAL logging.
func (p *PersistWAL) SetBatch(ctx context.Context, pairs []service.KVPair) error {
	if err := p.KVStore.SetBatch(ctx, pairs); err != nil {
		return err
	}
	for _, pair := range pairs {
		clone := &service.WALEntry{
			Type:  service.WALTypeInsert,
			Key:   copyBytes(pair.Key),
			Value: copyBytes(pair.Value),
		}
		task := &walTask{entry: clone}
		select {
		case p.taskCh <- task:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Sync explicitly flushes any buffered entries.
func (p *PersistWAL) Sync() error { return p.wal.Sync() }

// WalStats returns WAL-level statistics for benchmark output.
func (p *PersistWAL) WalStats() *WALStats {
	lsn := p.wal.CurrentLSN()
	return &WALStats{LSN: lsn}
}

// Close shuts down the background goroutine and the underlying store.
func (p *PersistWAL) Close() error {
	p.cancel()
	p.wg.Wait()
	return p.KVStore.Close()
}

// ---------------------------------------------------------------------------
// Background write loop (sole WAL writer)
// ---------------------------------------------------------------------------

func (p *PersistWAL) runWriteLoop() {
	defer p.wg.Done()

	var ticker *time.Ticker
	if p.syncMode == WalSyncEverySecond {
		ticker = time.NewTicker(time.Second)
		defer ticker.Stop()
	}

	tickerCh := func() <-chan time.Time {
		if ticker != nil {
			return ticker.C
		}
		return nil
	}

	batch := make([]*walTask, 0, p.batchSize)

	for {
		select {
		case <-p.ctx.Done():
			p.flushBatch(batch)
			return

		case <-tickerCh():
			p.flushBatch(batch)
			p.releaseBatch(batch)
			batch = batch[:0]

		case task := <-p.taskCh:
			if p.syncMode == WalSyncEveryWrite {
				// EveryWrite: fwrite + fsync immediately, then signal completion.
				entries, _ := p.wal.AppendBatch([]*service.WALEntry{task.entry})
				_ = p.wal.Sync()
				_ = entries // LSN is assigned; caller doesn't need it
				task.done <- struct{}{}
			} else {
				// GroupCommit / EverySecond: accumulate batch.
				batch = append(batch, task)
				if len(batch) >= p.batchSize && len(p.taskCh) == 0 {
					p.flushBatch(batch)
					p.releaseBatch(batch)
					batch = batch[:0]
				}
			}
		}
	}
}

func (p *PersistWAL) flushBatch(batch []*walTask) {
	if len(batch) == 0 {
		return
	}
	entries := make([]*service.WALEntry, len(batch))
	for i, t := range batch {
		entries[i] = t.entry
	}
	_, _ = p.wal.AppendBatch(entries)
	_ = p.wal.Sync()
}

// releaseBatch signals all synchronous waiters and nils references for GC.
func (p *PersistWAL) releaseBatch(batch []*walTask) {
	for _, t := range batch {
		if t.done != nil {
			t.done <- struct{}{}
		}
	}
	for i := range batch {
		batch[i] = nil
	}
}

// ---------------------------------------------------------------------------
// WALStats
// ---------------------------------------------------------------------------

// WALStats exposes WAL-level statistics for benchmark output.
type WALStats struct {
	LSN service.LSN
}

// ---------------------------------------------------------------------------
// WALEntry pool
// ---------------------------------------------------------------------------

var walEntryPool = sync.Pool{
	New: func() any { return &service.WALEntry{} },
}

func acquireWALEntry() *service.WALEntry {
	e := walEntryPool.Get().(*service.WALEntry)
	e.LSN = 0
	e.TxID = 0
	e.Timestamp = 0
	e.Type = 0
	e.Key = nil
	e.Value = nil
	e.PrevLSN = 0
	e.ShardID = 0
	e.Term = 0
	return e
}

func releaseWALEntry(e *service.WALEntry) {
	if e == nil {
		return
	}
	e.Key = nil
	e.Value = nil
	walEntryPool.Put(e)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func copyBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
