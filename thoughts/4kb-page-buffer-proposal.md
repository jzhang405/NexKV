# 4KB Page Buffer 架构设计提案

> **目标**：为 NexKV 引入类似 Lealone 的 2 级页面架构，优化内存复制开销

## 1. 背景与问题

### 1.1 当前架构

NexKV LeafPage 当前结构：
```go
type LeafPage struct {
    pageID   model.PageID
    version  uint64
    keys     [][]byte     // 解析后的键数组
    values   [][]byte     // 解析后的值数组
    pageLock *PageLock
    cowDelta *COWDeltaRef // Delta Chain 优化
}
```

**问题**：
- 每次 Clone 都需要复制 `keys` 和 `values` 切片（即使有 Delta Chain）
- Delta Chain 模式下仍有 ~11% 的直接内存分配（pprof 数据）
- `materialize()` 物化时分配 7.62GB（26.43% 的总内存分配）

### 1.2 Lealone 的 3 级架构

```java
public class PageInfo {
    // 级别1：解析了的
    public Page page;      // 包含 keys/values 数组

    // 级别2：未解析的
    public ByteBuffer buff;  // 4KB 原始数据

    // 级别3：文件的
    public long pos;       // 文件位置（持久化用）
}
```

**优势**：
- `buff` 在 copy 时只复制引用（4KB）
- GC 时可以释放 `page`，只保留 `buff`（节省内存）
- 读取时延迟解析（只在需要时才解析）

### 1.3 性能数据对比

| 指标 | NexKV 当前 | Lealone | 差距 |
|------|-----------|---------|------|
| 8 线程吞吐 | 1.65M ops/sec | 3.68M ops/sec | 2.23x |
| 内存分配 (materialize) | 7.62GB | ~1GB (buff复制) | ~7x |

---

## 2. 设计方案

### 2.1 目标架构（2 级，暂不涉及持久化）

```go
type PageInfo struct {
    // 级别1：解析了的 Page 对象
    page *Page  // 包含 keys/values 数组

    // 级别2：未解析的 4KB Buffer
    buff []byte  // 4KB 原始数据（由 PageManager 管理）

    // 其他字段保持不变
    pos     int64
    pageLock *PageLock
    // ...
}
```

**核心思想**：
- `buff` 由 PageManager 统一管理（mmap + 空闲页管理）
- Clone 时共享 `buff` 引用（只复制 8 字节指针）
- 需要修改时才解析 `buff` → `page.keys/values`
- GC 优先回收 `page`，保留 `buff`

### 2.2 PageManager 设计

```go
package btree

import (
    "sync"
    "unsafe"
)

// PageSize 系统页大小（Linux 默认 4KB）
const PageSize = 4096

// PageManager 4KB 页面管理器
type PageManager struct {
    data      []byte        // mmap 映射的大块内存
    pageSize  int           // 页面大小（4096）
    total     int           // 总页数
    freePages []int         // 空闲页编号（栈结构）
    mu        sync.Mutex    // 保护 freePages
}

// NewPageManager 创建 PageManager
func NewPageManager(mapSize int) (*PageManager, error) {
    // mapSize 必须是 PageSize 的整数倍
    if mapSize%PageSize != 0 {
        return nil, fmt.Errorf("mapSize must be multiple of PageSize")
    }

    // 使用匿名 mmap 映射内存
    data, err := unix.Mmap(
        -1,                          // 匿名映射
        0,
        mapSize,
        unix.PROT_READ|unix.PROT_WRITE,
        unix.MAP_ANON|unix.MAP_PRIVATE,
    )
    if err != nil {
        return nil, err
    }

    total := mapSize / PageSize
    freePages := make([]int, 0, total)

    // 初始所有页都空闲
    for i := 0; i < total; i++ {
        freePages = append(freePages, i)
    }

    return &PageManager{
        data:      data,
        pageSize:  PageSize,
        total:     total,
        freePages: freePages,
    }, nil
}

// Alloc 分配一个 4KB 页，返回页起始地址指针
func (m *PageManager) Alloc() unsafe.Pointer {
    m.mu.Lock()
    defer m.mu.Unlock()

    if len(m.freePages) == 0 {
        return nil // 无空闲页
    }

    // 取最后一个（栈操作，O(1)）
    pageID := m.freePages[len(m.freePages)-1]
    m.freePages = m.freePages[:len(m.freePages)-1]

    // 计算页在 mmap 中的偏移
    offset := pageID * m.pageSize
    return unsafe.Pointer(&m.data[offset])
}

// Free 释放一个 4KB 页
func (m *PageManager) Free(ptr unsafe.Pointer) {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 计算偏移和页ID
    offset := uintptr(ptr) - uintptr(unsafe.Pointer(&m.data[0]))
    pageID := int(offset / uintptr(m.pageSize))

    // 放回空闲列表
    m.freePages = append(m.freePages, pageID)
}

// Stats 返回统计信息
func (m *PageManager) Stats() PageManagerStats {
    m.mu.Lock()
    defer m.mu.Unlock()

    return PageManagerStats{
        Total:      m.total,
        Used:       m.total - len(m.freePages),
        Free:       len(m.freePages),
        TotalBytes: m.total * m.pageSize,
    }
}

type PageManagerStats struct {
    Total      int // 总页数
    Used       int // 已使用
    Free       int // 空闲
    TotalBytes int // 总字节数
}
```

### 2.3 PageInfo 结构修改

```go
type PageInfo struct {
    // 级别1：解析的 Page 对象
    page atomic.Value // *Page (原子操作，支持并发读写)

    // 级别2：未解析的 4KB Buffer
    buff []byte // 由 PageManager 分配的 4KB 页面

    // 级别3：持久化位置（暂不实现）
    pos int64

    // 其他字段
    pageLock *PageLock
    metaVersion int
    // ...
}

// GetPage 获取 Page 对象（延迟解析）
func (info *PageInfo) GetPage() *Page {
    // 快速路径：已解析
    if p := info.page.Load(); p != nil {
        return p.(*Page)
    }

    // 慢速路径：从 buff 解析
    if info.buff != nil {
        page := parsePageFromBuffer(info.buff)
        info.page.Store(page)
        return page
    }

    return nil
}

// SetPage 设置 Page 对象
func (info *PageInfo) SetPage(page *Page) {
    info.page.Store(page)
}

// Clone 克隆 PageInfo（共享 buff）
func (info *PageInfo) Clone() *PageInfo {
    return &PageInfo{
        buff:   info.buff,   // 共享 buff（关键！）
        pos:    info.pos,
        // page 不复制，由 Clone 后的 GetPage 延迟解析
    }
}
```

### 2.4 LeafPage 序列化/反序列化

```go
// Serialize 将 LeafPage 序列化为 4KB buffer
func (p *LeafPage) Serialize(buff []byte) error {
    if len(buff) < PageSize {
        return fmt.Errorf("buffer too small")
    }

    // 写入 PageHeader
    header := PageHeader{
        PageType:  LeafPage,
        Version:   p.version,
        KeyCount:  len(p.keys),
        PageID:    p.pageID,
    }

    offset := 0
    binary.Write(buff[offset:offset+PageHeaderSize], binary.LittleEndian, &header)
    offset += PageHeaderSize

    // 写入 keys 和 values（简化版，实际需要处理可变长度）
    for i := 0; i < len(p.keys); i++ {
        // 写入 key
        keyLen := len(p.keys[i])
        binary.LittleEndian.PutUint16(buff[offset:], uint16(keyLen))
        offset += 2
        copy(buff[offset:], p.keys[i])
        offset += keyLen

        // 写入 value
        valLen := len(p.values[i])
        binary.LittleEndian.PutUint16(buff[offset:], uint16(valLen))
        offset += 2
        copy(buff[offset:], p.values[i])
        offset += valLen
    }

    return nil
}

// Deserialize 从 4KB buffer 反序列化 LeafPage
func DeserializeLeafPage(buff []byte) (*LeafPage, error) {
    // 读取 PageHeader
    var header PageHeader
    err := binary.Read(bytes.NewReader(buff[:PageHeaderSize]), binary.LittleEndian, &header)
    if err != nil {
        return nil, err
    }

    page := &LeafPage{
        pageID:  header.PageID,
        version: header.Version,
        keys:    make([][]byte, 0, header.KeyCount),
        values:  make([][]byte, 0, header.KeyCount),
    }

    offset := PageHeaderSize

    // 读取 keys 和 values
    for i := 0; i < int(header.KeyCount); i++ {
        // 读取 key
        keyLen := binary.LittleEndian.Uint16(buff[offset:])
        offset += 2
        key := make([]byte, keyLen)
        copy(key, buff[offset:offset+int(keyLen)])
        offset += int(keyLen)
        page.keys = append(page.keys, key)

        // 读取 value
        valLen := binary.LittleEndian.Uint16(buff[offset:])
        offset += 2
        val := make([]byte, valLen)
        copy(val, buff[offset:offset+int(valLen)])
        offset += int(valLen)
        page.values = append(page.values, val)
    }

    return page, nil
}
```

### 2.5 集成到现有 BTree

```go
type BTree struct {
    // 现有字段
    rootRef  *RootPageRef
    config   *model.BTreeConfig

    // 新增：PageManager
    pageManager *PageManager
}

// 初始化时创建 PageManager
func NewBTree(config *model.BTreeConfig) (*BTree, error) {
    // 映射 16MB = 4096 个 4KB 页
    pageManager, err := NewPageManager(16 * 1024 * 1024)
    if err != nil {
        return nil, err
    }

    return &BTree{
        config:      config,
        pageManager: pageManager,
        // ...
    }, nil
}

// CloneWithBuff 使用 buff 模式克隆
func (p *LeafPage) CloneWithBuff(pageManager *PageManager) (*LeafPage, *PageInfo) {
    // 分配新的 4KB buffer
    buffPtr := pageManager.Alloc()
    if buffPtr == nil {
        return nil, nil // 无空闲页
    }
    buff := (*[PageSize]byte)(buffPtr)[:]

    // 序列化当前页面到 buff
    if err := p.Serialize(buff); err != nil {
        pageManager.Free(buffPtr)
        return nil, nil
    }

    // 创建新的 PageInfo（共享 buff）
    newInfo := &PageInfo{
        buff: buff,
        pos:  0, // 暂不持久化
    }

    // 创建新的 LeafPage（引用 buff）
    newPage := &LeafPage{
        pageID:  p.pageID,
        version: p.version + 1,
        // keys/values 不复制，由 GetPage() 延迟从 buff 解析
    }

    return newPage, newInfo
}
```

---

## 3. 优化效果预期

### 3.1 内存分配优化

| 操作 | 当前 | 使用 buff 后 | 提升 |
|------|------|-------------|------|
| Clone | 复制 keys/values | 只复制 buff 引用 | **~100x** |
| materialize | 7.62GB (26.43%) | ~1GB (buff复制) | **~7x** |
| NewPageInfo | 6.97GB (24.20%) | 共享 buff | **~3x** |

### 3.2 性能提升预期

| 场景 | 当前 | 预期 | 提升 |
|------|------|------|------|
| 8 线程吞吐 | 1.65M ops/sec | 2.5-3.0M ops/sec | **1.5-1.8x** |
| 单次 Clone 延迟 | ~600 ns | ~100 ns | **6x** |

### 3.3 GC 压力减轻

- **当前**：每次 Clone 创建新的 keys/values 切片
- **优化后**：只共享 buff 引用，GC 扫描对象减少 ~80%

---

## 4. 实施计划

### Phase 1: PageManager 实现（1-2 天）
- [ ] 实现 PageManager 基础结构
- [ ] 实现 Alloc/Free 操作
- [ ] 添加单元测试
- [ ] 性能基准测试

### Phase 2: PageInfo 结构修改（2-3 天）
- [ ] 添加 `buff []byte` 字段
- [ ] 实现 GetPage() 延迟解析
- [ ] 实现 Clone() 共享 buff
- [ ] 修改现有代码适配新结构

### Phase 3: 序列化/反序列化（2-3 天）
- [ ] 实现 LeafPage.Serialize()
- [ ] 实现 DeserializeLeafPage()
- [ ] 添加 PageHeader 结构
- [ ] 测试数据完整性

### Phase 4: 集成与测试（2-3 天）
- [ ] 集成到现有 BTree
- [ ] 并发安全性测试
- [ ] 性能基准测试
- [ ] 与 Lealone 对比

**总工期**：7-11 天

---

## 5. 风险与应对

| 风险点 | 影响 | 应对措施 |
|--------|------|---------|
| **序列化开销** | 中 | 缓存常用页面，避免频繁序列化 |
| **内存碎片** | 中 | PageManager 统一管理，避免碎片 |
| **并发安全性** | 高 | 使用 atomic.Value 保护 page 字段 |
| **向后兼容性** | 低 | 保持 API 不变，内部优化 |

---

## 6. 参考资料

1. **Lealone PageInfo**：
   - `thoughts/Lealone/lealone-aose/src/main/java/com/lealone/storage/aose/btree/page/PageInfo.java`

2. **Linux mmap 手册**：
   - `man 2 mmap`

3. **Go unsafe 包**：
   - https://pkg.go.dev/unsafe

---

## 7. 下一步

1. **评审本设计**：与团队讨论技术可行性
2. **创建原型**：实现最小可用版本验证性能
3. **性能测试**：与当前实现对比，量化收益
4. **逐步迁移**：先在测试环境验证，再上生产
