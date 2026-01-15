package implementations

import (
	"context"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
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

// WALEntry WAL 日志条目
type WALEntry struct {
	Timestamp time.Time
	NodeID    string
	Knowledge Knowledge
	Decision  DecisionState
	Version   int
}

// WAL Write-Ahead Log 结构
type WAL struct {
	file   *os.File
	path   string
	mu     sync.Mutex
	encoder *gob.Encoder
	decoder *gob.Decoder
}

// NewWAL 创建新的 WAL
func NewWAL(dataDir string) (*WAL, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	path := filepath.Join(dataDir, "wal.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAL file: %w", err)
	}

	return &WAL{
		file:    file,
		path:    path,
		encoder: gob.NewEncoder(file),
		decoder: gob.NewDecoder(file),
	}, nil
}

// Append 追加日志条目到 WAL
func (w *WAL) Append(entry WALEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.encoder.Encode(entry); err != nil {
		return fmt.Errorf("failed to encode WAL entry: %w", err)
	}

	// 立即刷盘
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync WAL: %w", err)
	}

	return nil
}

// Recover 从 WAL 恢复所有日志条目
func (w *WAL) Recover() ([]WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 重置文件指针到开头
	if _, err := w.file.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("failed to seek WAL file: %w", err)
	}

	var entries []WALEntry
	for {
		var entry WALEntry
		if err := w.decoder.Decode(&entry); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to decode WAL entry: %w", err)
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// Close 关闭 WAL
func (w *WAL) Close() error {
	return w.file.Close()
}

// Node 节点结构
type Node struct {
	ID        string
	Role      NodeRole
	Knowledge Knowledge
	Decision  DecisionState
	mu        sync.RWMutex

	// ===== 崩溃恢复相关字段 =====
	IsCrashed   bool      // 节点是否已崩溃
	CrashTime   time.Time // 崩溃时间
	RecoveredAt time.Time // 恢复时间
	CrashCount  int       // 崩溃次数（用于监控）
	WAL         *WAL      // 写前日志（持久化）
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

// Crash 模拟节点崩溃
// 前置条件：节点当前未崩溃
// 后置条件：节点标记为崩溃，状态持久化到 WAL
func (n *Node) Crash() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// 幂等性检查：重复崩溃不报错
	if n.IsCrashed {
		return nil
	}

	// 1. 持久化当前状态到 WAL
	if n.WAL != nil {
		entry := WALEntry{
			Timestamp: time.Now(),
			NodeID:    n.ID,
			Knowledge: n.Knowledge,
			Decision:  n.Decision,
			Version:   n.Knowledge.Version,
		}

		if err := n.WAL.Append(entry); err != nil {
			return fmt.Errorf("failed to persist state before crash: %w", err)
		}
	}

	// 2. 更新崩溃状态
	n.IsCrashed = true
	n.CrashTime = time.Now()
	n.CrashCount++

	log.Printf("[Node %s] Crashed at %v (count=%d)",
		n.ID, n.CrashTime.Format(time.RFC3339), n.CrashCount)

	return nil
}

// Recover 恢复崩溃的节点
// 前置条件：节点当前处于崩溃状态
// 后置条件：节点恢复到之前持久化的状态，并启动增量同步
func (n *Node) Recover(cluster *Cluster) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// 检查前置条件
	if !n.IsCrashed {
		return fmt.Errorf("node %s is not crashed", n.ID)
	}

	// 1. 从 WAL 恢复状态
	if n.WAL != nil {
		entries, err := n.WAL.Recover()
		if err != nil {
			return fmt.Errorf("failed to recover from WAL: %w", err)
		}

		if len(entries) > 0 {
			// 使用最后一个条目恢复状态
			lastEntry := entries[len(entries)-1]
			n.Knowledge = lastEntry.Knowledge
			n.Decision = lastEntry.Decision

			log.Printf("[Node %s] Recovered from WAL (version=%d, decision=%s)",
				n.ID, n.Knowledge.Version, n.Decision)
		}
	}

	// 2. 更新恢复状态
	n.IsCrashed = false
	n.RecoveredAt = time.Now()

	// 3. 启动后台增量同步（在 goroutine 中）
	go n.incrementalSync(cluster)

	return nil
}

// RecoverFromWAL 从 WAL 恢复状态（用于进程重启场景）
// 前置条件：WAL 文件存在且包含有效数据
// 后置条件：节点状态恢复到 WAL 中保存的最新状态，并启动增量同步
func (n *Node) RecoverFromWAL(cluster *Cluster) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// 1. 从 WAL 恢复状态
	if n.WAL != nil {
		entries, err := n.WAL.Recover()
		if err != nil {
			return fmt.Errorf("failed to recover from WAL: %w", err)
		}

		if len(entries) > 0 {
			// 使用最后一个条目恢复状态
			lastEntry := entries[len(entries)-1]
			n.Knowledge = lastEntry.Knowledge
			n.Decision = lastEntry.Decision

			log.Printf("[Node %s] Recovered from WAL (version=%d, decision=%s)",
				n.ID, n.Knowledge.Version, n.Decision)
		} else {
			// WAL 为空，无需恢复
			log.Printf("[Node %s] WAL is empty, no state to recover", n.ID)
			return nil
		}
	}

	// 2. 启动后台增量同步（在 goroutine 中）
	go n.incrementalSync(cluster)

	return nil
}

// incrementalSync 后台增量同步
func (n *Node) incrementalSync(cluster *Cluster) {
	log.Printf("[Node %s] Starting incremental sync", n.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	synced := false

	for !synced {
		select {
		case <-ctx.Done():
			log.Printf("[Node %s] Incremental sync timeout", n.ID)
			return

		case <-ticker.C:
			// 尝试与其他节点同步
			changed := false
			for _, other := range cluster.Nodes {
				if other.ID == n.ID {
					continue
				}

				// 跳过崩溃节点
				other.mu.RLock()
				isCrashed := other.IsCrashed
				other.mu.RUnlock()

				if isCrashed {
					continue
				}

				// 尝试 Gossip 交换
				if err := n.GossipExchange(other); err == nil {
					changed = true
				}
			}

			// 检查是否已收敛
			if !changed {
				log.Printf("[Node %s] Incremental sync completed", n.ID)
				synced = true
			}
		}
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
// 新增：崩溃节点不参与 Gossip
func (n *Node) GossipExchange(other *Node) error {
	n.mu.Lock()
	other.mu.Lock()
	defer other.mu.Unlock()
	defer n.mu.Unlock()

	// ===== 新增：崩溃节点检查 =====
	if n.IsCrashed {
		return fmt.Errorf("node %s is crashed, cannot gossip", n.ID)
	}
	if other.IsCrashed {
		return fmt.Errorf("peer %s is crashed, cannot gossip", other.ID)
	}

	// 只在同一版本的节点间交换
	if n.Knowledge.Version != other.Knowledge.Version {
		return nil
	}

	// 合并 knowledge
	newSeen := mergeMaps(n.Knowledge.Seen, other.Knowledge.Seen)
	newDecided := mergeMaps(n.Knowledge.Decided, other.Knowledge.Decided)

	n.Knowledge.Seen = newSeen
	n.Knowledge.Decided = newDecided

	other.Knowledge.Seen = newSeen
	other.Knowledge.Decided = newDecided

	return nil
}

// DecideCommit 决策提交（对应 TLA+ 的 DecideCommit）
// 要求：节点必须先给自己投票，且 quorum 达到多数派
// 新增：崩溃节点不能决策，决策后持久化到 WAL
func (n *Node) DecideCommit(majority int) (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// ===== 新增：崩溃节点检查 =====
	if n.IsCrashed {
		return false, fmt.Errorf("node %s is crashed, cannot decide", n.ID)
	}

	// 检查决策状态
	if n.Decision != Undecided {
		return false, nil
	}
	// 节点必须先给自己投票
	if !n.Knowledge.Seen[n.ID] {
		return false, nil
	}
	// 检查是否达到 quorum
	if len(n.Knowledge.Seen) < majority {
		return false, nil
	}

	// 提交决策
	n.Decision = Committed
	n.Knowledge.Decided[n.ID] = true

	// 持久化到 WAL
	if n.WAL != nil {
		entry := WALEntry{
			Timestamp: time.Now(),
			NodeID:    n.ID,
			Knowledge: n.Knowledge,
			Decision:  n.Decision,
			Version:   n.Knowledge.Version,
		}

		if err := n.WAL.Append(entry); err != nil {
			return false, fmt.Errorf("failed to persist decision: %w", err)
		}
	}

	return true, nil
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
func NewCluster(nodeIDs []string, dataDir string) *Cluster {
	nodes := make([]*Node, len(nodeIDs))

	for i, id := range nodeIDs {
		// 为每个节点创建独立的 WAL 目录
		walDir := filepath.Join(dataDir, id)
		wal, err := NewWAL(walDir)
		if err != nil {
			log.Printf("Failed to create WAL for node %s: %v", id, err)
			// 继续创建，但 WAL 为 nil
		}

		nodes[i] = &Node{
			ID:   id,
			Role: Follower,
			Knowledge: Knowledge{
				Seen:    make(map[string]bool),
				Version: 0,
				Decided: make(map[string]bool),
			},
			Decision: Undecided,
			WAL:      wal,
		}
	}

	return &Cluster{
		Nodes:   nodes,
		Version: 0,
	}
}

// Close 关闭集群资源
func (c *Cluster) Close() error {
	var errs []error

	for _, node := range c.Nodes {
		if node.WAL != nil {
			if err := node.WAL.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing cluster: %v", errs)
	}
	return nil
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
			_ = c.Nodes[i].GossipExchange(c.Nodes[j]) // 忽略错误，继续执行
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

	// 使用临时目录作为数据目录
	dataDir := os.TempDir()
	cluster := NewCluster(nodeIDs, dataDir)
	defer cluster.Close()

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
			if success, _ := node.DecideCommit(majority); success {
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
