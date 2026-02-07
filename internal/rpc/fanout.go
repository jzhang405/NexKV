// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/prometheus/client_golang/prometheus"
)

// ========================================
// Fanout 请求和响应
// ========================================

// FanoutRequest Fanout 请求
type FanoutRequest struct {
	Method string    // RPC 方法名
	Body   []byte    // 请求体
	Peers  []peer.ID // 目标 peer 列表

	// 转发控制（防循环）
	ForwardedPeers map[peer.ID]struct{} // 已转发的 peer 集合
	Hops           uint8                // 转发跳数（用于多跳转发）
}

// NewFanoutRequest 创建 Fanout 请求
func NewFanoutRequest(method string, body []byte, peers []peer.ID) *FanoutRequest {
	return &FanoutRequest{
		Method:         method,
		Body:           body,
		Peers:          peers,
		ForwardedPeers: make(map[peer.ID]struct{}),
		Hops:           1, // 默认 1 跳
	}
}

// FanoutResult Fanout 结果
type FanoutResult struct {
	Responses  []FanoutResponse // 所有响应
	Success    int              // 成功数量
	Failed     int              // 失败数量
	Timeout    int              // 超时数量
	TotalPeers int              // 总 peer 数
	Duration   time.Duration    // 总耗时
	Quorum     *QuorumResult    // Quorum 结果（仅 Quorum 模式）
}

// IsSuccess 判断响应是否成功
func (r *FanoutResponse) IsSuccess() bool {
	return r.Error == nil
}

// ========================================
// Fanout 监控指标
// ========================================

// FanoutMetrics Fanout 指标
type FanoutMetrics struct {
	// 调用指标
	FanoutTotal   prometheus.Counter
	FanoutSuccess prometheus.Counter
	FanoutFailed  prometheus.Counter
	FanoutTimeout prometheus.Counter

	// 延迟指标
	FanoutLatency prometheus.Histogram

	// 响应模式分布
	FireForgetCount prometheus.Counter
	QuorumCount     prometheus.Counter
	WaitAllCount    prometheus.Counter

	// Peer 级别指标
	PeerSuccess *prometheus.CounterVec
	PeerFailed  *prometheus.CounterVec
	PeerTimeout *prometheus.CounterVec

	// Hops/转发相关指标
	FanoutForwardTotal  prometheus.Counter     // 总转发次数
	FanoutForwardFailed prometheus.Counter     // 转发失败次数
	HopsDistribution    prometheus.Histogram   // 跳数分布
	ForwardPerHop       *prometheus.CounterVec // 每跳的转发数
}

// NewFanoutMetrics 创建 Fanout 指标
func NewFanoutMetrics() *FanoutMetrics {
	return &FanoutMetrics{
		FanoutTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_total",
			Help: "Total fanout requests",
		}),
		FanoutSuccess: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_success_total",
			Help: "Total successful fanout requests",
		}),
		FanoutFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_failed_total",
			Help: "Total failed fanout requests",
		}),
		FanoutTimeout: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_timeout_total",
			Help: "Total timed out fanout requests",
		}),
		FanoutLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "nexkv_rpc_fanout_latency_seconds",
			Help:    "Fanout request latency",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
		FireForgetCount: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_fireforget_total",
			Help: "Total Fire-and-Forget fanout requests",
		}),
		QuorumCount: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_quorum_total",
			Help: "Total Quorum fanout requests",
		}),
		WaitAllCount: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_waitall_total",
			Help: "Total WaitAll fanout requests",
		}),
		PeerSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_peer_success_total",
			Help: "Total successful peer responses",
		}, []string{"peer_id"}),
		PeerFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_peer_failed_total",
			Help: "Total failed peer responses",
		}, []string{"peer_id"}),
		PeerTimeout: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_peer_timeout_total",
			Help: "Total timed out peer responses",
		}, []string{"peer_id"}),
		FanoutForwardTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_forward_total",
			Help: "Total fanout forward operations",
		}),
		FanoutForwardFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_forward_failed_total",
			Help: "Total failed fanout forward operations",
		}),
		HopsDistribution: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "nexkv_rpc_fanout_hops",
			Help:    "Fanout hops distribution",
			Buckets: []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		}),
		ForwardPerHop: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nexkv_rpc_fanout_forward_per_hop_total",
			Help: "Total fanout forwards per hop",
		}, []string{"hop_level"}),
	}
}

// ========================================
// Fanout 核心实现
// ========================================

// Fanout 并发发送到多个 peers
func (c *Client) Fanout(
	ctx context.Context,
	req *FanoutRequest,
	opts *FanoutOptions,
) *FanoutResult {
	start := time.Now()

	// 验证并规范化选项
	normalizedOpts, err := ValidateAndNormalize(opts, len(req.Peers))
	if err != nil {
		return &FanoutResult{
			Responses:  nil,
			Failed:     len(req.Peers),
			TotalPeers: len(req.Peers),
			Duration:   0,
		}
	}

	// 使用 Quorum 管理器验证 Quorum 参数
	if normalizedOpts.Mode == Quorum {
		// 注意：这里暂时使用默认配置，后续集成集群层
		quorumManager := NewQuorumManager(DefaultQuorumConfig())
		_, err = quorumManager.ValidateAndNormalizeWithQuorum(normalizedOpts, req.Peers)
		if err != nil {
			logging.WithFields(map[string]any{
				"error": err,
			}).Error("Quorum 验证失败")
			return &FanoutResult{
				Responses:  nil,
				Failed:     len(req.Peers),
				TotalPeers: len(req.Peers),
				Duration:   0,
			}
		}
	}

	// 记录 Fanout 开始
	c.recordFanoutStart(normalizedOpts.Mode, len(req.Peers))
	c.recordModeStart(normalizedOpts.Mode)

	// 根据响应模式执行
	var result *FanoutResult
	switch normalizedOpts.Mode {
	case FireForget:
		result = c.fanoutFireForget(ctx, req, normalizedOpts)
	case Quorum:
		result = c.fanoutQuorum(ctx, req, normalizedOpts)
	case WaitAll:
		result = c.fanoutWaitAll(ctx, req, normalizedOpts)
	default:
		result = &FanoutResult{
			Responses:  nil,
			Failed:     len(req.Peers),
			TotalPeers: len(req.Peers),
			Duration:   0,
		}
	}

	// 记录结果
	result.Duration = time.Since(start)
	c.recordFanoutComplete(result, normalizedOpts.Mode)

	return result
}

// fanoutFireForget Fire-and-Forget 模式
func (c *Client) fanoutFireForget(
	ctx context.Context,
	req *FanoutRequest,
	opts *FanoutOptions,
) *FanoutResult {
	// FireForget 模式：异步发送，不等待响应，但仍记录指标
	for _, peerID := range req.Peers {
		go func(pid peer.ID) {
			start := time.Now()
			body, err := c.Call(ctx, pid, req.Method, req.Body)

			// 记录指标（正确记录错误）
			c.recordPeerResponse(pid, err, opts.Mode)

			// 记录延迟（无论成功或失败）
			_ = body // 避免未使用变量警告
			_ = time.Since(start)
		}(peerID)
	}

	// 立即返回，不等待响应
	return &FanoutResult{
		Responses:  nil, // FireForget 不收集响应
		Success:    0,   // 未知，因为不等待
		Failed:     0,   // 未知，因为不等待
		Timeout:    0,
		TotalPeers: len(req.Peers),
	}
}

// fanoutQuorum Quorum 模式
func (c *Client) fanoutQuorum(
	ctx context.Context,
	req *FanoutRequest,
	opts *FanoutOptions,
) *FanoutResult {
	poolConfig := &WorkerPoolConfig{
		MaxWorkers: opts.MaxConcurrent,
		QueueSize:  len(req.Peers),
	}
	pool := NewWorkerPool(poolConfig)
	defer pool.Close()

	quorumManager := NewQuorumManager(DefaultQuorumConfig())
	resultCh := make(chan FanoutResponse, len(req.Peers))

	// 并发调用所有 peers
	c.submitFanoutTasks(pool, req, opts, resultCh)

	// 收集响应
	responses, successCount, timeoutCount, failedCount := c.collectResponses(
		resultCh, len(req.Peers), opts.Timeout, opts.Mode, false,
	)

	quorumResult := quorumManager.CalculateQuorumResult(
		len(req.Peers), successCount, nil, nil,
	)

	return &FanoutResult{
		Responses:  responses,
		Success:    successCount,
		Failed:     failedCount,
		Timeout:    timeoutCount,
		TotalPeers: len(req.Peers),
		Quorum:     quorumResult,
	}
}

// fanoutWaitAll WaitAll 模式
func (c *Client) fanoutWaitAll(
	ctx context.Context,
	req *FanoutRequest,
	opts *FanoutOptions,
) *FanoutResult {
	poolConfig := &WorkerPoolConfig{
		MaxWorkers: opts.MaxConcurrent,
		QueueSize:  len(req.Peers),
	}
	pool := NewWorkerPool(poolConfig)
	defer pool.Close()

	resultCh := make(chan FanoutResponse, len(req.Peers))

	// 并发调用所有 peers
	c.submitFanoutTasks(pool, req, opts, resultCh)

	// 收集所有响应
	responses, successCount, timeoutCount, failedCount := c.collectResponses(
		resultCh, len(req.Peers), opts.Timeout, opts.Mode, true,
	)

	return &FanoutResult{
		Responses:  responses,
		Success:    successCount,
		Failed:     failedCount,
		Timeout:    timeoutCount,
		TotalPeers: len(req.Peers),
	}
}

// ========================================
// Hops 转发支持
// ========================================

// FanoutWithHops 带多跳转发的 Fanout（递归实现）
func (c *Client) FanoutWithHops(
	ctx context.Context,
	req *FanoutRequest,
	opts *FanoutOptions,
) *FanoutResult {
	// 检查是否可以继续转发
	if req.Hops == 0 || !CanForward(req.Hops, opts.MaxHops) {
		logging.WithFields(map[string]any{
			"hops":    req.Hops,
			"maxHops": opts.MaxHops,
		}).Debug("Hops 耗尽，停止转发")

		return &FanoutResult{
			Responses:  nil,
			Failed:     len(req.Peers),
			TotalPeers: len(req.Peers),
		}
	}

	// 递减跳数
	req.Hops = DecrementHops(req.Hops)

	// 执行普通 Fanout
	result := c.Fanout(ctx, req, opts)

	// 如果需要继续转发，递归调用（TODO: 后续实现）
	// 当前版本只支持单跳，多跳转发在后续版本中实现
	_ = req.Hops // 避免未使用变量警告

	return result
}

// ========================================
// 全局监控指标（Prometheus）
// ========================================

var (
	// 全局 Fanout 指标实例
	fanoutMetrics = NewFanoutMetrics()
)

// init 注册 Prometheus 指标
func init() {
	// 注册所有 Counter 指标
	prometheus.MustRegister(fanoutMetrics.FanoutTotal)
	prometheus.MustRegister(fanoutMetrics.FanoutSuccess)
	prometheus.MustRegister(fanoutMetrics.FanoutFailed)
	prometheus.MustRegister(fanoutMetrics.FanoutTimeout)
	prometheus.MustRegister(fanoutMetrics.FanoutLatency)
	prometheus.MustRegister(fanoutMetrics.FireForgetCount)
	prometheus.MustRegister(fanoutMetrics.QuorumCount)
	prometheus.MustRegister(fanoutMetrics.WaitAllCount)
	prometheus.MustRegister(fanoutMetrics.PeerSuccess)
	prometheus.MustRegister(fanoutMetrics.PeerFailed)
	prometheus.MustRegister(fanoutMetrics.PeerTimeout)
	prometheus.MustRegister(fanoutMetrics.FanoutForwardTotal)
	prometheus.MustRegister(fanoutMetrics.FanoutForwardFailed)
	prometheus.MustRegister(fanoutMetrics.HopsDistribution)
	prometheus.MustRegister(fanoutMetrics.ForwardPerHop)
}

// GetFanoutMetrics 获取全局 Fanout 指标
func GetFanoutMetrics() *FanoutMetrics {
	return fanoutMetrics
}

// ========================================
// 监控指标记录
// ========================================

func (c *Client) recordFanoutStart(mode ResponseMode, peerCount int) {
	// 记录总调用数
	fanoutMetrics.FanoutTotal.Inc()

	logging.WithFields(map[string]any{
		"mode":       mode.String(),
		"peer_count": peerCount,
	}).Debug("Fanout 开始")
}

func (c *Client) recordFanoutComplete(result *FanoutResult, mode ResponseMode) {
	// 记录成功/失败/超时数
	if result.Failed == 0 && result.Timeout == 0 {
		fanoutMetrics.FanoutSuccess.Inc()
	} else {
		fanoutMetrics.FanoutFailed.Inc()
	}

	if result.Timeout > 0 {
		fanoutMetrics.FanoutTimeout.Inc()
	}

	// 记录延迟
	fanoutMetrics.FanoutLatency.Observe(result.Duration.Seconds())

	logging.WithFields(map[string]any{
		"mode":     mode.String(),
		"success":  result.Success,
		"failed":   result.Failed,
		"timeout":  result.Timeout,
		"total":    result.TotalPeers,
		"duration": result.Duration,
	}).Debug("Fanout 完成")
}

func (c *Client) recordPeerResponse(peerID peer.ID, err error, mode ResponseMode) {
	// 记录 peer 级别指标
	peerIDStr := peerID.String()
	if err != nil {
		fanoutMetrics.PeerFailed.WithLabelValues(peerIDStr).Inc()

		if IsTimeout(err) {
			fanoutMetrics.PeerTimeout.WithLabelValues(peerIDStr).Inc()
		}

		logging.WithFields(map[string]any{
			"peer_id": peerID,
			"mode":    mode.String(),
			"error":   err,
		}).Debug("Peer 响应失败")
	} else {
		fanoutMetrics.PeerSuccess.WithLabelValues(peerIDStr).Inc()
	}
}

// recordModeStart 记录响应模式开始
func (c *Client) recordModeStart(mode ResponseMode) {
	switch mode {
	case FireForget:
		fanoutMetrics.FireForgetCount.Inc()
	case Quorum:
		fanoutMetrics.QuorumCount.Inc()
	case WaitAll:
		fanoutMetrics.WaitAllCount.Inc()
	}
}

// ========================================
// 辅助函数
// ========================================

// submitFanoutTasks 提交 Fanout 任务到工作池
func (c *Client) submitFanoutTasks(
	pool *WorkerPool,
	req *FanoutRequest,
	opts *FanoutOptions,
	resultCh chan FanoutResponse,
) {
	for _, peerID := range req.Peers {
		task := NewFanoutTask(peerID, req.Method, req.Body, c, opts.Timeout, resultCh)

		if err := pool.Submit(task); err != nil {
			c.recordPeerResponse(peerID, err, opts.Mode)
			resultCh <- FanoutResponse{PeerID: peerID, Error: err}
		}
	}
}

// ========================================
// 响应收集辅助函数
// ========================================

// processSingleResponse 处理单个响应，更新计数器
func processSingleResponse(
	resp FanoutResponse,
	responses []FanoutResponse,
	successCount, timeoutCount, failedCount int,
	mode ResponseMode,
	client *Client,
) ([]FanoutResponse, int, int, int) {
	responses = append(responses, resp)

	if resp.IsSuccess() {
		successCount++
		client.recordPeerResponse(resp.PeerID, nil, mode)
	} else {
		if IsTimeout(resp.Error) {
			timeoutCount++
		}
		failedCount++
		client.recordPeerResponse(resp.PeerID, resp.Error, mode)
	}

	return responses, successCount, timeoutCount, failedCount
}

// handleTimeoutInCollection 处理收集超时情况
func handleTimeoutInCollection(
	peerCount, collectedCount int,
	waitAll bool,
	currentTimeoutCount int,
) int {
	if waitAll {
		// WaitAll 模式：记录剩余 peers 为超时
		remaining := peerCount - collectedCount
		return currentTimeoutCount + remaining
	}
	// Quorum 模式：仅记录当前超时
	return currentTimeoutCount + 1
}

// collectResponses 收集响应（重构版）
func (c *Client) collectResponses(
	resultCh chan FanoutResponse,
	peerCount int,
	timeout time.Duration,
	mode ResponseMode,
	waitAll bool,
) ([]FanoutResponse, int, int, int) {
	responses := make([]FanoutResponse, 0, peerCount)
	successCount := 0
	timeoutCount := 0
	failedCount := 0

	for i := 0; i < peerCount; i++ {
		select {
		case resp := <-resultCh:
			responses, successCount, timeoutCount, failedCount = processSingleResponse(
				resp, responses, successCount, timeoutCount, failedCount, mode, c,
			)

		case <-time.After(timeout):
			timeoutCount = handleTimeoutInCollection(peerCount, i, waitAll, timeoutCount)
			return responses, successCount, timeoutCount, failedCount
		}
	}

	return responses, successCount, timeoutCount, failedCount
}
