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

package transport

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMessageCompatibility 测试消息协议兼容性
func TestMessageCompatibility(t *testing.T) {
	// 1. 创建结构化 Payload（MessagePack）
	putPayload := &PutPayload{
		Key:     []byte("test-key"),
		Value:   []byte("test-value"),
		Version: 1,
		Sync:    true,
	}

	// 2. 构建消息并编码 Payload
	msg := &Message{
		Seq:       10001,
		Timestamp: time.Now(),
		From:      "node-1",
		To:        "node-2",
	}
	err := msg.EncodePayload(putPayload)
	require.NoError(t, err)

	// 3. 验证消息类型已绑定
	assert.Equal(t, MessageTypePut, msg.Type)

	// 4. 验证 Payload 已序列化
	assert.NotEmpty(t, msg.Payload)

	// 5. 解码 Payload
	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	// 6. 类型断言验证
	putDecoded, ok := decoded.(*PutPayload)
	require.True(t, ok)

	// 7. 验证字段完整性
	assert.Equal(t, putPayload.Key, putDecoded.Key)
	assert.Equal(t, putPayload.Value, putDecoded.Value)
	assert.Equal(t, putPayload.Version, putDecoded.Version)
	assert.Equal(t, putPayload.Sync, putDecoded.Sync)
}

// TestGetPayloadEncoding 测试 GET Payload 编解码
func TestGetPayloadEncoding(t *testing.T) {
	getPayload := &GetPayload{
		Key:         []byte("get-key"),
		WithVersion: true,
	}

	msg := &Message{}
	err := msg.EncodePayload(getPayload)
	require.NoError(t, err)

	assert.Equal(t, MessageTypeGet, msg.Type)

	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	getDecoded, ok := decoded.(*GetPayload)
	require.True(t, ok)
	assert.Equal(t, getPayload.Key, getDecoded.Key)
	assert.Equal(t, getPayload.WithVersion, getDecoded.WithVersion)
}

// TestDeletePayloadEncoding 测试 DELETE Payload 编解码
func TestDeletePayloadEncoding(t *testing.T) {
	deletePayload := &DeletePayload{
		Key:    []byte("delete-key"),
		Verify: true,
	}

	msg := &Message{}
	err := msg.EncodePayload(deletePayload)
	require.NoError(t, err)

	assert.Equal(t, MessageTypeDelete, msg.Type)

	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	deleteDecoded, ok := decoded.(*DeletePayload)
	require.True(t, ok)
	assert.Equal(t, deletePayload.Key, deleteDecoded.Key)
	assert.Equal(t, deletePayload.Verify, deleteDecoded.Verify)
}

// TestQuorumPayloadEncoding 测试 Quorum Payload 编解码
func TestQuorumPayloadEncoding(t *testing.T) {
	// 1. 创建 Quorum 提议 Payload
	proposePayload := &QuorumPayload{
		Phase:      "propose",
		ProposalID: "prop-001",
		Key:        "shard-001",
		Value:      []byte("metadata"),
	}

	msg := &Message{}
	err := msg.EncodePayload(proposePayload)
	require.NoError(t, err)

	// 2. 解码验证
	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	quorumDecoded, ok := decoded.(*QuorumPayload)
	require.True(t, ok)
	assert.Equal(t, "propose", quorumDecoded.Phase)
	assert.Equal(t, "prop-001", quorumDecoded.ProposalID)
	assert.Equal(t, "shard-001", quorumDecoded.Key)
	assert.Equal(t, []byte("metadata"), quorumDecoded.Value)
}

// TestQuorumVotePayloadEncoding 测试 Quorum 投票 Payload 编解码
func TestQuorumVotePayloadEncoding(t *testing.T) {
	votePayload := &QuorumPayload{
		Phase:      "vote",
		ProposalID: "prop-001",
		Key:        "shard-001",
		Voter:      "node-1",
		Decision:   true,
	}

	msg := &Message{}
	err := msg.EncodePayload(votePayload)
	require.NoError(t, err)

	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	quorumDecoded, ok := decoded.(*QuorumPayload)
	require.True(t, ok)
	assert.Equal(t, "vote", quorumDecoded.Phase)
	assert.Equal(t, "node-1", quorumDecoded.Voter)
	assert.True(t, quorumDecoded.Decision)
}

// TestTwoPCPayloadEncoding 测试 2PC Payload 编解码
func TestTwoPCPayloadEncoding(t *testing.T) {
	// 1. 创建 2PC 准备 Payload
	preparePayload := &TwoPCPreparePayload{
		TxID:    "txn-001",
		Timeout: 30000,
		Operations: []Operation{
			{Type: "put", Key: "k1", Value: []byte("v1")},
			{Type: "delete", Key: "k2"},
		},
	}

	msg := &Message{}
	err := msg.EncodePayload(preparePayload)
	require.NoError(t, err)

	// 2. 解码验证
	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	twoPCDecoded, ok := decoded.(*TwoPCPreparePayload)
	require.True(t, ok)
	assert.Equal(t, "txn-001", twoPCDecoded.TxID)
	assert.Equal(t, int64(30000), twoPCDecoded.Timeout)
	assert.Len(t, twoPCDecoded.Operations, 2)
	assert.Equal(t, "put", twoPCDecoded.Operations[0].Type)
	assert.Equal(t, "k1", twoPCDecoded.Operations[0].Key)
	assert.Equal(t, []byte("v1"), twoPCDecoded.Operations[0].Value)
}

// TestTwoPCCommitPayloadEncoding 测试 2PC Commit Payload 编解码
func TestTwoPCCommitPayloadEncoding(t *testing.T) {
	commitPayload := &TwoPCCommitPayload{
		TxID:   "txn-001",
		Result: true,
	}

	msg := &Message{}
	err := msg.EncodePayload(commitPayload)
	require.NoError(t, err)

	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	commitDecoded, ok := decoded.(*TwoPCCommitPayload)
	require.True(t, ok)
	assert.Equal(t, "txn-001", commitDecoded.TxID)
	assert.True(t, commitDecoded.Result)
}

// TestTwoPCRollbackPayloadEncoding 测试 2PC Rollback Payload 编解码
func TestTwoPCRollbackPayloadEncoding(t *testing.T) {
	rollbackPayload := &TwoPCRollbackPayload{
		TxID:   "txn-001",
		Reason: "timeout",
	}

	msg := &Message{}
	err := msg.EncodePayload(rollbackPayload)
	require.NoError(t, err)

	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	rollbackDecoded, ok := decoded.(*TwoPCRollbackPayload)
	require.True(t, ok)
	assert.Equal(t, "txn-001", rollbackDecoded.TxID)
	assert.Equal(t, "timeout", rollbackDecoded.Reason)
}

// TestGossipPayloadEncoding 测试 Gossip Payload 编解码
func TestGossipPayloadEncoding(t *testing.T) {
	gossipPayload := &GossipPayload{
		Digest: map[string]uint64{
			"key1": 1,
			"key2": 2,
			"key3": 3,
		},
		VersionDelta: 10,
		FullSync:     false,
	}

	msg := &Message{}
	err := msg.EncodePayload(gossipPayload)
	require.NoError(t, err)

	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	gossipDecoded, ok := decoded.(*GossipPayload)
	require.True(t, ok)
	assert.Len(t, gossipDecoded.Digest, 3)
	assert.Equal(t, uint64(1), gossipDecoded.Digest["key1"])
	assert.Equal(t, uint64(10), gossipDecoded.VersionDelta)
	assert.False(t, gossipDecoded.FullSync)
}

// TestClusterPayloadEncoding 测试 Cluster Payload 编解码
func TestClusterPayloadEncoding(t *testing.T) {
	clusterPayload := &ClusterPayload{
		Action: "join",
		NodeID: "node-1",
		Metadata: map[string]string{
			"addr":   "127.0.0.1:9211",
			"region": "cn-east",
		},
	}

	msg := &Message{}
	err := msg.EncodePayload(clusterPayload)
	require.NoError(t, err)

	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	clusterDecoded, ok := decoded.(*ClusterPayload)
	require.True(t, ok)
	assert.Equal(t, "join", clusterDecoded.Action)
	assert.Equal(t, "node-1", clusterDecoded.NodeID)
	assert.Equal(t, "127.0.0.1:9211", clusterDecoded.Metadata["addr"])
}

// TestEncodePayloadUnsupportedType 测试不支持的 Payload 类型
func TestEncodePayloadUnsupportedType(t *testing.T) {
	msg := &Message{}

	// 尝试编码不支持的类型
	err := msg.EncodePayload("not a payload")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported payload type")
}

// TestDecodePayloadUnsupportedType 测试不支持的消息类型
func TestDecodePayloadUnsupportedType(t *testing.T) {
	// 创建一个未知类型的消息
	msg := &Message{
		Type:    MessageTypeUnknown,
		Payload: []byte("test"),
	}

	_, err := msg.DecodePayload()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported message type")
}

// TestEncodePayloadOmitEmpty 测试 omitempty 字段
func TestEncodePayloadOmitEmpty(t *testing.T) {
	// 测试空值字段的编解码
	putPayload := &PutPayload{
		Key:     []byte("test-key"),
		Value:   []byte{}, // 空切片
		Version: 1,
		Sync:    false,
	}

	msg := &Message{}
	err := msg.EncodePayload(putPayload)
	require.NoError(t, err)

	// 解码验证
	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	putDecoded, ok := decoded.(*PutPayload)
	require.True(t, ok)
	assert.Equal(t, []byte("test-key"), putDecoded.Key)
	// 空切片应正确解码
	assert.Empty(t, putDecoded.Value)
}

// TestEncodePayloadWithActualValue 测试带实际值的 Payload
func TestEncodePayloadWithActualValue(t *testing.T) {
	// 测试带实际值的编解码
	putPayload := &PutPayload{
		Key:     []byte("test-key"),
		Value:   []byte("test-value"),
		Version: 1,
		Sync:    true,
	}

	msg := &Message{}
	err := msg.EncodePayload(putPayload)
	require.NoError(t, err)

	decoded, err := msg.DecodePayload()
	require.NoError(t, err)

	putDecoded, ok := decoded.(*PutPayload)
	require.True(t, ok)
	assert.Equal(t, []byte("test-key"), putDecoded.Key)
	assert.Equal(t, []byte("test-value"), putDecoded.Value)
	assert.True(t, putDecoded.Sync)
}

// TestMessageWithPayloadClone 测试消息克隆包含 Payload
func TestMessageWithPayloadClone(t *testing.T) {
	putPayload := &PutPayload{
		Key:     []byte("test-key"),
		Value:   []byte("test-value"),
		Version: 1,
		Sync:    true,
	}

	original := &Message{
		Seq:       10001,
		Timestamp: time.Now(),
		From:      "node-1",
		To:        "node-2",
	}
	original.MustEncodePayload(putPayload)

	// 克隆消息
	cloned := original.Clone()

	// 验证 Payload 已深拷贝
	assert.Equal(t, original.Seq, cloned.Seq)
	assert.Equal(t, original.Payload, cloned.Payload)

	// 修改克隆的 Payload 不应影响原始
	cloned.Payload[0] = 0xFF
	assert.NotEqual(t, original.Payload[0], cloned.Payload[0])
}

// BenchmarkMessagePackEncoding 编码性能基准测试
func BenchmarkMessagePackEncoding(b *testing.B) {
	putPayload := &PutPayload{
		Key:     []byte("benchmark-key"),
		Value:   make([]byte, 1024),
		Version: 1,
		Sync:    true,
	}

	msg := &Message{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = msg.EncodePayload(putPayload)
	}
}

// BenchmarkMessagePackDecoding 解码性能基准测试
func BenchmarkMessagePackDecoding(b *testing.B) {
	putPayload := &PutPayload{
		Key:     []byte("benchmark-key"),
		Value:   make([]byte, 1024),
		Version: 1,
		Sync:    true,
	}

	msg := &Message{}
	msg.MustEncodePayload(putPayload)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = msg.DecodePayload()
	}
}
