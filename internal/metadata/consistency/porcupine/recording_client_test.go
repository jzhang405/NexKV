// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件包含记录客户端的单元测试
package porcupine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// errKeyNotFound key 不存在错误
var errKeyNotFound = errors.New(ErrKeyNotFound)

// mockKVClient 模拟 KV 客户端（线程安全）
type mockKVClient struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMockKVClient() *mockKVClient {
	return &mockKVClient{
		data: make(map[string][]byte),
	}
}

func (m *mockKVClient) Put(ctx context.Context, ns, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[ns+":"+key] = value
	return nil
}

func (m *mockKVClient) Get(ctx context.Context, ns, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.data[ns+":"+key]
	if !ok {
		return nil, errKeyNotFound
	}
	return val, nil
}

func (m *mockKVClient) Delete(ctx context.Context, ns, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, ns+":"+key)
	return nil
}

// TestRecordingClient_Put 测试 Put 操作记录
func TestRecordingClient_Put(t *testing.T) {
	mockKV := newMockKVClient()
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)
	client := NewRecordingClient(mockKV, recorder)

	ctx := context.Background()
	err := client.Put(ctx, "ns1", "key1", []byte("value1"))
	require.NoError(t, err)

	// 验证数据被写入
	val, err := mockKV.Get(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte("value1"), val)

	// 验证操作被记录
	ops := recorder.GetOperations()
	require.Len(t, ops, 1)
	require.Equal(t, OpPut, ops[0].Input.(NexKVInput).Op)
}

// TestRecordingClient_Get 测试 Get 操作记录
func TestRecordingClient_Get(t *testing.T) {
	mockKV := newMockKVClient()
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)
	client := NewRecordingClient(mockKV, recorder)

	ctx := context.Background()

	// 先写入数据
	_ = mockKV.Put(ctx, "ns1", "key1", []byte("value1"))

	// 读取
	val, err := client.Get(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte("value1"), val)

	// 验证操作被记录
	ops := recorder.GetOperations()
	require.Len(t, ops, 1)
	require.Equal(t, OpGet, ops[0].Input.(NexKVInput).Op)
}

// TestRecordingClient_Delete 测试 Delete 操作记录
func TestRecordingClient_Delete(t *testing.T) {
	mockKV := newMockKVClient()
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)
	client := NewRecordingClient(mockKV, recorder)

	ctx := context.Background()

	// 先写入数据
	_ = mockKV.Put(ctx, "ns1", "key1", []byte("value1"))

	// 删除
	err := client.Delete(ctx, "ns1", "key1")
	require.NoError(t, err)

	// 验证操作被记录
	ops := recorder.GetOperations()
	require.Len(t, ops, 1)
	require.Equal(t, OpDelete, ops[0].Input.(NexKVInput).Op)
}

// TestRecordingClient_QuorumPut 测试 QuorumPut 操作记录
func TestRecordingClient_QuorumPut(t *testing.T) {
	mockKV := newMockKVClient()
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)
	client := NewRecordingClient(mockKV, recorder)

	ctx := context.Background()
	err := client.QuorumPut(ctx, "ns1", "key1", []byte("quorum-value"))
	require.NoError(t, err)

	// 验证操作被记录
	ops := recorder.GetOperations()
	require.Len(t, ops, 1)
	require.Equal(t, OpQuorumPut, ops[0].Input.(NexKVInput).Op)
}

// TestRecordingClient_QuorumGet 测试 QuorumGet 操作记录
func TestRecordingClient_QuorumGet(t *testing.T) {
	mockKV := newMockKVClient()
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)
	client := NewRecordingClient(mockKV, recorder)

	ctx := context.Background()

	// 先写入数据
	_ = mockKV.Put(ctx, "ns1", "key1", []byte("value1"))

	// QuorumGet
	val, err := client.QuorumGet(ctx, "ns1", "key1")
	require.NoError(t, err)
	require.Equal(t, []byte("value1"), val)

	// 验证操作被记录
	ops := recorder.GetOperations()
	require.Len(t, ops, 1)
	require.Equal(t, OpQuorumGet, ops[0].Input.(NexKVInput).Op)
}

// TestRecordingClient_GetRecorder 测试获取记录器
func TestRecordingClient_GetRecorder(t *testing.T) {
	mockKV := newMockKVClient()
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)
	client := NewRecordingClient(mockKV, recorder)

	require.Equal(t, recorder, client.GetRecorder())
}

// TestRecordingClient_MultipleOperations 测试多个操作
func TestRecordingClient_MultipleOperations(t *testing.T) {
	mockKV := newMockKVClient()
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)
	client := NewRecordingClient(mockKV, recorder)

	ctx := context.Background()

	// 执行多个操作
	_ = client.Put(ctx, "ns1", "k1", []byte("v1"))
	_, _ = client.Get(ctx, "ns1", "k1")
	_ = client.Put(ctx, "ns1", "k2", []byte("v2"))
	_ = client.Delete(ctx, "ns1", "k1")

	// 验证所有操作被记录
	ops := recorder.GetOperations()
	require.Len(t, ops, 4)
}

// TestRecordingClient_LinearizabilityCheck 测试完整的线性化检查流程
func TestRecordingClient_LinearizabilityCheck(t *testing.T) {
	mockKV := newMockKVClient()
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)
	client := NewRecordingClient(mockKV, recorder)
	checker := NewConsistencyChecker(NexKVModel, 0, "")

	ctx := context.Background()

	// 执行操作
	_ = client.Put(ctx, "ns1", "k1", []byte("v1"))
	_, _ = client.Get(ctx, "ns1", "k1")
	_ = client.Delete(ctx, "ns1", "k1")

	// 检查线性化
	result := checker.CheckFromRecorder(recorder)
	require.True(t, result.Ok, "Operations should be linearizable: %s", result.Error)
}

// TestRecordingClient_ErrorHandling 测试错误处理
func TestRecordingClient_ErrorHandling(t *testing.T) {
	mockKV := newMockKVClient()
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)
	client := NewRecordingClient(mockKV, recorder)

	ctx := context.Background()

	// 读取不存在的 key
	_, err := client.Get(ctx, "ns1", "nonexistent")
	require.Error(t, err)

	// 验证操作被记录（即使失败）
	ops := recorder.GetOperations()
	require.Len(t, ops, 1)

	// 验证输出表示失败
	output := ops[0].Output.(NexKVOutput)
	require.False(t, output.Ok)
	require.Equal(t, ErrKeyNotFound, output.Error)
}
