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
// TLV 帧格式测试
// ========================================

// TestTLVFrame_NewFrame 测试创建 TLV 帧
func TestTLVFrame_NewFrame(t *testing.T) {
	data := []byte("test data")
	frame := NewFrame(12345, 1, MessageTypeGet, uint16(types.CodecTypeMessagePack), data)

	// 验证固定头
	assert.Equal(t, [4]byte{'N', 'X', 'U', 'T'}, frame.FixedHeader.Magic)
	assert.Equal(t, uint8(1), frame.FixedHeader.Version) // 验证 Version
	assert.Equal(t, uint64(12345), frame.FixedHeader.NodeID)
	assert.Equal(t, uint64(1), frame.FixedHeader.MsgSeq)
	assert.Equal(t, uint16(types.CodecTypeMessagePack), frame.FixedHeader.CodecID)
	assert.Equal(t, data, frame.Data)
}

// TestTLVFrame_Marshal 测试 TLV 帧序列化
func TestTLVFrame_Marshal(t *testing.T) {
	data := []byte("test data")
	frame := NewFrame(12345, 1, MessageTypeGet, uint16(types.CodecTypeMessagePack), data)

	// 序列化
	buf, err := frame.Marshal()
	require.NoError(t, err)

	// 验证最小大小（FixedHeader 31B + CRC32 4B = 35B，无扩展头）
	assert.GreaterOrEqual(t, len(buf), 35)

	// 验证 Magic (0-3)
	assert.Equal(t, []byte("NXUT"), buf[0:4])

	// 验证 Version (4) - 当前协议版本为 1
	assert.Equal(t, uint8(1), buf[4])

	// 验证 NodeID (5-12)
	assert.Equal(t, uint64(12345), binary.BigEndian.Uint64(buf[5:13]))

	// 验证 MsgSeq (13-20)
	assert.Equal(t, uint64(1), binary.BigEndian.Uint64(buf[13:21]))

	// 验证 MsgType (21-22)
	assert.Equal(t, uint16(MessageTypeGet), binary.BigEndian.Uint16(buf[21:23]))

	// 验证 CodecID (23-24)
	assert.Equal(t, uint16(types.CodecTypeMessagePack), binary.BigEndian.Uint16(buf[23:25]))

	// 验证 ExtHeaderLen (25-26) - 无扩展头，应该为 0
	assert.Equal(t, uint16(0), binary.BigEndian.Uint16(buf[25:27]))

	// 验证 DataLength (27-30) - 数据长度
	dataLength := binary.BigEndian.Uint32(buf[27:31])
	assert.Equal(t, uint32(len(data)), dataLength)
}

// TestTLVFrame_Unmarshal 测试 TLV 帧反序列化
func TestTLVFrame_Unmarshal(t *testing.T) {
	data := []byte("test data")
	frame1 := NewFrame(12345, 1, MessageTypeGet, uint16(types.CodecTypeMessagePack), data)

	// 序列化
	buf, err := frame1.Marshal()
	require.NoError(t, err)

	// 反序列化
	frame2 := &Frame{}
	err = frame2.Unmarshal(buf)
	require.NoError(t, err)

	// 验证
	assert.Equal(t, frame1.FixedHeader.Magic, frame2.FixedHeader.Magic)
	assert.Equal(t, frame1.FixedHeader.NodeID, frame2.FixedHeader.NodeID)
	assert.Equal(t, frame1.FixedHeader.MsgSeq, frame2.FixedHeader.MsgSeq)
	assert.Equal(t, frame1.FixedHeader.CodecID, frame2.FixedHeader.CodecID)
	assert.Equal(t, frame1.Data, frame2.Data)
}

// TestTLVFrame_Unmarshal_InvalidMagic 测试无效魔数
func TestTLVFrame_Unmarshal_InvalidMagic(t *testing.T) {
	buf := make([]byte, FixedHeaderLen+4) // FixedHeader + CRC32 (无扩展头，无数据)
	copy(buf[0:4], "TEST")                // 无效魔数

	frame := &Frame{}
	err := frame.Unmarshal(buf)
	require.Error(t, err)

	// 检查错误类型
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodeInvalidFrameMagic, cerr.Code)
	}
}

// TestTLVFrame_Unmarshal_InvalidSize 测试无效帧大小
func TestTLVFrame_Unmarshal_InvalidSize(t *testing.T) {
	buf := make([]byte, FixedHeaderLen-1) // 太小

	frame := &Frame{}
	err := frame.Unmarshal(buf)
	require.Error(t, err)

	// 检查错误类型
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodeInvalidFrameSize, cerr.Code)
	}
}

// TestTLVFrame_VerifyChecksum 测试校验和验证
func TestTLVFrame_VerifyChecksum(t *testing.T) {
	data := []byte("test data")
	frame := NewFrame(12345, 1, MessageTypeGet, uint16(types.CodecTypeMessagePack), data)

	// 序列化
	buf, err := frame.Marshal()
	require.NoError(t, err)

	// 修改数据破坏校验和（修改最后一个字节）
	buf[len(buf)-1] = 'X'

	// 反序列化应该失败
	frame2 := &Frame{}
	err = frame2.Unmarshal(buf)
	require.Error(t, err)

	// 检查错误类型
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodeFrameChecksum, cerr.Code)
	}
}

// TestTLVFrame_WithExtensions 测试带扩展字段的帧
func TestTLVFrame_WithExtensions(t *testing.T) {
	data := []byte("test data")
	frame := NewFrame(12345, 1, MessageTypeGet, uint16(types.CodecTypeMessagePack), data)

	// 添加扩展字段（空扩展）
	frame.VarExtHeader = NewVarExtHeader() // 没有扩展字段

	// 序列化
	buf, err := frame.Marshal()
	require.NoError(t, err)

	// 反序列化
	frame2 := &Frame{}
	err = frame2.Unmarshal(buf)
	require.NoError(t, err)

	// 验证
	assert.Equal(t, frame.FixedHeader.Magic, frame2.FixedHeader.Magic)
	assert.Equal(t, frame.FixedHeader.NodeID, frame2.FixedHeader.NodeID)
	assert.Equal(t, frame.FixedHeader.MsgSeq, frame2.FixedHeader.MsgSeq)
	assert.Equal(t, frame.FixedHeader.CodecID, frame2.FixedHeader.CodecID)
	assert.Equal(t, frame.Data, frame2.Data)
}

// TestFixedHeader_SerializeDeserialize 测试固定头序列化/反序列化
func TestFixedHeader_SerializeDeserialize(t *testing.T) {
	header := &FixedHeader{
		Magic:   [4]byte{'N', 'X', 'U', 'T'},
		NodeID:  12345,
		MsgSeq:  1,
		CodecID: uint16(types.CodecTypeMessagePack),
	}

	// 序列化
	buf := header.Serialize()

	// 反序列化
	header2, err := DeserializeFixedHeader(buf)
	require.NoError(t, err)

	// 验证
	assert.Equal(t, header.Magic, header2.Magic)
	assert.Equal(t, header.NodeID, header2.NodeID)
	assert.Equal(t, header.MsgSeq, header2.MsgSeq)
	assert.Equal(t, header.CodecID, header2.CodecID)
}

// ========================================
// 帧读写器测试
// ========================================

// TestFrameReader_ReadFrame 测试帧读取
func TestFrameReader_ReadFrame(t *testing.T) {
	// 创建测试帧
	data := []byte("test data")
	frame := NewFrame(12345, 1, MessageTypeGet, uint16(types.CodecTypeMessagePack), data)
	buf, err := frame.Marshal()
	require.NoError(t, err)

	// 创建读取器
	reader := NewFrameReader(bytes.NewReader(buf))

	// 读取帧
	readFrame, err := reader.ReadFrame()
	require.NoError(t, err)

	// 验证
	assert.Equal(t, frame.FixedHeader.Magic, readFrame.FixedHeader.Magic)
	assert.Equal(t, frame.FixedHeader.NodeID, readFrame.FixedHeader.NodeID)
	assert.Equal(t, frame.Data, readFrame.Data)
}

// TestFrameReader_ReadFrame_InvalidMagic 测试读取无效魔数帧
func TestFrameReader_ReadFrame_InvalidMagic(t *testing.T) {
	buf := make([]byte, FixedHeaderLen+2+4)   // FixedHeader + ExtTotalLen + CRC32
	copy(buf[0:4], "TEST")                    // 无效魔数
	binary.BigEndian.PutUint16(buf[16:18], 2) // ExtTotalLen = 2

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
	frame := NewFrame(12345, 1, MessageTypeGet, uint16(types.CodecTypeMessagePack), data)

	// 创建写入器
	var buf bytes.Buffer
	writer := NewFrameWriter(&buf)

	// 写入帧
	err := writer.WriteFrame(frame)
	require.NoError(t, err)

	// 验证写入的数据
	written := buf.Bytes()
	assert.GreaterOrEqual(t, len(written), FixedHeaderLen+2+4) // FixedHeader + ExtTotalLen + CRC32
	assert.Equal(t, []byte("NXUT"), written[0:4])
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
	decodedMsg, err := codec.Decode(msg.Type(), data)
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
			decodedMsg, err := codec.Decode(msg.Type(), data)
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

	_, err := codec.Decode(MessageTypeGet, []byte{})
	assert.Error(t, err)
	// MessagePack 返回 EOF 错误，包装后的错误消息包含"解码失败"
	assert.Contains(t, err.Error(), "解码失败")
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
	decodedMsg, err := codec.Decode(msg.Type(), data)
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
			decodedMsg, err := codec.Decode(msg.Type(), data)
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
	decoded1, err := codec.Decode(msg1.Type(), data1)
	require.NoError(t, err)
	assert.Equal(t, MessageTypeGossipSync, decoded1.Type())

	// GossipDigestMessage
	msg2 := &GossipDigestMessage{
		Version: 456,
		Digest:  map[string]uint64{"key1": 789},
	}
	data2, err := codec.Encode(msg2)
	require.NoError(t, err)
	decoded2, err := codec.Decode(msg1.Type(), data2)
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
			decodedMsg, err := codec.Decode(msg.Type(), data)
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
			decodedMsg, err := codec.Decode(msg.Type(), data)
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

	_, err := codec.Decode(MessageTypeGet, []byte{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "数据为空")
}

// TestProtobufCodec_DecodeInvalidLength 测试解码无效长度数据
func TestProtobufCodec_DecodeInvalidLength(t *testing.T) {
	codec := NewProtobufCodec()

	// 无效的 Protobuf 数据
	_, err := codec.Decode(MessageTypeGet, []byte{1, 2, 3, 4, 5})
	assert.Error(t, err)
	// Protobuf 返回格式错误，包装后的错误消息包含"解码失败"
	assert.Contains(t, err.Error(), "解码失败")
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
	frame, err := EncodeFrame(msg, 0, 0)
	require.NoError(t, err)
	assert.NotNil(t, frame)
	assert.Equal(t, MessageTypePut, msg.Type())

	// 从帧解码
	decoded, err := DecodeFrame(frame)
	require.NoError(t, err)

	putMsg, ok := decoded.Msg.(*PutMessage)
	require.True(t, ok)
	assert.Equal(t, msg.Key, putMsg.Key)
	assert.Equal(t, msg.Value, putMsg.Value)

	// 验证 nodeID 和 msgSeq
	assert.Equal(t, uint64(0), decoded.NodeID)
	assert.Equal(t, uint64(0), decoded.MsgSeq)
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
			frame, err := EncodeFrame(msg, 0, 0)
			require.NoError(t, err)

			// 从帧解码
			decoded, err := DecodeFrame(frame)
			require.NoError(t, err)

			// 验证类型
			assert.Equal(t, msg.Type(), decoded.Msg.Type())
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
	frame, err := EncodeFrame(msg, 0, 0)
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

	// defaultCodec 是 Protobuf，所以 CodecID 应该是 CodecTypeProtobuf
	assert.Equal(t, uint16(types.CodecTypeProtobuf), frame.FixedHeader.CodecID)
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

// ========================================
// 三 Codec 一致性测试
// ========================================

// TestThreeCodecConsistency 测试三种编解码器的一致性
// 验证 struct → JSON/MsgPack/Protobuf → struct 后数据保持一致
func TestThreeCodecConsistency(t *testing.T) {
	testMessages := []Message{
		&PutMessage{
			Key:   "test_key",
			Value: []byte("test_value"),
		},
		&GetMessage{
			Key: "get_key",
		},
		&DeleteMessage{
			Key: "delete_key",
		},
		&NodeJoinMessage{
			NodeID:   "node-1",
			Addr:     "192.168.1.10:9211",
			Role:     "follower",
			ParentID: "node-0",
		},
		&NodeLeaveMessage{
			NodeID: "node-2",
			Reason: "手动下线",
		},
		&NodePingMessage{
			NodeID:    "node-3",
			Sequence:  100,
			Timestamp: 1705689600000000000,
		},
		&NodePongMessage{
			NodeID:   "node-3",
			Sequence: 100,
			Status:   "ready",
		},
		&GossipSyncMessage{
			Version:   100,
			Metadata:  map[string][]byte{"key1": []byte("value1")},
			Timestamp: 1705689600000000000,
		},
		&TwoPCPrepareMessage{
			TransactionID: "tx-123",
			Participants:  []string{"node1", "node2"},
			Operations: []Operation{
				{Type: "put", Key: "k1", Value: []byte("v1")},
				{Type: "put", Key: "k2", Value: []byte("v2")},
			},
			Timeout: 30000,
		},
	}

	for _, originalMsg := range testMessages {
		t.Run(originalMsg.Type().String(), func(t *testing.T) {
			// 测试三种 Codec
			codecs := []struct {
				name  string
				codec Codec
			}{
				{"JSON", NewJSONCodec()},
				{"MessagePack", NewMessagePackCodec()},
				{"Protobuf", NewProtobufCodec()},
			}

			for _, tc := range codecs {
				t.Run(tc.name, func(t *testing.T) {
					// 编码
					encoded, err := tc.codec.Encode(originalMsg)
					require.NoError(t, err, "编码失败: %s", tc.name)
					require.NotNil(t, encoded, "编码结果不应为空")

					// 解码
					decodedMsg, err := tc.codec.Decode(originalMsg.Type(), encoded)
					require.NoError(t, err, "解码失败: %s", tc.name)
					require.NotNil(t, decodedMsg, "解码结果不应为空")

					// 验证类型
					assert.Equal(t, originalMsg.Type(), decodedMsg.Type(),
						"消息类型不一致: %s", tc.name)

					// 验证内容一致性
					assertMessagesEqual(t, originalMsg, decodedMsg, tc.name)
				})
			}
		})
	}
}

// assertMessagesEqual 验证两个消息内容相等
func assertMessagesEqual(t *testing.T, expected, actual Message, codecName string) {
	t.Helper()

	switch exp := expected.(type) {
	case *PutMessage:
		act, ok := actual.(*PutMessage)
		require.True(t, ok, "%s: 应该是 PutMessage", codecName)
		assert.Equal(t, exp.Key, act.Key, "%s: Key 不一致", codecName)
		assert.Equal(t, exp.Value, act.Value, "%s: Value 不一致", codecName)

	case *GetMessage:
		act, ok := actual.(*GetMessage)
		require.True(t, ok, "%s: 应该是 GetMessage", codecName)
		assert.Equal(t, exp.Key, act.Key, "%s: Key 不一致", codecName)

	case *DeleteMessage:
		act, ok := actual.(*DeleteMessage)
		require.True(t, ok, "%s: 应该是 DeleteMessage", codecName)
		assert.Equal(t, exp.Key, act.Key, "%s: Key 不一致", codecName)

	case *NodeJoinMessage:
		act, ok := actual.(*NodeJoinMessage)
		require.True(t, ok, "%s: 应该是 NodeJoinMessage", codecName)
		assert.Equal(t, exp.NodeID, act.NodeID, "%s: NodeID 不一致", codecName)
		assert.Equal(t, exp.Addr, act.Addr, "%s: Addr 不一致", codecName)
		assert.Equal(t, exp.Role, act.Role, "%s: Role 不一致", codecName)
		assert.Equal(t, exp.ParentID, act.ParentID, "%s: ParentID 不一致", codecName)

	case *NodeLeaveMessage:
		act, ok := actual.(*NodeLeaveMessage)
		require.True(t, ok, "%s: 应该是 NodeLeaveMessage", codecName)
		assert.Equal(t, exp.NodeID, act.NodeID, "%s: NodeID 不一致", codecName)
		assert.Equal(t, exp.Reason, act.Reason, "%s: Reason 不一致", codecName)

	case *NodePingMessage:
		act, ok := actual.(*NodePingMessage)
		require.True(t, ok, "%s: 应该是 NodePingMessage", codecName)
		assert.Equal(t, exp.NodeID, act.NodeID, "%s: NodeID 不一致", codecName)
		assert.Equal(t, exp.Sequence, act.Sequence, "%s: Sequence 不一致", codecName)
		assert.Equal(t, exp.Timestamp, act.Timestamp, "%s: Timestamp 不一致", codecName)

	case *NodePongMessage:
		act, ok := actual.(*NodePongMessage)
		require.True(t, ok, "%s: 应该是 NodePongMessage", codecName)
		assert.Equal(t, exp.NodeID, act.NodeID, "%s: NodeID 不一致", codecName)
		assert.Equal(t, exp.Sequence, act.Sequence, "%s: Sequence 不一致", codecName)
		assert.Equal(t, exp.Status, act.Status, "%s: Status 不一致", codecName)

	case *GossipSyncMessage:
		act, ok := actual.(*GossipSyncMessage)
		require.True(t, ok, "%s: 应该是 GossipSyncMessage", codecName)
		assert.Equal(t, exp.Version, act.Version, "%s: Version 不一致", codecName)
		assert.Equal(t, exp.Timestamp, act.Timestamp, "%s: Timestamp 不一致", codecName)
		// Metadata 是 map，比较需要特殊处理
		assert.Len(t, act.Metadata, len(exp.Metadata), "%s: Metadata 长度不一致", codecName)
		for k, v := range exp.Metadata {
			assert.Equal(t, v, act.Metadata[k], "%s: Metadata[%s] 不一致", codecName, k)
		}

	case *TwoPCPrepareMessage:
		act, ok := actual.(*TwoPCPrepareMessage)
		require.True(t, ok, "%s: 应该是 TwoPCPrepareMessage", codecName)
		assert.Equal(t, exp.TransactionID, act.TransactionID, "%s: TransactionID 不一致", codecName)
		assert.Equal(t, exp.Participants, act.Participants, "%s: Participants 不一致", codecName)
		assert.Equal(t, exp.Timeout, act.Timeout, "%s: Timeout 不一致", codecName)
		assert.Len(t, act.Operations, len(exp.Operations), "%s: Operations 长度不一致", codecName)
		for i := range exp.Operations {
			assert.Equal(t, exp.Operations[i].Type, act.Operations[i].Type, "%s: Operations[%d].Type 不一致", codecName, i)
			assert.Equal(t, exp.Operations[i].Key, act.Operations[i].Key, "%s: Operations[%d].Key 不一致", codecName, i)
			assert.Equal(t, exp.Operations[i].Value, act.Operations[i].Value, "%s: Operations[%d].Value 不一致", codecName, i)
		}

	default:
		t.Fatalf("不支持的消息类型: %T", expected)
	}
}
