// Package transport TLV (Type-Length-Value) 协议实现
//
// 统一的 TCP/UDP 传输协议，支持灵活的扩展机制
//
// 协议格式:
// [FixedHeader 16 bytes][VarExtHeader 变长][Data 变长][CRC32 4 bytes]
//
// - FixedHeader: 固定头（16 字节）
//   - Magic: 4 字节（"NXUT"）
//   - NodeID: 8 字节（发送节点 ID）
//   - MsgID: 2 字节（消息 ID）
//   - CodecID: 2 字节（编码器 ID）
//
// - VarExtHeader: 变长 TLV 扩展头
//   - ExtTotalLen: 2 字节（扩展头总长度）
//   - TLV Fields: N 个 TLV 字段（压缩、加密、分片等）
//
// - Data: 业务数据（MessagePack/JSON/Protobuf 序列化）
//
// - CRC32: 4 字节（CRC32-IEEE 校验，覆盖 VarExtHeader + Data）
package transport

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

const (
	// FixedHeaderLen 固定头长度（16 字节）
	FixedHeaderLen = 16

	// MagicNumber TLV 协议魔术字 "NXUT" (0x4E 0x58 0x55 0x54)
	MagicNumber = 0x4E585554

	// CRCLen CRC32 长度（4 字节）
	CRCLen = 4

	// UDPSafePayloadSize UDP 安全负载大小（1400 字节）
	// Ethernet MTU 1500 - IP 头 20 - UDP 头 8 - 安全余量 72
	UDPSafePayloadSize = 1400

	// MaxExtHeaderSize 最大扩展头大小（1KB）
	MaxExtHeaderSize = 1024

	// MaxTLVMessageSize 最大 TLV 消息大小（10MB）
	MaxTLVMessageSize = 10 * 1024 * 1024
)

// FixedHeader 固定头结构（16 字节）
//
// ┌─────────────────────────────────────────────────────────┐
// │ Magic (4) │ NodeID (8) │ MsgID (2) │ CodecID (2)      │
// ├─────────────────────────────────────────────────────────┤
// │ Magic: "NXUT" (0x4E 0x58 0x55 0x54)                    │
// │ NodeID: 发送节点 ID（uint64，大端序）                   │
// │ MsgID: 消息 ID（uint16，大端序，用于去重和匹配）        │
// │ CodecID: 编码器 ID（uint16，大端序）                    │
// └─────────────────────────────────────────────────────────┘
type FixedHeader struct {
	// Magic 魔术字（固定为 "NXUT"）
	Magic [4]byte

	// NodeID 发送节点 ID
	NodeID uint64

	// MsgID 消息 ID（用于去重和匹配请求响应）
	MsgID uint16

	// CodecID 编码器 ID（0=JSON, 1=MessagePack, 2=Protobuf）
	CodecID uint16
}

// NewFixedHeader 创建新的固定头
func NewFixedHeader(nodeID uint64, msgID uint16, codecID uint16) *FixedHeader {
	return &FixedHeader{
		Magic:   [4]byte{'N', 'X', 'U', 'T'},
		NodeID:  nodeID,
		MsgID:   msgID,
		CodecID: codecID,
	}
}

// Serialize 序列化固定头（16 字节）
func (h *FixedHeader) Serialize() []byte {
	buf := make([]byte, FixedHeaderLen)

	// 写入 Magic (0-3)
	copy(buf[0:4], h.Magic[:])

	// 写入 NodeID (4-11)
	binary.BigEndian.PutUint64(buf[4:12], h.NodeID)

	// 写入 MsgID (12-13)
	binary.BigEndian.PutUint16(buf[12:14], h.MsgID)

	// 写入 CodecID (14-15)
	binary.BigEndian.PutUint16(buf[14:16], h.CodecID)

	return buf
}

// DeserializeFixedHeader 反序列化固定头
func DeserializeFixedHeader(data []byte) (*FixedHeader, error) {
	if len(data) < FixedHeaderLen {
		return nil, types.NewInvalidFrameSizeError(fmt.Sprintf("固定头需要 %d 字节，实际 %d 字节", FixedHeaderLen, len(data)))
	}

	header := &FixedHeader{}

	// 读取 Magic (0-3)
	copy(header.Magic[:], data[0:4])

	// 验证魔术字
	magic := binary.BigEndian.Uint32(data[0:4])
	if magic != MagicNumber {
		return nil, types.NewFrameInvalidMagicError()
	}

	// 读取 NodeID (4-11)
	header.NodeID = binary.BigEndian.Uint64(data[4:12])

	// 读取 MsgID (12-13)
	header.MsgID = binary.BigEndian.Uint16(data[12:14])

	// 读取 CodecID (14-15)
	header.CodecID = binary.BigEndian.Uint16(data[14:16])

	return header, nil
}

// String 返回固定头的字符串表示
func (h *FixedHeader) String() string {
	return fmt.Sprintf("FixedHeader{NodeID=%d, MsgID=%d, CodecID=%d}",
		h.NodeID, h.MsgID, h.CodecID)
}

// ExtFieldType TLV 扩展字段类型
type ExtFieldType uint16

const (
	// ExtCompress 压缩扩展
	ExtCompress ExtFieldType = 1
	// ExtEncrypt 加密扩展
	ExtEncrypt ExtFieldType = 2
	// ExtSegment 分片扩展
	ExtSegment ExtFieldType = 3
	// ExtPriority 优先级扩展
	ExtPriority ExtFieldType = 4
	// ExtCustom 自定义扩展（>= 5）
	ExtCustom ExtFieldType = 5
)

// String 返回扩展字段类型的字符串表示
func (t ExtFieldType) String() string {
	switch t {
	case ExtCompress:
		return "Compress"
	case ExtEncrypt:
		return "Encrypt"
	case ExtSegment:
		return "Segment"
	case ExtPriority:
		return "Priority"
	default:
		return fmt.Sprintf("Custom(%d)", t)
	}
}

// ExtField TLV 扩展字段
//
// ┌─────────────────────────────────────┐
// │ Type (2) │ Length (2) │ Value (N)   │
// ├─────────────────────────────────────┤
// │ Type: 字段类型（ExtFieldType）       │
// │ Length: Value 长度（uint16）        │
// │ Value: 字段值（MessagePack 序列化） │
// └─────────────────────────────────────┘
type ExtField struct {
	// Type 字段类型
	Type ExtFieldType

	// Value 字段值（MessagePack 序列化后的字节流）
	Value []byte
}

// Serialize 序列化扩展字段（4 + len(Value) 字节）
func (f *ExtField) Serialize() []byte {
	totalLen := 4 + len(f.Value) // Type(2) + Length(2) + Value(N)
	buf := make([]byte, totalLen)

	// 写入 Type (0-1)
	binary.BigEndian.PutUint16(buf[0:2], uint16(f.Type))

	// 写入 Length (2-3)
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(f.Value)))

	// 写入 Value (4-)
	if len(f.Value) > 0 {
		copy(buf[4:], f.Value)
	}

	return buf
}

// DeserializeExtField 反序列化扩展字段
func DeserializeExtField(data []byte) (*ExtField, error) {
	if len(data) < 4 {
		return nil, types.NewInvalidFrameSizeError("扩展字段至少需要 4 字节")
	}

	field := &ExtField{}

	// 读取 Type (0-1)
	field.Type = ExtFieldType(binary.BigEndian.Uint16(data[0:2]))

	// 读取 Length (2-3)
	valueLen := binary.BigEndian.Uint16(data[2:4])

	// 验证长度
	totalLen := 4 + int(valueLen)
	if len(data) < totalLen {
		return nil, types.NewInvalidFrameSizeError(fmt.Sprintf("扩展字段需要 %d 字节，实际 %d 字节", totalLen, len(data)))
	}

	// 读取 Value (4-)
	if valueLen > 0 {
		field.Value = make([]byte, valueLen)
		copy(field.Value, data[4:totalLen])
	}

	return field, nil
}

// String 返回扩展字段的字符串表示
func (f *ExtField) String() string {
	return fmt.Sprintf("ExtField{Type=%s, ValueLen=%d}", f.Type, len(f.Value))
}

// VarExtHeader 变长 TLV 扩展头
//
// ┌─────────────────────────────────────────────────────────┐
// │ ExtTotalLen (2) │ TLV Fields (N)                       │
// ├─────────────────────────────────────────────────────────┤
// │ ExtTotalLen: 扩展头总长度（uint16，包括自身）           │
// │ TLV Fields: N 个 TLV 扩展字段                          │
// └─────────────────────────────────────────────────────────┘
type VarExtHeader struct {
	// Fields TLV 扩展字段列表
	Fields []*ExtField
}

// NewVarExtHeader 创建新的变长扩展头
func NewVarExtHeader(fields ...*ExtField) *VarExtHeader {
	return &VarExtHeader{
		Fields: fields,
	}
}

// Serialize 序列化变长扩展头（2 + N 字节）
func (h *VarExtHeader) Serialize() []byte {
	// 先序列化所有字段
	fieldsData := make([][]byte, len(h.Fields))
	totalFieldsLen := 0
	for i, field := range h.Fields {
		fieldData := field.Serialize()
		fieldsData[i] = fieldData
		totalFieldsLen += len(fieldData)
	}

	// 扩展头总长度 = ExtTotalLen(2) + Fields(N)
	totalLen := 2 + totalFieldsLen
	buf := make([]byte, totalLen)

	// 写入 ExtTotalLen (0-1)
	binary.BigEndian.PutUint16(buf[0:2], uint16(totalLen))

	// 写入 Fields (2-)
	offset := 2
	for _, fieldData := range fieldsData {
		copy(buf[offset:], fieldData)
		offset += len(fieldData)
	}

	return buf
}

// DeserializeVarExtHeader 反序列化变长扩展头
func DeserializeVarExtHeader(data []byte) (*VarExtHeader, error) {
	if len(data) < 2 {
		return nil, types.NewInvalidFrameSizeError("扩展头至少需要 2 字节（ExtTotalLen）")
	}

	// 读取 ExtTotalLen (0-1)
	extTotalLen := int(binary.BigEndian.Uint16(data[0:2]))

	// 验证长度
	if len(data) < extTotalLen {
		return nil, types.NewInvalidFrameSizeError(fmt.Sprintf("扩展头需要 %d 字节，实际 %d 字节", extTotalLen, len(data)))
	}

	if extTotalLen > MaxExtHeaderSize {
		return nil, types.NewFrameTooLargeError(extTotalLen)
	}

	header := &VarExtHeader{
		Fields: make([]*ExtField, 0),
	}

	// 解析 TLV 字段 (2-ExtTotalLen)
	offset := 2
	for offset < extTotalLen {
		field, err := DeserializeExtField(data[offset:])
		if err != nil {
			return nil, fmt.Errorf("解析扩展字段失败: %w", err)
		}

		header.Fields = append(header.Fields, field)

		// 移动到下一个字段
		fieldLen := 4 + len(field.Value) // Type(2) + Length(2) + Value(N)
		offset += fieldLen
	}

	return header, nil
}

// GetField 获取指定类型的扩展字段
func (h *VarExtHeader) GetField(fieldType ExtFieldType) *ExtField {
	for _, field := range h.Fields {
		if field.Type == fieldType {
			return field
		}
	}
	return nil
}

// AddField 添加扩展字段
func (h *VarExtHeader) AddField(field *ExtField) {
	h.Fields = append(h.Fields, field)
}

// Size 返回扩展头的总大小（字节）
func (h *VarExtHeader) Size() int {
	size := 2 // ExtTotalLen
	for _, field := range h.Fields {
		size += 4 + len(field.Value) // Type(2) + Length(2) + Value(N)
	}
	return size
}

// String 返回扩展头的字符串表示
func (h *VarExtHeader) String() string {
	return fmt.Sprintf("VarExtHeader{FieldsCount=%d, Size=%d}", len(h.Fields), h.Size())
}

// TLVMessage TLV 协议消息
//
// 完整的 TLV 消息结构：
// ┌─────────────────────────────────────────────────────────┐
// │ FixedHeader (16) │ VarExtHeader (变长) │ Data (变长)     │
// ├─────────────────────────────────────────────────────────┤
// │ CRC32 (4)                                                 │
// └─────────────────────────────────────────────────────────┘
type TLVMessage struct {
	// FixedHeader 固定头（16 字节）
	FixedHeader *FixedHeader

	// VarExtHeader 变长扩展头
	VarExtHeader *VarExtHeader

	// Data 业务数据（MessagePack/JSON/Protobuf 序列化）
	Data []byte

	// CRC32 校验和（覆盖 VarExtHeader + Data）
	CRC32 uint32
}

// NewTLVMessage 创建新的 TLV 消息
func NewTLVMessage(nodeID uint64, msgID uint16, codecID uint16, data []byte) *TLVMessage {
	msg := &TLVMessage{
		FixedHeader:  NewFixedHeader(nodeID, msgID, codecID),
		VarExtHeader: NewVarExtHeader(),
		Data:         data,
	}

	// 计算 CRC32
	msg.recalculateCRC32()

	return msg
}

// recalculateCRC32 重新计算 CRC32 校验和
//
// 计算范围：VarExtHeader + Data（不包括 FixedHeader 和 CRC32 字段本身）
func (m *TLVMessage) recalculateCRC32() {
	// 序列化 VarExtHeader
	extHeaderData := m.VarExtHeader.Serialize()

	// 计算校验和：VarExtHeader + Data
	crc := crc32.ChecksumIEEE(extHeaderData)         // VarExtHeader
	crc = crc32.Update(crc, crc32.IEEETable, m.Data) // Data
	m.CRC32 = crc
}

// Marshal 序列化 TLV 消息
func (m *TLVMessage) Marshal() ([]byte, error) {
	// 验证大小
	totalSize := m.Size()
	if totalSize > MaxTLVMessageSize {
		return nil, types.NewFrameTooLargeError(totalSize)
	}

	// 重新计算 CRC32
	m.recalculateCRC32()

	// 序列化各部分
	fixedHeaderData := m.FixedHeader.Serialize()
	extHeaderData := m.VarExtHeader.Serialize()

	// 组装完整消息
	buf := make([]byte, totalSize)
	offset := 0

	// 写入 FixedHeader (0-15)
	copy(buf[offset:], fixedHeaderData)
	offset += FixedHeaderLen

	// 写入 VarExtHeader (16-)
	copy(buf[offset:], extHeaderData)
	offset += len(extHeaderData)

	// 写入 Data
	copy(buf[offset:], m.Data)
	offset += len(m.Data)

	// 写入 CRC32
	binary.BigEndian.PutUint32(buf[offset:], m.CRC32)

	return buf, nil
}

// Unmarshal 反序列化 TLV 消息
func (m *TLVMessage) Unmarshal(data []byte) error {
	if len(data) < FixedHeaderLen+CRCLen {
		return types.NewInvalidFrameSizeError(fmt.Sprintf("TLV 消息至少需要 %d 字节，实际 %d 字节", FixedHeaderLen+CRCLen, len(data)))
	}

	// 解析 FixedHeader (0-15)
	fixedHeader, err := DeserializeFixedHeader(data[0:FixedHeaderLen])
	if err != nil {
		return fmt.Errorf("解析固定头失败: %w", err)
	}
	m.FixedHeader = fixedHeader

	// 解析 VarExtHeader (16-)
	varExtHeader, err := DeserializeVarExtHeader(data[FixedHeaderLen:])
	if err != nil {
		return fmt.Errorf("解析扩展头失败: %w", err)
	}
	m.VarExtHeader = varExtHeader

	// 计算 Data 起始位置
	extHeaderLen := m.VarExtHeader.Size()
	dataOffset := FixedHeaderLen + extHeaderLen
	crcOffset := len(data) - CRCLen

	// 验证长度
	if crcOffset < dataOffset {
		return types.NewInvalidFrameSizeError("消息格式错误：没有数据部分")
	}

	// 读取 Data
	if crcOffset > dataOffset {
		m.Data = make([]byte, crcOffset-dataOffset)
		copy(m.Data, data[dataOffset:crcOffset])
	} else {
		m.Data = nil
	}

	// 读取 CRC32
	m.CRC32 = binary.BigEndian.Uint32(data[crcOffset:])

	// 验证 CRC32
	if !m.verifyCRC32(data) {
		return types.NewFrameChecksumError()
	}

	return nil
}

// verifyCRC32 验证 CRC32 校验和
func (m *TLVMessage) verifyCRC32(data []byte) bool {
	// 重新计算校验和：VarExtHeader + Data
	dataOffset := FixedHeaderLen
	crcOffset := len(data) - CRCLen

	// VarExtHeader + Data
	crcData := data[dataOffset:crcOffset]
	calculated := crc32.ChecksumIEEE(crcData)

	return m.CRC32 == calculated
}

// Size 返回 TLV 消息的总大小（字节）
func (m *TLVMessage) Size() int {
	return FixedHeaderLen + m.VarExtHeader.Size() + len(m.Data) + CRCLen
}

// String 返回 TLV 消息的字符串表示
func (m *TLVMessage) String() string {
	return fmt.Sprintf("TLVMessage{FixedHeader=%s, VarExtHeader=%s, DataLen=%d, CRC32=%08x}",
		m.FixedHeader, m.VarExtHeader, len(m.Data), m.CRC32)
}

// HexDump 返回 TLV 消息的十六进制转储
func (m *TLVMessage) HexDump() string {
	data, err := m.Marshal()
	if err != nil {
		return fmt.Sprintf("Error marshaling TLV message: %v", err)
	}
	return hex.Dump(data)
}

// TLVMessageReader TLV 消息读取器
//
// 用于从流中读取完整 TLV 消息
type TLVMessageReader struct {
	r io.Reader
}

// NewTLVMessageReader 创建 TLV 消息读取器
func NewTLVMessageReader(r io.Reader) *TLVMessageReader {
	return &TLVMessageReader{r: r}
}

// ReadMessage 读取一条 TLV 消息
func (r *TLVMessageReader) ReadMessage() (*TLVMessage, error) {
	// 读取固定头（16 字节）
	fixedHeaderData := make([]byte, FixedHeaderLen)
	if _, err := io.ReadFull(r.r, fixedHeaderData); err != nil {
		return nil, err
	}

	// 解析固定头
	_, err := DeserializeFixedHeader(fixedHeaderData)
	if err != nil {
		return nil, fmt.Errorf("解析固定头失败: %w", err)
	}

	// 读取扩展头长度（2 字节）
	extTotalLenData := make([]byte, 2)
	if _, err := io.ReadFull(r.r, extTotalLenData); err != nil {
		return nil, err
	}
	extTotalLen := int(binary.BigEndian.Uint16(extTotalLenData))

	// 读取扩展头剩余部分（如果有）
	var extHeaderData []byte
	if extTotalLen > 2 {
		extHeaderRemaining := make([]byte, extTotalLen-2)
		if _, err := io.ReadFull(r.r, extHeaderRemaining); err != nil {
			return nil, err
		}
		extHeaderData = make([]byte, extTotalLen)
		copy(extHeaderData[0:2], extTotalLenData)
		copy(extHeaderData[2:], extHeaderRemaining)
	} else {
		extHeaderData = extTotalLenData
	}

	// 读取剩余数据（Data + CRC32）
	// 使用临时缓冲区读取所有剩余数据
	remainingData := make([]byte, 4096) // 初始缓冲区
	totalRead := 0

	for {
		n, err := r.r.Read(remainingData[totalRead:])
		if n > 0 {
			totalRead += n
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if totalRead >= len(remainingData) {
			// 缓冲区已满，可能还有更多数据
			newBuf := make([]byte, len(remainingData)*2)
			copy(newBuf, remainingData)
			remainingData = newBuf
		}
	}

	remainingData = remainingData[:totalRead]

	// 组装完整消息
	fullData := make([]byte, FixedHeaderLen+extTotalLen+len(remainingData))
	copy(fullData[0:FixedHeaderLen], fixedHeaderData)
	copy(fullData[FixedHeaderLen:], extHeaderData)
	copy(fullData[FixedHeaderLen+extTotalLen:], remainingData)

	msg := &TLVMessage{}
	if err := msg.Unmarshal(fullData); err != nil {
		return nil, err
	}

	return msg, nil
}

// TLVMessageWriter TLV 消息写入器
//
// 用于向流中写入完整 TLV 消息
type TLVMessageWriter struct {
	w io.Writer
}

// NewTLVMessageWriter 创建 TLV 消息写入器
func NewTLVMessageWriter(w io.Writer) *TLVMessageWriter {
	return &TLVMessageWriter{w: w}
}

// WriteMessage 写入一条 TLV 消息
func (w *TLVMessageWriter) WriteMessage(msg *TLVMessage) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}

	_, err = w.w.Write(data)
	if err != nil {
		return types.NewOpErr(types.ErrCodeTransport, "WriteMessage", "写入 TLV 消息失败", err)
	}
	return nil
}
