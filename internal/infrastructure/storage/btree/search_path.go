package btree

import (
	"context"
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
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
		// 修复：Get 操作不需要克隆，直接使用原始页面
		if leafPage, ok := currentPage.(*LeafPage); ok && leafPage != nil {
			// 到达叶子节点，直接添加到路径（不克隆）
			// Get 是只读操作，不需要 COW
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

// searchPathWithRefs 搜索从 Root 到 Leaf 的完整路径，同时收集 PageRef
// 这是 findLeafPageRef 的优化版本，一次遍历同时收集 PageInfo 和 PageRef
//
// 参数：
//
//	ctx - 上下文（用于取消和超时）
//	key - 搜索键
//
// 返回：
//
//	[]*PageInfo - 从 Root 到 Leaf 的路径（按深度排序）
//	[]*PageRef - 对应的 PageRef 链（按深度排序）
//	error - 错误信息
func (b *BTree) searchPathWithRefs(ctx context.Context, key []byte) ([]*PageInfo, []*PageRef, error) {
	// 检查上下文取消
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	default:
	}

	// 1. 获取 Root PageInfo（通过 RootPageRef）
	if b.rootRef == nil {
		return nil, nil, fmt.Errorf("root not initialized")
	}

	// 使用原子指针获取 Root PageInfo
	rootInfo := b.rootRef.pInfo.Load()
	if rootInfo == nil {
		return nil, nil, fmt.Errorf("root page info is nil")
	}

	// 2. 从 Root PageInfo 开始搜索
	var path []*PageInfo
	var refs []*PageRef
	currentInfo := rootInfo
	currentRef := b.rootRef.PageRef

	// 调试：追踪 key-06151、key-06267 和 key-09803 的搜索路径
	debugThisSearch := string(key) == "key-06151" || string(key) == "key-06267" || string(key) == "key-06266" || string(key) == "key-09803" || string(key) == "key-09802"
	if debugThisSearch {
		DebugPrintf("[SEARCH_PATH] key=%s starting search\n", string(key))
	}

	// 添加 Root 到路径和引用
	path = append(path, currentInfo)
	refs = append(refs, currentRef)

	// 循环引用检测：记录已访问的页面ID
	visitedPages := make(map[uint64]bool)
	visitedPages[currentInfo.GetPageID()] = true

	for {
		// 2.1 判断是否为叶子节点（Off-Heap 模式）
		// 修复：使用 Off-Heap 页面的 pageType，而不是 PageInfo 的 NodeRef.isLeaf
		currentPageID := model.PageID(currentInfo.GetPageID())
		currentIsLeaf := b.offheapAdapter.IsLeaf(currentPageID)
		if currentIsLeaf {
			// 到达叶子节点，返回收集的路径和引用
			if debugThisSearch {
				DebugPrintf("[SEARCH_PATH] key=%s reached leaf pageID=%d\n", string(key), currentPageID)
			}
			break
		}

		// 2.2 查找子节点（Off-Heap 模式）
		childPageID, _ := b.offheapAdapter.SearchChild(currentPageID, key)
		if debugThisSearch {
			DebugPrintf("[SEARCH_PATH] key=%s at parent pageID=%d found childPageID=%d\n", string(key), currentPageID, childPageID)
			// 打印父节点的所有 keys 和 children
			count := b.offheapAdapter.pa.GetCount(uint32(currentPageID))
			DebugPrintf("[SEARCH_PATH] parent pageID=%d has %d keys:\n", currentPageID, count)
			for i := 0; i < int(count); i++ {
				keyOff, keyLen, child := b.offheapAdapter.pa.GetIndexEntryOffset(uint32(currentPageID), i)
				pageKey := b.offheapAdapter.pa.GetKey(uint32(currentPageID), keyOff, keyLen)
				DebugPrintf("[SEARCH_PATH]   [%d] key=%s child=%d\n", i, string(pageKey), child)
			}
			// 打印 extraChild（N+1 child）
			extraChild := b.offheapAdapter.pa.GetChild(uint32(currentPageID), int(count))
			DebugPrintf("[SEARCH_PATH]   [%d] (extraChild)=%d\n", count, extraChild)
		}
		if childPageID == 0 {
			// 没有子节点，可能到达叶子
			break
		}

		// 2.3 判断子节点类型（叶子或内部）
		// 使用 Off-Heap 页面的 pageType
		isChildLeaf := b.offheapAdapter.IsLeaf(childPageID)

		// 2.5 从缓存获取或创建子节点的 PageRef
		childRef := b.pageRefCache.GetOrCreate(childPageID, isChildLeaf)
		childInfo := childRef.GetPageInfo()
		if childInfo == nil {
			return nil, nil, fmt.Errorf("child page info is nil at depth %d", len(path))
		}

		// 2.6 检查层级深度
		if len(path) >= b.maxLevels {
			return nil, nil, fmt.Errorf("search path exceeds max levels (%d)", b.maxLevels)
		}

		// 2.7 循环引用检测
		currentPageID = model.PageID(childInfo.GetPageID())
		if visitedPages[uint64(currentPageID)] {
			// 调试日志：打印完整路径
			DebugPrintf("[CIRCULAR_REF] pageID=%d depth=%d\n", currentPageID, len(path))
			DebugPrintf("[CIRCULAR_REF] Path: ")
			for _, p := range path {
				DebugPrintf("%d ", p.GetPageID())
			}
			DebugPrintf("\n")

			return nil, nil, fmt.Errorf("circular reference detected at page %d (path depth: %d)", currentPageID, len(path))
		}
		visitedPages[uint64(currentPageID)] = true

		// 2.8 检查 context 取消
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}

		// 2.9 同时收集 PageInfo 和 PageRef
		path = append(path, childInfo)
		refs = append(refs, childRef)

		// 2.10 继续向下搜索
		currentInfo = childInfo
	}

	return path, refs, nil
}

// findLeafPageRef 搜索叶子节点并返回其 PageRef（Leaf-Level Locking 专用）
// 通过遍历路径收集 PageRef 链
//
// 参数：
//
//	ctx - 上下文
//	key - 搜索键
//
// 返回：
//
//	*PageRef - 叶子节点的 PageRef
//	[]*PageInfo - 完整路径（包括 Root 和 Leaf）
//	error - 错误信息
func (b *BTree) findLeafPageRef(ctx context.Context, key []byte) (*PageRef, []*PageInfo, error) {
	// 使用优化版本：一次遍历同时收集 PageInfo 和 PageRef
	path, refs, err := b.searchPathWithRefs(ctx, key)
	if err != nil {
		return nil, nil, err
	}

	if len(path) == 0 {
		return nil, nil, fmt.Errorf("empty search path")
	}

	if len(refs) == 0 {
		return nil, nil, fmt.Errorf("empty refs")
	}

	// 最后一个引用是叶子节点的 PageRef
	leafRef := refs[len(refs)-1]
	return leafRef, path, nil
}
