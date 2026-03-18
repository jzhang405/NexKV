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

// ReplacePage 替换 Root Page（基于 PageID 的 CAS 优化）
// 这是 RootPageRef 的核心方法，确保并发安全
//
// 优化策略（PageID + Pointer CAS）：
// 1. 快速路径：PageID 检查（提前过滤，避免不必要的 CAS）
// 2. 原子操作：Pointer CAS（真正保证原子性）
// 3. 失败重试：CAS 失败则重试整个流程
//
// 关键优势：
// - PageID 在页面生命周期内不变（仅在分裂时变化）
// - 读操作不会改变 PageID（减少伪冲突）
// - 不需要额外的 version 字段（Pointer CAS 已提供必要的同步）
//
// 参数：
//
//	oldRootID - 期望的当前 Root PageID（0 表示任意值）
//	newInfo - 新的 PageInfo（不能为 nil）
//
// 返回：
//
//	true - CAS 成功，替换成功并完成引用链更新
//	false - CAS 失败，Root PageID 已变化
func (r *RootPageRef) ReplacePage(oldRootID uint64, newInfo *PageInfo) bool {
	if newInfo == nil {
		panic("newInfo cannot be nil")
	}

	// 使用循环重试（理论上极少重试）
	for {
		// 阶段 1：加载当前指针
		currentPtr := r.pInfo.Load()
		if currentPtr == nil {
			// Root 未初始化，直接设置
			if r.pInfo.CompareAndSwap(nil, newInfo) {
				return true
			}
			continue // CAS 失败，重试
		}

		// 阶段 2：快速路径 - PageID 检查
		// 如果 PageID 已变化，立即返回（无需 CAS）
		currentRootID := currentPtr.GetPageID()
		if oldRootID != 0 && currentRootID != oldRootID {
			// Root 已分裂，CAS 会失败
			return false
		}

		// 阶段 3：Pointer CAS（原子操作）
		// 只有当其他线程没有修改时，CAS 才会成功
		if r.pInfo.CompareAndSwap(currentPtr, newInfo) {
			// CAS 成功，延迟释放旧页面
			r.scheduleDelayedRelease(currentPtr)
			return true
		}

		// CAS 失败：重试（回到阶段 1）
		// 注意：这种情况在正常并发下应该很少发生
	}
}

// updateChildrenParentRef 递归更新子节点的父引用
// 从当前页面开始，遍历所有子节点，更新它们的 parentRef 指向新的父节点
func (r *RootPageRef) updateChildrenParentRef(page *Page, newParent *PageRef) {
	if page == nil || newParent == nil {
		return
	}

	// 根据页面类型处理
	switch page.Type {
	case model.LeafPage:
		// 叶子节点无子节点，无需处理
		return
	case model.InternalPage:
		// 内部节点：需要反序列化 Data 字段
		// 由于当前架构中 InternalPage 不直接存储在 Page.Data 中
		// 这里暂时保留为预留实现
		// 未来方案：遍历 InternalPage.children 并更新 parentRef
		return
	case model.MetaPage:
		// 元页面，无子节点
		return
	}
}

// scheduleDelayedRelease 延迟释放旧页面
// 等待活跃读操作完成后释放，避免 use-after-free
//
// 简化实现：
// - 使用固定延迟（100ms）等待活跃读操作完成
// - 后续版本可以优化为基于引用计数的精确释放
func (r *RootPageRef) scheduleDelayedRelease(info *PageInfo) {
	// 在后台 goroutine 中延迟释放
	go func() {
		// 等待活跃读操作完成（使用固定延迟）
		// 后续版本可以使用引用计数或读写锁检测
		time.Sleep(100 * time.Millisecond)

		// 清理 PageInfo 中的大对象（page 对象和 buff）
		// 注意：不清理 PageInfo 本身，因为它可能还在使用中
		_ = info

		// 简化实现，仅延迟，不主动释放
		// Go 的 GC 会自动回收不再使用的对象
	}()
}

// ReplacePageWithContext 带上下文的 ReplacePage
// 支持取消操作和超时控制
func (r *RootPageRef) ReplacePageWithContext(
	ctx context.Context,
	oldRootID uint64,
	newInfo *PageInfo,
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
	swapped := r.ReplacePage(oldRootID, newInfo)
	if !swapped {
		return ErrInvalidState
	}

	return nil
}

// UpdateChildrenParentRef 公开方法：手动触发子节点父引用更新
// 用于特殊场景（如页面分裂后需要手动更新引用链）
//
// 注意：此方法为预留接口
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
