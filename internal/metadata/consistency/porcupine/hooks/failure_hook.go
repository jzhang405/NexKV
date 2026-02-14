// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件实现故障检测模块的验证 Hook
package hooks

import (
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

// ==================== Failure Hook ====================

// FailureHook 故障检测验证 Hook
// 用于记录节点故障/恢复事件到 Porcupine 验证系统
//
// 设计说明（P0-01 修复）：
// - 不记录每次心跳（高频操作，会产生大量噪音）
// - 只在故障判定时记录（真正需要验证的事件）
// - FailureRecoveryModel 支持 FailureRecoveryOpNodeFail/NodeRecover 类型
type FailureHook struct {
	base        *BaseHook
	async       *AsyncProcessor
	pending     *PendingOpsManager
	asyncConfig porcupine.AsyncRecordConfig
}

// NewFailureHook 创建 Failure Hook
func NewFailureHook(recorder *porcupine.EnhancedHistoryRecorder, asyncConfig porcupine.AsyncRecordConfig) *FailureHook {
	h := &FailureHook{
		base:        NewBaseHook(recorder, porcupine.OpTypeFailureRecovery),
		pending:     NewPendingOpsManager(),
		asyncConfig: asyncConfig,
	}

	h.async = NewAsyncProcessor(asyncConfig, h.processOp)

	return h
}

// ==================== VerificationHook 接口实现 ====================

// Enabled 返回是否启用
func (h *FailureHook) Enabled() bool {
	return h.base.Enabled()
}

// SetEnabled 设置启用状态
func (h *FailureHook) SetEnabled(enabled bool) {
	h.base.SetEnabled(enabled)
}

// Recorder 返回 recorder
func (h *FailureHook) Recorder() *porcupine.EnhancedHistoryRecorder {
	return h.base.Recorder()
}

// Stats 返回统计信息
func (h *FailureHook) Stats() HookStats {
	return h.base.Stats()
}

// ==================== Failure 操作记录 ====================

// OnNodeFailure 节点故障记录
// 集成点: PhiAccrualDetector.IsNodeFailed() 返回 true 时调用
func (h *FailureHook) OnNodeFailure(nodeID string) int {
	return h.recordNodeEvent(nodeID, porcupine.FailureRecoveryOpNodeFail)
}

// OnNodeRecovery 节点恢复记录
// 集成点: PhiAccrualDetector.Reset() 时调用
func (h *FailureHook) OnNodeRecovery(nodeID string) int {
	return h.recordNodeEvent(nodeID, porcupine.FailureRecoveryOpNodeRecover)
}

// recordNodeEvent 统一记录节点事件（DRY 优化）
func (h *FailureHook) recordNodeEvent(nodeID string, opType porcupine.FailureRecoveryOpType) int {
	if !h.Enabled() {
		return -1
	}

	callTime := time.Now().UnixNano()

	op := porcupine.FailureRecoveryOperation{
		Type:   opType,
		NodeID: nodeID,
	}

	opID := h.pending.Add(callTime, op)

	asyncOp := AsyncOp{
		OpType:   AsyncOpTypeCall,
		CallOp:   op,
		CallTime: callTime,
	}

	if !h.async.Enqueue(asyncOp) {
		h.pending.Remove(opID)
		h.base.AddDropped(1)
		return -1
	}

	h.base.AddRecorded(1)
	return opID
}

// OnFailureReturn 故障操作返回
func (h *FailureHook) OnFailureReturn(opID int, ok bool, errMsg string) {
	if !h.Enabled() || opID < 0 {
		return
	}

	if _, exists := h.pending.Get(opID); !exists {
		return
	}

	output := porcupine.FailureRecoveryOutput{
		Ok:    ok,
		Error: errMsg,
	}

	asyncOp := AsyncOp{
		OpType:   AsyncOpTypeReturn,
		OpID:     opID,
		ReturnOp: output,
	}

	if !h.async.Enqueue(asyncOp) {
		h.base.AddDropped(1)
	}

	h.pending.Remove(opID)
}

// ==================== 异步处理 ====================

// processOp 处理单个操作
func (h *FailureHook) processOp(op AsyncOp) {
	switch op.OpType {
	case AsyncOpTypeCall:
		if failureOp, ok := op.CallOp.(porcupine.FailureRecoveryOperation); ok {
			h.base.Recorder().RecordFailureRecoveryCall(failureOp)
		}
	case AsyncOpTypeReturn:
		if output, ok := op.ReturnOp.(porcupine.FailureRecoveryOutput); ok {
			h.base.Recorder().RecordFailureRecoveryReturn(op.OpID, output)
		}
	}
}

// Start 启动后台处理
func (h *FailureHook) Start() {
	h.async.Start()
}

// Stop 停止后台处理
func (h *FailureHook) Stop() {
	h.async.Stop()
}

// Flush 刷新待处理的操作
func (h *FailureHook) Flush() {
	output := porcupine.FailureRecoveryOutput{
		Ok:    false,
		Error: "timeout",
	}

	h.pending.Range(func(opID int, op *PendingOp) bool {
		if failureOp, ok := op.CallData.(porcupine.FailureRecoveryOperation); ok {
			internalOpID := h.base.Recorder().RecordFailureRecoveryCall(failureOp)
			h.base.Recorder().RecordFailureRecoveryReturn(internalOpID, output)
		}
		h.pending.Remove(opID)
		return true
	})
}
