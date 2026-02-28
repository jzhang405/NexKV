// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
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

// 使用 pkg/errors 中的错误定义
var (
	ErrExecutorClosed = errors.ErrExecutorClosed
	ErrInvalidConfig  = errors.ErrInvalidConfig
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
	EnableAffini      bool              // 启用绑核
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
	queue taskQueue
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
	submitTime time.Time
	task       func(context.Context)
}

// taskQueue 任务队列（实现 heap.Interface）
// 使用结构体包装 slice，以支持可配置的饥饿防护超时
type taskQueue struct {
	items             []taskItem    // 底层 slice
	starvationTimeout time.Duration // 饥饿防护超时时间
}

// Len 返回队列长度
func (q taskQueue) Len() int { return len(q.items) }

// Less 比较两个任务的优先级
func (q taskQueue) Less(i, j int) bool {
	// 饥饿防护：超时提升优先级
	if q.starvationTimeout > 0 {
		timeI := time.Since(q.items[i].submitTime)
		timeJ := time.Since(q.items[j].submitTime)
		iTimeout := timeI > q.starvationTimeout
		jTimeout := timeJ > q.starvationTimeout

		switch {
		case iTimeout && !jTimeout:
			return true // i 超时优先
		case !iTimeout && jTimeout:
			return false // j 超时优先
		case iTimeout && jTimeout:
			return timeI > timeJ // 都超时：等待时间长的优先
		}
	}

	// 优先级相同时 FIFO（先提交的先执行）
	if q.items[i].priority == q.items[j].priority {
		return q.items[i].submitTime.Before(q.items[j].submitTime)
	}

	// Unix 传统：数值越小越重要（0 最高，9 最低）
	return q.items[i].priority < q.items[j].priority
}

func (q taskQueue) Swap(i, j int) {
	q.items[i], q.items[j] = q.items[j], q.items[i]
}

func (q *taskQueue) Push(x any) {
	q.items = append(q.items, x.(taskItem))
}

func (q *taskQueue) Pop() any {
	old := q.items
	n := len(old)
	item := old[n-1]
	q.items = old[0 : n-1]
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
func WithPanicHandler(handler func(any)) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.PanicHandler = handler
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
		EnableAffini:      isAffinitySupported(), // 默认绑核（仅支持的平台）
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
		queue: taskQueue{
			items:             make([]taskItem, 0, e.config.QueueSize),
			starvationTimeout: e.config.StarvationTimeout,
		},
		cond:   sync.NewCond(new(sync.Mutex)),
		ctx:    ctx,
		cancel: cancel,
		pinned: false, // 初始状态：未绑定
	}
}

// ==========================================
// TaskExecutor 接口实现
// ==========================================

// Submit 提交任务（使用默认优先级：TaskPriorityNormal = 5）
func (e *PerCoreExecutor) Submit(ctx context.Context, task func(context.Context)) error {
	return e.SubmitWithPriority(ctx, model.TaskPriorityNormal, task)
}

// SubmitWithPriority 带优先级提交任务
func (e *PerCoreExecutor) SubmitWithPriority(ctx context.Context, priority model.TaskPriority, task func(context.Context)) error {
	// 检查上下文
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// 检查执行器状态
	if atomic.LoadInt32(&e.state) != RUNNING {
		return ErrExecutorClosed
	}

	// 选择 worker（简单轮询）
	workerID := int(atomic.AddInt64(&e.stats.TotalSubmitted, 1)-1) % len(e.workers)
	worker := e.workers[workerID]

	// 提交任务
	worker.cond.L.Lock()
	defer worker.cond.L.Unlock()

	// 再次检查状态（持锁期间）
	if atomic.LoadInt32(&e.state) != RUNNING {
		return ErrExecutorClosed
	}

	// 检查队列容量
	if worker.queue.Len() >= e.config.QueueSize {
		return errors.Wrapf(errors.ErrQueueFull, "worker %d", workerID)
	}

	// 使用 heap.Push 维护优先级队列
	heapPush(&worker.queue, taskItem{
		priority:   priority,
		submitTime: time.Now(),
		task:       task,
	})

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

// run 运行 worker
func (w *coreWorker) run() {
	defer w.executor.wg.Done()

	// 启用绑核（如果配置且尚未绑定）
	// 使用标志位确保只绑定一次，避免重复系统调用
	if w.executor.config.EnableAffini && !w.pinned {
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
	q.items = append(q.items, item)
	// 向上调整
	heapUp(q, len(q.items)-1)
}

func heapPop(q *taskQueue) taskItem {
	n := len(q.items)
	if n == 0 {
		return taskItem{}
	}

	// 交换根和最后一个元素
	q.items[0], q.items[n-1] = q.items[n-1], q.items[0]
	// 向下调整
	heapDown(q, 0, n-1)

	// 弹出最后一个元素
	item := q.items[n-1]
	q.items = q.items[0 : n-1]
	return item
}

func heapUp(q *taskQueue, i int) {
	for {
		parent := (i - 1) / 2
		if parent == i || !q.Less(i, parent) {
			break
		}
		q.items[parent], q.items[i] = q.items[i], q.items[parent]
		i = parent
	}
}

func heapDown(q *taskQueue, i0, n int) {
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
		q.items[i], q.items[j] = q.items[j], q.items[i]
		i = j
	}
}
