// Package transport 实现传输层基础设施
package transport

import (
	"bytes"
	"encoding/binary"
	"io"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/vmihailenco/msgpack/v5"
)

// messageWire 用于 MessagePack 序列化的中间结构
// 由于 model.BaseMessage 使用私有字段，需要通过此结构进行传输
type messageWire struct {
	ID      string            `msgpack:"id"`
	Type    model.MessageType `msgpack:"type"`
	Source  model.PeerID      `msgpack:"source"`
	Target  model.PeerID      `msgpack:"target"`
	Payload []byte            `msgpack:"payload"`
	Ext     map[string]any    `msgpack:"ext,omitempty"`
}

// MessagePackCodec MessagePack 编解码器实现
type MessagePackCodec struct {
	version string
}

// NewMessagePackCodec 创建 MessagePack 编解码器
func NewMessagePackCodec() *MessagePackCodec {
	return &MessagePackCodec{
		version: "v1",
	}
}

// Encode 编码消息为字节切片
func (c *MessagePackCodec) Encode(msg model.Message) ([]byte, error) {
	if msg == nil {
		return nil, service.ErrCodecFailure
	}

	// 转换为传输结构
	wire := &messageWire{
		ID:      msg.ID(),
		Type:    msg.Type(),
		Source:  msg.Source(),
		Target:  msg.Target(),
		Payload: msg.Payload(),
	}

	// 序列化扩展字段
	if msg.Exts() != nil {
		wire.Ext = msg.Exts().All()
	}

	data, err := msgpack.Marshal(wire)
	if err != nil {
		return nil, service.Wrapf(service.ErrCodecFailure, "%v", err)
	}

	return data, nil
}

// Decode 解码字节切片为消息
func (c *MessagePackCodec) Decode(data []byte) (model.Message, error) {
	if len(data) == 0 {
		return nil, service.ErrCodecFailure
	}

	var wire messageWire
	if err := msgpack.Unmarshal(data, &wire); err != nil {
		return nil, service.Wrapf(service.ErrCodecFailure, "%v", err)
	}

	// P1-2 修复：字段验证
	if wire.ID == "" {
		return nil, service.Wrap(service.ErrCodecFailure, "missing message ID")
	}
	// MessageType 是 int 类型，检查是否在有效范围内
	if wire.Type < model.MessageTypeRequest || wire.Type > model.MessageTypeEvent {
		return nil, service.Wrapf(service.ErrCodecFailure, "invalid message type: %d", wire.Type)
	}
	// Source 和 Target 可以为空（用于广播场景）
	// 但需要验证格式是否有效
	if len(wire.Source) > 256 {
		return nil, service.Wrap(service.ErrCodecFailure, "source too long")
	}
	if len(wire.Target) > 256 {
		return nil, service.Wrap(service.ErrCodecFailure, "target too long")
	}

	// 重建 BaseMessage
	msg := model.NewMessage(wire.ID, wire.Type, wire.Source, wire.Target, wire.Payload)

	// 恢复扩展字段
	if wire.Ext != nil {
		for k, v := range wire.Ext {
			msg.Exts().Set(k, v)
		}
	}

	return msg, nil
}

// Name 返回编解码器名称
func (c *MessagePackCodec) Name() string {
	return "msgpack"
}

// Version 返回编解码器版本
func (c *MessagePackCodec) Version() string {
	return c.version
}

// ============================================================================
// StreamCodec 实现
// ============================================================================

// MessagePackStreamCodecHardLimit 硬限制（100MB）
const MessagePackStreamCodecHardLimit = 100 * 1024 * 1024

// MessagePackStreamCodec 流式 MessagePack 编解码器
//
// 消息格式：
// - 4 字节：消息长度（大端序）
// - N 字节：MessagePack 编码的消息内容
type MessagePackStreamCodec struct {
	*MessagePackCodec
	maxMessageSize uint32 // P1 修复：可配置的消息大小限制
}

// NewMessagePackStreamCodec 创建流式 MessagePack 编解码器（使用默认 10MB 限制）
func NewMessagePackStreamCodec() *MessagePackStreamCodec {
	return &MessagePackStreamCodec{
		MessagePackCodec: NewMessagePackCodec(),
		maxMessageSize:   DefaultMaxMessageSize,
	}
}

// NewMessagePackStreamCodecWithLimit 创建带自定义大小限制的流式编解码器
// maxSize: 最大消息大小（字节），<= 0 使用默认值 10MB，超过 100MB 使用 100MB
func NewMessagePackStreamCodecWithLimit(maxSize int) *MessagePackStreamCodec {
	if maxSize <= 0 {
		maxSize = DefaultMaxMessageSize
	}
	if maxSize > MessagePackStreamCodecHardLimit {
		maxSize = MessagePackStreamCodecHardLimit
	}

	return &MessagePackStreamCodec{
		MessagePackCodec: NewMessagePackCodec(),
		maxMessageSize:   uint32(maxSize),
	}
}

// MaxMessageSize 返回最大消息大小限制
func (c *MessagePackStreamCodec) MaxMessageSize() uint32 {
	return c.maxMessageSize
}

// EncodeToWriter 编码并写入 Writer
// 格式：[4字节长度][MessagePack数据]
func (c *MessagePackStreamCodec) EncodeToWriter(w io.Writer, msg model.Message) error {
	if msg == nil {
		return service.ErrCodecFailure
	}

	// 1. 编码消息
	data, err := c.Encode(msg)
	if err != nil {
		return err
	}

	// 2. 写入长度前缀（大端序）
	length := uint32(len(data))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return service.Wrapf(service.ErrCodecFailure, "failed to write length: %v", err)
	}

	// 3. 写入消息数据（循环确保完全写入）
	var written int
	for written < len(data) {
		n, err := w.Write(data[written:])
		if err != nil {
			return service.Wrapf(service.ErrCodecFailure, "failed to write data: %v", err)
		}
		written += n
	}

	return nil
}

// DecodeFromReader 从 Reader 解码
// 格式：[4字节长度][MessagePack数据]
func (c *MessagePackStreamCodec) DecodeFromReader(r io.Reader) (model.Message, error) {
	// 1. 读取长度前缀（大端序）
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, service.Wrapf(service.ErrCodecFailure, "failed to read length: %v", err)
	}

	// 2. 检查消息大小（使用可配置的限制）
	if length > c.maxMessageSize {
		return nil, service.Wrapf(service.ErrMessageTooLarge, "message too large (%d bytes, limit %d bytes)",
			length, c.maxMessageSize)
	}

	// 3. 读取消息数据
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, service.Wrapf(service.ErrCodecFailure, "failed to read data: %v", err)
	}

	// 4. 解码消息
	return c.Decode(data)
}

// ============================================================================
// 辅助函数
// ============================================================================

// EncodeToBuffer 编码消息到 buffer（用于测试）
func EncodeToBuffer(codec service.Codec, msg model.Message) (*bytes.Buffer, error) {
	data, err := codec.Encode(msg)
	if err != nil {
		return nil, err
	}
	return bytes.NewBuffer(data), nil
}
