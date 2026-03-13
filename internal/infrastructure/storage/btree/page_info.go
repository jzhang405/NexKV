package btree

import (
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

const cacheLineSize = 64

// PageInfo 页面信息（Cache Line 对齐优化）
// 减少伪共享（false sharing），提升并发性能
//
// 内存布局（3 个 cache lines，192 bytes）：
// ┌─────────────────────────────────────────────────────────────────┐
// │ Cache Line 1 (64 bytes) - 热数据（高并发访问）                    │
// │ pos(8) │ page(8) │ pageLock(8) │ lastTime(8) │ hits(8) │ pad(24)│
// ├─────────────────────────────────────────────────────────────────┤
// │ Cache Line 2 (64 bytes) - 温数据（序列化缓冲区）                  │
// │ buff(24) │ padding(40)                                             │
// ├─────────────────────────────────────────────────────────────────┤
// │ Cache Line 3 (64 bytes) - 冷数据（元数据，低频写入）               │
// │ parentRef(8) │ isDirty(1) │ isSplitted(1) │ metaVersion(4) │ pageSize(4) │ pad(46)│
// └─────────────────────────────────────────────────────────────────┘
type PageInfo struct {
	// Cache Line 1 (64 bytes) - 热数据（高并发访问）
	pos      atomic.Int64 // 8 bytes  - 在 Chunk 中的位置（0=未写入）✅ 使用原子操作
	page     any          // 8 bytes  - 页面对象（*LeafPage 或 *InternalPage）
	pageLock *PageLock    // 8 bytes  - 轻量级锁
	lastTime atomic.Int64 // 8 bytes  - LRU 时间戳（纳秒）✅ 并发安全
	hits     atomic.Int64 // 8 bytes  - 访问计数 ✅ 并发安全
	_        [24]byte     // padding to 64 bytes

	// Cache Line 2 (64 bytes) - 温数据（序列化缓冲区）
	buff []byte   // 24 bytes - slice header
	_    [40]byte // padding to 64 bytes

	// Cache Line 3 (64 bytes) - 冷数据（元数据，低频写入）
	parentRefMu sync.RWMutex  // 8 bytes  - ✅ P0-2 修复: 保护 parentRef 的并发访问
	parentRef   *PageRef      // 8 bytes  - 父节点引用
	flags       atomic.Uint32 // 4 bytes  - ✅ 并发安全标志位: bit0=isDirty, bit1=isSplitted
	metaVersion int32         // 4 bytes  - 元数据版本
	pageSize    int32         // 4 bytes  - 页面实际大小（固定 4KB）
	_           [60]byte      // padding to 64 bytes (调整 padding)
}

// NewPageInfo 创建新的 PageInfo
func NewPageInfo() *PageInfo {
	info := &PageInfo{
		page:        nil,
		pageLock:    NewPageLock(),
		parentRef:   nil, // ✅ 初始化父节点引用
		metaVersion: 0,
		pageSize:    PageSize,
	}
	info.SetPos(0) // ✅ 使用 SetPos() 方法避免 noCopy 违规
	info.lastTime.Store(time.Now().UnixNano())
	info.hits.Store(0)
	info.flags.Store(0) // 初始化所有标志位为 0
	return info
}

// GetPage 获取页面对象（返回 any，需要类型断言）
// 实际类型为 *LeafPage 或 *InternalPage
func (info *PageInfo) GetPage() any {
	return info.page
}

// SetPage 设置页面对象
func (info *PageInfo) SetPage(page any) {
	info.page = page
}

// GetLeafPage 获取叶子节点（类型断言）
// 如果不是叶子节点，返回 nil
func (info *PageInfo) GetLeafPage() *LeafPage {
	if leaf, ok := info.page.(*LeafPage); ok {
		return leaf
	}
	return nil
}

// GetInternalPage 获取内部节点（类型断言）
// 如果不是内部节点，返回 nil
func (info *PageInfo) GetInternalPage() *InternalPage {
	if internal, ok := info.page.(*InternalPage); ok {
		return internal
	}
	return nil
}

// GetPos 获取位置信息
func (info *PageInfo) GetPos() int64 {
	return info.pos.Load()
}

// SetPos 设置位置信息（原子操作，线程安全）
func (info *PageInfo) SetPos(pos int64) {
	info.pos.Store(pos)
}

// GetLock 获取轻量级锁
func (info *PageInfo) GetLock() *PageLock {
	return info.pageLock
}

// IsDirty 检查是否为脏页（并发安全）
func (info *PageInfo) IsDirty() bool {
	return info.flags.Load()&0x01 != 0
}

// MarkDirty 标记为脏页（并发安全）
func (info *PageInfo) MarkDirty() {
	info.flags.Or(0x01)
}

// ClearDirty 清除脏页标记（并发安全）
func (info *PageInfo) ClearDirty() {
	info.flags.And(^uint32(0x01))
}

// IsSplitted 检查是否被分裂（并发安全）
func (info *PageInfo) IsSplitted() bool {
	return info.flags.Load()&0x02 != 0
}

// MarkSplitted 标记为已分裂（并发安全）
func (info *PageInfo) MarkSplitted() {
	info.flags.Or(0x02)
}

// Touch 更新访问时间（LRU）
func (info *PageInfo) Touch() {
	info.lastTime.Store(time.Now().UnixNano())
	info.hits.Add(1)
}

// GetHits 获取访问计数
func (info *PageInfo) GetHits() int64 {
	return info.hits.Load()
}

// GetLastTime 获取最后访问时间
func (info *PageInfo) GetLastTime() int64 {
	return info.lastTime.Load()
}

// Clone 复制 PageInfo（Copy-on-Write）
//
// ✅ P0-4 修复: 深拷贝 Page 对象，避免并发修改问题
// 当多个 goroutine 并发修改时，每个 goroutine 需要独立的 Page 副本
func (info *PageInfo) Clone() *PageInfo {
	// 创建新的 PageInfo，复制所有字段
	newInfo := &PageInfo{
		pageLock:    NewPageLock(),       // 创建新锁
		parentRef:   info.GetParentRef(), // ✅ P0-2 修复: 线程安全地复制父节点引用
		metaVersion: info.metaVersion,
		pageSize:    info.pageSize,
	}

	// ✅ 修复：使用 SetPos() 方法设置 atomic.Int64，避免直接复制
	newInfo.SetPos(info.GetPos())

	// ✅ 复制原子字段（并发安全）
	newInfo.lastTime.Store(info.lastTime.Load())
	newInfo.hits.Store(info.hits.Load())

	// ✅ 复制标志位（并发安全）
	newInfo.flags.Store(info.flags.Load())

	// ✅ P0-4 修复: 深拷贝 Page 对象，避免并发修改共享 Page
	if info.IsPageLoaded() && info.page != nil {
		switch p := info.page.(type) {
		case *LeafPage:
			newInfo.page = p.Clone() // 深拷贝 LeafPage
		case *InternalPage:
			newInfo.page = p.Clone() // 深拷贝 InternalPage
		default:
			// 未知类型，保留原引用（不应该发生）
			newInfo.page = info.page
		}
	}

	return newInfo
}

// GetBuff 获取序列化缓冲区
func (info *PageInfo) GetBuff() []byte {
	return info.buff
}

// SetBuff 设置序列化缓冲区
func (info *PageInfo) SetBuff(buff []byte) {
	info.buff = buff
}

// GetMetaVersion 获取元数据版本
func (info *PageInfo) GetMetaVersion() int32 {
	return info.metaVersion
}

// IncrementMetaVersion 递增元数据版本
func (info *PageInfo) IncrementMetaVersion() {
	info.metaVersion++
}

// GetPageSize 获取页面大小
func (info *PageInfo) GetPageSize() int32 {
	return info.pageSize
}

// GetParentRef 获取父节点引用（线程安全）
func (info *PageInfo) GetParentRef() *PageRef {
	info.parentRefMu.RLock()
	defer info.parentRefMu.RUnlock()
	return info.parentRef
}

// SetParentRef 设置父节点引用（线程安全）
func (info *PageInfo) SetParentRef(ref *PageRef) {
	info.parentRefMu.Lock()
	defer info.parentRefMu.Unlock()
	info.parentRef = ref
}

// IsPageLoaded 检查页面是否已加载
func (info *PageInfo) IsPageLoaded() bool {
	return info.page != nil
}

// GetPageType 获取页面类型（"leaf" 或 "internal"）
func (info *PageInfo) GetPageType() string {
	if info.page == nil {
		return "nil"
	}

	switch info.page.(type) {
	case *LeafPage:
		return "leaf"
	case *InternalPage:
		return "internal"
	default:
		return "unknown"
	}
}

// GetPageVersion 获取页面版本号
func (info *PageInfo) GetPageVersion() uint64 {
	if info.page == nil {
		return 0
	}

	switch p := info.page.(type) {
	case *LeafPage:
		return p.GetVersion()
	case *InternalPage:
		return p.GetVersion()
	default:
		return 0
	}
}

// GetPageID 获取页面 ID
func (info *PageInfo) GetPageID() uint64 {
	if info.page == nil {
		return 0
	}

	switch p := info.page.(type) {
	case *LeafPage:
		return uint64(p.GetPageID())
	case *InternalPage:
		return uint64(p.GetPageID())
	default:
		return 0
	}
}

// VerifyAlignment 验证 Cache Line 对齐
func VerifyPageInfoAlignment() {
	var info PageInfo
	offset1 := unsafe.Offsetof(info.pos)
	offset2 := unsafe.Offsetof(info.buff)
	offset3 := unsafe.Offsetof(info.flags)

	// pos 应该在 cache line 边界（0 的倍数）
	if offset1%cacheLineSize != 0 {
		println("Warning: PageInfo.pos not aligned to cache line")
	}

	// buff 应该在独立的 cache line
	if (offset2-offset1)%cacheLineSize != 0 {
		println("Warning: PageInfo.buff not in separate cache line")
	}

	// isDirty 应该在独立的 cache line
	if (offset3-offset2)%cacheLineSize != 0 {
		println("Warning: PageInfo.metadata not in separate cache line")
	}
}

// SizeOfPageInfo 获取 PageInfo 大小
func SizeofPageInfo() int {
	return int(unsafe.Sizeof(PageInfo{}))
}
