// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件定义 Hook 接口和通用类型
package hooks

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

// ==================== Hook 接口定义 ====================

// VerificationHook 验证 Hook 接口
type VerificationHook interface {
	// Enabled 返回 Hook 是否启用
	Enabled() bool

	// SetEnabled 设置 Hook 启用状态
	SetEnabled(enabled bool)

	// Recorder 返回关联的记录器（共享实例）
	Recorder() *porcupine.EnhancedHistoryRecorder

	// Stats 返回 Hook 统计信息
	Stats() HookStats

	// Flush 刷新待处理的操作（用于优雅关闭）
	Flush()

	// Start 启动 Hook 的后台处理
	Start()

	// Stop 停止 Hook 的后台处理
	Stop()
}

// ==================== Hook 统计信息 ====================

// HookStats Hook 统计信息
type HookStats struct {
	TotalRecorded  int64 // 总记录数
	TotalVerified  int64 // 总验证数
	TotalFailed    int64 // 总失败数
	TotalErrors    int64 // 总错误数（记录失败）
	DroppedOps     int64 // 丢弃的操作数（异步队列满）
	LastVerifyTime int64 // 最后验证时间
}

// AddRecorded 原子增加记录数
func (s *HookStats) AddRecorded(delta int64) {
	atomic.AddInt64(&s.TotalRecorded, delta)
}

// AddDropped 原子增加丢弃数
func (s *HookStats) AddDropped(delta int64) {
	atomic.AddInt64(&s.DroppedOps, delta)
}

// AddError 原子增加错误数
func (s *HookStats) AddError(delta int64) {
	atomic.AddInt64(&s.TotalErrors, delta)
}

// SetLastVerifyTime 原子设置最后验证时间
func (s *HookStats) SetLastVerifyTime(t int64) {
	atomic.StoreInt64(&s.LastVerifyTime, t)
}

// ==================== 异步操作定义 ====================

// AsyncOpType 异步操作类型
type AsyncOpType int

const (
	// AsyncOpTypeCall 调用操作
	AsyncOpTypeCall AsyncOpType = iota
	// AsyncOpTypeReturn 返回操作
	AsyncOpTypeReturn
)

// AsyncOp 异步操作
type AsyncOp struct {
	OpType   AsyncOpType // 操作类型（Call/Return）
	CallOp   interface{} // Call 操作（TopologyOperation/FailureRecoveryOperation/LeaderHAOperation）
	ReturnOp interface{} // Return 操作（TopologyOutput/FailureRecoveryOutput/LeaderHAOutput）
	OpID     int         // 操作 ID（Return 时使用）
	CallTime int64       // Call 时间戳
}

// ==================== Hook 基础结构 ====================

// BaseHook Hook 基础结构（嵌入复用）
type BaseHook struct {
	enabled   atomic.Bool                        // 是否启用（并发安全）
	recorder  *porcupine.EnhancedHistoryRecorder // 共享的 recorder
	modelType porcupine.EnhancedOpType           // 模型类型
	stats     HookStats                          // 统计信息
}

// NewBaseHook 创建基础 Hook
func NewBaseHook(recorder *porcupine.EnhancedHistoryRecorder, modelType porcupine.EnhancedOpType) *BaseHook {
	return &BaseHook{
		recorder:  recorder,
		modelType: modelType,
		stats:     HookStats{},
	}
}

// Enabled 返回是否启用
func (h *BaseHook) Enabled() bool {
	return h.enabled.Load()
}

// SetEnabled 设置启用状态
func (h *BaseHook) SetEnabled(enabled bool) {
	h.enabled.Store(enabled)
}

// Recorder 返回 recorder
func (h *BaseHook) Recorder() *porcupine.EnhancedHistoryRecorder {
	return h.recorder
}

// Stats 返回统计信息
func (h *BaseHook) Stats() HookStats {
	return h.stats
}

// ModelType 返回模型类型
func (h *BaseHook) ModelType() porcupine.EnhancedOpType {
	return h.modelType
}

// AddRecorded 增加记录数
func (h *BaseHook) AddRecorded(delta int64) {
	h.stats.AddRecorded(delta)
}

// AddDropped 增加丢弃数
func (h *BaseHook) AddDropped(delta int64) {
	h.stats.AddDropped(delta)
}

// AddError 增加错误数
func (h *BaseHook) AddError(delta int64) {
	h.stats.AddError(delta)
}

// ==================== 异步处理基础设施（DRY 优化） ====================

// AsyncProcessor 通用的异步操作处理器
// 用于统一处理所有 Hook 的异步操作，避免代码重复
type AsyncProcessor struct {
	asyncConfig porcupine.AsyncRecordConfig
	opChan      chan AsyncOp

	// 生命周期管理
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 操作处理器（由具体 Hook 注入）
	processOpFunc func(op AsyncOp)
}

// NewAsyncProcessor 创建异步处理器
func NewAsyncProcessor(asyncConfig porcupine.AsyncRecordConfig, processOpFunc func(op AsyncOp)) *AsyncProcessor {
	ctx, cancel := context.WithCancel(context.Background())

	return &AsyncProcessor{
		asyncConfig:   asyncConfig,
		opChan:        make(chan AsyncOp, asyncConfig.BufferSize),
		ctx:           ctx,
		cancel:        cancel,
		processOpFunc: processOpFunc,
	}
}

// Enqueue 入队异步操作
func (p *AsyncProcessor) Enqueue(op AsyncOp) bool {
	if !p.asyncConfig.Enabled {
		// 同步模式，直接处理
		p.processOpFunc(op)
		return true
	}

	if p.asyncConfig.DropOnFull {
		select {
		case p.opChan <- op:
			return true
		default:
			return false
		}
	}

	// 阻塞模式
	p.opChan <- op
	return true
}

// Start 启动后台处理
func (p *AsyncProcessor) Start() {
	if !p.asyncConfig.Enabled {
		return
	}

	p.wg.Add(1)
	go p.processLoop()
}

// Stop 停止后台处理
func (p *AsyncProcessor) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
}

// processLoop 后台处理循环
func (p *AsyncProcessor) processLoop() {
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			return
		case op := <-p.opChan:
			p.processOpFunc(op)
		}
	}
}

// ==================== Pending 操作管理（泛型版本） ====================

// PendingOp 通用的待完成操作结构
type PendingOp struct {
	CallTime int64
	CallData interface{}
}

// PendingOpsManager 管理 pending 操作的通用结构
type PendingOpsManager struct {
	mu       sync.RWMutex
	pending  map[int]*PendingOp
	nextOpID int
}

// NewPendingOpsManager 创建 pending 操作管理器
func NewPendingOpsManager() *PendingOpsManager {
	return &PendingOpsManager{
		pending:  make(map[int]*PendingOp),
		nextOpID: 0,
	}
}

// Add 添加 pending 操作，返回 opID
func (m *PendingOpsManager) Add(callTime int64, callData interface{}) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	opID := m.nextOpID
	m.nextOpID++

	m.pending[opID] = &PendingOp{
		CallTime: callTime,
		CallData: callData,
	}

	return opID
}

// Get 获取 pending 操作
func (m *PendingOpsManager) Get(opID int) (*PendingOp, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, exists := m.pending[opID]
	return op, exists
}

// Remove 移除 pending 操作
func (m *PendingOpsManager) Remove(opID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, opID)
}

// Range 遍历所有 pending 操作
func (m *PendingOpsManager) Range(fn func(opID int, op *PendingOp) bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for opID, op := range m.pending {
		if !fn(opID, op) {
			break
		}
	}
}

// Clear 清空所有 pending 操作
func (m *PendingOpsManager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = make(map[int]*PendingOp)
}

// ==================== 时间戳版本号生成 ====================

// GenerateVersion 从时间戳生成版本号
func GenerateVersion() (callTime int64, version uint64) {
	callTime = time.Now().UnixNano()
	version = uint64(callTime)
	return callTime, version
}
