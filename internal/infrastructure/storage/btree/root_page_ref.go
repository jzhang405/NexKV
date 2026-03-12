package btree

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// RootPageRef Root 页面的特殊引用
// 处理 Root Page 的 CAS 更新和引用链维护
type RootPageRef struct {
	*PageRef
}

// NewRootPageRef 创建新的 RootPageRef
func NewRootPageRef() *RootPageRef {
	return &RootPageRef{
		PageRef: NewPageRef(),
	}
}

// NewRootPageRefWithInfo 创建带有初始 PageInfo 的 RootPageRef
func NewRootPageRefWithInfo(info *PageInfo) *RootPageRef {
	return &RootPageRef{
		PageRef: NewPageRefWithInfo(info),
	}
}

// ReplacePage 替换 Root Page（原子更新并维护引用链）
// 这是 RootPageRef 的核心方法，确保并发安全
//
// 执行顺序（根据 v3.0 设计）：
// 1. 先 CAS 更新 pInfo（原子操作）
// 2. CAS 成功后，更新所有子节点的 parentRef（指向新 Root）
// 3. 延迟释放旧页面（等待活跃读操作完成）
//
// 参数：
//   oldInfo - 期望的当前 PageInfo（可以为 nil）
//   newInfo - 新的 PageInfo（不能为 nil）
//
// 返回：
//   true - CAS 成功，替换成功并完成引用链更新
//   false - CAS 失败，当前值不是 oldInfo
func (r *RootPageRef) ReplacePage(oldInfo, newInfo *PageInfo) bool {
	if newInfo == nil {
		panic("newInfo cannot be nil")
	}

	// 步骤1：CAS 更新 pInfo（原子操作）
	swapped := r.pInfo.CompareAndSwap(oldInfo, newInfo)
	if !swapped {
		return false
	}

	// 步骤2：CAS 成功后，更新所有子节点的 parentRef
	// 注意：在当前架构下，Page.Data 是原始字节数组
	// 我们需要等到 LeafPage/InternalPage 新架构实现后再完善此功能
	// Phase 1: 预留接口，暂不实现
	_ = r.updateChildrenParentRef

	// 步骤3：延迟释放旧页面（如果存在）
	if oldInfo != nil {
		r.scheduleDelayedRelease(oldInfo)
	}

	return true
}

// updateChildrenParentRef 递归更新子节点的父引用
// 从当前页面开始，遍历所有子节点，更新它们的 parentRef 指向新的父节点
//
// 注意：此方法在 Phase 1 中是预留接口，完整实现需要等到：
// 1. LeafPage 和 InternalPage 新结构实现
// 2. Node → PageRef 迁移完成
func (r *RootPageRef) updateChildrenParentRef(page *Page, newParent *PageRef) {
	// Phase 1: 预留接口
	// TODO: 等待 LeafPage/InternalPage 实现后完善
	_ = page
	_ = newParent

	// 未来实现：
	// 1. 解析 page.Type (LeafPage, InternalPage)
	// 2. 如果是 InternalPage，获取其 children []*PageRef
	// 3. 递归更新每个子节点的 parentRef
	//
	// 示例代码（待实现）：
	// switch page.Type {
	// case model.LeafPage:
	//     return // 叶子节点无子节点
	// case model.InternalPage:
	//     internalNode := deserializeInternalNode(page.Data)
	//     for _, childRef := range internalNode.Children {
	//         childRef.SetParentRef(newParent)
	//         childPage := childRef.GetPage()
	//         if childPage != nil {
	//             r.updateChildrenParentRef(childPage, newParent)
	//         }
	//     }
	// }
}

// scheduleDelayedRelease 延迟释放旧页面
// 等待活跃读操作完成后释放，避免 use-after-free
//
// 在 Phase 1 中，我们简化实现：
// - 使用固定延迟（100ms）等待活跃读操作完成
// - 后续版本可以优化为基于引用计数的精确释放
func (r *RootPageRef) scheduleDelayedRelease(info *PageInfo) {
	// 在后台 goroutine 中延迟释放
	go func() {
		// 等待活跃读操作完成（Phase 1 使用固定延迟）
		// 后续版本可以使用引用计数或读写锁检测
		time.Sleep(100 * time.Millisecond)

		// 清理 PageInfo 中的大对象（page 对象和 buff）
		// 注意：不清理 PageInfo 本身，因为它可能还在使用中
		_ = info

		// Phase 1: 简化实现，仅延迟，不主动释放
		// Go 的 GC 会自动回收不再使用的对象
	}()
}

// ReplacePageWithContext 带上下文的 ReplacePage
// 支持取消操作和超时控制
func (r *RootPageRef) ReplacePageWithContext(
	ctx context.Context,
	oldInfo, newInfo *PageInfo,
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// 检查上下文是否已取消
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 执行 CAS 更新
	swapped := r.ReplacePage(oldInfo, newInfo)
	if !swapped {
		return ErrInvalidState
	}

	return nil
}

// UpdateChildrenParentRef 公开方法：手动触发子节点父引用更新
// 用于特殊场景（如页面分裂后需要手动更新引用链）
//
// 注意：Phase 1 中此方法为预留接口
func (r *RootPageRef) UpdateChildrenParentRef(page *Page) {
	r.updateChildrenParentRef(page, r.PageRef)
}

// GetRootPage 获取 Root Page（便捷方法）
// 返回 interface{}，实际类型为 *LeafPage 或 *InternalPage
func (r *RootPageRef) GetRootPage() interface{} {
	return r.GetPage()
}

// GetRootPageInfo 获取 Root PageInfo（便捷方法）
func (r *RootPageRef) GetRootPageInfo() *PageInfo {
	return r.GetPageInfo()
}

// IsRootPageType 检查页面是否为 Root 类型
func IsRootPageType(page *Page) bool {
	return page != nil && page.Type == model.InternalPage
}
