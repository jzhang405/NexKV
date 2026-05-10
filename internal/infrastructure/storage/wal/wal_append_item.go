// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package wal

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ShardIDWAL is the fixed shard ID for WAL operations. All WALAppendItems
// route to Core (1 % coreCount), serializing LSN allocation + file write.
const ShardIDWAL = 1

// WALAppendItem encapsulates a batch of WAL entries to be appended and synced.
// Implements model.TaskRunner and model.TaskResult for TaskScheduler integration.
type WALAppendItem struct {
	dw        *DiskWAL
	entries   []*WALEntry
	errCh     chan error
	done      chan struct{}
	lsn       LSN
	priority  int
	sourceID  model.SourceID
	retries   int
	taskOrder int
}

// NewWALAppendItem creates a WAL append task.
func NewWALAppendItem(dw *DiskWAL, entries []*WALEntry) *WALAppendItem {
	return &WALAppendItem{
		dw:       dw,
		entries:  entries,
		errCh:    make(chan error, 1), // buffered to prevent blocking on caller timeout
		done:     make(chan struct{}),
		priority: int(model.TaskPriorityCritical),
		sourceID: model.MustParseSourceID("wal:append"),
	}
}

// --- ShardItem ---

func (item *WALAppendItem) ShardID() int     { return ShardIDWAL }
func (item *WALAppendItem) MaxRetries() int  { return 0 }
func (item *WALAppendItem) IncAttempts() int { item.retries++; return item.retries }
func (item *WALAppendItem) TaskOrder() int   { return item.taskOrder }

// --- TaskRunner ---

func (item *WALAppendItem) Run(ctx context.Context, trCtx model.TaskRunnerContext) {
	// Allocate LSN + write to OS buffer. fsync is handled by PostBatchHook.
	for _, entry := range item.entries {
		entry.LSN = LSN(item.dw.currentLSN.Add(1))
		item.lsn = entry.LSN
	}
	// Write to OS buffer (non-blocking). fsync happens in batch.
	if err := item.dw.writeEntries(item.entries); err != nil {
		item.errCh <- err
		close(item.done)
	}
}

func (item *WALAppendItem) Priority() model.TaskPriority { return model.TaskPriorityCritical }
func (item *WALAppendItem) SourceID() model.SourceID     { return item.sourceID }

// --- TaskResult ---

func (item *WALAppendItem) Done() <-chan struct{} { return item.done }
func (item *WALAppendItem) WaitAny(ctx context.Context) (any, error) {
	select {
	case <-item.done:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (item *WALAppendItem) Status() model.TaskStatus {
	select {
	case <-item.done:
		return model.TaskPassed
	default:
		return model.TaskQueued
	}
}
func (item *WALAppendItem) IsDone() bool {
	select {
	case <-item.done:
		return true
	default:
		return false
	}
}
func (item *WALAppendItem) GetError() error {
	select {
	case err := <-item.errCh:
		return err
	default:
		return nil
	}
}

// Cancel signals failure (scheduler drain / fsync error).
func (item *WALAppendItem) Cancel(err error) {
	select {
	case item.errCh <- err:
	default:
	}
	close(item.done)
}

// SignalSuccess signals the caller that the batch was synced.
func (item *WALAppendItem) SignalSuccess() {
	close(item.errCh) // no error
	close(item.done)
}
