// Package types 压缩器实现
//
// 实现四种压缩算法：None、Snappy、ZSTD、LZ4
// 所有实现均为纯 Go，无 CGO 依赖
package types

import (
	"bytes"
	"fmt"
	"io"

	"github.com/klauspost/compress/s2"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

// NewCompressor 创建压缩器
func NewCompressor(compressionType CompressionType) (Compressor, error) {
	if err := compressionType.Validate(); err != nil {
		return nil, err
	}

	switch compressionType {
	case CompressionTypeSnappy:
		return NewSnappyCompressor()
	case CompressionTypeZSTD:
		return NewZSTDCompressor()
	case CompressionTypeLZ4:
		return NewLZ4Compressor()
	default:
		return NewNoneCompressor(), nil
	}
}

// ==================== None Compressor (无压缩) ====================

// noneCompressor 无压缩器（直接透传数据）
type noneCompressor struct{}

// NewNoneCompressor 创建无压缩器
func NewNoneCompressor() Compressor {
	return &noneCompressor{}
}

// Compress 压缩数据（无压缩，直接返回）
func (c *noneCompressor) Compress(data []byte) ([]byte, error) {
	result := make([]byte, len(data))
	copy(result, data)
	return result, nil
}

// Decompress 解压数据（无解压，直接返回）
func (c *noneCompressor) Decompress(data []byte) ([]byte, error) {
	result := make([]byte, len(data))
	copy(result, data)
	return result, nil
}

// Type 返回压缩算法类型
func (c *noneCompressor) Type() CompressionType {
	return CompressionTypeNone
}

// Name 返回压缩算法名称
func (c *noneCompressor) Name() string {
	return "none"
}

// ==================== Snappy Compressor ====================

// snappyCompressor Snappy 压缩器（使用 klauspost/compress/s2 优化分支）
type snappyCompressor struct{}

// NewSnappyCompressor 创建 Snappy 压缩器
func NewSnappyCompressor() (Compressor, error) {
	return &snappyCompressor{}, nil
}

// Compress 压缩数据
func (c *snappyCompressor) Compress(data []byte) ([]byte, error) {
	compressed := s2.Encode(nil, data)
	return compressed, nil
}

// Decompress 解压数据
func (c *snappyCompressor) Decompress(data []byte) ([]byte, error) {
	decompressed, err := s2.Decode(nil, data)
	if err != nil {
		return nil, NewCompressionDecompressError("Snappy", err)
	}
	return decompressed, nil
}

// Type 返回压缩算法类型
func (c *snappyCompressor) Type() CompressionType {
	return CompressionTypeSnappy
}

// Name 返回压缩算法名称
func (c *snappyCompressor) Name() string {
	return "snappy"
}

// ==================== ZSTD Compressor ====================

// zstdCompressor ZSTD 压缩器
type zstdCompressor struct {
	encoder *zstd.Encoder
	decoder *zstd.Decoder
	level   int
}

// NewZSTDCompressor 创建 ZSTD 压缩器（使用默认压缩级别 3）
func NewZSTDCompressor() (Compressor, error) {
	return NewZSTDCompressorWithLevel(3)
}

// NewZSTDCompressorWithLevel 创建指定级别的 ZSTD 压缩器
//
// 参数：
//   - level: 压缩级别（1-9，级别越高压缩率越好但速度越慢）
func NewZSTDCompressorWithLevel(level int) (Compressor, error) {
	if level < 1 || level > 9 {
		return nil, NewStoreInvalidParameterError("ZSTD 压缩级别必须在 1-9 之间")
	}

	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)),
	)
	if err != nil {
		return nil, NewCompressionCompressError("ZSTD 创建编码器", err)
	}

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, NewCompressionDecompressError("ZSTD 创建解码器", err)
	}

	return &zstdCompressor{
		encoder: encoder,
		decoder: decoder,
		level:   level,
	}, nil
}

// Compress 压缩数据
func (c *zstdCompressor) Compress(data []byte) ([]byte, error) {
	maxCompressedSize := c.encoder.MaxEncodedSize(len(data))
	compressed := c.encoder.EncodeAll(data, make([]byte, 0, maxCompressedSize))
	return compressed, nil
}

// Decompress 解压数据
func (c *zstdCompressor) Decompress(data []byte) ([]byte, error) {
	decompressed, err := c.decoder.DecodeAll(data, make([]byte, 0, len(data)*2))
	if err != nil {
		return nil, NewCompressionDecompressError("ZSTD", err)
	}
	return decompressed, nil
}

// Type 返回压缩算法类型
func (c *zstdCompressor) Type() CompressionType {
	return CompressionTypeZSTD
}

// Name 返回压缩算法名称
func (c *zstdCompressor) Name() string {
	return fmt.Sprintf("zstd (level=%d)", c.level)
}

// Close 关闭压缩器并释放资源
func (c *zstdCompressor) Close() error {
	return nil
}

// ==================== LZ4 Compressor ====================

// lz4Compressor LZ4 压缩器
type lz4Compressor struct{}

// NewLZ4Compressor 创建 LZ4 压缩器（使用 HC 模式以获得更高压缩率）
func NewLZ4Compressor() (Compressor, error) {
	return &lz4Compressor{}, nil
}

// Compress 压缩数据
func (c *lz4Compressor) Compress(data []byte) ([]byte, error) {
	var compressedBuf bytes.Buffer
	writer := lz4.NewWriter(&compressedBuf)

	if _, err := writer.Write(data); err != nil {
		return nil, NewCompressionCompressError("LZ4", err)
	}

	if err := writer.Close(); err != nil {
		return nil, NewCompressionCompressError("LZ4 刷新", err)
	}

	return compressedBuf.Bytes(), nil
}

// Decompress 解压数据
func (c *lz4Compressor) Decompress(data []byte) ([]byte, error) {
	reader := lz4.NewReader(bytes.NewReader(data))
	decompressedBuf := new(bytes.Buffer)

	if _, err := io.Copy(decompressedBuf, reader); err != nil {
		return nil, NewCompressionDecompressError("LZ4", err)
	}

	return decompressedBuf.Bytes(), nil
}

// Type 返回压缩算法类型
func (c *lz4Compressor) Type() CompressionType {
	return CompressionTypeLZ4
}

// Name 返回压缩算法名称
func (c *lz4Compressor) Name() string {
	return "lz4 (hc)"
}

// ==================== Compressor Stream Interface ====================

// CompressorWriter 压缩写入器接口（支持流式压缩）
type CompressorWriter interface {
	io.WriteCloser
	Flush() error
}

// DecompressorReader 解压读取器接口（支持流式解压）
type DecompressorReader interface {
	io.ReadCloser
}

// NewCompressorWriter 创建压缩写入器
func NewCompressorWriter(writer io.Writer, compressionType CompressionType) (CompressorWriter, error) {
	if err := compressionType.Validate(); err != nil {
		return nil, err
	}

	switch compressionType {
	case CompressionTypeSnappy:
		return s2.NewWriter(writer), nil
	case CompressionTypeZSTD:
		return zstd.NewWriter(writer, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(3)))
	case CompressionTypeLZ4:
		return lz4.NewWriter(writer), nil
	default:
		return &nopCompressorWriter{writer: writer}, nil
	}
}

// NewDecompressorReader 创建解压读取器
func NewDecompressorReader(reader io.Reader, compressionType CompressionType) (DecompressorReader, error) {
	if err := compressionType.Validate(); err != nil {
		return nil, err
	}

	switch compressionType {
	case CompressionTypeSnappy:
		return &s2DecompressorReader{reader: s2.NewReader(reader)}, nil
	case CompressionTypeZSTD:
		decoder, err := zstd.NewReader(reader)
		if err != nil {
			return nil, err
		}
		return &zstdDecompressorReader{decoder: decoder}, nil
	case CompressionTypeLZ4:
		return &lz4DecompressorReader{reader: lz4.NewReader(reader)}, nil
	default:
		return &nopDecompressorReader{reader: reader}, nil
	}
}

// decompressorReader 解压读取器包装器（提供通用实现）
type decompressorReader struct {
	readFunc  func(p []byte) (n int, err error)
	closeFunc func() error
}

func (r *decompressorReader) Read(p []byte) (n int, err error) {
	return r.readFunc(p)
}

func (r *decompressorReader) Close() error {
	return r.closeFunc()
}

// s2DecompressorReader 包装 s2.Reader 以实现 DecompressorReader 接口
type s2DecompressorReader struct {
	reader *s2.Reader
}

func (r *s2DecompressorReader) Read(p []byte) (n int, err error) {
	return r.reader.Read(p)
}

func (r *s2DecompressorReader) Close() error {
	return nil
}

// zstdDecompressorReader 包装 zstd.Decoder 以实现 DecompressorReader 接口
type zstdDecompressorReader struct {
	decoder *zstd.Decoder
}

func (r *zstdDecompressorReader) Read(p []byte) (n int, err error) {
	return r.decoder.Read(p)
}

func (r *zstdDecompressorReader) Close() error {
	return nil
}

// lz4DecompressorReader 包装 lz4.Reader 以实现 DecompressorReader 接口
type lz4DecompressorReader struct {
	reader *lz4.Reader
}

func (r *lz4DecompressorReader) Read(p []byte) (n int, err error) {
	return r.reader.Read(p)
}

func (r *lz4DecompressorReader) Close() error {
	return nil
}

// ==================== NOP Compressor (用于无压缩模式) ====================

// nopCompressorWriter 无压缩写入器（透传）
type nopCompressorWriter struct {
	writer io.Writer
}

func (w *nopCompressorWriter) Write(p []byte) (n int, err error) {
	return w.writer.Write(p)
}

func (w *nopCompressorWriter) Close() error {
	if closer, ok := w.writer.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (w *nopCompressorWriter) Flush() error {
	if flusher, ok := w.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

// nopDecompressorReader 无解压读取器（透传）
type nopDecompressorReader struct {
	reader io.Reader
}

func (r *nopDecompressorReader) Read(p []byte) (n int, err error) {
	return r.reader.Read(p)
}

func (r *nopDecompressorReader) Close() error {
	if closer, ok := r.reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
