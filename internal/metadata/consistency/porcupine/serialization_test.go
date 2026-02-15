// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件测试操作序列化功能
package porcupine

import (
	"encoding/json"
	"testing"

	"github.com/anishathalye/porcupine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== 序列化测试 ====================

func TestSerializeTopologyOperation(t *testing.T) {
	serializer := NewOperationSerializer()

	// 创建测试操作
	op := porcupine.Operation{
		ClientId: 1,
		Input: EnhancedInput{
			Type: OpTypeTopology,
			TopologyOp: TopologyOperation{
				Type:         TopologyOpWriteQuorum,
				NodeID:       "node-1",
				Key:          "test-key",
				Value:        []byte("test-value"),
				Version:      1,
				Term:         1,
				Participants: []string{"node-1", "node-2", "node-3"},
			},
		},
		Output: EnhancedOutput{
			Type: OpTypeTopology,
			TopologyOut: TopologyOutput{
				Ok:      true,
				Value:   []byte("result"),
				Version: 2,
				Error:   "",
			},
		},
		Call:   1000,
		Return: 2000,
	}

	// 序列化
	serialized, err := serializer.SerializeOperation(op)
	require.NoError(t, err)
	assert.Equal(t, 1, serialized.ClientID)
	assert.Equal(t, int64(1000), serialized.Call)
	assert.Equal(t, int64(2000), serialized.Return)
	assert.Equal(t, OpTypeTopology, serialized.ModelType)

	// 验证 JSON 可以正常序列化
	jsonBytes, err := json.Marshal(serialized)
	require.NoError(t, err)
	t.Logf("Serialized JSON: %s", string(jsonBytes))

	// 反序列化
	deserialized, err := serializer.DeserializeOperation(serialized)
	require.NoError(t, err)

	// 验证反序列化结果
	assert.Equal(t, op.ClientId, deserialized.ClientId)
	assert.Equal(t, op.Call, deserialized.Call)
	assert.Equal(t, op.Return, deserialized.Return)

	// 验证输入
	input := deserialized.Input.(EnhancedInput)
	assert.Equal(t, OpTypeTopology, input.Type)
	assert.Equal(t, TopologyOpWriteQuorum, input.TopologyOp.Type)
	assert.Equal(t, "node-1", input.TopologyOp.NodeID)
	assert.Equal(t, "test-key", input.TopologyOp.Key)
	assert.Equal(t, []byte("test-value"), input.TopologyOp.Value)

	// 验证输出
	output := deserialized.Output.(EnhancedOutput)
	assert.Equal(t, OpTypeTopology, output.Type)
	assert.True(t, output.TopologyOut.Ok)
	assert.Equal(t, []byte("result"), output.TopologyOut.Value)
}

func TestSerializeFailureRecoveryOperation(t *testing.T) {
	serializer := NewOperationSerializer()

	op := porcupine.Operation{
		ClientId: 2,
		Input: EnhancedInput{
			Type: OpTypeFailureRecovery,
			FailureRecoveryOp: FailureRecoveryOperation{
				Type:         FailureRecoveryOpQuorumWrite,
				NodeID:       "node-2",
				Key:          "fr-key",
				Value:        []byte("fr-value"),
				Version:      5,
				Participants: []string{"node-1", "node-2"},
				FailedNodes:  []string{"node-3"},
			},
		},
		Output: EnhancedOutput{
			Type: OpTypeFailureRecovery,
			FailureRecoveryOut: FailureRecoveryOutput{
				Ok:      true,
				Value:   nil,
				Version: 6,
				Error:   "",
			},
		},
		Call:   3000,
		Return: 4000,
	}

	serialized, err := serializer.SerializeOperation(op)
	require.NoError(t, err)
	assert.Equal(t, OpTypeFailureRecovery, serialized.ModelType)

	deserialized, err := serializer.DeserializeOperation(serialized)
	require.NoError(t, err)

	input := deserialized.Input.(EnhancedInput)
	assert.Equal(t, OpTypeFailureRecovery, input.Type)
	assert.Equal(t, FailureRecoveryOpQuorumWrite, input.FailureRecoveryOp.Type)
	assert.Equal(t, []string{"node-3"}, input.FailureRecoveryOp.FailedNodes)
}

func TestSerializeLeaderHAOperation(t *testing.T) {
	serializer := NewOperationSerializer()

	op := porcupine.Operation{
		ClientId: 3,
		Input: EnhancedInput{
			Type: OpTypeLeaderHA,
			LeaderHAOp: LeaderHAOperation{
				Type:      LeaderHAOpLeaderChange,
				NodeID:    "node-1",
				Term:      5,
				NewLeader: "node-2",
			},
		},
		Output: EnhancedOutput{
			Type: OpTypeLeaderHA,
			LeaderHAOut: LeaderHAOutput{
				Ok:         true,
				Term:       5,
				NewLeader:  "node-2",
				ActiveTerm: 5,
			},
		},
		Call:   5000,
		Return: 6000,
	}

	serialized, err := serializer.SerializeOperation(op)
	require.NoError(t, err)
	assert.Equal(t, OpTypeLeaderHA, serialized.ModelType)

	deserialized, err := serializer.DeserializeOperation(serialized)
	require.NoError(t, err)

	input := deserialized.Input.(EnhancedInput)
	assert.Equal(t, OpTypeLeaderHA, input.Type)
	assert.Equal(t, LeaderHAOpLeaderChange, input.LeaderHAOp.Type)
	assert.Equal(t, "node-2", input.LeaderHAOp.NewLeader)

	output := deserialized.Output.(EnhancedOutput)
	assert.Equal(t, uint64(5), output.LeaderHAOut.ActiveTerm)
}

func TestSerializeEmptyValue(t *testing.T) {
	serializer := NewOperationSerializer()

	op := porcupine.Operation{
		ClientId: 1,
		Input: EnhancedInput{
			Type: OpTypeTopology,
			TopologyOp: TopologyOperation{
				Type:  TopologyOpGet,
				Key:   "empty-key",
				Value: nil, // 空 value
			},
		},
		Output: EnhancedOutput{
			Type: OpTypeTopology,
			TopologyOut: TopologyOutput{
				Ok:    true,
				Value: nil, // 空 value
			},
		},
		Call:   100,
		Return: 200,
	}

	serialized, err := serializer.SerializeOperation(op)
	require.NoError(t, err)

	deserialized, err := serializer.DeserializeOperation(serialized)
	require.NoError(t, err)

	input := deserialized.Input.(EnhancedInput)
	assert.Nil(t, input.TopologyOp.Value)

	output := deserialized.Output.(EnhancedOutput)
	assert.Nil(t, output.TopologyOut.Value)
}

func TestSerializeBinaryValue(t *testing.T) {
	serializer := NewOperationSerializer()

	// 测试二进制数据（包含特殊字符）
	binaryValue := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD}

	op := porcupine.Operation{
		ClientId: 1,
		Input: EnhancedInput{
			Type: OpTypeTopology,
			TopologyOp: TopologyOperation{
				Type:  TopologyOpWriteQuorum,
				Key:   "binary-key",
				Value: binaryValue,
			},
		},
		Output: EnhancedOutput{
			Type: OpTypeTopology,
			TopologyOut: TopologyOutput{
				Ok:    true,
				Value: binaryValue,
			},
		},
		Call:   100,
		Return: 200,
	}

	serialized, err := serializer.SerializeOperation(op)
	require.NoError(t, err)

	// 验证 JSON 可以正常序列化（Base64 编码）
	jsonBytes, err := json.Marshal(serialized)
	require.NoError(t, err)

	var unmarshaled SerializableOperation
	err = json.Unmarshal(jsonBytes, &unmarshaled)
	require.NoError(t, err)

	deserialized, err := serializer.DeserializeOperation(&unmarshaled)
	require.NoError(t, err)

	input := deserialized.Input.(EnhancedInput)
	assert.Equal(t, binaryValue, input.TopologyOp.Value)

	output := deserialized.Output.(EnhancedOutput)
	assert.Equal(t, binaryValue, output.TopologyOut.Value)
}

func TestRoundTripJSON(t *testing.T) {
	serializer := NewOperationSerializer()

	// 创建复杂操作
	op := porcupine.Operation{
		ClientId: 42,
		Input: EnhancedInput{
			Type: OpTypeTopology,
			TopologyOp: TopologyOperation{
				Type:         TopologyOpWrite2PC,
				NodeID:       "node-1",
				Key:          "complex-key",
				Value:        []byte("complex-value-with-unicode-中文"),
				Version:      999,
				Term:         888,
				Participants: []string{"node-1", "node-2", "node-3", "node-4", "node-5"},
				Nodes: []*NodeInfo{
					{NodeID: "node-1", ParentID: "", Children: []string{"node-2", "node-3"}, TreeDepth: 0},
					{NodeID: "node-2", ParentID: "node-1", Children: []string{}, TreeDepth: 1},
				},
			},
		},
		Output: EnhancedOutput{
			Type: OpTypeTopology,
			TopologyOut: TopologyOutput{
				Ok:      true,
				Value:   []byte("response"),
				Version: 1000,
				Error:   "",
			},
		},
		Call:   1000000,
		Return: 2000000,
	}

	// 序列化到 JSON
	serialized, err := serializer.SerializeOperation(op)
	require.NoError(t, err)

	jsonBytes, err := json.MarshalIndent(serialized, "", "  ")
	require.NoError(t, err)
	t.Logf("JSON output:\n%s", string(jsonBytes))

	// 从 JSON 反序列化
	var unmarshaled SerializableOperation
	err = json.Unmarshal(jsonBytes, &unmarshaled)
	require.NoError(t, err)

	deserialized, err := serializer.DeserializeOperation(&unmarshaled)
	require.NoError(t, err)

	// 完整验证
	assert.Equal(t, op.ClientId, deserialized.ClientId)
	assert.Equal(t, op.Call, deserialized.Call)
	assert.Equal(t, op.Return, deserialized.Return)

	input := deserialized.Input.(EnhancedInput)
	assert.Equal(t, OpTypeTopology, input.Type)
	assert.Equal(t, TopologyOpWrite2PC, input.TopologyOp.Type)
	assert.Equal(t, "node-1", input.TopologyOp.NodeID)
	assert.Equal(t, []byte("complex-value-with-unicode-中文"), input.TopologyOp.Value)
	assert.Equal(t, 2, len(input.TopologyOp.Nodes))
	assert.Equal(t, "node-1", input.TopologyOp.Nodes[0].NodeID)
}

// ==================== 基准测试 ====================

func BenchmarkSerializeOperation(b *testing.B) {
	serializer := NewOperationSerializer()

	op := porcupine.Operation{
		ClientId: 1,
		Input: EnhancedInput{
			Type: OpTypeTopology,
			TopologyOp: TopologyOperation{
				Type:         TopologyOpWriteQuorum,
				NodeID:       "node-1",
				Key:          "benchmark-key",
				Value:        make([]byte, 1024), // 1KB value
				Participants: []string{"node-1", "node-2", "node-3"},
			},
		},
		Output: EnhancedOutput{
			Type: OpTypeTopology,
			TopologyOut: TopologyOutput{
				Ok:    true,
				Value: make([]byte, 1024),
			},
		},
		Call:   1000,
		Return: 2000,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = serializer.SerializeOperation(op)
	}
}

func BenchmarkDeserializeOperation(b *testing.B) {
	serializer := NewOperationSerializer()

	op := porcupine.Operation{
		ClientId: 1,
		Input: EnhancedInput{
			Type: OpTypeTopology,
			TopologyOp: TopologyOperation{
				Type:         TopologyOpWriteQuorum,
				NodeID:       "node-1",
				Key:          "benchmark-key",
				Value:        make([]byte, 1024),
				Participants: []string{"node-1", "node-2", "node-3"},
			},
		},
		Output: EnhancedOutput{
			Type: OpTypeTopology,
			TopologyOut: TopologyOutput{
				Ok:    true,
				Value: make([]byte, 1024),
			},
		},
		Call:   1000,
		Return: 2000,
	}

	serialized, _ := serializer.SerializeOperation(op)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = serializer.DeserializeOperation(serialized)
	}
}
