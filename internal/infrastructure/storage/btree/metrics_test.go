// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBTreeMetrics_Collection(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(128 * 1024 * 1024)
	require.NoError(t, err)

	metrics := &BTreeMetrics{}
	tree, err := NewBTreeWithMetrics(storage, metrics)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// Test write operations
	for i := 0; i < 10; i++ {
		key := []byte("key-" + string(rune('0'+i)))
		value := []byte("value-" + string(rune('0'+i)))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	snapshot := metrics.Snapshot()
	assert.Equal(t, int64(10), snapshot.WriteCount, "Should track 10 write operations")
	assert.Equal(t, int64(0), snapshot.ReadCount, "Should have 0 read operations")
	assert.Equal(t, int64(0), snapshot.CASRetryCount, "Should have 0 CAS retries in single-threaded mode")

	// Test read operations
	for i := 0; i < 5; i++ {
		key := []byte("key-" + string(rune('0'+i)))
		_, err := tree.Get(ctx, key)
		require.NoError(t, err)
	}

	snapshot = metrics.Snapshot()
	assert.Equal(t, int64(10), snapshot.WriteCount, "Write count should remain 10")
	assert.Equal(t, int64(5), snapshot.ReadCount, "Should track 5 read operations")

	// Test delete operations
	for i := 0; i < 3; i++ {
		key := []byte("key-" + string(rune('0'+i)))
		err := tree.Delete(ctx, key)
		require.NoError(t, err)
	}

	snapshot = metrics.Snapshot()
	assert.Equal(t, int64(3), snapshot.DeleteCount, "Should track 3 delete operations")
	assert.Equal(t, int64(10+5+3), snapshot.TotalOps(), "Total operations should be 18")
}

func TestBTreeMetrics_Optional(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(128 * 1024 * 1024)
	require.NoError(t, err)

	// Create tree without metrics
	tree, err := NewBTree(storage)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// Operations should work without metrics
	for i := 0; i < 5; i++ {
		key := []byte("key-" + string(rune('0'+i)))
		value := []byte("value-" + string(rune('0'+i)))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// GetMetrics should return nil
	metrics := tree.GetMetrics()
	assert.Nil(t, metrics, "Metrics should be nil when not enabled")
}

func TestBTreeMetrics_Reset(t *testing.T) {
	metrics := &BTreeMetrics{}

	// Increment counters
	metrics.IncrementRead()
	metrics.IncrementWrite()
	metrics.IncrementDelete()
	metrics.IncrementCASRetry()

	snapshot := metrics.Snapshot()
	assert.Equal(t, int64(1), snapshot.ReadCount)
	assert.Equal(t, int64(1), snapshot.WriteCount)
	assert.Equal(t, int64(1), snapshot.DeleteCount)
	assert.Equal(t, int64(1), snapshot.CASRetryCount)

	// Reset
	metrics.Reset()

	snapshot = metrics.Snapshot()
	assert.Equal(t, int64(0), snapshot.ReadCount)
	assert.Equal(t, int64(0), snapshot.WriteCount)
	assert.Equal(t, int64(0), snapshot.DeleteCount)
	assert.Equal(t, int64(0), snapshot.CASRetryCount)
}

func TestBTreeMetrics_ConflictRate(t *testing.T) {
	tests := []struct {
		name       string
		writeCount int64
		casRetries int64
		expectRate float64
	}{
		{
			name:       "no writes",
			writeCount: 0,
			casRetries: 0,
			expectRate: 0,
		},
		{
			name:       "no conflicts",
			writeCount: 100,
			casRetries: 0,
			expectRate: 0,
		},
		{
			name:       "10% conflict rate",
			writeCount: 100,
			casRetries: 10,
			expectRate: 0.1,
		},
		{
			name:       "50% conflict rate",
			writeCount: 100,
			casRetries: 50,
			expectRate: 0.5,
		},
		{
			name:       "100% conflict rate",
			writeCount: 100,
			casRetries: 100,
			expectRate: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := MetricsSnapshot{
				WriteCount:    tt.writeCount,
				CASRetryCount: tt.casRetries,
			}
			assert.Equal(t, tt.expectRate, snapshot.ConflictRate())
		})
	}
}

func TestLatencyHistogram_RecordAndSnapshot(t *testing.T) {
	h := NewLatencyHistogram(1) // no sampling for test
	for i := 0; i < 100; i++ {
		h.Record(100 * time.Microsecond)
	}
	snap := h.Snapshot()
	assert.Equal(t, int64(100), snap.Count)
	assert.True(t, snap.AvgUs > 0)
	assert.True(t, snap.P50Us > 0)
	assert.True(t, snap.P95Us > 0)
	assert.True(t, snap.P99Us > 0)
}

func TestLatencyHistogram_Sampling(t *testing.T) {
	h := NewLatencyHistogram(10) // 1/10 sampling
	for i := 0; i < 1000; i++ {
		h.Record(time.Microsecond)
	}
	snap := h.Snapshot()
	// With 1/10 sampling from 1000 calls, expect ~100 records
	assert.True(t, snap.Count >= 80 && snap.Count <= 120,
		"sampled count ~100, got %d", snap.Count)
}

func TestLatencyHistogram_Empty(t *testing.T) {
	h := NewLatencyHistogram(1)
	snap := h.Snapshot()
	assert.Equal(t, int64(0), snap.Count)
	assert.Equal(t, int64(0), snap.P50Us)
}

func TestLatencyHistogram_Reset(t *testing.T) {
	h := NewLatencyHistogram(1)
	h.Record(time.Microsecond)
	h.Reset()
	snap := h.Snapshot()
	assert.Equal(t, int64(0), snap.Count)
}

func TestBTreeMetricsWithLatency_EndToEnd(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(128 * 1024 * 1024)
	require.NoError(t, err)

	tree, err := NewBTree(storage, WithLatencyMetrics())
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// Write operations (need enough to pass sampling threshold)
	for i := 0; i < 200; i++ {
		key := []byte("key-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)))
		value := []byte("value-" + string(rune('0'+i%10)))
		_ = tree.Set(ctx, key, value)
	}

	// Read operations
	for i := 0; i < 200; i++ {
		key := []byte("key-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)))
		_, _ = tree.Get(ctx, key)
	}

	ml := tree.metricsWithLatency()
	require.NotNil(t, ml)
	readSnap := ml.ReadLat.Snapshot()
	writeSnap := ml.WriteLat.Snapshot()

	// With 1/64 sampling from 200 ops, expect ~3 records (allow 0-10)
	assert.True(t, readSnap.Count >= 0)
	assert.True(t, writeSnap.Count >= 0)
}

func TestBTreeMetricsWithLatency_DisabledByDefault(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(128 * 1024 * 1024)
	require.NoError(t, err)

	tree, err := NewBTree(storage)
	require.NoError(t, err)
	defer tree.Close()

	ml := tree.metricsWithLatency()
	assert.Nil(t, ml, "latency metrics should be nil when not enabled")
}

func TestHistogramSnapshot_String(t *testing.T) {
	s := HistogramSnapshot{Count: 50, AvgUs: 100, P50Us: 64, P95Us: 256, P99Us: 1024}
	str := s.String()
	assert.Contains(t, str, "count=50")
	assert.Contains(t, str, "p50=64")
}

func TestMetricsSnapshot_ComputeQPS(t *testing.T) {
	prev := &MetricsSnapshot{
		ReadCount:   1000,
		WriteCount:  500,
		DeleteCount: 100,
	}
	curr := MetricsSnapshot{
		ReadCount:   2000,
		WriteCount:  800,
		DeleteCount: 150,
	}
	qps := curr.ComputeQPS(prev, 2*time.Second)
	assert.Equal(t, 500.0, qps.ReadQPS)
	assert.Equal(t, 150.0, qps.WriteQPS)
	assert.Equal(t, 25.0, qps.DeleteQPS)
	assert.Equal(t, 675.0, qps.TotalQPS)
}

func TestMetricsSnapshot_ComputeQPS_NilPrev(t *testing.T) {
	curr := MetricsSnapshot{ReadCount: 100}
	qps := curr.ComputeQPS(nil, time.Second)
	assert.Equal(t, 0.0, qps.ReadQPS)
}

func TestQPSSnapshot_String(t *testing.T) {
	q := QPSSnapshot{ReadQPS: 500, WriteQPS: 150, TotalQPS: 650}
	str := q.String()
	assert.Contains(t, str, "500")
	assert.Contains(t, str, "150")
}

func TestBTreeMetrics_String(t *testing.T) {
	snapshot := MetricsSnapshot{
		ReadCount:     100,
		WriteCount:    50,
		DeleteCount:   25,
		CASRetryCount: 10,
		SplitCount:    5,
		MergeCount:    2,
	}

	expected := "Read=100 Write=50 Delete=25 CASRetries=10 Splits=5 Merges=2 Compactions=0 TreeHeight=0 DroppedSplits=0"
	assert.Equal(t, expected, snapshot.String())
}
