// Package concurrency 提供并发控制和任务调度机制
package concurrency

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ==========================================
// TaskStatus 调度队列视角的任务状态
// ==========================================

// TaskStatus 调度队列视角的任务状态
// 区别于 OperationStatus（BaseTask 的异步执行状态）
// TaskStatus 控制队列中元素的 Dequeue 时机
type TaskStatus int

const (
	// TaskQueued 任务已入队，等待执行
	TaskQueued TaskStatus = iota
	// TaskExecuting 正在执行（Peek 成功后）
	TaskExecuting
	// TaskPassed 执行成功，需要 Dequeue
	TaskPassed
	// TaskFailed 执行失败，需要 Dequeue
	TaskFailed
	// TaskRetrying 需要重试，保留在队列
	TaskRetrying
	// TaskTimeout 超时，需要 Dequeue
	TaskTimeout
)

// String 返回状态字符串
func (s TaskStatus) String() string {
	switch s {
	case TaskQueued:
		return "queued"
	case TaskExecuting:
		return "executing"
	case TaskPassed:
		return "passed"
	case TaskFailed:
		return "failed"
	case TaskRetrying:
		return "retrying"
	case TaskTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// ==========================================
// Task 调度器任务接口
// ==========================================

// Task 调度器任务接口（支持 Peek + Execute 两阶段执行）
type Task interface {
	// 队列管理
	QueueLen() int          // 获取队列长度
	Enqueue(item any) error // 客户端入队任务
	Peek(item *any) bool    // 查看队首元素（不出队）
	Dequeue(item *any) bool // 移除队首元素（出队）

	// 任务执行
	Execute(item any) TaskStatus // 执行任务处理逻辑，返回执行状态

	// 元数据
	Name() string                  // 任务名称
	Priority() model.TaskPriority  // 优先级（发给 Executor 的参数）
	ExecutionOrder() int           // 执行顺序（TaskScheduler 内部排序，从小到大）
	GetTask() *model.BaseTask[any] // 获取异步结果任务（复用现有）
}

// ==========================================
// TaskScheduler 调度器
// ==========================================

// internalTask 内部任务接口（包含未导出方法）
// 用于 TaskScheduler 内部设置调度器引用和执行顺序
type internalTask interface {
	Task
	setScheduler(s *TaskScheduler)
	setSchedulerWithOrder(s *TaskScheduler, executionOrder int)
}

// TaskScheduler 调度器（实现 model.TaskRunner 接口）
// 支持多任务按 ExecutionOrder 调度，无空转等待
type TaskScheduler struct {
	name     string
	tasks    []Task
	taskMap  map[string]Task
	mu       sync.RWMutex
	running  atomic.Bool // 使用原子操作保证并发安全
	cond     *sync.Cond  // 条件变量（无空转等待）
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	stats    TaskSchedulerStats
	executor service.TaskExecutor

	// 优化：缓存排序结果，避免每次循环都重新排序
	orderedTasks []Task
	tasksDirty   atomic.Bool // 标记是否需要重新排序
}

// TaskSchedulerStats 统计信息
type TaskSchedulerStats struct {
	TotalCycles    atomic.Int64             // 总循环次数
	TotalTasks     atomic.Int64             // 总处理任务数
	EmptyWaits     atomic.Int64             // 空等待次数
	PanicCount     atomic.Int64             // Panic 恢复次数
	LastPanicTime  atomic.Value             // 最后一次 panic 时间
	TaskExecutions map[string]*atomic.Int64 // 各任务执行次数
}

// NewTaskScheduler 创建任务调度器
func NewTaskScheduler(name string) *TaskScheduler {
	ctx, cancel := context.WithCancel(context.Background())

	ts := &TaskScheduler{
		name:    name,
		tasks:   make([]Task, 0, 8),
		taskMap: make(map[string]Task),
		running: atomic.Bool{}, // 初始化为 false
		cond:    sync.NewCond(&sync.Mutex{}),
		ctx:     ctx,
		cancel:  cancel,
		stats: TaskSchedulerStats{
			TaskExecutions: make(map[string]*atomic.Int64),
		},
	}

	return ts
}

// ==========================================
// TaskRunner 接口实现
// ==========================================

// Run 实现 model.TaskRunner 接口
func (s *TaskScheduler) Run(ctx context.Context, pipeline model.PipelineContext) {
	s.runLoop()
}

// Priority 实现 model.TaskRunner 接口
func (s *TaskScheduler) Priority() model.TaskPriority {
	return model.TaskPriorityNormal
}

// SourceID 实现 model.TaskRunner 接口
func (s *TaskScheduler) SourceID() model.SourceID {
	return model.SourceDefault
}

// ==========================================
// 调度器生命周期
// ==========================================

// Start 启动调度器（在 Executor 上运行）
func (s *TaskScheduler) Start(executor service.TaskExecutor) error {
	s.mu.Lock()
	if s.running.Load() {
		s.mu.Unlock()
		return fmt.Errorf("scheduler already running")
	}
	s.running.Store(true)
	s.executor = executor
	s.mu.Unlock()

	// 将调度器作为任务提交给 Executor
	return executor.Submit(
		context.Background(),
		model.SourceDefault,
		model.TaskPriorityNormal,
		func(ctx context.Context) {
			s.runLoop()
		},
	)
}

// Stop 停止调度器（优雅关闭，等待所有任务处理完毕）
func (s *TaskScheduler) Stop() {
	// 使用原子操作保证只停止一次
	if !s.running.CompareAndSwap(true, false) {
		return
	}

	// 取消上下文
	s.cancel()

	// 唤醒所有等待的 goroutine（需要持有 cond.L）
	s.cond.L.Lock()
	s.cond.Broadcast()
	s.cond.L.Unlock()

	// 等待调度器 goroutine 退出
	s.wg.Wait()
}

// ==========================================
// 核心调度循环
// ==========================================

// runLoop 调度循环（在 Executor goroutine 中运行）
func (s *TaskScheduler) runLoop() {
	s.wg.Add(1)
	defer func() {
		if r := recover(); r != nil {
			s.stats.PanicCount.Add(1)
			s.stats.LastPanicTime.Store(debug.Stack())
			// panic 后继续运行，保证调度器可用
		}
		s.wg.Done()
	}()

	for s.running.Load() {
		// 检查上下文是否已取消
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		s.stats.TotalCycles.Add(1)

		// ========== 按 ExecutionOrder 排序任务 ==========
		tasks := s.getOrderedTasks()

		// ========== 预检查：计算总队列长度 ==========
		totalQueueLen := 0
		for _, task := range tasks {
			totalQueueLen += task.QueueLen()
		}

		// 如果所有队列都空，等待 wakeup
		if totalQueueLen == 0 {
			s.waitForSignal()
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
			status := s.executeTask(task, item)
			s.stats.TotalTasks.Add(1)

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
	}
}

// executeTask 执行任务（带 panic 恢复）
func (s *TaskScheduler) executeTask(task Task, item any) TaskStatus {
	defer func() {
		if r := recover(); r != nil {
			s.stats.PanicCount.Add(1)
			s.stats.LastPanicTime.Store(debug.Stack())
		}
	}()

	// 更新任务执行统计
	s.incrementTaskExecution(task.Name())

	return task.Execute(item)
}

// ==========================================
// Wakeup 机制
// ==========================================

// waitForSignal 等待新任务入队时的唤醒信号
func (s *TaskScheduler) waitForSignal() {
	s.stats.EmptyWaits.Add(1)
	s.cond.L.Lock()
	s.cond.Wait()
	s.cond.L.Unlock()
}

// wakeup 唤醒调度器（由 Task.Enqueue 时调用）
func (s *TaskScheduler) wakeup() {
	s.cond.L.Lock()
	s.cond.Signal()
	s.cond.L.Unlock()
}

// ==========================================
// 任务管理
// ==========================================

// RegisterTask 注册任务
// RegisterTask 注册任务（需指定 ExecutionOrder）
func (s *TaskScheduler) RegisterTask(task Task, executionOrder int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 类型断言：确保 task 实现了内部接口
	internalTask, ok := task.(internalTask)
	if !ok {
		return fmt.Errorf("task %s must implement internalTask interface", task.Name())
	}

	// 检查 ExecutionOrder 是否重复
	for _, t := range s.tasks {
		if t.ExecutionOrder() == executionOrder {
			return fmt.Errorf("execution order %d already used by task %s", executionOrder, t.Name())
		}
	}

	name := task.Name()
	if _, exists := s.taskMap[name]; exists {
		return fmt.Errorf("task %s already registered", name)
	}

	s.tasks = append(s.tasks, task)
	s.taskMap[name] = task
	s.stats.TaskExecutions[name] = &atomic.Int64{}

	// 设置调度器引用和 ExecutionOrder
	internalTask.setSchedulerWithOrder(s, executionOrder)

	// 标记任务列表已变更，需要重新排序
	s.tasksDirty.Store(true)

	return nil
}

// UnregisterTask 注销任务
func (s *TaskScheduler) UnregisterTask(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.taskMap[name]; !exists {
		return fmt.Errorf("task %s not found", name)
	}

	// 从 slice 中移除
	newTasks := make([]Task, 0, len(s.tasks)-1)
	for _, t := range s.tasks {
		if t.Name() != name {
			newTasks = append(newTasks, t)
		}
	}
	s.tasks = newTasks

	delete(s.taskMap, name)
	delete(s.stats.TaskExecutions, name)

	// 标记任务列表已变更，需要重新排序
	s.tasksDirty.Store(true)

	return nil
}

// getOrderedTasks 按 ExecutionOrder 排序任务（从小到大）
// 优化：缓存排序结果，避免每次循环都重新排序
func (s *TaskScheduler) getOrderedTasks() []Task {
	// 快速路径：如果任务列表没有变化，直接返回缓存
	if !s.tasksDirty.Load() {
		return s.orderedTasks
	}

	// 慢速路径：需要重新排序
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 复制任务列表
	tasks := make([]Task, len(s.tasks))
	copy(tasks, s.tasks)

	// 按 ExecutionOrder 排序（使用标准库排序）
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].ExecutionOrder() < tasks[j].ExecutionOrder()
	})

	// 更新缓存（需要写锁）
	s.mu.RUnlock()
	s.mu.Lock()
	s.orderedTasks = tasks
	s.tasksDirty.Store(false)
	s.mu.Unlock()
	s.mu.RLock()

	return tasks
}

// ==========================================
// 统计信息
// ==========================================

// GetStats 获取统计信息
func (s *TaskScheduler) GetStats() TaskSchedulerStats {
	// 复制 TaskExecutions map
	taskExecutions := make(map[string]*atomic.Int64, len(s.stats.TaskExecutions))
	for name, counter := range s.stats.TaskExecutions {
		taskExecutions[name] = counter
	}

	return TaskSchedulerStats{
		TotalCycles:    atomic.Int64{}, // 创建新实例
		TotalTasks:     atomic.Int64{},
		EmptyWaits:     atomic.Int64{},
		PanicCount:     atomic.Int64{},
		LastPanicTime:  s.stats.LastPanicTime,
		TaskExecutions: taskExecutions,
	}
}

// incrementTaskExecution 增加任务执行计数
func (s *TaskScheduler) incrementTaskExecution(name string) {
	if counter, exists := s.stats.TaskExecutions[name]; exists {
		counter.Add(1)
	}
}

// ==========================================
// SchedulerBaseTask 调度器任务基类
// ==========================================

// SchedulerBaseTask 调度器任务基类（嵌入 model.BaseTask）
type SchedulerBaseTask struct {
	*model.BaseTask[any] // 嵌入现有 BaseTask，复用异步结果机制
	name                 string
	priority             model.TaskPriority // 发给 Executor 的参数
	executionOrder       int                // TaskScheduler 内部排序（从小到大）
	scheduler            *TaskScheduler
	queue                []any
	mu                   sync.Mutex
	taskStatus           atomic.Int32 // TaskStatus
}

// NewSchedulerBaseTask 创建调度器任务基类
func NewSchedulerBaseTask(name string, priority model.TaskPriority, executionOrder int) *SchedulerBaseTask {
	base := model.NewBaseTask(
		model.OpStorage,
		priority,
		model.SourceDefault,
		func(ctx context.Context, pipeline model.PipelineContext) (any, error) {
			return nil, nil // 默认实现
		},
	)

	t := &SchedulerBaseTask{
		BaseTask:       base,
		name:           name,
		priority:       priority,
		executionOrder: executionOrder,
		queue:          make([]any, 0, 64),
	}
	t.taskStatus.Store(int32(TaskQueued))

	return t
}

// ==========================================
// Task 接口实现
// ==========================================

// QueueLen 获取队列长度
func (t *SchedulerBaseTask) QueueLen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.queue)
}

// Enqueue 客户端入队任务
func (t *SchedulerBaseTask) Enqueue(item any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.queue = append(t.queue, item)
	t.taskStatus.Store(int32(TaskQueued))

	// 唤醒调度器（如果有新任务入队）
	if t.scheduler != nil {
		t.scheduler.wakeup()
	}

	return nil
}

// Peek 查看队首元素（不出队）
func (t *SchedulerBaseTask) Peek(item *any) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.queue) == 0 {
		return false
	}

	// 将队首元素存入 item，但不从队列移除
	*item = t.queue[0]
	t.taskStatus.Store(int32(TaskExecuting))
	return true
}

// Dequeue 移除队首元素（出队）
func (t *SchedulerBaseTask) Dequeue(item *any) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.queue) == 0 {
		return false
	}

	// 移除队首元素
	*item = t.queue[0]

	// 避免内存泄漏：使用 copy 而不是切片
	if len(t.queue) > 1 {
		copy(t.queue, t.queue[1:])
	}
	t.queue = t.queue[:len(t.queue)-1]

	return true
}

// Execute 执行任务处理逻辑（默认实现，返回 TaskPassed）
// 子类应该重写此方法
func (t *SchedulerBaseTask) Execute(item any) TaskStatus {
	return TaskPassed
}

// Name 返回任务名称
func (t *SchedulerBaseTask) Name() string {
	return t.name
}

// Priority 返回任务优先级（发给 Executor 的参数）
func (t *SchedulerBaseTask) Priority() model.TaskPriority {
	return t.priority
}

// ExecutionOrder 返回执行顺序（TaskScheduler 内部排序，从小到大）
func (t *SchedulerBaseTask) ExecutionOrder() int {
	return t.executionOrder
}

// GetTask 获取异步结果任务
func (t *SchedulerBaseTask) GetTask() *model.BaseTask[any] {
	return t.BaseTask
}

// setScheduler 设置调度器引用
func (t *SchedulerBaseTask) setScheduler(s *TaskScheduler) {
	t.scheduler = s
}

// setSchedulerWithOrder 设置调度器引用和 ExecutionOrder
func (t *SchedulerBaseTask) setSchedulerWithOrder(s *TaskScheduler, executionOrder int) {
	t.scheduler = s
	t.executionOrder = executionOrder
}

// GetTaskStatus 获取任务状态（调度队列视角）
func (t *SchedulerBaseTask) GetTaskStatus() TaskStatus {
	return TaskStatus(t.taskStatus.Load())
}
