// Package cluster 提供节点 ID 管理
//
// 支持：
//   - 环境变量配置
//   - 自动生成并持久化
//   - 获取节点数据目录路径
//
// NodeID 格式：[环境标识]_[主机名]_[服务名称(可选)]_[固定端口]
// 示例：
//   - prod_prod_shop_server_01_shop_service_8080
//   - prod_prod_shop_server_01_pay_service_8081
package cluster

import (
	"fmt"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
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

// NodeIDConfig 节点 ID 配置
type NodeIDConfig struct {
	// Env 环境标识（如: prod, dev, test）
	Env string
	// Hostname 主机名（如: prod-shop-server-01）
	// 如果为空，自动获取系统主机名
	Hostname string
	// Service 服务名称（可选，如: shop-service, pay-service）
	Service string
	// Port 固定端口（如: 8080, 8081）
	Port int
}

// LocalNodeInfo 本地节点信息
type LocalNodeInfo struct {
	mu     sync.RWMutex
	nodeID string
	config *NodeIDConfig
	// 数据根目录（例如：/data/nexkv）
	dataDir string
}

// NewLocalNodeInfo 创建本地节点信息
//
// 优先级：
// 1. 环境变量 NEXKV_NODE_ID（完整 NodeID）
// 2. config 参数（NodeIDConfig 结构）
// 3. 自动生成并持久化到 {dataDir}/node.id
func NewLocalNodeInfo(dataDir string, config *NodeIDConfig) (*LocalNodeInfo, error) {
	var nodeID string

	// 1. 尝试从环境变量读取完整 NodeID
	if envID := os.Getenv("NEXKV_NODE_ID"); envID != "" {
		nodeID = envID
		logging.WithField("node_id", nodeID).Info("从环境变量读取节点 ID")
	}

	// 2. 如果没有，尝试从配置生成
	if nodeID == "" && config != nil {
		generatedID, err := generateNodeID(config)
		if err != nil {
			return nil, types.NewClusterNodeManagementError("生成节点 ID", "", err)
		}
		nodeID = generatedID

		// 持久化到文件
		if err := storeNodeID(dataDir, nodeID); err != nil {
			return nil, err
		}
		logging.WithField("node_id", nodeID).Info("从配置生成并持久化节点 ID")
	}

	// 3. 如果仍然没有，尝试从持久化文件读取
	if nodeID == "" {
		storedID, err := readStoredNodeID(dataDir)
		if err == nil && storedID != "" {
			nodeID = storedID
			logging.WithField("node_id", nodeID).Info("从持久化文件读取节点 ID")
		}
	}

	// 4. 如果仍然没有，使用默认配置自动生成
	if nodeID == "" {
		defaultConfig := getDefaultNodeIDConfig()
		generatedID, err := generateNodeID(defaultConfig)
		if err != nil {
			return nil, types.NewClusterNodeManagementError("生成节点 ID", "", err)
		}
		nodeID = generatedID

		// 持久化到文件
		if err := storeNodeID(dataDir, nodeID); err != nil {
			return nil, err
		}
		logging.WithField("node_id", nodeID).Info("自动生成并持久化节点 ID")
	}

	info := &LocalNodeInfo{
		nodeID:  nodeID,
		dataDir: dataDir,
		config:  config,
	}

	return info, nil
}

// generateNodeID 根据配置生成节点 ID
// 格式：[环境]_[主机]_[服务]_[端口]
// 主机名和服务名内部使用 - 连接，非字母数字字符被替换为 -
func generateNodeID(config *NodeIDConfig) (string, error) {
	// 获取主机名
	hostname := config.Hostname
	if hostname == "" {
		var err error
		hostname, err = os.Hostname()
		if err != nil {
			return "", types.NewClusterNodeManagementError("获取主机名", "", err)
		}
	}

	// 清理主机名：转小写，非字母数字替换为 -
	hostname = strings.ToLower(hostname)
	hostname = sanitizeName(hostname)

	// 清理服务名：转小写，非字母数字替换为 -
	service := ""
	if config.Service != "" {
		service = strings.ToLower(config.Service)
		service = sanitizeName(service)
	}

	// 构建节点 ID
	nodeID := fmt.Sprintf("%s_%s", config.Env, hostname)

	// 如果有服务名称，添加服务名
	if service != "" {
		nodeID += "_" + service
	}

	// 添加端口
	nodeID += "_" + fmt.Sprintf("%d", config.Port)

	return nodeID, nil
}

// sanitizeName 将名称中的非字母数字字符替换为 -
func sanitizeName(name string) string {
	// 使用 strings.Builder 提高性能
	var result strings.Builder
	result.Grow(len(name))

	prevWasDash := false

	for i := 0; i < len(name); i++ {
		c := name[i]
		isAlnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')

		if isAlnum {
			result.WriteByte(c)
			prevWasDash = false
		} else if !prevWasDash && result.Len() > 0 {
			// 非字母数字字符替换为 -，但避免连续和开头
			result.WriteByte('-')
			prevWasDash = true
		}
	}

	// 移除末尾可能多余的 -
	s := result.String()
	if len(s) > 0 && s[len(s)-1] == '-' {
		s = s[:len(s)-1]
	}

	return s
}

// getDefaultNodeIDConfig 获取默认节点 ID 配置
func getDefaultNodeIDConfig() *NodeIDConfig {
	hostname, _ := os.Hostname()
	// hostname 会在 generateNodeID 中被清理

	return &NodeIDConfig{
		Env:      "dev", // 默认开发环境
		Hostname: hostname,
		Service:  "nexkv",
		Port:     8080, // 默认端口
	}
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
