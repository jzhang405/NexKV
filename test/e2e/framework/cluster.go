package framework

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

// TestCluster 表示测试集群
type TestCluster struct {
	// Nodes 集群节点
	Nodes []*DaemonProcess
	// ConfigDir 配置目录
	ConfigDir string
	// LogDir 日志目录
	LogDir string
	// started 是否已启动
	started bool
	// mu 保护并发访问
	mu sync.RWMutex
}

// NewTestCluster 创建测试集群
func NewTestCluster(nodeCount int) *TestCluster {
	configDir := filepath.Join("/tmp", "nexkv-e2e", fmt.Sprintf("cluster-%d", time.Now().Unix()))
	logDir := filepath.Join(configDir, "logs")

	nodes := make([]*DaemonProcess, nodeCount)
	basePort := 19000

	for i := 0; i < nodeCount; i++ {
		nodeID := fmt.Sprintf("node-%d", i+1)
		addr := fmt.Sprintf("127.0.0.1:%d", basePort+i*100)
		nodes[i] = NewDaemonProcess(nodeID, addr, configDir, logDir)
	}

	return &TestCluster{
		Nodes:     nodes,
		ConfigDir: configDir,
		LogDir:    logDir,
	}
}

// Start 启动集群所有节点
func (c *TestCluster) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return fmt.Errorf("cluster already started")
	}

	// 创建必要目录
	if err := createDirIfNotExists(c.ConfigDir); err != nil {
		return err
	}
	if err := createDirIfNotExists(c.LogDir); err != nil {
		return err
	}

	// 生成配置文件
	if err := c.generateConfigs(); err != nil {
		return err
	}

	// 启动所有节点
	for _, node := range c.Nodes {
		if err := node.Start(ctx); err != nil {
			// 启动失败，停止已启动的节点
			c.stopAll()
			return fmt.Errorf("failed to start node %s: %w", node.NodeID, err)
		}
	}

	c.started = true
	return nil
}

// Stop 停止集群所有节点
func (c *TestCluster) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return nil
	}

	return c.stopAll()
}

// stopAll 停止所有节点（内部方法，不加锁）
func (c *TestCluster) stopAll() error {
	var lastErr error
	for _, node := range c.Nodes {
		if err := node.Stop(); err != nil {
			lastErr = err
		}
	}

	c.started = false
	return lastErr
}

// WaitStable 等待集群稳定
func (c *TestCluster) WaitStable(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("cluster stable timeout")
		case <-ticker.C:
			if c.isStable() {
				return nil
			}
		}
	}
}

// isStable 检查集群是否稳定
func (c *TestCluster) isStable() bool {
	// 检查所有节点是否在线
	for _, node := range c.Nodes {
		if !node.IsRunning() {
			return false
		}
	}

	// TODO: 检查集群拓扑是否形成
	// TODO: 检查 Gossip 是否完成同步

	return true
}

// KillNode 杀死指定节点（模拟故障）
func (c *TestCluster) KillNode(nodeID string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	node := c.findNode(nodeID)
	if node == nil {
		return fmt.Errorf("node %s not found", nodeID)
	}

	// 强制杀死进程
	if node.cmd != nil && node.cmd.Process != nil {
		if err := node.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill node %s: %w", nodeID, err)
		}
	}

	node.started = false
	return nil
}

// RestartNode 重启指定节点
func (c *TestCluster) RestartNode(ctx context.Context, nodeID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	node := c.findNode(nodeID)
	if node == nil {
		return fmt.Errorf("node %s not found", nodeID)
	}

	// 先停止
	if err := node.Stop(); err != nil {
		return err
	}

	// 再启动
	if err := node.Start(ctx); err != nil {
		return err
	}

	return nil
}

// CLI 获取指定节点的 CLI 执行器
func (c *TestCluster) CLI(nodeID string) *CLIExecutor {
	node := c.findNode(nodeID)
	if node == nil {
		return nil
	}
	return NewCLIExecutor(node.Addr)
}

// NodeCount 获取节点数量
func (c *TestCluster) NodeCount() int {
	return len(c.Nodes)
}

// OnlineNodeCount 获取在线节点数量
func (c *TestCluster) OnlineNodeCount() int {
	count := 0
	for _, node := range c.Nodes {
		if node.IsRunning() {
			count++
		}
	}
	return count
}

// findNode 查找节点（内部方法，不加锁）
func (c *TestCluster) findNode(nodeID string) *DaemonProcess {
	for _, node := range c.Nodes {
		if node.NodeID == nodeID {
			return node
		}
	}
	return nil
}

// generateConfigs 生成配置文件
func (c *TestCluster) generateConfigs() error {
	// TODO: 生成实际的配置文件
	// 这里需要根据 NexKV 的配置格式生成
	return nil
}

// Cleanup 清理测试文件
func (c *TestCluster) Cleanup() error {
	// TODO: 删除测试目录
	return nil
}

// createDirIfNotExists 创建目录（如果不存在）
func createDirIfNotExists(dir string) error {
	// TODO: 实现
	return nil
}
