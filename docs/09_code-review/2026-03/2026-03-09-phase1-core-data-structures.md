# Phase 1: 核心数据结构实施计划

**创建时间**: 2026-03-09
**阶段**: Phase 1 - 核心数据结构
**预计时间**: 1 周（5-7 个工作日）
**状态**: 📝 计划中 - 待 Review
**依赖**: Phase 0.5 技术验证完成 ✅

---

## 📋 总体目标

实现 BTree 存储引擎的核心数据结构，为后续 CCOW 机制和业务逻辑奠定基础。

### 核心交付物

1. ✅ **接口定义** - 清晰的领域模型和服务接口
2. ✅ **Page 管理** - 页面分配、引用计数、序列化
3. ✅ **Node 操作** - 节点创建、分裂、合并、搜索
4. ✅ **序列化机制** - 高效的二进制序列化
5. ✅ **对象池优化** - sync.Pool 复用（基于 Phase 0.5 验证结果）
6. ✅ **单元测试** - 覆盖率 ≥ 85%

### 验收标准

- [ ] 所有接口定义清晰、文档完整
- [ ] Page 和 Node 结构体实现正确
- [ ] 序列化/反序列化往返测试通过
- [ ] 对象池性能验证通过
- [ ] 单元测试覆盖率 ≥ 85%
- [ ] 代码 review 通过

---

## 🗂️ 目录结构

```
internal/infrastructure/storage/btree/
├── btree.go              # BTree 主入口（接口实现）
├── btree_config.go       # 配置定义
├── btree_test.go         # BTree 测试
├── page.go               # 页面管理 ⭐ Phase 1
├── page_test.go          # 页面测试 ⭐ Phase 1
├── node.go               # 节点操作 ⭐ Phase 1
├── node_test.go          # 节点测试 ⭐ Phase 1
├── serializer.go         # 序列化机制 ⭐ Phase 1
├── serializer_test.go    # 序列化测试 ⭐ Phase 1
├── pool.go               # 对象池优化 ⭐ Phase 1
├── pool_test.go          # 对象池测试 ⭐ Phase 1
├── types.go              # 类型定义 ⭐ Phase 1
├── errors.go             # 错误定义 ⭐ Phase 1
└── base_test.go          # 测试辅助函数

# 已存在（Phase 0.5）
├── mini_ccow_prototype.go
├── mini_ccow_prototype_test.go
├── pool_test.go          # sync.Pool 基准测试（已有）
└── memory_leak_test.go   # 内存泄漏测试（已有）

internal/domain/service/
├── storage.go            # [扩展] KVStore 接口 ⭐ Phase 1
└── btree.go              # BTree 特定接口 ⭐ Phase 1

internal/domain/model/
├── storage.go            # [扩展] 存储模型 ⭐ Phase 1
└── btree_types.go        # BTree 类型定义 ⭐ Phase 1
```

**说明**: 标记 ⭐ 的是 Phase 1 新增/修改的文件

---

## 📅 详细任务分解

### Day 1: 接口定义和类型系统

**目标**: 定义清晰的领域模型和服务接口

#### 任务 1.1: 扩展 KVStore 接口 (2 小时)

```go
// internal/domain/service/storage.go

package service

import (
    "context"
    "io"
)

// KVStore 统一存储接口
type KVStore interface {
    // 基础 CRUD
    Get(ctx context.Context, key []byte) ([]byte, error)
    Set(ctx context.Context, key, value []byte) error
    Delete(ctx context.Context, key []byte) error

    // 批量操作
    GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error)
    SetBatch(ctx context.Context, kvs map[string][]byte) error

    // 范围查询
    RangeScan(ctx context.Context, start, end []byte) (Iterator, error)

    // 事务支持（预留，Phase 4 实现）
    BeginTx(ctx context.Context, opts ...TxOption) (Transaction, error)

    // 快照支持（预留，Phase 2 实现）
    CreateSnapshot(ctx context.Context) (SnapshotID, error)
    ReleaseSnapshot(ctx context.Context, id SnapshotID) error

    // 统计信息
    Stats(ctx context.Context) (*StoreStats, error)

    // 生命周期
    Close() error
}

// Iterator 范围扫描迭代器
type Iterator interface {
    Next() bool
    Key() []byte
    Value() []byte
    Err() error
    Close() error
}

// Transaction 事务接口（预留）
type Transaction interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
    Set(ctx context.Context, key, value []byte) error
    Delete(ctx context.Context, key []byte) error
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
}

// TxOption 事务选项
type TxOption func(*TxOptions)

type TxOptions struct {
    ReadOnly bool
    Snapshot SnapshotID
}

// SnapshotID 快照 ID
type SnapshotID uint64

// StoreStats 存储统计信息
type StoreStats struct {
    TotalKeys   int64
    TotalSize   int64
    TreeHeight  int
    PageCount   int
    Version     uint64
}
```

**验收**:
- [ ] 接口定义清晰、文档完整
- [ ] godoc 生成无警告
- [ ] 符合 Go 接口设计最佳实践

---

#### 任务 1.2: 定义 BTree 特定接口 (1 小时)

```go
// internal/domain/service/btree.go

package service

// BTree BTree 存储接口
type BTree interface {
    KVStore

    // BTree 特定操作
    GetHeight(ctx context.Context) (int, error)
    GetPageCount(ctx context.Context) (int, error)

    // 调试和监控
    DumpTree(ctx context.Context) (string, error)
    Validate(ctx context.Context) error
}
```

**验收**:
- [ ] 接口继承 KVStore
- [ ] BTree 特定方法合理
- [ ] 文档完整

---

#### 任务 1.3: BTree 占位实现 (1 小时) ⭐ 新增

**说明**: Phase 1 专注于数据结构，BTree 主体在 Phase 3 实现。这里提供占位实现。

```go
// internal/infrastructure/storage/btree/btree.go

package btree

import (
    "context"
    "errors"

    "github.com/jzhang405/NexKV/internal/domain/service"
)

// BTree BTree 存储引擎实现
type BTree struct {
    config *service.BTreeConfig
    // Phase 3 添加: root, pageManager, serializer, etc.
}

// NewBTree 创建 BTree（占位实现）
func NewBTree(config *service.BTreeConfig) (*BTree, error) {
    return &BTree{
        config: config,
    }, errors.New("BTree: not implemented until Phase 3")
}

// OpenBTree 打开 BTree（占位实现）
func OpenBTree(dir string, config *service.BTreeConfig) (*BTree, error) {
    return nil, errors.New("OpenBTree: not implemented until Phase 3")
}

// Get 实现（占位）
func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    return nil, errors.New("BTree.Get: not implemented until Phase 3")
}

// Set 实现（占位）
func (b *BTree) Set(ctx context.Context, key, value []byte) error {
    return errors.New("BTree.Set: not implemented until Phase 3")
}

// Close 实现（占位）
func (b *BTree) Close() error {
    return nil
}

// 其他 KVStore 方法类似...
```

**验收**:
- [ ] 占位实现编译通过
- [ ] 调用时返回清晰的错误信息
- [ ] 文档标注占位实现状态

---

#### 任务 1.4: 定义核心数据类型 (2 小时)

```go
// internal/domain/model/btree_types.go

package model

import "time"

// PageID 页面唯一标识符
type PageID uint64

const (
    // RootPageID 根页面 ID
    RootPageID PageID = 1

    // InvalidPageID 无效页面 ID
    InvalidPageID PageID = 0
)

// PageType 页面类型
type PageType uint8

const (
    // LeafPage 叶子节点页面（存储键值对）
    LeafPage PageType = iota

    // InternalPage 内部节点页面（存储索引）
    InternalPage

    // MetaPage 元数据页面（存储树信息）
    MetaPage
)

func (pt PageType) String() string {
    switch pt {
    case LeafPage:
        return "Leaf"
    case InternalPage:
        return "Internal"
    case MetaPage:
        return "Meta"
    default:
        return "Unknown"
    }
}

// BTreeConfig BTree 配置
type BTreeConfig struct {
    // 页面大小（字节）
    PageSize int

    // 最大键数量（每个页面）
    MaxKeys int

    // 最小键数量（每个页面，用于合并判断）
    MinKeys int

    // 最大版本数（用于 GC）
    MaxVersions int

    // 是否启用对象池
    EnablePool bool

    // 序列化压缩类型
    Compression CompressionType
}

const (
    // DefaultPageSize 默认页面大小 (4KB)
    DefaultPageSize = 4096

    // DefaultMaxKeys 默认最大键数
    DefaultMaxKeys = 128

    // DefaultMinKeys 默认最小键数 (MaxKeys / 2)
    DefaultMinKeys = 64

    // DefaultMaxVersions 默认最大版本数
    DefaultMaxVersions = 10
)

// NewDefaultBTreeConfig 创建默认配置
func NewDefaultBTreeConfig() *BTreeConfig {
    return &BTreeConfig{
        PageSize:    DefaultPageSize,
        MaxKeys:     DefaultMaxKeys,
        MinKeys:     DefaultMinKeys,
        MaxVersions: DefaultMaxVersions,
        EnablePool:  true, // Phase 0.5 验证：Node 使用 pool 有 14.9x 提升
        Compression: CompressionNone,
    }
}

// CompressionType 压缩类型
type CompressionType uint8

const (
    // CompressionNone 不压缩
    CompressionNone CompressionType = iota

    // CompressionSnappy Snappy 压缩
    CompressionSnappy

    // CompressionLZ4 LZ4 压缩
    CompressionLZ4

    // CompressionZSTD ZSTD 压缩
    CompressionZSTD
)

// BTreeStats BTree 统计信息
type BTreeStats struct {
    // 基础统计
    TotalKeys    int64
    TotalPages   int
    TreeHeight   int

    // 性能统计
    ReadCount    int64
    WriteCount   int64
    SplitCount   int64
    MergeCount   int64

    // 版本统计
    CurrentVersion uint64
    ActiveVersions int

    // 对象池统计
    PoolHits    int64
    PoolMisses  int64
}

// SnapshotInfo 快照信息
type SnapshotInfo struct {
    ID        SnapshotID
    RootID    PageID
    Version   uint64
    CreatedAt time.Time
    RefCount  int32
}
```

**验收**:
- [ ] 类型定义完整
- [ ] 常量定义合理
- [ ] 文档完整（godoc）

---

### Day 2: Page 管理

**目标**: 实现页面的分配、引用计数和基本操作

#### 任务 2.1: Page 结构体定义 (2 小时)

```go
// internal/infrastructure/storage/btree/page.go

package btree

import (
    "sync/atomic"
    "unsafe"
)

// Page BTree 页面
type Page struct {
    // 页面元数据
    ID      PageID
    Type    PageType
    Version uint64

    // 页面数据（固定大小，避免切片分配）
    Data [PageSize]byte

    // 引用计数（原子操作）
    RefCount atomic.Int32

    // 标记（用于 GC 和清理）
    dirty bool
}

const (
    // PageSize 页面大小 (4KB，与文件系统块对齐)
    PageSize = 4096
)

// NewPage 创建新页面
func NewPage(id PageID, pageType PageType) *Page {
    p := &Page{
        ID:      id,
        Type:    pageType,
        Version: 0,
        dirty:   false,  // 初始化 dirty 标志
    }
    p.RefCount.Store(1)
    return p
}

// Acquire 增加引用计数
func (p *Page) Acquire() {
    newCount := p.RefCount.Add(1)
    if newCount <= 1 {
        panic("btree: attempt to acquire page with zero refcount")
    }
}

// Release 减少引用计数
func (p *Page) Release() {
    newCount := p.RefCount.Add(-1)
    if newCount < 0 {
        panic("btree: page refcount underflow")
    }
    // 当引用计数为 0 时，页面可以被 GC 回收
    // 在 Phase 2 会实现 PageCache 来管理页面生命周期
}

// RefCountValue 获取当前引用计数（用于测试）
func (p *Page) RefCountValue() int32 {
    return p.RefCount.Load()
}

// IsLeaf 是否为叶子节点
func (p *Page) IsLeaf() bool {
    return p.Type == LeafPage
}

// Size 返回页面大小
func (p *Page) Size() int {
    return PageSize
}

// Bytes 返回页面数据的字节切片（零拷贝）
func (p *Page) Bytes() []byte {
    return unsafe.Slice(&p.Data[0], PageSize)
}
```

**验收**:
- [ ] Page 结构体实现正确
- [ ] 引用计数线程安全
- [ ] 单元测试覆盖所有方法

---

#### 任务 2.2: Page 测试 (2 小时)

```go
// internal/infrastructure/storage/btree/page_test.go

package btree

import (
    "sync"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestPage_NewPage(t *testing.T) {
    p := NewPage(1, LeafPage)

    assert.Equal(t, PageID(1), p.ID)
    assert.Equal(t, LeafPage, p.Type)
    assert.Equal(t, uint64(0), p.Version)
    assert.Equal(t, int32(1), p.RefCountValue())
    assert.True(t, p.IsLeaf())
}

func TestPage_AcquireRelease(t *testing.T) {
    p := NewPage(1, LeafPage)

    // 初始引用计数为 1
    assert.Equal(t, int32(1), p.RefCountValue())

    // Acquire 增加引用计数
    p.Acquire()
    assert.Equal(t, int32(2), p.RefCountValue())

    // Release 减少引用计数
    p.Release()
    assert.Equal(t, int32(1), p.RefCountValue())
}

func TestPage_ConcurrentAcquireRelease(t *testing.T) {
    p := NewPage(1, LeafPage)

    const goroutines = 100
    var wg sync.WaitGroup

    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < 10; j++ {
                p.Acquire()
                // 模拟使用
                p.Release()
            }
        }()
    }

    wg.Wait()

    // 最终引用计数应该回到初始值 1
    assert.Equal(t, int32(1), p.RefCountValue())
}

func TestPage_ReleasePanic(t *testing.T) {
    p := NewPage(1, LeafPage)
    p.Release()

    // 引用计数为 0 后再次 Release 应该 panic
    assert.Panics(t, func() {
        p.Release()
    })
}
```

**验收**:
- [ ] 所有测试通过
- [ ] 覆盖率 ≥ 85%
- [ ] 并发测试通过

---

### Day 3: Node 操作

**目标**: 实现节点的创建、分裂、合并和搜索操作

#### 任务 3.1: Node 结构体定义 (2 小时)

```go
// internal/infrastructure/storage/btree/node.go

package btree

import (
    "bytes"
    "sort"
)

// Node BTree 节点
type Node struct {
    Page     *Page
    Keys     [][]byte
    Values   [][]byte  // 叶子节点使用
    Children []PageID  // 内部节点使用
    IsLeaf   bool
}

// NewNode 创建新节点
func NewNode(isLeaf bool) *Node {
    return &Node{
        IsLeaf:   isLeaf,
        Keys:     make([][]byte, 0, DefaultMaxKeys),
        Values:   make([][]byte, 0, DefaultMaxKeys),
        Children: make([]PageID, 0, DefaultMaxKeys+1),
    }
}

// BinarySearch 二分查找键
func (n *Node) BinarySearch(key []byte) int {
    return sort.Search(len(n.Keys), func(i int) bool {
        return bytes.Compare(n.Keys[i], key) >= 0
    })
}

// Search 查找键对应的值
func (n *Node) Search(key []byte) ([]byte, bool) {
    idx := n.BinarySearch(key)

    if idx < len(n.Keys) && bytes.Equal(n.Keys[idx], key) {
        if n.IsLeaf {
            return n.Values[idx], true
        }
        // 内部节点不应该直接找到键（应该在子节点中）
        return nil, false
    }

    return nil, false
}

// Insert 插入键值对（假设节点有足够空间）
func (n *Node) Insert(key, value []byte) error {
    idx := n.BinarySearch(key)

    // 检查键是否已存在
    if idx < len(n.Keys) && bytes.Equal(n.Keys[idx], key) {
        // 更新现有值
        n.Values[idx] = value
        return nil
    }

    // 插入新键值对
    n.Keys = append(n.Keys, nil)
    n.Values = append(n.Values, nil)

    // 移动现有元素
    copy(n.Keys[idx+1:], n.Keys[idx:])
    copy(n.Values[idx+1:], n.Values[idx:])

    // 插入新元素
    n.Keys[idx] = key
    n.Values[idx] = value

    return nil
}

// Split 分裂节点（返回新节点和中间键）
func (n *Node) Split() (*Node, []byte, error) {
    if len(n.Keys) < DefaultMaxKeys {
        return nil, nil, ErrNodeNotFull
    }

    mid := DefaultMaxKeys / 2

    // 创建新节点
    newNode := &Node{
        IsLeaf: n.IsLeaf,
        Keys:   make([][]byte, 0, DefaultMaxKeys),
        Values: make([][]byte, 0, DefaultMaxKeys),
    }

    // 分裂键和值
    newNode.Keys = append(newNode.Keys, n.Keys[mid+1:]...)
    if n.IsLeaf {
        newNode.Values = append(newNode.Values, n.Values[mid+1:]...)
        n.Values = n.Values[:mid]
    } else {
        newNode.Children = append(newNode.Children, n.Children[mid+1:]...)
        n.Children = n.Children[:mid+1]
    }

    // 提取中间键
    midKey := n.Keys[mid]

    // 更新当前节点
    n.Keys = n.Keys[:mid]

    return newNode, midKey, nil
}

// Merge 合并节点
func (n *Node) Merge(other *Node) error {
    if len(n.Keys)+len(other.Keys) > DefaultMaxKeys {
        return ErrNodeOverflow
    }

    // 合并键和值
    n.Keys = append(n.Keys, other.Keys...)
    if n.IsLeaf {
        n.Values = append(n.Values, other.Values...)
    } else {
        n.Children = append(n.Children, other.Children...)
    }

    return nil
}
```

**验收**:
- [ ] Node 结构体实现正确
- [ ] 二分查找算法正确
- [ ] 插入、分裂、合并逻辑正确

---

#### 任务 3.2: Node 测试 (3 小时)

```go
// internal/infrastructure/storage/btree/node_test.go

package btree

import (
    "bytes"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestNode_BinarySearch(t *testing.T) {
    keys := [][]byte{
        []byte("apple"),
        []byte("banana"),
        []byte("cherry"),
    }

    node := &Node{Keys: keys}

    // 查找存在的键
    idx := node.BinarySearch([]byte("banana"))
    assert.Equal(t, 1, idx)

    // 查找不存在的键
    idx = node.BinarySearch([]byte("blueberry"))
    assert.Equal(t, 1, idx) // 应该插入在 banana 后面
}

func TestNode_Insert(t *testing.T) {
    node := NewNode(LeafPage, nil)

    // 插入第一个键
    err := node.Insert([]byte("key1"), []byte("value1"))
    require.NoError(t, err)
    assert.Equal(t, 1, len(node.Keys))
    assert.Equal(t, 1, len(node.Values))

    // 插入第二个键
    err = node.Insert([]byte("key2"), []byte("value2"))
    require.NoError(t, err)
    assert.Equal(t, 2, len(node.Keys))

    // 更新现有键
    err = node.Insert([]byte("key1"), []byte("value1-updated"))
    require.NoError(t, err)
    assert.Equal(t, 2, len(node.Keys)) // 长度不变

    val, ok := node.Search([]byte("key1"))
    assert.True(t, ok)
    assert.Equal(t, []byte("value1-updated"), val)
}

func TestNode_Split(t *testing.T) {
    // 创建满节点
    node := NewNode(LeafPage, nil)
    for i := 0; i < DefaultMaxKeys; i++ {
        key := []byte{byte(i)}
        value := []byte("value")
        err := node.Insert(key, value)
        require.NoError(t, err)
    }

    // 分裂节点
    newNode, midKey, err := node.Split()
    require.NoError(t, err)
    assert.NotNil(t, newNode)
    assert.NotNil(t, midKey)

    // 验证分裂后每个节点的键数量
    assert.LessOrEqual(t, len(node.Keys), DefaultMaxKeys/2)
    assert.LessOrEqual(t, len(newNode.Keys), DefaultMaxKeys/2)
}

func TestNode_Merge(t *testing.T) {
    node1 := NewNode(LeafPage, nil)
    node2 := NewNode(LeafPage, nil)

    // 填充节点
    for i := 0; i < DefaultMaxKeys/2; i++ {
        key := []byte{byte(i)}
        node1.Insert(key, []byte("value1"))
    }

    for i := DefaultMaxKeys / 2; i < DefaultMaxKeys; i++ {
        key := []byte{byte(i)}
        node2.Insert(key, []byte("value2"))
    }

    // 合并节点
    err := node1.Merge(node2)
    require.NoError(t, err)
    assert.Equal(t, DefaultMaxKeys, len(node1.Keys))
}
```

**验收**:
- [ ] 所有测试通过
- [ ] 边界条件测试完整
- [ ] 覆盖率 ≥ 85%

---

### Day 4: 序列化机制

**目标**: 实现高效的二进制序列化，支持压缩

#### 任务 4.1: 序列化器实现 (3 小时)

```go
// internal/infrastructure/storage/btree/serializer.go

package btree

import (
    "encoding/binary"
    "io"
)

// Serializer 序列化器
type Serializer struct {
    compression CompressionType
}

// NewSerializer 创建序列化器
func NewSerializer(compression CompressionType) *Serializer {
    return &Serializer{
        compression: compression,
    }
}

// MarshalPage 序列化页面
func (s *Serializer) MarshalPage(page *Page) ([]byte, error) {
    // 不压缩时：固定 4096 字节
    // 格式: [1]Type [8]Version [8]ID [4]RefCount [4075]Data
    if s.compression == CompressionNone {
        buf := make([]byte, PageSize)
        buf[0] = byte(page.Type)
        binary.BigEndian.PutUint64(buf[1:9], page.Version)
        binary.BigEndian.PutUint64(buf[9:17], uint64(page.ID))
        binary.BigEndian.PutUint32(buf[17:21], uint32(page.RefCount.Load()))
        copy(buf[21:], page.Data[:])
        return buf, nil
    }

    // 压缩时：可变长度
    // 格式: [1]Type [8]Version [8]ID [4]CompressedFlag(1) [4]DataLen [N]CompressedData
    dataToCompress := page.Data[:]
    compressed, err := s.compress(dataToCompress)
    if err != nil {
        return nil, err
    }

    // 分配缓冲区：头部(21) + 压缩标志(1) + 数据长度(4) + 压缩数据
    buf := make([]byte, 0, 21+1+4+len(compressed))
    buf[0] = byte(page.Type)
    binary.BigEndian.PutUint64(buf[1:9], page.Version)
    binary.BigEndian.PutUint64(buf[9:17], uint64(page.ID))
    buf[21] = 1 // 压缩标志
    binary.BigEndian.PutUint32(buf[22:26], uint32(len(compressed)))
    buf = append(buf, compressed...)

    return buf, nil
}

// UnmarshalPage 反序列化页面
func (s *Serializer) UnmarshalPage(data []byte) (*Page, error) {
    if len(data) < 21 {
        return nil, ErrInvalidPageData
    }

    page := &Page{
        Type:    PageType(data[0]),
        Version: binary.BigEndian.Uint64(data[1:9]),
        ID:      PageID(binary.BigEndian.Uint64(data[9:17])),
    }

    // 检查是否压缩（从第 21 字节开始）
    if len(data) > 21 && data[21] == 1 {
        // 压缩格式
        if len(data) < 26 {
            return nil, ErrInvalidPageData
        }
        dataLen := binary.BigEndian.Uint32(data[22:26])
        compressedData := data[26:]

        if uint32(len(compressedData)) != dataLen {
            return nil, ErrInvalidPageData
        }

        // 解压数据
        decompressed, err := s.decompress(compressedData)
        if err != nil {
            return nil, err
        }
        copy(page.Data[:], decompressed)
    } else {
        // 未压缩格式
        if len(data) < PageSize {
            return nil, ErrInvalidPageData
        }
        refCount := binary.BigEndian.Uint32(data[17:21])
        page.RefCount.Store(int32(refCount))
        copy(page.Data[:], data[21:])
    }

    return page, nil
}

// compress 压缩数据
// TODO(Phase 5): 集成实际的压缩库 (github.com/klauspost/compress/s2)
// 当前直接返回原数据（无压缩）
func (s *Serializer) compress(data []byte) ([]byte, error) {
    // Phase 5 实现：集成实际的压缩库
    return data, nil
}

// decompress 解压数据
// TODO(Phase 5): 集成实际的压缩库 (github.com/klauspost/compress/s2)
// 当前直接返回原数据（无压缩）
func (s *Serializer) decompress(data []byte) ([]byte, error) {
    // Phase 5 实现：集成实际的压缩库
    return data, nil
}
```

**验收**:
- [ ] 序列化/反序列化正确
- [ ] 往返测试通过
- [ ] 性能满足要求

---

#### 任务 4.2: 序列化测试 (2 小时)

```go
// internal/infrastructure/storage/btree/serializer_test.go

package btree

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestSerializer_MarshalUnmarshalPage(t *testing.T) {
    serializer := NewSerializer(CompressionNone)

    // 创建测试页面
    original := NewPage(123, LeafPage)
    original.Version = 42

    // 序列化
    data, err := serializer.MarshalPage(original)
    require.NoError(t, err)

    // 反序列化
    restored, err := serializer.UnmarshalPage(data)
    require.NoError(t, err)

    // 验证
    assert.Equal(t, original.ID, restored.ID)
    assert.Equal(t, original.Type, restored.Type)
    assert.Equal(t, original.Version, restored.Version)
    assert.Equal(t, original.Data, restored.Data)
}

func TestSerializer_RoundTrip(t *testing.T) {
    serializer := NewSerializer(CompressionNone)

    for i := 0; i < 100; i++ {
        page := NewPage(PageID(i), LeafPage)
        page.Data[0] = byte(i)

        // 序列化
        data, err := serializer.MarshalPage(page)
        require.NoError(t, err)

        // 反序列化
        restored, err := serializer.UnmarshalPage(data)
        require.NoError(t, err)

        // 验证数据完整性
        assert.Equal(t, page.ID, restored.ID)
        assert.Equal(t, page.Data[0], restored.Data[0])
    }
}
```

**验收**:
- [ ] 所有测试通过
- [ ] 往返测试覆盖率高
- [ ] 边界条件测试完整

---

### Day 5: 对象池优化

**目标**: 基于 Phase 0.5 验证结果，实现高效的对象池

#### 任务 5.1: 对象池实现 (2 小时)

```go
// internal/infrastructure/storage/btree/pool.go

package btree

import (
    "sync"
)

var (
    // pagePool 页面对象池（仅用于元数据页面）
    // 注意：Phase 0.5 验证显示 Page 不应该使用 pool（简单结构）
    pagePool = sync.Pool{
        New: func() any {
            return &Page{
                Data: [PageSize]byte{},
            }
        },
    }

    // nodePool 节点对象池
    // Phase 0.5 验证：Node 使用 pool 有 14.9x 性能提升
    nodePool = sync.Pool{
        New: func() any {
            return &Node{
                Keys:     make([][]byte, 0, DefaultMaxKeys),
                Values:   make([][]byte, 0, DefaultMaxKeys),
                Children: make([]PageID, 0, DefaultMaxKeys+1),
            }
        },
    }

    // slicePool 切片池（用于键值对切片）
    slicePool = sync.Pool{
        New: func() any {
            return make([][]byte, 0, DefaultMaxKeys)
        },
    }
)

// AcquirePage 从池中获取页面（仅用于元数据）
func AcquirePage() *Page {
    return pagePool.Get().(*Page)
}

// ReleasePage 将页面归还到池
func ReleasePage(page *Page) {
    // 重置页面状态
    page.ID = InvalidPageID
    page.Type = InternalPage
    page.Version = 0
    page.RefCount.Store(0)
    page.dirty = false

    // 清零数据（可选，取决于安全需求）
    // page.Data = [PageSize]byte{}

    pagePool.Put(page)
}

// AcquireNode 从池中获取节点
func AcquireNode() *Node {
    node := nodePool.Get().(*Node)

    // 重置节点状态
    node.Keys = node.Keys[:0]
    node.Values = node.Values[:0]
    node.Children = node.Children[:0]
    node.Page = nil

    return node
}

// ReleaseNode 将节点归还到池
func ReleaseNode(node *Node) {
    // 清空切片（保留容量）
    for i := range node.Keys {
        node.Keys[i] = nil // 帮助 GC
    }
    for i := range node.Values {
        node.Values[i] = nil // 帮助 GC
    }

    node.Keys = node.Keys[:0]
    node.Values = node.Values[:0]
    node.Children = node.Children[:0]
    node.Page = nil

    nodePool.Put(node)
}

// AcquireSlice 从池中获取切片
func AcquireSlice() [][]byte {
    slice := slicePool.Get().([][]byte)
    return slice[:0]
}

// ReleaseSlice 将切片归还到池
func ReleaseSlice(slice [][]byte) {
    // 清空切片
    for i := range slice {
        slice[i] = nil // 帮助 GC
    }

    slicePool.Put(slice[:0])
}
```

**验收**:
- [ ] 对象池实现正确
- [ ] 基于 Phase 0.5 验证结果（Node 使用 pool）
- [ ] 内存重置正确

---

#### 任务 5.2: 对象池测试 (2 小时)

```go
// 扩展 internal/infrastructure/storage/btree/pool_test.go

// Phase 1: 对象池功能测试

func TestPool_AcquireReleaseNode(t *testing.T) {
    // 获取节点
    node := AcquireNode()
    assert.NotNil(t, node)
    assert.Equal(t, 0, len(node.Keys))
    assert.Equal(t, 0, len(node.Values))

    // 使用节点
    node.Keys = append(node.Keys, []byte("test"))
    node.Values = append(node.Values, []byte("value"))

    // 释放节点
    ReleaseNode(node)

    // 再次获取，应该复用
    node2 := AcquireNode()
    assert.Equal(t, 0, len(node2.Keys)) // 应该被重置
}

func TestPool_ConcurrentAcquireRelease(t *testing.T) {
    const goroutines = 100
    const iterations = 1000

    var wg sync.WaitGroup
    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < iterations; j++ {
                node := AcquireNode()
                // 模拟使用
                node.Keys = append(node.Keys, []byte("test"))
                ReleaseNode(node)
            }
        }()
    }

    wg.Wait()
    // 如果没有 panic 或死锁，测试通过
}

func TestPool_MemoryReset(t *testing.T) {
    node := AcquireNode()

    // 添加一些数据
    node.Keys = append(node.Keys, []byte("key1"))
    node.Keys = append(node.Keys, []byte("key2"))
    node.Values = append(node.Values, []byte("value1"))
    node.Values = append(node.Values, []byte("value2"))

    assert.Equal(t, 2, len(node.Keys))
    assert.Equal(t, 2, len(node.Values))

    // 释放并重新获取
    ReleaseNode(node)
    node2 := AcquireNode()

    // 验证数据被清空
    assert.Equal(t, 0, len(node2.Keys))
    assert.Equal(t, 0, len(node2.Values))
}
```

**验收**:
- [ ] 所有测试通过
- [ ] 并发测试通过
- [ ] 内存重置验证通过

---

### Day 6-7: 集成和验证

**目标**: 集成所有组件，进行完整的单元测试

#### 任务 6.1: 错误定义 (1 小时)

```go
// internal/infrastructure/storage/btree/errors.go

package btree

import "errors"

var (
    // ErrKeyNotFound 键不存在
    ErrKeyNotFound = errors.New("btree: key not found")

    // ErrKeyAlreadyExists 键已存在
    ErrKeyAlreadyExists = errors.New("btree: key already exists")

    // ErrNodeNotFull 节点未满
    ErrNodeNotFull = errors.New("btree: node not full")

    // ErrNodeOverflow 节点溢出
    ErrNodeOverflow = errors.New("btree: node overflow")

    // ErrNodeUnderflow 节点下溢
    ErrNodeUnderflow = errors.New("btree: node underflow")

    // ErrInvalidPageData 无效的页面数据
    ErrInvalidPageData = errors.New("btree: invalid page data")

    // ErrTreeFull 树已满
    ErrTreeFull = errors.New("btree: tree is full")

    // ErrInvalidArgument 无效参数
    ErrInvalidArgument = errors.New("btree: invalid argument")
)
```

---

#### 任务 6.2: 基础测试框架 (2 小时)

```go
// internal/infrastructure/storage/btree/base_test.go

package btree

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/require"
)

// setupTestBTree 创建测试用 BTree
// ⚠️ Phase 1 占位：使用简单的 mock 对象
// Phase 2/3 会替换为真实的 OpenBTree 实现
func setupTestBTree(t *testing.T) *BTree {
    t.Helper()

    // Phase 1: 使用占位实现
    btree, err := NewBTree(NewDefaultBTreeConfig())

    // 如果占位实现返回错误，创建一个简单的 mock
    if err != nil {
        // 创建一个最小化的 BTree mock 用于测试 Page/Node
        btree = &BTree{}
    }

    t.Cleanup(func() {
        if btree != nil {
            btree.Close()
        }
    })

    return btree
}

// createTestPage 创建测试页面
func createTestPage(t *testing.T, id PageID, pageType PageType) *Page {
    t.Helper()

    page := NewPage(id, pageType)

    // 填充一些测试数据
    for i := 0; i < 100; i++ {
        page.Data[i] = byte(i)
    }

    return page
}

// createTestNode 创建测试节点
func createTestNode(t *testing.T, keys int) *Node {
    t.Helper()

    node := NewNode(LeafPage, nil)

    for i := 0; i < keys; i++ {
        key := []byte{byte(i)}
        value := []byte("test-value")
        err := node.Insert(key, value)
        require.NoError(t, err)
    }

    return node
}

// assertPageEqual 断言两个页面相等
func assertPageEqual(t *testing.T, expected, actual *Page) {
    t.Helper()

    require.Equal(t, expected.ID, actual.ID)
    require.Equal(t, expected.Type, actual.Type)
    require.Equal(t, expected.Version, actual.Version)
    require.Equal(t, expected.Data, actual.Data)
}

// assertNodeEqual 断言两个节点相等
func assertNodeEqual(t *testing.T, expected, actual *Node) {
    t.Helper()

    require.Equal(t, expected.IsLeaf, actual.IsLeaf)
    require.Equal(t, len(expected.Keys), len(actual.Keys))

    for i := range expected.Keys {
        require.Equal(t, expected.Keys[i], actual.Keys[i])
        if expected.IsLeaf {
            require.Equal(t, expected.Values[i], actual.Values[i])
        }
    }
}
```

---

#### 任务 6.3: 集成测试 (4 小时)

```go
// internal/infrastructure/storage/btree/integration_test.go

package btree

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestIntegration_PageNodeLifecycle(t *testing.T) {
    // 创建页面
    page := createTestPage(t, 1, LeafPage)

    // 创建节点
    node := NewNode(LeafPage, nil)
    node.Page = page

    // 插入数据
    err := node.Insert([]byte("key1"), []byte("value1"))
    require.NoError(t, err)

    // 序列化
    serializer := NewSerializer(CompressionNone)
    data, err := serializer.MarshalPage(page)
    require.NoError(t, err)

    // 反序列化
    restoredPage, err := serializer.UnmarshalPage(data)
    require.NoError(t, err)

    // 验证
    assertPageEqual(t, page, restoredPage)
}

func TestIntegration_ObjectPool(t *testing.T) {
    // 从池中获取节点
    node := AcquireNode()
    assert.NotNil(t, node)

    // 使用节点
    node.Keys = append(node.Keys, []byte("test"))

    // 释放节点
    ReleaseNode(node)

    // 再次获取，应该复用
    node2 := AcquireNode()
    assert.Equal(t, 0, len(node2.Keys)) // 应该被重置

    ReleaseNode(node2)
}
```

---

## ✅ 验收检查清单

### 代码质量

- [ ] 所有代码遵循 Go 编码规范
- [ ] godoc 注释完整
- [ ] 错误处理正确
- [ ] 无 race detector 警告

### 测试覆盖

- [ ] 单元测试覆盖率 ≥ 85%
- [ ] 所有公开 API 有测试
- [ ] 边界条件测试完整
- [ ] 并发测试通过

### 性能要求

- [ ] Page 分配性能基准测试
- [ ] Node 操作性能基准测试
- [ ] 序列化性能基准测试
- [ ] 对象池性能验证（对比 Phase 0.5 结果）

### 文档完整

- [ ] API 文档完整（godoc）
- [ ] 代码注释清晰
- [ ] README 或设计文档
- [ ] 测试用例文档

---

## 📊 预期输出

### 文件清单

**新增文件 (12 个)**:
1. `internal/domain/service/storage.go` (扩展)
2. `internal/domain/service/btree.go`
3. `internal/domain/model/btree_types.go`
4. `internal/infrastructure/storage/btree/page.go`
5. `internal/infrastructure/storage/btree/page_test.go`
6. `internal/infrastructure/storage/btree/node.go`
7. `internal/infrastructure/storage/btree/node_test.go`
8. `internal/infrastructure/storage/btree/serializer.go`
9. `internal/infrastructure/storage/btree/serializer_test.go`
10. `internal/infrastructure/storage/btree/pool.go`
11. `internal/infrastructure/storage/btree/pool_test.go` (扩展)
12. `internal/infrastructure/storage/btree/errors.go`

**代码量估算**:
- 代码: ~1,500 行
- 测试: ~1,200 行
- 文档: ~500 行
- **总计**: ~3,200 行

### 测试结果

```bash
# 单元测试
$ go test -v ./internal/infrastructure/storage/btree/
PASS
ok      github.com/jzhang405/NexKV/internal/infrastructure/storage/btree    0.123s

# 覆盖率
$ go test -coverprofile=coverage.out ./internal/infrastructure/storage/btree/
coverage: 87.3% of statements

# 性能基准
$ go test -bench=. -benchmem ./internal/infrastructure/storage/btree/
BenchmarkPage_Alloc-8     1000000    1.23 ns/op    0 B/op    0 allocs/op
BenchmarkNode_Insert-8    1000000   12.5 ns/op   64 B/op    1 allocs/op
```

---

## ⚠️ 风险和缓解

### 风险识别

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|----------|
| 接口设计不合理 | 中 | 中 | 参考 Phase 0.5 经验，先写后实现 |
| 性能不达标 | 低 | 高 | 对象池优化，参考 Phase 0.5 验证 |
| 测试覆盖不足 | 低 | 中 | 严格 85% 覆盖率要求 |
| 序列化复杂度高 | 中 | 中 | 先实现简单版本，Phase 5 优化 |

### 缓解策略

1. **增量开发**: 每天完成独立模块，及时验证
2. **持续测试**: 每个模块完成后立即编写测试
3. **代码 Review**: 每天结束后进行自我 review
4. **性能基准**: 参考 Phase 0.5 的 pool_test.go 经验

---

## 📚 参考资料

### Phase 0.5 成果
- [Mini CCOW 原型](../internal/infrastructure/storage/btree/mini_ccow_prototype.go)
- [sync.Pool 性能测试](../internal/infrastructure/storage/btree/pool_test.go)
- [Day 8-10 验证报告](../07_spike/btree-porting/2026-03-09-day8-10-validation-report.md)

### 设计文档
- [BTree 移植计划](../07_spike/btree-porting/2026-03-09-spike-btree-porting-plan.md)
- [Lealone 源码分析](../07_spike/btree-porting/2026-03-09-day1-2-lealone-source-analysis.md)

### 外部参考
- [Go 编码规范](../../CLAUDE.md)
- [Effective Go](https://golang.org/doc/effective_go)
- [Lealone BTree](https://github.com/lealone/lealone)

---

## 🎯 下一步（Phase 2）

Phase 1 完成后，进入 Phase 2: CCOW 机制实现（3 周）

**Phase 2 关键任务**:
1. 版本化根指针实现
2. CCOW 路径复制算法
3. 垃圾回收机制
4. 并发安全专项测试

**依赖关系**: Phase 2 依赖 Phase 1 的所有数据结构

---

**文档创建时间**: 2026-03-09
**作者**: Claude Code
**状态**: 📝 待 Review
**预计开始**: Review 通过后立即开始
