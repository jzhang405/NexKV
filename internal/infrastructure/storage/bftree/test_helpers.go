// Package bftree 测试辅助函数
package bftree

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// setupTestTree 创建用于测试的 BfTree
func setupTestTree(t *testing.T) *BfTree {
	t.Helper()
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false
	tree, err := NewBfTree(config)
	require.NoError(t, err)
	return tree
}
