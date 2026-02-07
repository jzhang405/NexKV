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
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMessageCodec_EncodeDecodeRoundTrip 测试编解码往返
func TestMessageCodec_EncodeDecodeRoundTrip(t *testing.T) {
	codec := NewMessagePackCodec()

	// 创建原始消息（使用 Payload 模式）
	original := &Message{}
	original.MustEncodePayload(&PutPayload{
		Key:     []byte("user:1001"),
		Value:   []byte("{\"name\":\"Alice\",\"age\":30}"),
		Version: 5,
	})

	// 编码
	encoded, err := codec.EncodeToBytes(original)
	require.NoError(t, err)
	assert.NotEmpty(t, encoded)

	// 解码
	decoded, err := codec.DecodeFromBytes(encoded)
	require.NoError(t, err)

	// 验证字段
	assert.Equal(t, original.Type, decoded.Type)

	// 解码 Payload
	payload, err := decoded.DecodePayload()
	require.NoError(t, err)

	putPayload, ok := payload.(*PutPayload)
	require.True(t, ok)
	assert.Equal(t, []byte("user:1001"), putPayload.Key)
	assert.Equal(t, []byte("{\"name\":\"Alice\",\"age\":30}"), putPayload.Value)
	assert.Equal(t, uint64(5), putPayload.Version)

	// Seq 应该被自动生成
	assert.Greater(t, decoded.Seq, uint64(0))
}

// TestMessageCodec_EncodeDecode_AllMessageTypes 测试所有消息类型
func TestMessageCodec_EncodeDecode_AllMessageTypes(t *testing.T) {
	codec := NewMessagePackCodec()

	msgTypes := []MessageType{
		MessageTypeGet,
		MessageTypePut,
		MessageTypeDelete,
		MessageTypeSync,
		MessageTypeAck,
		MessageTypeNack,
		MessageTypeGossip,
		MessageTypeCluster,
		MessageTypeQuorum,
	}

	for _, msgType := range msgTypes {
		t.Run(msgType.String(), func(t *testing.T) {
			// 创建带有适当 Payload 的消息
			msg := &Message{Type: msgType}

			// 根据类型添加 Payload
			switch msgType {
			case MessageTypeGet:
				msg.MustEncodePayload(&GetPayload{Key: []byte("test-key")})
			case MessageTypePut:
				msg.MustEncodePayload(&PutPayload{
					Key:   []byte("test-key"),
					Value: []byte("test-value"),
				})
			case MessageTypeDelete:
				msg.MustEncodePayload(&DeletePayload{Key: []byte("test-key")})
			case MessageTypeGossip:
				msg.MustEncodePayload(&GossipPayload{
					Digest: map[string]uint64{"k1": 1},
				})
			case MessageTypeQuorum:
				msg.MustEncodePayload(&QuorumPayload{
					Phase:      "propose",
					ProposalID: "test-proposal",
					Key:        "test-key",
				})
			case MessageTypeSync:
				msg.MustEncodePayload(&TwoPCPreparePayload{
					TxID: "test-tx-123",
					Operations: []Operation{
						{Type: "put", Key: "k1", Value: []byte("v1")},
					},
				})
			case MessageTypeAck:
				msg.MustEncodePayload(&TwoPCCommitPayload{
					TxID:   "test-tx-123",
					Result: true,
				})
			case MessageTypeNack:
				msg.MustEncodePayload(&TwoPCRollbackPayload{
					TxID:   "test-tx-123",
					Reason: "test-failure",
				})
			case MessageTypeCluster:
				msg.MustEncodePayload(&ClusterPayload{
					Action: "join",
					NodeID: "node-123",
				})
			}

			encoded, err := codec.EncodeToBytes(msg)
			require.NoError(t, err)

			decoded, err := codec.DecodeFromBytes(encoded)
			require.NoError(t, err)
			assert.Equal(t, msgType, decoded.Type)
		})
	}
}

// TestMessageCodec_EmptyPayload 测试空 Payload
func TestMessageCodec_EmptyPayload(t *testing.T) {
	codec := NewMessagePackCodec()

	// 创建带有 DeletePayload 的消息
	msg := &Message{}
	msg.MustEncodePayload(&DeletePayload{
		Key: []byte("deleted-key"),
	})

	encoded, err := codec.EncodeToBytes(msg)
	require.NoError(t, err)

	decoded, err := codec.DecodeFromBytes(encoded)
	require.NoError(t, err)

	// 解码 Payload 并验证
	payload, err := decoded.DecodePayload()
	require.NoError(t, err)

	deletePayload, ok := payload.(*DeletePayload)
	require.True(t, ok)
	assert.Equal(t, []byte("deleted-key"), deletePayload.Key)
	assert.Equal(t, MessageTypeDelete, decoded.Type)
}

// TestMessageCodec_LargePayload 测试大 Payload（接近限制）
func TestMessageCodec_LargePayload(t *testing.T) {
	codec := NewMessagePackCodec()

	// 5KB 数据（接近10KB限制）
	largeValue := make([]byte, 5*1024)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	msg := &Message{}
	msg.MustEncodePayload(&PutPayload{
		Key:   []byte("large-data"),
		Value: largeValue,
	})

	encoded, err := codec.EncodeToBytes(msg)
	require.NoError(t, err)

	decoded, err := codec.DecodeFromBytes(encoded)
	require.NoError(t, err)

	// 解码 Payload 并验证
	payload, err := decoded.DecodePayload()
	require.NoError(t, err)

	putPayload, ok := payload.(*PutPayload)
	require.True(t, ok)
	assert.Equal(t, len(largeValue), len(putPayload.Value))
	assert.Equal(t, largeValue[0:100], putPayload.Value[0:100])
}

// TestMessageCodec_InvalidData 测试无效数据
func TestMessageCodec_InvalidData(t *testing.T) {
	codec := NewMessagePackCodec()

	testCases := []struct {
		name string
		data []byte
	}{
		{"空数据", []byte{}},
		{"过短数据", []byte{0x01}},
		{"截断数据", []byte{0x01, 0x02, 0x03}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := codec.DecodeFromBytes(tc.data)
			assert.Error(t, err)
		})
	}
}

// TestMessageCodec_MessageTooLarge 测试超大消息拒绝
func TestMessageCodec_MessageTooLarge(t *testing.T) {
	codec := NewMessagePackCodec()

	// 构造一个声称超大但实际数据不足的消息
	buf := new(bytes.Buffer)
	// 写入类型
	buf.WriteByte(byte(MessageTypePut))
	// 写入长度（15KB，超过限制）
	binaryWriteUint16(buf, 15*1024)
	// 只写入少量数据
	buf.Write([]byte{0x01, 0x02, 0x03})

	_, err := codec.Decode(buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "消息过大")
}

// TestMessageCodec_SeqGeneration 测试序号自动生成
func TestMessageCodec_SeqGeneration(t *testing.T) {
	codec := NewMessagePackCodec()
	codec.ResetSeqGenerator()

	msg := &Message{}
	msg.MustEncodePayload(&PutPayload{Key: []byte("key")})

	// 第一次编码
	_, err := codec.EncodeToBytes(msg)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), codec.GetNextSeq()-1)

	// 第二次编码
	msg2 := &Message{}
	msg2.MustEncodePayload(&GetPayload{Key: []byte("key2")})
	_, err = codec.EncodeToBytes(msg2)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), codec.GetNextSeq()-1)
}

// TestMessageCodec_ExistingSeq 测试保留已有序号
func TestMessageCodec_ExistingSeq(t *testing.T) {
	codec := NewMessagePackCodec()

	msg := &Message{
		Type: MessageTypePut,
		Seq:  42, // 预设序号
	}
	msg.MustEncodePayload(&PutPayload{
		Key:     []byte("key"),
		Version: 1,
	})

	encoded, err := codec.EncodeToBytes(msg)
	require.NoError(t, err)

	decoded, err := codec.DecodeFromBytes(encoded)
	require.NoError(t, err)
	assert.Equal(t, uint64(42), decoded.Seq)
}

// TestMessageCodec_MultipleMessages 测试多条消息
func TestMessageCodec_MultipleMessages(t *testing.T) {
	codec := NewMessagePackCodec()

	// 创建多条消息
	messages := []*Message{
		func() *Message {
			m := &Message{Type: MessageTypeGet}
			m.MustEncodePayload(&GetPayload{Key: []byte("key1")})
			return m
		}(),
		func() *Message {
			m := &Message{Type: MessageTypePut}
			m.MustEncodePayload(&PutPayload{
				Key:   []byte("key2"),
				Value: []byte("value2"),
			})
			return m
		}(),
		func() *Message {
			m := &Message{Type: MessageTypeDelete}
			m.MustEncodePayload(&DeletePayload{Key: []byte("key3")})
			return m
		}(),
	}

	for _, msg := range messages {
		encoded, err := codec.EncodeToBytes(msg)
		require.NoError(t, err)

		decoded, err := codec.DecodeFromBytes(encoded)
		require.NoError(t, err)
		assert.Equal(t, msg.Type, decoded.Type)
	}
}

// TestMessage_Validation 测试消息验证
func TestMessage_Validation(t *testing.T) {
	testCases := []struct {
		name      string
		msg       *Message
		wantValid bool
	}{
		{
			name: "Valid message with Payload",
			msg: func() *Message {
				m := &Message{Type: MessageTypeGet}
				m.MustEncodePayload(&GetPayload{Key: []byte("key")})
				return m
			}(),
			wantValid: true,
		},
		{
			name:      "Valid ACK message (no Payload needed)",
			msg:       &Message{Type: MessageTypeAck, Seq: 1},
			wantValid: true,
		},
		{
			name:      "Invalid GET without Payload",
			msg:       &Message{Type: MessageTypeGet},
			wantValid: false,
		},
		{
			name:      "Invalid PUT without Payload",
			msg:       &Message{Type: MessageTypePut},
			wantValid: false,
		},
		{
			name:      "Invalid type",
			msg:       &Message{Type: MessageTypeUnknown},
			wantValid: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantValid, tc.msg.IsValid())
		})
	}
}

// TestMessage_Clone 测试消息克隆
func TestMessage_Clone(t *testing.T) {
	original := &Message{}
	original.MustEncodePayload(&PutPayload{
		Key:     []byte("key"),
		Value:   []byte("value"),
		Version: 1,
	})

	clone := original.Clone()

	// 验证基本字段相等
	assert.Equal(t, original.Type, clone.Type)
	assert.Equal(t, original.Seq, clone.Seq)

	// 验证 Payload 被深拷贝（使用指针地址比较）
	assert.NotSame(t, &original.Payload, &clone.Payload)
	assert.Equal(t, original.Payload, clone.Payload)

	// 修改原消息不影响克隆
	original.Payload[0] = 'X'
	assert.NotEqual(t, original.Payload, clone.Payload)
}

// TestMessage_IncrementHopCount 测试跳数递增
func TestMessage_IncrementHopCount(t *testing.T) {
	msg := &Message{HopCount: 0}

	// 正常递增到 HopMax (10次，从0到10)
	for i := 0; i < int(HopMax); i++ {
		assert.True(t, msg.IncrementHopCount(), "应该能递增到HopMax")
		assert.Equal(t, uint8(i+1), msg.HopCount)
	}

	// 此时 HopCount = HopMax = 10
	assert.Equal(t, uint8(HopMax), msg.HopCount)

	// 达到 HopMax 后再递增应返回 false
	assert.False(t, msg.IncrementHopCount(), "超过HopMax应返回false")
	assert.Equal(t, uint8(HopMax), msg.HopCount, "HopCount不应超过HopMax")
}

// TestMessage_TypeString 测试消息类型字符串表示
func TestMessage_TypeString(t *testing.T) {
	testCases := []struct {
		msgType    MessageType
		wantString string
	}{
		{MessageTypeGet, "GET"},
		{MessageTypePut, "PUT"},
		{MessageTypeDelete, "DELETE"},
		{MessageTypeSync, "SYNC"},
		{MessageTypeAck, "ACK"},
		{MessageTypeNack, "NACK"},
		{MessageTypeGossip, "GOSSIP"},
		{MessageTypeCluster, "CLUSTER"},
		{MessageTypeQuorum, "QUORUM"},
		{MessageTypeUnknown, "UNKNOWN"},
		{MessageType(99), "UNKNOWN"},
	}

	for _, tc := range testCases {
		t.Run(tc.wantString, func(t *testing.T) {
			assert.Equal(t, tc.wantString, tc.msgType.String())
		})
	}
}

// 辅助函数：写入uint16到buffer
func binaryWriteUint16(buf *bytes.Buffer, val uint16) {
	b := make([]byte, 2)
	b[0] = byte(val >> 8)
	b[1] = byte(val)
	buf.Write(b)
}

// TestMessageCodec_StreamEncodeDecode 测试流式编解码
func TestMessageCodec_StreamEncodeDecode(t *testing.T) {
	codec := NewMessagePackCodec()

	msg := &Message{}
	msg.MustEncodePayload(&PutPayload{
		Key:     []byte("stream-key"),
		Value:   []byte("stream-value"),
		Version: 1,
	})

	// 编码到内存buffer
	var buf bytes.Buffer
	err := codec.Encode(&buf, msg)
	require.NoError(t, err)

	// 从buffer解码
	decoded, err := codec.Decode(&buf)
	require.NoError(t, err)

	assert.Equal(t, msg.Type, decoded.Type)

	// 解码 Payload 并验证
	payload, err := decoded.DecodePayload()
	require.NoError(t, err)

	putPayload, ok := payload.(*PutPayload)
	require.True(t, ok)
	assert.Equal(t, []byte("stream-key"), putPayload.Key)
	assert.Equal(t, []byte("stream-value"), putPayload.Value)
}
