// Package transport 帧编解码器
//
// 实现 TCP 和 UDP 的帧编解码统一：
//   - TCP：4 字节长度前缀 + FixedHeader + VarExtHeader + Data + CRC32（处理粘包）
//   - UDP：FixedHeader + VarExtHeader + Data + CRC32（无长度前缀）
//
// 注意：此模块使用现有的 Frame 结构，与 codec.go 兼容
package transport

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

const (
	// FrameMagic 魔法数字（"NEXK" 的 ASCII 码）
	FrameMagic uint32 = 0x4E45584B // N=0x4E, E=0x45, X=0x58, K=0x4B

	// MaxBufferSize 最大缓冲区大小（防止 DoS 攻击）
	MaxBufferSize int = 10 * 1024 * 1024 // 10MB

	// MaxSingleFeedSize 单次 Feed 最大数据大小（防止慢速攻击）
	MaxSingleFeedSize int = 64 * 1024 // 64KB
)

// FrameCodec 帧编解码器接口
//
// 定义统一的帧编解码接口
type FrameCodec interface {
	// EncodeFrame 编码帧
	EncodeFrame(frame *Frame) ([]byte, error)

	// DecodeFrame 解码帧
	DecodeFrame(data []byte) (*Frame, error)

	// EstimateSize 估计编码后大小
	EstimateSize(frame *Frame) int
}

// TCPFrameCodec TCP 帧编解码器
//
// 帧格式：
//
//	+--------+--------+------------+-------+------------+
//	| Length | Fixed  | VarExt     | Data  | Checksum   |
//	+--------+--------+------------+-------+------------+
//	| 4 bytes | Header | Header    | Msg   | 4 bytes    |
//	+--------+--------+------------+-------+------------+
//
//	- Length: 帧总长度（不包括 Length 字段本身）
//	- FixedHeader: 固定头（31 字节）
//	- VarExtHeader: 变长 TLV 扩展头
//	- Data: 消息体
//	- Checksum: CRC32 校验和（VarExtHeader + Data）
type TCPFrameCodec struct {
	// MaxFrameSize 最大帧大小
	MaxFrameSize int
}

// NewTCPFrameCodec 创建 TCP 帧编解码器
func NewTCPFrameCodec() *TCPFrameCodec {
	return &TCPFrameCodec{
		MaxFrameSize: 1024 * 1024 * 100, // 100MB
	}
}

// EncodeFrame 编码 TCP 帧
func (c *TCPFrameCodec) EncodeFrame(frame *Frame) ([]byte, error) {
	data, err := frame.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal frame: %w", err)
	}

	if err := checkFrameSize(len(data), c.MaxFrameSize); err != nil {
		return nil, err
	}

	buf := new(bytes.Buffer)
	buf.Grow(4 + len(data))

	// 写入长度前缀（大端序）
	if err := binary.Write(buf, binary.BigEndian, uint32(len(data))); err != nil {
		return nil, fmt.Errorf("write length: %w", err)
	}

	if _, err := buf.Write(data); err != nil {
		return nil, fmt.Errorf("write frame data: %w", err)
	}

	return buf.Bytes(), nil
}

// DecodeFrame 解码 TCP 帧
func (c *TCPFrameCodec) DecodeFrame(data []byte) (*Frame, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("%w: data too short (%d < 4)", types.ErrInvalidFrameFormat, len(data))
	}

	buf := bytes.NewReader(data)
	var frameSize uint32
	if err := binary.Read(buf, binary.BigEndian, &frameSize); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}

	if err := checkFrameSize(int(frameSize), c.MaxFrameSize); err != nil {
		return nil, err
	}

	if int(frameSize) > len(data)-4 {
		return nil, fmt.Errorf("%w: incomplete frame (%d > %d)",
			types.ErrInvalidFrameFormat, frameSize, len(data)-4)
	}

	frameData := make([]byte, frameSize)
	if _, err := io.ReadFull(buf, frameData); err != nil {
		return nil, fmt.Errorf("read frame data: %w", err)
	}

	frame := &Frame{}
	if err := frame.Unmarshal(frameData); err != nil {
		return nil, fmt.Errorf("unmarshal frame: %w", err)
	}

	return frame, nil
}

// EstimateSize 估计编码后大小
func (c *TCPFrameCodec) EstimateSize(frame *Frame) int {
	// 长度前缀：4 字节
	size := 4

	// 帧数据
	size += frame.Size()

	return size
}

// UDPFrameCodec UDP 帧编解码器
//
// 帧格式：
//
//	+--------+------------+-------+------------+
//	| Fixed  | VarExt     | Data  | Checksum   |
//	+--------+------------+-------+------------+
//	| Header | Header     | Msg   | 4 bytes    |
//	+--------+------------+-------+------------+
//
//	- FixedHeader: 固定头（31 字节）
//	- VarExtHeader: 变长 TLV 扩展头
//	- Data: 消息体
//	- Checksum: CRC32 校验和（VarExtHeader + Data）
//	- 无长度前缀（UDP 保证消息边界）
type UDPFrameCodec struct {
	// MaxFrameSize 最大帧大小
	MaxFrameSize int
}

// NewUDPFrameCodec 创建 UDP 帧编解码器
func NewUDPFrameCodec() *UDPFrameCodec {
	return &UDPFrameCodec{
		MaxFrameSize: 1024 * 64, // UDP 限制：64KB（安全考虑）
	}
}

// EncodeFrame 编码 UDP 帧
func (c *UDPFrameCodec) EncodeFrame(frame *Frame) ([]byte, error) {
	data, err := frame.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal frame: %w", err)
	}

	if err := checkFrameSize(len(data), c.MaxFrameSize); err != nil {
		return nil, err
	}

	return data, nil
}

// DecodeFrame 解码 UDP 帧
func (c *UDPFrameCodec) DecodeFrame(data []byte) (*Frame, error) {
	if err := checkFrameSize(len(data), c.MaxFrameSize); err != nil {
		return nil, err
	}

	if len(data) < FixedHeaderLen+CRCLen {
		return nil, fmt.Errorf("%w: data too short (%d < %d)", types.ErrInvalidFrameFormat, len(data), FixedHeaderLen+CRCLen)
	}

	frame := &Frame{}
	if err := frame.Unmarshal(data); err != nil {
		return nil, fmt.Errorf("unmarshal frame: %w", err)
	}

	return frame, nil
}

// EstimateSize 估计编码后大小
func (c *UDPFrameCodec) EstimateSize(frame *Frame) int {
	return frame.Size()
}

// checkFrameSize 检查帧大小是否合法
func checkFrameSize(size int, maxSize int) error {
	if size > maxSize {
		return fmt.Errorf("%w: %d > %d", types.ErrFrameTooLarge, size, maxSize)
	}
	return nil
}

// AutoDetectCodec 自动检测编解码器
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
// 使用严格的检测逻辑区分 TCP 和 UDP 帧
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
			// 长度前缀读取成功
			// 验证长度值的合理性：
			// 1. 长度后的剩余数据应该匹配帧大小
			// 2. 帧大小应该包含至少 FixedHeader + CRC
			remainingData := len(data) - 4 // 减去长度前缀

			// TCP 帧判断条件：
			// - 帧大小等于剩余数据（完整帧）
			// - 帧大小小于等于剩余数据（粘包情况）
			// - 帧大小至少包含 FixedHeader + CRC
			minFrameSize := FixedHeaderLen + CRCLen
			if int(frameSize) >= minFrameSize && int(frameSize) <= remainingData {
				// 进一步验证：尝试读取 FixedHeader 的某些字段
				// 检查版本号是否合理（假设版本号在 1-255 之间）
				if remainingData >= 4 {
					// FixedHeader 前 4 字节：Magic(4) 或 Version(1) + 其他字段
					// 这里我们假设 FixedHeader 的第一个字节是版本号
					// 如果版本号在合理范围内（非零），认为是有效的 TCP 帧
					versionByte := data[4] // 跳过 4 字节长度前缀
					if versionByte >= 1 {
						// 可能是 TCP 帧
						return c.tcpCodec, nil
					}
				}
			}
		}
	}

	// 默认使用 UDP 编解码器
	return c.udpCodec, nil
}

// TCPFrameStreamDecoder TCP 帧流式解码器
//
// 处理 TCP 粘包问题，从流中正确提取帧
type TCPFrameStreamDecoder struct {
	// Codec 底层编解码器
	codec *TCPFrameCodec

	// buffer 缓冲区（处理粘包）
	buffer   []byte
	bufferMu sync.Mutex

	// 超时控制（防止慢速攻击）
	lastFeed time.Time     // 最后一次 Feed 时间
	timeout  time.Duration // 连接超时时间
}

// NewTCPFrameStreamDecoder 创建 TCP 帧流式解码器
func NewTCPFrameStreamDecoder() *TCPFrameStreamDecoder {
	return &TCPFrameStreamDecoder{
		codec: NewTCPFrameCodec(),
	}
}

// Feed 喂入数据
//
// 参数:
//   - data: 接收到的数据
//
// 返回:
//   - frames: 解码出的完整帧列表
//   - err: 解码错误
func (d *TCPFrameStreamDecoder) Feed(data []byte) (frames []*Frame, err error) {
	d.bufferMu.Lock()
	defer d.bufferMu.Unlock()

	// 1. 检查单次 Feed 大小（防止慢速攻击）
	if len(data) > MaxSingleFeedSize {
		return nil, fmt.Errorf("单次 Feed 数据过大 (%d > %d): %w",
			len(data), MaxSingleFeedSize, types.ErrFrameTooLarge)
	}

	// 2. 检查连接超时（防止慢速攻击）
	if d.timeout > 0 && !d.lastFeed.IsZero() {
		if time.Since(d.lastFeed) > d.timeout {
			d.buffer = nil // 超时后清空缓冲区
			return nil, fmt.Errorf("连接超时 (%v)", d.timeout)
		}
	}
	d.lastFeed = time.Now()

	// 3. 检查缓冲区大小限制（防止 DoS 攻击）
	newBufferSize := len(d.buffer) + len(data)
	if newBufferSize > MaxBufferSize {
		return nil, fmt.Errorf("缓冲区大小超过限制 (%d > %d): %w",
			newBufferSize, MaxBufferSize, types.ErrFrameTooLarge)
	}

	// 4. 追加数据到缓冲区
	d.buffer = append(d.buffer, data...)

	// 5. 循环解码帧
	for len(d.buffer) >= 4 {
		// 6. 读取帧长度
		buf := bytes.NewReader(d.buffer)
		var frameSize uint32
		if err := binary.Read(buf, binary.BigEndian, &frameSize); err != nil {
			// 解析失败，清空缓冲区
			d.buffer = nil
			return nil, fmt.Errorf("parse frame size: %w", err)
		}

		// 7. 检查帧是否完整
		totalFrameSize := int(frameSize) + 4 // +4 for length field
		if len(d.buffer) < totalFrameSize {
			// 帧不完整，等待更多数据
			break
		}

		// 8. 提取完整帧
		frameData := d.buffer[:totalFrameSize]

		// 9. 解码帧（使用 TCP 编解码器）
		frame, err := d.codec.DecodeFrame(frameData)
		if err != nil {
			// 解码失败，清空缓冲区
			d.buffer = nil
			return nil, fmt.Errorf("decode frame: %w", err)
		}

		frames = append(frames, frame)

		// 10. 从缓冲区移除已处理的帧
		d.buffer = d.buffer[totalFrameSize:]
	}

	return frames, nil
}

// Reset 重置解码器
func (d *TCPFrameStreamDecoder) Reset() {
	d.bufferMu.Lock()
	defer d.bufferMu.Unlock()

	d.buffer = nil
}

// BufferedSize 返回缓冲区大小
func (d *TCPFrameStreamDecoder) BufferedSize() int {
	d.bufferMu.Lock()
	defer d.bufferMu.Unlock()

	return len(d.buffer)
}
