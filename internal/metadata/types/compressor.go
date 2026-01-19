// Package types 压缩器实现
//
// 实现三种压缩算法：Snappy、ZSTD、LZ4
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
//
// 参数：
// - compressionType: 压缩算法类型
//
// 返回 Compressor 实例和错误信息
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

// snappyCompressor Snappy 压缩器
// 使用 klauspost/compress/s2 (Snappy 的优化分支)
type snappyCompressor struct {
	// Snappy 不支持压缩级别配置
}

// NewSnappyCompressor 创建 Snappy 压缩器
func NewSnappyCompressor() (Compressor, error) {
	return &snappyCompressor{}, nil
}

// Compress 压缩数据
func (c *snappyCompressor) Compress(data []byte) ([]byte, error) {
	// 使用 s2 编码（比原始 Snappy 更快）
	compressed := s2.Encode(nil, data)
	return compressed, nil
}

// Decompress 解压数据
func (c *snappyCompressor) Decompress(data []byte) ([]byte, error) {
	// 使用 s2 解码
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

// NewZSTDCompressor 创建 ZSTD 压缩器
//
// 使用默认压缩级别 3（可配置范围 1-9）
// 级别越高压缩率越好但速度越慢
func NewZSTDCompressor() (Compressor, error) {
	level := 3 // 默认级别

	// 创建编码器
	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)),
	)
	if err != nil {
		return nil, NewCompressionCompressError("ZSTD 创建编码器", err)
	}

	// 创建解码器
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

// NewZSTDCompressorWithLevel 创建指定级别的 ZSTD 压缩器
//
// 参数：
// - level: 压缩级别（1-9，默认 3）
//
// 返回 Compressor 实例和错误信息
func NewZSTDCompressorWithLevel(level int) (Compressor, error) {
	if level < 1 || level > 9 {
		return nil, NewStoreInvalidParameterError("ZSTD 压缩级别必须在 1-9 之间")
	}

	// 创建编码器
	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)),
	)
	if err != nil {
		return nil, NewCompressionCompressError("ZSTD 创建编码器", err)
	}

	// 创建解码器
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
	// P2-1 优化：使用 MaxEncodedSize() 预分配足够大的缓冲区
	// 原逻辑：make([]byte, 0, len(data)) 可能导致小数据或不可压缩数据时重新分配
	// 新逻辑：使用 zstd 库推荐的 MaxEncodedSize() 计算最大编码大小
	maxCompressedSize := c.encoder.MaxEncodedSize(len(data))
	compressed := c.encoder.EncodeAll(data, make([]byte, 0, maxCompressedSize))
	return compressed, nil
}

// Decompress 解压数据
func (c *zstdCompressor) Decompress(data []byte) ([]byte, error) {
	// 解码数据
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
	// ZSTD 编码器和解码器不需要显式关闭
	return nil
}

// ==================== LZ4 Compressor ====================

// lz4Compressor LZ4 压缩器
type lz4Compressor struct {
	fast bool // 快速模式（false=HC 模式，更高压缩率）
}

// NewLZ4Compressor 创建 LZ4 压缩器
//
// 默认使用 HC 模式（高压缩率）
func NewLZ4Compressor() (Compressor, error) {
	return &lz4Compressor{
		fast: false, // 默认 HC 模式
	}, nil
}

// NewLZ4FastCompressor 创建快速 LZ4 压缩器
//
// 快速模式压缩率较低但速度更快
func NewLZ4FastCompressor() (Compressor, error) {
	return &lz4Compressor{
		fast: true,
	}, nil
}

// Compress 压缩数据
func (c *lz4Compressor) Compress(data []byte) ([]byte, error) {
	// 使用流式 API（更可靠）
	var compressedBuf bytes.Buffer
	writer := lz4.NewWriter(&compressedBuf)

	// 写入数据
	_, err := writer.Write(data)
	if err != nil {
		return nil, NewCompressionCompressError("LZ4", err)
	}

	// 关闭 writer 以刷新所有数据
	if err := writer.Close(); err != nil {
		return nil, NewCompressionCompressError("LZ4 刷新", err)
	}

	return compressedBuf.Bytes(), nil
}

// Decompress 解压数据
func (c *lz4Compressor) Decompress(data []byte) ([]byte, error) {
	// 使用流式 API 解压
	reader := lz4.NewReader(bytes.NewReader(data))

	// 读取所有解压后的数据
	decompressedBuf := new(bytes.Buffer)
	_, err := io.Copy(decompressedBuf, reader)
	if err != nil {
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
	if c.fast {
		return "lz4 (fast)"
	}
	return "lz4 (hc)"
}

// ==================== Compressor Stream Interface ====================

// CompressorWriter 压缩写入器接口（支持流式压缩）
type CompressorWriter interface {
	io.WriteCloser
	// Flush 刷新压缩缓冲区
	Flush() error
}

// DecompressorReader 解压读取器接口（支持流式解压）
type DecompressorReader interface {
	io.ReadCloser
}

// NewCompressorWriter 创建压缩写入器
//
// 参数：
// - writer: 底层写入器
// - compressionType: 压缩算法类型
//
// 返回 CompressorWriter 实例和错误信息
func NewCompressorWriter(writer io.Writer, compressionType CompressionType) (CompressorWriter, error) {
	// P2-1 修复：先验证压缩类型，与 NewCompressor() 行为保持一致
	if err := compressionType.Validate(); err != nil {
		return nil, err
	}

	switch compressionType {
	case CompressionTypeSnappy:
		return s2.NewWriter(writer), nil
	case CompressionTypeZSTD:
		return zstd.NewWriter(writer,
			zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(3)),
		)
	case CompressionTypeLZ4:
		return lz4.NewWriter(writer), nil
	default:
		// P2-1 修复：CompressionTypeNone 走 default 分支，返回 nopCompressorWriter
		// 与 NewCompressor() 返回 NoneCompressor 的行为保持一致
		return &nopCompressorWriter{writer: writer}, nil
	}
}

// NewDecompressorReader 创建解压读取器
//
// 参数：
// - reader: 底层读取器
// - compressionType: 压缩算法类型
//
// 返回 DecompressorReader 实例和错误信息
func NewDecompressorReader(reader io.Reader, compressionType CompressionType) (DecompressorReader, error) {
	// P2-1 修复：先验证压缩类型，与 NewCompressor() 行为保持一致
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
		// P2-1 修复：CompressionTypeNone 走 default 分支，返回 nopDecompressorReader
		// 与 NewCompressor() 返回 NoneCompressor 的行为保持一致
		return &nopDecompressorReader{reader: reader}, nil
	}
}

// s2DecompressorReader 包装 s2.Reader 以实现 DecompressorReader 接口
type s2DecompressorReader struct {
	reader *s2.Reader
}

func (r *s2DecompressorReader) Read(p []byte) (n int, err error) {
	return r.reader.Read(p)
}

func (r *s2DecompressorReader) Close() error {
	// s2.Reader 不需要显式关闭
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
	// zstd.Decoder 不需要显式关闭
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
	// lz4.Reader 不需要显式关闭
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
