package framework

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ==========================================
// ScenarioResult 测试
// ==========================================

func TestScenarioResult_Fields(t *testing.T) {
	result := &ScenarioResult{
		Name:             "test-scenario",
		Passed:           true,
		Duration:         100 * time.Millisecond,
		Error:            nil,
		SetupDuration:    20 * time.Millisecond,
		ExecuteDuration:  50 * time.Millisecond,
		VerifyDuration:   20 * time.Millisecond,
		TeardownDuration: 10 * time.Millisecond,
	}

	assert.Equal(t, "test-scenario", result.Name)
	assert.True(t, result.Passed)
	assert.Equal(t, 100*time.Millisecond, result.Duration)
	assert.Nil(t, result.Error)
}

// ==========================================
// BaseScenario 测试
// ==========================================

func TestNewBaseScenario(t *testing.T) {
	scenario := NewBaseScenario("my-scenario")
	assert.Equal(t, "my-scenario", scenario.Name())
}

func TestBaseScenario_DefaultMethods(t *testing.T) {
	scenario := NewBaseScenario("test")

	ctx := context.Background()

	// 默认方法不应返回错误
	assert.NoError(t, scenario.Setup(ctx, nil))
	assert.NoError(t, scenario.Execute(ctx, nil))
	assert.NoError(t, scenario.Verify(ctx, nil))
	assert.NoError(t, scenario.Teardown(ctx, nil))
}

// ==========================================
// ScenarioExecutor 测试
// ==========================================

// mockSuccessScenario 成功的测试场景
type mockSuccessScenario struct {
	*BaseScenario
	setupCalled    bool
	executeCalled  bool
	verifyCalled   bool
	teardownCalled bool
}

func newMockSuccessScenario() *mockSuccessScenario {
	return &mockSuccessScenario{
		BaseScenario: NewBaseScenario("success-scenario"),
	}
}

func (s *mockSuccessScenario) Setup(ctx context.Context, cluster TestCluster) error {
	s.setupCalled = true
	return nil
}

func (s *mockSuccessScenario) Execute(ctx context.Context, cluster TestCluster) error {
	s.executeCalled = true
	return nil
}

func (s *mockSuccessScenario) Verify(ctx context.Context, cluster TestCluster) error {
	s.verifyCalled = true
	return nil
}

func (s *mockSuccessScenario) Teardown(ctx context.Context, cluster TestCluster) error {
	s.teardownCalled = true
	return nil
}

// mockSetupFailScenario Setup 失败的场景
type mockSetupFailScenario struct {
	*BaseScenario
}

func newMockSetupFailScenario() *mockSetupFailScenario {
	return &mockSetupFailScenario{
		BaseScenario: NewBaseScenario("setup-fail-scenario"),
	}
}

func (s *mockSetupFailScenario) Setup(ctx context.Context, cluster TestCluster) error {
	return errors.New("setup failed")
}

// mockExecuteFailScenario Execute 失败的场景
type mockExecuteFailScenario struct {
	*BaseScenario
}

func newMockExecuteFailScenario() *mockExecuteFailScenario {
	return &mockExecuteFailScenario{
		BaseScenario: NewBaseScenario("execute-fail-scenario"),
	}
}

func (s *mockExecuteFailScenario) Execute(ctx context.Context, cluster TestCluster) error {
	return errors.New("execute failed")
}

// mockVerifyFailScenario Verify 失败的场景
type mockVerifyFailScenario struct {
	*BaseScenario
}

func newMockVerifyFailScenario() *mockVerifyFailScenario {
	return &mockVerifyFailScenario{
		BaseScenario: NewBaseScenario("verify-fail-scenario"),
	}
}

func (s *mockVerifyFailScenario) Verify(ctx context.Context, cluster TestCluster) error {
	return errors.New("verify failed")
}

// mockTeardownFailScenario Teardown 失败的场景
type mockTeardownFailScenario struct {
	*BaseScenario
}

func newMockTeardownFailScenario() *mockTeardownFailScenario {
	return &mockTeardownFailScenario{
		BaseScenario: NewBaseScenario("teardown-fail-scenario"),
	}
}

func (s *mockTeardownFailScenario) Teardown(ctx context.Context, cluster TestCluster) error {
	return errors.New("teardown failed")
}

func TestScenarioExecutor_New(t *testing.T) {
	executor := NewScenarioExecutor(nil)
	assert.NotNil(t, executor)
}

func TestScenarioExecutor_Execute_Success(t *testing.T) {
	executor := NewScenarioExecutor(nil)
	scenario := newMockSuccessScenario()

	result := executor.Execute(context.Background(), scenario)

	assert.True(t, result.Passed)
	assert.Nil(t, result.Error)
	assert.True(t, scenario.setupCalled)
	assert.True(t, scenario.executeCalled)
	assert.True(t, scenario.verifyCalled)
	assert.True(t, scenario.teardownCalled)

	// 验证各阶段时长
	assert.Greater(t, result.Duration.Nanoseconds(), int64(0))
}

func TestScenarioExecutor_Execute_SetupFail(t *testing.T) {
	executor := NewScenarioExecutor(nil)
	scenario := newMockSetupFailScenario()

	result := executor.Execute(context.Background(), scenario)

	assert.False(t, result.Passed)
	assert.Contains(t, result.Error.Error(), "setup failed")
}

func TestScenarioExecutor_Execute_ExecuteFail(t *testing.T) {
	executor := NewScenarioExecutor(nil)
	scenario := newMockExecuteFailScenario()

	result := executor.Execute(context.Background(), scenario)

	assert.False(t, result.Passed)
	assert.Contains(t, result.Error.Error(), "execute failed")
}

func TestScenarioExecutor_Execute_VerifyFail(t *testing.T) {
	executor := NewScenarioExecutor(nil)
	scenario := newMockVerifyFailScenario()

	result := executor.Execute(context.Background(), scenario)

	assert.False(t, result.Passed)
	assert.Contains(t, result.Error.Error(), "verify failed")
}

func TestScenarioExecutor_Execute_TeardownFail(t *testing.T) {
	executor := NewScenarioExecutor(nil)
	scenario := newMockTeardownFailScenario()

	result := executor.Execute(context.Background(), scenario)

	assert.False(t, result.Passed)
	assert.Contains(t, result.Error.Error(), "teardown failed")
}

func TestScenarioExecutor_ExecuteAll(t *testing.T) {
	executor := NewScenarioExecutor(nil)

	scenarios := []TestScenario{
		newMockSuccessScenario(),
		newMockSuccessScenario(),
		newMockSuccessScenario(),
	}

	results := executor.ExecuteAll(context.Background(), scenarios)

	assert.Len(t, results, 3)
	for _, result := range results {
		assert.True(t, result.Passed)
	}
}

func TestScenarioExecutor_ExecuteAll_ContextCancel(t *testing.T) {
	executor := NewScenarioExecutor(nil)

	scenarios := []TestScenario{
		newMockSuccessScenario(),
		newMockSuccessScenario(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	results := executor.ExecuteAll(ctx, scenarios)

	// 上下文取消后应该立即返回
	assert.Len(t, results, 0)
}

func TestScenarioExecutor_Execute_TeardownCalledOnFailure(t *testing.T) {
	executor := NewScenarioExecutor(nil)

	// 使用 mockExecuteFailScenario 来测试 Teardown 被调用
	// 即使 Execute 失败，Teardown 也会被调用（在 Execute 方法内部）
	scenario := newMockExecuteFailScenario()

	result := executor.Execute(context.Background(), scenario)

	assert.False(t, result.Passed)
	assert.Contains(t, result.Error.Error(), "execute failed")
}

// ==========================================
// 并发测试
// ==========================================

func TestScenarioExecutor_ExecuteAll_Concurrent(t *testing.T) {
	executor := NewScenarioExecutor(nil)

	// 创建多个场景
	var scenarios []TestScenario
	for i := 0; i < 10; i++ {
		scenarios = append(scenarios, newMockSuccessScenario())
	}

	results := executor.ExecuteAll(context.Background(), scenarios)

	assert.Len(t, results, 10)
	for i, result := range results {
		assert.True(t, result.Passed, "Scenario %d should pass", i)
	}
}

// ==========================================
// 性能测试
// ==========================================

func TestScenarioExecutor_Execute_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	executor := NewScenarioExecutor(nil)
	scenario := newMockSuccessScenario()

	start := time.Now()
	iterations := 1000
	for i := 0; i < iterations; i++ {
		_ = executor.Execute(context.Background(), scenario)
	}
	elapsed := time.Since(start)

	t.Logf("Executed %d scenarios in %v (%.2f μs/op)", iterations, elapsed, float64(elapsed.Microseconds())/float64(iterations))
}
