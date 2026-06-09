# Spike：BTree LOB（Large Object）溢出页方案

> **日期**：2026-06-09  
> **分支**：`spike/btree-lob`  
> **参考**：[[2026-06-09-lob-scheme-investigation]] — Lealone LOB 方案深度调查  
> **参考**：[[2026-06-09-lob-implementation-design]] — LOB 实现详细设计（Ledgers 集成）  
> **背景**：当前 NexKV BTree 每条 record 必须在 4KB 单页内存储。MVCC value 约 50B，页面可存 ~80 entries。若 value 超过 ~3.9KB，insert 直接失败。

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

| 场景 | value 大小 | 频率 |
|------|:--:|:--:|
| 普通 KV | < 100B | 99% |
| 中等对象 | 4KB ~ 64KB | 0.9% |
| 大对象 (LOB) | 64KB ~ 1MB | 0.1% |

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
│         ValueEncoder (codec)            │ ← LOB 检测, 编解码, 数据拼接
│         LOBManager (lifecycle)          │ ← 溢出页分配, GC 回收 (epoch-based)
├─────────────────────────────────────────┤
│         BTree (index only)              │ ← Key 索引, Split/Merge, CAS 并发
│         PageManager (storage)           │ ← 页面分配/释放/读写
│         mmap (offheap)                  │ ← 物理内存
└─────────────────────────────────────────┘
```

| 模块 | 职责 | LOB 相关接口 |
|------|------|------|
| **BTree** | Key 索引、页面组织、CAS 并发 | 无——完全透明 |
| **ValueEncoder** | Value 编解码、LOB 检测、数据拼接 | `Encode/Decode`, `IsLOB` |
| **LOBManager** | 溢出页生命周期、epoch 回收 | `Allocate/Read/Free/Update/Size` |
| **PageManager** | 页面分配/释放/读写 | `AllocOverflow/FreeOverflow/ReadOverflow` |

### 2.3 LOBManager 接口

```go
// LOBManager 管理大对象的存储和生命周期
type LOBManager interface {
    // Allocate 分配溢出页面存储大对象，返回 BTree 可存储的 MVCC 编码字节
    Allocate(data []byte) ([]byte, error)

    // Read 根据 LOB 引用读取完整数据（MVCC 解析由上层 ParseMVCC 完成）
    Read(ref LOBRef) ([]byte, error)

    // Free 释放 LOB 引用指向的所有溢出页面
    Free(ref LOBRef) error

    // Update 更新大对象（释放旧溢出页 + 分配新溢出页）
    Update(data []byte, oldRef LOBRef) ([]byte, error)

    // Size 返回 LOB 引用的字节大小
    Size(ref LOBRef) int64
}
```

---

## 三、存储格式

### 3.1 页面头部格式（统一 Page Header）[新设计, 待实现]

所有页面（叶子页、索引页、溢出页）共享 56 字节头部：

| 偏移 | 大小 | 字段 | 说明 |
|------|------|------|------|
| 0 | 4 | Magic | 魔数 `0x4E45584B`（NEXK） |
| 4 | 1 | PageType | 0=Leaf, 1=Node, **2=Overflow** |
| 5 | 1 | Reserved | 保留 |
| 6 | 2 | Checksum | 头部校验和 |
| 8 | 8 | Version | MVCC 版本号 |
| 16 | 8 | PageID | 页面唯一标识 |
| 24 | 8 | ParentID | 父页面 ID（0=根） |
| 32 | 4 | Count | 当前条目数 |
| 36 | 4 | Flags | 状态标志位 |
| 40 | 4 | TombstoneCount | 墓碑条目计数 |
| 44 | 12 | Padding | 对齐填充 |

### 3.2 MVCC LOB 编码

新增以下 Flag 常量（当前代码不支持非 0x00/0x01 的 Flag，需修改 ParseMVCC 验证逻辑）：

| Flag | 值 | 含义 |
|------|:--:|------|
| `FlagNormal` | `0x00` | 普通数据 |
| `FlagTombstone` | `0x01` | 逻辑删除 |
| `FlagLOBNormal` | `0x02` | LOB 大对象（新增） |
| `FlagLOBTombstone` | `0x03` | LOB 大对象 Tombstone（新增，`0x02｜0x01`） |

> `FlagLOBTombstone=0x03` 用于 Delete LOB key 时写入 Tombstone 并保留 LOB 引用，供 epoch GC 延迟回收溢出页。Tombstone 恢复（重新 Put 后）时从旧 Tombstone 提取 LOB flag。

```
普通 value:  [Flag:0x00][prevFlag][prevBeginTS][prevValLen][prevVal][beginTS][realVal]
LOB value:   [Flag:0x02][prevFlag][prevBeginTS][prevValLen][prevVal][beginTS]
             [lobRefLen:2][lobRef:8]
                lobRef = [FirstPageID:4][TotalLen:4]

  BTree 叶子页存储: ~31B (MVCC header 20B + lobRefLen 2B + lobRef 8B + beginTS 8B)
```

> **注意**：`Flag 0x02` 是新增常量。当前 `ParseMVCC` 显式拒绝所有非 0x00/0x01 的 Flag（`mvcc/codec.go:56`）。实现时需修改 ParseMVCC/BuildMVCC 的 Flag 验证逻辑，允许 0x02/0x03（见 H4）。  
> Phase 3 版本内嵌复用 `FlagNormal=0x00/FlagTombstone=0x01`，通过 `PrevBeginTS != 0` 区分有无 prev，没有引入新 Flag。  
> prev 字段正常使用——大 value 的旧版本如果是 LOB，指向旧版溢出页链。

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
    LOB *LOBRef // nil for non-LOB values
}
```

### 3.3 溢出页面格式

```
Overflow Page (4KB):
  [PageHeader:56B][NextPageID:8B][ChunkSize:4B][Checksum:4B][Data:4024B]

  NextPageID: uint64, 0 = 链尾
  ChunkSize:  uint32, 本页实际数据大小（≤4024）
  Checksum:   uint32, 数据校验和（CRC32）
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

### 3.4 LOB 阈值

| 阈值 | 值 | 说明 |
|------|:--:|------|
| `LOBSizeThreshold` | 2048 (2KB) | value > 2KB 启用溢出页存储 |

> 2KB 阈值：Phase 3 版本内嵌后 value ~50B，页面可存 ~80 entries。阈值可按需调整为 3-4KB（不固定）——实现时可用 `leaf.IsFull` 动态判断，而非硬编码常量。仅当值**确实**超出页面容量时才走 LOB 路径。

---

## 四、核心流程

### 4.1 写入流程

```
Ledgers.Put(key, largeValue):
  1. ValueEncoder.Encode:
     a. if len(value) > 2KB:
        → LOBManager.Allocate(value)
           → PageManager.AllocOverflow(size)
              → 计算 N = ceil(size / 4024)
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

### 4.2 读取流程

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

### 4.3 删除流程（关键约束）

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
```

### 4.4 更新流程（非事务路径）

```
Ledgers.Put(key, newLargeValue) [existing key, 非事务]:
  1. BTree.Get(key) → oldValue
  2. ParseMVCC(oldValue) → if LOB → LOBManager.Free(mv.LOB)  ← 先释放旧版溢出页
  3. ValueEncoder.Encode(newValue) → LOBManager.Allocate(data)  ← 分配新版溢出页
  4. BTree.Set(key, encoded)                                    ← 写入新值

事务路径下旧 LOB 溢出页与 BTree 旧版本页面通过 epoch GC 一起延迟回收。
```

---

## 五、并发控制与 GC

### 5.1 Epoch-Based GC（复用现有 BTree GC）

当前 NexKV BTree 已有 epoch-based GC（`EpochManager` + `AllocSlot/EnterRead/ExitRead/RetireBatch`）。
LOB 溢出页复用同一套机制——不需要引用计数。

```
Reader:
  epochSlot = epochMgr.AllocSlot()
  epochMgr.EnterRead(epochSlot)
  ReadOverflow(FirstPageID)  ← 安全读, 页面不会被 free
  epochMgr.ExitRead(epochSlot)

Writer:
  COW 拷贝 → CAS 替换 → 旧页面推入 epochMgr.RetireBatch
  epoch 结束后 EpochManager 自动 Free 旧页面

GC:
  EpochManager 周期性 tryReclaim → 批量回收 retired 页面
  LOB 溢出页与 BTree 叶子页/索引页共用同一回收周期
```

### 5.2 LOB 页面生命周期

| 操作 | 页面状态 | 回收时机 |
|------|------|------|
| Allocate | 新页面进入活跃集 | 不会被 free（直到被 Release） |
| Update (写新值) | 旧 LOB 链标记 retired → 推入 epoch 队列 | epoch 结束后批量 free |
| Delete (Tombstone) | 旧 LOB 链标记 retired → 推入 epoch 队列 | 同上 |
| Rollback | 本事务分配的溢出的页立即 free（无 reader） | 立即 |
| BTree Split/COW | 被替换的 BTree 页面 Retire → 推入 epoch | 同上 |

### 5.3 CAS 原子操作（与现有 BTree 一致）

```
Reader:  无锁读 (mmap sub-slice + epoch 保护)
Writer:  COW 拷贝 → CAS 替换 → RetireBatch → epoch GC
GC:      epoch 结束后批量回收 retired 页面
```

---

## 六、性能优化

### 6.1 批量预分配

写入大对象时一次性分配所有溢出页（而非逐页分配+写入），减少 alloc 开销：

```go
func AllocOverflowChain(totalLen uint32) (firstPageID uint32, pageIDs []uint32) {
    n := (totalLen + 4023) / 4024 // ceil
    pageIDs = make([]uint32, n)
    for i := range n { pageIDs[i] = pm.Alloc() }
    // 构建链: 相邻页设置 NextPageID
}
```

### 6.2 顺序写入优化

溢出页 Data 写入时直接操作 mmap 指针（`unsafe.Slice`），避免中间 Go heap 拷贝：

```go
ptr := pm.PageIDToPtr(pageID)
// 偏移 = PageHeader(56B) + NextPageID(8B) + ChunkSize(4B) + Checksum(4B) = 72B
dataPtr := unsafe.Add(ptr, SizeofPageHeader + 8 + 4 + 4)
copy(unsafe.Slice((*byte)(dataPtr), 4024), chunk)
```

### 6.3 未来优化方向

| 优化 | 说明 | 优先级 |
|------|------|:--:|
| LOB 缓存 | 热点大对象 LRU 缓存 | P2 |
| 页面预读 | 顺序读时预读相邻溢出页 | P2 |
| 并行读取 | 多页并行 mmap 读取 | P3 |

---

## 七、实现计划

| Step | 内容 | 行数 | 文件 |
|------|------|:--:|------|
| 1 | MVCC LOB Flag + 编码 (`FlagLOBNormal=0x02`, LOBRef, Parse/Build) | ~30 | `mvcc/codec.go` |
| 2 | 溢出页面 AllocOverflow/FreeOverflow/ReadOverflow | ~60 | `offheap/page_manager.go` |
| 3 | LOBManager 接口 + 实现 (Allocate/Read/Free/Size) | ~50 | `storage/lob/manager.go` (新) |
| 4 | ValueEncoder 实现 (Encode/Decode + LOB 展开, 调用 LOBManager) | ~40 | `mvcc/codec.go` (ValueEncoder 部分) |
| — | **BTree 无需改动**：Get 返回 raw bytes，LOB 展开在上层完成 | 0 | — |
| 5 | MVCC 事务层集成 (Put/Get/commitKey/rollbackOneKey) | ~40 | `mvcc/transaction.go` |

事务层要点：
- `SnapshotTx.Put`: value > 阈值 → LOBManager.Allocate → 存储 lobRef 到 WriteBuffer
- `SnapshotTx.Get`: GetRaw → ParseMVCC → if LOB → LOBManager.Read → 返回完整值
- `rollbackOneKey`: 回滚时释放事务内新分配的 LOB 溢出页（UndoEntry 记录 LOB ref）
| 6 | 阈值配置 + benchmark (4KB/64KB/1MB) | ~40 | `cmd/tools/btree_bench` |
| **合计** | | **~230** | |

---

## 八、设计决策

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
| 阈值 | 2KB | Lealone 同值，平衡 leaf page 容量和溢出页开销 |

---

## 九、与 Lealone 方案对比

| 维度 | Lealone (Java) | NexKV (本方案) |
|------|---------------|----------------|
| LOB 感知层 | `BTreeStorage` (BTree 感知) | `MVCC codec` (BTree 透明) |
| BTree 改动 | 需要 `isLargeValue` 判断 | **零改动** |
| 溢出页管理 | `BTreeStorage.largeValue` | `PageManager.AllocOverflow` |
| LOB Flag | `0x80` (1 byte) | `0x02`(Normal)/`0x03`(Tomb) (新增) |
| 引用大小 | 21B | **8B** (仅 ID+Len) |
| 溢出页数据 | 4084B | 4024B |
| 并发控制 | `AtomicReferenceFieldUpdater` | `atomic.Pointer` + CAS |
| GC 机制 | JVM GC + 引用计数 | Epoch-based GC (复用 BTree) |
| 删除 | BTree 自动处理 | 上层先 free LOB 再 delete BTree |

---

## 十、关联文档

- [[2026-06-09-lob-scheme-investigation]] — Lealone LOB 方案深度调查  
- [[2026-06-09-lob-implementation-design]] — LOB 实现详细设计（Ledgers 集成）  
- [[2026-06-08-txn-benchmark-spike]] — 写路径 6 项优化  
- [[2026-06-08-get-perf-spike]] — 读路径 5 项优化
