// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/prometheus/client_golang/prometheus"
)

// ========================================
// 全局 RPC 指标实例
// ========================================

var (
	// 全局 RPC 指标
	rpcMetrics = NewRPCMetrics()
)

// init 注册 Prometheus 指标
func init() {
	// 注册 Stream 指标
	prometheus.MustRegister(rpcMetrics.StreamsActive)
	prometheus.MustRegister(rpcMetrics.StreamsCreated)
	prometheus.MustRegister(rpcMetrics.StreamsReused)
	prometheus.MustRegister(rpcMetrics.StreamsClosed)
	prometheus.MustRegister(rpcMetrics.StreamCacheHits)
	prometheus.MustRegister(rpcMetrics.StreamCacheMisses)
	prometheus.MustRegister(rpcMetrics.StreamOpenFailures)

	// 注册 Call 指标
	prometheus.MustRegister(rpcMetrics.CallTotal)
	prometheus.MustRegister(rpcMetrics.CallSuccess)
	prometheus.MustRegister(rpcMetrics.CallErrors)
	prometheus.MustRegister(rpcMetrics.CallLatency)
	prometheus.MustRegister(rpcMetrics.CallTimeout)

	// 注册 Batch 指标
	prometheus.MustRegister(rpcMetrics.BatchCallsTotal)
	prometheus.MustRegister(rpcMetrics.BatchCallSize)
	prometheus.MustRegister(rpcMetrics.BatchCallErrors)

	// 注册 Connection 指标
	prometheus.MustRegister(rpcMetrics.ConnectionsActive)
	prometheus.MustRegister(rpcMetrics.ConnectionsTotal)
	prometheus.MustRegister(rpcMetrics.ConnectionsFailed)

	// 注册 Peer 指标
	prometheus.MustRegister(rpcMetrics.PeerCallsTotal)
	prometheus.MustRegister(rpcMetrics.PeerCallErrors)
	prometheus.MustRegister(rpcMetrics.PeerCallLatency)
}

// GetRPCMetrics 获取全局 RPC 指标
func GetRPCMetrics() *RPCMetrics {
	return rpcMetrics
}

// ========================================
// RPC 监控指标
// ========================================

// RPCMetrics RPC 监控指标
type RPCMetrics struct {
	// Stream 指标
	StreamsActive      prometheus.Gauge   // 活跃 Stream 数量
	StreamsCreated     prometheus.Counter // 创建的 Stream 总数
	StreamsReused      prometheus.Counter // 复用的 Stream 总数
	StreamsClosed      prometheus.Counter // 关闭的 Stream 总数
	StreamCacheHits    prometheus.Counter // Stream 缓存命中次数
	StreamCacheMisses  prometheus.Counter // Stream 缓存未命中次数
	StreamOpenFailures prometheus.Counter // Stream 打开失败次数

	// Call 指标
	CallTotal   prometheus.Counter     // RPC 调用总数
	CallSuccess prometheus.Counter     // RPC 调用成功数
	CallErrors  *prometheus.CounterVec // RPC 调用错误数（按错误类型）
	CallLatency prometheus.Histogram   // RPC 调用延迟分布
	CallTimeout prometheus.Counter     // RPC 调用超时次数

	// Batch 指标
	BatchCallsTotal prometheus.Counter   // Batch 调用总数
	BatchCallSize   prometheus.Histogram // Batch 调用大小分布
	BatchCallErrors prometheus.Counter   // Batch 调用错误数

	// Connection 指标
	ConnectionsActive prometheus.Gauge   // 活跃连接数
	ConnectionsTotal  prometheus.Counter // 连接总数
	ConnectionsFailed prometheus.Counter // 连接失败数

	// Peer 级别指标
	PeerCallsTotal  *prometheus.CounterVec   // Peer 调用总数
	PeerCallErrors  *prometheus.CounterVec   // Peer 调用错误数
	PeerCallLatency *prometheus.HistogramVec // Peer 调用延迟
}

// NewRPCMetrics 创建 RPC 监控指标
func NewRPCMetrics() *RPCMetrics {
	return &RPCMetrics{
		// Stream 指标
		StreamsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nexkv_rpc_streams_active",
			Help: "Number of active libp2p streams",
		}),
		StreamsCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_streams_created_total",
			Help: "Total number of streams created",
		}),
		StreamsReused: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_streams_reused_total",
			Help: "Total number of streams reused from cache",
		}),
		StreamsClosed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_streams_closed_total",
			Help: "Total number of streams closed",
		}),
		StreamCacheHits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_stream_cache_hits_total",
			Help: "Total number of stream cache hits",
		}),
		StreamCacheMisses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_stream_cache_misses_total",
			Help: "Total number of stream cache misses",
		}),
		StreamOpenFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_stream_open_failures_total",
			Help: "Total number of stream open failures",
		}),

		// Call 指标
		CallTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_calls_total",
			Help: "Total number of RPC calls",
		}),
		CallSuccess: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_calls_success_total",
			Help: "Total number of successful RPC calls",
		}),
		CallErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nexkv_rpc_calls_errors_total",
				Help: "Total number of RPC call errors",
			},
			[]string{"error_type"},
		),
		CallLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "nexkv_rpc_call_latency_seconds",
			Help:    "RPC call latency distribution",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
		CallTimeout: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_calls_timeout_total",
			Help: "Total number of RPC call timeouts",
		}),

		// Batch 指标
		BatchCallsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_batch_calls_total",
			Help: "Total number of batch RPC calls",
		}),
		BatchCallSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "nexkv_rpc_batch_call_size",
			Help:    "Batch RPC call size distribution",
			Buckets: []float64{1, 2, 5, 10, 20, 50, 100},
		}),
		BatchCallErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_batch_calls_errors_total",
			Help: "Total number of batch RPC call errors",
		}),

		// Connection 指标
		ConnectionsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nexkv_rpc_connections_active",
			Help: "Number of active RPC connections",
		}),
		ConnectionsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_connections_total",
			Help: "Total number of RPC connections",
		}),
		ConnectionsFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_connections_failed_total",
			Help: "Total number of failed RPC connections",
		}),

		// Peer 级别指标
		PeerCallsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nexkv_rpc_peer_calls_total",
				Help: "Total number of RPC calls per peer",
			},
			[]string{"peer_id"},
		),
		PeerCallErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nexkv_rpc_peer_call_errors_total",
				Help: "Total number of RPC call errors per peer",
			},
			[]string{"peer_id", "error_type"},
		),
		PeerCallLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "nexkv_rpc_peer_call_latency_seconds",
				Help:    "RPC call latency distribution per peer",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"peer_id"},
		),
	}
}

// ========================================
// Stream 指标记录
// ========================================

// RecordStreamCreated 记录 Stream 创建
func (m *RPCMetrics) RecordStreamCreated(peerID string) {
	m.StreamsCreated.Inc()
	m.StreamsActive.Inc()
	m.StreamCacheMisses.Inc() // 新创建视为缓存未命中
	m.PeerCallsTotal.WithLabelValues(peerID).Inc()

	logging.WithField("peer_id", peerID).Debug("Stream 已创建")
}

// RecordStreamReused 记录 Stream 复用
func (m *RPCMetrics) RecordStreamReused(peerID string) {
	m.StreamsReused.Inc()
	m.StreamsActive.Inc()
	m.StreamCacheHits.Inc()

	logging.WithField("peer_id", peerID).Debug("Stream 已复用")
}

// RecordStreamClosed 记录 Stream 关闭
func (m *RPCMetrics) RecordStreamClosed(peerID string) {
	m.StreamsClosed.Inc()
	m.StreamsActive.Dec()

	logging.WithField("peer_id", peerID).Debug("Stream 已关闭")
}

// RecordStreamOpenFailure 记录 Stream 打开失败
func (m *RPCMetrics) RecordStreamOpenFailure(peerID string, err error) {
	m.StreamOpenFailures.Inc()
	m.PeerCallErrors.WithLabelValues(peerID, getErrorType(err)).Inc()

	logging.WithFields(map[string]any{
		"peer_id": peerID,
		"error":   err,
	}).Warn("Stream 打开失败")
}

// ========================================
// Call 指标记录
// ========================================

// RecordCallStart 记录 RPC 调用开始
func (m *RPCMetrics) RecordCallStart(peerID, method string) startTimer {
	m.CallTotal.Inc()
	m.PeerCallsTotal.WithLabelValues(peerID).Inc()

	return startTimer{
		metrics: m,
		start:   time.Now(),
		peerID:  peerID,
		method:  method,
	}
}

// RecordCallSuccess 记录 RPC 调用成功
func (m *RPCMetrics) RecordCallSuccess(peerID, method string, duration time.Duration) {
	m.CallSuccess.Inc()
	m.CallLatency.Observe(duration.Seconds())
	m.PeerCallLatency.WithLabelValues(peerID).Observe(duration.Seconds())

	logging.WithFields(map[string]any{
		"peer_id":  peerID,
		"method":   method,
		"duration": duration,
	}).Debug("RPC 调用成功")
}

// RecordCallError 记录 RPC 调用错误
func (m *RPCMetrics) RecordCallError(peerID, method string, err error, duration time.Duration) {
	errorType := getErrorType(err)
	m.CallErrors.WithLabelValues(errorType).Inc()
	m.PeerCallErrors.WithLabelValues(peerID, errorType).Inc()

	if IsTimeout(err) {
		m.CallTimeout.Inc()
	}

	logging.WithFields(map[string]any{
		"peer_id":  peerID,
		"method":   method,
		"error":    err,
		"duration": duration,
	}).Debug("RPC 调用错误")
}

// ========================================
// Batch 指标记录
// ========================================

// RecordBatchCall 记录 Batch 调用
func (m *RPCMetrics) RecordBatchCall(peerID string, size int, duration time.Duration, err error) {
	m.BatchCallsTotal.Inc()
	m.BatchCallSize.Observe(float64(size))

	if err != nil {
		m.BatchCallErrors.Inc()
		m.PeerCallErrors.WithLabelValues(peerID, getErrorType(err)).Inc()
	}

	logging.WithFields(map[string]any{
		"peer_id":  peerID,
		"size":     size,
		"duration": duration,
		"error":    err,
	}).Debug("Batch 调用完成")
}

// ========================================
// Connection 指标记录
// ========================================

// RecordConnectionOpened 记录连接打开
func (m *RPCMetrics) RecordConnectionOpened(peerID string) {
	m.ConnectionsTotal.Inc()
	m.ConnectionsActive.Inc()

	logging.WithField("peer_id", peerID).Debug("连接已打开")
}

// RecordConnectionClosed 记录连接关闭
func (m *RPCMetrics) RecordConnectionClosed(peerID string) {
	m.ConnectionsActive.Dec()

	logging.WithField("peer_id", peerID).Debug("连接已关闭")
}

// RecordConnectionFailed 记录连接失败
func (m *RPCMetrics) RecordConnectionFailed(peerID string, err error) {
	m.ConnectionsFailed.Inc()
	m.PeerCallErrors.WithLabelValues(peerID, getErrorType(err)).Inc()

	logging.WithFields(map[string]any{
		"peer_id": peerID,
		"error":   err,
	}).Warn("连接失败")
}

// ========================================
// 辅助类型和函数
// ========================================

// startTimer 用于测量调用持续时间
type startTimer struct {
	metrics *RPCMetrics
	start   time.Time
	peerID  string
	method  string
}

// Stop 停止计时并记录成功
func (st startTimer) Stop() {
	duration := time.Since(st.start)
	st.metrics.RecordCallSuccess(st.peerID, st.method, duration)
}

// StopWithError 停止计时并记录错误
func (st startTimer) StopWithError(err error) {
	duration := time.Since(st.start)
	if err != nil {
		st.metrics.RecordCallError(st.peerID, st.method, err, duration)
	} else {
		st.metrics.RecordCallSuccess(st.peerID, st.method, duration)
	}
}

// getErrorType 从错误中提取错误类型
func getErrorType(err error) string {
	if err == nil {
		return "none"
	}

	// 检查 RPC 错误码
	if rpcErr, ok := err.(*RPCError); ok {
		switch rpcErr.Code {
		case ErrCodeTimeout:
			return "timeout"
		case ErrCodePeerUnavailable:
			return "peer_unavailable"
		case ErrCodeTooManyRequests:
			return "rate_limited"
		case ErrCodeInvalidArgument:
			return "invalid_argument"
		case ErrCodeQuorumNotReached:
			return "quorum_not_reached"
		case ErrCodeConflict:
			return "conflict"
		default:
			return "rpc_error"
		}
	}

	// 检查 context 错误
	if err == context.Canceled {
		return "canceled"
	}
	if err == context.DeadlineExceeded {
		return "deadline_exceeded"
	}

	// 默认错误类型
	return "unknown"
}
