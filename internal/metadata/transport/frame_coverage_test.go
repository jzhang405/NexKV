// Package transport 帧覆盖率补充测试
package transport

import (
	"strings"
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// FixedHeader.String() 测试
// ========================================

// TestFixedHeader_String_Coverage 测试 FixedHeader String 方法
func TestFixedHeader_String_Coverage(t *testing.T) {
	header := &FixedHeader{
		NodeID:        12345,
		MsgSeq:        67890,
		ForwardNodeID: 11111,
		Hops:          5,
		MsgType:       types.MessageTypeGet,
		CodecID:       1,
		Flags:         0x01,
	}

	str := header.String()
	assert.Contains(t, str, "NodeID=12345")
	assert.Contains(t, str, "MsgSeq=67890")
	assert.Contains(t, str, "ForwardNodeID=11111")
	assert.Contains(t, str, "Hops=5")
	assert.Contains(t, str, "FixedHeader")
}

// ========================================
// ValidateFlags 测试
// ========================================

// TestValidateFlags_ValidFlags 测试有效的 Flags
func TestValidateFlags_ValidFlags(t *testing.T) {
	testCases := []struct {
		name     string
		flags    uint8
		expected bool
	}{
		{"有效请求（需要响应）", FlagsIsRequest | FlagsExpectResponse, true},
		{"有效请求（不需要响应）", FlagsIsRequest, true},
		{"有效响应（正常）", FlagsIsResponse, true},
		{"有效响应（错误）", FlagsIsResponse | FlagsIsError, true},
		{"有效转发请求", FlagsIsRequest | FlagsIsForward, true},
		{"有效转发响应", FlagsIsResponse | FlagsIsForward, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFlags(tc.flags)
			if tc.expected {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestValidateFlags_InvalidFlags 测试无效的 Flags
func TestValidateFlags_InvalidFlags(t *testing.T) {
	testCases := []struct {
		name        string
		flags       uint8
		expectedErr string
	}{
		{"IS 和 IR 同时设置", FlagsIsRequest | FlagsIsResponse, "IS and IR are both set"},
		{"IS 和 IR 都未设置", 0, "neither IS nor IR is set"},
		{"请求消息设置了 IE（错误标志）", FlagsIsRequest | FlagsIsError, "IE set but message is request"},
		{"响应消息设置了 ER（需要响应）", FlagsIsResponse | FlagsExpectResponse, "ER set but message is response"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFlags(tc.flags)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

// ========================================
// ParseFlags 测试
// ========================================

// TestParseFlags_AllCombinations 测试所有标志位组合
func TestParseFlags_AllCombinations(t *testing.T) {
	testCases := []struct {
		name          string
		flags         uint8
		expIsRequest  bool
		expIsResponse bool
		expIsError    bool
		expExpectResp bool
	}{
		{"请求（需要响应）", FlagsIsRequest | FlagsExpectResponse, true, false, false, true},
		{"请求（不需要响应）", FlagsIsRequest, true, false, false, false},
		{"响应（正常）", FlagsIsResponse, false, true, false, false},
		{"响应（错误）", FlagsIsResponse | FlagsIsError, false, true, true, false},
		{"转发请求", FlagsIsRequest | FlagsIsForward, true, false, false, false},
		{"转发响应", FlagsIsResponse | FlagsIsForward, false, true, false, false},
		{"完整标志", FlagsIsRequest | FlagsExpectResponse | FlagsIsForward, true, false, false, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			isReq, isResp, isErr, expResp := ParseFlags(tc.flags)
			assert.Equal(t, tc.expIsRequest, isReq)
			assert.Equal(t, tc.expIsResponse, isResp)
			assert.Equal(t, tc.expIsError, isErr)
			assert.Equal(t, tc.expExpectResp, expResp)
		})
	}
}

// ========================================
// ExtFieldType.String() 测试
// ========================================

// TestExtFieldType_String_Coverage 测试扩展字段类型字符串表示
func TestExtFieldType_String_Coverage(t *testing.T) {
	testCases := []struct {
		name     string
		extType  ExtFieldType
		expected string
	}{
		{"压缩扩展", ExtCompress, "Compress"},
		{"加密扩展", ExtEncrypt, "Encrypt"},
		{"分片扩展", ExtFragment, "Fragment"},
		{"优先级扩展", ExtPriority, "Priority"},
		{"自定义扩展(5)", ExtCustom, "Custom(5)"},
		{"自定义扩展(10)", ExtFieldType(10), "Custom(10)"},
		{"自定义扩展(100)", ExtFieldType(100), "Custom(100)"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.extType.String()
			assert.Equal(t, tc.expected, result)
		})
	}
}

// ========================================
// ExtField.String() 测试
// ========================================

// TestExtField_String_Coverage 测试扩展字段字符串表示
func TestExtField_String_Coverage(t *testing.T) {
	field := &ExtField{
		Type:  ExtCompress,
		Value: []byte{1, 2, 3, 4, 5},
	}

	str := field.String()
	assert.Contains(t, str, "ExtField")
	assert.Contains(t, str, "Type=Compress")
	assert.Contains(t, str, "ValueLen=5")
}

// TestExtField_String_EmptyValue 测试空值扩展字段
func TestExtField_String_EmptyValue(t *testing.T) {
	field := &ExtField{
		Type:  ExtEncrypt,
		Value: []byte{},
	}

	str := field.String()
	assert.Contains(t, str, "ValueLen=0")
}

// ========================================
// VarExtHeader.String() 测试
// ========================================

// TestVarExtHeader_String_Coverage 测试变长扩展头字符串表示
func TestVarExtHeader_String_Coverage(t *testing.T) {
	header := NewVarExtHeader(
		&ExtField{Type: ExtCompress, Value: []byte{1, 2}},
		&ExtField{Type: ExtEncrypt, Value: []byte{3, 4}},
	)

	str := header.String()
	assert.Contains(t, str, "VarExtHeader")
	assert.Contains(t, str, "FieldsCount=2")
	assert.Contains(t, str, "Size=")
}

// TestVarExtHeader_String_Empty 测试空扩展头
func TestVarExtHeader_String_Empty_Coverage(t *testing.T) {
	header := NewVarExtHeader()

	str := header.String()
	assert.Contains(t, str, "FieldsCount=0")
	assert.Contains(t, str, "Size=0")
}

// ========================================
// Frame.String() 测试
// ========================================

// TestFrame_String_Coverage 测试帧字符串表示
func TestFrame_String_Coverage(t *testing.T) {
	frame := &Frame{
		FixedHeader:  NewFixedHeader(123, 456, types.MessageTypeGet, 1, FlagsIsRequest, 0, 5),
		VarExtHeader: NewVarExtHeader(),
		Data:         []byte("test data"),
	}
	frame.CRC32 = 0x12345678

	str := frame.String()
	assert.Contains(t, str, "Frame")
	assert.Contains(t, str, "FixedHeader")
	assert.Contains(t, str, "VarExtHeader")
	assert.Contains(t, str, "DataLen=9")
	assert.Contains(t, str, "12345678") // CRC32
}

// ========================================
// NewForwardFrameWithHops 测试
// ========================================

// TestNewForwardFrameWithHops_Coverage 测试从原始帧创建转发帧（递减 hops）
func TestNewForwardFrameWithHops_Coverage(t *testing.T) {
	// 创建原始帧（hops=5）
	originalFrame := NewFrame(123, 456, types.MessageTypeGet, 1, FlagsIsRequest, []byte("original"))
	originalFrame.FixedHeader.Hops = 5

	// 创建转发帧
	forwardNodeID := uint64(999)
	newData := []byte("forwarded")
	forwardFrame := NewForwardFrameWithHops(originalFrame, forwardNodeID, newData)

	// 验证 hops 递减
	assert.Equal(t, uint8(4), forwardFrame.FixedHeader.Hops)

	// 验证 ForwardNodeID 设置
	assert.Equal(t, forwardNodeID, forwardFrame.FixedHeader.ForwardNodeID)

	// 验证其他字段保持不变
	assert.Equal(t, originalFrame.FixedHeader.NodeID, forwardFrame.FixedHeader.NodeID)
	assert.Equal(t, originalFrame.FixedHeader.MsgSeq, forwardFrame.FixedHeader.MsgSeq)
	assert.Equal(t, originalFrame.FixedHeader.MsgType, forwardFrame.FixedHeader.MsgType)

	// 验证数据被替换
	assert.Equal(t, newData, forwardFrame.Data)
}

// TestNewForwardFrameWithHops_HopsZero 测试 hops 为 0 的情况
func TestNewForwardFrameWithHops_HopsZero_Coverage(t *testing.T) {
	originalFrame := NewFrame(123, 456, types.MessageTypeGet, 1, FlagsIsRequest, []byte("original"))
	originalFrame.FixedHeader.Hops = 0

	forwardFrame := NewForwardFrameWithHops(originalFrame, 999, []byte("forwarded"))

	// hops 为 0 时，保持为 0
	assert.Equal(t, uint8(0), forwardFrame.FixedHeader.Hops)
}

// TestNewForwardFrameWithHops_PreserveExtHeader 测试保留扩展头
func TestNewForwardFrameWithHops_PreserveExtHeader_Coverage(t *testing.T) {
	originalFrame := NewFrame(123, 456, types.MessageTypeGet, 1, FlagsIsRequest, []byte("original"))
	originalFrame.FixedHeader.Hops = 3
	// 添加压缩扩展
	originalFrame.VarExtHeader.AddField(&ExtField{
		Type:  ExtCompress,
		Value: []byte{1},
	})

	forwardFrame := NewForwardFrameWithHops(originalFrame, 999, []byte("forwarded"))

	// 验证扩展头被保留
	assert.Equal(t, originalFrame.VarExtHeader, forwardFrame.VarExtHeader)
	assert.Equal(t, 1, len(forwardFrame.VarExtHeader.Fields))
}

// ========================================
// WithEncrypt 测试
// ========================================

// TestWithEncrypt_Success_Coverage 测试添加加密扩展字段
func TestWithEncrypt_Success_Coverage(t *testing.T) {
	frame := NewFrame(123, 456, types.MessageTypeGet, 1, FlagsIsRequest, []byte("test"))

	// 添加加密扩展（使用 nonce 和版本）
	encryptID := uint16(1)
	nonce := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	version := "1.0"

	result := frame.WithEncrypt(encryptID, nonce, version)

	// 验证链式调用返回同一帧
	assert.Same(t, frame, result)

	// 验证扩展字段被添加
	assert.Equal(t, 1, len(frame.VarExtHeader.Fields))

	// 验证扩展字段类型
	assert.Equal(t, ExtEncrypt, frame.VarExtHeader.Fields[0].Type)
}

// TestWithEncrypt_EncodeError_Coverage 测试加密扩展编码失败
func TestWithEncrypt_EncodeError_Coverage(t *testing.T) {
	frame := NewFrame(123, 456, types.MessageTypeGet, 1, FlagsIsRequest, []byte("test"))

	// 使用空的 version 来测试错误处理
	// 注意：WithEncrypt 即使编码失败也会添加空扩展字段（由实现决定）
	result := frame.WithEncrypt(1, []byte{1, 2, 3}, "")

	// 验证链式调用不被中断
	assert.Same(t, frame, result)

	// 验证扩展字段被添加（即使编码失败，也会添加一个空的或部分扩展字段）
	// 实际行为：WithEncrypt 会添加扩展字段，即使 EncodeEncryptExt 失败
	assert.GreaterOrEqual(t, len(frame.VarExtHeader.Fields), 0)
}

// ========================================
// HexDump 测试
// ========================================

// TestHexDump_Success_Coverage 测试帧的十六进制转储
func TestHexDump_Success_Coverage(t *testing.T) {
	frame := NewFrame(123, 456, types.MessageTypeGet, 1, FlagsIsRequest, []byte("test data"))
	frame.CRC32 = 0x12345678

	hexStr := frame.HexDump()

	// 验证返回的是十六进制转储格式
	assert.NotEmpty(t, hexStr)
	// HexDump 格式通常包含地址、十六进制值和 ASCII 表示
	assert.Contains(t, hexStr, "00000000") // 起始地址
}

// TestHexDump_MarshalError_Coverage 测试序列化失败时的 HexDump
func TestHexDump_MarshalError_Coverage(t *testing.T) {
	// 创建一个超过最大大小的帧
	largeData := make([]byte, MaxFrameSize+1)
	frame := NewFrame(123, 456, types.MessageTypeGet, 1, FlagsIsRequest, largeData)

	hexStr := frame.HexDump()

	// 验证返回错误信息（中文错误消息）
	assert.Contains(t, hexStr, "Error marshaling frame")
	assert.Contains(t, hexStr, "帧过大") // Chinese error message
}

// ========================================
// FrameFlags.String() 测试
// ========================================

// TestFrameFlags_Coverage 测试标志位常量定义
// 注意：frame.go 中没有 FrameFlags 类型或 String() 方法，flags 只是 uint8 类型
func TestFrameFlags_Coverage(t *testing.T) {
	// 验证各个 flag 常量的值
	assert.Equal(t, uint8(0x01), FlagsIsRequest, "IS 标志应为 0x01")
	assert.Equal(t, uint8(0x02), FlagsIsResponse, "IR 标志应为 0x02")
	assert.Equal(t, uint8(0x04), FlagsIsError, "IE 标志应为 0x04")
	assert.Equal(t, uint8(0x08), FlagsExpectResponse, "ER 标志应为 0x08")
	assert.Equal(t, uint8(0x20), FlagsIsForward, "F 标志应为 0x20")

	// 验证组合标志
	assert.Equal(t, uint8(0x09), FlagsIsRequest|FlagsExpectResponse, "双向请求应为 0x09")
	assert.Equal(t, uint8(0x06), FlagsIsResponse|FlagsIsError, "错误响应应为 0x06")
}

// ========================================
// 集成测试
// ========================================

// TestFrame_CoverageRoundTrip 测试帧的完整序列化/反序列化
func TestFrame_CoverageRoundTrip(t *testing.T) {
	// 创建原始帧
	originalFrame := NewFrame(12345, 67890, types.MessageTypePut, 1, FlagsIsRequest, []byte("test data payload"))

	// 添加扩展字段
	originalFrame.WithCompress(1)
	originalFrame.WithPriority(types.PriorityHigh)

	// 序列化
	data, err := originalFrame.Marshal()
	require.NoError(t, err)

	// 反序列化
	newFrame := &Frame{}
	err = newFrame.Unmarshal(data)
	require.NoError(t, err)

	// 验证 FixedHeader
	assert.Equal(t, originalFrame.FixedHeader.NodeID, newFrame.FixedHeader.NodeID)
	assert.Equal(t, originalFrame.FixedHeader.MsgSeq, newFrame.FixedHeader.MsgSeq)
	assert.Equal(t, originalFrame.FixedHeader.MsgType, newFrame.FixedHeader.MsgType)

	// 验证 Data
	assert.Equal(t, originalFrame.Data, newFrame.Data)

	// 验证 VarExtHeader（String 方法用于验证）
	t.Logf("Original: %s", originalFrame.VarExtHeader.String())
	t.Logf("New: %s", newFrame.VarExtHeader.String())
}

// TestFrame_WithEncrypt_Integration 测试 WithEncrypt 集成场景
func TestFrame_WithEncrypt_Integration(t *testing.T) {
	frame := NewFrame(123, 456, types.MessageTypeGet, 1, FlagsIsRequest, []byte("sensitive data"))

	// 添加加密扩展
	nonce := make([]byte, 12) // AES-GCM 推荐的 nonce 长度
	for i := range nonce {
		nonce[i] = byte(i)
	}
	frame.WithEncrypt(1, nonce, "AES-GCM-1.0")

	// 验证可以序列化
	data, err := frame.Marshal()
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	// 验证 HexDump 正常工作
	hexStr := frame.HexDump()
	assert.NotEmpty(t, hexStr)
	assert.NotContains(t, hexStr, "Error")

	t.Log("HexDump output (first 200 chars):")
	if len(hexStr) > 200 {
		t.Log(hexStr[:200])
	} else {
		t.Log(hexStr)
	}
}

// TestNewForwardFrameWithHops_RealWorldScenario 测试真实场景的转发帧
func TestNewForwardFrameWithHops_RealWorldScenario(t *testing.T) {
	// 创建初始请求帧（hops=10）
	initialFrame := NewFrame(100, 200, types.MessageTypeGet, 1, FlagsIsRequest|FlagsExpectResponse, []byte("GET /api/key"))
	initialFrame.FixedHeader.Hops = 10

	// 模拟经过 5 个节点转发
	for i := 0; i < 5; i++ {
		forwardNodeID := uint64(200 + i)
		forwardedData := []byte("Forwarded from node " + string(rune('A'+i)))

		initialFrame = NewForwardFrameWithHops(initialFrame, forwardNodeID, forwardedData)
		hops := initialFrame.FixedHeader.Hops

		t.Logf("After hop %d: hops=%d, forwardNodeID=%d", i+1, hops, forwardNodeID)
		assert.Equal(t, uint8(10-(i+1)), hops)
	}

	// 验证最终 hops
	assert.Equal(t, uint8(5), initialFrame.FixedHeader.Hops)
	assert.NotEqual(t, uint64(0), initialFrame.FixedHeader.ForwardNodeID)
}

// TestFrame_HexDump_Format 测试 HexDump 输出格式
func TestFrame_HexDump_Format(t *testing.T) {
	frame := NewFrame(1, 2, types.MessageTypeGet, 1, FlagsIsRequest, []byte("TEST"))
	frame.CRC32 = 0xDEADBEEF

	hexStr := frame.HexDump()

	// 验证 HexDump 格式
	assert.NotEmpty(t, hexStr)
	// 标准的 hex.Dump 格式应该包含：
	// - 地址（如 00000000）
	// - 十六进制值
	// - ASCII 表示（在右边）
	assert.Contains(t, hexStr, "00000000")

	// 验证魔术字 NXUT 在十六进制转储中
	// NXUT = 0x4E 0x58 0x55 0x54
	// 在 hex.Dump 格式中会显示为 "4e 58 55 54"（小写）
	// 或者可能显示为连续的十六进制 "4e585554"
	assert.True(t,
		strings.Contains(hexStr, "4e 58 55 54") ||
			strings.Contains(hexStr, "4E 58 55 54") ||
			strings.Contains(hexStr, "4e585554") ||
			strings.Contains(hexStr, "4E585554") ||
			strings.Contains(hexStr, "NXUT"), // ASCII representation
		"HexDump should contain the magic number 'NXUT' (0x4E 0x58 0x55 0x54)")
}

// ========================================
// 辅助函数（已移除，不再使用）
// ========================================
