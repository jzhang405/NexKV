// Package runtime 提供 Porcupine 运行时验证器
// 本文件提供测试辅助函数，消除重复的测试代码
package runtime

import (
	"testing"
	"time"
)

// ==================== 测试辅助函数 ====================

// syncTestConfig 创建同步模式测试配置
func syncTestConfig() VerifierConfig {
	config := DefaultVerifierConfig()
	config.Enabled = true
	config.AsyncConfig.Enabled = false
	return config
}

// testVerifierWithSync 创建带同步配置的验证器
func testVerifierWithSync(nodeID string) *RuntimeVerifier {
	return NewRuntimeVerifier(syncTestConfig(), nodeID)
}

// ==================== 通用断言辅助函数 ====================

// assertVerifierCreated 验证验证器创建成功
func assertVerifierCreated(t *testing.T, verifier *RuntimeVerifier) {
	if verifier == nil {
		t.Fatal("Expected non-nil RuntimeVerifier")
	}
	if verifier.Recorder() == nil {
		t.Error("Expected non-nil recorder")
	}
}

// assertHookNotNil 验证 Hook 非空
func assertHookNotNil(t *testing.T, hook any, name string) {
	if hook == nil {
		t.Errorf("Expected non-nil %s", name)
	}
}

// assertResultNotNil 验证结果非空
func assertResultNotNil(t *testing.T, result *VerificationResult) {
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
}

// assertHistoryLength 验证历史记录长度
func assertHistoryLength(t *testing.T, history []VerificationResult, expected int) {
	if len(history) != expected {
		t.Errorf("Expected %d results, got %d", expected, len(history))
	}
}

// ==================== 延迟等待辅助 ====================

// waitForIntervalVerification 等待周期验证
func waitForIntervalVerification() {
	time.Sleep(100 * time.Millisecond)
}

// ==================== 配置构建辅助 ====================

// configWithHistorySize 创建带指定历史大小的配置
func configWithHistorySize(size int) VerifierConfig {
	config := DefaultVerifierConfig()
	config.Enabled = true
	config.HistorySize = size
	config.AsyncConfig.Enabled = false
	return config
}

// configWithInterval 创建带指定验证间隔的配置
func configWithInterval(interval time.Duration) VerifierConfig {
	config := DefaultVerifierConfig()
	config.Enabled = true
	config.VerifyInterval = interval
	return config
}

// configWithMaxOps 创建带指定最大操作数的配置
func configWithMaxOps(maxOps int) VerifierConfig {
	config := DefaultVerifierConfig()
	config.Enabled = true
	config.MaxOpsPerRecorder = maxOps
	config.AsyncConfig.Enabled = false
	return config
}

// disabledConfig 创建禁用的配置
func disabledConfig() VerifierConfig {
	config := DefaultVerifierConfig()
	config.Enabled = false
	return config
}
