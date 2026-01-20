// Package transport TLV 协议编解码器实现
//
// 支持 MessagePack、JSON、Protobuf 等多种编码方式
package transport

import (
	"encoding/json"
	"fmt"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/vmihailenco/msgpack/v5"
	"google.golang.org/protobuf/proto"
)

// TLVCodec TLV 协议编解码器
//
// 将 Message 序列化为 TLV 协议格式的字节流
type TLVCodec struct {
	// codecType 编码器类型（MessagePack、JSON、Protobuf）
	codecType types.CodecType

	// nodeID 节点 ID
	nodeID uint64

	// msgIDGenerator 消息 ID 生成器
	msgIDGenerator uint16
}

// NewTLVCodec 创建新的 TLV 编解码器
func NewTLVCodec(codecType types.CodecType, nodeID uint64) *TLVCodec {
	return &TLVCodec{
		codecType:      codecType,
		nodeID:         nodeID,
		msgIDGenerator: 0,
	}
}

// nextMsgID 生成下一个消息 ID
func (c *TLVCodec) nextMsgID() uint16 {
	c.msgIDGenerator++
	return c.msgIDGenerator
}

// Encode 编码消息为 TLV 协议格式
func (c *TLVCodec) Encode(msg Message) (*TLVMessage, error) {
	if msg == nil {
		return nil, types.NewCodecInvalidMessageError("消息为空")
	}

	// 根据编码器类型序列化消息
	var data []byte
	var err error

	switch c.codecType {
	case types.CodecTypeMessagePack:
		data, err = msgpack.Marshal(msg)
	case types.CodecTypeJSON:
		data, err = json.Marshal(msg)
	case types.CodecTypeProtobuf:
		// 检查消息是否实现了 ProtoMessage
		if protoMsg, ok := msg.(proto.Message); ok {
			data, err = proto.Marshal(protoMsg)
		} else {
			return nil, types.NewCodecEncodeFailedError("protobuf", fmt.Errorf("消息不支持 Protobuf 编码"))
		}
	default:
		return nil, types.NewCodecEncodeFailedError(c.codecType.String(), fmt.Errorf("不支持的编码器类型"))
	}

	if err != nil {
		return nil, types.NewCodecEncodeFailedError(c.codecType.String(), err)
	}

	// 创建 TLV 消息
	tlvMsg := NewTLVMessage(
		c.nodeID,
		c.nextMsgID(),
		uint16(c.codecType),
		data,
	)

	return tlvMsg, nil
}

// Decode 解码 TLV 协议格式消息
func (c *TLVCodec) Decode(tlvMsg *TLVMessage) (Message, error) {
	if tlvMsg == nil {
		return nil, types.NewCodecInvalidMessageError("TLV 消息为空")
	}

	if len(tlvMsg.Data) == 0 {
		return nil, types.NewCodecInvalidDataError("Decode", "数据为空")
	}

	// 根据编码器类型反序列化消息
	codecType := types.CodecType(tlvMsg.FixedHeader.CodecID)

	var msg Message
	var err error

	switch codecType {
	case types.CodecTypeMessagePack:
		// 先读取消息类型（前 2 字节）
		if len(tlvMsg.Data) < 2 {
			return nil, types.NewCodecInvalidDataError("msgpack", "数据长度不足")
		}
		msgType := MessageType(tlvMsg.Data[0])<<8 | MessageType(tlvMsg.Data[1])

		// 创建消息实例
		msg, err = createMessageByType(msgType)
		if err != nil {
			return nil, err
		}

		// 反序列化（跳过前 2 字节的类型）
		if len(tlvMsg.Data) > 2 {
			if err := msgpack.Unmarshal(tlvMsg.Data[2:], msg); err != nil {
				return nil, types.NewCodecDecodeFailedError("msgpack", err)
			}
		}

	case types.CodecTypeJSON:
		// 先读取消息类型（前 2 字节）
		if len(tlvMsg.Data) < 2 {
			return nil, types.NewCodecInvalidDataError("json", "数据长度不足")
		}
		msgType := MessageType(tlvMsg.Data[0])<<8 | MessageType(tlvMsg.Data[1])

		// 创建消息实例
		msg, err = createMessageByType(msgType)
		if err != nil {
			return nil, err
		}

		// 反序列化（跳过前 2 字节的类型）
		if len(tlvMsg.Data) > 2 {
			if err := json.Unmarshal(tlvMsg.Data[2:], msg); err != nil {
				return nil, types.NewCodecDecodeFailedError("json", err)
			}
		}

	case types.CodecTypeProtobuf:
		// Protobuf 不包含类型信息，需要从其他方式获取
		// 这里我们假设第一个字节是消息类型
		if len(tlvMsg.Data) < 1 {
			return nil, types.NewCodecInvalidDataError("protobuf", "数据长度不足")
		}
		msgType := MessageType(tlvMsg.Data[0])

		// 创建消息实例
		msg, err = createMessageByType(msgType)
		if err != nil {
			return nil, err
		}

		// 检查消息是否实现了 ProtoMessage
		if protoMsg, ok := msg.(proto.Message); ok {
			// 反序列化（跳过第一个字节的类型）
			if len(tlvMsg.Data) > 1 {
				if err := proto.Unmarshal(tlvMsg.Data[1:], protoMsg); err != nil {
					return nil, types.NewCodecDecodeFailedError("protobuf", err)
				}
			}
		} else {
			return nil, types.NewCodecDecodeFailedError("protobuf", fmt.Errorf("消息不支持 Protobuf 解码"))
		}

	default:
		return nil, types.NewCodecDecodeFailedError(codecType.String(), fmt.Errorf("不支持的编码器类型"))
	}

	return msg, nil
}

// EncodeToBytes 编码消息为字节流（用于传输）
func (c *TLVCodec) EncodeToBytes(msg Message) ([]byte, error) {
	tlvMsg, err := c.Encode(msg)
	if err != nil {
		return nil, err
	}

	return tlvMsg.Marshal()
}

// DecodeFromBytes 从字节流解码消息
func (c *TLVCodec) DecodeFromBytes(data []byte) (Message, error) {
	tlvMsg := &TLVMessage{}
	if err := tlvMsg.Unmarshal(data); err != nil {
		return nil, err
	}

	return c.Decode(tlvMsg)
}

// TLVCodecExt TLV 扩展编解码器
//
// 用于处理 TLV 扩展字段的序列化和反序列化
type TLVCodecExt struct{}

// NewTLVCodecExt 创建新的 TLV 扩展编解码器
func NewTLVCodecExt() *TLVCodecExt {
	return &TLVCodecExt{}
}

// EncodeCompressExt 编码压缩扩展
func (c *TLVCodecExt) EncodeCompressExt(compressID uint16) (*ExtField, error) {
	data := make([]byte, 2)
	data[0] = byte(compressID >> 8)
	data[1] = byte(compressID)

	return &ExtField{
		Type:  ExtCompress,
		Value: data,
	}, nil
}

// DecodeCompressExt 解码压缩扩展
func (c *TLVCodecExt) DecodeCompressExt(field *ExtField) (uint16, error) {
	if len(field.Value) < 2 {
		return 0, types.NewInvalidFrameSizeError("压缩扩展数据长度不足")
	}

	compressID := uint16(field.Value[0])<<8 | uint16(field.Value[1])
	return compressID, nil
}

// EncodeEncryptExt 编码加密扩展
func (c *TLVCodecExt) EncodeEncryptExt(encryptID uint16, nonce []byte, version string) (*ExtField, error) {
	// 使用 MessagePack 序列化
	data := struct {
		EncryptID uint16 `msgpack:"eid"`
		Nonce     []byte `msgpack:"non"`
		Version   string `msgpack:"ver"`
	}{
		EncryptID: encryptID,
		Nonce:     nonce,
		Version:   version,
	}

	bytes, err := msgpack.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("序列化加密扩展失败: %w", err)
	}

	return &ExtField{
		Type:  ExtEncrypt,
		Value: bytes,
	}, nil
}

// DecodeEncryptExt 解码加密扩展
func (c *TLVCodecExt) DecodeEncryptExt(field *ExtField) (encryptID uint16, nonce []byte, version string, err error) {
	data := struct {
		EncryptID uint16 `msgpack:"eid"`
		Nonce     []byte `msgpack:"non"`
		Version   string `msgpack:"ver"`
	}{}

	if err := msgpack.Unmarshal(field.Value, &data); err != nil {
		return 0, nil, "", fmt.Errorf("反序列化加密扩展失败: %w", err)
	}

	return data.EncryptID, data.Nonce, data.Version, nil
}

// EncodePriorityExt 编码优先级扩展
func (c *TLVCodecExt) EncodePriorityExt(priority uint8) (*ExtField, error) {
	data := make([]byte, 1)
	data[0] = priority

	return &ExtField{
		Type:  ExtPriority,
		Value: data,
	}, nil
}

// DecodePriorityExt 解码优先级扩展
func (c *TLVCodecExt) DecodePriorityExt(field *ExtField) (uint8, error) {
	if len(field.Value) < 1 {
		return 0, types.NewInvalidFrameSizeError("优先级扩展数据长度不足")
	}

	return field.Value[0], nil
}
