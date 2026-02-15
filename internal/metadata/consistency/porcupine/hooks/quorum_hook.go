// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件实现 Quorum 模块的验证 Hook
package hooks

import (
	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

// ==================== Quorum Hook ====================

// QuorumHook Quorum 验证 Hook
// 用于记录 Quorum 写入操作到 Porcupine 验证系统
type QuorumHook struct {
	base        *BaseHook
	async       *AsyncProcessor
	pending     *PendingOpsManager
	asyncConfig porcupine.AsyncRecordConfig
	nodeID      string
}

// NewQuorumHook 创建 Quorum Hook
func NewQuorumHook(recorder *porcupine.EnhancedHistoryRecorder, nodeID string, asyncConfig porcupine.AsyncRecordConfig) *QuorumHook {
	h := &QuorumHook{
		base:        NewBaseHook(recorder, porcupine.OpTypeTopology),
		pending:     NewPendingOpsManager(),
		asyncConfig: asyncConfig,
		nodeID:      nodeID,
	}

	h.async = NewAsyncProcessor(asyncConfig, h.processOp)

	return h
}

// ==================== VerificationHook 接口实现 ====================

// Enabled 返回是否启用
func (h *QuorumHook) Enabled() bool {
	return h.base.Enabled()
}

// SetEnabled 设置启用状态
func (h *QuorumHook) SetEnabled(enabled bool) {
	h.base.SetEnabled(enabled)
}

// Recorder 返回 recorder
func (h *QuorumHook) Recorder() *porcupine.EnhancedHistoryRecorder {
	return h.base.Recorder()
}

// Stats 返回统计信息
func (h *QuorumHook) Stats() HookStats {
	return h.base.Stats()
}

// ==================== Quorum 操作记录 ====================

// OnQuorumWrite Quorum 写入时记录
// 集成点: QuorumCoordinator.PutWithQuorum()
func (h *QuorumHook) OnQuorumWrite(key string, value []byte, participants []string) int {
	if !h.Enabled() {
		return -1
	}

	callTime, version := GenerateVersion()

	op := porcupine.TopologyOperation{
		Type:         porcupine.TopologyOpWriteQuorum,
		NodeID:       h.nodeID,
		Key:          key,
		Value:        value,
		Version:      version,
		Participants: participants,
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

// OnQuorumReturn Quorum 返回时记录
func (h *QuorumHook) OnQuorumReturn(opID int, ok bool, errMsg string) {
	if !h.Enabled() || opID < 0 {
		return
	}

	if _, exists := h.pending.Get(opID); !exists {
		return
	}

	output := porcupine.TopologyOutput{
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
func (h *QuorumHook) processOp(op AsyncOp) {
	switch op.OpType {
	case AsyncOpTypeCall:
		if topologyOp, ok := op.CallOp.(porcupine.TopologyOperation); ok {
			h.base.Recorder().RecordTopologyCall(topologyOp)
		}
	case AsyncOpTypeReturn:
		if output, ok := op.ReturnOp.(porcupine.TopologyOutput); ok {
			h.base.Recorder().RecordTopologyReturn(op.OpID, output)
		}
	}
}

// Start 启动后台处理
func (h *QuorumHook) Start() {
	h.async.Start()
}

// Stop 停止后台处理
func (h *QuorumHook) Stop() {
	h.async.Stop()
}

// Flush 刷新待处理的操作
func (h *QuorumHook) Flush() {
	output := porcupine.TopologyOutput{
		Ok:    false,
		Error: "timeout",
	}

	h.pending.RangeAndDelete(func(opID int, op *PendingOp) (bool, bool) {
		if topologyOp, ok := op.CallData.(porcupine.TopologyOperation); ok {
			internalOpID := h.base.Recorder().RecordTopologyCall(topologyOp)
			h.base.Recorder().RecordTopologyReturn(internalOpID, output)
		}
		return true, true // continue, delete
	})
}
