package transport

import (
	"bufio"
	"context"
	stderrors "errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// 确保实现 service.Channel 接口
var _ service.Channel = (*Libp2pChannel)(nil)

// Libp2pChannel 实现 Channel 接口
type Libp2pChannel struct {
	stream *Libp2pStream
	codec  *LengthPrefixedCodec

	// 缓冲读写
	reader *bufio.Reader
	writer *bufio.Writer

	// 并发控制
	mu     sync.Mutex
	closed atomic.Bool

	// 超时配置
	readTimeout  time.Duration
	writeTimeout time.Duration
}

// ChannelConfig Channel 配置
type ChannelConfig struct {
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// DefaultChannelConfig 默认配置
func DefaultChannelConfig() *ChannelConfig {
	return &ChannelConfig{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
}

// NewLibp2pChannel 创建新的 Channel
func NewLibp2pChannel(stream *Libp2pStream, cfg *ChannelConfig) *Libp2pChannel {
	if cfg == nil {
		cfg = DefaultChannelConfig()
	}
	return &Libp2pChannel{
		stream:       stream,
		codec:        &LengthPrefixedCodec{},
		reader:       bufio.NewReaderSize(stream, DefaultBufferSize),
		writer:       bufio.NewWriterSize(stream, DefaultBufferSize),
		readTimeout:  cfg.ReadTimeout,
		writeTimeout: cfg.WriteTimeout,
	}
}

// Send 发送消息（使用长度前缀解决粘包问题）
func (c *Libp2pChannel) Send(ctx context.Context, msg []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 1. 检查是否已关闭
	if c.closed.Load() {
		return errors.ErrChannelClosed
	}

	// 2. 检查 1 - 开始前
	if err := ctx.Err(); err != nil {
		return errors.Wrap(errors.ErrCanceled, "context canceled before send")
	}

	// 3. 设置写超时
	if c.writeTimeout > 0 {
		deadline := time.Now().Add(c.writeTimeout)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err := c.stream.SetWriteDeadline(deadline); err != nil {
			transportLog.WithField("error", err).Warn("failed to set write deadline")
		}
	}

	// 4. 使用长度前缀编解码器发送
	if err := c.codec.Encode(c.writer, msg); err != nil {
		if stderrors.Is(err, errors.ErrMessageTooLarge) {
			return err
		}
		return errors.Wrap(err, "encode message")
	}

	// 5. 检查 2 - 刷新前
	if err := ctx.Err(); err != nil {
		if resetErr := c.stream.Reset(); resetErr != nil {
			transportLog.WithField("error", resetErr).Warn("failed to reset stream")
		}
		return errors.Wrap(errors.ErrCanceled, "context canceled during send")
	}

	// 6. 刷新缓冲区
	if err := c.writer.Flush(); err != nil {
		return errors.Wrap(err, "flush buffer")
	}

	return nil
}

// Recv 接收消息
func (c *Libp2pChannel) Recv(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 1. 检查是否已关闭
	if c.closed.Load() {
		return nil, errors.ErrChannelClosed
	}

	// 2. 检查上下文
	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(errors.ErrCanceled, "context canceled before recv")
	}

	// 3. 设置读超时
	if c.readTimeout > 0 {
		deadline := time.Now().Add(c.readTimeout)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err := c.stream.SetReadDeadline(deadline); err != nil {
			transportLog.WithField("error", err).Warn("failed to set read deadline")
		}
	}

	// 4. 使用长度前缀编解码器接收
	data, err := c.codec.Decode(c.reader)
	if err != nil {
		if stderrors.Is(err, errors.ErrMessageTooLarge) || stderrors.Is(err, errors.ErrInvalidMessage) {
			return nil, err
		}
		return nil, errors.Wrap(err, "decode message")
	}

	return data, nil
}

// Close 关闭通道
func (c *Libp2pChannel) Close() error {
	// 1. 原子标记关闭状态
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 2. 刷新并关闭
	if c.writer != nil {
		c.writer.Flush()
	}

	return c.stream.Close()
}
