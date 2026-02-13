// Package porcupine 测试 Quorum 线性一致性检查器
package porcupine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// mockQuorumKV 模拟 Quorum KV 操作
type mockQuorumKV struct {
	data map[string][]byte
}

func newMockQuorumKV() *mockQuorumKV {
	return &mockQuorumKV{
		data: make(map[string][]byte),
	}
}

func (m *mockQuorumKV) Put(ctx context.Context, namespace, key string, value []byte) error {
	m.data[namespace+":"+key] = value
	return nil
}

func (m *mockQuorumKV) Get(ctx context.Context, namespace, key string) ([]byte, error) {
	return m.data[namespace+":"+key], nil
}

func (m *mockQuorumKV) Delete(ctx context.Context, namespace, key string) error {
	delete(m.data, namespace+":"+key)
	return nil
}

// TestQuorumLinearizabilityChecker_GetTimestamp 测试时间戳生成
func TestQuorumLinearizabilityChecker_GetTimestamp(t *testing.T) {
	checker := NewQuorumLinearizabilityChecker(NexKVModel, "test-node")

	var last int64
	for i := 0; i < 100; i++ {
		ts := checker.GetTimestamp()

		// 验证单调递增
		if last > 0 && ts <= last {
			t.Errorf("时间戳不单调递增: last=%d, ts=%d", last, ts)
		}
		last = ts
	}
}

// TestRecordingQuorumScenario_BasicOperations 测试基本操作
func TestRecordingQuorumScenario_BasicOperations(t *testing.T) {
	kv := newMockQuorumKV()
	scenario := NewRecordingQuorumScenario("test-node", kv)

	ctx := context.Background()

	// 执行操作
	err := scenario.Client.Put(ctx, "ns1", "key1", []byte("value1"))
	require.NoError(t, err)

	val, err := scenario.Client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte("value1"), val)

	// 验证记录了操作
	ops := scenario.Recorder.GetOperations()
	require.Len(t, ops, 2, "应该记录了 2 个操作")
}

// TestRecordingQuorumScenario_VerifyLinearizability 测试线性一致性验证
func TestRecordingQuorumScenario_VerifyLinearizability(t *testing.T) {
	kv := newMockQuorumKV()
	scenario := NewRecordingQuorumScenario("test-node", kv)

	ctx := context.Background()

	// 执行线性化操作序列
	require.NoError(t, scenario.Client.Put(ctx, "ns1", "key1", []byte("value1")))
	_, err := scenario.Client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.NoError(t, scenario.Client.Put(ctx, "ns1", "key1", []byte("value2")))
	_, err = scenario.Client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)

	// 验证线性一致性
	result := scenario.VerifyLinearizability()
	require.True(t, result.Ok, "简单操作序列应该是线性一致的")
}

// TestRecordingQuorumScenario_VerifyLinearizabilityWithVis 测试带可视化的验证
func TestRecordingQuorumScenario_VerifyLinearizabilityWithVis(t *testing.T) {
	kv := newMockQuorumKV()
	scenario := NewRecordingQuorumScenario("test-node", kv)

	ctx := context.Background()

	// 执行操作
	require.NoError(t, scenario.Client.Put(ctx, "ns1", "key1", []byte("value1")))
	_, err := scenario.Client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)

	// 验证线性一致性（带可视化）
	result, visPath := scenario.VerifyLinearizabilityWithVis()
	require.True(t, result.Ok, "操作序列应该是线性一致的")
	require.Empty(t, visPath, "线性一致时不应生成可视化文件")
}

// TestRecordingQuorumScenario_EmptyOperations 测试空操作序列
func TestRecordingQuorumScenario_EmptyOperations(t *testing.T) {
	kv := newMockQuorumKV()
	scenario := NewRecordingQuorumScenario("test-node", kv)

	// 不执行任何操作
	result := scenario.VerifyLinearizability()
	require.True(t, result.Ok, "空操作序列应该是线性一致的")

	result, visPath := scenario.VerifyLinearizabilityWithVis()
	require.True(t, result.Ok)
	require.Empty(t, visPath)
}

// TestRecordingQuorumScenario_Clear 测试清空记录
func TestRecordingQuorumScenario_Clear(t *testing.T) {
	kv := newMockQuorumKV()
	scenario := NewRecordingQuorumScenario("test-node", kv)

	ctx := context.Background()

	// 执行操作
	require.NoError(t, scenario.Client.Put(ctx, "ns1", "key1", []byte("value1")))

	// 清空
	scenario.Clear()

	// 验证记录已清空
	ops := scenario.Recorder.GetOperations()
	require.Len(t, ops, 0, "清空后应该没有操作记录")
}

// TestRecordingQuorumScenario_Delete 测试删除操作
func TestRecordingQuorumScenario_Delete(t *testing.T) {
	kv := newMockQuorumKV()
	scenario := NewRecordingQuorumScenario("test-node", kv)

	ctx := context.Background()

	// 执行删除操作
	require.NoError(t, scenario.Client.Put(ctx, "ns1", "key1", []byte("value1")))
	require.NoError(t, scenario.Client.Delete(ctx, "ns1", "key1"))

	// 验证线性一致性
	result := scenario.VerifyLinearizability()
	require.True(t, result.Ok, "删除操作序列应该是线性一致的")
}

// BenchmarkQuorumLinearizabilityChecker 性能基准测试
func BenchmarkQuorumLinearizabilityChecker(b *testing.B) {
	checker := NewQuorumLinearizabilityChecker(NexKVModel, "bench-node")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = checker.GetTimestamp()
	}
}

// BenchmarkRecordingQuorumScenario 性能基准测试
func BenchmarkRecordingQuorumScenario(b *testing.B) {
	kv := newMockQuorumKV()
	scenario := NewRecordingQuorumScenario("bench-node", kv)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = scenario.Client.Put(ctx, "ns1", "key1", []byte("value1"))
		_, _ = scenario.Client.Get(ctx, "ns1", "key1")
		scenario.Clear()
	}
}
