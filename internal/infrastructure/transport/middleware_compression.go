// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"log/slog"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/compressor"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// 默认解压大小限制（10MB）
const defaultMaxDecompressedSize = 10 * 1024 * 1024

// CompressionMiddleware 压缩中间件
// 支持多种压缩算法，可配置压缩阈值和解压大小限制
type CompressionMiddleware struct {
	compressor          compressor.Compressor
	threshold           int // 最小压缩阈值（字节）
	maxDecompressedSize int // 最大解压大小（字节），防止压缩炸弹
	logger              *slog.Logger
}

// CompressionConfig 压缩配置
type CompressionConfig struct {
	// Algorithm 压缩算法（默认 Snappy）
	Algorithm compressor.CompressorType
	// Threshold 最小压缩阈值（默认 1024 字节）
	Threshold int
	// MaxDecompressedSize 最大解压大小（默认 10MB）
	// 用于防止压缩炸弹攻击
	MaxDecompressedSize int
}

// DefaultCompressionConfig 默认压缩配置
func DefaultCompressionConfig() CompressionConfig {
	return CompressionConfig{
		Algorithm:           compressor.Snappy,
		Threshold:           1024, // 1KB
		MaxDecompressedSize: defaultMaxDecompressedSize,
	}
}

// NewCompressionMiddleware 创建压缩中间件
func NewCompressionMiddleware(config CompressionConfig) *CompressionMiddleware {
	// 应用默认配置
	defaults := DefaultCompressionConfig()
	if config.Algorithm == "" {
		config.Algorithm = defaults.Algorithm
	}
	if config.Threshold <= 0 {
		config.Threshold = defaults.Threshold
	}
	if config.MaxDecompressedSize <= 0 {
		config.MaxDecompressedSize = defaults.MaxDecompressedSize
	}

	return &CompressionMiddleware{
		compressor:          compressor.New(config.Algorithm),
		threshold:           config.Threshold,
		maxDecompressedSize: config.MaxDecompressedSize,
		logger:              slog.Default().With("middleware", "compression"),
	}
}

// Name 返回中间件名称
func (m *CompressionMiddleware) Name() string {
	return "compression"
}

// Priority 返回中间件优先级
func (m *CompressionMiddleware) Priority() int {
	return service.MiddlewarePriorityCompression
}

// InterceptSend 拦截发送请求，压缩大于阈值的消息
func (m *CompressionMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next service.SendFunc) error {
	payload := msg.Payload()
	if len(payload) <= m.threshold {
		return next(ctx, peer, msg)
	}

	compressed, err := m.compressor.Compress(payload)
	if err != nil {
		m.logger.Warn("compression failed, sending uncompressed message",
			"peer", peer,
			"payload_size", len(payload),
			"algorithm", m.compressor.Type(),
			"error", err,
		)
		return next(ctx, peer, msg)
	}

	compressedMsg := model.NewMessage(
		msg.ID(),
		msg.Type(),
		msg.Source(),
		msg.Target(),
		compressed,
	)

	// 复制原有扩展信息并添加压缩标记
	copyExts(msg, compressedMsg)
	compressedMsg.Exts().Set("compression", string(m.compressor.Type()))

	return next(ctx, peer, compressedMsg)
}

// InterceptReceive 拦截接收请求，解压带压缩标记的消息
func (m *CompressionMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next service.ReceiveFunc) error {
	compressionTypeVal, ok := msg.Exts().GetString("compression")
	if !ok || compressionTypeVal == "" || compressionTypeVal == string(compressor.None) {
		return next(ctx, peer, msg)
	}

	compressionType := compressor.CompressorType(compressionTypeVal)
	if !isValidCompressionType(compressionType) {
		m.logger.Warn("unsupported compression algorithm",
			"peer", peer,
			"algorithm", compressionTypeVal,
		)
		return errors.Wrap(errors.ErrInvalidCompression, "unsupported compression algorithm: "+compressionTypeVal)
	}

	decompressorInstance := compressor.New(compressionType)
	payload := msg.Payload()
	decompressed, err := m.decompressWithLimit(decompressorInstance, payload)
	if err != nil {
		m.logger.Warn("decompression failed",
			"peer", peer,
			"payload_size", len(payload),
			"algorithm", compressionTypeVal,
			"error", err,
		)
		return errors.Wrap(err, "decompression failed")
	}

	decompressedMsg := model.NewMessage(
		msg.ID(),
		msg.Type(),
		msg.Source(),
		msg.Target(),
		decompressed,
	)

	// 复制扩展信息（移除压缩标记）
	copyExts(msg, decompressedMsg)

	return next(ctx, peer, decompressedMsg)
}

// decompressWithLimit 带大小限制的解压（P0 修复：使用 DecompressWithLimit）
func (m *CompressionMiddleware) decompressWithLimit(decomp compressor.Compressor, data []byte) ([]byte, error) {
	return decomp.DecompressWithLimit(data, m.maxDecompressedSize)
}

// isValidCompressionType 验证压缩算法是否在白名单内
func isValidCompressionType(t compressor.CompressorType) bool {
	switch t {
	case compressor.Snappy, compressor.LZ4, compressor.ZSTD, compressor.None:
		return true
	default:
		return false
	}
}

// 确保实现 Middleware 接口
var _ service.Middleware = (*CompressionMiddleware)(nil)
