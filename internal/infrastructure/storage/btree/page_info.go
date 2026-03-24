package btree

import (
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree/offheap"
)

const cacheLineSize = 64

// 克隆状态常量（延迟深拷贝优化）
const (
	CloneStatusShared  = 0 // 共享原始 Page（未克隆）
	CloneStatusShallow = 1 // 浅克隆（PageInfo 独立，Page 共享）
	CloneStatusDeep    = 2 // 深克隆（PageInfo 和 Page 都独立）
)

// encodeNodeRef 将 NodeRef 编码为 uint64
// 格式：[pageID:32 | isLeaf:1 | _pad:31]
func encodeNodeRef(pageID uint32, isLeaf bool) uint64 {
	ref := uint64(pageID)
	if isLeaf {
		ref |= 0x100000000 // 设置 bit 32
	}
	return ref
}

// decodeNodeRef 将 uint64 解码为 NodeRef
func decodeNodeRef(encoded uint64) (pageID uint32, isLeaf bool) {
	pageID = uint32(encoded & 0xFFFFFFFF)
	isLeaf = (encoded & 0x100000000) != 0
	return
}

// PageInfo 页面信息（Off-Heap 版本）
//
// **重大变更**：不再存储 Go 堆页面指针，而是存储 Off-Heap NodeRef
// 内存布局保持不变，只是 page any 替换为 nodeRef atomic.Uint64
//
// 内存布局（3 个 cache lines，192 bytes）：
// ┌─────────────────────────────────────────────────────────────────┐
// │ Cache Line 1 (64 bytes) - 热数据（高并发访问）                    │
// │ pos(8) │ nodeRef(8) │ pageLock(8) │ lastTime(8) │ hits(8) │ pad(24)│
// ├─────────────────────────────────────────────────────────────────┤
// │ Cache Line 2 (64 bytes) - 温数据（序列化缓冲区）                  │
// │ padding(64)                                                     │
// ├─────────────────────────────────────────────────────────────────┤
// │ Cache Line 3 (64 bytes) - 冷数据（元数据，低频写入）               │
// │ parentRef(8) │ isDirty(1) │ isSplitted(1) │ metaVersion(4) │ pageSize(4) │ pad(46)│
// └─────────────────────────────────────────────────────────────────┘
type PageInfo struct {
	// Cache Line 1 (64 bytes) - 热数据（高并发访问）
	pos      atomic.Int64  // 8 bytes  - 在 Chunk 中的位置（0=未写入）
	nodeRef  atomic.Uint64 // 8 bytes  - Off-Heap NodeRef（压缩为 uint64）
	//   格式：[pageID:32 | isLeaf:1 | _pad:31]
	// 性能优化：延迟 PageLock 创建（减少 15.45% 内存分配）
	pageLock atomic.Value  // 8 bytes  - *PageLock（懒加载）
	lastTime atomic.Int64  // 8 bytes  - LRU 时间戳（纳秒）并发安全
	hits     atomic.Int64  // 8 bytes  - 访问计数 并发安全
	_        [24]byte      // padding to 64 bytes

	// Cache Line 2 (64 bytes) - 不使用
	_ [64]byte

	// Cache Line 3 (64 bytes) - 冷数据（元数据，低频写入）
	parentRef   atomic.Value  // 8 bytes - 父 PageRef（使用 atomic.Value）
	flags       atomic.Uint32 // 4 bytes  - 并发安全标志位: bit0=isDirty, bit1=isSplitted
	metaVersion int32         // 4 bytes  - 元数据版本
	pageSize    int32         // 4 bytes  - 页面实际大小（固定 4KB）
	// 克隆状态标记（放在最后，避免对齐问题）
	cloneStatus atomic.Uint32 // 4 bytes
	_           [56]byte      // padding to 64 bytes
}

// NewPageInfo 创建新的 PageInfo
func NewPageInfo() *PageInfo {
	info := &PageInfo{
		// 性能优化：pageLock 延迟创建，减少内存分配
		// 纯内存模式下不需要锁，仅在需要时才创建
		pageLock: atomic.Value{},
		// parentRef 使用 atomic.Value，不需要显式初始化为 nil
		metaVersion: 0,
		pageSize:    offheap.PageSize,
	}
	info.SetPos(0) // 使用 SetPos() 方法避免 noCopy 违规
	info.lastTime.Store(time.Now().UnixNano())
	info.hits.Store(0)
	info.flags.Store(0)                    // 初始化所有标志位为 0
	info.parentRef.Store((*PageRef)(nil))  // 显式初始化为 nil
	info.cloneStatus.Store(0)
	info.nodeRef.Store(0)                  // 初始化为 0（无效）
	return info
}

// GetNodeRef 获取 Off-Heap 节点引用
func (info *PageInfo) GetNodeRef() offheap.NodeRef {
	encoded := info.nodeRef.Load()
	pageID, isLeaf := decodeNodeRef(encoded)
	return offheap.NewNodeRef(pageID, isLeaf)
}

// SetNodeRef 设置 Off-Heap 节点引用
func (info *PageInfo) SetNodeRef(ref offheap.NodeRef) {
	info.nodeRef.Store(encodeNodeRef(ref.GetPageID(), ref.IsLeaf()))
}

// GetPageID 获取页面 ID
func (info *PageInfo) GetPageID() uint64 {
	ref := info.GetNodeRef()
	return uint64(ref.GetPageID())
}

// IsLeaf 检查是否为叶子节点
func (info *PageInfo) IsLeaf() bool {
	ref := info.GetNodeRef()
	return ref.IsLeaf()
}

// GetPageVersion 获取页面版本号
// Off-Heap 模式：需要通过 OffHeapAdapter 获取
func (info *PageInfo) GetPageVersion() uint64 {
	// TODO: 需要通过 PageAccessor 获取版本号
	// 暂时返回 0，实际实现需要访问 PageManager
	return 0
}

// GetPage 兼容方法（已废弃，返回 nil）
// Off-Heap 模式下不再使用 Go 堆页面
func (info *PageInfo) GetPage() any {
	return nil // Off-Heap 模式下不使用 Go 堆
}

// SetPage 兼容方法（已废弃）
// Off-Heap 模式下忽略此调用
func (info *PageInfo) SetPage(page any) {
	// Off-Heap 模式下忽略此调用
}

// GetLeafPage 兼容方法（已废弃，返回 nil）
// Off-Heap 模式下不再使用 Go 堆页面
func (info *PageInfo) GetLeafPage() *LeafPage {
	return nil // Off-Heap 模式下不使用 Go 堆
}

// GetInternalPage 兼容方法（已废弃，返回 nil）
// Off-Heap 模式下不再使用 Go 堆页面
func (info *PageInfo) GetInternalPage() *InternalPage {
	return nil // Off-Heap 模式下不使用 Go 堆
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
// Off-Heap 模式：浅拷贝 NodeRef（共享 Off-Heap 页面）
func (info *PageInfo) Clone() *PageInfo {
	newInfo := &PageInfo{
		pageLock:    atomic.Value{},
		metaVersion: info.metaVersion,
		pageSize:    info.pageSize,
	}
	newInfo.parentRef.Store(info.parentRef.Load())
	newInfo.SetPos(info.GetPos())
	newInfo.lastTime.Store(info.lastTime.Load())
	newInfo.hits.Store(info.hits.Load())
	newInfo.flags.Store(info.flags.Load())
	newInfo.cloneStatus.Store(info.cloneStatus.Load())

	// 复制 parentRef（如果存在）
	if parent := info.GetParentRef(); parent != nil {
		newInfo.SetParentRef(parent)
	}

	// 复制 NodeRef（共享 Off-Heap 页面）
	newInfo.nodeRef.Store(info.nodeRef.Load())

	return newInfo
}

// CloneShallow 浅拷贝（延迟深拷贝优化）
// Off-Heap 模式：与 Clone() 相同，共享 NodeRef
func (info *PageInfo) CloneShallow() *PageInfo {
	newInfo := &PageInfo{
		pageLock:    atomic.Value{},
		metaVersion: info.metaVersion,
		pageSize:    info.pageSize,
	}
	newInfo.parentRef.Store(info.parentRef.Load())
	newInfo.SetPos(info.GetPos())
	newInfo.lastTime.Store(info.lastTime.Load())
	newInfo.hits.Store(info.hits.Load())
	newInfo.flags.Store(info.flags.Load())

	// 复制 NodeRef（共享 Off-Heap 页面）
	newInfo.nodeRef.Store(info.nodeRef.Load())

	// 标记为浅克隆状态
	newInfo.cloneStatus.Store(CloneStatusShallow)

	return newInfo
}

// CloneDeep 深拷贝（延迟深拷贝优化）
// Off-Heap 模式：与 Clone() 相同，共享 NodeRef
// 实际深拷贝由 OffHeapAdapter.CloneOffHeapPage() 处理
func (info *PageInfo) CloneDeep() *PageInfo {
	// Off-Heap 模式：深拷贝与浅拷贝相同
	// 实际深拷贝由 OffHeapAdapter 处理
	return info.Clone()
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

// IsPageLoaded 检查页面是否已加载（NodeRef 有效）
func (info *PageInfo) IsPageLoaded() bool {
	ref := info.GetNodeRef()
	return ref.IsValid()
}

// GetPageType 获取页面类型（"leaf" 或 "internal"）
func (info *PageInfo) GetPageType() string {
	if !info.IsPageLoaded() {
		return "nil"
	}

	if info.IsLeaf() {
		return "leaf"
	}
	return "internal"
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
