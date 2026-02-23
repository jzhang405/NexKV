// Package transport 提供 Transport 接口的 libp2p 实现
package transport

import (
	"context"
	"sync"
	"sync/atomic"
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

// Libp2pAsyncChannel 实现异步 Channel 接口
type Libp2pAsyncChannel struct {
	stream *Libp2pStream
	codec  *LengthPrefixedCodec
	config *AsyncChannelConfig

	// 异步通道
	sendCh chan []byte
	recvCh chan service.MsgOrError

	// 生命周期管理（直接使用 GoroutineProvider）
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	closed   atomic.Bool
	provider service.GoroutineProvider // 必需：集中式协程池

	// 发送错误（WaitClosed 返回）
	sendErr AtomicError
}

// NewLibp2pAsyncChannel 创建新的异步 Channel
// provider 参数可选，为 nil 时直接使用 goroutine
func NewLibp2pAsyncChannel(provider service.GoroutineProvider, stream *Libp2pStream, cfg *AsyncChannelConfig) *Libp2pAsyncChannel {
	if cfg == nil {
		cfg = DefaultAsyncChannelConfig()
	}
	cfg.validate()

	ctx, cancel := context.WithCancel(context.Background())
	c := &Libp2pAsyncChannel{
		stream:   stream,
		codec:    &LengthPrefixedCodec{},
		config:   cfg,
		sendCh:   make(chan []byte, cfg.SendBufferSize),
		recvCh:   make(chan service.MsgOrError, cfg.RecvBufferSize),
		ctx:      ctx,
		cancel:   cancel,
		provider: provider,
	}

	// 启动 goroutine
	c.startLoops()

	return c
}

// startLoops 启动发送和接收循环
func (c *Libp2pAsyncChannel) startLoops() {
	if c.provider != nil {
		c.wg.Add(2)
		_ = c.provider.Submit(c.ctx, func(ctx context.Context) {
			defer c.wg.Done()
			c.sendLoop()
		})
		_ = c.provider.Submit(c.ctx, func(ctx context.Context) {
			defer c.wg.Done()
			c.recvLoop()
		})
	} else {
		c.wg.Add(2)
		go func() {
			defer c.wg.Done()
			c.sendLoop()
		}()
		go func() {
			defer c.wg.Done()
			c.recvLoop()
		}()
	}
}

// sendLoop 发送循环
func (c *Libp2pAsyncChannel) sendLoop() {
	for {
		select {
		case msg, ok := <-c.sendCh:
			if !ok {
				return
			}

			if c.IsClosed() {
				return
			}

			// 验证消息大小
			if err := ValidateMessageSize(msg, c.config.MaxMessageSize, "AsyncChannel"); err != nil {
				c.sendErr.Store(&err)
				return
			}

			// 添加写超时保护
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

		case <-c.ctx.Done():
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
			if !c.IsClosed() {
				c.pushRecvError(err)
			}
			return
		}

		// 验证消息大小
		if err := ValidateMessageSize(msg, c.config.MaxMessageSize, "AsyncChannel"); err != nil {
			c.pushRecvError(err)
			return
		}

		// 非阻塞发送
		if !NonBlockingSendResult(c.recvCh, service.MsgOrError{Msg: msg}, "AsyncChannel", c.ctx.Done()) {
			return
		}
	}
}

// pushRecvError 推送接收错误（非阻塞）
func (c *Libp2pAsyncChannel) pushRecvError(err error) {
	select {
	case c.recvCh <- service.MsgOrError{Err: err}:
	case <-c.ctx.Done():
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

// Close 关闭通道
func (c *Libp2pAsyncChannel) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}

	// 1. 取消上下文
	c.cancel()

	// 2. 关闭发送 channel
	close(c.sendCh)

	// 3. 等待 goroutine 退出
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 正常退出，关闭底层流
		return c.stream.Close()
	case <-time.After(CloseTimeout):
		// 超时
		transportLog.WithFields(logrus.Fields{
			"async_component": "AsyncChannel",
			"timeout":         CloseTimeout,
		}).Warn("close timeout, forcing stream close")
	}

	// 4. 强制关闭底层流
	streamErr := c.stream.Close()

	// 5. 再次等待
	select {
	case <-done:
		return streamErr
	case <-time.After(CloseFinalTimeout):
		transportLog.WithFields(logrus.Fields{
			"async_component": "AsyncChannel",
		}).Error("goroutine leak detected after stream close")
		if streamErr != nil {
			return streamErr
		}
		return errors.Wrap(errors.ErrAsyncExecFailed, "goroutine leak detected")
	}
}

// IsClosed 检查是否已关闭
func (c *Libp2pAsyncChannel) IsClosed() bool {
	return c.closed.Load()
}

// WaitClosed 等待通道关闭
func (c *Libp2pAsyncChannel) WaitClosed() error {
	<-c.ctx.Done()
	return c.sendErr.Load()
}

// WaitClosedWithTimeout 带超时的等待通道关闭
func (c *Libp2pAsyncChannel) WaitClosedWithTimeout(timeout time.Duration) error {
	select {
	case <-c.ctx.Done():
		return c.sendErr.Load()
	case <-time.After(timeout):
		return errors.ErrTimeout
	}
}
