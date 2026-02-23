// Package model 定义领域模型
package model

import "io"

// ============================================================================
// Codec 接口定义
// ============================================================================

// Codec 消息编解码接口
type Codec interface {
	// Encode 编码消息为字节切片
	Encode(msg Message) ([]byte, error)

	// Decode 解码字节切片为消息
	Decode(data []byte) (Message, error)

	// Name 返回编解码器名称（如 "msgpack"）
	Name() string

	// Version 返回编解码器版本（如 "v1"），用于协议协商
	Version() string
}

// StreamCodec 流式编解码接口（支持分帧）
type StreamCodec interface {
	Codec

	// EncodeToWriter 编码并写入 Writer
	EncodeToWriter(w io.Writer, msg Message) error

	// DecodeFromReader 从 Reader 解码
	DecodeFromReader(r io.Reader) (Message, error)
}
