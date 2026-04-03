// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"testing"

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

func TestBTreeMetrics_String(t *testing.T) {
	snapshot := MetricsSnapshot{
		ReadCount:     100,
		WriteCount:    50,
		DeleteCount:   25,
		CASRetryCount: 10,
		SplitCount:    5,
		MergeCount:    2,
	}

	expected := "Read=100 Write=50 Delete=25 CASRetries=10 Splits=5 Merges=2"
	assert.Equal(t, expected, snapshot.String())
}
