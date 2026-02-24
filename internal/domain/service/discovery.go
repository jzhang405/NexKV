// Package service 定义领域服务接口
package service

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// DiscoveryService 节点发现服务接口
// ==========================================

// DiscoveryService 定义节点发现的领域服务接口
// 基础设施层提供具体实现（如 mDNS、DHT 等）
type DiscoveryService interface {
	// Start 启动发现服务
	Start(ctx context.Context) error

	// Stop 停止发现服务
	Stop() error

	// SetNotifee 设置节点发现通知处理器
	SetNotifee(notifee DiscoveryNotifee)
}

// DiscoveryNotifee 节点发现通知接口
// 当发现新节点、节点状态变化或节点丢失时，基础设施层调用此接口
// 使用领域抽象 NetworkAddress，避免依赖具体技术实现
type DiscoveryNotifee interface {
	// HandlePeerFound 处理发现的节点
	HandlePeerFound(peerID model.PeerID, addrs []model.NetworkAddress)
	// HandlePeerUpdated 处理节点地址更新（节点已存在但地址变化）
	HandlePeerUpdated(peerID model.PeerID, addrs []model.NetworkAddress)
	// HandlePeerSuspected 处理节点疑似失效（心跳超时但未确认丢失）
	HandlePeerSuspected(peerID model.PeerID, reason string)
	// HandlePeerLost 处理节点丢失（掉线/离开）
	HandlePeerLost(peerID model.PeerID)
}

// ==========================================
// 发现事件类型
// ==========================================

// DiscoveryEvent 发现事件
type DiscoveryEvent struct {
	PeerID model.PeerID
	Addrs  []model.NetworkAddress
	Type   DiscoveryEventType
}

// DiscoveryEventType 发现事件类型
type DiscoveryEventType int

const (
	// DiscoveryEventPeerFound 发现节点
	DiscoveryEventPeerFound DiscoveryEventType = iota
	// DiscoveryEventPeerUpdated 节点地址更新
	DiscoveryEventPeerUpdated
	// DiscoveryEventPeerSuspected 节点疑似失效
	DiscoveryEventPeerSuspected
	// DiscoveryEventPeerLost 节点丢失
	DiscoveryEventPeerLost
)

// String 返回事件类型字符串
func (t DiscoveryEventType) String() string {
	switch t {
	case DiscoveryEventPeerFound:
		return "peer_found"
	case DiscoveryEventPeerUpdated:
		return "peer_updated"
	case DiscoveryEventPeerSuspected:
		return "peer_suspected"
	case DiscoveryEventPeerLost:
		return "peer_lost"
	default:
		return "unknown"
	}
}
