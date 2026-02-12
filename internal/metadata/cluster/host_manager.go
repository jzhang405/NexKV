// Package cluster 提供 HostManager 实现
//
// HostManager：物理机器层拓扑管理器
//   - 管理物理机器（Host）的注册、删除、查询
//   - 提供拓扑查询接口
//   - 持久化到 MVStore（单一数据源）
//
// 架构优化（P2-5）：
//   - 移除内存 map 缓存，使用 MVStore 作为唯一数据源
//   - MVStore (MemoryMVStore) 本身使用 sync.Map，已是高性能内存存储
//   - 简化代码，消除"缓存上的缓存"带来的复杂度和一致性问题
package cluster

import (
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/jzhang405/NexKV/internal/wal"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	hostKeyPrefix = "host:" // Host 存储键前缀
)

// HostManager 物理机器拓扑管理器
//
// 架构优化（P2-5）：使用 MVStore 作为单一数据源，无额外缓存
type HostManager struct {
	metadataStore store.MVStore // 唯一数据源（MVStore 本身已是 sync.Map 实现）
}

// NewHostManager 创建 HostManager
//
// 架构优化（P2-5）：简化初始化，无需额外缓存
func NewHostManager(metadataStore store.MVStore) *HostManager {
	return &HostManager{
		metadataStore: metadataStore,
	}
}

// AddHost 添加物理机器
//
// 流程：
// 1. 验证 Host 参数（HostID、Hostname、Role）
// 2. 验证 HostRole 到 NodeID 约束（调用 ValidateNodeIDs）
// 3. 持久化到 MVStore（key: host:{hostID}）
//
// 架构优化（P2-5）：移除缓存更新逻辑，直接持久化到 MVStore
func (hm *HostManager) AddHost(host *Host) error {
	if host == nil {
		return types.NewClusterNilParameterError("host")
	}

	if host.HostID == "" {
		return types.NewClusterHostIDRequiredError()
	}

	if host.Hostname == "" {
		return types.NewClusterHostnameRequiredError()
	}

	// 验证 HostRole 到 NodeID 约束
	if err := host.ValidateNodeIDs(); err != nil {
		return types.NewClusterInvalidNodeIDConstraintsError(err)
	}

	// 持久化到 MVStore（直接写入，无需缓存更新）
	key := hostKeyPrefix + host.HostID
	data, err := msgpack.Marshal(host)
	if err != nil {
		return types.NewClusterHostMarshalFailedError(err)
	}

	if err := hm.metadataStore.Put(key, data); err != nil {
		return types.NewClusterHostSaveFailedError(err)
	}

	return nil
}

// GetHost 获取物理机器信息
//
// 架构优化（P2-5）：直接从 MVStore 读取（已是内存操作，O(1)）
func (hm *HostManager) GetHost(hostID string) (*Host, error) {
	// 从 MVStore 读取（MVStore.Get 是 sync.Map 操作，高性能）
	key := hostKeyPrefix + hostID
	data, err := hm.metadataStore.Get(key)
	if err != nil {
		return nil, types.NewClusterHostNotFoundError(hostID)
	}

	var host Host
	if err := msgpack.Unmarshal(data, &host); err != nil {
		return nil, types.NewClusterHostUnmarshalFailedError(err)
	}

	return &host, nil
}

// RemoveHost 移除物理机器
//
// 流程：
// 1. 从 MVStore 删除
//
// 架构优化（P2-5）：移除缓存删除逻辑
func (hm *HostManager) RemoveHost(hostID string) error {
	key := hostKeyPrefix + hostID
	if err := hm.metadataStore.Delete(key); err != nil {
		return types.NewClusterHostDeleteFailedError(err)
	}

	return nil
}

// ListAllHosts 列出所有物理机器
//
// 从 MVStore 读取所有 Host 记录
func (hm *HostManager) ListAllHosts() ([]*Host, error) {
	keys, err := hm.metadataStore.ListPrefix(hostKeyPrefix, 0, -1)
	if err != nil {
		return nil, types.NewClusterHostListFailedError(err)
	}

	hosts := make([]*Host, 0, len(keys))
	for _, key := range keys {
		data, err := hm.metadataStore.Get(key)
		if err != nil {
			continue
		}

		var host Host
		if err := msgpack.Unmarshal(data, &host); err != nil {
			continue
		}

		hosts = append(hosts, &host)
	}

	return hosts, nil
}

// GetHostTopology 获取 Host 拓扑结构
//
// 返回所有 Host 及其关联的 NodeID
func (hm *HostManager) GetHostTopology() (map[string]*Host, error) {
	hosts, err := hm.ListAllHosts()
	if err != nil {
		return nil, err
	}

	topology := make(map[string]*Host, len(hosts))
	for _, host := range hosts {
		topology[host.HostID] = host
	}

	return topology, nil
}

// GetHostCount 获取物理机器数量
func (hm *HostManager) GetHostCount() (int, error) {
	keys, err := hm.metadataStore.ListPrefix(hostKeyPrefix, 0, -1)
	if err != nil {
		return 0, types.NewClusterHostListFailedError(err)
	}

	return len(keys), nil
}

// UpdateHostStatus 更新 Host 状态和心跳时间
//
// 架构优化（P2-5）：简化为直接读写 MVStore，移除复杂锁和回滚逻辑
//
// 优化理由：
// 1. MVStore 本身是并发安全的（sync.Map）
// 2. 不再需要手动维护内存缓存与磁盘的一致性
// 3. 持久化失败时 MVStore 会保证内部一致性
func (hm *HostManager) UpdateHostStatus(hostID string, status HostStatus, lastHeartbeat int64) error {
	// 先加载现有 Host
	host, err := hm.loadHost(hostID)
	if err != nil {
		return err
	}

	// 更新字段
	host.HostStatus = status
	host.LastHeartbeat = lastHeartbeat

	// 持久化到 MVStore
	return hm.persistHost(host)
}

// loadHost 从 MVStore 加载 Host（辅助方法）
//
// 架构优化（P2-5）：简化的加载方法，无缓存层
func (hm *HostManager) loadHost(hostID string) (*Host, error) {
	key := hostKeyPrefix + hostID
	data, err := hm.metadataStore.Get(key)
	if err != nil {
		return nil, types.NewClusterHostNotFoundError(hostID)
	}

	var host Host
	if err := msgpack.Unmarshal(data, &host); err != nil {
		return nil, types.NewClusterHostUnmarshalFailedError(err)
	}

	return &host, nil
}

// persistHost 持久化 Host 到 MVStore（辅助方法）
//
// 架构优化（P2-5）：简化的持久化方法，无需回滚
// 理由：MVStore 内部保证一致性，无需应用层回滚机制
func (hm *HostManager) persistHost(host *Host) error {
	key := hostKeyPrefix + host.HostID
	data, err := msgpack.Marshal(host)
	if err != nil {
		return types.NewClusterHostMarshalFailedError(err)
	}

	if err := hm.metadataStore.Put(key, data); err != nil {
		return types.NewClusterHostSaveFailedError(err)
	}

	return nil
}

// GetHostsByRole 根据 HostRole 筛选 Host
func (hm *HostManager) GetHostsByRole(role HostRole) ([]*Host, error) {
	hosts, err := hm.ListAllHosts()
	if err != nil {
		return nil, err
	}

	filtered := make([]*Host, 0)
	for _, host := range hosts {
		if host.Role == role {
			filtered = append(filtered, host)
		}
	}

	return filtered, nil
}

// GetOnlineHosts 获取在线状态的 Host
func (hm *HostManager) GetOnlineHosts() ([]*Host, error) {
	hosts, err := hm.ListAllHosts()
	if err != nil {
		return nil, err
	}

	online := make([]*Host, 0)
	for _, host := range hosts {
		if host.HostStatus == HostStatusOnline {
			online = append(online, host)
		}
	}

	return online, nil
}
