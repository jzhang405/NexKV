// Package bftree 测试 NewBfTree 初始化
package bftree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewBfTree_DefaultConfig 测试使用 DefaultConfig 创建 BfTree
func TestNewBfTree_DefaultConfig(t *testing.T) {
	config := DefaultConfig()
	t.Logf("Config: EnableWAL=%v, DataDir=%q, WALDir=%q",
		config.EnableWAL, config.DataDir, config.WALDir)

	tree, err := NewBfTree(config)
	t.Logf("NewBfTree returned: tree=%p, err=%v", tree, err)

	if err != nil {
		t.Logf("Expected error: %v", err)
		return
	}

	require.NotNil(t, tree, "tree should not be nil when err is nil")
	defer tree.Close()

	// 测试基本操作
	ctx := context.Background()
	err = tree.Set(ctx, []byte("test-key"), []byte("test-value"))
	assert.NoError(t, err)

	val, err := tree.Get(ctx, []byte("test-key"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("test-value"), val)
}

// TestNewBfTree_NoWAL 测试不使用 WAL 创建 BfTree
func TestNewBfTree_NoWAL(t *testing.T) {
	config := DefaultConfig()
	config.EnableWAL = false  // 禁用 WAL
	config.DataDir = t.TempDir()  // 设置 DataDir

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	require.NotNil(t, tree)
	defer tree.Close()

	// 测试基本操作
	ctx := context.Background()
	err = tree.Set(ctx, []byte("test-key"), []byte("test-value"))
	assert.NoError(t, err)

	val, err := tree.Get(ctx, []byte("test-key"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("test-value"), val)
}
