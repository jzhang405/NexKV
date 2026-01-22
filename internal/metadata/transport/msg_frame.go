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
//   - interface{}: 解码后的数据（类型由具体解码器决定）
//   - error: 解码失败时返回错误
type ExtDecoder func(tlv TLV) (interface{}, error)

// TLV 是 ExtField 的别名，用于解码器接口
type TLV = ExtField

var (
	// decoders 全局解码器注册表
	decoders = map[ExtFieldType]ExtDecoder{
		ExtHop:      decodeHopExt,
		ExtCompress: decodeCompressExt,
		ExtEncrypt:  decodeEncryptExt,
		ExtFragment: decodeFragmentExt,
		ExtPriority: decodePriorityExt,
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
	FixedHeader         // 固定帧头（31 字节）
	TLVs        []TLV   // 扩展头 TLV（可变长度）
	Message     Message // 消息体（实际业务消息）
}

// NewMsgFrame 创建新的网络帧
func NewMsgFrame(nodeID uint64, msgSeq uint64, msgType MessageType, codecID uint16, message Message) *MsgFrame {
	return &MsgFrame{
		FixedHeader: FixedHeader{
			Magic:        [4]byte{'N', 'X', 'U', 'T'},
			Version:      1,
			NodeID:       nodeID,
			MsgSeq:       msgSeq,
			MsgType:      msgType,
			CodecID:      codecID,
			ExtHeaderLen: 0,
			DataLength:   0,
		},
		TLVs:    make([]TLV, 0),
		Message: message,
	}
}

// GetExt 获取指定类型的扩展字段（通用方法）
//
// 返回解码后的扩展字段和是否存在标志。每次调用都会重新解码。
func (f *MsgFrame) GetExt(fieldType ExtFieldType) (interface{}, bool) {
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

// GetHopCount 获取跳数 TTL
func (f *MsgFrame) GetHopCount() (*HopExt, bool) {
	return GetExtAs[*HopExt](f, ExtHop)
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

// GetPriority 获取优先级
func (f *MsgFrame) GetPriority() (*PriorityExt, bool) {
	return GetExtAs[*PriorityExt](f, ExtPriority)
}

// ========================================
// TLV 字段解码器（内部使用）
// ========================================

// decodeHopExt 解码跳数 TTL 扩展
func decodeHopExt(tlv TLV) (interface{}, error) {
	hop, totalHop, err := DecodeHopExt(&tlv)
	if err != nil {
		return nil, err
	}
	return &HopExt{Hop: hop, TotalHop: totalHop}, nil
}

// decodeCompressExt 解码压缩扩展
func decodeCompressExt(tlv TLV) (interface{}, error) {
	compressID, err := DecodeCompressExt(&tlv)
	if err != nil {
		return nil, err
	}
	return &CompressExt{CompressID: compressID}, nil
}

// decodeEncryptExt 解码加密扩展
func decodeEncryptExt(tlv TLV) (interface{}, error) {
	encryptID, nonce, version, err := DecodeEncryptExt(&tlv)
	if err != nil {
		return nil, err
	}
	return &EncryptExt{EncryptID: encryptID, Nonce: nonce, Version: version}, nil
}

// decodeFragmentExt 解码分片扩展
func decodeFragmentExt(tlv TLV) (interface{}, error) {
	index, total, err := DecodeFragmentExt(&tlv)
	if err != nil {
		return nil, err
	}
	return &SegmentExt{Index: index, Total: total}, nil
}

// decodePriorityExt 解码优先级扩展
func decodePriorityExt(tlv TLV) (interface{}, error) {
	priority, err := DecodePriorityExt(&tlv)
	if err != nil {
		return nil, err
	}
	return &PriorityExt{Priority: priority}, nil
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
		return PriorityNormal
	}
	return f.Message.Priority()
}

// ========================================
// 辅助方法
// ========================================

// GetTLV 获取指定类型的 TLV 字段（原始数据，未解码）
func (f *MsgFrame) GetTLV(fieldType ExtFieldType) *TLV {
	for i := range f.TLVs {
		if f.TLVs[i].Type == fieldType {
			return &f.TLVs[i]
		}
	}
	return nil
}

// HasHopCount 检查是否有 Hop Count 限制（便捷方法）
func (f *MsgFrame) HasHopCount() bool {
	v, _ := f.GetHopCount()
	return v != nil
}

// IsHopExpired 检查 Hop Count 是否过期（便捷方法）
func (f *MsgFrame) IsHopExpired() bool {
	v, ok := f.GetHopCount()
	return ok && v.Hop == 0
}

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
func (f *MsgFrame) EncodeTLVs() ([]ExtField, error) {
	var fields []ExtField

	// Hop Count
	if hop, ok := f.GetHopCount(); ok {
		fields = append(fields, *EncodeHopExt(hop.Hop, hop.TotalHop))
	}

	// Priority
	if priority, ok := f.GetPriority(); ok {
		fields = append(fields, *EncodePriorityExt(priority.Priority))
	}

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

// prepareForwardMessage 准备转发消息（深拷贝 + Hop Count 递减）
//
// 返回:
//   - *MsgFrame: 处理后的消息副本
//   - error: Hop Count 过期或其他错误
func prepareForwardMessage(frame *MsgFrame) (*MsgFrame, error) {
	if frame.Message == nil {
		return nil, types.NewOpErr(types.ErrCodecEncodeFailed, "ForwardMessage",
			"消息为空", nil)
	}

	forwardFrame := frame.DeepCopy()

	// 递减 Hop Count
	if hop, ok := forwardFrame.GetHopCount(); ok {
		if hop.Hop == 0 {
			return nil, types.NewTransportHopCountExpiredError()
		}
		hop.Hop--

		// 更新 TLVs 中的 Hop Count
		for i := range forwardFrame.TLVs {
			if forwardFrame.TLVs[i].Type == ExtHop {
				forwardFrame.TLVs[i] = *EncodeHopExt(hop.Hop, hop.TotalHop)
				break
			}
		}
	}

	return forwardFrame, nil
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

var (
	// sendOptionsPool 用于复用 sendOptions 对象，减少 GC 压力
	sendOptionsPool = sync.Pool{
		New: func() interface{} {
			return &sendOptions{}
		},
	}
)

// processSendOptions 处理发送选项（内部使用）
//
// 重要：调用方必须使用 defer releaseSendOptions(options) 确保归还对象。
// 推荐使用 withSendOptions 包装函数自动管理资源。
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

// withSendOptions 自动管理 sendOptions 生命周期的包装函数
//
// 自动从 sync.Pool 获取和归还 options，即使回调函数 panic 也会确保资源归还。
func withSendOptions(opts []SendOpt, fn func(*sendOptions) error) error {
	options := processSendOptions(opts...)
	defer releaseSendOptions(options)
	return fn(options)
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

// WithPriority 设置优先级
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
