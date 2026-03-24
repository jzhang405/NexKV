package btree

import (
	"fmt"
	"sync/atomic"
)

// PageRef 页面引用（间接寻址）
// 使用 atomic.Pointer[PageInfo] 实现无锁并发访问
// 要求：Go 1.19+ (atomic.Pointer 泛型支持)
type PageRef struct {
	pInfo     atomic.Pointer[PageInfo] // 原子指针，支持 CAS 更新
	parentRef atomic.Value             // 优化：使用 atomic.Value 存储 *PageRef，移除 defer 开销
	pageLock  atomic.Pointer[PageLock] // Leaf-Level Locking：页面锁（懒加载 + CAS 初始化）
}

// NewPageRef 创建新的 PageRef
func NewPageRef() *PageRef {
	ref := &PageRef{}
	ref.pInfo.Store(nil)
	ref.parentRef.Store((*PageRef)(nil)) // 显式初始化为 nil
	return ref
}

// NewPageRefWithInfo 创建带有初始 PageInfo 的 PageRef
func NewPageRefWithInfo(info *PageInfo) *PageRef {
	ref := &PageRef{}
	ref.pInfo.Store(info)
	ref.parentRef.Store((*PageRef)(nil)) // 显式初始化为 nil
	return ref
}

// GetPage 获取页面对象（原子操作）
// 返回 any，实际类型为 *LeafPage 或 *InternalPage
// 如果 PageInfo 为 nil，返回 nil
func (r *PageRef) GetPage() any {
	info := r.pInfo.Load()
	if info == nil {
		return nil
	}
	return info.GetPage()
}

// GetPageInfo 获取 PageInfo 对象（原子操作）
func (r *PageRef) GetPageInfo() *PageInfo {
	return r.pInfo.Load()
}

// ReplacePage 替换 PageInfo（CAS 操作）
// 使用 Compare-And-Swap 确保原子性
//
// 参数：
//
//	oldInfo - 期望的当前 PageInfo（可以为 nil）
//	newInfo - 新的 PageInfo（不能为 nil）
//
// 返回：
//
//	true - CAS 成功，替换成功
//	false - CAS 失败，当前值不是 oldInfo
func (r *PageRef) ReplacePage(oldInfo, newInfo *PageInfo) bool {
	if newInfo == nil {
		panic("newInfo cannot be nil")
	}
	return r.pInfo.CompareAndSwap(oldInfo, newInfo)
}

// SetPage 直接设置 PageInfo（非原子，用于初始化）
// 注意：此方法不使用 CAS，仅用于单线程初始化场景
func (r *PageRef) SetPage(info *PageInfo) {
	r.pInfo.Store(info)
}

// MarkDirty 标记页面为脏页
// 如果 PageInfo 为 nil，返回 false
func (r *PageRef) MarkDirty() bool {
	info := r.pInfo.Load()
	if info == nil {
		return false
	}
	info.MarkDirty()
	return true
}

// IsDirty 检查是否为脏页
// 如果 PageInfo 为 nil，返回 false
func (r *PageRef) IsDirty() bool {
	info := r.pInfo.Load()
	if info == nil {
		return false
	}
	return info.IsDirty()
}

// GetPos 获取页面位置
// 如果 PageInfo 为 nil，返回 0
func (r *PageRef) GetPos() int64 {
	info := r.pInfo.Load()
	if info == nil {
		return 0
	}
	return info.GetPos()
}

// SetPos 设置页面位置
// 如果 PageInfo 为 nil，直接返回
func (r *PageRef) SetPos(pos int64) {
	info := r.pInfo.Load()
	if info == nil {
		return
	}
	info.SetPos(pos)
}

// GetLock 获取页面锁（懒加载 + CAS 初始化）
// Leaf-Level Locking 核心方法：每个 PageRef 内置锁，懒加载创建
//
// 实现细节：
// 1. 快速路径：如果锁已初始化，直接返回
// 2. 慢速路径：使用 CAS 初始化，防止并发创建多个锁
// 3. CAS 失败：重新加载（其他 goroutine 已创建）
//
// 返回：
//
//	*PageLock - 页面锁（保证非 nil）
func (r *PageRef) GetLock() *PageLock {
	// 快速路径：锁已初始化
	if lock := r.pageLock.Load(); lock != nil {
		return lock
	}

	// 慢速路径：CAS 初始化（防止并发创建多个锁）
	newLock := NewPageLock()
	if r.pageLock.CompareAndSwap(nil, newLock) {
		return newLock // CAS 成功，返回新创建的锁
	}

	// CAS 失败：其他 goroutine 已创建，重新加载
	return r.pageLock.Load()
}

// Touch 更新访问时间（LRU）
// 如果 PageInfo 为 nil，直接返回
func (r *PageRef) Touch() {
	info := r.pInfo.Load()
	if info == nil {
		return
	}
	info.Touch()
}

// GetHits 获取访问计数
// 如果 PageInfo 为 nil，返回 0
func (r *PageRef) GetHits() int64 {
	info := r.pInfo.Load()
	if info == nil {
		return 0
	}
	return info.GetHits()
}

// GetLastTime 获取最后访问时间
// 如果 PageInfo 为 nil，返回 0
func (r *PageRef) GetLastTime() int64 {
	info := r.pInfo.Load()
	if info == nil {
		return 0
	}
	return info.GetLastTime()
}

// GetParentRef 获取父引用（无锁，atomic.Value）
// 优化：移除 defer，减少 tryDeferToSpanScan 开销
func (r *PageRef) GetParentRef() *PageRef {
	return r.parentRef.Load().(*PageRef)
}

// SetParentRef 设置父引用（无锁，atomic.Value）
// 优化：移除 defer，减少 tryDeferToSpanScan 开销
func (r *PageRef) SetParentRef(parent *PageRef) {
	r.parentRef.Store(parent)
}

// Clone 克隆 PageRef（浅拷贝 PageInfo）
// 创建新的 PageRef，共享同一个 PageInfo
func (r *PageRef) Clone() *PageRef {
	info := r.pInfo.Load()
	return NewPageRefWithInfo(info)
}

// IsLoaded 检查页面是否已加载（PageInfo 不为 nil）
func (r *PageRef) IsLoaded() bool {
	return r.pInfo.Load() != nil
}

// Unload 卸载页面（设置 PageInfo 为 nil）
// 返回卸载前的 PageInfo
func (r *PageRef) Unload() *PageInfo {
	oldInfo := r.pInfo.Load()
	r.pInfo.Store(nil)
	return oldInfo
}

// HasParent 检查是否有父引用（无锁，atomic.Value）
// 优化：移除 defer，减少 tryDeferToSpanScan 开销
func (r *PageRef) HasParent() bool {
	return r.parentRef.Load().(*PageRef) != nil
}

// 优化：移除 GetBuff/SetBuff 方法，序列化缓冲区现在由 ChunkManager.pagePool 管理

// GetOrLoad 获取 PageInfo，如果未加载则从 ChunkManager 加载（懒加载）
// 懒加载模式核心：只有 Root 常驻内存，其他页面按需加载
//
// 参数：
//
//	chunkMgr - ChunkManager 用于加载页面数据
//
// 返回：
//
//	*PageInfo - 页面信息（保证 page != nil）
//	error - 错误信息
//
// 懒加载逻辑：
// 1. 如果 pageInfo.page != nil，直接返回（已加载）
// 2. 如果 pageInfo.page == nil 且 pageInfo.pos != 0，从 ChunkManager 加载
// 3. 使用 CAS 更新 pageInfo.page，支持并发安全
func (r *PageRef) GetOrLoad(chunkMgr *ChunkManager) (*PageInfo, error) {
	// 1. 尝试从原子指针加载
	info := r.pInfo.Load()
	if info == nil {
		return nil, fmt.Errorf("pageInfo is nil")
	}

	// 2. 如果 page 已加载，直接返回
	if info.page != nil {
		info.Touch() // 更新访问时间
		return info, nil
	}

	// 3. Double-checked locking：如果 page == nil 且 pos != 0，触发加载
	pos := info.GetPos()
	if info.page == nil && pos != 0 {
		// 从 ChunkManager 加载页面
		page, err := chunkMgr.LoadPage(pos)
		if err != nil {
			return nil, fmt.Errorf("load page at %d: %w", pos, err)
		}

		// CAS 更新 pageInfo.page
		newInfo := info.Clone()
		newInfo.page = page

		if !r.pInfo.CompareAndSwap(info, newInfo) {
			// CAS 失败：其他 goroutine 已加载，重新加载
			return r.GetOrLoad(chunkMgr)
		}

		// 成功加载
		newInfo.Touch()
		return newInfo, nil
	}

	// 4. 如果 pos == 0，说明页面从未持久化
	return nil, fmt.Errorf("page not loaded and no position (pos=0)")
}

// Get 获取 PageInfo（简化版，不含加载逻辑）
// 返回 nil 如果未加载
func (r *PageRef) Get() *PageInfo {
	return r.pInfo.Load()
}
