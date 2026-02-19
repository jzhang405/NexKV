// Package transport 提供 Transport 接口的 libp2p 实现
package transport

import (
	"time"

	"github.com/sirupsen/logrus"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// 确保实现 service.AsyncChannel 接口
var _ service.AsyncChannel = (*Libp2pAsyncChannel)(nil)

// AsyncChannelConfig 异步通道配置
type AsyncChannelConfig struct {
	SendBufferSize int           // 发送缓冲区大小（消息数量）
	SendTimeout    time.Duration // 发送超时（用于 SetWriteDeadline）
	RecvBufferSize int           // 接收缓冲区大小（消息数量）
	MaxMessageSize int           // 最大消息大小（字节），0 表示不限制
}

// DefaultAsyncChannelConfig 默认异步通道配置
func DefaultAsyncChannelConfig() *AsyncChannelConfig {
	return &AsyncChannelConfig{
		SendBufferSize: DefaultAsyncBufferSize,
		SendTimeout:    DefaultTimeout,
		RecvBufferSize: DefaultAsyncBufferSize,
		MaxMessageSize: DefaultMaxMessageSize,
	}
}

// validate 验证并修正配置参数
func (c *AsyncChannelConfig) validate() {
	c.SendBufferSize = ValidateBufferSize(c.SendBufferSize)
	c.RecvBufferSize = ValidateBufferSize(c.RecvBufferSize)
	c.SendTimeout = ValidateTimeout(c.SendTimeout)
	c.MaxMessageSize = ValidateMaxMessageSize(c.MaxMessageSize)
}

// Libp2pAsyncChannel 实现异步 Channel 接口（使用 conc 库）
type Libp2pAsyncChannel struct {
	stream *Libp2pStream
	codec  *LengthPrefixedCodec
	config *AsyncChannelConfig

	// 异步通道
	sendCh chan []byte
	recvCh chan service.MsgOrError

	// 生命周期管理（使用 conc 库）
	lifecycle *AsyncLifecycle

	// 发送错误（WaitClosed 返回）- P0 修复：使用 atomic
	sendErr AtomicError
}

// NewLibp2pAsyncChannel 创建新的异步 Channel
func NewLibp2pAsyncChannel(stream *Libp2pStream, cfg *AsyncChannelConfig) *Libp2pAsyncChannel {
	if cfg == nil {
		cfg = DefaultAsyncChannelConfig()
	}
	cfg.validate()

	c := &Libp2pAsyncChannel{
		stream:    stream,
		codec:     &LengthPrefixedCodec{},
		config:    cfg,
		sendCh:    make(chan []byte, cfg.SendBufferSize),
		recvCh:    make(chan service.MsgOrError, cfg.RecvBufferSize),
		lifecycle: NewAsyncLifecycle(),
	}

	// 使用 conc wait.Group 启动 goroutine
	c.lifecycle.Go(c.sendLoop)
	c.lifecycle.Go(c.recvLoop)

	return c
}

// sendLoop 发送循环
func (c *Libp2pAsyncChannel) sendLoop() {
	for {
		select {
		case msg, ok := <-c.sendCh:
			if !ok {
				return
			}

			if c.lifecycle.IsClosed() {
				return
			}

			// P1 修复：验证消息大小
			if err := ValidateMessageSize(msg, c.config.MaxMessageSize, "AsyncChannel"); err != nil {
				c.sendErr.Store(&err)
				return
			}

			// P1 修复：添加写超时保护
			if c.config.SendTimeout > 0 {
				if err := c.stream.SetWriteDeadline(time.Now().Add(c.config.SendTimeout)); err != nil {
					transportLog.WithFields(logrus.Fields{
						"async_component": "AsyncChannel",
						"error":           err,
					}).Warn("failed to set write deadline")
				}
			}

			if err := c.stream.WriteWithCodec(c.codec, msg); err != nil {
				c.sendErr.Store(&err)
				c.pushRecvError(err)
				return
			}

		case <-c.lifecycle.Done():
			// P0 修复：上下文取消时直接退出
			return
		}
	}
}

// recvLoop 接收循环
func (c *Libp2pAsyncChannel) recvLoop() {
	defer close(c.recvCh)

	for {
		msg, err := c.stream.ReadWithCodec(c.codec)
		if err != nil {
			if !c.lifecycle.IsClosed() {
				c.pushRecvError(err)
			}
			return
		}

		// P1 修复：验证消息大小
		if err := ValidateMessageSize(msg, c.config.MaxMessageSize, "AsyncChannel"); err != nil {
			c.pushRecvError(err)
			return
		}

		// 使用 conc 库的非阻塞发送
		if !NonBlockingSendResult(c.recvCh, service.MsgOrError{Msg: msg}, "AsyncChannel", c.lifecycle.Done()) {
			return
		}
	}
}

// pushRecvError 推送接收错误（非阻塞）
func (c *Libp2pAsyncChannel) pushRecvError(err error) {
	select {
	case c.recvCh <- service.MsgOrError{Err: err}:
	case <-c.lifecycle.Done():
	default:
		transportLog.WithFields(logrus.Fields{
			"async_component": "AsyncChannel",
			"error":           err,
		}).Warn("recvCh blocked, dropping error")
	}
}

// SendChan 返回发送通道
func (c *Libp2pAsyncChannel) SendChan() chan<- []byte {
	return c.sendCh
}

// RecvChan 返回接收通道
func (c *Libp2pAsyncChannel) RecvChan() <-chan service.MsgOrError {
	return c.recvCh
}

// Close 关闭通道（P0 修复：超时后返回错误）
func (c *Libp2pAsyncChannel) Close() error {
	return CloseAsync(
		c.lifecycle,
		func() { close(c.sendCh) },
		c.stream.Close,
		"AsyncChannel",
	)
}

// WaitClosed 等待通道关闭（P1 修复：重命名，语义更清晰）
func (c *Libp2pAsyncChannel) WaitClosed() error {
	<-c.lifecycle.Done()
	return c.sendErr.Load()
}

// WaitClosedWithTimeout 带超时的等待通道关闭
func (c *Libp2pAsyncChannel) WaitClosedWithTimeout(timeout time.Duration) error {
	select {
	case <-c.lifecycle.Done():
		return c.sendErr.Load()
	case <-time.After(timeout):
		return errors.ErrTimeout
	}
}
