package btree

import (
	"context"
	"fmt"
)

// searchPath 搜索从 Root 到 Leaf 的完整路径
// 这是 BTree 搜索的核心方法，支持懒加载和并发安全
//
// 参数：
//
//	ctx - 上下文（用于取消和超时）
//	key - 搜索键
//
// 返回：
//
//	[]*PageInfo - 从 Root 到 Leaf 的路径（按深度排序）
//	error - 错误信息
//
// 搜索流程：
// 1. 从 Root PageRef 开始（通过 b.rootRef 获取）
// 2. 逐层向下搜索，每层都懒加载页面
// 3. 遇到 Leaf Page 时结束
// 4. 返回完整的路径（包括 Root 和 Leaf）
//
// 懒加载：
// - 对于 InternalPage，懒加载其子节点
// - 使用 BTree.getPageOrLoad() 按需加载
//
// 并发安全：
// - 读取操作使用原子指针
// - 不修改路径中的任何数据
func (b *BTree) searchPath(ctx context.Context, key []byte) ([]*PageInfo, error) {
	// 检查上下文取消
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// 1. 获取 Root PageInfo（通过 RootPageRef）
	if b.rootRef == nil {
		return nil, fmt.Errorf("root not initialized")
	}

	// 使用原子指针获取 Root PageInfo
	rootInfo := b.rootRef.pInfo.Load()
	if rootInfo == nil {
		return nil, fmt.Errorf("root page info is nil")
	}

	// 2. 从 Root PageInfo 开始搜索
	var path []*PageInfo
	currentInfo := rootInfo

	for {
		// 2.1 将当前 PageInfo 添加到路径
		path = append(path, currentInfo)

		// 2.2 懒加载当前页面（如果尚未加载）
		currentPage, err := b.getPageOrLoad(currentInfo)
		if err != nil {
			return nil, fmt.Errorf("load page at depth %d: %w", len(path)-1, err)
		}

		// 2.3 判断是否为叶子节点
		// ✅ PageLock 优化：使用 TryLock 避免立即深拷贝
		if leafPage, ok := currentPage.(*LeafPage); ok && leafPage != nil {
			// 尝试获取 PageLock，避免立即深拷贝
			if leafPage.pageLock.TryLock() {
				// ✅ 成功获取锁，使用浅拷贝（共享引用）
				clonedInfo := NewPageInfo()
				clonedInfo.SetPage(leafPage) // 共享引用，不深拷贝
				clonedInfo.cloneStatus.Store(CloneStatusShallow)
				// 注意：锁释放在 setWithCAS 的 defer 中
				path[len(path)-1] = clonedInfo
			} else {
				// ✅ 获取锁失败，回退到深拷贝（保证正确性）
				clonedPage := leafPage.Clone()
				clonedInfo := NewPageInfo()
				clonedInfo.SetPage(clonedPage)
				clonedInfo.cloneStatus.Store(CloneStatusDeep)
				path[len(path)-1] = clonedInfo
			}
			break
		}

		// 2.4 处理内部节点
		internalPage, ok := currentPage.(*InternalPage)
		if !ok || internalPage == nil {
			// 如果不是 InternalPage，可能是空树（Root 是 LeafPage）
			// 检查是否是空树的情况
			if len(path) == 1 {
				// Root 是一个空的 LeafPage，正常退出
				break
			}
			return nil, fmt.Errorf("expected internal page at depth %d, got %T", len(path)-1, currentPage)
		}

		// 2.5 查找子节点
		childRef := internalPage.FindChildRef(key)
		if childRef == nil {
			// 没有子节点，可能是到达叶子
			break
		}

		// 2.6 获取子节点的 PageInfo
		childInfo := childRef.GetPageInfo()
		if childInfo == nil {
			return nil, fmt.Errorf("child page info is nil at depth %d", len(path))
		}

		// 2.7 检查层级深度
		if len(path) >= b.maxLevels {
			return nil, fmt.Errorf("search path exceeds max levels (%d)", b.maxLevels)
		}

		// 2.8 检查 context 取消
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// 2.9 继续向下搜索
		currentInfo = childInfo
	}

	return path, nil
}

// findLeafPage 直接搜索到叶子节点（便捷方法）
// 返回叶子节点的 PageInfo 和路径
//
// 参数：
//
//	ctx - 上下文
//	key - 搜索键
//
// 返回：
//
//	*PageInfo - 叶子节点的 PageInfo
//	[]*PageInfo - 完整路径（包括 Root 和 Leaf）
//	error - 错误信息
func (b *BTree) findLeafPage(ctx context.Context, key []byte) (*PageInfo, []*PageInfo, error) {
	path, err := b.searchPath(ctx, key)
	if err != nil {
		return nil, nil, err
	}

	if len(path) == 0 {
		return nil, nil, fmt.Errorf("empty search path")
	}

	// 路径的最后一个元素是叶子节点
	leafInfo := path[len(path)-1]
	return leafInfo, path, nil
}
