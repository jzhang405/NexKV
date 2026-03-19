// Package concurrency 提供并发控制和任务调度机制
package concurrency

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// ShardTask 分片任务（独立队列）
// ==========================================

// ShardTask 分片任务（每个调度器独立实例）
type ShardTask struct {
	name           string
	priority       model.TaskPriority
	executionOrder int
	queue          []any
	mu             sync.Mutex
	taskStatus     atomic.Int32 // TaskStatus
	executeFunc    func(any) TaskStatus
}

// NewShardTask 创建分片任务
func NewShardTask(name string, priority model.TaskPriority, executionOrder int, executeFunc func(any) TaskStatus) *ShardTask {
	t := &ShardTask{
		name:           name,
		priority:       priority,
		executionOrder: executionOrder,
		queue:          make([]any, 0, 64),
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
	return len(t.queue)
}

func (t *ShardTask) Enqueue(item any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.queue = append(t.queue, item)
	t.taskStatus.Store(int32(TaskQueued))
	return nil
}

func (t *ShardTask) Peek(item *any) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.queue) == 0 {
		return false
	}

	*item = t.queue[0]
	t.taskStatus.Store(int32(TaskExecuting))
	return true
}

func (t *ShardTask) Dequeue(item *any) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.queue) == 0 {
		return false
	}

	*item = t.queue[0]

	// 避免内存泄漏：使用 copy 而不是切片
	if len(t.queue) > 1 {
		copy(t.queue, t.queue[1:])
	}
	t.queue = t.queue[:len(t.queue)-1]

	return true
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
	coreID     int
	tasks      []*ShardTask // 独立的 Task 实例
	taskMap    map[string]*ShardTask
	mu         sync.RWMutex
	running    atomic.Bool
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	stats      CoreStats //
	executor   service.TaskExecutor
	wakeupChan chan struct{} // 唤醒通道（替代 cond）
}

// NewSchedulerCore 创建调度器核心
func NewSchedulerCore(coreID int) *SchedulerCore {
	ctx, cancel := context.WithCancel(context.Background())

	return &SchedulerCore{
		coreID:     coreID,
		tasks:      make([]*ShardTask, 0, 8),
		taskMap:    make(map[string]*ShardTask),
		running:    atomic.Bool{},
		ctx:        ctx,
		cancel:     cancel,
		wakeupChan: make(chan struct{}, 1),
	}
}

// RegisterTask 注册任务（创建独立的 Task 实例）
func (c *SchedulerCore) RegisterTask(taskTemplate *ShardTask) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	name := taskTemplate.Name()
	if _, exists := c.taskMap[name]; exists {
		return errors.TaskAlreadyRegistered(name)
	}

	// 创建独立的 Task 实例（深拷贝）
	task := &ShardTask{
		name:           taskTemplate.name,
		priority:       taskTemplate.priority,
		executionOrder: taskTemplate.executionOrder,
		queue:          make([]any, 0, 64),
		executeFunc:    taskTemplate.executeFunc,
	}
	task.taskStatus.Store(int32(TaskQueued))

	c.tasks = append(c.tasks, task)
	c.taskMap[name] = task

	return nil
}

// runLoop 调度循环（独立运行）
func (c *SchedulerCore) runLoop() {
	c.wg.Add(1)
	defer c.wg.Done()

	for c.running.Load() {
		// 检查上下文是否已取消
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.stats.TotalCycles.Add(1)

		// ========== 按 ExecutionOrder 排序任务 ==========
		tasks := c.getOrderedTasks()

		// ========== 预检查：计算总队列长度 ==========
		totalQueueLen := 0
		for _, task := range tasks {
			totalQueueLen += task.QueueLen()
		}

		// 如果所有队列都空，等待 wakeup
		if totalQueueLen == 0 {
			c.waitForSignal()
			continue
		}

		// ========== 循环调度：每个 Task 最多处理一个 item ==========
		for _, task := range tasks {
			// ========== 阶段 1: Peek 查看队首（不出队）==========
			var item any
			if !task.Peek(&item) {
				continue // 队列空，跳过
			}

			// ========== 阶段 2: Execute 执行（返回状态）==========
			status := c.executeTask(task, item)
			c.stats.TotalTasksProcessed.Add(1)

			// ========== 阶段 3: 根据返回状态决定是否 Dequeue ==========
			switch status {
			case TaskPassed, TaskFailed, TaskTimeout:
				// 执行完成，出队
				var dequeued any
				task.Dequeue(&dequeued)

			case TaskRetrying:
				// 需要重试，不出队
				// item 保留在队列中，下次循环会再次 Peek 到
			}
		}

		// 让出 CPU
		runtime.Gosched()
	}
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

// waitForSignal 等待新任务入队时的唤醒信号
func (c *SchedulerCore) waitForSignal() {
	c.stats.EmptyWaits.Add(1)
	select {
	case <-c.wakeupChan:
		// 被唤醒
	case <-c.ctx.Done():
		// 上下文取消
	}
}

// wakeup 唤醒调度器（由 Enqueue 时调用）
func (c *SchedulerCore) wakeup() {
	select {
	case c.wakeupChan <- struct{}{}:
		// 成功发送唤醒信号
	default:
		// 通道已有信号，无需重复发送
	}
}

// getOrderedTasks 按 ExecutionOrder 排序任务（从小到大）
func (c *SchedulerCore) getOrderedTasks() []*ShardTask {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 复制任务列表
	tasks := make([]*ShardTask, len(c.tasks))
	copy(tasks, c.tasks)

	// 按 ExecutionOrder 排序
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].ExecutionOrder() < tasks[j].ExecutionOrder()
	})

	return tasks
}

// GetTaskByName 获取指定名称的任务
func (c *SchedulerCore) GetTaskByName(name string) (*ShardTask, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

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
	cores            []*SchedulerCore
	coreCount        int
	executor         service.TaskExecutor
	registeredOrders map[int]string // ExecutionOrder → TaskName
	mu               sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	running          atomic.Bool
	stats            SchedulerStats //
}

// NewTaskScheduler 创建多调度器管理器（V2）
func NewTaskScheduler(name string, coreCount int) *TaskScheduler {
	if coreCount <= 0 {
		coreCount = runtime.NumCPU()
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &TaskScheduler{
		cores:            make([]*SchedulerCore, coreCount),
		coreCount:        coreCount,
		registeredOrders: make(map[int]string),
		ctx:              ctx,
		cancel:           cancel,
		running:          atomic.Bool{},
		stats: SchedulerStats{
			CoreStats: make([]CoreStats, coreCount),
		},
	}

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

	// 获取指定 Task 并入队
	task, err := core.GetTaskByName(taskName)
	if err != nil {
		return err
	}

	// 入队并唤醒该核心
	if err := task.Enqueue(item); err != nil {
		return err
	}

	core.wakeup()
	return nil
}

// selectLeastLoadedCore 选择队列长度最小的核心
func (m *TaskScheduler) selectLeastLoadedCore() int {
	minQueueLen := int64(^uint64(0) >> 1)
	minIndex := 0

	for i, core := range m.cores {
		// 计算实际队列长度
		queueLen := int64(0)
		tasks := core.getOrderedTasks()
		for _, task := range tasks {
			queueLen += int64(task.QueueLen())
		}

		if queueLen < minQueueLen {
			minQueueLen = queueLen
			minIndex = i
		}
	}

	return minIndex
}

// Start 启动所有调度器核心
func (m *TaskScheduler) Start(executor service.TaskExecutor) error {
	m.executor = executor

	for i, core := range m.cores {
		sourceID := model.MustParseSourceID(fmt.Sprintf("multi-scheduler-v2:%d:runloop", i))

		// 提交到 Executor
		err := executor.Submit(
			context.Background(),
			sourceID,
			model.TaskPriorityHigh,
			func(ctx context.Context) {
				defer func() {
					if r := recover(); r != nil {
						// panic 恢复
					}
				}()
				core.runLoop()
			},
		)

		if err != nil {
			return errors.CoreStartFailed(i, err)
		}
	}

	m.running.Store(true)
	return nil
}

// Stop 停止所有调度器核心
func (m *TaskScheduler) Stop() {
	if !m.running.CompareAndSwap(true, false) {
		return
	}

	m.cancel()

	// 唤醒所有核心
	for _, core := range m.cores {
		core.wakeup()
	}

	// 等待所有核心退出
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

// calculateCoreQueueLen 计算核心的实际队列长度
func (m *TaskScheduler) calculateCoreQueueLen(core *SchedulerCore) int64 {
	tasks := core.getOrderedTasks()
	queueLen := 0
	for _, task := range tasks {
		queueLen += task.QueueLen()
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
		if queueLen > 10000 {
			return errors.CoreQueueTooLong(i, queueLen)
		}
	}
	return nil
}
