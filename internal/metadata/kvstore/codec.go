// Package kvstore MessagePack 编解码器
package kvstore

import (
	"bytes"
	"compress/flate"
	"io"
	"sync"

	"github.com/vmihailenco/msgpack/v5"
)

// CompressionType 压缩类型
type CompressionType int

const (
	// CompressionNone 不压缩
	CompressionNone CompressionType = iota

	// CompressionFlate 使用 DEFLATE 压缩
	CompressionFlate

	// CompressionSnappy 使用 Snappy 压缩（需外部库）
	CompressionSnappy

	// CompressionZSTD 使用 ZSTD 压缩（需外部库）
	CompressionZSTD
)

// MetadataCodec 元数据编解码器
//
// 核心功能：
//   - MessagePack 序列化/反序列化
//   - 可选压缩支持
//   - 类型安全的编码/解码
//   - 并发安全（使用 sync.Pool）
type MetadataCodec struct {
	compression CompressionType
	pool        sync.Pool
}

// NewMetadataCodec 创建元数据编解码器
func NewMetadataCodec(compression CompressionType) *MetadataCodec {
	return &MetadataCodec{
		compression: compression,
		pool: sync.Pool{
			New: func() any {
				return msgpack.NewEncoder(nil)
			},
		},
	}
}

// DefaultCodec 默认编解码器（不压缩）
func DefaultCodec() *MetadataCodec {
	return NewMetadataCodec(CompressionNone)
}

// Encode 将值编码为字节切片
//
// 流程：
//  1. 使用 MessagePack 序列化
//  2. 如果启用压缩，进行压缩
//  3. 返回编码后的字节切片
func (c *MetadataCodec) Encode(value any) ([]byte, error) {
	if value == nil {
		return nil, ErrNilValue
	}

	// MessagePack 编码
	var buf bytes.Buffer
	enc := msgpack.NewEncoder(&buf)
	if err := enc.Encode(value); err != nil {
		return nil, ErrEncodingFailed
	}

	data := buf.Bytes()

	// 压缩
	if c.compression != CompressionNone {
		compressed, err := c.compress(data)
		if err != nil {
			return nil, err
		}
		data = compressed
	}

	return data, nil
}

// Decode 将字节切片解码为指定类型
//
// 流程：
//  1. 如果启用压缩，先解压缩
//  2. 使用 MessagePack 反序列化
//  3. 返回解码后的值
func (c *MetadataCodec) Decode(data []byte, value any) error {
	if len(data) == 0 {
		return ErrKeyNotFound
	}

	if value == nil {
		return ErrNilValue
	}

	// 解压缩
	if c.compression != CompressionNone {
		decompressed, err := c.decompress(data)
		if err != nil {
			return err
		}
		data = decompressed
	}

	// MessagePack 解码
	dec := msgpack.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(value); err != nil {
		return ErrDecodingFailed
	}

	return nil
}

// compress 压缩数据
func (c *MetadataCodec) compress(data []byte) ([]byte, error) {
	switch c.compression {
	case CompressionNone:
		return data, nil
	case CompressionFlate:
		return c.compressFlate(data)
	case CompressionSnappy:
		// TODO: 实现 Snappy 压缩
		return nil, ErrCompressionFailed
	case CompressionZSTD:
		// TODO: 实现 ZSTD 压缩
		return nil, ErrCompressionFailed
	default:
		return data, nil
	}
}

// decompress 解压缩数据
func (c *MetadataCodec) decompress(data []byte) ([]byte, error) {
	switch c.compression {
	case CompressionNone:
		return data, nil
	case CompressionFlate:
		return c.decompressFlate(data)
	case CompressionSnappy:
		// TODO: 实现 Snappy 解压缩
		return nil, ErrDecompressionFailed
	case CompressionZSTD:
		// TODO: 实现 ZSTD 解压缩
		return nil, ErrDecompressionFailed
	default:
		return data, nil
	}
}

// compressFlate 使用 DEFLATE 算法压缩
func (c *MetadataCodec) compressFlate(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil, ErrCompressionFailed
	}

	if _, err := writer.Write(data); err != nil {
		writer.Close()
		return nil, ErrCompressionFailed
	}

	if err := writer.Close(); err != nil {
		return nil, ErrCompressionFailed
	}

	return buf.Bytes(), nil
}

// decompressFlate 使用 DEFLATE 算法解压缩
func (c *MetadataCodec) decompressFlate(data []byte) ([]byte, error) {
	reader := flate.NewReader(bytes.NewReader(data))
	defer reader.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		return nil, ErrDecompressionFailed
	}

	return buf.Bytes(), nil
}

// EncodeBatch 批量编码值
func (c *MetadataCodec) EncodeBatch(values []any) ([][]byte, error) {
	results := make([][]byte, len(values))
	for i, value := range values {
		encoded, err := c.Encode(value)
		if err != nil {
			return nil, err
		}
		results[i] = encoded
	}
	return results, nil
}

// DecodeBatch 批量解码值
func (c *MetadataCodec) DecodeBatch(data [][]byte, values []any) error {
	if len(data) != len(values) {
		return ErrDecodingFailed
	}

	for i := range data {
		if err := c.Decode(data[i], values[i]); err != nil {
			return err
		}
	}
	return nil
}

// GetCompressionType 获取当前压缩类型
func (c *MetadataCodec) GetCompressionType() CompressionType {
	return c.compression
}

// SetCompressionType 设置压缩类型
func (c *MetadataCodec) SetCompressionType(compression CompressionType) {
	c.compression = compression
}
