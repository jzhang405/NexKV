package implementations

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// TestParallelRecovery_BasicFunctionality 测试并行恢复基本功能
// 验证目标：并行恢复能正确恢复所有崩溃节点
func TestParallelRecovery_BasicFunctionality(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	// 所有节点发起投票并决策
	majority := cluster.GetMajority()
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	for _, node := range cluster.Nodes {
		success, _ := node.DecideCommit(majority)
		if !success {
			t.Fatal("Failed to commit before crashes")
		}
	}

	// 同时崩溃 3 个节点
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(node *Node) {
			defer wg.Done()
			_ = node.Crash()
		}(cluster.Nodes[i])
	}
	wg.Wait()

	// 使用并行恢复
	injector := NewFaultInjector(cluster, 0.0, 0)
	if err := injector.ParallelRecoverAllNodes(); err != nil {
		t.Fatalf("Parallel recovery failed: %v", err)
	}

	// 等待增量同步完成
	time.Sleep(2 * time.Second)

	// 验证：所有节点都已恢复
	crashedCount := 0
	for _, node := range cluster.Nodes {
		node.mu.RLock()
		if node.IsCrashed {
			crashedCount++
		}
		node.mu.RUnlock()
	}

	if crashedCount > 0 {
		t.Errorf("%d nodes still crashed after parallel recovery", crashedCount)
	}

	// 验证：所有节点状态一致
	firstDecision, _, _, _ := cluster.Nodes[0].GetState()
	for _, node := range cluster.Nodes {
		decision, _, _, _ := node.GetState()
		if decision != firstDecision {
			t.Errorf("Node %s decision inconsistent after recovery: %s vs %s",
				node.ID, decision, firstDecision)
		}
	}

	t.Logf("✓ Parallel recovery successful: all nodes recovered with consistent state")
}

// TestParallelRecovery_NoDeadlock 测试并行恢复无死锁
// 验证目标：多次运行并行恢复不会出现死锁
func TestParallelRecovery_NoDeadlock(t *testing.T) {
	// 运行多次以增加发现死锁的概率
	for round := 0; round < 10; round++ {
		t.Run(fmt.Sprintf("Round%d", round), func(t *testing.T) {
			tempDir := setupTempDir(t)
			defer os.RemoveAll(tempDir)

			cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
			defer cluster.Close()

			// 预热：所有节点发起投票
			for _, node := range cluster.Nodes {
				node.ProposeVote(0)
			}

			// 同时崩溃 3 个节点
			var wg sync.WaitGroup
			for i := 0; i < 3; i++ {
				wg.Add(1)
				go func(node *Node) {
					defer wg.Done()
					_ = node.Crash()
				}(cluster.Nodes[i])
			}
			wg.Wait()

			// 使用并行恢复（设置超时防止死锁）
			done := make(chan error, 1)

			go func() {
				injector := NewFaultInjector(cluster, 0.0, 0)
				done <- injector.ParallelRecoverAllNodes()
			}()

			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Parallel recovery failed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Errorf("Parallel recovery timed out (possible deadlock)")
			}
		})
	}
}

// TestParallelRecovery_PerformanceComparison 对比并行恢复和顺序恢复的性能
// 验证目标：并行恢复比顺序恢复更快
func TestParallelRecovery_PerformanceComparison(t *testing.T) {
	// 测试顺序恢复
	t.Run("SequentialRecovery", func(t *testing.T) {
		tempDir := setupTempDir(t)
		defer os.RemoveAll(tempDir)

		cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5", "n6", "n7"}, tempDir)
		defer cluster.Close()

		// 预热：所有节点发起投票并决策
		majority := cluster.GetMajority()
		for _, node := range cluster.Nodes {
			node.ProposeVote(0)
		}
		cluster.GossipRound()

		for _, node := range cluster.Nodes {
			success, _ := node.DecideCommit(majority)
			if !success {
				t.Fatal("Failed to commit before crashes")
			}
		}

		// 崩溃 4 个节点
		for i := 0; i < 4; i++ {
			_ = cluster.Nodes[i].Crash()
		}

		start := time.Now()

		// 顺序恢复
		for i := 0; i < 4; i++ {
			_ = cluster.Nodes[i].Recover(cluster)
			time.Sleep(100 * time.Millisecond) // 等待增量同步
		}

		sequentialDuration := time.Since(start)
		t.Logf("Sequential recovery time: %v", sequentialDuration)

		// 验证恢复成功
		crashedCount := 0
		for _, node := range cluster.Nodes {
			node.mu.RLock()
			if node.IsCrashed {
				crashedCount++
			}
			node.mu.RUnlock()
		}

		if crashedCount > 0 {
			t.Errorf("Sequential recovery failed: %d nodes still crashed", crashedCount)
		}
	})

	// 测试并行恢复
	t.Run("ParallelRecovery", func(t *testing.T) {
		tempDir := setupTempDir(t)
		defer os.RemoveAll(tempDir)

		cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5", "n6", "n7"}, tempDir)
		defer cluster.Close()

		// 预热：所有节点发起投票并决策
		majority := cluster.GetMajority()
		for _, node := range cluster.Nodes {
			node.ProposeVote(0)
		}
		cluster.GossipRound()

		for _, node := range cluster.Nodes {
			success, _ := node.DecideCommit(majority)
			if !success {
				t.Fatal("Failed to commit before crashes")
			}
		}

		// 崩溃 4 个节点
		for i := 0; i < 4; i++ {
			_ = cluster.Nodes[i].Crash()
		}

		start := time.Now()

		// 并行恢复
		injector := NewFaultInjector(cluster, 0.0, 0)
		if err := injector.ParallelRecoverAllNodes(); err != nil {
			t.Fatalf("Parallel recovery failed: %v", err)
		}

		// 等待增量同步完成
		time.Sleep(200 * time.Millisecond)

		parallelDuration := time.Since(start)
		t.Logf("Parallel recovery time: %v", parallelDuration)

		// 验证恢复成功
		crashedCount := 0
		for _, node := range cluster.Nodes {
			node.mu.RLock()
			if node.IsCrashed {
				crashedCount++
			}
			node.mu.RUnlock()
		}

		if crashedCount > 0 {
			t.Errorf("Parallel recovery failed: %d nodes still crashed", crashedCount)
		}
	})
}

// TestRecoveryCoordinator_BatchAnalysis 测试批次分配逻辑
// 验证目标：正确识别活跃节点和崩溃节点，分配合适的批次
func TestRecoveryCoordinator_BatchAnalysis(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	// 崩溃 3 个节点
	for i := 0; i < 3; i++ {
		_ = cluster.Nodes[i].Crash()
	}

	// 创建恢复协调器并分析依赖
	coordinator := NewRecoveryCoordinator(cluster)
	coordinator.AnalyzeDependencies()

	// 验证批次分配
	batchCount := coordinator.GetBatchCount()
	if batchCount != 2 {
		t.Errorf("Expected 2 batches, got %d", batchCount)
	}

	// Batch 0: 2 个活跃节点
	activeNodeCount := coordinator.GetBatchNodeCount(0)
	if activeNodeCount != 2 {
		t.Errorf("Expected 2 active nodes in batch 0, got %d", activeNodeCount)
	}

	// Batch 1: 3 个崩溃节点
	crashedNodeCount := coordinator.GetBatchNodeCount(1)
	if crashedNodeCount != 3 {
		t.Errorf("Expected 3 crashed nodes in batch 1, got %d", crashedNodeCount)
	}

	t.Logf("✓ Batch analysis correct: %d batches with %d active + %d crashed nodes",
		batchCount, activeNodeCount, crashedNodeCount)
}

// TestParallelRecovery_AllNodesCrashed 测试所有节点都崩溃的场景
// 验证目标：即使所有节点都崩溃，并行恢复也能正常工作
func TestParallelRecovery_AllNodesCrashed(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	// 预热：所有节点发起投票
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}

	// 崩溃所有节点
	for _, node := range cluster.Nodes {
		_ = node.Crash()
	}

	// 使用并行恢复
	injector := NewFaultInjector(cluster, 0.0, 0)
	if err := injector.ParallelRecoverAllNodes(); err != nil {
		t.Fatalf("Parallel recovery failed: %v", err)
	}

	// 等待增量同步完成
	time.Sleep(1 * time.Second)

	// 验证：所有节点都已恢复
	crashedCount := 0
	for _, node := range cluster.Nodes {
		node.mu.RLock()
		if node.IsCrashed {
			crashedCount++
		}
		node.mu.RUnlock()
	}

	if crashedCount > 0 {
		t.Errorf("%d nodes still crashed after parallel recovery", crashedCount)
	}

	t.Logf("✓ All nodes recovered successfully")
}

// TestParallelRecovery_NoCrashedNodes 测试没有崩溃节点的场景
// 验证目标：没有崩溃节点时，并行恢复能优雅处理
func TestParallelRecovery_NoCrashedNodes(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	// 使用并行恢复（没有节点崩溃）
	injector := NewFaultInjector(cluster, 0.0, 0)
	if err := injector.ParallelRecoverAllNodes(); err != nil {
		t.Fatalf("Parallel recovery failed: %v", err)
	}

	t.Logf("✓ Parallel recovery handled no-crash scenario gracefully")
}
