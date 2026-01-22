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
	baseMsg := NewBaseMessage(msgType, payload)
	return NewMsgFrame(12345, 1, msgType, 1, baseMsg)
}

// 辅助函数：为 frame 添加指定的 TLV 字段
func addTLVToFrame(frame *MsgFrame, tlv TLV) {
	frame.TLVs = append(frame.TLVs, tlv)
}

// 辅助函数：修改 frame 中指定类型的 TLV 字段
func updateTLVInFrame(frame *MsgFrame, fieldType ExtFieldType, newTLV TLV) {
	for i := range frame.TLVs {
		if frame.TLVs[i].Type == fieldType {
			frame.TLVs[i] = newTLV
			break
		}
	}
}

// ========================================
// MsgFrame 结构体测试
// ========================================

// TestMsgFrame_BasicCreation 测试 MsgFrame 基本创建
func TestMsgFrame_BasicCreation(t *testing.T) {
	baseMsg := NewBaseMessage(MessageTypeGet, []byte("test data"))
	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, baseMsg)

	assert.Equal(t, MessageTypeGet, frame.Type())
	assert.Equal(t, GetPriority(MessageTypeGet), frame.Priority())
	assert.Equal(t, []byte("test data"), baseMsg.GetPayload())
}

// TestMsgFrame_NilMessage 安全处理 nil Message
func TestMsgFrame_NilMessage(t *testing.T) {
	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, nil)

	assert.Equal(t, MessageTypeGet, frame.Type()) // 从 FixedHeader 获取
	assert.Equal(t, PriorityNormal, frame.Priority())
	// 空 TLVs 列表，GetTLV 应该返回 nil
	assert.Nil(t, frame.GetTLV(ExtHop))
}

// TestMsgFrame_HopCount 测试 Hop Count 扩展字段
func TestMsgFrame_HopCount(t *testing.T) {
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 添加 Hop Count TLV
	addTLVToFrame(frame, *EncodeHopExt(5, 10))

	assert.True(t, frame.HasHopCount())
	assert.False(t, frame.IsHopExpired())

	// 使用便捷方法获取
	hop, ok := frame.GetHopCount()
	require.True(t, ok)
	assert.Equal(t, uint16(5), hop.Hop)
	assert.Equal(t, uint16(10), hop.TotalHop)

	// 测试 Hop=0 的情况（过期）
	updateTLVInFrame(frame, ExtHop, *EncodeHopExt(0, 10))
	assert.True(t, frame.IsHopExpired())
}

// TestMsgFrame_Compression 测试压缩扩展字段
func TestMsgFrame_Compression(t *testing.T) {
	frame := createTestFrame(t, MessageTypePut, []byte("test"))

	// 添加 Compress TLV
	addTLVToFrame(frame, *EncodeCompressExt(2)) // Snappy

	assert.True(t, frame.HasCompression())

	compress, ok := frame.GetCompress()
	require.True(t, ok)
	assert.Equal(t, uint16(2), compress.CompressID)
}

// TestMsgFrame_Encryption 测试加密扩展字段
func TestMsgFrame_Encryption(t *testing.T) {
	frame := createTestFrame(t, MessageTypePut, []byte("test"))

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
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

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
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 添加 Priority TLV
	addTLVToFrame(frame, *EncodePriorityExt(types.PriorityHigh))

	priority, ok := frame.GetPriority()
	require.True(t, ok)
	assert.Equal(t, types.PriorityHigh, priority.Priority)
}

// TestMsgFrame_GetTLV 测试获取指定类型的 TLV 字段
func TestMsgFrame_GetTLV(t *testing.T) {
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 添加多个 TLV
	extFields := []TLV{
		{Type: ExtHop, Value: []byte{0x05, 0x00, 0x0A, 0x00}},
		{Type: ExtCompress, Value: []byte{0x02, 0x00}},
		{Type: ExtEncrypt, Value: []byte{0x01, 0x00}},
	}
	frame.TLVs = extFields

	hopField := frame.GetTLV(ExtHop)
	assert.NotNil(t, hopField)
	assert.Equal(t, ExtHop, hopField.Type)

	compressField := frame.GetTLV(ExtCompress)
	assert.NotNil(t, compressField)
	assert.Equal(t, ExtCompress, compressField.Type)

	// 测试不存在的 ExtField
	segmentField := frame.GetTLV(ExtFragment)
	assert.Nil(t, segmentField)
}

// TestMsgFrame_String 测试 String 方法
func TestMsgFrame_String(t *testing.T) {
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 添加 TLV
	addTLVToFrame(frame, *EncodeHopExt(5, 10))
	addTLVToFrame(frame, *EncodePriorityExt(types.PriorityHigh))

	str := frame.String()
	assert.Contains(t, str, "MsgFrame")
	assert.Contains(t, str, "TLVs=")
}

// TestMsgFrame_GetExt_GenericMethod 测试 GetExt 通用方法
func TestMsgFrame_GetExt_GenericMethod(t *testing.T) {
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 添加 Hop TLV
	addTLVToFrame(frame, *EncodeHopExt(5, 10))

	// 使用通用 GetExt 方法
	value, ok := frame.GetExt(ExtHop)
	assert.True(t, ok)
	assert.NotNil(t, value)

	// 类型断言
	hop, ok := value.(*HopExt)
	assert.True(t, ok)
	assert.Equal(t, uint16(5), hop.Hop)
	assert.Equal(t, uint16(10), hop.TotalHop)

	// 测试：每次调用都会重新解码，返回新对象
	value2, ok2 := frame.GetExt(ExtHop)
	assert.True(t, ok2)
	hop2, _ := value2.(*HopExt)
	assert.Equal(t, hop.Hop, hop2.Hop)
	assert.Equal(t, hop.TotalHop, hop2.TotalHop)
	// 注意：由于移除了缓存，指针会不同
}

// TestMsgFrame_GetExt_NotFound 测试 GetExt 查找不存在的字段
func TestMsgFrame_GetExt_NotFound(t *testing.T) {
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 查找不存在的字段
	value, ok := frame.GetExt(ExtHop)
	assert.False(t, ok)
	assert.Nil(t, value)
}

// TestMsgFrame_GetExt_UnknownDecoder 测试未知字段类型
func TestMsgFrame_GetExt_UnknownDecoder(t *testing.T) {
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

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
	original := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 添加多个 TLV
	addTLVToFrame(original, *EncodeHopExt(5, 10))
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
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 添加 TLV 字段
	addTLVToFrame(frame, *EncodeHopExt(5, 10))
	addTLVToFrame(frame, *EncodePriorityExt(types.PriorityHigh))
	addTLVToFrame(frame, *EncodeCompressExt(2))
	addTLVToFrame(frame, *EncodeFragmentExt(1, 5))

	// 编码 TLV
	fields, err := frame.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 4) // Hop, Priority, Compress, Segment

	// 验证 Hop Count 字段
	hopField := findExtField(fields, ExtHop)
	require.NotNil(t, hopField)
	hop, totalHop, err := DecodeHopExt(hopField)
	require.NoError(t, err)
	assert.Equal(t, uint16(5), hop)
	assert.Equal(t, uint16(10), totalHop)

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
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 只添加 Hop TLV
	addTLVToFrame(frame, *EncodeHopExt(3, 10))

	// 编码
	fields, err := frame.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 1) // 只有 Hop Count

	hopField := findExtField(fields, ExtHop)
	require.NotNil(t, hopField)
	hop, totalHop, err := DecodeHopExt(hopField)
	require.NoError(t, err)
	assert.Equal(t, uint16(3), hop)
	assert.Equal(t, uint16(10), totalHop)
}

// TestMsgFrame_EncodeTLVs_NoFields 测试无 TLV 字段
func TestMsgFrame_EncodeTLVs_NoFields(t *testing.T) {
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 编码
	fields, err := frame.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 0) // 无字段
}

// TestMsgFrame_EncodeTLVs_HopDecrement 测试 Hop Count 递减后的编码
func TestMsgFrame_EncodeTLVs_HopDecrement(t *testing.T) {
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 添加 Hop TLV
	addTLVToFrame(frame, *EncodeHopExt(10, 10))

	// 修改 TLV 中的 Hop 值为 9
	updateTLVInFrame(frame, ExtHop, *EncodeHopExt(9, 10))

	// 编码
	fields, err := frame.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 1)

	hopField := findExtField(fields, ExtHop)
	require.NotNil(t, hopField)
	decodedHop, totalHop, err := DecodeHopExt(hopField)
	require.NoError(t, err)
	assert.Equal(t, uint16(9), decodedHop, "Hop 应该被递减")
	assert.Equal(t, uint16(10), totalHop, "TotalHop 应该保持不变")
}

// TestMsgFrame_EncodeTLVs_EncryptField 测试加密字段编码
func TestMsgFrame_EncodeTLVs_EncryptField(t *testing.T) {
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

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
// BaseMessage 测试
// ========================================

// TestBaseMessage_Creation 测试 BaseMessage 创建
func TestBaseMessage_Creation(t *testing.T) {
	msg := NewBaseMessage(MessageTypePut, []byte("test payload"))

	assert.Equal(t, MessageTypePut, msg.Type())
	assert.Equal(t, []byte("test payload"), msg.GetPayload())
	assert.Equal(t, GetPriority(MessageTypePut), msg.Priority())
}

// TestBaseMessage_SetPriority 测试设置优先级
func TestBaseMessage_SetPriority(t *testing.T) {
	msg := NewBaseMessage(MessageTypeGet, []byte("test"))

	originalPriority := msg.Priority()
	msg.SetPriority(PriorityHigh)
	assert.Equal(t, PriorityHigh, msg.Priority())

	// 恢复原始优先级
	msg.SetPriority(originalPriority)
	assert.Equal(t, originalPriority, msg.Priority())
}

// ========================================
// ExtField 结构体测试
// ========================================

// TestExtField_Creation 测试 ExtField 创建
func TestExtField_Creation(t *testing.T) {
	extField := ExtField{
		Type:  ExtHop,
		Value: []byte{0x05, 0x00, 0x0A, 0x00},
	}

	assert.Equal(t, ExtHop, extField.Type)
	assert.Equal(t, []byte{0x05, 0x00, 0x0A, 0x00}, extField.Value)
}

// ========================================
// 扩展字段结构体测试
// ========================================

// TestHopExt_Structure 测试 HopExt 结构
func TestHopExt_Structure(t *testing.T) {
	hop := &HopExt{
		Hop:      3,
		TotalHop: 10,
	}

	assert.Equal(t, uint16(3), hop.Hop)
	assert.Equal(t, uint16(10), hop.TotalHop)
	assert.True(t, hop.Hop > 0)
	assert.True(t, hop.Hop <= hop.TotalHop)
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
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 添加 Hop TLV
	addTLVToFrame(frame, *EncodeHopExt(10, 20))

	// 准备转发
	forwardFrame, err := prepareForwardMessage(frame)
	require.NoError(t, err)
	require.NotNil(t, forwardFrame)

	// 验证 Hop Count 递减
	hop, ok := forwardFrame.GetHopCount()
	require.True(t, ok)
	assert.Equal(t, uint16(9), hop.Hop, "Hop 应该递减")
	assert.Equal(t, uint16(20), hop.TotalHop, "TotalHop 不变")

	// 验证原始 frame 不受影响（深拷贝）
	originalHop, ok := frame.GetHopCount()
	require.True(t, ok)
	assert.Equal(t, uint16(10), originalHop.Hop, "原始 Hop 不应该被修改")
}

// TestPrepareForwardMessage_HopExpired 测试 Hop Count 过期
func TestPrepareForwardMessage_HopExpired(t *testing.T) {
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

	// 添加 Hop=0 的 TLV
	addTLVToFrame(frame, *EncodeHopExt(0, 10))

	// 准备转发
	forwardFrame, err := prepareForwardMessage(frame)
	require.Error(t, err)
	assert.Nil(t, forwardFrame)
	assert.Equal(t, types.ErrTransportHopCountExpired, err.(*types.Error).Code)
}

// TestPrepareForwardMessage_NilMessage 测试 nil Message
func TestPrepareForwardMessage_NilMessage(t *testing.T) {
	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, nil)

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

	frame := createTestFrame(t, MessageTypeGet, []byte("test"))
	addTLVToFrame(frame, *EncodeHopExt(5, 10))

	// TCP Transport
	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	// 应该返回 context 取消错误
	_, err = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")

	// UDP Transport
	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	// 应该返回 context 取消错误
	_, err = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

// TestForwardMessage_NilMessage 测试 nil Message 检查（P1-2）
func TestForwardMessage_NilMessage(t *testing.T) {
	ctx := context.Background()

	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, nil)
	addTLVToFrame(frame, *EncodeHopExt(5, 10))

	// TCP Transport
	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	// 应该返回消息为空错误
	_, err = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "消息为空")

	// UDP Transport
	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	// 应该返回消息为空错误
	_, err = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "消息为空")
}

// TestForwardMessage_HopCountExpired 测试 Hop Count 过期
func TestForwardMessage_HopCountExpired(t *testing.T) {
	ctx := context.Background()

	frame := createTestFrame(t, MessageTypeGet, []byte("test"))
	addTLVToFrame(frame, *EncodeHopExt(0, 10))

	// TCP Transport
	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	// 应该返回 Hop Count 过期错误
	_, err = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Equal(t, types.ErrTransportHopCountExpired, err.(*types.Error).Code)

	// UDP Transport
	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	// 应该返回 Hop Count 过期错误
	_, err = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	assert.Error(t, err)
	assert.Equal(t, types.ErrTransportHopCountExpired, err.(*types.Error).Code)
}

// TestForwardMessage_DeepCopyPreventsDataRace 测试深拷贝防止 data race（P1-1）
func TestForwardMessage_DeepCopyPreventsDataRace(t *testing.T) {
	ctx := context.Background()

	frame := createTestFrame(t, MessageTypeGet, []byte("test"))
	addTLVToFrame(frame, *EncodeHopExt(5, 10))

	// 创建多个并发转发请求，验证没有 data race
	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
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
	frame := createTestFrame(t, MessageTypeGet, []byte("test"))

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
