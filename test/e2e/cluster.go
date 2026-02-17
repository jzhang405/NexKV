// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

// ClusterConfig 集群配置
type ClusterConfig struct {
	Name       string // 集群名称
	NodeCount  int    // 节点数量
	BinaryPath string // nexkvd 二进制路径（可选）
}

// TestNode 测试节点
type TestNode struct {
	ID        string // 节点 ID
	HostID    string // 主机 ID
	Addr      string // 监听地址
	RPCPort   int    // RPC 端口
	DataDir   string // 数据目录
	ProcessID string // 进程 ID
}

// TestCluster 测试集群
type TestCluster struct {
	Config         *ClusterConfig
	Nodes          []*TestNode
	PortAllocator  *TestPortAllocator
	DataDirManager *DataDirManager
	ProcessManager *ProcessManager
	Logger         *log.Logger
	mu             sync.RWMutex // 保护 Nodes 并发访问
}

// NewTestCluster 创建测试集群
func NewTestCluster(
	config *ClusterConfig,
	portAllocator *TestPortAllocator,
	dataDirManager *DataDirManager,
) (*TestCluster, error) {
	logger := log.New(os.Stderr, "[Cluster] ", log.LstdFlags)

	if config == nil {
		config = &ClusterConfig{
			Name:      "test-cluster",
			NodeCount: 1,
		}
	}

	cluster := &TestCluster{
		Config:         config,
		Nodes:          make([]*TestNode, 0, config.NodeCount),
		PortAllocator:  portAllocator,
		DataDirManager: dataDirManager,
		Logger:         logger,
	}

	// 创建节点配置
	for i := 0; i < config.NodeCount; i++ {
		nodeID := fmt.Sprintf("node-%d", i+1)
		hostID := fmt.Sprintf("host-%d", i+1)

		// 分配 RPC 端口
		rpcPort, err := portAllocator.AllocatePort(nodeID)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate port for %s: %w", nodeID, err)
		}

		// 创建数据目录
		dataDir, err := dataDirManager.CreateTestDir(nodeID)
		if err != nil {
			return nil, fmt.Errorf("failed to create data dir for %s: %w", nodeID, err)
		}

		node := &TestNode{
			ID:      nodeID,
			HostID:  hostID,
			Addr:    fmt.Sprintf("127.0.0.1:%d", rpcPort),
			RPCPort: rpcPort,
			DataDir: dataDir,
		}

		cluster.Nodes = append(cluster.Nodes, node)
	}

	return cluster, nil
}

// GetNode 获取指定 ID 的节点
func (c *TestCluster) GetNode(nodeID string) *TestNode {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, node := range c.Nodes {
		if node.ID == nodeID {
			return node
		}
	}
	return nil
}

// NodeCount 返回节点数量
func (c *TestCluster) NodeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.Nodes)
}

// AddNode 添加节点（线程安全）
func (c *TestCluster) AddNode(node *TestNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Nodes = append(c.Nodes, node)
}

// RemoveNode 移除节点（线程安全）
func (c *TestCluster) RemoveNode(nodeID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, node := range c.Nodes {
		if node.ID == nodeID {
			c.Nodes = append(c.Nodes[:i], c.Nodes[i+1:]...)
			return true
		}
	}
	return false
}

// Start 启动集群（预留接口，当前仅记录日志）
func (c *TestCluster) Start() error {
	c.mu.RLock()
	nodeCount := len(c.Nodes)
	c.mu.RUnlock()

	c.Logger.Printf("Starting cluster %s with %d nodes", c.Config.Name, nodeCount)
	// TODO: 实现真实的进程启动（需要 nexkvd 二进制）
	return nil
}

// Stop 停止集群（预留接口）
func (c *TestCluster) Stop() error {
	c.Logger.Printf("Stopping cluster %s", c.Config.Name)
	// TODO: 实现真实的进程停止
	return nil
}

// Close 实现 io.Closer 接口，便于 defer 调用
func (c *TestCluster) Close() error {
	return c.Stop()
}

// 确保实现 io.Closer 接口
var _ io.Closer = (*TestCluster)(nil)
