// Package service 定义领域服务接口
package service

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// Transport 传输层核心接口
type Transport interface {
	// Self 返回本地节点 ID
	Self() model.PeerID

	// Connect 连接到指定地址的节点
	Connect(ctx context.Context, addr string) (model.PeerID, error)

	// Disconnect 断开与指定节点的连接
	Disconnect(peer model.PeerID) error

	// ConnectedPeers 返回当前已连接的节点列表
	ConnectedPeers() []model.PeerID

	// IsConnected 检查是否与指定节点已连接
	IsConnected(peer model.PeerID) bool

	// OpenStream 打开到指定节点的流式连接
	OpenStream(ctx context.Context, peer model.PeerID, protocol string) (Stream, error)

	// AcceptStream 接受指定协议的入站流
	AcceptStream(protocol string) (Stream, error)

	// OpenChannel 打开到指定节点的双向通道
	OpenChannel(ctx context.Context, peer model.PeerID, protocol string) (Channel, error)

	// OpenAsyncChannel 打开到指定节点的异步双向通道
	OpenAsyncChannel(ctx context.Context, peer model.PeerID, protocol string) (AsyncChannel, error)

	// OpenAsyncStream 打开到指定节点的异步流
	OpenAsyncStream(ctx context.Context, peer model.PeerID, protocol string) (AsyncStream, error)

	// Close 关闭传输层
	Close() error
}

// Stream 流式通信接口
type Stream interface {
	// ID 返回流 ID
	ID() string

	// Protocol 返回协议名称
	Protocol() string

	// RemotePeer 返回远程节点 ID
	RemotePeer() model.PeerID

	// Read 读取数据
	Read(p []byte) (n int, err error)

	// Write 写入数据
	Write(p []byte) (n int, err error)

	// Close 关闭流
	Close() error

	// SetReadDeadline 设置读超时
	SetReadDeadline(t interface{ UnixNano() int64 }) error

	// SetWriteDeadline 设置写超时
	SetWriteDeadline(t interface{ UnixNano() int64 }) error

	// Reset 重置流（发送 RST）
	Reset() error
}

// Channel 双向通道接口
type Channel interface {
	// Send 发送消息
	Send(ctx context.Context, msg []byte) error

	// Recv 接收消息
	Recv(ctx context.Context) ([]byte, error)

	// Close 关闭通道
	Close() error
}

// MsgOrError 消息或错误（用于异步接收）
type MsgOrError struct {
	Msg []byte
	Err error
}

// AsyncChannel 异步双向通道接口（Go Channel 风格）
//
// 使用示例:
//
//	ch := transport.OpenAsyncChannel(ctx, peer, protocol)
//
//	// 发送消息
//	ch.SendChan() <- []byte("hello")
//
//	// 接收消息
//	select {
//	case msg := <-ch.RecvChan():
//	    if msg.Err != nil {
//	        // 处理错误
//	    }
//	    // 处理消息 msg.Msg
//	case <-ctx.Done():
//	    // 超时
//	}
//
//	// 关闭
//	ch.Close()
type AsyncChannel interface {
	// SendChan 返回发送通道
	// 向此 channel 写入消息会异步发送到对端
	// 注意：channel 关闭由 Close() 触发，用户无法主动关闭
	SendChan() chan<- []byte

	// RecvChan 返回接收通道
	// 从此 channel 读取 MsgOrError 可获取对端消息
	// channel 关闭时表示连接断开
	RecvChan() <-chan MsgOrError

	// Close 关闭通道
	// 会取消所有等待中的操作
	Close() error

	// WaitClosed 等待通道关闭
	// 返回发送过程中遇到的错误（如果有）
	// 注意：此方法会阻塞直到 Close() 被调用
	// 推荐使用 WaitClosedWithTimeout 避免永久阻塞
	WaitClosed() error

	// WaitClosedWithTimeout 带超时的等待通道关闭
	WaitClosedWithTimeout(timeout time.Duration) error
}

// ReadResult 异步读取结果
type ReadResult struct {
	Data []byte
	Err  error
}

// WriteRequest 异步写入请求
type WriteRequest struct {
	Data []byte
	Err  chan error // 写入完成后发送错误（nil 表示成功）
	// 注意：Err 可以为 nil（不等待结果）
	// 如果非 nil，推荐使用带缓冲的 channel: make(chan error, 1)
}

// AsyncStream 异步流接口（Go Channel 风格）
//
// 使用示例:
//
//	s := transport.OpenAsyncStream(ctx, peer, protocol)
//
//	// 写入数据（带确认）
//	errCh := make(chan error, 1) // 推荐：带缓冲
//	s.WriteChan() <- WriteRequest{Data: []byte("hello"), Err: errCh}
//	select {
//	case err := <-errCh:
//	    // 写入完成
//	case <-time.After(time.Second):
//	    // 超时
//	}
//
//	// 写入数据（不等待确认）
//	s.WriteChan() <- WriteRequest{Data: []byte("hello")}
//
//	// 读取数据
//	select {
//	case result := <-s.ReadChan():
//	    if result.Err != nil {
//	        // 处理错误
//	    }
//	    // 处理数据 result.Data
//	case <-ctx.Done():
//	}
//
//	// 关闭
//	s.Close()
type AsyncStream interface {
	// ReadChan 返回读取通道
	// 从此 channel 读取 ReadResult 可获取数据
	// channel 关闭时表示流结束（EOF）或错误
	ReadChan() <-chan ReadResult

	// WriteChan 返回写入通道
	// 向此 channel 写入 WriteRequest 会异步写入数据
	// 如果 WriteRequest.Err 非 nil，写入完成后会收到结果
	WriteChan() chan<- WriteRequest

	// Close 关闭流
	// 会取消所有等待中的操作
	Close() error

	// WaitClosed 等待流关闭
	// 返回写入过程中遇到的错误（如果有）
	// 注意：此方法会阻塞直到 Close() 被调用
	// 推荐使用 WaitClosedWithTimeout 避免永久阻塞
	WaitClosed() error

	// WaitClosedWithTimeout 带超时的等待流关闭
	WaitClosedWithTimeout(timeout time.Duration) error
}

// ============================================================================
// RPC 接口定义
// ============================================================================

// ResponseStrategy 广播响应策略
type ResponseStrategy int

const (
	// ResponseAll 等待所有节点响应（默认）
	// 适用场景：事务提交、配置变更（强一致性）
	ResponseAll ResponseStrategy = iota

	// ResponseMajority 等待多数派响应（> N/2）
	// 适用场景：3副本写入（W=2）、分片同步
	ResponseMajority

	// ResponseNone 不等待响应（单向发送）
	// 适用场景：日志广播、监控数据（高吞吐）
	ResponseNone
)

// BroadcastResult 广播结果（同消息广播）
type BroadcastResult struct {
	Responses    []model.Message // 成功响应（有序列表）
	SuccessPeers []model.PeerID  // 成功节点
	FailedPeers  []model.PeerID  // 失败/超时节点
}

// WriteVResult 批量写入结果（不同消息）
type WriteVResult struct {
	Responses    map[model.PeerID]model.Message // 成功响应（按节点映射）
	SuccessPeers []model.PeerID                 // 成功节点
	FailedPeers  []model.PeerID                 // 失败/超时节点
}

// ============================================================================
// BroadcastTracker Callback 机制（v1.4）
// ============================================================================

// BroadcastCallback 广播进度回调接口
//
// 回调执行顺序（针对每个响应）：
//  1. OnSuccess / OnFailure（每次响应）
//     ↓
//  2. OnMajorityReached（达到多数派时，仅一次）
//     ↓
//  3. OnFullDone（全部完成时，仅一次）
//
// 特殊场景：OnMajorityReached 和 OnFullDone 可能在同一次 RecordSuccess 中
// 顺序触发（如果 Majority 达成时恰好也是最后一个响应）
//
// 回调实现注意事项：
// 1. 回调在锁外执行，但应避免调用 BroadcastTracker 的方法（防止死锁）
// 2. 回调应快速返回（< 10ms），长时间处理应启动 goroutine
// 3. 回调可能被并发调用（OnSuccess/OnFailure），实现需线程安全
// 4. 不要依赖回调的调用顺序（除文档明确保证的之外）
type BroadcastCallback interface {
	// OnSuccess 每次收到成功响应时调用
	// 参数说明：
	//   - peer: 响应节点 ID
	//   - resp: 成功响应消息（不会为 nil）
	//   - stats: 当前统计信息
	OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats)

	// OnFailure 每次收到失败响应时调用
	// 参数说明：
	//   - peer: 失败节点 ID
	//   - err: 错误信息（不会为 nil，包含具体错误类型）
	//          - 超时错误：context.DeadlineExceeded
	//          - 网络错误：net.Error
	//          - 业务错误：业务逻辑返回的错误
	//   - stats: 当前统计信息
	OnFailure(peer model.PeerID, err error, stats BroadcastStats)

	// OnMajorityReached 达到多数派时调用（仅调用一次）
	// 触发条件：
	//   - 成功响应数 >= majority（len(targets)/2 + 1）
	//   - 只在 RecordSuccess 时检查，RecordFailure 不会触发
	//   - 例如：3 个节点，2 个成功即触发（即使 1 个失败）
	// 参数说明：
	//   - stats: 达到多数派时的统计信息
	OnMajorityReached(stats BroadcastStats)

	// OnFullDone 全部完成时调用（仅调用一次）
	// 触发条件：
	//   - 成功数 + 失败数 == 总节点数
	// 参数说明：
	//   - stats: 全部完成时的统计信息
	OnFullDone(stats BroadcastStats)
}

// BroadcastStats 广播统计信息
type BroadcastStats struct {
	TaskID             string         // 任务 ID
	Total              int            // 总节点数
	Success            int            // 成功数
	Failed             int            // 失败数
	Pending            int            // 待响应数
	SuccessRate        float64        // 成功率
	ElapsedTime        time.Duration  // 已耗时（从任务开始到现在）
	FirstResponseTime  time.Duration  // 首个响应耗时（从任务开始到首个响应）
	MajorityReachTime  time.Duration  // 达到多数派耗时（从任务开始到多数派达成）
}

// NoOpCallback 空实现的 BroadcastCallback
// 可用于嵌入到自定义 Callback 中，只重写需要的方法
//
// 使用示例:
//
//	type MyCallback struct {
//	    NoOpCallback // 嵌入所有空实现
//	}
//
//	// 只重写关心的方法
//	func (m *MyCallback) OnFullDone(stats BroadcastStats) {
//	    fmt.Printf("广播完成！成功率: %.2f%%\n", stats.SuccessRate*100)
//	}
type NoOpCallback struct{}

// OnSuccess 空实现
func (n NoOpCallback) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {}

// OnFailure 空实现
func (n NoOpCallback) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {}

// OnMajorityReached 空实现
func (n NoOpCallback) OnMajorityReached(stats BroadcastStats) {}

// OnFullDone 空实现
func (n NoOpCallback) OnFullDone(stats BroadcastStats) {}

// BroadcastTracker 可选的广播追踪器（一次性使用）
//
// 设计原则：
// 1. Tracker 是一次性的，不复用（避免 channel 泄漏）
// 2. Tracker 是**独立的监控工具**，与 RPC 调用的 ResponseStrategy **解耦**
// 3. 无论 RPC 使用什么策略（All/Majority/None），Tracker 都可以：
//   - WaitFull(): 等待所有节点响应
//   - WaitMajority(): 等待多数派响应
//   - Stats(): 实时查看进度
//
// 典型使用场景：
//
//	// 场景 1：RPC 用 ResponseMajority，tracker 后台监控 full completion
//	tracker := NewBroadcastTracker("task-001", replicas)
//	rpc.BroadcastCall(ctx, replicas, req, ResponseMajority, tracker)
//	// RPC 返回后，异步等待全部完成
//	go func() { tracker.WaitFull(ctx) }()
//
//	// 场景 2：RPC 用 ResponseNone，tracker 监控所有响应
//	tracker := NewBroadcastTracker("task-002", replicas)
//	rpc.BroadcastCall(ctx, replicas, req, ResponseNone, tracker)
//	// 后台等待全部或多数派完成
//	tracker.WaitMajority(ctx)
type BroadcastTracker struct {
	taskID       string                         // 任务 ID（用于日志）
	targets      []model.PeerID                 // 目标节点列表
	responses    map[model.PeerID]model.Message // 成功响应
	failures     map[model.PeerID]error         // 失败记录
	mu           sync.RWMutex                   // 保护并发访问
	fullDone     chan struct{}                  // 全部完成时关闭
	majorityDone chan struct{}                  // 多数派完成时关闭

	// ====== Callback 机制（v1.4）======
	callback                  BroadcastCallback // 回调接口（可选）
	callbacksEnabled          bool              // 回调启用/禁用开关
	majorityCallbackTriggered bool              // OnMajorityReached 是否已触发
	fullDoneCallbackTriggered bool              // OnFullDone 是否已触发
	firstResponseRecorded     bool              // 是否已记录首个响应
	startTime                 time.Time         // 任务开始时间
	firstResponseTime         time.Time         // 首个响应时间
	majorityReachTime         time.Time         // 达到多数派时间
}

// NewBroadcastTracker 创建广播追踪器
func NewBroadcastTracker(taskID string, targets []model.PeerID) *BroadcastTracker {
	// 保护性拷贝，防止外部修改
	targetsCopy := make([]model.PeerID, len(targets))
	copy(targetsCopy, targets)

	return &BroadcastTracker{
		taskID:       taskID,
		targets:      targetsCopy,
		responses:    make(map[model.PeerID]model.Message),
		failures:     make(map[model.PeerID]error),
		fullDone:     make(chan struct{}),
		majorityDone: make(chan struct{}),

		// Callback 机制初始化
		callbacksEnabled: true,  // 默认启用回调
		startTime:        time.Now(),
	}
}

// WaitFull 等待所有节点响应（包括失败的）
// 适用场景：集群关闭、全局同步
func (t *BroadcastTracker) WaitFull(ctx context.Context) error {
	select {
	case <-t.fullDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitMajority 等待多数派（> N/2）节点响应
// 适用场景：需要确认多数派完成的场景（如 3 副本写入确认 W=2）
// 注意：与 RPC 调用的 ResponseStrategy 无关，可独立使用
func (t *BroadcastTracker) WaitMajority(ctx context.Context) error {
	// 快速路径：先检查当前状态
	t.mu.RLock()
	majority := len(t.targets)/2 + 1
	if len(t.responses) >= majority || len(t.targets) == 0 {
		t.mu.RUnlock()
		return nil
	}
	t.mu.RUnlock()

	// 等待 majorityDone channel（零 CPU 开销）
	select {
	case <-t.majorityDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats 获取实时统计信息
func (t *BroadcastTracker) Stats() (success, failed, pending int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.responses), len(t.failures),
		len(t.targets) - len(t.responses) - len(t.failures)
}

// SetCallback 设置进度回调（必须在开始之前设置）
func (t *BroadcastTracker) SetCallback(cb BroadcastCallback) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callback = cb
}

// EnableCallbacks 启用/禁用回调（可选，便于测试）
func (t *BroadcastTracker) EnableCallbacks(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callbacksEnabled = enabled
}

// IsMajorityReached 检查是否已达到多数派
func (t *BroadcastTracker) IsMajorityReached() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	majority := len(t.targets)/2 + 1
	return len(t.responses) >= majority
}

// IsFullDone 检查是否全部完成
func (t *BroadcastTracker) IsFullDone() bool {
	select {
	case <-t.fullDone:
		return true
	default:
		return false
	}
}

// RecordSuccess 记录成功响应（由 RPC 实现调用）
// 线程安全，自动更新状态并关闭 channel
func (t *BroadcastTracker) RecordSuccess(peer model.PeerID, resp model.Message) {
	var callback BroadcastCallback
	var stats BroadcastStats
	var shouldTriggerMajority bool
	var shouldTriggerFullDone bool

	// === 锁内：只做状态更新 ===
	t.mu.Lock()

	// 1. 记录响应
	t.responses[peer] = resp

	// 2. 记录首个响应时间（仅一次）
	if !t.firstResponseRecorded {
		t.firstResponseTime = time.Now()
		t.firstResponseRecorded = true
	}

	// 3. 检查是否满足 Majority 策略
	majority := len(t.targets)/2 + 1
	if len(t.responses) >= majority {
		// 关闭 majorityDone channel（仅关闭一次）
		select {
		case <-t.majorityDone:
			// 已经关闭，跳过
		default:
			close(t.majorityDone)
		}

		// 触发 OnMajorityReached 回调（仅一次）
		if !t.majorityCallbackTriggered {
			t.majorityCallbackTriggered = true
			t.majorityReachTime = time.Now()
			shouldTriggerMajority = true
		}
	}

	// 4. 检查是否全部完成
	if len(t.responses)+len(t.failures) == len(t.targets) {
		// 关闭 fullDone channel
		select {
		case <-t.fullDone:
		default:
			close(t.fullDone)
		}

		// 触发 OnFullDone 回调（仅一次）
		if !t.fullDoneCallbackTriggered {
			t.fullDoneCallbackTriggered = true
			shouldTriggerFullDone = true
		}
	}

	// 5. 准备回调数据
	callback = t.callback
	stats = t.buildStatsLocked()
	t.mu.Unlock()
	// === 锁外：执行回调，避免死锁 ===

	// 6. 触发回调（如果启用）
	if callback == nil || !t.callbacksEnabled {
		return
	}

	// 触发 OnSuccess 回调
	safeCallback(func() {
		callback.OnSuccess(peer, resp, stats)
	})

	// 触发 OnMajorityReached 回调（仅一次）
	if shouldTriggerMajority {
		safeCallback(func() {
			callback.OnMajorityReached(stats)
		})
	}

	// 触发 OnFullDone 回调（仅一次）
	if shouldTriggerFullDone {
		safeCallback(func() {
			callback.OnFullDone(stats)
		})
	}
}

// RecordFailure 记录失败响应（由 RPC 实现调用）
// 线程安全，自动更新状态并关闭 channel
func (t *BroadcastTracker) RecordFailure(peer model.PeerID, err error) {
	var callback BroadcastCallback
	var stats BroadcastStats
	var shouldTriggerFullDone bool

	// === 锁内：只做状态更新 ===
	t.mu.Lock()

	// 1. 记录失败
	t.failures[peer] = err

	// 2. 检查是否全部完成
	if len(t.responses)+len(t.failures) == len(t.targets) {
		// 关闭 fullDone channel
		select {
		case <-t.fullDone:
		default:
			close(t.fullDone)
		}

		// 触发 OnFullDone 回调（仅一次）
		if !t.fullDoneCallbackTriggered {
			t.fullDoneCallbackTriggered = true
			shouldTriggerFullDone = true
		}
	}

	// 3. 准备回调数据
	callback = t.callback
	stats = t.buildStatsLocked()
	t.mu.Unlock()
	// === 锁外：执行回调，避免死锁 ===

	// 4. 触发回调（如果启用）
	if callback == nil || !t.callbacksEnabled {
		return
	}

	// 触发 OnFailure 回调
	safeCallback(func() {
		callback.OnFailure(peer, err, stats)
	})

	// 触发 OnFullDone 回调（仅一次）
	if shouldTriggerFullDone {
		safeCallback(func() {
			callback.OnFullDone(stats)
		})
	}
}

// buildStatsLocked 构建统计信息（内部方法，需持锁调用）
func (t *BroadcastTracker) buildStatsLocked() BroadcastStats {
	success := len(t.responses)
	failed := len(t.failures)
	total := len(t.targets)

	// P2 修复：避免除零
	var successRate float64
	if total > 0 {
		successRate = float64(success) / float64(total)
	}

	// 计算时间戳
	elapsedTime := time.Since(t.startTime)
	var firstResponseTime time.Duration
	var majorityReachTime time.Duration

	if t.firstResponseRecorded {
		firstResponseTime = t.firstResponseTime.Sub(t.startTime)
	}
	if t.majorityCallbackTriggered {
		majorityReachTime = t.majorityReachTime.Sub(t.startTime)
	}

	return BroadcastStats{
		TaskID:            t.taskID,
		Total:             total,
		Success:           success,
		Failed:            failed,
		Pending:           total - success - failed,
		SuccessRate:       successRate,
		ElapsedTime:       elapsedTime,
		FirstResponseTime: firstResponseTime,
		MajorityReachTime: majorityReachTime,
	}
}

// safeCallback 安全执行回调，防止 panic 影响主流程
func safeCallback(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			// 使用 fmt.Printf 记录 panic（项目未使用 slog）
			// 注意：生产环境建议使用 slog.Error 或日志框架
			fmt.Printf("[BroadcastTracker] callback panic recovered: %v\n", r)
		}
	}()
	fn()
}

// RPC 统一的 RPC 接口（合并原 RPC 和 MultiRPC）
//
// 统一了单播和广播两种通信模式，简化接口设计。
// - 单播：Call/CallAsync/OnRequest/OnRequestChan
// - 广播：BroadcastCall/BroadcastAsync/WriteV/WriteVCall（支持 ResponseStrategy + BroadcastTracker）
type RPC interface {
	// ====== 单播 ======
	// 同步调用（阻塞等响应）
	Call(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error)

	// 异步调用（不阻塞，回调返回）
	CallAsync(ctx context.Context, to model.PeerID, req model.Message, cb func(model.Message, error)) error

	// 函数式处理（服务端注册处理器）
	OnRequest(handler func(ctx context.Context, from model.PeerID, req model.Message) model.Message) error

	// Channel 模式接收请求
	OnRequestChan() <-chan RequestMsg

	// ====== 广播 ======
	// 同消息广播：支持响应策略 + 可选追踪器
	// - strategy: 响应策略（All/Majority/None）
	// - tracker: 可选追踪器，nil 表示不追踪
	BroadcastCall(
		ctx context.Context,
		to []model.PeerID,
		req model.Message,
		strategy ResponseStrategy,
		tracker *BroadcastTracker,
	) (BroadcastResult, error)

	// 同消息广播：异步回调 + 可选追踪器
	BroadcastAsync(
		ctx context.Context,
		to []model.PeerID,
		req model.Message,
		strategy ResponseStrategy,
		tracker *BroadcastTracker,
		cb func(from model.PeerID, resp model.Message, err error),
	) error

	// 不同消息群发：WriteV（单向，不等待响应，等价于 ResponseNone）
	// 注意：WriteV 是 "Write Vector" 的缩写，表示批量写入多个目标节点
	WriteV(ctx context.Context, targets []model.PeerID, msgs []model.Message, tracker *BroadcastTracker) error

	// 不同消息群发：支持响应策略 + 可选追踪器
	WriteVCall(
		ctx context.Context,
		targets []model.PeerID,
		msgs []model.Message,
		strategy ResponseStrategy,
		tracker *BroadcastTracker,
	) (WriteVResult, error)

	// ====== 生命周期 ======
	Close() error
}

// RequestMsg 用于 Channel 接收请求
type RequestMsg struct {
	Ctx    context.Context
	From   model.PeerID
	Req    model.Message
	RespCh chan ResponseMsg
}

// ResponseMsg 响应消息
type ResponseMsg struct {
	Msg model.Message
	Err error
}

// ============================================================================
// RequestID 生成器
// ============================================================================

// RequestID 请求唯一标识符
// 格式: {NodeID}-{Timestamp:08x}-{Sequence:04x}
// 示例: node-001-65d4a3f0-0001
//
// 设计说明：
// - nodeID: 节点唯一标识，确保跨节点不冲突
// - timestamp: Unix 时间戳（16 进制，8 位），支持跨节点时间排序
// - sequence: 自增序列号（16 进制，4 位），每秒最多 65535 个请求
//
// 优势：
// - 固定宽度：便于解析和索引
// - 16 进制：减少长度（vs 10 进制）
// - 时间排序：支持分布式追踪按时间排序
type RequestID string

// RequestIDGenerator 请求 ID 生成器（线程安全 + 时钟漂移保护）
type RequestIDGenerator struct {
	nodeID     string        // 节点 ID（启动时分配）
	lastSecond atomic.Int64  // 上次生成时间戳（秒）
	secondSeq  atomic.Uint32 // 当前秒内序列号
}

// NewRequestIDGenerator 创建请求 ID 生成器
func NewRequestIDGenerator(nodeID string) *RequestIDGenerator {
	return &RequestIDGenerator{
		nodeID:     nodeID,
		lastSecond: atomic.Int64{},
		secondSeq:  atomic.Uint32{},
	}
}

// Next 生成下一个请求 ID（线程安全 + 时钟漂移保护 + 序列号溢出保护）
//
// 时钟回退处理策略：
// - 当检测到系统时间回退（now < lastSecond）时，使用 lastSecond 作为时间戳
// - 这保证了 RequestID 单调递增，避免 ID 冲突
// - 场景：NTP 同步、闰秒、手动修改系统时间
//
// P1-1 修复：序列号溢出保护
// - 序列号格式为 4 位 16 进制（最大 0xFFFF = 65535）
// - 当序列号超过 65535 时，等待下一秒再生成
// - 这样可以保持 ID 格式的一致性
func (g *RequestIDGenerator) Next() RequestID {
	const maxSeq uint32 = 0xFFFF // 4 位 16 进制最大值

	for {
		now := time.Now().Unix()

		// 时钟漂移保护：检测时间回退
		for {
			lastSec := g.lastSecond.Load()

			if now > lastSec {
				// 时间前进：正常跨秒
				if g.lastSecond.CompareAndSwap(lastSec, now) {
					g.secondSeq.Store(0)
					break
				}
				// CAS 失败，重试
				continue
			}

			if now == lastSec {
				// 同一秒：继续递增序列号
				break
			}

			// now < lastSec：时间回退！
			// 策略：使用 lastSec 保证单调递增
			now = lastSec
			break
		}

		// 原子递增序列号
		seq := g.secondSeq.Add(1)

		// P1-1 修复：序列号溢出保护
		// 如果序列号超过 65535，等待下一秒再生成
		if seq > maxSeq {
			// 等待下一秒（最多 1 秒）
			time.Sleep(time.Until(time.Unix(now+1, 0)))
			continue
		}

		// 格式化：{NodeID}-{Timestamp:08x}-{Sequence:04x}
		return RequestID(fmt.Sprintf("%s-%08x-%04x", g.nodeID, now, seq))
	}
}

// ParseRequestID 解析请求 ID（用于日志和调试）
func ParseRequestID(id RequestID) (nodeID string, timestamp int64, sequence uint32, err error) {
	parts := strings.Split(string(id), "-")
	if len(parts) < 3 {
		return "", 0, 0, fmt.Errorf("invalid request ID format: expected {NodeID}-{Timestamp}-{Sequence}")
	}

	// 解析时间戳（倒数第二部分）
	tsHex := parts[len(parts)-2]
	ts, err := strconv.ParseInt(tsHex, 16, 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid timestamp: %w", err)
	}

	// 解析序列号（最后一部分）
	seqHex := parts[len(parts)-1]
	seq, err := strconv.ParseUint(seqHex, 16, 32)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid sequence: %w", err)
	}

	// 节点 ID（前面所有部分）
	nodeID = strings.Join(parts[:len(parts)-2], "-")

	return nodeID, ts, uint32(seq), nil
}

// Time 返回请求 ID 中的时间戳（用于排序）
func (id RequestID) Time() time.Time {
	_, ts, _, err := ParseRequestID(id)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}

// ============================================================================
// RPC 配置
// ============================================================================

// RPCConfig RPC 默认配置
type RPCConfig struct {
	// 超时配置
	CallTimeout      time.Duration // 单播调用超时，默认 30s
	BroadcastTimeout time.Duration // 广播调用超时，默认 60s
	ConnectTimeout   time.Duration // 连接超时，默认 10s

	// 重试配置
	MaxRetries   int           // 最大重试次数，默认 3
	RetryBackoff time.Duration // 重试退避时间，默认 1s

	// 并发配置
	MaxConcurrentCalls int // 最大并发调用数，默认 1000
	RequestBufferSize  int // 请求缓冲区大小，默认 256
}

// DefaultRPCConfig 返回默认配置
func DefaultRPCConfig() *RPCConfig {
	return &RPCConfig{
		CallTimeout:        30 * time.Second,
		BroadcastTimeout:   60 * time.Second,
		ConnectTimeout:     10 * time.Second,
		MaxRetries:         3,
		RetryBackoff:       time.Second,
		MaxConcurrentCalls: 1000,
		RequestBufferSize:  256,
	}
}

// ============================================================================
// Codec 接口定义
// ============================================================================

// Codec 消息编解码接口
type Codec interface {
	// Encode 编码消息为字节切片
	Encode(msg model.Message) ([]byte, error)

	// Decode 解码字节切片为消息
	Decode(data []byte) (model.Message, error)

	// Name 返回编解码器名称（如 "msgpack"）
	Name() string

	// Version 返回编解码器版本（如 "v1"），用于协议协商
	Version() string
}

// StreamCodec 流式编解码接口（支持分帧）
type StreamCodec interface {
	Codec

	// EncodeToWriter 编码并写入 Writer
	EncodeToWriter(w io.Writer, msg model.Message) error

	// DecodeFromReader 从 Reader 解码
	DecodeFromReader(r io.Reader) (model.Message, error)
}

// ============================================================================
// Middleware 接口定义
// ============================================================================

// SendFunc 发送函数签名
type SendFunc func(ctx context.Context, peer model.PeerID, msg model.Message) error

// ReceiveFunc 接收函数签名
type ReceiveFunc func(ctx context.Context, peer model.PeerID, msg model.Message) error

// Middleware 中间件接口（拦截器模式）
type Middleware interface {
	// Name 中间件名称
	Name() string

	// InterceptSend 拦截发送消息
	InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next SendFunc) error

	// InterceptReceive 拦截接收消息
	InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next ReceiveFunc) error
}

// MiddlewareChain 中间件链管理器
//
// 并发安全策略：
// 1. 使用读写锁（sync.RWMutex）保护中间件列表
// 2. Execute 时获取快照执行，避免持锁时间过长
// 3. 提供 Freeze 方法，冻结后禁止修改（高性能场景）
type MiddlewareChain interface {
	// Use 添加中间件到链尾
	Use(middleware Middleware) error

	// UseFirst 添加中间件到链头（优先执行）
	// 场景：日志中间件通常需要在最外层
	UseFirst(middleware Middleware) error

	// UseAt 在指定位置插入中间件
	// index=0 表示链头，index=len 表示链尾
	UseAt(index int, middleware Middleware) error

	// Remove 移除指定名称的中间件
	Remove(name string) error

	// List 获取所有中间件列表（返回快照）
	List() []Middleware

	// Freeze 冻结中间件链，禁止后续修改
	// 冻结后 Use/UseFirst/UseAt/Remove/Clear 返回 ErrChainFrozen
	// 适用场景：启动完成后调用，避免运行时修改开销
	Freeze()

	// IsFrozen 检查是否已冻结
	IsFrozen() bool

	// ExecuteSend 执行发送中间件链
	ExecuteSend(ctx context.Context, peer model.PeerID, msg model.Message, final SendFunc) error

	// ExecuteReceive 执行接收中间件链
	ExecuteReceive(ctx context.Context, peer model.PeerID, msg model.Message, final ReceiveFunc) error

	// Clear 清空所有中间件（冻结后返回错误）
	Clear() error
}
