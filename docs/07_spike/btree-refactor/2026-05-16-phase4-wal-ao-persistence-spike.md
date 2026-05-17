# Phase 4 Spike: WAL + AO 文件持久化完整方案

> **预研类型**: Spike
> **创建日期**: 2026-05-16
> **分支**: `spike/btree-wal-ao-persistence`
> **前置**: Phase 3 WAL + GC（已完成）
> **状态**: Draft
> **目标**: 补全 Lealone 风格的 `.wal` + `.ao` (Append-Only Chunk) 双层文件持久化方案

---

## 一、概述

### 1.1 当前状态

NexKV BTree 存储引擎是一个**纯内存引擎**。以下表格总结了各模块的持久化现状：

| 模块 | 持久化状态 | 说明 |
|------|-----------|------|
| BTree 页面 | **无持久化** | `MAP_ANON \| MAP_PRIVATE` 匿名 mmap，进程退出即丢失 |
| WAL | 完整 | DiskWAL + Segment 轮转 + CRC32C + Group Commit + Recovery |
| Checkpoint | **半成品** | DFS 遍历 COW 根后仅写 WAL 条目并截断，**不刷新页面到磁盘** |
| ChunkManager / .ao 文件 | **未实现** | Phase 1 设计过接口和文件格式，从未进入实施 |
| MVCC VersionChain | 仅内存 | 通过 WAL Recovery 重建，无独立持久化 |
| OffHeap PageManager | 仅内存 | `OffheapAllocator` 使用匿名 mmap，无文件回退 |

**关键差距**：当前 NexKV 崩溃后，BTree 页面结构完全丢失，必须通过 WAL 全量重放重建。无页面级快照，启动恢复时间与数据量成正比。

### 1.2 目标状态

Phase 4 完成后，NexKV 将具备 Lealone 同等级别的双层持久化能力：

| 能力 | Phase 3 | Phase 4 目标 |
|------|---------|-------------|
| 崩溃恢复 | WAL Redo（全量重放） | WAL Redo + AO Checkpoint（增量恢复） |
| 页面持久化 | 无 | `.ao` 文件追加写入 + 惰性加载 |
| Checkpoint | 仅 WAL 元数据 | WAL 授权 + 页面全量刷盘 |
| 内存管理 | 全量常驻 | 惰性加载 + 可选 LRU 驱逐（远期） |
| 数据集上限 | 受 mmap 大小限制 (6GB) | 受磁盘容量限制 (TB 级) |
| 恢复时间 | O(全量 WAL) | O(增量 WAL after Checkpoint) |

### 1.3 范围声明

**Phase 4 范围（包含）**：
- ChunkManager 接口定义 + `DiskChunkManager` 实现
- `.ao` 文件格式定义（超级块 + 页面数据 + 空闲列表）
- 页面序列化/反序列化（就地格式，与 mmap 内存布局一致）
- `OffheapBTreeStorage` 惰性加载路径
- Checkpoint 页面刷新集成（模糊检查点 + 锐检查点）
- WAL Recovery 与 AO 惰性加载的协调

**排除（推迟到 Phase 5+）**：
- 分布式 WAL / 全局 Checkpoint 协调
- 页面 LRU 驱逐策略（当前 Checkpoint 前页面保留在内存中）
- Chunk 压缩（`ChunkCompactor` 定义接口但实现为存根）
- HLC commitTS（仍使用本地 `atomic.Uint64`）
- 跨节点 WAL 复制

### 1.4 与 Phase 3 WAL+GC 的对比

| 维度 | Phase 3 WAL+GC Spike | Phase 4 WAL+AO Spike |
|------|---------------------|---------------------|
| 关注点 | 事务日志持久化 + 版本回收 | 页面物理存储持久化 |
| 持久化对象 | WAL Entry（操作日志） | BTree Page（4KB 页面） |
| 恢复机制 | WAL Redo 重放所有操作 | Checkpoint + 增量 WAL Redo |
| 存储介质 | `.wal` 文件 | `.ao` 文件 |
| 接口层 | `service.WAL` | `service.ChunkManager` |

---

## 二、差距分析：Lealone vs NexKV

### 2.1 Lealone AOSE 存储模型（源码级）

基于 Lealone 源码（`lealone-aose/src/main/java/com/lealone/storage/aose/btree/`）的精确分析。

#### 2.1.1 文件模型

Lealone 使用 **单一 Chunk 文件** 承载页面数据 + Redo Log（无独立的 `.wal` 文件目录）：

```
Chunk 文件命名: c_[chunkId]_[sequence].db
  - chunkId: 由 BitField 分配的整数 ID（删除后可复用）
  - sequence: 全局单调递增序列号（用于恢复排序）
  - 扩展名: .db (AOStorage.SUFFIX_AO_FILE)
```

Redo Log（旧版本）存储在 `redo_log/` 目录中（独立 segment 文件），但新版已迁移为**嵌入 Chunk 文件的尾部追加**。NexKV 保留独立 `.wal` 文件设计（Go 惯用 + Group Commit 成熟实现）。

#### 2.1.2 Chunk 文件格式（源码精确）

Chunk 头部 = **双块写入**（8KB = 2 × 4096 字节），相同内容写两次用于崩溃安全：

```
偏移量 0:        头部块 1 (4096 字节) — 文本键值对
偏移量 4096:      头部块 2 (4096 字节) — 完全相同副本
偏移量 8192:      页面数据区域（变长页面）
                  │ 页面 0: [变长二进制数据]
                  │ 页面 1: [变长二进制数据]
                  │ ...
                  └─ pagePositionAndLengthOffset 处:
                     pagePositionToLengthMap: (pos:8B, length:4B) × pageCount
                     removedPages 表: (pos:8B) × removedPageCount
                     尾部: Redo Log (变长追加)
```

**头部内容**（文本键值对，`DataUtils.parseMap/appendMap`）：

| 字段 | 编码 | 描述 |
|------|------|------|
| `id` | hex int | Chunk ID |
| `rootPagePos` | hex long | 根页面位置（64-bit pos） |
| `pageCount` | hex int | Chunk 中的页面数 |
| `sumOfPageLength` | hex long | 所有页面长度总和 |
| `pagePositionAndLengthOffset` | hex int | 页面位置表在 Chunk 数据中的偏移量 |
| `blockSize` | hex int | 始终为 4096 |
| `format` | hex int | 格式版本 |
| `removedPageOffset` | hex int | 已删除页面表偏移量 |
| `removedPageCount` | hex int | 已删除页面数 |
| `lastTransactionId` | hex long | 最后事务 ID（WAL GC 边界） |
| `fletcher` | hex int | **Fletcher-32 校验和**（计算前排除自身） |

**崩溃安全**：启动时分别校验偏移量 0 和 4096 处的两个 4KB 块。若第一块损坏但第二块完好 → 恢复成功。这比单一超级块 + CRC32C 更健壮（允许单扇区损坏）。

#### 2.1.3 PageInfo.pos 编码（源码精确）

`PageUtils.getPagePos(chunkId, offset, type)`:

```
PageInfo.pos (int64):
  ┌─────────────────┬─────────────────┬──────────┐
  │ ChunkID         │ FileOffset      │ PageType │
  │ 30 bits         │ 32 bits         │ 2 bits   │
  └─────────────────┴─────────────────┴──────────┘
  maxChunks: 2^30 (10亿), maxOffset: 4GB/chunk

  PageType: 0=Leaf, 1=Node, 2=ColumnStorage
  压缩标记: PAGE_COMPRESSED=2 (LZF), PAGE_COMPRESSED_HIGH=6 (Deflate)
```

**关键语义：pos == 0 表示页面未持久化（脏页）**。`PageInfo.isDirty()` 返回 `pos == 0`。页面写入磁盘后获得非零 pos。无需额外的 `dirty` 布尔字段。

#### 2.1.4 页面读写生命周期

```
读路径 (getOrReadPage):
  pInfo.page != null → 返回缓存 (updateTime 记录 LRU 时间戳)
  pInfo.page == null → readPage():
    - buff != null? → 重用缓存 ByteBuffer 反序列化
    - buff == null? → readPageBuffer(pos) → 从 FileStorage 读取
    - CAS replacePage(pInfoOld, pInfoNew) → 安装新 PageInfo
    - CAS 失败 → 递归重试

写路径 (markDirtyPage + executeSave):
  markDirtyPage():
    - CAS pInfoOld(pos≠0) → pInfoNew(pos=0, buff=null)  ← 标记脏页
    - 将旧 pos 加入 ChunkManager.removedPages
    - 沿 PageListener 父链向上传播 (自底向上)，每层 CAS 标记
  executeSave():
    - collectDirtyMemory() → 遍历 root 估算脏内存
    - NodePage.write() → children-first 递归写入
    - Chunk.write() → 写头部 + 页面数据 + Sync
```

**Children-First 写入**：`NodePage.write()` 先递归调用 `writeChildren()` 写入所有子页面，获取子页面的 pos，再将自己的数据（含子页面 pos 数组）写入。这保证磁盘上的父页面始终指向有效的子页面位置。

#### 2.1.5 PageListener 父链（脏页传播）

```java
class PageListener {
    IPageReference pageReference;  // 当前页面的 PageRef
    PageListener parent;           // 父节点的 Listener
}
```

每个 `PageReference` 持有一个 `PageLock`，后者包含 `PageListener`。Listener 形成从叶子到根的父链。

`markDirtyPage()` 自底向上传播：
1. 在当前页面 CAS: `pInfo(pos≠0) → pInfo(pos=0)`
2. 获取 `parentRef = getParentRef()`
3. 在父节点 CAS: `pInfo → pInfo(pos=0)`（使用 `oldPageListener.getParent()` 版本号检测并发 GC/分裂）
4. 重复直到根节点

**并发保护**：每层使用 `PageListener` 版本号 —— 若父节点的 Listener 已变（页面被 GC 或分裂），传播停止并返回错误码（1=GC'd, 2=Split）。

#### 2.1.6 Checkpoint 触发与流程

```
触发条件 (4种):
  1. 强制: SQL CHECKPOINT / 关闭时
  2. 周期: nextCheckpointTime (默认 12 小时)
  3. 脏内存阈值: collectDirtyMemory() > cacheSize
  4. WAL 写入计数: 每 512 个事务批次

Checkpoint 流程:
  1. lastTxnId = redoLog.getLastTransactionId()
  2. 对于每个脏 map:
     map.setLastTransactionId(lastTxnId)
     map.save(size) → 刷新脏页到 Chunk 文件
     map.setLastTransactionId(-1)
  3. ChunkCompactor.executeCompact() → 重写低填充率 Chunk
  4. 删除未使用的旧 Chunk 文件
```

**WAL GC**：Recovery 时扫描 `redo_log/` 目录，遇到 Checkpoint 标记后丢弃之前所有 redo 条目 —— 因为 Checkpoint 保证脏页已全部落盘。

#### 2.1.7 页面变长存储

Lealone 的页面**不是固定 4096 字节**。每个页面有独立的 `pageLength`，存储在 `pagePositionToLengthMap(ConcurrentHashMap<Long, Integer>)` 中。Chunk 写入时将此映射持久化为 `(pos:8B, length:4B) × pageCount`。页面大小受 `pageSize` 配置限制（默认 4KB，最大由页面分裂阈值决定）。

### 2.2 NexKV 当前状态

| 维度 | Lealone | NexKV 当前 | 差距 |
|------|---------|-----------|------|
| **页面存储** | mmap + `.ao` 文件 | `MAP_ANON` 纯内存 | **无磁盘持久化** |
| **PageID 编码** | 64-bit pos (chunk+offset+type) | `uint32` PageID（单调递增） | 位置模型不兼容 |
| **页面加载** | 按需惰性加载 (`getOrReadPage`) | 始终驻留内存 | **无磁盘读取路径** |
| **脏页追踪** | `PageInfo.isDirty` + 自底向上标记 | 隐式 COW（Go GC 管理） | **需要显式 flush 机制** |
| **序列化格式** | `FixedLayoutSerializer` | 内存 PageHeader/Layout | **需要磁盘序列化** |
| **空闲页面复用** | Chunk 内空闲列表 | `LockFreeQueue` 自由列表（仅内存） | **需要持久化空闲列表** |
| **Checkpoint** | 页面快照 + WAL 截断 | 仅 WAL 授权截断 | **缺少页面刷新** |

### 2.3 需要演变的约束

以下当前设计约束需要在 Phase 4 中演变：

| 约束 | 当前设计 | Phase 4 改造 |
|------|---------|-------------|
| `model.PageID` | `uint64`（领域模型定义） | 保留作为逻辑 ID；运行时映射（`pageLocs`）和磁盘格式（`CheckpointEntry`、`PageHeader`）使用 `uint32` 子集（4B × 4KB = 16TB 寻址范围，远超实际需求） |
| `PageHeader` 大小 | 56 字节 (`btree.HeaderSize`) | 在现有 16 字节填充区嵌入 `chunkPos`（8 字节），剩余 `[8]byte` 填充，保持 56 字节不变 |
| `OffheapBTreeStorage` | 直接调用 `PageManager.Alloc()` | 增加惰性加载路径：`ChunkManager.ReadPage()` → 反序列化 |
| `CheckpointManager` | 仅 WAL 操作 | 增加页面遍历 + `ChunkManager.WritePage()` + Sync |
| `Recovery` | 全量 WAL 重放 | Checkpoint 基础 + 增量 WAL 重放 + 惰性加载 |

---

## 三、ChunkManager 设计

### 3.1 领域接口

`ChunkManager` 接口遵循 `service.WAL` 的先例，定义在领域层：

```go
// domain/service/chunk_manager.go

// ChunkManager 管理 .ao 文件中 BTree 页面的物理存储。
// 页面被分配唯一的位置 (ChunkPosition)，支持按需惰性加载。
type ChunkManager interface {
    // Allocate 在 .ao 文件中分配空间，返回 ChunkPosition。
    Allocate(size int, pageType uint8) (ChunkPosition, error)

    // WritePage 在给定位置写入序列化页面数据。
    WritePage(pos ChunkPosition, data []byte) error

    // WritePages 批量写入页面（检查点优化路径）。
    WritePages(pages map[ChunkPosition][]byte) error

    // ReadPage 从给定位置读取序列化页面数据（惰性加载）。
    ReadPage(pos ChunkPosition) ([]byte, error)

    // FreePage 将页面位置标记为可重用。
    FreePage(pos ChunkPosition) error

    // Sync 将所有缓冲写入刷到磁盘。
    Sync() error

    // Stats 返回 ChunkManager 统计信息。
    Stats() ChunkManagerStats

    // Close 关闭所有 Chunk 文件。
    Close() error
}

// ChunkManagerStats 提供 ChunkManager 的运行时统计信息。
type ChunkManagerStats struct {
    TotalChunks  int   // 总 Chunk 文件数
    ActiveChunks int   // 活跃 Chunk 文件数
    TotalPages   int64 // 总分配页面数
    FreePages    int64 // 空闲页面数
    UsedBytes    int64 // 已用字节数
    FreeBytes    int64 // 空闲字节数
    ReadOps      int64 // 读操作计数
    WriteOps     int64 // 写操作计数
}
```

### 3.2 ChunkPosition 编码

64 位编码，与 Lealone 的 `PageInfo.pos` 兼容：

```go
// ChunkPosition 是 .ao 文件中的 64 位页面位置。
//
// 位布局（与 Lealone pos 字段一致）:
//   [63:38] ChunkID     (26 bits, 最多 67M 个 Chunk)
//   [37:6]  FileOffset  (32 bits, 每 Chunk 最大 4GB)
//   [5:1]   PageType    (5 bits, 32 种页面类型)
//   [0]     Reserved    (1 bit, 预留)
type ChunkPosition uint64

// 辅助方法
func EncodeChunkPosition(chunkID uint32, offset uint32, pageType uint8) (ChunkPosition, error)
    // 验证: chunkID 必须 ≤ MaxChunkID (2^26-1 = 67108863)，防止高位溢出到 FileOffset 区域
func (p ChunkPosition) ChunkID() uint32
func (p ChunkPosition) FileOffset() uint32
func (p ChunkPosition) PageType() uint8
func (p ChunkPosition) IsZero() bool  // 零值 = 未持久化

const MaxChunkID = (1 << 26) - 1 // 67108863
```

### 3.3 ChunkFile 结构

对齐 Lealone `Chunk.java`：Chunk 头部双块写入 (8KB)，RemovedPages 替代 FreeList，无超级块 Magic。

```go
// internal/infrastructure/storage/chunk/chunk_file.go

const (
    ChunkBlockSize      = 4096           // 物理扇区大小
    ChunkHeaderBlocks   = 2              // 双块写入 (崩溃安全)
    ChunkHeaderSize     = ChunkBlockSize * ChunkHeaderBlocks  // 8192 字节
)

type ChunkFile struct {
    id           uint32     // Chunk ID (0, 1, 2, ...)
    seq          uint64     // 全局单调序列号 (恢复排序)
    file         *os.File   // 底层文件句柄
    path         string     // btree_[id]_[seq].ao
    size         int64      // 当前文件大小
    capacity     int64      // 最大容量 (256MB，对齐 Lealone maxChunkSize)
    nextOffset   int64      // 下一个追加位置
    // 页面元数据 (对齐 Lealone pagePositionToLengthMap)
    pagePosToLen map[ChunkPosition]int32  // pos → pageLength (变长页面)
    // 已删除页面集 (对齐 Lealone removedPages — ConcurrentSkipListSet<Long> 的 Go 对应)
    removedPages map[ChunkPosition]struct{} // 待 Compactor 回收的位置
    mu           sync.RWMutex
}
```

**RemovedPages 替代 FreeList**（对齐 Lealone 设计）：Lealone 使用 `ConcurrentSkipListSet<Long> removedPages` 追踪已删除页面位置，而非显式 FreeList。空间复用由 `ChunkCompactor` 重写低填充率 Chunk 时合并 `removedPages` 实现。NexKV 采用相同模型：

- **写路径**：页面被 COW 覆盖 → 旧 pos 加入 `removedPages`
- **Checkpoint 时**：`removedPages` 持久化到 Chunk 文件尾部（`removedPageOffset + removedPageCount`）
- **Compactor**（Phase 5）：扫描 `removedPages`，重写低填充率 Chunk，物理删除旧文件

```go
// Chunk 头部字段 (文本键值对格式，对齐 Lealone)
type ChunkHeader struct {
    ID                         uint32  // chunk id
    RootPagePos                uint64  // 根页面位置 (64-bit ChunkPosition)
    PageCount                  int32   // 页面总数
    SumOfPageLength            int64   // 所有页面长度总和
    SumOfLivePageLength        int64   // 存活页面长度总和
    PagePositionAndLengthOffset int64  // pagePosToLen 映射在文件中的偏移量
    BlockSize                  int32   // 始终为 4096
    FormatVersion              int32   // 格式版本
    RemovedPageOffset          int64   // removedPages 表在文件中的偏移量
    RemovedPageCount           int32   // 已删除页面数
    LastTransactionID          int64   // 最后事务 ID (WAL GC 边界)
    MapSize                    int64   // BTreeMap 大小
    Fletcher32                 uint32  // Fletcher-32 校验和
}
```

### 3.4 DiskChunkManager 实现

对齐 Lealone `ChunkManager.java`：无 maxChunks 硬限制，序列号排序恢复，无超级块 Magic。

```go
// internal/infrastructure/storage/chunk/disk_chunk_manager.go

type DiskChunkManager struct {
    dir          string               // Chunk 文件目录
    chunkSize    int64                // 每 Chunk 大小 (256MB，对齐 Lealone maxChunkSize)
    chunks       []*ChunkFile         // 活跃 Chunk 列表（按 seq 排序）
    lastChunk    *ChunkFile           // 最近写入的 Chunk 引用
    maxSeq       uint64               // 全局最大序列号 (对齐 Lealone maxSeq)
    chunkIDs     *bitset.BitSet       // Chunk ID 位图 (对齐 Lealone BitField)
    idToChunk    map[uint32]*ChunkFile // chunkID → ChunkFile
    seqToID      map[uint64]uint32    // seq → chunkID (对齐 Lealone seqToIdMap)
    removedPages map[ChunkPosition]struct{} // 全局已删除页面集 (对齐 Lealone ConcurrentSkipListSet)
    mu           sync.RWMutex
    stats        ChunkManagerStats
}

// NewDiskChunkManager 首次创建（无已有 Chunk 文件）。
// 创建第一个 Chunk: btree_0_1.ao
func NewDiskChunkManager(dir string, chunkSize int64) (*DiskChunkManager, error)

// RestoreDiskChunkManager 从已有 Chunk 文件恢复（重启场景）。
// 对齐 Lealone ChunkManager.init() 恢复协议:
//   1. 扫描目录: 列出所有 btree_*_*.ao 文件
//   2. 删除零长度文件 (崩溃时创建但未写入)
//   3. 对每个文件:
//      a. 解析文件名: btree_[chunkId]_[seq].ao → chunkID + seq
//      b. 追踪 maxSeq 及其对应的 chunkID
//      c. 处理重复 chunkID: 保留最高 seq 的文件名 (备份恢复)
//   4. 按 seq 排序所有 Chunk
//   5. 打开最高 seq 的 Chunk (lastChunk)
//   6. 验证双块头部: 读 8192 字节，分别校验偏移量 0 和 4096 处的 4KB 块
//      - Fletcher32 校验 → 有效 → 解析头部
//      - 偏移量 0 损坏但 4096 完好 → 从副本恢复
//      - 两者都损坏 → 标记为损坏，跳过 (ERROR_FILE_CORRUPT)
//   7. 从头部恢复 lastTxnId, removedPages, pagePosToLen
//   8. 重建 chunkIDs BitSet, idToChunk, seqToID
//
// 无超级块 Magic: 恢复完全基于文件名序列号排序 + 双块头部校验，
// 比单一超级块 + CRC32C 更健壮（容忍单扇区损坏）。
func RestoreDiskChunkManager(dir string) (*DiskChunkManager, error)
```

**Chunk 文件命名**：`btree_0000.ao`, `btree_0001.ao`, `btree_0002.ao`, ...

### 3.5 块分配策略

对齐 Lealone `BTreeStorage.executeSave()` 的 Chunk 选择逻辑：

```
DiskChunkManager.Allocate(size, pageType):
  1. 获取 lastChunk
  2. 检查 lastChunk 剩余空间: nextOffset + size <= capacity?
     a. 是 → 在 lastChunk 分配（追加模式），返回 ChunkPosition(chunkID, nextOffset, pageType)
     b. 否 → createChunk():
       i.   chunkID = chunkIDs.nextClearBit(1)  ← BitField 分配 (对齐 Lealone)
       ii.  seq = ++maxSeq                       ← 全局序列号递增
       iii. path = fmt.Sprintf("btree_%d_%d.ao", chunkID, seq)
       iv.  fallocate(chunkSize) 预分配空间
       v.   写入双块头部（两个相同的 4KB 块）
       vi.  chunks.append, idToChunk[id]=c, seqToID[seq]=id
       vii. lastChunk = c
       返回 ChunkPosition(newChunkID, ChunkHeaderSize, pageType)

无 maxChunks 限制 (对齐 Lealone): Chunk 数量任意增长，由 ChunkCompactor (Phase 5) 控制空间回收。
```

### 3.6 ChunkCompactor

对齐 Lealone `ChunkCompactor.java`：基于填充率的重写 + 两阶段删除。

```go
// internal/infrastructure/storage/chunk/chunk_compactor.go

type ChunkCompactor struct {
    cm          *DiskChunkManager
    minFillRate int  // 最低填充率 (默认 30%, 对齐 Lealone，上限 50%)
}

// executeCompact 对齐 Lealone 的压缩算法:
//   1. 收集所有 removedPages (cm.removedPages + lastChunk.removedPages)
//   2. 若 removedPages 为空 → 直接返回
//   3. 读取包含已删除页面的 Chunk (跳过 NodePage)
//   4. 计算每个 Chunk 的 fillRate = 1 + 98*live/total (对齐 Lealone getFillRate)
//   5. 分离: fillRate==0 → unusedChunks; fillRate<=minFillRate → rewritable
//   6. 按 fillRate 升序 + sumOfLivePageLength 升序排序
//   7. 贪心选择尽可能多的 Chunk 重写，累计 liveSize <= MAX_SIZE
//   8. 将选中 Chunk 的活跃页面写入新 Chunk，更新 PageRef 的 pos
//   9. 两阶段删除: 先标记 unusedChunks，新 trunk 成功后物理删除

func (c *ChunkCompactor) NeedCompaction() bool  // 返回 fillRate <= minFillRate 的 Chunk 数量 > 0
func (c *ChunkCompactor) ExecuteCompact() error // 主入口 (Phase 5 实现)
```

---

## 四、AO 文件格式

对齐 Lealone `Chunk.java` 源码格式：双块文本头部 + Fletcher32 + 变长页面 + pagePositionToLengthMap + removedPages 表。**无超级块 Magic Number**。

### 4.1 文件布局

```
btree_[chunkId]_[seq].ao (对齐 Lealone c_[id]_[seq].db):
┌───────────────────────────────────────────────────────────┐
│ 头部块 1 (4096 字节) — 文本键值对                           │ ◀── 双块写入
│  例: "id:1\nrootPagePos:1a2b3c...\npageCount:100\n..."    │     (崩溃安全)
│  最后一个字段: fletcher:XXXXXXXX (计算前排除自身)            │
├───────────────────────────────────────────────────────────┤
│ 头部块 2 (4096 字节) — 完全相同的副本                       │ ◀── 单扇区损坏可恢复
├═══════════════════════════════════════════════════════════╡
│ 页面数据区域 (从偏移量 8192 开始，变长页面)                  │
│  │ Page 0: [CRC32C:4][PageHeader+Data:pageLength 字节]    │
│  │ Page 1: [CRC32C:4][PageHeader+Data:pageLength 字节]    │
│  │ ...                                                    │
│  │ Page N: [CRC32C:4][PageHeader+Data:pageLength 字节]    │
├───────────────────────────────────────────────────────────┤
│ pagePositionAndLengthOffset 处:                           │
│  │ pagePositionToLengthMap: (pos:8B + length:4B) × N     │
├───────────────────────────────────────────────────────────┤
│ removedPageOffset 处:                                     │
│  │ removedPages 表: (pos:8B) × removedPageCount           │
└───────────────────────────────────────────────────────────┘
```

**无超级块 Magic Number**（对齐 Lealone）：恢复时扫描目录 → 解析文件名 seq → 排序 → 取最高 seq chunk → 校验双块头部 Fletcher32。比单一 Magic + CRC32C 更健壮（容忍单扇区损坏）。

### 4.2 页面磁盘格式

采用**就地格式** — 磁盘格式与内存格式一致，仅在头部增加 4 字节 CRC32C。**磁盘页面大小为 4100 字节**（= `PageSize 4096 + CRCSize 4`）：

```
每个页面的磁盘格式 (4100 字节):
┌────────────────────────────────────────────┐
│ CRC32C      (4B)  Castagnoli 多项式         │ ◀── CRC 覆盖后续 4096 字节
│ ═══════ 以下与内存 PageHeader+Data 一致 ════  │
│ Version     (8B)  COW 版本号                │
│ PrevPage    (4B)  前一个叶子页面（叶子链表）   │
│ NextPage    (4B)  后一个叶子页面             │
│ ExtraChild  (8B)  内部节点的 N+1 子节点      │
│ Count       (2B)  条目数                    │
│ PageType    (1B)  0=内部节点, 1=叶子        │
│ Deleted     (1B)  删除标记                  │
│ TombstoneCnt(2B)  墓碑计数                  │
│ DeleteEpoch (8B)  删除纪元                  │
│ ChunkPos    (8B)  .ao 文件中的物理位置 ⬅ NEW  │
│ Reserved    (8B)  填充                      │
│ ═══════════ 56 字节 PageHeader ════════════  │
│ 条目数组    (变长)                           │
│ 空闲区域    (变长)                           │
│ KV 数据     (变长，从页尾向前分配)             │
│ ═══════════ 4096 字节 数据区 ═══════════════ │
└────────────────────────────────────────────┘
```

**关键设计决策**：`ChunkPos` (8 字节) 使用 `PageHeader` 中原本的 16 字节填充区域的一部分。`HeaderSize` 保持 56 字节不变（= `offheap.SizeofPageHeader`）。**磁盘页面 = 4 字节 CRC + 4096 字节完整页面数据 = 4100 字节**，不会丢失任何 KV 数据。

### 4.3 页面序列化协议

```go
// internal/infrastructure/storage/chunk/page_serializer.go

import "hash/crc32"

// crc32cTable 使用 Castagnoli 多项式，与 WAL 的 wal.CRC32C() 保持一致。
// Castagnoli 具有 x86 SSE4.2 (crc32q) 和 ARM (crc32w) 硬件加速。
var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

const (
    DiskPageSize = PageSize + CRCSize // 4100 字节 = 4096 + 4
    CRCSize      = 4
    PayloadSize  = PageSize           // 4096 字节
)
```

**序列化约束**：`Serialize` 返回完整的 `[CRC32C:4][PageHeader+Data:4096]` = 4100 字节，确保页面数据区最后的 KV 数据不会丢失。

```go
type PageSerializer struct{}

// Serialize 将 mmap 页面编码为磁盘格式。
// 输出: [CRC32C:4][PageHeader+Data:4096] = 4100 字节
// CRC32C 覆盖 PageHeader+Data 部分（偏移 4 之后的 4096 字节）。
// 使用 sync.Pool 复用缓冲区，减少 GC 压力。
func (s *PageSerializer) Serialize(ptr unsafe.Pointer) ([]byte, error) {
    buf := make([]byte, DiskPageSize) // 4100 字节
    // CRC32C 占前 4 字节，先置零
    binary.LittleEndian.PutUint32(buf[0:CRCSize], 0)
    // 复制完整 PageHeader + Data (4096 字节) 到偏移 4 处
    src := unsafe.Slice((*byte)(ptr), PageSize)
    copy(buf[CRCSize:CRCSize+PageSize], src) // 复制完整的 4096 字节
    // 计算 CRC32C — Castagnoli 多项式 (覆盖 buf[4:4100])
    crc := crc32.Checksum(buf[CRCSize:], crc32cTable)
    binary.LittleEndian.PutUint32(buf[0:CRCSize], crc)
    return buf, nil
}

// Deserialize 解码磁盘格式并写入 mmap 目标位置。
// dst 必须是有效的 4096 字节 mmap 页面指针。
func (s *PageSerializer) Deserialize(data []byte, dst unsafe.Pointer) error {
    // 输入长度检查（防止短切片 panic 和数据不完整）
    if len(data) < DiskPageSize {
        return fmt.Errorf("page_serializer: short data %d < %d", len(data), DiskPageSize)
    }
    if dst == nil {
        return ErrNilDestination
    }

    // 验证 CRC32C (Castagnoli 多项式)
    expectedCRC := binary.LittleEndian.Uint32(data[0:CRCSize])
    actualCRC := crc32.Checksum(data[CRCSize:CRCSize+PageSize], crc32cTable)
    if expectedCRC != actualCRC {
        return ErrCRCMismatch
    }

    // 复制完整的 PageHeader + Data (4096 字节) 到 mmap
    dstSlice := unsafe.Slice((*byte)(dst), PageSize)
    copy(dstSlice, data[CRCSize:CRCSize+PageSize])
    return nil
}
```

**序列化成本分析**：一次 `Serialize` = 1 次 `make([]byte, 4100)` + 1 次 `copy(4096)` + 1 次 `crc32.Checksum(4096)`。在 x86（SSE4.2 CRC32C 硬件加速）上预期延迟 3-5μs。建议 Phase 4.1 使用 `sync.Pool` 复用 4100 字节缓冲区，Checkpoint 遍历大量页面时避免 GC 压力。

**CRC 多项式一致性**：与 WAL 模块（`internal/infrastructure/storage/wal/crc.go`）共同使用 Castagnoli (CRC32C)，避免同一系统中两种 CRC 算法并存，统一验证工具链。

---

## 五、WAL-AO 协调协议

### 5.1 当前状态：仅 WAL 检查点

当前 `CheckpointManager.FuzzyCheckpoint()` 执行以下步骤：
1. 记录 `checkpointStartLSN = wal.CurrentLSN()`
2. COW 快照根指针
3. DFS 遍历可达页面（收集 PageID 列表）
4. 写 `WALTypeCheckpoint` 条目（携带 `startLSN` 和页面列表）
5. `wal.Sync()` + `wal.Truncate(startLSN)`

**缺失**：步骤 3 收集的页面列表只记录在 WAL 条目中，**页面数据本身从未写入磁盘**。

### 5.2 提议：WAL + AO 两阶段检查点

```
Phase 4 模糊检查点流程 (FuzzyCheckpoint with Page Flush + Mapping):

  T0: checkpointStartLSN = wal.CurrentLSN()       ← 先记录 LSN
      mapping := make(map[uint32]ChunkPosition)    ← pageID→ChunkPos 映射表
  T1: rootRef = LoadPointer(&tree.root)            ← COW 快照根
  T2: DFS 遍历 rootRef，收集所有可达 PageRef
      对于每个 PageRef:
        a. 记录映射: mapping[pageID] = pInfo.chunkPos（或即将分配的 pos）
        b. 检查 pInfo.chunkPos == 0（脏页）?
           是 → serializer.Serialize(pagePtr) → buf (4100字节)
                pos = cm.Allocate(PageSize, pageType)
                cm.WritePage(pos, buf)
                pInfo CAS: chunkPos = pos（标记为已持久化）
                mapping[pageID] = pos（更新为新的持久化位置）
           否 → 跳过（已在 AO 中持久化）
  T3: 持久化 FreeList 到各 Chunk 文件尾部
  T4: cm.Sync()                                     ← AO 文件 fsync
  T4a: pageLocs 批量更新 ← mapping                  ← AO Sync 后立即刷新映射（WAL 写入前）
  T5: wal.Append(CheckpointEntry{startLSN, rootPageID, mapping}) ← 授权 + 映射
  T6: wal.Sync()                                    ← WAL 持久化
  T7: wal.Truncate(startLSN)                        ← WAL GC
```

`pageLocs` 在 AO Sync 后、WAL Append 前更新（T4a），确保内存中的映射表与 AO 文件状态一致。若后续 WAL Append 失败（T5-T6），pageLocs 中有新增映射条目但 WAL 中没有对应的 CheckpointEntry，下次恢复时这些条目不被识别，对应的 AO 位置会被 FreeList 扫描标记为"未使用"——安全回退，无数据丢失。

**CheckpointEntry 扩展格式**（WAL entry Key 区域）：
```
[StartLSN:8][RootPageID:4][MappingCount:4]
[Mapping: MappingCount × (PageID:4 + ChunkPos:8)]
[CRC32C:4]
```

`mapping` 表是惰性加载和 BTree 重建的核心——它将每个逻辑 PageID 映射到 `.ao` 文件中的物理位置。Recovery 时，此映射被加载到 `OffheapBTreeStorage` 的内存中，惰性加载路径从此映射查找 `ChunkPosition`。

**关键约束**（来自 Phase 3 C1/C2）：
1. **checkpointStartLSN 必须在根快照之前记录**：防止快照后新写入的 WAL entry 被错误截断
2. **页面必须在 WAL 授权之前持久化（先 AO 后 WAL）**：若先写 WAL 再写 AO，AO 写入失败时 WAL 已被截断，页面数据丢失
3. **AO Sync 是屏障**：保证所有页面数据落盘后，才写入 Checkpoint 授权条目

**Checkpoint 触发条件**（对齐 Lealone `CheckpointService`）：

| 触发条件 | Lealone | NexKV Phase 4 |
|---------|---------|--------------|
| 周期 | 默认 12h (`checkpoint_period`) | 默认 30s (`Config.Interval`) |
| 脏内存阈值 | `collectDirtyMemory() > cacheSize` | `dirtyMemory > CacheSize` (新增) |
| 强制 | SQL CHECKPOINT / 关闭 | `ForceCheckpoint()` / Shutdown |
| WAL 写入计数 | 每 512 个事务批次 | 保留 (可配置)

### 5.3 惰性页面加载协议

**核心设计**：`OffheapBTreeStorage` 内部维护一个 `pageID → ChunkPosition` 映射表。正常运行时通过 Checkpoint 时的 DFS 遍历更新；Recovery 时从 CheckpointEntry 恢复。

```go
// OffheapBTreeStorage 内部字段（Phase 4 新增）
type OffheapBTreeStorage struct {
    // ... 现有字段 ...
    pageLocs sync.Map  // map[uint32]ChunkPosition — pageID → .ao 物理位置
}
```

**惰性加载路径**（从 `pageLocs` 映射查找，而非从 PageHeader.chunkPos 读取）：

```
BTree 操作 (Get/Set/Delete) 中的页面访问路径:

  GetLeafPage(pageID):
    1. 尝试从 PageManager 获取内存页面
       a. 命中 → 返回
       b. 未命中 → 进入惰性加载
    2. 从 pageLocs 映射查找 ChunkPosition
       a. pageLocs.Load(pageID) → nil → 页面尚未持久化（新分配，仅在 Recovery 前出现）
       b. pageLocs.Load(pageID) → pos → cm.ReadPage(pos) → deserialize → 分配 mmap → 返回
    3. 验证反序列化后 PageHeader.chunkPos == pos（额外的完整性校验）

  CopyLeafPage(srcID) (COW 写路径):
    1. AllocLeafPage() → 分配新 mmap 页面
    2. memcpy 4100 字节从 src → dst（包含 CRC）
    3. pageLocs.Store(newPageID, ChunkPosition(0)) // 标记为"脏"
    4. 设置新版 version
    5. 返回新页面

  Checkpoint 后更新:
    页面刷新到 AO 后 → pageLocs.Store(pageID, newChunkPos)
```

**为什么用 pageLocs 映射而不是从 PageHeader.chunkPos 读取**：
- 重启后 mmap 区域清空，PageHeader 中所有字段归零 — 包括 chunkPos
- 此时 `chunkPos == 0` 无法区分"新分配未持久化"和"已持久化但 mmap 未加载"
- `pageLocs` 映射由 CheckpointEntry 恢复，独立于物理 mmap 页面生命周期
- `PageHeader.chunkPos` 保留作为**辅助校验字段**：惰性加载后验证 `header.chunkPos == pageLocs.Load(pageID)`，检测映射表与页面数据的不一致

### 5.4 WAL-AO 并发模型

```
Commit 路径 (WAL-first, AO-later):

  1. PreCheck（冲突检测）
  2. commitTS = tsGen.NextTS()
  3. WALAppendItem:
     - LSN 分配 + 写入 WAL（通过 TaskScheduler, ShardIDWAL=1）
     - Batch fsync（Group Commit: 16 条目或 1ms）
  4. Apply WriteBuffer:
     a. 同步（默认）: VersionChain.Prepend → BTree.Set
        - BTree.Set 可能触发 COW → 分配新页面（chunkPos=0）
        - 新页面在下次 Checkpoint 之前是"脏页"
     b. 异步: Enqueue BTreeApplyItem
```

**核心原则**：
- **WAL 是持久性的唯一来源**：提交的事务在 WAL Sync 后即视为持久化
- **AO 是恢复加速器**：Checkpoint 将内存页面快照到 AO，减少恢复时需重放的 WAL 量
- **AO 写入不在热路径上**：页面写入 AO 是批量的、异步的 Checkpoint 操作

### 5.5 I/O 隔离

| 文件类型 | 写入模式 | Sync 策略 | 文件描述符 |
|---------|---------|----------|-----------|
| `.wal` | 顺序追加 | Group Commit (1ms/batch) | 每 Segment 1 个 |
| `.ao` | 随机写（Checkpoint 时） | Checkpoint 结束时 fsync | 每 Chunk 1 个 (最多 8 个) |

WAL 和 AO 使用独立的文件描述符和 I/O 路径，互不阻塞。

---

## 六、与现有代码的集成点

### 6.1 PageHeader 添加 chunkPos

```go
// internal/infrastructure/storage/offheap/page_layout.go

type PageHeader struct {
    // ... 现有字段保持不变 ...
    version        uint64   // COW 版本号
    prevPage       uint32   // 前一个页面
    nextPage       uint32   // 后一个页面
    extraChild     uint64   // 内部节点 N+1 子节点
    count          uint16   // 条目数
    pageType       uint8    // 0=内部节点, 1=叶子
    deleted        uint8    // 删除标记
    tombstoneCount uint16   // 墓碑计数
    deleteEpoch    uint64   // 删除纪元
    // +++ Phase 4 新增 +++
    chunkPos       uint64   // .ao 文件中的 ChunkPosition (0 = 未持久化)
    // +++ end +++
    _              [8]byte  // 填充（从 16 字节减少到 8 字节）
}
// SizeofPageHeader 保持不变 (56 字节)
```

`chunkPos` 使用 Header 中原本 16 字节填充区域的 8 字节，剩余 `[8]byte` 填充。保持 `HeaderSize` 不变（`btree.HeaderSize == offheap.SizeofPageHeader == 56`）。

### 6.2 OffheapBTreeStorage 适配

```go
// internal/infrastructure/storage/btree/offheap_storage.go

type OffheapBTreeStorage struct {
    pm         *offheap.PageManager     // mmap 页面分配器
    cm         chunk.ChunkManager       // .ao 文件管理器 (Phase 4 新增)
    serializer *chunk.PageSerializer    // 页面序列化器
    pageLocs   sync.Map                 // map[uint32]ChunkPosition (Phase 4 新增)
    closed     atomic.Bool
}

// GetLeafPage 增加惰性加载路径:
//   1. 尝试从 PageManager / 缓存获取
//   2. 未命中 → 从 ChunkManager 惰性加载
func (s *OffheapBTreeStorage) GetLeafPage(pageID model.PageID) (LeafPage, error)

// CopyLeafPage (COW) 创建新页面:
//   - 分配新 mmap 页面
//   - chunkPos = 0 (标记为"脏"，待 Checkpoint 持久化)
func (s *OffheapBTreeStorage) CopyLeafPage(srcID model.PageID) (model.PageID, LeafPage, error)
```

### 6.3 CheckpointManager 集成

**接口扩展需求**：现有 `checkpoint.PageRef` 接口仅提供 `PageID()/IsLeaf()/ChildIDs()` 三个方法，不足以支撑 Phase 4 的页面刷新。需要新增 `BTreeScanner` 接口扩展和 `PageFlushItem` 返回值类型。

```go
// checkpoint 包接口扩展 (checkpoint_manager.go)

// BTreeScanner 扩展 — 增加 EnumeratePages 方法
type BTreeScanner interface {
    RootPage() PageRef
    // EnumeratePages 从根开始 DFS 遍历所有可达 PageRef，
    // 返回带完整页面信息的列表，用于 Checkpoint 的序列化+刷新。
    // 遍历过程中根快照由 COW 保证不可变性。
    EnumeratePages(root PageRef) ([]PageFlushItem, error)
}

// PageFlushItem 封装 Checkpoint 页面刷新所需的完整信息
type PageFlushItem struct {
    PageID   model.PageID       // 逻辑页面 ID
    PageType uint8              // 0=内部节点, 1=叶子
    PagePtr  unsafe.Pointer     // mmap 页面指针（用于序列化）
    ChunkPos ChunkPosition      // 当前 AO 位置 (0 = 脏页)
}

// PageRef 接口保持不变
type PageRef interface {
    PageID() model.PageID
    IsLeaf() bool
    ChildIDs() []model.PageID
}
```

**职责分离**：`EnumeratePages` 在 BTree 内部完成遍历（BTree 拥有 `PageManager` 和 `PageAccessor`），CheckpointManager 只需接收结果列表，不直接操作 mmap 指针。这避免了 CheckpointManager 的职责膨胀。

```go
// Manager 结构体 — Phase 4 修改
type Manager struct {
    wal        service.WAL
    btree      BTreeScanner          // 扩展: EnumeratePages
    cm         service.ChunkManager  // Phase 4 新增
    serializer *chunk.PageSerializer // Phase 4 新增
}

func (m *Manager) FuzzyCheckpoint() error {
    startLSN := m.wal.CurrentLSN()
    mapping := make(map[uint32]ChunkPosition)
    rootRef := m.btree.RootPage()
    items, _ := m.btree.EnumeratePages(rootRef)
    for _, item := range items {
        mapping[uint32(item.PageID)] = item.ChunkPos
        if item.ChunkPos == 0 {
            buf, _ := m.serializer.Serialize(item.PagePtr)
            pos, _ := m.cm.Allocate(PageSize, item.PageType)
            m.cm.WritePage(pos, buf)
            mapping[uint32(item.PageID)] = pos
        }
    }
    m.cm.Sync()
    entry := NewCheckpointEntry(startLSN, rootRef.PageID(), mapping)
    m.wal.Append(entry)
    m.wal.Sync()
    m.wal.Truncate(startLSN)
    return nil
}
```

### 6.4 Recovery 集成

Recovery 是整个 Phase 4 最复杂的部分——不仅需要重放 WAL，还需要从 AO 文件中重建 BTree 的完整 PageRef 图结构（`RootPageRef` → `PageRef` tree → `ChildrenCache` → `PageInfo`）。

#### 6.4.1 Recovery 启动时序状态机

```
Recovery 分为三个明确阶段：

┌──────────────────────────────────────────────────────────────┐
│ Phase A: 基础设施初始化（无 BTree 依赖）                       │
│   1. ChunkManager 初始化                                      │
│      - 首次启动: NewDiskChunkManager()                        │
│      - 重启恢复: RestoreDiskChunkManager() ← 扫描 .ao 文件    │
│        ├─ 超级块 CRC 有效 → 加载 FreeList（阶段1）            │
│        └─ 超级块 CRC 损坏 → FreeList 推迟到 Phase B（阶段2）  │
│   2. PageManager 初始化（匿名 mmap，空页面池）                  │
│   3. WAL 扫描: scanSegments() → 找最新 CheckpointEntry        │
├──────────────────────────────────────────────────────────────┤
│ Phase B: BTree 结构重建（从 Checkpoint + AO）                  │
│   ┌─ 条件分支:                                                │
│   │ 有 CheckpointEntry:                                       │
│   │   4. 解析 CheckpointEntry → rootPageID + pageLocs 映射    │
│   │   5. OffheapBTreeStorage.pageLocs ← mapping               │
│   │   6. FreeList 阶段2 补全（若 Phase A 跳过）:               │
│   │      扫描 Chunk 文件 → 排除 pageLocs 中的位置 → 入 FreeList│
│   │   7. RebuildBTree(rootPageID, pageLocs) → PageRef 图      │
│   │      （所有页面加载由 RebuildBTree 内部完成，不做预加载）    │
│   │      （见 6.4.2 完整算法）                                │
│   │   8. checkpointStartLSN 记录                               │
│   │                                                           │
│   └─ 无 CheckpointEntry (首次启动 / Phase 3 遗留):            │
│       4'. BTree 初始化为空（NewBTree，无 pageLocs）            │
│       5'. checkpointStartLSN = 0（全量 WAL 重放）             │
├──────────────────────────────────────────────────────────────┤
│ Phase C: 增量 WAL 重放（BTree 已就绪）                         │
│   9. 从 checkpointStartLSN 开始重放 WAL                        │
│      对于每个 entry:                                           │
│        a. 三阶段幂等检查 (beginTS vs commitTS)                  │
│        b. BTree.Set/Delete — 此时可正常触发惰性加载             │
│        c. 重建 VersionChain                                    │
│   10. 设置下一个 LSN                                           │
│   11. 恢复完成，引擎接受请求                                    │
└──────────────────────────────────────────────────────────────┘
```

#### 6.4.2 BTree PageRef 图重建算法

这是 Phase 4 恢复路径的核心。从物理 `.ao` 页面恢复到完整的 BTree 内存结构：

```go
// RecoveryManager.RebuildBTree(checkpointEntry, pageLocs) (*BTree, error)
//
// 输入:
//   - rootPageID: CheckpointEntry 中记录的根 PageID
//   - pageLocs:   CheckpointEntry 中的 pageID→ChunkPosition 映射表
//   - pm:         PageManager (空 mmap 池)
//   - cm:         ChunkManager (已从 .ao 文件 Restore)
//   - serializer: PageSerializer
//
// 输出:
//   - *BTree: 完整重建的 BTree，PageRef 图就绪
```

**重建算法流程**：

```
RebuildBTree(rootPageID, pageLocs, pm, cm, serializer):

  Step 1: 分配 root PageID 的 mmap 页面
    rootRawID = pm.Alloc()  // 分配物理 mmap 页
    加载根页面: chunkPos = pageLocs[rootPageID]
    data = cm.ReadPage(chunkPos)
    serializer.Deserialize(data, pm.PageIDToPtr(rootRawID))
    验证 pageType

  Step 2: 创建 RootPageRef
    rootPI = NewPageInfo(rootRawID, pageType, chunkPos, NodeRoot)
    rootRef = NewRootPageRef(rootPI)

  Step 3: 递归重建（BFS 或 DFS）
    queue = [(rootRef, rootPI, rootPageID)]
    while queue not empty:
      (parentRef, parentPI, parentPageID) = queue.pop()

      if parentPI.IsLeaf:
        continue  // 叶子节点无需子 PageRef

      // 从 mmap 读取内部节点的子节点 ID 列表
      parentPtr = pm.PageIDToPtr(parentPI.pageID)
      childIDs = ReadChildIDsFromIndexPage(parentPtr)

      // 为每个子节点创建 PageRef
      children = []
      for childPageID in childIDs:
        childChunkPos = pageLocs[childPageID]
        childRawID = pm.Alloc()  // 分配 mmap
        data = cm.ReadPage(childChunkPos)
        serializer.Deserialize(data, pm.PageIDToPtr(childRawID))
        childPI = NewPageInfo(childRawID, detectType(data), childChunkPos, NodeNormal)
        childRef = NewPageRef(childPI, parentRef)
        children = append(children, childRef)
        queue.push((childRef, childPI, childPageID))

      // 构建 ChildrenCache
      childPageRefs = extractPageRefs(children)
      separators = extractSeparatorKeys(parentPtr)
      cache = NewChildrenCache(childPageRefs, separators)
      parentRef.children.Store(cache)

  Step 4: 构建 pageID→PageRef 映射（用于叶子链接）
    pageRefMap := make(map[uint32]*PageRef)
    在 BFS 遍历过程中，每创建一个 PageRef 即记录:
      pageRefMap[childPageID] = childRef

  Step 5: 重建叶子节点链表
    对于 pageRefMap 中的每个叶子 PageRef:
      leafPageID = ref.PageID()
      physPtr = pm.PageIDToPtr(leafPageID)
      prevPageID = ReadPrevPage(physPtr)   // PageHeader.prevPage
      nextPageID = ReadNextPage(physPtr)   // PageHeader.nextPage
      if prevPageID != 0:
        ref.SetPrevLeaf(pageRefMap[prevPageID])
      if nextPageID != 0:
        ref.SetNextLeaf(pageRefMap[nextPageID])
    这保证了范围查询 (SeekFirst/SeekLast/叶级迭代器) 的正确性。

  Step 6: 设置 BTree 结构
    tree = &BTree{
      rootRef:   rootRef,
      storage:   storage,  // OffheapBTreeStorage（已注入 pageLocs）
      pageCount: len(pageLocs),
    }
    return tree
```

**关键约束**：
- **恢复阶段 BTree 不接受外部请求**：所有 API 返回 `ErrEngineNotReady`（与 Phase 3 C6-3 一致）
- **Recovery 单 goroutine 执行**：禁止并发调用（与 Phase 3 约束一致）
- **子节点分离器键从物理页面读取**：pageLocs 映射只包含 pageID→ChunkPosition，ChildrenCache 的 separator keys 需从父节点物理页面重新扫描
- **page data 中的 child pageID 不直接用于导航**：RebuildBTree 从物理页面读取 child pageID 仅用于 `pageLocs` 查找 ChunkPosition；导航通过 `ChildrenCache`（持有 `[]*PageRef`），不使用 page data 中的 child entry。旧物理页面中存储的 child pageID 与新分配的 mmap slot（`pm.Alloc()` 返回的 pageID）不同，但 `ChildrenCache` 持有正确的 `*PageRef` 指针，隔离了新旧 pageID 的不一致

**Recovery 恢复时长估算修正**（原 Phase 4.4 3-4 day → 修正为 5-7 day）：

Recovery 路径的实现复杂度远高于原估算，因为 BTree PageRef 图重建是额外的、不可缩减的工作量。修正后的 Phase 4.4 估算反映了这一现实。

### 6.5 领域层接口变更

新增文件：
- `internal/domain/service/chunk_manager.go` — `service.ChunkManager` 接口
- `internal/domain/service/chunk_manager_stats.go` — `ChunkManagerStats` 类型
- `internal/infrastructure/storage/btree/btree_rebuild.go` — `RebuildBTree()` 恢复时 PageRef 图重建

新增包：
- `internal/infrastructure/storage/chunk/` — `DiskChunkManager`, `ChunkFile`, `FreeList`, `PageSerializer`, `ChunkPosition`

修改文件：
- `internal/infrastructure/storage/btree/offheap_storage.go` — 增加 ChunkManager + pageLocs + 惰性加载
- `internal/infrastructure/storage/offheap/page_layout.go` — PageHeader 增加 chunkPos（`_[8]byte` 填充）
- `internal/infrastructure/storage/checkpoint/checkpoint_manager.go` — 页面刷新 + mapping 表集成
- `internal/infrastructure/storage/wal/recovery.go` — RecoveryManager + 三阶段恢复

---

## 七、架构图

### 7.1 Phase 4 组件架构

```
┌──────────────────────────────────────────────────────────────────┐
│                      NexKV Phase 4 存储架构                        │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌─────────────┐    ┌──────────────┐    ┌──────────────────┐     │
│  │ MVCC 事务层  │    │ BTree CCOW   │    │  VersionChain    │     │
│  │ (txManager)  │───▶│ (B+Tree)     │───▶│  (MVCC 版本)     │     │
│  └──────┬───────┘    └──────┬───────┘    └──────────────────┘     │
│         │                    │                                     │
│         │ WALCommit          │ GetLeafPage(惰性加载)               │
│         ▼                    ▼                                     │
│  ┌──────────────┐   ┌─────────────────┐                          │
│  │    WAL       │   │ OffheapBTree     │                          │
│  │  (DiskWAL)   │   │    Storage       │                          │
│  │              │   │ ┌─────────────┐  │                          │
│  │  .wal 文件   │   │ │ PageManager │  │    mmap (MAP_ANON)       │
│  │              │   │ │ (匿名 mmap)  │◀─┼── 内存页面池              │
│  │  段轮转      │   │ └──────┬──────┘  │                          │
│  │  CRC32C     │   │        │          │                          │
│  │  Group Commit│  │        │ 惰性加载   │                          │
│  └──────┬───────┘   │        ▼          │                          │
│         │           │ ┌─────────────┐  │                          │
│         │           │ │ CHUNK MGR   │  │                          │
│         │           │ │ (DiskChunk)  │  │                          │
│         ▼           │ └──────┬──────┘  │                          │
│  ┌────────────────┐ └───────┼─────────┘                          │
│  │ CHECKPOINT     │         │                                     │
│  │ Manager        │         │                                     │
│  │                │         │                                     │
│  │ 页面刷新 +     │         │    .ao 文件                          │
│  │ WAL 授权 +     │◀────────┼── btree_NNNN.ao                     │
│  │ WAL 截断       │         │   fallocate + append-only           │
│  └────────────────┘         │                                     │
└──────────────────────────────────────────────────────────────────┘
```

### 7.2 惰性页面加载流程

```
读取者 (Get):
  1. searchPath(key) → PageRef → LoadPageInfo()
  2. pInfo.page == nil?
     ├─ 否 → 返回缓存的页面指针（快速路径）
     └─ 是 → 惰性加载:
          ├─ pageLocs.Load(pageID) → pos
          │   ├─ pos == 0? → 页面未持久化（新分配或 Recovery 前的脏页）
          │   └─ pos != 0 → cm.ReadPage(pos)
          │                  → serializer.Deserialize(data, pagePtr) [4100→4096]
          │                  → 验证 PageHeader.chunkPos == pos
          │                  → pInfo CAS(page=ptr)
          │                  → 返回

写入者 (Set/COW):
  1. 分配新 mmap 页面 (pm.Alloc())
  2. memcpy 4096 字节 (COW)
  3. pageLocs.Store(newPageID, ChunkPosition(0)) // "脏"标记
  4. CAS 发布新 PageInfo
  5. 返回（页面在 Checkpoint 之前保持"脏"状态）

Checkpoint (页面刷新 + 映射更新):
  1. 加载根快照
  2. 对每个可达 PageRef:
     a. 记录到 mapping
     b. chunkPos == 0? → serializer.Serialize(4100字节) → cm.WritePage → CAS chunkPos
  3. 持久化 FreeList → cm.Sync()
  4. WAL CheckpointEntry{startLSN, rootPageID, mapping} → WAL.Sync()
  5. pageLocs 批量更新 ← mapping（运行时映射刷新）
  6. WAL.Truncate(startLSN)
```

### 7.3 页面生命周期状态机

```
                    Alloc()
                       │
                       ▼
               ┌───────────────┐
       ┌──────▶│   New (脏)    │
       │       │ chunkPos = 0  │ ← 新分配或在 AO 中尚未持久化
       │       └───────┬───────┘
       │               │
       │        Checkpoint 刷新
       │               │
       │               ▼
       │       ┌───────────────┐
       │       │  Persisted    │ ← .ao 文件中已持久化
       │       │ chunkPos ≠ 0  │
       │       └───────┬───────┘
       │               │
       │         COW 复制 (旧页面可被释放)
       │               │
       │               ▼
       │       ┌───────────────┐
       └───────│   Freed       │ ← 返回空闲列表
               └───────────────┘
```

### 7.4 检查点恢复时序

```
正常操作 (Checkpoint):
  T0: checkpointStartLSN = wal.CurrentLSN()      ← 先记录
      mapping := map[uint32]ChunkPosition{}       ← 映射表
  T1: rootRef = LoadPointer(&tree.root)           ← COW 快照
  T2: DFS 遍历 + 脏页刷新到 .ao 文件
      mapping[pageID] = chunkPos                  ← 记录映射
  T3: cm.Sync() (FreeList + 页面数据)             ← .ao fsync 屏障
  T4: wal.Append(CheckpointEntry{rootPageID, mapping}) ← 授权+映射
  T5: wal.Sync()                                  ← 授权持久化
  T6: wal.Truncate(startLSN)                      ← WAL GC

崩溃恢复 (Recovery):
  Phase A — 基础设施:
    1. RestoreDiskChunkManager() ← 扫描 .ao 文件
    2. PageManager 初始化 (空 mmap)
    3. 扫描 .wal → 最新 CheckpointEntry
  Phase B — BTree 结构重建:
    4. 解析 CheckpointEntry → rootPageID + pageLocs 映射
    5. 预加载根页面 + 直接子页面到 PageManager
    6. RebuildBTree(rootPageID, pageLocs) → PageRef 图 + ChildrenCache
    7. BTree 就绪，记录 checkpointStartLSN
  Phase C — 增量 WAL 重放:
    8. 从 checkpointStartLSN 开始重放 WAL
       对于每个 entry:
         - BTree.GetWithMeta → 三阶段幂等检查
         - BTree.Set/Delete → 正常惰性加载（BTree 已就绪）
         - VersionChain.Prepend
    9. 设置下一个 LSN → 恢复完成 → 引擎接受请求
```

---

## 八、关键技术决策

### 决策 1：页面序列化格式 — 就地变长格式（对齐 Lealone）

| 选项 | 描述 | 优点 | 缺点 |
|------|------|------|------|
| **A. 就地变长（推荐）** | 磁盘格式 = [CRC32C:4] + [内存 PageHeader+Data:pageLength]，pageLength 可变 | 不浪费空间（小页面不填充） | 需要 pagePositionToLengthMap |
| B. 固定 4100 字节 | 所有页面统一大小 | 实现简单 | 小页面浪费磁盘 I/O |

**决定**：选项 A（对齐 Lealone 变长页面）。页面长度由实际数据量决定，存储在 `pagePositionToLengthMap` 中。序列化时从 `pageLength` 参数确定输出大小，反序列化时返回实际长度。

### 决策 1a：Chunk 头部格式 — 文本键值对 + Fletcher32 + 双块写入（对齐 Lealone）

**决定**：采用 Lealone Chunk 头部格式 — 文本键值对（`DataUtils.appendMap/parseMap` 的 Go 对应） + Fletcher32 校验和。头部写两次（2 × 4096 字节），崩溃时任一完好块即可恢复。无 Magic Number，恢复基于文件名序列号扫描。

### 决策 1b：RemovedPages 替代 FreeList（对齐 Lealone）

**决定**：使用 `removedPages` 集合（`map[ChunkPosition]struct{}`）记录已删除页面位置，而非显式 FreeList。空间复用由 `ChunkCompactor` 重写低填充率 Chunk 时合并 `removedPages` 实现。初始实现中 `removedPages` 在 Chunk 文件尾部持久化，Compactor 推迟到 Phase 5。

### 决策 1c：Children-First 递归写入（对齐 Lealone）

**决定**：`NodePage.write()` 先递归写入所有子页面（`writeChildren`），获取子页面 pos 数组，再写父页面数据。这保证磁盘上父页面始终持有有效的子页面位置。对齐 Lealone `NodePage.java` 的 children-first 顺序。

### 决策 1d：PageListener 父链脏页传播（对齐 Lealone）

**决定**：Phase 4 实现显式的 `PageListener` 父链机制。每个 `PageRef` 持有 `PageListener`（包含父节点引用），`markDirtyPage()` 自底向上沿链 CAS 标记，每层使用 Listener 版本号检测并发 GC/分裂。对齐 Lealone `PageReference.markDirtyPage0()` 的三路返回值（0=成功, 1=GC'd, 2=Split）。

### 决策 1e：Checkpoint 脏内存阈值触发（对齐 Lealone）

**决定**：Checkpoint 触发条件增加「脏内存超过 `cacheSize`」阈值（对齐 Lealone `CheckpointService.collectDirtyMemory()`），保留固定 30s 周期作为兜底。

### 决策 2：惰性加载入口点

**决定**：在 `OffheapBTreeStorage.GetLeafPage()/GetNodePage()` 层面实现，而非在 `PageRef.GetOrReadPage()` 中。遵循现有架构模式——`OffheapBTreeStorage` 已经在 BTree 逻辑和物理存储之间充当桥接层。对 BTree 遍历逻辑透明。

### 决策 3：页面驱逐策略 — 仅 Checkpoint

**决定**：Phase 4 从「仅 Checkpoint」开始。页面在 Checkpoint 之前全部保留在内存中；Checkpoint 将它们写入 `.ao` 文件后，页面继续保持在内存中（缓存）。LRU 驱逐延迟到 Phase 5+。

### 决策 4：检查点顺序 — 先页面后授权

**决定**：先刷新所有脏页到 AO，再写 WAL Checkpoint 授权条目。这保证：
- WAL Checkpoint 条目存在 → 所有页面已安全写入 AO
- 页面可在 Checkpoint 后立即从 WAL 截断的安全点恢复

### 决策 5：文件描述符管理

| 文件类型 | 策略 | 原因 |
|---------|------|------|
| `.wal` | 每 Segment 1 个 fd，顺序写入 | 追加写入，低 fd 压力 |
| `.ao` | 每 Chunk 1 个 fd，随机读取，追加写入 | 最多 8 个 fd，池化管理 |

Chunk 文件在其生命周期内保持打开，正常关闭时由生命周期管理器关闭。

---

## 九、实施阶段

### Phase 4.1：ChunkManager + 页面序列化（5-7 天）

**新建文件**：
- `internal/domain/service/chunk_manager.go` — ChunkManager 接口
- `internal/infrastructure/storage/chunk/disk_chunk_manager.go` — 具体实现
- `internal/infrastructure/storage/chunk/chunk_file.go` — ChunkFile 包装器
- `internal/infrastructure/storage/chunk/chunk_position.go` — ChunkPosition 编码/解码
- `internal/infrastructure/storage/chunk/free_list.go` — 空闲列表管理
- `internal/infrastructure/storage/chunk/page_serializer.go` — 序列化/反序列化
- `internal/infrastructure/storage/chunk/chunk_compactor.go` — 压缩器存根

**交付物**：
- [ ] ChunkManager 接口定义在领域层
- [ ] DiskChunkManager：`.ao` 文件创建、追加写入、随机读取
- [ ] PageSerializer：mmap ↔ 磁盘格式往返（CRC32C 校验）
- [ ] FreeList 页面复用
- [ ] 单元测试：`TestDiskChunkManagerRoundtrip`, `FuzzDiskChunkManagerRoundtrip`
- [ ] `go test -race -count=5` 通过
- [ ] `goleak.VerifyTestMain(m)` goroutine 泄漏检查

**验证标准**：
- [ ] 页面序列化往返：写入 → 读取 → 逐位一致
- [ ] CRC32C 检测损坏页面
- [ ] Chunk 轮转：写满当前 Chunk 后自动创建新 Chunk
- [ ] 空闲列表：FreePage → 后续 Allocate 复用该位置

### Phase 4.2：惰性页面加载（3-4 天）

**修改文件**：
- `internal/infrastructure/storage/btree/offheap_storage.go`
- `internal/infrastructure/storage/offheap/page_layout.go` — 添加 chunkPos 字段
- `internal/infrastructure/storage/btree/btree.go` — NewBTree 接受 ChunkManager 参数

**交付物**：
- [ ] `OffheapBTreeStorage` 集成 `ChunkManager`
- [ ] `GetLeafPage`/`GetNodePage` 惰性加载路径
- [ ] `CopyLeafPage`/`CopyNodePage` 设置 `chunkPos = 0`
- [ ] PageHeader.chunkPos 字段（保持 HeaderSize = 56）
- [ ] 测试：进程内写入 → 触发惰性加载 → 数据一致

**验证标准**：
- [ ] 惰性加载页面内容与原始 mmap 页面逐位一致
- [ ] COW 新页面 chunkPos == 0（脏标记）
- [ ] 并发 Get 在惰性加载期间无竞争（`go test -race`）

### Phase 4.3：Checkpoint + 页面刷新（4-5 天）

**修改文件**：
- `internal/infrastructure/storage/checkpoint/checkpoint_manager.go` — 重大修改
- `internal/infrastructure/storage/checkpoint/config.go` — 添加 AO 配置

**交付物**：
- [ ] 模糊检查点刷新脏页到 `.ao` 文件
- [ ] `chunkPos == 0` 脏页检测 + 序列化 + 写入
- [ ] `cm.Sync()` AO 屏障 + WAL 授权 + 截断
- [ ] 锐检查点：暂停写入，刷新所有页面
- [ ] 测试：`TestCheckpointFlushDirtyPages`, `TestCheckpointRecoveryCycle`

**验证标准**：
- [ ] 模糊检查点在并发 COW 期间正确刷新所有脏页
- [ ] Checkpoint 后 WAL 截断删除授权行
- [ ] 崩溃 → 恢复 → WAL 重放 → 数据完整性
- [ ] `go test -race` 检查点 + 并发写入通过

### Phase 4.4：恢复 + 协调（**5-7 天**，原估 3-4 天，BTree 图重建增加 2-3 天）

**修改文件**：
- `internal/infrastructure/storage/wal/recovery.go` — `RecoveryManager` + `RebuildBTree()`
- `internal/infrastructure/storage/btree/btree_rebuild.go` — **新建**：PageRef 图重建算法
- `internal/infrastructure/storage/mvcc/wal_integration.go` — 适配惰性加载

**交付物**：
- [ ] CheckpointEntry 扩展格式（rootPageID + pageLocs 映射）
- [ ] Recovery 三阶段状态机（基础设施 → BTree 重建 → WAL 重放）
- [ ] `RebuildBTree()` — 从 AO 页面递归重建 PageRef 图 + ChildrenCache
- [ ] Recovery 路径惰性加载 BTree 页面（使用 pageLocs 映射）
- [ ] 三阶段幂等检查与惰性加载协调
- [ ] 叶子链表（prevPage/nextPage）重建
- [ ] 端到端崩溃恢复测试

**验证标准**：
- [ ] 完整崩溃恢复循环：写入 100K keys → Checkpoint → kill -9 → 重启 → 数据完整
- [ ] WAL 在 Checkpoint 后截断到授权位
- [ ] 有 Checkpoint 时恢复时间显著短于无 Checkpoint
- [ ] Recovery 期间所有 API 返回 `ErrEngineNotReady`
- [ ] 无 Checkpoint（纯 WAL）恢复路径仍正常工作

### 时间估算（修正后）

```
Phase 4.1: ChunkManager + 序列化    ████████         5-7 天
Phase 4.2: 惰性加载 + pageLocs      █████            4-5 天
Phase 4.3: Checkpoint + 页面刷新    ██████           4-5 天
Phase 4.4: 恢复 + BTree 重建        ███████          5-7 天
                                    ─────────────────
                                    总计: 18-24 天
```

> **时间修正说明**：Phase 4.4 原估 3-4 天，经审查发现 BTree PageRef 图重建是额外不可缩减的工作量——需要从物理 `.ao` 页面递归重建 `RootPageRef → PageRef tree → ChildrenCache → PageInfo` 的完整结构。增加 2-3 天反映这一现实。

---

## 十、风险和未解决问题

### 10.1 高风险

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| **惰性加载竞争**：CAS 替换 `pInfo.page` 时与并发读取者冲突 | 中 | 高 | 使用现有 `PageRef.refCount` 做生命周期保护；COW 期间不触发惰性加载 |
| **检查点与并发 COW**：DFS 遍历时写入者创建新页面 | 高 | 低 | COW 保证旧根可达子树不变；新页面在下次 Checkpoint 刷新 |
| **mmap 地址空间耗尽**：惰性加载分配新 mmap 页面 | 中 | 中 | 仅 Checkpoint 策略限制内存使用；可配置 `MaxLoadedPages` |
| **检查点延迟**：大数据集下 DFS + 序列化 + I/O 可能达到秒级 | 中 | 中 | 模糊检查点不阻塞写入；间隔可配置（默认 30s） |
| **部分写入的 AO 页面**：Checkpoint 期间崩溃导致 AO 文件尾部有残缺页面 | 低 | 高 | 在 Chunk 文件尾部增加 Trailer 标记；Recovery 时校验并忽略 |

### 10.2 开放问题

1. **Q1**：页面驱逐策略——何时以及如何从内存中清除已持久化的页面？（推迟到 Phase 5）
2. **Q2**：`PageHeader` 中 `chunkPos` 的精确位偏移量——是否与 `offheap.SizeofPageHeader` 对齐？（需要检查现有填充布局）
3. **Q3**：Checkpoint 期间的 I/O 优先级——Checkpoint 页面写入 vs WAL Group Commit 的调度策略
4. **Q4**：AO 文件中部分写入页面的处理——Checkpoint 崩溃后如何安全恢复？
5. **Q5**：`ChunkCompactor` 压缩时如何原子更新所有受影响 PageRef 的 `chunkPos`？

---

## 十一、验证策略

### 单元级

- `TestDiskChunkManagerRoundtrip` / `Fuzz`：写入 → 读取 → CRC 校验
- `TestPageSerializeDeserialize`：所有页面类型（叶子、内部节点）精确位匹配
- `TestLazyLoadFromAO`：写入 AO → 清除 mmap → 惰性加载 → 位匹配
- `TestCheckpointPageFlush`：COW → 检查点 → 刷新 → chunkPos 非零

### 集成级

- `TestCheckpointRecoveryCycle`：写入 100K keys → 检查点 → 重启 → 验证所有 key
- `TestCrashDuringCheckpoint`：在检查点不同阶段模拟崩溃 → 验证恢复正确性
- `TestConcurrentReadDuringCheckpoint`：检查点 DFS 期间进行 Get 操作

### 性能级

- `BenchmarkPageSerialize` / `BenchmarkPageDeserialize` — 预期 < 1μs/op
- `BenchmarkCheckpointTime(pageCount)` vs 总数据大小 — 确定可扩展性基线
- `BenchmarkLazyLoadLatency` vs 内存中 Get — 预期 ~5-10μs 额外延迟（读取 + 反序列化 + mmap 分配）
- `BenchmarkRecoveryTime` — 有 Checkpoint vs 无 Checkpoint 对比

### Fuzz 测试

- `FuzzDiskChunkManagerRoundtrip`：随机页面内容 → 序列化 → 反序列化 → 内容校验
- `FuzzCheckpointRecovery`：随机写入序列 → 检查点 → 模拟崩溃 → 恢复 → 数据完整性
- `FuzzLazyLoadConcurrent`：惰性加载与并发 COW 写入的并发 fuzz

---

## 十二、参考资料

### 项目内文档
- [Phase 3 WAL+GC Spike](../btree-refactor/2026-04-17-phase3-wal-gc-spike.md) — WAL 集成、Recovery、GC 设计
- [Lealone 源码分析](../btree-porting/2026-03-09-day1-2-lealone-source-analysis.md) — CCOW、PageInfo.pos 编码
- [BTree Page 重构 Phase 1](../../06_PM/feature/2026-03-12_PR-088_BTree-Page-重构-Phase1_全流程.md) — ChunkManager 原始设计
- [BTree PageID Refactor](../../06_PM/feature/2026-03-10_btree-pageid-refactor_full.md) — PageID 与持久化的关系

### 项目内代码
- `internal/domain/service/wal.go` — WAL 领域接口（对等先例）
- `internal/infrastructure/storage/btree/offheap_storage.go` — 主要集成点
- `internal/infrastructure/storage/checkpoint/checkpoint_manager.go` — 检查点改造点
- `internal/infrastructure/storage/offheap/page_layout.go` — PageHeader 布局
- `internal/infrastructure/storage/wal/recovery.go` — WAL 恢复逻辑

### 外部参考
- Lealone 存储引擎：https://github.com/lealone/lealone
- Knuth, D. "The Art of Computer Programming, Vol. 3: Sorting and Searching" (1973) — B* Tree
- CRC32C (Castagnoli)：IETF RFC 3720

---

**文档版本**: v1.0
**创建日期**: 2026-05-16
**分支**: `spike/btree-wal-ao-persistence`
**状态**: Draft
**作者**: Claude Code

---

## 十三、附录 C：Lealone 源码对齐差异总表

基于 Lealone 源码 (`lealone-aose/src/main/java/com/lealone/storage/aose/btree/`) 的精确分析，与 NexKV Phase 4 设计对齐。

| # | 维度 | Lealone 源码 | NexKV Phase 4 对齐 | 状态 |
|---|------|-------------|-------------------|------|
| 1 | Chunk 命名 | `c_[id]_[seq].db` | `btree_[id]_[seq].ao` | ✅ 对齐（扩展名差异化） |
| 2 | 超级块 | 无 — 序列号扫描恢复 | 无 — 序列号扫描 + 双块头部 | ✅ 对齐 |
| 3 | 头部格式 | 文本键值对 + Fletcher32 | 文本键值对 + Fletcher32 | ✅ 对齐 |
| 4 | 头部大小 | 8KB (2 × 4096 双块) | 8KB (2 × 4096 双块) | ✅ 对齐 |
| 5 | 页面大小 | 变长 (pageLength) | 变长 (pageLength) | ✅ 对齐（Phase 4 改动） |
| 6 | 页面→长度映射 | pagePositionToLengthMap | pagePosToLen map | ✅ 对齐 |
| 7 | 脏标记 | `pos == 0` (PageInfo.isDirty) | `ChunkPosition == 0` | ✅ 对齐 |
| 8 | 空闲空间 | removedPages + Compactor | removedPages + Compactor (Phase 5) | ✅ 对齐 |
| 9 | maxChunks | 无硬限制 | 无硬限制 | ✅ 对齐（Phase 4 改动） |
| 10 | Chunk 选择 | lastChunk 追加 / 新建 | lastChunk 追加 / 新建 | ✅ 对齐 |
| 11 | 写入顺序 | Children-First 递归 | Children-First 递归 | ✅ 对齐（Phase 4 新增） |
| 12 | 脏页传播 | PageListener 父链 CAS | PageListener 父链 CAS | ✅ 对齐（Phase 4 新增） |
| 13 | Checkpoint 触发 | 脏内存阈值 + 周期 + 强制 | 脏内存阈值 + 30s 周期 + 强制 | ✅ 对齐 |
| 14 | **WAL 位置** | 嵌入 Chunk 文件尾部 | **独立 .wal segment 文件** | ⚠️ NexKV 特有 |
| 15 | **CRC 算法** | Fletcher32 | **CRC32C (Castagnoli)** | ⚠️ NexKV 特有 |
| 16 | **原子操作** | AtomicReferenceFieldUpdater | **sync.Map / atomic.Value** | ⚠️ Go 惯用 |
| 17 | **页表映射** | pos 编码直接寻址 | **pageLocs sync.Map 额外映射** | ⚠️ NexKV 特有 |

**NexKV 特有设计理由**：
- **独立 .wal 文件**：Go 中已有成熟 DiskWAL + Group Commit，独立 Segment 管理优于嵌入 Chunk
- **CRC32C**：SSE4.2/ARM 硬件加速，与 WAL 模块统一（优于 Fletcher32 的软件计算）
- **pageLocs 映射**：重启后 mmap 清零，必须从 CheckpointEntry 恢复 pageID→pos 映射（Lealone 无此问题——Java 堆内存由 GC 管理，PageReference 对象重启后从文件重新创建）

**对齐覆盖率**：13/17 = 76%（4 项保留 NexKV 特有设计）

---

**文档版本**: v2.0
**创建日期**: 2026-05-16
**最后更新**: 2026-05-17 (Lealone 源码对齐)
**分支**: `spike/btree-wal-ao-persistence`
**状态**: Draft
**作者**: Claude Code
