// Package kvstore 元数据 KV 存储核心封装
//
// 核心功能：
//   - 命名空间隔离：自动添加命名空间前缀
//   - MVCC 版本控制：封装 MVStore 的多版本能力
//   - 一致性机制：根据命名空间自动选择 Quorum/Gossip
//   - 并发安全：使用 sync.RWMutex 保护共享状态
package kvstore

import (
	"context"
	"path"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/clock"
	store "github.com/jzhang405/NexKV/internal/wal"
)

// cachedEntry 缓存条目（P1 性能优化）
type cachedEntry struct {
	value   any    // 缓存的值（已解码的对象）
	version uint64 // 版本号
	data    []byte // 原始数据（用于验证）
}

// ConsistencyLevel 一致性级别
type ConsistencyLevel int

const (
	// ConsistencyStrong 强一致（Quorum）
	// 写入后立即对所有节点可见
	// 适用于：集群配置、分片信息、静态配置、版本控制
	ConsistencyStrong ConsistencyLevel = iota

	// ConsistencyEventual 最终一致（Gossip）
	// 写入后异步扩散，秒级一致
	// 适用于：节点信息、角色信息、拓扑关系、动态状态、操作记录
	ConsistencyEventual
)

// consistencyMapping 命名空间到一致性级别的映射
var consistencyMapping = map[string]ConsistencyLevel{
	NamespaceCluster: ConsistencyStrong,   // 集群配置：强一致
	NamespaceNode:    ConsistencyEventual, // 节点信息：最终一致
	NamespaceRole:    ConsistencyEventual, // 角色信息：最终一致
	NamespaceTopo:    ConsistencyEventual, // 拓扑关系：最终一致
	NamespaceShard:   ConsistencyStrong,   // 分片信息：强一致
	NamespaceStatic:  ConsistencyStrong,   // 静态配置：强一致
	NamespaceDynamic: ConsistencyEventual, // 动态状态：最终一致
	NamespaceOp:      ConsistencyEventual, // 操作记录：最终一致
	NamespaceVersion: ConsistencyStrong,   // 版本控制：强一致
}

// PutOptions 写入选项
type PutOptions struct {
	// Consistency 一致性级别（如果为空，使用命名空间默认值）
	// 使用指针类型以区分"未设置"和"显式设置为 Strong"（Strong = 0）
	Consistency *ConsistencyLevel

	// Sync 是否同步等待确认
	Sync bool
}

// MetadataKV 元数据 KV 存储封装
//
// 职责：
//   - 封装 MVStore，提供命名空间隔离
//   - 强类型接口：Get/Put 支持类型断言
//   - MVCC 版本控制：GetVersion 支持历史版本查询
//   - 一致性机制：根据命名空间自动选择同步策略
//   - P1 性能优化：内置缓存层减少编解码开销
type MetadataKV struct {
	store  store.MVStore  // MVStore 存储引擎
	hlc    *clock.HLC     // HLC 时钟（生成版本号）
	codec  *MetadataCodec // MessagePack 编解码器
	mu     sync.RWMutex   // 保护 closed 和 gossipCallbacks
	closed bool           // 是否已关闭

	// P1 性能优化：缓存层
	cache     *sync.Map    // 缓存已解码的对象 (key -> cachedEntry)
	cacheHit  atomic.Int64 // 缓存命中计数
	cacheMiss atomic.Int64 // 缓存未命中计数

	// 同步回调（用于集成 Gossip/Quorum）
	gossipCallback func(ns, key string, version uint64)
	quorumCallback func(ns, key string, version uint64)
}

// MetadataKVOptions MetadataKV 配置选项
type MetadataKVOptions struct {
	// HLC HLC 时钟实例（如果为空，创建新实例）
	HLC *clock.HLC

	// Codec 编解码器（如果为空，使用默认编解码器）
	Codec *MetadataCodec

	// GossipCallback 最终一致的同步回调
	GossipCallback func(ns, key string, version uint64)

	// QuorumCallback 强一致的同步回调
	QuorumCallback func(ns, key string, version uint64)
}

// NewMetadataKV 创建元数据 KV 存储
func NewMetadataKV(store store.MVStore, opts *MetadataKVOptions) (*MetadataKV, error) {
	if store == nil {
		return nil, ErrStoreNotInitialized
	}

	// 默认选项
	if opts == nil {
		opts = &MetadataKVOptions{}
	}

	// 默认 HLC 时钟
	hlc := opts.HLC
	if hlc == nil {
		hlc = clock.NewHLC()
	}

	// 默认编解码器
	codec := opts.Codec
	if codec == nil {
		codec = DefaultCodec()
	}

	return &MetadataKV{
		store:          store,
		hlc:            hlc,
		codec:          codec,
		cache:          &sync.Map{}, // P1 性能优化：初始化缓存
		gossipCallback: opts.GossipCallback,
		quorumCallback: opts.QuorumCallback,
	}, nil
}

// Put 写入键值对
//
// 流程：
//  1. 验证命名空间和键
//  2. 编码值为 []byte
//  3. 写入 MVStore（自动生成版本号）
//  4. 根据命名空间触发同步（Gossip 或 Quorum）
func (m *MetadataKV) Put(ctx context.Context, ns, key string, value any) error {
	return m.PutWithOptions(ctx, ns, key, value, nil)
}

// PutWithOptions 带选项的写入
func (m *MetadataKV) PutWithOptions(ctx context.Context, ns, key string, value any, opts *PutOptions) error {
	// 检查是否已关闭
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return ErrStoreClosed
	}
	m.mu.RUnlock()

	// 验证命名空间
	if !ValidateNamespace(ns) {
		return NewMetadataError(ns, key, ErrCodeInvalidNamespace, "invalid namespace", ErrInvalidNamespace)
	}

	// 验证键
	if key == "" {
		return NewMetadataError(ns, key, ErrCodeEmptyKey, "empty key", ErrEmptyKey)
	}

	// 构建完整键
	fullKey := BuildKey(ns, key)

	// 编码值
	data, err := m.codec.Encode(value)
	if err != nil {
		return NewMetadataError(ns, key, ErrCodeEncodingFailed, "encode failed", err)
	}

	// 写入 MVStore
	if err := m.store.Put(fullKey, data); err != nil {
		return NewMetadataError(ns, key, ErrCodeEncodingFailed, "put to store failed", err)
	}

	// 获取版本号（使用 HLC 生成）
	hlcTS := m.hlc.Now()
	version := (uint64(hlcTS.PhysicalTime()) << 16) | uint64(hlcTS.LogicalCounter())

	// 根据选项或命名空间选择一致性级别
	// 如果 opts 提供了明确的一致性级别，使用 opts 的值
	// 否则使用命名空间的默认一致性级别
	var consistency ConsistencyLevel
	if opts != nil && opts.Consistency != nil {
		consistency = *opts.Consistency
	} else {
		consistency = consistencyMapping[ns]
	}

	// 触发同步（异步）
	go m.triggerSync(ns, key, version, consistency)

	return nil
}

// Get 获取键值
//
// P1 性能优化：优先从缓存获取，减少编解码开销
func (m *MetadataKV) Get(ctx context.Context, ns, key string, value any) error {
	// 检查是否已关闭
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return ErrStoreClosed
	}
	m.mu.RUnlock()

	// 验证命名空间
	if !ValidateNamespace(ns) {
		return NewMetadataError(ns, key, ErrCodeInvalidNamespace, "invalid namespace", ErrInvalidNamespace)
	}

	// 验证键
	if key == "" {
		return NewMetadataError(ns, key, ErrCodeEmptyKey, "empty key", ErrEmptyKey)
	}

	// 构建完整键
	fullKey := BuildKey(ns, key)

	// P1 性能优化：先检查缓存
	if cached, ok := m.cache.Load(fullKey); ok {
		entry := cached.(*cachedEntry)
		m.cacheHit.Add(1)
		// 从缓存的 data 字节解码
		if err := m.codec.Decode(entry.data, value); err == nil {
			return nil
		}
		// 缓存使用失败，继续从存储读取
	}
	m.cacheMiss.Add(1)

	// 从 MVStore 读取
	data, err := m.store.Get(fullKey)
	if err != nil {
		return NewMetadataError(ns, key, ErrCodeKeyNotFound, "key not found", ErrKeyNotFound)
	}

	// 解码值
	if err := m.codec.Decode(data, value); err != nil {
		return NewMetadataError(ns, key, ErrCodeDecodingFailed, "decode failed", err)
	}

	// P1 性能优化：更新缓存（异步）
	go m.updateCache(fullKey, value, data)

	return nil
}

// GetVersion 获取指定版本的键值
func (m *MetadataKV) GetVersion(ctx context.Context, ns, key string, hlcTS *clock.HLC, value any) error {
	// 检查是否已关闭
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return ErrStoreClosed
	}
	m.mu.RUnlock()

	// 验证命名空间
	if !ValidateNamespace(ns) {
		return NewMetadataError(ns, key, ErrCodeInvalidNamespace, "invalid namespace", ErrInvalidNamespace)
	}

	// 验证键
	if key == "" {
		return NewMetadataError(ns, key, ErrCodeEmptyKey, "empty key", ErrEmptyKey)
	}

	// 构建完整键
	fullKey := BuildKey(ns, key)

	// 从 MVStore 读取指定版本
	data, err := m.store.GetVersion(fullKey, hlcTS)
	if err != nil {
		return NewMetadataError(ns, key, ErrCodeVersionNotFound, "version not found", ErrVersionNotFound)
	}

	// 解码值
	if err := m.codec.Decode(data, value); err != nil {
		return NewMetadataError(ns, key, ErrCodeDecodingFailed, "decode failed", err)
	}

	return nil
}

// Delete 删除键
func (m *MetadataKV) Delete(ctx context.Context, ns, key string) error {
	// 检查是否已关闭
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return ErrStoreClosed
	}
	m.mu.RUnlock()

	// 验证命名空间
	if !ValidateNamespace(ns) {
		return NewMetadataError(ns, key, ErrCodeInvalidNamespace, "invalid namespace", ErrInvalidNamespace)
	}

	// 验证键
	if key == "" {
		return NewMetadataError(ns, key, ErrCodeEmptyKey, "empty key", ErrEmptyKey)
	}

	// 构建完整键
	fullKey := BuildKey(ns, key)

	// 从 MVStore 删除
	if err := m.store.Delete(fullKey); err != nil {
		return NewMetadataError(ns, key, ErrCodeKeyNotFound, "delete failed", err)
	}

	// 获取版本号
	hlcTS := m.hlc.Now()
	version := (uint64(hlcTS.PhysicalTime()) << 16) | uint64(hlcTS.LogicalCounter())

	// 触发同步（异步）
	go m.triggerSync(ns, key, version, consistencyMapping[ns])

	return nil
}

// Exists 检查键是否存在
func (m *MetadataKV) Exists(ctx context.Context, ns, key string) (bool, error) {
	// 检查是否已关闭
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return false, ErrStoreClosed
	}
	m.mu.RUnlock()

	// 验证命名空间
	if !ValidateNamespace(ns) {
		return false, NewMetadataError(ns, key, ErrCodeInvalidNamespace, "invalid namespace", ErrInvalidNamespace)
	}

	// 验证键
	if key == "" {
		return false, nil
	}

	// 构建完整键
	fullKey := BuildKey(ns, key)

	// 检查是否存在
	return m.store.Exists(fullKey)
}

// ListPrefix 列出指定前缀的所有键
//
// 返回：不包含命名空间前缀的用户键列表
func (m *MetadataKV) ListPrefix(ctx context.Context, ns, prefix string) ([]string, error) {
	// 检查是否已关闭
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, ErrStoreClosed
	}
	m.mu.RUnlock()

	// 验证命名空间
	if !ValidateNamespace(ns) {
		return nil, ErrInvalidNamespace
	}

	// 构建完整前缀
	fullPrefix := BuildKey(ns, prefix)

	// 从 MVStore 列出
	fullKeys, err := m.store.ListPrefix(fullPrefix, 0, -1)
	if err != nil {
		return nil, err
	}

	// 移除命名空间前缀，返回用户键
	result := make([]string, 0, len(fullKeys))
	for _, fullKey := range fullKeys {
		if strings.HasPrefix(fullKey, ns) {
			userKey := strings.TrimPrefix(fullKey, ns)
			result = append(result, userKey)
		}
	}

	return result, nil
}

// Close 关闭存储
func (m *MetadataKV) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil // 已经关闭
	}

	m.closed = true

	// 关闭 MVStore
	if err := m.store.Close(); err != nil {
		return err
	}

	return nil
}

// triggerSync 触发同步（根据一致性级别）
func (m *MetadataKV) triggerSync(ns, key string, version uint64, consistency ConsistencyLevel) {
	// 再次检查 closed 状态，防止在 Close() 后继续执行
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()

	if closed {
		return
	}

	if consistency == ConsistencyStrong {
		// 强一致：触发 Quorum 确认
		if m.quorumCallback != nil {
			m.quorumCallback(ns, key, version)
		}
	} else {
		// 最终一致：触发 Gossip 扩散
		if m.gossipCallback != nil {
			m.gossipCallback(ns, key, version)
		}
	}
}

// GetConsistencyLevel 获取命名空间的一致性级别
func GetConsistencyLevel(ns string) ConsistencyLevel {
	if level, ok := consistencyMapping[ns]; ok {
		return level
	}
	return ConsistencyEventual // 默认最终一致
}

// BuildUserKeyFromFullKey 从完整键提取用户键
// 例如：meta:node:node-001 → node-001
func BuildUserKeyFromFullKey(fullKey string) (string, bool) {
	_, key, ok := ParseKey(fullKey)
	if !ok {
		return "", false
	}
	// key 已经是用户键（ParseKey 会移除命名空间前缀）
	return key, true
}

// BuildNamespaceKeyFromUserKey 从用户键和命名空间构建完整键
// 例如：node-001 + meta:node: → meta:node:node-001
func BuildNamespaceKeyFromUserKey(ns, userKey string) string {
	return path.Join(ns, userKey)
}

// ========================================
// 原始字节访问接口（用于元数据同步）
// ========================================

// GetRaw 获取原始字节数据（跳过 MessagePack 解码）
//
// 用于元数据同步场景，直接返回 MVStore 中的原始字节
func (m *MetadataKV) GetRaw(ctx context.Context, ns, key string) ([]byte, error) {
	// 检查是否已关闭
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, ErrStoreClosed
	}
	m.mu.RUnlock()

	// 验证命名空间
	if !ValidateNamespace(ns) {
		return nil, ErrInvalidNamespace
	}

	// 验证键
	if key == "" {
		return nil, ErrEmptyKey
	}

	// 构建完整键
	fullKey := BuildKey(ns, key)

	// 直接从 MVStore 读取原始字节
	return m.store.Get(fullKey)
}

// PutRaw 写入原始字节数据（跳过 MessagePack 编码）
//
// 用于元数据同步场景，直接存储接收到的原始字节
func (m *MetadataKV) PutRaw(ctx context.Context, ns, key string, data []byte) error {
	// 检查是否已关闭
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return ErrStoreClosed
	}
	m.mu.RUnlock()

	// 验证命名空间
	if !ValidateNamespace(ns) {
		return ErrInvalidNamespace
	}

	// 验证键
	if key == "" {
		return ErrEmptyKey
	}

	// 构建完整键
	fullKey := BuildKey(ns, key)

	// 直接写入 MVStore
	if err := m.store.Put(fullKey, data); err != nil {
		return err
	}

	// 获取版本号（使用 HLC 生成）
	hlcTS := m.hlc.Now()
	version := (uint64(hlcTS.PhysicalTime()) << 16) | uint64(hlcTS.LogicalCounter())

	// 触发同步（根据命名空间的一致性级别）
	consistency := consistencyMapping[ns]
	go m.triggerSync(ns, key, version, consistency)

	return nil
}

// BatchGetRaw 批量获取原始字节数据
func (m *MetadataKV) BatchGetRaw(ctx context.Context, ns string, keys []string) (map[string][]byte, error) {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, ErrStoreClosed
	}
	m.mu.RUnlock()

	if !ValidateNamespace(ns) {
		return nil, ErrInvalidNamespace
	}

	result := make(map[string][]byte, len(keys))
	for _, key := range keys {
		fullKey := BuildKey(ns, key)
		data, err := m.store.Get(fullKey)
		if err == nil {
			result[key] = data
		}
		// 跳过不存在的键
	}

	return result, nil
}

// ========================================
// P1 性能优化：缓存辅助方法
// ========================================

// updateCache 异步更新缓存
func (m *MetadataKV) updateCache(fullKey string, value any, data []byte) {
	// 检查是否已关闭
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return
	}
	m.mu.RUnlock()

	// 复制 data 字节（避免后续修改影响）
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	// 直接缓存 data 字节，在缓存命中时重新解码
	// 这样避免了并发修改 value 的问题
	cached := &cachedEntry{
		value:   nil, // 不缓存解码后的值，只缓存原始字节
		version: uint64(len(dataCopy)),
		data:    dataCopy,
	}
	m.cache.Store(fullKey, cached)
}

// GetCacheStats 获取缓存统计信息
func (m *MetadataKV) GetCacheStats() map[string]int64 {
	return map[string]int64{
		"hit":  m.cacheHit.Load(),
		"miss": m.cacheMiss.Load(),
	}
}

// DIP 修复：显式接口实现检查
// 确保 MetadataKV 实现了 kvstore.Store 接口
var _ Store = (*MetadataKV)(nil)
