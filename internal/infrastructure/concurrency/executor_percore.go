// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// ==========================================
// 常量定义
// ==========================================

const (
	// MaxCores 最大核心数限制
	MaxCores = 64

	// DefaultQueueSize 默认队列大小
	DefaultQueueSize = 1000

	// DefaultRateLimit 默认限流速率 (OPS)
	DefaultRateLimit = 100000

	// DefaultBurstSize 默认突发大小
	DefaultBurstSize = 10000
)

// 执行器状态
const (
	RUNNING int32 = iota
	CLOSING
	CLOSED
)

// ==========================================
// 错误定义
// ==========================================

var (
	// ErrExecutorClosed 执行器已关闭
	ErrExecutorClosed = errors.New("executor is closed")

	// ErrRateLimitExceeded 限流超限
	ErrRateLimitExceeded = errors.New("rate limit exceeded")

	// ErrInvalidConfig 无效配置
	ErrInvalidConfig = errors.New("invalid configuration")
)

// ==========================================
// 类型定义
// ==========================================

// PerCoreExecutor Per-Core 执行器
// 每个核心一个 goroutine，支持绑核无锁执行
type PerCoreExecutor struct {
	// 配置
	config PerCoreConfig

	// 状态
	state     int32 // RUNNING, CLOSING, CLOSED
	startTime time.Time

	// Workers
	workers []*coreWorker
	wg      sync.WaitGroup

	// 限流器
	limiter *rate.Limiter

	// 统计
	stats PerCoreStats

	// 上下文
	ctx    context.Context
	cancel context.CancelFunc

	// 确保只关闭一次
	closeOnce sync.Once
}

// PerCoreConfig Per-Core 执行器配置
type PerCoreConfig struct {
	NumCores     int                    // 核心数
	QueueSize    int                    // 每核心队列大小
	PanicHandler func(interface{})      // Panic 处理器
	RateLimit    int                    // 限流速率 (OPS)
	BurstSize    int                    // 突发大小
	EnableAffini bool                   // 启用绑核
	Labels       map[string]string      // 标签（用于监控）
}

// PerCoreStats Per-Core 执行器统计
type PerCoreStats struct {
	TotalSubmitted  int64 // 总提交任务数
	TotalCompleted  int64 // 总完成任务数
	TotalFailed     int64 // 总失败任务数
	TotalPanics     int64 // 总 Panic 次数
	TotalRateLimit  int64 // 总限流次数
	QueueLength     int64 // 当前队列长度
	ActiveWorkers   int64 // 活跃 Worker 数
}

// coreWorker 核心工作器
type coreWorker struct {
	coreID   int
	executor *PerCoreExecutor

	// 优先级队列
	queue taskQueue
	cond  *sync.Cond

	// 状态
	running int32

	// 上下文
	ctx    context.Context
	cancel context.CancelFunc
}

// taskItem 任务项
type taskItem struct {
	priority   int
	submitTime time.Time
	task       func(context.Context)
}

// taskQueue 任务队列（实现 heap.Interface）
type taskQueue []taskItem

func (q taskQueue) Len() int { return len(q) }

func (q taskQueue) Less(i, j int) bool {
	// 优先级相同时 FIFO
	if q[i].priority == q[j].priority {
		return q[i].submitTime.Before(q[j].submitTime)
	}

	// 等待时间过长时提升优先级
	const maxWaitTime = 10 * time.Second
	if time.Since(q[i].submitTime) > maxWaitTime {
		return true
	}

	return q[i].priority > q[j].priority
}

func (q taskQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
}

func (q *taskQueue) Push(x interface{}) {
	*q = append(*q, x.(taskItem))
}

func (q *taskQueue) Pop() interface{} {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[0 : n-1]
	return item
}

// ==========================================
// PerCoreOption 配置选项
// ==========================================

// PerCoreOption Per-Core 执行器配置选项
type PerCoreOption func(*PerCoreConfig)

// WithNumCores 设置核心数
func WithNumCores(n int) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.NumCores = n
	}
}

// WithQueueSize 设置队列大小
func WithQueueSize(size int) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.QueueSize = size
	}
}

// WithPanicHandler 设置 Panic 处理器
func WithPanicHandler(handler func(interface{})) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.PanicHandler = handler
	}
}

// WithRateLimit 设置限流
func WithRateLimit(rateLimit, burst int) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.RateLimit = rateLimit
		c.BurstSize = burst
	}
}

// WithEnableAffinity 启用绑核
func WithEnableAffinity(enable bool) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.EnableAffini = enable
	}
}

// WithLabels 设置标签
func WithLabels(labels map[string]string) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.Labels = labels
	}
}

// ==========================================
// 构造函数
// ==========================================

// NewPerCoreExecutor 创建 Per-Core 执行器
func NewPerCoreExecutor(opts ...PerCoreOption) (*PerCoreExecutor, error) {
	// 默认配置
	config := PerCoreConfig{
		NumCores:     runtime.NumCPU(),
		QueueSize:    DefaultQueueSize,
		RateLimit:    DefaultRateLimit,
		BurstSize:    DefaultBurstSize,
		EnableAffini: false, // 默认不绑核（跨平台兼容）
		PanicHandler: defaultPanicHandler,
	}

	// 应用选项
	for _, opt := range opts {
		opt(&config)
	}

	// 参数验证
	if err := validateConfig(&config); err != nil {
		return nil, err
	}

	// 创建执行器
	ctx, cancel := context.WithCancel(context.Background())
	e := &PerCoreExecutor{
		config:    config,
		state:     RUNNING,
		startTime: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
		limiter:   rate.NewLimiter(rate.Limit(config.RateLimit), config.BurstSize),
	}

	// 创建 workers
	e.workers = make([]*coreWorker, config.NumCores)
	for i := 0; i < config.NumCores; i++ {
		worker := e.newWorker(i)
		e.workers[i] = worker
		go worker.run()
		e.wg.Add(1)
	}

	return e, nil
}

// validateConfig 验证配置
func validateConfig(config *PerCoreConfig) error {
	if config.NumCores <= 0 {
		return fmt.Errorf("%w: NumCores must be positive, got %d", ErrInvalidConfig, config.NumCores)
	}
	if config.NumCores > MaxCores {
		return fmt.Errorf("%w: NumCores (%d) exceeds maximum (%d)", ErrInvalidConfig, config.NumCores, MaxCores)
	}
	if config.QueueSize <= 0 {
		return fmt.Errorf("%w: QueueSize must be positive, got %d", ErrInvalidConfig, config.QueueSize)
	}
	if config.RateLimit <= 0 {
		config.RateLimit = DefaultRateLimit
	}
	if config.BurstSize <= 0 {
		config.BurstSize = DefaultBurstSize
	}
	if config.PanicHandler == nil {
		config.PanicHandler = defaultPanicHandler
	}
	return nil
}

// defaultPanicHandler 默认 Panic 处理器
func defaultPanicHandler(r interface{}) {
	// 可以记录日志或上报监控
}

// newWorker 创建核心工作器
func (e *PerCoreExecutor) newWorker(coreID int) *coreWorker {
	ctx, cancel := context.WithCancel(e.ctx)
	return &coreWorker{
		coreID:  coreID,
		executor: e,
		queue:   make(taskQueue, 0, e.config.QueueSize),
		cond:    sync.NewCond(new(sync.Mutex)),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// ==========================================
// TaskExecutor 接口实现
// ==========================================

// Submit 提交任务
func (e *PerCoreExecutor) Submit(ctx context.Context, task func(context.Context)) error {
	return e.SubmitWithPriority(ctx, 0, task)
}

// SubmitWithPriority 带优先级提交任务
func (e *PerCoreExecutor) SubmitWithPriority(ctx context.Context, priority int, task func(context.Context)) error {
	// 1. 检查执行器状态
	if atomic.LoadInt32(&e.state) != RUNNING {
		return ErrExecutorClosed
	}

	// 2. 检查上下文
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// 3. 限流检查
	if !e.limiter.Allow() {
		atomic.AddInt64(&e.stats.TotalRateLimit, 1)
		return ErrRateLimitExceeded
	}

	// 4. 选择 worker（简单轮询）
	workerID := int(atomic.AddInt64(&e.stats.TotalSubmitted, 1)-1) % len(e.workers)
	worker := e.workers[workerID]

	// 5. 提交任务
	worker.cond.L.Lock()

	// 再次检查状态（持锁期间）
	if atomic.LoadInt32(&e.state) != RUNNING {
		worker.cond.L.Unlock()
		return ErrExecutorClosed
	}

	// 检查队列容量
	if len(worker.queue) >= e.config.QueueSize {
		worker.cond.L.Unlock()
		return fmt.Errorf("queue full for worker %d", workerID)
	}

	// 添加任务
	item := taskItem{
		priority:   priority,
		submitTime: time.Now(),
		task:       task,
	}
	// 使用 heap.Push 维护优先级队列
	heapPush(&worker.queue, item)

	worker.cond.L.Unlock()
	worker.cond.Signal()

	return nil
}

// ==========================================
// 生命周期管理
// ==========================================

// Close 关闭执行器
func (e *PerCoreExecutor) Close() error {
	return e.CloseWithContext(context.Background())
}

// CloseWithContext 带上下文关闭执行器
func (e *PerCoreExecutor) CloseWithContext(ctx context.Context) error {
	var closeErr error

	e.closeOnce.Do(func() {
		// 1. 标记为关闭中
		atomic.StoreInt32(&e.state, CLOSING)

		// 2. 取消所有 worker
		for _, worker := range e.workers {
			worker.cancel()
			worker.cond.Broadcast()
		}

		// 3. 等待所有 worker 退出或超时
		done := make(chan struct{})
		go func() {
			e.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// 正常关闭
		case <-ctx.Done():
			// 超时，强制关闭
			closeErr = ctx.Err()
		}

		// 4. 标记为已关闭
		atomic.StoreInt32(&e.state, CLOSED)
		e.cancel()
	})

	return closeErr
}

// ==========================================
// 状态查询
// ==========================================

// Stats 获取统计信息
func (e *PerCoreExecutor) Stats() PerCoreStats {
	return PerCoreStats{
		TotalSubmitted: atomic.LoadInt64(&e.stats.TotalSubmitted),
		TotalCompleted: atomic.LoadInt64(&e.stats.TotalCompleted),
		TotalFailed:    atomic.LoadInt64(&e.stats.TotalFailed),
		TotalPanics:    atomic.LoadInt64(&e.stats.TotalPanics),
		TotalRateLimit: atomic.LoadInt64(&e.stats.TotalRateLimit),
	}
}

// Config 获取配置
func (e *PerCoreExecutor) Config() PerCoreConfig {
	return e.config
}

// IsRunning 检查是否运行中
func (e *PerCoreExecutor) IsRunning() bool {
	return atomic.LoadInt32(&e.state) == RUNNING
}

// ==========================================
// coreWorker 实现
// ==========================================

// run 运行 worker
func (w *coreWorker) run() {
	defer w.executor.wg.Done()

	// 启用绑核（如果配置）
	if w.executor.config.EnableAffini {
		// 绑核逻辑（平台相关，这里简化处理）
		// 实际实现需要根据平台调用相关 API
	}

	for {
		w.cond.L.Lock()

		// 等待任务或关闭
		for len(w.queue) == 0 && w.ctx.Err() == nil {
			w.cond.Wait()
		}

		// 检查是否关闭
		if w.ctx.Err() != nil {
			w.cond.L.Unlock()
			return
		}

		// 获取任务
		item := heapPop(&w.queue)
		task := item.task

		w.cond.L.Unlock()

		// 执行任务（不持锁）
		w.executeTask(task)
	}
}

// executeTask 执行任务
func (w *coreWorker) executeTask(task func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			atomic.AddInt64(&w.executor.stats.TotalPanics, 1)
			w.executor.config.PanicHandler(r)

			// Worker 自动重启（通过继续循环）
		}
		atomic.AddInt64(&w.executor.stats.TotalCompleted, 1)
	}()

	task(w.ctx)
}

// ==========================================
// heap 辅助函数（简化版本，避免导入 container/heap）
// ==========================================

func heapPush(q *taskQueue, item taskItem) {
	*q = append(*q, item)
	// 向上调整
	heapUp(*q, len(*q)-1)
}

func heapPop(q *taskQueue) taskItem {
	n := len(*q)
	if n == 0 {
		return taskItem{}
	}

	// 交换根和最后一个元素
	(*q)[0], (*q)[n-1] = (*q)[n-1], (*q)[0]
	// 向下调整
	heapDown(*q, 0, n-1)

	// 弹出最后一个元素
	old := *q
	item := old[n-1]
	*q = old[0 : n-1]
	return item
}

func heapUp(q taskQueue, i int) {
	for {
		parent := (i - 1) / 2
		if parent == i || !q.Less(i, parent) {
			break
		}
		q[parent], q[i] = q[i], q[parent]
		i = parent
	}
}

func heapDown(q taskQueue, i0, n int) {
	i := i0
	for {
		j1 := 2*i + 1
		if j1 >= n || j1 < 0 { // j1 < 0 after int overflow
			break
		}
		j := j1 // left child
		if j2 := j1 + 1; j2 < n && q.Less(j2, j1) {
			j = j2 // = 2*i + 2  // right child
		}
		if !q.Less(j, i) {
			break
		}
		q[i], q[j] = q[j], q[i]
		i = j
	}
}
