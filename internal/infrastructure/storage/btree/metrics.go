// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"
	"sync/atomic"
	"time"
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

	// TreeHeightCount tracks the number of times the tree height has increased.
	TreeHeightCount atomic.Int64

	// CompactionCount tracks the number of compaction cycles (Phase 6.5).
	CompactionCount atomic.Int64

	// DroppedSplitCount tracks how many cascade splits were dropped (split queue full).
	DroppedSplitCount atomic.Int64
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

// IncrementTreeHeight atomically increments the tree height counter.
func (m *BTreeMetrics) IncrementTreeHeight() {
	m.TreeHeightCount.Add(1)
}

// IncrementCompact atomically increments the compaction counter.
func (m *BTreeMetrics) IncrementCompact() {
	m.CompactionCount.Add(1)
}

// IncrementDroppedSplit atomically increments the dropped split counter.
func (m *BTreeMetrics) IncrementDroppedSplit() {
	if m != nil {
		m.DroppedSplitCount.Add(1)
	}
}

// Snapshot returns a point-in-time snapshot of all metrics.
// The returned values are consistent but not transactional.
func (m *BTreeMetrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		ReadCount:       m.ReadCount.Load(),
		WriteCount:      m.WriteCount.Load(),
		DeleteCount:     m.DeleteCount.Load(),
		CASRetryCount:   m.CASRetryCount.Load(),
		SplitCount:      m.SplitCount.Load(),
		MergeCount:      m.MergeCount.Load(),
		TreeHeightCount: m.TreeHeightCount.Load(),
		CompactionCount:  m.CompactionCount.Load(),
		DroppedSplitCount: m.DroppedSplitCount.Load(),
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
	m.TreeHeightCount.Store(0)
	m.CompactionCount.Store(0)
	m.DroppedSplitCount.Store(0)
}

// MetricsSnapshot is an immutable snapshot of BTreeMetrics.
type MetricsSnapshot struct {
	ReadCount       int64
	WriteCount      int64
	DeleteCount     int64
	CASRetryCount   int64
	SplitCount      int64
	MergeCount      int64
	CompactionCount int64
	TreeHeightCount   int64
	DroppedSplitCount int64
}

// String returns a formatted string representation of the metrics.
func (s MetricsSnapshot) String() string {
	return fmt.Sprintf(
		"Read=%d Write=%d Delete=%d CASRetries=%d Splits=%d Merges=%d Compactions=%d TreeHeight=%d DroppedSplits=%d",
		s.ReadCount, s.WriteCount, s.DeleteCount,
		s.CASRetryCount, s.SplitCount, s.MergeCount, s.CompactionCount, s.TreeHeightCount, s.DroppedSplitCount,
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

// QPSSnapshot holds computed QPS values derived from two MetricsSnapshots.
type QPSSnapshot struct {
	ReadQPS   float64
	WriteQPS  float64
	DeleteQPS float64
	TotalQPS  float64
}

// ComputeQPS calculates per-second rates between two snapshots over the given duration.
// Returns zero values if duration is <= 0 or prev is nil.
func (s MetricsSnapshot) ComputeQPS(prev *MetricsSnapshot, d time.Duration) QPSSnapshot {
	if prev == nil || d <= 0 {
		return QPSSnapshot{}
	}
	sec := d.Seconds()
	return QPSSnapshot{
		ReadQPS:   float64(s.ReadCount-prev.ReadCount) / sec,
		WriteQPS:  float64(s.WriteCount-prev.WriteCount) / sec,
		DeleteQPS: float64(s.DeleteCount-prev.DeleteCount) / sec,
		TotalQPS:  float64(s.TotalOps()-prev.TotalOps()) / sec,
	}
}

// String returns a formatted string of the QPS snapshot.
func (q QPSSnapshot) String() string {
	return fmt.Sprintf(
		"Read=%.0f/s Write=%.0f/s Delete=%.0f/s Total=%.0f/s",
		q.ReadQPS, q.WriteQPS, q.DeleteQPS, q.TotalQPS,
	)
}

// LatencyHistogram tracks operation latency using power-of-2 buckets.
// Bucket boundaries: 1us, 2us, 4us, 8us, ..., ~1s (64 buckets total).
// Uses 1/64 sampling to avoid hot-path overhead.
type LatencyHistogram struct {
	buckets    [64]atomic.Int64
	count      atomic.Int64
	sumUs      atomic.Int64
	sampleCtr  atomic.Int64
	sampleRate int64 // 1/sampleRate calls are recorded
}

// NewLatencyHistogram creates a histogram with the given sampling rate.
// sampleRate=64 means every 64th call is recorded; use 1 for no sampling.
func NewLatencyHistogram(sampleRate int64) *LatencyHistogram {
	if sampleRate < 1 {
		sampleRate = 1
	}
	return &LatencyHistogram{sampleRate: sampleRate}
}

// Record records a latency observation. Uses sampling to avoid hot-path overhead.
func (h *LatencyHistogram) Record(d time.Duration) {
	ctr := h.sampleCtr.Add(1)
	if ctr%h.sampleRate != 0 {
		return
	}
	us := d.Microseconds()
	h.sumUs.Add(us)
	h.count.Add(1)
	// Find bucket: 64 buckets, boundaries at 1<<bucket us
	// Bucket 0: 0-1us, Bucket 1: 1-2us, ... Bucket 63: >= 2^63 us (~292 years)
	bucket := 0
	v := us
	for v > 0 && bucket < 63 {
		v >>= 1
		bucket++
	}
	h.buckets[bucket].Add(1)
}

// Snapshot returns a point-in-time latency snapshot.
func (h *LatencyHistogram) Snapshot() HistogramSnapshot {
	total := h.count.Load()
	if total == 0 {
		return HistogramSnapshot{}
	}
	// Compute percentiles from bucket distribution
	var buckets [64]int64
	var cumSum int64
	for i := range buckets {
		buckets[i] = h.buckets[i].Load()
	}
	var p50, p95, p99 int64
	p50Idx := total / 2
	p95Idx := total * 95 / 100
	p99Idx := total * 99 / 100
	for i := range buckets {
		cumSum += buckets[i]
		if p50 == 0 && cumSum >= p50Idx {
			p50 = 1 << i // upper bound of bucket i
		}
		if p95 == 0 && cumSum >= p95Idx {
			p95 = 1 << i
		}
		if p99 == 0 && cumSum >= p99Idx {
			p99 = 1 << i
		}
	}
	avgUs := int64(0)
	if total > 0 {
		avgUs = h.sumUs.Load() / total
	}
	return HistogramSnapshot{
		Count: total,
		AvgUs: avgUs,
		P50Us: p50,
		P95Us: p95,
		P99Us: p99,
	}
}

// Reset clears all histogram counters.
func (h *LatencyHistogram) Reset() {
	for i := range h.buckets {
		h.buckets[i].Store(0)
	}
	h.count.Store(0)
	h.sumUs.Store(0)
	h.sampleCtr.Store(0)
}

// HistogramSnapshot is an immutable snapshot of latency distribution.
type HistogramSnapshot struct {
	Count int64
	AvgUs int64
	P50Us int64
	P95Us int64
	P99Us int64
}

// String returns a formatted string of the latency snapshot.
func (s HistogramSnapshot) String() string {
	return fmt.Sprintf(
		"count=%d avg=%dus p50=%dus p95=%dus p99=%dus",
		s.Count, s.AvgUs, s.P50Us, s.P95Us, s.P99Us,
	)
}

// BTreeMetricsWithLatency extends BTreeMetrics with latency histograms.
type BTreeMetricsWithLatency struct {
	Counters *BTreeMetrics
	ReadLat  *LatencyHistogram
	WriteLat *LatencyHistogram
	SplitLat *LatencyHistogram
	MergeLat *LatencyHistogram
}

// NewBTreeMetricsWithLatency creates a BTreeMetricsWithLatency with default 1/64 sampling.
func NewBTreeMetricsWithLatency() *BTreeMetricsWithLatency {
	return &BTreeMetricsWithLatency{
		Counters: NewBTreeMetrics(),
		ReadLat:  NewLatencyHistogram(64),
		WriteLat: NewLatencyHistogram(64),
		SplitLat: NewLatencyHistogram(64),
		MergeLat: NewLatencyHistogram(64),
	}
}

// Reset resets all counters and histograms.
func (ml *BTreeMetricsWithLatency) Reset() {
	ml.Counters.Reset()
	ml.ReadLat.Reset()
	ml.WriteLat.Reset()
	ml.SplitLat.Reset()
	ml.MergeLat.Reset()
}
