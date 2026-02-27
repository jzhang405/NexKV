// Package model 定义领域模型
package model

import (
	"fmt"
	"runtime"
)

// TaskMode 任务调度模式（值对象）
// 定义任务应该如何被调度和执行
type TaskMode int

const (
	// ModePerCore 每核单 goroutine 模式
	// 支持 CPU 绑定，适用于 HLC、WAL、Transpose 等延迟敏感场景
	ModePerCore TaskMode = iota + 1

	// ModeCustomPool ants 自定义池模式
	// 适用于通用场景，提供独立的资源隔离
	ModeCustomPool

	// ModeFuncPool ants 函数池模式
	// 适用于高频重复任务，减少函数对象创建开销
	ModeFuncPool

	// ModeMultiPool ants 多池模式
	// 适用于分片场景，每个分片独立的池
	ModeMultiPool

	// ModeDefaultPool ants 默认池模式（隐式回退）
	// 适用于临时任务、测试场景
	ModeDefaultPool TaskMode = 99
)

// String 返回调度模式字符串
func (m TaskMode) String() string {
	switch m {
	case ModePerCore:
		return "per-core"
	case ModeCustomPool:
		return "custom-pool"
	case ModeFuncPool:
		return "func-pool"
	case ModeMultiPool:
		return "multi-pool"
	case ModeDefaultPool:
		return "default-pool"
	default:
		return "unknown"
	}
}

// IsValid 验证 TaskMode 是否有效
func (m TaskMode) IsValid() bool {
	switch m {
	case ModePerCore, ModeCustomPool, ModeFuncPool, ModeMultiPool, ModeDefaultPool:
		return true
	default:
		return false
	}
}

// FallbackMode 返回降级后的调度模式
// 当当前模式不支持时，应该降级到的模式
func (m TaskMode) FallbackMode() TaskMode {
	switch m {
	case ModePerCore:
		return ModeCustomPool // PerCore 降级到 CustomPool
	case ModeCustomPool, ModeFuncPool, ModeMultiPool:
		return ModeDefaultPool // 其他模式降级到 DefaultPool
	default:
		return ModeDefaultPool
	}
}

// 支持的平台常量
const (
	PlatformLinux   = "linux"
	PlatformWindows = "windows"
	PlatformDarwin  = "darwin"
)

// IsSupportedOn 检查当前模式是否支持指定平台
// Linux 和 Windows 支持真正的 CPU 绑核
// macOS 不支持真正的 CPU 绑核（仅 LockOSThread）
func (m TaskMode) IsSupportedOn(platform string) bool {
	switch m {
	case ModePerCore:
		// 只有 Linux 和 Windows 支持真正的 CPU 绑核
		return platform == PlatformLinux || platform == PlatformWindows
	case ModeCustomPool, ModeFuncPool, ModeMultiPool, ModeDefaultPool:
		// 其他模式跨平台支持
		return true
	default:
		return false
	}
}

// RecommendedConfig 返回当前模式的推荐配置
func (m TaskMode) RecommendedConfig() ModeConfig {
	switch m {
	case ModePerCore:
		return ModeConfig{
			MaxConcurrency: runtime.NumCPU(),
			QueueSize:      1000,
			EnableAffinity: true,
		}
	case ModeCustomPool:
		return ModeConfig{
			MaxConcurrency: runtime.NumCPU() * 2,
			QueueSize:      2000,
			EnableAffinity: false,
		}
	case ModeFuncPool:
		return ModeConfig{
			MaxConcurrency: runtime.NumCPU() * 4,
			QueueSize:      5000,
			EnableAffinity: false,
		}
	case ModeMultiPool:
		return ModeConfig{
			MaxConcurrency: runtime.NumCPU() * 2,
			QueueSize:      1000,
			EnableAffinity: false,
		}
	case ModeDefaultPool:
		return ModeConfig{
			MaxConcurrency: -1, // 使用 ants 默认值
			QueueSize:      -1, // 使用 ants 默认值
			EnableAffinity: false,
		}
	default:
		return ModeConfig{
			MaxConcurrency: runtime.NumCPU(),
			QueueSize:      1000,
			EnableAffinity: false,
		}
	}
}

// ModeConfig 调度模式配置
type ModeConfig struct {
	// MaxConcurrency 最大并发数
	// -1 表示使用默认值
	MaxConcurrency int

	// QueueSize 队列大小
	// -1 表示使用默认值
	QueueSize int

	// EnableAffinity 是否启用 CPU 绑核
	EnableAffinity bool
}

// ParseTaskMode 从字符串解析 TaskMode
func ParseTaskMode(s string) (TaskMode, error) {
	switch s {
	case "per-core":
		return ModePerCore, nil
	case "custom-pool":
		return ModeCustomPool, nil
	case "func-pool":
		return ModeFuncPool, nil
	case "multi-pool":
		return ModeMultiPool, nil
	case "default-pool":
		return ModeDefaultPool, nil
	default:
		return ModeDefaultPool, fmt.Errorf("unknown task mode: %s", s)
	}
}

// MustParseTaskMode 从字符串解析 TaskMode，解析失败时 panic
func MustParseTaskMode(s string) TaskMode {
	mode, err := ParseTaskMode(s)
	if err != nil {
		panic(err)
	}
	return mode
}
