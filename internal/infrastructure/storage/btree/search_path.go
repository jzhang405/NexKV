package btree

import (
	"context"
	"fmt"
)

// searchPath 搜索从 Root 到 Leaf 的完整路径
// 这是 BTree 搜索的核心方法，支持懒加载和并发安全
//
// 参数：
//   ctx - 上下文（用于取消和超时）
//   key - 搜索键
//
// 返回：
//   []*PageInfo - 从 Root 到 Leaf 的路径（按深度排序）
//   error - 错误信息
//
// 搜索流程：
// 1. 从 Root PageRef 开始（通过 VersionedRoot 获取）
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

	// 1. 获取 Root PageInfo（通过 VersionedRoot）
	// TODO: Week 13-14 - 使用 b.rootRef 替代 b.root
	if b.root == nil {
		return nil, fmt.Errorf("root not initialized")
	}

	rootInfo := b.root.Get() // 获取 *RootInfo
	if rootInfo == nil {
		return nil, fmt.Errorf("root info is nil")
	}

	// 2. 从 RootInfo 获取 Root Node
	// TODO: Week 13-14 - 这里使用旧的 Node 架构
	// 需要在后续版本中迁移到 PageRef 架构

	// 临时方案：将 Node 转换为 PageInfo
	// 这是一个过渡期的实现，Week 13-14 会完全迁移到 PageRef

	rootNode := rootInfo.Root
	if rootNode == nil {
		return nil, fmt.Errorf("root node is nil")
	}

	// 3. 创建临时的 PageInfo 包装
	// 注意：这是过渡期的实现，Week 13-14 会移除此转换
	rootPageInfo := b.nodeToPageInfo(rootNode)
	if rootPageInfo == nil {
		return nil, fmt.Errorf("failed to convert root node to page info")
	}

	// 4. 开始 DFS 搜索，构建路径
	var path []*PageInfo
	currentInfo := rootPageInfo

	for {
		// 4.1 将当前 PageInfo 添加到路径
		path = append(path, currentInfo)

		// 4.2 懒加载当前页面（如果尚未加载）
		currentPage, err := b.getPageOrLoad(currentInfo)
		if err != nil {
			return nil, fmt.Errorf("load page at depth %d: %w", len(path)-1, err)
		}

		// 4.3 判断是否为叶子节点
		// 类型断言：检查是否为 LeafPage
		if leafPage, ok := currentPage.(*LeafPage); ok && leafPage != nil {
			// 到达叶子节点，搜索结束
			break
		}

		// 4.4 处理内部节点
		internalPage, ok := currentPage.(*InternalPage)
		if !ok || internalPage == nil {
			// 可能是旧的 Node 类型
			return nil, fmt.Errorf("expected internal page, got %T", currentPage)
		}

		// 4.5 查找子节点
		childRef := internalPage.FindChildRef(key)
		if childRef == nil {
			// 没有子节点，可能是空树或到达叶子
			break
		}

		// 4.6 获取子节点的 PageInfo
		childInfo := childRef.GetPageInfo()
		if childInfo == nil {
			return nil, fmt.Errorf("child page info is nil")
		}

		// 4.7 检查层级深度
		if len(path) >= b.maxLevels {
			return nil, fmt.Errorf("search path exceeds max levels (%d)", b.maxLevels)
		}

		// 4.8 检查 context 取消
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// 4.9 继续向下搜索
		currentInfo = childInfo
	}

	return path, nil
}

// nodeToPageInfo 临时方法：将 Node 转换为 PageInfo（过渡期实现）
// TODO: Week 13-14 - 完全迁移到 PageRef 架构后删除此方法
func (b *BTree) nodeToPageInfo(node *Node) *PageInfo {
	if node == nil {
		return nil
	}

	// 临时创建 PageInfo（实际应该从 PageRef 获取）
	info := NewPageInfo()
	// info.page = node // 旧架构：直接使用 Node
	// 新架构：应该使用 LeafPage 或 InternalPage

	// TODO: 实现从 Node 到 Page 的转换
	// 这需要根据 Node.IsLeaf() 来决定创建 LeafPage 还是 InternalPage

	return info
}

// findLeafPage 直接搜索到叶子节点（便捷方法）
// 返回叶子节点的 PageInfo 和路径
//
// 参数：
//   ctx - 上下文
//   key - 搜索键
//
// 返回：
//   *PageInfo - 叶子节点的 PageInfo
//   []*PageInfo - 完整路径（包括 Root 和 Leaf）
//   error - 错误信息
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
