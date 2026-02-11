// Package kvstore 提供 Namespace 分层 Merkle 摘要树实现
//
// 核心功能：
//   - Namespace 分层：9 个 Namespace，每个一个 Merkle Tree
//   - O(1) 差异检测：Global Root + Namespace Root + Key Hash
//   - 并发安全：使用 sync.RWMutex 保护共享状态
//   - 版本追踪：支持 Epoch + Version + HLC 时序控制
//   - 增量哈希优化：只重新计算变化的 Namespace（P1 性能优化）
//   - 缓存高频 Namespace：减少重复计算开销
package kvstore

import (
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore/hash"
)

// ==================== NamespacedMerkleTree ====================

// NamespacedMerkleTree Namespace 分层 Merkle 摘要树
//
// 结构：
//
//	GlobalRootHash = SHA256(NamespaceCluster.RootHash + NamespaceShard.RootHash + ...)
//
// 每个 Namespace 一个独立的 Merkle Tree：
//
//	NamespaceRootHash = SHA256(KeyHash1 + KeyHash2 + ...)
//
// 核心特性：
//   - O(1) 差异检测：Global Root → Namespace Root → Key Hash
//   - 并发安全：读写锁保护
//   - 版本追踪：Epoch + Version + HLC
//   - 增量哈希优化：只重新计算变化的 Namespace（dirty tracking）
//   - 缓存高频 Namespace：减少重复计算开销
type NamespacedMerkleTree struct {
	mu         sync.RWMutex
	epoch      uint64                          // 全局逻辑时钟
	version    uint64                          // 全局版本
	namespaces map[string]*NamespaceMerkleTree // 9个Namespace独立树（使用 string 作为键）
	hlc        *clock.HLC                      // HLC 时钟

	// P1 性能优化：增量哈希和缓存
	dirtyNamespaces  map[string]bool // 脏 Namespace 标记（需要重新计算 Global Root）
	cachedGlobalRoot atomic.Value    // 缓存的 Global Root Hash（atomic.Value 存储 string）
	cacheHitCount    atomic.Int64    // 缓存命中计数
	cacheMissCount   atomic.Int64    // 缓存未命中计数
}

// NewNamespacedMerkleTree 创建新的 Namespace 分层 Merkle Tree
func NewNamespacedMerkleTree(hlc *clock.HLC) *NamespacedMerkleTree {
	if hlc == nil {
		hlc = clock.NewHLC()
	}

	nmt := &NamespacedMerkleTree{
		epoch:           0,
		version:         0,
		namespaces:      make(map[string]*NamespaceMerkleTree),
		hlc:             hlc,
		dirtyNamespaces: make(map[string]bool), // 初始化脏标记
	}

	// 初始化缓存为空值
	nmt.cachedGlobalRoot.Store("")

	// 初始化所有 Namespace
	allNamespaces := []string{
		NamespaceCluster,
		NamespaceShard,
		NamespaceNode,
		NamespaceRole,
		NamespaceStatic,
		NamespaceTopo,
		NamespaceDynamic,
		NamespaceOp,
		NamespaceVersion,
	}

	for _, ns := range allNamespaces {
		nmt.namespaces[ns] = &NamespaceMerkleTree{
			Namespace: ns,
			KeyHashes: make(map[string]string),
			RootHash:  computeEmptyHash(),
			version:   0,
			epoch:     0,
		}
	}

	return nmt
}

// GetGlobalRootHash 获取全局 Root Hash
//
// 复杂度：O(1) - 固定 9 个 Namespace + 缓存优化
//
// P1 性能优化：
//   - 如果没有脏 Namespace，直接返回缓存的 Global Root
//   - 如果有脏 Namespace，只重新计算变化的 Namespace
func (n *NamespacedMerkleTree) GetGlobalRootHash() string {
	n.mu.RLock()

	// 检查是否有脏 Namespace（需要重新计算）
	hasDirty := false
	for _, dirty := range n.dirtyNamespaces {
		if dirty {
			hasDirty = true
			break
		}
	}

	// 如果没有脏 Namespace，尝试使用缓存
	if !hasDirty {
		cachedRoot, ok := n.cachedGlobalRoot.Load().(string)
		if ok && cachedRoot != "" {
			n.mu.RUnlock()
			n.cacheHitCount.Add(1)
			return cachedRoot
		}
	}

	// 需要重新计算，升级到写锁
	n.mu.RUnlock()
	n.mu.Lock()
	defer n.mu.Unlock()

	// 再次检查缓存（可能其他 goroutine 已经计算了）
	cachedRoot, ok := n.cachedGlobalRoot.Load().(string)
	hasDirtyNow := false
	for _, dirty := range n.dirtyNamespaces {
		if dirty {
			hasDirtyNow = true
			break
		}
	}
	if !hasDirtyNow && ok && cachedRoot != "" {
		n.cacheHitCount.Add(1)
		return cachedRoot
	}

	n.cacheMissCount.Add(1)

	// 按固定顺序遍历，确保全局Root计算唯一
	orderedNamespaces := []string{
		NamespaceCluster,
		NamespaceShard,
		NamespaceNode,
		NamespaceRole,
		NamespaceStatic,
		NamespaceTopo,
		NamespaceDynamic,
		NamespaceOp,
		NamespaceVersion,
	}

	var namespaceHashes []string
	for _, ns := range orderedNamespaces {
		if tree, ok := n.namespaces[ns]; ok {
			namespaceHashes = append(namespaceHashes, tree.RootHash)
		}
	}

	sum256 := hash.Sum256([]byte(strings.Join(namespaceHashes, "")))
	globalRoot := hex.EncodeToString(sum256[:])

	// 更新缓存
	n.cachedGlobalRoot.Store(globalRoot)

	// 清空脏标记
	for ns := range n.dirtyNamespaces {
		n.dirtyNamespaces[ns] = false
	}

	return globalRoot
}

// GetNamespaceRootHash 获取指定 Namespace 的 Root Hash
//
// 复杂度：O(1) - Hash Map 查找
func (n *NamespacedMerkleTree) GetNamespaceRootHash(ns string) (string, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	tree, ok := n.namespaces[ns]
	if !ok {
		return "", ErrNamespaceNotFound
	}

	return tree.RootHash, nil
}

// GetKeyHash 获取单个 Key 的 Hash
//
// 复杂度：O(1) - Hash Map 查找
func (n *NamespacedMerkleTree) GetKeyHash(ns string, key string) (string, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	tree, ok := n.namespaces[ns]
	if !ok {
		return "", ErrNamespaceNotFound
	}

	hash, ok := tree.KeyHashes[key]
	if !ok {
		return "", ErrKeyNotFound
	}

	return hash, nil
}

// GetAllNamespaceRootHashes 获取所有 Namespace 的 Root Hash
//
// 返回：map[Namespace]RootHash
func (n *NamespacedMerkleTree) GetAllNamespaceRootHashes() map[string]string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	result := make(map[string]string, len(n.namespaces))
	for ns, tree := range n.namespaces {
		result[ns] = tree.RootHash
	}
	return result
}

// UpdateKey 更新 Key 并重新计算 Hash
//
// 流程：
//  1. 计算 Key 的 Hash
//  2. 更新 KeyHashes
//  3. 重新计算 Namespace Root Hash
//  4. 更新版本号
//
// 并发安全性：使用写锁，独占访问
func (n *NamespacedMerkleTree) UpdateKey(ns string, key string, metadata map[string]string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	tree, ok := n.namespaces[ns]
	if !ok {
		return ErrNamespaceNotFound
	}

	// 更新 Key 的 Hash
	tree.KeyHashes[key] = computeHashFromMetadata(metadata)

	// 重新计算 Namespace Root Hash
	n.recomputeNamespaceRootHash(ns)

	// 更新版本号
	n.version++

	return nil
}

// UpdateKeyFromBytes 从字节数组更新 Key（用于元数据同步）
func (n *NamespacedMerkleTree) UpdateKeyFromBytes(ns string, key string, data []byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	tree, ok := n.namespaces[ns]
	if !ok {
		return ErrNamespaceNotFound
	}

	// 计算字节数据的 Hash
	tree.KeyHashes[key] = computeHashFromBytes(data)

	// 重新计算 Namespace Root Hash
	n.recomputeNamespaceRootHash(ns)

	// 更新版本号
	n.version++

	return nil
}

// DeleteKey 删除 Key 并重新计算 Hash
func (n *NamespacedMerkleTree) DeleteKey(ns string, key string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	tree, ok := n.namespaces[ns]
	if !ok {
		return ErrNamespaceNotFound
	}

	// 删除 Key
	delete(tree.KeyHashes, key)

	// 重新计算 Namespace Root Hash
	n.recomputeNamespaceRootHash(ns)

	// 更新版本号
	n.version++

	return nil
}

// recomputeNamespaceRootHash 重新计算 Namespace Root Hash
//
// 注意：调用此方法前必须持有写锁
//
// P1 性能优化：
//   - 标记该 Namespace 为脏，延迟 Global Root 重新计算
//   - 只在 GetGlobalRootHash 时才真正计算（按需计算）
func (n *NamespacedMerkleTree) recomputeNamespaceRootHash(ns string) {
	tree := n.namespaces[ns]

	var keyHashes []string
	for _, hash := range tree.KeyHashes {
		keyHashes = append(keyHashes, hash)
	}

	// 排序确保顺序一致
	sort.Strings(keyHashes)

	// 计算新的 Root Hash
	if len(keyHashes) == 0 {
		tree.RootHash = computeEmptyHash()
	} else {
		sum256 := hash.Sum256([]byte(strings.Join(keyHashes, "")))
		tree.RootHash = hex.EncodeToString(sum256[:])
	}

	// 更新 Namespace 版本号
	tree.version++

	// 标记该 Namespace 为脏（需要重新计算 Global Root）
	n.dirtyNamespaces[ns] = true
}

// GetVersion 获取全局版本号
func (n *NamespacedMerkleTree) GetVersion() uint64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.version
}

// GetEpoch 获取全局逻辑时钟
func (n *NamespacedMerkleTree) GetEpoch() uint64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.epoch
}

// IncrementEpoch 增加全局逻辑时钟
func (n *NamespacedMerkleTree) IncrementEpoch() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.epoch++
}

// GetNamespaceVersion 获取指定 Namespace 的版本号
func (n *NamespacedMerkleTree) GetNamespaceVersion(ns string) (uint64, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	tree, ok := n.namespaces[ns]
	if !ok {
		return 0, ErrNamespaceNotFound
	}

	return tree.version, nil
}

// ==================== P1 性能优化：缓存统计 ====================

// GetCacheStats 获取缓存统计信息
//
// 返回：
//   - hit_count: 缓存命中次数
//   - miss_count: 缓存未命中次数
//   - hit_rate: 缓存命中率（0.0 - 1.0）
func (n *NamespacedMerkleTree) GetCacheStats() map[string]interface{} {
	hitCount := n.cacheHitCount.Load()
	missCount := n.cacheMissCount.Load()
	total := hitCount + missCount

	hitRate := 0.0
	if total > 0 {
		hitRate = float64(hitCount) / float64(total)
	}

	return map[string]interface{}{
		"hit_count":  hitCount,
		"miss_count": missCount,
		"hit_rate":   hitRate,
	}
}

// ResetCacheStats 重置缓存统计（用于测试）
func (n *NamespacedMerkleTree) ResetCacheStats() {
	n.cacheHitCount.Store(0)
	n.cacheMissCount.Store(0)
}

// IsNamespaceDirty 检查 Namespace 是否为脏（需要重新计算 Global Root）
func (n *NamespacedMerkleTree) IsNamespaceDirty(ns string) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.dirtyNamespaces[ns]
}

// ForceRecomputeGlobalRoot 强制重新计算 Global Root（用于测试）
//
// 注意：此方法会清空所有脏标记并重新计算 Global Root
func (n *NamespacedMerkleTree) ForceRecomputeGlobalRoot() string {
	n.mu.Lock()
	defer n.mu.Unlock()

	// 清空缓存
	n.cachedGlobalRoot.Store("")

	// 清空脏标记
	for ns := range n.dirtyNamespaces {
		n.dirtyNamespaces[ns] = false
	}

	// 按固定顺序遍历
	orderedNamespaces := []string{
		NamespaceCluster,
		NamespaceShard,
		NamespaceNode,
		NamespaceRole,
		NamespaceStatic,
		NamespaceTopo,
		NamespaceDynamic,
		NamespaceOp,
		NamespaceVersion,
	}

	var namespaceHashes []string
	for _, ns := range orderedNamespaces {
		if tree, ok := n.namespaces[ns]; ok {
			namespaceHashes = append(namespaceHashes, tree.RootHash)
		}
	}

	sum256 := hash.Sum256([]byte(strings.Join(namespaceHashes, "")))
	globalRoot := hex.EncodeToString(sum256[:])

	// 更新缓存
	n.cachedGlobalRoot.Store(globalRoot)

	return globalRoot
}

// ==================== NamespaceMerkleTree ====================

// NamespaceMerkleTree 单个 Namespace 的 Merkle 摘要树
type NamespaceMerkleTree struct {
	Namespace string            // Namespace 标识
	KeyHashes map[string]string // key -> Hash
	RootHash  string            // 该Namespace的Root Hash（SHA256）
	version   uint64            // 该Namespace的版本号
	epoch     uint64            // 逻辑时钟
}

// ==================== 辅助函数 ====================

// computeHashFromMetadata 从元数据 map 计算 Hash
func computeHashFromMetadata(metadata map[string]string) string {
	// 序列化为字符串（按 key 排序）
	keys := make([]string, 0, len(metadata))
	for k := range metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, k+":"+metadata[k])
	}

	data := strings.Join(parts, ",")
	sum256 := hash.Sum256([]byte(data))
	return hex.EncodeToString(sum256[:])
}

// computeHashFromBytes 从字节数组计算 Hash
func computeHashFromBytes(data []byte) string {
	sum256 := hash.Sum256(data)
	return hex.EncodeToString(sum256[:])
}

// computeEmptyHash 计算空数据的 Hash
func computeEmptyHash() string {
	sum256 := hash.Sum256([]byte(""))
	return hex.EncodeToString(sum256[:])
}
