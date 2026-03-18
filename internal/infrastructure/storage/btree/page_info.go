package btree

import (
	"sync/atomic"
	"time"
	"unsafe"
)

const cacheLineSize = 64

// 克隆状态常量（延迟深拷贝优化）
const (
	CloneStatusShared  = 0 // 共享原始 Page（未克隆）
	CloneStatusShallow = 1 // 浅克隆（PageInfo 独立，Page 共享）
	CloneStatusDeep    = 2 // 深克隆（PageInfo 和 Page 都独立）
)

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
	pos  atomic.Int64 // 8 bytes  - 在 Chunk 中的位置（0=未写入）使用原子操作
	page any          // 8 bytes  - 页面对象（*LeafPage 或 *InternalPage）
	// 性能优化：延迟 PageLock 创建（减少 15.45% 内存分配）
	// 纯内存模式下不需要锁，仅在持久化模式的分裂/合并时使用
	pageLock atomic.Value // 8 bytes  - *PageLock（懒加载）
	lastTime atomic.Int64 // 8 bytes  - LRU 时间戳（纳秒）并发安全
	hits     atomic.Int64 // 8 bytes  - 访问计数 并发安全
	_        [24]byte     // padding to 64 bytes

	// Cache Line 2 (64 bytes) - 优化：移除 buff 字段，使用 ChunkManager.pagePool 管理
	// 原 buff []byte 字段已移除，减少1个指针（25%↓），序列化时使用 ChunkManager.AcquirePageBuffer()
	_ [0]byte // 占位，保持对齐

	// Cache Line 3 (64 bytes) - 冷数据（元数据，低频写入）
	parentRef   atomic.Value  // 8 bytes - 优化：使用 atomic.Value 存储 *PageRef，移除 defer 开销
	flags       atomic.Uint32 // 4 bytes  - 并发安全标志位: bit0=isDirty, bit1=isSplitted
	metaVersion int32         // 4 bytes  - 元数据版本
	pageSize    int32         // 4 bytes  - 页面实际大小（固定 4KB）
	// 克隆状态标记（放在最后，避免对齐问题）
	// 0=共享原始 Page, 1=浅克隆（PageInfo 独立，Page 共享）, 2=深克隆（PageInfo 和 Page 都独立）
	cloneStatus atomic.Uint32 // 4 bytes
	_           [56]byte      // padding to 64 bytes
}

// NewPageInfo 创建新的 PageInfo
func NewPageInfo() *PageInfo {
	info := &PageInfo{
		page: nil,
		// 性能优化：pageLock 延迟创建，减少内存分配
		// 纯内存模式下不需要锁，仅在需要时才创建
		pageLock: atomic.Value{},
		// parentRef 使用 atomic.Value，不需要显式初始化为 nil
		metaVersion: 0,
		pageSize:    PageSize,
	}
	info.SetPos(0) // 使用 SetPos() 方法避免 noCopy 违规
	info.lastTime.Store(time.Now().UnixNano())
	info.hits.Store(0)
	info.flags.Store(0)                   // 初始化所有标志位为 0
	info.parentRef.Store((*PageRef)(nil)) // 显式初始化为 nil
	info.cloneStatus.Store(0)
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

// GetLock 获取轻量级锁（懒加载优化）
// 性能优化：仅在首次访问时创建 PageLock，减少内存分配
// 纯内存模式下不会调用此方法，因此不会创建不必要的锁
func (info *PageInfo) GetLock() *PageLock {
	// 快速路径：已经初始化
	if lock := info.pageLock.Load(); lock != nil {
		return lock.(*PageLock)
	}
	// 慢速路径：初始化 PageLock
	newLock := NewPageLock()
	if info.pageLock.CompareAndSwap(nil, newLock) {
		// 成功设置，返回新锁
		return newLock
	}
	// 其他 goroutine 已经设置，返回已存在的锁
	return info.pageLock.Load().(*PageLock)
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
// 深拷贝 Page 对象，避免并发修改问题
// 当多个 goroutine 并发修改时，每个 goroutine 需要独立的 Page 副本
func (info *PageInfo) Clone() *PageInfo {
	// 创建新的 PageInfo，复制所有字段
	newInfo := &PageInfo{
		pageLock:    atomic.Value{}, // 性能优化：延迟创建 PageLock
		metaVersion: info.metaVersion,
		pageSize:    info.pageSize,
	}
	// 优化：使用 atomic.Value 复制 parentRef（无锁）
	newInfo.parentRef.Store(info.parentRef.Load())

	// 修复：使用 SetPos() 方法设置 atomic.Int64，避免直接复制
	newInfo.SetPos(info.GetPos())

	// 复制原子字段（并发安全）
	newInfo.lastTime.Store(info.lastTime.Load())
	newInfo.hits.Store(info.hits.Load())

	// 复制标志位（并发安全）
	newInfo.flags.Store(info.flags.Load())

	// 深拷贝 Page 对象，避免并发修改共享 Page
	if info.IsPageLoaded() && info.page != nil {
		switch p := info.page.(type) {
		case *LeafPage:
			newInfo.page = p.CloneWithDelta() // 深拷贝 LeafPage
		case *InternalPage:
			newInfo.page = p.CloneWithDelta() // 深拷贝 InternalPage
		default:
			// 未知类型，保留原引用（不应该发生）
			newInfo.page = info.page
		}
	}

	return newInfo
}

// CloneShallow 浅拷贝（延迟深拷贝优化）
// 只拷贝 PageInfo 元数据，不拷贝 Page 对象（共享引用）
//
// 使用场景：
// - CAS 前的路径拷贝，避免大量无效深拷贝
// - 只读访问场景，不需要独立 Page 副本
//
// 并发安全性：
// - 浅拷贝状态下的 Page 必须只读
// - 如果需要修改，必须先转换为深拷贝
func (info *PageInfo) CloneShallow() *PageInfo {
	newInfo := &PageInfo{
		pageLock:    atomic.Value{}, // 性能优化：延迟创建 PageLock
		metaVersion: info.metaVersion,
		pageSize:    info.pageSize,
	}
	// 优化：使用 atomic.Value 复制 parentRef（无锁）
	newInfo.parentRef.Store(info.parentRef.Load())

	newInfo.SetPos(info.GetPos())
	newInfo.lastTime.Store(info.lastTime.Load())
	newInfo.hits.Store(info.hits.Load())
	newInfo.flags.Store(info.flags.Load())

	// 关键：共享 Page 对象，不进行深拷贝
	newInfo.page = info.page

	// 标记为浅克隆状态
	newInfo.cloneStatus.Store(CloneStatusShallow)

	return newInfo
}

// CloneDeep 深拷贝（延迟深拷贝优化）
// 拷贝 PageInfo 元数据和 Page 对象，完全独立
//
// 使用场景：
// - CAS 成功后的最终深拷贝
// - 需要修改 Page 的场景
//
// 实现逻辑：
// - 如果当前是浅克隆状态，则执行深拷贝
// - 如果当前是深克隆状态，直接返回
func (info *PageInfo) CloneDeep() *PageInfo {
	// 如果已经是深克隆，直接返回
	if info.cloneStatus.Load() == CloneStatusDeep {
		return info
	}

	// 如果当前是浅克隆或共享状态，执行深拷贝
	newInfo := info.CloneShallow()

	// 深拷贝 Page 对象
	if info.IsPageLoaded() && info.page != nil {
		switch p := info.page.(type) {
		case *LeafPage:
			// 策略：使用 CloneDeep 进行深拷贝
			newInfo.page = p.CloneDeep()
		case *InternalPage:
			newInfo.page = p.CloneDeep() // 深拷贝 InternalPage
		default:
			// 未知类型，保留共享引用（不应该发生）
		}
	}

	// 标记为深克隆状态
	newInfo.cloneStatus.Store(CloneStatusDeep)

	return newInfo
}

// GetCloneStatus 获取克隆状态（延迟深拷贝优化）
// 返回值：0=共享, 1=浅克隆, 2=深克隆
func (info *PageInfo) GetCloneStatus() uint32 {
	return info.cloneStatus.Load()
}

// IsShallowClone 判断是否为浅克隆状态（延迟深拷贝优化）
func (info *PageInfo) IsShallowClone() bool {
	return info.cloneStatus.Load() == CloneStatusShallow
}

// IsDeepClone 判断是否为深克隆状态（延迟深拷贝优化）
func (info *PageInfo) IsDeepClone() bool {
	return info.cloneStatus.Load() == CloneStatusDeep
}

// 优化：移除 GetBuff/SetBuff 方法，序列化缓冲区现在由 ChunkManager.pagePool 管理

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

// GetParentRef 获取父节点引用（无锁，atomic.Value）
// 优化：移除 defer，减少 tryDeferToSpanScan 开销
func (info *PageInfo) GetParentRef() *PageRef {
	return info.parentRef.Load().(*PageRef)
}

// SetParentRef 设置父节点引用（无锁，atomic.Value）
// 优化：移除 defer，减少 tryDeferToSpanScan 开销
func (info *PageInfo) SetParentRef(ref *PageRef) {
	info.parentRef.Store(ref)
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
	offset3 := unsafe.Offsetof(info.flags)

	// pos 应该在 cache line 边界（0 的倍数）
	if offset1%cacheLineSize != 0 {
		println("Warning: PageInfo.pos not aligned to cache line")
	}

	// 移除 buff 对齐检查（字段已移除）

	// flags 应该在独立的 cache line
	if (offset3-offset1)%cacheLineSize != 0 {
		println("Warning: PageInfo.metadata not in separate cache line")
	}
}

// SizeOfPageInfo 获取 PageInfo 大小
func SizeofPageInfo() int {
	return int(unsafe.Sizeof(PageInfo{}))
}
