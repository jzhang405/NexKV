// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件实现 Gossip 模块的验证 Hook
package hooks

import (
	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

// ==================== Gossip Hook ====================

// GossipHook Gossip 验证 Hook
// 用于记录 Gossip 写入操作到 Porcupine 验证系统
type GossipHook struct {
	base        *BaseHook
	async       *AsyncProcessor
	pending     *PendingOpsManager
	asyncConfig porcupine.AsyncRecordConfig
	topology    *porcupine.Topology
	nodeID      string
}

// NewGossipHook 创建 Gossip Hook
func NewGossipHook(recorder *porcupine.EnhancedHistoryRecorder, nodeID string, asyncConfig porcupine.AsyncRecordConfig) *GossipHook {
	h := &GossipHook{
		base:        NewBaseHook(recorder, porcupine.OpTypeTopology),
		pending:     NewPendingOpsManager(),
		asyncConfig: asyncConfig,
		nodeID:      nodeID,
	}

	// 创建异步处理器，注入处理函数
	h.async = NewAsyncProcessor(asyncConfig, h.processOp)

	return h
}

// SetTopology 设置拓扑信息
func (h *GossipHook) SetTopology(topology *porcupine.Topology) {
	h.topology = topology
}

// ==================== VerificationHook 接口实现 ====================

// Enabled 返回是否启用
func (h *GossipHook) Enabled() bool {
	return h.base.Enabled()
}

// SetEnabled 设置启用状态
func (h *GossipHook) SetEnabled(enabled bool) {
	h.base.SetEnabled(enabled)
}

// Recorder 返回 recorder
func (h *GossipHook) Recorder() *porcupine.EnhancedHistoryRecorder {
	return h.base.Recorder()
}

// Stats 返回统计信息
func (h *GossipHook) Stats() HookStats {
	return h.base.Stats()
}

// ==================== Gossip 操作记录 ====================

// OnGossipWrite Gossip 写入时记录
// 集成点: EventDrivenGossipSync.OnWrite()
func (h *GossipHook) OnGossipWrite(key string, value []byte) int {
	if !h.Enabled() {
		return -1
	}

	callTime, version := GenerateVersion()

	op := porcupine.TopologyOperation{
		Type:    porcupine.TopologyOpWriteGossip,
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

// OnGossipReturn Gossip 返回时记录
func (h *GossipHook) OnGossipReturn(opID int, ok bool, errMsg string) {
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
func (h *GossipHook) processOp(op AsyncOp) {
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
func (h *GossipHook) Start() {
	h.async.Start()
}

// Stop 停止后台处理
func (h *GossipHook) Stop() {
	h.async.Stop()
}

// Flush 刷新待处理的操作
func (h *GossipHook) Flush() {
	output := porcupine.TopologyOutput{
		Ok:    false,
		Error: "timeout",
	}

	h.pending.Range(func(opID int, op *PendingOp) bool {
		if topologyOp, ok := op.CallData.(porcupine.TopologyOperation); ok {
			internalOpID := h.base.Recorder().RecordTopologyCall(topologyOp)
			h.base.Recorder().RecordTopologyReturn(internalOpID, output)
		}
		h.pending.Remove(opID)
		return true
	})
}
