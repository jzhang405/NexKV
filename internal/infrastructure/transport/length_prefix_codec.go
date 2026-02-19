package transport

import (
	"encoding/binary"
	"io"

	"github.com/jzhang405/NexKV/pkg/errors"
)

// 常量定义
const (
	// DefaultBufferSize 默认缓冲区大小 (4KB)
	DefaultBufferSize = 4096
	// MaxMessageSize 最大消息大小 (1MB)
	MaxMessageSize = 1024 * 1024
	// LengthPrefixSize 长度前缀大小 (4字节)
	LengthPrefixSize = 4
)

// LengthPrefixedCodec 长度前缀编解码器（解决 TCP 粘包问题 + DoS 防护）
type LengthPrefixedCodec struct{}

// Encode 编码消息：[4字节长度][消息内容]
// 确保完整写入，处理部分写入情况
func (c *LengthPrefixedCodec) Encode(w io.Writer, data []byte) error {
	// 1. 检查消息大小
	if len(data) > MaxMessageSize {
		return errors.Wrapf(errors.ErrMessageTooLarge, "size=%d, max=%d", len(data), MaxMessageSize)
	}

	// 2. 写入长度前缀（4字节，大端序）- 确保完整写入
	length := uint32(len(data))
	lengthBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBuf, length)

	if _, err := writeFull(w, lengthBuf); err != nil {
		return errors.Wrap(err, "write length prefix")
	}

	// 3. 写入消息内容 - 确保完整写入
	if _, err := writeFull(w, data); err != nil {
		return errors.Wrap(err, "write message body")
	}

	return nil
}

// Decode 解码消息
// 先检查长度再分配内存，防止 DoS 攻击
func (c *LengthPrefixedCodec) Decode(r io.Reader) ([]byte, error) {
	// 1. 读取长度前缀（4字节固定大小）
	var lengthBuf [4]byte
	if _, err := io.ReadFull(r, lengthBuf[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lengthBuf[:])

	// 2. 先检查长度合法性，再分配内存（防止 DoS 攻击）
	if length > MaxMessageSize {
		return nil, errors.Wrapf(errors.ErrMessageTooLarge, "length=%d, max=%d", length, MaxMessageSize)
	}
	if length == 0 {
		return nil, errors.Wrap(errors.ErrInvalidMessage, "zero length message")
	}

	// 3. 现在安全地分配内存
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	return data, nil
}

// writeFull 确保完整写入（处理部分写入情况）
func writeFull(w io.Writer, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := w.Write(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
