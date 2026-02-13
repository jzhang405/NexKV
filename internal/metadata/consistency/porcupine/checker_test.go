// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件包含一致性检查器的单元测试
package porcupine

import (
	"os"
	"testing"
	"time"

	"github.com/anishathalye/porcupine"
	"github.com/stretchr/testify/require"
)

// TestConsistencyChecker_Check_Valid 测试有效的线性化历史
func TestConsistencyChecker_Check_Valid(t *testing.T) {
	checker := NewConsistencyChecker(NexKVModel, time.Minute, "")

	// 创建一个简单的线性化历史
	ops := []porcupine.Operation{
		{
			ClientId: 0,
			Input:    NexKVInput{Op: OpPut, Key: "k1", Value: []byte("v1")},
			Call:     1,
			Output:   NexKVOutput{Ok: true},
			Return:   2,
		},
		{
			ClientId: 0,
			Input:    NexKVInput{Op: OpGet, Key: "k1"},
			Call:     3,
			Output:   NexKVOutput{Ok: true, Value: []byte("v1")},
			Return:   4,
		},
	}

	result := checker.CheckOperations(ops)
	require.True(t, result.Ok, "Linearizable history should pass: %s", result.Error)
}

// TestConsistencyChecker_Check_Invalid 测试无效的线性化历史
func TestConsistencyChecker_Check_Invalid(t *testing.T) {
	// 创建一个非线性化的历史比较复杂
	// Porcupine 会尝试找到有效的线性化点
	// 这里我们测试一个简单的场景：读取到错误的值
	checker := NewConsistencyChecker(NexKVModel, time.Minute, "")

	// 写入 v1，但读取返回 v2（违反状态模型）
	ops := []porcupine.Operation{
		{
			ClientId: 0,
			Input:    NexKVInput{Op: OpPut, Key: "k1", Value: []byte("v1")},
			Call:     1,
			Output:   NexKVOutput{Ok: true},
			Return:   2,
		},
		{
			ClientId: 0,
			Input:    NexKVInput{Op: OpGet, Key: "k1"},
			Call:     3,
			Output:   NexKVOutput{Ok: true, Value: []byte("v2")}, // 错误的值！
			Return:   4,
		},
	}

	result := checker.CheckOperations(ops)
	// 这应该是 Illegal，因为输出违反了状态模型
	require.False(t, result.Ok, "Reading wrong value should fail linearizability")
}

// TestConsistencyChecker_Check_Empty 测试空历史
func TestConsistencyChecker_Check_Empty(t *testing.T) {
	checker := NewConsistencyChecker(NexKVModel, time.Minute, "")

	ops := []porcupine.Operation{}
	result := checker.CheckOperations(ops)
	require.True(t, result.Ok, "Empty history should be linearizable")
}

// TestConsistencyChecker_Check_Concurrent 测试并发操作
func TestConsistencyChecker_Check_Concurrent(t *testing.T) {
	checker := NewConsistencyChecker(NexKVModel, time.Minute, "")

	// 两个客户端并发写入同一个 key
	ops := []porcupine.Operation{
		{
			ClientId: 0,
			Input:    NexKVInput{Op: OpPut, Key: "k1", Value: []byte("v1")},
			Call:     1,
			Output:   NexKVOutput{Ok: true},
			Return:   5,
		},
		{
			ClientId: 1,
			Input:    NexKVInput{Op: OpPut, Key: "k1", Value: []byte("v2")},
			Call:     2,
			Output:   NexKVOutput{Ok: true},
			Return:   4,
		},
		{
			ClientId: 0,
			Input:    NexKVInput{Op: OpGet, Key: "k1"},
			Call:     6,
			Output:   NexKVOutput{Ok: true, Value: []byte("v1")}, // 或者 v2
			Return:   7,
		},
	}

	result := checker.CheckOperations(ops)
	// 并发写入后，读取任意一个值都是有效的
	require.True(t, result.Ok, "Concurrent writes should be linearizable: %s", result.Error)
}

// TestConsistencyChecker_WithReport 测试报告生成
func TestConsistencyChecker_WithReport(t *testing.T) {
	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "porcupine-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	checker := NewConsistencyChecker(NexKVModel, time.Minute, tmpDir)

	ops := []porcupine.Operation{
		{
			ClientId: 0,
			Input:    NexKVInput{Op: OpPut, Key: "k1", Value: []byte("v1")},
			Call:     1,
			Output:   NexKVOutput{Ok: true},
			Return:   2,
		},
	}

	result := checker.CheckOperations(ops)
	require.True(t, result.Ok)
	// 报告路径可能为空（只有失败时才生成）
}

// TestConsistencyChecker_Timeout 测试超时
func TestConsistencyChecker_Timeout(t *testing.T) {
	// 使用非常短的超时
	checker := NewConsistencyChecker(NexKVModel, 1*time.Nanosecond, "")

	// 创建大量操作来触发超时
	ops := make([]porcupine.Operation, 1000)
	for i := 0; i < 1000; i++ {
		ops[i] = porcupine.Operation{
			ClientId: i % 10,
			Input:    NexKVInput{Op: OpPut, Key: "k1", Value: []byte("v")},
			Call:     int64(i * 2),
			Output:   NexKVOutput{Ok: true},
			Return:   int64(i*2 + 1),
		}
	}

	// 即使超时，也应该返回结果（可能是 Unknown）
	result := checker.CheckOperations(ops)
	require.NotNil(t, result)
}

// TestCheckResult 测试 CheckResult 的方法
func TestCheckResult(t *testing.T) {
	// 测试 IsOk
	result := CheckResult{Ok: true}
	require.True(t, result.IsOk())

	result = CheckResult{Ok: false, Error: "test error"}
	require.False(t, result.IsOk())

	// 测试 String
	require.NotEmpty(t, result.String())
}

// TestNewConsistencyChecker 测试构造函数
func TestNewConsistencyChecker(t *testing.T) {
	checker := NewConsistencyChecker(NexKVModel, time.Minute, "/tmp")
	require.NotNil(t, checker)
	// 验证模型可用（通过执行一个简单检查）
	ops := []porcupine.Operation{
		{
			ClientId: 0,
			Input:    NexKVInput{Op: OpPut, Key: "test", Value: []byte("v")},
			Call:     1,
			Output:   NexKVOutput{Ok: true},
			Return:   2,
		},
	}
	result := checker.CheckOperations(ops)
	require.True(t, result.Ok)
}

// TestConsistencyChecker_CheckFromRecorder 测试从 Recorder 检查
func TestConsistencyChecker_CheckFromRecorder(t *testing.T) {
	checker := NewConsistencyChecker(NexKVModel, time.Minute, "")
	ts := NewMonotonicTimestamp()
	recorder := NewHistoryRecorder("client-1", ts)

	// 记录操作
	opID1 := recorder.RecordCall(OpPut, "ns1", "k1", []byte("v1"))
	recorder.RecordReturn(opID1, true, nil, "")

	opID2 := recorder.RecordCall(OpGet, "ns1", "k1", nil)
	recorder.RecordReturn(opID2, true, []byte("v1"), "")

	// 从 recorder 获取操作并检查
	ops := recorder.GetOperations()
	result := checker.CheckOperations(ops)
	require.True(t, result.Ok, "Recorder history should be linearizable: %s", result.Error)
}
