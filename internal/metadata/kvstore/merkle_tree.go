// Package kvstore 提供 Namespace 分层 Merkle 摘要树实现
//
// 核心功能：
//   - Namespace 分层：9 个 Namespace，每个一个 Merkle Tree
//   - O(1) 差异检测：Global Root + Namespace Root + Key Hash
//   - 并发安全：使用 sync.RWMutex 保护共享状态
//   - 版本追踪：支持 Epoch + Version + HLC 时序控制
package kvstore

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"

	"github.com/jzhang405/NexKV/internal/clock"
)

// ==================== NamespacedMerkleTree ====================

// NamespacedMerkleTree Namespace 分层 Merkle 摘要树
//
// 结构：
//   GlobalRootHash = SHA256(NamespaceCluster.RootHash + NamespaceShard.RootHash + ...)
//
// 每个 Namespace 一个独立的 Merkle Tree：
//   NamespaceRootHash = SHA256(KeyHash1 + KeyHash2 + ...)
//
// 核心特性：
//   - O(1) 差异检测：Global Root → Namespace Root → Key Hash
//   - 并发安全：读写锁保护
//   - 版本追踪：Epoch + Version + HLC
type NamespacedMerkleTree struct {
	mu         sync.RWMutex
	epoch      uint64                                  // 全局逻辑时钟
	version    uint64                                  // 全局版本
	namespaces map[string]*NamespaceMerkleTree         // 9个Namespace独立树（使用 string 作为键）
	hlc        *clock.HLC                              // HLC 时钟
}

// NewNamespacedMerkleTree 创建新的 Namespace 分层 Merkle Tree
func NewNamespacedMerkleTree(hlc *clock.HLC) *NamespacedMerkleTree {
	if hlc == nil {
		hlc = clock.NewHLC()
	}

	nmt := &NamespacedMerkleTree{
		epoch:      0,
		version:    0,
		namespaces: make(map[string]*NamespaceMerkleTree),
		hlc:        hlc,
	}

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
// 复杂度：O(1) - 固定 9 个 Namespace
func (n *NamespacedMerkleTree) GetGlobalRootHash() string {
	n.mu.RLock()
	defer n.mu.RUnlock()

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

	hash := sha256.Sum256([]byte(strings.Join(namespaceHashes, "")))
	return hex.EncodeToString(hash[:])
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
		hash := sha256.Sum256([]byte(strings.Join(keyHashes, "")))
		tree.RootHash = hex.EncodeToString(hash[:])
	}

	// 更新 Namespace 版本号
	tree.version++
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
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// computeHashFromBytes 从字节数组计算 Hash
func computeHashFromBytes(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// computeEmptyHash 计算空数据的 Hash
func computeEmptyHash() string {
	hash := sha256.Sum256([]byte(""))
	return hex.EncodeToString(hash[:])
}
