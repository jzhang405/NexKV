// Package bftree 提供 Bf-Tree 页面压缩功能
package bftree

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/golang/snappy"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

// CompressionType 压缩算法类型
type CompressionType string

const (
	CompressionNone  CompressionType = "none"  // 不压缩
	CompressionSnappy CompressionType = "snappy" // Snappy 压缩
	CompressionLZ4    CompressionType = "lz4"    // LZ4 压缩
	CompressionZSTD   CompressionType = "zstd"   // ZSTD 压缩
)

// Compressor 压缩器接口
type Compressor interface {
	// Compress 压缩数据
	Compress(data []byte) ([]byte, error)

	// Decompress 解压数据
	Decompress(data []byte) ([]byte, error)

	// Type 返回压缩类型
	Type() CompressionType
}

// NoneCompressor 不压缩（空对象模式）
type NoneCompressor struct{}

// Compress 直接返回原数据
func (c *NoneCompressor) Compress(data []byte) ([]byte, error) {
	return data, nil
}

// Decompress 直接返回原数据
func (c *NoneCompressor) Decompress(data []byte) ([]byte, error) {
	return data, nil
}

// Type 返回压缩类型
func (c *NoneCompressor) Type() CompressionType {
	return CompressionNone
}

// SnappyCompressor Snappy 压缩器
//
// 特点：
// - 高速压缩和解压
// - 压缩比中等（~2x）
// - 适合实时场景
type SnappyCompressor struct{}

// Compress 使用 Snappy 压缩
func (c *SnappyCompressor) Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// Snappy 压缩
	compressed := snappy.Encode(nil, data)
	return compressed, nil
}

// Decompress 使用 Snappy 解压
func (c *SnappyCompressor) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// Snappy 解压
	decompressed, err := snappy.Decode(nil, data)
	if err != nil {
		return nil, fmt.Errorf("snappy decompress failed: %w", err)
	}
	return decompressed, nil
}

// Type 返回压缩类型
func (c *SnappyCompressor) Type() CompressionType {
	return CompressionSnappy
}

// LZ4Compressor LZ4 压缩器
//
// 特点：
// - 极速压缩和解压
// - 压缩比低于 Snappy（~1.5x）
// - 适合对延迟敏感的场景
type LZ4Compressor struct {
	compressor *lz4.Compressor
}

// NewLZ4Compressor 创建 LZ4 压缩器
func NewLZ4Compressor() *LZ4Compressor {
	return &LZ4Compressor{
		compressor: &lz4.Compressor{},
	}
}

// Compress 使用 LZ4 压缩
func (c *LZ4Compressor) Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// LZ4 压缩
	compressed := make([]byte, lz4.CompressBlockBound(len(data)))
	n, err := c.compressor.CompressBlock(data, compressed)
	if err != nil {
		return nil, fmt.Errorf("lz4 compress failed: %w", err)
	}

	// 只返回有效数据
	return compressed[:n], nil
}

// Decompress 使用 LZ4 解压
func (c *LZ4Compressor) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// LZ4 解压（需要知道原始大小，但这里我们不知道）
	// 为了简化，我们使用一个固定大小的缓冲区
	decompressed := make([]byte, 16*1024) // 16KB 最大页面
	n, err := lz4.UncompressBlock(data, decompressed)
	if err != nil {
		return nil, fmt.Errorf("lz4 decompress failed: %w", err)
	}

	return decompressed[:n], nil
}

// Type 返回压缩类型
func (c *LZ4Compressor) Type() CompressionType {
	return CompressionLZ4
}

// ZSTDCompressor ZSTD 压缩器
//
// 特点：
// - 高压缩比（~3-5x）
// - 压缩速度中等，解压速度快
// - 适合存储密集型场景
type ZSTDCompressor struct {
	encoder *zstd.Encoder
	decoder *zstd.Decoder
}

// NewZSTDCompressor 创建 ZSTD 压缩器
func NewZSTDCompressor(level int) (*ZSTDCompressor, error) {
	// 创建 encoder
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)))
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
	}

	// 创建 decoder
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
	}

	return &ZSTDCompressor{
		encoder: encoder,
		decoder: decoder,
	}, nil
}

// Compress 使用 ZSTD 压缩
func (c *ZSTDCompressor) Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// ZSTD 压缩
	compressed := c.encoder.EncodeAll(data, nil)
	return compressed, nil
}

// Decompress 使用 ZSTD 解压
func (c *ZSTDCompressor) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// ZSTD 解压
	decompressed, err := c.decoder.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd decompress failed: %w", err)
	}
	return decompressed, nil
}

// Type 返回压缩类型
func (c *ZSTDCompressor) Type() CompressionType {
	return CompressionZSTD
}

// CompressedPage 压缩页面格式
//
// 格式：
// +-------------------+
// | Magic (4 bytes)    | 0x425A4350 ("BZCP")
// +-------------------+
// | Type (1 byte)     | 压缩类型
// +-------------------+
// | OriginalSize (4)  | 原始大小
// +-------------------+
// | CompressedSize (4)| 压缩后大小
// +-------------------+
// | Data (...)        | 压缩数据
// +-------------------+
type CompressedPage struct {
	Magic         [4]byte // 魔数: "BZCP" (Bf-Tree Compressed Page)
	Type          byte    // 压缩类型
	OriginalSize  uint32  // 原始大小
	CompressedSize uint32 // 压缩后大小
	Data          []byte  // 压缩数据
}

const (
	CompressedPageMagic = "BZCP" // 压缩页面魔数

	// 压缩类型编码
	compressionTypeNone  byte = 0x00
	compressionTypeSnappy byte = 0x01
	compressionTypeLZ4   byte = 0x02
	compressionTypeZSTD  byte = 0x03
)

// NewCompressedPage 创建压缩页面
func NewCompressedPage(compressionType CompressionType, originalData []byte, compressor Compressor) (*CompressedPage, error) {
	// 压缩数据
	compressed, err := compressor.Compress(originalData)
	if err != nil {
		return nil, fmt.Errorf("compress failed: %w", err)
	}

	// 如果压缩后反而更大，则不压缩
	if len(compressed) >= len(originalData) {
		compressed = originalData
		compressionType = CompressionNone
	}

	// 编码压缩类型
	var typeByte byte
	switch compressionType {
	case CompressionNone:
		typeByte = compressionTypeNone
	case CompressionSnappy:
		typeByte = compressionTypeSnappy
	case CompressionLZ4:
		typeByte = compressionTypeLZ4
	case CompressionZSTD:
		typeByte = compressionTypeZSTD
	default:
		return nil, fmt.Errorf("unknown compression type: %s", compressionType)
	}

	// 创建压缩页面
	cp := &CompressedPage{
		Magic:         [4]byte{'B', 'Z', 'C', 'P'},
		Type:          typeByte,
		OriginalSize:  uint32(len(originalData)),
		CompressedSize: uint32(len(compressed)),
		Data:          compressed,
	}

	return cp, nil
}

// Serialize 序列化压缩页面
func (cp *CompressedPage) Serialize() ([]byte, error) {
	buf := new(bytes.Buffer)

	// 写入魔数
	if _, err := buf.Write(cp.Magic[:]); err != nil {
		return nil, fmt.Errorf("failed to write magic: %w", err)
	}

	// 写入类型
	if err := buf.WriteByte(cp.Type); err != nil {
		return nil, fmt.Errorf("failed to write type: %w", err)
	}

	// 写入原始大小
	if err := binary.Write(buf, binary.BigEndian, cp.OriginalSize); err != nil {
		return nil, fmt.Errorf("failed to write original size: %w", err)
	}

	// 写入压缩大小
	if err := binary.Write(buf, binary.BigEndian, cp.CompressedSize); err != nil {
		return nil, fmt.Errorf("failed to write compressed size: %w", err)
	}

	// 写入数据
	if _, err := buf.Write(cp.Data); err != nil {
		return nil, fmt.Errorf("failed to write data: %w", err)
	}

	return buf.Bytes(), nil
}

// Deserialize 反序列化压缩页面
func DeserializeCompressedPage(data []byte) (*CompressedPage, error) {
	if len(data) < 13 { // 4 (magic) + 1 (type) + 4 (original) + 4 (compressed)
		return nil, fmt.Errorf("invalid compressed page: too short")
	}

	cp := &CompressedPage{}

	// 读取魔数
	copy(cp.Magic[:], data[0:4])
	if string(cp.Magic[:]) != CompressedPageMagic {
		return nil, fmt.Errorf("invalid compressed page: wrong magic")
	}

	// 读取类型
	cp.Type = data[4]

	// 读取原始大小
	cp.OriginalSize = binary.BigEndian.Uint32(data[5:9])

	// 读取压缩大小
	cp.CompressedSize = binary.BigEndian.Uint32(data[9:13])

	// 读取数据
	if len(data) < int(13+cp.CompressedSize) {
		return nil, fmt.Errorf("invalid compressed page: data truncated")
	}
	cp.Data = data[13 : 13+cp.CompressedSize]

	return cp, nil
}

// GetCompressionType 获取压缩类型
func (cp *CompressedPage) GetCompressionType() CompressionType {
	switch cp.Type {
	case compressionTypeNone:
		return CompressionNone
	case compressionTypeSnappy:
		return CompressionSnappy
	case compressionTypeLZ4:
		return CompressionLZ4
	case compressionTypeZSTD:
		return CompressionZSTD
	default:
		return CompressionNone
	}
}

// NewCompressor 创建压缩器
func NewCompressor(compressionType CompressionType, zstdLevel int) (Compressor, error) {
	switch compressionType {
	case CompressionNone:
		return &NoneCompressor{}, nil
	case CompressionSnappy:
		return &SnappyCompressor{}, nil
	case CompressionLZ4:
		return NewLZ4Compressor(), nil
	case CompressionZSTD:
		return NewZSTDCompressor(zstdLevel)
	default:
		return nil, fmt.Errorf("unknown compression type: %s", compressionType)
	}
}
