// Package transport 帧编解码器测试
package transport

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// 测试辅助函数
// ========================================

// createTestCodecFrame 创建测试用 Frame（用于编解码器测试）
func createTestCodecFrame(msgType types.MessageType, data []byte) *Frame {
	return NewFrame(
		12345,                // nodeID
		1,                    // msgSeq
		MessageType(msgType), // msgType
		1,                    // codecID (MessagePack)
		FlagsTwoWayRequest,   // flags
		data,
	)
}

// createTestCodecFrameWithExt 创建带扩展头的测试帧
func createTestCodecFrameWithExt(msgType types.MessageType, data []byte, exts []*ExtField) *Frame {
	frame := NewFrame(
		12345,
		1,
		MessageType(msgType),
		1,
		FlagsTwoWayRequest,
		data,
	)
	// 添加扩展字段
	for _, ext := range exts {
		frame.VarExtHeader.AddField(ext)
	}
	return frame
}

// ========================================
// 测试辅助类型（AutoDetectCodec 仅用于测试）
// ========================================

// AutoDetectCodec 自动检测编解码器（测试专用）
type AutoDetectCodec struct {
	tcpCodec *TCPFrameCodec
	udpCodec *UDPFrameCodec
}

// NewAutoDetectCodec 创建自动检测编解码器
func NewAutoDetectCodec() *AutoDetectCodec {
	return &AutoDetectCodec{
		tcpCodec: NewTCPFrameCodec(),
		udpCodec: NewUDPFrameCodec(),
	}
}

// DetectProtocol 检测协议类型
func (c *AutoDetectCodec) DetectProtocol(data []byte) (FrameCodec, error) {
	// 数据太短，无法判断
	if len(data) < FixedHeaderLen+CRCLen {
		return c.udpCodec, nil
	}

	// 尝试检测 TCP 帧（4 字节长度前缀）
	if len(data) >= 8 {
		buf := bytes.NewReader(data)
		var frameSize uint32
		if err := binary.Read(buf, binary.BigEndian, &frameSize); err == nil {
			remainingData := len(data) - 4
			minFrameSize := FixedHeaderLen + CRCLen
			if int(frameSize) >= minFrameSize && int(frameSize) <= remainingData {
				if remainingData >= 4 {
					versionByte := data[4]
					if versionByte >= 1 {
						return c.tcpCodec, nil
					}
				}
			}
		}
	}

	return c.udpCodec, nil
}

// ========================================
// TCPFrameCodec 测试
// ========================================

// TestTCPFrameCodec_EncodeFrame_Normal 测试正常编码 TCP 帧
func TestTCPFrameCodec_EncodeFrame_Normal(t *testing.T) {
	codec := NewTCPFrameCodec()

	testCases := []struct {
		name    string
		frame   *Frame
		wantMin int // 最小预期大小
	}{
		{
			name:    "最小帧（无数据）",
			frame:   createTestCodecFrame(types.MessageTypeGet, nil),
			wantMin: FixedHeaderLen + CRCLen + 4, // +4 for length prefix
		},
		{
			name:    "带数据帧",
			frame:   createTestCodecFrame(types.MessageTypePut, []byte("test data")),
			wantMin: FixedHeaderLen + CRCLen + 4 + 9,
		},
		{
			name: "带扩展头帧",
			frame: createTestCodecFrameWithExt(
				types.MessageTypeGet,
				[]byte("data"),
				[]*ExtField{
					{Type: ExtFragment, Value: []byte{0, 2}},
				},
			),
			wantMin: FixedHeaderLen + CRCLen + 4 + 4,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := codec.EncodeFrame(tc.frame)
			require.NoError(t, err)
			require.NotNil(t, encoded)
			assert.GreaterOrEqual(t, len(encoded), tc.wantMin)

			// 验证长度前缀
			require.GreaterOrEqual(t, len(encoded), 4)
			lengthPrefix := binary.BigEndian.Uint32(encoded[:4])
			assert.Equal(t, uint32(len(encoded)-4), lengthPrefix)
		})
	}
}

// TestTCPFrameCodec_EncodeFrame_FrameTooLarge 测试帧过大错误
func TestTCPFrameCodec_EncodeFrame_FrameTooLarge(t *testing.T) {
	codec := &TCPFrameCodec{MaxFrameSize: 100}

	// 创建超过最大大小的帧
	largeData := make([]byte, 200)
	frame := createTestCodecFrame(types.MessageTypePut, largeData)

	encoded, err := codec.EncodeFrame(frame)
	assert.Error(t, err)
	assert.Nil(t, encoded)
	assert.ErrorIs(t, err, types.ErrFrameTooLarge)
}

// TestTCPFrameCodec_DecodeFrame_Normal 测试正常解码 TCP 帧
func TestTCPFrameCodec_DecodeFrame_Normal(t *testing.T) {
	codec := NewTCPFrameCodec()

	originalFrame := createTestCodecFrame(types.MessageTypeGet, []byte("test data"))

	// 编码
	encoded, err := codec.EncodeFrame(originalFrame)
	require.NoError(t, err)

	// 解码
	decoded, err := codec.DecodeFrame(encoded)
	require.NoError(t, err)
	require.NotNil(t, decoded)

	// 验证
	assert.Equal(t, originalFrame.FixedHeader.Magic, decoded.FixedHeader.Magic)
	assert.Equal(t, originalFrame.FixedHeader.MsgType, decoded.FixedHeader.MsgType)
	assert.Equal(t, originalFrame.Data, decoded.Data)
}

// TestTCPFrameCodec_DecodeFrame_RoundTrip 测试各种消息类型的编解码往返
func TestTCPFrameCodec_DecodeFrame_RoundTrip(t *testing.T) {
	codec := NewTCPFrameCodec()

	msgTypes := []types.MessageType{
		types.MessageTypeGet,
		types.MessageTypePut,
		types.MessageTypeDelete,
		types.MessageTypeGossipSync,
		types.MessageType2PCPrepare,
		types.MessageTypeNodeJoin,
	}

	for _, msgType := range msgTypes {
		t.Run(msgType.String(), func(t *testing.T) {
			original := createTestCodecFrameWithExt(
				msgType,
				[]byte("test payload"),
				[]*ExtField{},
			)

			encoded, err := codec.EncodeFrame(original)
			require.NoError(t, err)

			decoded, err := codec.DecodeFrame(encoded)
			require.NoError(t, err)

			assert.Equal(t, original.FixedHeader.MsgType, decoded.FixedHeader.MsgType)
			assert.Equal(t, original.Data, decoded.Data)
			assert.Equal(t, len(original.VarExtHeader.Fields), len(decoded.VarExtHeader.Fields))
		})
	}
}

// TestTCPFrameCodec_DecodeFrame_DataTooShort 测试数据过短错误
func TestTCPFrameCodec_DecodeFrame_DataTooShort(t *testing.T) {
	codec := NewTCPFrameCodec()

	testCases := []struct {
		name string
		data []byte
	}{
		{"空数据", nil},
		{"1字节", []byte{0x00}},
		{"2字节", []byte{0x00, 0x01}},
		{"3字节", []byte{0x00, 0x01, 0x02}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := codec.DecodeFrame(tc.data)
			assert.Error(t, err)
			assert.Nil(t, decoded)
			assert.ErrorIs(t, err, types.ErrInvalidFrameFormat)
		})
	}
}

// TestTCPFrameCodec_DecodeFrame_IncompleteFrame 测试不完整帧错误
func TestTCPFrameCodec_DecodeFrame_IncompleteFrame(t *testing.T) {
	codec := NewTCPFrameCodec()

	// 创建有效帧
	frame := createTestCodecFrame(types.MessageTypeGet, []byte("test"))
	encoded, _ := codec.EncodeFrame(frame)

	// 截断数据
	incompleteData := encoded[:len(encoded)-5]

	decoded, err := codec.DecodeFrame(incompleteData)
	assert.Error(t, err)
	assert.Nil(t, decoded)
	assert.ErrorIs(t, err, types.ErrInvalidFrameFormat)
}

// TestTCPFrameCodec_DecodeFrame_FrameTooLarge 测试解码时帧过大错误
func TestTCPFrameCodec_DecodeFrame_FrameTooLarge(t *testing.T) {
	codec := &TCPFrameCodec{MaxFrameSize: 100}

	// 创建声明大小超过限制的帧
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.BigEndian, uint32(200)) // 声称 200 字节
	buf.Write(make([]byte, 50))                          // 实际只有 50 字节

	decoded, err := codec.DecodeFrame(buf.Bytes())
	assert.Error(t, err)
	assert.Nil(t, decoded)
	assert.ErrorIs(t, err, types.ErrFrameTooLarge)
}

// TestTCPFrameCodec_EstimateSize 测试大小估算
func TestTCPFrameCodec_EstimateSize(t *testing.T) {
	codec := NewTCPFrameCodec()

	testCases := []struct {
		name          string
		frame         *Frame
		expectedExtra int // 额外大小（长度前缀）
	}{
		{
			name:          "无数据帧",
			frame:         createTestCodecFrame(types.MessageTypeGet, nil),
			expectedExtra: 4, // 长度前缀
		},
		{
			name:          "带数据帧",
			frame:         createTestCodecFrame(types.MessageTypePut, []byte("test")),
			expectedExtra: 4,
		},
		{
			name: "带扩展头帧",
			frame: createTestCodecFrameWithExt(
				types.MessageTypeGet,
				nil, []*ExtField{},
			),
			expectedExtra: 4,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			estimated := codec.EstimateSize(tc.frame)
			// 估算应该接近实际编码大小
			encoded, _ := codec.EncodeFrame(tc.frame)
			assert.Equal(t, len(encoded), estimated)
		})
	}
}

// ========================================
// UDPFrameCodec 测试
// ========================================

// TestUDPFrameCodec_EncodeFrame_Normal 测试正常编码 UDP 帧
func TestUDPFrameCodec_EncodeFrame_Normal(t *testing.T) {
	codec := NewUDPFrameCodec()

	testCases := []struct {
		name    string
		frame   *Frame
		wantMin int // 最小预期大小
	}{
		{
			name:    "最小帧",
			frame:   createTestCodecFrame(types.MessageTypeGet, nil),
			wantMin: FixedHeaderLen + CRCLen,
		},
		{
			name:    "带数据帧",
			frame:   createTestCodecFrame(types.MessageTypePut, []byte("test data")),
			wantMin: FixedHeaderLen + CRCLen + 9,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := codec.EncodeFrame(tc.frame)
			require.NoError(t, err)
			require.NotNil(t, encoded)
			assert.GreaterOrEqual(t, len(encoded), tc.wantMin)
		})
	}
}

// TestUDPFrameCodec_EncodeFrame_FrameTooLarge 测试 UDP 帧大小限制
func TestUDPFrameCodec_EncodeFrame_FrameTooLarge(t *testing.T) {
	codec := &UDPFrameCodec{MaxFrameSize: 100}

	largeData := make([]byte, 200)
	frame := createTestCodecFrame(types.MessageTypePut, largeData)

	encoded, err := codec.EncodeFrame(frame)
	assert.Error(t, err)
	assert.Nil(t, encoded)
	assert.ErrorIs(t, err, types.ErrFrameTooLarge)
}

// TestUDPFrameCodec_DecodeFrame_Normal 测试正常解码 UDP 帧
func TestUDPFrameCodec_DecodeFrame_Normal(t *testing.T) {
	codec := NewUDPFrameCodec()

	originalFrame := createTestCodecFrame(types.MessageTypeGet, []byte("udp data"))

	encoded, err := codec.EncodeFrame(originalFrame)
	require.NoError(t, err)

	decoded, err := codec.DecodeFrame(encoded)
	require.NoError(t, err)
	require.NotNil(t, decoded)

	assert.Equal(t, originalFrame.FixedHeader.Magic, decoded.FixedHeader.Magic)
	assert.Equal(t, originalFrame.FixedHeader.MsgType, decoded.FixedHeader.MsgType)
	assert.Equal(t, originalFrame.Data, decoded.Data)
}

// TestUDPFrameCodec_DecodeFrame_TooShort 测试数据过短错误
func TestUDPFrameCodec_DecodeFrame_TooShort(t *testing.T) {
	codec := NewUDPFrameCodec()

	testCases := []struct {
		name string
		data []byte
	}{
		{"空数据", nil},
		{"小于固定头", make([]byte, FixedHeaderLen-1)},
		{"缺少CRC", make([]byte, FixedHeaderLen+CRCLen-1)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := codec.DecodeFrame(tc.data)
			assert.Error(t, err)
			assert.Nil(t, decoded)
			assert.ErrorIs(t, err, types.ErrInvalidFrameFormat)
		})
	}
}

// TestUDPFrameCodec_DecodeFrame_FrameTooLarge 测试解码时帧过大错误
func TestUDPFrameCodec_DecodeFrame_FrameTooLarge(t *testing.T) {
	maxSize := FixedHeaderLen + CRCLen + 50
	codec := &UDPFrameCodec{MaxFrameSize: maxSize}

	// 创建超过最大大小的帧
	largeData := make([]byte, 200) // 200 bytes of data
	frame := createTestCodecFrame(types.MessageTypePut, largeData)

	// 编码会失败因为帧太大
	_, err := codec.EncodeFrame(frame)
	assert.Error(t, err)
	assert.ErrorIs(t, err, types.ErrFrameTooLarge)
}

// TestUDPFrameCodec_EstimateSize 测试 UDP 大小估算
func TestUDPFrameCodec_EstimateSize(t *testing.T) {
	codec := NewUDPFrameCodec()

	frame := createTestCodecFrame(types.MessageTypeGet, []byte("test"))
	estimated := codec.EstimateSize(frame)

	// UDP 估算应该等于帧大小
	assert.Equal(t, frame.Size(), estimated)
}

// ========================================
// AutoDetectCodec 测试
// ========================================

// TestAutoDetectCodec_DetectProtocol_TCP 测试检测 TCP 协议
func TestAutoDetectCodec_DetectProtocol_TCP(t *testing.T) {
	codec := NewAutoDetectCodec()
	tcpCodec := NewTCPFrameCodec()

	// 创建 TCP 帧
	frame := createTestCodecFrame(types.MessageTypeGet, []byte("test data"))
	tcpData, _ := tcpCodec.EncodeFrame(frame)

	detected, err := codec.DetectProtocol(tcpData)
	assert.NoError(t, err)
	assert.IsType(t, &TCPFrameCodec{}, detected)
}

// TestAutoDetectCodec_DetectProtocol_UDP 测试检测 UDP 协议
func TestAutoDetectCodec_DetectProtocol_UDP(t *testing.T) {
	codec := NewAutoDetectCodec()
	udpCodec := NewUDPFrameCodec()

	// 创建 UDP 帧
	frame := createTestCodecFrame(types.MessageTypeGet, []byte("test data"))
	udpData, _ := udpCodec.EncodeFrame(frame)

	detected, err := codec.DetectProtocol(udpData)
	assert.NoError(t, err)
	assert.IsType(t, &UDPFrameCodec{}, detected)
}

// TestAutoDetectCodec_DetectProtocol_TooShort 测试数据过短时默认 UDP
func TestAutoDetectCodec_DetectProtocol_TooShort(t *testing.T) {
	codec := NewAutoDetectCodec()

	testCases := []struct {
		name string
		data []byte
	}{
		{"空数据", nil},
		{"少量数据", []byte{0x01, 0x02}},
		{"接近最小", make([]byte, FixedHeaderLen+CRCLen-1)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			detected, err := codec.DetectProtocol(tc.data)
			assert.NoError(t, err)
			assert.IsType(t, &UDPFrameCodec{}, detected)
		})
	}
}

// TestAutoDetectCodec_DetectProtocol_VersionCheck 测试版本号检查
func TestAutoDetectCodec_DetectProtocol_VersionCheck(t *testing.T) {
	codec := NewAutoDetectCodec()

	// 模拟 TCP 帧，但版本号为 0
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.BigEndian, uint32(FixedHeaderLen+CRCLen)) // 长度前缀
	buf.WriteByte(0)                                                       // 版本号 = 0
	buf.Write(make([]byte, FixedHeaderLen+CRCLen-1))

	detected, err := codec.DetectProtocol(buf.Bytes())
	assert.NoError(t, err)
	// 版本号为 0，应该检测为 UDP
	assert.IsType(t, &UDPFrameCodec{}, detected)
}

// ========================================
// TCPFrameStreamDecoder 测试
// ========================================

// TestTCPFrameStreamDecoder_Feed_SingleFrame 测试喂入单个完整帧
func TestTCPFrameStreamDecoder_Feed_SingleFrame(t *testing.T) {
	decoder := NewTCPFrameStreamDecoder()
	tcpCodec := NewTCPFrameCodec()

	frame := createTestCodecFrame(types.MessageTypeGet, []byte("single frame"))
	encoded, _ := tcpCodec.EncodeFrame(frame)

	frames, err := decoder.Feed(encoded)
	assert.NoError(t, err)
	assert.Len(t, frames, 1)
	assert.Equal(t, frame.FixedHeader.MsgType, frames[0].FixedHeader.MsgType)
}

// TestTCPFrameStreamDecoder_Feed_MultipleFrames 测试喂入多个帧（粘包）
func TestTCPFrameStreamDecoder_Feed_MultipleFrames(t *testing.T) {
	decoder := NewTCPFrameStreamDecoder()
	tcpCodec := NewTCPFrameCodec()

	// 创建多个帧并拼接
	frame1 := createTestCodecFrame(types.MessageTypeGet, []byte("frame 1"))
	frame2 := createTestCodecFrame(types.MessageTypePut, []byte("frame 2"))
	frame3 := createTestCodecFrame(types.MessageTypeDelete, []byte("frame 3"))

	data1, _ := tcpCodec.EncodeFrame(frame1)
	data2, _ := tcpCodec.EncodeFrame(frame2)
	data3, _ := tcpCodec.EncodeFrame(frame3)

	// 模拟粘包：三个帧的数据连在一起
	stickyData := append(data1, data2...)
	stickyData = append(stickyData, data3...)

	frames, err := decoder.Feed(stickyData)
	assert.NoError(t, err)
	assert.Len(t, frames, 3)
	assert.Equal(t, types.MessageTypeGet, frames[0].FixedHeader.MsgType)
	assert.Equal(t, types.MessageTypePut, frames[1].FixedHeader.MsgType)
	assert.Equal(t, types.MessageTypeDelete, frames[2].FixedHeader.MsgType)
}

// TestTCPFrameStreamDecoder_Feed_PartialFrame 测试喂入不完整帧
func TestTCPFrameStreamDecoder_Feed_PartialFrame(t *testing.T) {
	decoder := NewTCPFrameStreamDecoder()
	tcpCodec := NewTCPFrameCodec()

	frame := createTestCodecFrame(types.MessageTypeGet, []byte("test data"))
	encoded, _ := tcpCodec.EncodeFrame(frame)

	// 只喂入前半部分
	partialData := encoded[:len(encoded)/2]

	frames, err := decoder.Feed(partialData)
	assert.NoError(t, err)
	assert.Len(t, frames, 0) // 没有完整帧

	// 喂入剩余部分
	remainingData := encoded[len(encoded)/2:]
	frames, err = decoder.Feed(remainingData)
	assert.NoError(t, err)
	assert.Len(t, frames, 1)
}

// TestTCPFrameStreamDecoder_Feed_FeedTooLarge 测试单次 Feed 过大（DoS 保护）
func TestTCPFrameStreamDecoder_Feed_FeedTooLarge(t *testing.T) {
	decoder := NewTCPFrameStreamDecoder()

	// 创建超过 MaxSingleFeedSize 的数据
	largeData := make([]byte, MaxSingleFeedSize+1)

	frames, err := decoder.Feed(largeData)
	assert.Error(t, err)
	assert.Nil(t, frames)
	assert.ErrorIs(t, err, types.ErrFrameTooLarge)
}

// TestTCPFrameStreamDecoder_Feed_BufferOverflow 测试缓冲区溢出保护
// 注意: MaxBufferSize 是 10MB，为避免测试时间过长，这里测试缓冲区累积机制
func TestTCPFrameStreamDecoder_Feed_BufferOverflow(t *testing.T) {
	decoder := NewTCPFrameStreamDecoder()

	// 策略: 喂入一个长度前缀，表示后续有一个很大的帧
	// 然后持续喂入数据，但由于帧始终不完整，数据会累积在缓冲区中
	// 直到超过 MaxBufferSize

	// 构造一个长度前缀，声称帧大小接近 MaxBufferSize
	// 这样 decoder 会等待足够的数据，导致缓冲区累积
	largeFrameSize := MaxBufferSize - 1000 // 稍小于 MaxBufferSize
	lengthPrefix := make([]byte, 4)
	lengthPrefix[0] = byte(largeFrameSize >> 24)
	lengthPrefix[1] = byte(largeFrameSize >> 16)
	lengthPrefix[2] = byte(largeFrameSize >> 8)
	lengthPrefix[3] = byte(largeFrameSize)

	// 喂入长度前缀
	frames, err := decoder.Feed(lengthPrefix)
	assert.NoError(t, err)
	assert.Empty(t, frames, "长度前缀不应产生完整帧")

	// 现在缓冲区中有 4 字节，decoder 期望接收 largeFrameSize 字节的帧数据
	// 我们持续喂入小块数据，累积直到超过 MaxBufferSize

	chunkSize := 10 * 1024 // 每次喂入 10KB
	chunk := make([]byte, chunkSize)

	// 喂入数据直到接近限制
	for {
		currentSize := decoder.BufferedSize()
		if currentSize+chunkSize > MaxBufferSize {
			// 下一次喂入会触发溢出
			break
		}
		frames, err = decoder.Feed(chunk)
		assert.NoError(t, err)
		assert.Empty(t, frames, "不完整的帧不应产生输出")
	}

	// 当前缓冲区已接近 MaxBufferSize
	// 再喂入一块数据应该触发溢出错误
	remaining := MaxBufferSize - decoder.BufferedSize() + 1
	overflowChunk := make([]byte, remaining)
	frames, err = decoder.Feed(overflowChunk)
	assert.Error(t, err, "应该触发缓冲区溢出错误")
	assert.Nil(t, frames)
	assert.ErrorIs(t, err, types.ErrFrameTooLarge)
}

// TestTCPFrameStreamDecoder_Feed_Timeout 测试超时保护（慢速攻击）
func TestTCPFrameStreamDecoder_Feed_Timeout(t *testing.T) {
	decoder := NewTCPFrameStreamDecoder()
	decoder.timeout = 100 * time.Millisecond

	frame := createTestCodecFrame(types.MessageTypeGet, []byte("test"))
	encoded, _ := NewTCPFrameCodec().EncodeFrame(frame)

	// 第一次喂入
	_, err := decoder.Feed(encoded)
	assert.NoError(t, err)

	// 等待超时
	time.Sleep(150 * time.Millisecond)

	// 再次喂入应该触发超时错误
	_, err = decoder.Feed(encoded)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "超时")
}

// TestTCPFrameStreamDecoder_Reset 测试重置解码器
func TestTCPFrameStreamDecoder_Reset(t *testing.T) {
	decoder := NewTCPFrameStreamDecoder()
	tcpCodec := NewTCPFrameCodec()

	// 喂入不完整帧
	frame := createTestCodecFrame(types.MessageTypeGet, []byte("test"))
	encoded, _ := tcpCodec.EncodeFrame(frame)
	partialData := encoded[:len(encoded)/2]

	_, _ = decoder.Feed(partialData)
	assert.Greater(t, decoder.BufferedSize(), 0)

	// 重置
	decoder.Reset()
	assert.Equal(t, 0, decoder.BufferedSize())
}

// TestTCPFrameStreamDecoder_BufferedSize 测试缓冲区大小
func TestTCPFrameStreamDecoder_BufferedSize(t *testing.T) {
	decoder := NewTCPFrameStreamDecoder()
	tcpCodec := NewTCPFrameCodec()

	// 初始缓冲区为空
	assert.Equal(t, 0, decoder.BufferedSize())

	// 喂入不完整帧
	frame := createTestCodecFrame(types.MessageTypeGet, []byte("test"))
	encoded, _ := tcpCodec.EncodeFrame(frame)
	partialData := encoded[:len(encoded)/2]

	_, _ = decoder.Feed(partialData)
	assert.Greater(t, decoder.BufferedSize(), 0)

	// 喂入剩余部分，缓冲区应清空
	remainingData := encoded[len(encoded)/2:]
	_, _ = decoder.Feed(remainingData)
	assert.Equal(t, 0, decoder.BufferedSize())
}

// ========================================
// 集成测试
// ========================================

// TestFrameCodec_Integration_TCPStream 测试 TCP 流式解码集成场景
func TestFrameCodec_Integration_TCPStream(t *testing.T) {
	decoder := NewTCPFrameStreamDecoder()
	tcpCodec := NewTCPFrameCodec()

	// 场景：模拟真实的 TCP 流，可能包含粘包、不完整帧
	frames := []*Frame{
		createTestCodecFrame(types.MessageTypeGet, []byte("req 1")),
		createTestCodecFrame(types.MessageTypePut, []byte("req 2")),
		createTestCodecFrame(types.MessageTypeDelete, []byte("req 3")),
	}

	var streamData []byte
	for _, f := range frames {
		data, _ := tcpCodec.EncodeFrame(f)
		streamData = append(streamData, data...)
	}

	// 模拟分段接收
	chunkSize := 20
	var receivedFrames []*Frame

	for i := 0; i < len(streamData); i += chunkSize {
		end := i + chunkSize
		if end > len(streamData) {
			end = len(streamData)
		}
		chunk := streamData[i:end]

		fs, err := decoder.Feed(chunk)
		assert.NoError(t, err)
		receivedFrames = append(receivedFrames, fs...)
	}

	// 验证所有帧都被接收
	assert.Len(t, receivedFrames, len(frames))
	for i, frame := range receivedFrames {
		assert.Equal(t, frames[i].FixedHeader.MsgType, frame.FixedHeader.MsgType)
		assert.Equal(t, frames[i].Data, frame.Data)
	}
}

// TestFrameCodec_Integration_AutoDetect 测试自动检测集成
func TestFrameCodec_Integration_AutoDetect(t *testing.T) {
	autoCodec := NewAutoDetectCodec()
	tcpCodec := NewTCPFrameCodec()
	udpCodec := NewUDPFrameCodec()

	// TCP 帧
	tcpFrame := createTestCodecFrame(types.MessageTypeGet, []byte("tcp data"))
	tcpData, _ := tcpCodec.EncodeFrame(tcpFrame)

	detected, _ := autoCodec.DetectProtocol(tcpData)
	decoded, _ := detected.DecodeFrame(tcpData)
	assert.Equal(t, tcpFrame.FixedHeader.MsgType, decoded.FixedHeader.MsgType)

	// UDP 帧
	udpFrame := createTestCodecFrame(types.MessageTypePut, []byte("udp data"))
	udpData, _ := udpCodec.EncodeFrame(udpFrame)

	detected, _ = autoCodec.DetectProtocol(udpData)
	decoded, _ = detected.DecodeFrame(udpData)
	assert.Equal(t, udpFrame.FixedHeader.MsgType, decoded.FixedHeader.MsgType)
}

// TestFrameCodec_EdgeCases 测试边界情况
func TestFrameCodec_EdgeCases(t *testing.T) {
	t.Run("空数据帧", func(t *testing.T) {
		codec := NewTCPFrameCodec()
		frame := createTestCodecFrame(types.MessageTypeGet, nil)

		encoded, err := codec.EncodeFrame(frame)
		assert.NoError(t, err)

		decoded, err := codec.DecodeFrame(encoded)
		assert.NoError(t, err)
		assert.Nil(t, decoded.Data)
	})

	t.Run("最大大小帧", func(t *testing.T) {
		maxSize := 1000
		codec := &TCPFrameCodec{MaxFrameSize: maxSize}

		data := make([]byte, maxSize-FixedHeaderLen-CRCLen-10)
		frame := createTestCodecFrame(types.MessageTypePut, data)

		encoded, err := codec.EncodeFrame(frame)
		assert.NoError(t, err)

		decoded, err := codec.DecodeFrame(encoded)
		assert.NoError(t, err)
		assert.Len(t, decoded.Data, len(data))
	})

	t.Run("零值帧", func(t *testing.T) {
		codec := NewTCPFrameCodec()
		frame := NewFrame(0, 0, types.MessageTypeGet, 1, 0, nil)

		encoded, err := codec.EncodeFrame(frame)
		assert.NoError(t, err)

		decoded, err := codec.DecodeFrame(encoded)
		assert.NoError(t, err)
		assert.Equal(t, uint8(1), decoded.FixedHeader.Version)
	})
}
