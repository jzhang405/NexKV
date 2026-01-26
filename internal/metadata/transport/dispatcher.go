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

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// ========================================
// Dispatcher 配置
// ========================================

// DispatcherConfig 分发器配置
type DispatcherConfig struct {
	// WorkerCount worker 协程数量（默认：CPU 核心数）
	WorkerCount int

	// QueueSize 消息队列大小（默认：10000）
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
}

// DefaultDispatcherConfig 返回默认配置
func DefaultDispatcherConfig() *DispatcherConfig {
	return &DispatcherConfig{
		WorkerCount:        8,     // 默认 8 个 worker
		QueueSize:          10000, // 队列大小 10000
		BatchSize:          32,    // 批量处理 32 条消息
		FlushInterval:      10,    // 10ms 刷新间隔
		EnableBackpressure: true,  // 默认启用背压机制
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
//
// 参数：
//   - config: 分发器配置（nil 时使用默认配置）
//   - handler: 消息处理器（必需）
//
// 返回：
//   - *Dispatcher: 分发器实例
//   - error: 配置无效时返回错误
func NewDispatcher(config *DispatcherConfig, handler Handler) (*Dispatcher, error) {
	if handler == nil {
		return nil, fmt.Errorf("handler is required")
	}

	// 使用默认配置
	if config == nil {
		config = DefaultDispatcherConfig()
	}

	// 验证配置
	if config.WorkerCount <= 0 {
		return nil, fmt.Errorf("invalid WorkerCount: %d", config.WorkerCount)
	}
	if config.QueueSize <= 0 {
		return nil, fmt.Errorf("invalid QueueSize: %d", config.QueueSize)
	}

	ctx, cancel := context.WithCancel(context.Background())

	d := &Dispatcher{
		config:       config,
		messageQueue: make(chan MsgFrame, config.QueueSize),
		handler:      handler,
		ctx:          ctx,
		cancel:       cancel,
		connections:  make(map[string]context.CancelFunc),
	}

	// 创建 worker
	d.workers = make([]*worker, config.WorkerCount)
	for i := 0; i < config.WorkerCount; i++ {
		d.workers[i] = newWorker(i, d)
	}

	return d, nil
}

// ========================================
// Dispatcher 生命周期
// ========================================

// Start 启动分发器
//
// 启动所有 worker 协程，开始处理消息
func (d *Dispatcher) Start() error {
	if !d.running.CompareAndSwap(false, true) {
		return fmt.Errorf("dispatcher already running")
	}

	logging.Infof("[Dispatcher] Starting dispatcher with %d workers", d.config.WorkerCount)

	// 启动所有 worker
	for _, w := range d.workers {
		d.wg.Add(1)
		go w.run()
	}

	return nil
}

// Stop 停止分发器
//
// 优雅关闭：
//  1. 取消 context（唤醒所有阻塞的 worker）
//  2. 停止接收新消息（关闭队列）
//  3. 等待队列中消息处理完成
//  4. 关闭所有 worker
func (d *Dispatcher) Stop() error {
	if !d.running.CompareAndSwap(true, false) {
		return fmt.Errorf("dispatcher not running")
	}

	logging.Infof("[Dispatcher] Stopping dispatcher (processed: %d, dropped: %d)",
		d.msgCount.Load(), d.dropCount.Load())

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

	// 停止接收新消息（关闭队列）
	close(d.messageQueue)

	// 等待所有 worker 完成
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
//   - EnableBackpressure=true: 队列满时阻塞发送者，保证消息不丢失
//   - EnableBackpressure=false: 队列满时丢弃消息，调用回调
func (d *Dispatcher) forwardMessages(ctx context.Context, addr string, msgChan <-chan MsgFrame) {
	// 退出时清理连接映射，允许重新注册
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

			// 根据配置选择发送策略
			if d.config.EnableBackpressure {
				// 背压模式：阻塞发送，保证消息不丢失
				d.messageQueue <- msg
				d.msgCount.Add(1)
			} else {
				// 非背压模式：尝试发送，失败时调用回调
				select {
				case d.messageQueue <- msg:
					d.msgCount.Add(1)
				default:
					// 队列满，处理丢弃
					d.dropCount.Add(1)

					// 调用回调函数（如果配置了）
					if d.config.OnDroppedMessage != nil {
						retry := d.config.OnDroppedMessage(addr, msg)
						if retry {
							// 重试：阻塞发送
							d.messageQueue <- msg
							d.msgCount.Add(1)
						} else {
							// 放弃
							logging.Warnf("[Dispatcher] Message queue full, dropping message from %s", addr)
						}
					} else {
						// 没有配置回调，静默丢弃
						logging.Warnf("[Dispatcher] Message queue full, dropping message from %s (no callback configured)", addr)
					}
				}
			}
		}
	}
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
