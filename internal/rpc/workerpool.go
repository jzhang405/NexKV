// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/prometheus/client_golang/prometheus"
)

// WorkerPool goroutine 池（用于 Fanout 并发发送）
type WorkerPool struct {
	maxWorkers int            // 最大 worker 数量
	taskQueue  chan Task      // 任务队列
	wg         sync.WaitGroup // 等待所有 worker 完成
	ctx        context.Context
	cancel     context.CancelFunc
	metrics    *WorkerPoolMetrics
	once       sync.Once
}

// Task 任务接口
type Task interface {
	Execute(ctx context.Context) error
}

// WorkerPoolConfig goroutine 池配置
type WorkerPoolConfig struct {
	MaxWorkers  int           // 最大 worker 数量
	QueueSize   int           // 任务队列大小
	IdleTimeout time.Duration // worker 空闲超时
}

// DefaultWorkerPoolConfig 返回默认配置
func DefaultWorkerPoolConfig() *WorkerPoolConfig {
	return &WorkerPoolConfig{
		MaxWorkers:  10,  // 默认 10 个 worker
		QueueSize:   100, // 队列大小 100
		IdleTimeout: 30 * time.Second,
	}
}

// WorkerPoolMetrics goroutine 池指标
type WorkerPoolMetrics struct {
	WorkersActive  prometheus.Gauge
	WorkersIdle    prometheus.Gauge
	TasksQueued    prometheus.Gauge
	TasksProcessed prometheus.Counter
	TasksFailed    prometheus.Counter
	TaskDuration   prometheus.Histogram
}

// NewWorkerPoolMetrics 创建 goroutine 池指标
func NewWorkerPoolMetrics() *WorkerPoolMetrics {
	return &WorkerPoolMetrics{
		WorkersActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nexkv_rpc_worker_pool_active",
			Help: "Active workers in pool",
		}),
		WorkersIdle: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nexkv_rpc_worker_pool_idle",
			Help: "Idle workers in pool",
		}),
		TasksQueued: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nexkv_rpc_worker_pool_tasks_queued",
			Help: "Tasks waiting in queue",
		}),
		TasksProcessed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_worker_pool_tasks_processed_total",
			Help: "Total tasks processed",
		}),
		TasksFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_worker_pool_tasks_failed_total",
			Help: "Total tasks failed",
		}),
		TaskDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "nexkv_rpc_worker_pool_task_duration_seconds",
			Help:    "Task execution duration",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		}),
	}
}

// NewWorkerPool 创建 goroutine 池
func NewWorkerPool(cfg *WorkerPoolConfig) *WorkerPool {
	if cfg == nil {
		cfg = DefaultWorkerPoolConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &WorkerPool{
		maxWorkers: cfg.MaxWorkers,
		taskQueue:  make(chan Task, cfg.QueueSize),
		ctx:        ctx,
		cancel:     cancel,
		metrics:    NewWorkerPoolMetrics(),
	}

	// 启动 workers
	pool.start()

	return pool
}

// start 启动所有 worker
func (p *WorkerPool) start() {
	p.once.Do(func() {
		for i := 0; i < p.maxWorkers; i++ {
			p.wg.Add(1)
			go p.worker(i)
		}
		logging.WithField("workers", p.maxWorkers).Info("Worker pool started")
	})
}

// worker worker 协程
func (p *WorkerPool) worker(id int) {
	defer p.wg.Done()

	for {
		select {
		case task, ok := <-p.taskQueue:
			if !ok {
				// 队列关闭，退出
				logging.WithField("worker_id", id).Debug("Worker exiting: queue closed")
				return
			}

			// 执行任务
			p.executeTask(id, task)

		case <-p.ctx.Done():
			logging.WithField("worker_id", id).Debug("Worker exiting: context canceled")
			return
		}
	}
}

// executeTask 执行单个任务
func (p *WorkerPool) executeTask(workerID int, task Task) {
	start := time.Now()

	// 更新活跃 worker 数
	p.metrics.WorkersActive.Inc()
	p.metrics.WorkersIdle.Dec()
	defer func() {
		p.metrics.WorkersActive.Dec()
		p.metrics.WorkersIdle.Inc()
	}()

	// 执行任务
	err := task.Execute(p.ctx)

	// 记录指标
	duration := time.Since(start).Seconds()
	p.metrics.TaskDuration.Observe(duration)
	p.metrics.TasksProcessed.Inc()

	if err != nil {
		p.metrics.TasksFailed.Inc()
		logging.WithFields(map[string]any{
			"worker_id": workerID,
			"error":     err,
			"duration":  duration,
		}).Warn("Task execution failed")
	} else {
		logging.WithFields(map[string]any{
			"worker_id": workerID,
			"duration":  duration,
		}).Debug("Task executed successfully")
	}
}

// Submit 提交任务到池
func (p *WorkerPool) Submit(task Task) error {
	select {
	case p.taskQueue <- task:
		p.metrics.TasksQueued.Inc()
		return nil
	case <-p.ctx.Done():
		return fmt.Errorf("worker pool 已关闭")
	default:
		return fmt.Errorf("任务队列已满（max=%d）", cap(p.taskQueue))
	}
}

// SubmitWithTimeout 带超时提交任务
func (p *WorkerPool) SubmitWithTimeout(task Task, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(p.ctx, timeout)
	defer cancel()

	select {
	case p.taskQueue <- task:
		p.metrics.TasksQueued.Inc()
		return nil
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("提交任务超时（%v）", timeout)
		}
		return fmt.Errorf("worker pool 已关闭")
	}
}

// Close 关闭 goroutine 池
func (p *WorkerPool) Close() error {
	p.cancel()
	close(p.taskQueue)
	p.wg.Wait()
	logging.Info("Worker pool closed")
	return nil
}

// Stats 获取池统计信息
func (p *WorkerPool) Stats() WorkerPoolStats {
	return WorkerPoolStats{
		MaxWorkers:  p.maxWorkers,
		QueuedTasks: len(p.taskQueue),
		QueueSize:   cap(p.taskQueue),
	}
}

// WorkerPoolStats 池统计信息
type WorkerPoolStats struct {
	MaxWorkers  int
	QueuedTasks int
	QueueSize   int
}

// ========================================
// 并发控制
// ========================================

// ConcurrencyLimiter 并发限制器
type ConcurrencyLimiter struct {
	maxConcurrent int32
	current       int32
	waiting       int32
	semaphore     chan struct{}
	metrics       *ConcurrencyMetrics
}

// ConcurrencyMetrics 并发控制指标
type ConcurrencyMetrics struct {
	ConcurrentActive  prometheus.Gauge
	ConcurrentWaiting prometheus.Gauge
	AcquireTotal      prometheus.Counter
	AcquireSuccess    prometheus.Counter
	AcquireTimeout    prometheus.Counter
	ReleaseTotal      prometheus.Counter
}

// NewConcurrencyMetrics 创建并发控制指标
func NewConcurrencyMetrics() *ConcurrencyMetrics {
	return &ConcurrencyMetrics{
		ConcurrentActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nexkv_rpc_concurrent_active",
			Help: "Currently active concurrent operations",
		}),
		ConcurrentWaiting: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nexkv_rpc_concurrent_waiting",
			Help: "Operations waiting for semaphore",
		}),
		AcquireTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_concurrent_acquire_total",
			Help: "Total semaphore acquire attempts",
		}),
		AcquireSuccess: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_concurrent_acquire_success_total",
			Help: "Total successful semaphore acquires",
		}),
		AcquireTimeout: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_concurrent_acquire_timeout_total",
			Help: "Total semaphore acquire timeouts",
		}),
		ReleaseTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_concurrent_release_total",
			Help: "Total semaphore releases",
		}),
	}
}

// NewConcurrencyLimiter 创建并发限制器
// 使用带缓冲 channel 作为 semaphore：
// - 初始时 channel 满载 maxConcurrent 个空的 struct{}
// - Acquire 从 channel 接收（取走一个许可）
// - Release 向 channel 发送（放回一个许可）
func NewConcurrencyLimiter(maxConcurrent int32) *ConcurrencyLimiter {
	c := &ConcurrencyLimiter{
		maxConcurrent: maxConcurrent,
		semaphore:     make(chan struct{}, maxConcurrent),
		metrics:       NewConcurrencyMetrics(),
	}
	// 预填充 semaphore（channel 满表示所有许可都可用）
	for i := int32(0); i < maxConcurrent; i++ {
		c.semaphore <- struct{}{}
	}
	return c
}

// Acquire 获取并发许可（阻塞直到获取成功或上下文取消）
func (c *ConcurrencyLimiter) Acquire(ctx context.Context) error {
	c.metrics.AcquireTotal.Inc()

	atomic.AddInt32(&c.waiting, 1)
	c.metrics.ConcurrentWaiting.Inc()

	acquired := false
	defer func() {
		if !acquired {
			// 未获取许可，恢复等待指标
			atomic.AddInt32(&c.waiting, -1)
			c.metrics.ConcurrentWaiting.Dec()
		}
	}()

	select {
	case <-c.semaphore: // 从 channel 接收，表示取走一个许可
		acquired = true
		atomic.AddInt32(&c.current, 1)
		c.metrics.ConcurrentActive.Inc()
		c.metrics.AcquireSuccess.Inc()
		// 成功获取许可，递减等待计数
		atomic.AddInt32(&c.waiting, -1)
		c.metrics.ConcurrentWaiting.Dec()
		return nil
	case <-ctx.Done():
		c.metrics.AcquireTimeout.Inc()
		return ctx.Err()
	}
}

// AcquireWithTimeout 带超时获取并发许可
func (c *ConcurrencyLimiter) AcquireWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.Acquire(ctx)
}

// TryAcquire 尝试获取并发许可（非阻塞）
func (c *ConcurrencyLimiter) TryAcquire() bool {
	select {
	case <-c.semaphore: // 从 channel 接收，表示取走一个许可
		atomic.AddInt32(&c.current, 1)
		c.metrics.ConcurrentActive.Inc()
		c.metrics.AcquireSuccess.Inc()
		return true
	default:
		c.metrics.AcquireTimeout.Inc()
		return false
	}
}

// Release 释放并发许可
func (c *ConcurrencyLimiter) Release() {
	// 先减少当前计数
	newCurrent := atomic.AddInt32(&c.current, -1)
	if newCurrent < 0 {
		// 防止计数为负（异常情况）
		atomic.AddInt32(&c.current, 1)
		return
	}

	// 更新指标
	c.metrics.ConcurrentActive.Dec()
	c.metrics.ReleaseTotal.Inc()

	// 向 semaphore 发送信号（放回一个许可）
	// 如果 channel 满了，会阻塞直到有 Acquire 取走值
	select {
	case c.semaphore <- struct{}{}:
		// 成功放回许可
	default:
		// 不应该到达这里，因为 Release 前必有 Acquire
	}
}

// Current 获取当前并发数
func (c *ConcurrencyLimiter) Current() int32 {
	return atomic.LoadInt32(&c.current)
}

// Waiting 获取等待数
func (c *ConcurrencyLimiter) Waiting() int32 {
	return atomic.LoadInt32(&c.waiting)
}

// ========================================
// Fanout 任务实现
// ========================================

// FanoutTask Fanout 任务（实现 Task 接口）
type FanoutTask struct {
	peerID  peer.ID
	method  string
	request []byte
	client  *Client
	timeout time.Duration
	result  chan<- FanoutResponse
}

// Execute 执行 Fanout 任务
func (t *FanoutTask) Execute(ctx context.Context) error {
	start := time.Now()

	// 设置超时
	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	// 发送 RPC 请求
	respBody, err := t.client.Call(ctx, t.peerID, t.method, t.request)

	// 发送结果到结果通道
	response := FanoutResponse{
		PeerID:  t.peerID,
		Body:    respBody,
		Error:   err,
		Latency: time.Since(start),
	}
	select {
	case t.result <- response:
	case <-ctx.Done():
		return ctx.Err()
	}

	return err
}

// NewFanoutTask 创建 Fanout 任务
func NewFanoutTask(
	peerID peer.ID,
	method string,
	request []byte,
	client *Client,
	timeout time.Duration,
	result chan<- FanoutResponse,
) *FanoutTask {
	return &FanoutTask{
		peerID:  peerID,
		method:  method,
		request: request,
		client:  client,
		timeout: timeout,
		result:  result,
	}
}
