# Spike：BTree LOB（Large Object）两级存储方案

> **日期**：2026-06-09  
> **分支**：`spike/btree-lob`  
> **Tier 1 参考**：[[2026-06-09-lob-implementation-design]] — Overflow Page 设计（Ledgers 集成、PageHeader 格式、GC 策略）  
> **Tier 1 参考**：[[2026-06-09-lob-scheme-investigation]] — Lealone LOB 方案深度调查  
> **背景**：当前 NexKV BTree 每条 record 必须在 4KB 单页内存储。MVCC value 约 50B，页面可存 ~80 entries。若 value 超过 ~3.9KB，insert 直接失败。

**两级存储策略**：
- **Tier 1 — Overflow Page（2KB ~ 64KB）**：数据存储在 mmap 溢出页链中，BTree 叶子页存 8B 引用。✅ 已实现
- **Tier 2 — LOB File（> 64KB）**：数据存储在独立文件中，BTree 叶子页存 16B 文件引用。✅ 已实现

---

## 一、问题定义

### 1.1 当前限制

```
当前 MVCC 格式：
  [Flag:1][prevFlag:1][prevBeginTS:8][prevValLen:2][prevVal:N][beginTS:8][realVal:M]
  total = 12 + prevVal + 8 + realVal
  单页容量: 4096B - PageHeader(56B) - 1 entry slot(16B) = 4024B
  → realVal ≤ 4024 - 12 - 8 = 4004B (Insert)

  value > 4004B → InsertLeafEntry 返回 page full → 整个操作失败
```

### 1.2 需求场景

| 场景 | value 大小 | 频率 | 存储方式 |
|------|:--:|:--:|------|
| 普通 KV | < 2KB | 99% | BTree 行内存储 |
| 中等对象 | 2KB ~ 64KB | 0.9% | **Overflow Page**（mmap 溢出页链）✅ |
| 大对象 (LOB) | 64KB ~ 1MB+ | 0.1% | **LOB File**（独立文件系统存储）✅ |

**两级分层理由**：
- Overflow Page（mmap 内）：适合中小对象，零系统调用，纳秒级延迟。但占用 mmap 配额，上限受 mmap 大小约束
- LOB File（文件系统）：适合大对象，不占 mmap 配额，利用 OS page cache。支持流式读写、GB 级对象

### 1.3 Lealone 方案（简要）

Lealone 在 `BTreeStorage` 层实现 LOB——**BTree 感知** LOB 存在：
- 阈值 2KB → value 存储在溢出页链中
- BTree 叶子页存储 21B LOB 引用（Flag:1 + Version:8 + FirstPageID:8 + Length:4）
- 溢出页：NextPageID:8 + ChunkSize:4 + Data:4084B
- 链式结构，按需分配，删除时沿链 free

---

## 二、NexKV LOB 架构设计

### 2.1 核心原则：BTree 完全透明

根据 btree2 设计决策 **D10**：

> **BTree 对 value 格式透明**：BTree 不感知 value 的内部结构（MVCC version、LOB 溢出页面指针等）。value 是 `[]byte` 黑盒，BTree 只负责按 key 索引存储。

**与 Lealone 的关键差异**：NexKV LOB 逻辑在 **MVCC codec 层**，BTree 零改动。

```
优势：
  - BTree 零改动（不需要 isLargeValue 判断）
  - MVCC 格式扩展自然（在现有编码体系中嵌入 LOB 引用）
  - 删除/回滚路径统一（BTree 只看到 value 替换）
  - 单一职责：BTree 专注索引，LOBManager 管大对象，ValueEncoder 管编解码
```

### 2.2 三层架构与职责边界

```
┌─────────────────────────────────────────┐
│          Ledgers / Application          │
├─────────────────────────────────────────┤
│         ValueEncoder (codec)            │ ← LOB 检测, 路由, 编解码
│         LOBManager (lifecycle)          │ ← 两级路由: OverflowPage / LOBFile
├─────────────────────────────────────────┤
│     ┌──────────────┐ ┌──────────────┐   │
│     │ OverflowPage  │ │  LOBFile     │   │ ← 两级存储后端
│     │ (mmap pages)  │ │  (disk files)│   │
│     └──────────────┘ └──────────────┘   │
├─────────────────────────────────────────┤
│         BTree (index only)              │ ← Key 索引, Split/Merge, CAS 并发
│         PageManager (storage)           │ ← 页面分配/释放/读写
│         mmap (offheap)                  │ ← 索引页 + 溢出页物理内存
│         File System (data/lob/)         │ ← LOB 文件物理存储
└─────────────────────────────────────────┘
```

| 模块 | 职责 | LOB 相关接口 |
|------|------|------|
| **BTree** | Key 索引、页面组织、CAS 并发 | 无——完全透明 |
| **ValueEncoder** | Value 编解码、两级路由、数据拼接 | `Encode/Decode`, `IsLOB` |
| **LOBManager** | 两级路由、生命周期管理 | `Allocate/Read/Free/Update` |
| **OverflowPage** | mmap 溢出页链（2KB~64KB）| `AllocOverflow/FreeOverflow/ReadOverflow` |
| **LOBFileManager** | LOB 文件存储（>64KB）| `CreateLOB/ReadLOB/DeleteLOB` |

### 2.3 LOBManager 接口（Tier 1）

```go
// LOBManager 管理大对象的存储和生命周期（Tier 1: overflow page chain）。
type LOBManager interface {
    Allocate(data []byte) (LOBRef, error)  // overflow page chain 分配
    Read(ref LOBRef) ([]byte, error)       // 沿链读取
    Free(ref LOBRef) error                 // 释放链式页面
    Update(data []byte, oldRef LOBRef) (LOBRef, error) // 释放旧→分配新
    Size(ref LOBRef) int64                 // 返回 TotalLen
}
```

> **Tier 2 扩展**：LOBFileManager（§4.5）独立接口，SnapshotTx 同时持有两个 Manager。
> ValueEncoder 根据阈值做两级路由（§3.4），Flags 0x02/0x03→LOBManager, 0x04/0x05→LOBFileManager。

---

## 三、存储格式

### 3.1 页面头部格式（复用现有 PageHeader）

LOB 溢出页**不引入新 PageHeader 格式**——复用现有 56 字节 PageHeader（`page_layout.go`）。

唯一变更：`pageType` 字段新增 `PageTypeOverflow = 2`（现有 0=Index/1=Leaf）。

溢出页的 NextPageID/ChunkSize/Checksum 存储于 metadata 区（Header 之后），不修改 PageHeader 结构体。

> **向前兼容**：BTree 叶子页/索引页的 PageHeader 格式不变。Checkpoint/WAL Recovery/Compaction 代码不受影响。

### 3.2 MVCC LOB 编码

新增以下 Flag 常量（当前代码不支持非 0x00/0x01 的 Flag，需修改 ParseMVCC 验证逻辑）：

| Flag | 值 | 含义 |
|------|:--:|------|
| `FlagNormal` | `0x00` | 普通数据 |
| `FlagTombstone` | `0x01` | 逻辑删除 |
| `FlagLOBNormal` | `0x02` | LOB 大对象（新增） |
| `FlagLOBTombstone` | `0x03` | LOB 大对象 Tombstone（新增，`0x02｜0x01`） |

> `FlagLOBTombstone=0x03` 用于 Delete LOB key 时写入 Tombstone 并保留 LOB 引用，供 epoch GC 延迟回收溢出页。

> **Tier 2 Flags**（设计阶段，ParseMVCC/BuildMVCC 验证已支持）：
> `FlagLOBFile=0x04` (LOB 文件), `FlagLOBFileTombstone=0x05` (0x04|0x01)。
> Tombstone 恢复（重新 Put 后）时从旧 Tombstone 提取 LOB flag。

```
普通 value:  [Flag:0x00][prevFlag][prevBeginTS][prevValLen][prevVal][beginTS][realVal]
LOB value:   [Flag:0x02][prevFlag][prevBeginTS][prevValLen][prevVal][beginTS]
             [lobRefLen:2][lobRef:8]
                lobRef = [FirstPageID:4][TotalLen:4]

  BTree 叶子页存储: ~31B (MVCC header 20B + lobRefLen 2B + lobRef 8B + beginTS 8B)
```


> **BuildMVCC 兼容性**：`lobRef = [FirstPageID:4][TotalLen:4]` (8 字节 `[]byte`) 作为现有 `BuildMVCC(flag, ts, realVal, prevFlag, prevBeginTS, prevVal)` 的 `realVal` 参数传入。**无需新增参数**——Flag 0x02 标识 `realVal` 是一份 LOB 引用而非原始 value。`ParseMVCC` 根据 Flag 决定如何解释 `realVal`（普通 value vs LOB 引用）。


**`MVCCValue` 扩展**：

```go
type LOBRef struct {
    FirstPageID uint32 // 溢出页链首页 ID
    TotalLen    uint32 // 原始数据总长度（最大 4GB）
}

type MVCCValue struct {
    Flag    byte
    BeginTS uint64
    RealVal []byte
    PrevFlag    byte
    PrevBeginTS uint64
    PrevVal     []byte
    LOB     *LOBRef     // non-nil when Flag is FlagLOBNormal or FlagLOBTombstone (Tier 1)
    LOBFile *LOBFileRef // non-nil when Flag is FlagLOBFile or FlagLOBFileTombstone (Tier 2)
}
```

> **RealVal 语义说明**：
> - `FlagNormal/Tombstone`: `RealVal` = 用户原始 value（mmap sub-slice）
> - `FlagLOBNormal/LOBTombstone`: `RealVal` = LOB 引用字节 `[lobRefLen:2][FirstPageID:4][TotalLen:4]`（8-10 bytes）
> - 调用方**不可**直接用 `mv.RealVal` 作为返回值——必须先检查 `mv.Flag & 0x02` 然后 `LOBManager.Read(mv.LOB)` 展开。推荐封装 `ValueEncoder.Decode(mv)` 统一处理。

**ParseMVCC Flag→LOBRef 解析**（`mvcc/codec.go` 中实现）：

```go
func ParseMVCC(val []byte) (MVCCValue, error) {
    // ... existing header parsing ...

    if flag == FlagLOBNormal || flag == FlagLOBTombstone {
        // Tier 1: realVal = [lobRefLen:2][FirstPageID:4][TotalLen:4]
        if len(realVal) >= 10 {
            mv.LOB = &LOBRef{
                FirstPageID: binary.BigEndian.Uint32(realVal[2:6]),
                TotalLen:    binary.BigEndian.Uint32(realVal[6:10]),
            }
        }
    } else if flag == FlagLOBFile || flag == FlagLOBFileTombstone {
        // Tier 2: realVal = [lobRefLen:2][LOBID:8][TotalLen:8]
        if len(realVal) >= 18 {
            mv.LOBFile = &LOBFileRef{
                LOBID:    binary.BigEndian.Uint64(realVal[2:10]),
                TotalLen: binary.BigEndian.Uint64(realVal[10:18]),
            }
        }
    }

    return mv, nil
}
```

### 3.3 溢出页面格式

```
Overflow Page (4KB):
  [PageHeader:56B][NextPageID:8B][ChunkSize:4B][Reserved:4B][Data:4024B]

  NextPageID: uint64, 0 = 链尾
  ChunkSize:  uint32, 本页实际数据大小（≤4024）
  Reserved:   uint32, 保留位（未来 CRC32C 校验和或压缩标志）
```

**完整 LOB 链式存储示例（15KB value）**：

```
BTree LeafPage:
  key="tx-12345"
  value=[0x02][prev...][beginTS][lobRefLen=8][FirstPageID=1001][TotalLen=15000]

OverflowPage 1001:  NextPageID=1002  ChunkSize=4024  Data=[0..4023]
OverflowPage 1002:  NextPageID=1003  ChunkSize=4024  Data=[4024..8047]  
OverflowPage 1003:  NextPageID=1004  ChunkSize=4024  Data=[8048..12071]
OverflowPage 1004:  NextPageID=0     ChunkSize=2928  Data=[12072..14999]

空间计算：ceil(15000 / 4024) = 4 页
```

### 3.4 两级阈值

| 阈值 | 值 | 说明 |
|------|:--:|------|
| `LOBSizeThreshold` | 2048 (2KB) | value > 2KB → Overflow Page（mmap 溢出页链）|
| `LOBFileThreshold` | 65536 (64KB) | value > 64KB → LOB File（独立文件系统存储）|

> **两级路由逻辑**（ValueEncoder 层）：
> ```
> len(value) ≤ 2KB     → BTree 行内存储
> 2KB < len(value) ≤ 64KB → Overflow Page（mmap 溢出页链）
> len(value) > 64KB    → LOB File（独立文件系统）
> ```
> 64KB 阈值确保 mmap 空间不会被少量大对象耗尽。默认 6GB mmap 中，64KB 阈值下即使 100% 溢出页也需 ~10 万个大对象才可能触及 mmap 上限。
> **注意**：溢出页当前不计算 CRC32C 校验和——mmap 内数据损坏极少发生，且上层可自行校验。Reserved 字段保留用于未来实现。

---

## 四、Tier 2 — LOB 文件存储格式 ✅

> **状态**：设计阶段，待实现
> **参考**：[[2026-06-09-lob-implementation-design]] — 遵循相同的 LOBManager 接口模式、删除顺序约束、GC 策略

### 4.1 设计目标

| 目标 | 说明 |
|------|------|
| **不占 mmap 配额** | LOB 文件存储在文件系统中，mmap 只存 BTree 索引页 + 溢出页 |
| **支持大对象** | 64KB ~ GB 级，流式读写 |
| **不可变写入** | 每个 LOB 写入后不可变（COW 语义），更新 = 新文件 + 旧文件 epoch GC |
| **崩溃安全** | 文件写入 + fsync 后原子可见 |
| **简单 GC** | epoch-based，unlink 即可回收 |
| **OS page cache** | 利用文件系统缓存，热点 LOB 自动驻留内存 |

### 4.2 存储布局

```
data/lob/                          ← LOB 根目录
├── 00/                            ← 目录分片（高 2 字节）
│   ├── 00/                        ← 目录分片（次高 2 字节）
│   │   ├── 0000000000000001.lob   ← LOB 文件（LOBID 为文件名）
│   │   ├── 0000000000000002.lob
│   │   └── ...
│   └── 01/
│       └── ...
├── 01/
│   └── ...
└── ff/
    └── ff/
        └── ffffffffffffffff.lob
```

**目录分片**：取 LOBID 的高 4 字节，拆为两级目录（每级 2 字节 = 65536 个目录）。

- 第一级：LOBID >> 16 & 0xFFFF（高 2 字节）
- 第二级：LOBID & 0xFFFF（低 2 字节）
- 每级 65536 个目录，总计 4G 个潜在叶子目录（实际按需创建）
- 假设 100 万 LOB，每目录 ~1 个文件——无性能压力
- 避免单目录百万文件的文件系统瓶颈

### 4.3 LOB 文件格式

每个 `.lob` 文件是一个独立的不可变文件：

```
┌──────────────────────────────────────┐
│          LOB File Header (40B)       │
├──────────────────────────────────────┤
│  Magic:      [4]byte = "NXLB"       │
│  Version:    uint16 = 1             │
│  Flags:      uint16                 │
│    bit 0: Tombstone (已删除)         │
│    bits 1-15: reserved              │
│  LOBID:      uint64 (8B)            │
│  DataLen:    uint64 (8B)            │
│  DataCRC:    uint32 (4B, CRC32C)    │
│  Reserved:   [6]byte (padding)      │
│                                     │
│  Total: 4+2+2+8+8+4+6 = 34B        │
│  → aligned to 40B (round up)        │
├──────────────────────────────────────┤
│          Raw Data (DataLen bytes)    │
│          [byte][byte]...            │
└──────────────────────────────────────┘
```

**Header 字段说明**：

| 字段 | 大小 | 说明 |
|------|:--:|------|
| Magic | 4B | `NXLB` — 文件类型标识，防止误读 |
| Version | 2B | 格式版本，当前 = 1 |
| Flags | 2B | bit0=Tombstone（GC 标记），其余保留 |
| LOBID | 8B | 唯一标识，与文件名一致（冗余校验） |
| DataLen | 8B | 原始数据字节数 |
| DataCRC | 4B | CRC32C 数据校验和（仅覆盖 data） |
| Reserved | 6B | 对齐填充至 40B |

**关键设计决策**：

- **Header 内嵌 LOBID**：即使文件被误移动/重命名，仍可自描述校验
- **DataLen 为 uint64**：支持 GB 级对象（overflow page 的 TotalLen 只需 uint32）
- **Checksum 只覆盖 data**：Header 校验通过 Magic + LOBID 一致性检查完成
- **无压缩**：压缩可选在 ValueEncoder 层做（snappy/zstd），LOB 文件存压缩后的 data
- **无加密**：加密由上层（Ledgers）负责

### 4.4 BTree 中的 LOB File 引用

BTree 叶子页存储 **16B LOBFileRef**（vs overflow page 的 8B LOBRef）：

```
MVCC 编码扩展:
  普通 value:  [Flag:0x00][prev...][beginTS:8][realVal]
  Overflow:    [Flag:0x02][prev...][beginTS:8][lobRefLen:2=8][FirstPageID:4][TotalLen:4]
  LOB File:    [Flag:0x04][prev...][beginTS:8][lobRefLen:2=16][LOBID:8][TotalLen:8]
```

新增 Flag 常量：

| Flag | 值 | 含义 |
|------|:--:|------|
| `FlagLOBFile` | `0x04` | LOB 文件存储（Tier 2）|
| `FlagLOBFileTombstone` | `0x05` | LOB 文件 Tombstone（`0x04｜0x01`）|

**LOBFileRef 结构**：

```go
type LOBFileRef struct {
    LOBID    uint64 // 唯一 LOB 标识（即文件名中的 LOBID）
    TotalLen uint64 // 原始数据长度（最大 16EB，实际受文件系统限制）
}
```

**MVCCValue 扩展**：

```go
type MVCCValue struct {
    // ... existing fields ...
    LOB     *LOBRef     // non-nil for FlagLOBNormal/FlagLOBTombstone (Tier 1)
    LOBFile *LOBFileRef // non-nil for FlagLOBFile/FlagLOBFileTombstone (Tier 2)
}
```

### 4.5 LOBFileManager 接口

```go
// LOBFileManager manages large objects stored as independent files.
type LOBFileManager interface {
    // Create writes data to a new LOB file and returns its reference.
    // The file is fsync'd before returning — crash-safe.
    Create(data []byte) (LOBFileRef, error)

    // Read reads the full data of a LOB file.
    // Uses mmap for files > 64KB, pread for smaller.
    Read(ref LOBFileRef) ([]byte, error)

    // Delete marks a LOB file as deleted (Tombstone flag) for epoch GC.
    // Does NOT unlink immediately — epoch protects concurrent readers.
    Delete(ref LOBFileRef) error

    // Retire unlinks retired LOB files after epoch advances past all readers.
    Retire(lobIDs []uint64) error

    // CreateStream returns a writer for streaming large LOB data.
    // Caller writes chunks, then Close() finalizes checksum + fsync.
    CreateStream() (LOBStreamWriter, error)
}

// LOBStreamWriter supports chunked write for very large objects.
type LOBStreamWriter interface {
    Write(chunk []byte) (int, error)
    Close() (LOBFileRef, error) // finalize checksum + fsync
}
```

### 4.6 CRUD 流程

**写入（Create）**：

```
ValueEncoder.Encode(value):
  if len(value) > LOBFileThreshold:
    ref, err := lobFileMgr.Create(value)
    // ref = {LOBID: <monotonic>, TotalLen: len(value)}
    return BuildMVCC(FlagLOBFile, ts, lobFileRefBytes(ref), prev...)

LOBFileManager.Create(data):
  1. lobID = atomic.AddUint64(&nextLOBID, 1)
  2. dir = shardDir(lobID)  // data/lob/00/00/
  3. os.MkdirAll(dir)
  4. tmp = dir + "/.tmp-{lobID}"  // 临时文件
  5. write header + data to tmp
  6. f.Sync()
  7. os.Rename(tmp, dir + "/{lobID}.lob")  // 原子重命名
  8. return LOBFileRef{LOBID: lobID, TotalLen: len(data)}
```

**读取（Read）**：

```
LOBFileManager.Read(ref):
  1. path = lobPath(ref.LOBID)
  2. f = os.Open(path)
  3. validate header (magic + lobID + checksum)
  4. if ref.TotalLen > mmapThreshold:
       return mmap file region           // 零拷贝大对象
     else:
       return pread full data            // 小对象直接读
  5. return data
```

**删除（Delete）**：

```
EpochManager.RetireBatch(lobIDs):
  1. 批量 unlink：os.Remove(lobPath(id)) for each id
  2. 清理空目录（可选，后台协程）
```

**更新（Update）**：

```
// 不可变语义：创建新 LOB 文件 + epoch 回收旧文件
oldRef := mv.LOBFile
newRef := lobFileMgr.Create(newValue)
// BTree.Set 将 value 指向新 ref
// 旧 ref 推入 epoch retire 队列
```

### 4.7 并发安全

```
Reader:
  snapshotGet → DecodeValue → lobFileMgr.Read(ref)
  - 文件打开后持有 fd，unlink 不影响已打开 fd 的读取
  - POSIX 语义：unlink 只删除目录项，inode 在最后一个 fd 关闭后才释放

Writer:
  - Create: tmp + rename 原子操作，reader 看不到半写文件
  - Delete: 不立即 unlink，epoch 结束后 retire 批量 unlink
  - Update: Create new → BTree CAS → Retire old

GC:
  - EpochManager.RetireBatch(lobIDs) → os.Remove
  - 在 watermark 推进后调用，保证无 reader 持有旧 ref
```

### 4.8 性能考量

| 操作 | 延迟来源 | 优化 |
|------|------|------|
| Create 小 LOB (<256KB) | write + fsync | batch fsync（group commit） |
| Create 大 LOB (>1MB) | write 吞吐 | 流式写入，按 chunk fsync |
| Read 热点 LOB | open + pread | fd 缓存池（LRU），mmap 大文件 |
| Read 冷 LOB | open + pread | OS page cache 自然淘汰 |
| Delete | os.Remove（极快） | 批量 unlink，后台清理空目录 |

---

## 五、Tier 1 核心流程（overflow page）

### 5.1 写入流程

```
Ledgers.Put(key, largeValue):
  1. ValueEncoder.Encode:
     a. if len(value) > LOBSizeThreshold: // 固定阈值 2KB, ValueEncoder 层判断
        → LOBManager.Allocate(value)
           → PageManager.AllocOverflow(totalLen)
              → 计算 N = ceil(totalLen / 4024)
              → 分配 N 个溢出页，构建链
              → 逐页写入 Data chunk
              → 返回 FirstPageID
        → BuildMVCC(FlagLOBNormal, ts, lobRef, ...)
           → lobRef = [FirstPageID:4][TotalLen:4]
           → encoded = [0x02][prev...][beginTS][lobRefLen:2][lobRef:8]
     b. else:
        → BuildMVCC(FlagNormal, ts, value, ...)
           → encoded = [0x00][prev...][beginTS][value]

  2. BTree.Set(key, encoded)  ← BTree 无感知, ~31B 正常插入
```

### 5.2 读取流程

```
Ledgers.Get(key):
  1. BTree.Get(key) → raw (mmap copy)
  2. ParseMVCC(raw) → mv  (返回 MVCCValue, 含 LOB ref)
     a. if mv.Flag == 0x02 || mv.Flag == 0x03 (LOB / LOB-Tombstone):
        → LOBManager.Read(mv.LOB)
           → PageManager.ReadOverflow(FirstPageID, TotalLen)
              → 沿链读取各页 Data chunk
              → concatenate
           → 返回完整 originalValue
     b. else:
        → 返回 mv.RealVal
```

### 5.3 删除流程（关键约束）

根据 D10：

> **上层必须在调用 BTree.Delete 前先释放外部资源**。BTree.Delete 只回收叶子条目的页面空间。

```
Ledgers.Delete(key):
  1. BTree.Get(key) → oldValue
  2. ParseMVCC(oldValue) → if mv.LOB != nil:
     → LOBManager.Free(mv.LOB)
        → PageManager.FreeOverflow(FirstPageID)
           → 沿链释放所有溢出页
  3. BTree.Delete(key)  ← 只删叶子条目

事务 Delete（MVCC Tombstone 写入）:
  SnapshotTx.Delete(key):
    1. Put 阶段: 记录 OldValue → WriteBuffer (含 LOB ref)
    2. Commit → BuildMVCC(FlagLOBTombstone=0x03, ..., lobRef=oldLOBRef)
       → BTree.Set(key, encoded)  ← Tombstone 内含 LOB ref
    3. LOB 溢出页**不立即释放**: 旧 snapshot 读可能需要旧 LOB 数据
    4. epoch GC 延迟回收: 当 watermark > commitTS 后,
       FreeOverflow 由 GC 回调触发

  Tombstone 恢复 (Put after Delete):
    旧 Tombstone(0x03) → 新 Put(0x02): 复用旧 LOB ref (溢出页链不变)

  回滚:
    Commit 失败 → rollbackOneKey:
      本事务新分配的 LOB 溢出页立即 free (无 reader)
      → UndoEntry 包含 LOB ref → FreeOverflow
```

### 5.4 更新流程（非事务路径）

```
Ledgers.Put(key, newLargeValue) [existing key, 非事务]:
  1. BTree.Get(key) → oldValue
  2. ParseMVCC(oldValue) → if LOB → LOBManager.Free(mv.LOB)  ← 先释放旧版溢出页
  3. ValueEncoder.Encode(newValue) → LOBManager.Allocate(data)  ← 分配新版溢出页
  4. BTree.Set(key, encoded)                                    ← 写入新值

事务路径下旧 LOB 溢出页与 BTree 旧版本页面通过 epoch GC 一起延迟回收。
```

### 5.5 prevVal LOB 展开说明

`snapshotGet` 和 `GetBatch` 中，当当前版本对快照不可见、需要回退到 prev 版本时：

```go
// PrevVal 是完整的 MVCC 编码字节（含 Flag + Header + beginTS + realVal）
// DecodeValue → ParseMVCC 可以正确解析——PrevVal 本身就是一段 MVCC 编码
if mv.PrevFlag == FlagLOBNormal || mv.PrevFlag == FlagLOBTombstone {
    prevMV, err := DecodeValue(mv.PrevVal, tx.lobManager)
    // prevMV.RealVal 即展开后的完整 value
}
```

> **关键**：`PrevVal` 不是裸 value，是完整 MVCC 编码 `[prevFlag][prevBeginTS][prevValLen][prevVal][beginTS][realVal]`。
> `DecodeValue` 调用 `ParseMVCC` 重新解析并展开 LOB。这是**递归解析**而非直接解释字节。

### 5.6 EncodeDeleteValue 语义

```go
// EncodeDeleteValue 用于事务 Delete 路径的 MVCC 编码。
// 当旧 value 是 LOB 时，Tombstone 保留旧 LOB ref → epoch GC 延迟回收溢出页。
// commitKey 中的调用：两次传入 entry.OldFlag/OldBeginTS/OldValue——
//   - 前一组：当前版本的 old flag/ts/value（用于编码 Tombstone 的 flag 判断）
//   - 后一组：作为 prev 版本嵌入新 Tombstone 的 prev 字段
encoded, _ = EncodeDeleteValue(commitTS,
    entry.OldFlag, entry.OldBeginTS, entry.OldValue,   // 当前版本信息
    entry.OldFlag, entry.OldBeginTS, entry.OldValue)   // prev 版本嵌入
```

---

## 六、并发控制与 GC

### 6.1 Epoch-Based GC（复用现有 BTree GC）

当前 NexKV BTree 已有 epoch-based GC（`EpochManager` + `AllocSlot/EnterRead/ExitRead/RetireBatch`）。
LOB 溢出页复用同一套机制——不需要引用计数。

```
Reader:
  epochSlot = epochMgr.AllocSlot()
  epochMgr.EnterRead(epochSlot)
  ReadOverflow(FirstPageID, totalLen)  ← 安全读, 页面不会被 free
  epochMgr.ExitRead(epochSlot)

Writer (BTree value 更新):
  LOBManager.Allocate → 分配新溢出页链 → 数据写入新页
  BTree.Set(key, encoded) → CAS 更新叶子页 value (指向新 LOB ref)
  旧 LOB ref → 旧溢出页链 → 推入 epochMgr.RetireBatch
  epoch 结束后 EpochManager 批量 Free 旧溢出页

GC:
  EpochManager 周期性 tryReclaim → 批量回收 retired 页面
  LOB 溢出页与 BTree 叶子页/索引页共用同一回收周期
```

#### RetireOverflowChain — 链式 LOB 页面批量回收

`EpochManager.RetireBatch` 接受 `...PageID` 列表，不理解链结构。需新增方法：

```go
// RetireOverflowChain 遍历 LOB 溢出页链，将所有页面推入 epoch 回收队列。
func (pm *PageManager) RetireOverflowChain(firstPageID uint32) {
    for pageID := firstPageID; pageID != 0; {
        // 读取本页 NextPageID（必须在 Free 之前读取）
        ptr := pm.PageIDToPtr(pageID)
        offset := SizeofPageHeader // skip 56B header
        nextPageID := *(*uint64)(unsafe.Add(ptr, offset)) // NextPageID is uint64, 兼容 model.PageID
        // 推入 epoch 队列（与 BTree 页面统一回收）
        pm.epochMgr.RetireBatch(epochSlot, model.PageID(pageID))
        pageID = nextPageID
    }
}
```

> 事务 Delete 路径中，旧 LOB 链通过 `RetireOverflowChain` 推入 epoch 队列，与 BTree 页面的 COW 旧页共用同一回收周期。

### 6.2 LOB 页面生命周期

| 操作 | 页面状态 | 回收时机 |
|------|------|------|
| Allocate | 新页面进入活跃集 | 不会被 free（直到被 Release） |
| Update (写新值) | 旧 LOB 链标记 retired → 推入 epoch 队列 | epoch 结束后批量 free |
| Delete (Tombstone) | 旧 LOB 链标记 retired → 推入 epoch 队列 | 同上 |
| Rollback | 本事务分配的溢出的页立即 free（无 reader） | 立即 |
| BTree Split/COW | 被替换的 BTree 页面 Retire → 推入 epoch | 同上 |

### 6.3 CAS 原子操作（与现有 BTree 一致）

```
Reader:  无锁读 (mmap sub-slice + epoch 保护)
Writer (BTree value 更新): 分配新页 → 写入数据 → BTree.Set CAS 更新 value ref → RetireBatch
Writer (LOB 溢出页): 分配新页 → 写入数据 (只追加，无 COW) → CAS 更新的仅是 BTree 叶子页 value
GC:      epoch 结束后批量回收 retired 页面（BTree 页 + LOB 溢出页，统一回收）
```

---

## 七、性能优化

### 7.1 批量预分配

写入大对象时一次性分配所有溢出页（而非逐页分配+写入），减少 alloc 开销：

```go
func AllocOverflowChain(totalLen uint32) (firstPageID uint32, pageIDs []uint32) {
    n := (totalLen + 4023) / 4024 // ceil
    pageIDs = make([]uint32, n)
    for i := range n { pageIDs[i] = pm.Alloc() }
    // 构建链: 相邻页设置 NextPageID
}
```

### 7.2 顺序写入优化

溢出页 Data 写入时直接操作 mmap 指针（`unsafe.Slice`），避免中间 Go heap 拷贝：

```go
ptr := pm.PageIDToPtr(pageID)
// 偏移 = PageHeader(56B) + NextPageID(8B) + ChunkSize(4B) + Checksum(4B) = 72B
dataPtr := unsafe.Add(ptr, SizeofPageHeader + 8 + 4 + 4)
copy(unsafe.Slice((*byte)(dataPtr), 4024), chunk)
```

### 7.3 未来优化方向

| 优化 | 说明 | 优先级 |
|------|------|:--:|
| LOB 缓存 | 热点大对象 LRU 缓存 | P2 |
| 页面预读 | 顺序读时预读相邻溢出页 | P2 |
| 并行读取 | 多页并行 mmap 读取 | P3 |

---

## 八、实现计划

### Tier 1 — Overflow Page 实施状态：✅ 全部完成（2026-06-09）

| Step | 内容 | 行数 | 文件 | 状态 |
|------|------|:--:|------|:--:|
| 1 | MVCC LOB Flag + 编码 (`FlagLOBNormal=0x02`, LOBRef, Parse/Build) | ~30 | `mvcc/codec.go` | ✅ |
| 2 | 溢出页面 AllocOverflow/FreeOverflow/ReadOverflow | ~60 | `offheap/page_manager.go` | ✅ |
| 3 | LOBManager 接口 + 实现 (Allocate/Read/Free/Update) | ~50 | `storage/lob/manager.go` (新) | ✅ |
| 4 | ValueEncoder 实现 (EncodeValue/DecodeValue + LOB 展开) | ~50 | `mvcc/lob.go` (新) | ✅ |
| — | **BTree 无需改动**：Get 返回 raw bytes，LOB 展开在上层完成 | 0 | — | ✅ |
| 5 | MVCC 事务层集成 (Get/GetBatch/commitKey/rollbackOneKey) | ~80 | `mvcc/transaction.go` | ✅ |
| 6 | 阈值配置 + benchmark (4KB LOB) | ~130 | `cmd/tools/btree-txn-bench` | ✅ |
| **Tier 1 合计** | | **~400** | |

### ✅ epoch-GC 已实现

- `EpochManager.RetireLobChain(firstPageID)` / `RetireLobFile(lobID)` 延迟回收
- `SetLOBFreeFns` 连接 EpochManager → LOB 管理器
- `commitKey` 优先使用 epoch-GC，fallback 立即释放
- `drainLOBRetired` 在 `tryReclaim` 后处理安全 epoch 的资源
- fd 缓存 LRU 64 条目，热点 LOB 避免重复 open
- `CleanupTmp` 启动时清理崩溃遗留 .tmp 文件

### Tier 2 — LOB File 实施状态：✅ Step 7-10 完成（2026-06-09）

| Step | 内容 | 行数 | 文件 | 状态 |
|------|------|:--:|------|:--:|
| 7 | `FlagLOBFile=0x04`/`0x05` + LOBFileRef + ParseMVCC 解析 | ~25 | `mvcc/codec.go` | ✅ |
| 8 | `LOBFileManager` 接口 + DefaultLOBFileManager | ~50 | `storage/lob/file_manager.go` (新) | ✅ |
| 9 | LOB 文件存储引擎：目录分片 + tmp→rename + mmap/pread | ~180 | `storage/lob/file_store.go` (新) | ✅ |
| 10 | ValueEncoder 两级路由 + DecodeValue 两级展开 | ~60 | `mvcc/lob.go` | ✅ |
| 11 | LOBFileManager 注入 TxManager + SnapshotTx + BTree | ~30 | `mvcc/transaction.go`, `btree/options.go`, `btree/btree.go` | ✅ |
| 12 | Epoch GC 集成：RetireLobChain/File + fd缓存 + CleanupTmp | ~80 | `storage/btree/epoch.go` | ✅ |
| 13 | Benchmark：128KB LOB 文件读写 | ~30 | `cmd/tools/btree_bench` | ✅ |
| 14 | Code Review 全量修复 (2 CRITICAL + 4 HIGH + 3 MEDIUM) | ~190 | 9 files | ✅ |
| **Tier 2 已实现** | | **~565** | |
| **已实现** | | **~965** | |

### ✅ P2 优化已完成

| 项目 | 说明 | 实现 |
|------|------|------|
| 空目录清理 | 后台协程定期清除 data/lob/ 下空目录 | ✅ 5min 间隔，自底向上 |
| Group commit fsync | 批量 fsync 减少 LOB 写入延迟 | ✅ 1ms 窗口，最大批量 32 |
| fd 缓存监控 | 缓存命中率指标暴露 | ✅ FDCacheStats + HitRate() |
| fd cache 并发安全 | 引用计数 + ReadAt 替换 Seek+Read + pendingClose map | ✅ C1+C2 修复 |
| LOB flag 完整性 | Put/Delete/rollback 所有 flag 的 OldValue/OldPrev 正确保存 | ✅ H1+H3+H4 修复 |
| CleanupTmp 安全 | 短文件名 len>=5 检查防止 slice 越界 panic | ✅ H2 修复 |
| nextLOBID 随机起点 | crypto/rand 初始化高 32 位避免重启 ID 冲突 | ✅ M1 修复 |
| lobRetired 上限 | maxLobRetiredLen=65536 + 溢出强制 reclaim | ✅ M2 修复 |
| 辅助函数提取 | IsLOBFlag/IsLOBFileFlag/isValidFlag 消除重复 | ✅ M3+M4 修复 |
| YAGNI 清理 | 移除 Retire/Update/Size/CommitAndWait 死代码 | ✅ L1-L5 清理 |

### 测试覆盖率

| 包 | 覆盖率 |
|----|--------|
| `offheap` | **80.2%** |
| `persist` | **80.5%** |
| `lob` | **76.5%** |
| `chunk` | **73.7%** |
| `btree` | **63.3%** |
| `mvcc` | **61.7%** |
| `checkpoint` | **56.2%** |
| `wal` | **48.3%** |
| **storage total** | **66.0%** |

> 新增测试用例：lob 19 个 + mvcc 15 个 + btree 4 个 = **38 个**。lint 0 issues，全部通过 `-race` 竞态检测。

### 未来 P3 方向

| 项目 | 说明 |
|------|------|
| LOB 缓存 | 热点大对象 LRU 缓存（可复用 fd 缓存模式）|
| 页面预读 | 顺序读时预读相邻溢出页 |
| 并行读取 | 多页并行 mmap 读取

### 实施细节

**新增文件**：
- `internal/infrastructure/storage/mvcc/lob.go` — LOBManager 接口 + LOBSizeThreshold + EncodeValue/DecodeValue
- `internal/infrastructure/storage/lob/manager.go` — DefaultLOBManager 实现
- `internal/infrastructure/storage/btree/lob_e2e_test.go` — LOB 端到端测试

**关键设计决策实施**：
- LOB 分配在 Commit 时（commitKey 中），不在 Put 时——回滚无需释放 LOB 页
- prevFlag 不再标准化（存储原始值 0x00/0x01/0x02/0x03），保留 LOB 信息
- IsTombstoneFlag() 使用 bit 0 判断：`flag & 0x01 == FlagTombstone`
- BTree 零改动，LOB 逻辑全在 MVCC 层
- LOBManager 通过 TxManager → SnapshotTx 注入，nil = LOB 禁用（向后兼容）

**prevFlag 格式变更**：
- 旧格式：prevFlag 标准化为 0x00/0x01（`& 0x01`）
- 新格式：prevFlag 存储原始值（0x00=Normal, 0x01=Tombstone, 0x02=LOBNormal, 0x03=LOBTombstone, 0x04=LOBFile, 0x05=LOBFileTombstone）
- 向前兼容：旧数据 prevFlag=0x00/0x01，新 ParseMVCC 不再标准化，IsTombstoneFlag 处理 bit 0

### LOB Benchmark 结果（100K ops, warmup 10K, mmap=512MB, M2 Pro）

| Benchmark | LOB Size | Tier | QPS (单线程) | QPS (8 线程) |
|-----------|:--:|:--:|------:|------:|
| lob-put-4k | 4KB | Tier 1 | **1,335,730** | **3,168,204** |
| lob-get-4k | 4KB | Tier 1 | **1,486,858** | **2,034,911** |
| lob-put-64k | 64KB | Tier 1 上限 | **654,079** | — |
| lob-put-128k | 128KB | Tier 2 file | **127** | — |
| lob-get-128k | 128KB | Tier 2 file | **1,514** | — |

> **分析**：
> - **Tier 1 (mmap overflow page)**：4KB 写入 1.3M QPS，读取 1.5M QPS。64KB 写入 654K QPS（退化为 16 页链式分配）。全部在内存中，零系统调用。
> - **Tier 2 (disk file)**：128KB 写入仅 127 QPS（每条含 tmp 写入 + fsync + rename，~7.9ms/op）。读取 1,514 QPS（利用 OS page cache + mmap）。写入瓶颈在 fsync；可进一步用 group commit batch fsync 提升。
> - **前后对比**：inline KV put ~2M QPS。4KB LOB 写入仅降低 ~33%（1.96M→1.34M），因为溢出页分配在 mmap 内开销极低。

---

## 九、设计决策

### Tier 1 决策（Overflow Page）

| 决策点 | 方案 | 理由 |
|--------|------|------|
| BTree 透明性 | value 作为 `[]byte` 黑盒 | 单一职责，BTree 专注索引 |
| LOB 感知层 | MVCC codec 层 | BTree 零改动，编解码自然扩展 |
| 溢出页管理 | PageManager 扩展 | 复用现有页面分配/释放/epoch 机制 |
| 删除顺序 | 先释放 LOB 页，再删 BTree 条目 | 防止溢出页泄漏 |
| LOB Flag | 0x02(Normal)/0x03(Tombstone) | 新增常量, 需修改 ParseMVCC 验证 |
| LOB 引用大小 | 8B (ID:4 + Len:4) | BTree 叶子页最小化存储开销 |
| 溢出页数据量 | 4024B/page (4096-56-8-4-4) | 最大化数据密度 |
| GC 策略 | Epoch-based（复用 BTree EpochManager） | 与 BTree 页面统一批次回收, 无需引用计数 |
| Tier 1 阈值 | 2KB | Lealone 同值，平衡 leaf page 容量和溢出页开销 |

### Tier 2 决策（LOB File）

| 决策点 | 方案 | 理由 |
|--------|------|------|
| 存储介质 | 独立文件系统（非 mmap） | 不占 mmap 配额，利用 OS page cache |
| 文件粒度 | 一个 LOB = 一个文件 | 简单可靠，unlink = 回收，无碎片 |
| 目录分片 | LOBID 高 4 字节 → `XX/YY/` 两级目录 | 256×256=65K 叶子目录，百万文件无压力 |
| 原子写入 | tmp 文件 + rename | POSIX 原子操作，reader 看不到半写文件 |
| 读取方式 | mmap（>64KB） / pread（≤64KB） | mmap 零拷贝大文件，pread 避免小文件 mmap 开销 |
| LOB Flag | 0x04(Normal)/0x05(Tombstone) | 与 overflow 0x02/0x03 区分 |
| LOB 引用大小 | 16B (LOBID:8 + TotalLen:8) | 支持 GB 级对象，uint64 TotalLen |
| GC 策略 | Epoch-based（与 overflow 同一 EpochManager） | 统一回收周期，RetireBatch 批量 unlink |
| Tier 2 阈值 | 64KB | 确保 mmap 不被少量大对象耗尽 |
| 校验 | Header CRC32C self-check + Data CRC32C | 防止文件损坏或误识别 |

---

## 十、与 Lealone 方案对比

| 维度 | Lealone (Java) | NexKV Tier 1 (Overflow) | NexKV Tier 2 (LOB File) |
|------|---------------|------------------------|--------------------------|
| LOB 感知层 | `BTreeStorage` (BTree 感知) | `MVCC codec` (BTree 透明) | `MVCC codec` (BTree 透明) |
| BTree 改动 | 需要 `isLargeValue` 判断 | **零改动** | **零改动** |
| 存储介质 | mmap 溢出页 | mmap 溢出页 | **独立文件系统** |
| LOB Flag | `0x80` (1 byte) | `0x02`/`0x03` | `0x04`/`0x05` |
| 引用大小 | 21B | **8B** | 16B |
| 最大对象 | 4GB (uint32) | 4GB (uint32) | 16EB (uint64) |
| 并发控制 | `AtomicReferenceFieldUpdater` | `atomic.Pointer` + CAS | epoch + rename 原子操作 |
| GC 机制 | JVM GC + 引用计数 | Epoch-based GC | Epoch-based unlink |
| mmap 占用 | 数据+索引全在 mmap | 索引+溢出页在 mmap | **只索引在 mmap** |
| 删除方式 | BTree 自动处理 | 上层先 free LOB 再 delete BTree | 上层先 delete LOB file 再 delete BTree |

---

## 十一、关联文档

- [[2026-06-09-lob-scheme-investigation]] — Lealone LOB 方案深度调查  
- [[2026-06-09-lob-implementation-design]] — LOB 实现详细设计（Ledgers 集成）  
- [[2026-06-08-txn-benchmark-spike]] — 写路径 6 项优化  
- [[2026-06-08-get-perf-spike]] — 读路径 5 项优化
