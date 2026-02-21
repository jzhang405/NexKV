// Package scenarios 提供集成测试场景实现
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const defaultTestTimeout = 30 * time.Second

// TestSetupIntegrationTestWithoutExecutor_Basic 测试基本功能
func TestSetupIntegrationTestWithoutExecutor_Basic(t *testing.T) {
	ctx, testCtx := setupIntegrationTestWithoutExecutor(t, defaultTestTimeout)

	// 验证返回值
	assert.NotNil(t, ctx)
	assert.NotNil(t, testCtx)

	// 验证 context 有效
	deadline, ok := ctx.Deadline()
	assert.True(t, ok)
	assert.True(t, deadline.After(time.Now()))

	// 验证 Registry 已初始化
	assert.NotNil(t, testCtx.Registry)
}

// TestSetupIntegrationTestWithoutExecutor_Timeout 测试超时设置
func TestSetupIntegrationTestWithoutExecutor_Timeout(t *testing.T) {
	start := time.Now()
	ctx, _ := setupIntegrationTestWithoutExecutor(t, 5*time.Second)

	// 验证超时设置正确
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, start.Add(5*time.Second), deadline, 100*time.Millisecond)
}

// TestSetupIntegrationTestWithoutExecutor_ContextCancellation 测试 context 取消
func TestSetupIntegrationTestWithoutExecutor_ContextCancellation(t *testing.T) {
	ctx, _ := setupIntegrationTestWithoutExecutor(t, defaultTestTimeout)

	// context 应该是有效的（未被取消）
	select {
	case <-ctx.Done():
		t.Fatal("context should not be cancelled initially")
	default:
		// 预期行为
	}
}

// TestSetupIntegrationTestWithoutExecutor_TestContextRegistry 测试 Registry 功能
func TestSetupIntegrationTestWithoutExecutor_TestContextRegistry(t *testing.T) {
	_, testCtx := setupIntegrationTestWithoutExecutor(t, defaultTestTimeout)

	// 验证 Registry 可以正常使用
	require.NotNil(t, testCtx.Registry)
	require.NotNil(t, testCtx.GoroutinePool)
	require.NotEmpty(t, testCtx.TempDir)
}

// TestSetupIntegrationTestWithoutExecutor_MultipleCalls 测试多次调用
func TestSetupIntegrationTestWithoutExecutor_MultipleCalls(t *testing.T) {
	// 多次调用应该各自创建独立的 context
	ctx1, testCtx1 := setupIntegrationTestWithoutExecutor(t, 10*time.Second)
	ctx2, testCtx2 := setupIntegrationTestWithoutExecutor(t, 20*time.Second)

	// context 的 deadline 应该不同
	deadline1, _ := ctx1.Deadline()
	deadline2, _ := ctx2.Deadline()
	assert.True(t, deadline1.Before(deadline2))

	// testCtx 应该是不同的实例
	assert.NotSame(t, testCtx1, testCtx2)
}

// TestSetupIntegrationTest_Basic 测试 setupIntegrationTest 基本功能
func TestSetupIntegrationTest_Basic(t *testing.T) {
	ctx, testCtx, executor := setupIntegrationTest(t, defaultTestTimeout)

	// 验证返回值
	assert.NotNil(t, ctx)
	assert.NotNil(t, testCtx)
	assert.NotNil(t, executor)

	// 验证 context 有效
	deadline, ok := ctx.Deadline()
	assert.True(t, ok)
	assert.True(t, deadline.After(time.Now()))

	// 验证 Registry 已初始化
	assert.NotNil(t, testCtx.Registry)
}

// TestSetupIntegrationTest_ExecutorNotNil 测试执行器创建
func TestSetupIntegrationTest_ExecutorNotNil(t *testing.T) {
	_, _, executor := setupIntegrationTest(t, defaultTestTimeout)

	require.NotNil(t, executor)
}

// TestSetupIntegrationTest_ContextTimeout 测试 context 超时设置
func TestSetupIntegrationTest_ContextTimeout(t *testing.T) {
	start := time.Now()
	ctx, _, _ := setupIntegrationTest(t, 5*time.Second)

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, start.Add(5*time.Second), deadline, 100*time.Millisecond)
}

// TestSetupIntegrationTest_DifferentTimeouts 测试不同超时时间
func TestSetupIntegrationTest_DifferentTimeouts(t *testing.T) {
	shortTimeout := 5 * time.Second
	longTimeout := 30 * time.Second

	ctx1, _, _ := setupIntegrationTest(t, shortTimeout)
	ctx2, _, _ := setupIntegrationTest(t, longTimeout)

	deadline1, _ := ctx1.Deadline()
	deadline2, _ := ctx2.Deadline()

	assert.True(t, deadline1.Before(deadline2))
}

// TestSetupIntegrationTest_TestContextFields 测试 TestContext 字段
func TestSetupIntegrationTest_TestContextFields(t *testing.T) {
	_, testCtx, _ := setupIntegrationTest(t, defaultTestTimeout)

	// 验证 TestContext 已初始化
	require.NotNil(t, testCtx)
	require.NotNil(t, testCtx.Registry)
	require.NotNil(t, testCtx.GoroutinePool)
	require.NotEmpty(t, testCtx.TempDir)
}
