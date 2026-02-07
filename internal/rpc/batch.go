// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ========================================
// 批量 RPC 调用
// ========================================

// BatchRequest 批量请求项
type BatchRequest struct {
	Method  string        // RPC 方法名
	Body    []byte        // 请求体
	ID      string        // 请求标识符（可选）
	Timeout time.Duration // 单个请求超时（可选）
}

// BatchResponse 批量响应项
type BatchResponse struct {
	ID      string        // 请求标识符
	Method  string        // RPC 方法名
	Body    []byte        // 响应体
	Error   error         // 错误信息
	Latency time.Duration // 响应延迟
	Success bool          // 是否成功
}

// BatchResult 批量调用结果
type BatchResult struct {
	Responses  []BatchResponse // 所有响应
	Total      int             // 总请求数
	Success    int             // 成功数
	Failed     int             // 失败数
	Duration   time.Duration   // 总耗时
	AvgLatency time.Duration   // 平均延迟
	MaxLatency time.Duration   // 最大延迟
	MinLatency time.Duration   // 最小延迟
}

// BatchOptions 批量调用选项
type BatchOptions struct {
	// 并发控制
	MaxConcurrent int // 最大并发数（0 表示不限制）

	// 超时配置
	Timeout time.Duration // 整体超时时间

	// 错误处理
	ContinueOnError bool // 遇到错误是否继续

	// 顺序保证
	PreserveOrder bool // 是否保持响应顺序
}

// DefaultBatchOptions 返回默认选项
func DefaultBatchOptions() *BatchOptions {
	return &BatchOptions{
		MaxConcurrent:   10,               // 默认最大并发 10
		Timeout:         30 * time.Second, // 默认超时 30 秒
		ContinueOnError: false,            // 默认遇到错误停止
		PreserveOrder:   true,             // 默认保持顺序
	}
}

// ========================================
// 批量调用核心实现
// ========================================

// CallParallel 并行批量调用（单个 peer）
func (c *Client) CallParallel(
	ctx context.Context,
	peerID peer.ID,
	reqs []BatchRequest,
	opts *BatchOptions,
) *BatchResult {
	start := time.Now()

	// 验证并规范化选项
	if opts == nil {
		opts = DefaultBatchOptions()
	}

	// 记录批量调用开始
	rpcMetrics.BatchCallsTotal.Inc()
	rpcMetrics.BatchCallSize.Observe(float64(len(reqs)))

	// 创建整体超时上下文（如果配置了）
	batchCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		batchCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// 如果需要保持顺序，使用有序执行
	if opts.PreserveOrder {
		return c.callParallelOrdered(batchCtx, peerID, reqs, opts, start)
	}

	// 否则使用无序并行执行
	return c.callParallelUnordered(batchCtx, peerID, reqs, opts, start)
}

// callParallelOrdered 有序并行执行（保持响应顺序）
func (c *Client) callParallelOrdered(
	ctx context.Context,
	peerID peer.ID,
	reqs []BatchRequest,
	opts *BatchOptions,
	start time.Time,
) *BatchResult {
	responses := make([]BatchResponse, len(reqs))
	var wg sync.WaitGroup

	sem := newSemaphore(opts.MaxConcurrent, len(reqs))

	for i, req := range reqs {
		wg.Add(1)
		go func(idx int, r BatchRequest) {
			defer wg.Done()
			sem.acquire()
			defer sem.release()

			// 单个请求的超时配置（可选）
			// 注意：不创建额外的 context，直接使用传入的 batchCtx
			// 如果需要单个请求超时，应该在调用层配置
			reqCtx := ctx
			if r.Timeout > 0 {
				var cancel context.CancelFunc
				reqCtx, cancel = context.WithTimeout(ctx, r.Timeout)
				defer cancel()
			}

			reqStart := time.Now()
			body, err := c.Call(reqCtx, peerID, r.Method, r.Body)

			responses[idx] = BatchResponse{
				ID:      r.ID,
				Method:  r.Method,
				Body:    body,
				Error:   err,
				Latency: time.Since(reqStart),
				Success: err == nil,
			}
		}(i, req)
	}

	wg.Wait()
	result := c.calculateBatchResult(responses, start)
	rpcMetrics.RecordBatchCall(peerID.String(), len(reqs), result.Duration, nil)

	return result
}

// callParallelUnordered 无序并行执行（不保持响应顺序）
func (c *Client) callParallelUnordered(
	ctx context.Context,
	peerID peer.ID,
	reqs []BatchRequest,
	opts *BatchOptions,
	start time.Time,
) *BatchResult {
	var responses []BatchResponse
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := newSemaphore(opts.MaxConcurrent, len(reqs))

	for _, req := range reqs {
		wg.Add(1)
		go func(r BatchRequest) {
			defer wg.Done()

			if !sem.tryAcquire(ctx) {
				return
			}
			defer sem.release()

			// 单个请求的超时配置（可选）
			reqCtx := ctx
			if r.Timeout > 0 {
				var cancel context.CancelFunc
				reqCtx, cancel = context.WithTimeout(ctx, r.Timeout)
				defer cancel()
			}

			reqStart := time.Now()
			body, err := c.Call(reqCtx, peerID, r.Method, r.Body)

			resp := BatchResponse{
				ID:      r.ID,
				Method:  r.Method,
				Body:    body,
				Error:   err,
				Latency: time.Since(reqStart),
				Success: err == nil,
			}

			mu.Lock()
			responses = append(responses, resp)
			mu.Unlock()
		}(req)
	}

	wg.Wait()
	result := c.calculateBatchResult(responses, start)
	rpcMetrics.RecordBatchCall(peerID.String(), len(reqs), result.Duration, nil)

	return result
}

// calculateBatchResult 计算批量调用结果
func (c *Client) calculateBatchResult(responses []BatchResponse, start time.Time) *BatchResult {
	result := &BatchResult{
		Responses: responses,
		Total:     len(responses),
		Duration:  time.Since(start),
	}

	var totalLatency time.Duration
	var maxLatency time.Duration
	minLatency := time.Hour // 初始化为一个大值
	successCount := 0
	failedCount := 0

	for _, resp := range responses {
		if resp.Success {
			successCount++
			totalLatency += resp.Latency

			if resp.Latency > maxLatency {
				maxLatency = resp.Latency
			}
			if resp.Latency < minLatency {
				minLatency = resp.Latency
			}
		} else {
			failedCount++
		}
	}

	result.Success = successCount
	result.Failed = failedCount

	if successCount > 0 {
		result.AvgLatency = totalLatency / time.Duration(successCount)
		result.MaxLatency = maxLatency
		result.MinLatency = minLatency
	}

	return result
}

// ========================================
// 批量调用变体
// ========================================

// CallParallelBatch 并行批量调用（多个 peer，每个 peer 多个请求）
func (c *Client) CallParallelBatch(
	ctx context.Context,
	peerReqs map[peer.ID][]BatchRequest,
	opts *BatchOptions,
) map[peer.ID]*BatchResult {
	start := time.Now()
	opts = normalizeOptions(opts)

	// 创建整体超时上下文（如果配置了）
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	results := executeInParallel(ctx, peerReqs, opts, func(pid peer.ID, reqs []BatchRequest) *BatchResult {
		return c.CallParallel(ctx, pid, reqs, opts)
	})

	logBatchCompletion("批量 RPC 调用完成", len(peerReqs), time.Since(start))
	return results
}

// CallParallelFanout 并行 Fanout 调用（多个 peer，相同请求）
func (c *Client) CallParallelFanout(
	ctx context.Context,
	peerIDs []peer.ID,
	req BatchRequest,
	opts *BatchOptions,
) map[peer.ID]*BatchResult {
	start := time.Now()
	opts = normalizeOptions(opts)

	// 创建整体超时上下文（如果配置了）
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	peerReqs := make(map[peer.ID][]BatchRequest, len(peerIDs))
	for _, pid := range peerIDs {
		peerReqs[pid] = []BatchRequest{req}
	}

	results := executeInParallel(ctx, peerReqs, opts, func(pid peer.ID, reqs []BatchRequest) *BatchResult {
		body, err := c.Call(ctx, pid, reqs[0].Method, reqs[0].Body)
		latency := time.Since(start)

		return buildSingleResult(reqs[0], body, err, latency)
	})

	logBatchCompletion("Fanout RPC 调用完成", len(peerIDs), time.Since(start))
	return results
}

// ========================================
// 辅助方法
// ========================================

// String 返回批量结果的字符串表示
func (r *BatchResult) String() string {
	return fmt.Sprintf(
		"BatchResult{Total: %d, Success: %d, Failed: %d, Duration: %v, AvgLatency: %v}",
		r.Total, r.Success, r.Failed, r.Duration, r.AvgLatency,
	)
}

// GetSuccessRate 获取成功率
func (r *BatchResult) GetSuccessRate() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.Success) / float64(r.Total)
}

// ========================================
// 辅助类型和函数
// ========================================

// semaphore 信号量（用于并发控制）
type semaphore chan struct{}

func newSemaphore(maxSize, fallbackSize int) semaphore {
	if maxSize <= 0 {
		maxSize = fallbackSize
	}
	return make(chan struct{}, maxSize)
}

func (s semaphore) acquire() {
	s <- struct{}{}
}

func (s semaphore) tryAcquire(ctx context.Context) bool {
	select {
	case s <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s semaphore) release() {
	<-s
}

// normalizeOptions 规范化选项
func normalizeOptions(opts *BatchOptions) *BatchOptions {
	if opts == nil {
		return DefaultBatchOptions()
	}
	return opts
}

// executeInParallel 在多个 peer 上并行执行任务
func executeInParallel[T any](
	ctx context.Context,
	inputs map[peer.ID][]BatchRequest,
	opts *BatchOptions,
	fn func(peer.ID, []BatchRequest) T,
) map[peer.ID]T {
	results := make(map[peer.ID]T)
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := newSemaphore(opts.MaxConcurrent, len(inputs))

	for pid, reqs := range inputs {
		wg.Add(1)
		go func(peerID peer.ID, requests []BatchRequest) {
			defer wg.Done()
			sem.acquire()
			defer sem.release()

			result := fn(peerID, requests)

			mu.Lock()
			results[peerID] = result
			mu.Unlock()
		}(pid, reqs)
	}

	wg.Wait()
	return results
}

// buildSingleResult 构建单个请求的结果
func buildSingleResult(req BatchRequest, body []byte, err error, latency time.Duration) *BatchResult {
	result := &BatchResult{
		Responses: []BatchResponse{{
			ID:      req.ID,
			Method:  req.Method,
			Body:    body,
			Error:   err,
			Latency: latency,
			Success: err == nil,
		}},
		Total:    1,
		Duration: latency,
		Success:  0,
		Failed:   0,
	}

	if err == nil {
		result.Success = 1
		result.AvgLatency = latency
		result.MaxLatency = latency
		result.MinLatency = latency
	} else {
		result.Failed = 1
	}

	return result
}

// logBatchCompletion 记录批量调用完成日志
func logBatchCompletion(message string, count int, duration time.Duration) {
	logging.WithFields(map[string]any{
		"count":    count,
		"duration": duration,
	}).Info(message)
}
