// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package wal

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// BTreeApplyItem is an async BTree Apply task for use with TaskScheduler.
// Implements model.TaskRunner and model.TaskResult.
type BTreeApplyItem struct {
	txID     uint64
	commitTS uint64
	buf      *mvcc.WriteBufferSnapshot
	keyHash  int
	done     chan struct{}
	err      error
	priority int
	sourceID model.SourceID
	retries  int
	taskOrder int
	applier  BTreeApplier
}

// BTreeApplier applies a write buffer snapshot to BTree.
type BTreeApplier func(txID, commitTS uint64, buf *mvcc.WriteBufferSnapshot) error

// NewBTreeApplyItem creates an async BTree apply task.
func NewBTreeApplyItem(txID, commitTS uint64, buf *mvcc.WriteBufferSnapshot, keyHash int, applier BTreeApplier) *BTreeApplyItem {
	if keyHash == 0 {
		keyHash = 1 // ShardID=0 triggers dynamic routing, must avoid
	}
	return &BTreeApplyItem{
		txID:      txID,
		commitTS:  commitTS,
		buf:       buf,
		keyHash:   keyHash,
		done:      make(chan struct{}),
		priority:  int(model.TaskPriorityHigh),
		sourceID:  model.MustParseSourceID("btree:apply:write"),
		taskOrder: 2, // ExecutionOrderBTreeSet
		applier:   applier,
	}
}

// --- ShardItem ---

func (item *BTreeApplyItem) ShardID() int    { return item.keyHash }
func (item *BTreeApplyItem) MaxRetries() int  { return 0 }
func (item *BTreeApplyItem) IncAttempts() int { item.retries++; return item.retries }
func (item *BTreeApplyItem) TaskOrder() int    { return item.taskOrder }

// --- TaskRunner ---

func (item *BTreeApplyItem) Run(ctx context.Context, trCtx model.TaskRunnerContext) {
	defer close(item.done)
	if err := item.applier(item.txID, item.commitTS, item.buf); err != nil {
		item.err = err
	}
}

func (item *BTreeApplyItem) Priority() model.TaskPriority { return model.TaskPriorityHigh }
func (item *BTreeApplyItem) SourceID() model.SourceID     { return item.sourceID }

// --- TaskResult ---

func (item *BTreeApplyItem) Done() <-chan struct{} { return item.done }
func (item *BTreeApplyItem) WaitAny(ctx context.Context) (any, error) {
	select {
	case <-item.done:
		return nil, item.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (item *BTreeApplyItem) Status() model.TaskStatus {
	select {
	case <-item.done:
		return model.TaskPassed
	default:
		return model.TaskQueued
	}
}
func (item *BTreeApplyItem) IsDone() bool {
	select {
	case <-item.done:
		return true
	default:
		return false
	}
}
func (item *BTreeApplyItem) GetError() error { return item.err }

func (item *BTreeApplyItem) Cancel(err error) {
	item.err = err
	close(item.done)
}
