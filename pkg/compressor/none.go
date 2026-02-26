// Package compressor 提供压缩算法抽象
package compressor

import "github.com/jzhang405/NexKV/pkg/errors"

// noneCompressor 不压缩
type noneCompressor struct{}

// newNoneCompressor 创建不压缩器
func newNoneCompressor() *noneCompressor {
	return &noneCompressor{}
}

// Type 返回压缩算法类型
func (c *noneCompressor) Type() CompressorType {
	return None
}

// Compress 不压缩，直接返回原数据
func (c *noneCompressor) Compress(data []byte) ([]byte, error) {
	return data, nil
}

// Decompress 不解压，直接返回原数据
func (c *noneCompressor) Decompress(data []byte) ([]byte, error) {
	return c.DecompressWithLimit(data, DefaultMaxDecompressedSize)
}

// DecompressWithLimit 带大小限制的解压（None 不压缩，直接检查大小）
func (c *noneCompressor) DecompressWithLimit(data []byte, maxBytes int) ([]byte, error) {
	if len(data) > maxBytes {
		return nil, errors.ErrDecompressionTooBig
	}
	return data, nil
}

// 确保实现 Compressor 接口
var _ Compressor = (*noneCompressor)(nil)
