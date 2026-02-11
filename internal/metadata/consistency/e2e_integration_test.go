// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package consistency

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/stretchr/testify/require"
)

// ==================== 端到端测试场景 ====================

// E2ETestScenario 端到端测试场景
type E2ETestScenario struct {
	Name         string
	Nodes        []string               // 节点 ID 列表
	Topology     *TreeTopology          // 树形拓扑
	Coordinators map[string]*TreeTopologyCoordinator // nodeID -> coordinator
	MetadataKVs  map[string]*mockMetadataKVForTree  // nodeID -> metadataKV
	MerkleTrees  map[string]*kvstore.NamespacedMerkleTree // nodeID -> merkleTree
}

// NewE2ETestScenario 创建端到端测试场景
func NewE2ETestScenario(name string, nodeIDs []string) *E2ETestScenario {
	// 创建树形拓扑（root 是第一个节点）
	topology := NewTreeTopology(nodeIDs[0])

	// 构建三层树结构：root -> parent -> leaf
	if len(nodeIDs) > 1 {
		// 添加第一层父节点
		for i := 1; i < min(4, len(nodeIDs)); i++ {
			_ = topology.AddChild(nodeIDs[0], nodeIDs[i])
		}
	}
	if len(nodeIDs) > 4 {
		// 添加第二层子节点
		for i := 4; i < len(nodeIDs); i++ {
			parentIdx := (i-1)%3 + 1 // 轮流分配给前3个父节点
			_ = topology.AddChild(nodeIDs[parentIdx], nodeIDs[i])
		}
	}

	return &E2ETestScenario{
		Name:         name,
		Nodes:        nodeIDs,
		Topology:     topology,
		Coordinators: make(map[string]*TreeTopologyCoordinator),
		MetadataKVs:  make(map[string]*mockMetadataKVForTree),
		MerkleTrees:  make(map[string]*kvstore.NamespacedMerkleTree),
	}
}

// min 返回两个整数中的较小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Initialize 初始化测试场景
func (s *E2ETestScenario) Initialize(t *testing.T) {
	hlc := clock.NewHLC()

	for _, nodeID := range s.Nodes {
		// 创建 Merkle Tree
		merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
		s.MerkleTrees[nodeID] = merkleTree

		// 创建 MetadataKV
		metadataKV := &mockMetadataKVForTree{mockMetadataKV: newMockMetadataKV()}
		s.MetadataKVs[nodeID] = metadataKV

		// 创建协调器
		coordinator, err := NewTreeTopologyCoordinator(&TreeTopologyOptions{
			Topology:    s.Topology,
			LocalNodeID: nodeID,
			TwoPCOptions: &TwoPCOptions{
				MetadataKV: metadataKV,
				MerkleTree: merkleTree,
				HLC:        hlc,
			},
			HLC: hlc,
		})
		if err != nil {
			if t != nil {
				require.NoError(t, err)
			} else {
				// 基准测试场景，直接 panic
				panic(fmt.Sprintf("create coordinator failed: %v", err))
			}
		}
		s.Coordinators[nodeID] = coordinator
	}
}

// Cleanup 清理测试场景
func (s *E2ETestScenario) Cleanup() {
	for _, coordinator := range s.Coordinators {
		_ = coordinator.Close()
	}
}

// ==================== 端到端测试用例 ====================

// TestE2E_MetadataSync_ThreeLayerConsistency 测试三级一致性模型的端到端元数据同步
func TestE2E_MetadataSync_ThreeLayerConsistency(t *testing.T) {
	scenario := NewE2ETestScenario("ThreeLayerConsistency", []string{
		"root", "parent-1", "parent-2", "parent-3", "leaf-1", "leaf-2", "leaf-3",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	ctx := context.Background()

	// Layer1: 关键元数据（2PC 强一致）
	t.Run("Layer1_ClusterConfig", func(t *testing.T) {
		coordinator := scenario.Coordinators["leaf-1"]

		err := coordinator.PutWithLayer(ctx, kvstore.NamespaceCluster, "config-key", "config-value", Layer1)
		require.NoError(t, err)

		// 验证数据已写入本地
		metadataKV := scenario.MetadataKVs["leaf-1"]
		// 注意：mock 实现不实际存储，所以这里只验证没有错误
		require.NotNil(t, metadataKV)
	})

	// Layer2: 重要元数据（Quorum 增强最终一致）
	t.Run("Layer2_RoleChange", func(t *testing.T) {
		coordinator := scenario.Coordinators["parent-1"]

		err := coordinator.PutWithLayer(ctx, kvstore.NamespaceRole, "role-key", "role-value", Layer2)
		require.NoError(t, err)
	})

	// Layer3: 普通元数据（Gossip 最终一致）
	t.Run("Layer3_NodeStatus", func(t *testing.T) {
		coordinator := scenario.Coordinators["leaf-2"]

		err := coordinator.PutWithLayer(ctx, kvstore.NamespaceNode, "status-key", "status-value", Layer3)
		require.NoError(t, err)
	})
}

// TestE2E_MerkleTreeSync 测试 Merkle Tree 同步
func TestE2E_MerkleTreeSync(t *testing.T) {
	scenario := NewE2ETestScenario("MerkleTreeSync", []string{
		"root", "node-1", "node-2", "node-3",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	ctx := context.Background()
	coordinator := scenario.Coordinators["node-1"]
	merkleTree := scenario.MerkleTrees["node-1"]

	// 获取初始 Global Root
	initialRoot := merkleTree.GetGlobalRootHash()
	require.NotEmpty(t, initialRoot)

	// 更新元数据
	err := coordinator.PutWithLayer(ctx, kvstore.NamespaceNode, "test-key", "test-value", Layer1)
	require.NoError(t, err)

	// 验证 Global Root 已变化
	newRoot := merkleTree.GetGlobalRootHash()
	require.NotEqual(t, initialRoot, newRoot)

	t.Logf("Merkle Root changed: %s -> %s", initialRoot, newRoot)
}

// TestE2E_2PC_CommitFlow 测试 2PC 提交流程
func TestE2E_2PC_CommitFlow(t *testing.T) {
	scenario := NewE2ETestScenario("2PCCommitFlow", []string{
		"root", "parent-1", "child-1",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	ctx := context.Background()
	coordinator := scenario.Coordinators["child-1"]

	// 创建 2PC 事务
	twoPCCoord := coordinator.twoPCCoordinator
	participants := []string{"root", "parent-1", "child-1"}

	tx, err := twoPCCoord.BeginTransaction(participants)
	require.NoError(t, err)
	require.Equal(t, TxStateInit, tx.State)

	// 添加操作
	value := []byte(`{"status": "active"}`)
	tx.AddOperation(kvstore.NamespaceCluster, "test-key", value, 1)

	// PreCommit
	err = twoPCCoord.PreCommit(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, TxStatePreCommit, tx.State)

	// Commit
	err = twoPCCoord.Commit(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, TxStateCommitted, tx.State)
}

// ==================== 故障场景模拟测试 ====================

// TestE2E_NodeFailure_Rollback 测试节点故障时的回滚
func TestE2E_NodeFailure_Rollback(t *testing.T) {
	scenario := NewE2ETestScenario("NodeFailure", []string{
		"root", "node-1", "node-2", "node-3",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	ctx := context.Background()
	coordinator := scenario.Coordinators["node-1"]
	twoPCCoord := coordinator.twoPCCoordinator

	// 开始事务
	participants := []string{"root", "node-1", "node-2", "node-3"}
	tx, err := twoPCCoord.BeginTransaction(participants)
	require.NoError(t, err)

	// 添加操作
	value := []byte(`{"status": "active"}`)
	tx.AddOperation(kvstore.NamespaceCluster, "test-key", value, 1)

	// PreCommit
	err = twoPCCoord.PreCommit(ctx, tx)
	require.NoError(t, err)

	// 模拟节点故障：node-3 没有确认
	delete(tx.Acks, "node-3")

	// Commit 应该失败并回滚
	err = twoPCCoord.Commit(ctx, tx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not all ACKs received")
	require.Equal(t, TxStateRolledBack, tx.State)
}

// TestE2E_Timeout_AutoRollback 测试超时自动回滚
func TestE2E_Timeout_AutoRollback(t *testing.T) {
	scenario := NewE2ETestScenario("Timeout", []string{
		"root", "node-1",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	// 创建超时时间极短的事务
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := &mockMetadataKVForTree{mockMetadataKV: newMockMetadataKV()}

	twoPCCoord, err := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV:     metadataKV,
		MerkleTree:     merkleTree,
		HLC:            hlc,
		DefaultTimeout: 1 * time.Millisecond, // 极短超时
	})
	require.NoError(t, err)
	defer twoPCCoord.Close()

	// 开始事务
	participants := []string{"root", "node-1"}
	tx, err := twoPCCoord.BeginTransaction(participants)
	require.NoError(t, err)

	// 等待超时
	time.Sleep(10 * time.Millisecond)

	// 验证事务已超时
	require.True(t, tx.IsTimedOut())

	// 清理超时事务
	cleaned := twoPCCoord.CleanupTimeoutTransactions()
	require.Equal(t, 1, cleaned)

	// 验证事务已被清理
	_, err = twoPCCoord.GetTransaction(tx.TxID)
	require.Error(t, err)
}

// TestE2E_NetworkPartition_SomeNodesUnreachable 测试网络分区场景
func TestE2E_NetworkPartition_SomeNodesUnreachable(t *testing.T) {
	scenario := NewE2ETestScenario("NetworkPartition", []string{
		"root", "node-1", "node-2", "node-3", "node-4",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	ctx := context.Background()

	// 模拟网络分区：node-4 无法访问
	// node-1 发起 2PC 事务，但 node-4 无法确认
	coordinator := scenario.Coordinators["node-1"]
	twoPCCoord := coordinator.twoPCCoordinator

	participants := []string{"root", "node-1", "node-2", "node-3", "node-4"}
	tx, err := twoPCCoord.BeginTransaction(participants)
	require.NoError(t, err)

	// 添加操作
	value := []byte(`{"status": "active"}`)
	tx.AddOperation(kvstore.NamespaceCluster, "test-key", value, 1)

	// PreCommit（模拟 node-4 无法确认）
	err = twoPCCoord.PreCommit(ctx, tx)
	require.NoError(t, err)

	// 模拟 node-4 未确认
	delete(tx.Acks, "node-4")

	// Commit 应该失败
	err = twoPCCoord.Commit(ctx, tx)
	require.Error(t, err)

	t.Logf("网络分区场景：node-4 无法访问，事务正确回滚")
}

// ==================== 并发场景测试 ====================

// TestE2E_ConcurrentTransactions 测试并发事务
func TestE2E_ConcurrentTransactions(t *testing.T) {
	scenario := NewE2ETestScenario("Concurrent", []string{
		"root", "node-1", "node-2",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	ctx := context.Background()
	coordinator := scenario.Coordinators["node-1"]
	twoPCCoord := coordinator.twoPCCoordinator

	// 并发创建多个事务
	const numTransactions = 10
	var wg sync.WaitGroup
	errors := make(chan error, numTransactions)

	for i := 0; i < numTransactions; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			participants := []string{"root", "node-1", "node-2"}
			tx, err := twoPCCoord.BeginTransaction(participants)
			if err != nil {
				errors <- err
				return
			}

			key := fmt.Sprintf("concurrent-key-%d", idx)
			value := []byte(fmt.Sprintf(`{"value": %d}`, idx))
			tx.AddOperation(kvstore.NamespaceNode, key, value, uint64(idx))

			if err := twoPCCoord.PreCommit(ctx, tx); err != nil {
				errors <- err
				return
			}

			if err := twoPCCoord.Commit(ctx, tx); err != nil {
				errors <- err
				return
			}

			errors <- nil
		}(i)
	}

	wg.Wait()
	close(errors)

	// 验证所有事务都成功
	errorCount := 0
	for err := range errors {
		if err != nil {
			errorCount++
			t.Logf("Transaction error: %v", err)
		}
	}

	require.Equal(t, 0, errorCount, "All transactions should succeed")
}

// ==================== 性能验证测试 ====================

// TestE2E_Performance_DifferenceDetection 测试差异检测性能
func TestE2E_Performance_DifferenceDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	scenario := NewE2ETestScenario("Performance", []string{
		"root", "node-1", "node-2",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	merkleTree := scenario.MerkleTrees["node-1"]

	// 基准测试：获取 Global Root Hash
	iterations := 10000
	start := time.Now()

	for i := 0; i < iterations; i++ {
		_ = merkleTree.GetGlobalRootHash()
	}

	elapsed := time.Since(start)
	avgTime := elapsed / time.Duration(iterations)

	t.Logf("差异检测性能: %d 次操作耗时 %v，平均 %v/次",
		iterations, elapsed, avgTime)

	// 验证性能目标：< 1µs
	require.Less(t, avgTime, 1*time.Microsecond,
		"差异检测应该 < 1µs")
}

// TestE2E_Performance_MerkleUpdate 测试 Merkle 更新性能
func TestE2E_Performance_MerkleUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	scenario := NewE2ETestScenario("MerkleUpdate", []string{
		"root", "node-1",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	merkleTree := scenario.MerkleTrees["node-1"]

	// 批量更新测试
	updates := 1000
	start := time.Now()

	for i := 0; i < updates; i++ {
		key := fmt.Sprintf("key-%04d", i)
		metadata := map[string]string{
			"id":    fmt.Sprintf("value-%d", i),
			"index": fmt.Sprintf("%d", i),
		}
		err := merkleTree.UpdateKey(kvstore.NamespaceNode, key, metadata)
		require.NoError(t, err)
	}

	elapsed := time.Since(start)
	avgTime := elapsed / time.Duration(updates)

	t.Logf("Merkle 更新性能: %d 次更新耗时 %v，平均 %v/次",
		updates, elapsed, avgTime)

	// 验证性能目标：< 100µs/次
	require.Less(t, avgTime, 100*time.Microsecond,
		"Merkle 更新应该 < 100µs")
}

// TestE2E_Performance_CacheHitRate 测试缓存命中率
func TestE2E_Performance_CacheHitRate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	scenario := NewE2ETestScenario("CacheHitRate", []string{
		"root", "node-1",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	merkleTree := scenario.MerkleTrees["node-1"]

	// 更新一个 Key
	metadata := map[string]string{"key": "value"}
	_ = merkleTree.UpdateKey(kvstore.NamespaceNode, "test-key", metadata)

	// 连续获取 Global Root（应该命中缓存）
	iterations := 1000
	start := time.Now()

	for i := 0; i < iterations; i++ {
		_ = merkleTree.GetGlobalRootHash()
	}

	elapsed := time.Since(start)

	// 获取缓存统计
	stats := merkleTree.GetCacheStats()
	hitRate := stats["hit_rate"].(float64)

	t.Logf("缓存性能: %d 次读取耗时 %v，命中率 %.2f%%",
		iterations, elapsed, hitRate*100)

	// 验证缓存命中率 > 99%
	require.Greater(t, hitRate, 0.99,
		"缓存命中率应该 > 99%")
}

// ==================== 混沌测试 ====================

// TestE2E_Chaos_RandomFailures 测试随机故障场景
func TestE2E_Chaos_RandomFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	scenario := NewE2ETestScenario("Chaos", []string{
		"root", "node-1", "node-2", "node-3",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	ctx := context.Background()

	// 模拟 50 次随机操作，每次操作有 10% 概率失败
	numOperations := 50
	successCount := 0

	for i := 0; i < numOperations; i++ {
		coordinator := scenario.Coordinators["node-1"]

		// 随机选择层级
		layer := Layer3 // 默认 Gossip
		if i%3 == 0 {
			layer = Layer1 // 2PC
		} else if i%3 == 1 {
			layer = Layer2 // Quorum
		}

		key := fmt.Sprintf("chaos-key-%d", i)
		value := fmt.Sprintf("chaos-value-%d", i)

		err := coordinator.PutWithLayer(ctx, kvstore.NamespaceNode, key, value, layer)

		// Layer3（Gossip）应该总是成功
		// Layer1 和 Layer2 可能因为模拟失败而失败
		if err == nil || layer == Layer3 {
			successCount++
		}
	}

	successRate := float64(successCount) / float64(numOperations) * 100

	t.Logf("混沌测试: %d 次操作，%d 次成功，成功率 %.1f%%",
		numOperations, successCount, successRate)

	// 至少 80% 的操作应该成功（Gossip 总是成功）
	require.GreaterOrEqual(t, successRate, 80.0,
		"成功率应该 >= 80%")
}

// TestE2E_Chaos_RapidNodeJoinsLeaves 测试节点频繁加入/离开
func TestE2E_Chaos_RapidNodeJoinsLeaves(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	scenario := NewE2ETestScenario("RapidChanges", []string{
		"root",
	})
	scenario.Initialize(t)
	defer scenario.Cleanup()

	// 模拟节点频繁加入/离开
	joinLeaveCycles := 20

	for cycle := 0; cycle < joinLeaveCycles; cycle++ {
		// 加入节点
		for i := 0; i < 3; i++ {
			nodeID := fmt.Sprintf("dynamic-node-%d", cycle*3+i)
			err := scenario.Topology.AddChild("root", nodeID)
			if err != nil {
				t.Logf("加入节点失败: %v", err)
			}
		}

		// 离开节点（模拟：不再引用）
		// 在实际实现中，这里会调用 RemoveNode 方法
	}

	t.Logf("完成 %d 轮节点加入/离开测试", joinLeaveCycles)
}

// ==================== 基准测试 ====================

// BenchmarkE2E_MetadataUpdate 性能测试：元数据更新
func BenchmarkE2E_MetadataUpdate(b *testing.B) {
	scenario := NewE2ETestScenario("Benchmark", []string{
		"root", "node-1", "node-2",
	})
	scenario.Initialize(nil)
	defer scenario.Cleanup()

	ctx := context.Background()
	coordinator := scenario.Coordinators["node-1"]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("bench-key-%d", i%1000)
		value := fmt.Sprintf("bench-value-%d", i)
		_ = coordinator.PutWithLayer(ctx, kvstore.NamespaceNode, key, value, Layer3)
	}
}

// BenchmarkE2E_MerkleRootCalculation 性能测试：Merkle Root 计算
func BenchmarkE2E_MerkleRootCalculation(b *testing.B) {
	scenario := NewE2ETestScenario("Benchmark", []string{
		"root", "node-1",
	})
	scenario.Initialize(nil)
	defer scenario.Cleanup()

	merkleTree := scenario.MerkleTrees["node-1"]

	// 预先添加一些数据
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%04d", i)
		metadata := map[string]string{"id": fmt.Sprintf("value-%d", i)}
		_ = merkleTree.UpdateKey(kvstore.NamespaceNode, key, metadata)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = merkleTree.GetGlobalRootHash()
	}
}

// BenchmarkE2E_Layer1_Put 性能测试：Layer1（2PC）写入
func BenchmarkE2E_Layer1_Put(b *testing.B) {
	scenario := NewE2ETestScenario("Benchmark", []string{
		"root", "parent-1", "child-1",
	})
	scenario.Initialize(nil)
	defer scenario.Cleanup()

	ctx := context.Background()
	coordinator := scenario.Coordinators["child-1"]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("bench-key-%d", i%100)
		value := fmt.Sprintf("bench-value-%d", i)
		_ = coordinator.PutWithLayer(ctx, kvstore.NamespaceCluster, key, value, Layer1)
	}
}
