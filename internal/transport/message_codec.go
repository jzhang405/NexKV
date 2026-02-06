// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package transport

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/vmihailenco/msgpack/v5"
)

// MessageCodec 消息编解码器接口
type MessageCodec interface {
	Encode(w io.Writer, msg *Message) error
	Decode(r io.Reader) (*Message, error)
}

// MessagePackCodec MessagePack 编解码实现
// 使用 TLV (Type-Length-Value) 格式封装 MessagePack 编码的消息体
type MessagePackCodec struct {
	seqGenerator *atomic.Uint64 // 消息序号生成器
}

// NewMessagePackCodec 创建 MessagePack 编解码器
func NewMessagePackCodec() *MessagePackCodec {
	seq := atomic.Uint64{}
	seq.Store(0)
	return &MessagePackCodec{
		seqGenerator: &seq,
	}
}

// Encode 编码消息（TLV 格式）
// 消息格式：
// +--------+--------+--------+--------+--------+
// | Type   | Length (2 bytes)           | Value (MessagePack) |
// +--------+--------+--------+--------+--------+
func (c *MessagePackCodec) Encode(w io.Writer, msg *Message) error {
	// 自动生成消息序号（如果未设置）
	// 注意：如果同一消息被多个 goroutine 并发编码，需要先克隆消息
	if msg.Seq == 0 {
		msg.Seq = c.seqGenerator.Add(1)
	}

	// 1. 使用 MessagePack 编码消息体
	msgData, err := msgpack.Marshal(msg)
	if err != nil {
		return fmt.Errorf("MessagePack 编码失败: %w", err)
	}

	// 2. 写入消息类型
	if err := binary.Write(w, binary.BigEndian, msg.Type); err != nil {
		return fmt.Errorf("写入消息类型失败: %w", err)
	}

	// 3. 写入长度（2 字节，大端序）
	length := uint16(len(msgData))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("写入长度失败: %w", err)
	}

	// 4. 写入消息体
	if _, err := w.Write(msgData); err != nil {
		return fmt.Errorf("写入消息体失败: %w", err)
	}

	return nil
}

// Decode 解码消息
func (c *MessagePackCodec) Decode(r io.Reader) (*Message, error) {
	// 1. 读取消息类型
	var msgType MessageType
	if err := binary.Read(r, binary.BigEndian, &msgType); err != nil {
		return nil, fmt.Errorf("读取消息类型失败: %w", err)
	}

	// 2. 读取长度
	var length uint16
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("读取长度失败: %w", err)
	}

	// 验证长度（防止过大的消息）
	const maxMessageSize = uint16(10 * 1024) // 最大 10KB（可根据需要调整）
	if length > maxMessageSize {
		return nil, fmt.Errorf("消息过大: %d 字节（最大 %d 字节）", length, maxMessageSize)
	}

	// 3. 读取消息体
	msgData := make([]byte, length)
	if _, err := io.ReadFull(r, msgData); err != nil {
		return nil, fmt.Errorf("读取消息体失败: %w", err)
	}

	// 4. 解码消息（MessagePack）
	var msg Message
	if err := msgpack.Unmarshal(msgData, &msg); err != nil {
		return nil, fmt.Errorf("MessagePack 解码失败: %w", err)
	}

	// 确保消息类型正确
	msg.Type = msgType

	return &msg, nil
}

// EncodeToBytes 编码消息为字节切片（便捷方法）
func (c *MessagePackCodec) EncodeToBytes(msg *Message) ([]byte, error) {
	// 预分配缓冲区
	buf := make([]byte, 0, msg.Size()+3) // +3 for Type (1) + Length (2)

	// 使用内存缓冲区编码
	bufWriter := newByteSliceWriter(&buf)
	if err := c.Encode(bufWriter, msg); err != nil {
		return nil, err
	}

	return buf, nil
}

// DecodeFromBytes 从字节切片解码消息（便捷方法）
func (c *MessagePackCodec) DecodeFromBytes(data []byte) (*Message, error) {
	if len(data) < 3 {
		return nil, fmt.Errorf("数据过短: %d 字节", len(data))
	}

	bufReader := newByteSliceReader(data)
	return c.Decode(bufReader)
}

// byteSliceWriter 用于写入字节切片的 io.Writer 实现
type byteSliceWriter struct {
	buf *[]byte
}

func newByteSliceWriter(buf *[]byte) *byteSliceWriter {
	return &byteSliceWriter{buf: buf}
}

func (w *byteSliceWriter) Write(p []byte) (n int, err error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

// byteSliceReader 用于读取字节切片的 io.Reader 实现
type byteSliceReader struct {
	data []byte
	pos  int
}

func newByteSliceReader(data []byte) *byteSliceReader {
	return &byteSliceReader{data: data, pos: 0}
}

func (r *byteSliceReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// ResetSeqGenerator 重置序号生成器（主要用于测试）
func (c *MessagePackCodec) ResetSeqGenerator() {
	c.seqGenerator.Store(0)
}

// GetNextSeq 获取下一个消息序号（不递增）
func (c *MessagePackCodec) GetNextSeq() uint64 {
	return c.seqGenerator.Load() + 1
}
