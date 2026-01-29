// Package cluster 提供 HostManager 实现
//
// HostManager：物理机器层拓扑管理器
//   - 管理物理机器（Host）的注册、删除、查询
//   - 维护 HostID → Host 映射
//   - 提供拓扑查询接口
//   - 持久化到 MVStore
package cluster

import (
	"sync"

	"github.com/jzhang405/NexKV/internal/metadata/store"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	hostKeyPrefix = "host:" // Host 存储键前缀
)

// HostManager 物理机器拓扑管理器
type HostManager struct {
	metadataStore store.MVStore
	hosts         map[string]*Host // HostID → Host 映射（内存缓存）
	mu            sync.RWMutex     // 读写锁，保护 hosts map
}

// NewHostManager 创建 HostManager
func NewHostManager(metadataStore store.MVStore) *HostManager {
	return &HostManager{
		metadataStore: metadataStore,
		hosts:         make(map[string]*Host),
	}
}

// AddHost 添加物理机器
//
// 流程：
//  1. 验证 Host 参数（HostID、Hostname、Role）
//  2. 验证 HostRole 到 NodeID 约束（调用 ValidateNodeIDs）
//  3. 持久化到 MVStore（key: host:{hostID}）
//  4. 更新内存缓存
func (hm *HostManager) AddHost(host *Host) error {
	if host == nil {
		return types.NewClusterNilParameterError("host")
	}

	// 兼容 HostID 和 ID 字段
	hostID := host.HostID
	if hostID == "" {
		hostID = host.ID
	}

	if hostID == "" {
		return types.NewClusterHostIDRequiredError()
	}

	if host.Hostname == "" {
		return types.NewClusterHostnameRequiredError()
	}

	// 验证 HostRole 到 NodeID 约束
	if err := host.ValidateNodeIDs(); err != nil {
		return types.NewClusterInvalidNodeIDConstraintsError(err)
	}

	// 持久化到 MVStore
	key := hostKeyPrefix + hostID
	data, err := msgpack.Marshal(host)
	if err != nil {
		return types.NewClusterHostMarshalFailedError(err)
	}

	if err := hm.metadataStore.Put(key, data); err != nil {
		return types.NewClusterHostSaveFailedError(err)
	}

	// 更新内存缓存（加写锁）
	hm.mu.Lock()
	hm.hosts[hostID] = host
	hm.mu.Unlock()

	return nil
}

// GetHost 获取物理机器信息
//
// 先查询内存缓存，未命中再查询 MVStore
func (hm *HostManager) GetHost(hostID string) (*Host, error) {
	// 查询内存缓存（加读锁）
	hm.mu.RLock()
	if host, exists := hm.hosts[hostID]; exists {
		hm.mu.RUnlock()
		return host, nil
	}
	hm.mu.RUnlock()

	// 查询 MVStore
	key := hostKeyPrefix + hostID
	data, err := hm.metadataStore.Get(key)
	if err != nil {
		return nil, types.NewClusterHostNotFoundError(hostID)
	}

	var host Host
	if err := msgpack.Unmarshal(data, &host); err != nil {
		return nil, types.NewClusterHostUnmarshalFailedError(err)
	}

	// 更新内存缓存（加写锁）
	hm.mu.Lock()
	hm.hosts[hostID] = &host
	hm.mu.Unlock()

	return &host, nil
}

// RemoveHost 移除物理机器
//
// 流程：
//  1. 从 MVStore 删除
//  2. 从内存缓存删除
func (hm *HostManager) RemoveHost(hostID string) error {
	key := hostKeyPrefix + hostID
	if err := hm.metadataStore.Delete(key); err != nil {
		return types.NewClusterHostDeleteFailedError(err)
	}

	// 从内存缓存删除（加写锁）
	hm.mu.Lock()
	delete(hm.hosts, hostID)
	hm.mu.Unlock()

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
// P1-2 修复：在锁保护下完成整个更新流程，避免数据竞争
//   - 先从内存缓存查找，未命中则从 MVStore 加载
//   - 在锁保护下更新字段并持久化
//   - 避免并发场景下的数据覆盖和丢失
func (hm *HostManager) UpdateHostStatus(hostID string, status HostStatus, lastHeartbeat int64) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// 步骤 1: 从内存缓存查找
	host, exists := hm.hosts[hostID]

	// 步骤 2: 内存缓存未命中，从 MVStore 加载
	if !exists {
		key := hostKeyPrefix + hostID
		data, err := hm.metadataStore.Get(key)
		if err != nil {
			return types.NewClusterHostNotFoundError(hostID)
		}

		var loadedHost Host
		if err := msgpack.Unmarshal(data, &loadedHost); err != nil {
			return types.NewClusterHostUnmarshalFailedError(err)
		}

		// 更新内存缓存
		host = &loadedHost
		hm.hosts[hostID] = host
	}

	// 步骤 3: 在锁保护下更新字段
	host.HostStatus = status
	host.LastHeartbeat = lastHeartbeat

	// 步骤 4: 持久化到 MVStore
	key := hostKeyPrefix + hostID
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
		// 兼容 HostStatus 和 Status 字段
		if host.HostStatus == HostStatusOnline {
			online = append(online, host)
		} else if host.Status == "active" || host.Status == "online" {
			// 兼容旧的状态字符串
			online = append(online, host)
		}
	}

	return online, nil
}
