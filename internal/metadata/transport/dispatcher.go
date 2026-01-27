// Package transport 消息分发器（Dispatcher）
//
// P0-1 性能优化：fan-in 模式替代"每连接一协程"
//
// 优化前：
//   - 每个 TCP 连接一个 goroutine 处理消息
//   - 100 个连接 = 100 个 goroutine
//   - 上下文切换开销大
//
// 优化后：
//   - 多个连接共享一组 worker goroutine
//   - 100 个连接 = 固定数量（如 8 个）worker
//   - 减少 goroutine 数量，降低上下文切换开销
package transport

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// ========================================
// Dispatcher 配置
// ========================================

// DispatcherConfig 分发器配置
type DispatcherConfig struct {
	// WorkerCount worker 协程数量（默认：CPU 核心数）
	WorkerCount int

	// QueueSize 消息队列大小（默认：50000，P0 修复）
	QueueSize int

	// BatchSize 批量处理大小（默认：32）
	BatchSize int

	// FlushInterval 批量刷新间隔（默认：10ms）
	FlushInterval int

	// EnableBackpressure 启用背压机制（默认：true）
	// true: 队列满时阻塞发送者，保证消息不丢失
	// false: 队列满时丢弃消息，调用 OnDroppedMessage 回调
	EnableBackpressure bool

	// OnDroppedMessage 消息丢弃回调（EnableBackpressure=false 时使用）
	// 参数：
	//   - addr: 消息来源地址
	//   - msg: 被丢弃的消息
	// 返回：
	//   - bool: true 表示重试，false 表示放弃
	OnDroppedMessage func(addr string, msg MsgFrame) bool

	// P0: 动态 Worker 扩缩容配置
	// MinWorkers 最小 worker 数量（默认：4）
	MinWorkers int
	// MaxWorkers 最大 worker 数量（默认：32）
	MaxWorkers int
	// ScaleUpThreshold 扩容阈值：队列使用率 > 此值时扩容（默认：0.7）
	ScaleUpThreshold float64
	// ScaleDownThreshold 缩容阈值：队列使用率 < 此值时缩容（默认：0.3）
	ScaleDownThreshold float64
}

// DefaultDispatcherConfig 返回默认配置
func DefaultDispatcherConfig() *DispatcherConfig {
	return &DispatcherConfig{
		WorkerCount:        8,     // 默认 8 个 worker
		QueueSize:          50000, // 队列大小 50000（P0 修复：增加队列容量）
		BatchSize:          32,    // 批量处理 32 条消息
		FlushInterval:      10,    // 10ms 刷新间隔
		EnableBackpressure: true,  // 默认启用背压机制

		// P0: 动态 Worker 扩缩容配置
		MinWorkers:         4,   // 最小 worker 数量
		MaxWorkers:         32,  // 最大 worker 数量（P0 修复：支持动态扩容）
		ScaleUpThreshold:   0.7, // 队列使用率 > 70% 时扩容
		ScaleDownThreshold: 0.3, // 队列使用率 < 30% 时缩容
	}
}

// ========================================
// Dispatcher 结构体
// ========================================

// Dispatcher 消息分发器（fan-in 模式）
//
// 核心设计：
//   - 多个连接的消息汇聚到单一 channel（fan-in）
//   - 固定数量的 worker 从 channel 读取并处理消息
//   - 减少 goroutine 数量，降低上下文切换开销
//
// 架构图：
//
//	┌─────────┐     ┌─────────┐     ┌─────────┐
//	│ TCP 连接1│     │ TCP 连接2│     │ TCP 连接3│
//	└────┬────┘     └────┬────┘     └────┬────┘
//	     │               │               │
//	     └───────────────┴───────────────┘
//	                     │
//	                [fan-in]
//	                     │
//	             ┌───────▼────────┐
//	             │  messageQueue  │ (channel)
//	             └───────┬────────┘
//	                     │
//	      ┌──────────────┼──────────────┐
//	      │              │              │
//	 ┌────▼───┐     ┌───▼────┐    ┌──▼─────┐
//	 │worker1 │     │worker2 │    │worker3 │ ... (固定数量)
//	 └────┬───┘     └───┬────┘    └──┬─────┘
//	      │              │              │
//	      └──────────────┴──────────────┘
//	                     │
//	                [handler]
//	                     │
//	            ┌────────▼────────┐
//	            │  业务处理逻辑    │
//	            └─────────────────┘
type Dispatcher struct {
	// 配置
	config *DispatcherConfig

	// 消息队列（fan-in 汇聚点）
	messageQueue chan MsgFrame

	// worker 协程组
	workers []*worker

	// 消息处理器
	handler Handler

	// 生命周期管理
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 状态统计（原子操作）
	running   atomic.Bool
	msgCount  atomic.Uint64
	dropCount atomic.Uint64

	// P0: 动态 Worker 扩缩容字段
	currentWorkers atomic.Uint64 // 当前 worker 数量（原子操作）
	scaleDone      chan struct{} // 监控 goroutine 停止信号
	workersMu      sync.RWMutex  // 保护 workers 切片的读写操作

	// 连接管理（用于 fan-in）
	mu          sync.RWMutex
	connections map[string]context.CancelFunc // addr -> cancel
}

// Handler 消息处理器接口
//
// 实现此接口来处理接收到的消息
type Handler interface {
	// HandleMessage 处理消息
	// MsgFrame 包含完整的网络帧信息（FixedHeader + VarExtHeader + Data）
	HandleMessage(ctx context.Context, msg MsgFrame) error
}

// HandlerFunc 函数式处理器（便捷实现）
type HandlerFunc func(ctx context.Context, msg MsgFrame) error

// HandleMessage 实现 Handler 接口
func (f HandlerFunc) HandleMessage(ctx context.Context, msg MsgFrame) error {
	return f(ctx, msg)
}

// ========================================
// Dispatcher 创建
// ========================================

// NewDispatcher 创建新的分发器
func NewDispatcher(config *DispatcherConfig, handler Handler) (*Dispatcher, error) {
	if handler == nil {
		return nil, fmt.Errorf("handler is required")
	}

	if config == nil {
		config = DefaultDispatcherConfig()
	}

	if err := validateDispatcherConfig(config); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	d := &Dispatcher{
		config:       config,
		messageQueue: make(chan MsgFrame, config.QueueSize),
		handler:      handler,
		ctx:          ctx,
		cancel:       cancel,
		connections:  make(map[string]context.CancelFunc),
		scaleDone:    make(chan struct{}),
	}

	d.currentWorkers.Store(uint64(config.WorkerCount))

	d.workers = make([]*worker, config.WorkerCount)
	for i := 0; i < config.WorkerCount; i++ {
		d.workers[i] = newWorker(i, d)
	}

	return d, nil
}

// validateDispatcherConfig 验证分发器配置
func validateDispatcherConfig(config *DispatcherConfig) error {
	if config.WorkerCount <= 0 {
		return fmt.Errorf("invalid WorkerCount: %d", config.WorkerCount)
	}
	if config.QueueSize <= 0 {
		return fmt.Errorf("invalid QueueSize: %d", config.QueueSize)
	}

	// 设置默认值
	if config.MinWorkers <= 0 {
		config.MinWorkers = 4
	}
	if config.MaxWorkers <= 0 {
		config.MaxWorkers = 32
	}
	if config.ScaleUpThreshold <= 0 || config.ScaleUpThreshold > 1 {
		config.ScaleUpThreshold = 0.7
	}
	if config.ScaleDownThreshold <= 0 || config.ScaleDownThreshold > 1 {
		config.ScaleDownThreshold = 0.3
	}

	// 验证配置合法性
	if config.MinWorkers > config.MaxWorkers {
		return fmt.Errorf("MinWorkers (%d) cannot be greater than MaxWorkers (%d)",
			config.MinWorkers, config.MaxWorkers)
	}
	if config.ScaleUpThreshold <= config.ScaleDownThreshold {
		return fmt.Errorf("ScaleUpThreshold (%.2f) must be greater than ScaleDownThreshold (%.2f)",
			config.ScaleUpThreshold, config.ScaleDownThreshold)
	}
	if config.WorkerCount < config.MinWorkers || config.WorkerCount > config.MaxWorkers {
		return fmt.Errorf("WorkerCount (%d) must be between MinWorkers (%d) and MaxWorkers (%d)",
			config.WorkerCount, config.MinWorkers, config.MaxWorkers)
	}

	return nil
}

// ========================================
// Dispatcher 生命周期
// ========================================

// Start 启动分发器
//
// 启动所有 worker 协程，开始处理消息
// P0: 同时启动动态 Worker 扩缩容监控
func (d *Dispatcher) Start() error {
	if !d.running.CompareAndSwap(false, true) {
		return fmt.Errorf("dispatcher already running")
	}

	logging.Infof("[Dispatcher] Starting dispatcher with %d workers (dynamic scaling: %d~%d)",
		d.config.WorkerCount, d.config.MinWorkers, d.config.MaxWorkers)

	// 启动所有 worker
	for _, w := range d.workers {
		d.wg.Add(1)
		go w.run()
	}

	// P0: 启动动态 Worker 扩缩容监控
	d.wg.Add(1)
	go d.monitorQueue()

	return nil
}

// Stop 停止分发器
//
// 优雅关闭：
//  1. 取消 context（唤醒所有阻塞的 worker）
//  2. 停止动态扩缩容监控
//  3. 停止接收新消息（关闭队列）
//  4. 等待队列中消息处理完成
//  5. 关闭所有 worker
func (d *Dispatcher) Stop() error {
	if !d.running.CompareAndSwap(true, false) {
		return fmt.Errorf("dispatcher not running")
	}

	logging.Infof("[Dispatcher] Stopping dispatcher (processed: %d, dropped: %d, workers: %d)",
		d.msgCount.Load(), d.dropCount.Load(), d.currentWorkers.Load())

	// 取消所有连接
	d.mu.Lock()
	for addr, cancel := range d.connections {
		cancel()                    // 取消上下文，停止 goroutine
		delete(d.connections, addr) // 直接删除连接映射
	}
	d.mu.Unlock()

	// === FIX: 先取消 context，唤醒所有阻塞在 select 中的 worker ===
	// 这样 worker 会被 ctx.Done() 唤醒，而不是一直等待 messageQueue
	d.cancel()

	// P0: 停止动态 Worker 扩缩容监控
	close(d.scaleDone)

	// 停止接收新消息（关闭队列）
	close(d.messageQueue)

	// 等待所有 worker 和监控 goroutine 完成
	d.wg.Wait()

	logging.Infof("[Dispatcher] Dispatcher stopped")
	return nil
}

// ========================================
// 连接管理（fan-in）
// ========================================

// RegisterConnection 注册连接（fan-in 输入源）
//
// 每个连接启动一个 goroutine，将消息发送到 messageQueue（fan-in）
// 所有连接共享一组 worker 处理消息
//
// 参数：
//   - addr: 连接地址（唯一标识）
//   - msgChan: 消息通道（从连接读取的消息）
//
// 返回：
//   - context.CancelFunc: 取消函数，用于注销连接
func (d *Dispatcher) RegisterConnection(addr string, msgChan <-chan MsgFrame) context.CancelFunc {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 检查连接是否已注册
	if _, ok := d.connections[addr]; ok {
		logging.Warnf("[Dispatcher] Connection already registered: %s", addr)
		return nil
	}

	// 创建连接上下文
	connCtx, cancel := context.WithCancel(d.ctx)

	// 启动消息转发 goroutine
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.forwardMessages(connCtx, addr, msgChan)
	}()

	// 记录连接
	d.connections[addr] = cancel

	logging.Debugf("[Dispatcher] Connection registered: %s", addr)

	// 返回取消函数
	// 注意：此函数仅取消上下文，不删除连接映射
	// 连接映射的删除由调用方负责，或由 Stop() 统一处理
	return func() {
		cancel() // 取消上下文，停止 forwardMessages goroutine
		// 注意：不在此处删除 d.connections[addr]，避免死锁
		logging.Debugf("[Dispatcher] Connection canceled: %s", addr)
	}
}

// forwardMessages 转发消息到队列（fan-in）
//
// P0-3 修复：添加背压机制
func (d *Dispatcher) forwardMessages(ctx context.Context, addr string, msgChan <-chan MsgFrame) {
	defer func() {
		d.mu.Lock()
		delete(d.connections, addr)
		d.mu.Unlock()
		logging.Debugf("[Dispatcher] Connection removed: %s", addr)
	}()

	for {
		select {
		case <-ctx.Done():
			logging.Debugf("[Dispatcher] Connection %s forwarder stopped", addr)
			return

		case msg, ok := <-msgChan:
			if !ok {
				logging.Debugf("[Dispatcher] Connection %s channel closed", addr)
				return
			}

			if d.config.EnableBackpressure {
				d.sendMessageWithBackpressure(msg)
			} else {
				d.sendMessageWithoutBackpressure(addr, msg)
			}
		}
	}
}

// sendMessageWithBackpressure 背压模式发送消息
func (d *Dispatcher) sendMessageWithBackpressure(msg MsgFrame) {
	d.messageQueue <- msg
	d.msgCount.Add(1)
}

// sendMessageWithoutBackpressure 非背压模式发送消息
func (d *Dispatcher) sendMessageWithoutBackpressure(addr string, msg MsgFrame) {
	select {
	case d.messageQueue <- msg:
		d.msgCount.Add(1)
	default:
		d.dropCount.Add(1)
		d.handleDroppedMessage(addr, msg)
	}
}

// handleDroppedMessage 处理丢弃的消息
func (d *Dispatcher) handleDroppedMessage(addr string, msg MsgFrame) {
	if d.config.OnDroppedMessage != nil {
		if d.config.OnDroppedMessage(addr, msg) {
			d.messageQueue <- msg
			d.msgCount.Add(1)
			return
		}
	}

	logging.Warnf("[Dispatcher] Message queue full, dropping message from %s", addr)
}

// ========================================
// 统计信息
// ========================================

// Stats 分发器统计信息
type Stats struct {
	Running     bool   // 是否运行中
	MsgCount    uint64 // 已处理消息数
	DropCount   uint64 // 已丢弃消息数
	WorkerCount int    // worker 数量
	QueueSize   int    // 队列大小
	QueuedMsgs  int    // 队列中待处理消息数
}

// GetStats 获取统计信息
func (d *Dispatcher) GetStats() Stats {
	return Stats{
		Running:     d.running.Load(),
		MsgCount:    d.msgCount.Load(),
		DropCount:   d.dropCount.Load(),
		WorkerCount: d.config.WorkerCount,
		QueueSize:   d.config.QueueSize,
		QueuedMsgs:  len(d.messageQueue),
	}
}

// ========================================
// Worker 结构体
// ========================================

// worker 消息处理工作协程
type worker struct {
	id         int         // worker ID
	dispatcher *Dispatcher // 所属分发器
}

// newWorker 创建新的 worker
func newWorker(id int, dispatcher *Dispatcher) *worker {
	return &worker{
		id:         id,
		dispatcher: dispatcher,
	}
}

// run 运行 worker（主循环）
func (w *worker) run() {
	defer w.dispatcher.wg.Done()

	logging.Debugf("[Worker-%d] Started", w.id)

	for {
		select {
		case <-w.dispatcher.ctx.Done():
			logging.Debugf("[Worker-%d] Stopped", w.id)
			return

		case msg, ok := <-w.dispatcher.messageQueue:
			if !ok {
				// 队列关闭，退出
				logging.Debugf("[Worker-%d] Message queue closed", w.id)
				return
			}

			// 处理消息
			if err := w.dispatcher.handler.HandleMessage(w.dispatcher.ctx, msg); err != nil {
				logging.Errorf("[Worker-%d] Failed to handle message: %v", w.id, err)
				// TODO: 可以根据错误类型决定是否重试
			}
		}
	}
}

// ========================================
// P0: 动态 Worker 扩缩容机制
// ========================================

// monitorQueue 监控队列使用率并动态调整 worker 数量
//
// 工作原理：
//  1. 每秒检查一次队列使用率
//  2. 队列使用率 > ScaleUpThreshold (70%)：触发扩容
//  3. 队列使用率 < ScaleDownThreshold (30%)：触发缩容
//  4. Worker 数量限制在 [MinWorkers, MaxWorkers] 范围内
func (d *Dispatcher) monitorQueue() {
	defer d.wg.Done()

	logging.Infof("[Dispatcher-ScaleMonitor] Started (min=%d, max=%d, up=%.2f, down=%.2f)",
		d.config.MinWorkers, d.config.MaxWorkers,
		d.config.ScaleUpThreshold, d.config.ScaleDownThreshold)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			logging.Infof("[Dispatcher-ScaleMonitor] Stopped")
			return

		case <-d.scaleDone:
			logging.Infof("[Dispatcher-ScaleMonitor] Received stop signal")
			return

		case <-ticker.C:
			d.adjustWorkerCount()
		}
	}
}

// adjustWorkerCount 根据队列使用率调整 worker 数量
func (d *Dispatcher) adjustWorkerCount() {
	queueLen := len(d.messageQueue)
	queueCap := cap(d.messageQueue)
	utilization := float64(queueLen) / float64(queueCap)

	current := d.currentWorkers.Load()
	minWorkers := uint64(d.config.MinWorkers)
	maxWorkers := uint64(d.config.MaxWorkers)

	if d.shouldScaleUp(utilization, current, maxWorkers) {
		target := d.calculateScaleUpTarget(current, maxWorkers)
		logging.Infof("[Dispatcher-ScaleMonitor] Queue utilization %.2f%% (%d/%d), scaling up: %d -> %d",
			utilization*100, queueLen, queueCap, current, target)
		d.scaleUp(int(target))
		return
	}

	if d.shouldScaleDown(utilization, current, minWorkers) {
		target := d.calculateScaleDownTarget(current, minWorkers)
		logging.Infof("[Dispatcher-ScaleMonitor] Queue utilization %.2f%% (%d/%d), scaling down: %d -> %d",
			utilization*100, queueLen, queueCap, current, target)
		d.scaleDown(int(target))
		return
	}

	d.logStableState(utilization, queueLen, queueCap, current, minWorkers, maxWorkers)
}

// shouldScaleUp 判断是否需要扩容
func (d *Dispatcher) shouldScaleUp(utilization float64, current, maxWorkers uint64) bool {
	return utilization > d.config.ScaleUpThreshold && current < maxWorkers
}

// shouldScaleDown 判断是否需要缩容
func (d *Dispatcher) shouldScaleDown(utilization float64, current, minWorkers uint64) bool {
	return utilization < d.config.ScaleDownThreshold && current > minWorkers
}

// calculateScaleUpTarget 计算扩容目标数量
func (d *Dispatcher) calculateScaleUpTarget(current, maxWorkers uint64) uint64 {
	target := current + (current / 2)
	if target > maxWorkers {
		target = maxWorkers
	}
	return target
}

// calculateScaleDownTarget 计算缩容目标数量
func (d *Dispatcher) calculateScaleDownTarget(current, minWorkers uint64) uint64 {
	target := current - (current / 4)
	if target < minWorkers {
		target = minWorkers
	}
	return target
}

// logStableState 记录稳定状态
func (d *Dispatcher) logStableState(utilization float64, queueLen, queueCap int, current, minWorkers, maxWorkers uint64) {
	if current != minWorkers && current != maxWorkers {
		logging.Debugf("[Dispatcher-ScaleMonitor] Queue utilization %.2f%% (%d/%d), workers: %d (stable)",
			utilization*100, queueLen, queueCap, current)
	}
}

// scaleUp 扩容 worker 数量
func (d *Dispatcher) scaleUp(target int) {
	d.workersMu.Lock()
	defer d.workersMu.Unlock()

	current := len(d.workers)

	if target <= current {
		return
	}

	if target > d.config.MaxWorkers {
		target = d.config.MaxWorkers
	}

	for i := current; i < target; i++ {
		w := newWorker(i, d)
		d.workers = append(d.workers, w)

		d.wg.Add(1)
		go w.run()

		d.currentWorkers.Add(1)
	}

	logging.Infof("[Dispatcher-ScaleUp] Scaled up: %d -> %d workers", current, len(d.workers))
}

// scaleDown 缩容 worker 数量
//
// P1-5 修复：从切片移除时减少计数，确保 wg 正确完成
func (d *Dispatcher) scaleDown(target int) {
	d.workersMu.Lock()
	defer d.workersMu.Unlock()

	current := len(d.workers)

	if target >= current {
		return
	}

	if target < d.config.MinWorkers {
		target = d.config.MinWorkers
	}

	toRemove := current - target
	d.workers = d.workers[:target]
	d.currentWorkers.Add(^uint64(toRemove) + 1)

	logging.Infof("[Dispatcher-ScaleDown] Scaled down: %d -> %d workers (removed %d)", current, len(d.workers), toRemove)
}

// GetQueueUtilization 获取队列使用率
//
// 返回：
//   - float64: 队列使用率（0.0 ~ 1.0）
func (d *Dispatcher) GetQueueUtilization() float64 {
	queueLen := len(d.messageQueue)
	queueCap := cap(d.messageQueue)
	return float64(queueLen) / float64(queueCap)
}

// GetCurrentWorkerCount 获取当前 worker 数量
//
// 返回：
//   - uint64: 当前 worker 数量
func (d *Dispatcher) GetCurrentWorkerCount() uint64 {
	return d.currentWorkers.Load()
}

// ========================================
// P0: 增强的统计信息
// ========================================

// ScalingStats 动态扩缩容统计信息
type ScalingStats struct {
	CurrentWorkers     uint64  // 当前 worker 数量
	MinWorkers         int     // 最小 worker 数量
	MaxWorkers         int     // 最大 worker 数量
	QueueUtilization   float64 // 队列使用率
	QueuedMessages     int     // 队列中待处理消息数
	QueueCapacity      int     // 队列容量
	ScaleUpThreshold   float64 // 扩容阈值
	ScaleDownThreshold float64 // 缩容阈值
}

// GetScalingStats 获取动态扩缩容统计信息
func (d *Dispatcher) GetScalingStats() ScalingStats {
	d.workersMu.RLock()
	defer d.workersMu.RUnlock()

	return ScalingStats{
		CurrentWorkers:     d.currentWorkers.Load(),
		MinWorkers:         d.config.MinWorkers,
		MaxWorkers:         d.config.MaxWorkers,
		QueueUtilization:   d.GetQueueUtilization(),
		QueuedMessages:     len(d.messageQueue),
		QueueCapacity:      cap(d.messageQueue),
		ScaleUpThreshold:   d.config.ScaleUpThreshold,
		ScaleDownThreshold: d.config.ScaleDownThreshold,
	}
}

// ========================================
// P0: 手动扩缩容接口（用于测试）
// ========================================

// ScaleUpTo 手动扩容到指定数量
//
// 参数：
//   - target: 目标 worker 数量
//
// 返回：
//   - error: 目标数量非法时返回错误
func (d *Dispatcher) ScaleUpTo(target int) error {
	if target < d.config.MinWorkers || target > d.config.MaxWorkers {
		return fmt.Errorf("target worker count %d out of range [%d, %d]",
			target, d.config.MinWorkers, d.config.MaxWorkers)
	}

	current := int(d.currentWorkers.Load())
	if target <= current {
		return fmt.Errorf("target %d not greater than current %d", target, current)
	}

	logging.Infof("[Dispatcher-ManualScale] Manual scale up requested: %d -> %d", current, target)
	d.scaleUp(target)
	return nil
}

// ScaleDownTo 手动缩容到指定数量
//
// 参数：
//   - target: 目标 worker 数量
//
// 返回：
//   - error: 目标数量非法时返回错误
func (d *Dispatcher) ScaleDownTo(target int) error {
	if target < d.config.MinWorkers || target > d.config.MaxWorkers {
		return fmt.Errorf("target worker count %d out of range [%d, %d]",
			target, d.config.MinWorkers, d.config.MaxWorkers)
	}

	current := int(d.currentWorkers.Load())
	if target >= current {
		return fmt.Errorf("target %d not less than current %d", target, current)
	}

	logging.Infof("[Dispatcher-ManualScale] Manual scale down requested: %d -> %d", current, target)
	d.scaleDown(target)
	return nil
}

// WaitForScaling 等待扩缩容完成
//
// 参数：
//   - target: 目标 worker 数量
//   - timeout: 超时时间
//
// 返回：
//   - error: 超时或取消时返回错误
func (d *Dispatcher) WaitForScaling(target int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for scaling to %d workers", target)
		}

		current := int(d.currentWorkers.Load())
		if current == target {
			return nil
		}

		select {
		case <-d.ctx.Done():
			return fmt.Errorf("dispatcher canceled while waiting for scaling")
		case <-time.After(100 * time.Millisecond):
			// 继续等待
		}
	}
}
