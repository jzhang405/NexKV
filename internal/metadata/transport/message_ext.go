// Package transport 增强消息结构和函数选项模式
//
// 本文件实现：
//   - MsgExt: 增强消息结构（原始消息 + TLV 扩展字段）
//   - SendOpt: 函数选项模式（Functional Options）
//
// 注意：
//   - TLV 扩展字段结构（HopExt、CompressExt、EncryptExt、SegmentExt、PriorityExt）
//     统一定义在 frame.go 中，用于 MessagePack 序列化和便捷访问
//   - TLV 字段结构使用 frame.ExtField
package transport

import (
	"fmt"
	"sync"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// MsgExt 增强消息结构
// ========================================

// MsgExt 增强消息（原始消息 + TLV 扩展字段）
//
// 说明：
//   - Message: 原始消息（嵌入，继承所有方法）
//   - TLVs: 原始 ExtField 字段列表
//   - HopCount/Compress/Encrypt/Segment/PriorityExt: 解析后的便捷访问字段
type MsgExt struct {
	Message                // 原始消息（嵌入，继承所有方法）
	TLVs       []ExtField   // 原始 ExtField 字段列表
	HopCount   *HopExt      // 跳数 TTL（便捷访问，nil 表示无）
	Compress   *CompressExt // 压缩配置（便捷访问，nil 表示无）
	Encrypt    *EncryptExt  // 加密配置（便捷访问，nil 表示无）
	Segment    *SegmentExt  // 分片配置（便捷访问，nil 表示无）
	PriorityExt *PriorityExt // 优先级（便捷访问，nil 表示无）
}

// GetType 返回消息类型（实现 Message 接口）
func (m MsgExt) GetType() MessageType {
	if m.Message == nil {
		return MessageType(0)
	}
	embedded := m.Message // 使用局部变量避免方法提升歧义
	return embedded.Type()
}

// GetPriority 返回消息优先级（实现 Message 接口）
func (m MsgExt) Priority() int {
	if m.Message == nil {
		return PriorityNormal
	}
	return m.Message.Priority()
}

// GetTLV 获取指定类型的 TLV 字段
func (m MsgExt) GetTLV(fieldType ExtFieldType) *ExtField {
	for _, tlv := range m.TLVs {
		if tlv.Type == fieldType {
			return &tlv
		}
	}
	return nil
}

// parseExtField 解析单个 TLV 扩展字段并填充到 MsgExt
//
// 参数:
//   - msgExt: 要填充的增强消息
//   - field: 要解析的 TLV 字段
//
// 说明:
//   - 解析失败时记录警告日志，但不中断处理流程
//   - 每种字段类型都有独立的 Decode 函数（定义在 codec.go）
func parseExtField(msgExt *MsgExt, field *ExtField) {
	switch field.Type {
	case ExtHop:
		hop, totalHop, err := DecodeHopExt(field)
		if err != nil {
			logging.Warnf("解析 HopExt 失败: %v, 字段类型: %d", err, field.Type)
		} else {
			msgExt.HopCount = &HopExt{Hop: hop, TotalHop: totalHop}
		}
	case ExtCompress:
		compressID, err := DecodeCompressExt(field)
		if err != nil {
			logging.Warnf("解析 CompressExt 失败: %v, 字段类型: %d", err, field.Type)
		} else {
			msgExt.Compress = &CompressExt{CompressID: compressID}
		}
	case ExtEncrypt:
		encryptID, nonce, version, err := DecodeEncryptExt(field)
		if err != nil {
			logging.Warnf("解析 EncryptExt 失败: %v, 字段类型: %d", err, field.Type)
		} else {
			msgExt.Encrypt = &EncryptExt{EncryptID: encryptID, Nonce: nonce, Version: version}
		}
	case ExtFragment:
		index, total, err := DecodeFragmentExt(field)
		if err != nil {
			logging.Warnf("解析 SegmentExt 失败: %v, 字段类型: %d", err, field.Type)
		} else {
			msgExt.Segment = &SegmentExt{Index: index, Total: total}
		}
	case ExtPriority:
		priority, err := DecodePriorityExt(field)
		if err != nil {
			logging.Warnf("解析 PriorityExt 失败: %v, 字段类型: %d", err, field.Type)
		} else {
			msgExt.PriorityExt = &PriorityExt{Priority: priority}
		}
	}
}

// HasHopCount 检查是否有 Hop Count 限制
func (m MsgExt) HasHopCount() bool {
	return m.HopCount != nil
}

// IsHopExpired 检查 Hop Count 是否过期
func (m MsgExt) IsHopExpired() bool {
	return m.HopCount != nil && m.HopCount.Hop == 0
}

// HasCompression 检查是否有压缩配置
func (m MsgExt) HasCompression() bool {
	return m.Compress != nil
}

// HasEncryption 检查是否有加密配置
func (m MsgExt) HasEncryption() bool {
	return m.Encrypt != nil
}

// HasSegment 检查是否有分片配置
func (m MsgExt) HasSegment() bool {
	return m.Segment != nil
}

// String 返回 MsgExt 的字符串表示
func (m MsgExt) String() string {
	return fmt.Sprintf("MsgExt{Type=%d, TLVs=%d, HopCount=%v, Compress=%v, Encrypt=%v, Segment=%v, PriorityExt=%v}",
		m.GetType(), len(m.TLVs), m.HopCount, m.Compress, m.Encrypt, m.Segment, m.PriorityExt)
}

// ========================================
// SendOpt 函数选项模式
// ========================================

// SendOpt 发送选项（functional options 模式）
type SendOpt func(*sendOptions)

// sendOptions 发送选项内部结构
type sendOptions struct {
	hopCount   *uint16         // 跳数 TTL
	compressID *uint16         // 压缩算法 ID
	encryptID  *uint16         // 加密算法 ID
	priority   *types.Priority // 优先级
}

// sendOptionsPool 用于复用 sendOptions 对象，减少 GC 压力
var sendOptionsPool = sync.Pool{
	New: func() interface{} {
		return &sendOptions{}
	},
}

// processSendOptions 处理发送选项（内部使用，使用 sync.Pool 优化性能）
func processSendOptions(opts ...SendOpt) *sendOptions {
	// 从池中获取对象
	options := sendOptionsPool.Get().(*sendOptions)

	// 应用所有选项
	for _, opt := range opts {
		opt(options)
	}

	return options
}

// releaseSendOptions 将 sendOptions 对象归还到池中
// 注意：调用方应在使用完 options 后调用此方法
func releaseSendOptions(opts *sendOptions) {
	if opts != nil {
		// 重置所有字段为零值，避免数据泄漏
		*opts = sendOptions{}
		sendOptionsPool.Put(opts)
	}
}

// WithHopCount 设置跳数 TTL
//
// 参数：
//   - totalHop: 最大跳数（发送时自动初始化 hop = totalHop）
func WithHopCount(totalHop uint16) SendOpt {
	return func(o *sendOptions) {
		o.hopCount = &totalHop
	}
}

// WithCompression 设置压缩算法
//
// 参数：
//   - compressID: 压缩算法 ID（1=无压缩, 2=Snappy, 3=GZIP, 4=LZ4）
func WithCompression(compressID uint16) SendOpt {
	return func(o *sendOptions) {
		o.compressID = &compressID
	}
}

// WithEncryption 设置加密算法
//
// 参数：
//   - encryptID: 加密算法 ID
func WithEncryption(encryptID uint16) SendOpt {
	return func(o *sendOptions) {
		o.encryptID = &encryptID
	}
}

// WithPriority 设置优先级
//
// 参数：
//   - priority: 优先级（0-4，0最低，4最高）
func WithPriority(priority types.Priority) SendOpt {
	return func(o *sendOptions) {
		o.priority = &priority
	}
}

// ========================================
// BaseMessage 基础消息实现
// ========================================

// BaseMessage 基础消息实现（提供默认的 Message 接口实现）
type BaseMessage struct {
	msgType  MessageType
	payload  []byte
	priority int
}

// NewBaseMessage 创建基础消息
func NewBaseMessage(msgType MessageType, payload []byte) *BaseMessage {
	return &BaseMessage{
		msgType:  msgType,
		payload:  payload,
		priority: GetPriority(msgType),
	}
}

// Type 返回消息类型
func (m *BaseMessage) Type() MessageType {
	return m.msgType
}

// GetPayload 返回消息负载
func (m *BaseMessage) GetPayload() []byte {
	return m.payload
}

// Priority 返回消息优先级
func (m *BaseMessage) Priority() int {
	return m.priority
}

// SetPriority 设置消息优先级
func (m *BaseMessage) SetPriority(priority int) {
	m.priority = priority
}
