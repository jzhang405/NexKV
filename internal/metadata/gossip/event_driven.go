// Package gossip 提供事件驱动的 Gossip 同步机制
//
// P0-3: Gossip 事件驱动触发机制
//
// 核心功能：
//   - 写入事件触发：写入操作后立即触发 Gossip
//   - 定时器兜底：周期性 Gossip 保证最终一致性
//   - 防风暴机制：通道满时丢弃事件，定时器兜底
package gossip

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/jzhang405/NexKV/internal/transport"
)

// ==================== 事件定义 ====================

// GossipEventType Gossip 事件类型
type GossipEventType int

const (
	// EventWrite 写入事件
	EventWrite GossipEventType = iota
	// EventNamespaceChange Namespace 变更事件
	EventNamespaceChange
	// EventPeerJoin 节点加入事件
	EventPeerJoin
	// EventPeerLeave 节点离开事件
	EventPeerLeave
	// EventBatch 批量事件
	EventBatch
)

// GossipEvent Gossip 事件
type GossipEvent struct {
	Type      GossipEventType // 事件类型
	Namespace string          // Namespace（可选）
	Key       string          // Key（可选）
	Value     []byte          // Value（可选，用于批量事件）
	NodeID    string          // 节点 ID（可选）
	Timestamp time.Time       // 事件时间戳
}

// ==================== EventDrivenGossipSync ====================

// EventDrivenGossipSync 事件驱动的 Gossip 同步
//
// 结合事件触发和定时触发：
//   - 事件驱动：写入操作后立即触发，减少同步延迟
//   - 定时触发：周期性 Gossip，保证最终一致性
//   - 防风暴：通道满时丢弃事件，依赖定时器兜底
type EventDrivenGossipSync struct {
	mu sync.RWMutex

	// 核心 Gossip 同步器
	*merkleGossipSyncAdapter

	// 事件通道
	eventChan chan GossipEvent

	// 定时器
	ticker      *time.Ticker
	tickerDelay time.Duration

	// 待同步的 Namespace（批量优化）
	pendingNamespaces map[string]bool

	// 配置
	eventChanSize int // 事件通道大小（默认 1000）

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 统计
	eventsReceived   uint64
	eventsProcessed  uint64
	eventsDropped    uint64
	timerTriggered   uint64
	eventsTriggered  uint64
	pendingBatchSize uint64
}

// EventDrivenConfig 事件驱动 Gossip 配置
type EventDrivenConfig struct {
	// MerkleTree Merkle Tree
	MerkleTree *kvstore.NamespacedMerkleTree

	// MetadataKV 元数据存储
	MetadataKV *kvstore.MetadataKV

	// Transport 传输层
	Transport transport.Transport

	// LocalNodeID 本地节点 ID
	LocalNodeID string

	// PeerSelector Peer 选择器
	PeerSelector PeerSelector

	// EventChanSize 事件通道大小（默认 1000）
	EventChanSize int

	// TickerDelay 定时器间隔（默认 10 秒）
	TickerDelay time.Duration

	// GossipTimeout Gossip 超时（默认 5 秒）
	GossipTimeout time.Duration
}

// merkleGossipSyncAdapter 适配器，复用 MerkleGossipSync 的功能
type merkleGossipSyncAdapter struct {
	merkleSync *MerkleGossipSync
}

// NewEventDrivenGossipSync 创建事件驱动的 Gossip 同步
func NewEventDrivenGossipSync(config *EventDrivenConfig) *EventDrivenGossipSync {
	if config == nil {
		config = &EventDrivenConfig{}
	}

	// 设置默认值
	eventChanSize := config.EventChanSize
	if eventChanSize <= 0 {
		eventChanSize = 1000
	}

	tickerDelay := config.TickerDelay
	if tickerDelay <= 0 {
		tickerDelay = 10 * time.Second
	}

	// 创建内部 MerkleGossipSync
	merkleSync := NewMerkleGossipSync(
		config.MerkleTree,
		config.MetadataKV,
		config.Transport,
		config.LocalNodeID,
		config.PeerSelector,
	)

	ctx, cancel := context.WithCancel(context.Background())

	sync := &EventDrivenGossipSync{
		merkleGossipSyncAdapter: &merkleGossipSyncAdapter{
			merkleSync: merkleSync,
		},
		eventChan:         make(chan GossipEvent, eventChanSize),
		ticker:            time.NewTicker(tickerDelay),
		tickerDelay:       tickerDelay,
		pendingNamespaces: make(map[string]bool),
		eventChanSize:     eventChanSize,
		ctx:               ctx,
		cancel:            cancel,
	}

	// 启动事件处理循环
	sync.wg.Add(1)
	go sync.runEventLoop()

	logging.WithFields(map[string]interface{}{
		"event_chan_size": eventChanSize,
		"ticker_delay":    tickerDelay.String(),
	}).Info("事件驱动 Gossip 同步已启动")

	return sync
}

// runEventLoop 运行事件处理循环
func (s *EventDrivenGossipSync) runEventLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			logging.Info("事件驱动 Gossip 循环停止")
			return

		case event := <-s.eventChan:
			s.handleEvent(event)

		case <-s.ticker.C:
			s.handleTimer()
		}
	}
}

// handleEvent 处理单个事件
func (s *EventDrivenGossipSync) handleEvent(event GossipEvent) {
	s.mu.Lock()
	s.eventsReceived++
	s.mu.Unlock()

	// 添加到待同步列表
	if event.Namespace != "" {
		s.mu.Lock()
		s.pendingNamespaces[event.Namespace] = true
		s.pendingBatchSize = uint64(len(s.pendingNamespaces))
		s.mu.Unlock()
	}

	// 批量触发 Gossip（如果积累了足够多的事件）
	s.mu.RLock()
	pendingCount := len(s.pendingNamespaces)
	s.mu.RUnlock()

	if pendingCount >= 5 {
		s.triggerGossip()
	}

	s.mu.Lock()
	s.eventsProcessed++
	s.mu.Unlock()
}

// handleTimer 处理定时器触发
func (s *EventDrivenGossipSync) handleTimer() {
	s.mu.Lock()
	s.timerTriggered++
	s.mu.Unlock()

	// 定时器触发时执行 Gossip
	s.triggerGossip()
}

// triggerGossip 触发 Gossip 同步
func (s *EventDrivenGossipSync) triggerGossip() {
	s.mu.Lock()
	s.eventsTriggered++
	pending := s.pendingNamespaces
	s.pendingNamespaces = make(map[string]bool)
	s.pendingBatchSize = 0
	s.mu.Unlock()

	// 记录待同步的 Namespace
	if len(pending) > 0 {
		logging.WithField("pending_namespaces", len(pending)).Debug("触发事件驱动 Gossip")
	}

	// 执行 Gossip（复用 MerkleGossipSync 的逻辑）
	s.gossipRandomPeer(s.ctx)
}

// gossipRandomPeer 与随机 Peer 同步（复用 MerkleGossipSync）
func (s *EventDrivenGossipSync) gossipRandomPeer(ctx context.Context) {
	if s.merkleSync == nil || s.merkleSync.peerSelector == nil {
		return
	}

	// 收集 peer 列表
	s.merkleSync.mu.RLock()
	if len(s.merkleSync.knownPeers) == 0 {
		s.merkleSync.mu.RUnlock()
		return
	}

	peers := make([]string, 0, len(s.merkleSync.knownPeers))
	for peerID := range s.merkleSync.knownPeers {
		peers = append(peers, peerID)
	}
	s.merkleSync.mu.RUnlock()

	// 选择 peer
	selectedPeer := s.merkleSync.peerSelector.Select(peers)
	if selectedPeer == "" {
		return
	}

	// 执行同步
	_, _ = s.merkleSync.SyncWithPeer(ctx, selectedPeer)
}

// ==================== 公共 API ====================

// OnWrite 写入事件触发（写入操作后调用）
func (s *EventDrivenGossipSync) OnWrite(namespace, key string) {
	event := GossipEvent{
		Type:      EventWrite,
		Namespace: namespace,
		Key:       key,
		Timestamp: time.Now(),
	}

	select {
	case s.eventChan <- event:
		// 成功发送
	default:
		// 通道满，丢弃事件（定时器会兜底）
		s.mu.Lock()
		s.eventsDropped++
		s.mu.Unlock()

		logging.WithFields(map[string]interface{}{
			"namespace": namespace,
			"key":       key,
		}).Debug("Gossip 事件通道满，丢弃事件")
	}
}

// OnNamespaceChange Namespace 变更事件触发
func (s *EventDrivenGossipSync) OnNamespaceChange(namespace string) {
	event := GossipEvent{
		Type:      EventNamespaceChange,
		Namespace: namespace,
		Timestamp: time.Now(),
	}

	select {
	case s.eventChan <- event:
	default:
		s.mu.Lock()
		s.eventsDropped++
		s.mu.Unlock()
	}
}

// OnPeerJoin 节点加入事件触发
func (s *EventDrivenGossipSync) OnPeerJoin(nodeID string) {
	event := GossipEvent{
		Type:      EventPeerJoin,
		NodeID:    nodeID,
		Timestamp: time.Now(),
	}

	select {
	case s.eventChan <- event:
	default:
		s.mu.Lock()
		s.eventsDropped++
		s.mu.Unlock()
	}
}

// OnPeerLeave 节点离开事件触发
func (s *EventDrivenGossipSync) OnPeerLeave(nodeID string) {
	event := GossipEvent{
		Type:      EventPeerLeave,
		NodeID:    nodeID,
		Timestamp: time.Now(),
	}

	select {
	case s.eventChan <- event:
	default:
		s.mu.Lock()
		s.eventsDropped++
		s.mu.Unlock()
	}
}

// AddKnownPeer 添加已知的 peer（委托给 MerkleGossipSync）
func (s *EventDrivenGossipSync) AddKnownPeer(peerID string) {
	if s.merkleSync != nil {
		s.merkleSync.AddKnownPeer(peerID)
	}
}

// RemoveKnownPeer 移除已知的 peer（委托给 MerkleGossipSync）
func (s *EventDrivenGossipSync) RemoveKnownPeer(peerID string) {
	if s.merkleSync != nil {
		s.merkleSync.RemoveKnownPeer(peerID)
	}
}

// GetStats 获取统计信息
func (s *EventDrivenGossipSync) GetStats() map[string]interface{} {
	s.mu.RLock()
	stats := map[string]interface{}{
		"events_received":    s.eventsReceived,
		"events_processed":   s.eventsProcessed,
		"events_dropped":     s.eventsDropped,
		"timer_triggered":    s.timerTriggered,
		"events_triggered":   s.eventsTriggered,
		"pending_batch_size": s.pendingBatchSize,
		"event_chan_size":    s.eventChanSize,
		"ticker_delay":       s.tickerDelay.String(),
	}
	s.mu.RUnlock()

	// 合并 MerkleGossipSync 的统计
	if s.merkleSync != nil {
		merkleStats := s.merkleSync.GetStats()
		for k, v := range merkleStats {
			stats[k] = v
		}
	}

	return stats
}

// ForceSync 强制触发同步（用于测试）
func (s *EventDrivenGossipSync) ForceSync() {
	s.triggerGossip()
}

// Close 关闭事件驱动 Gossip 同步
func (s *EventDrivenGossipSync) Close() error {
	s.cancel()
	s.ticker.Stop()
	s.wg.Wait()

	// 关闭内部的 MerkleGossipSync
	if s.merkleSync != nil {
		_ = s.merkleSync.Close()
	}

	logging.Info("事件驱动 Gossip 同步已关闭")
	return nil
}
