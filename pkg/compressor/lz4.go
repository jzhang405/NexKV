// Package compressor 提供压缩算法抽象
package compressor

import (
	"bytes"
	"io"

	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/pierrec/lz4/v4"
)

// lz4Compressor LZ4 压缩器
type lz4Compressor struct{}

// newLZ4Compressor 创建 LZ4 压缩器
func newLZ4Compressor() *lz4Compressor {
	return &lz4Compressor{}
}

// Type 返回压缩算法类型
func (c *lz4Compressor) Type() CompressorType {
	return LZ4
}

// Compress 压缩数据
func (c *lz4Compressor) Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	var buf bytes.Buffer
	writer := lz4.NewWriter(&buf)
	defer writer.Close()

	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decompress 解压数据（使用默认大小限制）
func (c *lz4Compressor) Decompress(data []byte) ([]byte, error) {
	return c.DecompressWithLimit(data, DefaultMaxDecompressedSize)
}

// DecompressWithLimit 带大小限制的解压（P0 修复：防止压缩炸弹）
func (c *lz4Compressor) DecompressWithLimit(data []byte, maxBytes int) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// 使用 LZ4 流式解压 + LimitReader 防止压缩炸弹
	reader := lz4.NewReader(bytes.NewReader(data))
	limitedReader := io.LimitReader(reader, int64(maxBytes)+1) // +1 用于检测是否超限

	// 读取解压后的数据
	decompressed, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, err
	}

	// 检查是否超过大小限制
	if len(decompressed) > maxBytes {
		return nil, errors.ErrDecompressionTooBig
	}

	return decompressed, nil
}

// 确保实现 Compressor 接口
var _ Compressor = (*lz4Compressor)(nil)
