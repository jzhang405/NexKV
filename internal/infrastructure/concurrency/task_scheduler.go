// Package concurrency 提供并发控制和任务调度机制
package concurrency

import (
	"context"
	"fmt"
	"math/bits"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// NumPriorityLevels 优先级级别数量（与 executor_percore.go 保持一致）
// TaskPriorityCritical(0) ~ TaskPriorityIdle(9)

// lbConfig 负载均衡配置常量
const (
	// lbLowLoadPerCore 每个 core 的低负载阈值
	// 队列长度 < lbLowLoadPerCore × coreCount 时走 RoundRobin 快速路径
	lbLowLoadPerCore = 16
)

// ==========================================
// ShardTask 分片任务（独立队列）
// ==========================================

// taskBox 任务包装器（用于在 MPSC RingBuffer 中存储 any 类型）
type taskBox struct {
	item any
}

// ShardTask 分片任务（每个调度器独立实例）
type ShardTask struct {
	name           string
	priority       model.TaskPriority
	executionOrder int
	queue          *MPSCExtQueue // 无锁扩展环形缓冲区（数组+链表）
	mu             sync.Mutex
	taskStatus     atomic.Int32 // TaskStatus
	executeFunc    func(any) TaskStatus

	// 队列项计数指针（指向 SchedulerCore.totalQueueItems，用于 O(1) 总队列长度查询）
	totalQueueItemsPtr *atomic.Int64

	// 饥饿防护（参考 executor_percore.go:146-147）
	lastSubmitTime atomic.Int64 // 最近一次 Enqueue 的时间（UnixNano）
	priorityBoost  atomic.Bool  // 饥饿防护：临时提升标志
}

// NewShardTask 创建分片任务
func NewShardTask(name string, priority model.TaskPriority, executionOrder int, executeFunc func(any) TaskStatus) *ShardTask {
	t := &ShardTask{
		name:           name,
		priority:       priority,
		executionOrder: executionOrder,
		queue:          NewMPSCExtQueue(),
		executeFunc:    executeFunc,
	}
	t.taskStatus.Store(int32(TaskQueued))
	return t
}

// ==========================================
// Task 接口实现
// ==========================================

func (t *ShardTask) QueueLen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return int(t.queue.Size())
}

func (t *ShardTask) Enqueue(item any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	box := new(taskBox)
	box.item = item
	ok := t.queue.Enqueue(unsafe.Pointer(box))
	if ok && t.totalQueueItemsPtr != nil {
		t.totalQueueItemsPtr.Add(1)
		// 记录入队时间，为饥饿防护提供时间戳
		t.lastSubmitTime.Store(time.Now().UnixNano())
	}
	return nil
}

func (t *ShardTask) Peek(item *any) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	ptr, ok := t.queue.Peek()
	if !ok {
		return false
	}
	box := (*taskBox)(ptr)
	*item = box.item

	t.taskStatus.Store(int32(TaskExecuting))
	return true
}

// PeekN 批量查看队首 N 个元素（不出队）
// 返回实际查看的元素数量
func (t *ShardTask) PeekN(items []any) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(items) == 0 {
		return 0
	}

	// 临时 buffer 用于 ring buffer
	tmp := make([]unsafe.Pointer, len(items))
	n := t.queue.PeekN(tmp)
	if n == 0 {
		return 0
	}

	// 解包并复制到输出
	for i := range n {
		box := (*taskBox)(tmp[i])
		items[i] = box.item
	}

	t.taskStatus.Store(int32(TaskExecuting))
	return n
}

func (t *ShardTask) Dequeue(item *any) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	ptr, ok := t.queue.Dequeue()
	if !ok {
		return false
	}
	box := (*taskBox)(ptr)
	*item = box.item

	if t.totalQueueItemsPtr != nil {
		t.totalQueueItemsPtr.Add(-1)
	}
	return true
}

// DequeueN 批量出队（丢弃）: O(N)
// 一次性出队并丢弃 n 个元素，减少锁竞争
// 返回实际出队的元素数量
func (t *ShardTask) DequeueN(n int) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if n <= 0 {
		return 0
	}

	// 临时 buffer 用于接收（丢弃）
	tmp := make([]unsafe.Pointer, n)
	actualDequeue := t.queue.DequeueN(tmp)
	if t.totalQueueItemsPtr != nil {
		t.totalQueueItemsPtr.Add(int64(-actualDequeue))
	}
	return actualDequeue
}

func (t *ShardTask) Execute(item any) TaskStatus {
	if t.executeFunc != nil {
		return t.executeFunc(item)
	}
	return TaskPassed
}

func (t *ShardTask) Name() string {
	return t.name
}

func (t *ShardTask) Priority() model.TaskPriority {
	return t.priority
}

func (t *ShardTask) ExecutionOrder() int {
	return t.executionOrder
}

func (t *ShardTask) GetTask() *model.BaseTask[any] {
	// ShardTask 不使用 BaseTask
	return nil
}

// LastSubmitTime 返回最近一次 Enqueue 的纳秒时间戳
func (t *ShardTask) LastSubmitTime() int64 {
	return t.lastSubmitTime.Load()
}

// SetPriorityBoost 设置/清除临时优先级提升
func (t *ShardTask) SetPriorityBoost(boost bool) {
	t.priorityBoost.Store(boost)
}

// HasPriorityBoost 检查是否有临时优先级提升
func (t *ShardTask) HasPriorityBoost() bool {
	return t.priorityBoost.Load()
}

// ==========================================
// SchedulerCore 单个调度器核心
// ==========================================

// CoreStats 单个 Core 的统计信息（V2）
type CoreStats struct {
	CoreID              int          // Core ID
	TotalCycles         atomic.Int64 // 总循环次数
	TotalTasksProcessed atomic.Int64 // 总处理任务数
	EmptyWaits          atomic.Int64 // 空等待次数
	PanicCount          atomic.Int64 // Panic 恢复次数
	QueueLen            atomic.Int64 // 当前队列长度
}

// SchedulerCore 单个调度器核心（对应一个 CPU 核心）
type SchedulerCore struct {
	coreID int

	// 优先级桶：替代原来的 tasks []*ShardTask 平面数组
	// 参考 executor_percore.go taskQueue.queues
	priorityBuckets [NumPriorityLevels][]*ShardTask // 按优先级分桶，桶内按 executionOrder 排序
	activeBitmap    uint16                          // 位图：标记哪些优先级桶有注册的 ShardTask

	// taskMap: 只读（Start 后不再写入），并发只读安全，无需锁
	taskMap map[string]*ShardTask // name → ShardTask（RegisterTask 时填充，只读）

	// runLoop 缓存（避免每轮重建）
	cachedBuckets [NumPriorityLevels][]*ShardTask

	// 饥饿防护（参考 executor_percore.go:146-147）
	starvationCheck   int64 // 上次饥饿检查时间（纳秒）
	starvationTimeout int64 // 饥饿防护超时（纳秒），默认 100ms
	checkInterval     int64 // 检查间隔（纳秒），默认 10ms

	mu              sync.Mutex
	running         atomic.Bool
	wg              sync.WaitGroup
	ctx             context.Context
	cancel          context.CancelFunc
	stats           CoreStats
	cond            *sync.Cond
	totalQueueItems atomic.Int64
	batchBuffer     []any
}

// NewSchedulerCore 创建调度器核心
func NewSchedulerCore(coreID int) *SchedulerCore {
	ctx, cancel := context.WithCancel(context.Background())

	c := &SchedulerCore{
		coreID:            coreID,
		taskMap:           make(map[string]*ShardTask),
		running:           atomic.Bool{},
		ctx:               ctx,
		cancel:            cancel,
		cond:              sync.NewCond(&sync.Mutex{}),
		batchBuffer:       make([]any, 32),
		starvationTimeout: int64(100 * time.Millisecond),
		checkInterval:     int64(10 * time.Millisecond),
	}

	return c
}

// createTaskInstance 创建任务实例（P1 重构）
func (c *SchedulerCore) createTaskInstance(template *ShardTask) *ShardTask {
	task := &ShardTask{
		name:               template.name,
		priority:           template.priority,
		executionOrder:     template.executionOrder,
		queue:              NewMPSCExtQueue(),
		executeFunc:        template.executeFunc,
		totalQueueItemsPtr: &c.totalQueueItems,
	}
	task.taskStatus.Store(int32(TaskQueued))
	return task
}

// insertTaskOrdered 按优先级桶 + executionOrder 插入任务
// 参考 executor_percore.go taskQueue.Push 的分桶模式
func (c *SchedulerCore) insertTaskOrdered(task *ShardTask) {
	p := int(task.Priority())
	if p < 0 {
		p = 0
	}
	if p >= NumPriorityLevels {
		p = NumPriorityLevels - 1
	}

	bucket := c.priorityBuckets[p]

	// 桶内仍按 executionOrder 排序（保持同优先级内的确定性顺序）
	insertPos := sort.Search(len(bucket), func(i int) bool {
		return bucket[i].ExecutionOrder() >= task.ExecutionOrder()
	})

	bucket = append(bucket, nil)
	copy(bucket[insertPos+1:], bucket[insertPos:])
	bucket[insertPos] = task
	c.priorityBuckets[p] = bucket

	// bitmap 置位：标记该优先级桶有注册的 ShardTask
	// 注意：这是静态注册标记，运行时不修改
	c.activeBitmap |= (1 << p)
}

// RegisterTask 注册任务（创建独立的 Task 实例）
func (c *SchedulerCore) RegisterTask(taskTemplate *ShardTask) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 步骤 1: 验证
	if _, exists := c.taskMap[taskTemplate.Name()]; exists {
		return errors.TaskAlreadyRegistered(taskTemplate.Name())
	}

	// 步骤 2: 创建
	task := c.createTaskInstance(taskTemplate)

	// 步骤 3: 写入优先级桶 + bitmap 更新
	c.insertTaskOrdered(task)

	// 步骤 4: 更新 taskMap（普通 map，RegisterTask 仅启动时调用，并发安全）
	c.taskMap[task.Name()] = task

	return nil
}

// runLoop 调度循环（独立运行）
// P0 修复：wg.Add() 和 wg.Done() 已移至 Start() 方法中，确保时序正确
func (c *SchedulerCore) runLoop() {
	// 初始化桶缓存（假设 RegisterTask 只在启动时调用）
	c.cachedBuckets = c.getOrderedBuckets()

	for {
		// 检查上下文是否已取消（优先检查，避免竞态）
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.stats.TotalCycles.Add(1)

		// ========== 预检查：总队列为空则等待 ==========
		if c.totalQueueItems.Load() == 0 {
			if c.waitForSignal() {
				return // Context 已取消
			}
			continue
		}

		// ========== 饥饿防护：检查并提升超时的低优先级任务 ==========
		c.checkStarvation()

		// ========== Phase 1: 处理被提升的低优先级 ShardTask ==========
		for p := NumPriorityLevels - 1; p >= 1; p-- {
			for _, task := range c.cachedBuckets[p] {
				if task.HasPriorityBoost() {
					c.tryProcessBatch(task)
					task.SetPriorityBoost(false) // 一次性提升，用完恢复
				}
			}
		}

		// ========== Phase 2: bitmap O(1) 优先级遍历 ==========
		// 遍历所有有注册 ShardTask 的优先级桶（bitmap 是静态注册标记）
		// 参考 executor_percore.go:227-238 的 bits.TrailingZeros16 模式
		bitmap := c.activeBitmap
		for bitmap != 0 {
			// O(1) 找最高优先级非空桶
			p := bits.TrailingZeros16(bitmap)
			if p >= NumPriorityLevels {
				break
			}

			// 处理该优先级桶内所有 ShardTask
			for _, task := range c.cachedBuckets[p] {
				// 尝试批量处理
				if c.tryProcessBatch(task) {
					continue
				}

				// 回退到单个处理
				var item any
				if !task.Peek(&item) {
					continue // 队列空，跳过
				}

				status := c.executeTask(task, item)
				c.stats.TotalTasksProcessed.Add(1)

				switch status {
				case TaskPassed, TaskFailed:
					var dequeued any
					task.Dequeue(&dequeued)

				case TaskTimeout, TaskBusy, TaskRetrying:
					if shardItem, ok := item.(ShardItem); ok {
						if shardItem.IncAttempts() > shardItem.MaxRetries() {
							var dequeued any
							task.Dequeue(&dequeued)
						}
					} else {
						var dequeued any
						task.Dequeue(&dequeued)
					}
				}
			}

			// 清除局部变量位，继续下一个优先级
			bitmap &^= (1 << p)
		}
	}
}

// tryProcessBatch 尝试批量处理任务（新增）
//
// 策略："有多少 batch 多少"（偏向批量处理）
// - 队列有 N 个 → 批量 N 个（上限 batchSize）
// - 至少 2 个才批量（1 个不需要批量）
//
// 返回：
//   - true: 成功批量处理（>= 2 个 items）
//   - false: 队列不足 2 个或不支持批量
func (c *SchedulerCore) tryProcessBatch(task *ShardTask) bool {
	// 获取批量大小
	batchSize := c.getBatchSize(task)
	queueLen := task.QueueLen()

	// 策略：有多少 batch 多少
	actualBatchSize := min(batchSize, queueLen)

	// 至少 2 个才批量（1 个不需要批量）
	if actualBatchSize < 2 {
		return false
	}

	// P3 优化：使用预分配缓冲区，避免 make([]any, actualBatchSize) 分配
	// 切片复用底层缓冲区，仅前 actualBatchSize 个元素有效
	items := c.batchBuffer[:actualBatchSize]
	n := task.PeekN(items)

	if n < 2 {
		return false // 不足 2 个，回退到单个处理
	}

	// 截取实际获取的元素
	items = items[:n]

	// 批量执行并收集结果
	results := c.executeBatch(task, items)

	// P2 优化：使用 DequeueN 批量出队，减少锁竞争
	// 一次性移除所有元素，无需内存分配（N 次锁竞争 → 1 次锁竞争）
	task.DequeueN(len(items))

	// 处理执行结果
	c.handleBatchResults(task, items, results)

	return true // 批量处理完成
}

// executeTask 执行任务（带 panic 恢复）
func (c *SchedulerCore) executeTask(task *ShardTask, item any) TaskStatus {
	defer func() {
		if r := recover(); r != nil {
			c.stats.PanicCount.Add(1)
		}
	}()

	return task.Execute(item)
}

// ===== 批量处理支持 =====

// getBatchSize 获取批量大小（优先级：配置 > PreferredBatchSize > 默认）
func (c *SchedulerCore) getBatchSize(task *ShardTask) int {
	// 1. 优先从第一个 item 获取建议值（通过 Peek 查看）
	var firstItem any
	if task.Peek(&firstItem) {
		if batchItem, ok := firstItem.(BatchShardItem); ok {
			return batchItem.PreferredBatchSize()
		}
	}

	// 2. 默认批量大小（不实现 BatchShardItem 的任务使用）
	return 16
}

// TaskResult 任务执行结果
type TaskResult struct {
	Status TaskStatus // TaskPassed, TaskFailed, TaskRetry
	Err    error      // 错误信息（如果有）
	Value  any        // 返回值（如果有）
}

// executeBatch 批量执行任务，返回结果
func (c *SchedulerCore) executeBatch(task *ShardTask, items []any) []TaskResult {
	results := make([]TaskResult, len(items))

	for i, item := range items {
		// 执行任务
		status := c.executeTask(task, item)
		results[i] = TaskResult{Status: status}
		c.stats.TotalTasksProcessed.Add(1)
	}

	return results
}

// handleBatchResults 处理批量执行结果
func (c *SchedulerCore) handleBatchResults(_ *ShardTask, items []any, results []TaskResult) {
	for i, result := range results {
		item := items[i]

		switch result.Status {
		case TaskPassed:
			// 任务成功，已在 executeBatch 中通过 c.executeTask 记录

		case TaskFailed:
			// 任务失败，检查重试次数
			if shardItem, ok := item.(ShardItem); ok {
				attempts := shardItem.IncAttempts()
				if attempts > shardItem.MaxRetries() {
					// TODO: 超过最大重试次数，需要将任务移除队列
					// 注意：由于在批量处理中，这部分需要进一步细化
					_ = attempts // 避免未使用变量警告
				}
				// 否则保留在队列，下次循环会再次 Peek 到
			}

		case TaskTimeout, TaskBusy, TaskRetrying:
			// 需要重试
			if shardItem, ok := item.(ShardItem); ok {
				attempts := shardItem.IncAttempts()
				if attempts > shardItem.MaxRetries() {
					// TODO: 超过最大重试次数，需要将任务移除队列
					_ = attempts // 避免未使用变量警告
				}
				// 否则保留在队列，下次循环会再次 Peek 到
			}
		}
	}
}

// waitForSignal 等待新任务入队时的唤醒信号
// 返回值表示是否因为 context 取消而返回（true 表示应该退出）
func (c *SchedulerCore) waitForSignal() bool {
	c.stats.EmptyWaits.Add(1)
	c.cond.L.Lock()
	defer c.cond.L.Unlock()

	// 检查 context 是否已取消
	if c.ctx.Err() != nil {
		return true
	}

	// 等待 Signal 或 Broadcast
	c.cond.Wait()

	// 被唤醒后（无论是 Signal 还是虚假唤醒），返回让调用者检查队列
	// 如果此时 context 已被取消，调用者会检查并退出
	return c.ctx.Err() != nil
}

// wakeup 唤醒调度器（由 Enqueue 时调用）
func (c *SchedulerCore) wakeup() {
	c.cond.L.Lock()
	defer c.cond.L.Unlock()
	c.cond.Signal()
}

// getOrderedBuckets 返回所有桶的缓存副本（只读，供 runLoop 使用）
// runLoop 启动后调用一次，后续读 cachedBuckets 即可
func (c *SchedulerCore) getOrderedBuckets() [NumPriorityLevels][]*ShardTask {
	var result [NumPriorityLevels][]*ShardTask
	for i := 0; i < NumPriorityLevels; i++ {
		if len(c.priorityBuckets[i]) > 0 {
			bucket := make([]*ShardTask, len(c.priorityBuckets[i]))
			copy(bucket, c.priorityBuckets[i])
			result[i] = bucket
		}
	}
	return result
}

// checkStarvation 定期检查并提升超时的低优先级 ShardTask
// 参考 executor_percore.go:255-292 的 promoteStarvedTasks
func (c *SchedulerCore) checkStarvation() {
	if c.starvationTimeout <= 0 {
		return
	}

	now := time.Now().UnixNano()
	lastCheck := atomic.LoadInt64(&c.starvationCheck)

	// 每 10ms 检查一次（避免频繁扫描的开销）
	if now-lastCheck <= c.checkInterval {
		return
	}

	if !atomic.CompareAndSwapInt64(&c.starvationCheck, lastCheck, now) {
		return // 另一个 goroutine 已在检查
	}

	// 从低优先级到高优先级遍历（跳过最高优先级 0）
	for p := NumPriorityLevels - 1; p >= 1; p-- {
		for _, task := range c.cachedBuckets[p] {
			submitTime := task.LastSubmitTime()
			if submitTime > 0 && now-submitTime > c.starvationTimeout {
				task.SetPriorityBoost(true)
			}
		}
	}
}

// GetTaskByName 获取指定名称的任务
// taskMap: 只读（Start 后不再写入），并发只读安全，无需锁
func (c *SchedulerCore) GetTaskByName(name string) (*ShardTask, error) {
	task, exists := c.taskMap[name]
	if !exists {
		return nil, errors.TaskNotFound(name)
	}
	return task, nil
}

// ==========================================
// TaskScheduler V2（重构版）
// ==========================================

// SchedulerStats 统计信息（V2）
type SchedulerStats struct {
	TotalTasksEnqueued  atomic.Int64 // 总入队任务数
	TotalTasksProcessed atomic.Int64 // 总处理任务数
	CoreStats           []CoreStats  // 各 Core 统计（V2）
}

// TaskScheduler 多调度器管理器（V2：独立队列架构）
type TaskScheduler struct {
	cores     []*SchedulerCore
	coreCount int
	// executor 已移除：runLoop 直接在 goroutine + LockOSThread 中运行
	registeredOrders map[int]string // ExecutionOrder → TaskName
	mu               sync.RWMutex
	running          atomic.Bool
	stats            SchedulerStats

	// 负载均衡：RoundRobin 快速路径 + LoadBalance 慢路径
	rrCounter   atomic.Uint64 // RoundRobin 计数器（快速路径 O(1)）
	lbThreshold int64         // 负载阈值 = lbLowLoadPerCore × coreCount（低负载时用 RR）
}

// NewTaskScheduler 创建多调度器管理器（V2）
func NewTaskScheduler(name string, coreCount int) *TaskScheduler {
	if coreCount <= 0 {
		coreCount = runtime.NumCPU()
	}

	m := &TaskScheduler{
		cores:            make([]*SchedulerCore, coreCount),
		coreCount:        coreCount,
		registeredOrders: make(map[int]string),
		running:          atomic.Bool{},
		stats: SchedulerStats{
			CoreStats: make([]CoreStats, coreCount),
		},
	}
	// 负载均衡初始化：阈值 = 每核阈值 × 核数
	m.lbThreshold = int64(lbLowLoadPerCore * coreCount)

	// 创建 N 个调度器核心
	for i := 0; i < coreCount; i++ {
		m.cores[i] = NewSchedulerCore(i)
		m.stats.CoreStats[i] = CoreStats{CoreID: i}
	}

	return m
}

// RegisterTask 注册任务模板到所有核心
func (m *TaskScheduler) RegisterTask(executeFunc func(any) TaskStatus, name string, priority model.TaskPriority, executionOrder int) error {
	m.mu.Lock()
	// 检查 ExecutionOrder 冲突
	if existingTask, exists := m.registeredOrders[executionOrder]; exists {
		m.mu.Unlock()
		return errors.ExecutionOrderConflict(executionOrder, existingTask)
	}

	// 创建任务模板
	taskTemplate := &ShardTask{
		name:           name,
		priority:       priority,
		executionOrder: executionOrder,
		executeFunc:    executeFunc,
	}

	// 注册到所有核心（每个核心创建独立的 Task 实例）
	for _, core := range m.cores {
		if err := core.RegisterTask(taskTemplate); err != nil {
			m.mu.Unlock()
			return errors.CoreRegisterFailed(core.coreID, err)
		}
	}

	// 全部成功后才标记
	m.registeredOrders[executionOrder] = name
	m.mu.Unlock()

	return nil
}

// EnqueueWithShard 根据 ShardID 分发任务到对应核心
func (m *TaskScheduler) EnqueueWithShard(item ShardItem, taskName string) error {
	if !m.running.Load() {
		return errors.SchedulerNotStarted()
	}

	shardID := item.ShardID()
	var coreIndex int

	if shardID == 0 {
		// 无偏好：动态选择负载最小的 Core
		coreIndex = m.selectLeastLoadedCore()
	} else if shardID > 0 {
		// 固定路由：取模计算
		coreIndex = shardID % m.coreCount
	} else {
		// 负数：取绝对值后取模
		coreIndex = (-shardID) % m.coreCount
	}

	core := m.cores[coreIndex]
	m.stats.TotalTasksEnqueued.Add(1)

	// 使用 taskMap 按名称查找 ShardTask（替代原来的 tasksSnapshot[taskOrder]）
	// taskMap 是普通 map，RegisterTask 仅启动时写入，Start 后只读，并发只读安全
	task, exists := core.taskMap[taskName]
	if !exists {
		return errors.TaskNotFound(fmt.Sprintf("task %q not registered on core %d", taskName, coreIndex))
	}

	// 入队并唤醒该核心
	if err := task.Enqueue(item); err != nil {
		return err
	}

	core.wakeup()
	return nil
}

// selectLeastLoadedCore 选择负载最小的核心
// 策略：低负载 RoundRobin(O(1)) + 高负载精确选择(O(n))
// 低负载时 RoundRobin 已天然均衡，无需遍历所有 core
// 高负载时退化为精确的最小负载选择
func (m *TaskScheduler) selectLeastLoadedCore() int {
	// O(1) 全局负载估计：所有 core 的 totalQueueItems 之和
	totalLoad := int64(0)
	for _, core := range m.cores {
		totalLoad += core.totalQueueItems.Load()
	}

	// 快速路径：低负载时用 RoundRobin（无竞争、O(1)）
	if totalLoad < m.lbThreshold {
		return int(m.rrCounter.Add(1) % uint64(m.coreCount))
	}

	// 慢速路径：高负载时精确选择最小负载 core
	minQueueLen := int64(^uint64(0) >> 1)
	minIndex := 0

	for i, core := range m.cores {
		queueLen := core.totalQueueItems.Load()
		if queueLen < minQueueLen {
			minQueueLen = queueLen
			minIndex = i
		}
	}

	return minIndex
}

// Start 启动所有调度器核心
// executor 参数保留兼容（内部不使用），直接 goroutine + LockOSThread
func (m *TaskScheduler) Start() error {
	if !m.running.CompareAndSwap(false, true) {
		return errors.SchedulerAlreadyRunning()
	}

	for i, core := range m.cores {
		core.running.Store(true)
		core.wg.Add(1)

		go func(coreIdx int, c *SchedulerCore) {
			defer c.wg.Done()
			runtime.LockOSThread()
			_ = pinToCore(coreIdx)
			c.runLoop()
		}(i, core)
	}

	return nil
}

// Stop 停止所有调度器核心
func (m *TaskScheduler) Stop() {
	if !m.running.CompareAndSwap(true, false) {
		return
	}

	for _, core := range m.cores {
		core.cancel()
	}

	// 唤醒所有阻塞在 cond.Wait 的 runLoop
	for _, core := range m.cores {
		core.wakeup()
	}

	for _, core := range m.cores {
		core.wg.Wait()
	}
}

// GetStats 获取统计信息
func (m *TaskScheduler) GetStats() *SchedulerStats {
	// 更新核心统计
	for i, core := range m.cores {
		m.stats.CoreStats[i].TotalCycles.Store(core.stats.TotalCycles.Load())
		m.stats.CoreStats[i].TotalTasksProcessed.Store(core.stats.TotalTasksProcessed.Load())
		m.stats.CoreStats[i].EmptyWaits.Store(core.stats.EmptyWaits.Load())
		m.stats.CoreStats[i].PanicCount.Store(core.stats.PanicCount.Load())
		m.stats.CoreStats[i].QueueLen.Store(m.calculateCoreQueueLen(core))
	}

	return &m.stats
}

// SetLoadBalanceThreshold 设置负载均衡阈值（每核队列长度）
func (m *TaskScheduler) SetLoadBalanceThreshold(perCoreThreshold int) {
	if perCoreThreshold < 1 {
		perCoreThreshold = lbLowLoadPerCore
	}
	m.lbThreshold = int64(perCoreThreshold) * int64(m.coreCount)
}

// GetLoadBalanceThreshold 获取当前每核负载均衡阈值
func (m *TaskScheduler) GetLoadBalanceThreshold() int {
	return int(m.lbThreshold / int64(m.coreCount))
}

// calculateCoreQueueLen 计算核心的实际队列长度
func (m *TaskScheduler) calculateCoreQueueLen(core *SchedulerCore) int64 {
	queueLen := 0
	for p := 0; p < NumPriorityLevels; p++ {
		for _, task := range core.priorityBuckets[p] {
			queueLen += task.QueueLen()
		}
	}
	return int64(queueLen)
}

// HealthCheck 健康检查
func (m *TaskScheduler) HealthCheck() error {
	for i, core := range m.cores {
		if core.stats.PanicCount.Load() > 0 {
			return errors.CorePanicDetected(i)
		}
		queueLen := m.calculateCoreQueueLen(core)
		if queueLen > MaxQueueLengthHealthCheck {
			return errors.CoreQueueTooLong(i, queueLen)
		}
	}
	return nil
}
