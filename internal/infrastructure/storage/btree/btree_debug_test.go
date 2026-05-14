// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintTree_Format(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	for i := 0; i < 50; i++ {
		key := []byte(fmt.Sprintf("pt-%04d", i))
		val := []byte(fmt.Sprintf("val-%04d", i))
		require.NoError(t, tree.Set(ctx, key, val))
	}

	out := tree.PrintTree()
	assert.True(t, strings.Contains(out, "Leaf") || strings.Contains(out, "Node"),
		"output should contain node type: %s", out)
	assert.True(t, strings.Contains(out, "keys"), "output should mention keys")
	t.Logf("Tree:\n%s", out)
}

func TestAssertInvariants_ValidTree(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("iv-%04d", i))
		val := []byte(fmt.Sprintf("val-%04d", i))
		require.NoError(t, tree.Set(ctx, key, val))
	}

	err := tree.AssertInvariants()
	assert.NoError(t, err, "valid tree should pass invariant checks")
}

func TestAssertInvariants_AfterDeletes(t *testing.T) {
	tree, _ := newTestBTree(t)
	ctx := context.Background()

	for i := 0; i < 60; i++ {
		key := []byte(fmt.Sprintf("dv-%04d", i))
		val := []byte(fmt.Sprintf("val-%04d", i))
		require.NoError(t, tree.Set(ctx, key, val))
	}
	for i := 0; i < 30; i++ {
		key := []byte(fmt.Sprintf("dv-%04d", i))
		require.NoError(t, tree.Delete(ctx, key))
	}

	err := tree.AssertInvariants()
	assert.NoError(t, err, "tree after deletes should pass invariant checks")
}

func TestMetricsSnapshot(t *testing.T) {
	storage, _ := NewOffheapBTreeStorage(4 * 1024 * 1024 * 1024)
	defer storage.Close()
	tree, err := NewBTree(storage, WithMetrics(NewBTreeMetrics()))
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()
	tree.Set(ctx, []byte("m-key"), []byte("m-val"))
	tree.Get(ctx, []byte("m-key"))

	snap := tree.GetMetrics()
	require.NotNil(t, snap)
	assert.True(t, snap.WriteCount >= 1)
	assert.True(t, snap.ReadCount >= 1)
	assert.NotEmpty(t, snap.String())
	assert.True(t, snap.TotalOps() >= 2)
}

func TestPrintTree_EmptyTree(t *testing.T) {
	tree, _ := newTestBTree(t)
	out := tree.PrintTree()
	assert.Contains(t, out, "Leaf", "empty tree has root leaf")
}
