package implementations

import (
	"fmt"
	"sync"
	"time"
)

// DecisionState 决策状态
type DecisionState string

const (
	Undecided DecisionState = "undecided"
	Committed DecisionState = "committed"
)

// NodeRole 节点角色
type NodeRole string

const (
	Follower NodeRole = "follower"
	Leader   NodeRole = "leader"
)

// Knowledge 节点知识（gossip 收集的信息）
type Knowledge struct {
	Seen    map[string]bool // 已知已投票的节点
	Version int             // 当前投票版本号
	Decided map[string]bool // 已知已决策的节点
}

// Node 节点结构
type Node struct {
	ID        string
	Role      NodeRole
	Knowledge Knowledge
	Decision  DecisionState
	mu        sync.RWMutex
}

// NewNode 创建新节点
func NewNode(id string) *Node {
	return &Node{
		ID:   id,
		Role: Follower,
		Knowledge: Knowledge{
			Seen:    make(map[string]bool),
			Version: 0,
			Decided: make(map[string]bool),
		},
		Decision: Undecided,
	}
}

// ProposeVote 发起投票（对应 TLA+ 的 ProposeVote）
func (n *Node) ProposeVote(version int) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.Decision != Undecided {
		return false
	}
	if n.Knowledge.Version != version {
		return false
	}

	// 给自己投票
	n.Knowledge.Seen[n.ID] = true
	return true
}

// GossipExchange gossip 交换（对应 TLA+ 的 GossipExchange）
func (n *Node) GossipExchange(other *Node) {
	n.mu.Lock()
	other.mu.Lock()

	// 只在同一版本的节点间交换
	if n.Knowledge.Version != other.Knowledge.Version {
		other.mu.Unlock()
		n.mu.Unlock()
		return
	}

	// 合并 knowledge
	newSeen := mergeMaps(n.Knowledge.Seen, other.Knowledge.Seen)
	newDecided := mergeMaps(n.Knowledge.Decided, other.Knowledge.Decided)

	n.Knowledge.Seen = newSeen
	n.Knowledge.Decided = newDecided

	other.Knowledge.Seen = newSeen
	other.Knowledge.Decided = newDecided

	other.mu.Unlock()
	n.mu.Unlock()
}

// DecideCommit 决策提交（对应 TLA+ 的 DecideCommit）
// 要求：节点必须先给自己投票，且 quorum 达到多数派
func (n *Node) DecideCommit(majority int) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.Decision != Undecided {
		return false
	}
	// 节点必须先给自己投票
	if !n.Knowledge.Seen[n.ID] {
		return false
	}
	// 检查是否达到 quorum
	if len(n.Knowledge.Seen) < majority {
		return false
	}

	n.Decision = Committed
	n.Knowledge.Decided[n.ID] = true
	return true
}

// FollowDecision 跟随决策（对应 TLA+ 的 FollowDecision）
// 要求：节点必须先给自己投票，且知道其他节点已提交
func (n *Node) FollowDecision(majority int) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.Decision != Undecided {
		return false
	}
	// 节点必须先给自己投票
	if !n.Knowledge.Seen[n.ID] {
		return false
	}
	// 检查是否知道其他节点已提交
	for nodeID := range n.Knowledge.Decided {
		if nodeID != n.ID && n.Knowledge.Decided[nodeID] {
			n.Decision = Committed
			n.Knowledge.Decided[n.ID] = true
			return true
		}
	}
	return false
}

// GetState 获取节点状态（用于测试）
func (n *Node) GetState() (DecisionState, int, map[string]bool, map[string]bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.Decision, n.Knowledge.Version, n.Knowledge.Seen, n.Knowledge.Decided
}

// mergeMaps 合并两个 map
func mergeMaps(a, b map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
}

// Cluster 集群结构
type Cluster struct {
	Nodes   []*Node
	Version int
	mu      sync.RWMutex
}

// NewCluster 创建新集群
func NewCluster(nodeIDs []string) *Cluster {
	nodes := make([]*Node, len(nodeIDs))
	for i, id := range nodeIDs {
		nodes[i] = NewNode(id)
	}
	return &Cluster{
		Nodes:   nodes,
		Version: 0,
	}
}

// GetNode 获取节点
func (c *Cluster) GetNode(id string) *Node {
	for _, n := range c.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// GetMajority 获取多数派阈值
func (c *Cluster) GetMajority() int {
	return len(c.Nodes)/2 + 1
}

// GossipRound 一轮 gossip 交换
func (c *Cluster) GossipRound() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 随机选择节点进行 gossip
	for i := 0; i < len(c.Nodes); i++ {
		for j := i + 1; j < len(c.Nodes); j++ {
			c.Nodes[i].GossipExchange(c.Nodes[j])
		}
	}
}

// PrintState 打印集群状态（用于调试）
func (c *Cluster) PrintState() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	fmt.Printf("Version: %d, Majority: %d\n", c.Version, c.GetMajority())
	for _, n := range c.Nodes {
		decision, version, seen, decided := n.GetState()
		seenList := keysFromMap(seen)
		decidedList := keysFromMap(decided)
		fmt.Printf("  Node %s: decision=%s, version=%d, seen=%v, decided=%v\n",
			n.ID, decision, version, seenList, decidedList)
	}
}

// keysFromMap 从 map 获取 key 列表
func keysFromMap(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// SimulationConfig 模拟配置
type SimulationConfig struct {
	NodeCount      int
	MaxRounds      int
	GossipInterval time.Duration
	Majority       int
}

// DefaultSimulationConfig 默认配置
func DefaultSimulationConfig() *SimulationConfig {
	return &SimulationConfig{
		NodeCount:      3,
		MaxRounds:      20,
		GossipInterval: 100 * time.Millisecond,
		Majority:       2,
	}
}

// RunSimulation 运行模拟
func RunSimulation(config *SimulationConfig) *SimulationResult {
	if config == nil {
		config = DefaultSimulationConfig()
	}

	// 创建集群
	nodeIDs := make([]string, config.NodeCount)
	for i := 0; i < config.NodeCount; i++ {
		nodeIDs[i] = fmt.Sprintf("n%d", i+1)
	}
	cluster := NewCluster(nodeIDs)

	// 多数派
	majority := config.Majority
	if majority == 0 {
		majority = cluster.GetMajority()
	}

	// 模拟结果
	result := &SimulationResult{
		TotalRounds:    0,
		CommittedNodes: 0,
		Success:        false,
	}

	// 运行模拟
	for round := 0; round < config.MaxRounds; round++ {
		result.TotalRounds = round + 1

		// 节点发起投票
		for _, node := range cluster.Nodes {
			node.ProposeVote(cluster.Version)
		}

		// Gossip 交换
		cluster.GossipRound()

		// 节点尝试决策
		committedCount := 0
		for _, node := range cluster.Nodes {
			// 先尝试直接决策
			if node.DecideCommit(majority) {
				committedCount++
				continue
			}
			// 然后尝试跟随决策
			if node.FollowDecision(majority) {
				committedCount++
			}
		}

		result.CommittedNodes = committedCount

		// 检查是否所有节点都提交了
		if committedCount == config.NodeCount {
			result.Success = true
			break
		}

		// 等待
		time.Sleep(config.GossipInterval)
	}

	return result
}

// SimulationResult 模拟结果
type SimulationResult struct {
	TotalRounds    int
	CommittedNodes int
	Success        bool
}
