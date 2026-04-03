// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"
	"sync/atomic"
)

// BTreeMetrics holds performance counters for monitoring BTree operations.
// All counters are atomic and safe for concurrent access.
type BTreeMetrics struct {
	// ReadCount tracks the number of successful read operations (Get).
	ReadCount atomic.Int64

	// WriteCount tracks the number of successful write operations (Set/Update).
	WriteCount atomic.Int64

	// DeleteCount tracks the number of successful delete operations.
	DeleteCount atomic.Int64

	// CASRetryCount tracks the number of CAS retry attempts due to conflicts.
	CASRetryCount atomic.Int64

	// SplitCount tracks the number of page splits (Phase 6+).
	SplitCount atomic.Int64

	// MergeCount tracks the number of page merges (Phase 6.5+).
	MergeCount atomic.Int64
}

// NewBTreeMetrics creates a new metrics instance with all counters initialized to 0.
func NewBTreeMetrics() *BTreeMetrics {
	return &BTreeMetrics{}
}

// IncrementRead atomically increments the read counter.
func (m *BTreeMetrics) IncrementRead() {
	m.ReadCount.Add(1)
}

// IncrementWrite atomically increments the write counter.
func (m *BTreeMetrics) IncrementWrite() {
	m.WriteCount.Add(1)
}

// IncrementDelete atomically increments the delete counter.
func (m *BTreeMetrics) IncrementDelete() {
	m.DeleteCount.Add(1)
}

// IncrementCASRetry atomically increments the CAS retry counter.
func (m *BTreeMetrics) IncrementCASRetry() {
	m.CASRetryCount.Add(1)
}

// IncrementSplit atomically increments the split counter.
func (m *BTreeMetrics) IncrementSplit() {
	m.SplitCount.Add(1)
}

// IncrementMerge atomically increments the merge counter.
func (m *BTreeMetrics) IncrementMerge() {
	m.MergeCount.Add(1)
}

// Snapshot returns a point-in-time snapshot of all metrics.
// The returned values are consistent but not transactional.
func (m *BTreeMetrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		ReadCount:     m.ReadCount.Load(),
		WriteCount:    m.WriteCount.Load(),
		DeleteCount:   m.DeleteCount.Load(),
		CASRetryCount: m.CASRetryCount.Load(),
		SplitCount:    m.SplitCount.Load(),
		MergeCount:    m.MergeCount.Load(),
	}
}

// Reset resets all counters to 0.
// Useful for testing and benchmarking.
func (m *BTreeMetrics) Reset() {
	m.ReadCount.Store(0)
	m.WriteCount.Store(0)
	m.DeleteCount.Store(0)
	m.CASRetryCount.Store(0)
	m.SplitCount.Store(0)
	m.MergeCount.Store(0)
}

// MetricsSnapshot is an immutable snapshot of BTreeMetrics.
type MetricsSnapshot struct {
	ReadCount     int64
	WriteCount    int64
	DeleteCount   int64
	CASRetryCount int64
	SplitCount    int64
	MergeCount    int64
}

// String returns a formatted string representation of the metrics.
func (s MetricsSnapshot) String() string {
	return fmt.Sprintf(
		"Read=%d Write=%d Delete=%d CASRetries=%d Splits=%d Merges=%d",
		s.ReadCount, s.WriteCount, s.DeleteCount,
		s.CASRetryCount, s.SplitCount, s.MergeCount,
	)
}

// TotalOps returns the total number of operations (read + write + delete).
func (s MetricsSnapshot) TotalOps() int64 {
	return s.ReadCount + s.WriteCount + s.DeleteCount
}

// ConflictRate returns the ratio of CAS retries to write operations.
// Returns 0 if there are no writes.
func (s MetricsSnapshot) ConflictRate() float64 {
	if s.WriteCount == 0 {
		return 0
	}
	return float64(s.CASRetryCount) / float64(s.WriteCount)
}
