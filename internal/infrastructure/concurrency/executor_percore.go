// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/jzhang405/NexKV/pkg/recovery"
	"github.com/sirupsen/logrus"
)

// 执行器状态
const (
	RUNNING int32 = iota
	CLOSING
	CLOSED
)

// 优先级配置
const (
	// NumPriorityLevels 优先级级别数量（0-9，共 10 级）
	// 遵循 Unix 传统：0 最高优先级，9 最低优先级
	NumPriorityLevels = 10
)

// ==========================================
// 错误定义
// ==========================================

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
// 支持基于 SourceID 的智能调度，相同 SourceID 的任务总是在同一 Worker 上执行
type PerCoreExecutor struct {
	// 配置
	config PerCoreConfig

	// 状态
	state     int32 // RUNNING, CLOSING, CLOSED
	startTime time.Time

	// Workers
	workers []*coreWorker
	wg      sync.WaitGroup

	// SourceID -> Binding 映射（智能调度）
	// 相同 SourceID 的任务总是路由到同一个 Worker，保证 CPU 亲和性
	// 支持空闲超时自动解绑，让其他 SourceID 可以使用
	sourceBindings sync.Map   // map[string]*sourceIDBinding (线程安全)
	bindingMu      sync.Mutex // 保护首次绑定操作的互斥锁（确保严格独占绑定）

	// 提交计数器（混合清理策略：次数触发）
	submitCount    atomic.Int64 // 提交次数计数器
	cleanThreshold int64        // 清理阈值（每 N 次提交清理一次）

	// 清理协程控制（混合清理策略：时间触发）
	cleanupTicker *time.Ticker  // 清理定时器
	cleanupDone   chan struct{} // 停止清理协程信号

	// 统计
	stats PerCoreStats

	// 上下文
	ctx    context.Context
	cancel context.CancelFunc

	// 确保只关闭一次
	closeOnce sync.Once
}

// sourceIDBinding SourceID 与 Worker 的绑定关系
type sourceIDBinding struct {
	workerID     int64 // 绑定的 Worker ID（使用 int64 方便原子操作）
	lastUsedTime int64 // 最后使用时间（纳秒时间戳）
}

// PerCoreConfig Per-Core 执行器配置
type PerCoreConfig struct {
	NumCores             int               // 核心数
	QueueSize            int               // 每核心队列大小
	PanicHandler         func(any)         // Panic 处理器
	Labels               map[string]string // 标签（用于监控）
	StarvationTimeout    time.Duration     // 饥饿防护超时时间（默认 10s）
	BindingTimeout       time.Duration     // SourceID 绑定超时时间（默认 30秒，超时后自动解绑）
	BindingCleanInterval time.Duration     // 绑定清理时间间隔（默认 30秒，混合策略-时间触发）
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
// 使用 NumPriorityLevels 个独立队列替代堆结构，大幅提升性能
// 使用 bitmap 优化优先级查找，避免循环扫描
type taskQueue struct {
	queues            [NumPriorityLevels][]taskItem // 优先级队列（0 最高，9 最低）
	bitmap            uint16                        // 位图：位 0-9 表示对应优先级队列是否有任务
	starvationCheck   int64                         // 上次饥饿检查时间（纳秒）
	starvationTimeout time.Duration                 // 饥饿防护超时时间（0 = 禁用）
	checkInterval     int64                         // 饥饿检查间隔（默认 10ms）
	mu                sync.RWMutex                  // 保护队列访问（优化：读写锁提高并发）
}

// newTaskQueue 创建新的任务队列
func newTaskQueue(capacity int, starvationTimeout time.Duration) *taskQueue {
	q := &taskQueue{
		starvationTimeout: starvationTimeout,
		checkInterval:     10_000_000, // 10ms
	}
	// 预分配队列容量
	for i := range NumPriorityLevels {
		q.queues[i] = make([]taskItem, 0, capacity/NumPriorityLevels)
	}
	return q
}

// Len 返回队列总长度（优化：使用读锁）
func (q *taskQueue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	total := 0
	for i := range NumPriorityLevels {
		total += len(q.queues[i])
	}
	return total
}

// LenUnsafe 返回队列总长度（无锁版本，调用者必须已持有锁）
// 用于在已持锁的场景下避免读锁升级写锁问题
func (q *taskQueue) LenUnsafe() int {
	total := 0
	for i := range q.queues {
		total += len(q.queues[i])
	}
	return total
}

// Push 添加任务到对应优先级队列（O(1)，无锁快速路径
func (q *taskQueue) Push(item taskItem) {
	p := int(item.priority)
	// 限制优先级范围到 [0, NumPriorityLevels-1]
	p = max(0, min(p, NumPriorityLevels-1))
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
	if p >= NumPriorityLevels {
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
// 从优先级 1 到 NumPriorityLevels-1 检查（跳过最高优先级 0）
func (q *taskQueue) promoteStarvedTasks(now int64) {
	timeout := int64(q.starvationTimeout)
	if timeout <= 0 {
		return
	}

	// 从优先级 1-9 检查（跳过最高优先级 0）
	for p := 1; p < NumPriorityLevels; p++ {
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

// WithBindingTimeout 设置 SourceID 绑定超时时间
// 超时后自动解绑，让其他 SourceID 可以使用该 Worker
// 默认 30 秒，设置为 0 表示禁用自动清理（永久绑定）
// 混合清理策略：在提交任务时定期检查（每 1000 次）+ 后台定时器（可配置间隔）
func WithBindingTimeout(timeout time.Duration) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.BindingTimeout = timeout
	}
}

// WithBindingCleanInterval 设置绑定清理的时间间隔（混合策略-时间触发）
// 默认 30 秒，与 BindingTimeout 相同确保及时性
// 设置为 0 表示禁用时间触发，仅使用次数触发
func WithBindingCleanInterval(interval time.Duration) PerCoreOption {
	return func(c *PerCoreConfig) {
		c.BindingCleanInterval = interval
	}
}

// ==========================================
// 构造函数
// ==========================================

// NewPerCoreExecutor 创建 Per-Core 执行器
func NewPerCoreExecutor(opts ...PerCoreOption) (*PerCoreExecutor, error) {
	// 默认配置：总是使用所有 CPU 核心
	config := PerCoreConfig{
		NumCores:             runtime.NumCPU(), // 固定使用所有核心
		QueueSize:            DefaultQueueSize,
		PanicHandler:         defaultPanicHandler,
		StarvationTimeout:    10 * time.Second, // 默认饥饿防护超时 10 秒
		BindingTimeout:       30 * time.Second, // 默认绑定超时 30 秒
		BindingCleanInterval: 30 * time.Second, // 默认清理间隔 30 秒（混合策略-时间触发）
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
		config:         config,
		state:          RUNNING,
		startTime:      time.Now(),
		ctx:            ctx,
		cancel:         cancel,
		cleanThreshold: 1000, // 每 1000 次提交清理一次（混合策略-次数触发）
		cleanupDone:    make(chan struct{}),
	}

	// 创建 workers
	e.workers = make([]*coreWorker, config.NumCores)
	for i := range e.workers {
		worker := e.newWorker(i)
		e.workers[i] = worker
		e.wg.Add(1) // P1-02: 先 Add 再启动 goroutine，避免竞态
		go worker.run()
	}

	// 启动绑定清理协程（混合策略：时间触发）
	if config.BindingTimeout > 0 && config.BindingCleanInterval > 0 {
		e.startBindingCleaner()
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

// SubmitWithSource 基于 SourceID 提交任务（智能调度）
// 相同 SourceID 的任务总是路由到同一个 Worker，保证 CPU 亲和性
// 调度规则：
//   - 规则 A：首次提交时，选择空闲 Worker 绑定，记录到 map[source]=workerID
//   - 规则 B：后续相同 source，从 map 复用 Worker，并更新最后使用时间
func (e *PerCoreExecutor) SubmitWithSource(
	ctx context.Context,
	sourceID model.SourceID,
	priority model.TaskPriority,
	task func(context.Context),
) error {
	// 检查上下文
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// 检查 SourceID 有效性
	if err := sourceID.Validate(); err != nil {
		return err
	}

	// 检查执行器状态
	if atomic.LoadInt32(&e.state) != RUNNING {
		logrus.Debugf("[PerCore] Executor is closed, rejecting task")
		return ErrExecutorClosed
	}

	sourceKey := sourceID.Hash()
	now := time.Now().UnixNano()

	// 规则 B：快速路径 - 尝试从 map 获取已绑定的 WorkerID（无锁）
	if bindingValue, ok := e.sourceBindings.Load(sourceKey); ok {
		binding := bindingValue.(*sourceIDBinding)
		workerID := int(binding.workerID)
		worker := e.workers[workerID]

		// 更新最后使用时间
		atomic.StoreInt64(&binding.lastUsedTime, now)

		// 提交到绑定的 Worker
		return e.submitToWorker(ctx, workerID, worker, priority, task)
	}

	// 规则 A：首次绑定 - 慢速路径（需要保护 selectIdleWorker + Store 的原子性）
	e.bindingMu.Lock()

	// 双重检查：等待锁期间其他 goroutine 可能已绑定
	if bindingValue, ok := e.sourceBindings.Load(sourceKey); ok {
		e.bindingMu.Unlock()
		binding := bindingValue.(*sourceIDBinding)
		workerID := int(binding.workerID)
		worker := e.workers[workerID]
		atomic.StoreInt64(&binding.lastUsedTime, now)
		return e.submitToWorker(ctx, workerID, worker, priority, task)
	}

	// 选择未绑定的 Worker（持锁，保证独占）
	workerID, err := e.selectIdleWorker()
	if err != nil {
		e.bindingMu.Unlock()
		logrus.Warnf("[PerCore] All workers busy, rejecting SourceID=%s", sourceKey)
		return err
	}

	// 创建并存储新的绑定关系（持锁，保证原子性）
	newBinding := &sourceIDBinding{
		workerID:     int64(workerID),
		lastUsedTime: now,
	}
	e.sourceBindings.Store(sourceKey, newBinding)
	e.bindingMu.Unlock()

	worker := e.workers[workerID]
	logrus.Debugf("[PerCore] SourceID %s bound to Worker %d", sourceKey, workerID)

	return e.submitToWorker(ctx, workerID, worker, priority, task)
}

// selectIdleWorker 选择未绑定的 Worker
// 严格独占绑定：每个 Worker 只能绑定一个 SourceID
// 返回未绑定的 WorkerID，如果全部已被绑定则返回错误
func (e *PerCoreExecutor) selectIdleWorker() (int, error) {
	// 收集已绑定的 WorkerID
	boundWorkers := make(map[int]bool)
	e.sourceBindings.Range(func(_, value interface{}) bool {
		binding := value.(*sourceIDBinding)
		boundWorkers[int(binding.workerID)] = true
		return true
	})

	// 找第一个未绑定的 Worker
	for i := range e.workers {
		if !boundWorkers[i] {
			return i, nil
		}
	}

	return 0, errors.ErrAllWorkersBusy
}

// startBindingCleaner 启动绑定清理协程（混合策略：时间触发）
func (e *PerCoreExecutor) startBindingCleaner() {
	// 防御性检查：避免 NewTicker(0) panic
	if e.config.BindingCleanInterval <= 0 {
		logrus.Warnf("[PerCore] Invalid BindingCleanInterval=%v, skipping cleaner start",
			e.config.BindingCleanInterval)
		return
	}

	e.cleanupTicker = time.NewTicker(e.config.BindingCleanInterval)

	go func() {
		defer e.cleanupTicker.Stop()

		for {
			select {
			case <-e.cleanupTicker.C:
				e.cleanExpiredBindings()
			case <-e.cleanupDone:
				return
			case <-e.ctx.Done():
				return
			}
		}
	}()

	logrus.Infof("[PerCore] Binding cleaner started (interval=%v, timeout=%v, strategy=mixed)",
		e.config.BindingCleanInterval, e.config.BindingTimeout)
}

// cleanExpiredBindings 清理过期的 SourceID 绑定
// 混合策略：由定时器（时间触发）或提交任务（次数触发）调用
func (e *PerCoreExecutor) cleanExpiredBindings() {
	now := time.Now().UnixNano()
	timeoutNanos := e.config.BindingTimeout.Nanoseconds()

	var cleanedCount int
	e.sourceBindings.Range(func(key, value interface{}) bool {
		binding := value.(*sourceIDBinding)
		lastUsed := atomic.LoadInt64(&binding.lastUsedTime)

		// 检查是否超时
		if now-lastUsed > timeoutNanos {
			e.sourceBindings.Delete(key)
			cleanedCount++
			logrus.Debugf("[PerCore] Cleaned expired binding: SourceID=%s, WorkerID=%d, idle_time=%v",
				key, binding.workerID, time.Duration(now-lastUsed))
		}
		return true
	})

	if cleanedCount > 0 {
		logrus.Infof("[PerCore] Cleaned %d expired SourceID bindings", cleanedCount)
	}
}

// submitToWorker 提交任务到指定 Worker（内部方法）
func (e *PerCoreExecutor) submitToWorker(
	ctx context.Context,
	workerID int,
	worker *coreWorker,
	priority model.TaskPriority,
	task func(context.Context),
) error {
	// 创建任务项（在锁外，减少临界区）
	item := taskItem{
		priority:   priority,
		submitTime: time.Now().UnixNano(),
		task:       task,
	}

	logrus.Debugf("[PerCore] Submitting task with priority %d to worker %d", priority, workerID)

	// 持锁进行所有队列操作，避免读锁升级写锁问题
	worker.cond.L.Lock()

	// 检查状态（持锁期间）
	if atomic.LoadInt32(&e.state) != RUNNING {
		worker.cond.L.Unlock()
		logrus.Debugf("[PerCore] Executor closed during submit")
		return ErrExecutorClosed
	}

	// 检查队列容量（使用无锁版本，避免读锁升级写锁）
	if worker.queue.LenUnsafe() >= e.config.QueueSize {
		worker.cond.L.Unlock()
		logrus.Warnf("[PerCore] Worker %d queue full (len=%d)", workerID, worker.queue.Len())
		return errors.Wrapf(errors.ErrQueueFull, "worker %d", workerID)
	}

	// Push 到队列（持锁）
	worker.queue.Push(item)

	// 通知 worker（持锁）
	worker.cond.Signal()
	worker.cond.L.Unlock()

	atomic.AddInt64(&e.stats.TotalSubmitted, 1)

	// 统一处理次数触发清理（所有提交路径共享）
	if e.config.BindingTimeout > 0 {
		count := e.submitCount.Add(1)
		if count%e.cleanThreshold == 0 {
			go e.cleanExpiredBindings() // 异步清理，不阻塞提交
		}
	}

	return nil
}

// SubmitWithPriority 带优先级提交任务（优化：减少锁持有时间）
func (e *PerCoreExecutor) SubmitWithPriority(ctx context.Context, priority model.TaskPriority, task func(context.Context)) error {
	// 检查上下文
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// 检查执行器状态
	if atomic.LoadInt32(&e.state) != RUNNING {
		logrus.Debugf("[PerCore] Executor is closed, rejecting task")
		return ErrExecutorClosed
	}

	// 选择 worker（简单轮询）
	workerID := int(atomic.AddInt64(&e.stats.TotalSubmitted, 1)-1) % len(e.workers)
	worker := e.workers[workerID]

	// 优化：在持锁之前检查队列容量（快速失败）
	if worker.queue.Len() >= e.config.QueueSize {
		logrus.Warnf("[PerCore] Worker %d queue full (len=%d)", workerID, worker.queue.Len())
		return errors.Wrapf(errors.ErrQueueFull, "worker %d", workerID)
	}

	// 创建任务项（在锁外，减少临界区）
	item := taskItem{
		priority:   priority,
		submitTime: time.Now().UnixNano(),
		task:       task,
	}

	logrus.Debugf("[PerCore] Submitting task with priority %d to worker %d", priority, workerID)

	// 优化：只在必要时持有锁
	worker.cond.L.Lock()

	// 再次检查状态（持锁期间）
	if atomic.LoadInt32(&e.state) != RUNNING {
		worker.cond.L.Unlock()
		logrus.Debugf("[PerCore] Executor closed during submit")
		return ErrExecutorClosed
	}

	// 再次检查队列容量（持锁期间，使用无锁版本避免读锁升级写锁）
	if worker.queue.LenUnsafe() >= e.config.QueueSize {
		worker.cond.L.Unlock()
		logrus.Warnf("[PerCore] Worker %d queue full during submit (len=%d)", workerID, worker.queue.LenUnsafe())
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

		// 停止绑定清理协程
		if e.cleanupTicker != nil {
			e.cleanupTicker.Stop()
		}
		if e.cleanupDone != nil {
			close(e.cleanupDone)
		}

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

// executeTask 执行任务（使用统一的 panic 恢复）
func (w *coreWorker) executeTask(task func(context.Context)) {
	// 使用统一的 recovery 包，保留自定义逻辑
	_ = recovery.Safe(func() {
		// 执行任务
		task(w.ctx)
	}, func(r any, stack []byte) {
		// 统计 panic
		atomic.AddInt64(&w.executor.stats.TotalPanics, 1)
		logrus.Errorf("[PerCore] Worker %d panic recovered: %v", w.coreID, r)

		// 调用用户配置的 panic 处理器
		if w.executor.config.PanicHandler != nil {
			w.executor.config.PanicHandler(r)
		}
		// Worker 自动重启（通过继续循环）
	})

	// 任务完成统计
	atomic.AddInt64(&w.executor.stats.TotalCompleted, 1)
}
