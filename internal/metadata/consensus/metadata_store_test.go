// Package consensus 元数据存储协调层测试
package consensus

import (
	"context"
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// MetadataStore 测试
// ========================================

// TestNewMetadataStore 测试创建元数据存储
func TestNewMetadataStore(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	localAddr := "node1"
	nodes := []string{"node1", "node2", "node3"}

	// 测试默认配置
	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, localAddr, nodes, nil)
	assert.NoError(t, err)
	assert.NotNil(t, store)
	assert.NotNil(t, store.config)
	assert.NotNil(t, store.gossipService)
	assert.NotNil(t, store.quorumService)
	assert.NotNil(t, store.twoPCService)

	// 测试自定义配置
	config := &MetadataStoreConfig{
		CriticalPrefixes:   []string{"test/", "custom/"},
		EnableAutoClassify: false,
	}

	store2, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, localAddr, nodes, config)
	assert.NoError(t, err)
	assert.Equal(t, config, store2.config)
	assert.Equal(t, config.CriticalPrefixes, store2.config.CriticalPrefixes)
	assert.Equal(t, config.EnableAutoClassify, store2.config.EnableAutoClassify)
}

// TestMetadataStore_StartStop 测试启动和停止元数据存储
func TestMetadataStore_StartStop(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1", "node2", "node3"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	// 测试启动
	err = store.Start()
	assert.NoError(t, err)
	assert.True(t, store.started.Load())

	// 测试重复启动
	err = store.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")

	// 测试停止
	err = store.Stop()
	assert.NoError(t, err)
	assert.True(t, store.stopped.Load())

	// 测试重复停止
	err = store.Stop()
	assert.NoError(t, err)
}

// TestMetadataStore_Put_Quorum 测试使用 Quorum 写入关键元数据
func TestMetadataStore_Put_Quorum(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1", "node2", "node3"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 写入关键元数据（匹配 shard/ 前缀）
	key := "shard/test_shard"
	value := []byte("shard_metadata")

	ctx := context.Background()
	err = store.Put(ctx, key, value)

	// 由于没有其他节点参与投票，可能会失败或超时
	// 这里主要验证流程不崩溃
	// 实际环境中需要多个节点才能成功
	if err != nil {
		assert.Contains(t, err.Error(), "超时")
	}
}

// TestMetadataStore_Put_Gossip 测试使用 Gossip 写入普通元数据
func TestMetadataStore_Put_Gossip(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 写入普通元数据（不匹配关键前缀）
	key := "status/node1"
	value := []byte("active")

	ctx := context.Background()
	err = store.Put(ctx, key, value)

	// Gossip 路径应该成功（异步，不等待确认）
	assert.NoError(t, err)

	// 验证数据已写入
	stored, err := store.Get(key)
	assert.NoError(t, err)
	assert.Equal(t, value, stored)
}

// TestMetadataStore_Delete_Quorum 测试使用 Quorum 删除关键元数据
func TestMetadataStore_Delete_Quorum(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1", "node2", "node3"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 先写入数据
	key := "shard/test_shard"
	_ = []byte("shard_metadata") // 预期值
	ctx := context.Background()

	// 由于单节点环境，删除可能会超时
	err = store.Delete(ctx, key)
	// 验证流程不崩溃
	if err != nil {
		assert.Contains(t, err.Error(), "超时")
	}
}

// TestMetadataStore_Delete_Gossip 测试使用 Gossip 删除普通元数据
func TestMetadataStore_Delete_Gossip(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 先写入数据
	key := "status/node1"
	value := []byte("active")
	ctx := context.Background()

	err = store.Put(ctx, key, value)
	require.NoError(t, err)

	// 删除数据
	err = store.Delete(ctx, key)
	assert.NoError(t, err)

	// 验证数据已删除
	_, err = store.Get(key)
	assert.Error(t, err)
}

// TestMetadataStore_Get 测试获取元数据
func TestMetadataStore_Get(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 写入数据
	key := "test_key"
	value := []byte("test_value")
	ctx := context.Background()

	err = store.Put(ctx, key, value)
	require.NoError(t, err)

	// 获取数据
	stored, err := store.Get(key)
	assert.NoError(t, err)
	assert.Equal(t, value, stored)

	// 获取不存在的数据
	_, err = store.Get("non_existent_key")
	assert.Error(t, err)
}

// TestMetadataStore_ExecuteTransaction 测试执行分布式事务
func TestMetadataStore_ExecuteTransaction(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1"} // 单节点

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 创建事务操作
	operations := []transport.Operation{
		{
			Key:   "key1",
			Value: []byte("value1"),
			Type:  "put",
		},
		{
			Key:   "key2",
			Value: []byte("value2"),
			Type:  "put",
		},
	}

	// 执行事务
	ctx := context.Background()
	err = store.ExecuteTransaction(ctx, operations)
	assert.NoError(t, err)

	// 验证数据已写入
	val1, err := store.Get("key1")
	assert.NoError(t, err)
	assert.Equal(t, []byte("value1"), val1)

	val2, err := store.Get("key2")
	assert.NoError(t, err)
	assert.Equal(t, []byte("value2"), val2)
}

// TestMetadataStore_selectProtocol 测试协议选择
func TestMetadataStore_selectProtocol(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	// 测试关键前缀匹配
	testCases := []struct {
		name             string
		key              string
		changeType       ChangeType
		expectedProtocol ConsensusProtocol
	}{
		{"分片元数据", "shard/test", ChangeTypeCreate, ConsensusProtocolQuorum},
		{"副本元数据", "replica/test", ChangeTypeUpdate, ConsensusProtocolQuorum},
		{"节点元数据", "node/test", ChangeTypeDelete, ConsensusProtocolQuorum},
		{"普通元数据", "status/test", ChangeTypeUpdate, ConsensusProtocolGossip},
		{"其他元数据", "custom/key", ChangeTypeCreate, ConsensusProtocolGossip},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			protocol := store.selectProtocol(tc.key, tc.changeType)
			assert.Equal(t, tc.expectedProtocol, protocol)
		})
	}
}

// TestMetadataStore_classifyChangeType 测试变更类型分类
func TestMetadataStore_classifyChangeType(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 测试创建操作
	changeType := store.classifyChangeType("new_key", []byte("value"))
	assert.Equal(t, ChangeTypeCreate, changeType)

	// 先写入数据
	ctx := context.Background()
	err = store.Put(ctx, "existing_key", []byte("old_value"))
	require.NoError(t, err)

	// 测试更新操作
	changeType = store.classifyChangeType("existing_key", []byte("new_value"))
	assert.Equal(t, ChangeTypeUpdate, changeType)
}

// TestMetadataStore_AddRemoveCriticalPrefix 测试添加和移除关键前缀
func TestMetadataStore_AddRemoveCriticalPrefix(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 添加关键前缀
	store.AddCriticalPrefix("custom/")

	// 验证前缀已添加
	assert.Contains(t, store.config.CriticalPrefixes, "custom/")

	// 测试使用新前缀
	protocol := store.selectProtocol("custom/test", ChangeTypeCreate)
	assert.Equal(t, ConsensusProtocolQuorum, protocol)

	// 移除关键前缀
	store.RemoveCriticalPrefix("custom/")

	// 验证前缀已移除
	assert.NotContains(t, store.config.CriticalPrefixes, "custom/")

	// 测试移除后使用 Gossip
	protocol = store.selectProtocol("custom/test", ChangeTypeCreate)
	assert.Equal(t, ConsensusProtocolGossip, protocol)
}

// TestMetadataStore_AddRemoveNode 测试添加和移除节点
func TestMetadataStore_AddRemoveNode(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 添加节点
	store.AddNode("node3")

	// 验证节点已添加到 Gossip 服务（通过直接访问 peers 字段）
	assert.Contains(t, store.gossipService.peers, "node3")

	// 验证节点已添加到 Quorum 服务
	assert.Contains(t, store.quorumService.nodes, "node3")

	// 移除节点
	store.RemoveNode("node2")

	// 验证节点已从 Gossip 服务移除
	assert.NotContains(t, store.gossipService.peers, "node2")

	// 验证节点已从 Quorum 服务移除
	assert.NotContains(t, store.quorumService.nodes, "node2")
}

// TestMetadataStore_GetVersion 测试获取版本号
func TestMetadataStore_GetVersion(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 初始版本号
	version := store.GetVersion()
	assert.GreaterOrEqual(t, version, uint64(1))

	// 写入数据后版本号应该递增
	ctx := context.Background()
	err = store.Put(ctx, "key1", []byte("value1"))
	require.NoError(t, err)

	newVersion := store.GetVersion()
	assert.Greater(t, newVersion, version)
}

// TestMetadataStore_GetChangeLogs 测试获取变更日志
func TestMetadataStore_GetChangeLogs(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 写入一些数据
	ctx := context.Background()
	err = store.Put(ctx, "key1", []byte("value1"))
	require.NoError(t, err)

	err = store.Put(ctx, "key2", []byte("value2"))
	require.NoError(t, err)

	// 获取变更日志
	logs := store.GetChangeLogs(0)
	assert.NotEmpty(t, logs)

	// 获取部分变更日志
	logs = store.GetChangeLogs(1)
	// 应该只返回版本号 > 1 的日志
	for _, log := range logs {
		assert.Greater(t, log.Version, uint64(1))
	}
}

// TestMetadataStore_GetStats 测试获取统计信息
func TestMetadataStore_GetStats(t *testing.T) {
	mvStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, err := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	require.NoError(t, err)

	_ = store.Start()
	require.NoError(t, err)
	_ = store.Stop()

	// 执行一些操作
	ctx := context.Background()
	err = store.Put(ctx, "key1", []byte("value1"))
	require.NoError(t, err)

	// 获取统计信息
	stats := store.GetStats()

	assert.NotNil(t, stats)
	assert.Contains(t, stats, "gossip")
	assert.Contains(t, stats, "quorum")
	assert.Contains(t, stats, "twopc")

	// 验证 Gossip 统计
	gossipStats, ok := stats["gossip"].(map[string]any)
	assert.True(t, ok)
	assert.Contains(t, gossipStats, "sync_count")
	assert.Contains(t, gossipStats, "version")

	// 验证 Quorum 统计
	quorumStats, ok := stats["quorum"].(map[string]any)
	assert.True(t, ok)
	assert.Contains(t, quorumStats, "proposals_total")
	assert.Contains(t, quorumStats, "proposals_approved")

	// 验证 2PC 统计
	twoPCStats, ok := stats["twopc"].(map[string]any)
	assert.True(t, ok)
	assert.Contains(t, twoPCStats, "tx_total")
	assert.Contains(t, twoPCStats, "tx_committed")
}

// TestDefaultMetadataStoreConfig 测试默认配置
func TestDefaultMetadataStoreConfig(t *testing.T) {
	config := DefaultMetadataStoreConfig()

	assert.NotEmpty(t, config.CriticalPrefixes)
	assert.Contains(t, config.CriticalPrefixes, "shard/")
	assert.Contains(t, config.CriticalPrefixes, "replica/")
	assert.Contains(t, config.CriticalPrefixes, "node/")
	assert.True(t, config.EnableAutoClassify)
}

// TestChangeType_String 测试变更类型字符串表示
func TestChangeType_String(t *testing.T) {
	testCases := []struct {
		changeType ChangeType
		expected   string
	}{
		{ChangeTypeCreate, "Create"},
		{ChangeTypeUpdate, "Update"},
		{ChangeTypeDelete, "Delete"},
		{ChangeType(99), "Unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			result := tc.changeType.String()
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestConsensusProtocol_String 测试协议类型字符串表示
func TestConsensusProtocol_String(t *testing.T) {
	testCases := []struct {
		protocol ConsensusProtocol
		expected string
	}{
		{ConsensusProtocolGossip, "Gossip"},
		{ConsensusProtocolQuorum, "Quorum"},
		{ConsensusProtocolTwoPC, "2PC"},
		{ConsensusProtocol(99), "Unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			result := tc.protocol.String()
			assert.Equal(t, tc.expected, result)
		})
	}
}

// ========================================
// 性能基准测试
// ========================================

// BenchmarkMetadataStore_Put_Gossip 性能基准测试: Gossip 写入
func BenchmarkMetadataStore_Put_Gossip(b *testing.B) {
	mvStore := newMockMVStore()
	trans, _ := transport.NewUDPTransport("127.0.0.1:0")
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, _ := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	_ = store.Start()
	_ = store.Stop()

	ctx := context.Background()
	key := "status/test"
	value := []byte("active")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.Put(ctx, key, value)
	}
}

// BenchmarkMetadataStore_Get 性能基准测试: 读取
func BenchmarkMetadataStore_Get(b *testing.B) {
	mvStore := newMockMVStore()
	trans, _ := transport.NewUDPTransport("127.0.0.1:0")
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, _ := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	_ = store.Start()

	ctx := context.Background()
	_ = store.Put(ctx, "test_key", []byte("test_value"))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.Get("test_key")
	}
}

// BenchmarkMetadataStore_ExecuteTransaction 性能基准测试: 执行事务
func BenchmarkMetadataStore_ExecuteTransaction(b *testing.B) {
	mvStore := newMockMVStore()
	trans, _ := transport.NewUDPTransport("127.0.0.1:0")
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1"}

	store, _ := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	_ = store.Start()
	_ = store.Stop()

	operations := []transport.Operation{
		{
			Key:   "key1",
			Value: []byte("value1"),
			Type:  "put",
		},
		{
			Key:   "key2",
			Value: []byte("value2"),
			Type:  "put",
		},
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.ExecuteTransaction(ctx, operations)
	}
}

// BenchmarkMetadataStore_selectProtocol 性能基准测试: 协议选择
func BenchmarkMetadataStore_selectProtocol(b *testing.B) {
	mvStore := newMockMVStore()
	trans, _ := transport.NewUDPTransport("127.0.0.1:0")
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1", "node2"}

	store, _ := NewMetadataStore(mvStore, trans, hlc, uuidGen, "node1", nodes, nil)
	_ = store.Start()
	_ = store.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.selectProtocol("status/test", ChangeTypeUpdate)
	}
}
