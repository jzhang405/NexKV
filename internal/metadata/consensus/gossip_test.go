// Package consensus 一致性协议测试
package consensus

import (
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/store"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// 测试辅助工具
// ========================================

// mockMVStore 模拟 MVStore 实现
type mockMVStore struct {
	mu      sync.RWMutex
	data    map[string][]byte
	version uint64
}

func newMockMVStore() *mockMVStore {
	return &mockMVStore{
		data:    make(map[string][]byte),
		version: 1,
	}
}

func (m *mockMVStore) Put(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	m.version++
	return nil
}

func (m *mockMVStore) Get(key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if val, exists := m.data[key]; exists {
		return val, nil
	}
	return nil, types.NewNotFoundError(key)
}

func (m *mockMVStore) GetVersion(key string, hlcTimestamp *clock.HLC) ([]byte, error) {
	return m.Get(key)
}

func (m *mockMVStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	m.version++
	return nil
}

func (m *mockMVStore) Exists(key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.data[key]
	return exists, nil
}

func (m *mockMVStore) List(offset, limit int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func (m *mockMVStore) ListPrefix(prefix string, offset, limit int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var keys []string
	for k := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *mockMVStore) GetVersionCount(key string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.data[key]; exists {
		return 1, nil
	}
	return 0, nil
}

func (m *mockMVStore) GetAllVersions(key string) ([]*store.VersionInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if val, exists := m.data[key]; exists {
		return []*store.VersionInfo{
			{
				Timestamp: clock.NewHLC().Now(),
				Version:   m.version,
				Deleted:   false,
				Size:      len(val),
			},
		}, nil
	}
	return nil, nil
}

func (m *mockMVStore) Flush() error {
	return nil
}

func (m *mockMVStore) CreateSnapshot() ([]byte, error) {
	return nil, nil
}

func (m *mockMVStore) RestoreFromSnapshot(snapshot []byte) error {
	return nil
}

func (m *mockMVStore) Close() error {
	return nil
}

// ========================================
// Gossip 协议测试
// ========================================

// TestNewGossipService 测试创建 Gossip 服务
func TestNewGossipService(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	peers := []string{"node2", "node3"}

	config := DefaultGossipConfig()
	service, err := NewGossipService(metaStore, trans, hlc, peers, config)

	assert.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, config, service.config)
	assert.Equal(t, metaStore, service.metaStore)
	assert.Equal(t, trans, service.transport)
	assert.Equal(t, hlc, service.hlc)
}

// TestGossipService_StartStop 测试启动和停止 Gossip 服务
func TestGossipService_StartStop(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	peers := []string{"node2", "node3"}

	service, err := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	require.NoError(t, err)

	// 测试启动
	_ = service.Start()
	assert.True(t, service.started.Load())

	// 测试重复启动
	err = service.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")

	// 测试停止
	err = service.Stop()
	assert.NoError(t, err)
	assert.True(t, service.stopped.Load())

	// 测试重复停止
	err = service.Stop()
	assert.NoError(t, err) // Stop 应该是幂等的
}

// TestGossipService_Put 测试写入元数据
func TestGossipService_Put(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	peers := []string{"node2", "node3"}

	service, err := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	require.NoError(t, err)

	_ = service.Start()
	_ = service.Stop()

	// 写入元数据
	key := "test_key"
	value := []byte("test_value")
	err = service.Put(key, value)
	assert.NoError(t, err)

	// 验证数据已写入
	stored, err := service.Get(key)
	assert.NoError(t, err)
	assert.Equal(t, value, stored)

	// 验证版本号递增
	version := service.GetVersion()
	assert.Greater(t, version, uint64(1))
}

// TestGossipService_Delete 测试删除元数据
func TestGossipService_Delete(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	peers := []string{"node2", "node3"}

	service, err := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	require.NoError(t, err)

	_ = service.Start()
	_ = service.Stop()

	// 先写入数据
	key := "test_key"
	value := []byte("test_value")
	err = service.Put(key, value)
	require.NoError(t, err)

	// 删除数据
	err = service.Delete(key)
	assert.NoError(t, err)

	// 验证数据已删除
	_, err = service.Get(key)
	assert.Error(t, err)
}

// TestGossipService_AddRemovePeer 测试添加和移除节点
func TestGossipService_AddRemovePeer(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	peers := []string{"node2"}

	service, err := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	require.NoError(t, err)

	_ = service.Start()
	_ = service.Stop()

	// 添加节点
	service.AddPeer("node3")

	// 移除节点
	service.RemovePeer("node2")
}

// TestGossipService_selectRandomPeers 测试随机节点选择
func TestGossipService_selectRandomPeers(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	peers := []string{"node2", "node3", "node4", "node5", "node6"}

	service, err := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	require.NoError(t, err)

	_ = service.Start()
	_ = service.Stop()

	// 选择 2 个节点
	selected := service.selectRandomPeers(2)
	assert.Len(t, selected, 2)

	// 验证选中的节点都在 peers 列表中
	for _, peer := range selected {
		assert.Contains(t, peers, peer)
	}

	// 选择的节点应该不重复
	assert.NotEqual(t, selected[0], selected[1])

	// 测试选择数量大于节点数量
	selected = service.selectRandomPeers(10)
	assert.Len(t, selected, len(peers))
}

// TestGossipService_addChangeLog 测试变更日志缓存
func TestGossipService_addChangeLog(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	peers := []string{"node2"}

	config := DefaultGossipConfig()
	config.MaxChangeLogs = 5 // 设置较小的缓存大小

	service, err := NewGossipService(metaStore, trans, hlc, peers, config)
	require.NoError(t, err)

	_ = service.Start()
	require.NoError(t, err)
	_ = service.Stop()

	// 写入超过缓存大小的数据
	for i := 0; i < 10; i++ {
		key := "test_key"
		value := []byte("test_value")
		err = service.Put(key, value)
		assert.NoError(t, err)
	}

	// 验证变更日志数量不超过缓存大小
	service.changeLogsMu.RLock()
	logCount := len(service.changeLogs)
	service.changeLogsMu.RUnlock()

	assert.LessOrEqual(t, logCount, config.MaxChangeLogs)
}

// TestGossipService_buildMetadataDigest 测试构建元数据摘要
func TestGossipService_buildMetadataDigest(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	peers := []string{"node2"}

	service, err := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	require.NoError(t, err)

	_ = service.Start()
	_ = service.Stop()

	// 写入一些元数据
	_ = service.Put("key1", []byte("value1"))
	_ = service.Put("key2", []byte("value2"))

	// 构建摘要
	digest := service.buildMetadataDigest()

	// 验证摘要包含所有 key
	assert.Contains(t, digest, "key1")
	assert.Contains(t, digest, "key2")

	// 验证版本号正确
	assert.Greater(t, digest["key1"], uint64(0))
	assert.Greater(t, digest["key2"], uint64(0))
}

// TestGossipService_applyMetadata 测试应用元数据
func TestGossipService_applyMetadata(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	peers := []string{"node2"}

	service, err := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	require.NoError(t, err)

	_ = service.Start()
	_ = service.Stop()

	// 应用元数据
	metadata := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	}

	service.applyMetadata(metadata)

	// 验证数据已应用
	val1, err := service.Get("key1")
	assert.NoError(t, err)
	assert.Equal(t, []byte("value1"), val1)

	val2, err := service.Get("key2")
	assert.NoError(t, err)
	assert.Equal(t, []byte("value2"), val2)
}

// TestGossipService_GetStats 测试获取统计信息
func TestGossipService_GetStats(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	peers := []string{"node2"}

	service, err := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	require.NoError(t, err)

	_ = service.Start()
	_ = service.Stop()

	// 执行一些操作
	_ = service.Put("key1", []byte("value1"))
	_ = service.Put("key2", []byte("value2"))

	// 获取统计信息
	stats := service.GetStats()

	assert.NotNil(t, stats)
	assert.NotNil(t, stats.SyncCount.Load())
	assert.NotNil(t, stats.SyncSuccess.Load())
	assert.NotNil(t, stats.SyncFailed.Load())
	assert.NotNil(t, stats.ChangeLogsSent.Load())
	assert.NotNil(t, stats.ChangeLogsReceived.Load())
	assert.NotNil(t, stats.LastSyncTime.Load())
}

// TestGossipService_TriggerSync 测试手动触发同步
func TestGossipService_TriggerSync(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	peers := []string{"node2"}

	service, err := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	require.NoError(t, err)

	_ = service.Start()
	require.NoError(t, err)

	// 注册远程节点
	trans.RegisterRemoteNode("node2")

	// 手动触发同步
	service.TriggerSync()

	// 等待同步完成
	time.Sleep(100 * time.Millisecond)

	_ = service.Stop()
}

// TestDefaultGossipConfig 测试默认 Gossip 配置
func TestDefaultGossipConfig(t *testing.T) {
	config := DefaultGossipConfig()

	assert.Equal(t, 10*time.Second, config.Interval)
	assert.Equal(t, 2, config.Fanout)
	assert.Equal(t, 5*time.Second, config.Timeout)
	assert.Equal(t, 1000, config.MaxChangeLogs)
}

// ========================================
// 性能基准测试
// ========================================

// BenchmarkGossipService_Put 性能基准测试: 写入元数据
func BenchmarkGossipService_Put(b *testing.B) {
	metaStore := newMockMVStore()
	trans, _ := transport.NewMemoryTransport("node1")
	hlc := clock.NewHLC()
	peers := []string{"node2", "node3"}

	service, _ := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	_ = service.Start()
	_ = service.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = service.Put("key", []byte("value"))
	}
}

// BenchmarkGossipService_Get 性能基准测试: 读取元数据
func BenchmarkGossipService_Get(b *testing.B) {
	metaStore := newMockMVStore()
	trans, _ := transport.NewMemoryTransport("node1")
	hlc := clock.NewHLC()
	peers := []string{"node2", "node3"}

	service, _ := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	_ = service.Start()
	_ = service.Put("key", []byte("value"))
	_ = service.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.Get("key")
	}
}

// BenchmarkGossipService_selectRandomPeers 性能基准测试: 随机选择节点
func BenchmarkGossipService_selectRandomPeers(b *testing.B) {
	metaStore := newMockMVStore()
	trans, _ := transport.NewMemoryTransport("node1")
	hlc := clock.NewHLC()

	// 创建 100 个节点
	peers := make([]string, 100)
	for i := 0; i < 100; i++ {
		peers[i] = "node" + string(rune(i))
	}

	service, _ := NewGossipService(metaStore, trans, hlc, peers, DefaultGossipConfig())
	_ = service.Start()
	_ = service.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = service.selectRandomPeers(2)
	}
}
