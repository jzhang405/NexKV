// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDataMigrator(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	assert.NotNil(t, migrator)
	assert.Equal(t, tempDir, migrator.oldDBPath)
	assert.Equal(t, cm, migrator.newMgr)
	assert.Equal(t, gc, migrator.gc)
	assert.Equal(t, ccow, migrator.ccow)
}

func TestMigrateStatus(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	status := migrator.GetStatus()
	assert.Equal(t, PhaseInit, status.Phase)
	assert.False(t, status.Completed)
	assert.Zero(t, status.TotalNodes)
}

func TestMigrateStats(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	stats := migrator.GetStats()
	assert.Zero(t, stats.TotalNodes)
	assert.Zero(t, stats.MigratedNodes)
	assert.Zero(t, stats.FailedNodes)
}

func TestMigrateLeafNode(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	// 创建旧的叶子节点
	oldNode := &Node{
		PageID: 1,
		IsLeaf: true,
		Keys: [][]byte{
			[]byte("key1"),
			[]byte("key2"),
			[]byte("key3"),
		},
		Values: [][]byte{
			[]byte("value1"),
			[]byte("value2"),
			[]byte("value3"),
		},
	}

	// 迁移叶子节点
	err = migrator.migrateLeafNode(oldNode)
	require.NoError(t, err)

	// 验证统计信息
	stats := migrator.GetStats()
	assert.Greater(t, stats.BytesMigrated, int64(0))
}

func TestMigrateInternalNode(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	// 创建旧的内部节点
	oldNode := &Node{
		PageID: 2,
		IsLeaf: false,
		Keys: [][]byte{
			[]byte("key1"),
			[]byte("key2"),
		},
		ChildIDs: []model.PageID{3, 4, 5},
	}

	// 迁移内部节点
	err = migrator.migrateInternalNode(oldNode)
	require.NoError(t, err)

	// 验证统计信息
	stats := migrator.GetStats()
	assert.Greater(t, stats.BytesMigrated, int64(0))
}

func TestDeserializeNode_LeafNode(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	// 手动构造叶子节点数据（使用 Big-Endian）
	// PageID (8 bytes) + IsLeaf (1 byte) + NumKeys (4 bytes) + Key/Value pairs
	data := make([]byte, 0, 100)

	// PageID = 1
	data = append(data, 0, 0, 0, 0, 0, 0, 0, 1)

	// IsLeaf = 1
	data = append(data, 1)

	// NumKeys = 2
	data = append(data, 0, 0, 0, 2)

	// Key1: "key1" (len=4, data)
	data = append(data, 0, 4)           // Key length
	data = append(data, []byte("key1")...) // Key data

	// Value1: "value1" (len=6, data)
	data = append(data, 0, 6)              // Value length
	data = append(data, []byte("value1")...) // Value data

	// Key2: "key2" (len=4, data)
	data = append(data, 0, 4)           // Key length
	data = append(data, []byte("key2")...) // Key data

	// Value2: "value2" (len=6, data)
	data = append(data, 0, 6)              // Value length
	data = append(data, []byte("value2")...) // Value data

	// 反序列化
	node, err := migrator.deserializeNode(data)
	require.NoError(t, err)
	require.NotNil(t, node)

	assert.Equal(t, model.PageID(1), node.PageID)
	assert.True(t, node.IsLeaf)
	assert.Equal(t, 2, len(node.Keys))
	assert.Equal(t, []byte("key1"), node.Keys[0])
	assert.Equal(t, []byte("key2"), node.Keys[1])
	assert.Equal(t, []byte("value1"), node.Values[0])
	assert.Equal(t, []byte("value2"), node.Values[1])
}

func TestDeserializeNode_InternalNode(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	// 手动构造内部节点数据
	data := make([]byte, 0, 100)

	// PageID = 10
	data = append(data, 0, 0, 0, 0, 0, 0, 0, 10)

	// IsLeaf = 0
	data = append(data, 0)

	// NumKeys = 2
	data = append(data, 0, 0, 0, 2)

	// Key1: "key1"
	data = append(data, 0, 4)
	data = append(data, []byte("key1")...)

	// Key2: "key2"
	data = append(data, 0, 4)
	data = append(data, []byte("key2")...)

	// ChildIDs: [3, 4, 5]
	data = append(data, 0, 0, 0, 0, 0, 0, 0, 3)
	data = append(data, 0, 0, 0, 0, 0, 0, 0, 4)
	data = append(data, 0, 0, 0, 0, 0, 0, 0, 5)

	// 反序列化
	node, err := migrator.deserializeNode(data)
	require.NoError(t, err)
	require.NotNil(t, node)

	assert.Equal(t, model.PageID(10), node.PageID)
	assert.False(t, node.IsLeaf)
	assert.Equal(t, 2, len(node.Keys))
	assert.Equal(t, []byte("key1"), node.Keys[0])
	assert.Equal(t, []byte("key2"), node.Keys[1])
	assert.Equal(t, 3, len(node.ChildIDs))
	assert.Equal(t, model.PageID(3), node.ChildIDs[0])
	assert.Equal(t, model.PageID(4), node.ChildIDs[1])
	assert.Equal(t, model.PageID(5), node.ChildIDs[2])
}

func TestScanOldDatabase_Empty(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	// 扫描空目录
	nodes, err := migrator.scanOldDatabase()
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

func TestScanOldDatabase_WithDataFiles(t *testing.T) {
	tempDir := t.TempDir()

	// 创建测试数据文件
	dataFile := filepath.Join(tempDir, "test.dat")
	data := []byte{1, 2, 3, 4, 5}
	err := os.WriteFile(dataFile, data, 0644)
	require.NoError(t, err)

	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	// 扫描目录（会找到 test.dat 但无法解析，返回空列表）
	nodes, err := migrator.scanOldDatabase()
	require.NoError(t, err)
	// 由于数据格式不对，应该返回空列表
	assert.NotNil(t, nodes)
	assert.Empty(t, nodes, "Invalid data files should return empty node list")
}

func TestMigrate_EmptyDatabase(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	ctx := context.Background()

	// 迁移空数据库
	err = migrator.Migrate(ctx, nil)
	require.NoError(t, err)

	// 验证状态
	status := migrator.GetStatus()
	assert.Equal(t, PhaseCompleted, status.Phase)
	assert.True(t, status.Completed)
}

func TestMigrate_WithProgressCallback(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	ctx := context.Background()

	progressCalls := 0
	progressCb := func(migrated, total int) {
		progressCalls++
	}

	// 迁移空数据库
	err = migrator.Migrate(ctx, progressCb)
	require.NoError(t, err)

	// 空数据库不应该调用进度回调
	assert.Equal(t, 0, progressCalls)
}

func TestRollback(t *testing.T) {
	tempDir := t.TempDir()
	cm, err := NewChunkManager(tempDir)
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	ccow := NewCCOWManager(gc)

	migrator := NewDataMigrator(tempDir, cm, gc, ccow)

	ctx := context.Background()

	// 执行迁移
	err = migrator.Migrate(ctx, nil)
	require.NoError(t, err)

	// 回滚迁移
	err = migrator.Rollback(ctx)
	require.NoError(t, err)

	// 验证状态已重置
	status := migrator.GetStatus()
	assert.Equal(t, PhaseInit, status.Phase)
	assert.False(t, status.Completed)
}

func TestMigrate_Comprehensive(t *testing.T) {
	t.Skip("Skipping comprehensive migration test in Phase 1")

	// TODO: 在 Phase 2 实现完整的端到端迁移测试
	// 1. 创建旧的 Node-based 数据库
	// 2. 写入大量测试数据
	// 3. 执行迁移
	// 4. 验证数据完整性
	// 5. 性能测试
}
