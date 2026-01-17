// Package transport 自定义帧格式实现
//
// 帧格式:
// [Magic 4 bytes][Type 2 bytes][CodecType 2 bytes][Length 4 bytes][CRC32 4 bytes][Data N bytes]
//
// - Magic: "NxKV" (4 字节，魔数用于识别协议)
// - Type: 消息类型 (2 字节，MessageType uint16，大端序)
// - CodecType: 编解码器类型 (2 字节，CodecType uint16，大端序)
// - Length: Data 长度 (4 字节，uint32，大端序)
// - CRC32: 整个帧的校验和 (4 字节，uint32，大端序)
// - Data: 消息体 (N 字节)
package transport

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	// FrameMagic 帧魔数 "NxKV"
	FrameMagic = "NxKV"

	// FrameHeaderSize 帧头大小 (Magic + Type + CodecType + Length + CRC32)
	// 4 + 2 + 2 + 4 + 4 = 16 字节（凑整）
	FrameHeaderSize = 4 + 2 + 2 + 4 + 4

	// MaxFrameSize 最大帧大小 (100MB)
	MaxFrameSize = 100 * 1024 * 1024

	// MinFrameSize 最小帧大小
	MinFrameSize = FrameHeaderSize
)

var (
	// ErrInvalidMagic 魔数无效
	ErrInvalidMagic = errors.New("invalid frame magic")

	// ErrFrameTooLarge 帧过大
	ErrFrameTooLarge = errors.New("frame too large")

	// ErrChecksum 校验和错误
	ErrChecksum = errors.New("frame checksum mismatch")

	// ErrInvalidFrameSize 无效的帧大小
	ErrInvalidFrameSize = errors.New("invalid frame size")
)

// Frame 自定义帧结构
//
// 帧格式 (16字节帧头):
// ┌───────────────────────────────────────────────────────────────┐
// │ Magic (4) │ Type (2) │ CodecType (2) │ Length (4) │ CRC32 (4) │
// ├───────────────────────────────────────────────────────────────┤
// │ Data (N bytes)                                                │
// └───────────────────────────────────────────────────────────────┘
type Frame struct {
	// Magic 魔数 (固定为 "NxKV")
	Magic [4]byte

	// Type 消息类型
	Type MessageType

	// CodecType 编解码器类型
	CodecType CodecType

	// Length 数据长度
	Length uint32

	// CRC32 校验和（覆盖 Magic + Type + CodecType + Length + Data）
	CRC32 uint32

	// Data 帧数据
	Data []byte
}

// NewFrame 创建新帧
func NewFrame(msgType MessageType, codecType CodecType, data []byte) *Frame {
	// 计算 Data 长度
	length := uint32(len(data))

	// 构建帧头（不包含 Data 和 CRC32）
	header := make([]byte, 12) // Magic(4) + Type(2) + CodecType(2) + Length(4)
	copy(header[0:4], []byte(FrameMagic))
	binary.BigEndian.PutUint16(header[4:6], uint16(msgType))
	binary.BigEndian.PutUint16(header[6:8], uint16(codecType))
	binary.BigEndian.PutUint32(header[8:12], length)

	// 计算校验和 (Magic + Type + CodecType + Length + Data)
	crc := crc32.ChecksumIEEE(data)
	crc = crc32.Update(crc, crc32.IEEETable, header)
	calculatedCRC32 := crc

	return &Frame{
		Magic:     [4]byte{'N', 'x', 'K', 'V'},
		Type:      msgType,
		CodecType: codecType,
		Length:    length,
		CRC32:     calculatedCRC32,
		Data:      data,
	}
}

// Marshal 序列化帧为字节流
func (f *Frame) Marshal() ([]byte, error) {
	if f.Length > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}

	// 帧总大小 = Header(16) + Data
	totalSize := FrameHeaderSize + len(f.Data)
	buf := make([]byte, totalSize)

	// 写入 Magic (0-3)
	copy(buf[0:4], f.Magic[:])

	// 写入 Type (4-5)
	binary.BigEndian.PutUint16(buf[4:6], uint16(f.Type))

	// 写入 CodecType (6-7)
	binary.BigEndian.PutUint16(buf[6:8], uint16(f.CodecType))

	// 写入 Length (8-11)
	binary.BigEndian.PutUint32(buf[8:12], f.Length)

	// 写入 CRC32 (12-15)
	binary.BigEndian.PutUint32(buf[12:16], f.CRC32)

	// 写入 Data (16-)
	if len(f.Data) > 0 {
		copy(buf[16:], f.Data)
	}

	return buf, nil
}

// Unmarshal 从字节流解析帧
func (f *Frame) Unmarshal(data []byte) error {
	if len(data) < FrameHeaderSize {
		return ErrInvalidFrameSize
	}

	// 读取 Magic (0-3)
	copy(f.Magic[:], data[0:4])
	if string(f.Magic[:]) != FrameMagic {
		return ErrInvalidMagic
	}

	// 读取 Type (4-5)
	f.Type = MessageType(binary.BigEndian.Uint16(data[4:6]))

	// 读取 CodecType (6-7)
	f.CodecType = CodecType(binary.BigEndian.Uint16(data[6:8]))

	// 读取 Length (8-11)
	f.Length = binary.BigEndian.Uint32(data[8:12])

	// 验证帧大小
	if f.Length > MaxFrameSize {
		return ErrFrameTooLarge
	}

	totalSize := FrameHeaderSize + int(f.Length)
	if len(data) < totalSize {
		return ErrInvalidFrameSize
	}

	// 读取 CRC32 (12-15)
	f.CRC32 = binary.BigEndian.Uint32(data[12:16])

	// 读取 Data (16-)
	if f.Length > 0 {
		f.Data = make([]byte, f.Length)
		copy(f.Data, data[16:totalSize])
	} else {
		f.Data = nil
	}

	// 验证校验和
	if !f.verifyChecksum(data) {
		return ErrChecksum
	}

	return nil
}

// verifyChecksum 验证帧校验和
func (f *Frame) verifyChecksum(data []byte) bool {
	// 重新计算校验和：Magic(0:4) + Type(4:6) + CodecType(6:8) + Length(8:12) + Data(16:end)
	// 注意：不包括 CRC32 字段本身(12:16)
	crc := crc32.ChecksumIEEE(data[16:])                 // Data
	crc = crc32.Update(crc, crc32.IEEETable, data[0:12]) // Magic + Type + CodecType + Length
	calculated := crc

	return f.CRC32 == calculated || f.CRC32 == 0 // CRC32 为 0 表示未启用校验
}

// String 返回帧的字符串表示
func (f *Frame) String() string {
	return fmt.Sprintf("Frame{Type=%s, CodecType=%s, Length=%d, CRC32=%08x, DataSize=%d}",
		f.Type, f.CodecType, f.Length, f.CRC32, len(f.Data))
}

// HexDump 返回帧的十六进制转储
func (f *Frame) HexDump() string {
	data, _ := f.Marshal()
	return hex.Dump(data)
}

// FrameReader 帧读取器
//
// 用于从流中读取完整帧
type FrameReader struct {
	r io.Reader
}

// NewFrameReader 创建帧读取器
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{r: r}
}

// ReadFrame 读取一帧
func (fr *FrameReader) ReadFrame() (*Frame, error) {
	// 读取帧头 (16 字节)
	header := make([]byte, FrameHeaderSize)
	if _, err := io.ReadFull(fr.r, header); err != nil {
		return nil, err
	}

	// 解析 Magic
	if string(header[0:4]) != FrameMagic {
		return nil, ErrInvalidMagic
	}

	// 解析 Length (4 字节，从偏移 8 开始)
	length := binary.BigEndian.Uint32(header[8:12])
	if length > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}

	// 读取 Data 部分（帧头的 16 字节已包含 CRC32）
	data := make([]byte, length)
	if _, err := io.ReadFull(fr.r, data); err != nil {
		return nil, err
	}

	// 组装完整帧数据
	fullData := make([]byte, FrameHeaderSize+length)
	copy(fullData, header)
	copy(fullData[FrameHeaderSize:], data)

	// 解析帧
	frame := &Frame{}
	if err := frame.Unmarshal(fullData); err != nil {
		return nil, err
	}

	return frame, nil
}

// FrameWriter 帧写入器
//
// 用于向流中写入完整帧
type FrameWriter struct {
	w io.Writer
}

// NewFrameWriter 创建帧写入器
func NewFrameWriter(w io.Writer) *FrameWriter {
	return &FrameWriter{w: w}
}

// WriteFrame 写入一帧
func (fw *FrameWriter) WriteFrame(frame *Frame) error {
	data, err := frame.Marshal()
	if err != nil {
		return err
	}

	_, err = fw.w.Write(data)
	return err
}

// ReadFrame 从连接读取一帧
func ReadFrame(conn Conn) (*Frame, error) {
	reader := NewFrameReader(conn)
	return reader.ReadFrame()
}

// WriteFrame 向连接写入一帧
func WriteFrame(conn Conn, frame *Frame) error {
	writer := NewFrameWriter(conn)
	return writer.WriteFrame(frame)
}
