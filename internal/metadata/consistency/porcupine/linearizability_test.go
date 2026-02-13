// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件包含线性一致性验证的集成测试
package porcupine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// =============================================================================
// 基础线性化测试
// =============================================================================

// TestLinearizability_BasicPutGet 测试基本的 Put/Get 操作线性化
func TestLinearizability_BasicPutGet(t *testing.T) {
	// 创建测试场景
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 执行 Put
	err := client.Put(ctx, "ns1", "key1", []byte("value1"))
	require.NoError(t, err)

	// 执行 Get
	val, err := client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte("value1"), val)

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Basic Put/Get should be linearizable: %s", result.Error)
}

// TestLinearizability_PutGetDelete 测试 Put/Get/Delete 操作线性化
func TestLinearizability_PutGetDelete(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// Put
	err := client.Put(ctx, "ns1", "key1", []byte("value1"))
	require.NoError(t, err)

	// Get
	val, err := client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte("value1"), val)

	// Delete
	err = client.Delete(ctx, "ns1", "key1")
	require.NoError(t, err)

	// Get 应该失败
	_, err = client.Get(ctx, "ns1", "key1")
	require.Error(t, err)

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Put/Get/Delete should be linearizable: %s", result.Error)
}

// TestLinearizability_Overwrite 测试覆盖写入线性化
func TestLinearizability_Overwrite(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 第一次写入
	err := client.Put(ctx, "ns1", "key1", []byte("value1"))
	require.NoError(t, err)

	// 覆盖写入
	err = client.Put(ctx, "ns1", "key1", []byte("value2"))
	require.NoError(t, err)

	// 读取应该返回新值
	val, err := client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte("value2"), val)

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Overwrite should be linearizable: %s", result.Error)
}

// TestLinearizability_MultipleKeys 测试多 key 操作线性化
func TestLinearizability_MultipleKeys(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 写入多个 key
	for i := 0; i < 10; i++ {
		key := string(rune('a' + i))
		err := client.Put(ctx, "ns1", key, []byte("value"+key))
		require.NoError(t, err)
	}

	// 读取多个 key
	for i := 0; i < 10; i++ {
		key := string(rune('a' + i))
		val, err := client.Get(ctx, "ns1", key)
		require.NoError(t, err)
		require.Equal(t, []byte("value"+key), val)
	}

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Multiple keys should be linearizable: %s", result.Error)
}

// TestLinearizability_MultipleNamespaces 测试多命名空间操作线性化
func TestLinearizability_MultipleNamespaces(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 在不同命名空间写入相同 key
	err := client.Put(ctx, "ns1", "key1", []byte("value-ns1"))
	require.NoError(t, err)

	err = client.Put(ctx, "ns2", "key1", []byte("value-ns2"))
	require.NoError(t, err)

	// 读取应该返回各自命名空间的值
	val1, err := client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte("value-ns1"), val1)

	val2, err := client.Get(ctx, "ns2", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte("value-ns2"), val2)

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Multiple namespaces should be linearizable: %s", result.Error)
}

// =============================================================================
// 并发线性化测试
// =============================================================================

// TestLinearizability_ConcurrentWrites 测试并发写入线性化
func TestLinearizability_ConcurrentWrites(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	var wg sync.WaitGroup
	numOps := 100

	// 并发写入同一个 key
	for i := 0; i < numOps; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			value := []byte("value")
			_ = client.Put(ctx, "ns1", "key1", value)
		}(i)
	}

	wg.Wait()

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Concurrent writes should be linearizable: %s", result.Error)
}

// TestLinearizability_ConcurrentReadWrite 测试并发读写线性化
func TestLinearizability_ConcurrentReadWrite(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	var wg sync.WaitGroup
	numOps := 50

	// 并发写入
	for i := 0; i < numOps; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = client.Put(ctx, "ns1", "key1", []byte("value"))
		}(i)
	}

	// 并发读取（在写入之后启动，确保有数据可读）
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < numOps; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = client.Get(ctx, "ns1", "key1")
		}()
	}

	wg.Wait()

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Concurrent read/write should be linearizable: %s", result.Error)
}

// TestLinearizability_MultiClient 测试多客户端并发操作
func TestLinearizability_MultiClient(t *testing.T) {
	nodes := []string{"node-1", "node-2", "node-3"}
	recScenario := NewRecordingE2ETestScenario(nodes)
	for _, nodeID := range nodes {
		recScenario.AddNode(nodeID, newMockScenarioKV())
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	opsPerClient := 20

	// 每个客户端并发执行操作
	for _, nodeID := range nodes {
		wg.Add(1)
		go func(nid string) {
			defer wg.Done()
			client := recScenario.RecordingClients[nid]
			for i := 0; i < opsPerClient; i++ {
				key := nid + "-key"
				_ = client.Put(ctx, "ns1", key, []byte("value"))
				_, _ = client.Get(ctx, "ns1", key)
			}
		}(nodeID)
	}

	wg.Wait()

	// 验证线性化（每个客户端单独验证）
	for _, nodeID := range nodes {
		recorder := recScenario.Recorders[nodeID]
		ops := recorder.GetOperations()
		if len(ops) == 0 {
			continue
		}
		result := recScenario.Checker.CheckOperations(ops)
		require.True(t, result.Ok, "Client %s operations should be linearizable: %s", nodeID, result.Error)
	}
}

// TestLinearizability_ConcurrentDifferentKeys 测试并发操作不同 key
func TestLinearizability_ConcurrentDifferentKeys(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	var wg sync.WaitGroup
	numKeys := 10
	opsPerKey := 10

	// 并发操作不同的 key
	for k := 0; k < numKeys; k++ {
		wg.Add(1)
		go func(keyIdx int) {
			defer wg.Done()
			key := string(rune('a' + keyIdx))
			for i := 0; i < opsPerKey; i++ {
				_ = client.Put(ctx, "ns1", key, []byte("value"))
				_, _ = client.Get(ctx, "ns1", key)
			}
		}(k)
	}

	wg.Wait()

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Concurrent different keys should be linearizable: %s", result.Error)
}

// =============================================================================
// Quorum 线性化测试
// =============================================================================

// TestLinearizability_QuorumPutGet 测试 Quorum Put/Get 线性化
func TestLinearizability_QuorumPutGet(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// QuorumPut
	err := client.QuorumPut(ctx, "ns1", "key1", []byte("quorum-value"))
	require.NoError(t, err)

	// QuorumGet
	val, err := client.QuorumGet(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte("quorum-value"), val)

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Quorum Put/Get should be linearizable: %s", result.Error)
}

// TestLinearizability_QuorumConcurrent 测试并发 Quorum 操作
func TestLinearizability_QuorumConcurrent(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	var wg sync.WaitGroup
	numOps := 50

	// 并发 QuorumPut
	for i := 0; i < numOps; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = client.QuorumPut(ctx, "ns1", "key1", []byte("value"))
		}()
	}

	// 并发 QuorumGet
	for i := 0; i < numOps; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = client.QuorumGet(ctx, "ns1", "key1")
		}()
	}

	wg.Wait()

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Concurrent Quorum operations should be linearizable: %s", result.Error)
}

// TestLinearizability_MixedOperations 测试混合操作线性化
func TestLinearizability_MixedOperations(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 混合使用普通操作和 Quorum 操作
	err := client.Put(ctx, "ns1", "key1", []byte("normal-value"))
	require.NoError(t, err)

	err = client.QuorumPut(ctx, "ns1", "key2", []byte("quorum-value"))
	require.NoError(t, err)

	_, err = client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)

	_, err = client.QuorumGet(ctx, "ns1", "key2")
	require.NoError(t, err)

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Mixed operations should be linearizable: %s", result.Error)
}

// =============================================================================
// 边界条件测试
// =============================================================================

// TestLinearizability_EmptyValue 测试空值
func TestLinearizability_EmptyValue(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 写入空值
	err := client.Put(ctx, "ns1", "key1", []byte{})
	require.NoError(t, err)

	// 读取空值
	val, err := client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte{}, val)

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Empty value should be linearizable: %s", result.Error)
}

// TestLinearizability_LargeValue 测试大值
func TestLinearizability_LargeValue(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 写入大值（1KB）
	largeValue := make([]byte, 1024)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	err := client.Put(ctx, "ns1", "key1", largeValue)
	require.NoError(t, err)

	// 读取大值
	val, err := client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, largeValue, val)

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Large value should be linearizable: %s", result.Error)
}

// TestLinearizability_GetNonExistent 测试读取不存在的 key
func TestLinearizability_GetNonExistent(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 读取不存在的 key
	_, err := client.Get(ctx, "ns1", "nonexistent")
	require.Error(t, err)

	// 验证线性化（即使操作失败，也应该可线性化）
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Get non-existent should be linearizable: %s", result.Error)
}

// TestLinearizability_DeleteNonExistent 测试删除不存在的 key
func TestLinearizability_DeleteNonExistent(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 删除不存在的 key（mock 实现通常返回成功）
	_ = client.Delete(ctx, "ns1", "nonexistent")
	// 不检查错误，因为不同实现可能有不同行为

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Delete non-existent should be linearizable: %s", result.Error)
}

// TestLinearizability_RapidOperations 测试快速连续操作
func TestLinearizability_RapidOperations(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 快速连续执行大量操作
	for i := 0; i < 1000; i++ {
		_ = client.Put(ctx, "ns1", "key1", []byte("value"))
		_, _ = client.Get(ctx, "ns1", "key1")
	}

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Rapid operations should be linearizable: %s", result.Error)
}

// TestLinearizability_MaxClients 测试最大客户端数（10个）
func TestLinearizability_MaxClients(t *testing.T) {
	// 创建 10 个客户端（Pre 文档定义的最大值）
	nodes := make([]string, 10)
	for i := 0; i < 10; i++ {
		nodes[i] = "node-" + string(rune('0'+i))
	}

	recScenario := NewRecordingE2ETestScenario(nodes)
	for _, nodeID := range nodes {
		recScenario.AddNode(nodeID, newMockScenarioKV())
	}

	ctx := context.Background()
	var wg sync.WaitGroup

	// 每个客户端执行操作
	for _, nodeID := range nodes {
		wg.Add(1)
		go func(nid string) {
			defer wg.Done()
			client := recScenario.RecordingClients[nid]
			_ = client.Put(ctx, "ns1", "key", []byte(nid))
		}(nodeID)
	}

	wg.Wait()

	// 验证每个客户端的线性化
	for _, nodeID := range nodes {
		recorder := recScenario.Recorders[nodeID]
		ops := recorder.GetOperations()
		if len(ops) == 0 {
			continue
		}
		result := recScenario.Checker.CheckOperations(ops)
		require.True(t, result.Ok, "Client %s should be linearizable: %s", nodeID, result.Error)
	}
}

// =============================================================================
// 性能测试
// =============================================================================

// TestLinearizability_Performance1000Ops 测试 1000 操作性能
func TestLinearizability_Performance1000Ops(t *testing.T) {
	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 执行 1000 操作
	for i := 0; i < 1000; i++ {
		_ = client.Put(ctx, "ns1", "key1", []byte("value"))
	}

	// 验证性能目标：1000 操作 < 100ms
	start := time.Now()
	result := recScenario.VerifyLinearizability()
	elapsed := time.Since(start)

	require.True(t, result.Ok, "Should be linearizable: %s", result.Error)
	require.Less(t, elapsed.Milliseconds(), int64(100), "1000 ops should check in < 100ms, took %v", elapsed)

	t.Logf("1000 operations check time: %v", elapsed)
}

// TestLinearizability_Performance10000Ops 测试 10000 操作性能
func TestLinearizability_Performance10000Ops(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()
	client := recScenario.RecordingClients["node-1"]

	// 执行 10000 操作
	for i := 0; i < 10000; i++ {
		_ = client.Put(ctx, "ns1", "key1", []byte("value"))
	}

	// 验证性能目标：10000 操作 < 1s
	start := time.Now()
	result := recScenario.VerifyLinearizability()
	elapsed := time.Since(start)

	require.True(t, result.Ok, "Should be linearizable: %s", result.Error)
	require.Less(t, elapsed.Milliseconds(), int64(1000), "10000 ops should check in < 1s, took %v", elapsed)

	t.Logf("10000 operations check time: %v", elapsed)
}
