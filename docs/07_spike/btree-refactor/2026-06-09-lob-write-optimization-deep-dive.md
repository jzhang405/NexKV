# LOB 写入性能优化三方案深度剖析

> **日期**：2026-06-09  
> **背景**：128KB Tier 2 LOB 写入仅 127 QPS，vs Tier 1 overflow page 的 15,493 QPS（106x 差距）。  
> **根因**：fsync 占单次写入 87% 延迟（~3.5ms/4.4ms）。  
> **本文**：从延迟构成出发，逐层深入三种优化方案。

---

## 一、先理解问题：为什么这么慢？

### 1.1 一条 128KB LOB 写入走过了什么

```
时间线 (Apple M2 Pro SSD):

  0.00ms  ┌─ MkdirAll(shardDir)    10μs   ← 创建分片目录 (已去掉)
  0.01ms  ├─ OpenFile(tmp)         15μs   ← 打开临时文件
  0.01ms  ├─ Write(header 40B)      2μs   ← 写入魔数+版本+lobID+长度
  0.31ms  ├─ Write(data 128KB)    300μs   ← 写入数据到页缓存
         │
  0.31ms  │  ★ 此时数据还在 OS 页缓存中，未落盘 ★
         │
  3.81ms  ├─ f.Sync()            3500μs   ← 87%! 等待磁盘完成
         │    │
         │    │  内核做了什么:
         │    │  ① 遍历文件的 dirty pages
         │    │  ② 提交到磁盘队列 (NVMe: ~50μs)
         │    │  ③ 等待磁盘确认 (fdatasync: ~2ms, fsync: ~3.5ms)
         │    │     └─ fsync 多刷了 inode (mtime, size), fdatasync 只刷数据
         │    │  ④ 返回
         │    │
  3.82ms  ├─ Close()               5μs   ← 关闭 fd
  3.87ms  ├─ Rename(tmp→final)    50μs   ← 原子重命名
  3.97ms  ├─ BTree.Set()         100μs   ← CAS 写入 BTree
         │
  4.37ms  └─ 其他 (GC, alloc...)  400μs

  总延迟: ~4.4ms  →  理论 QPS: 227
  实测:   127 QPS  (含 BTree split/COW 额外开销)
```

### 1.2 延迟饼图

```
        ┌──────────────────────────────────────┐
        │                                      │
        │           f.Sync()  87%              │
        │           (~3.5ms)                   │
        │                                      │
        │      ┌─────────────────────┐         │
        │      │ 其他 13%            │         │
        │      │ (0.9ms)            │         │
        │      └─────────────────────┘         │
        └──────────────────────────────────────┘

  87% 是无法通过"优化代码"消除的——它是物理定律 (磁盘 I/O)。
```

### 1.3 为什么一定要 fsync？

```
崩溃场景分析:

  Case 1: Write 后崩溃，未 fsync，未 rename
    ┌─────────────┐
    │ 页缓存: ✓   │ ← 数据在内存中
    │ 磁盘:   ✗   │ ← 未落盘
    │ tmp文件: ✗  │ ← 目录项也未持久化
    └─────────────┘
    → 重启后: tmp 不存在, 事务回滚 ✅ 安全

  Case 2: Write + fsync 后崩溃，未 rename
    ┌─────────────┐
    │ 页缓存: ✓   │
    │ 磁盘:   ✓   │ ← fsync 保证了
    │ tmp文件: ✓  │
    └─────────────┘
    → 重启后: CleanupTmp 清理 tmp, OK ✅ 安全

  Case 3: Write 后崩溃，未 fsync，但 rename 了 (★ 最危险)
    ┌─────────────┐
    │ 页缓存: ✓   │ ← rename 在页缓存中
    │ 磁盘:   ✗   │ ← 数据未落盘!
    │ lob_xxx.ao: ?│ ← 目录项可能持久化
    └─────────────┘
    → 重启后: 文件存在但内容为零/不完整!
    → BTree 指向 lobID=N → Read → 读到损坏数据 → 数据丢失! 💀

  ★ 这就是为什么 fsync 必须在 rename 之前 ★
```

---

## 二、方案 C：fdatasync + Group Fsync（零风险热修复）

> **定位**：Phase 1，立即实施。不改变架构，只优化现有的 fsync 路径。

### 2.1 核心改动一：fsync → fdatasync

```
fsync(2) 做了什么:
  ┌─────────────────────────────────────────────┐
  │  ① 刷新数据页 (dirty data pages)   ~2.0ms   │
  │  ② 刷新 inode (mtime, size)       ~1.5ms   │ ← 我们不依赖这些!
  └─────────────────────────────────────────────┘
  总计: ~3.5ms

fdatasync(2) 做了什么:
  ┌─────────────────────────────────────────────┐
  │  ① 刷新数据页 (dirty data pages)   ~2.0ms   │
  │  ② 只刷新 inode 中影响后续读的部分    ~0ms   │ ← 文件大小不变, 跳过
  └─────────────────────────────────────────────┘
  总计: ~2.0ms  (-43%)

适用条件 (我们都满足):
  ✅ .ao 文件写后不可变 — 文件大小只增一次
  ✅ TotalLen 存在 header 中 — 不依赖 inode size
  ✅ 不修改 mtime — 不需要刷新
```

**代码量**：一行。

```go
// 旧: f.Sync()
// 新: unix.Fdatasync(int(f.Fd()))
```

### 2.2 核心改动二：并行 flush

当前 `flush()` 是串行的：

```
当前 (10 并发写入, batch 收集后大家一起等):
  fd1.Sync() ────────────────────────→ 3.5ms
  fd2.Sync()   ────────────────────────→ 3.5ms  ← 串行!
  fd3.Sync()     ────────────────────────→ 3.5ms
  ...
  fd10.Sync()                               ──→ 3.5ms

  总 flush 延迟: 10 × 3.5ms = 35ms
  10 写入总延迟: 0.5ms(channel等) + 35ms(flush) = ~36ms
  QPS: 10/0.036 = 277
```

改为 `errgroup` 并行：

```
优化后 (10 并发写入, 并行 flush):
  fd1.Sync() ────────────→
  fd2.Sync() ────────────→
  fd3.Sync() ────────────→  全部并行, ~3.5ms 一起完成!
  ...                      │
  fd10.Sync() ────────────→

  总 flush 延迟: max(3.5ms) = 3.5ms  ← 只等最慢的一个
  10 写入总延迟: 0.5ms + 3.5ms = ~4.0ms
  QPS: 10/0.004 = 2,500
```

**代码量**：~30 行。

```go
import (
    "golang.org/x/sync/errgroup"
    "golang.org/x/sys/unix"
)

func (g *fsyncGroup) flush() {
    if len(g.batch) == 0 {
        return
    }
    var eg errgroup.Group
    if g.parallelism > 0 {
        eg.SetLimit(g.parallelism)
    }
    for _, e := range g.batch {
        e := e
        eg.Go(func() error {
            return unix.Fdatasync(int(e.fd.Fd()))
        })
    }
    _ = eg.Wait()
    for _, e := range g.batch {
        e.doneCh <- nil
    }
    g.batch = g.batch[:0]
}
```

### 2.3 效果预估

```
┌────────────────┬──────────┬──────────┬──────────┐
│                │   当前   │ fdatasync│ +Parallel│
├────────────────┼──────────┼──────────┼──────────┤
│ 1 并发 128KB   │ 127 QPS  │ ~180 QPS │   —      │
│ 8 并发 128KB   │ 277 QPS  │ ~400 QPS │ ~2,500   │
│ 16 并发 128KB  │ 277 QPS  │ ~400 QPS │ ~5,000   │
├────────────────┼──────────┼──────────┼──────────┤
│ 单条延迟       │ 7.9ms    │ 5.5ms    │ 5.5ms    │
│ 批量延迟(8条)  │ 28ms     │ 20ms     │ 5.5ms    │
└────────────────┴──────────┴──────────┴──────────┘
```

> **单线程提升来自 fdatasync（-43%），并发提升来自并行化（+9x）。**

### 2.4 风险：零

- fdatasync 是 POSIX 标准，Linux/macOS/FreeBSD 都支持
- 语义无变化：只刷数据不刷 inode，对写后不可变的 .ao 文件完全安全
- 并行 flush 用 errgroup.SetLimit 防止 I/O 风暴

---

## 三、方案 B：Async LOB + Checksum（低风险大跃进）

> **定位**：Phase 2。允许 Create() 不等待 fsync 即返回，用 checksum 在崩溃恢复时检测损坏。

### 3.1 核心思路

```
┌─────────────────────────────────────────────────────────────┐
│                      当前 (方案 C)                           │
│                                                             │
│  Create(data):                                              │
│    write(tmp) ──→ fdatasync ──→ rename ──→ 返回            │
│                   ~2ms 等待                                  │
│                                                             │
│  Commit():                                                  │
│    WAL.Append ──→ WAL.Sync ──→ BTree.Set                   │
│                  ~3.5ms                                      │
│                                                             │
│  总 fsync: 2 次 (LOB + WAL)                                 │
│  总延迟:  ~5.5ms                                            │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                      方案 B                                 │
│                                                             │
│  Create(data):                                              │
│    write(tmp) ──→ rename ──→ 返回  ← 0.3ms! 不等 fsync!    │
│    checksum = CRC32C(data)                                  │
│                                                             │
│  Commit():                                                  │
│    WAL: "LOB lobID=N checksum=X committed"                  │
│    WAL.Sync() ──→ BTree.Set  ← 唯一一次 fsync               │
│                                                             │
│  总 fsync: 1 次 (只有 WAL)                                   │
│  总延迟:  ~3.8ms (不变) 但 LOB write 不阻塞事务!              │
└─────────────────────────────────────────────────────────────┘
```

**关键洞察**：BTree WAL 已经在做 fsync 了——它是事务的持久化锚点。LOB 文件的 fsync 是多余的——只要 WAL 记录了 checksum，崩溃后可以检测并优雅处理。

### 3.2 时间线对比

```
方案 C (当前最优):
  T=0.0ms   Create() 开始
  T=0.3ms   数据写入页缓存 (write done)
  T=2.3ms   fdatasync 完成 ★ 等待磁盘
  T=2.4ms   rename 完成
  T=2.4ms   Create() 返回
  T=6.0ms   WAL.Sync() ★ 第二次等待磁盘
  T=6.1ms   Commit() 完成
  ─────────────────────────
  事务延迟: ~6.1ms

方案 B:
  T=0.0ms   Create() 开始
  T=0.3ms   数据写入页缓存 (write done)
  T=0.4ms   rename 完成 (页缓存内操作, 极快)
  T=0.4ms   Create() 返回 ★ 0.4ms 即返回!
  T=3.9ms   WAL.Sync() ★ 唯一一次等待磁盘
  T=4.0ms   Commit() 完成
  ─────────────────────────
  事务延迟: ~4.0ms (-34%)

  而且 Create() 和 BTree.Set 之间的窗口可以做其他事情!
```

```
  方案 C:  Create ████████████░░░░░░░░░░░░░░░░ Commit ████████████████████████
           write  fdatasync(2ms)  返回        WAL.Sync(3.5ms)              done
                  ↑ 阻塞在这里!

  方案 B:  Create ████░░░░░░░░░░░░░░░░░░░░░░░░ Commit ████████████████████████
           write  返回 (0.4ms!)                WAL.Sync(3.5ms)              done
                  ↑ 不等待!
```

### 3.3 崩溃恢复机制

```
崩溃恢复流程:

  Step 1: 重放 BTree WAL
    → BTree 恢复到 crash 前的逻辑状态
    → WAL 中有 "LOB lobID=42 committed, checksum=0xABCD1234"

  Step 2: 对每个 BTree 引用的 lobID:
    ┌──────────────────────────────────────────┐
    │ .ao 文件存在?                             │
    │   ├─ 存在 → 读取 → CRC32C 验证           │
    │   │   ├─ 匹配 → ✅ OK                     │
    │   │   └─ 不匹配 → ❌ 数据损坏              │
    │   │       → 返回 ErrLOBDataCorrupted      │
    │   │       → 上游 Ledgers 重放该事务        │
    │   └─ 不存在 → WAL 说 committed            │
    │       → rename 未持久化 (目录未落盘)       │
    │       → 返回 ErrLOBNotFound               │
    │       → 上游重放                           │
    └──────────────────────────────────────────┘
```

### 3.4 为什么需要上游支持重试

```
┌─────────────────────────────────────────────────┐
│                                                  │
│  方案 B 不保证 .ao 文件一定在磁盘上                │
│  (我们故意跳过了 fsync)                            │
│                                                  │
│  后果:                                            │
│    极端情况下 (崩溃时页缓存未落盘)                  │
│    → .ao 文件损坏/缺失                            │
│    → 校验和检测到                                 │
│    → 返回 ErrLOBDataCorrupted                    │
│    → 上游重放该事务 (Ledgers 支持全量重放)         │
│                                                  │
│  这不是"数据丢失"——这是"需要重试"                   │
│  (类似 TCP 重传——网络丢包不代表数据丢了)            │
│                                                  │
│  适用场景:                                        │
│    ✅ Ledgers 有事务日志, 支持重放                  │
│    ✅ 数据可从其他副本恢复 (多副本部署)             │
│    ❌ 唯一数据源, 无重放机制                       │
│                                                  │
└─────────────────────────────────────────────────┘
```

### 3.5 效果预估

```
┌────────────────┬──────────┬──────────┬──────────┐
│                │ 方案 C   │ 方案 B   │  提升    │
├────────────────┼──────────┼──────────┼──────────┤
│ Create() 延迟  │ 2.4ms    │ 0.4ms    │  6x      │
│ 事务延迟       │ 6.1ms    │ 4.0ms    │  1.5x    │
│ 1 并发 128KB   │ ~180 QPS │ ~250 QPS │  1.4x    │
│ 8 并发 128KB   │ ~2,500   │ ~3,500   │  1.4x    │
│ 单线程批处理   │ —        │ ~2,500   │  🆕      │
└────────────────┴──────────┴──────────┴──────────┘

关键收益: Create() 不再阻塞在 fsync
  → 应用层可以在 Commit 前做其他事情
  → 批处理: 10 个 Create 全部 0.4ms 返回, 一次性 WAL.Sync
```

---

## 四、方案 A：WAL-Integrated LOB Write（架构级优化）

> **定位**：Phase 3。消除 LOB 和 BTree 之间的"双重持久化"，统一到 BTree WAL。

### 4.1 当前问题：双重 fsync

```
当前架构:

  ┌─────────────┐         ┌──────────────┐
  │  LOB File    │         │  BTree       │
  │             │         │              │
  │ write tmp   │         │ WAL append   │
  │ fdatasync ★ │         │ WAL.fsync ★  │ ← 两次 fsync!
  │ rename      │         │ Set          │
  └─────────────┘         └──────────────┘
       │                        │
       └────────┬───────────────┘
                │
        两次独立的持久化操作
        磁盘写了两个不同的位置
        两次等待磁盘确认
```

**核心洞察**：BTree WAL 是"事务已提交"的唯一证据。如果 LOB 数据包含在 WAL 记录中（或 WAL 引用到），就不需要 LOB 自己做 fsync。

### 4.2 方案 A 架构

```
                     方案 A: 统一持久化

  ┌──────────────────────────────────────────────────────────┐
  │                      Commit                              │
  ├──────────────────────────────────────────────────────────┤
  │                                                          │
  │  LOB data (128KB)                                        │
  │    │                                                     │
  │    │  ① WAL-LOB Segment: 顺序追加                        │
  │    │     [lobID:8][len:4][data:N]                        │
  │    │     ↓ 内存操作, ~10μs                                │
  │    │                                                     │
  │  BTree KV (80B)                                          │
  │    │                                                     │
  │    │  ② WAL: "tx: keys=[k1,k2], lobIDs=[42,43]"         │
  │    └──→ ③ WAL.Sync() ★ 唯一一次 fsync!                   │
  │              ↓                                           │
  │         ④ BTree.Set()                                    │
  │                                                          │
  └──────────────────────────────────────────────────────────┘

  后台 Flusher (独立 goroutine, 不影响事务延迟):
  ┌──────────────────────────────────────────────────────────┐
  │  WAL-LOB Segment 满了 (64MB) 或 超时 (100ms):             │
  │    → 遍历 segment 中的 LOB records                        │
  │    → lz4 压缩 (可选)                                      │
  │    → 写入独立 lob_<id>.ao 文件                            │
  │    → fsync (后台, 不阻塞任何事务!)                         │
  │    → 标记 WAL-LOB segment 为 flushed                      │
  │    → segment 可被回收                                     │
  └──────────────────────────────────────────────────────────┘
```

### 4.3 WAL-LOB Segment 格式

```
WAL-LOB Segment (64MB, pre-allocated mmap):

  ┌──────────────────────────────────────────┐  ← offset 0
  │ Segment Header (64B)                     │
  │  Magic:       "NXWL"        [4]byte     │
  │  SegmentID:   42            uint64      │
  │  BaseLobID:   1000          uint64      │  ← 首个 lobID
  │  WritePos:    128340        uint64      │  ← 当前写入偏移
  │  Flushed:     false         uint32      │  ← 是否已落盘到 .ao
  │  Checksum:    0xA1B2C3D4   uint32      │  ← header CRC32C
  ├──────────────────────────────────────────┤  ← offset 64
  │                                          │
  │  LOB Record #1:                          │
  │  ┌────────────────────────────────────┐  │
  │  │ lobID:       1000      uint64      │  │
  │  │ dataLen:     131072    uint32      │  │
  │  │ checksum:    0x1234    uint32      │  │  ← data CRC32C
  │  │ data:        [131072]byte          │  │
  │  │ total:       16 + 131072 = 131088  │  │
  │  └────────────────────────────────────┘  │
  │                                          │
  │  LOB Record #2:                          │
  │  ┌────────────────────────────────────┐  │
  │  │ lobID:       1001      uint64      │  │
  │  │ ...                                 │  │
  │  └────────────────────────────────────┘  │
  │                                          │
  │  ... up to ~500 records (64MB/128KB)     │
  └──────────────────────────────────────────┘
```

### 4.4 Read 路径：三层查找

```
Read(ref{LOBID:42}):

  ┌─ ① 先查 BigCache (write-through, 大概率命中)
  │   hit → 返回 (~100ns)
  │   miss ↓
  │
  ├─ ② 再查 .ao 文件 (已被后台 flusher 落盘)
  │   os.Open("lob_42.ao") → ReadAt/mmap
  │   hit → 返回 (~50μs)
  │   miss ↓
  │
  └─ ③ 最后查 WAL-LOB Segment (还未 flush)
      顺序扫描 segment → 定位 lobID=42 → 返回 (~10μs)
      未找到 → ErrNotFound
```

### 4.5 崩溃恢复

```
Recovery:

  扫描所有 WAL-LOB Segment:
    对每个 segment:
      if segment.Flushed:
        跳过 (.ao 文件已就绪)
      else:
        遍历所有 LOB records:
          检查对应 .ao 文件:
            ✅ 存在 + checksum 匹配 → OK
            ❌ 不存在/损坏 → 从 WAL-LOB segment 重建 .ao 文件

  重放 BTree WAL → BTree refs 指向已就绪的 .ao 文件

  ★ 崩溃恢复后, 不丢失任何已提交事务的 LOB 数据 ★
```

### 4.6 批量事务性能

```
方案 A 的最大优势不在单事务, 而在批量:

  10 个事务, 每个 128KB LOB:
    方案 C: 10 × 5.5ms = 55ms (独立 fdatasync)
    方案 B: 10 × 4.0ms = 40ms (独立 WAL.Sync)
    方案 A: 1 × 3.5ms = 3.5ms! ← 所有 LOB 合并一次 WAL.Sync

  100 个事务:
    方案 C: 550ms
    方案 B: 400ms
    方案 A: 3.5ms  ← 100 倍差距!

  WAL-LOB segment 64MB 可容纳 ~500 条 128KB LOB
  500 条 × 一次 fsync = 3.5ms 总延迟
  平均单条延迟: 3.5ms/500 = 7μs ← 几乎免费!
```

### 4.7 效果预估

```
┌───────────────────┬──────────┬──────────┬──────────┐
│                   │ 方案 C   │ 方案 B   │ 方案 A   │
├───────────────────┼──────────┼──────────┼──────────┤
│ 单事务 128KB      │ ~180 QPS │ ~250 QPS │ ~260 QPS │
│ 10 事务批处理     │ ~18 QPS  │ ~25 QPS  │ ~285 QPS │  ← 15x!
│ 100 事务批处理    │ ~2 QPS   │ ~2.5 QPS │ ~285 QPS │  ← 142x!
│ 崩溃安全          │ ✅ 完全  │ ⚠️ 需重试 │ ✅ 完全  │
│ 代码量            │ ~55 行   │ ~90 行   │ ~500 行  │
└───────────────────┴──────────┴──────────┴──────────┘
```

---

## 五、三方案全景对比

```
                           复杂度 / 风险
                            ↑
                            │          ┌──────────┐
                            │          │ 方案 A   │  500行, 中风险
                            │          │ WAL统一  │  批处理王者
                            │          │          │
                            │   ┌──────┼──────────┤
                            │   │方案 B│          │  90行, 低风险
                            │   │Async │          │  单线程王者
                            │   │+CkSm │          │
                            │   │      │          │
              ┌─────────────┼───┼──────┤          │
              │  方案 C     │   │      │          │  55行, 零风险
              │  fdatasync  │   │      │          │  并发王者
              │  +Group     │   │      │          │
              │             │   │      │          │
              └─────────────┴───┴──────┴──────────┴──→ 单线程提升
                   +52%        +73%       +82%
```

```
                    批量性能对比 (10 事务 × 128KB)

  方案 C ████████████████████████████████████████████████  55ms
  方案 B ██████████████████████████████████                40ms
  方案 A ████                                               3.5ms
         ↑                                                ↑
    每个事务独立 fsync                          所有事务一次 fsync
```

### 决策矩阵

```
┌──────────────┬──────────────┬──────────────┬──────────────┐
│              │   方案 C     │   方案 B     │   方案 A     │
├──────────────┼──────────────┼──────────────┼──────────────┤
│ 适合场景     │ 任何场景     │ 有上游重放   │ 高频批处理   │
│ 不适合       │ —            │ 唯一数据源   │ 小团队       │
│ 单线程提升   │ +52%         │ +73%         │ +82%         │
│ 并发提升     │ +11x (8线程) │ +14x (8线程) │ +14x (8线程) │
│ 批处理提升   │ —            │ —            │ +142x        │
│ 崩溃安全     │ ✅ 完全      │ ⚠️ 需重试    │ ✅ 完全      │
│ 代码量       │ 55 行        │ 90 行        │ 500 行       │
│ 实施周期     │ 1 天         │ 2-3 天       │ 1-2 周       │
│ 风险等级     │ 零           │ 低           │ 中           │
│ 向后兼容     │ 完全         │ 完全         │ 需要迁移     │
└──────────────┴──────────────┴──────────────┴──────────────┘
```

---

## 六、推荐路径

```
  现在 ──────────────────────────────────────────→ 未来

  Phase 1 (当前 spike)         Phase 2 (下个 feature)       Phase 3 (未来架构)
  ┌──────────────────┐        ┌──────────────────┐        ┌──────────────────┐
  │ 方案 C           │        │ 方案 B           │        │ 方案 A           │
  │                  │        │                  │        │                  │
  │ fdatasync        │   →    │ Async + Checksum │   →    │ WAL-Integrated   │
  │ + Group Fsync    │        │                  │        │ LOB Write        │
  │                  │        │                  │        │                  │
  │ 成本: 55行, 0风险│        │ 成本: 90行, 低险 │        │ 成本: 500行, 中险│
  │ 提升: 并发 11x   │        │ 提升: 单线 1.4x  │        │ 提升: 批处理 142x│
  └──────────────────┘        └──────────────────┘        └──────────────────┘
       立即实施                   下个迭代                    Phase 3 WAL 就绪后

  为什么这个顺序:

  1. 方案 C 不改变任何语义 → 可以现在就做, 零风险
  2. 方案 B 需要上游支持重试 → 需要 Ledgers 配合, 下个迭代
  3. 方案 A 需要 WAL 段管理器 → 依赖 Phase 3 WAL 基础设施, 最快也要两个迭代后

  三个阶段都做完后, LOB 写入从 127 QPS 到 批处理 ~2,800 QPS
  → 总提升 22x
```

---

## 七、关键技术细节

### 7.1 fdatasync 在 macOS 上的行为

```
Linux:
  fdatasync(2) → 只刷数据, 不刷 inode (除非 size 变化)
  man 2 fdatasync: "does not flush modified metadata unless
  that metadata is needed in order to allow a subsequent data
  retrieval to be correctly handled"

macOS:
  没有 fdatasync(2) 系统调用!
  golang.org/x/sys/unix.Fdatasync → 回退到 unix.Fsync
  → 在 macOS 上无性能提升

  但生产部署在 Linux → 收益完整
  开发/测试在 macOS → 行为正确 (回退到 fsync)
```

### 7.2 并行 fsync 的并行度控制

```
I/O 并行度的经验法则:

  NVMe SSD:  并行度 = 4-8   (队列深度 32+)
  SATA SSD:  并行度 = 2-4   (队列深度 32)
  HDD:       并行度 = 1     (串行更快, 避免寻道风暴)

  默认: GOMAXPROCS (可配置)
  → errgroup.SetLimit(parallelism) 防止 I/O 风暴
```

### 7.3 校验和为什么选 CRC32C

```
| 算法      | 速度 (GB/s) | 碰撞概率 (64KB) | 硬件加速       |
|-----------|:---------:|:------------:|-------------|
| CRC32C    | ~20       | 2^-32        | ✅ x86 SSE4.2 |
| xxhash64  | ~15       | 2^-64        | ❌ 纯软件     |
| SHA256    | ~0.5      | 2^-256       | ✅ ARM crypto |

选 CRC32C:
  - x86 有 SSE4.2 硬件指令 (crc32q), 极快
  - 2^-32 碰撞概率对崩溃恢复够用 (一秒检测一次也要 136 年才撞一次)
  - Go 标准库 hash/crc32 支持 Castagnoli 表
```

---

## 八、相关文件

| 文件 | 相关度 |
|------|:--:|
| `internal/infrastructure/storage/lob/file_store.go` | ★★★★★ 三个方案都改这里 |
| `internal/infrastructure/storage/lob/config.go` | ★★★★ 方案 C/B 的配置项 |
| `internal/infrastructure/storage/lob/lob_cache.go` | ★★★ BigCache (已规划, 方案 A 依赖) |
| `internal/infrastructure/storage/mvcc/lob.go` | ★★★ 方案 B 的 checksum 传递 |
| `internal/infrastructure/storage/mvcc/transaction.go` | ★★ 方案 B 的 Commit 路径 |
| `internal/infrastructure/storage/wal/` | ★★★★★ 方案 A 的段管理器 |
| `docs/07_spike/btree-refactor/2026-06-09-btree-lob-spike.md` | ★★★★ 主文档 §十一/十二 |
