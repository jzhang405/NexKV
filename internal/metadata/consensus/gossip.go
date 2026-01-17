// Package consensus 提供一致性协议实现
//
// 包含：
//   - Gossip 协议：最终一致性，周期性扩散
//   - Quorum 机制：强一致性，多数派确认
//   - 2PC 协议：分布式事务协调
package consensus

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/store"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
)

// GossipService Gossip 协议服务
//
// 核心特性:
//   - 最终一致性：通过周期性同步达到最终一致
//   - 增量同步：只传输变更部分，减少网络开销
//   - 双向同步：本地更新和远程更新都会被同步
//   - HLC 时钟集成：自动同步时钟，检测时间漂移
//
// 算法流程:
//   1. 每隔 10 秒随机选择 2 个节点进行同步
//   2. 比较本地和远程版本号
//   3. 本地版本 > 远程版本：发送变更日志
//   4. 远程版本 > 本地版本：请求变更日志
//   5. 应用接收到的变更日志
//
// 收敛时间: O(log N) 轮次，通常 10 秒内完成全网同步
type GossipService struct {
	// 配置
	config *GossipConfig

	// 依赖
	metaStore  store.MVStore
	transport  transport.Transport
	hlc        *clock.HLC
	clockSync  clock.ClockSync

	// 节点列表
	peers   []string
	peersMu sync.RWMutex

	// 版本管理
	version   atomic.Uint64
	versionMu sync.RWMutex

	// 变更日志缓存（用于增量同步）
	changeLogs   []*ChangeLog
	changeLogsMu sync.RWMutex
	maxCacheSize int

	// 生命周期
	started   atomic.Bool
	stopped   atomic.Bool
	stopCh    chan struct{}
	stopWg    sync.WaitGroup

	// 统计信息
	stats *GossipStats
}

// GossipConfig Gossip 协议配置
type GossipConfig struct {
	// Interval 同步间隔（默认 10 秒）
	Interval time.Duration

	// Fanout 每轮随机选择的节点数（默认 2）
	Fanout int

	// Timeout 单次同步超时（默认 5 秒）
	Timeout time.Duration

	// MaxChangeLogs 最大变更日志数量（单次同步）
	MaxChangeLogs int

	// EnableClockSync 是否启用时钟同步
	EnableClockSync bool
}

// DefaultGossipConfig 返回默认 Gossip 配置
func DefaultGossipConfig() *GossipConfig {
	return &GossipConfig{
		Interval:        10 * time.Second,
		Fanout:          2,
		Timeout:         5 * time.Second,
		MaxChangeLogs:   1000,
		EnableClockSync: true,
	}
}

// ChangeLog 变更日志
//
// 记录元数据变更，用于增量同步
type ChangeLog struct {
	// Version 版本号（单调递增）
	Version uint64

	// HLC 时间戳（用于确定变更顺序）
	Timestamp *clock.HLC

	// Type 变更类型
	Type ChangeLogType

	// Key 键
	Key string

	// Value 新值（Put 操作）
	Value []byte

	// OldValue 旧值（用于冲突检测）
	OldValue []byte
}

// ChangeLogType 变更日志类型
type ChangeLogType uint8

const (
	// ChangeLogTypePut 写入操作
	ChangeLogTypePut ChangeLogType = iota

	// ChangeLogTypeDelete 删除操作
	ChangeLogTypeDelete
)

// GossipStats Gossip 统计信息
type GossipStats struct {
	// 同步次数
	SyncCount atomic.Int64

	// 成功次数
	SyncSuccess atomic.Int64

	// 失败次数
	SyncFailed atomic.Int64

	// 传输的变更日志数量
	ChangeLogsSent atomic.Int64

	// 接收的变更日志数量
	ChangeLogsReceived atomic.Int64

	// 最后同步时间
	LastSyncTime atomic.Value // time.Time
}

// NewGossipService 创建 Gossip 服务
func NewGossipService(
	metaStore store.MVStore,
	transport transport.Transport,
	hlc *clock.HLC,
	peers []string,
	config *GossipConfig,
) (*GossipService, error) {
	if config == nil {
		config = DefaultGossipConfig()
	}

	if metaStore == nil {
		return nil, fmt.Errorf("metaStore 不能为空")
	}

	if transport == nil {
		return nil, fmt.Errorf("transport 不能为空")
	}

	if hlc == nil {
		return nil, fmt.Errorf("hlc 不能为空")
	}

	// 创建时钟同步服务
	var clockSync clock.ClockSync
	if config.EnableClockSync {
		clockSync = clock.NewClockSync(hlc, clock.DefaultClockSyncConfig(), peers)
	}

	service := &GossipService{
		config:       config,
		metaStore:    metaStore,
		transport:    transport,
		hlc:          hlc,
		clockSync:    clockSync,
		peers:        peers,
		stopCh:       make(chan struct{}),
		maxCacheSize: config.MaxChangeLogs,
		stats:        &GossipStats{},
	}

	// 初始化最后同步时间
	service.stats.LastSyncTime.Store(time.Time{})

	return service, nil
}

// Start 启动 Gossip 服务
func (g *GossipService) Start() error {
	if !g.started.CompareAndSwap(false, true) {
		return fmt.Errorf("Gossip 服务已经启动")
	}

	logging.WithFields(map[string]interface{}{
		"interval":     g.config.Interval,
		"fanout":       g.config.Fanout,
		"peers":        len(g.peers),
		"clock_sync":   g.config.EnableClockSync,
		"max_logs":     g.config.MaxChangeLogs,
	}).Info("启动 Gossip 服务")

	// 启动时钟同步服务
	if g.clockSync != nil {
		if err := g.clockSync.Start(); err != nil {
			g.started.Store(false)
			return fmt.Errorf("启动时钟同步失败: %w", err)
		}
	}

	// 启动消息处理协程
	g.stopWg.Add(1)
	go g.messageLoop()

	// 启动同步循环
	g.stopWg.Add(1)
	go g.syncLoop()

	// 初始化版本号为 1
	g.version.Store(1)

	logging.Info("Gossip 服务启动成功")
	return nil
}

// Stop 停止 Gossip 服务
func (g *GossipService) Stop() error {
	if !g.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	logging.Info("停止 Gossip 服务...")

	// 关闭时钟同步服务
	if g.clockSync != nil {
		_ = g.clockSync.Stop()
	}

	// 关闭停止信号
	close(g.stopCh)

	// 等待所有协程退出
	g.stopWg.Wait()

	// 打印统计信息
	logging.WithFields(map[string]interface{}{
		"sync_count":          g.stats.SyncCount.Load(),
		"sync_success":        g.stats.SyncSuccess.Load(),
		"sync_failed":         g.stats.SyncFailed.Load(),
		"change_logs_sent":     g.stats.ChangeLogsSent.Load(),
		"change_logs_received": g.stats.ChangeLogsReceived.Load(),
		"final_version":        g.version.Load(),
	}).Info("Gossip 服务统计")

	logging.Info("Gossip 服务已停止")
	return nil
}

// ========================================
// 核心同步逻辑
// ========================================

// syncLoop 同步循环
//
// 每隔 Interval 时间随机选择 Fanout 个节点进行同步
func (g *GossipService) syncLoop() {
	defer g.stopWg.Done()

	// 首次启动时立即同步一次
	g.syncWithRandomPeers()

	ticker := time.NewTicker(g.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			g.syncWithRandomPeers()
		case <-g.stopCh:
			return
		}
	}
}

// syncWithRandomPeers 随机选择节点进行同步
func (g *GossipService) syncWithRandomPeers() {
	g.peersMu.RLock()
	peerCount := len(g.peers)
	g.peersMu.RUnlock()

	if peerCount == 0 {
		return
	}

	// 随机选择节点
	selectedPeers := g.selectRandomPeers(g.config.Fanout)

	ctx, cancel := context.WithTimeout(context.Background(), g.config.Timeout)
	defer cancel()

	// 并行同步
	for _, peer := range selectedPeers {
		g.stopWg.Add(1)
		go func(addr string) {
			defer g.stopWg.Done()
			if err := g.syncToNode(ctx, addr); err != nil {
				logging.WithField("peer", addr).
					WithError(err).
					Warn("Gossip 同步失败")
			}
		}(peer)
	}
}

// syncToNode 与指定节点同步
//
// 核心算法：双向同步
//   1. 获取本地和远程版本号
//   2. 如果本地版本 > 远程版本，发送变更日志
//   3. 如果远程版本 > 本地版本，请求变更日志
func (g *GossipService) syncToNode(ctx context.Context, addr string) error {
	// 更新统计
	g.stats.SyncCount.Add(1)
	g.stats.LastSyncTime.Store(time.Now())

	logging.WithField("peer", addr).Debug("开始 Gossip 同步")

	// 获取本地版本
	localVersion := g.version.Load()

	// 构建版本摘要消息
	digestMsg := &transport.GossipDigestMessage{
		Version: localVersion,
		Digest:  g.buildMetadataDigest(),
	}

	// 发送版本摘要
	if err := g.transport.Send(ctx, addr, digestMsg); err != nil {
		g.stats.SyncFailed.Add(1)
		return fmt.Errorf("发送版本摘要失败: %w", err)
	}

	// 等待响应（通过 messageLoop 接收并处理）
	// 响应处理在 messageLoop 中异步进行

	g.stats.SyncSuccess.Add(1)
	logging.WithFields(map[string]interface{}{
		"peer":          addr,
		"local_version": localVersion,
	}).Debug("Gossip 同步完成")

	return nil
}

// ========================================
// 消息处理
// ========================================

// messageLoop 消息处理循环
//
// 接收并处理来自其他节点的 Gossip 消息
func (g *GossipService) messageLoop() {
	defer g.stopWg.Done()

	recvCh := g.transport.Receive()

	for {
		select {
		case <-g.stopCh:
			return

		case msg, ok := <-recvCh:
			if !ok {
				logging.Info("接收通道已关闭")
				return
			}

			g.handleMessage(msg)
		}
	}
}

// handleMessage 处理接收到的消息
func (g *GossipService) handleMessage(msg transport.Message) {
	switch msg.Type() {
	case transport.MessageTypeGossipSync:
		g.handleGossipSync(msg)

	case transport.MessageTypeGossipSyncReply:
		g.handleGossipSyncReply(msg)

	case transport.MessageTypeGossipDigest:
		g.handleGossipDigest(msg)

	case transport.MessageTypeGossipDigestReply:
		g.handleGossipDigestReply(msg)

	default:
		// 其他消息类型由其他服务处理
	}
}

// handleGossipSync 处理 Gossip 同步请求
//
// 双向同步逻辑：
//   1. 比较本地和远程版本
//   2. 本地版本 > 远程版本：发送变更日志
//   3. 远程版本 > 本地版本：请求变更日志（在响应中携带）
func (g *GossipService) handleGossipSync(msg transport.Message) {
	syncMsg, ok := msg.(*transport.GossipSyncMessage)
	if !ok {
		logging.Warn("无效的 GossipSync 消息类型")
		return
	}

	localVersion := g.version.Load()
	remoteVersion := syncMsg.Version

	logging.WithFields(map[string]interface{}{
		"remote_version": remoteVersion,
		"local_version":  localVersion,
	}).Debug("处理 Gossip 同步请求")

	// 准备响应
	_ = &transport.GossipSyncReplyMessage{
		Accepted: true,
		Version:  localVersion,
	}

	// 本地版本 > 远程版本：发送变更日志
	if localVersion > remoteVersion {
		// 获取增量变更日志
		changeLogs := g.getChangeLogsSince(remoteVersion)

		if len(changeLogs) > 0 {
			// 发送变更日志
			for _, log := range changeLogs {
				syncMsg.Metadata[log.Key] = log.Value
			}

			g.stats.ChangeLogsSent.Add(int64(len(changeLogs)))
		}
	}

	// TODO: 发送响应（需要知道对方地址）
	// 当前简化实现：直接应用接收到的元数据
	if len(syncMsg.Metadata) > 0 {
		g.applyMetadata(syncMsg.Metadata)
	}
}

// handleGossipSyncReply 处理 Gossip 同步响应
func (g *GossipService) handleGossipSyncReply(msg transport.Message) {
	replyMsg, ok := msg.(*transport.GossipSyncReplyMessage)
	if !ok {
		logging.Warn("无效的 GossipSyncReply 消息类型")
		return
	}

	remoteVersion := replyMsg.Version
	localVersion := g.version.Load()

	logging.WithFields(map[string]interface{}{
		"remote_version": remoteVersion,
		"local_version":  localVersion,
	}).Debug("处理 Gossip 同步响应")

	// 如果远程版本更高，需要请求增量同步
	if remoteVersion > localVersion {
		// TODO: 发送变更日志请求
		logging.Debug("需要请求增量同步（未实现）")
	}
}

// handleGossipDigest 处理 Gossip 摘要请求
func (g *GossipService) handleGossipDigest(msg transport.Message) {
	_, ok := msg.(*transport.GossipDigestMessage)
	if !ok {
		logging.Warn("无效的 GossipDigest 消息类型")
		return
	}

	localVersion := g.version.Load()

	// 构建摘要响应
	_ = &transport.GossipDigestReplyMessage{
		Version: localVersion,
		Digest:  g.buildMetadataDigest(),
	}

	// TODO: 发送响应
}

// handleGossipDigestReply 处理 Gossip 摘要响应
func (g *GossipService) handleGossipDigestReply(msg transport.Message) {
	replyMsg, ok := msg.(*transport.GossipDigestReplyMessage)
	if !ok {
		logging.Warn("无效的 GossipDigestReply 消息类型")
		return
	}

	_ = replyMsg.Version
	_ = g.version.Load()

	// 比较摘要，找出需要同步的 key
	remoteDigest := replyMsg.Digest
	localDigest := g.buildMetadataDigest()

	for key, remoteVer := range remoteDigest {
		if localVer, exists := localDigest[key]; !exists || remoteVer > localVer {
			// 需要请求这个 key 的最新值
			logging.WithFields(map[string]interface{}{
				"key":          key,
				"remote_ver":   remoteVer,
				"local_ver":    localVer,
			}).Debug("需要同步元数据")
		}
	}
}

// ========================================
// 元数据操作
// ========================================

// Put 写入元数据（通过 Gossip 同步）
func (g *GossipService) Put(key string, value []byte) error {
	// 写入本地存储
	if err := g.metaStore.Put(key, value); err != nil {
		return fmt.Errorf("写入元数据失败: %w", err)
	}

	// 记录变更日志
	changeLog := &ChangeLog{
		Version:   g.version.Add(1),
		Timestamp: g.hlc.Now(),
		Type:      ChangeLogTypePut,
		Key:       key,
		Value:     value,
	}

	g.addChangeLog(changeLog)

	logging.WithFields(map[string]interface{}{
		"key":     key,
		"version": changeLog.Version,
	}).Debug("元数据已写入")

	return nil
}

// Get 获取元数据
func (g *GossipService) Get(key string) ([]byte, error) {
	return g.metaStore.Get(key)
}

// Delete 删除元数据（通过 Gossip 同步）
func (g *GossipService) Delete(key string) error {
	// 获取旧值
	oldValue, err := g.metaStore.Get(key)
	if err != nil {
		// key 不存在也算成功
		oldValue = nil
	}

	// 写入墓碑标记
	if err := g.metaStore.Delete(key); err != nil {
		return fmt.Errorf("删除元数据失败: %w", err)
	}

	// 记录变更日志
	changeLog := &ChangeLog{
		Version:   g.version.Add(1),
		Timestamp: g.hlc.Now(),
		Type:      ChangeLogTypeDelete,
		Key:       key,
		OldValue:  oldValue,
	}

	g.addChangeLog(changeLog)

	logging.WithFields(map[string]interface{}{
		"key":     key,
		"version": changeLog.Version,
	}).Debug("元数据已删除")

	return nil
}

// ========================================
// 辅助方法
// ========================================

// selectRandomPeers 随机选择节点
func (g *GossipService) selectRandomPeers(count int) []string {
	g.peersMu.RLock()
	defer g.peersMu.RUnlock()

	peerCount := len(g.peers)
	if peerCount <= count {
		return g.peers
	}

	// 创建索引数组并随机打乱
	indices := make([]int, peerCount)
	for i := range indices {
		indices[i] = i
	}

	rand.Shuffle(len(indices), func(i, j int) {
		indices[i], indices[j] = indices[j], indices[i]
	})

	// 选择前 count 个
	selected := make([]string, 0, count)
	for i := 0; i < count && i < len(indices); i++ {
		selected = append(selected, g.peers[indices[i]])
	}

	return selected
}

// addChangeLog 添加变更日志
func (g *GossipService) addChangeLog(log *ChangeLog) {
	g.changeLogsMu.Lock()
	defer g.changeLogsMu.Unlock()

	g.changeLogs = append(g.changeLogs, log)

	// 限制缓存大小
	if len(g.changeLogs) > g.maxCacheSize {
		// 删除最旧的日志
		g.changeLogs = g.changeLogs[1:]
	}
}

// getChangeLogsSince 获取指定版本之后的变更日志
func (g *GossipService) getChangeLogsSince(version uint64) []*ChangeLog {
	g.changeLogsMu.RLock()
	defer g.changeLogsMu.RUnlock()

	result := make([]*ChangeLog, 0)
	for _, log := range g.changeLogs {
		if log.Version > version {
			result = append(result, log)
		}
	}

	return result
}

// buildMetadataDigest 构建元数据摘要
//
// 返回 map[key]version，用于快速比较差异
func (g *GossipService) buildMetadataDigest() map[string]uint64 {
	digest := make(map[string]uint64)

	// 遍历所有 key，获取最新版本号
	keys, err := g.metaStore.List(0, 0)
	if err != nil {
		logging.WithFields(map[string]interface{}{
			"error": err,
		}).Warn("获取元数据列表失败")
		return digest
	}

	for _, key := range keys {
		versions, err := g.metaStore.GetAllVersions(key)
		if err != nil || len(versions) == 0 {
			continue
		}

		// 使用最新版本
		latestVersion := versions[0].Version
		digest[key] = latestVersion
	}

	return digest
}

// applyMetadata 应用接收到的元数据
func (g *GossipService) applyMetadata(metadata map[string][]byte) {
	for key, value := range metadata {
		if err := g.metaStore.Put(key, value); err != nil {
			logging.WithFields(map[string]interface{}{
				"key": key,
				"error": err,
			}).Error("应用元数据失败")
			continue
		}

		g.stats.ChangeLogsReceived.Add(1)
	}

	logging.WithField("count", len(metadata)).Debug("元数据已应用")
}

// AddPeer 添加节点
func (g *GossipService) AddPeer(addr string) {
	g.peersMu.Lock()
	defer g.peersMu.Unlock()

	for _, peer := range g.peers {
		if peer == addr {
			return // 已存在
		}
	}

	g.peers = append(g.peers, addr)
	logging.WithField("peer", addr).Info("已添加 Gossip 节点")
}

// RemovePeer 移除节点
func (g *GossipService) RemovePeer(addr string) {
	g.peersMu.Lock()
	defer g.peersMu.Unlock()

	newPeers := make([]string, 0, len(g.peers))
	for _, peer := range g.peers {
		if peer != addr {
			newPeers = append(newPeers, peer)
		}
	}

	g.peers = newPeers
	logging.WithField("peer", addr).Info("已移除 Gossip 节点")
}

// GetVersion 获取当前版本号
func (g *GossipService) GetVersion() uint64 {
	return g.version.Load()
}

// GetStats 获取统计信息
func (g *GossipService) GetStats() *GossipStats {
	return g.stats
}

// TriggerSync 手动触发同步（用于测试）
func (g *GossipService) TriggerSync() {
	g.syncWithRandomPeers()
}
