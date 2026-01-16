package implementations

import (
	"os"
	"testing"
	"time"
)

// BenchmarkCalculateSyncTimeout 基准测试：超时计算性能
// 验证：动态超时计算本身的开销极小（纳秒级）
func BenchmarkCalculateSyncTimeout(b *testing.B) {
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for nodeCount := 3; nodeCount <= 10; nodeCount++ {
			_ = calculateSyncTimeout(nodeCount)
		}
	}
}

// BenchmarkCrashRecoverySingleCycle 基准测试：单次崩溃恢复周期
// 测量：崩溃 → 恢复 → 增量同步 → 完成 的完整周期
// 注意：使用b.N=1，因为实际恢复过程需要等待
func BenchmarkCrashRecoverySingleCycle_3Nodes(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-recovery-3-*")
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

	// 设置基准测试只运行一次（因为需要等待真实时间）
	b.ResetTimer()
	b.N = 1

	// 模拟崩溃和恢复
	node := cluster.Nodes[0]
	_ = node.Crash()

	start := time.Now()
	_ = node.Recover(cluster)

	// 等待增量同步完成（最多5秒）
	for time.Since(start) < 5*time.Second {
		node.mu.RLock()
		recovered := !node.IsCrashed
		node.mu.RUnlock()

		if recovered {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// BenchmarkCrashRecoverySingleCycle_7Nodes 基准测试：7节点崩溃恢复
// 对比：7节点使用13秒超时，比3节点的5秒慢
func BenchmarkCrashRecoverySingleCycle_7Nodes(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-recovery-7-*")
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
	b.N = 1

	// 模拟崩溃和恢复
	node := cluster.Nodes[0]
	_ = node.Crash()

	start := time.Now()
	_ = node.Recover(cluster)

	// 等待增量同步完成（最多13秒）
	for time.Since(start) < 13*time.Second {
		node.mu.RLock()
		recovered := !node.IsCrashed
		node.mu.RUnlock()

		if recovered {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}
