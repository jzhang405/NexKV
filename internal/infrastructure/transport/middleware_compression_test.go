// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/compressor"
)

func TestCompressionMiddleware_Name(t *testing.T) {
	m := NewCompressionMiddleware(DefaultCompressionConfig())
	assert.Equal(t, "compression", m.Name())
}

func TestCompressionMiddleware_SmallMessageNotCompressed(t *testing.T) {
	m := NewCompressionMiddleware(CompressionConfig{
		Algorithm: compressor.Snappy,
		Threshold: 1024, // 1KB
	})

	peer := model.PeerID("test-peer")
	// 小于阈值的消息
	smallPayload := make([]byte, 100)
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", smallPayload)
	ctx := context.Background()

	var receivedMsg model.Message
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		receivedMsg = m
		return nil
	}

	err := m.InterceptSend(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.NotNil(t, receivedMsg)

	// 小消息不应该被压缩
	_, hasCompression := receivedMsg.Exts().GetString("compression")
	assert.False(t, hasCompression, "small message should not have compression marker")
	assert.Equal(t, smallPayload, receivedMsg.Payload())
}

func TestCompressionMiddleware_LargeMessageCompressed(t *testing.T) {
	m := NewCompressionMiddleware(CompressionConfig{
		Algorithm: compressor.Snappy,
		Threshold: 100, // 100 bytes threshold
	})

	peer := model.PeerID("test-peer")
	// 大于阈值的消息（重复数据压缩效果好）
	largePayload := make([]byte, 1000)
	for i := range largePayload {
		largePayload[i] = byte(i % 10)
	}
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", largePayload)
	ctx := context.Background()

	var receivedMsg model.Message
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		receivedMsg = m
		return nil
	}

	err := m.InterceptSend(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.NotNil(t, receivedMsg)

	// 大消息应该被压缩
	compressionType, hasCompression := receivedMsg.Exts().GetString("compression")
	assert.True(t, hasCompression, "large message should have compression marker")
	assert.Equal(t, "snappy", compressionType)

	// 压缩后的数据应该比原始数据小（对于重复数据）
	compressedPayload := receivedMsg.Payload()
	assert.Less(t, len(compressedPayload), len(largePayload), "compressed payload should be smaller")
}

func TestCompressionMiddleware_InterceptReceive_Decompress(t *testing.T) {
	m := NewCompressionMiddleware(CompressionConfig{
		Algorithm: compressor.Snappy,
		Threshold: 100,
	})

	// 创建一个已压缩的消息
	originalPayload := make([]byte, 1000)
	for i := range originalPayload {
		originalPayload[i] = byte(i % 10)
	}

	comp := compressor.New(compressor.Snappy)
	compressedPayload, err := comp.Compress(originalPayload)
	require.NoError(t, err)

	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", compressedPayload)
	msg.Exts().Set("compression", "snappy")

	peer := model.PeerID("test-peer")
	ctx := context.Background()

	var receivedMsg model.Message
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		receivedMsg = m
		return nil
	}

	err = m.InterceptReceive(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.NotNil(t, receivedMsg)

	// 解压后的数据应该与原始数据相同
	assert.Equal(t, originalPayload, receivedMsg.Payload())

	// compression 标记应该被移除
	_, hasCompression := receivedMsg.Exts().GetString("compression")
	assert.False(t, hasCompression, "compression marker should be removed after decompression")
}

func TestCompressionMiddleware_InterceptReceive_NoCompression(t *testing.T) {
	m := NewCompressionMiddleware(DefaultCompressionConfig())

	originalPayload := []byte("test payload")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", originalPayload)
	// 不设置 compression 标记

	peer := model.PeerID("test-peer")
	ctx := context.Background()

	var receivedMsg model.Message
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		receivedMsg = m
		return nil
	}

	err := m.InterceptReceive(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.NotNil(t, receivedMsg)

	// 未压缩的消息应该直接透传
	assert.Equal(t, originalPayload, receivedMsg.Payload())
}

func TestCompressionMiddleware_InterceptReceive_NoneCompression(t *testing.T) {
	m := NewCompressionMiddleware(DefaultCompressionConfig())

	originalPayload := []byte("test payload")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", originalPayload)
	msg.Exts().Set("compression", "none")

	peer := model.PeerID("test-peer")
	ctx := context.Background()

	var receivedMsg model.Message
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		receivedMsg = m
		return nil
	}

	err := m.InterceptReceive(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.NotNil(t, receivedMsg)

	// compression=none 应该直接透传
	assert.Equal(t, originalPayload, receivedMsg.Payload())
}

func TestCompressionMiddleware_DefaultConfig(t *testing.T) {
	config := DefaultCompressionConfig()
	assert.Equal(t, compressor.Snappy, config.Algorithm)
	assert.Equal(t, 1024, config.Threshold)
}

func TestCompressionMiddleware_ZeroConfig(t *testing.T) {
	// 零值配置应该使用默认值
	m := NewCompressionMiddleware(CompressionConfig{})
	assert.Equal(t, compressor.Snappy, m.compressor.Type())
	assert.Equal(t, 1024, m.threshold)
}

func TestCompressionMiddleware_PreservesExtensions(t *testing.T) {
	m := NewCompressionMiddleware(CompressionConfig{
		Algorithm: compressor.Snappy,
		Threshold: 100,
	})

	peer := model.PeerID("test-peer")
	largePayload := make([]byte, 1000)
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", largePayload)
	msg.Exts().Set("custom-key", "custom-value")
	ctx := context.Background()

	var receivedMsg model.Message
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		receivedMsg = m
		return nil
	}

	err := m.InterceptSend(ctx, peer, msg, next)
	assert.NoError(t, err)

	// 自定义扩展信息应该被保留
	customVal, ok := receivedMsg.Exts().GetString("custom-key")
	assert.True(t, ok)
	assert.Equal(t, "custom-value", customVal)
}

func TestCompressionMiddleware_PreserveOtherExtensionsOnReceive(t *testing.T) {
	m := NewCompressionMiddleware(CompressionConfig{
		Algorithm: compressor.Snappy,
		Threshold: 100,
	})

	originalPayload := make([]byte, 1000)
	comp := compressor.New(compressor.Snappy)
	compressedPayload, _ := comp.Compress(originalPayload)

	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", compressedPayload)
	msg.Exts().Set("compression", "snappy")
	msg.Exts().Set("custom-key", "custom-value")

	peer := model.PeerID("test-peer")
	ctx := context.Background()

	var receivedMsg model.Message
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		receivedMsg = m
		return nil
	}

	err := m.InterceptReceive(ctx, peer, msg, next)
	assert.NoError(t, err)

	// 自定义扩展信息应该被保留
	customVal, ok := receivedMsg.Exts().GetString("custom-key")
	assert.True(t, ok)
	assert.Equal(t, "custom-value", customVal)

	// compression 标记应该被移除
	_, hasCompression := receivedMsg.Exts().GetString("compression")
	assert.False(t, hasCompression)
}

func TestCompressionMiddleware_RoundTrip(t *testing.T) {
	m := NewCompressionMiddleware(CompressionConfig{
		Algorithm: compressor.Snappy,
		Threshold: 100,
	})

	originalPayload := make([]byte, 1000)
	for i := range originalPayload {
		originalPayload[i] = byte(i % 256)
	}

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", originalPayload)
	ctx := context.Background()

	// 模拟发送端压缩
	var compressedMsg model.Message
	sendNext := func(ctx context.Context, p model.PeerID, m model.Message) error {
		compressedMsg = m
		return nil
	}

	err := m.InterceptSend(ctx, peer, msg, sendNext)
	require.NoError(t, err)

	// 模拟接收端解压
	var decompressedMsg model.Message
	receiveNext := func(ctx context.Context, p model.PeerID, m model.Message) error {
		decompressedMsg = m
		return nil
	}

	err = m.InterceptReceive(ctx, peer, compressedMsg, receiveNext)
	require.NoError(t, err)

	// 最终数据应该与原始数据相同
	assert.Equal(t, originalPayload, decompressedMsg.Payload())
}

// 确保实现 Middleware 接口
func TestCompressionMiddleware_ImplementsInterface(t *testing.T) {
	var _ service.Middleware = NewCompressionMiddleware(DefaultCompressionConfig())
}

// BenchmarkCompressionMiddleware 基准测试
func BenchmarkCompressionMiddleware(b *testing.B) {
	m := NewCompressionMiddleware(CompressionConfig{
		Algorithm: compressor.Snappy,
		Threshold: 100,
	})

	peer := model.PeerID("test-peer")
	payload := make([]byte, 1000)
	for i := range payload {
		payload[i] = byte(i % 10)
	}
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", payload)
	ctx := context.Background()

	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.InterceptSend(ctx, peer, msg, next)
	}
}

// BenchmarkCompressionMiddleware_Decompress 解压基准测试
func BenchmarkCompressionMiddleware_Decompress(b *testing.B) {
	m := NewCompressionMiddleware(CompressionConfig{
		Algorithm: compressor.Snappy,
		Threshold: 100,
	})

	originalPayload := make([]byte, 1000)
	for i := range originalPayload {
		originalPayload[i] = byte(i % 10)
	}

	comp := compressor.New(compressor.Snappy)
	compressedPayload, _ := comp.Compress(originalPayload)

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", compressedPayload)
	msg.Exts().Set("compression", "snappy")
	ctx := context.Background()

	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.InterceptReceive(ctx, peer, msg, next)
	}
}
