package btree

import (
	"sync"
	"sync/atomic"
)

// PageReference 页面引用（间接寻址）
// 使用 atomic.Pointer[PageInfo] 实现无锁并发访问
// 要求：Go 1.19+ (atomic.Pointer 泛型支持)
type PageReference struct {
	pInfo     atomic.Pointer[PageInfo]  // 原子指针，支持 CAS 更新
	parentRef *PageReference             // 父引用，形成引用链（弱引用避免循环）
	mu        sync.RWMutex              // 保护 parentRef 更新
}

// NewPageReference 创建新的 PageReference
func NewPageReference() *PageReference {
	ref := &PageReference{
		parentRef: nil,
	}
	ref.pInfo.Store(nil)
	return ref
}

// NewPageReferenceWithInfo 创建带有初始 PageInfo 的 PageReference
func NewPageReferenceWithInfo(info *PageInfo) *PageReference {
	ref := &PageReference{
		parentRef: nil,
	}
	ref.pInfo.Store(info)
	return ref
}

// GetPage 获取 Page 对象（原子操作）
// 如果 PageInfo 为 nil，返回 nil
func (r *PageReference) GetPage() *Page {
	info := r.pInfo.Load()
	if info == nil {
		return nil
	}
	return info.GetPage()
}

// GetPageInfo 获取 PageInfo 对象（原子操作）
func (r *PageReference) GetPageInfo() *PageInfo {
	return r.pInfo.Load()
}

// ReplacePage 替换 PageInfo（CAS 操作）
// 使用 Compare-And-Swap 确保原子性
//
// 参数：
//   oldInfo - 期望的当前 PageInfo（可以为 nil）
//   newInfo - 新的 PageInfo（不能为 nil）
//
// 返回：
//   true - CAS 成功，替换成功
//   false - CAS 失败，当前值不是 oldInfo
func (r *PageReference) ReplacePage(oldInfo, newInfo *PageInfo) bool {
	if newInfo == nil {
		panic("newInfo cannot be nil")
	}
	return r.pInfo.CompareAndSwap(oldInfo, newInfo)
}

// SetPage 直接设置 PageInfo（非原子，用于初始化）
// 注意：此方法不使用 CAS，仅用于单线程初始化场景
func (r *PageReference) SetPage(info *PageInfo) {
	r.pInfo.Store(info)
}

// MarkDirty 标记页面为脏页
// 如果 PageInfo 为 nil，返回 false
func (r *PageReference) MarkDirty() bool {
	info := r.pInfo.Load()
	if info == nil {
		return false
	}
	info.MarkDirty()
	return true
}

// IsDirty 检查是否为脏页
// 如果 PageInfo 为 nil，返回 false
func (r *PageReference) IsDirty() bool {
	info := r.pInfo.Load()
	if info == nil {
		return false
	}
	return info.IsDirty()
}

// GetPos 获取页面位置
// 如果 PageInfo 为 nil，返回 0
func (r *PageReference) GetPos() int64 {
	info := r.pInfo.Load()
	if info == nil {
		return 0
	}
	return info.GetPos()
}

// SetPos 设置页面位置
// 如果 PageInfo 为 nil，直接返回
func (r *PageReference) SetPos(pos int64) {
	info := r.pInfo.Load()
	if info == nil {
		return
	}
	info.SetPos(pos)
}

// GetLock 获取页面锁
// 如果 PageInfo 为 nil，返回 nil
func (r *PageReference) GetLock() *PageLock {
	info := r.pInfo.Load()
	if info == nil {
		return nil
	}
	return info.GetLock()
}

// Touch 更新访问时间（LRU）
// 如果 PageInfo 为 nil，直接返回
func (r *PageReference) Touch() {
	info := r.pInfo.Load()
	if info == nil {
		return
	}
	info.Touch()
}

// GetHits 获取访问计数
// 如果 PageInfo 为 nil，返回 0
func (r *PageReference) GetHits() int64 {
	info := r.pInfo.Load()
	if info == nil {
		return 0
	}
	return info.GetHits()
}

// GetLastTime 获取最后访问时间
// 如果 PageInfo 为 nil，返回 0
func (r *PageReference) GetLastTime() int64 {
	info := r.pInfo.Load()
	if info == nil {
		return 0
	}
	return info.GetLastTime()
}

// GetParentRef 获取父引用
func (r *PageReference) GetParentRef() *PageReference {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.parentRef
}

// SetParentRef 设置父引用（线程安全）
func (r *PageReference) SetParentRef(parent *PageReference) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parentRef = parent
}

// Clone 克隆 PageReference（浅拷贝 PageInfo）
// 创建新的 PageReference，共享同一个 PageInfo
func (r *PageReference) Clone() *PageReference {
	info := r.pInfo.Load()
	return NewPageReferenceWithInfo(info)
}

// IsLoaded 检查页面是否已加载（PageInfo 不为 nil）
func (r *PageReference) IsLoaded() bool {
	return r.pInfo.Load() != nil
}

// Unload 卸载页面（设置 PageInfo 为 nil）
// 返回卸载前的 PageInfo
func (r *PageReference) Unload() *PageInfo {
	oldInfo := r.pInfo.Load()
	r.pInfo.Store(nil)
	return oldInfo
}

// HasParent 检查是否有父引用
func (r *PageReference) HasParent() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.parentRef != nil
}

// GetBuff 获取序列化缓冲区
func (r *PageReference) GetBuff() []byte {
	info := r.pInfo.Load()
	if info == nil {
		return nil
	}
	return info.GetBuff()
}

// SetBuff 设置序列化缓冲区
func (r *PageReference) SetBuff(buff []byte) {
	info := r.pInfo.Load()
	if info == nil {
		return
	}
	info.SetBuff(buff)
}
