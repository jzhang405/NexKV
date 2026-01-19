// Package store WAL 编解码器实现
//
// 提供灵活的 WAL 条目序列化/反序列化能力
// 支持多种编解码器：MessagePack（默认）和 JSON（兼容性）
package store

import (
	"encoding/json"
	"fmt"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/vmihailenco/msgpack/v5"
)

// ========================================
// WALCodec 接口定义
// ========================================

// WALCodec WAL 编解码器接口
//
// 定义 WAL 条目的序列化/反序列化抽象
// 应用场景：WAL 持久化、快照存储、跨节点传输
type WALCodec interface {
	// Encode 编码 WAL 条目为字节数组
	Encode(entry *WALEntry) ([]byte, error)

	// Decode 从字节数组解码 WAL 条目
	Decode(data []byte) (*WALEntry, error)

	// Type 返回编解码器类型
	Type() types.CodecType

	// Name 返回编解码器名称
	Name() string
}

// ========================================
// WAL 条目可编码数据结构
// ========================================

// walEntryData WAL 条目的可编码数据结构
//
// 同时支持 JSON 和 MessagePack 编解码：
//   - JSON: 使用完整字段名，可读性好
//   - MessagePack: 使用简短字段名，节省空间
//
// 同一个结构体，同时添加 json 和 msgpack 两种标签
// 两种标签可独立定义字段名（也可保持一致），互不干扰
type walEntryData struct {
	Timestamp []byte `json:"timestamp" msgpack:"ts"`        // HLC 时间戳序列化数据
	Type      uint16 `json:"type" msgpack:"type"`           // 操作类型
	Key       string `json:"key" msgpack:"key"`             // 键
	Value     []byte `json:"value" msgpack:"value"`         // 值
	OldValue  []byte `json:"old_value" msgpack:"old_value"` // 旧值（不用 omitempty，确保空值也能正确编码）
	Checksum  uint32 `json:"checksum" msgpack:"checksum"`   // 校验和
}

// ========================================
// MessagePackWALCodec 实现
// ========================================

// MessagePackWALCodec MessagePack 编解码器
//
// 特点：
//   - 二进制格式，高效紧凑
//   - 性能：编码/解码速度约为 JSON 的 2-3 倍
//   - 数据大小：约为 JSON 的 30-50%
//   - 推荐用于生产环境
type MessagePackWALCodec struct{}

// NewMessagePackWALCodec 创建 MessagePack 编解码器
func NewMessagePackWALCodec() *MessagePackWALCodec {
	return &MessagePackWALCodec{}
}

// Encode 编码 WAL 条目（MessagePack 格式）
func (c *MessagePackWALCodec) Encode(entry *WALEntry) ([]byte, error) {
	if entry == nil {
		return nil, types.NewCodecInvalidMessageError("WAL 条目为空")
	}

	// 序列化 HLC 时间戳（如果为 nil，使用零值）
	var timestampBytes []byte
	if entry.Timestamp != nil {
		var err error
		timestampBytes, err = entry.Timestamp.MarshalBinary()
		if err != nil {
			return nil, types.NewCodecEncodeFailedError("Encode Timestamp", err)
		}
	} else {
		// 使用零值 HLC
		hlc := clock.NewHLC()
		var err error
		timestampBytes, err = hlc.MarshalBinary()
		if err != nil {
			return nil, types.NewCodecEncodeFailedError("Encode Timestamp", err)
		}
	}

	// 使用统一的中间结构体
	data := walEntryData{
		Timestamp: timestampBytes,
		Type:      uint16(entry.Type),
		Key:       entry.Key,
		Value:     entry.Value,
		OldValue:  entry.OldValue,
		Checksum:  entry.Checksum,
	}

	// 使用 MessagePack 编码（使用 msgpack 标签）
	encoded, err := msgpack.Marshal(data)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("MessagePack", err)
	}

	return encoded, nil
}

// Decode 解码 WAL 条目（MessagePack 格式）
func (c *MessagePackWALCodec) Decode(data []byte) (*WALEntry, error) {
	if len(data) == 0 {
		return nil, types.NewCodecInvalidDataError("Decode", "数据为空")
	}

	// 解码到统一的中间结构体
	var decoded walEntryData
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		return nil, types.NewCodecDecodeFailedError("MessagePack", err)
	}

	// 构建 WALEntry
	entry := &WALEntry{
		Type:     WALType(decoded.Type),
		Key:      decoded.Key,
		Value:    decoded.Value,
		OldValue: decoded.OldValue,
		Checksum: decoded.Checksum,
	}

	// 反序列化 HLC 时间戳
	entry.Timestamp = &clock.HLC{}
	if err := entry.Timestamp.UnmarshalBinary(decoded.Timestamp); err != nil {
		return nil, types.NewCodecDecodeFailedError("Timestamp", err)
	}

	return entry, nil
}

// Type 返回编解码器类型
func (c *MessagePackWALCodec) Type() types.CodecType {
	return types.CodecTypeMessagePack
}

// Name 返回编解码器名称
func (c *MessagePackWALCodec) Name() string {
	return "msgpack"
}

// ========================================
// JSONWALCodec 实现
// ========================================

// JSONWALCodec JSON 编解码器
//
// 特点：
//   - 文本格式，可读性好，支持跨语言
//   - 性能：编码/解码速度相对较慢
//   - 数据大小：约为 MessagePack 的 2-3 倍
//   - 推荐用于调试和跨语言兼容场景
type JSONWALCodec struct{}

// NewJSONWALCodec 创建 JSON 编解码器
func NewJSONWALCodec() *JSONWALCodec {
	return &JSONWALCodec{}
}

// Encode 编码 WAL 条目（JSON 格式）
func (c *JSONWALCodec) Encode(entry *WALEntry) ([]byte, error) {
	if entry == nil {
		return nil, types.NewCodecInvalidMessageError("WAL 条目为空")
	}

	// 序列化 HLC 时间戳（如果为 nil，使用零值）
	var timestampBytes []byte
	if entry.Timestamp != nil {
		var err error
		timestampBytes, err = entry.Timestamp.MarshalBinary()
		if err != nil {
			return nil, types.NewCodecEncodeFailedError("Encode Timestamp", err)
		}
	} else {
		// 使用零值 HLC
		hlc := clock.NewHLC()
		var err error
		timestampBytes, err = hlc.MarshalBinary()
		if err != nil {
			return nil, types.NewCodecEncodeFailedError("Encode Timestamp", err)
		}
	}

	// 使用统一的中间结构体
	data := walEntryData{
		Timestamp: timestampBytes,
		Type:      uint16(entry.Type),
		Key:       entry.Key,
		Value:     entry.Value,
		OldValue:  entry.OldValue,
		Checksum:  entry.Checksum,
	}

	// 使用 JSON 编码（使用 json 标签）
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("JSON", err)
	}

	return encoded, nil
}

// Decode 解码 WAL 条目（JSON 格式）
func (c *JSONWALCodec) Decode(data []byte) (*WALEntry, error) {
	if len(data) == 0 {
		return nil, types.NewCodecInvalidDataError("Decode", "数据为空")
	}

	// 解码到统一的中间结构体
	var decoded walEntryData
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, types.NewCodecDecodeFailedError("JSON", err)
	}

	// 构建 WALEntry
	entry := &WALEntry{
		Type:     WALType(decoded.Type),
		Key:      decoded.Key,
		Value:    decoded.Value,
		OldValue: decoded.OldValue,
		Checksum: decoded.Checksum,
	}

	// 反序列化 HLC 时间戳
	entry.Timestamp = &clock.HLC{}
	if err := entry.Timestamp.UnmarshalBinary(decoded.Timestamp); err != nil {
		return nil, types.NewCodecDecodeFailedError("Timestamp", err)
	}

	return entry, nil
}

// Type 返回编解码器类型
func (c *JSONWALCodec) Type() types.CodecType {
	return types.CodecTypeJSON
}

// Name 返回编解码器名称
func (c *JSONWALCodec) Name() string {
	return "json"
}

// ========================================
// 编解码器工厂函数
// ========================================

// NewWALCodec 根据类型创建编解码器
//
// 支持的类型：
//   - types.CodecTypeMessagePack: MessagePack 编解码器（默认）
//   - types.CodecTypeJSON: JSON 编解码器
func NewWALCodec(codecType types.CodecType) (WALCodec, error) {
	switch codecType {
	case types.CodecTypeMessagePack:
		return NewMessagePackWALCodec(), nil
	case types.CodecTypeJSON:
		return NewJSONWALCodec(), nil
	default:
		return nil, types.NewStoreInvalidParameterError(fmt.Sprintf("不支持的编解码器类型: %d", codecType))
	}
}
