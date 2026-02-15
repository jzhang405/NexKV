// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件实现降级管理模块的验证 Hook
package hooks

import (
	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

// ==================== Degradation Hook ====================

// DegradationHook 降级管理验证 Hook
// 用于记录降级写入操作到 Porcupine 验证系统
//
// 类型映射（P0-03 修复）：
// - 使用 FailureRecoveryOpQuorumWrite（正确的枚举值）
// - 通过 Output.Error 字段标记降级状态
type DegradationHook struct {
	base        *BaseHook
	async       *AsyncProcessor
	pending     *PendingOpsManager
	asyncConfig porcupine.AsyncRecordConfig
	nodeID      string
}

// NewDegradationHook 创建 Degradation Hook
func NewDegradationHook(recorder *porcupine.EnhancedHistoryRecorder, nodeID string, asyncConfig porcupine.AsyncRecordConfig) *DegradationHook {
	h := &DegradationHook{
		base:        NewBaseHook(recorder, porcupine.OpTypeFailureRecovery),
		pending:     NewPendingOpsManager(),
		asyncConfig: asyncConfig,
		nodeID:      nodeID,
	}

	h.async = NewAsyncProcessor(asyncConfig, h.processOp)

	return h
}

// ==================== VerificationHook 接口实现 ====================

// Enabled 返回是否启用
func (h *DegradationHook) Enabled() bool {
	return h.base.Enabled()
}

// SetEnabled 设置启用状态
func (h *DegradationHook) SetEnabled(enabled bool) {
	h.base.SetEnabled(enabled)
}

// Recorder 返回 recorder
func (h *DegradationHook) Recorder() *porcupine.EnhancedHistoryRecorder {
	return h.base.Recorder()
}

// Stats 返回统计信息
func (h *DegradationHook) Stats() HookStats {
	return h.base.Stats()
}

// ==================== Degradation 操作记录 ====================

// OnDegradedWrite 降级写入记录
// 集成点: Manager.writeWithDegradation()
func (h *DegradationHook) OnDegradedWrite(key string, value []byte) int {
	if !h.Enabled() {
		return -1
	}

	callTime, version := GenerateVersion()

	op := porcupine.FailureRecoveryOperation{
		Type:    porcupine.FailureRecoveryOpQuorumWrite,
		NodeID:  h.nodeID,
		Key:     key,
		Value:   value,
		Version: version,
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

// OnDegradedReturn 降级写入返回
// 通过 Error 字段标记降级状态
func (h *DegradationHook) OnDegradedReturn(opID int, ok bool, degraded bool) {
	if !h.Enabled() || opID < 0 {
		return
	}

	if _, exists := h.pending.Get(opID); !exists {
		return
	}

	output := porcupine.FailureRecoveryOutput{
		Ok: ok,
	}
	if degraded {
		output.Error = "degraded"
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
func (h *DegradationHook) processOp(op AsyncOp) {
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
func (h *DegradationHook) Start() {
	h.async.Start()
}

// Stop 停止后台处理
func (h *DegradationHook) Stop() {
	h.async.Stop()
}

// Flush 刷新待处理的操作
func (h *DegradationHook) Flush() {
	output := porcupine.FailureRecoveryOutput{
		Ok:    false,
		Error: "timeout",
	}

	h.pending.RangeAndDelete(func(opID int, op *PendingOp) (bool, bool) {
		if failureOp, ok := op.CallData.(porcupine.FailureRecoveryOperation); ok {
			internalOpID := h.base.Recorder().RecordFailureRecoveryCall(failureOp)
			h.base.Recorder().RecordFailureRecoveryReturn(internalOpID, output)
		}
		return true, true // continue, delete
	})
}
