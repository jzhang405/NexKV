// Package cluster 的 FailureDetector 单元测试
//
// 测试覆盖：
// - IT-FD-001: DetectHeartbeatTimeout 测试
// - IT-FD-002: ProbeHost 测试
// - IT-FD-003: IsHostFailed 测试
// - IT-FD-004: CheckAllHosts 测试
// - IT-FD-005: 防脑裂延迟机制测试
package cluster

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/metadata/store"
)

// mockMVStoreForFailureDetector 创建用于测试的 MVStore
func mockMVStoreForFailureDetector(t *testing.T) store.MVStore {
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
// FailureDetector 测试 (IT-FD-001 ~ IT-FD-005)
// ============================================================================

// Test_FailureDetector_DetectHeartbeatTimeout IT-FD-001: 心跳超时检测
func Test_FailureDetector_DetectHeartbeatTimeout(t *testing.T) {
	mvstore := mockMVStoreForFailureDetector(t)
	hm := NewHostManager(mvstore)
	pa := NewPortAllocator(mvstore)
	fd := NewFailureDetector(hm, pa, DefaultFailureDetectorConfig)

	// 添加正常 Host（最近心跳）
	_, _, err := pa.AllocTCPPort("server-1")
	require.NoError(t, err, "Failed to allocate ports for server-1")
	host1 := &Host{
		HostID:        "server-1",
		Hostname:      "192.168.1.100",
		Role:          LeafOnly,
		LeafNodeID:    "node-leaf-1",
		HostStatus:    HostStatusOnline,
		LastHeartbeat: time.Now().Unix(),
	}
	err = hm.AddHost(host1)
	require.NoError(t, err)

	// 添加超时 Host（旧心跳）
	_, _, err = pa.AllocTCPPort("server-2")
	require.NoError(t, err, "Failed to allocate ports for server-2")
	host2 := &Host{
		HostID:        "server-2",
		Hostname:      "192.168.1.101",
		Role:          LeafOnly,
		LeafNodeID:    "node-leaf-2",
		HostStatus:    HostStatusOnline,
		LastHeartbeat: time.Now().Add(-60 * time.Second).Unix(), // 60 秒前
	}
	err = hm.AddHost(host2)
	require.NoError(t, err)

	// 检测心跳超时
	timeoutHosts, err := fd.DetectHeartbeatTimeout()
	require.NoError(t, err)

	// 验证结果
	assert.Equal(t, 1, len(timeoutHosts), "Should detect 1 timeout host")
	assert.Contains(t, timeoutHosts, "server-2", "server-2 should be timeout")
}

// Test_FailureDetector_ProbeHost IT-FD-002: 探测 Host
func Test_FailureDetector_ProbeHost(t *testing.T) {
	mvstore := mockMVStoreForFailureDetector(t)
	hm := NewHostManager(mvstore)
	pa := NewPortAllocator(mvstore)
	fd := NewFailureDetector(hm, pa, DefaultFailureDetectorConfig)

	// 添加 localhost Host（用于测试）
	_, _, err := pa.AllocTCPPort("localhost-test")
	require.NoError(t, err, "Failed to allocate ports for localhost-test")
	host := &Host{
		HostID:        "localhost-test",
		Hostname:      "127.0.0.1",
		Role:          LeafOnly,
		LeafNodeID:    "node-leaf-1",
		HostStatus:    HostStatusOnline,
		LastHeartbeat: time.Now().Unix(),
	}
	err = hm.AddHost(host)
	require.NoError(t, err)

	// 探测 Host（预期失败，因为 127.0.0.1:9000 没有服务监听）
	result, err := fd.ProbeHost("localhost-test")
	require.NoError(t, err)

	// 验证结果
	assert.False(t, result.TCPReachable, "TCP should not be reachable")
	// 注意：UDP 是无连接协议，Write 会成功（数据包被发送），但不意味着有服务接收
	// 所以 UDPReachable 可能为 true（这是预期的）
	assert.NotNil(t, result.Error, "Should have probe error")
	assert.Greater(t, result.ProbedAt, int64(0), "Should have probe timestamp")
}

// Test_FailureDetector_IsHostFailed IT-FD-003: 判断 Host 是否故障
func Test_FailureDetector_IsHostFailed(t *testing.T) {
	mvstore := mockMVStoreForFailureDetector(t)
	hm := NewHostManager(mvstore)
	pa := NewPortAllocator(mvstore)
	config := DefaultFailureDetectorConfig
	config.MaxConsecutiveFails = 2 // 降低阈值以加速测试
	fd := NewFailureDetector(hm, pa, config)

	// 添加正常 Host
	_, _, err := pa.AllocTCPPort("server-1")
	require.NoError(t, err, "Failed to allocate ports for server-1")
	host := &Host{
		HostID:        "server-1",
		Hostname:      "192.168.1.100",
		Role:          LeafOnly,
		LeafNodeID:    "node-leaf-1",
		HostStatus:    HostStatusOnline,
		LastHeartbeat: time.Now().Unix(),
	}
	err = hm.AddHost(host)
	require.NoError(t, err)

	// 步骤 1: 正常心跳，不应该判定为故障
	failed, err := fd.IsHostFailed("server-1")
	require.NoError(t, err)
	assert.False(t, failed, "Should not be failed with normal heartbeat")

	// 步骤 2: 模拟心跳超时
	host.LastHeartbeat = time.Now().Add(-60 * time.Second).Unix()
	err = hm.AddHost(host)
	require.NoError(t, err)

	// 第一次检测（失败计数 1）
	failed, err = fd.IsHostFailed("server-1")
	require.NoError(t, err)
	assert.False(t, failed, "Should not be failed on first detection")

	// 第二次检测（失败计数 2，达到阈值）
	failed, err = fd.IsHostFailed("server-1")
	require.NoError(t, err)
	assert.True(t, failed, "Should be failed after threshold")

	// 步骤 3: 重置失败计数
	fd.ResetFailureCount("server-1")

	// 更新心跳为正常
	host.LastHeartbeat = time.Now().Unix()
	err = hm.AddHost(host)
	require.NoError(t, err)

	// 再次检测，应该未故障
	failed, err = fd.IsHostFailed("server-1")
	require.NoError(t, err)
	assert.False(t, failed, "Should not be failed after reset and heartbeat update")
}

// Test_FailureDetector_CheckAllHosts IT-FD-004: 检查所有 Host
func Test_FailureDetector_CheckAllHosts(t *testing.T) {
	mvstore := mockMVStoreForFailureDetector(t)
	hm := NewHostManager(mvstore)
	pa := NewPortAllocator(mvstore)
	config := DefaultFailureDetectorConfig
	config.MaxConsecutiveFails = 1 // 降低阈值以简化测试
	config.HeartbeatTimeout = 30 * time.Second
	fd := NewFailureDetector(hm, pa, config)

	// 使用固定时间戳，避免时间差异
	staleHeartbeat := time.Now().Add(-60 * time.Second).Unix()
	currentHeartbeat := time.Now().Unix()

	// 添加多个 Host
	hosts := []*Host{
		{
			HostID:        "server-1",
			Hostname:      "192.168.1.100",
			Role:          LeafOnly,
			LeafNodeID:    "node-leaf-1",
			HostStatus:    HostStatusOnline,
			LastHeartbeat: currentHeartbeat, // 正常心跳
		},
		{
			HostID:        "server-2",
			Hostname:      "192.168.1.101",
			Role:          LeafOnly,
			LeafNodeID:    "node-leaf-2",
			HostStatus:    HostStatusOnline,
			LastHeartbeat: staleHeartbeat, // 超时
		},
		{
			HostID:        "server-3",
			Hostname:      "192.168.1.102",
			Role:          LeafOnly,
			LeafNodeID:    "node-leaf-3",
			HostStatus:    HostStatusOnline,
			LastHeartbeat: staleHeartbeat, // 超时
		},
	}

	for _, host := range hosts {
		_, _, err := pa.AllocTCPPort(host.HostID)
		require.NoError(t, err, "Failed to allocate ports for %s", host.HostID)
		err = hm.AddHost(host)
		require.NoError(t, err)
	}

	// 检查所有 Host
	// 注意：MaxConsecutiveFails = 1，所以一次检测就会判定为故障
	failedHosts, err := fd.CheckAllHosts()
	require.NoError(t, err)
	t.Logf("Failed hosts: %v (expected at least 2)", failedHosts)

	// 验证检测到了故障 Host（server-2 和 server-3）
	assert.GreaterOrEqual(t, len(failedHosts), 2, "Should detect at least 2 failed hosts")
}

// Test_FailureDetector_GetLastProbe IT-FD-005: 获取最后探测结果
func Test_FailureDetector_GetLastProbe(t *testing.T) {
	mvstore := mockMVStoreForFailureDetector(t)
	hm := NewHostManager(mvstore)
	pa := NewPortAllocator(mvstore)
	fd := NewFailureDetector(hm, pa, DefaultFailureDetectorConfig)

	// 添加 Host
	_, _, err := pa.AllocTCPPort("server-1")
	require.NoError(t, err, "Failed to allocate ports for server-1")
	host := &Host{
		HostID:        "server-1",
		Hostname:      "127.0.0.1",
		Role:          LeafOnly,
		LeafNodeID:    "node-leaf-1",
		HostStatus:    HostStatusOnline,
		LastHeartbeat: time.Now().Unix(),
	}
	err = hm.AddHost(host)
	require.NoError(t, err)

	// 步骤 1: 探测前获取，应该返回错误
	_, err = fd.GetLastProbe("server-1")
	assert.Error(t, err, "Should return error when no probe result exists")

	// 步骤 2: 执行探测
	_, err = fd.ProbeHost("server-1")
	require.NoError(t, err)

	// 步骤 3: 获取探测结果
	result, err := fd.GetLastProbe("server-1")
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Greater(t, result.ProbedAt, int64(0))
}

// Test_FailureDetector_GetFailureCount 测试获取失败计数
func Test_FailureDetector_GetFailureCount(t *testing.T) {
	mvstore := mockMVStoreForFailureDetector(t)
	hm := NewHostManager(mvstore)
	pa := NewPortAllocator(mvstore)
	config := DefaultFailureDetectorConfig
	config.MaxConsecutiveFails = 2
	fd := NewFailureDetector(hm, pa, config)

	// 添加超时 Host
	_, _, err := pa.AllocTCPPort("server-1")
	require.NoError(t, err, "Failed to allocate ports for server-1")
	host := &Host{
		HostID:        "server-1",
		Hostname:      "192.168.1.100",
		Role:          LeafOnly,
		LeafNodeID:    "node-leaf-1",
		HostStatus:    HostStatusOnline,
		LastHeartbeat: time.Now().Add(-60 * time.Second).Unix(),
	}
	err = hm.AddHost(host)
	require.NoError(t, err)

	// 初始计数
	count, err := fd.GetFailureCount("server-1")
	require.NoError(t, err)
	assert.Equal(t, 0, count, "Initial failure count should be 0")

	// 第一次检测
	_, _ = fd.IsHostFailed("server-1")
	count, err = fd.GetFailureCount("server-1")
	require.NoError(t, err)
	assert.Equal(t, 1, count, "Failure count should be 1 after first detection")

	// 重置计数
	fd.ResetFailureCount("server-1")
	count, err = fd.GetFailureCount("server-1")
	require.NoError(t, err)
	assert.Equal(t, 0, count, "Failure count should be 0 after reset")
}

// Test_FailureDetector_Config 测试自定义配置
func Test_FailureDetector_Config(t *testing.T) {
	mvstore := mockMVStoreForFailureDetector(t)
	hm := NewHostManager(mvstore)
	pa := NewPortAllocator(mvstore)

	customConfig := FailureDetectorConfig{
		HeartbeatTimeout:    60 * time.Second,
		ProbeTimeout:        5 * time.Second,
		MaxConsecutiveFails: 5,
		DelayDuration:       3 * time.Second,
	}

	fd := NewFailureDetector(hm, pa, customConfig)

	// 验证配置
	assert.Equal(t, 60*time.Second, fd.config.HeartbeatTimeout)
	assert.Equal(t, 5*time.Second, fd.config.ProbeTimeout)
	assert.Equal(t, 5, fd.config.MaxConsecutiveFails)
	assert.Equal(t, 3*time.Second, fd.config.DelayDuration)
}

// Test_FailureDetector_IPv6Support 测试 IPv6 地址格式支持
func Test_FailureDetector_IPv6Support(t *testing.T) {
	// 测试 IPv4 地址格式
	ipv4Addr := formatAddress("192.168.1.100", 9000)
	assert.Equal(t, "192.168.1.100:9000", ipv4Addr, "IPv4 address should not have brackets")

	// 测试 IPv6 地址格式
	ipv6Addr := formatAddress("2001:db8::1", 9000)
	assert.Equal(t, "[2001:db8::1]:9000", ipv6Addr, "IPv6 address should have brackets")

	// 测试 IPv6 loopback 地址格式
	ipv6Loopback := formatAddress("::1", 9000)
	assert.Equal(t, "[::1]:9000", ipv6Loopback, "IPv6 loopback should have brackets")

	// 测试主机名格式（无冒号，视为 IPv4）
	hostnameAddr := formatAddress("localhost", 9000)
	assert.Equal(t, "localhost:9000", hostnameAddr, "Hostname should not have brackets")
}
