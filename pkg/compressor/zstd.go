// Package compressor 提供压缩算法抽象
package compressor

import (
	"github.com/klauspost/compress/zstd"
)

// zstdCompressor ZSTD 压缩器
type zstdCompressor struct{}

// newZSTDCompressor 创建 ZSTD 压缩器
func newZSTDCompressor() *zstdCompressor {
	return &zstdCompressor{}
}

// Type 返回压缩算法类型
func (c *zstdCompressor) Type() CompressorType {
	return ZSTD
}

// Compress 压缩数据
func (c *zstdCompressor) Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// 创建编码器（每次创建以避免状态复用问题）
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, err
	}
	defer encoder.Close()

	compressed := encoder.EncodeAll(data, make([]byte, 0, len(data)))
	return compressed, nil
}

// Decompress 解压数据（使用默认大小限制）
func (c *zstdCompressor) Decompress(data []byte) ([]byte, error) {
	return c.DecompressWithLimit(data, DefaultMaxDecompressedSize)
}

// DecompressWithLimit 带大小限制的解压（P0 修复：防止压缩炸弹）
func (c *zstdCompressor) DecompressWithLimit(data []byte, maxBytes int) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer decoder.Close()

	// 计算安全的初始容量，避免预分配过大
	initialCap := min(maxBytes, len(data)*100)
	initialCap = max(initialCap, 1024)

	decompressed, err := decoder.DecodeAll(data, make([]byte, 0, initialCap))
	if err != nil {
		return nil, err
	}

	if len(decompressed) > maxBytes {
		return nil, ErrDecompressionTooBig
	}
	return decompressed, nil
}

// 确保实现 Compressor 接口
var _ Compressor = (*zstdCompressor)(nil)
