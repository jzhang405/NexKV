// Package store WAL Protobuf 编解码器实现
//
// 提供 WAL 条目的 Protobuf 序列化/反序列化能力
// 特点：
//   - protoc 预编译，性能最优
//   - 数据大小最小，节省存储空间
//   - Schema 明确，跨语言支持好
package store

import (
	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/proto"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	googleproto "google.golang.org/protobuf/proto"
)

// ========================================
// ProtobufWALCodec 实现
// ========================================

// ProtobufWALCodec Protobuf 编解码器
//
// 特点：
//   - protoc 预编译，性能最优（编码/解码约为 JSON 的 3-5 倍）
//   - 数据大小最小，约为 JSON 的 40-60%
//   - Schema 明确，跨语言支持好
//   - 推荐用于高性能场景
type ProtobufWALCodec struct{}

// NewProtobufWALCodec 创建 Protobuf 编解码器
func NewProtobufWALCodec() *ProtobufWALCodec {
	return &ProtobufWALCodec{}
}

// Encode 编码 WAL 条目（Protobuf 格式）
func (c *ProtobufWALCodec) Encode(entry *WALEntry) ([]byte, error) {
	if entry == nil {
		return nil, types.NewCodecInvalidMessageError("WAL 条目为空")
	}

	// 转换 HLC 时间戳
	pbHLC := &proto.HLC{}
	if entry.Timestamp != nil {
		pbHLC.PhysicalTime = uint64(entry.Timestamp.PhysicalTime())
		pbHLC.LogicalCounter = uint32(entry.Timestamp.LogicalCounter())
	} else {
		// 使用零值 HLC
		hlc := clock.NewHLC()
		pbHLC.PhysicalTime = uint64(hlc.PhysicalTime())
		pbHLC.LogicalCounter = uint32(hlc.LogicalCounter())
	}

	// 转换 WAL 类型
	pbType := proto.WALType_WAL_TYPE_UNSPECIFIED
	switch entry.Type {
	case WALTypePut:
		pbType = proto.WALType_WAL_TYPE_PUT
	case WALTypeDelete:
		pbType = proto.WALType_WAL_TYPE_DELETE
	case WALTypeCheckpoint:
		pbType = proto.WALType_WAL_TYPE_CHECKPOINT
	}

	// 构建 Protobuf 消息
	pbEntry := &proto.WALEntry{
		Type:      pbType,
		Key:       entry.Key,
		Value:     entry.Value,
		OldValue:  entry.OldValue,
		Timestamp: pbHLC,
		Checksum:  entry.Checksum,
	}

	// 使用 Protobuf 编码
	encoded, err := googleproto.Marshal(pbEntry)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("Protobuf", err)
	}

	return encoded, nil
}

// Decode 解码 WAL 条目（Protobuf 格式）
func (c *ProtobufWALCodec) Decode(data []byte) (*WALEntry, error) {
	if len(data) == 0 {
		return nil, types.NewCodecInvalidDataError("Decode", "数据为空")
	}

	// 解码 Protobuf 消息
	pbEntry := &proto.WALEntry{}
	if err := googleproto.Unmarshal(data, pbEntry); err != nil {
		return nil, types.NewCodecDecodeFailedError("Protobuf", err)
	}

	// 转换 WAL 类型
	var walType WALType
	switch pbEntry.Type {
	case proto.WALType_WAL_TYPE_PUT:
		walType = WALTypePut
	case proto.WALType_WAL_TYPE_DELETE:
		walType = WALTypeDelete
	case proto.WALType_WAL_TYPE_CHECKPOINT:
		walType = WALTypeCheckpoint
	default:
		walType = WALTypePut // 默认为 PUT
	}

	// 构建 WALEntry
	entry := &WALEntry{
		Type:     walType,
		Key:      pbEntry.Key,
		Value:    pbEntry.Value,
		OldValue: pbEntry.OldValue,
		Checksum: pbEntry.Checksum,
	}

	// 转换 HLC 时间戳（使用 UnmarshalBinary 方法）
	entry.Timestamp = &clock.HLC{}
	if err := unmarshalHLC(entry.Timestamp, pbEntry.Timestamp.PhysicalTime, pbEntry.Timestamp.LogicalCounter); err != nil {
		return nil, types.NewCodecDecodeFailedError("Timestamp", err)
	}

	return entry, nil
}

// unmarshalHLC 从 Protobuf HLC 转换为 clock.HLC
// 手动构建 10 字节的二进制数据并使用 UnmarshalBinary
func unmarshalHLC(hlc *clock.HLC, physicalTime uint64, logicalCounter uint32) error {
	// 构建 10 字节的 HLC 二进制数据
	// [PhysicalTime 8 bytes][LogicalCounter 2 bytes]
	data := make([]byte, 10)

	// 写入物理时间（大端序，8 字节）
	data[0] = byte((physicalTime >> 56) & 0xFF)
	data[1] = byte((physicalTime >> 48) & 0xFF)
	data[2] = byte((physicalTime >> 40) & 0xFF)
	data[3] = byte((physicalTime >> 32) & 0xFF)
	data[4] = byte((physicalTime >> 24) & 0xFF)
	data[5] = byte((physicalTime >> 16) & 0xFF)
	data[6] = byte((physicalTime >> 8) & 0xFF)
	data[7] = byte(physicalTime & 0xFF)

	// 写入逻辑计数（大端序，2 字节）
	data[8] = byte((logicalCounter >> 8) & 0xFF)
	data[9] = byte(logicalCounter & 0xFF)

	return hlc.UnmarshalBinary(data)
}

// Type 返回编解码器类型
func (c *ProtobufWALCodec) Type() types.CodecType {
	return types.CodecTypeProtobuf
}

// Name 返回编解码器名称
func (c *ProtobufWALCodec) Name() string {
	return "protobuf"
}
