// Package consensus 提供一致性协议实现
//
// MetadataStore 元数据存储服务
// 统一的元数据 API，根据变更类型选择一致性协议
package consensus

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/store"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/jzhang405/NexKV/internal/metadata/uuid"
)

// MetadataStore 元数据存储服务
//
// 统一的元数据 API，自动选择一致性协议：
//   - 关键变更：2PC（强一致性，全员commit或rollback）
//   - 重要变更：Quorum（增强的最终一致性，多数派确认）
//   - 普通变更：Gossip（最终一致性，异步扩散）
//
// 变更类型分类:
//   - 关键变更：分片创建/删除（跨多个分片的事务操作）
//   - 重要变更：分片创建/删除、主副本切换、节点加入/离开
//   - 普通变更：节点状态更新、负载信息刷新
type MetadataStore struct {
	// 配置
	config *MetadataStoreConfig

	// 依赖
	gossipService *GossipService
	quorumService *QuorumService
	twoPCService  *TwoPCService
	transport     transport.Transport
	hlc           *clock.HLC
	uuidGen       uuid.UUIDGenerator

	// 生命周期
	started atomic.Bool
	stopped atomic.Bool

	// 变更日志追踪
	changeLogs   []*MetadataChangeLog
	changeLogsMu sync.RWMutex
	version      atomic.Uint64
}

// MetadataStoreConfig 元数据存储配置
type MetadataStoreConfig struct {
	// 关键变更前缀列表
	// 匹配这些前缀的 key 将使用 Quorum
	CriticalPrefixes []string

	// 自动分类是否启用
	EnableAutoClassify bool
}

// DefaultMetadataStoreConfig 返回默认配置
func DefaultMetadataStoreConfig() *MetadataStoreConfig {
	return &MetadataStoreConfig{
		CriticalPrefixes: []string{
			"shard/",   // 分片元数据
			"replica/", // 副本元数据
			"node/",    // 节点元数据（变更）
		},
		EnableAutoClassify: true,
	}
}

// MetadataChangeLog 元数据变更日志
type MetadataChangeLog struct {
	// 版本号
	Version uint64

	// HLC 时间戳
	Timestamp *clock.HLC

	// 变更类型
	Type ChangeType

	// Key 键
	Key string

	// Value 新值（可选）
	Value []byte

	// OldValue 旧值（可选）
	OldValue []byte

	// 使用的一致性协议
	ConsensusProtocol ConsensusProtocol
}

// ChangeType 变更类型
type ChangeType int

const (
	// ChangeTypeCreate 创建
	ChangeTypeCreate ChangeType = iota

	// ChangeTypeUpdate 更新
	ChangeTypeUpdate

	// ChangeTypeDelete 删除
	ChangeTypeDelete
)

// String 返回变更类型的字符串表示
func (ct ChangeType) String() string {
	switch ct {
	case ChangeTypeCreate:
		return "Create"
	case ChangeTypeUpdate:
		return "Update"
	case ChangeTypeDelete:
		return "Delete"
	default:
		return "Unknown"
	}
}

// ConsensusProtocol 一致性协议类型
type ConsensusProtocol int

const (
	// ConsensusProtocolGossip Gossip 协议（最终一致性）
	ConsensusProtocolGossip ConsensusProtocol = iota

	// ConsensusProtocolQuorum Quorum 协议（增强的最终一致性）
	ConsensusProtocolQuorum

	// ConsensusProtocolTwoPC 2PC 协议（强一致性）
	ConsensusProtocolTwoPC
)

// String 返回协议类型的字符串表示
func (p ConsensusProtocol) String() string {
	switch p {
	case ConsensusProtocolGossip:
		return "Gossip"
	case ConsensusProtocolQuorum:
		return "Quorum"
	case ConsensusProtocolTwoPC:
		return "2PC"
	default:
		return "Unknown"
	}
}

// NewMetadataStore 创建元数据存储服务
func NewMetadataStore(
	mvStore store.MVStore,
	transport transport.Transport,
	hlc *clock.HLC,
	uuidGen uuid.UUIDGenerator,
	localAddr string,
	nodes []string,
	config *MetadataStoreConfig,
) (*MetadataStore, error) {
	if config == nil {
		config = DefaultMetadataStoreConfig()
	}

	// 创建 Gossip 服务
	gossipConfig := DefaultGossipConfig()
	gossipService, err := NewGossipService(mvStore, transport, hlc, nodes, gossipConfig)
	if err != nil {
		return nil, fmt.Errorf("创建 Gossip 服务失败: %w", err)
	}

	// 创建 Quorum 服务
	quorumConfig := DefaultQuorumConfig()
	quorumService, err := NewQuorumService(mvStore, transport, hlc, localAddr, nodes, quorumConfig)
	if err != nil {
		return nil, fmt.Errorf("创建 Quorum 服务失败: %w", err)
	}

	// 创建 2PC 服务
	twoPCConfig := DefaultTwoPCConfig()
	twoPCService, err := NewTwoPCService(mvStore, transport, hlc, uuidGen, localAddr, nodes, twoPCConfig)
	if err != nil {
		return nil, fmt.Errorf("创建 2PC 服务失败: %w", err)
	}

	service := &MetadataStore{
		config:        config,
		gossipService: gossipService,
		quorumService: quorumService,
		twoPCService:  twoPCService,
		transport:     transport,
		hlc:           hlc,
		uuidGen:       uuidGen,
		changeLogs:    make([]*MetadataChangeLog, 0),
	}

	return service, nil
}

// Start 启动元数据存储服务
func (m *MetadataStore) Start() error {
	if !m.started.CompareAndSwap(false, true) {
		return fmt.Errorf("元数据存储服务已经启动")
	}

	logging.Info("启动元数据存储服务")

	// 启动 Gossip 服务
	if err := m.gossipService.Start(); err != nil {
		m.started.Store(false)
		return fmt.Errorf("启动 Gossip 服务失败: %w", err)
	}

	// 启动 Quorum 服务
	if err := m.quorumService.Start(); err != nil {
		_ = m.gossipService.Stop()
		m.started.Store(false)
		return fmt.Errorf("启动 Quorum 服务失败: %w", err)
	}

	// 启动 2PC 服务
	if err := m.twoPCService.Start(); err != nil {
		_ = m.gossipService.Stop()
		_ = m.quorumService.Stop()
		m.started.Store(false)
		return fmt.Errorf("启动 2PC 服务失败: %w", err)
	}

	logging.Info("元数据存储服务启动成功")
	return nil
}

// Stop 停止元数据存储服务
func (m *MetadataStore) Stop() error {
	if !m.stopped.CompareAndSwap(false, true) {
		return nil
	}

	logging.Info("停止元数据存储服务...")

	// 停止 2PC 服务
	_ = m.twoPCService.Stop()

	// 停止 Quorum 服务
	_ = m.quorumService.Stop()

	// 停止 Gossip 服务
	_ = m.gossipService.Stop()

	logging.Info("元数据存储服务已停止")
	return nil
}

// ========================================
// 元数据操作 API
// ========================================

// Put 写入元数据
//
// 自动选择一致性协议：
//   - 关键变更：Quorum（阻塞等待）
//   - 普通变更：Gossip（异步扩散）
func (m *MetadataStore) Put(
	ctx context.Context,
	key string,
	value []byte,
) error {
	// 判断变更类型
	changeType := m.classifyChangeType(key, value)

	// 选择一致性协议
	protocol := m.selectProtocol(key, changeType)

	logging.WithFields(map[string]any{
		"key":             key,
		"change_type":     changeType,
		"consensus_proto": protocol,
	}).Debug("写入元数据")

	switch protocol {
	case ConsensusProtocolQuorum:
		return m.putWithQuorum(ctx, key, value)

	case ConsensusProtocolGossip:
		return m.putWithGossip(key, value)

	default:
		return fmt.Errorf("未知的一致性协议: %v", protocol)
	}
}

// Get 获取元数据
func (m *MetadataStore) Get(key string) ([]byte, error) {
	return m.gossipService.Get(key)
}

// Delete 删除元数据
func (m *MetadataStore) Delete(
	ctx context.Context,
	key string,
) error {
	// 判断变更类型
	changeType := ChangeTypeDelete

	// 选择一致性协议
	protocol := m.selectProtocol(key, changeType)

	logging.WithFields(map[string]any{
		"key":             key,
		"change_type":     changeType,
		"consensus_proto": protocol,
	}).Debug("删除元数据")

	switch protocol {
	case ConsensusProtocolQuorum:
		return m.deleteWithQuorum(ctx, key)

	case ConsensusProtocolGossip:
		return m.deleteWithGossip(key)

	default:
		return fmt.Errorf("未知的一致性协议: %v", protocol)
	}
}

// ExecuteTransaction 执行分布式事务（2PC）
func (m *MetadataStore) ExecuteTransaction(
	ctx context.Context,
	operations []transport.Operation,
) error {
	return m.twoPCService.Execute(ctx, operations)
}

// ========================================
// Quorum 路径
// ========================================

// putWithQuorum 使用 Quorum 机制写入元数据
func (m *MetadataStore) putWithQuorum(
	ctx context.Context,
	key string,
	value []byte,
) error {
	// 创建提案
	proposal := &transport.QuorumProposeMessage{
		Key:       key,
		Value:     value,
		Operation: "put",
	}

	// 提交提案
	err := m.quorumService.Propose(ctx, proposal)
	if err != nil {
		return err
	}

	// 记录变更日志
	changeType := m.classifyChangeType(key, value)
	m.addChangeLog(changeType, key, value)

	return nil
}

// deleteWithQuorum 使用 Quorum 机制删除元数据
func (m *MetadataStore) deleteWithQuorum(
	ctx context.Context,
	key string,
) error {
	// 创建提案
	proposal := &transport.QuorumProposeMessage{
		Key:       key,
		Operation: "delete",
	}

	// 提交提案
	err := m.quorumService.Propose(ctx, proposal)
	if err != nil {
		return err
	}

	// 记录变更日志
	m.addChangeLog(ChangeTypeDelete, key, nil)

	return nil
}

// ========================================
// Gossip 路径
// ========================================

// putWithGossip 使用 Gossip 机制写入元数据
func (m *MetadataStore) putWithGossip(
	key string,
	value []byte,
) error {
	// 先写入
	err := m.gossipService.Put(key, value)
	if err != nil {
		return err
	}

	// 记录变更日志
	changeType := m.classifyChangeType(key, value)
	m.addChangeLog(changeType, key, value)

	return nil
}

// deleteWithGossip 使用 Gossip 机制删除元数据
func (m *MetadataStore) deleteWithGossip(
	key string,
) error {
	// 先删除
	err := m.gossipService.Delete(key)
	if err != nil {
		return err
	}

	// 记录变更日志
	m.addChangeLog(ChangeTypeDelete, key, nil)

	return nil
}

// ========================================
// 辅助方法
// ========================================

// addChangeLog 添加变更日志
func (m *MetadataStore) addChangeLog(
	changeType ChangeType,
	key string,
	value []byte,
) {
	m.changeLogsMu.Lock()
	defer m.changeLogsMu.Unlock()

	version := m.version.Add(1)
	log := &MetadataChangeLog{
		Version:   version,
		Timestamp: m.hlc.Now(),
		Type:      changeType,
		Key:       key,
		Value:     value,
	}

	m.changeLogs = append(m.changeLogs, log)

	// 限制日志数量
	maxLogs := 1000
	if len(m.changeLogs) > maxLogs {
		// 保留最近的日志
		m.changeLogs = m.changeLogs[len(m.changeLogs)-maxLogs:]
	}
}

// classifyChangeType 分类变更类型
func (m *MetadataStore) classifyChangeType(
	key string,
	value []byte,
) ChangeType {
	// 检查 key 是否已存在
	_, err := m.gossipService.Get(key)
	if err != nil {
		// key 不存在，是创建操作
		return ChangeTypeCreate
	}

	// key 已存在，是更新操作
	return ChangeTypeUpdate
}

// selectProtocol 选择一致性协议
//
// 规则:
//  1. 关键前缀匹配 → Quorum
//  2. 其他 → Gossip
func (m *MetadataStore) selectProtocol(
	key string,
	changeType ChangeType,
) ConsensusProtocol {
	// 检查是否匹配关键前缀
	for _, prefix := range m.config.CriticalPrefixes {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			return ConsensusProtocolQuorum
		}
	}

	// 默认使用 Gossip
	return ConsensusProtocolGossip
}

// AddCriticalPrefix 添加关键前缀
func (m *MetadataStore) AddCriticalPrefix(prefix string) {
	m.config.CriticalPrefixes = append(m.config.CriticalPrefixes, prefix)
	logging.WithField("prefix", prefix).Info("已添加关键前缀")
}

// RemoveCriticalPrefix 移除关键前缀
func (m *MetadataStore) RemoveCriticalPrefix(prefix string) {
	newPrefixes := make([]string, 0, len(m.config.CriticalPrefixes))
	for _, p := range m.config.CriticalPrefixes {
		if p != prefix {
			newPrefixes = append(newPrefixes, p)
		}
	}

	m.config.CriticalPrefixes = newPrefixes
	logging.WithField("prefix", prefix).Info("已移除关键前缀")
}

// GetVersion 获取当前版本号
func (m *MetadataStore) GetVersion() uint64 {
	return m.gossipService.GetVersion()
}

// GetChangeLogs 获取变更日志
func (m *MetadataStore) GetChangeLogs(
	sinceVersion uint64,
) []*MetadataChangeLog {
	m.changeLogsMu.RLock()
	defer m.changeLogsMu.RUnlock()

	result := make([]*MetadataChangeLog, 0)
	for _, log := range m.changeLogs {
		if log.Version > sinceVersion {
			result = append(result, log)
		}
	}

	return result
}

// ========================================
// 节点管理
// ========================================

// AddNode 添加节点
func (m *MetadataStore) AddNode(addr string) {
	m.gossipService.AddPeer(addr)
	m.quorumService.AddNode(addr)
	m.twoPCService.AddNode(addr)

	logging.WithField("node", addr).Info("已添加节点到所有一致性服务")
}

// RemoveNode 移除节点
func (m *MetadataStore) RemoveNode(addr string) {
	m.gossipService.RemovePeer(addr)
	m.quorumService.RemoveNode(addr)
	m.twoPCService.RemoveNode(addr)

	logging.WithField("node", addr).Info("已从所有一致性服务移除节点")
}

// ========================================
// 统计信息
// ========================================

// GetStats 获取统计信息
func (m *MetadataStore) GetStats() map[string]any {
	gossipStats := m.gossipService.GetStats()
	quorumStats := m.quorumService.GetStats()
	twoPCStats := m.twoPCService.GetStats()

	return map[string]any{
		"gossip": map[string]any{
			"sync_count":           gossipStats.SyncCount.Load(),
			"sync_success":         gossipStats.SyncSuccess.Load(),
			"sync_failed":          gossipStats.SyncFailed.Load(),
			"change_logs_sent":     gossipStats.ChangeLogsSent.Load(),
			"change_logs_received": gossipStats.ChangeLogsReceived.Load(),
			"version":              gossipStats.LastSyncTime.Load(),
		},
		"quorum": map[string]any{
			"proposals_total":    quorumStats.ProposalsTotal.Load(),
			"proposals_approved": quorumStats.ProposalsApproved.Load(),
			"proposals_rejected": quorumStats.ProposalsRejected.Load(),
			"proposals_timeout":  quorumStats.ProposalsTimeout.Load(),
			"avg_vote_latency":   quorumStats.AvgVoteLatency.Load(),
		},
		"twopc": map[string]any{
			"tx_total":       twoPCStats.TransactionsTotal.Load(),
			"tx_committed":   twoPCStats.TransactionsCommitted.Load(),
			"tx_aborted":     twoPCStats.TransactionsAborted.Load(),
			"tx_timeout":     twoPCStats.TransactionsTimeout.Load(),
			"avg_tx_latency": twoPCStats.AvgTxLatency.Load(),
		},
	}
}
