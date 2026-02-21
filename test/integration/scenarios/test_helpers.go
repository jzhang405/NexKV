// Package scenarios 提供集成测试场景实现
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/pkg/test/framework"
)

// setupIntegrationTest 创建通用的集成测试基础设施
// 返回：context、测试上下文、场景执行器
//
// 使用示例：
//
//	func TestIntegration_XXX(t *testing.T) {
//	    ctx, testCtx, executor := setupIntegrationTest(t, 30*time.Second)
//	    // 使用 ctx, testCtx, executor 进行测试
//	}
func setupIntegrationTest(t *testing.T, timeout time.Duration) (context.Context, *framework.TestContext, *framework.ScenarioExecutor) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)

	testCtx, err := framework.NewTestContext()
	if err != nil {
		t.Fatalf("Failed to create test context: %v", err)
	}
	t.Cleanup(func() { testCtx.Close() })

	cluster := framework.NewDefaultCluster("test-cluster", testCtx.Registry)
	executor := framework.NewScenarioExecutor(cluster)

	return ctx, testCtx, executor
}

// setupIntegrationTestWithoutExecutor 创建通用的集成测试基础设施（不带执行器）
// 适用于不需要使用 ScenarioExecutor 的测试场景
//
// 使用示例：
//
//	func TestIntegration_XXX(t *testing.T) {
//	    ctx, testCtx := setupIntegrationTestWithoutExecutor(t, 30*time.Second)
//	    // 使用 ctx, testCtx 进行测试
//	}
//
//nolint:unused // 保留为将来测试使用
func setupIntegrationTestWithoutExecutor(t *testing.T, timeout time.Duration) (context.Context, *framework.TestContext) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)

	testCtx, err := framework.NewTestContext()
	if err != nil {
		t.Fatalf("Failed to create test context: %v", err)
	}
	t.Cleanup(func() { testCtx.Close() })

	return ctx, testCtx
}
