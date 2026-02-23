// Package transport 提供 Transport 接口的 libp2p 实现
package transport

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// 确保实现 service.AsyncStream 接口
var _ service.AsyncStream = (*Libp2pAsyncStream)(nil)

// AsyncStreamConfig 异步流配置
type AsyncStreamConfig struct {
	ReadBufferSize  int           // 读取缓冲区大小（消息数量）
	ReadTimeout     time.Duration // 读取超时
	WriteBufferSize int           // 写入缓冲区大小（消息数量）
	WriteTimeout    time.Duration // 写入超时（用于 SetWriteDeadline）
	MaxMessageSize  int           // 最大消息大小（字节），0 表示不限制
}

// DefaultAsyncStreamConfig 默认异步流配置
func DefaultAsyncStreamConfig() *AsyncStreamConfig {
	return &AsyncStreamConfig{
		ReadBufferSize:  DefaultAsyncBufferSize,
		ReadTimeout:     DefaultTimeout,
		WriteBufferSize: DefaultAsyncBufferSize,
		WriteTimeout:    DefaultTimeout,
		MaxMessageSize:  DefaultMaxMessageSize,
	}
}

// validate 验证并修正配置参数
func (c *AsyncStreamConfig) validate() {
	c.ReadBufferSize = ValidateBufferSize(c.ReadBufferSize)
	c.WriteBufferSize = ValidateBufferSize(c.WriteBufferSize)
	c.ReadTimeout = ValidateTimeout(c.ReadTimeout)
	c.WriteTimeout = ValidateTimeout(c.WriteTimeout)
	c.MaxMessageSize = ValidateMaxMessageSize(c.MaxMessageSize)
}

// Libp2pAsyncStream 实现异步 Stream 接口
type Libp2pAsyncStream struct {
	stream *Libp2pStream
	codec  *LengthPrefixedCodec
	config *AsyncStreamConfig

	// 异步通道
	readCh  chan service.ReadResult
	writeCh chan service.WriteRequest

	// 生命周期管理（直接使用 GoroutineProvider）
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	closed   atomic.Bool
	provider service.GoroutineProvider // 必需：集中式协程池

	// 写入错误（WaitClosed 返回）
	writeErr AtomicError
}

// NewLibp2pAsyncStream 创建新的异步 Stream
// provider 参数可选，为 nil 时直接使用 goroutine
func NewLibp2pAsyncStream(provider service.GoroutineProvider, stream *Libp2pStream, cfg *AsyncStreamConfig) *Libp2pAsyncStream {
	if cfg == nil {
		cfg = DefaultAsyncStreamConfig()
	}
	cfg.validate()

	ctx, cancel := context.WithCancel(context.Background())
	s := &Libp2pAsyncStream{
		stream:   stream,
		codec:    &LengthPrefixedCodec{},
		config:   cfg,
		readCh:   make(chan service.ReadResult, cfg.ReadBufferSize),
		writeCh:  make(chan service.WriteRequest, cfg.WriteBufferSize),
		ctx:      ctx,
		cancel:   cancel,
		provider: provider,
	}

	// 启动 goroutine
	s.startLoops()

	return s
}

// startLoops 启动读写循环
func (s *Libp2pAsyncStream) startLoops() {
	if s.provider != nil {
		s.wg.Add(2)
		_ = s.provider.Submit(s.ctx, func(ctx context.Context) {
			defer s.wg.Done()
			s.readLoop()
		})
		_ = s.provider.Submit(s.ctx, func(ctx context.Context) {
			defer s.wg.Done()
			s.writeLoop()
		})
	} else {
		s.wg.Add(2)
		go func() {
			defer s.wg.Done()
			s.readLoop()
		}()
		go func() {
			defer s.wg.Done()
			s.writeLoop()
		}()
	}
}

// readLoop 读取循环
func (s *Libp2pAsyncStream) readLoop() {
	defer close(s.readCh)

	for {
		data, err := s.stream.ReadWithCodec(s.codec)
		if err != nil {
			if !s.IsClosed() {
				if err == io.EOF {
					s.pushReadResultNonBlocking(nil, nil)
				} else {
					s.pushReadResultNonBlocking(nil, err)
				}
			}
			return
		}

		// 验证消息大小
		if err := ValidateMessageSize(data, s.config.MaxMessageSize, "AsyncStream"); err != nil {
			s.pushReadResultNonBlocking(nil, err)
			return
		}

		// 非阻塞发送
		if !NonBlockingSendResult(s.readCh, service.ReadResult{Data: data}, "AsyncStream", s.ctx.Done()) {
			return
		}
	}
}

// writeLoop 写入循环
func (s *Libp2pAsyncStream) writeLoop() {
	for {
		select {
		case req, ok := <-s.writeCh:
			if !ok {
				return
			}

			if s.IsClosed() {
				NonBlockingSendError(req.Err, errors.ErrChannelClosed, "AsyncStream")
				return
			}

			// 验证消息大小
			if err := ValidateMessageSize(req.Data, s.config.MaxMessageSize, "AsyncStream"); err != nil {
				NonBlockingSendError(req.Err, err, "AsyncStream")
				s.writeErr.Store(&err)
				return
			}

			// 添加写超时保护
			if s.config.WriteTimeout > 0 {
				if err := s.stream.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout)); err != nil {
					transportLog.WithFields(logrus.Fields{
						"async_component": "AsyncStream",
						"error":           err,
					}).Warn("failed to set write deadline")
				}
			}

			err := s.stream.WriteWithCodec(s.codec, req.Data)
			NonBlockingSendError(req.Err, err, "AsyncStream")

			if err != nil {
				s.writeErr.Store(&err)
				return
			}

		case <-s.ctx.Done():
			s.drainWriteQueue()
			return
		}
	}
}

// pushReadResultNonBlocking 非阻塞推送读取结果
func (s *Libp2pAsyncStream) pushReadResultNonBlocking(data []byte, err error) {
	select {
	case s.readCh <- service.ReadResult{Data: data, Err: err}:
	case <-s.ctx.Done():
	default:
		transportLog.WithFields(logrus.Fields{
			"async_component": "AsyncStream",
		}).Warn("readCh blocked, dropping result")
	}
}

// drainWriteQueue 清空写入队列
func (s *Libp2pAsyncStream) drainWriteQueue() {
	DrainWriteQueue(s.writeCh, func(req service.WriteRequest) {
		NonBlockingSendError(req.Err, errors.ErrCanceled, "AsyncStream")
	})
}

// ReadChan 返回读取通道
func (s *Libp2pAsyncStream) ReadChan() <-chan service.ReadResult {
	return s.readCh
}

// WriteChan 返回写入通道
func (s *Libp2pAsyncStream) WriteChan() chan<- service.WriteRequest {
	return s.writeCh
}

// Close 关闭流
func (s *Libp2pAsyncStream) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	// 1. 取消上下文
	s.cancel()

	// 2. 关闭写入 channel
	close(s.writeCh)

	// 3. 等待 goroutine 退出
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 正常退出，关闭底层流
		return s.stream.Close()
	case <-time.After(CloseTimeout):
		// 超时
		transportLog.WithFields(logrus.Fields{
			"async_component": "AsyncStream",
			"timeout":         CloseTimeout,
		}).Warn("close timeout, forcing stream close")
	}

	// 4. 强制关闭底层流
	streamErr := s.stream.Close()

	// 5. 再次等待
	select {
	case <-done:
		return streamErr
	case <-time.After(CloseFinalTimeout):
		transportLog.WithFields(logrus.Fields{
			"async_component": "AsyncStream",
		}).Error("goroutine leak detected after stream close")
		if streamErr != nil {
			return streamErr
		}
		return errors.Wrap(errors.ErrAsyncExecFailed, "goroutine leak detected")
	}
}

// IsClosed 检查是否已关闭
func (s *Libp2pAsyncStream) IsClosed() bool {
	return s.closed.Load()
}

// WaitClosed 等待流关闭
func (s *Libp2pAsyncStream) WaitClosed() error {
	<-s.ctx.Done()
	return s.writeErr.Load()
}

// WaitClosedWithTimeout 带超时的等待流关闭
func (s *Libp2pAsyncStream) WaitClosedWithTimeout(timeout time.Duration) error {
	select {
	case <-s.ctx.Done():
		return s.writeErr.Load()
	case <-time.After(timeout):
		return errors.ErrTimeout
	}
}
