// Package concurrency 提供并发控制和任务调度机制
package concurrency

import (
	"context"
	"fmt"
	"maps"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// RingQueue 环形缓冲区（避免 Dequeue 的内存拷贝）
// ==========================================

// RingQueue 环形队列，实现 O(1) 的入队和出队操作
type RingQueue struct {
	data []any // 底层数组
	head int   // 读指针
	tail int   // 写指针
	size int   // 当前元素数
	cap  int   // 容量（避免重复调用 len）
}

// NewRingQueue 创建环形队列
func NewRingQueue(initialCapacity int) *RingQueue {
	return &RingQueue{
		data: make([]any, initialCapacity),
		cap:  initialCapacity,
	}
}

// Enqueue 入队: O(1)
func (q *RingQueue) Enqueue(item any) error {
	// 扩容检查：如果满了，扩容为 2 倍
	if q.size >= q.cap {
		q.expand()
	}

	q.data[q.tail] = item
	q.tail = (q.tail + 1) % q.cap
	q.size++
	return nil
}

// Dequeue 出队: O(1) - 无需拷贝！
func (q *RingQueue) Dequeue(item *any) bool {
	if q.size == 0 {
		return false
	}

	*item = q.data[q.head]
	q.data[q.head] = nil // 帮助 GC
	q.head = (q.head + 1) % q.cap
	q.size--
	return true
}

// Peek 查看队首: O(1)
func (q *RingQueue) Peek(item *any) bool {
	if q.size == 0 {
		return false
	}

	*item = q.data[q.head]
	return true
}

// Len 队列长度: O(1)
func (q *RingQueue) Len() int {
	return q.size
}

// expand 扩容为当前容量的 2 倍
func (q *RingQueue) expand() {
	newCap := q.cap * 2
	if newCap == 0 {
		newCap = 64 // 最小容量
	}

	newData := make([]any, newCap)

	// 将环形数据展平到新数组
	if q.head < q.tail {
		// 数据未回绕，直接拷贝
		copy(newData, q.data[q.head:q.head+q.size])
	} else {
		// 数据回绕，分两段拷贝
		n := copy(newData, q.data[q.head:])
		copy(newData[n:], q.data[:q.tail])
	}

	q.data = newData
	q.head = 0
	q.tail = q.size
	q.cap = newCap
}

// ==========================================
// ShardTask 分片任务（独立队列）
// ==========================================

// ShardTask 分片任务（每个调度器独立实例）
type ShardTask struct {
	name           string
	priority       model.TaskPriority
	executionOrder int
	queue          *RingQueue // 环形缓冲区优化：避免 Dequeue 的内存拷贝
	mu             sync.Mutex
	taskStatus     atomic.Int32 // TaskStatus
	executeFunc    func(any) TaskStatus

	// 队列项计数指针（指向 SchedulerCore.totalQueueItems，用于 O(1) 总队列长度查询）
	totalQueueItemsPtr *atomic.Int64
}

// NewShardTask 创建分片任务
func NewShardTask(name string, priority model.TaskPriority, executionOrder int, executeFunc func(any) TaskStatus) *ShardTask {
	t := &ShardTask{
		name:           name,
		priority:       priority,
		executionOrder: executionOrder,
		queue:          NewRingQueue(DefaultShardTaskQueueCapacity),
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
	return t.queue.Len()
}

func (t *ShardTask) Enqueue(item any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	err := t.queue.Enqueue(item)
	if err == nil && t.totalQueueItemsPtr != nil {
		t.totalQueueItemsPtr.Add(1)
	}
	return err
}

func (t *ShardTask) Peek(item *any) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.queue.Peek(item) {
		return false
	}

	t.taskStatus.Store(int32(TaskExecuting))
	return true
}

func (t *ShardTask) Dequeue(item *any) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	ok := t.queue.Dequeue(item)
	if ok && t.totalQueueItemsPtr != nil {
		t.totalQueueItemsPtr.Add(-1)
	}
	return ok
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
	coreID        int
	tasks         []*ShardTask // 独立的 Task 实例
	tasksSnapshot atomic.Value // 不可变切片快照，类型为 []*ShardTask
	taskMap       atomic.Value // 不可变 map 快照，类型为 map[string]*ShardTask
	mu            sync.Mutex   // RegisterTask 时尚需锁
	running       atomic.Bool
	wg            sync.WaitGroup
	ctx           context.Context
	cancel        context.CancelFunc
	stats         CoreStats     //
	wakeupChan    chan struct{} // 唤醒通道（替代 cond）

	// runLoop 缓存：每次循环开始时更新，避免多次调用 getOrderedTasks()
	cachedTasks []*ShardTask

	// 总队列项计数器（避免遍历计算总队列长度）
	totalQueueItems atomic.Int64
}

// NewSchedulerCore 创建调度器核心
func NewSchedulerCore(coreID int) *SchedulerCore {
	ctx, cancel := context.WithCancel(context.Background())

	c := &SchedulerCore{
		coreID:     coreID,
		tasks:      make([]*ShardTask, 0, DefaultCoreTasksCapacity),
		running:    atomic.Bool{},
		ctx:        ctx,
		cancel:     cancel,
		wakeupChan: make(chan struct{}, 1),
	}

	// P2 建议4: 初始化空的 tasks 快照
	emptyTasks := make([]*ShardTask, 0)
	c.tasksSnapshot.Store(emptyTasks)

	// GetTaskByName 优化: 初始化空的 taskMap 快照
	emptyMap := make(map[string]*ShardTask)
	c.taskMap.Store(emptyMap)

	return c
}

// validateTaskRegistration 验证任务是否可以注册（P1 重构）
func (c *SchedulerCore) validateTaskRegistration(name string) error {
	currentMap := c.taskMap.Load().(map[string]*ShardTask)
	if _, exists := currentMap[name]; exists {
		return errors.TaskAlreadyRegistered(name)
	}
	return nil
}

// createTaskInstance 创建任务实例（P1 重构）
func (c *SchedulerCore) createTaskInstance(template *ShardTask) *ShardTask {
	task := &ShardTask{
		name:               template.name,
		priority:           template.priority,
		executionOrder:     template.executionOrder,
		queue:              NewRingQueue(64),
		executeFunc:        template.executeFunc,
		totalQueueItemsPtr: &c.totalQueueItems,
	}
	task.taskStatus.Store(int32(TaskQueued))
	return task
}

// insertTaskOrdered 按执行顺序插入任务（P1 重构）
func (c *SchedulerCore) insertTaskOrdered(task *ShardTask) {
	insertPos := sort.Search(len(c.tasks), func(i int) bool {
		return c.tasks[i].ExecutionOrder() >= task.ExecutionOrder()
	})
	c.tasks = append(c.tasks, nil)
	copy(c.tasks[insertPos+1:], c.tasks[insertPos:])
	c.tasks[insertPos] = task
}

// updateTaskSnapshots 更新任务快照（tasksSnapshot 和 taskMap）（P1 重构）
func (c *SchedulerCore) updateTaskSnapshots(task *ShardTask) {
	// 更新 tasksSnapshot (COW)
	tasksCopy := make([]*ShardTask, len(c.tasks))
	copy(tasksCopy, c.tasks)
	c.tasksSnapshot.Store(tasksCopy)

	// 更新 taskMap (COW)
	currentMap := c.taskMap.Load().(map[string]*ShardTask)
	newMap := make(map[string]*ShardTask, len(currentMap)+1)
	maps.Copy(newMap, currentMap)
	newMap[task.Name()] = task
	c.taskMap.Store(newMap)
}

// RegisterTask 注册任务（创建独立的 Task 实例）
// P1 重构：拆分为多个小方法，提高可读性和可维护性
func (c *SchedulerCore) RegisterTask(taskTemplate *ShardTask) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 步骤 1: 验证
	if err := c.validateTaskRegistration(taskTemplate.Name()); err != nil {
		return err
	}

	// 步骤 2: 创建
	task := c.createTaskInstance(taskTemplate)

	// 步骤 3: 有序插入
	c.insertTaskOrdered(task)

	// 步骤 4: 更新快照
	c.updateTaskSnapshots(task)

	return nil
}

// runLoop 调度循环（独立运行）
// P0 修复：wg.Add() 和 wg.Done() 已移至 Start() 方法中，确保时序正确
func (c *SchedulerCore) runLoop() {
	// 初始化任务缓存（假设 RegisterTask 只在启动时调用）
	c.cachedTasks = c.getOrderedTasks()

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
			c.waitForSignal()
			continue
		}

		// ========== 循环调度：每个 Task 最多处理一个 item ==========
		for _, task := range c.cachedTasks {
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
			case TaskPassed, TaskFailed:
				// 执行完成（成功或永久失败），出队
				var dequeued any
				task.Dequeue(&dequeued)

			case TaskTimeout, TaskBusy, TaskRetrying:
				// 需要重试的状态
				// 检查重试次数：超过最大重试次数则出队
				if shardItem, ok := item.(ShardItem); ok {
					if shardItem.IncAttempts() > shardItem.MaxRetries() {
						// 超过最大重试次数，出队
						var dequeued any
						task.Dequeue(&dequeued)
					}
					// 否则保留在队列，下次循环会再次 Peek 到
				} else {
					// 不是 ShardItem 类型，直接出队
					var dequeued any
					task.Dequeue(&dequeued)
				}
			}
		}
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

// getOrderedTasks 获取按 ExecutionOrder 排序的任务列表
// P0 优化：c.tasks 在 RegisterTask 时已保持有序，这里只需复制切片，无需排序
// P2 建议4: 使用 atomic.Value 加载不可变快照，完全无锁且避免复制
func (c *SchedulerCore) getOrderedTasks() []*ShardTask {
	// 原子加载快照，无需锁，无需复制
	return c.tasksSnapshot.Load().([]*ShardTask)
}

// GetTaskByName 获取指定名称的任务
// GetTaskByName 优化: 使用 atomic.Value 加载不可变 map 快照，完全无锁
// 前提: RegisterTask 只在启动时调用，runLoop 启动后 taskMap 不再修改
func (c *SchedulerCore) GetTaskByName(name string) (*ShardTask, error) {
	// 原子加载 map 快照，无需锁
	taskMap := c.taskMap.Load().(map[string]*ShardTask)

	task, exists := taskMap[name]
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
	running          atomic.Bool
	stats            SchedulerStats

	// 负载均衡缓存（避免每次都计算）
	loadBalanceCacheInterval  int          // 缓存间隔（多少次 Enqueue 重新计算一次，<=0 表示使用动态策略）
	loadBalanceIntervalManual bool         // 是否手动设置缓存间隔（true=使用手动值，false=使用动态策略）
	cachedLeastLoadedCore     int          // 当前缓存的最少负载 core
	loadBalanceCounter        atomic.Int64 // 计数器
	loadBalanceMu             sync.RWMutex // 保护缓存字段
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
		loadBalanceCacheInterval:  100,   // 默认值（动态策略下不使用）
		loadBalanceIntervalManual: false, // P2 优化：默认使用动态策略
		cachedLeastLoadedCore:     0,     // 初始 core 0
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

// getMaxQueueLen 获取当前最大队列长度（P2 优化）
func (m *TaskScheduler) getMaxQueueLen() int64 {
	maxLen := int64(0)
	for _, core := range m.cores {
		if queueLen := core.totalQueueItems.Load(); queueLen > maxLen {
			maxLen = queueLen
		}
	}
	return maxLen
}

// calculateDynamicInterval 根据队列长度动态计算缓存间隔（P2 优化）
// 高负载（队列 >= 1000）：快速响应（10 次）
// 中等负载（队列 >= 100）：正常响应（100 次）
// 低负载（队列 < 100）：降低开销（200 次）
func (m *TaskScheduler) calculateDynamicInterval(maxQueueLen int64) int {
	switch {
	case maxQueueLen >= 1000:
		return 10 // 高负载：快速响应
	case maxQueueLen >= 100:
		return 100 // 中等负载：正常响应
	default:
		return 200 // 低负载：降低开销
	}
}

// selectLeastLoadedCore 选择队列长度最小的核心（带动态缓存优化，P2 优化）
func (m *TaskScheduler) selectLeastLoadedCore() int {
	// 确定使用的缓存间隔
	var interval int
	m.loadBalanceMu.RLock()
	if m.loadBalanceIntervalManual {
		// 手动设置的间隔（用于测试和特殊场景）
		interval = m.loadBalanceCacheInterval
	} else {
		// P2 优化：动态计算缓存间隔
		maxQueueLen := m.getMaxQueueLen()
		interval = m.calculateDynamicInterval(maxQueueLen)
	}
	m.loadBalanceMu.RUnlock()

	// 快速路径：使用缓存
	counter := m.loadBalanceCounter.Add(1)
	if counter%int64(interval) != 0 {
		// 未达到缓存间隔，使用缓存值
		m.loadBalanceMu.RLock()
		cached := m.cachedLeastLoadedCore
		m.loadBalanceMu.RUnlock()
		return cached
	}

	// 慢速路径：重新计算最少负载 core
	minQueueLen := int64(^uint64(0) >> 1)
	minIndex := 0

	for i, core := range m.cores {
		// P0 优化：直接读取原子计数器，O(1) 查询
		queueLen := core.totalQueueItems.Load()

		if queueLen < minQueueLen {
			minQueueLen = queueLen
			minIndex = i
		}
	}

	// 更新缓存
	m.loadBalanceMu.Lock()
	m.cachedLeastLoadedCore = minIndex
	m.loadBalanceMu.Unlock()

	return minIndex
}

// Start 启动所有调度器核心
func (m *TaskScheduler) Start(executor service.TaskExecutor) error {
	// 使用 CompareAndSwap 确保原子性：如果已启动则返回错误
	if !m.running.CompareAndSwap(false, true) {
		return errors.SchedulerAlreadyRunning()
	}

	m.executor = executor

	// 提交所有核心到 Executor
	for i, core := range m.cores {
		core.running.Store(true)
		// P0 修复：在 Submit 之前调用 wg.Add()，确保时序正确
		core.wg.Add(1)

		sourceID := model.MustParseSourceID(fmt.Sprintf("multi-scheduler-v2:%d:runloop", i))

		err := executor.Submit(
			context.Background(),
			sourceID,
			model.TaskPriorityHigh,
			func(ctx context.Context) {
				// P0 修复：在 defer 中调用 Done()，确保一定被调用
				defer core.wg.Done()
				core.runLoop()
			},
		)

		if err != nil {
			// 提交失败，回滚已设置的状态
			core.wg.Done() // 回滚 Add()
			for j := 0; j < i; j++ {
				m.cores[j].running.Store(false)
				m.cores[j].wg.Done() // 回滚之前的 Add()
			}
			m.running.Store(false)
			return errors.CoreStartFailed(i, err)
		}
	}

	// 所有核心成功启动，running 已在开始时通过 CompareAndSwap 设置为 true
	return nil
}

// Stop 停止所有调度器核心
func (m *TaskScheduler) Stop() {
	if !m.running.CompareAndSwap(true, false) {
		return
	}

	// 先取消每个核心的 context（触发 runLoop 中的 ctx.Done() 检查）
	for _, core := range m.cores {
		core.cancel()
	}

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

// SetLoadBalanceCacheInterval 设置负载均衡缓存间隔
// interval: 每 interval 次 EnqueueWithShard(shardID=0) 重新计算一次最少负载 core
//
//	设置为 1 表示每次都计算（禁用缓存）
//	设置为 100 表示每 100 次计算一次（推荐，平衡性能和准确性）
//	设置为更大值（如 200）可以进一步提升性能，但负载均衡准确性降低
//
// 注意：设置此值将覆盖 P2 动态缓存策略，改用手动间隔。传入 0 或负值可恢复动态策略。
func (m *TaskScheduler) SetLoadBalanceCacheInterval(interval int) {
	m.loadBalanceMu.Lock()
	defer m.loadBalanceMu.Unlock()

	if interval < 1 {
		// 恢复动态策略（P2 优化）
		m.loadBalanceIntervalManual = false
		m.loadBalanceCacheInterval = 100 // 默认值（实际上不会使用）
		return
	}

	// 手动设置间隔（覆盖动态策略）
	m.loadBalanceIntervalManual = true
	m.loadBalanceCacheInterval = interval
}

// GetLoadBalanceCacheInterval 获取当前负载均衡缓存间隔
func (m *TaskScheduler) GetLoadBalanceCacheInterval() int {
	m.loadBalanceMu.RLock()
	defer m.loadBalanceMu.RUnlock()
	return m.loadBalanceCacheInterval
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
		if queueLen > MaxQueueLengthHealthCheck {
			return errors.CoreQueueTooLong(i, queueLen)
		}
	}
	return nil
}
