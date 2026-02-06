package transport

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
)

// NodeIDMapper 节点ID映射器（业务层 ↔ 网络层）
type NodeIDMapper struct {
	nodeIDToPeerID map[string]peer.ID // node_id -> PeerID
	peerIDToNodeID map[peer.ID]string  // PeerID -> node_id
	mu            sync.RWMutex
}

// NewNodeIDMapper 创建映射器
func NewNodeIDMapper() *NodeIDMapper {
	return &NodeIDMapper{
		nodeIDToPeerID: make(map[string]peer.ID),
		peerIDToNodeID: make(map[peer.ID]string),
	}
}

// Register 注册 node_id 与 PeerID 的映射
func (m *NodeIDMapper) Register(nodeID string, pid peer.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodeIDToPeerID[nodeID] = pid
	m.peerIDToNodeID[pid] = nodeID
}

// GetPeerID 根据 node_id 获取 PeerID
func (m *NodeIDMapper) GetPeerID(nodeID string) (peer.ID, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pid, ok := m.nodeIDToPeerID[nodeID]
	return pid, ok
}

// GetNodeID 根据 PeerID 获取 node_id
func (m *NodeIDMapper) GetNodeID(pid peer.ID) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	nodeID, ok := m.peerIDToNodeID[pid]
	return nodeID, ok
}
