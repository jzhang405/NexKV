// Package transport 传输层测试
package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
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
	frame := NewFrame(12345, 1, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), FlagsIsRequest, data)

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
	frame := NewFrame(12345, 1, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), FlagsIsRequest, data)

	// 序列化
	buf, err := frame.Marshal()
	require.NoError(t, err)

	// 验证最小大小（FixedHeader 42B + CRC32 4B = 46B，无扩展头）
	assert.GreaterOrEqual(t, len(buf), 46)

	// 验证 Magic (0-3)
	assert.Equal(t, []byte("NXUT"), buf[0:4])

	// 验证 Version (4) - 当前协议版本为 1
	assert.Equal(t, uint8(1), buf[4])

	// 验证 Flags (5) - FlagsIsRequest = 0x01
	assert.Equal(t, uint8(FlagsIsRequest), buf[5])

	// 验证 NodeID (6-13)
	assert.Equal(t, uint64(12345), binary.BigEndian.Uint64(buf[6:14]))

	// 验证 MsgSeq (14-21)
	assert.Equal(t, uint64(1), binary.BigEndian.Uint64(buf[14:22]))

	// 验证 ForwardNodeID (22-29) - 原始消息，应该为 0
	assert.Equal(t, uint64(0), binary.BigEndian.Uint64(buf[22:30]))

	// 验证 Hops (30) - 初始值 MaxHops=10
	assert.Equal(t, uint8(MaxHops), buf[30])

	// 验证 Reserved (31) - 保留字段，应该为 0
	assert.Equal(t, uint8(0), buf[31])

	// 验证 MsgType (32-33)
	assert.Equal(t, uint16(types.MessageTypeGet), binary.BigEndian.Uint16(buf[32:34]))

	// 验证 CodecID (34-35)
	assert.Equal(t, uint16(types.CodecTypeMessagePack), binary.BigEndian.Uint16(buf[34:36]))

	// 验证 ExtHeaderLen (36-37) - 无扩展头，应该为 0
	assert.Equal(t, uint16(0), binary.BigEndian.Uint16(buf[36:38]))

	// 验证 DataLength (38-41) - 数据长度
	dataLength := binary.BigEndian.Uint32(buf[38:42])
	assert.Equal(t, uint32(len(data)), dataLength)
}

// TestTLVFrame_Unmarshal 测试 TLV 帧反序列化
func TestTLVFrame_Unmarshal(t *testing.T) {
	data := []byte("test data")
	frame1 := NewFrame(12345, 1, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), FlagsIsRequest, data)

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
	frame := NewFrame(12345, 1, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), FlagsIsRequest, data)

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
	frame := NewFrame(12345, 1, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), FlagsIsRequest, data)

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
	frame := NewFrame(12345, 1, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), FlagsIsRequest, data)
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
	frame := NewFrame(12345, 1, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), FlagsIsRequest, data)

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
		BaseMessage: BaseMessage{MessageType: types.MessageTypeGet},
		Key:         "test_key",
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
		&GetMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGet}, Key: "test"},
		&PutMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypePut}, Key: "test", Value: []byte("value")},
		&DeleteMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeDelete}, Key: "test"},
		&GetReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGetReply}, Key: "test", Value: []byte("value"), Found: true, Version: 1},
		&PutReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypePutReply}, Key: "test", Success: true, Version: 1},
		&DeleteReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeDeleteReply}, Key: "test", Success: true},
		&GossipSyncMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipSync}, Version: 1, Metadata: map[string][]byte{"key": []byte("value")}, Timestamp: time.Now().Unix()},
		&NodePingMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodePing}, NodeID: "node1", Sequence: 1, Timestamp: time.Now().Unix()},
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

	_, err := codec.Decode(types.MessageTypeGet, []byte{})
	assert.Error(t, err)
	// MessagePack 返回 EOF 错误，包装后的错误消息包含"解码失败"
	assert.Contains(t, err.Error(), "解码失败")
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
		{types.MessageTypeGet, &GetMessage{}},
		{types.MessageTypePut, &PutMessage{}},
		{types.MessageTypeDelete, &DeleteMessage{}},
		{types.MessageTypeNodePing, &NodePingMessage{}},
		{types.MessageTypeNodePong, &NodePongMessage{}},
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
		BaseMessage: BaseMessage{MessageType: types.MessageTypePut},
		Key:         "test_key",
		Value:       []byte("test_value"),
	}

	// 编码为帧
	frame, err := EncodeFrame(msg, 0, 0)
	require.NoError(t, err)
	assert.NotNil(t, frame)
	assert.Equal(t, types.MessageTypePut, msg.Type())

	// 从帧解码
	msgFrame, err := DecodeFrame(frame)
	require.NoError(t, err)

	putMsg, ok := msgFrame.Message.(*PutMessage)
	require.True(t, ok)
	assert.Equal(t, msg.Key, putMsg.Key)
	assert.Equal(t, msg.Value, putMsg.Value)

	// 验证 nodeID 和 msgSeq
	assert.Equal(t, uint64(0), msgFrame.NodeID)
	assert.Equal(t, uint64(0), msgFrame.MsgSeq)
}

// TestEncodeFrame_DecodeFrame_AllTypes 测试所有消息类型的帧编解码
func TestEncodeFrame_DecodeFrame_AllTypes(t *testing.T) {
	testCases := []Message{
		&GetMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypeGet},
			Key:         "test",
		},
		&PutMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypePut},
			Key:         "test",
			Value:       []byte("value"),
		},
		&DeleteMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypeDelete},
			Key:         "test",
		},
		&NodePingMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypeNodePing},
			NodeID:      "node1",
			Sequence:    1,
			Timestamp:   time.Now().Unix(),
		},
		&NodePongMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypeNodePong},
			NodeID:      "node1",
			Sequence:    1,
			Status:      "ready",
		},
	}

	for _, msg := range testCases {
		t.Run(msg.Type().String(), func(t *testing.T) {
			// 编码为帧
			frame, err := EncodeFrame(msg, 0, 0)
			require.NoError(t, err)

			// 从帧解码
			msgFrame, err := DecodeFrame(frame)
			require.NoError(t, err)

			// 验证类型
			assert.Equal(t, msg.Type(), msgFrame.Message.Type())
		})
	}
}

// ========================================
// MessageReader/Writer 测试
// ========================================

// TestMessageReader_ReadMessage 测试消息读取器
func TestMessageReader_ReadMessage(t *testing.T) {
	// 创建消息
	msg := &GetMessage{
		BaseMessage: BaseMessage{MessageType: types.MessageTypeGet},
		Key:         "test_key",
	}

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

	// 写入消息（使用测试用的 nodeID 和 msgSeq）
	err := writer.WriteMessage(msg, 12345, 1)
	require.NoError(t, err)

	// 验证写入了数据
	assert.Greater(t, buf.Len(), 0)

	// 读取并验证
	frame := &Frame{}
	frameData := buf.Bytes()
	err = frame.Unmarshal(frameData)
	require.NoError(t, err)

	// defaultCodec 是 MessagePack，所以 CodecID 应该是 CodecTypeMessagePack
	assert.Equal(t, uint16(types.CodecTypeMessagePack), frame.FixedHeader.CodecID)

	// 验证 NodeID 和 MsgSeq
	assert.Equal(t, uint64(12345), frame.FixedHeader.NodeID)
	assert.Equal(t, uint64(1), frame.FixedHeader.MsgSeq)
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

// TestThreeCodecConsistency 测试两种编解码器的一致性
// 验证 struct → JSON/MsgPack → struct 后数据保持一致
func TestThreeCodecConsistency(t *testing.T) {
	testMessages := []Message{
		&PutMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypePut},
			Key:         "test_key",
			Value:       []byte("test_value"),
		},
		&GetMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypeGet},
			Key:         "get_key",
		},
		&DeleteMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypeDelete},
			Key:         "delete_key",
		},
		&NodeJoinMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypeNodeJoin},
			NodeID:      "node-1",
			Addr:        "192.168.1.10:9211",
			Role:        "follower",
			ParentID:    "node-0",
		},
		&NodeLeaveMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypeNodeLeave},
			NodeID:      "node-2",
			Reason:      "手动下线",
		},
		&NodePingMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypeNodePing},
			NodeID:      "node-3",
			Sequence:    100,
			Timestamp:   1705689600000000000,
		},
		&NodePongMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypeNodePong},
			NodeID:      "node-3",
			Sequence:    100,
			Status:      "ready",
		},
		&GossipSyncMessage{
			BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipSync},
			Version:     100,
			Metadata:    map[string][]byte{"key1": []byte("value1")},
			Timestamp:   1705689600000000000,
		},
		&TwoPCPrepareMessage{
			BaseMessage:   BaseMessage{MessageType: types.MessageType2PCPrepare},
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
			// 测试两种 Codec
			codecs := []struct {
				name  string
				codec Codec
			}{
				{"JSON", NewJSONCodec()},
				{"MessagePack", NewMessagePackCodec()},
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

// ========================================
// transport_common 公共实现测试
// ========================================

// TestValidateTransportConfig_ValidConfig 测试有效配置
func TestValidateTransportConfig_ValidConfig(t *testing.T) {
	testCases := []struct {
		name   string
		config *TransportConfig
	}{
		{
			name:   "默认配置",
			config: DefaultTransportConfig(),
		},
		{
			name: "最小有效配置",
			config: &TransportConfig{
				ListenAddr:         "127.0.0.1:8080",
				MaxMessageSize:     1,
				ReadTimeout:        0,
				WriteTimeout:       0,
				KeepAliveInterval:  0,
				KeepAliveTimeout:   0,
				BufferSize:         1,
				ChannelSendTimeout: 0,
			},
		},
		{
			name: "最大有效配置",
			config: &TransportConfig{
				ListenAddr:         "0.0.0.0:9211",
				MaxMessageSize:     1024 * 1024 * 1024, // 1GB
				ReadTimeout:        1 * time.Hour,
				WriteTimeout:       1 * time.Hour,
				KeepAliveInterval:  1 * time.Hour,
				KeepAliveTimeout:   1 * time.Hour,
				BufferSize:         65536, // 64KB
				ChannelSendTimeout: 1 * time.Hour,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTransportConfig(tc.config)
			assert.NoError(t, err)
		})
	}
}

// TestValidateTransportConfig_EmptyListenAddr 测试空监听地址
func TestValidateTransportConfig_EmptyListenAddr(t *testing.T) {
	config := &TransportConfig{
		ListenAddr:         "",
		MaxMessageSize:     1024,
		ReadTimeout:        30 * time.Second,
		WriteTimeout:       30 * time.Second,
		KeepAliveInterval:  10 * time.Second,
		KeepAliveTimeout:   30 * time.Second,
		BufferSize:         4096,
		ChannelSendTimeout: 5 * time.Second,
	}

	err := validateTransportConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "监听地址不能为空")
}

// TestValidateTransportConfig_InvalidMaxMessageSize 测试无效的最大消息大小
func TestValidateTransportConfig_InvalidMaxMessageSize(t *testing.T) {
	testCases := []struct {
		name           string
		maxMessageSize int64
		expectedErr    string
	}{
		{"零大小", 0, "最大消息大小必须在"},
		{"负大小", -1, "最大消息大小必须在"},
		{"超过1GB", 1024*1024*1024 + 1, "最大消息大小必须在"},
		{"远超限制", 10 * 1024 * 1024 * 1024, "最大消息大小必须在"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &TransportConfig{
				ListenAddr:         "127.0.0.1:8080",
				MaxMessageSize:     tc.maxMessageSize,
				ReadTimeout:        30 * time.Second,
				WriteTimeout:       30 * time.Second,
				KeepAliveInterval:  10 * time.Second,
				KeepAliveTimeout:   30 * time.Second,
				BufferSize:         4096,
				ChannelSendTimeout: 5 * time.Second,
			}

			err := validateTransportConfig(config)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

// TestValidateTransportConfig_NegativeTimeout 测试负超时值
func TestValidateTransportConfig_NegativeTimeout(t *testing.T) {
	timeoutFields := []struct {
		name        string
		fieldSetter func(*TransportConfig, time.Duration)
		expectedErr string
	}{
		{"读超时", func(c *TransportConfig, d time.Duration) { c.ReadTimeout = d }, "读超时不能为负数"},
		{"写超时", func(c *TransportConfig, d time.Duration) { c.WriteTimeout = d }, "写超时不能为负数"},
		{"保活间隔", func(c *TransportConfig, d time.Duration) { c.KeepAliveInterval = d }, "保活间隔不能为负数"},
		{"保活超时", func(c *TransportConfig, d time.Duration) { c.KeepAliveTimeout = d }, "保活超时不能为负数"},
		{"通道发送超时", func(c *TransportConfig, d time.Duration) { c.ChannelSendTimeout = d }, "通道发送超时不能为负数"},
	}

	for _, tc := range timeoutFields {
		t.Run(tc.name, func(t *testing.T) {
			config := &TransportConfig{
				ListenAddr:         "127.0.0.1:8080",
				MaxMessageSize:     1024,
				ReadTimeout:        30 * time.Second,
				WriteTimeout:       30 * time.Second,
				KeepAliveInterval:  10 * time.Second,
				KeepAliveTimeout:   30 * time.Second,
				BufferSize:         4096,
				ChannelSendTimeout: 5 * time.Second,
			}

			tc.fieldSetter(config, -1*time.Second)

			err := validateTransportConfig(config)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

// TestValidateTransportConfig_InvalidBufferSize 测试无效的缓冲区大小
func TestValidateTransportConfig_InvalidBufferSize(t *testing.T) {
	testCases := []struct {
		name        string
		bufferSize  int
		expectedErr string
	}{
		{"零大小", 0, "缓冲区大小必须在"},
		{"负大小", -1, "缓冲区大小必须在"},
		{"超过64KB", 65536 + 1, "缓冲区大小必须在"},
		{"远超限制", 1000000, "缓冲区大小必须在"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &TransportConfig{
				ListenAddr:         "127.0.0.1:8080",
				MaxMessageSize:     1024,
				ReadTimeout:        30 * time.Second,
				WriteTimeout:       30 * time.Second,
				KeepAliveInterval:  10 * time.Second,
				KeepAliveTimeout:   30 * time.Second,
				BufferSize:         tc.bufferSize,
				ChannelSendTimeout: 5 * time.Second,
			}

			err := validateTransportConfig(config)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

// TestValidateTransportConfig_BoundaryValues 测试边界值
func TestValidateTransportConfig_BoundaryValues(t *testing.T) {
	testCases := []struct {
		name   string
		config *TransportConfig
		valid  bool
	}{
		{
			name: "MaxMessageSize 边界值 1",
			config: &TransportConfig{
				ListenAddr:     "127.0.0.1:8080",
				MaxMessageSize: 1,
				BufferSize:     4096,
			},
			valid: true,
		},
		{
			name: "MaxMessageSize 边界值 1GB",
			config: &TransportConfig{
				ListenAddr:     "127.0.0.1:8080",
				MaxMessageSize: 1024 * 1024 * 1024,
				BufferSize:     4096,
			},
			valid: true,
		},
		{
			name: "BufferSize 边界值 1",
			config: &TransportConfig{
				ListenAddr:     "127.0.0.1:8080",
				MaxMessageSize: 1024,
				BufferSize:     1,
			},
			valid: true,
		},
		{
			name: "BufferSize 边界值 64KB",
			config: &TransportConfig{
				ListenAddr:     "127.0.0.1:8080",
				MaxMessageSize: 1024,
				BufferSize:     65536,
			},
			valid: true,
		},
		{
			name: "超时零值（有效）",
			config: &TransportConfig{
				ListenAddr:         "127.0.0.1:8080",
				MaxMessageSize:     1024,
				BufferSize:         4096,
				ReadTimeout:        0,
				WriteTimeout:       0,
				KeepAliveInterval:  0,
				KeepAliveTimeout:   0,
				ChannelSendTimeout: 0,
			},
			valid: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTransportConfig(tc.config)
			if tc.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestCreateBatchForwardResult 测试创建批量转发结果
func TestCreateBatchForwardResult(t *testing.T) {
	addrs := []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	testErr := errors.New("test error")

	result := createBatchForwardResult(addrs, testErr)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 3, result.FailureCount)
	assert.Len(t, result.Results, 3)

	// 验证每个结果
	for i, addr := range addrs {
		assert.Equal(t, addr, result.Results[i].Addr)
		assert.Equal(t, uint64(0), result.Results[i].SeqID)
		assert.Equal(t, testErr, result.Results[i].Error)
	}
}

// TestCreateBatchForwardResult_EmptyAddrs 测试空地址列表
func TestCreateBatchForwardResult_EmptyAddrs(t *testing.T) {
	addrs := []string{}
	testErr := errors.New("test error")

	result := createBatchForwardResult(addrs, testErr)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Len(t, result.Results, 0)
}

// TestCreateBatchForwardResult_SingleAddr 测试单个地址
func TestCreateBatchForwardResult_SingleAddr(t *testing.T) {
	addrs := []string{"127.0.0.1:8080"}
	testErr := errors.New("test error")

	result := createBatchForwardResult(addrs, testErr)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 1, result.FailureCount)
	assert.Len(t, result.Results, 1)
	assert.Equal(t, "127.0.0.1:8080", result.Results[0].Addr)
}

// TestGenerateMsgSeq_DefaultCounter 测试默认计数器
func TestGenerateMsgSeq_DefaultCounter(t *testing.T) {
	var counter atomic.Uint64

	for i := uint64(1); i <= 10; i++ {
		seq := generateMsgSeq(nil, &counter)
		assert.Equal(t, i, seq)
	}
}

// TestGenerateMsgSeq_CustomGenerator 测试自定义生成器
func TestGenerateMsgSeq_CustomGenerator(t *testing.T) {
	var counter atomic.Uint64
	customSeq := uint64(1000)

	generator := func() uint64 {
		customSeq++
		return customSeq
	}

	for i := uint64(1); i <= 10; i++ {
		seq := generateMsgSeq(generator, &counter)
		assert.Equal(t, uint64(1000+i), seq)
	}

	// 验证默认计数器未被使用
	assert.Equal(t, uint64(0), counter.Load())
}

// TestGenerateMsgSeq_InvalidGenerator 测试无效生成器
func TestGenerateMsgSeq_InvalidGenerator(t *testing.T) {
	var counter atomic.Uint64

	// 传入非函数类型
	invalidGenerator := "not a function"

	seq := generateMsgSeq(invalidGenerator, &counter)
	assert.Equal(t, uint64(1), seq)
	assert.Equal(t, uint64(1), counter.Load())
}

// TestGenerateMsgSeq_NilGeneratorFunction 测试 nil 函数
func TestGenerateMsgSeq_NilGeneratorFunction(t *testing.T) {
	var counter atomic.Uint64

	var nilFunc func() uint64

	seq := generateMsgSeq(nilFunc, &counter)
	assert.Equal(t, uint64(1), seq)
	assert.Equal(t, uint64(1), counter.Load())
}

// TestGenerateMsgSeq_Concurrent 测试并发安全性
func TestGenerateMsgSeq_Concurrent(t *testing.T) {
	var counter atomic.Uint64

	done := make(chan bool, 50)
	sequences := make(chan uint64, 50)

	for i := 0; i < 50; i++ {
		go func() {
			seq := generateMsgSeq(nil, &counter)
			sequences <- seq
			done <- true
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 50; i++ {
		<-done
	}
	close(sequences)

	// 验证序列号唯一且连续
	seqMap := make(map[uint64]bool)
	for seq := range sequences {
		if seqMap[seq] {
			t.Errorf("重复的序列号: %d", seq)
		}
		seqMap[seq] = true
	}

	assert.Equal(t, 50, len(seqMap))
	assert.Equal(t, uint64(50), counter.Load())
}

// ========================================
// executeBatchForward 测试辅助类型
// ========================================

// mockBatchForwarder 模拟批量转发器
type mockBatchForwarder struct {
	results map[string]forwardResult
	delay   time.Duration
}

type forwardResult struct {
	seqID uint64
	err   error
}

func (m *mockBatchForwarder) forward(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if result, ok := m.results[addr]; ok {
		return result.seqID, result.err
	}
	return 0, fmt.Errorf("unknown address: %s", addr)
}

// TestExecuteBatchForward_AllSuccess 测试全部成功
func TestExecuteBatchForward_AllSuccess(t *testing.T) {
	addrs := []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		results: map[string]forwardResult{
			"127.0.0.1:8080": {seqID: 1, err: nil},
			"127.0.0.1:8081": {seqID: 2, err: nil},
			"127.0.0.1:8082": {seqID: 3, err: nil},
		},
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	assert.Equal(t, 3, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Len(t, result.Results, 3)

	// 验证结果
	assert.Equal(t, "127.0.0.1:8080", result.Results[0].Addr)
	assert.Equal(t, uint64(1), result.Results[0].SeqID)
	assert.NoError(t, result.Results[0].Error)

	assert.Equal(t, "127.0.0.1:8081", result.Results[1].Addr)
	assert.Equal(t, uint64(2), result.Results[1].SeqID)
	assert.NoError(t, result.Results[1].Error)

	assert.Equal(t, "127.0.0.1:8082", result.Results[2].Addr)
	assert.Equal(t, uint64(3), result.Results[2].SeqID)
	assert.NoError(t, result.Results[2].Error)
}

// TestExecuteBatchForward_PartialFailure 测试部分失败
func TestExecuteBatchForward_PartialFailure(t *testing.T) {
	addrs := []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		results: map[string]forwardResult{
			"127.0.0.1:8080": {seqID: 1, err: nil},
			"127.0.0.1:8081": {seqID: 0, err: errors.New("connection refused")},
			"127.0.0.1:8082": {seqID: 3, err: nil},
		},
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	assert.Equal(t, 2, result.SuccessCount)
	assert.Equal(t, 1, result.FailureCount)
	assert.Len(t, result.Results, 3)

	// 验证失败地址
	assert.Equal(t, "127.0.0.1:8081", result.Results[1].Addr)
	assert.Error(t, result.Results[1].Error)
	assert.Contains(t, result.Results[1].Error.Error(), "connection refused")
}

// TestExecuteBatchForward_AllFailure 测试全部失败
func TestExecuteBatchForward_AllFailure(t *testing.T) {
	addrs := []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		results: map[string]forwardResult{
			"127.0.0.1:8080": {seqID: 0, err: errors.New("timeout")},
			"127.0.0.1:8081": {seqID: 0, err: errors.New("connection refused")},
			"127.0.0.1:8082": {seqID: 0, err: errors.New("network unreachable")},
		},
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 3, result.FailureCount)
	assert.Len(t, result.Results, 3)

	for _, r := range result.Results {
		assert.Error(t, r.Error)
	}
}

// TestExecuteBatchForward_MaxBatchSize 测试超过最大批量大小
func TestExecuteBatchForward_MaxBatchSize(t *testing.T) {
	// 创建超过 maxBatchSize 的地址列表
	addrs := make([]string, maxBatchSize+10)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("127.0.0.1:%d", 8080+i)
	}

	msgExt := MsgFrame{}
	mock := &mockBatchForwarder{
		results: make(map[string]forwardResult),
	}

	// 添加所有地址的成功结果
	for _, addr := range addrs {
		mock.results[addr] = forwardResult{seqID: 1, err: nil}
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	// 应该只处理前 maxBatchSize 个地址
	assert.Equal(t, maxBatchSize, result.SuccessCount)
	assert.Len(t, result.Results, maxBatchSize)
}

// TestExecuteBatchForward_EmptyAddrs 测试空地址列表
func TestExecuteBatchForward_EmptyAddrs(t *testing.T) {
	addrs := []string{}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Len(t, result.Results, 0)
}

// TestExecuteBatchForward_SingleAddr 测试单个地址
func TestExecuteBatchForward_SingleAddr(t *testing.T) {
	addrs := []string{"127.0.0.1:8080"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		results: map[string]forwardResult{
			"127.0.0.1:8080": {seqID: 42, err: nil},
		},
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	assert.Equal(t, 1, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Equal(t, uint64(42), result.Results[0].SeqID)
}

// TestExecuteBatchForward_ConcurrencyLimit 测试并发限制
func TestExecuteBatchForward_ConcurrencyLimit(t *testing.T) {
	// 创建超过 maxBatchConcurrency 的地址列表
	addrs := make([]string, maxBatchConcurrency*2)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("127.0.0.1:%d", 8080+i)
	}

	msgExt := MsgFrame{}
	mock := &mockBatchForwarder{
		delay:   100 * time.Millisecond, // 每个请求需要 100ms
		results: make(map[string]forwardResult),
	}

	// 添加所有地址的成功结果
	for _, addr := range addrs {
		mock.results[addr] = forwardResult{seqID: 1, err: nil}
	}

	start := time.Now()
	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)
	elapsed := time.Since(start)

	// 如果没有并发限制，20个请求串行需要 2000ms
	// 有并发限制（maxBatchConcurrency=10），最多需要 200ms（2批次）
	assert.Less(t, elapsed, 500*time.Millisecond, "并发限制应该加速执行")
	assert.Equal(t, len(addrs), result.SuccessCount)
}

// TestExecuteBatchForward_ContextCancellation 测试上下文取消
func TestExecuteBatchForward_ContextCancellation(t *testing.T) {
	addrs := []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		delay: 1 * time.Second, // 每个请求需要 1s
		results: map[string]forwardResult{
			"127.0.0.1:8080": {seqID: 1, err: nil},
			"127.0.0.1:8081": {seqID: 2, err: nil},
			"127.0.0.1:8082": {seqID: 3, err: nil},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := executeBatchForward(ctx, addrs, msgExt, mock.forward)

	// 由于上下文超时，部分请求可能失败
	assert.True(t, result.SuccessCount+result.FailureCount > 0, "应该有一些请求被处理")
}

// TestExecuteBatchForward_ResultOrder 测试结果顺序保持
func TestExecuteBatchForward_ResultOrder(t *testing.T) {
	addrs := []string{"addr1", "addr2", "addr3", "addr4", "addr5"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		results: map[string]forwardResult{
			"addr1": {seqID: 10, err: nil},
			"addr2": {seqID: 20, err: nil},
			"addr3": {seqID: 30, err: nil},
			"addr4": {seqID: 40, err: nil},
			"addr5": {seqID: 50, err: nil},
		},
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	// 验证结果顺序与输入地址顺序一致
	assert.Equal(t, "addr1", result.Results[0].Addr)
	assert.Equal(t, "addr2", result.Results[1].Addr)
	assert.Equal(t, "addr3", result.Results[2].Addr)
	assert.Equal(t, "addr4", result.Results[3].Addr)
	assert.Equal(t, "addr5", result.Results[4].Addr)

	// 验证 SeqID 顺序
	assert.Equal(t, uint64(10), result.Results[0].SeqID)
	assert.Equal(t, uint64(20), result.Results[1].SeqID)
	assert.Equal(t, uint64(30), result.Results[2].SeqID)
	assert.Equal(t, uint64(40), result.Results[3].SeqID)
	assert.Equal(t, uint64(50), result.Results[4].SeqID)
}

// TestTransportCommon_Integration 测试公共函数集成场景
func TestTransportCommon_Integration(t *testing.T) {
	t.Run("场景1: 配置验证 + 批量转发", func(t *testing.T) {
		// 验证配置
		config := &TransportConfig{
			ListenAddr:         "127.0.0.1:8080",
			MaxMessageSize:     10 * 1024 * 1024, // 10MB
			ReadTimeout:        30 * time.Second,
			WriteTimeout:       30 * time.Second,
			KeepAliveInterval:  10 * time.Second,
			KeepAliveTimeout:   30 * time.Second,
			BufferSize:         8192,
			ChannelSendTimeout: 5 * time.Second,
		}

		err := validateTransportConfig(config)
		require.NoError(t, err)

		// 执行批量转发
		addrs := []string{"127.0.0.1:8081", "127.0.0.1:8082"}
		msgExt := MsgFrame{}

		mock := &mockBatchForwarder{
			results: map[string]forwardResult{
				"127.0.0.1:8081": {seqID: 1, err: nil},
				"127.0.0.1:8082": {seqID: 2, err: nil},
			},
		}

		result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

		assert.Equal(t, 2, result.SuccessCount)
		assert.Equal(t, 0, result.FailureCount)
	})

	t.Run("场景2: 序列号生成 + 批量转发", func(t *testing.T) {
		var counter atomic.Uint64
		currentSeq := uint64(0)

		// 使用自定义序列号生成器
		generator := func() uint64 {
			currentSeq += 100 // 每次增加 100
			return currentSeq
		}

		// 生成序列号
		seq1 := generateMsgSeq(generator, &counter)
		seq2 := generateMsgSeq(generator, &counter)

		assert.Equal(t, uint64(100), seq1)
		assert.Equal(t, uint64(200), seq2)

		// 执行批量转发（验证序列号未被重置）
		addrs := []string{"127.0.0.1:8081"}
		msgExt := MsgFrame{}

		mock := &mockBatchForwarder{
			results: map[string]forwardResult{
				"127.0.0.1:8081": {seqID: seq2, err: nil},
			},
		}

		result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

		assert.Equal(t, 1, result.SuccessCount)
		assert.Equal(t, uint64(200), result.Results[0].SeqID)
		assert.Equal(t, uint64(0), counter.Load()) // 默认计数器未被使用
	})

	t.Run("场景3: 错误处理流程", func(t *testing.T) {
		// 无效配置
		invalidConfig := &TransportConfig{
			ListenAddr: "", // 空地址
		}

		err := validateTransportConfig(invalidConfig)
		assert.Error(t, err)

		// 创建批量转发失败结果
		addrs := []string{"127.0.0.1:8081", "127.0.0.1:8082"}
		result := createBatchForwardResult(addrs, err)

		assert.Equal(t, 0, result.SuccessCount)
		assert.Equal(t, 2, result.FailureCount)

		// 验证错误信息传递
		for _, r := range result.Results {
			assert.Same(t, err, r.Error)
		}
	})
}
