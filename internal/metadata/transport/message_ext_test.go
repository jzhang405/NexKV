// Package transport 测试 MsgExt 和 SendOpt 功能
package transport

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// MsgExt 结构体测试
// ========================================

// TestMsgExt_BasicCreation 测试 MsgExt 基本创建
func TestMsgExt_BasicCreation(t *testing.T) {
	baseMsg := NewBaseMessage(MessageTypeGet, []byte("test data"))

	msgExt := MsgExt{
		Message:     baseMsg,
		TLVs:        make([]ExtField, 0),
		HopCount:    nil,
		Compress:    nil,
		Encrypt:     nil,
		Segment:     nil,
		PriorityExt: nil,
	}

	assert.Equal(t, MessageTypeGet, msgExt.GetType())
	assert.Equal(t, GetPriority(MessageTypeGet), msgExt.Priority())
	// 通过嵌入的 Message 访问 payload
	assert.Equal(t, []byte("test data"), baseMsg.GetPayload())
}

// TestMsgExt_NilMessage 安全处理 nil Message
func TestMsgExt_NilMessage(t *testing.T) {
	msgExt := MsgExt{
		Message: nil,
		TLVs:    make([]ExtField, 0),
	}

	assert.Equal(t, MessageType(0), msgExt.GetType())
	assert.Equal(t, PriorityNormal, msgExt.Priority())
	// 空 TLVs 列表，GetTLV 应该返回 nil
	assert.Nil(t, msgExt.GetTLV(ExtHop))
}

// TestMsgExt_HopCount 测试 Hop Count 扩展字段
func TestMsgExt_HopCount(t *testing.T) {
	hopExt := &HopExt{
		Hop:      5,
		TotalHop: 10,
	}

	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		TLVs:     make([]ExtField, 0),
		HopCount: hopExt,
	}

	assert.True(t, msgExt.HasHopCount())
	assert.False(t, msgExt.IsHopExpired())
	assert.Equal(t, uint16(5), msgExt.HopCount.Hop)
	assert.Equal(t, uint16(10), msgExt.HopCount.TotalHop)

	// 测试 Hop=0 的情况（过期）
	msgExt.HopCount.Hop = 0
	assert.True(t, msgExt.IsHopExpired())
}

// TestMsgExt_Compression 测试压缩扩展字段
func TestMsgExt_Compression(t *testing.T) {
	compressExt := &CompressExt{
		CompressID: 2, // Snappy
	}

	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypePut, []byte("test")),
		TLVs:     make([]ExtField, 0),
		Compress: compressExt,
	}

	assert.True(t, msgExt.HasCompression())
	assert.Equal(t, uint16(2), msgExt.Compress.CompressID)
}

// TestMsgExt_Encryption 测试加密扩展字段
func TestMsgExt_Encryption(t *testing.T) {
	encryptExt := &EncryptExt{
		EncryptID: 1,
		Nonce:     []byte{1, 2, 3, 4},
		Version:   "1.0",
	}

	msgExt := MsgExt{
		Message: NewBaseMessage(MessageTypePut, []byte("test")),
		TLVs:    make([]ExtField, 0),
		Encrypt: encryptExt,
	}

	assert.True(t, msgExt.HasEncryption())
	assert.Equal(t, uint16(1), msgExt.Encrypt.EncryptID)
	assert.Equal(t, []byte{1, 2, 3, 4}, msgExt.Encrypt.Nonce)
	assert.Equal(t, "1.0", msgExt.Encrypt.Version)
}

// TestMsgExt_Segment 测试分片扩展字段
func TestMsgExt_Segment(t *testing.T) {
	segmentExt := &SegmentExt{
		Index: 2,
		Total: 10,
	}

	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		TLVs:     make([]ExtField, 0),
		Segment:  segmentExt,
		HopCount: nil,
	}

	assert.True(t, msgExt.HasSegment())
	assert.Equal(t, uint16(2), msgExt.Segment.Index)
	assert.Equal(t, uint16(10), msgExt.Segment.Total)
}

// TestMsgExt_Priority 测试优先级扩展字段
func TestMsgExt_Priority(t *testing.T) {
	priorityExt := &PriorityExt{
		Priority: types.PriorityHigh,
	}

	msgExt := MsgExt{
		Message:     NewBaseMessage(MessageTypeGet, []byte("test")),
		TLVs:        make([]ExtField, 0),
		PriorityExt: priorityExt,
	}

	assert.Equal(t, types.PriorityHigh, msgExt.PriorityExt.Priority)
}

// TestMsgExt_GetTLV 测试获取指定类型的 TLV 字段
func TestMsgExt_GetTLV(t *testing.T) {
	extFields := []ExtField{
		{Type: ExtHop, Value: []byte{0x05, 0x00, 0x0A, 0x00}},
		{Type: ExtCompress, Value: []byte{0x02, 0x00}},
		{Type: ExtEncrypt, Value: []byte{0x01, 0x00}},
	}

	msgExt := MsgExt{
		Message: NewBaseMessage(MessageTypeGet, []byte("test")),
		TLVs:    extFields,
	}

	hopField := msgExt.GetTLV(ExtHop)
	assert.NotNil(t, hopField)
	assert.Equal(t, ExtHop, hopField.Type)

	compressField := msgExt.GetTLV(ExtCompress)
	assert.NotNil(t, compressField)
	assert.Equal(t, ExtCompress, compressField.Type)

	// 测试不存在的 ExtField
	segmentField := msgExt.GetTLV(ExtFragment)
	assert.Nil(t, segmentField)
}

// TestMsgExt_String 测试 String 方法
func TestMsgExt_String(t *testing.T) {
	msgExt := MsgExt{
		Message:     NewBaseMessage(MessageTypeGet, []byte("test")),
		TLVs:        make([]ExtField, 0),
		HopCount:    &HopExt{Hop: 5, TotalHop: 10},
		PriorityExt: &PriorityExt{Priority: types.PriorityHigh},
	}

	str := msgExt.String()
	assert.Contains(t, str, "MsgExt")
	assert.Contains(t, str, "Type=")
	assert.Contains(t, str, "TLVs=")
	assert.Contains(t, str, "HopCount=")
	assert.Contains(t, str, "PriorityExt=")
}

// ========================================
// SendOpt 函数选项模式测试
// ========================================

// TestSendOpt_WithHopCount 测试 WithHopCount 选项
func TestSendOpt_WithHopCount(t *testing.T) {
	opts := processSendOptions(WithHopCount(10))

	require.NotNil(t, opts)
	require.NotNil(t, opts.hopCount)
	assert.Equal(t, uint16(10), *opts.hopCount)
}

// TestSendOpt_WithCompression 测试 WithCompression 选项
func TestSendOpt_WithCompression(t *testing.T) {
	opts := processSendOptions(WithCompression(2))

	require.NotNil(t, opts)
	require.NotNil(t, opts.compressID)
	assert.Equal(t, uint16(2), *opts.compressID)
}

// TestSendOpt_WithEncryption 测试 WithEncryption 选项
func TestSendOpt_WithEncryption(t *testing.T) {
	opts := processSendOptions(WithEncryption(1))

	require.NotNil(t, opts)
	require.NotNil(t, opts.encryptID)
	assert.Equal(t, uint16(1), *opts.encryptID)
}

// TestSendOpt_WithPriority 测试 WithPriority 选项
func TestSendOpt_WithPriority(t *testing.T) {
	opts := processSendOptions(WithPriority(types.PriorityHigh))

	require.NotNil(t, opts)
	require.NotNil(t, opts.priority)
	assert.Equal(t, types.PriorityHigh, *opts.priority)
}

// TestSendopt_MultipleOptions 测试多个选项组合
func TestSendOpt_MultipleOptions(t *testing.T) {
	opts := processSendOptions(
		WithHopCount(10),
		WithCompression(2),
		WithPriority(types.PriorityLow),
	)

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

	require.NotNil(t, opts)
	require.NotNil(t, opts.hopCount)
	assert.Equal(t, uint16(10), *opts.hopCount, "后面的选项应该覆盖前面的")
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
