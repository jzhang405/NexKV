// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件定义拓扑感知增强模型，用于验证树拓扑相关的 2PC/Quorum/Gossip 操作
package porcupine

import (
	"fmt"
	"sort"
	"time"

	"github.com/anishathalye/porcupine"
)

// ==================== 节点类型定义 ====================

// NodeType 节点类型（树感知 Gossip 核心）
type NodeType int

const (
	// NodeTypeUnknown 未知类型
	NodeTypeUnknown NodeType = iota
	// NodeTypeLeaf 叶子节点：只发父节点，带宽最低，延迟最低
	NodeTypeLeaf
	// NodeTypeMiddle 中间节点：向上发父节点 + 向下广播子节点
	NodeTypeMiddle
	// NodeTypeRoot Root 节点：只广播子节点，带宽省，延迟最高
	NodeTypeRoot
)

// String 返回节点类型的字符串表示
func (t NodeType) String() string {
	switch t {
	case NodeTypeLeaf:
		return "leaf"
	case NodeTypeMiddle:
		return "middle"
	case NodeTypeRoot:
		return "root"
	default:
		return "unknown"
	}
}

// ==================== 拓扑信息定义 ====================

// NodeInfo 节点信息
type NodeInfo struct {
	NodeID    string   // 节点 ID
	ParentID  string   // 父节点 ID（Root 为空）
	Children  []string // 子节点列表（叶子为空）
	TreeDepth int      // 树深度
}

// Topology 拓扑信息
// 注意：拓扑在运行时不变（immutable），可安全共享引用
type Topology struct {
	Nodes    map[string]*NodeInfo // 节点信息
	ParentOf map[string][]string  // 父节点 -> 子节点列表
	ChildOf  map[string]string    // 子节点 -> 父节点
}

// GetNodeType 获取节点类型
func (t *Topology) GetNodeType(nodeID string) NodeType {
	// 防御性检查
	if t == nil || t.Nodes == nil {
		return NodeTypeUnknown
	}

	node, exists := t.Nodes[nodeID]
	if !exists {
		return NodeTypeUnknown
	}

	hasParent := node.ParentID != ""
	hasChildren := len(node.Children) > 0

	if !hasParent && hasChildren {
		return NodeTypeRoot
	}
	if hasParent && !hasChildren {
		return NodeTypeLeaf
	}
	if hasParent && hasChildren {
		return NodeTypeMiddle
	}
	return NodeTypeUnknown
}

// GetMaxTreeDepth 获取最大树深度
func (t *Topology) GetMaxTreeDepth() int {
	maxDepth := 0
	for _, node := range t.Nodes {
		if node.TreeDepth > maxDepth {
			maxDepth = node.TreeDepth
		}
	}
	return maxDepth
}

// GetNodeDepth 获取节点深度
func (t *Topology) GetNodeDepth(nodeID string) int {
	node, exists := t.Nodes[nodeID]
	if !exists {
		return 0
	}
	return node.TreeDepth
}

// ==================== 版本化值定义 ====================

// VersionedValue 带版本号的值
type VersionedValue struct {
	Value   []byte // 值
	Version uint64 // 版本号（用于冲突检测）
}

// Clone 克隆版本化值
func (v VersionedValue) Clone() VersionedValue {
	newValue := make([]byte, len(v.Value))
	copy(newValue, v.Value)
	return VersionedValue{
		Value:   newValue,
		Version: v.Version,
	}
}

// ==================== 拓扑状态定义 ====================

// TopologyState 拓扑状态
type TopologyState struct {
	NodeStores    map[string]map[string]VersionedValue // 每个节点的存储
	Topology      *Topology                            // 拓扑信息（immutable，共享引用）
	CurrentLeader string                               // 当前 Leader
	CurrentTerm   uint64                               // 当前任期
}

// Clone 克隆 TopologyState
// 注意：Topology 字段共享引用，假设拓扑在运行时不变（immutable）
func (s *TopologyState) Clone() *TopologyState {
	return &TopologyState{
		NodeStores:    CloneNodeStores(s.NodeStores),
		Topology:      s.Topology, // 共享引用，拓扑 immutable
		CurrentLeader: s.CurrentLeader,
		CurrentTerm:   s.CurrentTerm,
	}
}

// ==================== 拓扑操作定义 ====================

// TopologyOpType 拓扑操作类型
type TopologyOpType int

const (
	// TopologyOpInitTopology 初始化拓扑
	TopologyOpInitTopology TopologyOpType = iota
	// TopologyOpWrite2PC 2PC 写入
	TopologyOpWrite2PC
	// TopologyOpWriteQuorum Quorum 写入
	TopologyOpWriteQuorum
	// TopologyOpWriteGossip Gossip 写入
	TopologyOpWriteGossip
	// TopologyOpGet 读取
	TopologyOpGet
)

// String 返回操作类型的字符串表示
func (op TopologyOpType) String() string {
	switch op {
	case TopologyOpInitTopology:
		return "InitTopology"
	case TopologyOpWrite2PC:
		return "Write2PC"
	case TopologyOpWriteQuorum:
		return "WriteQuorum"
	case TopologyOpWriteGossip:
		return "WriteGossip"
	case TopologyOpGet:
		return "Get"
	default:
		return fmt.Sprintf("Unknown(%d)", op)
	}
}

// TopologyOperation 拓扑操作
type TopologyOperation struct {
	Type         TopologyOpType // 操作类型
	NodeID       string         // 节点 ID
	Key          string         // 键
	Value        []byte         // 值
	Version      uint64         // 版本号
	Term         uint64         // 任期
	Participants []string       // 参与者列表
	Nodes        []*NodeInfo    // 节点列表（用于初始化拓扑）
}

// TopologyOutput 拓扑操作输出
type TopologyOutput struct {
	Ok      bool   // 操作是否成功
	Value   []byte // 返回值（Get 操作）
	Version uint64 // 版本号
	Error   string // 错误信息
}

// ==================== 延迟计算 ====================

const (
	// DefaultSingleHopDelay 默认单跳延迟（100ms）
	DefaultSingleHopDelay = 100 * time.Millisecond
)

// GetExpectedDelay 获取预期延迟（与 tree_aware.go 实现一致）
// 公式: delay = treeDepth * 100ms
// 此方法通常用于 Root 节点估算等待向上传播完成的时间
func GetExpectedDelay(treeDepth int) time.Duration {
	return time.Duration(treeDepth) * DefaultSingleHopDelay
}

// GetNodeExpectedDelay 计算特定节点的预期延迟
// 根据节点类型返回不同的延迟值
func GetNodeExpectedDelay(topology *Topology, nodeID string) time.Duration {
	if topology == nil {
		return 0
	}

	nodeType := topology.GetNodeType(nodeID)
	depth := topology.GetNodeDepth(nodeID)

	switch nodeType {
	case NodeTypeLeaf:
		// 叶子节点：本地产生，延迟为 0
		return 0
	case NodeTypeMiddle:
		// 中间节点：取决于深度
		return time.Duration(depth) * DefaultSingleHopDelay
	case NodeTypeRoot:
		// Root 节点：需等待所有向上传播，延迟最高
		maxDepth := topology.GetMaxTreeDepth()
		return time.Duration(maxDepth) * DefaultSingleHopDelay
	default:
		return 0
	}
}

// ==================== 处理函数 ====================

// handleInitTopology 处理拓扑初始化
func handleInitTopology(st *TopologyState, op TopologyOperation, output TopologyOutput) []interface{} {
	if len(op.Nodes) == 0 {
		if output.Ok {
			return []interface{}{st}
		}
		return nil
	}

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

	// 初始化节点存储
	nodeStores := make(map[string]map[string]VersionedValue)
	for nodeID := range topology.Nodes {
		nodeStores[nodeID] = make(map[string]VersionedValue)
	}

	newState := &TopologyState{
		NodeStores: nodeStores,
		Topology:   topology,
	}

	// 确定 Leader（父节点 ID 最小者）
	var parentNodes []string
	for nodeID, node := range topology.Nodes {
		if len(node.Children) > 0 {
			parentNodes = append(parentNodes, nodeID)
		}
	}
	sort.Strings(parentNodes)
	if len(parentNodes) > 0 {
		newState.CurrentLeader = parentNodes[0]
		newState.CurrentTerm = 1
	}

	if output.Ok {
		return []interface{}{newState}
	}
	return nil
}

// handleTreeAwareGossip 处理树感知 Gossip 传播
// 核心特性：
// - Leaf 节点：只发父节点
// - Middle 节点：向上发父节点 + 向下广播子节点
// - Root 节点：只广播子节点
func handleTreeAwareGossip(st *TopologyState, op TopologyOperation, output TopologyOutput) []interface{} {
	if st.Topology == nil {
		return nil
	}

	node, exists := st.Topology.Nodes[op.NodeID]
	if !exists {
		// 节点不存在，返回 nil 表示验证失败
		return nil
	}

	newSt := st.Clone()

	// 1. 本地写入
	if newSt.NodeStores[op.NodeID] == nil {
		newSt.NodeStores[op.NodeID] = make(map[string]VersionedValue)
	}
	newSt.NodeStores[op.NodeID][op.Key] = VersionedValue{
		Value:   op.Value,
		Version: op.Version,
	}

	// 2. 根据节点类型传播
	nodeType := st.Topology.GetNodeType(op.NodeID)

	switch nodeType {
	case NodeTypeLeaf:
		// 叶子节点：只向上发父节点
		if node.ParentID != "" {
			if newSt.NodeStores[node.ParentID] == nil {
				newSt.NodeStores[node.ParentID] = make(map[string]VersionedValue)
			}
			newSt.NodeStores[node.ParentID][op.Key] = VersionedValue{
				Value:   op.Value,
				Version: op.Version,
			}
		}

	case NodeTypeMiddle:
		// 中间节点：向上发父节点 + 向下广播子节点
		if node.ParentID != "" {
			if newSt.NodeStores[node.ParentID] == nil {
				newSt.NodeStores[node.ParentID] = make(map[string]VersionedValue)
			}
			newSt.NodeStores[node.ParentID][op.Key] = VersionedValue{
				Value:   op.Value,
				Version: op.Version,
			}
		}
		for _, childID := range node.Children {
			if newSt.NodeStores[childID] == nil {
				newSt.NodeStores[childID] = make(map[string]VersionedValue)
			}
			newSt.NodeStores[childID][op.Key] = VersionedValue{
				Value:   op.Value,
				Version: op.Version,
			}
		}

	case NodeTypeRoot:
		// Root 节点：只向下广播子节点
		for _, childID := range node.Children {
			if newSt.NodeStores[childID] == nil {
				newSt.NodeStores[childID] = make(map[string]VersionedValue)
			}
			newSt.NodeStores[childID][op.Key] = VersionedValue{
				Value:   op.Value,
				Version: op.Version,
			}
		}
	}

	if output.Ok {
		return []interface{}{newSt}
	}
	return nil
}

// handle2PCWrite 处理 2PC 写入（拓扑感知）
// 参与者：本地节点 + 父节点 + 兄弟节点
func handle2PCWrite(st *TopologyState, op TopologyOperation, output TopologyOutput) []interface{} {
	if st.Topology == nil {
		return nil
	}

	node, exists := st.Topology.Nodes[op.NodeID]
	if !exists {
		return nil
	}

	// 计算 2PC 参与者
	participants := []string{op.NodeID}
	if node.ParentID != "" {
		participants = append(participants, node.ParentID)
		// 添加兄弟节点
		for _, sibling := range st.Topology.ParentOf[node.ParentID] {
			if sibling != op.NodeID {
				participants = append(participants, sibling)
			}
		}
	}

	// 验证版本并更新所有参与者
	newSt := st.Clone()
	for _, participantID := range participants {
		if newSt.NodeStores[participantID] == nil {
			newSt.NodeStores[participantID] = make(map[string]VersionedValue)
		}
		store := newSt.NodeStores[participantID]
		if existing, exists := store[op.Key]; exists {
			if existing.Version >= op.Version {
				// 版本冲突，返回原状态
				return []interface{}{st}
			}
		}
		store[op.Key] = VersionedValue{
			Value:   op.Value,
			Version: op.Version,
		}
	}

	// 返回成功状态
	if output.Ok {
		return []interface{}{newSt}
	}
	return []interface{}{st}
}

// handleQuorumWrite 处理 Quorum 写入
func handleQuorumWrite(st *TopologyState, op TopologyOperation, output TopologyOutput) []interface{} {
	if st.Topology == nil {
		return nil
	}

	_, exists := st.Topology.Nodes[op.NodeID]
	if !exists {
		return nil
	}

	// 使用传入的参与者列表
	participants := op.Participants
	if len(participants) == 0 {
		// 如果没有指定参与者，使用所有节点
		for nodeID := range st.Topology.Nodes {
			participants = append(participants, nodeID)
		}
	}

	quorum := (len(participants) / 2) + 1
	if quorum < 1 {
		quorum = 1
	}

	// 特殊处理：空参与者列表
	if len(participants) == 0 {
		if output.Error == "quorum_failed" {
			return []interface{}{st}
		}
		return nil
	}

	// 执行 Quorum 写入
	newSt := st.Clone()
	successCount := 0
	for _, participantID := range participants {
		if newSt.NodeStores[participantID] == nil {
			newSt.NodeStores[participantID] = make(map[string]VersionedValue)
		}
		store := newSt.NodeStores[participantID]
		if existing, exists := store[op.Key]; exists {
			if existing.Version >= op.Version {
				continue // 版本冲突，跳过此节点
			}
		}
		store[op.Key] = VersionedValue{
			Value:   op.Value,
			Version: op.Version,
		}
		successCount++
	}

	if successCount >= quorum && output.Ok {
		return []interface{}{newSt}
	}
	if successCount < quorum && output.Error == "quorum_failed" {
		return []interface{}{st}
	}
	return nil
}

// handleTopologyGet 处理拓扑感知读取
func handleTopologyGet(st *TopologyState, op TopologyOperation, output TopologyOutput) []interface{} {
	if st.Topology == nil {
		return nil
	}

	_, exists := st.Topology.Nodes[op.NodeID]
	if !exists {
		return nil
	}

	store, exists := st.NodeStores[op.NodeID]
	if !exists {
		if output.Error == "key not found" {
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

// topologyStateEqual 深度比较两个拓扑状态
func topologyStateEqual(state1, state2 interface{}) bool {
	s1, ok1 := state1.(*TopologyState)
	s2, ok2 := state2.(*TopologyState)
	if !ok1 || !ok2 {
		return false
	}

	// 比较 NodeStores
	if !NodeStoresEqual(s1.NodeStores, s2.NodeStores) {
		return false
	}

	// 比较其他字段
	return s1.CurrentLeader == s2.CurrentLeader &&
		s1.CurrentTerm == s2.CurrentTerm
}

// ==================== 模型定义 ====================

// newTopologyAwareNondeterministicModel 创建非确定性拓扑感知模型
func newTopologyAwareNondeterministicModel() *porcupine.NondeterministicModel {
	return &porcupine.NondeterministicModel{
		// Init 返回 []interface{}
		Init: func() []interface{} {
			return []interface{}{&TopologyState{
				NodeStores:    make(map[string]map[string]VersionedValue),
				Topology:      nil,
				CurrentLeader: "",
				CurrentTerm:   0,
			}}
		},

		// Step 返回所有可能的状态
		Step: func(state, input, output interface{}) []interface{} {
			st, ok := state.(*TopologyState)
			if !ok {
				return nil
			}

			op, ok := input.(TopologyOperation)
			if !ok {
				return nil
			}

			out, ok := output.(TopologyOutput)
			if !ok {
				return nil
			}

			// 根据操作类型分发
			switch op.Type {
			case TopologyOpInitTopology:
				return handleInitTopology(st, op, out)
			case TopologyOpWrite2PC:
				return handle2PCWrite(st, op, out)
			case TopologyOpWriteQuorum:
				return handleQuorumWrite(st, op, out)
			case TopologyOpWriteGossip:
				return handleTreeAwareGossip(st, op, out)
			case TopologyOpGet:
				return handleTopologyGet(st, op, out)
			default:
				return nil
			}
		},

		// Equal 状态比较函数
		Equal: topologyStateEqual,

		// DescribeOperation 描述操作（用于可视化）
		DescribeOperation: func(input, output interface{}) string {
			op, _ := input.(TopologyOperation)
			out, _ := output.(TopologyOutput)
			return fmt.Sprintf("%s(%s:%s) -> ok=%v", op.Type, op.NodeID, op.Key, out.Ok)
		},

		// DescribeState 描述状态（用于可视化）
		DescribeState: func(state interface{}) string {
			st, _ := state.(*TopologyState)
			if st == nil {
				return "<nil>"
			}
			return fmt.Sprintf("nodes=%d,leader=%s,term=%d",
				len(st.NodeStores), st.CurrentLeader, st.CurrentTerm)
		},
	}
}

// TopologyAwareModel 创建拓扑感知模型
// 使用 NondeterministicModel 模式
func TopologyAwareModel() porcupine.Model {
	nm := newTopologyAwareNondeterministicModel()
	return nm.ToModel()
}
