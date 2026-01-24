// Package transport 测试 MsgFrame 和 SendOpt 功能
package transport

import (
	"context"
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 辅助函数：创建测试用的基础 MsgFrame
func createTestFrame(t *testing.T, msgType MessageType, payload []byte) *MsgFrame {
	t.Helper()
	// 使用实际的消息类型而不是 BaseMessage
	var msg Message
	switch msgType {
	case types.MessageTypeGet:
		msg = &GetMessage{BaseMessage: BaseMessage{MessageType: msgType}, Key: string(payload)}
	case types.MessageTypePut:
		msg = &PutMessage{BaseMessage: BaseMessage{MessageType: msgType}, Key: "test", Value: payload}
	default:
		// 对于其他消息类型，使用简单的 GetMessage 作为示例
		msg = &GetMessage{BaseMessage: BaseMessage{MessageType: msgType}, Key: string(payload)}
	}
	return NewMsgFrame(12345, 1, msgType, 1, msg)
}

// 辅助函数：为 frame 添加指定的 TLV 字段
func addTLVToFrame(frame *MsgFrame, tlv TLV) {
	frame.TLVs = append(frame.TLVs, tlv)
}

// ========================================
// MsgFrame 结构体测试
// ========================================

// TestMsgFrame_BasicCreation 测试 MsgFrame 基本创建
func TestMsgFrame_BasicCreation(t *testing.T) {
	msg := &GetMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGet}, Key: "test data"}
	frame := NewMsgFrame(12345, 1, types.MessageTypeGet, 1, msg)

	assert.Equal(t, types.MessageTypeGet, frame.Type())
	assert.Equal(t, int(GetPriority(types.MessageTypeGet)), frame.Priority())
	assert.Equal(t, "test data", msg.Key)
}

// TestMsgFrame_NilMessage 安全处理 nil Message
func TestMsgFrame_NilMessage(t *testing.T) {
	frame := NewMsgFrame(12345, 1, types.MessageTypeGet, 1, nil)

	assert.Equal(t, types.MessageTypeGet, frame.Type()) // 从 FixedHeader 获取
	assert.Equal(t, int(types.PriorityNormal), frame.Priority())
	// 空 TLVs 列表，GetTLV 应该返回 nil（测试一个未使用的类型）
	assert.Nil(t, frame.GetTLV(ExtFieldType(999)))
}

// TestMsgFrame_HopCount 测试 Hops 字段（FixedHeader）
func TestMsgFrame_HopCount(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 设置 Hops 字段（FixedHeader）
	frame.Hops = 5

	assert.True(t, frame.HasHopCount())
	assert.False(t, frame.IsHopExpired())

	// 使用便捷方法获取
	hops, ok := frame.GetHopCount()
	require.True(t, ok)
	assert.Equal(t, uint8(5), hops)

	// 测试 Hops=0 的情况（过期）
	frame.Hops = 0
	assert.True(t, frame.IsHopExpired())
}

// TestMsgFrame_Compression 测试压缩扩展字段
func TestMsgFrame_Compression(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypePut, []byte("test"))

	// 添加 Compress TLV
	addTLVToFrame(frame, *EncodeCompressExt(2)) // Snappy

	assert.True(t, frame.HasCompression())

	compress, ok := frame.GetCompress()
	require.True(t, ok)
	assert.Equal(t, uint16(2), compress.CompressID)
}

// TestMsgFrame_Encryption 测试加密扩展字段
func TestMsgFrame_Encryption(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypePut, []byte("test"))

	// 添加 Encrypt TLV
	nonce := []byte{1, 2, 3, 4}
	encryptTLV, err := EncodeEncryptExt(1, nonce, "1.0")
	require.NoError(t, err)
	addTLVToFrame(frame, *encryptTLV)

	assert.True(t, frame.HasEncryption())

	encrypt, ok := frame.GetEncrypt()
	require.True(t, ok)
	assert.Equal(t, uint16(1), encrypt.EncryptID)
	assert.Equal(t, []byte{1, 2, 3, 4}, encrypt.Nonce)
	assert.Equal(t, "1.0", encrypt.Version)
}

// TestMsgFrame_Segment 测试分片扩展字段
func TestMsgFrame_Segment(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 添加 Segment TLV
	addTLVToFrame(frame, *EncodeFragmentExt(2, 10))

	assert.True(t, frame.HasSegment())

	segment, ok := frame.GetSegment()
	require.True(t, ok)
	assert.Equal(t, uint16(2), segment.Index)
	assert.Equal(t, uint16(10), segment.Total)
}

// TestMsgFrame_Priority 测试优先级扩展字段
func TestMsgFrame_Priority(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 添加 Priority TLV
	addTLVToFrame(frame, *EncodePriorityExt(types.PriorityHigh))

	priority, ok := frame.GetPriority()
	require.True(t, ok)
	assert.Equal(t, types.PriorityHigh, priority.Priority)
}

// TestMsgFrame_GetTLV 测试获取指定类型的 TLV 字段
func TestMsgFrame_GetTLV(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 添加多个 TLV（不包含 ExtHop，因为它已移至 FixedHeader）
	extFields := []TLV{
		{Type: ExtCompress, Value: []byte{0x02, 0x00}},
		{Type: ExtEncrypt, Value: []byte{0x01, 0x00}},
	}
	frame.TLVs = extFields

	compressField := frame.GetTLV(ExtCompress)
	assert.NotNil(t, compressField)
	assert.Equal(t, ExtCompress, compressField.Type)

	// 测试不存在的 ExtField
	segmentField := frame.GetTLV(ExtFragment)
	assert.Nil(t, segmentField)
}

// TestMsgFrame_String 测试 String 方法
func TestMsgFrame_String(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 添加 TLV
	frame.Hops = 5
	addTLVToFrame(frame, *EncodePriorityExt(types.PriorityHigh))

	str := frame.String()
	assert.Contains(t, str, "MsgFrame")
	assert.Contains(t, str, "TLVs=")
}

// TestMsgFrame_GetExt_GenericMethod 测试 GetExt 通用方法（使用 Priority 作为示例）
func TestMsgFrame_GetExt_GenericMethod(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 添加 Priority TLV
	addTLVToFrame(frame, *EncodePriorityExt(types.PriorityHigh))

	// 使用通用 GetExt 方法
	value, ok := frame.GetExt(ExtPriority)
	assert.True(t, ok)
	assert.NotNil(t, value)

	// 类型断言
	priority, ok := value.(*PriorityExt)
	assert.True(t, ok)
	assert.Equal(t, types.PriorityHigh, priority.Priority)
}

// TestMsgFrame_GetExt_NotFound 测试 GetExt 查找不存在的字段
func TestMsgFrame_GetExt_NotFound(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 查找不存在的字段（使用一个未注册的类型）
	value, ok := frame.GetExt(ExtFieldType(999))
	assert.False(t, ok)
	assert.Nil(t, value)
}

// TestMsgFrame_GetExt_UnknownDecoder 测试未知字段类型
func TestMsgFrame_GetExt_UnknownDecoder(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 添加一个未知类型的 TLV（假设类型 999）
	unknownTLV := TLV{
		Type:  ExtFieldType(999),
		Value: []byte{0x01, 0x02, 0x03},
	}
	addTLVToFrame(frame, unknownTLV)

	// 尝试获取未知字段
	value, ok := frame.GetExt(ExtFieldType(999))
	assert.False(t, ok, "未知字段类型应该返回 false")
	assert.Nil(t, value)
}

// TestMsgFrame_DeepCopy 测试深拷贝功能
func TestMsgFrame_DeepCopy(t *testing.T) {
	original := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 添加 TLV 字段
	original.Hops = 5
	addTLVToFrame(original, *EncodeCompressExt(2))

	// 执行深拷贝
	copy := original.DeepCopy()

	// 验证基本字段
	assert.Equal(t, original.NodeID, copy.NodeID)
	assert.Equal(t, original.MsgSeq, copy.MsgSeq)
	assert.Equal(t, original.MsgType, copy.MsgType)
	assert.Equal(t, original.Message, copy.Message)

	// 验证 TLV 深拷贝
	assert.Len(t, copy.TLVs, len(original.TLVs))
	for i := range original.TLVs {
		assert.Equal(t, original.TLVs[i].Type, copy.TLVs[i].Type)
		assert.Equal(t, original.TLVs[i].Value, copy.TLVs[i].Value)
		// 修改副本的 Value 不应该影响原始值
		copy.TLVs[i].Value[0] = 0xFF
		assert.NotEqual(t, original.TLVs[i].Value[0], copy.TLVs[i].Value[0])
	}
}

// TestMsgFrame_EncodeTLVs 测试 EncodeTLVs 方法
func TestMsgFrame_EncodeTLVs(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 添加 TLV 字段
	frame.Hops = 5
	addTLVToFrame(frame, *EncodePriorityExt(types.PriorityHigh))
	addTLVToFrame(frame, *EncodeCompressExt(2))
	addTLVToFrame(frame, *EncodeFragmentExt(1, 5))

	// 编码 TLV
	fields, err := frame.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 3) // Priority, Compress, Segment (Hops is in FixedHeader)

	// 验证 Priority 字段
	priorityField := findExtField(fields, ExtPriority)
	require.NotNil(t, priorityField)
	priority, err := DecodePriorityExt(priorityField)
	require.NoError(t, err)
	assert.Equal(t, types.PriorityHigh, priority)

	// 验证 Compress 字段
	compressField := findExtField(fields, ExtCompress)
	require.NotNil(t, compressField)
	compressID, err := DecodeCompressExt(compressField)
	require.NoError(t, err)
	assert.Equal(t, uint16(2), compressID)

	// 验证 Segment 字段
	segmentField := findExtField(fields, ExtFragment)
	require.NotNil(t, segmentField)
	index, total, err := DecodeFragmentExt(segmentField)
	require.NoError(t, err)
	assert.Equal(t, uint16(1), index)
	assert.Equal(t, uint16(5), total)
}

// TestMsgFrame_EncodeTLVs_PartialFields 测试编码部分 TLV 字段
func TestMsgFrame_EncodeTLVs_PartialFields(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 添加 Priority TLV
	addTLVToFrame(frame, *EncodePriorityExt(types.PriorityHigh))

	// 编码
	fields, err := frame.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 1) // 只有 Priority

	priorityField := findExtField(fields, ExtPriority)
	require.NotNil(t, priorityField)
	priority, err := DecodePriorityExt(priorityField)
	require.NoError(t, err)
	assert.Equal(t, types.PriorityHigh, priority)
}

// TestMsgFrame_EncodeTLVs_NoFields 测试无 TLV 字段
func TestMsgFrame_EncodeTLVs_NoFields(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 编码
	fields, err := frame.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 0) // 无字段
}

// TestMsgFrame_EncodeTLVs_HopsInFixedHeader 测试 Hops 在 FixedHeader 中
func TestMsgFrame_EncodeTLVs_HopsInFixedHeader(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 设置 Hops 字段（FixedHeader）
	frame.Hops = 9

	// 编码 TLV（应该不包含 Hops）
	fields, err := frame.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 0) // Hops 不在 TLV 中

	// Hops 应该在 FixedHeader 中
	hops, _ := frame.GetHopCount()
	assert.Equal(t, uint8(9), hops)
}

// TestMsgFrame_EncodeTLVs_EncryptField 测试加密字段编码
func TestMsgFrame_EncodeTLVs_EncryptField(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 添加 Encrypt TLV
	nonce := []byte{1, 2, 3, 4}
	encryptTLV, err := EncodeEncryptExt(1, nonce, "1.0")
	require.NoError(t, err)
	addTLVToFrame(frame, *encryptTLV)

	// 编码
	fields, err := frame.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 1)

	encryptField := findExtField(fields, ExtEncrypt)
	require.NotNil(t, encryptField)
	encryptID, decodedNonce, version, err := DecodeEncryptExt(encryptField)
	require.NoError(t, err)
	assert.Equal(t, uint16(1), encryptID)
	assert.Equal(t, []byte{1, 2, 3, 4}, decodedNonce)
	assert.Equal(t, "1.0", version)
}

// findExtField 辅助函数：查找指定类型的 TLV 字段
func findExtField(fields []ExtField, fieldType ExtFieldType) *ExtField {
	for _, field := range fields {
		if field.Type == fieldType {
			return &field
		}
	}
	return nil
}

// ========================================
// SendOpt 函数选项模式测试
// ========================================

// TestSendOpt_WithHopCount 测试 WithHopCount 选项
func TestSendOpt_WithHopCount(t *testing.T) {
	opts := processSendOptions(WithHopCount(10))
	defer releaseSendOptions(opts)

	require.NotNil(t, opts)
	require.NotNil(t, opts.hopCount)
	assert.Equal(t, uint16(10), *opts.hopCount)
}

// TestSendOpt_WithCompression 测试 WithCompression 选项
func TestSendOpt_WithCompression(t *testing.T) {
	opts := processSendOptions(WithCompression(2))
	defer releaseSendOptions(opts)

	require.NotNil(t, opts)
	require.NotNil(t, opts.compressID)
	assert.Equal(t, uint16(2), *opts.compressID)
}

// TestSendOpt_WithEncryption 测试 WithEncryption 选项
func TestSendOpt_WithEncryption(t *testing.T) {
	opts := processSendOptions(WithEncryption(1))
	defer releaseSendOptions(opts)

	require.NotNil(t, opts)
	require.NotNil(t, opts.encryptID)
	assert.Equal(t, uint16(1), *opts.encryptID)
}

// TestSendOpt_WithPriority 测试 WithPriority 选项
func TestSendOpt_WithPriority(t *testing.T) {
	opts := processSendOptions(WithPriority(types.PriorityHigh))
	defer releaseSendOptions(opts)

	require.NotNil(t, opts)
	require.NotNil(t, opts.priority)
	assert.Equal(t, types.PriorityHigh, *opts.priority)
}

// TestSendopt_MultipleOptions 测试多个选项组合
func TestSendopt_MultipleOptions(t *testing.T) {
	opts := processSendOptions(
		WithHopCount(10),
		WithCompression(2),
		WithPriority(types.PriorityLow),
	)
	defer releaseSendOptions(opts)

	require.NotNil(t, opts)
	require.NotNil(t, opts.hopCount)
	require.NotNil(t, opts.compressID)
	require.NotNil(t, opts.priority)
	assert.Nil(t, opts.encryptID)

	assert.Equal(t, uint16(10), *opts.hopCount)
	assert.Equal(t, uint16(2), *opts.compressID)
	assert.Equal(t, types.PriorityLow, *opts.priority)
}

// TestSendOpt_NoOptions 测试无选项的情况
func TestSendOpt_NoOptions(t *testing.T) {
	opts := processSendOptions()
	defer releaseSendOptions(opts)

	require.NotNil(t, opts)
	assert.Nil(t, opts.hopCount)
	assert.Nil(t, opts.compressID)
	assert.Nil(t, opts.encryptID)
	assert.Nil(t, opts.priority)
}

// TestSendOpt_LastOptionWins 测试最后选项覆盖前面
func TestSendOpt_LastOptionWins(t *testing.T) {
	opts := processSendOptions(
		WithHopCount(5),
		WithHopCount(10),
	)
	defer releaseSendOptions(opts)

	require.NotNil(t, opts)
	require.NotNil(t, opts.hopCount)
	assert.Equal(t, uint16(10), *opts.hopCount, "后面的选项应该覆盖前面的")
}

// TestSendOpt_withSendOptions 测试 withSendOptions 包装函数
func TestSendOpt_withSendOptions(t *testing.T) {
	err := withSendOptions([]SendOpt{WithHopCount(10)}, func(opts *sendOptions) error {
		require.NotNil(t, opts)
		require.NotNil(t, opts.hopCount)
		assert.Equal(t, uint16(10), *opts.hopCount)
		return nil
	})

	assert.NoError(t, err)
}

// TestSendOpt_withSendOptions_MultipleOptions 测试 withSendOptions 多选项
func TestSendOpt_withSendOptions_MultipleOptions(t *testing.T) {
	err := withSendOptions([]SendOpt{
		WithHopCount(10),
		WithCompression(2),
		WithPriority(types.PriorityHigh),
	}, func(opts *sendOptions) error {
		require.NotNil(t, opts)
		require.NotNil(t, opts.hopCount)
		require.NotNil(t, opts.compressID)
		require.NotNil(t, opts.priority)
		assert.Nil(t, opts.encryptID)

		assert.Equal(t, uint16(10), *opts.hopCount)
		assert.Equal(t, uint16(2), *opts.compressID)
		assert.Equal(t, types.PriorityHigh, *opts.priority)
		return nil
	})

	assert.NoError(t, err)
}

// ========================================
// ExtField 结构体测试
// ========================================

// TestExtField_Creation 测试 ExtField 创建
func TestExtField_Creation(t *testing.T) {
	extField := ExtField{
		Type:  ExtPriority,
		Value: []byte{0x03, 0x00}, // types.PriorityHigh = 3
	}

	assert.Equal(t, ExtPriority, extField.Type)
	assert.Equal(t, []byte{0x03, 0x00}, extField.Value)
}

// ========================================
// 扩展字段结构体测试
// ========================================

// TestPriorityExt_Structure 测试 PriorityExt 结构
func TestPriorityExt_Structure(t *testing.T) {
	priority := &PriorityExt{
		Priority: types.PriorityHigh,
	}

	assert.Equal(t, types.PriorityHigh, priority.Priority)
}

// TestCompressExt_Structure 测试 CompressExt 结构
func TestCompressExt_Structure(t *testing.T) {
	compress := &CompressExt{
		CompressID: 2,
	}

	assert.Equal(t, uint16(2), compress.CompressID)
}

// TestEncryptExt_Structure 测试 EncryptExt 结构
func TestEncryptExt_Structure(t *testing.T) {
	encrypt := &EncryptExt{
		EncryptID: 1,
		Nonce:     []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Version:   "AES-256-GCM",
	}

	assert.Equal(t, uint16(1), encrypt.EncryptID)
	assert.Equal(t, 8, len(encrypt.Nonce))
	assert.Equal(t, "AES-256-GCM", encrypt.Version)
}

// TestSegmentExt_Structure 测试 SegmentExt 结构
func TestSegmentExt_Structure(t *testing.T) {
	segment := &SegmentExt{
		Index: 5,
		Total: 10,
	}

	assert.Equal(t, uint16(5), segment.Index)
	assert.Equal(t, uint16(10), segment.Total)
	assert.True(t, segment.Index < segment.Total)
}

// ========================================
// prepareForwardMessage 测试
// ========================================

// TestPrepareForwardMessage_Success 测试准备转发消息成功
func TestPrepareForwardMessage_Success(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 设置 Hops 字段（FixedHeader）
	frame.Hops = 10

	// 准备转发
	forwardFrame, err := prepareForwardMessage(frame)
	require.NoError(t, err)
	require.NotNil(t, forwardFrame)

	// 验证 Hops 递减
	hops, ok := forwardFrame.GetHopCount()
	require.True(t, ok)
	assert.Equal(t, uint8(9), hops, "Hops 应该递减")

	// 验证原始 frame 不受影响（深拷贝）
	originalHops, ok := frame.GetHopCount()
	require.True(t, ok)
	assert.Equal(t, uint8(10), originalHops, "原始 Hops 不应该被修改")
}

// TestPrepareForwardMessage_HopExpired 测试 Hops 过期
func TestPrepareForwardMessage_HopExpired(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 设置 Hops=0（过期）
	frame.Hops = 0

	// 准备转发
	forwardFrame, err := prepareForwardMessage(frame)
	require.Error(t, err)
	assert.Nil(t, forwardFrame)
	assert.Equal(t, types.ErrTransportHopCountExpired, err.(*types.Error).Code)
}

// TestPrepareForwardMessage_NilMessage 测试 nil Message
func TestPrepareForwardMessage_NilMessage(t *testing.T) {
	frame := NewMsgFrame(12345, 1, types.MessageTypeGet, 1, nil)

	// 准备转发
	forwardFrame, err := prepareForwardMessage(frame)
	require.Error(t, err)
	assert.Nil(t, forwardFrame)
	assert.Contains(t, err.Error(), "消息为空")
}

// ========================================
// ForwardMessage 集成测试
// ========================================

// TestForwardMessage_ContextCancel 测试 context 取消场景（P1-4）
func TestForwardMessage_ContextCancel(t *testing.T) {
	// 创建已取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))
	frame.Hops = 5

	// TCP Transport
	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	nodeID := uint64(12345)
	require.NoError(t, tcpTransport.Start(&nodeID, nil))
	defer func() { _ = tcpTransport.Stop() }()

	// 应该返回 context 取消错误
	_, err = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")

	// UDP Transport
	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpNodeID := uint64(12345)
	require.NoError(t, udpTransport.Start(&udpNodeID, nil))
	defer func() { _ = udpTransport.Stop() }()

	// 应该返回 context 取消错误
	_, err = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

// TestForwardMessage_NilMessage 测试 nil Message 检查（P1-2）
func TestForwardMessage_NilMessage(t *testing.T) {
	ctx := context.Background()

	frame := NewMsgFrame(12345, 1, types.MessageTypeGet, 1, nil)
	frame.Hops = 5

	// TCP Transport
	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	nodeID := uint64(12345)
	require.NoError(t, tcpTransport.Start(&nodeID, nil))
	defer func() { _ = tcpTransport.Stop() }()

	// 应该返回消息为空错误
	_, err = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "消息为空")

	// UDP Transport
	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpNodeID := uint64(12345)
	require.NoError(t, udpTransport.Start(&udpNodeID, nil))
	defer func() { _ = udpTransport.Stop() }()

	// 应该返回消息为空错误
	_, err = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "消息为空")
}

// TestForwardMessage_HopCountExpired 测试 Hop Count 过期
func TestForwardMessage_HopCountExpired(t *testing.T) {
	ctx := context.Background()

	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))
	// 设置 Hops=0 来触发过期错误
	frame.Hops = 0

	// TCP Transport
	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	nodeID := uint64(12345)
	require.NoError(t, tcpTransport.Start(&nodeID, nil))
	defer func() { _ = tcpTransport.Stop() }()

	// 应该返回 Hop Count 过期错误
	_, err = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Equal(t, types.ErrTransportHopCountExpired, err.(*types.Error).Code)

	// UDP Transport
	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpNodeID := uint64(12345)
	require.NoError(t, udpTransport.Start(&udpNodeID, nil))
	defer func() { _ = udpTransport.Stop() }()

	// 应该返回 Hop Count 过期错误
	_, err = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Equal(t, types.ErrTransportHopCountExpired, err.(*types.Error).Code)
}

// TestForwardMessage_DeepCopyPreventsDataRace 测试深拷贝防止 data race（P1-1）
func TestForwardMessage_DeepCopyPreventsDataRace(t *testing.T) {
	ctx := context.Background()

	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))
	frame.Hops = 5

	// 创建多个并发转发请求，验证没有 data race
	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	nodeID := uint64(12345)
	require.NoError(t, tcpTransport.Start(&nodeID, nil))
	defer func() { _ = tcpTransport.Stop() }()

	// 使用 t.Run 并发执行
	t.Run("并发转发测试", func(t *testing.T) {
		done := make(chan bool, 10)
		for i := 0; i < 10; i++ {
			go func() {
				// 每个协程独立的 frame 拷贝
				localFrame := frame.DeepCopy()
				_, _ = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *localFrame)
				done <- true
			}()
		}

		// 等待所有协程完成
		for i := 0; i < 10; i++ {
			<-done
		}
	})
}

// ========================================
// Decoder 注册机制测试
// ========================================

// TestRegisterDecoder 测试解码器注册
func TestRegisterDecoder(t *testing.T) {
	// 创建一个自定义解码器
	customDecoder := func(tlv TLV) (interface{}, error) {
		return "custom_value", nil
	}

	// 注册自定义解码器
	customType := ExtFieldType(999)
	RegisterDecoder(customType, customDecoder)

	// 验证解码器已注册
	decoder, ok := getDecoder(customType)
	assert.True(t, ok, "解码器应该被注册")
	assert.NotNil(t, decoder, "解码器不应该为 nil")
}

// TestGetExt_MissingDecoder 测试缺少解码器的情况
func TestGetExt_MissingDecoder(t *testing.T) {
	frame := createTestFrame(t, types.MessageTypeGet, []byte("test"))

	// 添加一个没有解码器的 TLV 类型
	unknownTLV := TLV{
		Type:  ExtFieldType(9999),
		Value: []byte{0x01, 0x02},
	}
	addTLVToFrame(frame, unknownTLV)

	// 尝试获取未知字段
	value, ok := frame.GetExt(ExtFieldType(9999))
	assert.False(t, ok, "缺少解码器应该返回 false")
	assert.Nil(t, value)
}

// ========================================
// ExpectResponse 和 Reliability 测试
// ========================================

// TestMsgFrame_ExpectResponse_WithMessage 测试有 Message 时的 ExpectResponse
func TestMsgFrame_ExpectResponse_WithMessage(t *testing.T) {
	// 创建一个实现了 ExpectResponse 的测试消息
	testMsg := &testMessageWithExpectResponse{
		expectResponse: types.ExpectResponse,
	}
	frame := NewMsgFrame(12345, 1, types.MessageTypeGet, 1, testMsg)

	assert.Equal(t, types.ExpectResponse, frame.ExpectResponse())
}

// TestMsgFrame_ExpectResponse_WithoutMessage 测试无 Message 时的 ExpectResponse
func TestMsgFrame_ExpectResponse_WithoutMessage(t *testing.T) {
	// 无 Message，从 MsgType 获取
	frame := NewMsgFrame(12345, 1, types.MessageTypeGet, 1, nil)

	// MessageTypeGet 需要 Response
	assert.Equal(t, types.ExpectResponse, frame.ExpectResponse())
}

// TestMsgFrame_ExpectResponse_NoResponse 测试不需要响应的消息类型
func TestMsgFrame_ExpectResponse_NoResponse(t *testing.T) {
	// 根据 MessageType 的实际行为，某些消息类型不需要响应
	// 注意：MessageTypeGossipSync 实际上需要响应（根据 types 包的定义）
	// 这里测试真正不需要响应的消息类型（如果有的话）
	// 例如：如果类型系统中定义了 NoResponse 的消息类型

	// 由于大多数消息类型都需要响应，这里测试 MsgType 的 ExpectResponse() 方法确实被调用
	frame := NewMsgFrame(12345, 1, types.MessageTypeGossipSync, 1, nil)

	// MessageTypeGossipSync 返回 ExpectResponse（需要响应）
	// 这是根据 types.MessageType.ExpectResponse() 的实际实现
	assert.Equal(t, types.ExpectResponse, frame.ExpectResponse())
}

// TestMsgFrame_Reliability_WithMessage 测试有 Message 时的 Reliability
func TestMsgFrame_Reliability_WithMessage(t *testing.T) {
	testMsg := &testMessageWithReliability{
		reliability: types.Reliable,
	}
	frame := NewMsgFrame(12345, 1, types.MessageTypeGet, 1, testMsg)

	assert.Equal(t, types.Reliable, frame.Reliability())
}

// TestMsgFrame_Reliability_WithoutMessage 测试无 Message 时的 Reliability
func TestMsgFrame_Reliability_WithoutMessage(t *testing.T) {
	// 无 Message，从 MsgType 获取
	frame := NewMsgFrame(12345, 1, types.MessageTypeGet, 1, nil)

	// MessageTypeGet 是 Reliable
	assert.Equal(t, types.Reliable, frame.Reliability())
}

// TestMsgFrame_Reliability_BestEffort 测试 BestEffort 消息类型
func TestMsgFrame_Reliability_BestEffort(t *testing.T) {
	// GossipSync 是 BestEffort
	frame := NewMsgFrame(12345, 1, types.MessageTypeGossipSync, 1, nil)

	assert.Equal(t, types.BestEffort, frame.Reliability())
}

// ========================================
// 测试辅助类型
// ========================================

// testMessageWithExpectResponse 实现了 ExpectResponse 的测试消息
type testMessageWithExpectResponse struct {
	expectResponse types.ResponseExpectation
}

func (m *testMessageWithExpectResponse) Type() types.MessageType {
	return types.MessageTypeGet
}

func (m *testMessageWithExpectResponse) Priority() int {
	return int(types.PriorityNormal)
}

func (m *testMessageWithExpectResponse) ExpectResponse() types.ResponseExpectation {
	return m.expectResponse
}

func (m *testMessageWithExpectResponse) Reliability() types.ReliabilityRequirement {
	return types.Reliable
}

func (m *testMessageWithExpectResponse) GetPayload() []byte {
	return []byte("test")
}

// testMessageWithReliability 实现了 Reliability 的测试消息
type testMessageWithReliability struct {
	reliability types.ReliabilityRequirement
}

func (m *testMessageWithReliability) Type() types.MessageType {
	return types.MessageTypePut
}

func (m *testMessageWithReliability) Priority() int {
	return int(types.PriorityHigh)
}

func (m *testMessageWithReliability) ExpectResponse() types.ResponseExpectation {
	return types.ExpectResponse
}

func (m *testMessageWithReliability) Reliability() types.ReliabilityRequirement {
	return m.reliability
}

func (m *testMessageWithReliability) GetPayload() []byte {
	return []byte("test data")
}
