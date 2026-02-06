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

	original := &Message{
		Type:    MessageTypePut,
		Key:     []byte("user:1001"),
		Value:   []byte("{\"name\":\"Alice\",\"age\":30}"),
		Version: 5,
	}

	// 编码
	encoded, err := codec.EncodeToBytes(original)
	require.NoError(t, err)
	assert.NotEmpty(t, encoded)

	// 解码
	decoded, err := codec.DecodeFromBytes(encoded)
	require.NoError(t, err)

	// 验证字段
	assert.Equal(t, original.Type, decoded.Type)
	assert.Equal(t, original.Key, decoded.Key)
	assert.Equal(t, original.Value, decoded.Value)
	assert.Equal(t, original.Version, decoded.Version)
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
			msg := &Message{
				Type:  msgType,
				Key:   []byte("test-key"),
				Value: []byte("test-value"),
			}

			encoded, err := codec.EncodeToBytes(msg)
			require.NoError(t, err)

			decoded, err := codec.DecodeFromBytes(encoded)
			require.NoError(t, err)
			assert.Equal(t, msgType, decoded.Type)
		})
	}
}

// TestMessageCodec_EmptyPayload 测试空payload
func TestMessageCodec_EmptyPayload(t *testing.T) {
	codec := NewMessagePackCodec()

	msg := &Message{
		Type:    MessageTypeDelete,
		Key:     []byte("deleted-key"),
		Value:   nil,
		Version: 1,
	}

	encoded, err := codec.EncodeToBytes(msg)
	require.NoError(t, err)

	decoded, err := codec.DecodeFromBytes(encoded)
	require.NoError(t, err)
	assert.Nil(t, decoded.Value)
	assert.Equal(t, MessageTypeDelete, decoded.Type)
}

// TestMessageCodec_LargePayload 测试大payload（接近限制）
func TestMessageCodec_LargePayload(t *testing.T) {
	codec := NewMessagePackCodec()

	// 5KB 数据（接近10KB限制）
	largeValue := make([]byte, 5*1024)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	msg := &Message{
		Type:    MessageTypePut,
		Key:     []byte("large-data"),
		Value:   largeValue,
		Version: 1,
	}

	encoded, err := codec.EncodeToBytes(msg)
	require.NoError(t, err)

	decoded, err := codec.DecodeFromBytes(encoded)
	require.NoError(t, err)
	assert.Equal(t, len(largeValue), len(decoded.Value))
	assert.Equal(t, largeValue[0:100], decoded.Value[0:100])
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

	msg := &Message{
		Type: MessageTypePut,
		Key:  []byte("key"),
	}

	// 第一次编码
	_, err := codec.EncodeToBytes(msg)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), codec.GetNextSeq()-1)

	// 第二次编码
	msg2 := &Message{
		Type: MessageTypeGet,
		Key:  []byte("key2"),
	}
	_, err = codec.EncodeToBytes(msg2)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), codec.GetNextSeq()-1)
}

// TestMessageCodec_ExistingSeq 测试保留已有序号
func TestMessageCodec_ExistingSeq(t *testing.T) {
	codec := NewMessagePackCodec()

	msg := &Message{
		Type:    MessageTypePut,
		Key:     []byte("key"),
		Seq:     42, // 预设序号
		Version: 1,
	}

	encoded, err := codec.EncodeToBytes(msg)
	require.NoError(t, err)

	decoded, err := codec.DecodeFromBytes(encoded)
	require.NoError(t, err)
	assert.Equal(t, uint64(42), decoded.Seq)
}

// TestMessageCodec_MultipleMessages 测试多条消息
func TestMessageCodec_MultipleMessages(t *testing.T) {
	codec := NewMessagePackCodec()

	messages := []*Message{
		{Type: MessageTypeGet, Key: []byte("key1")},
		{Type: MessageTypePut, Key: []byte("key2"), Value: []byte("value2")},
		{Type: MessageTypeDelete, Key: []byte("key3")},
	}

	for _, msg := range messages {
		encoded, err := codec.EncodeToBytes(msg)
		require.NoError(t, err)

		decoded, err := codec.DecodeFromBytes(encoded)
		require.NoError(t, err)
		assert.Equal(t, msg.Type, decoded.Type)
		assert.Equal(t, msg.Key, decoded.Key)
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
			name:      "Valid GET message",
			msg:       &Message{Type: MessageTypeGet, Key: []byte("key")},
			wantValid: true,
		},
		{
			name:      "Valid PUT message",
			msg:       &Message{Type: MessageTypePut, Key: []byte("key"), Value: []byte("value")},
			wantValid: true,
		},
		{
			name:      "Valid DELETE message",
			msg:       &Message{Type: MessageTypeDelete, Key: []byte("key")},
			wantValid: true,
		},
		{
			name:      "Invalid GET without key",
			msg:       &Message{Type: MessageTypeGet},
			wantValid: false,
		},
		{
			name:      "Invalid PUT without key",
			msg:       &Message{Type: MessageTypePut, Value: []byte("value")},
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
	original := &Message{
		Type:    MessageTypePut,
		Key:     []byte("key"),
		Value:   []byte("value"),
		Version: 1,
	}

	clone := original.Clone()

	// 验证字段相等
	assert.Equal(t, original.Type, clone.Type)
	assert.Equal(t, original.Key, clone.Key)
	assert.Equal(t, original.Value, clone.Value)
	assert.Equal(t, original.Version, clone.Version)

	// 验证深拷贝（修改原消息不影响克隆）
	original.Key[0] = 'X'
	assert.NotEqual(t, original.Key, clone.Key)
	assert.Equal(t, []byte("key"), clone.Key)
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

	msg := &Message{
		Type:    MessageTypePut,
		Key:     []byte("stream-key"),
		Value:   []byte("stream-value"),
		Version: 1,
	}

	// 编码到内存buffer
	var buf bytes.Buffer
	err := codec.Encode(&buf, msg)
	require.NoError(t, err)

	// 从buffer解码
	decoded, err := codec.Decode(&buf)
	require.NoError(t, err)

	assert.Equal(t, msg.Type, decoded.Type)
	assert.Equal(t, msg.Key, decoded.Key)
	assert.Equal(t, msg.Value, decoded.Value)
}
