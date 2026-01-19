// Package types 定义内部通用的数据类型
//
// 避免各层之间的循环依赖，提供统一的类型定义
package types

// CodecType 编解码器类型
//
// 用于标识数据序列化/反序列化格式
// 应用场景：WAL 持久化、网络传输、快照存储
type CodecType uint16

const (
	// CodecTypeMessagePack MessagePack 编解码
	//
	// 特点：二进制格式，高效紧凑，支持丰富数据类型
	// 性能：编码/解码速度约为 JSON 的 2-3 倍
	// 数据大小：约为 JSON 的 30-50%
	CodecTypeMessagePack CodecType = 1

	// CodecTypeJSON JSON 编解码
	//
	// 特点：文本格式，可读性好，支持跨语言
	// 性能：编码/解码速度相对较慢
	// 数据大小：约为 MessagePack 的 2-3 倍
	CodecTypeJSON CodecType = 2

	// CodecTypeProtobuf Protobuf 编解码（预留）
	//
	// 特点：二进制格式，极致性能，强类型 Schema
	// 性能：编码/解码速度约为 JSON 的 3-5 倍
	// 数据大小：约为 JSON 的 40-60%
	// 状态：PR-002 实现
	CodecTypeProtobuf CodecType = 3
)

// String 返回 CodecType 的字符串表示
func (c CodecType) String() string {
	switch c {
	case CodecTypeMessagePack:
		return "msgpack"
	case CodecTypeJSON:
		return "json"
	case CodecTypeProtobuf:
		return "protobuf"
	default:
		return "unknown"
	}
}

// Validate 验证 CodecType 是否有效
func (c CodecType) Validate() error {
	switch c {
	case CodecTypeMessagePack, CodecTypeJSON, CodecTypeProtobuf:
		return nil
	default:
		return NewStoreInvalidParameterError("CodecType")
	}
}

// Codec 基础编解码器接口（可选，未来扩展）
//
// 定义统一的序列化抽象，用于 types 包内部使用
type Codec interface {
	// Encode 编码数据
	Encode(v any) ([]byte, error)

	// Decode 解码数据
	Decode(data []byte, v any) error

	// Type 返回编解码器类型
	Type() CodecType

	// Name 返回编解码器名称
	Name() string
}
