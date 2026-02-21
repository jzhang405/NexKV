package framework

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/pkg/errors"
)

// ScenarioResult 场景执行结果
type ScenarioResult struct {
	// Name 场景名称
	Name string
	// Passed 是否通过
	Passed bool
	// Duration 执行时长
	Duration time.Duration
	// Error 错误信息（如果失败）
	Error error
	// SetupDuration Setup 阶段时长
	SetupDuration time.Duration
	// ExecuteDuration Execute 阶段时长
	ExecuteDuration time.Duration
	// VerifyDuration Verify 阶段时长
	VerifyDuration time.Duration
	// TeardownDuration Teardown 阶段时长
	TeardownDuration time.Duration
}

// TestScenario 测试场景接口
type TestScenario interface {
	// Name 返回场景名称
	Name() string

	// Setup 设置测试环境
	// 在 Execute 之前调用，用于准备测试数据和环境
	Setup(ctx context.Context, cluster TestCluster) error

	// Execute 执行测试操作
	// 主要的测试逻辑
	Execute(ctx context.Context, cluster TestCluster) error

	// Verify 验证测试结果
	// 检查测试操作是否达到预期效果
	Verify(ctx context.Context, cluster TestCluster) error

	// Teardown 清理测试环境
	// 在测试完成后调用，用于清理资源
	Teardown(ctx context.Context, cluster TestCluster) error
}

// ScenarioExecutor 场景执行器
type ScenarioExecutor struct {
	cluster TestCluster
}

// NewScenarioExecutor 创建场景执行器
func NewScenarioExecutor(cluster TestCluster) *ScenarioExecutor {
	return &ScenarioExecutor{
		cluster: cluster,
	}
}

// Execute 执行测试场景
func (e *ScenarioExecutor) Execute(ctx context.Context, scenario TestScenario) *ScenarioResult {
	result := &ScenarioResult{
		Name:   scenario.Name(),
		Passed: false,
	}

	startTime := time.Now()
	defer func() {
		result.Duration = time.Since(startTime)
	}()

	// Phase 1: Setup
	setupStart := time.Now()
	if err := scenario.Setup(ctx, e.cluster); err != nil {
		result.Error = errors.Wrap(err, "setup failed")
		result.SetupDuration = time.Since(setupStart)
		// 即使 Setup 失败也尝试 Teardown
		_ = scenario.Teardown(ctx, e.cluster)
		return result
	}
	result.SetupDuration = time.Since(setupStart)

	// Phase 2: Execute
	executeStart := time.Now()
	if err := scenario.Execute(ctx, e.cluster); err != nil {
		result.Error = errors.Wrap(err, "execute failed")
		result.ExecuteDuration = time.Since(executeStart)
		_ = scenario.Teardown(ctx, e.cluster)
		return result
	}
	result.ExecuteDuration = time.Since(executeStart)

	// Phase 3: Verify
	verifyStart := time.Now()
	if err := scenario.Verify(ctx, e.cluster); err != nil {
		result.Error = errors.Wrap(err, "verify failed")
		result.VerifyDuration = time.Since(verifyStart)
		_ = scenario.Teardown(ctx, e.cluster)
		return result
	}
	result.VerifyDuration = time.Since(verifyStart)

	// Phase 4: Teardown
	teardownStart := time.Now()
	if err := scenario.Teardown(ctx, e.cluster); err != nil {
		result.Error = errors.Wrap(err, "teardown failed")
		result.TeardownDuration = time.Since(teardownStart)
		return result
	}
	result.TeardownDuration = time.Since(teardownStart)

	result.Passed = true
	return result
}

// ExecuteAll 执行多个测试场景
func (e *ScenarioExecutor) ExecuteAll(ctx context.Context, scenarios []TestScenario) []*ScenarioResult {
	results := make([]*ScenarioResult, 0, len(scenarios))

	for _, scenario := range scenarios {
		select {
		case <-ctx.Done():
			// 上下文取消，返回已完成的results
			return results
		default:
		}

		result := e.Execute(ctx, scenario)
		results = append(results, result)
	}

	return results
}

// BaseScenario 提供TestScenario 接口的默认实现
type BaseScenario struct {
	name string
}

// NewBaseScenario 创建基础场景
func NewBaseScenario(name string) *BaseScenario {
	return &BaseScenario{name: name}
}

// Name 返回场景名称
func (s *BaseScenario) Name() string {
	return s.name
}

// Setup 默认设置实现（空操作）
func (s *BaseScenario) Setup(ctx context.Context, cluster TestCluster) error {
	return nil
}

// Execute 默认执行实现（空操作）
func (s *BaseScenario) Execute(ctx context.Context, cluster TestCluster) error {
	return nil
}

// Verify 默认验证实现（总是成功）
func (s *BaseScenario) Verify(ctx context.Context, cluster TestCluster) error {
	return nil
}

// Teardown 默认清理实现（空操作）
func (s *BaseScenario) Teardown(ctx context.Context, cluster TestCluster) error {
	return nil
}
