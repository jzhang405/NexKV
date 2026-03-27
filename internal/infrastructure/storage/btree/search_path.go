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

		// ✅ 添加 key-05655 的调试日志
		if string(key) == "key-05655" {
			fmt.Printf("\n[SEARCH_PATH key-05655] currentPageID=%d currentIsLeaf=%v\n", currentPageID, currentIsLeaf)
		}

		if currentIsLeaf {
			// 到达叶子节点，返回收集的路径和引用
			if debugThisSearch {
				DebugPrintf("[SEARCH_PATH] key=%s reached leaf pageID=%d\n", string(key), currentPageID)
			}
			break
		}

		// 2.2 查找子节点（Off-Heap 模式）
		childPageID, _, err := b.offheapAdapter.SearchChild(currentPageID, key)
		if err != nil {
			// 版本号不匹配，检测到僵尸引用
			// 返回 ErrRetry 让外层重试
			// ✅ 修复：不要包装 ErrRetry，否则 errors.Is() 检查会失败
			return nil, nil, ErrRetry
		}
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

		// 2.4 防御性检查：检测接近页面分配上限的情况
		// Page 4095 (0xFFF) 是 4KB 页面下的最后一个可分配页面 ID
		// 95% 的循环引用失败涉及此页面，可能是页面释放后重新分配导致的
		if childPageID > 4000 {
			DebugPrintf("[SEARCH_PATH] Warning: childPageID %d near max limit (4095), parent=%d depth=%d\n",
				childPageID, currentPageID, len(path))
			// 验证 PageRefCache 一致性：检查 childInfo 的 pageID 是否匹配
			// 这可能在页面被释放并重新分配后检测到不一致
		}

		// 2.5 从缓存获取或创建子节点的 PageRef
		// 注意：PageRefCache.GetOrCreate 内部已处理 pageID 重用情况
		// 当缓存的 PageInfo.pageID 与请求的 pageID 不匹配时，会自动创建新的 PageRef
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

			return nil, nil, fmt.Errorf("%w: at page %d (path depth: %d)", ErrCircularReference, currentPageID, len(path))
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
//	[]*PageRef - 完整路径的 PageRef 引用（包括 Root 和 Leaf）
//	error - 错误信息
func (b *BTree) findLeafPageRef(ctx context.Context, key []byte) (*PageRef, []*PageInfo, []*PageRef, error) {
	// 使用优化版本：一次遍历同时收集 PageInfo 和 PageRef
	path, refs, err := b.searchPathWithRefs(ctx, key)
	if err != nil {
		return nil, nil, nil, err
	}

	if len(path) == 0 {
		return nil, nil, nil, fmt.Errorf("empty search path")
	}

	if len(refs) == 0 {
		return nil, nil, nil, fmt.Errorf("empty refs")
	}

	// 最后一个引用是叶子节点的 PageRef
	leafRef := refs[len(refs)-1]
	return leafRef, path, refs, nil
}

// hasCycleFrom 检测从指定页面开始是否存在循环引用
// 用于防御性检查，确保 B-Tree 结构正确性
//
// 参数：
//
//	pageID - 起始页面 ID
//
// 返回：true 如果检测到循环引用
func (b *BTree) hasCycleFrom(pageID model.PageID) bool {
	// 使用 visited map 检测循环
	visited := make(map[uint32]bool)
	maxDepth := 100 // 防止无限循环

	var traverse func(pid model.PageID, depth int) bool
	traverse = func(pid model.PageID, depth int) bool {
		if depth > maxDepth {
			DebugPrintf("[HAS_CYCLE] Max depth exceeded at page %d\n", pid)
			return true // 可能存在循环
		}

		pid32 := uint32(pid)
		if visited[pid32] {
			DebugPrintf("[HAS_CYCLE] Cycle detected at page %d (depth %d)\n", pid, depth)
			return true // 发现循环
		}

		visited[pid32] = true

		// 检查是否为叶子节点
		isLeaf := b.offheapAdapter.IsLeaf(pid)
		if isLeaf {
			return false // 叶子节点没有子节点，不可能有循环
		}

		// 递归检查所有子节点
		count := b.offheapAdapter.pa.GetCount(pid32)
		// 只有内部节点才有子节点（count > 0）
		// 叶子节点的 count=0，不应该进入循环
		for i := 0; i <= int(count) && int(count) > 0; i++ {
			// 修复：GetChild 返回编码后的值（包含版本号）
			// 需要解码才能获取真实的 pageID
			encodedChild := b.offheapAdapter.pa.GetChild(pid32, i)
			if encodedChild == 0 {
				continue // 跳过空子节点
			}
			child, _ := b.offheapAdapter.DecodeChildWithVersion(encodedChild)
			if traverse(model.PageID(child), depth+1) {
				return true
			}
		}

		return false
	}

	return traverse(pageID, 0)
}

// validateParentSplitIntegrity 验证父节点分裂后的完整性
// 确保旧子节点被正确移除，新子节点被正确添加
//
// 参数：
//
//	parentPageID - 父页面 ID
//	oldChild - 被分裂的旧子节点（应该不存在）
//	leftChild - 分裂后的左子节点（应该存在）
//	rightChild - 分裂后的右子节点（应该存在）
//
// 返回：error 如果验证失败
func (b *BTree) validateParentSplitIntegrity(
	parentPageID model.PageID,
	oldChild, leftChild, rightChild model.PageID,
) error {
	count := b.offheapAdapter.pa.GetCount(uint32(parentPageID))

	foundOld := false
	foundLeft := false
	foundRight := false
	childCount := 0

	for i := 0; i <= int(count); i++ {
		// 修复：GetChild 返回编码后的值（包含版本号）
		// 需要解码才能获取真实的 pageID
		encodedChild := b.offheapAdapter.pa.GetChild(uint32(parentPageID), i)
		if encodedChild == 0 {
			continue // 跳过空子节点
		}
		child, _ := b.offheapAdapter.DecodeChildWithVersion(encodedChild)
		childCount++
		if child == uint32(oldChild) {
			foundOld = true
		}
		if child == uint32(leftChild) {
			foundLeft = true
		}
		if child == uint32(rightChild) {
			foundRight = true
		}
	}

	// 验证：旧子节点不应该存在
	if foundOld {
		return fmt.Errorf("old child %d still exists after split at parent %d",
			oldChild, parentPageID)
	}

	// 验证：新子节点必须存在
	if !foundLeft || !foundRight {
		return fmt.Errorf("new children not found in parent %d: left=%v, right=%v",
			parentPageID, foundLeft, foundRight)
	}

	// 验证：子节点数量应该正确（count keys + 1 children）
	expectedChildren := int(count) + 1
	if childCount != expectedChildren {
		return fmt.Errorf("child count mismatch: expected %d, got %d",
			expectedChildren, childCount)
	}

	return nil
}
