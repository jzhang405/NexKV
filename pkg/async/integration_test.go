// Package async 提供异步操作集成测试
package async

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/concurrency"
)

// ==========================================
// 集成测试：模拟 5 节点集群场景
// ==========================================

// mockNode 模拟集群节点
type mockNode struct {
	id       model.PeerID
	latency  time.Duration
	healthy  atomic.Bool
	response string
}

// mockCluster 模拟 5 节点集群
type mockCluster struct {
	nodes    []*mockNode
	provider *concurrency.AntsGoroutineProvider
}

func newMockCluster() *mockCluster {
	nodes := make([]*mockNode, 5)
	for i := 0; i < 5; i++ {
		nodes[i] = &mockNode{
			id:       model.PeerID(fmt.Sprintf("node-%d", i+1)),
			latency:  time.Duration(10+i*5) * time.Millisecond,
			response: fmt.Sprintf("response-from-node-%d", i+1),
		}
		nodes[i].healthy.Store(true)
	}

	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 100,
	})

	return &mockCluster{
		nodes:    nodes,
		provider: provider,
	}
}

func (c *mockCluster) close() {
	if c.provider != nil {
		_ = c.provider.Close()
	}
}

func (c *mockCluster) sendToNode(ctx context.Context, target model.PeerID) (string, error) {
	for _, node := range c.nodes {
		if node.id == target {
			if !node.healthy.Load() {
				return "", errors.New("node unhealthy")
			}
			// 模拟网络延迟
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(node.latency):
				return node.response, nil
			}
		}
	}
	return "", errors.New("node not found")
}

// ==========================================
// 场景 1: Quorum 写入
// ==========================================

// TestIntegration_QuorumWrite 测试 Quorum 写入场景
func TestIntegration_QuorumWrite(t *testing.T) {
	cluster := newMockCluster()
	defer cluster.close()

	ctx := context.Background()
	targets := make([]model.PeerID, 5)
	for i, node := range cluster.nodes {
		targets[i] = node.id
	}

	// 使用 AsyncGroup 发送 Quorum 写入
	group := NewGroup(ctx, cluster.provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return cluster.sendToNode(ctx, target)
	})

	// 等待多数派（3/5）
	result := group.WaitMajority(ctx)

	// 验证：应该有至少 3 个成功
	if len(result.SuccessPeers) < 3 {
		t.Fatalf("expected at least 3 success peers for quorum, got: %d", len(result.SuccessPeers))
	}

	t.Logf("Quorum write: %d/%d nodes responded", len(result.SuccessPeers), len(targets))
}

// ==========================================
// 场景 2: 节点故障优雅降级
// ==========================================

// TestIntegration_NodeFailure 测试节点故障时的优雅降级
func TestIntegration_NodeFailure(t *testing.T) {
	cluster := newMockCluster()
	defer cluster.close()

	// 模拟 2 个节点故障
	cluster.nodes[3].healthy.Store(false)
	cluster.nodes[4].healthy.Store(false)

	ctx := context.Background()
	targets := make([]model.PeerID, 5)
	for i, node := range cluster.nodes {
		targets[i] = node.id
	}

	group := NewGroup(ctx, cluster.provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return cluster.sendToNode(ctx, target)
	})

	result := group.WaitAll(ctx)

	// 验证：3 个成功，2 个失败
	if len(result.SuccessPeers) != 3 {
		t.Fatalf("expected 3 success peers, got: %d", len(result.SuccessPeers))
	}
	if len(result.FailedPeers) != 2 {
		t.Fatalf("expected 2 failed peers, got: %d", len(result.FailedPeers))
	}

	t.Logf("Graceful degradation: %d success, %d failed", len(result.SuccessPeers), len(result.FailedPeers))
}

// ==========================================
// 场景 3: 网络分区恢复
// ==========================================

// TestIntegration_NetworkPartition 测试网络分区恢复后的状态一致性
func TestIntegration_NetworkPartition(t *testing.T) {
	cluster := newMockCluster()
	defer cluster.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	targets := make([]model.PeerID, 5)
	for i, node := range cluster.nodes {
		targets[i] = node.id
	}

	// 第一次调用：所有节点正常
	group1 := NewGroup(ctx, cluster.provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return cluster.sendToNode(ctx, target)
	})
	result1 := group1.WaitAll(ctx)

	// 模拟网络分区
	cluster.nodes[0].healthy.Store(false)
	cluster.nodes[1].healthy.Store(false)

	// 第二次调用：分区中
	group2 := NewGroup(ctx, cluster.provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return cluster.sendToNode(ctx, target)
	})
	result2 := group2.WaitAll(ctx)

	// 模拟网络恢复
	cluster.nodes[0].healthy.Store(true)
	cluster.nodes[1].healthy.Store(true)

	// 第三次调用：恢复后
	group3 := NewGroup(ctx, cluster.provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return cluster.sendToNode(ctx, target)
	})
	result3 := group3.WaitAll(ctx)

	// 验证
	if len(result1.SuccessPeers) != 5 {
		t.Errorf("before partition: expected 5 success, got %d", len(result1.SuccessPeers))
	}
	if len(result2.SuccessPeers) != 3 {
		t.Errorf("during partition: expected 3 success, got %d", len(result2.SuccessPeers))
	}
	if len(result3.SuccessPeers) != 5 {
		t.Errorf("after recovery: expected 5 success, got %d", len(result3.SuccessPeers))
	}

	t.Logf("Network partition test passed: before=%d, during=%d, after=%d",
		len(result1.SuccessPeers), len(result2.SuccessPeers), len(result3.SuccessPeers))
}

// ==========================================
// 场景 4: 高并发协程池行为
// ==========================================

// TestIntegration_HighConcurrency 测试高并发场景下的协程池行为
func TestIntegration_HighConcurrency(t *testing.T) {
	cluster := newMockCluster()
	defer cluster.close()

	ctx := context.Background()
	const concurrentOps = 1000

	var wg sync.WaitGroup
	var successCount atomic.Int64
	var failureCount atomic.Int64

	// 启动 1000 个并发操作
	for i := 0; i < concurrentOps; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			target := cluster.nodes[idx%5].id
			op := NewOp(ctx, cluster.provider, func(ctx context.Context) (string, error) {
				return cluster.sendToNode(ctx, target)
			})

			_, err := op.Get(ctx)
			if err != nil {
				failureCount.Add(1)
			} else {
				successCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	// 验证所有操作都完成
	total := successCount.Load() + failureCount.Load()
	if total != concurrentOps {
		t.Fatalf("expected %d total operations, got %d", concurrentOps, total)
	}

	// 验证协程池状态
	stats := cluster.provider.Stats()
	t.Logf("High concurrency test: %d success, %d failed", successCount.Load(), failureCount.Load())
	t.Logf("Pool stats: running=%d, waiting=%d", stats.Running, stats.Waiting)
}

// ==========================================
// 场景 5: 定时任务集群调度
// ==========================================

// TestIntegration_ScheduledTask 测试定时任务在集群环境下的调度
func TestIntegration_ScheduledTask(t *testing.T) {
	cluster := newMockCluster()
	defer cluster.close()

	// 创建 CronJobProvider
	cronProvider := concurrency.NewRobfigCronProvider(cluster.provider)
	defer cronProvider.Stop()

	var executionCount atomic.Int64

	// 注册定时任务 - 使用标准 cron 表达式：每秒执行一次
	_, err := cronProvider.Register("*/1 * * * * *", "test-task", func(ctx context.Context) {
		executionCount.Add(1)

		// 模拟向所有节点发送心跳
		targets := make([]model.PeerID, 5)
		for i, node := range cluster.nodes {
			targets[i] = node.id
		}

		group := NewGroup(ctx, cluster.provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			return cluster.sendToNode(ctx, target)
		})
		_ = group.WaitAll(ctx)
	})

	if err != nil {
		t.Fatalf("failed to register cron job: %v", err)
	}

	// 启动调度器
	cronProvider.Start()

	// 等待任务执行（3秒）
	time.Sleep(3 * time.Second)

	// 验证任务被多次执行
	count := executionCount.Load()
	if count < 2 {
		t.Errorf("expected at least 2 executions, got %d", count)
	}

	t.Logf("Scheduled task executed %d times in 3 seconds", count)
}

// ==========================================
// 场景 6: 优雅关闭任务不丢失
// ==========================================

// TestIntegration_GracefulShutdown 测试优雅关闭时任务不丢失
func TestIntegration_GracefulShutdown(t *testing.T) {
	cluster := newMockCluster()
	defer cluster.close()

	ctx := context.Background()
	targets := make([]model.PeerID, 5)
	for i, node := range cluster.nodes {
		targets[i] = node.id
	}

	// 创建多个 AsyncGroup
	groups := make([]*AsyncGroup[string], 3)
	for i := 0; i < 3; i++ {
		groups[i] = NewGroup(ctx, cluster.provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			return cluster.sendToNode(ctx, target)
		})
	}

	// 等待所有操作完成
	var totalSuccess int
	for _, group := range groups {
		result := group.WaitAll(ctx)
		totalSuccess += len(result.SuccessPeers)

		// 优雅关闭
		_ = group.Close()
	}

	// 验证所有任务都成功完成
	expectedTotal := 3 * 5 // 3 groups × 5 nodes
	if totalSuccess != expectedTotal {
		t.Errorf("expected %d total success, got %d", expectedTotal, totalSuccess)
	}

	t.Logf("Graceful shutdown: all %d tasks completed successfully", totalSuccess)
}
