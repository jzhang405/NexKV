// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorSentinels(t *testing.T) {
	// All 8 sentinel errors must be non-nil and mutually distinct.
	sentinels := []error{
		ErrCASConflict,
		ErrPageFreed,
		ErrKeyNotFound,
		ErrTreeClosed,
		ErrInvalidPage,
		ErrPageFull,
		ErrPageEmpty,
		ErrDuplicateKey,
	}

	for _, s := range sentinels {
		assert.NotNil(t, s, "sentinel error must not be nil")
	}

	// Verify pairwise inequality.
	for i := range len(sentinels) {
		for j := i + 1; j < len(sentinels); j++ {
			assert.NotEqual(t, sentinels[i], sentinels[j],
				"errors[%d] and errors[%d] must differ", i, j)
		}
	}

	// Verify errors.Is works correctly.
	for _, s := range sentinels {
		assert.True(t, errors.Is(s, s), "errors.Is(%v, %v) must be true", s, s)
	}
}

func TestConstantValues(t *testing.T) {
	assert.Equal(t, 56, HeaderSize, "HeaderSize must be 56 (Go struct alignment)")
	assert.Equal(t, 126, MaxInternalKeys, "MaxInternalKeys must be 126")
	assert.Equal(t, 16, IndexEntrySize, "IndexEntrySize must be 16")
	assert.Equal(t, 16, LeafEntrySize, "LeafEntrySize must be 16")
	assert.Equal(t, 4096, PageSize, "PageSize must be 4096")
	assert.Equal(t, 4096-56, UsableSize, "UsableSize must be PageSize - HeaderSize")
	assert.Equal(t, 0.5, MergeThreshold, "MergeThreshold must be 0.5")
	assert.Equal(t, 100, MaxCASRetries, "MaxCASRetries must be 100")
}

func TestNewBTreeWithMetricsAndTracer_NilTracer(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024)
	require.NoError(t, err)
	defer storage.Close()

	metrics := &BTreeMetrics{}
	tree, err := NewBTreeWithMetricsAndTracer(storage, metrics, nil)
	require.NoError(t, err)
	defer tree.Close()

	// Verify tree works with nil tracer (uses DefaultTracer)
	ctx := context.Background()
	err = tree.Set(ctx, []byte("key"), []byte("val"))
	require.NoError(t, err)
}

func TestIncrementMerge(t *testing.T) {
	m := NewBTreeMetrics()
	m.IncrementMerge()
	s := m.Snapshot()
	assert.Equal(t, int64(1), s.MergeCount)
}

func TestIsLeafPageError(t *testing.T) {
	assert.True(t, isLeafPageError(fmt.Errorf("page 5 is not a node page")))
	assert.False(t, isLeafPageError(fmt.Errorf("some other error")))
}

func TestNilTracer_NoPanic(t *testing.T) {
	// Verify nilTracer no-op methods don't panic (coverage for tracer.go).
	tr := &nilTracer{}
	tr.LogOp("test")
	tr.LogPageRefOp(nil, "test")
	tr.LogPageOp(1, "test")
	tr.LogPageData(1, "test", nil)
	result := tr.WithContext(context.TODO())
	assert.NotNil(t, result)
}

func TestTestTracer_NoPanic(t *testing.T) {
	// Verify TestTracer no-op stubs don't panic (coverage for tracer_release.go).
	tr := NewTestTracer(t, 0, 0)
	tr.LogOp("test")
	tr.LogPageRefOp(nil, "test")
	tr.LogPageOp(1, "test")
	tr.LogPageData(1, "test", nil)
	tr.WithContext(context.TODO())
	assert.Nil(t, tr.DumpLogs())
	assert.NoError(t, tr.DumpToFile(""))
	assert.Equal(t, 0, tr.GetRefCount(0))
	tr.Close()
}
