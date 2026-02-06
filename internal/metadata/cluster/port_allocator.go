// Package cluster 提供端口分配器实现
//
// PortAllocator：基于 MVStore 的确定性端口分配器
//   - 确定性：同一 host_id 始终获得相同的端口对
//   - 范围：TCP 端口 [9000, 32767]，UDP 端口自动 = TCP + 1
//   - 持久化：使用 MVStore 持久化分配记录，支持多进程环境
//   - 冲突检测：自动检测并避免端口冲突
//   - 重试机制：冲突时自动重试
package cluster

import (
	"crypto/md5"
	"encoding/binary"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/jzhang405/NexKV/internal/wal"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	minTCPPort           = 9000               // 最小 TCP 端口
	maxTCPPort           = 32767              // 最大 TCP 端口
	portAllocationPrefix = "port_allocation:" // 端口分配记录前缀
	retryHostIDSuffix    = "-retry"           // 重试时的 host_id 后缀
)

// PortAllocation 端口分配记录（持久化到 MVStore）
type PortAllocation struct {
	HostID      string `msgpack:"host_id"`      // 机器唯一标识
	TCPPort     int    `msgpack:"tcp_port"`     // 分配的 TCP 端口
	UDPPort     int    `msgpack:"udp_port"`     // 分配的 UDP 端口（UDP = TCP + 1）
	AllocatedAt int64  `msgpack:"allocated_at"` // 分配时间戳（Unix 秒）
}

// PortAllocator 端口分配器（基于 MVStore）
type PortAllocator struct {
	metadataStore store.MVStore  // 元数据存储（已有组件）
	tcpToHostID   map[int]string // TCP 端口 → HostID 反向索引（内存缓存，O(1) 冲突检测）
	mu            sync.RWMutex   // 保护 tcpToHostID map
}

// NewPortAllocator 创建端口分配器
func NewPortAllocator(metadataStore store.MVStore) *PortAllocator {
	pa := &PortAllocator{
		metadataStore: metadataStore,
		tcpToHostID:   make(map[int]string),
	}

	// 从 MVStore 加载现有的端口分配到反向索引
	pa.loadExistingAllocations()

	return pa
}

// loadExistingAllocations 启动时加载现有的端口分配到反向索引
func (pa *PortAllocator) loadExistingAllocations() {
	keys, err := pa.metadataStore.ListPrefix(portAllocationPrefix, 0, -1)
	if err != nil {
		return
	}

	pa.mu.Lock()
	defer pa.mu.Unlock()

	for _, key := range keys {
		data, err := pa.metadataStore.Get(key)
		if err != nil {
			continue
		}

		var allocation PortAllocation
		if err := msgpack.Unmarshal(data, &allocation); err != nil {
			continue
		}

		pa.tcpToHostID[allocation.TCPPort] = allocation.HostID
	}
}

// AllocTCPPort 基于 host_id 分配 TCP 端口（UDP = TCP + 1）
//
// 分配流程：
//  1. 检查是否已分配（从 MVStore 读取）
//  2. 计算 MD5 哈希
//  3. 映射到端口范围 [9000, 32767]
//  4. UDP 端口 = TCP 端口 + 1
//  5. 检查端口冲突
//  6. 持久化分配记录到 MVStore
func (pa *PortAllocator) AllocTCPPort(hostID string) (tcpPort, udpPort int, err error) {
	portRange := maxTCPPort - minTCPPort + 1

	// 步骤 1: 检查是否已分配
	allocated, err := pa.checkExistingAllocation(hostID)
	if err == nil && allocated != nil {
		return allocated.TCPPort, allocated.UDPPort, nil
	}

	// 步骤 2: 计算 MD5 哈希并映射到端口范围
	hash := md5.Sum([]byte(hostID))
	hashUint32 := binary.BigEndian.Uint32(hash[:4])
	tcpPort = minTCPPort + int(hashUint32%uint32(portRange))

	// 步骤 3: UDP 端口 = TCP 端口 + 1
	udpPort = tcpPort + 1

	// 步骤 4: 检查端口冲突
	conflict, _, err := pa.checkPortConflict(tcpPort)
	if err != nil {
		return 0, 0, types.NewClusterPortConflictCheckFailedError(err)
	}

	if conflict {
		// P1-1 修复：端口冲突时递增端口号重试（而不是修改 hostID）
		// 递增端口号，直到找到可用端口
		for tcpPort++; tcpPort <= maxTCPPort; tcpPort++ {
			conflict, _, err := pa.checkPortConflict(tcpPort)
			if err != nil {
				return 0, 0, types.NewClusterPortConflictCheckFailedError(err)
			}
			if !conflict {
				// 找到可用端口
				udpPort = tcpPort + 1
				break
			}
		}

		// 检查是否端口耗尽
		if tcpPort > maxTCPPort {
			return 0, 0, types.NewClusterPortExhaustedError()
		}
	}

	// 步骤 5: 持久化分配记录
	allocation := &PortAllocation{
		HostID:      hostID,
		TCPPort:     tcpPort,
		UDPPort:     udpPort,
		AllocatedAt: time.Now().Unix(),
	}

	if err := pa.saveAllocation(allocation); err != nil {
		return 0, 0, types.NewClusterPortAllocationSaveFailedError(err)
	}

	return tcpPort, udpPort, nil
}

// checkExistingAllocation 检查 host_id 是否已分配端口
func (pa *PortAllocator) checkExistingAllocation(hostID string) (*PortAllocation, error) {
	key := portAllocationPrefix + hostID
	data, err := pa.metadataStore.Get(key)
	if err != nil {
		return nil, err
	}

	var allocation PortAllocation
	if err := msgpack.Unmarshal(data, &allocation); err != nil {
		return nil, types.NewClusterPortAllocationUnmarshalFailedError(err)
	}

	return &allocation, nil
}

// checkPortConflict 检查端口是否已被占用
//
// 返回值：
//   - conflict: 是否存在冲突
//   - conflictingHostID: 占用该端口的 host_id（如果存在）
//   - err: 错误信息
//
// P0-2 优化：使用反向索引，O(n) → O(1)
func (pa *PortAllocator) checkPortConflict(tcpPort int) (conflict bool, conflictingHostID string, err error) {
	pa.mu.RLock()
	hostID, exists := pa.tcpToHostID[tcpPort]
	pa.mu.RUnlock()

	if exists {
		return true, hostID, nil
	}

	return false, "", nil
}

// saveAllocation 持久化端口分配记录
func (pa *PortAllocator) saveAllocation(allocation *PortAllocation) error {
	key := portAllocationPrefix + allocation.HostID
	data, err := msgpack.Marshal(allocation)
	if err != nil {
		return types.NewClusterPortAllocationMarshalFailedError(err)
	}

	if err := pa.metadataStore.Put(key, data); err != nil {
		return types.NewClusterPortAllocationSaveFailedError(err)
	}

	// 更新反向索引（加写锁）
	pa.mu.Lock()
	pa.tcpToHostID[allocation.TCPPort] = allocation.HostID
	pa.mu.Unlock()

	return nil
}

// ReleasePort 释放端口（用于故障转移等场景）
func (pa *PortAllocator) ReleasePort(hostID string) error {
	// 先获取分配记录以知道要删除哪个 TCP 端口
	allocation, err := pa.checkExistingAllocation(hostID)
	if err != nil {
		// 不存在，直接返回成功
		return nil
	}

	key := portAllocationPrefix + hostID
	if err := pa.metadataStore.Delete(key); err != nil {
		return types.NewClusterPortReleaseFailedError(err)
	}

	// 从反向索引删除（加写锁）
	pa.mu.Lock()
	delete(pa.tcpToHostID, allocation.TCPPort)
	pa.mu.Unlock()

	return nil
}

// GetAllocation 获取指定 host_id 的端口分配记录
func (pa *PortAllocator) GetAllocation(hostID string) (*PortAllocation, error) {
	return pa.checkExistingAllocation(hostID)
}

// ListAllAllocations 列出所有端口分配记录
func (pa *PortAllocator) ListAllAllocations() ([]*PortAllocation, error) {
	keys, err := pa.metadataStore.ListPrefix(portAllocationPrefix, 0, -1)
	if err != nil {
		return nil, types.NewClusterPortAllocationListFailedError(err)
	}

	allocations := make([]*PortAllocation, 0, len(keys))
	for _, key := range keys {
		data, err := pa.metadataStore.Get(key)
		if err != nil {
			continue
		}

		var allocation PortAllocation
		if err := msgpack.Unmarshal(data, &allocation); err != nil {
			continue
		}

		allocations = append(allocations, &allocation)
	}

	return allocations, nil
}

// GetAllocatedPortCount 获取已分配的端口数量
func (pa *PortAllocator) GetAllocatedPortCount() (int, error) {
	keys, err := pa.metadataStore.ListPrefix(portAllocationPrefix, 0, -1)
	if err != nil {
		return 0, types.NewClusterPortAllocationListFailedError(err)
	}

	return len(keys), nil
}
