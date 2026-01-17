// Package cluster 节点 ID 管理测试
package cluster

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewLocalNodeInfo_AutoGenerate 测试自动生成节点 ID
func TestNewLocalNodeInfo_AutoGenerate(t *testing.T) {
	tempDir := t.TempDir()

	info, err := NewLocalNodeInfo(tempDir, "")
	require.NoError(t, err)

	nodeID := info.GetNodeID()
	assert.NotEmpty(t, nodeID)
	assert.Regexp(t, `^[0-9a-f-]{36}$`, nodeID) // UUID v7 格式
	assert.Equal(t, tempDir, info.GetDataDir())
}

// TestNewLocalNodeInfo_EnvVariable 测试环境变量配置
func TestNewLocalNodeInfo_EnvVariable(t *testing.T) {
	tempDir := t.TempDir()

	// 设置环境变量
	require.NoError(t, os.Setenv("NEXKV_NODE_ID", "test-node-123"))
	t.Cleanup(func() { require.NoError(t, os.Unsetenv("NEXKV_NODE_ID")) })

	info, err := NewLocalNodeInfo(tempDir, "")
	require.NoError(t, err)

	assert.Equal(t, "test-node-123", info.GetNodeID())
}

// TestNewLocalNodeInfo_ConfigParam 测试配置参数
func TestNewLocalNodeInfo_ConfigParam(t *testing.T) {
	tempDir := t.TempDir()

	info, err := NewLocalNodeInfo(tempDir, "config-node-456")
	require.NoError(t, err)

	assert.Equal(t, "config-node-456", info.GetNodeID())
}

// TestNewLocalNodeInfo_Persistence 测试持久化
func TestNewLocalNodeInfo_Persistence(t *testing.T) {
	tempDir := t.TempDir()

	// 第一次创建：自动生成
	info1, err := NewLocalNodeInfo(tempDir, "")
	require.NoError(t, err)
	nodeID1 := info1.GetNodeID()

	// 第二次创建：应该读取持久化的 ID
	info2, err := NewLocalNodeInfo(tempDir, "")
	require.NoError(t, err)
	nodeID2 := info2.GetNodeID()

	assert.Equal(t, nodeID1, nodeID2, "节点 ID 应该保持一致")
}

// TestLocalNodeInfo_Paths 测试路径获取
func TestLocalNodeInfo_Paths(t *testing.T) {
	tempDir := "/data/nexkv"

	info, err := NewLocalNodeInfo(tempDir, "node-1")
	require.NoError(t, err)

	assert.Equal(t, "node-1", info.GetNodeID())
	assert.Equal(t, "/data/nexkv", info.GetDataDir())
	assert.Equal(t, "/data/nexkv/node-1/wal", info.GetWalPath())
	assert.Equal(t, "/data/nexkv/node-1/snapshots", info.GetSnapshotPath())
	assert.Equal(t, "/data/nexkv/node-1/sst", info.GetSSTPath())
}
