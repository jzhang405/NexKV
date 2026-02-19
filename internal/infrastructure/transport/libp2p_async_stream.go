// Package transport 提供 Transport 接口的 libp2p 实现
package transport

import (
	"io"
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

// Libp2pAsyncStream 实现异步 Stream 接口（使用 conc 库）
type Libp2pAsyncStream struct {
	stream *Libp2pStream
	codec  *LengthPrefixedCodec
	config *AsyncStreamConfig

	// 异步通道
	readCh  chan service.ReadResult
	writeCh chan service.WriteRequest

	// 生命周期管理（使用 conc 库）
	lifecycle *AsyncLifecycle

	// 写入错误（WaitClosed 返回）- P0 修复：使用 atomic
	writeErr AtomicError
}

// NewLibp2pAsyncStream 创建新的异步 Stream
func NewLibp2pAsyncStream(stream *Libp2pStream, cfg *AsyncStreamConfig) *Libp2pAsyncStream {
	if cfg == nil {
		cfg = DefaultAsyncStreamConfig()
	}
	cfg.validate()

	s := &Libp2pAsyncStream{
		stream:    stream,
		codec:     &LengthPrefixedCodec{},
		config:    cfg,
		readCh:    make(chan service.ReadResult, cfg.ReadBufferSize),
		writeCh:   make(chan service.WriteRequest, cfg.WriteBufferSize),
		lifecycle: NewAsyncLifecycle(),
	}

	// 使用 conc wait.Group 启动 goroutine
	s.lifecycle.Go(s.readLoop)
	s.lifecycle.Go(s.writeLoop)

	return s
}

// readLoop 读取循环
func (s *Libp2pAsyncStream) readLoop() {
	defer close(s.readCh)

	for {
		data, err := s.stream.ReadWithCodec(s.codec)
		if err != nil {
			if !s.lifecycle.IsClosed() {
				if err == io.EOF {
					s.pushReadResultNonBlocking(nil, nil)
				} else {
					s.pushReadResultNonBlocking(nil, err)
				}
			}
			return
		}

		// P1 修复：验证消息大小
		if err := ValidateMessageSize(data, s.config.MaxMessageSize, "AsyncStream"); err != nil {
			s.pushReadResultNonBlocking(nil, err)
			return
		}

		// 使用 conc 库的非阻塞发送
		if !NonBlockingSendResult(s.readCh, service.ReadResult{Data: data}, "AsyncStream", s.lifecycle.Done()) {
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

			if s.lifecycle.IsClosed() {
				NonBlockingSendError(req.Err, errors.ErrChannelClosed, "AsyncStream")
				return
			}

			// P1 修复：验证消息大小
			if err := ValidateMessageSize(req.Data, s.config.MaxMessageSize, "AsyncStream"); err != nil {
				NonBlockingSendError(req.Err, err, "AsyncStream")
				s.writeErr.Store(&err)
				return
			}

			// P1 修复：添加写超时保护
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

		case <-s.lifecycle.Done():
			s.drainWriteQueue()
			return
		}
	}
}

// pushReadResultNonBlocking 非阻塞推送读取结果
func (s *Libp2pAsyncStream) pushReadResultNonBlocking(data []byte, err error) {
	select {
	case s.readCh <- service.ReadResult{Data: data, Err: err}:
	case <-s.lifecycle.Done():
	default:
		transportLog.WithFields(logrus.Fields{
			"async_component": "AsyncStream",
		}).Warn("readCh blocked, dropping result")
	}
}

// drainWriteQueue 清空写入队列（P1 修复：检查 channel 关闭）
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

// Close 关闭流（P0 修复：超时后返回错误）
func (s *Libp2pAsyncStream) Close() error {
	return CloseAsync(
		s.lifecycle,
		func() { close(s.writeCh) },
		s.stream.Close,
		"AsyncStream",
	)
}

// WaitClosed 等待流关闭（P1 修复：重命名，语义更清晰）
func (s *Libp2pAsyncStream) WaitClosed() error {
	<-s.lifecycle.Done()
	return s.writeErr.Load()
}

// WaitClosedWithTimeout 带超时的等待流关闭
func (s *Libp2pAsyncStream) WaitClosedWithTimeout(timeout time.Duration) error {
	select {
	case <-s.lifecycle.Done():
		return s.writeErr.Load()
	case <-time.After(timeout):
		return errors.ErrTimeout
	}
}
