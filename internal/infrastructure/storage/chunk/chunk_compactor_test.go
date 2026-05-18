// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewChunkCompactor_DefaultFillRate(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	c := NewChunkCompactor(cm, 0)
	require.NotNil(t, c)
	assert.Equal(t, 30, c.minFillRate)
}

func TestNewChunkCompactor_NegativeFillRate(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	c := NewChunkCompactor(cm, -5)
	require.NotNil(t, c)
	assert.Equal(t, 30, c.minFillRate)
}

func TestNewChunkCompactor_MaxFillRate(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	c := NewChunkCompactor(cm, 80)
	require.NotNil(t, c)
	assert.Equal(t, 50, c.minFillRate)
}

func TestNewChunkCompactor_ValidFillRate(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	c := NewChunkCompactor(cm, 40)
	require.NotNil(t, c)
	assert.Equal(t, 40, c.minFillRate)
}

func TestChunkCompactor_NeedCompaction(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	c := NewChunkCompactor(cm, 30)
	assert.False(t, c.NeedCompaction())
}

func TestChunkCompactor_Compact(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	c := NewChunkCompactor(cm, 30)
	assert.NoError(t, c.Compact())
}
