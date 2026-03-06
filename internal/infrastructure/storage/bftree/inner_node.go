// Package bftree 提供 Bf-Tree 的内部节点实现
package bftree

import (
	"bytes"
	"errors"
	"sync"
)

// InnerNode Bf-Tree 内部节点（索引节点）
//
// 结构设计：
// - 用于索引和路由查找
// - 包含子页面 ID 和分隔键
// - 支持 m-way 查找（m = 子节点数量）
//
// B+ 树内部节点特性：
// - keys[i] 是 children[i] 和 children[i+1] 的分隔键
// - 所有查找都从根节点开始，递归到叶子节点
// - 内部节点不存储实际数据，只存储索引
type InnerNode struct {
	// 基础元数据
	pageID  uint64    // 页面 ID（唯一标识）
	level   PageLevel // 节点级别（L1-L6/Full）
	version uint64    // 版本号（用于并发控制）

	// 索引数据
	keys     [][]byte // 分隔键（len(keys) = len(children) - 1）
	children []uint64 // 子页面 ID（LeafNode 或 InnerNode）

	// 并发控制
	mu sync.RWMutex // 读写锁

	// 配置
	maxKeys int // 最大键数量（对应分支因子）
}

// NewInnerNode 创建新的内部节点
//
// 参数：
//   - pageID: 页面 ID
//   - level: 节点级别
//
// 返回：
//   - 初始化的 InnerNode
func NewInnerNode(pageID uint64, level PageLevel) *InnerNode {
	// 根据 PageLevel 确定最大键数量
	maxKeys := maxKeysForLevel(level)

	return &InnerNode{
		pageID:   pageID,
		level:    level,
		version:  1,
		keys:     make([][]byte, 0, maxKeys-1),
		children: make([]uint64, 0, maxKeys),
		maxKeys:  maxKeys,
	}
}

// maxKeysForLevel 获取各级别最大键数量
// B+ 树特性：内部节点的子节点数 = 键数 + 1
func maxKeysForLevel(level PageLevel) int {
	switch level {
	case L1:
		return 2 // 2 个子节点，1 个键
	case L2:
		return 3 // 3 个子节点，2 个键
	case L3:
		return 5 // 5 个子节点，4 个键
	case L4:
		return 9 // 9 个子节点，8 个键
	case L5:
		return 17 // 17 个子节点，16 个键
	case L6:
		return 33 // 33 个子节点，32 个键
	default:
		return 65 // Full: 65 个子节点，64 个键
	}
}

// FindChild 查找子节点
//
// 给定一个键，返回应该访问的子节点 ID
//
// 算法：
// 1. 从左到右扫描 keys
// 2. 找到第一个大于目标键的 key
// 3. 返回对应的 children[i]
// 4. 如果所有键都小于目标，返回最后一个子节点
//
// 参数：
//   - key: 查找键
//
// 返回：
//   - childID: 子页面 ID
//   - found: 是否找到
func (n *InnerNode) FindChild(key []byte) (uint64, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if len(n.children) == 0 {
		return 0, false
	}

	// 从左到右扫描 keys
	for i, k := range n.keys {
		// 如果 key < k，应该访问 children[i]
		if bytes.Compare(key, k) < 0 {
			return n.children[i], true
		}
	}

	// 所有键都小于目标，返回最后一个子节点
	return n.children[len(n.children)-1], true
}

// InsertChild 插入子节点
//
// 在指定的位置插入子节点和分隔键
//
// 参数：
//   - index: 插入位置
//   - key: 分隔键
//   - childID: 子页面 ID
//
// 返回：
//   - error: 错误
func (n *InnerNode) InsertChild(index int, key []byte, childID uint64) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// 参数验证
	if index > 0 && key == nil {
		return ErrNilKey
	}
	if index > 0 && len(key) == 0 {
		return ErrEmptyKey
	}
	if index < 0 || index > len(n.children) {
		return errors.New("invalid index")
	}

	// 检查容量
	if len(n.children) >= n.maxKeys {
		return ErrPageFull
	}

	// 插入子节点
	n.children = append(n.children, 0)
	copy(n.children[index+1:], n.children[index:])
	n.children[index] = childID

	// 插入分隔键（如果不是第一个子节点）
	if index > 0 {
		n.keys = append(n.keys, []byte{})
		copy(n.keys[index:], n.keys[index-1:])
		n.keys[index-1] = key
	}

	n.version++
	return nil
}

// Split 分裂节点
//
// 当节点满时，分裂成两个节点
//
// 返回：
//   - newNode: 新节点
//   - splitKey: 分裂键（提升到父节点）
//   - error: 错误
func (n *InnerNode) Split() (*InnerNode, []byte, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// 检查是否需要分裂
	if len(n.children) < n.maxKeys {
		return nil, nil, errors.New("node not full")
	}

	// 计算分裂点（中间位置）
	splitIndex := len(n.children) / 2

	// 创建新节点（右半部分）
	newNode := &InnerNode{
		pageID:   n.pageID + 1, // 简化：使用 pageID + 1
		level:    n.level,
		version:  1,
		keys:     make([][]byte, 0),
		children: make([]uint64, 0),
		maxKeys:  n.maxKeys,
	}

	// 复制右半部分的键和子节点
	newNode.keys = append(newNode.keys, n.keys[splitIndex:]...)
	newNode.children = append(newNode.children, n.children[splitIndex:]...)

	// 分裂键（提升到父节点）
	splitKey := n.keys[splitIndex-1]

	// 保留左半部分
	n.keys = n.keys[:splitIndex-1]
	n.children = n.children[:splitIndex]

	n.version++

	return newNode, splitKey, nil
}

// Merge 合并节点
//
// 当节点过少时，与兄弟节点合并
//
// 参数：
//   - sibling: 兄弟节点
//
// 返回：
//   - error: 错误
func (n *InnerNode) Merge(sibling *InnerNode) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// 参数验证
	if sibling == nil {
		return errors.New("sibling is nil")
	}
	if sibling.level != n.level {
		return errors.New("level mismatch")
	}

	// 合并键和子节点
	n.keys = append(n.keys, sibling.keys...)
	n.children = append(n.children, sibling.children...)

	n.version++

	return nil
}

// GetKeyCount 获取键数量
func (n *InnerNode) GetKeyCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.keys)
}

// GetChildCount 获取子节点数量
func (n *InnerNode) GetChildCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.children)
}

// IsFull 检查节点是否已满
func (n *InnerNode) IsFull() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.children) >= n.maxKeys
}

// CanMerge 检查是否可以合并
func (n *InnerNode) CanMerge(sibling *InnerNode) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()

	totalChildren := len(n.children) + len(sibling.children)
	return totalChildren < n.maxKeys
}

// GetPageID 获取页面 ID
func (n *InnerNode) GetPageID() uint64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.pageID
}

// GetLevel 获取节点级别
func (n *InnerNode) GetLevel() PageLevel {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.level
}

// GetVersion 获取版本号
func (n *InnerNode) GetVersion() uint64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.version
}
