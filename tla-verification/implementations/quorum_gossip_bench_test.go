package implementations

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ===== 基准 1：决策延迟测试 =====

// BenchmarkDecisionLatency_3Nodes 测试 3 节点决策延迟
// 性能目标：< 50ms
func BenchmarkDecisionLatency_3Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "3nodes")
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()

		// 重置状态
		for _, node := range cluster.Nodes {
			node.mu.Lock()
			node.Knowledge = Knowledge{
				Seen:    make(map[string]bool),
				Version: 0,
				Decided: make(map[string]bool),
			}
			node.Decision = Undecided
			node.mu.Unlock()
		}

		// 所有节点发起投票
		for _, node := range cluster.Nodes {
			node.ProposeVote(0)
		}

		b.StartTimer()

		// 执行 Gossip 直到决策
		for round := 0; round < 10; round++ {
			cluster.GossipRound()

			// 尝试决策
			committed := 0
			for _, node := range cluster.Nodes {
				if success, _ := node.DecideCommit(majority); success {
					committed++
				}
			}

			if committed == len(cluster.Nodes) {
				break
			}
		}
	}
}

// BenchmarkDecisionLatency_5Nodes 测试 5 节点决策延迟
// 性能目标：< 100ms
func BenchmarkDecisionLatency_5Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "5nodes")
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()

		// 重置状态
		for _, node := range cluster.Nodes {
			node.mu.Lock()
			node.Knowledge = Knowledge{
				Seen:    make(map[string]bool),
				Version: 0,
				Decided: make(map[string]bool),
			}
			node.Decision = Undecided
			node.mu.Unlock()
		}

		// 所有节点发起投票
		for _, node := range cluster.Nodes {
			node.ProposeVote(0)
		}

		b.StartTimer()

		// 执行 Gossip 直到决策
		for round := 0; round < 15; round++ {
			cluster.GossipRound()

			// 尝试决策
			committed := 0
			for _, node := range cluster.Nodes {
				if success, _ := node.DecideCommit(majority); success {
					committed++
				}
			}

			if committed == len(cluster.Nodes) {
				break
			}
		}
	}
}

// BenchmarkDecisionLatency_7Nodes 测试 7 节点决策延迟
// 性能目标：< 150ms
func BenchmarkDecisionLatency_7Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "7nodes")
	defer os.RemoveAll(tempDir)

	nodeIDs := []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7"}
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()

		// 重置状态
		for _, node := range cluster.Nodes {
			node.mu.Lock()
			node.Knowledge = Knowledge{
				Seen:    make(map[string]bool),
				Version: 0,
				Decided: make(map[string]bool),
			}
			node.Decision = Undecided
			node.mu.Unlock()
		}

		// 所有节点发起投票
		for _, node := range cluster.Nodes {
			node.ProposeVote(0)
		}

		b.StartTimer()

		// 执行 Gossip 直到决策
		for round := 0; round < 20; round++ {
			cluster.GossipRound()

			// 尝试决策
			committed := 0
			for _, node := range cluster.Nodes {
				if success, _ := node.DecideCommit(majority); success {
					committed++
				}
			}

			if committed == len(cluster.Nodes) {
				break
			}
		}
	}
}

// BenchmarkDecisionLatency_10Nodes 测试 10 节点决策延迟
// 性能目标：< 200ms
func BenchmarkDecisionLatency_10Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "10nodes")
	defer os.RemoveAll(tempDir)

	nodeIDs := make([]string, 10)
	for i := 0; i < 10; i++ {
		nodeIDs[i] = fmt.Sprintf("n%d", i+1)
	}
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()

		// 重置状态
		for _, node := range cluster.Nodes {
			node.mu.Lock()
			node.Knowledge = Knowledge{
				Seen:    make(map[string]bool),
				Version: 0,
				Decided: make(map[string]bool),
			}
			node.Decision = Undecided
			node.mu.Unlock()
		}

		// 所有节点发起投票
		for _, node := range cluster.Nodes {
			node.ProposeVote(0)
		}

		b.StartTimer()

		// 执行 Gossip 直到决策
		for round := 0; round < 25; round++ {
			cluster.GossipRound()

			// 尝试决策
			committed := 0
			for _, node := range cluster.Nodes {
				if success, _ := node.DecideCommit(majority); success {
					committed++
				}
			}

			if committed == len(cluster.Nodes) {
				break
			}
		}
	}
}

// ===== 基准 2：GossipRound 性能测试 =====

// BenchmarkGossipRound_3Nodes 测试 3 节点单轮 Gossip 性能
// 性能目标：< 5ms
func BenchmarkGossipRound_3Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "gossip-3nodes")
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	// 预热：让所有节点发起投票
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cluster.GossipRound()
	}
}

// BenchmarkGossipRound_5Nodes 测试 5 节点单轮 Gossip 性能
// 性能目标：< 10ms
func BenchmarkGossipRound_5Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "gossip-5nodes")
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
		cluster.GossipRound()
	}
}

// BenchmarkGossipRound_7Nodes 测试 7 节点单轮 Gossip 性能
// 性能目标：< 20ms
func BenchmarkGossipRound_7Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "gossip-7nodes")
	defer os.RemoveAll(tempDir)

	nodeIDs := []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7"}
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	// 预热
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cluster.GossipRound()
	}
}

// BenchmarkGossipRound_10Nodes 测试 10 节点单轮 Gossip 性能
// 性能目标：< 30ms
func BenchmarkGossipRound_10Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "gossip-10nodes")
	defer os.RemoveAll(tempDir)

	nodeIDs := make([]string, 10)
	for i := 0; i < 10; i++ {
		nodeIDs[i] = fmt.Sprintf("n%d", i+1)
	}
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	// 预热
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cluster.GossipRound()
	}
}

// ===== 基准 3：吞吐量测试 =====

// BenchmarkThroughput_3Nodes 测试 3 节点吞吐量
// 性能目标：> 1000 ops/s
func BenchmarkThroughput_3Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "throughput-3nodes")
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 发起投票
		for _, node := range cluster.Nodes {
			node.ProposeVote(0)
		}

		// Gossip
		cluster.GossipRound()

		// 决策
		for _, node := range cluster.Nodes {
			node.DecideCommit(majority)
		}

		// 重置（模拟下一轮）
		for _, node := range cluster.Nodes {
			node.mu.Lock()
			node.Knowledge.Version++
			node.Knowledge.Seen = make(map[string]bool)
			node.Knowledge.Decided = make(map[string]bool)
			node.Decision = Undecided
			node.mu.Unlock()
		}
	}
}

// BenchmarkThroughput_5Nodes 测试 5 节点吞吐量
// 性能目标：> 800 ops/s
func BenchmarkThroughput_5Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "throughput-5nodes")
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 发起投票
		for _, node := range cluster.Nodes {
			node.ProposeVote(0)
		}

		// Gossip
		cluster.GossipRound()

		// 决策
		for _, node := range cluster.Nodes {
			node.DecideCommit(majority)
		}

		// 重置
		for _, node := range cluster.Nodes {
			node.mu.Lock()
			node.Knowledge.Version++
			node.Knowledge.Seen = make(map[string]bool)
			node.Knowledge.Decided = make(map[string]bool)
			node.Decision = Undecided
			node.mu.Unlock()
		}
	}
}

// BenchmarkThroughput_7Nodes 测试 7 节点吞吐量
// 性能目标：> 600 ops/s
func BenchmarkThroughput_7Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "throughput-7nodes")
	defer os.RemoveAll(tempDir)

	nodeIDs := []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7"}
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 发起投票
		for _, node := range cluster.Nodes {
			node.ProposeVote(0)
		}

		// Gossip
		cluster.GossipRound()

		// 决策
		for _, node := range cluster.Nodes {
			node.DecideCommit(majority)
		}

		// 重置
		for _, node := range cluster.Nodes {
			node.mu.Lock()
			node.Knowledge.Version++
			node.Knowledge.Seen = make(map[string]bool)
			node.Knowledge.Decided = make(map[string]bool)
			node.Decision = Undecided
			node.mu.Unlock()
		}
	}
}

// BenchmarkThroughput_10Nodes 测试 10 节点吞吐量
// 性能目标：> 400 ops/s
func BenchmarkThroughput_10Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "throughput-10nodes")
	defer os.RemoveAll(tempDir)

	nodeIDs := make([]string, 10)
	for i := 0; i < 10; i++ {
		nodeIDs[i] = fmt.Sprintf("n%d", i+1)
	}
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 发起投票
		for _, node := range cluster.Nodes {
			node.ProposeVote(0)
		}

		// Gossip
		cluster.GossipRound()

		// 决策
		for _, node := range cluster.Nodes {
			node.DecideCommit(majority)
		}

		// 重置
		for _, node := range cluster.Nodes {
			node.mu.Lock()
			node.Knowledge.Version++
			node.Knowledge.Seen = make(map[string]bool)
			node.Knowledge.Decided = make(map[string]bool)
			node.Decision = Undecided
			node.mu.Unlock()
		}
	}
}

// ===== 基准 4：内存占用测试 =====

// BenchmarkMemory_3Nodes 测试 3 节点内存占用
// 性能目标：< 50MB
func BenchmarkMemory_3Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "memory-3nodes")
	defer os.RemoveAll(tempDir)

	var m1, m2 runtime.MemStats

	runtime.ReadMemStats(&m1)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	// 执行一些操作以模拟真实使用
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	runtime.ReadMemStats(&m2)

	// 报告内存占用（字节）
	b.ReportMetric(float64(m2.TotalAlloc-m1.TotalAlloc), "bytes")
}

// BenchmarkMemory_5Nodes 测试 5 节点内存占用
// 性能目标：< 80MB
func BenchmarkMemory_5Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "memory-5nodes")
	defer os.RemoveAll(tempDir)

	var m1, m2 runtime.MemStats

	runtime.ReadMemStats(&m1)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	// 执行一些操作
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	runtime.ReadMemStats(&m2)

	b.ReportMetric(float64(m2.TotalAlloc-m1.TotalAlloc), "bytes")
}

// BenchmarkMemory_7Nodes 测试 7 节点内存占用
// 性能目标：< 120MB
func BenchmarkMemory_7Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "memory-7nodes")
	defer os.RemoveAll(tempDir)

	var m1, m2 runtime.MemStats

	runtime.ReadMemStats(&m1)

	nodeIDs := []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7"}
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	// 执行一些操作
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	runtime.ReadMemStats(&m2)

	b.ReportMetric(float64(m2.TotalAlloc-m1.TotalAlloc), "bytes")
}

// BenchmarkMemory_10Nodes 测试 10 节点内存占用
// 性能目标：< 200MB
func BenchmarkMemory_10Nodes(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "nexkv-bench", "memory-10nodes")
	defer os.RemoveAll(tempDir)

	var m1, m2 runtime.MemStats

	runtime.ReadMemStats(&m1)

	nodeIDs := make([]string, 10)
	for i := 0; i < 10; i++ {
		nodeIDs[i] = fmt.Sprintf("n%d", i+1)
	}
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	// 执行一些操作
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}
	cluster.GossipRound()

	runtime.ReadMemStats(&m2)

	b.ReportMetric(float64(m2.TotalAlloc-m1.TotalAlloc), "bytes")
}

// ===== 基准 5：可扩展性测试 =====

// BenchmarkScalability_GossipRound 测试不同节点数的 GossipRound 性能
func BenchmarkScalability_GossipRound(b *testing.B) {
	nodeCounts := []int{3, 5, 7, 10}

	for _, count := range nodeCounts {
		b.Run(fmt.Sprintf("%dNodes", count), func(b *testing.B) {
			tempDir := filepath.Join(os.TempDir(), "nexkv-bench", fmt.Sprintf("scalability-%dnodes", count))
			defer os.RemoveAll(tempDir)

			nodeIDs := make([]string, count)
			for i := 0; i < count; i++ {
				nodeIDs[i] = fmt.Sprintf("n%d", i+1)
			}

			cluster := NewCluster(nodeIDs, tempDir)
			defer cluster.Close()

			// 预热
			for _, node := range cluster.Nodes {
				node.ProposeVote(0)
			}
			cluster.GossipRound()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cluster.GossipRound()
			}
		})
	}
}

// BenchmarkScalability_DecisionLatency 测试不同节点数的决策延迟
func BenchmarkScalability_DecisionLatency(b *testing.B) {
	nodeCounts := []int{3, 5, 7, 10}

	for _, count := range nodeCounts {
		b.Run(fmt.Sprintf("%dNodes", count), func(b *testing.B) {
			tempDir := filepath.Join(os.TempDir(), "nexkv-bench", fmt.Sprintf("scalability-decision-%dnodes", count))
			defer os.RemoveAll(tempDir)

			nodeIDs := make([]string, count)
			for i := 0; i < count; i++ {
				nodeIDs[i] = fmt.Sprintf("n%d", i+1)
			}

			cluster := NewCluster(nodeIDs, tempDir)
			defer cluster.Close()

			majority := cluster.GetMajority()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()

				// 重置状态
				for _, node := range cluster.Nodes {
					node.mu.Lock()
					node.Knowledge = Knowledge{
						Seen:    make(map[string]bool),
						Version: 0,
						Decided: make(map[string]bool),
					}
					node.Decision = Undecided
					node.mu.Unlock()
				}

				// 所有节点发起投票
				for _, node := range cluster.Nodes {
					node.ProposeVote(0)
				}

				b.StartTimer()

				// 执行 Gossip 直到决策
				maxRounds := 10 + count*2
				for round := 0; round < maxRounds; round++ {
					cluster.GossipRound()

					// 尝试决策
					committed := 0
					for _, node := range cluster.Nodes {
						if success, _ := node.DecideCommit(majority); success {
							committed++
						}
					}

					if committed == len(cluster.Nodes) {
						break
					}
				}
			}
		})
	}
}

// BenchmarkScalability_Throughput 测试不同节点数的吞吐量
func BenchmarkScalability_Throughput(b *testing.B) {
	nodeCounts := []int{3, 5, 7, 10}

	for _, count := range nodeCounts {
		b.Run(fmt.Sprintf("%dNodes", count), func(b *testing.B) {
			tempDir := filepath.Join(os.TempDir(), "nexkv-bench", fmt.Sprintf("scalability-throughput-%dnodes", count))
			defer os.RemoveAll(tempDir)

			nodeIDs := make([]string, count)
			for i := 0; i < count; i++ {
				nodeIDs[i] = fmt.Sprintf("n%d", i+1)
			}

			cluster := NewCluster(nodeIDs, tempDir)
			defer cluster.Close()

			majority := cluster.GetMajority()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// 发起投票
				for _, node := range cluster.Nodes {
					node.ProposeVote(0)
				}

				// Gossip
				cluster.GossipRound()

				// 决策
				for _, node := range cluster.Nodes {
					node.DecideCommit(majority)
				}

				// 重置
				for _, node := range cluster.Nodes {
					node.mu.Lock()
					node.Knowledge.Version++
					node.Knowledge.Seen = make(map[string]bool)
					node.Knowledge.Decided = make(map[string]bool)
					node.Decision = Undecided
					node.mu.Unlock()
				}
			}
		})
	}
}
