// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"log"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// 常量定义
// ==========================================

const (
	// MaxCores 最大核心数限制
	MaxCores = 64

	// DefaultQueueSize 默认队列大小
	DefaultQueueSize = 1000
)

// 执行器状态
const (
	RUNNING int32 = iota
	CLOSING
	CLOSED
)

// 日志级别
const (
	// LogLevelDebug 调试级别（输出所有日志）
	LogLevelDebug int = iota
	// LogLevelInfo 信息级别（输出重要信息）
	LogLevelInfo
	// LogLevelWarn 警告级别（只输出警告和错误）
	LogLevelWarn
	// LogLevelError 错误级别（只输出错误）
	LogLevelError
)

// ==========================================
// 全局变量
// ==========================================

var (
	// executorLogLevel 执行器日志级别（原子变量，可动态调整）
	// 默认：LogLevelError（生产环境推荐）
	executorLogLevel int32 = int32(LogLevelError)
)

// SetExecutorLogLevel 设置执行器日志级别
func SetExecutorLogLevel(level int) {
	atomic.StoreInt32(&executorLogLevel, int32(level))
}

// ==========================================
// 错误定义
// ==========================================

// 使用 pkg/errors 中的错误定义
var (
	ErrExecutorClosed = errors.ErrExecutorClosed
	ErrInvalidConfig  = errors.ErrInvalidConfig
)

// ==========================================
// 日志辅助函数
// ==========================================

// shouldLog 检查是否应该输出日志
func shouldLog(level int) bool {
	return int32(level) >= atomic.LoadInt32(&executorLogLevel)
}

// logf 格式化日志输出（条件判断）
func logf(level int, format string, args ...any) {
	if shouldLog(level) {
		log.Printf(format, args...)
	}
}

// logDebug 调试日志
func logDebug(format string, args ...any) {
	logf(LogLevelDebug, format, args...)
}

// logWarn 警告日志
func logWarn(format string, args ...any) {
	logf(LogLevelWarn, format, args...)
}

// logError 错误日志
func logError(format string, args ...any) {
	logf(LogLevelError, format, args...)
}

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
	NumCores          int               // 核心数
	QueueSize         int               // 每核心队列大小
	PanicHandler      func(any)         // Panic 处理器
	Labels            map[string]string // 标签（用于监控）
	StarvationTimeout time.Duration     // 饥饿防护超时时间（默认 10s）
}

// PerCoreStats Per-Core 执行器统计
type PerCoreStats struct {
	TotalSubmitted int64 // 总提交任务数
	TotalCompleted int64 // 总完成任务数
	TotalFailed    int64 // 总失败任务数
	TotalPanics    int64 // 总 Panic 次数
	QueueLength    int64 // 当前队列长度
	ActiveWorkers  int64 // 活跃 Worker 数
}

// coreWorker 核心工作器
type coreWorker struct {
	coreID   int
	executor *PerCoreExecutor

	// 优先级队列
	queue *taskQueue
	cond  *sync.Cond

	// 上下文
	ctx    context.Context
	cancel context.CancelFunc

	// 绑核状态标志（确保只绑定一次）
	pinned bool
}

// taskItem 任务项
// 优化说明：移除过大的 padding（64字节），避免内存浪费
// 伪共享问题通过核心本地数据和独立的 worker 队列已经解决
type taskItem struct {
	priority   model.TaskPriority
	submitTime int64 // 纳秒时间戳（优化：使用 int64 避免 time.Time 计算）
	task       func(context.Context)
}

// taskQueue 多级优先级队列（O(1) Push/Pop）
// 使用 10 个独立队列替代堆结构，大幅提升性能
// 使用 bitmap 优化优先级查找，避免循环扫描
type taskQueue struct {
	queues            [10][]taskItem // 10 个优先级队列（0 最高，9 最低）
	bitmap            uint16         // 位图：位 0-9 表示对应优先级队列是否有任务
	starvationCheck   int64          // 上次饥饿检查时间（纳秒）
	starvationTimeout time.Duration  // 饥饿防护超时时间（0 = 禁用）
	checkInterval     int64          // 饥饿检查间隔（默认 10ms）
	mu                sync.RWMutex   // 保护队列访问（优化：读写锁提高并发）
}

// newTaskQueue 创建新的任务队列
func newTaskQueue(capacity int, starvationTimeout time.Duration) *taskQueue {
	q := &taskQueue{
		starvationTimeout: starvationTimeout,
		checkInterval:     10_000_000, // 10ms
	}
	// 预分配队列容量
	for i := 0; i < 10; i++ {
		q.queues[i] = make([]taskItem, 0, capacity/10)
	}
	return q
}

// Len 返回队列总长度（优化：使用读锁）
func (q *taskQueue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	total := 0
	for i := 0; i < 10; i++ {
		total += len(q.queues[i])
	}
	return total
}

// Push 添加任务到对应优先级队列（O(1)，无锁快速路径
func (q *taskQueue) Push(item taskItem) {
	p := int(item.priority)
	// 限制优先级范围到 [0, 9]
	if p < 0 {
		p = 0
	}
	if p > 9 {
		p = 9
	}
	// 更新 item 的优先级（确保一致性）
	item.priority = model.TaskPriority(p)

	// 优化：使用写锁保护队列操作
	q.mu.Lock()
	wasEmpty := len(q.queues[p]) == 0
	q.queues[p] = append(q.queues[p], item)
	// 更新 bitmap：如果队列之前为空，设置对应位
	if wasEmpty {
		q.bitmap |= (1 << p)
	}
	q.mu.Unlock()
}

// Pop 从最高优先级非空队列取出任务（O(1)）
// 使用 bitmap 快速查找，避免循环扫描
func (q *taskQueue) Pop() taskItem {
	q.mu.Lock()
	defer q.mu.Unlock()

	// 定期检查并提升超时的低优先级任务
	if q.starvationTimeout > 0 {
		now := time.Now().UnixNano()
		lastCheck := atomic.LoadInt64(&q.starvationCheck)

		if now-lastCheck > q.checkInterval {
			// 尝试更新检查时间（只有一个 goroutine 会成功）
			if atomic.CompareAndSwapInt64(&q.starvationCheck, lastCheck, now) {
				q.promoteStarvedTasks(now)
			}
		}
	}

	// 使用 bitmap 快速找到第一个非空队列（O(1)）
	// bits.TrailingZeros16 返回从最低位开始的连续零数
	// 即 bitmap 中第一个为 1 的位的位置
	bitmap := q.bitmap
	if bitmap == 0 {
		// 所有队列都为空
		return taskItem{}
	}

	// 找到第一个非空队列（优先级最高的）
	p := bits.TrailingZeros16(bitmap)
	if p >= 10 {
		// 不应该发生，但防御性编程
		return taskItem{}
	}

	// 取出第一个任务（FIFO）
	item := q.queues[p][0]
	q.queues[p] = q.queues[p][1:]

	// 如果队列变为空，清除 bitmap 对应位
	if len(q.queues[p]) == 0 {
		q.bitmap &^= (1 << p)
	}

	return item
}

// promoteStarvedTasks 提升超时的低优先级任务到最高优先级队列
// 防止低优先级任务在高负载下饥饿
func (q *taskQueue) promoteStarvedTasks(now int64) {
	timeout := int64(q.starvationTimeout)
	if timeout <= 0 {
		return
	}

	// 从优先级 1-9 检查（跳过最高优先级 0）
	for p := 1; p < 10; p++ {
		n := len(q.queues[p])
		for i := 0; i < n; {
			item := q.queues[p][i]
			// 检查是否超时
			if now-item.submitTime > timeout {
				// 从当前队列移除
				copy(q.queues[p][i:], q.queues[p][i+1:])
				q.queues[p] = q.queues[p][:len(q.queues[p])-1]
				n--

				// 如果队列 p 变为空，清除 bitmap 对应位
				if len(q.queues[p]) == 0 {
					q.bitmap &^= (1 << p)
				}

				// 提升到优先级 0（修改优先级字段）
				item.priority = model.TaskPriority(0)
				wasEmpty := len(q.queues[0]) == 0
				q.queues[0] = append(q.queues[0], item)

				// 更新优先级 0 的 bitmap
				if wasEmpty {
					q.bitmap |= 1 // 设置位 0
				}
			} else {
				i++
			}
		}
	}
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
func WithPanicHandler(handler func(any)) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.PanicHandler = handler
	}
}

// WithLabels 设置标签
func WithLabels(labels map[string]string) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.Labels = labels
	}
}

// WithStarvationTimeout 设置饥饿防护超时时间
// 超时后低优先级任务会被自动提升优先级，防止饥饿
// 默认 10 秒，设置为 0 表示禁用饥饿防护
func WithStarvationTimeout(timeout time.Duration) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.StarvationTimeout = timeout
	}
}

// ==========================================
// 构造函数
// ==========================================

// NewPerCoreExecutor 创建 Per-Core 执行器
func NewPerCoreExecutor(opts ...PerCoreOption) (*PerCoreExecutor, error) {
	// 默认配置
	config := PerCoreConfig{
		NumCores:          runtime.NumCPU(),
		QueueSize:         DefaultQueueSize,
		PanicHandler:      defaultPanicHandler,
		StarvationTimeout: 10 * time.Second, // 默认饥饿防护超时 10 秒
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
	}

	// 创建 workers
	e.workers = make([]*coreWorker, config.NumCores)
	for i := 0; i < config.NumCores; i++ {
		worker := e.newWorker(i)
		e.workers[i] = worker
		e.wg.Add(1) // P1-02: 先 Add 再启动 goroutine，避免竞态
		go worker.run()
	}

	return e, nil
}

// validateConfig 验证配置
func validateConfig(config *PerCoreConfig) error {
	if config.NumCores <= 0 {
		return errors.Wrapf(ErrInvalidConfig, "NumCores must be positive, got %d", config.NumCores)
	}
	if config.NumCores > MaxCores {
		return errors.Wrapf(ErrInvalidConfig, "NumCores (%d) exceeds maximum (%d)", config.NumCores, MaxCores)
	}
	if config.QueueSize <= 0 {
		return errors.Wrapf(ErrInvalidConfig, "QueueSize must be positive, got %d", config.QueueSize)
	}
	if config.PanicHandler == nil {
		config.PanicHandler = defaultPanicHandler
	}
	return nil
}

// defaultPanicHandler 默认 Panic 处理器
func defaultPanicHandler(r any) {
	// 可以记录日志或上报监控
}

// newWorker 创建核心工作器
func (e *PerCoreExecutor) newWorker(coreID int) *coreWorker {
	ctx, cancel := context.WithCancel(e.ctx)

	return &coreWorker{
		coreID:   coreID,
		executor: e,
		queue:    newTaskQueue(e.config.QueueSize, e.config.StarvationTimeout),
		cond:     sync.NewCond(new(sync.Mutex)),
		ctx:      ctx,
		cancel:   cancel,
		pinned:   false, // 初始状态：未绑定
	}
}

// ==========================================
// TaskExecutor 接口实现
// ==========================================

// Submit 提交任务（使用默认优先级：TaskPriorityNormal = 5）
func (e *PerCoreExecutor) Submit(ctx context.Context, task func(context.Context)) error {
	return e.SubmitWithPriority(ctx, model.TaskPriorityNormal, task)
}

// SubmitWithPriority 带优先级提交任务（优化：减少锁持有时间）
func (e *PerCoreExecutor) SubmitWithPriority(ctx context.Context, priority model.TaskPriority, task func(context.Context)) error {
	// 检查上下文
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// 检查执行器状态
	if atomic.LoadInt32(&e.state) != RUNNING {
		logDebug("[PerCore] Executor is closed, rejecting task")
		return ErrExecutorClosed
	}

	// 选择 worker（简单轮询）
	workerID := int(atomic.AddInt64(&e.stats.TotalSubmitted, 1)-1) % len(e.workers)
	worker := e.workers[workerID]

	// 优化：在持锁之前检查队列容量（快速失败）
	if worker.queue.Len() >= e.config.QueueSize {
		logWarn("[PerCore] Worker %d queue full (len=%d)", workerID, worker.queue.Len())
		return errors.Wrapf(errors.ErrQueueFull, "worker %d", workerID)
	}

	// 创建任务项（在锁外，减少临界区）
	item := taskItem{
		priority:   priority,
		submitTime: time.Now().UnixNano(),
		task:       task,
	}

	logDebug("[PerCore] Submitting task with priority %d to worker %d", priority, workerID)

	// 优化：只在必要时持有锁
	worker.cond.L.Lock()

	// 再次检查状态（持锁期间）
	if atomic.LoadInt32(&e.state) != RUNNING {
		worker.cond.L.Unlock()
		logDebug("[PerCore] Executor closed during submit")
		return ErrExecutorClosed
	}

	// 再次检查队列容量（持锁期间）
	if worker.queue.Len() >= e.config.QueueSize {
		worker.cond.L.Unlock()
		logWarn("[PerCore] Worker %d queue full during submit (len=%d)", workerID, worker.queue.Len())
		return errors.Wrapf(errors.ErrQueueFull, "worker %d", workerID)
	}

	// 添加到多级队列（O(1) 操作，已经在 queue.Push 内部处理锁）
	worker.queue.Push(item)

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
		atomic.StoreInt32(&e.state, CLOSING)

		// 取消所有 worker 并广播
		for _, worker := range e.workers {
			worker.cancel()
			worker.cond.L.Lock()
			worker.cond.Broadcast()
			worker.cond.L.Unlock()
		}

		// 等待所有 worker 退出或超时
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

// run 运行 worker（优化：减少锁持有时间）
func (w *coreWorker) run() {
	defer w.executor.wg.Done()

	// 启用绑核（PerCore 总是启用绑核）
	// 使用标志位确保只绑定一次，避免重复系统调用
	if !w.pinned {
		// macOS 特殊处理：使用 LockOSThread + defer UnlockOSThread
		// Linux/Windows: pinToCore 内部已经处理了 LockOSThread
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		if err := pinToCore(w.coreID); err == nil {
			w.pinned = true // 标记已绑定
		}
		// 绑核失败不应阻止 worker 启动
	}

	for {
		w.cond.L.Lock()

		// 等待任务或关闭
		for w.queue.Len() == 0 && w.ctx.Err() == nil {
			w.cond.Wait()
		}

		// 检查是否关闭
		if w.ctx.Err() != nil {
			w.cond.L.Unlock()
			return
		}

		// 获取任务（从多级队列 O(1) 取出）
		item := w.queue.Pop()
		if item.task == nil {
			// 队列为空，继续等待
			w.cond.L.Unlock()
			continue
		}
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
			logError("[PerCore] Worker %d panic recovered: %v", w.coreID, r)

			// 调用 panic 处理器
			if w.executor.config.PanicHandler != nil {
				w.executor.config.PanicHandler(r)
			}

			// Worker 自动重启（通过继续循环）
		}
		atomic.AddInt64(&w.executor.stats.TotalCompleted, 1)
	}()

	// 执行任务
	task(w.ctx)
}
