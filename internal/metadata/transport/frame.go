// Package transport TLV (Type-Length-Value) 协议的帧格式实现
//
// 协议格式:
// [FixedHeader 31 bytes][VarExtHeader ExtHeaderLen bytes][Data DataLength bytes][CRC32 4 bytes]
//
// FixedHeader (31 bytes):
//   - Magic: 4 bytes ("NXUT")
//   - Version: 1 byte (协议版本，当前为 1)
//   - NodeID: 8 bytes (发送节点 ID)
//   - MsgSeq: 8 bytes (消息序列号)
//   - MsgType: 2 bytes (消息类型)
//   - CodecID: 2 bytes (编码器 ID，指定 Data 使用的编解码器)
//   - ExtHeaderLen: 2 bytes (扩展头长度，0 表示无扩展头)
//   - DataLength: 4 bytes (数据长度)
//
// VarExtHeader (变长 TLV 扩展头):
//   - TLV Fields: N 个 TLV 字段
//   - Type: 2 bytes (字段类型)
//   - Length: 2 bytes (Value 长度)
//   - Value: N bytes (固定使用 MessagePack 序列化)
//
// Data (变长):
//   - 业务数据，根据 FixedHeader.CodecID 选择编解码器
//   - CodecID=1: MessagePack
//   - CodecID=2: JSON
//   - CodecID=3: Protobuf
//
// CRC32 (4 bytes):
//   - 校验 VarExtHeader + Data
package transport

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	// FixedHeaderLen 固定头长度（31 字节）
	// Magic(4) + Version(1) + NodeID(8) + MsgSeq(8) + MsgType(2) + CodecID(2) + ExtHeaderLen(2) + DataLength(4) = 31
	FixedHeaderLen = 31

	// MagicNumber TLV 协议魔术字 "NXUT" (0x4E 0x58 0x55 0x54)
	MagicNumber = 0x4E585554

	// CRCLen CRC32 长度（4 字节）
	CRCLen = 4

	// UDPSafePayloadSize UDP 安全负载大小（1400 字节）
	// Ethernet MTU 1500 - IP 头 20 - UDP 头 8 - 安全余量 72
	UDPSafePayloadSize = 1400

	// MaxExtHeaderSize 最大扩展头大小（1KB）
	MaxExtHeaderSize = 1024

	// MaxFrameSize 最大帧大小（10MB）
	MaxFrameSize = 10 * 1024 * 1024
)

// FixedHeader 固定头结构（31 字节）
type FixedHeader struct {
	// Magic 魔术字（固定为 "NXUT"）
	Magic [4]byte

	// Version 协议版本（当前为 1，用于协议升级和向后兼容）
	Version uint8

	// NodeID 发送节点 ID
	NodeID uint64

	// MsgSeq 消息序列号（用于去重和匹配请求响应）
	MsgSeq uint64

	// MsgType 消息类型
	MsgType MessageType

	// CodecID 编解码器 ID（1=MessagePack, 2=JSON, 3=Protobuf）
	CodecID uint16

	// ExtHeaderLen 扩展头长度（字节，0 表示无扩展头）
	ExtHeaderLen uint16

	// DataLength 数据长度（字节）
	DataLength uint32
}

// NewFixedHeader 创建新的固定头
// 注意：ExtHeaderLen 和 DataLength 会在 Marshal 时自动计算
func NewFixedHeader(nodeID uint64, msgSeq uint64, msgType MessageType, codecID uint16) *FixedHeader {
	return &FixedHeader{
		Magic:        [4]byte{'N', 'X', 'U', 'T'},
		Version:      1, // 当前协议版本为 1
		NodeID:       nodeID,
		MsgSeq:       msgSeq,
		MsgType:      msgType,
		CodecID:      codecID,
		ExtHeaderLen: 0, // 默认无扩展头
		DataLength:   0, // Marshal 时计算
	}
}

// Serialize 序列化固定头（31 字节）
func (h *FixedHeader) Serialize() []byte {
	buf := make([]byte, FixedHeaderLen)

	// 写入 Magic (0-3)
	copy(buf[0:4], h.Magic[:])

	// 写入 Version (4)
	buf[4] = h.Version

	// 写入 NodeID (5-12)
	binary.BigEndian.PutUint64(buf[5:13], h.NodeID)

	// 写入 MsgSeq (13-20)
	binary.BigEndian.PutUint64(buf[13:21], h.MsgSeq)

	// 写入 MsgType (21-22)
	binary.BigEndian.PutUint16(buf[21:23], uint16(h.MsgType))

	// 写入 CodecID (23-24)
	binary.BigEndian.PutUint16(buf[23:25], h.CodecID)

	// 写入 ExtHeaderLen (25-26)
	binary.BigEndian.PutUint16(buf[25:27], h.ExtHeaderLen)

	// 写入 DataLength (27-30)
	binary.BigEndian.PutUint32(buf[27:31], h.DataLength)

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

	// 读取 Version (4)
	header.Version = data[4]

	// 读取 NodeID (5-12)
	header.NodeID = binary.BigEndian.Uint64(data[5:13])

	// 读取 MsgSeq (13-20)
	header.MsgSeq = binary.BigEndian.Uint64(data[13:21])

	// 读取 MsgType (21-22)
	header.MsgType = MessageType(binary.BigEndian.Uint16(data[21:23]))

	// 读取 CodecID (23-24)
	header.CodecID = binary.BigEndian.Uint16(data[23:25])

	// 读取 ExtHeaderLen (25-26)
	header.ExtHeaderLen = binary.BigEndian.Uint16(data[25:27])

	// 读取 DataLength (27-30)
	header.DataLength = binary.BigEndian.Uint32(data[27:31])

	return header, nil
}

// String 返回固定头的字符串表示
func (h *FixedHeader) String() string {
	return fmt.Sprintf("FixedHeader{NodeID=%d, MsgSeq=%d, MsgType=%d, CodecID=%d}",
		h.NodeID, h.MsgSeq, h.MsgType, h.CodecID)
}

// ExtFieldType TLV 扩展字段类型
type ExtFieldType uint16

const (
	// ExtCompress 压缩扩展
	ExtCompress ExtFieldType = 1
	// ExtEncrypt 加密扩展
	ExtEncrypt ExtFieldType = 2
	// ExtFragment 分片扩展（UDP 分片使用：index + total）
	ExtFragment ExtFieldType = 3
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
	case ExtFragment:
		return "Fragment"
	case ExtPriority:
		return "Priority"
	default:
		return fmt.Sprintf("Custom(%d)", t)
	}
}

// ExtField TLV 扩展字段
type ExtField struct {
	// Type 字段类型
	Type ExtFieldType

	// Value 字段值（固定使用 MessagePack 序列化）
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

// Serialize 序列化变长扩展头（N 字节）
// 注意：不再包含 ExtTotalLen 字段（已移到 FixedHeader.ExtHeaderLen）
func (h *VarExtHeader) Serialize() []byte {
	// 先序列化所有字段
	fieldsData := make([][]byte, len(h.Fields))
	totalFieldsLen := 0
	for i, field := range h.Fields {
		fieldData := field.Serialize()
		fieldsData[i] = fieldData
		totalFieldsLen += len(fieldData)
	}

	// 扩展头只包含 Fields（不再包含 ExtTotalLen）
	buf := make([]byte, totalFieldsLen)

	// 写入 Fields
	offset := 0
	for _, fieldData := range fieldsData {
		copy(buf[offset:], fieldData)
		offset += len(fieldData)
	}

	return buf
}

// DeserializeVarExtHeader 反序列化变长扩展头
// 注意：不再从 data 中读取 ExtTotalLen（由调用者从 FixedHeader.ExtHeaderLen 获取）
func DeserializeVarExtHeader(data []byte) (*VarExtHeader, error) {
	// 验证长度
	if len(data) > MaxExtHeaderSize {
		return nil, types.NewFrameTooLargeError(len(data))
	}

	header := &VarExtHeader{
		Fields: make([]*ExtField, 0),
	}

	// 解析 TLV 字段
	offset := 0
	for offset < len(data) {
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

// Frame 协议帧
//
// 完整的帧结构：
// [FixedHeader 31 bytes][VarExtHeader ExtHeaderLen bytes][Data DataLength bytes][CRC32 4 bytes]
type Frame struct {
	// FixedHeader 固定头（31 字节）
	FixedHeader *FixedHeader

	// VarExtHeader 变长扩展头
	VarExtHeader *VarExtHeader

	// Data 业务数据（根据 CodecID 选择编解码器）
	Data []byte

	// CRC32 校验和（覆盖 VarExtHeader + Data）
	CRC32 uint32
}

// NewFrame 创建新的帧
func NewFrame(nodeID uint64, msgSeq uint64, msgType MessageType, codecID uint16, data []byte) *Frame {
	return &Frame{
		FixedHeader:  NewFixedHeader(nodeID, msgSeq, msgType, codecID),
		VarExtHeader: NewVarExtHeader(),
		Data:         data,
	}
}

// ========================================
// Frame Builder 模式 - 链式调用
// ========================================

// WithCompress 添加压缩扩展字段
func (f *Frame) WithCompress(compressID uint16) *Frame {
	f.VarExtHeader.AddField(EncodeCompressExt(compressID))
	return f
}

// WithFragment 添加分片扩展字段
func (f *Frame) WithFragment(index, total uint16) *Frame {
	f.VarExtHeader.AddField(EncodeFragmentExt(index, total))
	return f
}

// WithEncrypt 添加加密扩展字段
func (f *Frame) WithEncrypt(encryptID uint16, nonce []byte, version string) *Frame {
	field, err := EncodeEncryptExt(encryptID, nonce, version)
	if err != nil {
		// 加密扩展编码失败，记录警告但不中断链式调用
		logging.Warnf("编码加密扩展失败: %v", err)
		return f
	}
	f.VarExtHeader.AddField(field)
	return f
}

// WithPriority 添加优先级扩展字段
func (f *Frame) WithPriority(priority types.Priority) *Frame {
	f.VarExtHeader.AddField(EncodePriorityExt(priority))
	return f
}

// Finalize 完成 Frame 构建并计算 CRC32
//
// 在添加所有扩展字段后必须调用此方法
func (f *Frame) Finalize() *Frame {
	f.recalculateCRC32()
	return f
}

// recalculateCRC32 重新计算 CRC32 校验和
//
// 计算范围：VarExtHeader + Data（不包括 FixedHeader 和 CRC32 字段本身）
func (f *Frame) recalculateCRC32() {
	// 序列化 VarExtHeader
	extHeaderData := f.VarExtHeader.Serialize()

	// 计算校验和：VarExtHeader + Data
	crc := crc32.ChecksumIEEE(extHeaderData)         // VarExtHeader
	crc = crc32.Update(crc, crc32.IEEETable, f.Data) // Data
	f.CRC32 = crc
}

// Marshal 序列化帧
func (f *Frame) Marshal() ([]byte, error) {
	// 序列化 VarExtHeader 以获取其实际大小
	extHeaderData := f.VarExtHeader.Serialize()
	extHeaderSize := len(extHeaderData)

	// 计算 ExtHeaderLen 和 DataLength
	f.FixedHeader.ExtHeaderLen = uint16(extHeaderSize)
	f.FixedHeader.DataLength = uint32(len(f.Data))

	// 验证总大小
	totalSize := FixedHeaderLen + extHeaderSize + len(f.Data) + CRCLen
	if totalSize > MaxFrameSize {
		return nil, types.NewFrameTooLargeError(totalSize)
	}

	// 重新计算 CRC32
	f.recalculateCRC32()

	// 序列化 FixedHeader（包含已计算的 ExtHeaderLen 和 DataLength）
	fixedHeaderData := f.FixedHeader.Serialize()

	// 组装完整帧
	buf := make([]byte, totalSize)
	offset := 0

	// 写入 FixedHeader
	copy(buf[offset:], fixedHeaderData)
	offset += FixedHeaderLen

	// 写入 VarExtHeader
	copy(buf[offset:], extHeaderData)
	offset += extHeaderSize

	// 写入 Data
	copy(buf[offset:], f.Data)
	offset += len(f.Data)

	// 写入 CRC32
	binary.BigEndian.PutUint32(buf[offset:], f.CRC32)

	return buf, nil
}

// Unmarshal 反序列化帧
func (f *Frame) Unmarshal(data []byte) error {
	if len(data) < FixedHeaderLen+CRCLen {
		return types.NewInvalidFrameSizeError(fmt.Sprintf("帧至少需要 %d 字节，实际 %d 字节", FixedHeaderLen+CRCLen, len(data)))
	}

	// 解析 FixedHeader (0-30)
	fixedHeader, err := DeserializeFixedHeader(data[0:FixedHeaderLen])
	if err != nil {
		return fmt.Errorf("解析固定头失败: %w", err)
	}
	f.FixedHeader = fixedHeader

	// 从 FixedHeader 获取 ExtHeaderLen 和 DataLength
	extHeaderLen := int(f.FixedHeader.ExtHeaderLen)
	dataLength := int(f.FixedHeader.DataLength)

	// 验证数据长度
	expectedLen := FixedHeaderLen + extHeaderLen + dataLength + CRCLen
	if len(data) != expectedLen {
		return types.NewInvalidFrameSizeError(fmt.Sprintf("帧长度不匹配，期望 %d 字节，实际 %d 字节", expectedLen, len(data)))
	}

	// 解析 VarExtHeader
	var extHeaderData []byte
	if extHeaderLen > 0 {
		extHeaderData = data[FixedHeaderLen : FixedHeaderLen+extHeaderLen]
		varExtHeader, err := DeserializeVarExtHeader(extHeaderData)
		if err != nil {
			return fmt.Errorf("解析扩展头失败: %w", err)
		}
		f.VarExtHeader = varExtHeader
	} else {
		f.VarExtHeader = NewVarExtHeader()
	}

	// 读取 Data
	dataOffset := FixedHeaderLen + extHeaderLen
	if dataLength > 0 {
		f.Data = make([]byte, dataLength)
		copy(f.Data, data[dataOffset:dataOffset+dataLength])
	} else {
		f.Data = nil
	}

	// 读取 CRC32
	crcOffset := dataOffset + dataLength
	f.CRC32 = binary.BigEndian.Uint32(data[crcOffset : crcOffset+CRCLen])

	// 验证 CRC32
	if !f.verifyCRC32(data) {
		return types.NewFrameChecksumError()
	}

	return nil
}

// verifyCRC32 验证 CRC32 校验和
func (f *Frame) verifyCRC32(data []byte) bool {
	// 重新计算校验和：VarExtHeader + Data
	dataOffset := FixedHeaderLen
	crcOffset := len(data) - CRCLen

	// VarExtHeader + Data
	crcData := data[dataOffset:crcOffset]
	calculated := crc32.ChecksumIEEE(crcData)

	return f.CRC32 == calculated
}

// ValidateCRC32 验证 CRC32 校验和（公开方法）
//
// 用于外部调用验证帧的 CRC32 是否正确。
// 返回 nil 表示校验通过，返回 error 表示校验失败。
//
// CRC32 校验范围：VarExtHeader + Data
// 注意：不包括 FixedHeader 和 CRC32 字段本身
func (f *Frame) ValidateCRC32() error {
	// 序列化 VarExtHeader
	extHeaderData := f.VarExtHeader.Serialize()

	// 计算校验和：VarExtHeader + Data
	crc := crc32.ChecksumIEEE(extHeaderData)         // VarExtHeader
	crc = crc32.Update(crc, crc32.IEEETable, f.Data) // Data

	if f.CRC32 != crc {
		return types.NewFrameChecksumError()
	}
	return nil
}

// GetCRCScope 返回 CRC32 校验覆盖的数据范围
//
// 返回的数据包含：VarExtHeader + Data
// 用于调试和验证 CRC32 校验范围是否正确
func (f *Frame) GetCRCScope() []byte {
	// 序列化 VarExtHeader
	extHeaderData := f.VarExtHeader.Serialize()

	// 组合 VarExtHeader + Data
	result := make([]byte, 0, len(extHeaderData)+len(f.Data))
	result = append(result, extHeaderData...)
	result = append(result, f.Data...)

	return result
}

// Size 返回帧的总大小（字节）
func (f *Frame) Size() int {
	return FixedHeaderLen + f.VarExtHeader.Size() + len(f.Data) + CRCLen
}

// String 返回帧的字符串表示
func (f *Frame) String() string {
	return fmt.Sprintf("Frame{FixedHeader=%s, VarExtHeader=%s, DataLen=%d, CRC32=%08x}",
		f.FixedHeader, f.VarExtHeader, len(f.Data), f.CRC32)
}

// HexDump 返回帧的十六进制转储
func (f *Frame) HexDump() string {
	data, err := f.Marshal()
	if err != nil {
		return fmt.Sprintf("Error marshaling frame: %v", err)
	}
	return hex.Dump(data)
}

// FrameReader 帧读取器
type FrameReader struct {
	r io.Reader
}

// NewFrameReader 创建帧读取器
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{r: r}
}

// ReadFrame 读取一帧
func (fr *FrameReader) ReadFrame() (*Frame, error) {
	// 读取固定头（31 字节）
	fixedHeaderData := make([]byte, FixedHeaderLen)
	if _, err := io.ReadFull(fr.r, fixedHeaderData); err != nil {
		return nil, err
	}

	// 解析固定头
	fixedHeader, err := DeserializeFixedHeader(fixedHeaderData)
	if err != nil {
		return nil, fmt.Errorf("解析固定头失败: %w", err)
	}

	// 从固定头中获取 ExtHeaderLen 和 DataLength
	extHeaderLen := int(fixedHeader.ExtHeaderLen)
	dataLength := int(fixedHeader.DataLength)

	// 验证长度的合理性
	totalSize := FixedHeaderLen + extHeaderLen + dataLength + CRCLen
	if totalSize > MaxFrameSize {
		return nil, types.NewFrameTooLargeError(totalSize)
	}

	// 计算需要读取的总字节数（不包括 FixedHeader）
	remainingSize := extHeaderLen + dataLength + CRCLen

	// 读取剩余数据（VarExtHeader + Data + CRC32）
	remainingData := make([]byte, remainingSize)
	if _, err := io.ReadFull(fr.r, remainingData); err != nil {
		return nil, fmt.Errorf("读取帧数据失败: %w", err)
	}

	// 组装完整帧
	fullData := make([]byte, totalSize)
	copy(fullData[0:FixedHeaderLen], fixedHeaderData)
	copy(fullData[FixedHeaderLen:], remainingData)

	frame := &Frame{}
	if err := frame.Unmarshal(fullData); err != nil {
		return nil, err
	}

	return frame, nil
}

// FrameWriter 帧写入器
type FrameWriter struct {
	w io.Writer
}

// NewFrameWriter 创建帧写入器
func NewFrameWriter(w io.Writer) *FrameWriter {
	return &FrameWriter{w: w}
}

// WriteFrame 写入一帧
func (fw *FrameWriter) WriteFrame(frame *Frame) error {
	data, err := frame.Marshal()
	if err != nil {
		return err
	}

	_, err = fw.w.Write(data)
	if err != nil {
		return types.NewOpErr(types.ErrCodeTransport, "WriteFrame", "写入帧失败", err)
	}
	return nil
}

// ========================================
// TLV 扩展字段编解码辅助函数
// ========================================

// compressExtData 压缩扩展数据（MessagePack 序列化用）
type compressExtData struct {
	CompressID uint16 `msgpack:"cid"`
}

// EncodeCompressExt 编码压缩扩展
func EncodeCompressExt(compressID uint16) *ExtField {
	data := compressExtData{CompressID: compressID}

	bytes, err := msgpack.Marshal(data)
	if err != nil {
		// 压缩扩展编码失败不应发生，直接panic
		panic(fmt.Sprintf("序列化压缩扩展失败: %v", err))
	}

	return &ExtField{
		Type:  ExtCompress,
		Value: bytes,
	}
}

// encryptExtData 加密扩展数据（MessagePack 序列化用）
type encryptExtData struct {
	EncryptID uint16 `msgpack:"eid"`
	Nonce     []byte `msgpack:"non"`
	Version   string `msgpack:"ver"`
}

// DecodeCompressExt 解码压缩扩展
func DecodeCompressExt(field *ExtField) (uint16, error) {
	var data compressExtData

	if err := msgpack.Unmarshal(field.Value, &data); err != nil {
		return 0, fmt.Errorf("反序列化压缩扩展失败: %w", err)
	}

	return data.CompressID, nil
}

// EncodeEncryptExt 编码加密扩展（使用 MessagePack）
func EncodeEncryptExt(encryptID uint16, nonce []byte, version string) (*ExtField, error) {
	// 使用 MessagePack 序列化
	data := encryptExtData{
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

// DecodeEncryptExt 解码加密扩展（使用 MessagePack）
func DecodeEncryptExt(field *ExtField) (encryptID uint16, nonce []byte, version string, err error) {
	var data encryptExtData

	if err := msgpack.Unmarshal(field.Value, &data); err != nil {
		return 0, nil, "", fmt.Errorf("反序列化加密扩展失败: %w", err)
	}

	return data.EncryptID, data.Nonce, data.Version, nil
}

// priorityExtData 优先级扩展数据（MessagePack 序列化用）
type priorityExtData struct {
	Priority types.Priority `msgpack:"pri"`
}

// EncodePriorityExt 编码优先级扩展
func EncodePriorityExt(priority types.Priority) *ExtField {
	data := priorityExtData{Priority: priority}

	bytes, err := msgpack.Marshal(data)
	if err != nil {
		// 优先级扩展编码失败不应发生，直接panic
		panic(fmt.Sprintf("序列化优先级扩展失败: %v", err))
	}

	return &ExtField{
		Type:  ExtPriority,
		Value: bytes,
	}
}

// DecodePriorityExt 解码优先级扩展
func DecodePriorityExt(field *ExtField) (types.Priority, error) {
	var data priorityExtData

	if err := msgpack.Unmarshal(field.Value, &data); err != nil {
		return 0, fmt.Errorf("反序列化优先级扩展失败: %w", err)
	}

	return data.Priority, nil
}

// fragmentExtData 分片扩展数据（MessagePack 序列化用）
type fragmentExtData struct {
	Index uint16 `msgpack:"idx"` // 当前分片索引（从 0 开始）
	Total uint16 `msgpack:"tot"` // 总分片数
}

// EncodeFragmentExt 编码分片扩展
func EncodeFragmentExt(index, total uint16) *ExtField {
	data := fragmentExtData{
		Index: index,
		Total: total,
	}

	bytes, err := msgpack.Marshal(data)
	if err != nil {
		// 分片扩展编码失败不应发生，直接panic
		panic(fmt.Sprintf("序列化分片扩展失败: %v", err))
	}

	return &ExtField{
		Type:  ExtFragment,
		Value: bytes,
	}
}

// DecodeFragmentExt 解码分片扩展
func DecodeFragmentExt(field *ExtField) (index, total uint16, err error) {
	var data fragmentExtData

	if err := msgpack.Unmarshal(field.Value, &data); err != nil {
		return 0, 0, fmt.Errorf("反序列化分片扩展失败: %w", err)
	}

	return data.Index, data.Total, nil
}
