// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件定义 Leader HA 增强模型，用于验证 Leader 切换和 Fencing Token
package porcupine

import (
	"fmt"
	"sort"

	"github.com/anishathalye/porcupine"
)

// ==================== Leader HA 状态定义 ====================

// LeaderHAState Leader HA 状态
type LeaderHAState struct {
	NodeStores     map[string]map[string]VersionedValue // 每个节点的存储
	Topology       *Topology                            // 拓扑信息（immutable，共享引用）
	ActiveLeader   string                               // 当前 Active Leader（父节点 ID 最小者）
	StandbyLeaders []string                             // Standby Leader 列表（按优先级排序）
	CurrentTerm    uint64                               // 当前任期（Fencing Token）
}

// Clone 克隆 LeaderHAState
// 注意：此方法不是并发安全的。如果需要在并发环境中使用，
// 调用方需要自行加锁保护 LeaderHAState 的访问。
func (s *LeaderHAState) Clone() *LeaderHAState {
	return &LeaderHAState{
		NodeStores:     CloneNodeStores(s.NodeStores),
		Topology:       s.Topology, // 共享引用，拓扑 immutable
		ActiveLeader:   s.ActiveLeader,
		StandbyLeaders: CloneStringSlice(s.StandbyLeaders),
		CurrentTerm:    s.CurrentTerm,
	}
}

// NewLeaderHAState 初始化 Leader HA 状态（P1-05: 补充初始化逻辑）
func NewLeaderHAState(topology *Topology) *LeaderHAState {
	if topology == nil {
		return &LeaderHAState{
			NodeStores:  make(map[string]map[string]VersionedValue),
			Topology:    nil,
			CurrentTerm: 1,
		}
	}

	// 1. 收集所有父节点（有子节点的节点）
	parentNodes := make([]string, 0)
	for nodeID, node := range topology.Nodes {
		if len(node.Children) > 0 {
			parentNodes = append(parentNodes, nodeID)
		}
	}

	// 2. 按节点 ID 排序（确定性 Leader 选举，ID 最小者为 Active）
	sort.Strings(parentNodes)

	// 3. 初始化节点存储
	nodeStores := make(map[string]map[string]VersionedValue)
	for nodeID := range topology.Nodes {
		nodeStores[nodeID] = make(map[string]VersionedValue)
	}

	// 4. 第一个为 Active，其余为 Standby
	if len(parentNodes) == 0 {
		return &LeaderHAState{
			NodeStores:  nodeStores,
			Topology:    topology,
			CurrentTerm: 1,
		}
	}

	return &LeaderHAState{
		NodeStores:     nodeStores,
		Topology:       topology,
		ActiveLeader:   parentNodes[0],
		StandbyLeaders: parentNodes[1:],
		CurrentTerm:    1,
	}
}

// GetActiveLeader 获取 Active Leader
// P1-03: 添加基于拓扑的自动计算逻辑
func (s *LeaderHAState) GetActiveLeader() string {
	if s.ActiveLeader != "" {
		return s.ActiveLeader
	}
	// 如果 ActiveLeader 为空，基于拓扑重新计算
	return s.computeActiveLeader()
}

// computeActiveLeader 基于拓扑计算 Active Leader（P1-03）
func (s *LeaderHAState) computeActiveLeader() string {
	if s.Topology == nil {
		return ""
	}

	parentNodes := make([]string, 0)
	for nodeID, node := range s.Topology.Nodes {
		if len(node.Children) > 0 {
			parentNodes = append(parentNodes, nodeID)
		}
	}
	sort.Strings(parentNodes)
	if len(parentNodes) > 0 {
		s.ActiveLeader = parentNodes[0]
		s.StandbyLeaders = parentNodes[1:]
	}
	return s.ActiveLeader
}

// HandleLeaderFailover 处理 Leader 故障转移
func (s *LeaderHAState) HandleLeaderFailover(failedLeader string) string {
	// 从 Standby 列表中选择下一个作为 Active Leader
	for i, standby := range s.StandbyLeaders {
		if standby != failedLeader {
			s.ActiveLeader = standby
			s.StandbyLeaders = s.StandbyLeaders[i+1:]
			s.CurrentTerm++ // Term 递增
			return s.ActiveLeader
		}
	}
	return "" // 无可用 Standby
}

// ==================== Leader HA 操作定义 ====================

// LeaderHAOpType Leader HA 操作类型
type LeaderHAOpType int

const (
	// LeaderHAOpInit 初始化
	LeaderHAOpInit LeaderHAOpType = iota
	// LeaderHAOpLeaderChange Leader 切换
	LeaderHAOpLeaderChange
	// LeaderHAOpWrite 写入（带 Term 验证）
	LeaderHAOpWrite
	// LeaderHAOpGet 读取
	LeaderHAOpGet
)

// String 返回操作类型的字符串表示
func (op LeaderHAOpType) String() string {
	switch op {
	case LeaderHAOpInit:
		return "Init"
	case LeaderHAOpLeaderChange:
		return "LeaderChange"
	case LeaderHAOpWrite:
		return "Write"
	case LeaderHAOpGet:
		return "Get"
	default:
		return fmt.Sprintf("Unknown(%d)", op)
	}
}

// LeaderHAOperation Leader HA 操作
type LeaderHAOperation struct {
	Type      LeaderHAOpType // 操作类型
	NodeID    string         // 节点 ID
	Key       string         // 键
	Value     []byte         // 值
	Version   uint64         // 版本号
	Term      uint64         // 任期（用于 Fencing Token）
	Nodes     []*NodeInfo    // 节点列表（用于初始化拓扑）
	NewLeader string         // 新 Leader（用于 Leader 切换）
}

// LeaderHAOutput Leader HA 操作输出
type LeaderHAOutput struct {
	Ok         bool   // 操作是否成功
	Value      []byte // 返回值（Get 操作）
	Version    uint64 // 版本号
	Term       uint64 // 当前 Term
	Error      string // 错误信息
	NewLeader  string // 新 Leader（Leader 切换）
	ActiveTerm uint64 // Active Term
}

// ==================== 处理函数 ====================

// handleLeaderHAInit 处理初始化
func handleLeaderHAInit(st *LeaderHAState, op LeaderHAOperation, output LeaderHAOutput) []any {
	// 构建拓扑
	topology := &Topology{
		Nodes:    make(map[string]*NodeInfo),
		ParentOf: make(map[string][]string),
		ChildOf:  make(map[string]string),
	}

	for _, node := range op.Nodes {
		topology.Nodes[node.NodeID] = node
		if node.ParentID != "" {
			topology.ChildOf[node.NodeID] = node.ParentID
			topology.ParentOf[node.ParentID] = append(topology.ParentOf[node.ParentID], node.NodeID)
		}
	}

	// 使用 NewLeaderHAState 初始化
	newState := NewLeaderHAState(topology)

	if output.Ok {
		// 验证输出中的 Term
		if output.Term > 0 && output.Term != newState.CurrentTerm {
			return nil
		}
		return []any{newState}
	}
	return nil
}

// handleLeaderChange 处理 Leader 切换
func handleLeaderChange(st *LeaderHAState, op LeaderHAOperation, output LeaderHAOutput) []any {
	// 验证新 Leader 的 Term
	if op.Term < st.CurrentTerm {
		// Term 回退，拒绝
		if output.Error == "stale_term" {
			return []any{st}
		}
		return nil
	}

	newSt := st.Clone()

	// 执行故障转移
	var newLeader string
	if op.NewLeader != "" {
		// 指定新 Leader
		newLeader = op.NewLeader
	} else {
		// 自动选择下一个 Standby
		newLeader = newSt.HandleLeaderFailover(st.ActiveLeader)
	}

	if newLeader == "" {
		// 无可用 Standby
		if output.Error == "no_standby" {
			return []any{st}
		}
		return nil
	}

	// 更新 Leader 状态
	newSt.ActiveLeader = newLeader
	// 从 Standby 列表中移除新 Leader
	newStandby := make([]string, 0)
	for _, standby := range newSt.StandbyLeaders {
		if standby != newLeader {
			newStandby = append(newStandby, standby)
		}
	}
	// 如果旧 Leader 仍在拓扑中，将其加入 Standby
	if st.ActiveLeader != "" && st.ActiveLeader != newLeader {
		newStandby = append(newStandby, st.ActiveLeader)
	}
	newSt.StandbyLeaders = newStandby
	newSt.CurrentTerm++

	// 验证输出
	if !output.Ok {
		return nil
	}
	if output.NewLeader != newLeader {
		return nil
	}
	if output.ActiveTerm != newSt.CurrentTerm {
		return nil
	}

	return []any{newSt}
}

// handleLeaderHAWrite 处理带 Term 验证的写入
func handleLeaderHAWrite(st *LeaderHAState, op LeaderHAOperation, output LeaderHAOutput) []any {
	// Fencing Token 验证
	if op.Term < st.CurrentTerm {
		// 旧 Leader 尝试写入，拒绝
		if output.Error == "stale_term" {
			return []any{st}
		}
		return nil
	}

	// 只有 Active Leader 可以写入
	if op.NodeID != st.ActiveLeader {
		if output.Error == "not_leader" {
			return []any{st}
		}
		return nil
	}

	// 检查节点存储
	if st.NodeStores[op.NodeID] == nil {
		st.NodeStores[op.NodeID] = make(map[string]VersionedValue)
	}

	// 版本检查
	if existing, exists := st.NodeStores[op.NodeID][op.Key]; exists {
		if existing.Version >= op.Version {
			// 版本冲突
			if output.Error == "version_conflict" {
				return []any{st}
			}
			return nil
		}
	}

	// 执行写入
	newSt := st.Clone()
	if newSt.NodeStores[op.NodeID] == nil {
		newSt.NodeStores[op.NodeID] = make(map[string]VersionedValue)
	}
	newSt.NodeStores[op.NodeID][op.Key] = VersionedValue{
		Value:   op.Value,
		Version: op.Version,
	}

	if output.Ok {
		return []any{newSt}
	}
	return nil
}

// handleLeaderHAGet 处理读取
func handleLeaderHAGet(st *LeaderHAState, op LeaderHAOperation, output LeaderHAOutput) []any {
	// 检查节点存储
	store, exists := st.NodeStores[op.NodeID]
	if !exists {
		if output.Error == "node not found" {
			return []any{st}
		}
		return nil
	}

	value, exists := store[op.Key]
	if !exists {
		if output.Error == "key not found" {
			return []any{st}
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

	return []any{st}
}

// ==================== 状态比较 ====================

// leaderHAStateEqual 深度比较两个 Leader HA 状态
func leaderHAStateEqual(state1, state2 any) bool {
	s1, ok1 := state1.(*LeaderHAState)
	s2, ok2 := state2.(*LeaderHAState)
	if !ok1 || !ok2 {
		return false
	}

	// 比较 NodeStores
	if !NodeStoresEqual(s1.NodeStores, s2.NodeStores) {
		return false
	}

	// 比较其他字段
	return s1.ActiveLeader == s2.ActiveLeader &&
		StringSliceEqual(s1.StandbyLeaders, s2.StandbyLeaders) &&
		s1.CurrentTerm == s2.CurrentTerm
}

// ==================== 模型定义 ====================

// newLeaderHANondeterministicModel 创建非确定性 Leader HA 模型
func newLeaderHANondeterministicModel() *porcupine.NondeterministicModel {
	return &porcupine.NondeterministicModel{
		// Init 返回 []any
		Init: func() []any {
			return []any{&LeaderHAState{
				NodeStores:     make(map[string]map[string]VersionedValue),
				Topology:       nil,
				ActiveLeader:   "",
				StandbyLeaders: []string{},
				CurrentTerm:    0,
			}}
		},

		// Step 返回所有可能的状态
		Step: func(state, input, output any) []any {
			st, ok := state.(*LeaderHAState)
			if !ok {
				return nil
			}

			op, ok := input.(LeaderHAOperation)
			if !ok {
				return nil
			}

			out, ok := output.(LeaderHAOutput)
			if !ok {
				return nil
			}

			// 根据操作类型分发
			switch op.Type {
			case LeaderHAOpInit:
				return handleLeaderHAInit(st, op, out)
			case LeaderHAOpLeaderChange:
				return handleLeaderChange(st, op, out)
			case LeaderHAOpWrite:
				return handleLeaderHAWrite(st, op, out)
			case LeaderHAOpGet:
				return handleLeaderHAGet(st, op, out)
			default:
				return nil
			}
		},

		// Equal 状态比较函数
		Equal: leaderHAStateEqual,

		// DescribeOperation 描述操作（用于可视化）
		DescribeOperation: func(input, output any) string {
			op, _ := input.(LeaderHAOperation)
			out, _ := output.(LeaderHAOutput)
			return fmt.Sprintf("%s(node=%s,term=%d) -> ok=%v,term=%d",
				op.Type, op.NodeID, op.Term, out.Ok, out.ActiveTerm)
		},

		// DescribeState 描述状态（用于可视化）
		DescribeState: func(state any) string {
			st, _ := state.(*LeaderHAState)
			if st == nil {
				return "<nil>"
			}
			return fmt.Sprintf("leader=%s,term=%d,standby=%v",
				st.ActiveLeader, st.CurrentTerm, st.StandbyLeaders)
		},
	}
}

// LeaderHAModel 创建 Leader HA 模型
func LeaderHAModel() porcupine.Model {
	nm := newLeaderHANondeterministicModel()
	return nm.ToModel()
}
