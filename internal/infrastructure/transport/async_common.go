// Package transport 提供 Transport 接口的 libp2p 实现
package transport

import (
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/sirupsen/logrus"
)

// transportLog 使用 logrus 结构化日志
var transportLog = logrus.WithField("component", "transport")

// 异步操作常量
const (
	// DefaultAsyncBufferSize 默认异步缓冲区大小
	DefaultAsyncBufferSize = 256
	// MinAsyncBufferSize 最小异步缓冲区大小
	MinAsyncBufferSize = 16
	// MaxAsyncBufferSize 最大异步缓冲区大小
	MaxAsyncBufferSize = 1024
	// DefaultTimeout 默认超时时间
	DefaultTimeout = 30 * time.Second
	// DefaultMaxMessageSize 默认最大消息大小 (10MB)
	DefaultMaxMessageSize = 10 * 1024 * 1024
	// CloseTimeout 关闭超时（CI 环境使用更短的超时）
	CloseTimeout = 1 * time.Second
	// CloseFinalTimeout 最终关闭超时
	CloseFinalTimeout = 500 * time.Millisecond
)

// ValidateBufferSize 验证缓冲区大小
func ValidateBufferSize(size int) int {
	if size < MinAsyncBufferSize {
		return DefaultAsyncBufferSize
	}
	if size > MaxAsyncBufferSize {
		return MaxAsyncBufferSize
	}
	return size
}

// ValidateTimeout 验证超时时间
func ValidateTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return DefaultTimeout
	}
	return timeout
}

// ValidateMaxMessageSize 验证最大消息大小
func ValidateMaxMessageSize(maxSize int) int {
	if maxSize <= 0 {
		return DefaultMaxMessageSize
	}
	return maxSize
}

// AtomicError atomic 错误存储辅助类型
type AtomicError struct {
	ptr atomic.Pointer[error]
}

// Store 存储错误
func (e *AtomicError) Store(err *error) {
	e.ptr.Store(err)
}

// Load 加载错误
func (e *AtomicError) Load() error {
	if p := e.ptr.Load(); p != nil {
		return *p
	}
	return nil
}

// NonBlockingSendError 非阻塞发送错误
func NonBlockingSendError(errCh chan error, err error, componentName string) {
	if errCh == nil {
		return
	}
	select {
	case errCh <- err:
		// 成功发送
	default:
		// Channel 满或已关闭，记录警告
		transportLog.WithFields(logrus.Fields{
			"async_component": componentName,
			"error":           err,
		}).Warn("error channel full or closed, dropping error")
	}
}

// NonBlockingSendResult 非阻塞发送读取结果
func NonBlockingSendResult[T any](ch chan T, result T, componentName string, ctxDone <-chan struct{}) bool {
	select {
	case ch <- result:
		return true
	case <-ctxDone:
		return false
	default:
		transportLog.WithFields(logrus.Fields{
			"async_component": componentName,
		}).Warn("result channel blocked, dropping result")
		return false
	}
}

// ValidateMessageSize 验证消息大小
func ValidateMessageSize(data []byte, maxSize int, componentName string) error {
	if maxSize > 0 && len(data) > maxSize {
		transportLog.WithFields(logrus.Fields{
			"async_component": componentName,
			"msg_size":        len(data),
			"max_size":        maxSize,
		}).Warn("message size exceeds limit")
		return errors.ErrMessageTooLarge
	}
	return nil
}

// DrainWriteQueue 清空写入队列
func DrainWriteQueue(ch <-chan service.WriteRequest, callback func(service.WriteRequest)) int {
	count := 0
	for {
		select {
		case req, ok := <-ch:
			if !ok {
				// Channel 已关闭
				return count
			}
			callback(req)
			count++
		default:
			// Channel 为空
			return count
		}
	}
}
