// Package cluster 提供节点 ID 管理
//
// 支持：
//   - 环境变量配置
//   - 自动生成并持久化
//   - 获取节点数据目录路径
package cluster

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/jzhang405/NexKV/internal/metadata/uuid"
)

// NodeIDProvider 节点 ID 提供者接口
type NodeIDProvider interface {
	// GetNodeID 获取当前节点 ID
	GetNodeID() string
	// GetDataDir 获取节点数据根目录
	GetDataDir() string
	// GetWalPath 获取 WAL 路径
	GetWalPath() string
	// GetSnapshotPath 获取快照路径
	GetSnapshotPath() string
	// GetSSTPath 获取 SSTable 路径
	GetSSTPath() string
}

// LocalNodeInfo 本地节点信息
type LocalNodeInfo struct {
	mu     sync.RWMutex
	nodeID string
	// 数据根目录（例如：/data/nexkv）
	dataDir string
}

// NewLocalNodeInfo 创建本地节点信息
//
// 优先级：
// 1. 环境变量 NEXKV_NODE_ID
// 2. configNodeID 参数
// 3. 自动生成并持久化到 {dataDir}/node.id
func NewLocalNodeInfo(dataDir, configNodeID string) (*LocalNodeInfo, error) {
	nodeID := configNodeID

	// 1. 尝试从环境变量读取
	if envID := os.Getenv("NEXKV_NODE_ID"); envID != "" {
		nodeID = envID
	}

	// 2. 如果都没有，尝试从持久化文件读取
	if nodeID == "" {
		storedID, err := readStoredNodeID(dataDir)
		if err == nil && storedID != "" {
			nodeID = storedID
		}
	}

	// 3. 如果仍然没有，自动生成并持久化
	if nodeID == "" {
		nodeID = uuid.GenerateUUIDv7()

		// 持久化到文件
		if err := storeNodeID(dataDir, nodeID); err != nil {
			return nil, err
		}
	}

	info := &LocalNodeInfo{
		nodeID:  nodeID,
		dataDir: dataDir,
	}

	return info, nil
}

// GetNodeID 获取节点 ID
func (n *LocalNodeInfo) GetNodeID() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.nodeID
}

// GetDataDir 获取节点数据根目录
func (n *LocalNodeInfo) GetDataDir() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.dataDir
}

// GetWalPath 获取 WAL 目录路径
// 格式：{dataDir}/{nodeID}/wal
func (n *LocalNodeInfo) GetWalPath() string {
	return filepath.Join(n.GetDataDir(), n.GetNodeID(), "wal")
}

// GetSnapshotPath 获取快照目录路径
// 格式：{dataDir}/{nodeID}/snapshots
func (n *LocalNodeInfo) GetSnapshotPath() string {
	return filepath.Join(n.GetDataDir(), n.GetNodeID(), "snapshots")
}

// GetSSTPath 获取 SSTable 目录路径
// 格式：{dataDir}/{nodeID}/sst
func (n *LocalNodeInfo) GetSSTPath() string {
	return filepath.Join(n.GetDataDir(), n.GetNodeID(), "sst")
}

// readStoredNodeID 从文件读取已存储的节点 ID
func readStoredNodeID(dataDir string) (string, error) {
	nodeIDFile := filepath.Join(dataDir, "node.id")

	data, err := os.ReadFile(nodeIDFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // 文件不存在不是错误
		}
		return "", err
	}

	return string(data), nil
}

// storeNodeID 持久化节点 ID 到文件
func storeNodeID(dataDir, nodeID string) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}

	nodeIDFile := filepath.Join(dataDir, "node.id")
	return os.WriteFile(nodeIDFile, []byte(nodeID), 0644)
}
