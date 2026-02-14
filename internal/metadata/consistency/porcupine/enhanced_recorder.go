// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件实现增强记录器，支持多种增强模型的操作记录
package porcupine

import (
	"sync"

	"github.com/anishathalye/porcupine"
)

// ==================== 增强操作类型定义 ====================

// EnhancedOpType 增强操作类型
type EnhancedOpType int

const (
	// OpTypeTopology 拓扑感知操作
	OpTypeTopology EnhancedOpType = iota
	// OpTypeFailureRecovery 失败恢复操作
	OpTypeFailureRecovery
	// OpTypeLeaderHA Leader HA 操作
	OpTypeLeaderHA
)

// String 返回操作类型的字符串表示
func (t EnhancedOpType) String() string {
	switch t {
	case OpTypeTopology:
		return "Topology"
	case OpTypeFailureRecovery:
		return "FailureRecovery"
	case OpTypeLeaderHA:
		return "LeaderHA"
	default:
		return "Unknown"
	}
}

// ==================== 增强操作输入/输出 ====================

// EnhancedInput 增强操作输入（联合类型）
type EnhancedInput struct {
	Type              EnhancedOpType
	TopologyOp        TopologyOperation
	FailureRecoveryOp FailureRecoveryOperation
	LeaderHAOp        LeaderHAOperation
}

// EnhancedOutput 增强操作输出（联合类型）
type EnhancedOutput struct {
	Type               EnhancedOpType
	TopologyOut        TopologyOutput
	FailureRecoveryOut FailureRecoveryOutput
	LeaderHAOut        LeaderHAOutput
}

// ==================== 增强记录器 ====================

// enhancedPendingOp 待完成的增强操作
type enhancedPendingOp struct {
	input EnhancedInput
	call  int64
}

// EnhancedHistoryRecorder 增强历史记录器
// 支持记录多种增强模型的操作事件
type EnhancedHistoryRecorder struct {
	mu        sync.Mutex
	clientID  int
	timestamp TimestampGenerator
	ops       []porcupine.Operation
	pending   map[int]enhancedPendingOp
	opID      int
}

// NewEnhancedHistoryRecorder 创建增强历史记录器
func NewEnhancedHistoryRecorder(clientID string, timestamp TimestampGenerator) *EnhancedHistoryRecorder {
	return &EnhancedHistoryRecorder{
		clientID:  extractClientID(clientID),
		timestamp: timestamp,
		ops:       make([]porcupine.Operation, 0),
		pending:   make(map[int]enhancedPendingOp),
		opID:      0,
	}
}

// ==================== 拓扑感知操作记录 ====================

// RecordTopologyCall 记录拓扑感知操作调用
func (r *EnhancedHistoryRecorder) RecordTopologyCall(op TopologyOperation) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	opID := r.opID
	r.opID++

	r.pending[opID] = enhancedPendingOp{
		input: EnhancedInput{
			Type:       OpTypeTopology,
			TopologyOp: op,
		},
		call: r.timestamp.Now(),
	}

	return opID
}

// RecordTopologyReturn 记录拓扑感知操作返回
func (r *EnhancedHistoryRecorder) RecordTopologyReturn(opID int, output TopologyOutput) {
	r.mu.Lock()
	defer r.mu.Unlock()

	pending, exists := r.pending[opID]
	if !exists {
		return
	}
	delete(r.pending, opID)

	op := porcupine.Operation{
		ClientId: r.clientID,
		Input:    pending.input,
		Call:     pending.call,
		Output: EnhancedOutput{
			Type:        OpTypeTopology,
			TopologyOut: output,
		},
		Return: r.timestamp.Now(),
	}
	r.ops = append(r.ops, op)
}

// ==================== 失败恢复操作记录 ====================

// RecordFailureRecoveryCall 记录失败恢复操作调用
func (r *EnhancedHistoryRecorder) RecordFailureRecoveryCall(op FailureRecoveryOperation) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	opID := r.opID
	r.opID++

	r.pending[opID] = enhancedPendingOp{
		input: EnhancedInput{
			Type:              OpTypeFailureRecovery,
			FailureRecoveryOp: op,
		},
		call: r.timestamp.Now(),
	}

	return opID
}

// RecordFailureRecoveryReturn 记录失败恢复操作返回
func (r *EnhancedHistoryRecorder) RecordFailureRecoveryReturn(opID int, output FailureRecoveryOutput) {
	r.mu.Lock()
	defer r.mu.Unlock()

	pending, exists := r.pending[opID]
	if !exists {
		return
	}
	delete(r.pending, opID)

	op := porcupine.Operation{
		ClientId: r.clientID,
		Input:    pending.input,
		Call:     pending.call,
		Output: EnhancedOutput{
			Type:               OpTypeFailureRecovery,
			FailureRecoveryOut: output,
		},
		Return: r.timestamp.Now(),
	}
	r.ops = append(r.ops, op)
}

// ==================== Leader HA 操作记录 ====================

// RecordLeaderHACall 记录 Leader HA 操作调用
func (r *EnhancedHistoryRecorder) RecordLeaderHACall(op LeaderHAOperation) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	opID := r.opID
	r.opID++

	r.pending[opID] = enhancedPendingOp{
		input: EnhancedInput{
			Type:       OpTypeLeaderHA,
			LeaderHAOp: op,
		},
		call: r.timestamp.Now(),
	}

	return opID
}

// RecordLeaderHAReturn 记录 Leader HA 操作返回
func (r *EnhancedHistoryRecorder) RecordLeaderHAReturn(opID int, output LeaderHAOutput) {
	r.mu.Lock()
	defer r.mu.Unlock()

	pending, exists := r.pending[opID]
	if !exists {
		return
	}
	delete(r.pending, opID)

	op := porcupine.Operation{
		ClientId: r.clientID,
		Input:    pending.input,
		Call:     pending.call,
		Output: EnhancedOutput{
			Type:        OpTypeLeaderHA,
			LeaderHAOut: output,
		},
		Return: r.timestamp.Now(),
	}
	r.ops = append(r.ops, op)
}

// ==================== 通用方法 ====================

// GetOperations 获取所有已完成的操作
func (r *EnhancedHistoryRecorder) GetOperations() []porcupine.Operation {
	r.mu.Lock()
	defer r.mu.Unlock()

	ops := make([]porcupine.Operation, len(r.ops))
	copy(ops, r.ops)
	return ops
}

// GetTopologyOperations 获取拓扑感知操作（过滤）
func (r *EnhancedHistoryRecorder) GetTopologyOperations() []porcupine.Operation {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []porcupine.Operation
	for _, op := range r.ops {
		if input, ok := op.Input.(EnhancedInput); ok && input.Type == OpTypeTopology {
			result = append(result, op)
		}
	}
	return result
}

// GetFailureRecoveryOperations 获取失败恢复操作（过滤）
func (r *EnhancedHistoryRecorder) GetFailureRecoveryOperations() []porcupine.Operation {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []porcupine.Operation
	for _, op := range r.ops {
		if input, ok := op.Input.(EnhancedInput); ok && input.Type == OpTypeFailureRecovery {
			result = append(result, op)
		}
	}
	return result
}

// GetLeaderHAOperations 获取 Leader HA 操作（过滤）
func (r *EnhancedHistoryRecorder) GetLeaderHAOperations() []porcupine.Operation {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []porcupine.Operation
	for _, op := range r.ops {
		if input, ok := op.Input.(EnhancedInput); ok && input.Type == OpTypeLeaderHA {
			result = append(result, op)
		}
	}
	return result
}

// GetPendingInput 获取待完成操作的输入
func (r *EnhancedHistoryRecorder) GetPendingInput(opID int) *EnhancedInput {
	r.mu.Lock()
	defer r.mu.Unlock()

	if pending, ok := r.pending[opID]; ok {
		return &pending.input
	}
	return nil
}

// Clear 清空所有记录
func (r *EnhancedHistoryRecorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ops = make([]porcupine.Operation, 0)
	r.pending = make(map[int]enhancedPendingOp)
}

// ClientID 获取客户端 ID
func (r *EnhancedHistoryRecorder) ClientID() int {
	return r.clientID
}

// Len 获取已完成的操作数量
func (r *EnhancedHistoryRecorder) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ops)
}

// PendingLen 获取待完成的操作数量
func (r *EnhancedHistoryRecorder) PendingLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// Trim 修剪历史记录（P1-03 内存控制）
// 保留最新的 maxOps 个操作，删除旧操作
func (r *EnhancedHistoryRecorder) Trim(maxOps int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.ops) > maxOps {
		r.ops = r.ops[len(r.ops)-maxOps:]
	}
}

// Stats 获取统计信息
func (r *EnhancedHistoryRecorder) Stats() EnhancedRecorderStats {
	r.mu.Lock()
	defer r.mu.Unlock()

	stats := EnhancedRecorderStats{
		TotalOps:   len(r.ops),
		PendingOps: len(r.pending),
		ByType:     make(map[EnhancedOpType]int),
	}

	for _, op := range r.ops {
		if input, ok := op.Input.(EnhancedInput); ok {
			stats.ByType[input.Type]++
		}
	}

	return stats
}

// EnhancedRecorderStats 增强记录器统计信息
type EnhancedRecorderStats struct {
	TotalOps   int
	PendingOps int
	ByType     map[EnhancedOpType]int
}

// ==================== 验证辅助函数 ====================

// VerifyTopology 验证拓扑感知操作
func VerifyTopology(recorder *EnhancedHistoryRecorder) (bool, string) {
	ops := recorder.GetTopologyOperations()
	if len(ops) == 0 {
		return true, "no topology operations to verify"
	}

	// 转换为拓扑模型可用的格式
	topologyOps := make([]porcupine.Operation, len(ops))
	for i, op := range ops {
		input := op.Input.(EnhancedInput)
		output := op.Output.(EnhancedOutput)
		topologyOps[i] = porcupine.Operation{
			ClientId: op.ClientId,
			Input:    input.TopologyOp,
			Output:   output.TopologyOut,
			Call:     op.Call,
			Return:   op.Return,
		}
	}

	model := TopologyAwareModel()
	result := porcupine.CheckOperations(model, topologyOps)
	if result {
		return true, "topology model verification passed"
	}
	return false, "topology model verification failed"
}

// VerifyFailureRecovery 验证失败恢复操作
func VerifyFailureRecovery(recorder *EnhancedHistoryRecorder) (bool, string) {
	ops := recorder.GetFailureRecoveryOperations()
	if len(ops) == 0 {
		return true, "no failure recovery operations to verify"
	}

	// 转换为失败恢复模型可用的格式
	frOps := make([]porcupine.Operation, len(ops))
	for i, op := range ops {
		input := op.Input.(EnhancedInput)
		output := op.Output.(EnhancedOutput)
		frOps[i] = porcupine.Operation{
			ClientId: op.ClientId,
			Input:    input.FailureRecoveryOp,
			Output:   output.FailureRecoveryOut,
			Call:     op.Call,
			Return:   op.Return,
		}
	}

	model := FailureRecoveryModel()
	result := porcupine.CheckOperations(model, frOps)
	if result {
		return true, "failure recovery model verification passed"
	}
	return false, "failure recovery model verification failed"
}

// VerifyLeaderHA 验证 Leader HA 操作
func VerifyLeaderHA(recorder *EnhancedHistoryRecorder) (bool, string) {
	ops := recorder.GetLeaderHAOperations()
	if len(ops) == 0 {
		return true, "no leader HA operations to verify"
	}

	// 转换为 Leader HA 模型可用的格式
	leaderOps := make([]porcupine.Operation, len(ops))
	for i, op := range ops {
		input := op.Input.(EnhancedInput)
		output := op.Output.(EnhancedOutput)
		leaderOps[i] = porcupine.Operation{
			ClientId: op.ClientId,
			Input:    input.LeaderHAOp,
			Output:   output.LeaderHAOut,
			Call:     op.Call,
			Return:   op.Return,
		}
	}

	model := LeaderHAModel()
	result := porcupine.CheckOperations(model, leaderOps)
	if result {
		return true, "leader HA model verification passed"
	}
	return false, "leader HA model verification failed"
}

// VerifyAll 验证所有类型的操作
func VerifyAll(recorder *EnhancedHistoryRecorder) (bool, []string) {
	var messages []string
	allPassed := true

	// 验证拓扑感知
	if passed, msg := VerifyTopology(recorder); !passed {
		allPassed = false
		messages = append(messages, "[FAIL] Topology: "+msg)
	} else if recorder.Stats().ByType[OpTypeTopology] > 0 {
		messages = append(messages, "[PASS] Topology: "+msg)
	}

	// 验证失败恢复
	if passed, msg := VerifyFailureRecovery(recorder); !passed {
		allPassed = false
		messages = append(messages, "[FAIL] FailureRecovery: "+msg)
	} else if recorder.Stats().ByType[OpTypeFailureRecovery] > 0 {
		messages = append(messages, "[PASS] FailureRecovery: "+msg)
	}

	// 验证 Leader HA
	if passed, msg := VerifyLeaderHA(recorder); !passed {
		allPassed = false
		messages = append(messages, "[FAIL] LeaderHA: "+msg)
	} else if recorder.Stats().ByType[OpTypeLeaderHA] > 0 {
		messages = append(messages, "[PASS] LeaderHA: "+msg)
	}

	return allPassed, messages
}
