// Package model 定义领域模型
package model

import (
	"runtime"
	"testing"
)

func TestTaskMode_String(t *testing.T) {
	tests := []struct {
		mode     TaskMode
		expected string
	}{
		{ModePerCore, "per-core"},
		{ModeCustomPool, "custom-pool"},
		{ModeFuncPool, "func-pool"},
		{ModeMultiPool, "multi-pool"},
		{ModeDefaultPool, "default-pool"},
		{TaskMode(100), "unknown"}, // 真正的无效值
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.mode.String(); got != tt.expected {
				t.Errorf("TaskMode.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTaskMode_IsValid(t *testing.T) {
	tests := []struct {
		mode     TaskMode
		expected bool
	}{
		{ModePerCore, true},
		{ModeCustomPool, true},
		{ModeFuncPool, true},
		{ModeMultiPool, true},
		{ModeDefaultPool, true},
		{TaskMode(-1), false},
		{TaskMode(0), false},
		{TaskMode(100), false},
	}

	for _, tt := range tests {
		name := tt.mode.String()
		t.Run(name, func(t *testing.T) {
			if got := tt.mode.IsValid(); got != tt.expected {
				t.Errorf("TaskMode.IsValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTaskMode_FallbackMode(t *testing.T) {
	tests := []struct {
		mode     TaskMode
		expected TaskMode
	}{
		{ModePerCore, ModeDefaultPool},     // PerCore 降级到 DefaultPool（三级降级策略）
		{ModeCustomPool, ModeDefaultPool},  // CustomPool 降级到 DefaultPool
		{ModeFuncPool, ModeDefaultPool},    // FuncPool 降级到 DefaultPool
		{ModeMultiPool, ModeDefaultPool},   // MultiPool 降级到 DefaultPool
		{ModeDefaultPool, ModeDefaultPool}, // DefaultPool 无需降级
	}

	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			if got := tt.mode.FallbackMode(); got != tt.expected {
				t.Errorf("TaskMode.FallbackMode() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTaskMode_IsSupportedOn(t *testing.T) {
	tests := []struct {
		platform string
		mode     TaskMode
		expected bool
	}{
		// Linux 支持所有模式
		{"linux", ModePerCore, true},
		{"linux", ModeCustomPool, true},
		{"linux", ModeFuncPool, true},
		{"linux", ModeMultiPool, true},
		{"linux", ModeDefaultPool, true},

		// Windows 支持所有模式
		{"windows", ModePerCore, true},
		{"windows", ModeCustomPool, true},

		// macOS 不支持真绑核，PerCore 降级
		{"darwin", ModePerCore, false},
		{"darwin", ModeCustomPool, true},

		// 未知平台
		{"freebsd", ModePerCore, false},
		{"freebsd", ModeDefaultPool, true},
	}

	for _, tt := range tests {
		name := tt.platform + "_" + tt.mode.String()
		t.Run(name, func(t *testing.T) {
			if got := tt.mode.IsSupportedOn(tt.platform); got != tt.expected {
				t.Errorf("TaskMode.IsSupportedOn(%s) = %v, want %v", tt.platform, got, tt.expected)
			}
		})
	}
}

func TestTaskMode_RecommendedConfig(t *testing.T) {
	tests := []struct {
		mode TaskMode
	}{
		{ModePerCore},
		{ModeCustomPool},
		{ModeFuncPool},
		{ModeMultiPool},
		{ModeDefaultPool},
	}

	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			config := tt.mode.RecommendedConfig()

			// DefaultPool 使用 -1 表示使用 ants 默认值
			if tt.mode == ModeDefaultPool {
				if config.MaxConcurrency != -1 {
					t.Errorf("DefaultPool MaxConcurrency should be -1, got %d", config.MaxConcurrency)
				}
				if config.QueueSize != -1 {
					t.Errorf("DefaultPool QueueSize should be -1, got %d", config.QueueSize)
				}
				if config.EnableAffinity {
					t.Error("DefaultPool should not have EnableAffinity = true")
				}
				return
			}

			// 其他模式验证配置有效性
			if config.MaxConcurrency <= 0 {
				t.Errorf("RecommendedConfig().MaxConcurrency = %v, want > 0", config.MaxConcurrency)
			}
			if config.QueueSize <= 0 {
				t.Errorf("RecommendedConfig().QueueSize = %v, want > 0", config.QueueSize)
			}

			// PerCore 模式应该启用绑核
			if tt.mode == ModePerCore && !config.EnableAffinity {
				t.Error("PerCore mode should have EnableAffinity = true")
			}

			// 其他模式不应该启用绑核
			if tt.mode != ModePerCore && config.EnableAffinity {
				t.Errorf("%s mode should not have EnableAffinity = true", tt.mode)
			}
		})
	}
}

func TestTaskMode_ParseTaskMode(t *testing.T) {
	tests := []struct {
		input    string
		expected TaskMode
		hasError bool
	}{
		{"per-core", ModePerCore, false},
		{"custom-pool", ModeCustomPool, false},
		{"func-pool", ModeFuncPool, false},
		{"multi-pool", ModeMultiPool, false},
		{"default-pool", ModeDefaultPool, false},
		{"invalid", ModeDefaultPool, true},
		{"", ModeDefaultPool, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseTaskMode(tt.input)
			if tt.hasError {
				if err == nil {
					t.Errorf("ParseTaskMode(%s) expected error, got nil", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("ParseTaskMode(%s) unexpected error: %v", tt.input, err)
				}
				if got != tt.expected {
					t.Errorf("ParseTaskMode(%s) = %v, want %v", tt.input, got, tt.expected)
				}
			}
		})
	}
}

func TestModeConfig_Defaults(t *testing.T) {
	// 测试 PerCore 推荐配置
	perCoreConfig := ModePerCore.RecommendedConfig()
	if perCoreConfig.MaxConcurrency != runtime.NumCPU() {
		t.Errorf("PerCore MaxConcurrency should be runtime.NumCPU(), got %d", perCoreConfig.MaxConcurrency)
	}
	if perCoreConfig.QueueSize != 1000 {
		t.Errorf("PerCore QueueSize should be 1000, got %d", perCoreConfig.QueueSize)
	}
	if !perCoreConfig.EnableAffinity {
		t.Error("PerCore should have EnableAffinity = true")
	}

	// 测试 DefaultPool 推荐配置
	defaultConfig := ModeDefaultPool.RecommendedConfig()
	if defaultConfig.EnableAffinity {
		t.Error("DefaultPool should not have EnableAffinity")
	}
}
