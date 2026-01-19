// Package transport 传输层测试
package transport

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// 帧格式测试
// ========================================

// TestFrame_NewFrame 测试创建帧
func TestFrame_NewFrame(t *testing.T) {
	data := []byte("test data")
	frame := NewFrame(MessageTypeGet, types.CodecTypeMessagePack, data)

	assert.Equal(t, [4]byte{'N', 'x', 'K', 'V'}, frame.Magic)
	assert.Equal(t, MessageTypeGet, frame.Type)
	assert.Equal(t, uint32(len(data)), frame.Length)
	assert.NotEqual(t, uint32(0), frame.CRC32) // CRC32 应该被计算
	assert.Equal(t, data, frame.Data)
}

// TestFrame_Marshal 测试帧序列化
func TestFrame_Marshal(t *testing.T) {
	data := []byte("test data")
	frame := NewFrame(MessageTypePut, types.CodecTypeMessagePack, data)

	// 序列化
	buf, err := frame.Marshal()
	require.NoError(t, err)

	// 验证大小
	expectedSize := FrameHeaderSize + len(data)
	assert.Equal(t, expectedSize, len(buf))

	// 验证 Magic (0-3)
	assert.Equal(t, []byte("NxKV"), buf[0:4])

	// 验证 Type (4-5)
	assert.Equal(t, uint16(MessageTypePut), binary.BigEndian.Uint16(buf[4:6]))

	// 验证 CodecType (6-7)
	assert.Equal(t, uint16(types.CodecTypeMessagePack), binary.BigEndian.Uint16(buf[6:8]))

	// 验证 Length (8-11)
	assert.Equal(t, uint32(len(data)), binary.BigEndian.Uint32(buf[8:12]))
}

// TestFrame_Unmarshal 测试帧反序列化
func TestFrame_Unmarshal(t *testing.T) {
	data := []byte("test data")
	frame1 := NewFrame(MessageTypeDelete, types.CodecTypeMessagePack, data)

	// 序列化
	buf, err := frame1.Marshal()
	require.NoError(t, err)

	// 反序列化
	frame2 := &Frame{}
	err = frame2.Unmarshal(buf)
	require.NoError(t, err)

	// 验证
	assert.Equal(t, frame1.Magic, frame2.Magic)
	assert.Equal(t, frame1.Type, frame2.Type)
	assert.Equal(t, frame1.CodecType, frame2.CodecType)
	assert.Equal(t, frame1.Length, frame2.Length)
	assert.Equal(t, frame1.Data, frame2.Data)
}

// TestFrame_Unmarshal_InvalidMagic 测试无效魔数
func TestFrame_Unmarshal_InvalidMagic(t *testing.T) {
	data := []byte("TEST") // 无效魔数
	buf := make([]byte, FrameHeaderSize)
	copy(buf[0:4], data)

	frame := &Frame{}
	err := frame.Unmarshal(buf)
	require.Error(t, err)

	// 检查错误类型
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodeInvalidFrameMagic, cerr.Code)
	}
}

// TestFrame_Unmarshal_InvalidSize 测试无效帧大小
func TestFrame_Unmarshal_InvalidSize(t *testing.T) {
	buf := make([]byte, FrameHeaderSize-1) // 太小

	frame := &Frame{}
	err := frame.Unmarshal(buf)
	require.Error(t, err)

	// 检查错误类型
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodeInvalidFrameSize, cerr.Code)
	}
}

// TestFrame_VerifyChecksum 测试校验和验证
func TestFrame_VerifyChecksum(t *testing.T) {
	data := []byte("test data")
	frame := NewFrame(MessageTypeGet, types.CodecTypeMessagePack, data)

	// 序列化
	buf, err := frame.Marshal()
	require.NoError(t, err)

	// 修改数据破坏校验和 (修改 Data 部分)
	buf[16] = 'X'

	// 反序列化应该失败
	frame2 := &Frame{}
	err = frame2.Unmarshal(buf)
	require.Error(t, err)

	// 检查错误类型
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodeFrameChecksum, cerr.Code)
	}
}

// TestFrame_HexDump 测试十六进制转储
func TestFrame_HexDump(t *testing.T) {
	data := []byte("test")
	frame := NewFrame(MessageTypeGet, types.CodecTypeMessagePack, data)

	dump := frame.HexDump()
	assert.Contains(t, dump, "NxKV") // 魔数应该在转储中
	assert.NotEmpty(t, dump)
}

// ========================================
// 帧读写器测试
// ========================================

// TestFrameReader_ReadFrame 测试帧读取
func TestFrameReader_ReadFrame(t *testing.T) {
	// 创建测试帧
	data := []byte("test data")
	frame := NewFrame(MessageTypeGet, types.CodecTypeMessagePack, data)
	buf, err := frame.Marshal()
	require.NoError(t, err)

	// 创建读取器
	reader := NewFrameReader(bytes.NewReader(buf))

	// 读取帧
	readFrame, err := reader.ReadFrame()
	require.NoError(t, err)

	// 验证
	assert.Equal(t, frame.Magic, readFrame.Magic)
	assert.Equal(t, frame.Type, readFrame.Type)
	assert.Equal(t, frame.Data, readFrame.Data)
}

// TestFrameReader_ReadFrame_InvalidMagic 测试读取无效魔数帧
func TestFrameReader_ReadFrame_InvalidMagic(t *testing.T) {
	buf := make([]byte, FrameHeaderSize)
	copy(buf[0:4], "TEST") // 无效魔数

	reader := NewFrameReader(bytes.NewReader(buf))

	_, err := reader.ReadFrame()
	require.Error(t, err)

	// 检查错误类型
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodeInvalidFrameMagic, cerr.Code)
	}
}

// TestFrameWriter_WriteFrame 测试帧写入
func TestFrameWriter_WriteFrame(t *testing.T) {
	// 创建测试帧
	data := []byte("test data")
	frame := NewFrame(MessageTypePut, types.CodecTypeMessagePack, data)

	// 创建写入器
	var buf bytes.Buffer
	writer := NewFrameWriter(&buf)

	// 写入帧
	err := writer.WriteFrame(frame)
	require.NoError(t, err)

	// 验证写入的数据
	written := buf.Bytes()
	assert.Equal(t, FrameHeaderSize+len(data), len(written))
	assert.Equal(t, []byte("NxKV"), written[0:4])
}

// ========================================
// 编解码器测试
// ========================================

// TestMessagePackCodec_EncodeDecode 测试编解码
func TestMessagePackCodec_EncodeDecode(t *testing.T) {
	codec := NewMessagePackCodec()

	// 创建测试消息
	msg := &GetMessage{
		Key: "test_key",
	}

	// 编码
	data, err := codec.Encode(msg)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// 解码
	decodedMsg, err := codec.Decode(data)
	require.NoError(t, err)

	// 验证
	getMsg, ok := decodedMsg.(*GetMessage)
	require.True(t, ok)
	assert.Equal(t, msg.Key, getMsg.Key)
}

// TestMessagePackCodec_AllMessageTypes 测试所有消息类型
func TestMessagePackCodec_AllMessageTypes(t *testing.T) {
	codec := NewMessagePackCodec()

	testCases := []Message{
		&GetMessage{Key: "test"},
		&PutMessage{Key: "test", Value: []byte("value")},
		&DeleteMessage{Key: "test"},
		&GetReplyMessage{Key: "test", Value: []byte("value"), Found: true, Version: 1},
		&PutReplyMessage{Key: "test", Success: true, Version: 1},
		&DeleteReplyMessage{Key: "test", Success: true},
		&GossipSyncMessage{Version: 1, Metadata: map[string][]byte{"key": []byte("value")}, Timestamp: time.Now().Unix()},
		&NodePingMessage{NodeID: "node1", Sequence: 1, Timestamp: time.Now().Unix()},
	}

	for _, msg := range testCases {
		t.Run(msg.Type().String(), func(t *testing.T) {
			// 编码
			data, err := codec.Encode(msg)
			require.NoError(t, err, "编码失败: %s", msg.Type())

			// 解码
			decodedMsg, err := codec.Decode(data)
			require.NoError(t, err, "解码失败: %s", msg.Type())

			// 验证类型
			assert.Equal(t, msg.Type(), decodedMsg.Type())
		})
	}
}

// TestMessagePackCodec_EncodeNil 测试编码空消息
func TestMessagePackCodec_EncodeNil(t *testing.T) {
	codec := NewMessagePackCodec()

	_, err := codec.Encode(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "消息为空")
}

// TestMessagePackCodec_DecodeEmpty 测试解码空数据
func TestMessagePackCodec_DecodeEmpty(t *testing.T) {
	codec := NewMessagePackCodec()

	_, err := codec.Decode([]byte{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "数据为空")
}

// ========================================
// Protobuf 编解码器测试
// ========================================

// TestProtobufCodec_EncodeDecode 测试 Protobuf 编解码
func TestProtobufCodec_EncodeDecode(t *testing.T) {
	codec := NewProtobufCodec()

	// 创建测试消息
	msg := &GetMessage{
		Key: "test_key",
	}

	// 编码
	data, err := codec.Encode(msg)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// 解码
	decodedMsg, err := codec.Decode(data)
	require.NoError(t, err)

	// 验证
	getMsg, ok := decodedMsg.(*GetMessage)
	require.True(t, ok)
	assert.Equal(t, msg.Key, getMsg.Key)
}

// TestProtobufCodec_MetadataMessages 测试元数据操作消息
func TestProtobufCodec_MetadataMessages(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []Message{
		&GetMessage{Key: "test_key"},
		&PutMessage{Key: "test_key", Value: []byte("test_value")},
		&DeleteMessage{Key: "test_key"},
		&GetReplyMessage{Key: "test_key", Value: []byte("test_value"), Found: true, Version: 1},
		&PutReplyMessage{Key: "test_key", Success: true, Version: 1},
		&DeleteReplyMessage{Key: "test_key", Success: true},
	}

	for _, msg := range testCases {
		t.Run(msg.Type().String(), func(t *testing.T) {
			// 编码
			data, err := codec.Encode(msg)
			require.NoError(t, err, "编码失败: %s", msg.Type())

			// 解码
			decodedMsg, err := codec.Decode(data)
			require.NoError(t, err, "解码失败: %s", msg.Type())

			// 验证类型
			assert.Equal(t, msg.Type(), decodedMsg.Type())
		})
	}
}

// TestProtobufCodec_GossipMessages 测试 Gossip 协议消息
func TestProtobufCodec_GossipMessages(t *testing.T) {
	codec := NewProtobufCodec()

	// GossipSyncMessage
	msg1 := &GossipSyncMessage{
		Version:  123,
		Metadata: map[string][]byte{"key1": []byte("value1")},
	}
	data1, err := codec.Encode(msg1)
	require.NoError(t, err)
	decoded1, err := codec.Decode(data1)
	require.NoError(t, err)
	assert.Equal(t, MessageTypeGossipSync, decoded1.Type())

	// GossipDigestMessage
	msg2 := &GossipDigestMessage{
		Version: 456,
		Digest:  map[string]uint64{"key1": 789},
	}
	data2, err := codec.Encode(msg2)
	require.NoError(t, err)
	decoded2, err := codec.Decode(data2)
	require.NoError(t, err)
	assert.Equal(t, MessageTypeGossipDigest, decoded2.Type())
}

// TestProtobufCodec_TwoPCMessages 测试 2PC 协议消息
func TestProtobufCodec_TwoPCMessages(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []Message{
		&TwoPCPrepareMessage{
			TransactionID: "txn-1",
			Participants:  []string{"node1", "node2"},
		},
		&TwoPCCommitMessage{
			TransactionID: "txn-1",
		},
		&TwoPCRollbackMessage{
			TransactionID: "txn-1",
		},
	}

	for _, msg := range testCases {
		t.Run(msg.Type().String(), func(t *testing.T) {
			// 编码
			data, err := codec.Encode(msg)
			require.NoError(t, err, "编码失败: %s", msg.Type())

			// 解码
			decodedMsg, err := codec.Decode(data)
			require.NoError(t, err, "解码失败: %s", msg.Type())

			// 验证类型
			assert.Equal(t, msg.Type(), decodedMsg.Type())
		})
	}
}

// TestProtobufCodec_NodeMessages 测试节点管理消息
func TestProtobufCodec_NodeMessages(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []Message{
		&NodePingMessage{NodeID: "node-1", Sequence: 1, Timestamp: time.Now().Unix()},
		&NodePongMessage{NodeID: "node-1", Sequence: 1, Timestamp: time.Now().Unix()},
		&NodeJoinMessage{NodeID: "node-1", Addr: "127.0.0.1:9211"},
		&NodeLeaveMessage{NodeID: "node-1"},
	}

	for _, msg := range testCases {
		t.Run(msg.Type().String(), func(t *testing.T) {
			// 编码
			data, err := codec.Encode(msg)
			require.NoError(t, err, "编码失败: %s", msg.Type())

			// 解码
			decodedMsg, err := codec.Decode(data)
			require.NoError(t, err, "解码失败: %s", msg.Type())

			// 验证类型
			assert.Equal(t, msg.Type(), decodedMsg.Type())
		})
	}
}

// TestProtobufCodec_EncodeNil 测试编码空消息
func TestProtobufCodec_EncodeNil(t *testing.T) {
	codec := NewProtobufCodec()

	_, err := codec.Encode(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "消息为空")
}

// TestProtobufCodec_DecodeEmpty 测试解码空数据
func TestProtobufCodec_DecodeEmpty(t *testing.T) {
	codec := NewProtobufCodec()

	_, err := codec.Decode([]byte{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "数据为空")
}

// TestProtobufCodec_DecodeInvalidLength 测试解码无效长度数据
func TestProtobufCodec_DecodeInvalidLength(t *testing.T) {
	codec := NewProtobufCodec()

	// 数据不足 6 字节
	_, err := codec.Decode([]byte{1, 2, 3, 4, 5})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "数据长度不足")
}

// TestNewCodec_Protobuf 测试工厂函数创建 Protobuf 编解码器
func TestNewCodec_Protobuf(t *testing.T) {
	codec, err := NewCodec(types.CodecTypeProtobuf)
	require.NoError(t, err)
	assert.NotNil(t, codec)
	assert.Equal(t, "protobuf", codec.Name())
}

// ========================================
// createMessageByType 测试
// ========================================

// TestCreateMessageByType 测试创建消息实例
func TestCreateMessageByType(t *testing.T) {
	testCases := []struct {
		msgType     MessageType
		expectedMsg Message
	}{
		{MessageTypeGet, &GetMessage{}},
		{MessageTypePut, &PutMessage{}},
		{MessageTypeDelete, &DeleteMessage{}},
		{MessageTypeNodePing, &NodePingMessage{}},
		{MessageTypeNodePong, &NodePongMessage{}},
	}

	for _, tc := range testCases {
		t.Run(tc.msgType.String(), func(t *testing.T) {
			msg, err := createMessageByType(tc.msgType)

			require.NoError(t, err)
			assert.IsType(t, tc.expectedMsg, msg)
			assert.Equal(t, tc.msgType, msg.Type())
		})
	}
}

// TestCreateMessageByType_UnknownType 测试未知消息类型
func TestCreateMessageByType_UnknownType(t *testing.T) {
	_, err := createMessageByType(MessageType(999))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未知消息类型")
}

// ========================================
// 消息辅助函数测试
// ========================================

// TestEncodeFrame_DecodeFrame 测试帧编解码辅助函数
func TestEncodeFrame_DecodeFrame(t *testing.T) {
	// 创建消息
	msg := &PutMessage{
		Key:   "test_key",
		Value: []byte("test_value"),
	}

	// 编码为帧
	frame, err := EncodeFrame(msg)
	require.NoError(t, err)
	assert.NotNil(t, frame)
	assert.Equal(t, MessageTypePut, frame.Type)

	// 从帧解码
	decodedMsg, err := DecodeFrame(frame)
	require.NoError(t, err)

	putMsg, ok := decodedMsg.(*PutMessage)
	require.True(t, ok)
	assert.Equal(t, msg.Key, putMsg.Key)
	assert.Equal(t, msg.Value, putMsg.Value)
}

// TestEncodeFrame_DecodeFrame_AllTypes 测试所有消息类型的帧编解码
func TestEncodeFrame_DecodeFrame_AllTypes(t *testing.T) {
	testCases := []Message{
		&GetMessage{Key: "test"},
		&PutMessage{Key: "test", Value: []byte("value")},
		&DeleteMessage{Key: "test"},
		&NodePingMessage{NodeID: "node1", Sequence: 1, Timestamp: time.Now().Unix()},
		&NodePongMessage{NodeID: "node1", Sequence: 1, Status: "ready"},
	}

	for _, msg := range testCases {
		t.Run(msg.Type().String(), func(t *testing.T) {
			// 编码为帧
			frame, err := EncodeFrame(msg)
			require.NoError(t, err)

			// 从帧解码
			decodedMsg, err := DecodeFrame(frame)
			require.NoError(t, err)

			// 验证类型
			assert.Equal(t, msg.Type(), decodedMsg.Type())
		})
	}
}

// ========================================
// MessageReader/Writer 测试
// ========================================

// TestMessageReader_ReadMessage 测试消息读取器
func TestMessageReader_ReadMessage(t *testing.T) {
	// 创建消息
	msg := &GetMessage{Key: "test_key"}

	// 编码为帧
	frame, err := EncodeFrame(msg)
	require.NoError(t, err)

	// 序列化帧
	frameData, err := frame.Marshal()
	require.NoError(t, err)

	// 创建读取器
	reader := NewMessageReader(bytes.NewReader(frameData), nil)

	// 读取消息
	readMsg, err := reader.ReadMessage()
	require.NoError(t, err)

	// 验证
	getMsg, ok := readMsg.(*GetMessage)
	require.True(t, ok)
	assert.Equal(t, msg.Key, getMsg.Key)
}

// TestMessageWriter_WriteMessage 测试消息写入器
func TestMessageWriter_WriteMessage(t *testing.T) {
	// 创建消息
	msg := &PutMessage{
		Key:   "test_key",
		Value: []byte("test_value"),
	}

	// 创建写入器
	var buf bytes.Buffer
	writer := NewMessageWriter(&buf, nil)

	// 写入消息
	err := writer.WriteMessage(msg)
	require.NoError(t, err)

	// 验证写入了数据
	assert.Greater(t, buf.Len(), 0)

	// 读取并验证
	frame := &Frame{}
	frameData := buf.Bytes()
	err = frame.Unmarshal(frameData)
	require.NoError(t, err)

	assert.Equal(t, MessageTypePut, frame.Type)
}

// ========================================
// 配置测试
// ========================================

// TestDefaultTransportConfig 测试默认配置
func TestDefaultTransportConfig(t *testing.T) {
	config := DefaultTransportConfig()

	assert.Equal(t, "0.0.0.0:9211", config.ListenAddr)
	assert.Equal(t, int64(1024*1024*100), config.MaxMessageSize)
	assert.Equal(t, 30*time.Second, config.ReadTimeout)
	assert.Equal(t, 30*time.Second, config.WriteTimeout)
	assert.Equal(t, 10*time.Second, config.KeepAliveInterval)
	assert.Equal(t, 30*time.Second, config.KeepAliveTimeout)
	assert.Equal(t, 4096, config.BufferSize)
}

// TestAddress_String 测试地址字符串格式化
func TestAddress_String(t *testing.T) {
	tests := []struct {
		name     string
		address  *Address
		expected string
	}{
		{
			name:     "IPv4地址",
			address:  &Address{Host: "127.0.0.1", Port: 9211},
			expected: "127.0.0.1:9211",
		},
		{
			name:     "IPv6地址",
			address:  &Address{Host: "::1", Port: 8080},
			expected: "::1:8080",
		},
		{
			name:     "主机名",
			address:  &Address{Host: "localhost", Port: 9211},
			expected: "localhost:9211",
		},
		{
			name:     "域名",
			address:  &Address{Host: "example.com", Port: 443},
			expected: "example.com:443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.address.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ========================================
// 错误处理测试
// ========================================

// TestTransportError_Error 测试传输错误
func TestTransportError_Error(t *testing.T) {
	err := types.NewTransportConnectionError("Send", "localhost:9211", io.EOF)

	errMsg := err.Error()
	assert.Contains(t, errMsg, "Send")
	assert.Contains(t, errMsg, "localhost:9211")
	assert.Contains(t, errMsg, "EOF")
}

// TestTransportError_Unwrap 测试错误解包
func TestTransportError_Unwrap(t *testing.T) {
	originalErr := io.EOF
	err := types.NewTransportConnectionError("Send", "", originalErr)

	assert.Equal(t, originalErr, err.Unwrap())
}

// TestTransportError_Timeout 测试超时错误
func TestTransportError_Timeout(t *testing.T) {
	err := types.NewTransportTimeoutError("test")
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "test")
}
