// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件包含历史记录器的单元测试
package porcupine

import (
	"sync"
	"testing"

	"github.com/anishathalye/porcupine"
	"github.com/stretchr/testify/require"
)

// TestHistoryRecorder_RecordCallAndReturn 测试基本的 Call/Return 记录
func TestHistoryRecorder_RecordCallAndReturn(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	// 记录 Call
	opID := recorder.RecordCall(OpPut, "ns1", "key1", []byte("value1"))
	require.Equal(t, 0, opID)

	// 记录 Return
	recorder.RecordReturn(opID, true, []byte("value1"), "")

	// 获取操作
	ops := recorder.GetOperations()
	require.Len(t, ops, 1)

	// 验证操作
	op := ops[0]
	// ClientId 是从 "client-1" 提取的数字 1
	require.Equal(t, 1, op.ClientId)
	require.NotNil(t, op.Input)

	input := op.Input.(NexKVInput)
	require.Equal(t, OpPut, input.Op)
	require.Equal(t, "ns1", input.Namespace)
	require.Equal(t, "key1", input.Key)
}

// TestHistoryRecorder_MultipleOperations 测试多个操作记录
func TestHistoryRecorder_MultipleOperations(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	// 记录 Put
	opID1 := recorder.RecordCall(OpPut, "ns1", "key1", []byte("value1"))
	recorder.RecordReturn(opID1, true, nil, "")

	// 记录 Get
	opID2 := recorder.RecordCall(OpGet, "ns1", "key1", nil)
	recorder.RecordReturn(opID2, true, []byte("value1"), "")

	// 记录 Delete
	opID3 := recorder.RecordCall(OpDelete, "ns1", "key1", nil)
	recorder.RecordReturn(opID3, true, nil, "")

	ops := recorder.GetOperations()
	require.Len(t, ops, 3)
}

// TestHistoryRecorder_TimestampOrdering 测试时间戳顺序
func TestHistoryRecorder_TimestampOrdering(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	// 记录多个操作
	opID1 := recorder.RecordCall(OpPut, "ns1", "key1", []byte("value1"))
	recorder.RecordReturn(opID1, true, nil, "")

	opID2 := recorder.RecordCall(OpGet, "ns1", "key1", nil)
	recorder.RecordReturn(opID2, true, []byte("value1"), "")

	ops := recorder.GetOperations()
	require.Len(t, ops, 2)

	// 验证时间戳递增
	require.True(t, ops[0].Call <= ops[0].Return, "Call <= Return for op1")
	require.True(t, ops[0].Return <= ops[1].Call, "op1.Return <= op2.Call")
	require.True(t, ops[1].Call <= ops[1].Return, "Call <= Return for op2")
}

// TestHistoryRecorder_Concurrent 测试并发记录
func TestHistoryRecorder_Concurrent(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	var wg sync.WaitGroup
	numOps := 100

	// 并发记录操作
	for i := 0; i < numOps; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			opID := recorder.RecordCall(OpPut, "ns1", "key", []byte("value"))
			recorder.RecordReturn(opID, true, nil, "")
		}(i)
	}

	wg.Wait()

	ops := recorder.GetOperations()
	require.Len(t, ops, numOps)
}

// TestHistoryRecorder_Clear 测试清空操作
func TestHistoryRecorder_Clear(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	// 记录一些操作
	opID := recorder.RecordCall(OpPut, "ns1", "key1", []byte("value1"))
	recorder.RecordReturn(opID, true, nil, "")

	require.Len(t, recorder.GetOperations(), 1)

	// 清空
	recorder.Clear()
	require.Len(t, recorder.GetOperations(), 0)
}

// TestHistoryRecorder_GetInput 测试获取输入
func TestHistoryRecorder_GetInput(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	opID := recorder.RecordCall(OpPut, "test-ns", "test-key", []byte("test-value"))

	input := recorder.GetInput(opID)
	require.NotNil(t, input)
	require.Equal(t, OpPut, input.Op)
	require.Equal(t, "test-ns", input.Namespace)
	require.Equal(t, "test-key", input.Key)
	require.Equal(t, []byte("test-value"), input.Value)

	// 完成操作后 GetInput 应返回 nil
	recorder.RecordReturn(opID, true, nil, "")
	input = recorder.GetInput(opID)
	require.Nil(t, input)
}

// TestHistoryRecorder_GetInput_NotFound 测试获取不存在的输入
func TestHistoryRecorder_GetInput_NotFound(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	input := recorder.GetInput(999)
	require.Nil(t, input)
}

// TestHistoryRecorder_ClientID 测试客户端 ID
func TestHistoryRecorder_ClientID(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder1 := NewHistoryRecorder("client-1", ts)
	recorder2 := NewHistoryRecorder("client-2", ts)

	// 两个 recorder 应该有不同的 client ID
	id1 := recorder1.ClientID()
	id2 := recorder2.ClientID()
	require.NotEqual(t, id1, id2)
}

// TestHistoryRecorder_QuorumOperations 测试 Quorum 操作记录
func TestHistoryRecorder_QuorumOperations(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	// QuorumPut
	opID1 := recorder.RecordCall(OpQuorumPut, "ns1", "key1", []byte("quorum-value"))
	recorder.RecordReturn(opID1, true, nil, "")

	// QuorumGet
	opID2 := recorder.RecordCall(OpQuorumGet, "ns1", "key1", nil)
	recorder.RecordReturn(opID2, true, []byte("quorum-value"), "")

	ops := recorder.GetOperations()
	require.Len(t, ops, 2)

	// 验证 Quorum 操作类型
	input1 := ops[0].Input.(NexKVInput)
	require.Equal(t, OpQuorumPut, input1.Op)

	input2 := ops[1].Input.(NexKVInput)
	require.Equal(t, OpQuorumGet, input2.Op)
}

// TestHistoryRecorder_ErrorOutput 测试错误输出记录
func TestHistoryRecorder_ErrorOutput(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	// 记录一个失败的操作
	opID := recorder.RecordCall(OpGet, "ns1", "non-existent", nil)
	recorder.RecordReturn(opID, false, nil, ErrKeyNotFound)

	ops := recorder.GetOperations()
	require.Len(t, ops, 1)

	// 验证输出
	output := ops[0].Output.(NexKVOutput)
	require.False(t, output.Ok)
	require.Equal(t, ErrKeyNotFound, output.Error)
}

// TestHistoryRecorder_GetEvents 测试转换为 Event 格式
func TestHistoryRecorder_GetEvents(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	// 记录一个操作
	opID := recorder.RecordCall(OpPut, "ns1", "key1", []byte("value1"))
	recorder.RecordReturn(opID, true, nil, "")

	// 获取事件
	events := recorder.GetEvents()
	require.Len(t, events, 2) // Call + Return

	// 验证事件类型
	require.Equal(t, porcupine.CallEvent, events[0].Kind)
	require.Equal(t, porcupine.ReturnEvent, events[1].Kind)
}

// TestHistoryRecorder_Len 测试操作计数
func TestHistoryRecorder_Len(t *testing.T) {
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	require.Equal(t, 0, recorder.Len())

	opID1 := recorder.RecordCall(OpPut, "ns1", "key1", []byte("value1"))
	require.Equal(t, 0, recorder.Len()) // 未完成

	recorder.RecordReturn(opID1, true, nil, "")
	require.Equal(t, 1, recorder.Len()) // 已完成
}
