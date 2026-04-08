// Package framework 提供 NexKV 集成测试框架的集群管理功能
//
// 本包包含测试集群的创建、管理和生命周期控制，支持多节点分布式测试场景
package framework

import (
	"context"
	"sync"

	"github.com/jzhang405/NexKV/pkg/errors"
)

// NodeConfig 节点配置
// 用于创建测试节点时的配置
type NodeConfig struct {
	// ID 节点唯一标识
	ID string
	// Index 节点索引（在集群中的序号）
	Index int
	// ClusterID 集群唯一标识
	ClusterID string
	// BaseDir 节点数据目录
	BaseDir string
	// IsBootstrap 是否为引导节点
	IsBootstrap bool
	// Components 节点包含的组件类型列表
	Components []ComponentType
	// Properties 额外配置属性
	Properties map[string]any
}

// TestNode 测试节点接口
//
// v2.11 更新：添加完整生命周期方法
type TestNode interface {
	// ID 返回节点唯一标识
	ID() string

	// Address 返回节点地址
	Address() string

	// Start 启动节点
	Start(ctx context.Context) error

	// Stop 停止节点
	Stop(ctx context.Context) error

	// IsRunning 检查节点是否运行
	IsRunning() bool

	// AddComponent 添加组件到节点
	AddComponent(comp TestComponent) error

	// GetComponent 获取节点上的组件实例
	GetComponent(name string) (TestComponent, error)

	// IsHealthy 检查节点健康状态
	IsHealthy(ctx context.Context) bool

	// ConnectTo 连接到另一个节点
	ConnectTo(ctx context.Context, target TestNode) error

	// DisconnectFrom 断开与另一个节点的连接
	DisconnectFrom(ctx context.Context, target TestNode) error

	// IsConnectedTo 检查是否连接到指定节点
	IsConnectedTo(nodeID string) bool

	// GetConnectedPeers 获取已连接的节点列表
	GetConnectedPeers() []string
}

// TestCluster 测试集群接口
type TestCluster interface {
	TestEnvironment

	// AddNode 添加节点到集群
	AddNode(config NodeConfig) (TestNode, error)

	// GetNode 获取指定 ID 的节点
	GetNode(id string) (TestNode, error)

	// RemoveNode 从集群移除节点
	RemoveNode(id string) error

	// ListNodes 列出所有节点
	ListNodes() []TestNode

	// StartAll 启动所有节点
	StartAll(ctx context.Context) error

	// StopAll 停止所有节点
	StopAll(ctx context.Context) error
}

// DefaultCluster 默认集群实现
type DefaultCluster struct {
	mu       sync.RWMutex
	id       string
	nodes    map[string]TestNode
	status   EnvironmentStatus
	registry *ComponentRegistry
}

// NewDefaultCluster 创建默认集群
func NewDefaultCluster(id string, registry *ComponentRegistry) *DefaultCluster {
	return &DefaultCluster{
		id:       id,
		nodes:    make(map[string]TestNode),
		status:   EnvironmentStatusCreated,
		registry: registry,
	}
}

// ID 返回集群唯一标识
func (c *DefaultCluster) ID() string {
	return c.id
}

// Init 初始化集群
func (c *DefaultCluster) Init(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.status != EnvironmentStatusCreated {
		return errors.WrapString(errors.ErrInvalidState, "cluster must be in created state to init, current: %s", c.status.String())
	}

	c.status = EnvironmentStatusCreating
	// 初始化逻辑（如创建目录等）
	c.status = EnvironmentStatusCreated
	return nil
}

// Start 启动集群
func (c *DefaultCluster) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.status != EnvironmentStatusCreated && c.status != EnvironmentStatusStopped {
		return errors.WrapString(errors.ErrInvalidState, "cluster must be in created or stopped state to start, current: %s", c.status.String())
	}

	c.status = EnvironmentStatusStarting
	if err := c.StartAll(ctx); err != nil {
		c.status = EnvironmentStatusError
		return err
	}
	c.status = EnvironmentStatusRunning
	return nil
}

// Stop 停止集群
func (c *DefaultCluster) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.status != EnvironmentStatusRunning {
		return errors.WrapString(errors.ErrInvalidState, "cluster must be in running state to stop, current: %s", c.status.String())
	}

	c.status = EnvironmentStatusStopping
	if err := c.StopAll(ctx); err != nil {
		c.status = EnvironmentStatusError
		return err
	}
	c.status = EnvironmentStatusStopped
	return nil
}

// Close 关闭集群并释放资源
func (c *DefaultCluster) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 清理所有节点
	c.nodes = make(map[string]TestNode)
	c.status = EnvironmentStatusUnknown
	return nil
}

// Status 返回集群状态
func (c *DefaultCluster) Status() EnvironmentStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// GetComponent 获取组件（集群级别）
func (c *DefaultCluster) GetComponent(name string) (TestComponent, error) {
	return nil, errors.Wrap(errors.ErrComponentNotFound, "component lookup not supported at cluster level, use node.GetComponent")
}

// ListComponents 列出所有组件（集群级别）
func (c *DefaultCluster) ListComponents() []TestComponent {
	return []TestComponent{}
}

// AddNode 添加节点到集群
func (c *DefaultCluster) AddNode(config NodeConfig) (TestNode, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.nodes[config.ID]; exists {
		return nil, errors.WrapString(errors.ErrComponentExists, "node already exists", config.ID)
	}

	// 创建节点（具体实现由子类提供）
	return nil, errors.Wrap(errors.ErrNotImplemented, "AddNode must be implemented by subclass")
}

// GetNode 获取指定 ID 的节点
func (c *DefaultCluster) GetNode(id string) (TestNode, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	node, exists := c.nodes[id]
	if !exists {
		return nil, errors.WrapString(errors.ErrTestNodeNotFound, "node not found", id)
	}
	return node, nil
}

// RemoveNode 从集群移除节点
func (c *DefaultCluster) RemoveNode(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	node, exists := c.nodes[id]
	if !exists {
		return errors.WrapString(errors.ErrTestNodeNotFound, "node not found", id)
	}

	// 停止节点
	if node.IsRunning() {
		ctx := context.Background()
		if err := node.Stop(ctx); err != nil {
			return errors.WrapString(err, "failed to stop node", id)
		}
	}

	delete(c.nodes, id)
	return nil
}

// ListNodes 列出所有节点
func (c *DefaultCluster) ListNodes() []TestNode {
	c.mu.RLock()
	defer c.mu.RUnlock()

	nodes := make([]TestNode, 0, len(c.nodes))
	for _, node := range c.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// StartAll 启动所有节点
func (c *DefaultCluster) StartAll(ctx context.Context) error {
	c.mu.RLock()
	nodes := make([]TestNode, 0, len(c.nodes))
	for _, node := range c.nodes {
		nodes = append(nodes, node)
	}
	c.mu.RUnlock()

	for _, node := range nodes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !node.IsRunning() {
			if err := node.Start(ctx); err != nil {
				return errors.WrapString(err, "failed to start node", node.ID())
			}
		}
	}
	return nil
}

// StopAll 停止所有节点
func (c *DefaultCluster) StopAll(ctx context.Context) error {
	c.mu.RLock()
	nodes := make([]TestNode, 0, len(c.nodes))
	for _, node := range c.nodes {
		nodes = append(nodes, node)
	}
	c.mu.RUnlock()

	for _, node := range nodes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if node.IsRunning() {
			if err := node.Stop(ctx); err != nil {
				return errors.WrapString(err, "failed to stop node", node.ID())
			}
		}
	}
	return nil
}
