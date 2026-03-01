// Package concurrency 提供并发执行器相关常量
package concurrency

import "time"

// ==========================================
// 任务池容量限制
// ==========================================

const (
	// MinPoolCapacity 最小池容量
	MinPoolCapacity = 1

	// MaxPoolCapacity 最大池容量
	MaxPoolCapacity = 100000

	// DefaultPoolCapacity 默认池容量
	DefaultPoolCapacity = 10000

	// DefaultMaxDelayedTasks 默认最大延迟任务数
	DefaultMaxDelayedTasks = 10000
)

// ==========================================
// 自动扩缩容配置
// ==========================================

const (
	// DefaultScaleThreshold 默认扩容阈值 (80%)
	DefaultScaleThreshold = 0.8

	// DefaultScaleStep 默认扩容步长
	DefaultScaleStep = 5000

	// DefaultScaleCheckInterval 默认扩容检查间隔 (每100次)
	DefaultScaleCheckInterval = int64(100)

	// DefaultShrinkThreshold 默认缩容阈值 (30%)
	DefaultShrinkThreshold = 0.3

	// DefaultShrinkStep 默认缩容步长
	DefaultShrinkStep = 5000

	// DefaultShrinkCheckInterval 默认缩容检查间隔
	DefaultShrinkCheckInterval = 5 * time.Minute

	// DefaultShrinkCooldown 默认缩容冷却时间
	DefaultShrinkCooldown = 10 * time.Minute
)

// ==========================================
// 执行器状态
// ==========================================

const (
	// ExecutorRunning 执行器运行中
	ExecutorRunning = iota

	// ExecutorClosing 执行器关闭中
	ExecutorClosing

	// ExecutorClosed 执行器已关闭
	ExecutorClosed
)

// ==========================================
// Per-Core 执行器配置
// ==========================================

const (
	// MaxCores 最大核心数限制
	MaxCores = 64

	// DefaultQueueSize 默认队列大小
	DefaultQueueSize = 1000

	// DefaultStarvationTimeout 默认饥饿防护超时时间
	DefaultStarvationTimeout = 10 * time.Second

	// StarvationCheckInterval 饥饿检查间隔
	StarvationCheckInterval = 10 * time.Millisecond

	// DefaultStreamBufferSize 默认流缓冲队列大小
	DefaultStreamBufferSize = 10
)

// ==========================================
// CPU 亲和性常量
// ==========================================

const (
	// CPUSetSize CPU 集合大小
	CPUSetSize = 1024
)
