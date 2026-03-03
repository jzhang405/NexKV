// Package model 定义领域模型
package model

import (
	"runtime"

	"github.com/jzhang405/NexKV/pkg/errors"
)

// TaskMode 任务调度模式（值对象）
// 简化为两种模式：PerCore（延迟敏感）和 AntsPool（通用）
type TaskMode int

const (
	// ModePerCore 每核单 goroutine 模式
	// 支持 CPU 绑定，适用于 HLC、WAL、Transaction 等延迟敏感场景
	ModePerCore TaskMode = iota + 1

	// ModeAntsPool ants 默认池模式（通用场景）
	// 适用于大多数普通任务，提供良好的并发性能
	ModeAntsPool TaskMode = 99
)

// String 返回调度模式字符串
func (m TaskMode) String() string {
	switch m {
	case ModePerCore:
		return "per-core"
	case ModeAntsPool:
		return "ants-pool"
	default:
		return "unknown"
	}
}

// IsValid 验证 TaskMode 是否有效
func (m TaskMode) IsValid() bool {
	switch m {
	case ModePerCore, ModeAntsPool:
		return true
	default:
		return false
	}
}

// FallbackMode 返回降级后的调度模式
// 双执行器降级链: PerCore -> AntsPool
func (m TaskMode) FallbackMode() TaskMode {
	switch m {
	case ModePerCore:
		return ModeAntsPool
	default:
		return ModeAntsPool
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
	case ModeAntsPool:
		// AntsPool 跨平台支持
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
	case ModeAntsPool:
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
	case "ants-pool":
		return ModeAntsPool, nil
	default:
		return ModeAntsPool, errors.UnknownTaskMode(s)
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
