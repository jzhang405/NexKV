// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件提供测试辅助函数，消除重复的测试代码
package hooks

import (
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

// ==================== 测试辅助函数 ====================

// newTestRecorder 创建测试用的 EnhancedHistoryRecorder
func newTestRecorder() *porcupine.EnhancedHistoryRecorder {
	return porcupine.NewEnhancedHistoryRecorder("test", porcupine.NewMonotonicTimestamp())
}

// syncTestConfig 创建同步模式测试配置
func syncTestConfig() porcupine.AsyncRecordConfig {
	return porcupine.AsyncRecordConfig{
		Enabled:    false, // 同步模式便于测试
		BufferSize: 10,
		DropOnFull: true,
	}
}

// asyncTestConfig 创建异步模式测试配置
func asyncTestConfig() porcupine.AsyncRecordConfig {
	return porcupine.AsyncRecordConfig{
		Enabled:    true,
		BufferSize: 10,
		DropOnFull: true,
	}
}

// ==================== Hook 通用测试辅助 ====================

// HookTestContext 封装 Hook 测试的通用上下文
type HookTestContext struct {
	Recorder *porcupine.EnhancedHistoryRecorder
	Config   porcupine.AsyncRecordConfig
	T        *testing.T
}

// NewHookTestContext 创建 Hook 测试上下文
func NewHookTestContext(t *testing.T) *HookTestContext {
	return &HookTestContext{
		Recorder: newTestRecorder(),
		Config:   syncTestConfig(),
		T:        t,
	}
}

// WithAsyncConfig 设置异步配置
func (c *HookTestContext) WithAsyncConfig() *HookTestContext {
	c.Config = asyncTestConfig()
	return c
}

// ==================== 通用断言辅助函数 ====================

// assertHookCreated 验证 Hook 创建成功
func assertHookCreated(t *testing.T, hook interface{ Enabled() bool }, name string) {
	if hook == nil {
		t.Fatalf("Expected non-nil %s", name)
	}
	if hook.Enabled() {
		t.Errorf("Expected %s to be disabled by default", name)
	}
}

// assertEnabledState 验证 Hook 启用/禁用状态切换
func assertEnabledState(t *testing.T, hook interface {
	Enabled() bool
	SetEnabled(bool)
}) {
	// 默认禁用
	if hook.Enabled() {
		t.Error("Expected hook to be disabled by default")
	}

	// 启用
	hook.SetEnabled(true)
	if !hook.Enabled() {
		t.Error("Expected hook to be enabled")
	}

	// 再次禁用
	hook.SetEnabled(false)
	if hook.Enabled() {
		t.Error("Expected hook to be disabled")
	}
}

// assertDisabledHookReturnsMinus1 验证禁用的 Hook 返回 -1
func assertDisabledHookReturnsMinus1(t *testing.T, opID int) {
	if opID != -1 {
		t.Errorf("Expected opID=-1 when disabled, got %d", opID)
	}
}

// assertValidOpID 验证 OpID 有效
func assertValidOpID(t *testing.T, opID int) {
	if opID < 0 {
		t.Errorf("Expected valid opID, got %d", opID)
	}
}

// assertTotalRecorded 验证记录数量
func assertTotalRecorded(t *testing.T, stats HookStats, expected int64) {
	if stats.TotalRecorded != expected {
		t.Errorf("Expected TotalRecorded=%d, got %d", expected, stats.TotalRecorded)
	}
}

// ==================== 延迟等待辅助 ====================

// waitForAsync 等待异步操作入队
func waitForAsync() {
	time.Sleep(10 * time.Millisecond)
}

// waitForProcessing 等待处理完成
func waitForProcessing() {
	time.Sleep(50 * time.Millisecond)
}
