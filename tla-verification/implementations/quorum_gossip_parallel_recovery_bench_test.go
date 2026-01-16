package implementations

import (
	"os"
	"sync"
	"testing"
	"time"
)

// BenchmarkSequentialRecovery_3Nodes 基准测试：顺序恢复3节点
func BenchmarkSequentialRecovery_3Nodes(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-sequential-3-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	// 预热
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 崩溃 2 个节点
		for j := 0; j < 2; j++ {
			_ = cluster.Nodes[j].Crash()
		}

		// 顺序恢复
		for j := 0; j < 2; j++ {
			_ = cluster.Nodes[j].Recover(cluster)
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// BenchmarkParallelRecovery_3Nodes 基准测试：并行恢复3节点
func BenchmarkParallelRecovery_3Nodes(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-parallel-3-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	// 预热
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 崩溃 2 个节点
		for j := 0; j < 2; j++ {
			_ = cluster.Nodes[j].Crash()
		}

		// 并行恢复
		injector := NewFaultInjector(cluster, 0.0, 0)
		_ = injector.ParallelRecoverAllNodes()

		// 等待增量同步完成
		time.Sleep(200 * time.Millisecond)
	}
}

// BenchmarkSequentialRecovery_5Nodes 基准测试：顺序恢复5节点
func BenchmarkSequentialRecovery_5Nodes(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-sequential-5-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	// 预热
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 崩溃 3 个节点
		for j := 0; j < 3; j++ {
			_ = cluster.Nodes[j].Crash()
		}

		// 顺序恢复
		for j := 0; j < 3; j++ {
			_ = cluster.Nodes[j].Recover(cluster)
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// BenchmarkParallelRecovery_5Nodes 基准测试：并行恢复5节点
func BenchmarkParallelRecovery_5Nodes(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-parallel-5-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	// 预热
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 崩溃 3 个节点
		for j := 0; j < 3; j++ {
			_ = cluster.Nodes[j].Crash()
		}

		// 并行恢复
		injector := NewFaultInjector(cluster, 0.0, 0)
		_ = injector.ParallelRecoverAllNodes()

		// 等待增量同步完成
		time.Sleep(200 * time.Millisecond)
	}
}

// BenchmarkSequentialRecovery_7Nodes 基准测试：顺序恢复7节点
func BenchmarkSequentialRecovery_7Nodes(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-sequential-7-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5", "n6", "n7"}, tempDir)
	defer cluster.Close()

	// 预热
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 崩溃 4 个节点
		for j := 0; j < 4; j++ {
			_ = cluster.Nodes[j].Crash()
		}

		// 顺序恢复
		for j := 0; j < 4; j++ {
			_ = cluster.Nodes[j].Recover(cluster)
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// BenchmarkParallelRecovery_7Nodes 基准测试：并行恢复7节点
func BenchmarkParallelRecovery_7Nodes(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-parallel-7-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5", "n6", "n7"}, tempDir)
	defer cluster.Close()

	// 预热
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 崩溃 4 个节点
		for j := 0; j < 4; j++ {
			_ = cluster.Nodes[j].Crash()
		}

		// 并行恢复
		injector := NewFaultInjector(cluster, 0.0, 0)
		_ = injector.ParallelRecoverAllNodes()

		// 等待增量同步完成
		time.Sleep(200 * time.Millisecond)
	}
}

// BenchmarkRecoveryCoordinator_AnalyzeDependencies 基准测试：依赖分析性能
func BenchmarkRecoveryCoordinator_AnalyzeDependencies(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-coordinator-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5", "n6", "n7"}, tempDir)
	defer cluster.Close()

	// 崩溃 4 个节点
	for i := 0; i < 4; i++ {
		_ = cluster.Nodes[i].Crash()
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		coordinator := NewRecoveryCoordinator(cluster)
		coordinator.AnalyzeDependencies()
	}
}

// BenchmarkConcurrentCrashRecovery 基准测试：并发崩溃恢复场景
// 模拟真实场景：多个节点同时崩溃后恢复
func BenchmarkConcurrentCrashRecovery(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-concurrent-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		func() {
			cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
			defer cluster.Close()

			// 预热
			for _, node := range cluster.Nodes {
				node.ProposeVote(0)
			}
			cluster.GossipRound()

			// 并发崩溃 3 个节点
			var wg sync.WaitGroup
			for j := 0; j < 3; j++ {
				wg.Add(1)
				go func(node *Node) {
					defer wg.Done()
					_ = node.Crash()
				}(cluster.Nodes[j])
			}
			wg.Wait()

			// 并行恢复
			injector := NewFaultInjector(cluster, 0.0, 0)
			_ = injector.ParallelRecoverAllNodes()

			// 等待增量同步完成
			time.Sleep(200 * time.Millisecond)
		}()
	}
}
