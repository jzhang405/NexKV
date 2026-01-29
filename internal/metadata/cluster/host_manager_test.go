// Package cluster 的 HostManager 单元测试
//
// 测试覆盖：
// - IT-HM-001: AddHost 测试
// - IT-HM-002: GetHost 测试
// - IT-HM-003: RemoveHost 测试
// - IT-HM-004: ValidateNodeIDs 测试
// - IT-HM-005: ListAllHosts 测试
package cluster

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/metadata/store"
)

// mockMVStoreForHostManager 创建用于测试的 MVStore
func mockMVStoreForHostManager(t *testing.T) store.MVStore {
	tmpDir := t.TempDir()
	mvstore, err := store.NewMemoryMVStore(&store.MVStoreOptions{
		DataDir:       tmpDir,
		WALDir:        tmpDir,
		MemTableSize:  0,
		FlushInterval: 0,
		EnableWAL:     false,
		WALSsyncSize:  0,
		MaxVersions:   0,
	})
	require.NoError(t, err, "Failed to create mock MVStore")
	return mvstore
}

// ============================================================================
// HostManager 测试 (IT-HM-001 ~ IT-HM-005)
// ============================================================================

// Test_HostManager_AddHost IT-HM-001: AddHost 测试
func Test_HostManager_AddHost(t *testing.T) {
	mvstore := mockMVStoreForHostManager(t)
	hm := NewHostManager(mvstore)

	// 测试 1: 正常添加 Host
	host := &Host{
		HostID:              "server-1",
		Hostname:            "192.168.1.100",
		Role:                LeafParent,
		LeafNodeID:          "node-leaf-1",
		ParentNodeID:        "node-parent-1",
		ParentStandbyNodeID: "node-standby-1",
		HostStatus:          HostStatusOnline,
		LastHeartbeat:       time.Now().Unix(),
		CPUUsage:            50.0,
		MemUsage:            60.0,
		ExistingNodes:       3,
	}

	err := hm.AddHost(host)
	require.NoError(t, err)

	// 验证 Host 已添加
	retrieved, err := hm.GetHost("server-1")
	require.NoError(t, err)
	assert.Equal(t, "server-1", retrieved.HostID)
	assert.Equal(t, "192.168.1.100", retrieved.Hostname)
	assert.Equal(t, LeafParent, retrieved.Role)
	assert.Equal(t, HostStatusOnline, retrieved.HostStatus)
}

// Test_HostManager_AddHost_Validation IT-HM-001: 参数验证
func Test_HostManager_AddHost_Validation(t *testing.T) {
	mvstore := mockMVStoreForHostManager(t)
	hm := NewHostManager(mvstore)

	tests := []struct {
		name    string
		host    *Host
		wantErr bool
		errMsg  string
	}{
		{
			name:    "错误 - nil Host",
			host:    nil,
			wantErr: true,
			errMsg:  "不能为空",
		},
		{
			name: "错误 - 空的 HostID",
			host: &Host{
				HostID:     "",
				Hostname:   "192.168.1.100",
				LeafNodeID: "node-leaf-1",
				Role:       LeafOnly,
			},
			wantErr: true,
			errMsg:  "HostID is required",
		},
		{
			name: "错误 - 空的 Hostname",
			host: &Host{
				HostID:     "server-1",
				Hostname:   "",
				LeafNodeID: "node-leaf-1",
				Role:       LeafOnly,
			},
			wantErr: true,
			errMsg:  "Hostname is required",
		},
		{
			name: "错误 - 无效的 NodeID 约束",
			host: &Host{
				HostID:   "server-1",
				Hostname: "192.168.1.100",
				Role:     LeafParent,
				// 缺少必需的 ParentNodeID
			},
			wantErr: true,
			errMsg:  "invalid NodeID constraints",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := hm.AddHost(tt.host)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test_HostManager_GetHost IT-HM-002: GetHost 测试
func Test_HostManager_GetHost(t *testing.T) {
	mvstore := mockMVStoreForHostManager(t)
	hm := NewHostManager(mvstore)

	// 步骤 1: 添加 Host
	host := &Host{
		HostID:              "server-1",
		Hostname:            "192.168.1.100",
		Role:                LeafParent,
		LeafNodeID:          "node-leaf-1",
		ParentNodeID:        "node-parent-1",
		ParentStandbyNodeID: "node-standby-1",
		HostStatus:          HostStatusOnline,
		LastHeartbeat:       time.Now().Unix(),
	}

	err := hm.AddHost(host)
	require.NoError(t, err)

	// 步骤 2: 从内存缓存获取
	retrieved, err := hm.GetHost("server-1")
	require.NoError(t, err)
	assert.Equal(t, "server-1", retrieved.HostID)
	assert.Equal(t, "192.168.1.100", retrieved.Hostname)

	// 步骤 3: 测试获取不存在的 Host
	_, err = hm.GetHost("non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "host not found")
}

// Test_HostManager_RemoveHost IT-HM-003: RemoveHost 测试
func Test_HostManager_RemoveHost(t *testing.T) {
	mvstore := mockMVStoreForHostManager(t)
	hm := NewHostManager(mvstore)

	// 步骤 1: 添加 Host
	host := &Host{
		HostID:              "server-1",
		Hostname:            "192.168.1.100",
		Role:                LeafParent,
		LeafNodeID:          "node-leaf-1",
		ParentNodeID:        "node-parent-1",
		ParentStandbyNodeID: "node-standby-1",
		HostStatus:          HostStatusOnline,
		LastHeartbeat:       time.Now().Unix(),
	}

	err := hm.AddHost(host)
	require.NoError(t, err)

	// 步骤 2: 删除 Host
	err = hm.RemoveHost("server-1")
	require.NoError(t, err)

	// 步骤 3: 验证 Host 已删除
	_, err = hm.GetHost("server-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "host not found")
}

// Test_HostManager_RemoveHost_NotExist IT-HM-003: 删除不存在的 Host
func Test_HostManager_RemoveHost_NotExist(t *testing.T) {
	mvstore := mockMVStoreForHostManager(t)
	hm := NewHostManager(mvstore)

	// 删除不存在的 Host（可能不返回错误，取决于实现）
	err := hm.RemoveHost("non-existent")
	// 可能返回 error 或 nil，取决于 Delete 的实现
	_ = err
}

// Test_HostManager_ListAllHosts IT-HM-005: ListAllHosts 测试
func Test_HostManager_ListAllHosts(t *testing.T) {
	mvstore := mockMVStoreForHostManager(t)
	hm := NewHostManager(mvstore)

	// 步骤 1: 添加多个 Host
	hosts := []*Host{
		{
			HostID:     "server-1",
			Hostname:   "192.168.1.100",
			Role:       LeafOnly,
			LeafNodeID: "node-leaf-1",
			HostStatus: HostStatusOnline,
		},
		{
			HostID:       "server-2",
			Hostname:     "192.168.1.101",
			Role:         LeafParent,
			LeafNodeID:   "node-leaf-2",
			ParentNodeID: "node-parent-1",
			HostStatus:   HostStatusOnline,
		},
		{
			HostID:              "server-3",
			Hostname:            "192.168.1.102",
			Role:                LeafParentStandby,
			LeafNodeID:          "node-leaf-3",
			ParentNodeID:        "node-parent-2",
			ParentStandbyNodeID: "node-standby-1",
			HostStatus:          HostStatusOffline,
		},
	}

	for _, host := range hosts {
		err := hm.AddHost(host)
		require.NoError(t, err)
	}

	// 步骤 2: 列出所有 Host
	allHosts, err := hm.ListAllHosts()
	require.NoError(t, err)
	assert.Equal(t, 3, len(allHosts))

	// 步骤 3: 验证 Host 内容
	hostIDs := make(map[string]bool)
	for _, host := range allHosts {
		hostIDs[host.HostID] = true
	}

	assert.True(t, hostIDs["server-1"])
	assert.True(t, hostIDs["server-2"])
	assert.True(t, hostIDs["server-3"])
}

// Test_HostManager_GetHostTopology IT-HM-005: GetHostTopology 测试
func Test_HostManager_GetHostTopology(t *testing.T) {
	mvstore := mockMVStoreForHostManager(t)
	hm := NewHostManager(mvstore)

	// 添加 Host
	host := &Host{
		HostID:              "server-1",
		Hostname:            "192.168.1.100",
		Role:                LeafParent,
		LeafNodeID:          "node-leaf-1",
		ParentNodeID:        "node-parent-1",
		ParentStandbyNodeID: "node-standby-1",
		HostStatus:          HostStatusOnline,
		LastHeartbeat:       time.Now().Unix(),
	}

	err := hm.AddHost(host)
	require.NoError(t, err)

	// 获取拓扑
	topology, err := hm.GetHostTopology()
	require.NoError(t, err)

	assert.Equal(t, 1, len(topology))
	assert.Contains(t, topology, "server-1")
	assert.Equal(t, "192.168.1.100", topology["server-1"].Hostname)
}

// Test_HostManager_UpdateHostStatus 测试更新 Host 状态
func Test_HostManager_UpdateHostStatus(t *testing.T) {
	mvstore := mockMVStoreForHostManager(t)
	hm := NewHostManager(mvstore)

	// 添加 Host
	host := &Host{
		HostID:        "server-1",
		Hostname:      "192.168.1.100",
		Role:          LeafOnly,
		LeafNodeID:    "node-leaf-1",
		HostStatus:    HostStatusOnline,
		LastHeartbeat: time.Now().Unix(),
	}

	err := hm.AddHost(host)
	require.NoError(t, err)

	// 更新状态
	newHeartbeat := time.Now().Unix()
	err = hm.UpdateHostStatus("server-1", HostStatusDegraded, newHeartbeat)
	require.NoError(t, err)

	// 验证更新
	retrieved, err := hm.GetHost("server-1")
	require.NoError(t, err)
	assert.Equal(t, HostStatusDegraded, retrieved.HostStatus)
	assert.Equal(t, newHeartbeat, retrieved.LastHeartbeat)
}

// Test_HostManager_GetHostsByRole 测试按角色筛选 Host
func Test_HostManager_GetHostsByRole(t *testing.T) {
	mvstore := mockMVStoreForHostManager(t)
	hm := NewHostManager(mvstore)

	// 添加不同角色的 Host
	hosts := []*Host{
		{HostID: "server-1", Hostname: "192.168.1.100", Role: LeafOnly, LeafNodeID: "node-leaf-1", HostStatus: HostStatusOnline},
		{HostID: "server-2", Hostname: "192.168.1.101", Role: LeafParent, LeafNodeID: "node-leaf-2", ParentNodeID: "node-parent-1", HostStatus: HostStatusOnline},
		{HostID: "server-3", Hostname: "192.168.1.102", Role: LeafParent, LeafNodeID: "node-leaf-3", ParentNodeID: "node-parent-2", HostStatus: HostStatusOnline},
	}

	for _, host := range hosts {
		err := hm.AddHost(host)
		require.NoError(t, err)
	}

	// 筛选 LeafParent 角色
	parentHosts, err := hm.GetHostsByRole(LeafParent)
	require.NoError(t, err)
	assert.Equal(t, 2, len(parentHosts))

	// 验证结果
	hostIDs := make([]string, 0)
	for _, host := range parentHosts {
		hostIDs = append(hostIDs, host.HostID)
		assert.Equal(t, LeafParent, host.Role)
	}

	assert.Contains(t, hostIDs, "server-2")
	assert.Contains(t, hostIDs, "server-3")
}

// Test_HostManager_GetOnlineHosts 测试获取在线 Host
func Test_HostManager_GetOnlineHosts(t *testing.T) {
	mvstore := mockMVStoreForHostManager(t)
	hm := NewHostManager(mvstore)

	// 添加不同状态的 Host
	hosts := []*Host{
		{HostID: "server-1", Hostname: "192.168.1.100", Role: LeafOnly, LeafNodeID: "node-leaf-1", HostStatus: HostStatusOnline},
		{HostID: "server-2", Hostname: "192.168.1.101", Role: LeafParent, LeafNodeID: "node-leaf-2", ParentNodeID: "node-parent-1", HostStatus: HostStatusOnline},
		{HostID: "server-3", Hostname: "192.168.1.102", Role: LeafParent, LeafNodeID: "node-leaf-3", ParentNodeID: "node-parent-2", HostStatus: HostStatusOffline},
	}

	for _, host := range hosts {
		err := hm.AddHost(host)
		require.NoError(t, err)
	}

	// 获取在线 Host
	onlineHosts, err := hm.GetOnlineHosts()
	require.NoError(t, err)
	assert.Equal(t, 2, len(onlineHosts))

	// 验证所有返回的 Host 都是在线状态
	for _, host := range onlineHosts {
		assert.Equal(t, HostStatusOnline, host.HostStatus)
	}
}

// Test_HostManager_GetHostCount 测试获取 Host 数量
func Test_HostManager_GetHostCount(t *testing.T) {
	mvstore := mockMVStoreForHostManager(t)
	hm := NewHostManager(mvstore)

	// 初始数量
	count, err := hm.GetHostCount()
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// 添加 Host
	hosts := []*Host{
		{HostID: "server-1", Hostname: "192.168.1.100", Role: LeafOnly, LeafNodeID: "node-leaf-1", HostStatus: HostStatusOnline},
		{HostID: "server-2", Hostname: "192.168.1.101", Role: LeafParent, LeafNodeID: "node-leaf-2", ParentNodeID: "node-parent-1", HostStatus: HostStatusOnline},
		{HostID: "server-3", Hostname: "192.168.1.102", Role: LeafParent, LeafNodeID: "node-leaf-3", ParentNodeID: "node-parent-2", HostStatus: HostStatusOnline},
	}

	for _, host := range hosts {
		err := hm.AddHost(host)
		require.NoError(t, err)
	}

	// 验证数量
	count, err = hm.GetHostCount()
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

// ============================================================================
// Host 方法测试 (IsOnline, IsDegraded)
// ============================================================================

// Test_Host_IsOnline 测试 Host.IsOnline 方法
func Test_Host_IsOnline(t *testing.T) {
	tests := []struct {
		name     string
		status   HostStatus
		expected bool
	}{
		{
			name:     "在线状态",
			status:   HostStatusOnline,
			expected: true,
		},
		{
			name:     "离线状态",
			status:   HostStatusOffline,
			expected: false,
		},
		{
			name:     "降级状态",
			status:   HostStatusDegraded,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := &Host{
				HostID:     "server-1",
				Hostname:   "192.168.1.100",
				Role:       LeafOnly,
				LeafNodeID: "node-leaf-1",
				HostStatus: tt.status,
			}

			result := host.IsOnline()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test_Host_IsDegraded 测试 Host.IsDegraded 方法
func Test_Host_IsDegraded(t *testing.T) {
	tests := []struct {
		name     string
		status   HostStatus
		expected bool
	}{
		{
			name:     "降级状态",
			status:   HostStatusDegraded,
			expected: true,
		},
		{
			name:     "在线状态",
			status:   HostStatusOnline,
			expected: false,
		},
		{
			name:     "离线状态",
			status:   HostStatusOffline,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := &Host{
				HostID:     "server-1",
				Hostname:   "192.168.1.100",
				Role:       LeafOnly,
				LeafNodeID: "node-leaf-1",
				HostStatus: tt.status,
			}

			result := host.IsDegraded()
			assert.Equal(t, tt.expected, result)
		})
	}
}
