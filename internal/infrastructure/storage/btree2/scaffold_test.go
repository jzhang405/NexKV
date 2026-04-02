// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree2

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
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
	for i := 0; i < len(sentinels); i++ {
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
