// Package compressor 提供压缩算法抽象
package compressor

import (
	"bytes"
	"io"

	"github.com/golang/snappy"
)

// snappyCompressor Snappy 压缩器
type snappyCompressor struct{}

// newSnappyCompressor 创建 Snappy 压缩器
func newSnappyCompressor() *snappyCompressor {
	return &snappyCompressor{}
}

// Type 返回压缩算法类型
func (c *snappyCompressor) Type() CompressorType {
	return Snappy
}

// Compress 压缩数据（使用流式格式，与 Decompress 一致）
func (c *snappyCompressor) Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	var buf bytes.Buffer
	writer := snappy.NewBufferedWriter(&buf)
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
func (c *snappyCompressor) Decompress(data []byte) ([]byte, error) {
	return c.DecompressWithLimit(data, DefaultMaxDecompressedSize)
}

// DecompressWithLimit 带大小限制的解压（P0 修复：防止压缩炸弹）
func (c *snappyCompressor) DecompressWithLimit(data []byte, maxBytes int) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// 使用流式解压 + LimitReader
	reader := snappy.NewReader(bytes.NewReader(data))
	limitedReader := io.LimitReader(reader, int64(maxBytes)+1)

	decompressed, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, err
	}

	// 检查是否超过大小限制
	if len(decompressed) > maxBytes {
		return nil, ErrDecompressionTooBig
	}

	return decompressed, nil
}

// 确保实现 Compressor 接口
var _ Compressor = (*snappyCompressor)(nil)
