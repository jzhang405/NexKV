// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件包含状态模型的单元测试
package porcupine

import (
	"testing"

	"github.com/anishathalye/porcupine"
	"github.com/stretchr/testify/require"
)

// TestNexKVModel_PutAndGet 测试 Put 和 Get 操作的状态转移
func TestNexKVModel_PutAndGet(t *testing.T) {
	model := NexKVModel
	state := model.Init()

	// Put 操作
	input := NexKVInput{Op: OpPut, Key: "test-key", Value: []byte("test-value")}
	output := NexKVOutput{Ok: true}

	ok, newState := model.Step(state, input, output)
	require.True(t, ok, "Put operation should succeed")
	require.NotNil(t, newState)

	// Get 操作
	state = newState
	input = NexKVInput{Op: OpGet, Key: "test-key"}
	output = NexKVOutput{Ok: true, Value: []byte("test-value")}

	ok, newState = model.Step(state, input, output)
	require.True(t, ok, "Get operation should succeed")
	require.Equal(t, []byte("test-value"), output.Value)
}

// TestNexKVModel_GetNonExistent 测试读取不存在的 key
func TestNexKVModel_GetNonExistent(t *testing.T) {
	model := NexKVModel
	state := model.Init()

	// Get 不存在的 key
	input := NexKVInput{Op: OpGet, Key: "non-existent"}
	output := NexKVOutput{Ok: false, Error: ErrKeyNotFound}

	ok, newState := model.Step(state, input, output)
	require.True(t, ok, "Get non-existent key should be valid")
	// 状态应该保持不变（返回原 state，不是 nil）
	require.NotNil(t, newState)
}

// TestNexKVModel_Delete 测试 Delete 操作
func TestNexKVModel_Delete(t *testing.T) {
	model := NexKVModel
	state := model.Init()

	// Put
	input := NexKVInput{Op: OpPut, Key: "delete-key", Value: []byte("value")}
	output := NexKVOutput{Ok: true}
	ok, state := model.Step(state, input, output)
	require.True(t, ok)

	// Delete
	input = NexKVInput{Op: OpDelete, Key: "delete-key"}
	output = NexKVOutput{Ok: true}
	ok, state = model.Step(state, input, output)
	require.True(t, ok)

	// Get 已删除的 key 应该返回不存在
	input = NexKVInput{Op: OpGet, Key: "delete-key"}
	output = NexKVOutput{Ok: false, Error: ErrKeyNotFound}
	ok, _ = model.Step(state, input, output)
	require.True(t, ok)
}

// TestNexKVModel_PutOverwrite 测试覆盖写入
func TestNexKVModel_PutOverwrite(t *testing.T) {
	model := NexKVModel
	state := model.Init()

	// 第一次 Put
	input := NexKVInput{Op: OpPut, Key: "overwrite-key", Value: []byte("value1")}
	output := NexKVOutput{Ok: true}
	ok, state := model.Step(state, input, output)
	require.True(t, ok)

	// 覆盖 Put
	input = NexKVInput{Op: OpPut, Key: "overwrite-key", Value: []byte("value2")}
	output = NexKVOutput{Ok: true}
	ok, state = model.Step(state, input, output)
	require.True(t, ok)

	// Get 应该返回新值
	input = NexKVInput{Op: OpGet, Key: "overwrite-key"}
	output = NexKVOutput{Ok: true, Value: []byte("value2")}
	ok, _ = model.Step(state, input, output)
	require.True(t, ok)
}

// TestNexKVModel_InvalidTransition 测试无效状态转移
func TestNexKVModel_InvalidTransition(t *testing.T) {
	model := NexKVModel
	state := model.Init()

	// Put value1
	input := NexKVInput{Op: OpPut, Key: "key", Value: []byte("value1")}
	output := NexKVOutput{Ok: true}
	_, state = model.Step(state, input, output)

	// Get 返回错误的值应该失败
	input = NexKVInput{Op: OpGet, Key: "key"}
	output = NexKVOutput{Ok: true, Value: []byte("wrong-value")}
	ok, _ := model.Step(state, input, output)
	require.False(t, ok, "Get with wrong value should fail validation")
}

// TestNexKVModel_WithNamespace 测试命名空间
func TestNexKVModel_WithNamespace(t *testing.T) {
	model := NexKVModel
	state := model.Init()

	// Put to namespace1
	input := NexKVInput{Op: OpPut, Namespace: "ns1", Key: "key", Value: []byte("value1")}
	output := NexKVOutput{Ok: true}
	ok, state := model.Step(state, input, output)
	require.True(t, ok)

	// Put to namespace2 with same key
	input = NexKVInput{Op: OpPut, Namespace: "ns2", Key: "key", Value: []byte("value2")}
	output = NexKVOutput{Ok: true}
	ok, state = model.Step(state, input, output)
	require.True(t, ok)

	// Get from namespace1 should return value1
	input = NexKVInput{Op: OpGet, Namespace: "ns1", Key: "key"}
	output = NexKVOutput{Ok: true, Value: []byte("value1")}
	ok, _ = model.Step(state, input, output)
	require.True(t, ok)

	// Get from namespace2 should return value2
	input = NexKVInput{Op: OpGet, Namespace: "ns2", Key: "key"}
	output = NexKVOutput{Ok: true, Value: []byte("value2")}
	ok, _ = model.Step(state, input, output)
	require.True(t, ok)
}

// TestNexKVModel_QuorumWrite 测试 Quorum 写入操作
func TestNexKVModel_QuorumWrite(t *testing.T) {
	model := NexKVModel
	state := model.Init()

	// QuorumPut
	input := NexKVInput{Op: OpQuorumPut, Key: "quorum-key", Value: []byte("quorum-value")}
	output := NexKVOutput{Ok: true}
	ok, state := model.Step(state, input, output)
	require.True(t, ok)

	// QuorumGet
	input = NexKVInput{Op: OpQuorumGet, Key: "quorum-key"}
	output = NexKVOutput{Ok: true, Value: []byte("quorum-value")}
	ok, _ = model.Step(state, input, output)
	require.True(t, ok)
}

// TestNexKVModel_IsPorcupineModel 测试模型满足 porcupine.Model 接口
func TestNexKVModel_IsPorcupineModel(t *testing.T) {
	var _ porcupine.Model = NexKVModel
}

// TestNexKVInput_KeyWithNamespace 测试带命名空间的 key 生成
func TestNexKVInput_KeyWithNamespace(t *testing.T) {
	input := NexKVInput{Namespace: "test-ns", Key: "test-key"}
	expected := "test-ns:test-key"
	require.Equal(t, expected, input.KeyWithNamespace())
}

// TestNexKVInput_KeyWithNamespace_Empty 测试空命名空间
func TestNexKVInput_KeyWithNamespace_Empty(t *testing.T) {
	input := NexKVInput{Namespace: "", Key: "test-key"}
	expected := "test-key"
	require.Equal(t, expected, input.KeyWithNamespace())
}
