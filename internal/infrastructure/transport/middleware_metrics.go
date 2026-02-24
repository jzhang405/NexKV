// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// MetricsCollector 指标收集器接口
type MetricsCollector interface {
	// RecordLatency 记录延迟
	RecordLatency(operation string, duration time.Duration)
	// RecordCount 记录计数
	RecordCount(operation string, success bool)
	// RecordSize 记录消息大小
	RecordSize(operation string, size int)
}

// MetricsMiddleware 监控中间件
//
// 收集以下指标：
// - 消息数量（发送/接收，成功/失败）
// - 消息大小（字节）
// - 处理延迟（毫秒）
type MetricsMiddleware struct {
	collector MetricsCollector
}

// NewMetricsMiddleware 创建监控中间件
func NewMetricsMiddleware(collector MetricsCollector) *MetricsMiddleware {
	if collector == nil {
		collector = NewDefaultMetricsCollector()
	}
	return &MetricsMiddleware{
		collector: collector,
	}
}

// Name 返回中间件名称
func (m *MetricsMiddleware) Name() string {
	return "metrics"
}

// Priority 返回中间件优先级
func (m *MetricsMiddleware) Priority() int {
	return service.MiddlewarePriorityMetrics
}

// InterceptSend 拦截发送消息
func (m *MetricsMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next service.SendFunc) error {
	return m.recordMetrics("send", func() error {
		return next(ctx, peer, msg)
	}, msg)
}

// InterceptReceive 拦截接收消息
func (m *MetricsMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next service.ReceiveFunc) error {
	return m.recordMetrics("receive", func() error {
		return next(ctx, peer, msg)
	}, msg)
}

// recordMetrics 记录操作指标
func (m *MetricsMiddleware) recordMetrics(operation string, fn func() error, msg model.Message) error {
	start := time.Now()
	payloadLen := 0
	if msg != nil {
		payloadLen = len(msg.Payload())
	}

	m.collector.RecordSize(operation, payloadLen)
	err := fn()

	duration := time.Since(start)
	m.collector.RecordLatency(operation, duration)
	m.collector.RecordCount(operation, err == nil)

	return err
}

// 确保实现 Middleware 接口
var _ service.Middleware = (*MetricsMiddleware)(nil)

// ============================================================================
// 默认指标收集器实现
// ============================================================================

// DefaultMetricsCollector 默认指标收集器（内存实现）
type DefaultMetricsCollector struct {
	// 计数器
	sendSuccess    atomic.Int64
	sendFailure    atomic.Int64
	receiveSuccess atomic.Int64
	receiveFailure atomic.Int64

	// 延迟统计
	sendLatencySum    atomic.Int64 // 纳秒
	receiveLatencySum atomic.Int64
	sendCount         atomic.Int64
	receiveCount      atomic.Int64

	// 大小统计
	sendSizeSum    atomic.Int64
	receiveSizeSum atomic.Int64
}

// NewDefaultMetricsCollector 创建默认指标收集器
func NewDefaultMetricsCollector() *DefaultMetricsCollector {
	return &DefaultMetricsCollector{}
}

// RecordLatency 记录延迟
func (c *DefaultMetricsCollector) RecordLatency(operation string, duration time.Duration) {
	nanos := duration.Nanoseconds()
	switch operation {
	case "send":
		c.sendLatencySum.Add(nanos)
		c.sendCount.Add(1)
	case "receive":
		c.receiveLatencySum.Add(nanos)
		c.receiveCount.Add(1)
	}
}

// RecordCount 记录计数
func (c *DefaultMetricsCollector) RecordCount(operation string, success bool) {
	switch {
	case operation == "send" && success:
		c.sendSuccess.Add(1)
	case operation == "send":
		c.sendFailure.Add(1)
	case operation == "receive" && success:
		c.receiveSuccess.Add(1)
	case operation == "receive":
		c.receiveFailure.Add(1)
	}
}

// RecordSize 记录消息大小
func (c *DefaultMetricsCollector) RecordSize(operation string, size int) {
	switch operation {
	case "send":
		c.sendSizeSum.Add(int64(size))
	case "receive":
		c.receiveSizeSum.Add(int64(size))
	}
}

// MetricsSnapshot 指标快照
type MetricsSnapshot struct {
	SendTotal         int64
	SendSuccess       int64
	SendFailure       int64
	SendAvgLatency    time.Duration
	SendAvgSize       int64
	ReceiveTotal      int64
	ReceiveSuccess    int64
	ReceiveFailure    int64
	ReceiveAvgLatency time.Duration
	ReceiveAvgSize    int64
}

// Snapshot 获取指标快照
func (c *DefaultMetricsCollector) Snapshot() MetricsSnapshot {
	sendCount := c.sendCount.Load()
	receiveCount := c.receiveCount.Load()

	snap := MetricsSnapshot{
		SendSuccess:    c.sendSuccess.Load(),
		SendFailure:    c.sendFailure.Load(),
		ReceiveSuccess: c.receiveSuccess.Load(),
		ReceiveFailure: c.receiveFailure.Load(),
	}

	snap.SendTotal = snap.SendSuccess + snap.SendFailure
	snap.ReceiveTotal = snap.ReceiveSuccess + snap.ReceiveFailure

	if sendCount > 0 {
		snap.SendAvgLatency = time.Duration(c.sendLatencySum.Load() / sendCount)
		snap.SendAvgSize = c.sendSizeSum.Load() / sendCount
	}

	if receiveCount > 0 {
		snap.ReceiveAvgLatency = time.Duration(c.receiveLatencySum.Load() / receiveCount)
		snap.ReceiveAvgSize = c.receiveSizeSum.Load() / receiveCount
	}

	return snap
}

// Reset 重置所有指标
func (c *DefaultMetricsCollector) Reset() {
	c.sendSuccess.Store(0)
	c.sendFailure.Store(0)
	c.receiveSuccess.Store(0)
	c.receiveFailure.Store(0)
	c.sendLatencySum.Store(0)
	c.receiveLatencySum.Store(0)
	c.sendCount.Store(0)
	c.receiveCount.Store(0)
	c.sendSizeSum.Store(0)
	c.receiveSizeSum.Store(0)
}

// 确保实现 MetricsCollector 接口
var _ MetricsCollector = (*DefaultMetricsCollector)(nil)
