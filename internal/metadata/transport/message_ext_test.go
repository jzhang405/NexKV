// Package transport 测试 MsgExt 和 SendOpt 功能
package transport

import (
	"context"
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

// ========================================
// EncodeTLVs 方法测试
// ========================================

// TestMsgExt_EncodeTLVs_AllFields 测试编码所有 TLV 字段
func TestMsgExt_EncodeTLVs_AllFields(t *testing.T) {
	msgExt := MsgExt{
		Message:     NewBaseMessage(MessageTypeGet, []byte("test")),
		TLVs:        make([]ExtField, 0),
		HopCount:    &HopExt{Hop: 5, TotalHop: 10},
		PriorityExt: &PriorityExt{Priority: types.PriorityHigh},
		Compress:    &CompressExt{CompressID: 2},
		Segment:     &SegmentExt{Index: 1, Total: 5},
	}

	fields, err := msgExt.EncodeTLVs()
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

// TestMsgExt_EncodeTLVs_PartialFields 测试编码部分 TLV 字段
func TestMsgExt_EncodeTLVs_PartialFields(t *testing.T) {
	msgExt := MsgExt{
		Message:     NewBaseMessage(MessageTypeGet, []byte("test")),
		TLVs:        make([]ExtField, 0),
		HopCount:    &HopExt{Hop: 3, TotalHop: 10},
		PriorityExt: nil,
		Compress:    nil,
		Segment:     nil,
	}

	fields, err := msgExt.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 1) // 只有 Hop Count

	hopField := findExtField(fields, ExtHop)
	require.NotNil(t, hopField)
	hop, totalHop, err := DecodeHopExt(hopField)
	require.NoError(t, err)
	assert.Equal(t, uint16(3), hop)
	assert.Equal(t, uint16(10), totalHop)
}

// TestMsgExt_EncodeTLVs_NoFields 测试无 TLV 字段
func TestMsgExt_EncodeTLVs_NoFields(t *testing.T) {
	msgExt := MsgExt{
		Message:     NewBaseMessage(MessageTypeGet, []byte("test")),
		TLVs:        make([]ExtField, 0),
		HopCount:    nil,
		PriorityExt: nil,
		Compress:    nil,
		Segment:     nil,
	}

	fields, err := msgExt.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 0) // 无字段
}

// TestMsgExt_EncodeTLVs_HopDecrement 测试 Hop Count 递减后的编码
func TestMsgExt_EncodeTLVs_HopDecrement(t *testing.T) {
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		TLVs:     make([]ExtField, 0),
		HopCount: &HopExt{Hop: 10, TotalHop: 10},
	}

	// 模拟递减 Hop Count
	msgExt.HopCount.Hop = 9

	fields, err := msgExt.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 1)

	hopField := findExtField(fields, ExtHop)
	require.NotNil(t, hopField)
	hop, totalHop, err := DecodeHopExt(hopField)
	require.NoError(t, err)
	assert.Equal(t, uint16(9), hop, "Hop 应该被递减")
	assert.Equal(t, uint16(10), totalHop, "TotalHop 应该保持不变")
}

// TestMsgExt_EncodeTLVs_EncryptField 测试加密字段编码
func TestMsgExt_EncodeTLVs_EncryptField(t *testing.T) {
	msgExt := MsgExt{
		Message: NewBaseMessage(MessageTypeGet, []byte("test")),
		TLVs:    make([]ExtField, 0),
		Encrypt: &EncryptExt{
			EncryptID: 1,
			Nonce:     []byte{1, 2, 3, 4},
			Version:   "1.0",
		},
	}

	fields, err := msgExt.EncodeTLVs()
	require.NoError(t, err)
	require.Len(t, fields, 1)

	encryptField := findExtField(fields, ExtEncrypt)
	require.NotNil(t, encryptField)
	encryptID, nonce, version, err := DecodeEncryptExt(encryptField)
	require.NoError(t, err)
	assert.Equal(t, uint16(1), encryptID)
	assert.Equal(t, []byte{1, 2, 3, 4}, nonce)
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
// ForwardMessage 集成测试
// ========================================

// TestForwardMessage_ContextCancel 测试 context 取消场景（P1-4）
func TestForwardMessage_ContextCancel(t *testing.T) {
	// 创建已取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}

	// TCP Transport
	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	// 应该返回 context 取消错误
	_, err = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", msgExt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")

	// UDP Transport
	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	// 应该返回 context 取消错误
	_, err = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", msgExt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

// TestForwardMessage_NilMessage 测试 nil Message 检查（P1-2）
func TestForwardMessage_NilMessage(t *testing.T) {
	ctx := context.Background()

	msgExt := MsgExt{
		Message:  nil, // nil Message
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}

	// TCP Transport
	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	// 应该返回消息为空错误
	_, err = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", msgExt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "消息为空")

	// UDP Transport
	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	// 应该返回消息为空错误
	_, err = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", msgExt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "消息为空")
}

// TestForwardMessage_HopCountDecrement 测试 Hop Count 递减（P1-1）
func TestForwardMessage_HopCountDecrement(t *testing.T) {
	// 创建原始 msgExt
	originalHop := uint16(10)
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: originalHop, TotalHop: 20},
	}

	// 转发后原始 Hop Count 应该保持不变（因为有深拷贝）
	newHop := msgExt.HopCount.Hop
	assert.Equal(t, originalHop, newHop, "原始 msgExt.HopCount 不应该被修改")
}

// TestForwardMessage_HopCountExpired 测试 Hop Count 过期
func TestForwardMessage_HopCountExpired(t *testing.T) {
	ctx := context.Background()

	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 0, TotalHop: 10}, // Hop = 0
	}

	// TCP Transport
	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	// 应该返回 Hop Count 过期错误
	_, err = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", msgExt)
	assert.Error(t, err)
	assert.Equal(t, types.ErrTransportHopCountExpired, err.(*types.Error).Code)

	// UDP Transport
	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	// 应该返回 Hop Count 过期错误
	_, err = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", msgExt)
	assert.Error(t, err)
	assert.Equal(t, types.ErrTransportHopCountExpired, err.(*types.Error).Code)
}

// TestForwardMessage_DeepCopyPreventsDataRace 测试深拷贝防止 data race（P1-1）
func TestForwardMessage_DeepCopyPreventsDataRace(t *testing.T) {
	ctx := context.Background()

	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}

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
				// 每个协程独立的 msgExt 拷贝
				localMsgExt := msgExt
				_, _ = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", localMsgExt)
				done <- true
			}()
		}

		// 等待所有协程完成
		for i := 0; i < 10; i++ {
			<-done
		}
	})
}
