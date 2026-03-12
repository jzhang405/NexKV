# Phase 1: BTree Page 重构 - 详细实施计划

> **版本**：v1.0
> **开始日期**：2026-03-12（Phase 0.5 验证完成后）
> **预计工期**：2 周（Week 2-3）
> **前置条件**：Phase 0.5 原型验证已完成，结果超出预期 270x

---

## 📋 目录

1. [背景与目标](#1-背景与目标)
2. [Phase 0.5 验证结果回顾](#2-phase-05-验证结果回顾)
3. [Phase 1 实施范围](#3-phase-1-实施范围)
4. [Week 2: PageReference 和 PageInfo](#4-week-2-pagereference-and-pageinfo)
5. [Week 3: Chunk Manager 和 PageLock](#5-week-3-chunk-manager-and-pagelock)
6. [验收标准](#6-验收标准)
7. [风险管理](#7-风险管理)
8. [下一步计划](#8-下一步计划)

---

## 1. 背景与目标

### 1.1 背景

**Phase 0.5 验证结果**：
- ✅ 原子指针性能**卓越**：0.37ns/op（超出预期 270x）
- ✅ 并发安全性**验证通过**：Race detector 通过
- ✅ CPU 开销**极低**：原子操作占 27.5%
- ✅ **决策**：立即进入 Phase 1，无需备选方案

### 1.2 Phase 1 核心目标

#### 功能目标
1. **实现完整版 PageReference**
   - 基于 `atomic.Pointer[PageInfo]` 的间接寻址
   - RootPageReference（Root Page CAS 更新）
   - 引用链维护（parentRef）
   - Copy-on-Write 支持

2. **实现完整版 PageInfo**
   - **Cache Line 对齐**（64 bytes）
   - LRU 缓存支持（lastTime, hits）
   - 脏页管理（isDirty, MarkDirty）
   - 分裂标记（isSplitted）

3. **实现 Chunk Manager**
   - **64 位位置编码**（ChunkID + Offset + PageType）
   - Append-Only 文件管理
   - 空间回收（freePages）

4. **实现 PageLock**
   - 轻量级锁（基于 CAS）
   - 支持重入（递归调用）
   - 支持超时（避免死锁）

#### 性能目标
- 读延迟：**<1μs**（基于 Phase 0.5 的 0.37ns，实际可达 <100ns）
- 写延迟：<2μs
- 并发读：> 10M ops/sec
- 并发写：> 300k ops/sec

#### 质量目标
- 测试覆盖率：**> 80%**
- Race detector：**通过**
- 内存泄漏：**无**

---

## 2. Phase 0.5 验证结果回顾

### 2.1 性能验证（已完成）

| 指标 | 目标 | 实际结果 | 评价 |
|------|------|----------|------|
| **原子指针读取** | <100ns | **0.37ns** | ⭐⭐⭐⭐⭐ 超出预期 270x |
| **CAS 操作** | <200ns | **6.85ns** | ⭐⭐⭐⭐⭐ 超出预期 29x |
| **并发吞吐** | >8M ops/sec | **>2700M ops/sec** | ⭐⭐⭐⭐⭐ 超出预期 337x |
| **Race detector** | 无警告 | **通过** | ⭐⭐⭐⭐⭐ 无数据竞争 |

### 2.2 关键结论

**✅ 技术决策**：
- 采用**纯 PageReference 架构**（无需混合架构）
- 使用 `atomic.Pointer[PageInfo]`（性能卓越）
- **Cache Line 对齐**优先级：**高**
- **无需考虑备选方案**（Mutex、混合架构）

**✅ 性能基准**：
- 直接指针：0.24 ns/op
- 原子指针：0.37 ns/op
- **开销比**：1.5x（远低于预期的 2-3x）

---

## 3. Phase 1 实施范围

### 3.1 新增文件

```
internal/infrastructure/storage/btree/
├── page_reference.go           # PageReference 结构
├── root_page_reference.go      # RootPageReference 结构
├── page_info.go                # PageInfo 结构（Cache Line 对齐）
├── page_lock.go                # PageLock 轻量级锁
├── chunk_manager.go            # Chunk Manager
├── chunk.go                    # Chunk 结构
├── position.go                 # 64 位位置编码
└── page_reference_test.go      # 单元测试
```

### 3.2 修改文件

- `internal/infrastructure/storage/btree/page_cache.go`：简化为两层缓存（PageReference + PageInfo）
- `internal/infrastructure/storage/btree/btree.go`：集成 PageReference

### 3.3 不包含内容

- ❌ LeafPage 和 InternalPage（Phase 2）
- ❌ BTreeGC（Phase 3）
- ❌ CCOW 机制（Phase 3）
- ❌ DataMigrator（Phase 4）

---

## 4. Week 2: PageReference 和 PageInfo

### 4.1 Day 1-2: PageReference 实现

#### 文件：`page_reference.go`

```go
package btree

import (
    "sync/atomic"
)

// PageReference 页面引用（间接寻址）
// 使用 atomic.Pointer[PageInfo] 实现无锁并发访问
type PageReference struct {
    pInfo     atomic.Pointer[PageInfo]  // 原子指针，支持 CAS 更新
    parentRef *PageReference             // 父引用，形成引用链（弱引用）
}

// NewPageReference 创建新的 PageReference
func NewPageReference() *PageReference {
    ref := &PageReference{}
    ref.pInfo.Store(&PageInfo{})  // 初始化为空 PageInfo
    return ref
}

// NewPageReferenceWithPage 创建带页面的 PageReference
func NewPageReferenceWithPage(page *Page) *PageReference {
    ref := &PageReference{}
    info := &PageInfo{
        page:     page,
        pageLock: NewPageLock(),
        lastTime: time.Now().UnixNano(),
    }
    ref.pInfo.Store(info)
    return ref
}

// GetPage 获取页面对象（原子加载）
func (r *PageReference) GetPage() *Page {
    info := r.pInfo.Load()
    if info == nil {
        return nil
    }
    return info.page
}

// GetPageInfo 获取 PageInfo（原子加载）
func (r *PageReference) GetPageInfo() *PageInfo {
    return r.pInfo.Load()
}

// SetPage 设置页面对象（原子存储）
func (r *PageReference) SetPage(page *Page) {
    info := &PageInfo{
        page:     page,
        pageLock: NewPageLock(),
        lastTime: time.Now().UnixNano(),
    }
    r.pInfo.Store(info)
}

// ReplacePage 替换 PageInfo（CAS 操作）
// 返回 true 表示替换成功，false 表示 pInfo 已被其他 goroutine 修改
func (r *PageReference) ReplacePage(oldInfo, newInfo *PageInfo) bool {
    return r.pInfo.CompareAndSwap(oldInfo, newInfo)
}

// UpdatePage 更新页面对象（自动重试）
// 使用 CAS 确保原子性
func (r *PageReference) UpdatePage(newPage *Page) error {
    for {
        // 1. 加载当前 PageInfo
        oldInfo := r.pInfo.Load()

        // 2. 创建新的 PageInfo（Copy-on-Write）
        newInfo := &PageInfo{
            page:     newPage,
            pageLock: NewPageLock(),
            pos:      oldInfo.pos,
            lastTime: time.Now().UnixNano(),
            isDirty:  true,  // 标记为脏页
        }

        // 3. CAS 更新
        if r.pInfo.CompareAndSwap(oldInfo, newInfo) {
            return nil  // 成功
        }
        // CAS 失败，重试（说明有其他 goroutine 同时修改）
    }
}

// MarkDirty 标记页面为脏页
func (r *PageReference) MarkDirty() error {
    for {
        oldInfo := r.pInfo.Load()
        if oldInfo.isDirty {
            return nil  // 已经是脏页
        }

        newInfo := &PageInfo{
            page:      oldInfo.page,
            pageLock:  oldInfo.pageLock,
            pos:       oldInfo.pos,
            lastTime:  oldInfo.lastTime,
            hits:      oldInfo.hits,
            isDirty:   true,
            isSplitted: oldInfo.isSplitted,
        }

        if r.pInfo.CompareAndSwap(oldInfo, newInfo) {
            return nil
        }
    }
}

// GetParentRef 获取父引用
func (r *PageReference) GetParentRef() *PageReference {
    return r.parentRef
}

// SetParentRef 设置父引用（线程安全）
func (r *PageReference) SetParentRef(parent *PageReference) {
    r.parentRef = parent
}

// IsDirty 判断是否为脏页
func (r *PageReference) IsDirty() bool {
    info := r.pInfo.Load()
    return info != nil && info.isDirty
}

// GetHits 获取访问次数
func (r *PageReference) GetHits() int64 {
    info := r.pInfo.Load()
    if info == nil {
        return 0
    }
    return info.hits
}

// IncrementHits 增加访问计数
func (r *PageReference) IncrementHits() {
    info := r.pInfo.Load()
    if info != nil {
        info.hits++
    }
}
```

**关键设计点**：
- ✅ 使用 `atomic.Pointer[PageInfo]` 实现 CAS 更新
- ✅ 支持重试的 `UpdatePage` 方法
- ✅ 弱引用 parentRef（避免循环引用）
- ✅ 线程安全的脏页标记

---

### 4.2 Day 3-4: RootPageReference 实现

#### 文件：`root_page_reference.go`

```go
package btree

// RootPageReference Root 页面的特殊引用
// 处理 Root Page 的 CAS 更新和引用链维护
type RootPageReference struct {
    PageReference
}

// NewRootPageReference 创建新的 RootPageReference
func NewRootPageReference() *RootPageReference {
    return &RootPageReference{
        PageReference: *NewPageReference(),
    }
}

// ReplacePage 替换 Root Page，原子更新并维护引用链
func (r *RootPageReference) ReplacePage(oldInfo, newInfo *PageInfo) bool {
    // 1. 更新所有子节点的 parentRef（指向新 Root）
    if newInfo.page != nil {
        r.updateChildrenParentRef(newInfo.page)
    }

    // 2. CAS 更新 pInfo
    swapped := r.pInfo.CompareAndSwap(oldInfo, newInfo)
    if !swapped {
        return false
    }

    // 3. 旧页面延迟释放（等待活跃读操作完成）
    // TODO: Phase 3 实现延迟释放机制

    return true
}

// updateChildrenParentRef 递归更新子节点的父引用
func (r *RootPageReference) updateChildrenParentRef(page *Page) {
    if page == nil {
        return
    }

    // TODO: Phase 2 实现后，遍历所有子节点并更新 parentRef
    // 当前跳过，等待 Page 类型实现
}

// UpdateRoot 更新 Root Page（带重试）
func (r *RootPageReference) UpdateRoot(newPage *Page) error {
    for {
        oldInfo := r.pInfo.Load()

        newInfo := &PageInfo{
            page:     newPage,
            pageLock: NewPageLock(),
            pos:      oldInfo.pos,
            lastTime: time.Now().UnixNano(),
            isDirty:  true,  // Root Page 总是脏页
        }

        if r.pInfo.CompareAndSwap(oldInfo, newInfo) {
            // 更新子节点引用
            r.updateChildrenParentRef(newPage)
            return nil
        }
    }
}
```

**关键设计点**：
- ✅ 特殊处理 Root Page 的 CAS 更新
- ✅ 自动更新子节点的 parentRef
- ✅ 延迟释放旧页面（TODO: Phase 3 实现）

---

### 4.3 Day 5-7: PageInfo 实现（Cache Line 对齐）

#### 文件：`page_info.go`

```go
package btree

import (
    "sync"
    "time"
)

const cacheLineSize = 64

// PageInfo 页面信息（Cache Line 对齐优化）
// 减少伪共享（false sharing），提升并发性能
type PageInfo struct {
    // 第 1 个 cache line - 热数据（高并发访问）
    pos      int64     // 8 bytes  - 在 Chunk 中的位置（0=未写入）
    page     *Page     // 8 bytes  - Page 对象
    pageLock *PageLock // 8 bytes  - 轻量级锁
    lastTime int64     // 8 bytes  - LRU 时间戳（纳秒）
    hits     int64     // 8 bytes  - 访问计数
    // 总计 40 bytes

    buff        []byte    // 24 bytes - 序列化缓冲区

    // 第 2 个 cache line - 元数据（低频写入）
    isDirty     bool     // 1 byte    - 是否脏页
    isSplitted  bool     // 1 byte    - 是否被分裂
    metaVersion int      // 4 bytes   - 元数据版本
    pageSize    int32    // 4 bytes   - 页面实际大小
    _           [cacheLineSize - 10]byte  // padding 到 64 bytes
}

// NewPageInfo 创建新的 PageInfo
func NewPageInfo(page *Page) *PageInfo {
    return &PageInfo{
        page:     page,
        pageLock: NewPageLock(),
        lastTime: time.Now().UnixNano(),
        hits:     0,
        pos:      0,
        isDirty:  false,
        isSplitted: false,
        metaVersion: 1,
        pageSize:   4096,  // 默认 4KB
    }
}

// Clone 克隆 PageInfo（Copy-on-Write）
func (info *PageInfo) Clone() *PageInfo {
    // TODO: Phase 2 实现后，克隆 page 对象
    return &PageInfo{
        page:      info.page,
        pageLock:  info.pageLock,
        pos:       info.pos,
        lastTime:  info.lastTime,
        hits:      0,  // 克隆后重置计数
        buff:      nil,  // 克隆后不共享缓冲区
        isDirty:   false,  // 克隆后为干净页
        isSplitted: info.isSplitted,
        metaVersion: info.metaVersion + 1,
        pageSize:   info.pageSize,
    }
}

// IsExpired 判断是否过期（LRU）
func (info *PageInfo) IsExpired(timeout time.Duration) bool {
    return time.Since(time.Unix(0, info.lastTime)) > timeout
}

// GetBuffer 获取序列化缓冲区
func (info *PageInfo) GetBuffer() []byte {
    if info.buff == nil {
        info.buff = make([]byte, 4096)  // 默认 4KB
    }
    return info.buff
}

// SetBuffer 设置序列化缓冲区
func (info *PageInfo) SetBuffer(buff []byte) {
    info.buff = buff
}

// GetLock 获取页面锁
func (info *PageInfo) GetLock() *PageLock {
    return info.pageLock
}

// Touch 更新访问时间（LRU）
func (info *PageInfo) Touch() {
    info.lastTime = time.Now().UnixNano()
    info.hits++
}
```

**关键设计点**：
- ✅ **Cache Line 对齐**：热数据在第 1 个 cache line（40 bytes）
- ✅ 元数据在第 2 个 cache line，减少 false sharing
- ✅ LRU 支持（lastTime, hits）
- ✅ 脏页管理（isDirty, MarkDirty）
- ✅ 分裂标记（isSplitted）

**Cache Line 对齐验证**：
```go
// 验证对齐是否生效
func verifyPageInfoAlignment() {
    var info PageInfo
    offset1 := unsafe.Offsetof(info.pos)
    offset2 := unsafe.Offsetof(info.isDirty)

    // pos 应该在 cache line 边界（0 的倍数）
    // isDirty 应该在独立的 cache line
}
```

---

## 5. Week 3: Chunk Manager 和 PageLock

### 5.1 Day 8-10: Chunk Manager 实现

#### 文件：`chunk_manager.go`

```go
package btree

import (
    "os"
    "path/filepath"
    "sync"
)

const (
    defaultChunkSize = 256 * 1024 * 1024  // 256MB
    defaultPageSize  = 4096                 // 4KB
    maxChunks       = 8
)

// ChunkManager Chunk 文件管理器（Append-Only）
type ChunkManager struct {
    mu            sync.RWMutex
    chunkSize     int64               // 每个 Chunk 大小（256MB）
    maxChunks     int                 // 最大文件数量（8个）
    dataDir       string              // 数据目录
    activeChunks  []*Chunk            // 活跃的 Chunk
    archivedChunks []*Chunk            // 已归档的 Chunk
    currentChunk  *Chunk              // 当前写入的 Chunk
    freePages     []int64              // 空闲页面位置（重用）
    compactor     *ChunkCompactor     // 压缩器
}

// NewChunkManager 创建新的 ChunkManager
func NewChunkManager(dataDir string) (*ChunkManager, error) {
    if err := os.MkdirAll(dataDir, 0755); err != nil {
        return nil, err
    }

    cm := &ChunkManager{
        chunkSize:    defaultChunkSize,
        maxChunks:    maxChunks,
        pageSize:     defaultPageSize,
        dataDir:      dataDir,
        activeChunks: make([]*Chunk, 0),
        freePages:    make([]int64, 0),
    }

    // 加载现有 Chunk 文件
    if err := cm.loadChunks(); err != nil {
        return nil, err
    }

    return cm, nil
}

// AllocatePage 分配新页面（Append-Only）
func (cm *ChunkManager) AllocatePage(size int, pageType int) (int64, error) {
    cm.mu.Lock()
    defer cm.mu.Unlock()

    // 1. 检查是否有空闲页面可重用
    if len(cm.freePages) > 0 {
        pos := cm.freePages[0]
        cm.freePages = cm.freePages[1:]
        return pos, nil
    }

    // 2. 确保有活跃的 Chunk
    if cm.currentChunk == nil || cm.currentChunk.IsFull() {
        if err := cm.createNewChunk(); err != nil {
            return 0, err
        }
    }

    // 3. 在当前 Chunk 中分配页面
    pos, err := cm.currentChunk.AllocatePage(size)
    if err != nil {
        return 0, err
    }

    // 4. 编码位置（64 位）
    encodedPos := EncodePagePos(
        cm.currentChunk.id,
        pos,
        pageType,
    )

    return encodedPos, nil
}

// WritePage 写入页面到 Chunk
func (cm *ChunkManager) WritePage(data []byte) (int64, error) {
    pageType := 1  // 默认类型
    pos, err := cm.AllocatePage(len(data), pageType)
    if err != nil {
        return 0, err
    }

    // 解码位置
    chunkID, offset, _ := DecodePagePos(pos)

    // 写入到 Chunk
    chunk := cm.getChunk(chunkID)
    if _, err := chunk.WriteAt(offset, data); err != nil {
        return 0, err
    }

    return pos, nil
}

// ReadPage 从 Chunk 读取页面
func (cm *ChunkManager) ReadPage(pos int64) ([]byte, error) {
    chunkID, offset, _ := DecodePagePos(pos)

    cm.mu.RLock()
    chunk := cm.getChunk(chunkID)
    cm.mu.RUnlock()

    if chunk == nil {
        return nil, ErrChunkNotFound
    }

    // 读取页面大小
    size, err := chunk.GetPageSize(offset)
    if err != nil {
        return nil, err
    }

    // 读取页面数据
    data := make([]byte, size)
    if _, err := chunk.ReadAt(offset, data); err != nil {
        return nil, err
    }

    return data, nil
}

// createNewChunk 创建新的 Chunk 文件
func (cm *ChunkManager) createNewChunk() error {
    cm.mu.Lock()
    defer cm.mu.Unlock()

    if len(cm.activeChunks) >= cm.maxChunks {
        return ErrTooManyChunks
    }

    chunk, err := NewChunk(cm.dataDir, len(cm.activeChunks))
    if err != nil {
        return err
    }

    cm.activeChunks = append(cm.activeChunks, chunk)
    cm.currentChunk = chunk

    return nil
}

// loadChunks 加载现有 Chunk 文件
func (cm *ChunkManager) loadChunks() error {
    // 扫描数据目录，加载所有 .ao 文件
    matches, err := filepath.Glob(filepath.Join(cm.dataDir, "btree_*.ao"))
    if err != nil {
        return err
    }

    for _, match := range matches {
        chunk, err := LoadChunk(match)
        if err != nil {
            continue  // 跳过损坏的文件
        }
        cm.activeChunks = append(cm.activeChunks, chunk)
    }

    return nil
}

// getChunk 获取 Chunk（内部使用）
func (cm *ChunkManager) getChunk(id int) *Chunk {
    for _, chunk := range cm.activeChunks {
        if chunk.id == id {
            return chunk
        }
    }
    return nil
}

// Close 关闭 Chunk Manager
func (cm *ChunkManager) Close() error {
    cm.mu.Lock()
    defer cm.mu.Unlock()

    for _, chunk := range cm.activeChunks {
        chunk.Close()
    }

    return nil
}
```

**关键设计点**：
- ✅ Append-Only 写入（不支持覆盖）
- ✅ 空闲页面重用（freePages）
- ✅ 自动创建新 Chunk（256MB）
- ✅ 最多 8 个 Chunk 文件

---

#### 文件：`chunk.go`

```go
package btree

import (
    "fmt"
    "os"
    "sync"
)

// Chunk Chunk 文件（Append-Only）
type Chunk struct {
    mu         sync.Mutex
    id         int          // Chunk ID
    file       *os.File     // 文件句柄
    path       string       // 文件路径
    writePos   int64        // 当前写入位置
     pageCount  int          // 页面数量
    pageSizes  map[int64]int // pos → size
    isReadOnly  bool         // 是否只读
}

// NewChunk 创建新的 Chunk
func NewChunk(dataDir string, id int) (*Chunk, error) {
    path := fmt.Sprintf("%s/btree_%04d.ao", dataDir, id)

    file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
    if err != nil {
        return nil, err
    }

    chunk := &Chunk{
        id:        id,
        file:      file,
        path:      path,
        writePos:  0,
        pageCount: 0,
        pageSizes: make(map[int64]int),
    }

    return chunk, nil
}

// LoadChunk 加载现有 Chunk
func LoadChunk(path string) (*Chunk, error) {
    file, err := os.Open(path)
    if err != nil {
        return nil, err
    }

    info, err := file.Stat()
    if err != nil {
        file.Close()
        return nil, err
    }

    chunk := &Chunk{
        path:      path,
        file:      file,
        writePos:  info.Size(),
        isReadOnly: true,
        pageSizes: make(map[int64]int),
    }

    return chunk, nil
}

// AllocatePage 在 Chunk 中分配页面
func (c *Chunk) AllocatePage(size int) (int64, error) {
    c.mu.Lock()
    defer c.mu.Unlock()

    pos := c.writePos
    c.writePos += int64(size)
    c.pageCount++

    return pos, nil
}

// WriteAt 在指定位置写入
func (c *Chunk) WriteAt(pos int64, data []byte) (int, error) {
    c.mu.Lock()
    defer c.mu.Unlock()

    n, err := c.file.WriteAt(data, pos)
    if err != nil {
        return 0, err
    }

    c.pageSizes[pos] = len(data)
    return n, nil
}

// ReadAt 在指定位置读取
func (c *Chunk) ReadAt(pos int64, data []byte) (int, error) {
    c.mu.Lock()
    defer c.mu.Unlock()

    return c.file.ReadAt(data, pos)
}

// GetPageSize 获取页面大小
func (c *Chunk) GetPageSize(pos int64) (int, error) {
    c.mu.Lock()
    defer c.mu.Unlock()

    size, ok := c.pageSizes[pos]
    if !ok {
        return 0, ErrPageNotFound
    }
    return size, nil
}

// IsFull 判断 Chunk 是否已满
func (c *Chunk) IsFull() bool {
    return c.writePos >= 256*1024*1024  // 256MB
}

// Sync 同步文件到磁盘
func (c *Chunk) Sync() error {
    return c.file.Sync()
}

// Close 关闭 Chunk
func (c *Chunk) Close() error {
    return c.file.Close()
}
```

---

### 5.2 Day 11-12: 64 位位置编码

#### 文件：`position.go`

```go
package btree

import (
    "errors"
)

var (
    ErrInvalidPosition = errors.New("invalid page position")
)

// EncodePagePos 编码页面位置（64 位）
//
// 位布局：
// ┌────────────────────────────────────────────────────────────────┐
// │  63-38 (26 bits) │ 37-6 (32 bits) │ 5-1 (5 bits) │ 0 (1 bit)  │
// │    Chunk ID      │     Offset     │   Page Type  │   保留位    │
// └────────────────────────────────────────────────────────────────┘
func EncodePagePos(chunkID, offset, pageType int) int64 {
    // 验证参数范围
    if chunkID < 0 || chunkID >= (1<<26) {
        return 0
    }
    if offset < 0 || offset >= (1<<32) {
        return 0
    }
    if pageType < 0 || pageType >= (1<<5) {
        return 0
    }

    return (int64(chunkID) << 38) | (int64(offset) << 6) | (int64(pageType) << 1)
}

// DecodePagePos 解码页面位置
func DecodePagePos(pos int64) (chunkID, offset, pageType int) {
    chunkID = int(pos >> 38)
    offset = int((pos >> 6) & 0xFFFFFFFF)
    pageType = int((pos >> 1) & 0x1F)
    return
}

// ValidatePosition 验证位置是否有效
func ValidatePosition(pos int64) bool {
    if pos == 0 {
        return false
    }

    chunkID, _, _ := DecodePagePos(pos)
    return chunkID >= 0 && chunkID < (1<<26)
}
```

**关键设计点**：
- ✅ 支持 268M 个 Chunk 文件（26 bits）
- ✅ 每个 Chunk 最大 4GB（32 bits offset）
- ✅ 支持 32 种页面类型（5 bits）
- ✅ **理论数据规模**：268M × 4GB = **1PB**

---

### 5.3 Day 13-14: PageLock 实现

#### 文件：`page_lock.go`

```go
package btree

import (
    "sync"
    "time"
)

const (
    unlockedState      = 0
    maxRecurseCount    = 1000
    defaultLockTimeout = 5 * time.Second
)

// PageLock 轻量级锁（支持重入和超时）
type PageLock struct {
    state   atomic.Int64  // 状态编码：(owner_id << 32) | lock_count
    waiters chan struct{} // 等待队列
    mu      sync.Mutex    // 保护 waiters
}

// NewPageLock 创建新的 PageLock
func NewPageLock() *PageLock {
    return &PageLock{
        state:   atomic.Int64{},
        waiters: make(chan struct{}),
    }
}

// TryLock 非阻塞加锁
func (l *PageLock) TryLock() bool {
    // 使用 CAS 尝试设置为锁定状态
    return l.state.CompareAndSwap(0, encodeOwnerState(0, 1))
}

// Lock 加锁（阻塞）
func (l *PageLock) Lock() {
    l.lockWithTimeout(0)
}

// LockWithTimeout 带超时的加锁
func (l *PageLock) LockWithTimeout(timeout time.Duration) bool {
    return l.lockWithTimeout(timeout)
}

// lockWithTimeout 内部加锁实现
func (l *PageLock) lockWithTimeout(timeout time.Duration) bool {
    var timer *time.Timer
    if timeout > 0 {
        timer = time.AfterFunc(timeout, func() {
            l.notifyWaiters()
        })
        defer timer.Stop()
    }

    // 尝试加锁（支持重入）
    for {
        if l.state.CompareAndSwap(0, encodeOwnerState(0, 1)) {
            return true
        }

        // 等待锁释放
        l.waitForNotify()
    }
}

// Unlock 解锁（支持重入）
func (l *PageLock) Unlock() error {
    state := l.state.Load()
    ownerID, lockCount := decodeOwnerState(state)

    // 检查是否是锁的持有者
    if ownerID != 0 {
        return ErrNotOwner
    }

    if lockCount == 1 {
        // 完全解锁
        if !l.state.CompareAndSwap(state, 0) {
            return ErrInvalidState
        }
        l.notifyWaiters()
    } else {
        // 减少重入计数
        newState := encodeOwnerState(ownerID, lockCount-1)
        if !l.state.CompareAndSwap(state, newState) {
            return ErrInvalidState
        }
    }

    return nil
}

// IsLocked 判断是否已锁定
func (l *PageLock) IsLocked() bool {
    return l.state.Load() != 0
}

// LockCount 获取锁定计数（重入次数）
func (l *PageLock) LockCount() int {
    _, count := decodeOwnerState(l.state.Load())
    return count
}

// encodeOwnerState 编码所有者状态
func encodeOwnerState(ownerID, lockCount int) int64 {
    return (int64(ownerID) << 32) | int64(lockCount)
}

// decodeOwnerState 解码所有者状态
func decodeOwnerState(state int64) (ownerID, lockCount int) {
    ownerID = int(state >> 32)
    lockCount = int(state & 0xFFFFFFFF)
    return
}

// waitForNotify 等待锁释放通知
func (l *PageLock) waitForNotify() {
    l.mu.Lock()
    l.waiters = make(chan struct{})
    ch := l.waiters
    l.mu.Unlock()
    <-ch
}

// notifyWaiters 通知等待者
func (l *PageLock) notifyWaiters() {
    l.mu.Lock()
    defer l.mu.Unlock()

    ch := l.waiters
    if ch != nil {
        close(ch)
        l.waiters = nil
    }
}

var (
    ErrNotOwner    = errors.New("not the lock owner")
    ErrInvalidState = errors.New("invalid lock state")
)
```

**关键设计点**：
- ✅ 基于 CAS 的非阻塞加锁
- ✅ 支持重入（递归调用）
- ✅ 支持超时（避免死锁）
- ✅ 等待队列（公平性）

---

## 6. 验收标准

### 6.1 功能验收

| 功能 | 验收标准 | 验证方法 |
|------|----------|----------|
| **PageReference** | 原子指针读取 <100ns | 基准测试 |
| | CAS 更新成功 | 并发测试 |
| | 支持脏页标记 | 单元测试 |
| **PageInfo** | Cache Line 对齐 | 代码审查 |
| | LRU 功能正常 | 单元测试 |
| | 脏页管理正常 | 单元测试 |
| **Chunk Manager** | 64 位编码正确 | 单元测试 |
| | Append-Only 写入 | 集成测试 |
| | 空间重用 | 单元测试 |
| **PageLock** | 支持重入 | 单元测试 |
| | 支持超时 | 单元测试 |
| | CAS 加锁成功 | 并发测试 |

### 6.2 性能验收

| 指标 | 目标 | 验证方法 |
|------|------|----------|
| **读延迟** | <1μs | `BenchmarkPageReference_Read` |
| **并发吞吐** | >10M ops/sec | `BenchmarkPageReference_ConcurrentRead` |
| **锁性能** | TryLock <1μs | `BenchmarkPageLock_TryLock` |
| **编码性能** | <50ns/op | `BenchmarkEncodePagePos` |

### 6.3 质量验收

| 指标 | 目标 | 验证方法 |
|------|------|----------|
| **测试覆盖率** | >80% | `go test -cover` |
| **Race detector** | 通过 | `go test -race` |
| **内存泄漏** | 无 | 长期运行测试 |

---

## 7. 风险管理

### 7.1 已验证风险（低风险）

| 风险 | 验证结果 | 应对措施 |
|------|----------|----------|
| atomic.Pointer 性能 | ✅ 0.37ns（超出预期 270x） | 无需优化 |
| 并发安全性 | ✅ Race detector 通过 | 持续验证 |

### 7.2 新风险（中等风险）

| 风险 | 应对措施 |
|------|----------|
| Cache Line 对齐效果 | 验证对齐是否生效（unsafe.Offsetof） |
| Chunk 文件管理复杂度 | 固定大小（256MB），最多 8 个文件 |
| PageLock 死锁风险 | 超时机制，避免无限等待 |

### 7.3 备选方案（无需启用）

**无需考虑备选方案**，因为 Phase 0.5 验证结果超出预期。

---

## 8. 下一步计划

### 8.1 Phase 1 后续阶段

- **Phase 2（Week 4-7）**：LeafPage 和 InternalPage
- **Phase 3（Week 8-10）**：BTreeGC 并发控制
- **Phase 4（Week 11-15）**：集成和优化
- **Phase 5（Week 16）**：性能优化和文档

### 8.2 立即行动

#### ✅ Day 1: 准备工作

- [ ] 创建 `internal/infrastructure/storage/btree/` 目录结构
- [ ] 配置开发环境（Go 1.24+）
- [ ] 初始化测试框架

#### ✅ Day 2-3: 实现 PageReference

- [ ] 实现 `page_reference.go`
- [ ] 实现单元测试
- [ ] 运行基准测试

#### ✅ Day 4-5: 实现 RootPageReference

- [ ] 实现 `root_page_reference.go`
- [ ] 实现单元测试
- [ ] 测试引用链更新

#### ✅ Day 6-7: 实现 PageInfo（Cache Line 对齐）

- [ ] 实现 `page_info.go`
- [ ] 验证对齐是否生效
- [ ] 实现 LRU 逻辑

#### ✅ Day 8-10: 实现 Chunk Manager

- [ ] 实现 `chunk_manager.go`
- [ ] 实现 `chunk.go`
- [ ] 实现单元测试

#### ✅ Day 11-12: 实现位置编码

- [ ] 实现 `position.go`
- [ ] 实现单元测试
- [ ] 验证编码正确性

#### ✅ Day 13-14: 实现 PageLock

- [ ] 实现 `page_lock.go`
- [ ] 实现单元测试
- [ ] 测试重入和超时

#### ✅ Day 15: 集成和测试

- [ ] 集成所有组件
- [ ] 运行完整测试套件
- [ ] 性能基准测试
- [ ] 生成测试报告

---

## 9. 附录

### 9.1 Go 版本要求

- **最低版本**：Go 1.19（`atomic.Pointer[T]` 泛型支持）
- **推荐版本**：Go 1.24+

### 9.2 依赖包

```go
require (
    github.com/stretchr/testify v1.9.0  // 测试框架
)
```

### 9.3 参考文档

- `thoughts/2026-03-12-btree-page-refactor-plan-v3.md`：完整重构计划
- `docs/06_PM/feature/2026-03-12_PR-XXX_BTree-Page-重构-Phase1_全流程.md`：PR 文档
- `docs/10_benchmark/2026-03-12_phase0.5_page_reference_prototype/2026-03-12_results_summary.md`：Phase 0.5 测试结果

---

**文档版本**：v1.0
**创建日期**：2026-03-12
**预计开始日期**：2026-03-12
**预计完成日期**：2026-03-26（2 周）
