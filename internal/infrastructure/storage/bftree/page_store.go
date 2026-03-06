// Package bftree 提供 Bf-Tree 的内存页面存储实现
package bftree

import (
	"fmt"
	"sync"
)

// pageStore 内存页面存储（MVP 简化实现）
//
// 设计说明：
// - MVP 阶段：所有页面保持在内存中
// - 未来优化：添加 LRU 缓存 + 磁盘持久化
type pageStore struct {
	mu         sync.RWMutex
	leafNodes  map[uint64]*LeafNode
	innerNodes map[uint64]*InnerNode
}

// newPageStore 创建新的页面存储
func newPageStore() *pageStore {
	return &pageStore{
		leafNodes:  make(map[uint64]*LeafNode),
		innerNodes: make(map[uint64]*InnerNode),
	}
}

// getLeaf 获取叶子节点
func (ps *pageStore) getLeaf(pageID uint64) (*LeafNode, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	node, ok := ps.leafNodes[pageID]
	if !ok {
		return nil, fmt.Errorf("leaf node %d not found", pageID)
	}
	return node, nil
}

// putLeaf 存储叶子节点
func (ps *pageStore) putLeaf(pageID uint64, node *LeafNode) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.leafNodes[pageID] = node
}

// getInner 获取内部节点
func (ps *pageStore) getInner(pageID uint64) (*InnerNode, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	node, ok := ps.innerNodes[pageID]
	if !ok {
		return nil, fmt.Errorf("inner node %d not found", pageID)
	}
	return node, nil
}

// putInner 存储内部节点
//lint:ignore U1000 // 预留方法，未来内部节点分裂时使用
func (ps *pageStore) putInner(pageID uint64, node *InnerNode) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.innerNodes[pageID] = node
}
