// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件包含场景适配器的单元测试
package porcupine

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// mockScenarioKV 模拟 E2ETestScenario 中的 KV（线程安全）
type mockScenarioKV struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMockScenarioKV() *mockScenarioKV {
	return &mockScenarioKV{
		data: make(map[string][]byte),
	}
}

func (m *mockScenarioKV) Put(ctx context.Context, ns, key string, value any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[ns+":"+key] = value.([]byte)
	return nil
}

func (m *mockScenarioKV) Get(ctx context.Context, ns, key string) (any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.data[ns+":"+key]
	if !ok {
		return nil, errKeyNotFound
	}
	return val, nil
}

func (m *mockScenarioKV) Delete(ctx context.Context, ns, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, ns+":"+key)
	return nil
}

// TestScenarioKVAdapter_Put 测试适配器 Put
func TestScenarioKVAdapter_Put(t *testing.T) {
	kv := newMockScenarioKV()
	adapter := NewScenarioKVAdapter(kv)

	ctx := context.Background()
	err := adapter.Put(ctx, "ns1", "key1", []byte("value1"))
	require.NoError(t, err)
}

// TestScenarioKVAdapter_Get 测试适配器 Get
func TestScenarioKVAdapter_Get(t *testing.T) {
	kv := newMockScenarioKV()
	adapter := NewScenarioKVAdapter(kv)

	ctx := context.Background()
	_ = kv.Put(ctx, "ns1", "key1", []byte("value1"))

	val, err := adapter.Get(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte("value1"), val)
}

// TestScenarioKVAdapter_Delete 测试适配器 Delete
func TestScenarioKVAdapter_Delete(t *testing.T) {
	kv := newMockScenarioKV()
	adapter := NewScenarioKVAdapter(kv)

	ctx := context.Background()
	_ = kv.Put(ctx, "ns1", "key1", []byte("value1"))

	err := adapter.Delete(ctx, "ns1", "key1")
	require.NoError(t, err)

	// 验证已删除
	_, err = adapter.Get(ctx, "ns1", "key1")
	require.Error(t, err)
}

// TestScenarioKVAdapter_FullFlow 测试完整流程
func TestScenarioKVAdapter_FullFlow(t *testing.T) {
	kv := newMockScenarioKV()
	adapter := NewScenarioKVAdapter(kv)

	// 创建 RecordingClient
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)
	client := NewRecordingClient(adapter, recorder)

	ctx := context.Background()

	// 执行操作
	_ = client.Put(ctx, "ns1", "k1", []byte("v1"))
	val, _ := client.Get(ctx, "ns1", "k1")
	require.Equal(t, []byte("v1"), val)
	_ = client.Delete(ctx, "ns1", "k1")

	// 验证记录
	ops := recorder.GetOperations()
	require.Len(t, ops, 3)

	// 验证线性化
	checker := NewConsistencyChecker(NexKVModel, 0, "")
	result := checker.CheckOperations(ops)
	require.True(t, result.Ok)
}

// TestRecordingE2ETestScenario 测试 RecordingE2ETestScenario
func TestRecordingE2ETestScenario(t *testing.T) {
	// 模拟一个简单的测试场景
	nodes := []string{"node-1", "node-2"}
	recScenario := NewRecordingE2ETestScenario(nodes)

	// 为每个节点添加 mock KV
	for _, nodeID := range nodes {
		recScenario.AddNode(nodeID, newMockScenarioKV())
	}

	// 验证初始化
	require.NotNil(t, recScenario)
	require.Len(t, recScenario.Nodes, 2)
	require.NotNil(t, recScenario.RecordingClients)
	require.Len(t, recScenario.RecordingClients, 2)
	require.NotNil(t, recScenario.Checker)

	// 验证每个节点都有 RecordingClient
	for _, nodeID := range nodes {
		_, exists := recScenario.RecordingClients[nodeID]
		require.True(t, exists, "Node %s should have a RecordingClient", nodeID)
	}
}

// TestRecordingE2ETestScenario_Operations 测试操作记录
func TestRecordingE2ETestScenario_Operations(t *testing.T) {
	nodes := []string{"node-1", "node-2"}
	recScenario := NewRecordingE2ETestScenario(nodes)

	// 为每个节点添加 mock KV
	for _, nodeID := range nodes {
		recScenario.AddNode(nodeID, newMockScenarioKV())
	}

	ctx := context.Background()

	// 在 node-1 上执行 Put
	client1 := recScenario.RecordingClients["node-1"]
	err := client1.Put(ctx, "ns1", "key1", []byte("value1"))
	require.NoError(t, err)

	// 在 node-2 上执行 Get（由于是独立的 mock，不会读到数据）
	client2 := recScenario.RecordingClients["node-2"]
	_, _ = client2.Get(ctx, "ns1", "key1") // 可能返回错误，但不影响记录测试

	// 验证操作被记录
	ops1 := recScenario.Recorders["node-1"].GetOperations()
	require.Len(t, ops1, 1)

	// 验证 node-2 的操作也被记录
	ops2 := recScenario.Recorders["node-2"].GetOperations()
	require.Len(t, ops2, 1)

	// 收集所有操作并验证
	allOps := recScenario.GetAllOperations()
	require.Len(t, allOps, 2)

	// 使用 client2 变量避免编译错误
	_ = client2
}

// TestRecordingE2ETestScenario_VerifyLinearizability 测试线性化验证
func TestRecordingE2ETestScenario_VerifyLinearizability(t *testing.T) {
	nodes := []string{"node-1"}
	recScenario := NewRecordingE2ETestScenario(nodes)

	// 为节点添加 mock KV
	recScenario.AddNode("node-1", newMockScenarioKV())

	ctx := context.Background()

	// 执行一系列操作
	client := recScenario.RecordingClients["node-1"]
	_ = client.Put(ctx, "ns1", "k1", []byte("v1"))
	_, _ = client.Get(ctx, "ns1", "k1")
	_ = client.Delete(ctx, "ns1", "k1")

	// 验证线性化
	result := recScenario.VerifyLinearizability()
	require.True(t, result.Ok, "Operations should be linearizable: %s", result.Error)
}
