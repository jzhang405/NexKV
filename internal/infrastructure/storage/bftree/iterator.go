// Package bftree 提供 Bf-Tree 的迭代器实现
package bftree

import (
	"context"
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// Iterator 键值对迭代器接口
//
// 提供顺序遍历 Bf-Tree 中键值对的能力
type Iterator interface {
	// Next 移动到下一个键值对
	// 返回：
	//   - valid: 是否还有有效数据
	//   - key: 键
	//   - value: 值
	//   - err: 错误
	Next() (valid bool, key []byte, value []byte, err error)

	// Close 关闭迭代器
	Close() error
}

// ScanIterator 顺序扫描迭代器
//
// MVP 实现：
// - 从小到大遍历键
// - 支持 [start, end] 范围扫描
// - 使用深度优先遍历
type ScanIterator struct {
	tree    *BfTree
	ctx     context.Context
	start   []byte // 起始键（包含）
	end     []byte // 结束键（不包含）
	current []byte // 当前键

	// 遍历状态
	stack []*iteratorStackEntry // 遍历栈
	closed bool
}

// iteratorStackEntry 遍历栈条目
type iteratorStackEntry struct {
	pageID uint64
	index  int // 当前访问的子节点索引
}

// Scan 扫描指定范围的键值对
//
// 参数：
//   - ctx: 上下文
//   - start: 起始键（包含），nil 表示从头开始
//   - end: 结束键（不包含），nil 表示到末尾
//
// 返回：
//   - Iterator: 迭代器
func (t *BfTree) Scan(ctx context.Context, start, end []byte) Iterator {
	t.rwLock.RLock()
	defer t.rwLock.RUnlock()

	if t.closed.Load() {
		return &errorIterator{err: ErrTreeClosed}
	}

	// 空树
	if t.rootPageID == 0 {
		return &emptyIterator{}
	}

	iter := &ScanIterator{
		tree:    t,
		ctx:     ctx,
		start:   start,
		end:     end,
		current: nil,
		stack:   make([]*iteratorStackEntry, 0),
		closed:  false,
	}

	// 初始化遍历栈
	if err := iter.initStack(); err != nil {
		return &errorIterator{err: fmt.Errorf("failed to init iterator: %w", err)}
	}

	// 移动到第一个有效的键
	iter.moveToNextValid()

	return iter
}

// initStack 初始化遍历栈
func (it *ScanIterator) initStack() error {
	// 从根节点开始，找到最左边的叶子节点
	currentPageID := it.tree.rootPageID

	for {
		entry, found := it.tree.pageTable.Get(currentPageID)
		if !found {
			return fmt.Errorf("page not found: %d", currentPageID)
		}

		if entry.pageType == PageTypeLeaf {
			// 叶子节点：压入栈
			it.stack = append(it.stack, &iteratorStackEntry{
				pageID: currentPageID,
				index:  0,
			})
			break
		}

		// 内部节点：找到最左边的子节点
		innerNode, err := it.tree.pageStore.getInner(currentPageID)
		if err != nil {
			return fmt.Errorf("failed to get inner node: %w", err)
		}

		// 压入内部节点
		it.stack = append(it.stack, &iteratorStackEntry{
			pageID: currentPageID,
			index:  0,
		})

		// 移动到最左边的子节点
		if len(innerNode.children) == 0 {
			return fmt.Errorf("inner node has no children")
		}

		currentPageID = innerNode.children[0]
	}

	return nil
}

// Next 移动到下一个键值对
func (it *ScanIterator) Next() (valid bool, key []byte, value []byte, err error) {
	if it.closed {
		return false, nil, nil, fmt.Errorf("iterator closed")
	}

	it.tree.rwLock.RLock()
	defer it.tree.rwLock.RUnlock()

	if it.tree.closed.Load() {
		return false, nil, nil, ErrTreeClosed
	}

	// 如果栈为空，遍历结束
	if len(it.stack) == 0 {
		return false, nil, nil, nil
	}

	// 获取栈顶的叶子节点
	top := it.stack[len(it.stack)-1]
	if top == nil {
		return false, nil, nil, nil
	}

	leafNode, err := it.tree.pageStore.getLeaf(top.pageID)
	if err != nil {
		return false, nil, nil, fmt.Errorf("failed to get leaf node: %w", err)
	}

	// 获取当前槽位的键值对
	if top.index >= len(leafNode.miniPage.slots) {
		// 当前叶子节点已遍历完，需要向上回溯
		if err := it.moveUp(); err != nil {
			return false, nil, nil, err
		}

		// 检查是否还有数据
		if len(it.stack) == 0 {
			return false, nil, nil, nil
		}

		// 重新获取栈顶
		top = it.stack[len(it.stack)-1]
		leafNode, err = it.tree.pageStore.getLeaf(top.pageID)
		if err != nil {
			return false, nil, nil, fmt.Errorf("failed to get leaf node after move up: %w", err)
		}
	}

	if top.index >= len(leafNode.miniPage.slots) {
		// 应该不会到这里
		return false, nil, nil, nil
	}

	slot := leafNode.miniPage.slots[top.index]

	// 检查范围
	if it.start != nil && compareKeys(slot.key, it.start) < 0 {
		// 还没到起始键，继续移动
		top.index++
		it.moveToNextValid()
		return it.Next()
	}

	if it.end != nil && compareKeys(slot.key, it.end) >= 0 {
		// 超过结束键，遍历结束
		return false, nil, nil, nil
	}

	// 深拷贝键值
	keyCopy := make([]byte, len(slot.key))
	copy(keyCopy, slot.key)

	valueCopy := make([]byte, len(slot.value))
	copy(valueCopy, slot.value)

	// 移动到下一个
	top.index++
	it.current = keyCopy

	return true, keyCopy, valueCopy, nil
}

// moveUp 向上回溯，移动到下一个叶子节点
func (it *ScanIterator) moveUp() error {
	for len(it.stack) > 0 {
		// 弹出当前节点
		it.stack = it.stack[:len(it.stack)-1]

		if len(it.stack) == 0 {
			// 栈已空，遍历结束
			return nil
		}

		top := it.stack[len(it.stack)-1]

		// 如果是内部节点，尝试移动到下一个子节点
		entry, found := it.tree.pageTable.Get(top.pageID)
		if !found {
			return fmt.Errorf("page not found: %d", top.pageID)
		}

		if entry.pageType == PageTypeInner {
			innerNode, err := it.tree.pageStore.getInner(top.pageID)
			if err != nil {
				return fmt.Errorf("failed to get inner node: %w", err)
			}

			top.index++

			// 如果还有下一个子节点，移动到它
			if top.index < len(innerNode.children) {
				nextPageID := innerNode.children[top.index]

				// 从下一个子节点开始，找到最左边的叶子节点
				for {
					entry, found := it.tree.pageTable.Get(nextPageID)
					if !found {
						return fmt.Errorf("page not found: %d", nextPageID)
					}

					if entry.pageType == PageTypeLeaf {
						// 找到叶子节点，压入栈
						it.stack = append(it.stack, &iteratorStackEntry{
							pageID: nextPageID,
							index:  0,
						})
						return nil
					}

					// 内部节点，继续向下
					nextInner, err := it.tree.pageStore.getInner(nextPageID)
					if err != nil {
						return fmt.Errorf("failed to get inner node: %w", err)
					}

					it.stack = append(it.stack, &iteratorStackEntry{
						pageID: nextPageID,
						index:  0,
					})

					if len(nextInner.children) == 0 {
						return fmt.Errorf("inner node has no children")
					}

					nextPageID = nextInner.children[0]
				}
			}

			// 没有下一个子节点，继续向上回溯
			continue
		}
	}

	return nil
}

// moveToNextValid 移动到下一个有效位置
func (it *ScanIterator) moveToNextValid() {
	// MVP: 简化实现，直接返回
	// 实际遍历在 Next() 中处理
}

// Close 关闭迭代器
func (it *ScanIterator) Close() error {
	it.closed = true
	it.stack = nil
	it.current = nil
	return nil
}

// errorIterator 错误迭代器
type errorIterator struct {
	err error
}

func (it *errorIterator) Next() (bool, []byte, []byte, error) {
	return false, nil, nil, it.err
}

func (it *errorIterator) Close() error {
	return nil
}

// emptyIterator 空迭代器
type emptyIterator struct{}

func (it *emptyIterator) Next() (bool, []byte, []byte, error) {
	return false, nil, nil, nil
}

func (it *emptyIterator) Close() error {
	return nil
}

// ScanAsync 异步扫描（v4 模式）
func (t *BfTree) ScanAsync(ctx context.Context, start, end []byte) model.Task[Iterator] {
	return model.NewBaseTask(
		model.OpStorage,
		model.TaskPriorityNormal,
		model.NewSourceShard("bftree"),
		func(ctx context.Context, pipeline model.PipelineContext) (Iterator, error) {
			return t.Scan(ctx, start, end), nil
		},
	)
}
