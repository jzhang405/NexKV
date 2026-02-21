// Package framework 提供 NexKV 集成测试框架的核心组件
//
// 本包实现可复用的测试框架，支持：
// - 组件化测试（Transport/Storage/Replication 等）
// - 测试隔离（独立的 TestContext）
// - 生命周期管理（Init/Start/Stop/Close）
// - 健康检查（带重试和超时控制）
//
// 基于 Spike 文档 v2.12 设计
package framework

import (
	"context"
	"time"
)

// ComponentType 组件类型（type-safe，使用 string）
// ⭐ v1.3 完全对齐 Spike
type ComponentType string

const (
	// ComponentTypeTransport Transport 层组件
	ComponentTypeTransport ComponentType = "transport"
	// ComponentTypeStorage Storage 层组件
	ComponentTypeStorage ComponentType = "storage"
	// ComponentTypeReplication Replication 层组件
	ComponentTypeReplication ComponentType = "replication"
	// ComponentTypeCluster Cluster 层组件
	ComponentTypeCluster ComponentType = "cluster"
	// ComponentTypeTransaction Transaction 层组件
	ComponentTypeTransaction ComponentType = "transaction"
	// ComponentTypeMetadata Metadata 层组件
	ComponentTypeMetadata ComponentType = "metadata"
)

// EnvironmentStatus 环境状态
type EnvironmentStatus int

const (
	// EnvironmentStatusUnknown 未知状态
	EnvironmentStatusUnknown EnvironmentStatus = iota
	// EnvironmentStatusCreating 创建中
	EnvironmentStatusCreating
	// EnvironmentStatusCreated 已创建
	EnvironmentStatusCreated
	// EnvironmentStatusStarting 启动中
	EnvironmentStatusStarting
	// EnvironmentStatusRunning 运行中
	EnvironmentStatusRunning
	// EnvironmentStatusStopping 停止中
	EnvironmentStatusStopping
	// EnvironmentStatusStopped 已停止
	EnvironmentStatusStopped
	// EnvironmentStatusError 错误状态
	EnvironmentStatusError
)

// String 返回环境状态的字符串表示
func (s EnvironmentStatus) String() string {
	switch s {
	case EnvironmentStatusCreating:
		return "creating"
	case EnvironmentStatusCreated:
		return "created"
	case EnvironmentStatusStarting:
		return "starting"
	case EnvironmentStatusRunning:
		return "running"
	case EnvironmentStatusStopping:
		return "stopping"
	case EnvironmentStatusStopped:
		return "stopped"
	case EnvironmentStatusError:
		return "error"
	default:
		return "unknown"
	}
}

// HealthCheckConfig 健康检查配置
// ⭐ v1.3 补充（Spike 第 323-344 行）
type HealthCheckConfig struct {
	// Timeout 健康检查超时时间
	Timeout time.Duration
	// Interval 健康检查间隔
	Interval time.Duration
	// RetryCount 重试次数
	RetryCount int
	// RetryInterval 重试间隔
	RetryInterval time.Duration
	// Critical 是否为关键组件
	Critical bool
}

// DefaultHealthCheckConfig 返回默认健康检查配置
func DefaultHealthCheckConfig() *HealthCheckConfig {
	return &HealthCheckConfig{
		Timeout:       5 * time.Second,
		Interval:      10 * time.Second,
		RetryCount:    3,
		RetryInterval: 1 * time.Second,
		Critical:      true,
	}
}

// TestEnvironment 测试环境接口
// 所有组件集成测试的基础接口
//
// v1.5 更新：添加 Init 和 Close 方法
type TestEnvironment interface {
	// ID 返回环境唯一标识
	ID() string

	// Init 初始化测试环境资源
	// 在 Start 之前调用，用于预分配资源、创建目录等
	Init(ctx context.Context) error

	// Start 启动测试环境
	Start(ctx context.Context) error

	// Stop 停止测试环境并清理资源
	Stop(ctx context.Context) error

	// Close 释放所有环境资源
	// 在测试完成后调用，用于释放 Init 中分配的资源
	Close() error

	// Status 返回当前环境状态
	Status() EnvironmentStatus

	// GetComponent 获取指定名称的组件实例
	GetComponent(name string) (TestComponent, error)

	// ListComponents 列出所有组件实例
	ListComponents() []TestComponent
}

// TestComponent 可测试组件接口
// ⭐ v1.3 完全对齐 Spike（第 290-320 行）
type TestComponent interface {
	// Name 返回组件名称
	// ⭐ v1.3: Name() 而非 GetName()
	Name() string

	// Type 返回组件类型
	// ⭐ v1.3: Type() 而非 GetType()
	Type() ComponentType

	// GetDependencies 返回组件依赖列表
	// type-safe 设计
	GetDependencies() []ComponentType

	// Init 初始化组件
	// ⭐ v1.3: 补充 env 参数
	Init(ctx context.Context, env TestEnvironment) error

	// Start 启动组件
	Start(ctx context.Context) error

	// Stop 停止组件
	Stop(ctx context.Context) error

	// HealthCheck 执行深度健康检查
	HealthCheck(ctx context.Context) error

	// GetHealthCheckConfig 返回健康检查配置
	// ⭐ v1.3 补充
	GetHealthCheckConfig() *HealthCheckConfig
}

// BaseComponent 提供 TestComponent 接口的默认实现
// 可嵌入其他组件实现中以减少重复代码
type BaseComponent struct {
	name         string
	compType     ComponentType
	dependencies []ComponentType
	healthConfig *HealthCheckConfig
}

// NewBaseComponent 创建基础组件
func NewBaseComponent(name string, compType ComponentType, deps []ComponentType) *BaseComponent {
	return &BaseComponent{
		name:         name,
		compType:     compType,
		dependencies: deps,
		healthConfig: DefaultHealthCheckConfig(),
	}
}

// Name 返回组件名称
func (b *BaseComponent) Name() string {
	return b.name
}

// Type 返回组件类型
func (b *BaseComponent) Type() ComponentType {
	return b.compType
}

// GetDependencies 返回组件依赖列表
func (b *BaseComponent) GetDependencies() []ComponentType {
	return b.dependencies
}

// Init 默认初始化实现（空操作）
func (b *BaseComponent) Init(ctx context.Context, env TestEnvironment) error {
	return nil
}

// Start 默认启动实现（空操作）
func (b *BaseComponent) Start(ctx context.Context) error {
	return nil
}

// Stop 默认停止实现（空操作）
func (b *BaseComponent) Stop(ctx context.Context) error {
	return nil
}

// HealthCheck 默认健康检查实现（总是成功）
func (b *BaseComponent) HealthCheck(ctx context.Context) error {
	return nil
}

// GetHealthCheckConfig 返回健康检查配置
func (b *BaseComponent) GetHealthCheckConfig() *HealthCheckConfig {
	return b.healthConfig
}

// SetHealthCheckConfig 设置健康检查配置
func (b *BaseComponent) SetHealthCheckConfig(config *HealthCheckConfig) {
	b.healthConfig = config
}
