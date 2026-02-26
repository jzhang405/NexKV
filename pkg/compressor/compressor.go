// Package compressor 提供压缩算法抽象
package compressor

// DefaultMaxDecompressedSize 默认最大解压大小（10MB）
const DefaultMaxDecompressedSize = 10 * 1024 * 1024

// CompressorType 压缩算法类型
type CompressorType string

const (
	// Snappy 高性能压缩算法（默认）
	Snappy CompressorType = "snappy"
	// LZ4 高速压缩算法
	LZ4 CompressorType = "lz4"
	// ZSTD 高压缩比算法
	ZSTD CompressorType = "zstd"
	// None 不压缩
	None CompressorType = "none"
)

// Compressor 压缩器接口
type Compressor interface {
	// Type 返回压缩算法类型
	Type() CompressorType
	// Compress 压缩数据
	Compress(data []byte) ([]byte, error)
	// Decompress 解压数据（使用默认大小限制）
	Decompress(data []byte) ([]byte, error)
	// DecompressWithLimit 带大小限制的解压（防止压缩炸弹）
	// maxBytes 为最大允许的解压后字节数，超过则返回错误
	DecompressWithLimit(data []byte, maxBytes int) ([]byte, error)
}

// New 根据类型创建压缩器
func New(t CompressorType) Compressor {
	switch t {
	case Snappy:
		return newSnappyCompressor()
	case LZ4:
		return newLZ4Compressor()
	case ZSTD:
		return newZSTDCompressor()
	case None:
		return newNoneCompressor()
	default:
		// 默认使用 Snappy
		return newSnappyCompressor()
	}
}
