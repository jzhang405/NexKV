// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件实现历史记录器，用于记录操作事件
package porcupine

import (
	"sync"

	"github.com/anishathalye/porcupine"
)

// pendingOp 待完成的操作
type pendingOp struct {
	input NexKVInput
	call  int64 // 调用时间戳
}

// HistoryRecorder 历史记录器
// 记录所有操作的 Call/Return 事件，用于线性一致性验证
type HistoryRecorder struct {
	mu        sync.Mutex
	clientID  int                   // 客户端 ID
	timestamp TimestampGenerator    // 时间戳生成器
	ops       []porcupine.Operation // 已完成的操作列表
	pending   map[int]pendingOp     // 待完成的操作（opID -> pendingOp）
	opID      int                   // 下一个操作 ID（原子递增）
}

// NewHistoryRecorder 创建历史记录器
// clientID: 客户端唯一标识（用于区分不同客户端的操作）
// timestamp: 时间戳生成器（用于生成事件时间戳）
func NewHistoryRecorder(clientID string, timestamp TimestampGenerator) *HistoryRecorder {
	return &HistoryRecorder{
		clientID:  extractClientID(clientID),
		timestamp: timestamp,
		ops:       make([]porcupine.Operation, 0),
		pending:   make(map[int]pendingOp),
		opID:      0,
	}
}

// RecordCall 记录操作调用（Call 事件）
// 参数:
//   - op: 操作类型
//   - ns: 命名空间
//   - key: 键
//   - value: 值（仅 Put 操作使用）
//
// 返回: 操作 ID（用于关联 Return 事件）
func (r *HistoryRecorder) RecordCall(op OpType, ns, key string, value []byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 分配操作 ID
	opID := r.opID
	r.opID++

	// 保存待完成的操作
	input := NexKVInput{
		Op:        op,
		Namespace: ns,
		Key:       key,
		Value:     value,
	}
	r.pending[opID] = pendingOp{
		input: input,
		call:  r.timestamp.Now(),
	}

	return opID
}

// RecordReturn 记录操作返回（Return 事件）
// 参数:
//   - opID: 操作 ID（由 RecordCall 返回）
//   - ok: 操作是否成功
//   - value: 返回值（仅 Get 操作使用）
//   - errMsg: 错误信息（操作失败时）
func (r *HistoryRecorder) RecordReturn(opID int, ok bool, value []byte, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 获取待完成的操作
	pending, exists := r.pending[opID]
	if !exists {
		return // 忽略不存在的操作
	}
	delete(r.pending, opID)

	// 创建输出
	output := NexKVOutput{
		Ok:    ok,
		Value: value,
		Error: errMsg,
	}

	// 创建完整的 Operation
	op := porcupine.Operation{
		ClientId: r.clientID,
		Input:    pending.input,
		Call:     pending.call,
		Output:   output,
		Return:   r.timestamp.Now(),
	}
	r.ops = append(r.ops, op)
}

// GetOperations 获取所有已完成的操作
// 返回操作列表的副本，避免并发问题
func (r *HistoryRecorder) GetOperations() []porcupine.Operation {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 返回副本
	ops := make([]porcupine.Operation, len(r.ops))
	copy(ops, r.ops)
	return ops
}

// GetEvents 获取所有记录的事件（转换为 Event 格式）
// 每个操作转换为 Call + Return 两个事件
func (r *HistoryRecorder) GetEvents() []porcupine.Event {
	ops := r.GetOperations()
	events := make([]porcupine.Event, 0, len(ops)*2)

	for i, op := range ops {
		// Call 事件
		events = append(events, porcupine.Event{
			Kind:     porcupine.CallEvent,
			ClientId: op.ClientId,
			Id:       i, // 使用索引作为 ID
			Value:    op.Input,
		})
		// Return 事件
		events = append(events, porcupine.Event{
			Kind:     porcupine.ReturnEvent,
			ClientId: op.ClientId,
			Id:       i, // 使用相同的 ID 匹配 Call
			Value:    op.Output,
		})
	}

	return events
}

// GetInput 获取指定操作 ID 的输入
// 如果操作 ID 不存在或已完成，返回 nil
func (r *HistoryRecorder) GetInput(opID int) *NexKVInput {
	r.mu.Lock()
	defer r.mu.Unlock()

	if pending, ok := r.pending[opID]; ok {
		return &pending.input
	}
	return nil
}

// Clear 清空所有记录的操作
// 用于增量检查模式，定期清空历史
func (r *HistoryRecorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ops = make([]porcupine.Operation, 0)
	r.pending = make(map[int]pendingOp)
}

// ClientID 获取客户端 ID
func (r *HistoryRecorder) ClientID() int {
	return r.clientID
}

// Len 获取已完成的操作数量
func (r *HistoryRecorder) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ops)
}
