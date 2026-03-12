package prototype

import (
	"sync/atomic"
)

// PageReference 页面引用（使用 atomic.Pointer 实现间接寻址）
// 这是 Phase 0.5 的核心原型，验证原子指针性能
type PageReference struct {
	pInfo atomic.Pointer[PageInfo]
}

// NewPageReference 创建新的 PageReference
func NewPageReference() *PageReference {
	ref := &PageReference{}
	// 初始化为空的 PageInfo
	ref.pInfo.Store(&PageInfo{page: nil})
	return ref
}

// NewPageReferenceWithPage 创建带页面的 PageReference
func NewPageReferenceWithPage(page *Page) *PageReference {
	ref := &PageReference{}
	info := NewPageInfo(page)
	ref.pInfo.Store(info)
	return ref
}

// GetPage 获取页面对象（原子加载）
func (r *PageReference) GetPage() *Page {
	info := r.pInfo.Load()
	if info == nil {
		return nil
	}
	return info.GetPage()
}

// GetPageInfo 获取 PageInfo（原子加载）
func (r *PageReference) GetPageInfo() *PageInfo {
	return r.pInfo.Load()
}

// SetPage 设置页面对象（原子存储）
func (r *PageReference) SetPage(page *Page) {
	info := NewPageInfo(page)
	r.pInfo.Store(info)
}

// ReplacePage 替换 PageInfo（CAS 操作，确保并发安全）
// 返回 true 表示替换成功，false 表示 pInfo 已被其他 goroutine 修改
func (r *PageReference) ReplacePage(oldInfo, newInfo *PageInfo) bool {
	return r.pInfo.CompareAndSwap(oldInfo, newInfo)
}

// UpdatePage 更新页面对象（使用 CAS 确保原子性）
func (r *PageReference) UpdatePage(newPage *Page) bool {
	for {
		// 1. 加载当前 PageInfo
		oldInfo := r.pInfo.Load()

		// 2. 创建新的 PageInfo（Copy-on-Write）
		newInfo := &PageInfo{
			page:  newPage,
			pos:   oldInfo.pos,
			state: 1, // 标记为脏页
		}

		// 3. CAS 更新
		if r.pInfo.CompareAndSwap(oldInfo, newInfo) {
			return true
		}
		// CAS 失败，重试（说明有其他 goroutine 同时修改）
	}
}

// MarkDirty 标记页面为脏页
func (r *PageReference) MarkDirty() bool {
	for {
		oldInfo := r.pInfo.Load()
		if oldInfo.IsDirty() {
			return true // 已经是脏页
		}

		newInfo := &PageInfo{
			page:  oldInfo.page,
			pos:   oldInfo.pos,
			state: 1, // 标记为脏页
		}

		if r.pInfo.CompareAndSwap(oldInfo, newInfo) {
			return true
		}
	}
}
