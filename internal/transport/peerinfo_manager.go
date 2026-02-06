package transport

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
)

// PeerInfoManager PeerInfo 管理器
// 用于缓存和管理 libp2p 节点的 PeerInfo 信息
type PeerInfoManager struct {
	peerInfos map[peer.ID]*peer.AddrInfo
	mutex     sync.RWMutex
}

// NewPeerInfoManager 创建 PeerInfo 管理器
func NewPeerInfoManager() *PeerInfoManager {
	return &PeerInfoManager{
		peerInfos: make(map[peer.ID]*peer.AddrInfo),
	}
}

// Add 添加 PeerInfo
// 如果已存在相同 ID 的 PeerInfo，将覆盖旧的
func (pm *PeerInfoManager) Add(pi *peer.AddrInfo) {
	if pi == nil {
		return
	}

	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	pm.peerInfos[pi.ID] = pi
}

// Get 获取 PeerInfo
// 返回 PeerInfo 和是否存在的标志
func (pm *PeerInfoManager) Get(pid peer.ID) (*peer.AddrInfo, bool) {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	pi, ok := pm.peerInfos[pid]
	return pi, ok
}

// Remove 移除 PeerInfo
// 如果不存在，不做任何操作
func (pm *PeerInfoManager) Remove(pid peer.ID) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	delete(pm.peerInfos, pid)
}

// List 列出所有 PeerInfo
// 返回的列表是副本，修改不会影响内部数据
func (pm *PeerInfoManager) List() []*peer.AddrInfo {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	result := make([]*peer.AddrInfo, 0, len(pm.peerInfos))
	for _, pi := range pm.peerInfos {
		result = append(result, pi)
	}
	return result
}

// Count 返回 PeerInfo 数量
func (pm *PeerInfoManager) Count() int {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	return len(pm.peerInfos)
}

// Clear 清空所有 PeerInfo
func (pm *PeerInfoManager) Clear() {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	pm.peerInfos = make(map[peer.ID]*peer.AddrInfo)
}

// Exists 检查 PeerInfo 是否存在
func (pm *PeerInfoManager) Exists(pid peer.ID) bool {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	_, ok := pm.peerInfos[pid]
	return ok
}
