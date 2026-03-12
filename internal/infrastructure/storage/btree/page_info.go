package btree

import (
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
// │ isDirty(1) │ isSplitted(1) │ metaVersion(4) │ pageSize(4) │ pad(52)│
// └─────────────────────────────────────────────────────────────────┘
type PageInfo struct {
	// Cache Line 1 (64 bytes) - 热数据（高并发访问）
	pos      int64     // 8 bytes  - 在 Chunk 中的位置（0=未写入）
	page     *Page     // 8 bytes  - Page 对象
	pageLock *PageLock // 8 bytes  - 轻量级锁
	lastTime int64     // 8 bytes  - LRU 时间戳（纳秒）
	hits     int64     // 8 bytes  - 访问计数
	_        [24]byte  // padding to 64 bytes

	// Cache Line 2 (64 bytes) - 温数据（序列化缓冲区）
	buff []byte   // 24 bytes - slice header
	_    [40]byte // padding to 64 bytes

	// Cache Line 3 (64 bytes) - 冷数据（元数据，低频写入）
	isDirty     bool  // 1 byte  - 是否脏页
	isSplitted  bool  // 1 byte  - 是否被分裂
	metaVersion int32 // 4 bytes - 元数据版本
	pageSize    int32 // 4 bytes - 页面实际大小（固定 4KB）
	_           [52]byte // padding to 64 bytes
}

// NewPageInfo 创建新的 PageInfo
func NewPageInfo() *PageInfo {
	return &PageInfo{
		pos:         0,
		page:        nil,
		pageLock:    NewPageLock(),
		lastTime:    time.Now().UnixNano(),
		hits:        0,
		isDirty:     false,
		isSplitted:  false,
		metaVersion: 0,
		pageSize:    PageSize,
	}
}

// GetPage 获取 Page 对象
func (info *PageInfo) GetPage() *Page {
	return info.page
}

// SetPage 设置 Page 对象
func (info *PageInfo) SetPage(page *Page) {
	info.page = page
}

// GetPos 获取位置信息
func (info *PageInfo) GetPos() int64 {
	return info.pos
}

// SetPos 设置位置信息
func (info *PageInfo) SetPos(pos int64) {
	info.pos = pos
}

// GetLock 获取轻量级锁
func (info *PageInfo) GetLock() *PageLock {
	return info.pageLock
}

// IsDirty 检查是否为脏页
func (info *PageInfo) IsDirty() bool {
	return info.isDirty
}

// MarkDirty 标记为脏页
func (info *PageInfo) MarkDirty() {
	info.isDirty = true
}

// ClearDirty 清除脏页标记
func (info *PageInfo) ClearDirty() {
	info.isDirty = false
}

// IsSplitted 检查是否被分裂
func (info *PageInfo) IsSplitted() bool {
	return info.isSplitted
}

// MarkSplitted 标记为已分裂
func (info *PageInfo) MarkSplitted() {
	info.isSplitted = true
}

// Touch 更新访问时间（LRU）
func (info *PageInfo) Touch() {
	info.lastTime = time.Now().UnixNano()
	info.hits++
}

// GetHits 获取访问计数
func (info *PageInfo) GetHits() int64 {
	return info.hits
}

// GetLastTime 获取最后访问时间
func (info *PageInfo) GetLastTime() int64 {
	return info.lastTime
}

// Clone 复制 PageInfo（Copy-on-Write）
func (info *PageInfo) Clone() *PageInfo {
	// 创建新的 PageInfo，复制所有字段
	newInfo := &PageInfo{
		pos:         info.pos,
		page:        info.page, // 浅拷贝 Page 指针
		pageLock:    NewPageLock(), // 创建新锁
		lastTime:    info.lastTime,
		hits:        info.hits,
		isDirty:     info.isDirty,
		isSplitted:  info.isSplitted,
		metaVersion: info.metaVersion,
		pageSize:    info.pageSize,
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

// VerifyAlignment 验证 Cache Line 对齐
func VerifyPageInfoAlignment() {
	var info PageInfo
	offset1 := unsafe.Offsetof(info.pos)
	offset2 := unsafe.Offsetof(info.buff)
	offset3 := unsafe.Offsetof(info.isDirty)

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
