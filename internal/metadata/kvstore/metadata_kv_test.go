// Package kvstore MetadataKV 单元测试
package kvstore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	store "github.com/jzhang405/NexKV/internal/wal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMVStore 模拟 MVStore 实现
type mockMVStore struct {
	mu     sync.RWMutex
	data   map[string][]byte
	closed bool
}

func newMockMVStore() *mockMVStore {
	return &mockMVStore{
		data: make(map[string][]byte),
	}
}

func (m *mockMVStore) Put(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrStoreClosed
	}
	m.data[key] = value
	return nil
}

func (m *mockMVStore) Get(key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrStoreClosed
	}
	val, ok := m.data[key]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return val, nil
}

func (m *mockMVStore) GetVersion(key string, hlcTimestamp *clock.HLC) ([]byte, error) {
	// 简化实现：返回最新版本
	return m.Get(key)
}

func (m *mockMVStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrStoreClosed
	}
	delete(m.data, key)
	return nil
}

func (m *mockMVStore) Exists(key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return false, ErrStoreClosed
	}
	_, ok := m.data[key]
	return ok, nil
}

func (m *mockMVStore) List(offset, limit int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrStoreClosed
	}
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func (m *mockMVStore) ListPrefix(prefix string, offset, limit int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrStoreClosed
	}
	keys := make([]string, 0)
	for k := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *mockMVStore) GetVersionCount(key string) (int, error) {
	return 1, nil
}

func (m *mockMVStore) GetAllVersions(key string) ([]*store.VersionInfo, error) {
	// 简化实现：返回 nil
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// TestMetadataKV_PutAndGet 测试 Put 和 Get
func TestMetadataKV_PutAndGet(t *testing.T) {
	store := newMockMVStore()
	hlc := clock.NewHLC()
	kv, err := NewMetadataKV(store, &MetadataKVOptions{HLC: hlc})
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 测试数据
	type TestStruct struct {
		Name  string `msgpack:"name"`
		Value int    `msgpack:"value"`
	}

	testValue := &TestStruct{Name: "test", Value: 123}

	// Put
	err = kv.Put(ctx, NamespaceNode, "node-001", testValue)
	require.NoError(t, err)

	// Get
	var result TestStruct
	err = kv.Get(ctx, NamespaceNode, "node-001", &result)
	require.NoError(t, err)

	assert.Equal(t, "test", result.Name)
	assert.Equal(t, 123, result.Value)
}

// TestMetadataKV_InvalidNamespace 测试无效命名空间
func TestMetadataKV_InvalidNamespace(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 使用无效命名空间
	err = kv.Put(ctx, "invalid:ns:", "key", "value")
	assert.Error(t, err)
	assert.True(t, IsInvalidNamespace(err))
}

// TestMetadataKV_EmptyKey 测试空键
func TestMetadataKV_EmptyKey(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 使用空键
	err = kv.Put(ctx, NamespaceNode, "", "value")
	assert.Error(t, err)
}

// TestMetadataKV_KeyNotFound 测试键不存在
func TestMetadataKV_KeyNotFound(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 查询不存在的键
	var result string
	err = kv.Get(ctx, NamespaceNode, "non-existent", &result)
	assert.Error(t, err)
	assert.True(t, IsKeyNotFound(err))
}

// TestMetadataKV_Delete 测试删除键
func TestMetadataKV_Delete(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 先写入
	err = kv.Put(ctx, NamespaceNode, "node-001", "value")
	require.NoError(t, err)

	// 删除
	err = kv.Delete(ctx, NamespaceNode, "node-001")
	require.NoError(t, err)

	// 验证已删除
	exists, err := kv.Exists(ctx, NamespaceNode, "node-001")
	require.NoError(t, err)
	assert.False(t, exists)
}

// TestMetadataKV_Exists 测试键存在性检查
func TestMetadataKV_Exists(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 不存在的键
	exists, err := kv.Exists(ctx, NamespaceNode, "node-001")
	require.NoError(t, err)
	assert.False(t, exists)

	// 写入键
	err = kv.Put(ctx, NamespaceNode, "node-001", "value")
	require.NoError(t, err)

	// 存在的键
	exists, err = kv.Exists(ctx, NamespaceNode, "node-001")
	require.NoError(t, err)
	assert.True(t, exists)
}

// TestMetadataKV_ListPrefix 测试前缀扫描
func TestMetadataKV_ListPrefix(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 写入多个键
	err = kv.Put(ctx, NamespaceNode, "node-001", "value1")
	require.NoError(t, err)

	err = kv.Put(ctx, NamespaceNode, "node-002", "value2")
	require.NoError(t, err)

	err = kv.Put(ctx, NamespaceNode, "node-003", "value3")
	require.NoError(t, err)

	// 前缀扫描
	keys, err := kv.ListPrefix(ctx, NamespaceNode, "node-")
	require.NoError(t, err)
	assert.Len(t, keys, 3)
	assert.Contains(t, keys, "node-001")
	assert.Contains(t, keys, "node-002")
	assert.Contains(t, keys, "node-003")

	// 前缀扫描（部分匹配）
	keys, err = kv.ListPrefix(ctx, NamespaceNode, "node-0")
	require.NoError(t, err)
	assert.Len(t, keys, 3)
}

// TestMetadataKV_Close 测试关闭存储
func TestMetadataKV_Close(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// 正常写入
	err = kv.Put(ctx, NamespaceNode, "node-001", "value")
	require.NoError(t, err)

	// 关闭存储
	err = kv.Close()
	require.NoError(t, err)

	// 关闭后写入应该失败
	err = kv.Put(ctx, NamespaceNode, "node-002", "value")
	assert.Error(t, err)
	assert.True(t, IsStoreClosed(err))
}

// TestMetadataKV_PutWithOptions 测试带选项的写入
func TestMetadataKV_PutWithOptions(t *testing.T) {
	store := newMockMVStore()
	hlc := clock.NewHLC()

	// 使用 channel 进行同步，避免竞态条件
	gossipCh := make(chan struct{}, 1)
	quorumCh := make(chan struct{}, 1)

	kv, err := NewMetadataKV(store, &MetadataKVOptions{
		HLC: hlc,
		GossipCallback: func(ns, key string, version uint64) {
			select {
			case gossipCh <- struct{}{}:
			default: // 避免阻塞
			}
		},
		QuorumCallback: func(ns, key string, version uint64) {
			select {
			case quorumCh <- struct{}{}:
			default: // 避免阻塞
			}
		},
	})
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 测试最终一致（默认）
	err = kv.Put(ctx, NamespaceNode, "node-001", "value")
	require.NoError(t, err)

	// 等待回调，使用带超时的 select
	select {
	case <-gossipCh:
		// 回调成功
	case <-time.After(100 * time.Millisecond):
		t.Fatal("gossip callback timeout")
	}

	// 测试强一致（显式指定）
	strong := ConsistencyStrong
	opts := &PutOptions{Consistency: &strong}
	err = kv.PutWithOptions(ctx, NamespaceCluster, "config", "value", opts)
	require.NoError(t, err)

	// 等待回调
	select {
	case <-quorumCh:
		// 回调成功
	case <-time.After(100 * time.Millisecond):
		t.Fatal("quorum callback timeout")
	}
}

// TestMetadataKV_GetConsistencyLevel 测试获取一致性级别
func TestMetadataKV_GetConsistencyLevel(t *testing.T) {
	tests := []struct {
		name          string
		namespace     string
		expectedLevel ConsistencyLevel
	}{
		{"Cluster 强一致", NamespaceCluster, ConsistencyStrong},
		{"Node 最终一致", NamespaceNode, ConsistencyEventual},
		{"Role 增强最终一致（Quorum）", NamespaceRole, ConsistencyEnhancedEventual}, // ⚠️ Phase 2 升级
		{"Topo 最终一致", NamespaceTopo, ConsistencyEventual},
		{"Shard 强一致", NamespaceShard, ConsistencyStrong},
		{"Static 强一致", NamespaceStatic, ConsistencyStrong},
		{"Dynamic 最终一致", NamespaceDynamic, ConsistencyEventual},
		{"Op 最终一致", NamespaceOp, ConsistencyEventual},
		{"Version 强一致", NamespaceVersion, ConsistencyStrong},
		{"未知命名空间默认最终一致", "unknown:", ConsistencyEventual},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			level := GetConsistencyLevel(tt.namespace)
			assert.Equal(t, tt.expectedLevel, level)
		})
	}
}

// TestBuildKey 测试键构建
func TestBuildKey(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		key       string
		expected  string
	}{
		{"Node 键", NamespaceNode, "node-001", "meta:node:node-001"},
		{"Role 键", NamespaceRole, "role-001", "meta:role:role-001"},
		{"Topo 键", NamespaceTopo, "node-001", "meta:topo:node-001"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildKey(tt.namespace, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestParseKey 测试键解析
func TestParseKey(t *testing.T) {
	tests := []struct {
		name        string
		fullKey     string
		expectedNS  string
		expectedKey string
		expectedOK  bool
	}{
		{"Node 键", "meta:node:node-001", NamespaceNode, "node-001", true},
		{"Role 键", "meta:role:role-001", NamespaceRole, "role-001", true},
		{"无效键", "invalid:key", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns, key, ok := ParseKey(tt.fullKey)
			assert.Equal(t, tt.expectedOK, ok)
			if ok {
				assert.Equal(t, tt.expectedNS, ns)
				assert.Equal(t, tt.expectedKey, key)
			}
		})
	}
}

// TestMetadataKV_GetVersion 测试版本查询
func TestMetadataKV_GetVersion(t *testing.T) {
	store := newMockMVStore()
	hlc := clock.NewHLC()
	kv, err := NewMetadataKV(store, &MetadataKVOptions{HLC: hlc})
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	type TestStruct struct {
		Name  string `msgpack:"name"`
		Value int    `msgpack:"value"`
	}

	// 写入数据
	testValue := &TestStruct{Name: "test", Value: 123}
	err = kv.Put(ctx, NamespaceNode, "node-001", testValue)
	require.NoError(t, err)

	// 使用当前时间戳查询版本
	hlcTS := hlc.Now()
	var result TestStruct
	err = kv.GetVersion(ctx, NamespaceNode, "node-001", hlcTS, &result)
	require.NoError(t, err)
	assert.Equal(t, "test", result.Name)
	assert.Equal(t, 123, result.Value)
}

// TestMetadataKV_GetVersion_NotFound 测试查询不存在的版本
func TestMetadataKV_GetVersion_NotFound(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()
	hlcTS := clock.NewHLC().Now()

	var result string
	err = kv.GetVersion(ctx, NamespaceNode, "non-existent", hlcTS, &result)
	assert.Error(t, err)
}

// TestMetadataKV_GetRaw 测试原始字节获取
func TestMetadataKV_GetRaw(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 写入数据
	err = kv.Put(ctx, NamespaceNode, "node-001", "value")
	require.NoError(t, err)

	// 获取原始字节
	rawData, err := kv.GetRaw(ctx, NamespaceNode, "node-001")
	require.NoError(t, err)
	assert.NotEmpty(t, rawData)

	// 获取不存在的键
	_, err = kv.GetRaw(ctx, NamespaceNode, "non-existent")
	assert.Error(t, err)
}

// TestMetadataKV_PutRaw 测试原始字节写入
func TestMetadataKV_PutRaw(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 写入原始字节
	err = kv.PutRaw(ctx, NamespaceNode, "node-001", []byte("raw data"))
	require.NoError(t, err)

	// 读取验证
	var result string
	_ = kv.Get(ctx, NamespaceNode, "node-001", &result)
	// 可能解码失败，因为数据不是 MessagePack 格式
	// 这里只验证写入不会报错
}

// TestMetadataKV_BatchGetRaw 测试批量获取
func TestMetadataKV_BatchGetRaw(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 写入多个键
	for i := 1; i <= 3; i++ {
		key := fmt.Sprintf("node-%03d", i)
		err = kv.Put(ctx, NamespaceNode, key, fmt.Sprintf("value%d", i))
		require.NoError(t, err)
	}

	// 批量获取
	keys := []string{"node-001", "node-002", "node-003", "non-existent"}
	result, err := kv.BatchGetRaw(ctx, NamespaceNode, keys)
	require.NoError(t, err)
	assert.Len(t, result, 3) // 只返回存在的键
	assert.Contains(t, result, "node-001")
	assert.Contains(t, result, "node-002")
	assert.Contains(t, result, "node-003")
	assert.NotContains(t, result, "non-existent")
}

// TestMetadataKV_ClosedOperations 测试关闭后的操作
func TestMetadataKV_ClosedOperations(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// 关闭存储
	err = kv.Close()
	require.NoError(t, err)

	// 所有操作都应该失败
	var result string
	err = kv.Get(ctx, NamespaceNode, "key", &result)
	assert.Error(t, err)
	assert.True(t, IsStoreClosed(err))

	err = kv.Put(ctx, NamespaceNode, "key", "value")
	assert.Error(t, err)
	assert.True(t, IsStoreClosed(err))

	err = kv.Delete(ctx, NamespaceNode, "key")
	assert.Error(t, err)
	assert.True(t, IsStoreClosed(err))

	_, err = kv.Exists(ctx, NamespaceNode, "key")
	assert.Error(t, err)
	assert.True(t, IsStoreClosed(err))

	_, err = kv.ListPrefix(ctx, NamespaceNode, "prefix")
	assert.Error(t, err)
	assert.True(t, IsStoreClosed(err))

	_, err = kv.GetRaw(ctx, NamespaceNode, "key")
	assert.Error(t, err)
	assert.True(t, IsStoreClosed(err))

	err = kv.PutRaw(ctx, NamespaceNode, "key", []byte("data"))
	assert.Error(t, err)
	assert.True(t, IsStoreClosed(err))

	_, err = kv.BatchGetRaw(ctx, NamespaceNode, []string{"key"})
	assert.Error(t, err)
	assert.True(t, IsStoreClosed(err))
}

// TestMetadataKV_DoubleClose 测试重复关闭
func TestMetadataKV_DoubleClose(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)

	// 第一次关闭
	err = kv.Close()
	require.NoError(t, err)

	// 第二次关闭应该成功（幂等）
	err = kv.Close()
	require.NoError(t, err)
}

// TestNewMetadataKV_NilStore 测试 nil 存储
func TestNewMetadataKV_NilStore(t *testing.T) {
	_, err := NewMetadataKV(nil, nil)
	assert.Error(t, err)
}

// TestMetadataKV_ListPrefix_EmptyResult 测试前缀扫描空结果
func TestMetadataKV_ListPrefix_EmptyResult(t *testing.T) {
	store := newMockMVStore()
	kv, err := NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// 扫描不存在的键
	keys, err := kv.ListPrefix(ctx, NamespaceNode, "non-existent-")
	require.NoError(t, err)
	assert.Empty(t, keys)

	// 扫描空前缀
	keys, err = kv.ListPrefix(ctx, NamespaceNode, "")
	require.NoError(t, err)
	// 应该返回所有该命名空间的键
	_ = keys // 使用 keys 避免冗余赋值警告
}

// TestBuildUserKeyFromFullKey 测试从完整键提取用户键
func TestBuildUserKeyFromFullKey(t *testing.T) {
	tests := []struct {
		name        string
		fullKey     string
		expectedKey string
		expectedOK  bool
	}{
		{"Node 键", "meta:node:node-001", "node-001", true},
		{"无效键", "invalid", "", false},
		{"空键", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, ok := BuildUserKeyFromFullKey(tt.fullKey)
			assert.Equal(t, tt.expectedOK, ok)
			if ok {
				assert.Equal(t, tt.expectedKey, key)
			}
		})
	}
}

// TestBuildNamespaceKeyFromUserKey 测试从用户键构建完整键
func TestBuildNamespaceKeyFromUserKey(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		userKey   string
		expected  string
	}{
		// path.Join 会在命名空间和用户键之间添加 "/"
		{"Node 键", NamespaceNode, "node-001", "meta:node:/node-001"},
		{"Role 键", NamespaceRole, "role-001", "meta:role:/role-001"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildNamespaceKeyFromUserKey(tt.namespace, tt.userKey)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestMetadataKV_PutWithOptions_OverrideConsistency 测试覆盖默认一致性级别
func TestMetadataKV_PutWithOptions_OverrideConsistency(t *testing.T) {
	store := newMockMVStore()
	hlc := clock.NewHLC()

	// 使用 channel 进行同步
	quorumCh := make(chan struct{}, 1)

	kv, err := NewMetadataKV(store, &MetadataKVOptions{
		HLC: hlc,
		QuorumCallback: func(ns, key string, version uint64) {
			select {
			case quorumCh <- struct{}{}:
			default: // 避免阻塞
			}
		},
	})
	require.NoError(t, err)
	defer kv.Close()

	ctx := context.Background()

	// NamespaceNode 默认是最终一致，但这里强制使用强一致
	strong := ConsistencyStrong
	opts := &PutOptions{Consistency: &strong}
	err = kv.PutWithOptions(ctx, NamespaceNode, "node-001", "value", opts)
	require.NoError(t, err)

	// 等待 goroutine 执行，使用带超时的 select
	select {
	case <-quorumCh:
		// 回调成功
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Quorum callback timeout - ConsistencyStrong override should trigger QuorumCallback")
	}
}
