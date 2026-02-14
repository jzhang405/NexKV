// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件定义失败恢复增强模型，用于验证节点故障场景下的正确性
package porcupine

import (
	"fmt"

	"github.com/anishathalye/porcupine"
)

// ==================== 失败恢复状态定义 ====================

// FailureRecoveryState 失败恢复状态
type FailureRecoveryState struct {
	NodeStores     map[string]map[string]VersionedValue // 每个节点的存储
	FailedNodes    map[string]bool                      // 故障节点
	RecoveredNodes map[string]bool                      // 恢复节点
}

// Clone 克隆 FailureRecoveryState
func (s *FailureRecoveryState) Clone() *FailureRecoveryState {
	return &FailureRecoveryState{
		NodeStores:     CloneNodeStores(s.NodeStores),
		FailedNodes:    CloneBoolMap(s.FailedNodes),
		RecoveredNodes: CloneBoolMap(s.RecoveredNodes),
	}
}

// ==================== 失败恢复操作定义 ====================

// FailureRecoveryOpType 失败恢复操作类型
type FailureRecoveryOpType int

const (
	// FailureRecoveryOpInit 初始化
	FailureRecoveryOpInit FailureRecoveryOpType = iota
	// FailureRecoveryOpNodeFail 节点故障
	FailureRecoveryOpNodeFail
	// FailureRecoveryOpNodeRecover 节点恢复
	FailureRecoveryOpNodeRecover
	// FailureRecoveryOpQuorumWrite Quorum 写入
	FailureRecoveryOpQuorumWrite
	// FailureRecoveryOpGet 读取
	FailureRecoveryOpGet
)

// String 返回操作类型的字符串表示
func (op FailureRecoveryOpType) String() string {
	switch op {
	case FailureRecoveryOpInit:
		return "Init"
	case FailureRecoveryOpNodeFail:
		return "NodeFail"
	case FailureRecoveryOpNodeRecover:
		return "NodeRecover"
	case FailureRecoveryOpQuorumWrite:
		return "QuorumWrite"
	case FailureRecoveryOpGet:
		return "Get"
	default:
		return fmt.Sprintf("Unknown(%d)", op)
	}
}

// FailureRecoveryOperation 失败恢复操作
type FailureRecoveryOperation struct {
	Type         FailureRecoveryOpType // 操作类型
	NodeID       string                // 节点 ID
	Key          string                // 键
	Value        []byte                // 值
	Version      uint64                // 版本号
	Participants []string              // 参与者列表
	FailedNodes  []string              // 故障节点列表（用于初始化）
	AllNodes     []string              // 所有节点（用于初始化）
}

// FailureRecoveryOutput 失败恢复操作输出
type FailureRecoveryOutput struct {
	Ok      bool   // 操作是否成功
	Value   []byte // 返回值（Get 操作）
	Version uint64 // 版本号
	Error   string // 错误信息
}

// ==================== 处理函数 ====================

// handleFRInit 处理初始化
func handleFRInit(st *FailureRecoveryState, op FailureRecoveryOperation, output FailureRecoveryOutput) []interface{} {
	if len(op.AllNodes) == 0 {
		if output.Ok {
			return []interface{}{st}
		}
		return nil
	}

	// 初始化节点存储
	nodeStores := make(map[string]map[string]VersionedValue)
	for _, nodeID := range op.AllNodes {
		nodeStores[nodeID] = make(map[string]VersionedValue)
	}

	// 初始化故障节点
	failedNodes := make(map[string]bool)
	for _, nodeID := range op.FailedNodes {
		failedNodes[nodeID] = true
	}

	newState := &FailureRecoveryState{
		NodeStores:     nodeStores,
		FailedNodes:    failedNodes,
		RecoveredNodes: make(map[string]bool),
	}

	if output.Ok {
		return []interface{}{newState}
	}
	return nil
}

// handleNodeFail 处理节点故障
func handleNodeFail(st *FailureRecoveryState, op FailureRecoveryOperation, output FailureRecoveryOutput) []interface{} {
	if op.NodeID == "" {
		return nil
	}

	newSt := st.Clone()
	newSt.FailedNodes[op.NodeID] = true
	delete(newSt.RecoveredNodes, op.NodeID)

	if output.Ok {
		return []interface{}{newSt}
	}
	return nil
}

// handleNodeRecover 处理节点恢复
func handleNodeRecover(st *FailureRecoveryState, op FailureRecoveryOperation, output FailureRecoveryOutput) []interface{} {
	if op.NodeID == "" {
		return nil
	}

	// 检查节点是否在故障列表中
	if !st.FailedNodes[op.NodeID] {
		// 节点未故障，无需恢复
		if output.Ok {
			return []interface{}{st}
		}
		return nil
	}

	newSt := st.Clone()
	delete(newSt.FailedNodes, op.NodeID)
	newSt.RecoveredNodes[op.NodeID] = true

	if output.Ok {
		return []interface{}{newSt}
	}
	return nil
}

// handleQuorumWithFailure 处理带故障的 Quorum 写入
// P1-01: 添加失败回滚逻辑
func handleQuorumWithFailure(st *FailureRecoveryState, op FailureRecoveryOperation, output FailureRecoveryOutput) []interface{} {
	// 过滤故障节点
	var healthyParticipants []string
	for _, pID := range op.Participants {
		if !st.FailedNodes[pID] {
			healthyParticipants = append(healthyParticipants, pID)
		}
	}

	quorum := (len(op.Participants) / 2) + 1
	if quorum < 1 {
		quorum = 1
	}

	if len(healthyParticipants) < quorum {
		// P1-01: Quorum 不可达时返回失败状态，不修改任何状态
		if output.Error == "quorum_failed" {
			return []interface{}{st} // 返回原状态（回滚）
		}
		return nil
	}

	// 执行写入
	newSt := st.Clone()
	for _, pID := range healthyParticipants {
		if newSt.NodeStores[pID] == nil {
			newSt.NodeStores[pID] = make(map[string]VersionedValue)
		}
		store := newSt.NodeStores[pID]
		// P1-01: 检查版本冲突
		if existing, exists := store[op.Key]; exists && existing.Version >= op.Version {
			// 版本冲突，回滚到原状态
			return []interface{}{st}
		}
		store[op.Key] = VersionedValue{Value: op.Value, Version: op.Version}
	}

	// 成功返回新状态
	if output.Ok {
		return []interface{}{newSt}
	}
	// 输出不匹配，回滚
	return []interface{}{st}
}

// handleFRGet 处理失败恢复场景下的读取
func handleFRGet(st *FailureRecoveryState, op FailureRecoveryOperation, output FailureRecoveryOutput) []interface{} {
	// 检查节点是否故障
	if st.FailedNodes[op.NodeID] {
		if output.Error == "node_failed" {
			return []interface{}{st}
		}
		return nil
	}

	store, exists := st.NodeStores[op.NodeID]
	if !exists {
		if output.Error == "key not found" || output.Error == "node not found" {
			return []interface{}{st}
		}
		return nil
	}

	value, exists := store[op.Key]
	if !exists {
		if output.Error == "key not found" {
			return []interface{}{st}
		}
		return nil
	}

	// 验证返回值
	if !output.Ok {
		return nil
	}
	if string(output.Value) != string(value.Value) {
		return nil
	}
	if output.Version != value.Version {
		return nil
	}

	return []interface{}{st}
}

// ==================== 状态比较 ====================

// failureRecoveryStateEqual 深度比较两个失败恢复状态
func failureRecoveryStateEqual(state1, state2 interface{}) bool {
	s1, ok1 := state1.(*FailureRecoveryState)
	s2, ok2 := state2.(*FailureRecoveryState)
	if !ok1 || !ok2 {
		return false
	}

	// 比较 NodeStores
	if !NodeStoresEqual(s1.NodeStores, s2.NodeStores) {
		return false
	}

	// 比较 FailedNodes
	if !BoolMapEqual(s1.FailedNodes, s2.FailedNodes) {
		return false
	}

	// 比较 RecoveredNodes
	return BoolMapEqual(s1.RecoveredNodes, s2.RecoveredNodes)
}

// ==================== 模型定义 ====================

// newFailureRecoveryNondeterministicModel 创建非确定性失败恢复模型
func newFailureRecoveryNondeterministicModel() *porcupine.NondeterministicModel {
	return &porcupine.NondeterministicModel{
		// Init 返回 []interface{}
		Init: func() []interface{} {
			return []interface{}{&FailureRecoveryState{
				NodeStores:     make(map[string]map[string]VersionedValue),
				FailedNodes:    make(map[string]bool),
				RecoveredNodes: make(map[string]bool),
			}}
		},

		// Step 返回所有可能的状态
		Step: func(state, input, output interface{}) []interface{} {
			st, ok := state.(*FailureRecoveryState)
			if !ok {
				return nil
			}

			op, ok := input.(FailureRecoveryOperation)
			if !ok {
				return nil
			}

			out, ok := output.(FailureRecoveryOutput)
			if !ok {
				return nil
			}

			// 根据操作类型分发
			switch op.Type {
			case FailureRecoveryOpInit:
				return handleFRInit(st, op, out)
			case FailureRecoveryOpNodeFail:
				return handleNodeFail(st, op, out)
			case FailureRecoveryOpNodeRecover:
				return handleNodeRecover(st, op, out)
			case FailureRecoveryOpQuorumWrite:
				return handleQuorumWithFailure(st, op, out)
			case FailureRecoveryOpGet:
				return handleFRGet(st, op, out)
			default:
				return nil
			}
		},

		// Equal 状态比较函数
		Equal: failureRecoveryStateEqual,

		// DescribeOperation 描述操作（用于可视化）
		DescribeOperation: func(input, output interface{}) string {
			op, _ := input.(FailureRecoveryOperation)
			out, _ := output.(FailureRecoveryOutput)
			return fmt.Sprintf("%s -> ok=%v", op.Type, out.Ok)
		},

		// DescribeState 描述状态（用于可视化）
		DescribeState: func(state interface{}) string {
			st, _ := state.(*FailureRecoveryState)
			if st == nil {
				return "<nil>"
			}
			return fmt.Sprintf("nodes=%d,failed=%d,recovered=%d",
				len(st.NodeStores), len(st.FailedNodes), len(st.RecoveredNodes))
		},
	}
}

// FailureRecoveryModel 创建失败恢复模型
func FailureRecoveryModel() porcupine.Model {
	nm := newFailureRecoveryNondeterministicModel()
	return nm.ToModel()
}
