// Package transport 提供网络帧结构和函数选项模式
//
// 核心组件：
//   - MsgFrame: 网络帧结构（FixedHeader + TLV + Message）
//   - Decoder: 动态解码器注册表
//   - SendOpt: 函数选项模式（Functional Options）
package transport

import (
	"fmt"
	"sync"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// ExtDecoder TLV 字段解码器接口
// ========================================

// ExtDecoder TLV 字段解码器函数类型
//
// 参数:
//   - tlv: TLV 字段
//
// 返回:
//   - any: 解码后的数据（类型由具体解码器决定）
//   - error: 解码失败时返回错误
type ExtDecoder func(tlv TLV) (any, error)

// TLV 是 ExtField 的别名，用于解码器接口
type TLV = ExtField

var (
	// decoders 全局解码器注册表
	decoders = map[ExtFieldType]ExtDecoder{
		// ExtHop 已移至 FixedHeader，不再作为 TLV 解码
		// ExtCorrelationID 已移至 FixedHeader (NodeID+MsgSeq)，不再作为 TLV 解码
		// ExtPriority 已移除，priority 通过 Message.Priority() 获取
		// 注意：ExtFragment (分片) 保留，用于 UDP 分片处理
		ExtCompress: decodeCompressExt,
		ExtEncrypt:  decodeEncryptExt,
		ExtFragment: decodeFragmentExt,
	}
	// decoderMutex 保护解码器注册表的并发访问
	decoderMutex sync.RWMutex
)

// RegisterDecoder 注册 TLV 字段解码器
//
// 参数:
//   - fieldType: 字段类型
//   - decoder: 解码器函数
//
// 说明:
//   - 支持运行时动态注册新的解码器
//   - 如果 fieldType 已存在，会覆盖旧的解码器
//   - 并发安全：使用 RWMutex 保护
func RegisterDecoder(fieldType ExtFieldType, decoder ExtDecoder) {
	decoderMutex.Lock()
	defer decoderMutex.Unlock()
	decoders[fieldType] = decoder
}

// getDecoder 获取解码器（内部使用）
func getDecoder(fieldType ExtFieldType) (ExtDecoder, bool) {
	decoderMutex.RLock()
	defer decoderMutex.RUnlock()
	decoder, ok := decoders[fieldType]
	return decoder, ok
}

// ========================================
// MsgFrame 网络帧结构
// ========================================

// MsgFrame 网络帧（FixedHeader + TLV + Message）
//
// 注意：MsgFrame 按值传递，无缓存机制。每次调用 GetExt() 都会重新解码。
type MsgFrame struct {
	FixedHeader         // 固定帧头（42 字节）
	TLVs        []TLV   // 扩展头 TLV（可变长度）
	Message     Message // 消息体（实际业务消息）
}

// NewMsgFrame 创建新的网络帧
func NewMsgFrame(nodeID uint64, msgSeq uint64, msgType MessageType, codecID uint16, message Message) *MsgFrame {
	return &MsgFrame{
		FixedHeader: FixedHeader{
			Magic:         [4]byte{'N', 'X', 'U', 'T'},
			Version:       1,
			NodeID:        nodeID,
			MsgSeq:        msgSeq,
			ForwardNodeID: 0,
			Hops:          MaxHops, // 默认最大跳数
			MsgType:       msgType,
			CodecID:       codecID,
			ExtHeaderLen:  0,
			DataLength:    0,
		},
		TLVs:    make([]TLV, 0),
		Message: message,
	}
}

// GetExt 获取指定类型的扩展字段（通用方法）
//
// 返回解码后的扩展字段和是否存在标志。每次调用都会重新解码。
func (f *MsgFrame) GetExt(fieldType ExtFieldType) (any, bool) {
	// 查找 TLV 字段
	var tlv *TLV
	for i := range f.TLVs {
		if f.TLVs[i].Type == fieldType {
			tlv = &f.TLVs[i]
			break
		}
	}

	if tlv == nil {
		return nil, false
	}

	// 获取解码器
	decoder, ok := getDecoder(fieldType)
	if !ok {
		logging.Debugf("未找到解码器: %d（字段类型可能未注册或版本不兼容）", fieldType)
		return nil, false
	}

	// 解码
	decoded, err := decoder(*tlv)
	if err != nil {
		logging.Warnf("解码扩展字段失败: type=%d, err=%v（数据可能损坏）", fieldType, err)
		return nil, false
	}

	return decoded, true
}

// GetExtAs 获取扩展字段并断言为指定类型
func GetExtAs[T any](f *MsgFrame, fieldType ExtFieldType) (T, bool) {
	var zero T
	value, ok := f.GetExt(fieldType)
	if !ok {
		return zero, false
	}
	typed, ok := value.(T)
	return typed, ok
}

// GetHopCount 获取剩余跳数（从 FixedHeader）
func (f *MsgFrame) GetHopCount() (uint8, bool) {
	// hops 现在是 FixedHeader 的一部分
	// 注意：总是返回 hops 值，即使为 0。调用方可以通过 hops == 0 判断是否过期。
	return f.Hops, true
}

// GetCompress 获取压缩配置
func (f *MsgFrame) GetCompress() (*CompressExt, bool) {
	return GetExtAs[*CompressExt](f, ExtCompress)
}

// GetEncrypt 获取加密配置
func (f *MsgFrame) GetEncrypt() (*EncryptExt, bool) {
	return GetExtAs[*EncryptExt](f, ExtEncrypt)
}

// GetSegment 获取分片配置
func (f *MsgFrame) GetSegment() (*SegmentExt, bool) {
	return GetExtAs[*SegmentExt](f, ExtFragment)
}

// ========================================
// TLV 字段解码器（内部使用）
// ========================================

// decodeCompressExt 解码压缩扩展
func decodeCompressExt(tlv TLV) (any, error) {
	compressID, err := DecodeCompressExt(&tlv)
	if err != nil {
		return nil, err
	}
	return &CompressExt{CompressID: compressID}, nil
}

// decodeEncryptExt 解码加密扩展
func decodeEncryptExt(tlv TLV) (any, error) {
	encryptID, nonce, version, err := DecodeEncryptExt(&tlv)
	if err != nil {
		return nil, err
	}
	return &EncryptExt{EncryptID: encryptID, Nonce: nonce, Version: version}, nil
}

// decodeFragmentExt 解码分片扩展（用于 UDP 分片重组）
func decodeFragmentExt(tlv TLV) (any, error) {
	index, total, err := DecodeFragmentExt(&tlv)
	if err != nil {
		return nil, err
	}
	return &SegmentExt{Index: index, Total: total}, nil
}

// ========================================
// Message 接口实现
// ========================================

// Type 返回消息类型（实现 Message 接口）
func (f MsgFrame) Type() MessageType {
	if f.Message == nil {
		return f.MsgType // 从 FixedHeader 获取
	}
	return f.Message.Type()
}

// Priority 返回消息优先级（实现 Message 接口）
func (f MsgFrame) Priority() int {
	if f.Message == nil {
		return int(types.PriorityNormal)
	}
	return f.Message.Priority()
}

// MsgRole 返回消息角色（实现 Message 接口）
func (f MsgFrame) MsgRole() types.MsgRole {
	if f.Message == nil {
		return f.MsgType.MsgRole()
	}
	return f.Message.MsgRole()
}

// ExpectResponse 返回响应期望（实现 Message 接口）
func (f MsgFrame) ExpectResponse() types.ResponseExpectation {
	if f.Message == nil {
		return f.MsgType.ExpectResponse()
	}
	return f.Message.ExpectResponse()
}

// ProtocolType 返回传输协议类型（实现 Message 接口）
func (f MsgFrame) ProtocolType() types.ProtocolType {
	if f.Message == nil {
		return f.MsgType.ProtocolType()
	}
	return f.Message.ProtocolType()
}

// CorrelationID 返回全局唯一的关联ID（实现 Message 接口）
//
// 自动从 FixedHeader 的 NodeID + MsgSeq 组装生成
// 格式："{NodeID}:{MsgSeq}"
// 用途：传输层通过此ID匹配请求-响应，reqTable 核心索引
func (f MsgFrame) CorrelationID() string {
	// 自动从 FixedHeader 组装 NodeID:MsgSeq
	return fmt.Sprintf("%d:%d", f.NodeID, f.MsgSeq)
}

// ========================================
// 辅助方法
// ========================================

// HasCompression 检查是否有压缩配置（便捷方法）
func (f *MsgFrame) HasCompression() bool {
	v, _ := f.GetCompress()
	return v != nil
}

// HasEncryption 检查是否有加密配置（便捷方法）
func (f *MsgFrame) HasEncryption() bool {
	v, _ := f.GetEncrypt()
	return v != nil
}

// HasSegment 检查是否有分片配置（便捷方法）
func (f *MsgFrame) HasSegment() bool {
	v, _ := f.GetSegment()
	return v != nil
}

// String 返回 MsgFrame 的字符串表示
func (f *MsgFrame) String() string {
	return fmt.Sprintf("MsgFrame{NodeID=%d, MsgSeq=%d, MsgType=%d, TLVs=%d}",
		f.NodeID, f.MsgSeq, f.MsgType, len(f.TLVs))
}

// EncodeTLVs 编码所有 TLV 扩展字段
//
// 用于 ForwardMessage() 场景，重新编码 TLV 字段。每次调用都会通过 GetExt() 重新解码。
// 注意：Hops 现在在 FixedHeader 中，不再作为 TLV 编码。
// 注意：Priority 已移除，priority 通过 Message.Priority() 获取。
func (f *MsgFrame) EncodeTLVs() ([]ExtField, error) {
	var fields []ExtField

	// Compress
	if compress, ok := f.GetCompress(); ok {
		fields = append(fields, *EncodeCompressExt(compress.CompressID))
	}

	// Encrypt
	if encrypt, ok := f.GetEncrypt(); ok {
		encryptField, err := EncodeEncryptExt(encrypt.EncryptID, encrypt.Nonce, encrypt.Version)
		if err != nil {
			return nil, err
		}
		fields = append(fields, *encryptField)
	}

	// Segment
	if segment, ok := f.GetSegment(); ok {
		fields = append(fields, *EncodeFragmentExt(segment.Index, segment.Total))
	}

	return fields, nil
}

// DeepCopy 创建 MsgFrame 的深拷贝，避免修改原始数据造成 data race
func (f *MsgFrame) DeepCopy() *MsgFrame {
	result := &MsgFrame{
		FixedHeader: f.FixedHeader,
		TLVs:        make([]TLV, len(f.TLVs)),
		Message:     f.Message,
	}

	// 深拷贝 TLVs
	for i := range f.TLVs {
		result.TLVs[i] = TLV{
			Type:  f.TLVs[i].Type,
			Value: cloneBytes(f.TLVs[i].Value),
		}
	}

	return result
}

// cloneBytes 深拷贝字节切片
func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

// prepareForwardMessage 准备转发消息（深拷贝 + Hops 递减）
//
// 返回:
//   - *MsgFrame: 处理后的消息副本
//   - error: Hops 过期或其他错误
// validateAndDecrementHops 验证并递减跳数
//
// 返回递减后的跳数，如果跳数为 0 则返回错误
// 此函数用于简化转发逻辑中的跳数处理
func validateAndDecrementHops(frame MsgFrame) (uint8, error) {
	currentHops, hasHops := frame.GetHopCount()

	// 如果没有设置跳数，默认为 255（不限制）
	if !hasHops {
		return 255, nil
	}

	// 检查跳数是否已过期
	if currentHops == 0 {
		return 0, types.NewTransportHopCountExpiredError()
	}

	// 递减跳数
	return currentHops - 1, nil
}

// prepareForwardMessage 准备转发消息（返回完整的 MsgFrame 副本）
func prepareForwardMessage(frame *MsgFrame) (*MsgFrame, error) {
	if frame.Message == nil {
		return nil, types.NewOpErr(types.ErrCodecEncodeFailed, "ForwardMessage",
			"消息为空", nil)
	}

	forwardFrame := frame.DeepCopy()

	// 递减 Hops（从 FixedHeader）
	hops, _ := forwardFrame.GetHopCount()
	if hops == 0 {
		return nil, types.NewTransportHopCountExpiredError()
	}
	forwardFrame.Hops = hops - 1

	return forwardFrame, nil
}

// ========================================
// SendOpt 函数选项模式
// ========================================

// SendOpt 发送选项（functional options 模式）
type SendOpt func(*sendOptions)

// sendOptions 发送选项内部结构
type sendOptions struct {
	hopCount   *uint16 // 跳数 TTL
	compressID *uint16 // 压缩算法 ID
	encryptID  *uint16 // 加密算法 ID
}

var (
	// sendOptionsPool 用于复用 sendOptions 对象，减少 GC 压力
	sendOptionsPool = sync.Pool{
		New: func() any {
			return &sendOptions{}
		},
	}
)

// processSendOptions 处理发送选项（内部使用）
//
// 重要：调用方必须使用 defer releaseSendOptions(options) 确保归还对象。
func processSendOptions(opts ...SendOpt) *sendOptions {
	options := sendOptionsPool.Get().(*sendOptions)
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
func WithHopCount(totalHop uint16) SendOpt {
	return func(o *sendOptions) {
		o.hopCount = &totalHop
	}
}

// WithCompression 设置压缩算法
func WithCompression(compressID uint16) SendOpt {
	return func(o *sendOptions) {
		o.compressID = &compressID
	}
}

// WithEncryption 设置加密算法
func WithEncryption(encryptID uint16) SendOpt {
	return func(o *sendOptions) {
		o.encryptID = &encryptID
	}
}

// ========================================
// BaseMessage 基础消息实现
// ========================================

// ========================================
// 消息注册表（替代 switch-case 工厂函数）
// ========================================

// messageFactory 消息工厂函数类型
type messageFactory func() Message

var (
	// messageRegistry 消息注册表（msgType -> factory）
	messageRegistry = map[MessageType]messageFactory{}
	// registryMutex 保护注册表的并发访问
	registryMutex sync.RWMutex
)

// registerMessage 注册消息类型
//
// 参数:
//   - msgType: 消息类型
//   - factory: 消息工厂函数（返回新的消息实例）
//
// 说明:
//   - 用于初始化阶段注册所有消息类型
//   - 并发安全：使用 RWMutex 保护
func registerMessage(msgType MessageType, factory messageFactory) {
	registryMutex.Lock()
	defer registryMutex.Unlock()
	messageRegistry[msgType] = factory
}

// createMessage 根据消息类型创建消息实例（使用注册表）
//
// 替代 codec.go 中的 createMessageByType() 函数
func createMessage(msgType MessageType) (Message, error) {
	registryMutex.RLock()
	factory, ok := messageRegistry[msgType]
	registryMutex.RUnlock()

	if !ok {
		return nil, types.NewCodecUnknownMessageTypeError(int(msgType))
	}

	return factory(), nil
}

// ========================================
// BaseMessage 基础消息实现
// ========================================

// BaseMessage 基础消息实现（提供默认的 Message 接口实现）
//
// 用途:
//   - 作为所有消息类型的内嵌基类，提供统一的接口实现
//   - 消除重复代码，避免每个消息类型都实现相同的 7 个方法
//   - 支持通过内嵌 BaseMessage 来自动获得 Message 接口实现
type BaseMessage struct {
	// MessageType 消息类型（由具体消息类型在初始化时设置）
	MessageType MessageType
	// correlationID 关联ID（由传输层自动设置，用于请求-响应匹配）
	correlationID string
}

// Type 返回消息类型（实现 Message 接口）
func (m *BaseMessage) Type() MessageType {
	return m.MessageType
}

// Priority 返回消息优先级（实现 Message 接口）
//
// 默认实现：从消息类型的优先级配置中获取
// 具体消息类型可以覆盖此方法以提供自定义优先级
func (m *BaseMessage) Priority() int {
	return int(GetPriority(m.MessageType))
}

// MsgRole 返回消息角色（实现 Message 接口）
//
// 默认实现：从消息类型的配置中获取
// 用于快速判断消息是请求还是响应
func (m *BaseMessage) MsgRole() types.MsgRole {
	return m.MessageType.MsgRole()
}

// ExpectResponse 返回响应期望（实现 Message 接口）
//
// 默认实现：从消息类型的配置中获取
func (m *BaseMessage) ExpectResponse() types.ResponseExpectation {
	return m.MessageType.ExpectResponse()
}

// ProtocolType 返回传输协议类型（实现 Message 接口）
//
// 默认实现：从消息类型的配置中获取
func (m *BaseMessage) ProtocolType() types.ProtocolType {
	return m.MessageType.ProtocolType()
}

// CorrelationID 返回全局唯一的关联ID（实现 Message 接口）
//
// 用途：传输层通过此ID匹配请求-响应，reqTable 核心索引
// - 请求消息：由发送端生成（如 UUID/节点ID+递增序列）
// - 响应消息：必须和对应请求的 CorrelationID 一致
func (m *BaseMessage) CorrelationID() string {
	return m.correlationID
}
