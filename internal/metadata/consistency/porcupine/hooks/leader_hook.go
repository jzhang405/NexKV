// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件实现 Leader HA 模块的验证 Hook
package hooks

import (
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

// ==================== Leader Hook ====================

// LeaderHook Leader HA 验证 Hook
// 用于记录 Leader 变更和 Fencing Token 写入到 Porcupine 验证系统
type LeaderHook struct {
	base        *BaseHook
	async       *AsyncProcessor
	pending     *PendingOpsManager
	asyncConfig porcupine.AsyncRecordConfig
	nodeID      string
}

// NewLeaderHook 创建 Leader Hook
func NewLeaderHook(recorder *porcupine.EnhancedHistoryRecorder, nodeID string, asyncConfig porcupine.AsyncRecordConfig) *LeaderHook {
	h := &LeaderHook{
		base:        NewBaseHook(recorder, porcupine.OpTypeLeaderHA),
		pending:     NewPendingOpsManager(),
		asyncConfig: asyncConfig,
		nodeID:      nodeID,
	}

	h.async = NewAsyncProcessor(asyncConfig, h.processOp)

	return h
}

// ==================== VerificationHook 接口实现 ====================

// Enabled 返回是否启用
func (h *LeaderHook) Enabled() bool {
	return h.base.Enabled()
}

// SetEnabled 设置启用状态
func (h *LeaderHook) SetEnabled(enabled bool) {
	h.base.SetEnabled(enabled)
}

// Recorder 返回 recorder
func (h *LeaderHook) Recorder() *porcupine.EnhancedHistoryRecorder {
	return h.base.Recorder()
}

// Stats 返回统计信息
func (h *LeaderHook) Stats() HookStats {
	return h.base.Stats()
}

// ==================== Leader HA 操作记录 ====================

// OnLeaderChange Leader 变更记录
// 集成点: LeaderManager.BecomeLeader()
func (h *LeaderHook) OnLeaderChange(oldLeader, newLeader string, newTerm uint64) int {
	if !h.Enabled() {
		return -1
	}

	callTime := time.Now().UnixNano()

	op := porcupine.LeaderHAOperation{
		Type:      porcupine.LeaderHAOpLeaderChange,
		NodeID:    h.nodeID,
		NewLeader: newLeader,
		Term:      newTerm,
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

// OnLeaderChangeReturn Leader 变更返回
func (h *LeaderHook) OnLeaderChangeReturn(opID int, ok bool, errMsg string, newLeader string, newTerm uint64) {
	if !h.Enabled() || opID < 0 {
		return
	}

	if _, exists := h.pending.Get(opID); !exists {
		return
	}

	output := porcupine.LeaderHAOutput{
		Ok:         ok,
		Error:      errMsg,
		NewLeader:  newLeader,
		ActiveTerm: newTerm,
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

// OnFencingWrite Fencing Token 写入记录
// 集成点: FencingStore.Write()
func (h *LeaderHook) OnFencingWrite(key string, value []byte, term uint64) int {
	if !h.Enabled() {
		return -1
	}

	callTime, version := GenerateVersion()

	op := porcupine.LeaderHAOperation{
		Type:    porcupine.LeaderHAOpWrite,
		NodeID:  h.nodeID,
		Key:     key,
		Value:   value,
		Version: version,
		Term:    term,
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

// OnFencingWriteReturn Fencing 写入返回
func (h *LeaderHook) OnFencingWriteReturn(opID int, ok bool, errMsg string, term uint64) {
	if !h.Enabled() || opID < 0 {
		return
	}

	if _, exists := h.pending.Get(opID); !exists {
		return
	}

	output := porcupine.LeaderHAOutput{
		Ok:    ok,
		Error: errMsg,
		Term:  term,
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
func (h *LeaderHook) processOp(op AsyncOp) {
	switch op.OpType {
	case AsyncOpTypeCall:
		if leaderOp, ok := op.CallOp.(porcupine.LeaderHAOperation); ok {
			h.base.Recorder().RecordLeaderHACall(leaderOp)
		}
	case AsyncOpTypeReturn:
		if output, ok := op.ReturnOp.(porcupine.LeaderHAOutput); ok {
			h.base.Recorder().RecordLeaderHAReturn(op.OpID, output)
		}
	}
}

// Start 启动后台处理
func (h *LeaderHook) Start() {
	h.async.Start()
}

// Stop 停止后台处理
func (h *LeaderHook) Stop() {
	h.async.Stop()
}

// Flush 刷新待处理的操作
func (h *LeaderHook) Flush() {
	output := porcupine.LeaderHAOutput{
		Ok:    false,
		Error: "timeout",
	}

	h.pending.Range(func(opID int, op *PendingOp) bool {
		if leaderOp, ok := op.CallData.(porcupine.LeaderHAOperation); ok {
			internalOpID := h.base.Recorder().RecordLeaderHACall(leaderOp)
			h.base.Recorder().RecordLeaderHAReturn(internalOpID, output)
		}
		h.pending.Remove(opID)
		return true
	})
}
