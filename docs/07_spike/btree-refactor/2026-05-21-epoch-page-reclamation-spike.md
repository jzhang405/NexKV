# Epoch-based Page Reclamation Spike

> 创建日期：2026-05-21
> 前置：BTree COW 架构（Phase 5）+ Tombstone 补全
> 参考：Lealone ChunkManager / PageReference / ChunkCompactor
> 状态：Planning

---

## 一、问题定义

### 1.1 现状：COW 页面泄漏

BTree COW 写路径每次 Set/Delete 都分配新物理页面，旧页面从未回收：

```
Set(key, val):
  oldLeaf = GetLeafPage(leafRef.pInfo.pageID)   // pageID = N
  newLeaf = AllocLeafPage()                       // pageID = N+1 (COW copy)
  newLeaf.Update(idx, val)
  leafRef.CAS(oldInfo → newInfo)                  // 原子切换到新页
  // ★ 旧页面 N 永远不会被 FreePage → 泄漏 4KB/op
```

**后果**：
- Benchmark 写测试 OOM：`131,072 pages used / 131,072 total`
- 生产环境长时间运行后页面池耗尽
- 每个 Set 泄漏 1 页（4KB），每个 Delete 同上

### 1.2 为什么不能 CAS 后立即 FreePage

```go
if leafRef.CAS(oldInfo, newInfo) {
    b.storage.FreePage(oldInfo.PageID)  // ← 竞态！
}
```

**竞态时序**：

```
T0: reader: searchPath → GetPageInfo(leafRef) → oldInfo (pageID=N)
T1: writer: CAS(oldInfo → newInfo) ✓
T2: writer: FreePage(N)              ← N 回收到 free list
T3: writer2: AllocLeafPage() → N     ← N 被重用
T4: writer2: InitLeafPage(N) → count=0
T5: reader: GetLeafPage(N)           ← 读到 count=0 → PANIC
```

---

## 二、Lealone 方案分析

Lealone 的页面生命周期管理分布在三个层级：

| 层级 | 组件 | 职责 |
|------|------|------|
| Chunk 层 | `Chunk` / `ChunkManager` / `ChunkCompactor` | Chunk 级空间回收 |
| Page 层 | `PageReference` / `PageInfo` / `PageOperations` | 单页状态管理 |
| GC 层 | `BTreeGC` | 内存中 Page 对象驱逐 |

### 2.1 ChunkManager：全局 removedPages

**文件**：`lealone-aose/.../chunk/ChunkManager.java`

```java
public class ChunkManager {
    // 全局已删除页面集 — ConcurrentSkipListSet 保证并发安全
    private ConcurrentSkipListSet<Long> removedPages = new ConcurrentSkipListSet<>();

    // Chunk ID → Chunk 映射
    private ConcurrentHashMap<Integer, Chunk> chunks;

    // Chunk ID 位图（用于分配新 Chunk ID）
    private BitField chunkIds;

    public void addRemovedPage(long removedPage) {
        this.removedPages.add(removedPage);
    }

    public ConcurrentSkipListSet<Long> getAllRemovedPages() {
        return removedPages;
    }
}
```

### 2.2 Chunk：页面计数与填充率

**文件**：`lealone-aose/.../chunk/Chunk.java`

```java
public class Chunk {
    int pageCount;                          // 总页面数
    long sumOfPageLength;                   // 总页大小
    long sumOfLivePageLength;               // 存活页总大小
    HashSet<Long> removedPages;             // 本 Chunk 内的已删除页面
    ConcurrentHashMap<Long, Integer> pagePositionToLengthMap;  // pos → 页大小

    // 填充率 = 1 + 98 * liveLength / totalLength（Lealone 特有公式）
    int getFillRate() {
        if (sumOfPageLength == 0) return 1;
        return 1 + (int) (98 * sumOfLivePageLength / sumOfPageLength);
    }
}
```

**关键语义**：
- `fillRate == 1`：Chunk 完全空，可整块删除
- `fillRate <= minFillRate`（默认 30，上限 50）：值得压缩的低填充率 Chunk

### 2.3 PageReference.markDirtyPage：页面脏标记 → removedPages

**文件**：`lealone-aose/.../page/PageReference.java`

Lealone 中 **页面回收的入口是 markDirtyPage()**——当页面被修改（COW），旧页面位置标记为 removed：

```java
public class PageReference {
    private volatile PageInfo pInfo;  // 原子引用（AtomicReferenceFieldUpdater）

    void markDirtyPage(long oldPos) {
        // 1. 将旧位置加入全局 removedPages
        getBtreeStorage().getChunkManager().addRemovedPage(oldPos);
        // 2. 更新 PageInfo 指向新位置
        // ...
    }

    Page getOrReadPage() {
        // 惰性加载：内存命中 → 返回；未命中 → 从 Chunk 反序列化
    }

    void replacePage(PageInfo oldInfo, PageInfo newInfo) {
        // CAS 替换 PageInfo（类似 NexKV 的 leafRef.CAS）
    }
}
```

### 2.4 Page.updateChunk：写入时注册页面到 Chunk

**文件**：`lealone-aose/.../page/Page.java`

```java
static void updateChunk(ChunkManager chunkManager, Chunk chunk, Page p, 
                        long pos, int pageLength, int pageCount) {
    // 将页面位置和长度写入 Chunk 的 pagePositionToLengthMap
    chunk.pagePositionToLengthMap.put(pos, pageLength);
    chunk.pageCount += pageCount;
    chunk.sumOfPageLength += pageLength;
    chunk.sumOfLivePageLength += pageLength;
    chunkManager.addChunk(chunk);
}
```

### 2.5 ChunkCompactor：空间回收

**文件**：`lealone-aose/.../chunk/ChunkCompactor.java`

```java
public class ChunkCompactor {
    static final int MAX_REWRITE_SIZE = 64 * 1024 * 1024; // 64MB = chunkSize / 4
    int minFillRate = 30;  // 默认 30，上限 50

    void executeCompact() {
        // 1. 收集全局 removedPages
        ConcurrentSkipListSet<Long> removedPages = 
            chunkManager.getAllRemovedPages();
        if (removedPages.isEmpty()) return;

        // 2. 遍历所有 Chunk（跳过 lastChunk）
        // 3. 计算 fillRate
        // 4. fillRate == 1 → unusedChunks（可直接删除整个 Chunk）
        // 5. fillRate <= minFillRate → rewritable（需重写存活页）
        // 6. 对 rewritable Chunk 设置 compacting 标记（写屏障）
        // 7. 按 fillRate 升序排序，贪心选择（累计 ≤ MAX_REWRITE_SIZE）
        // 8. 将选中 Chunk 的存活页重写到新 Chunk
        // 9. 新 Chunk Sync → Checkpoint 刷新 pageLocs
        // 10. 两阶段删除旧 Chunk：rename → .ao.deleting → os.remove
    }
}
```

### 2.6 BTreeStorage.save：压缩与持久化协调

**文件**：`lealone-aose/.../btree/BTreeStorage.java`

```java
void save() {
    // 1. 执行 ChunkCompactor（写入前回收空间）
    chunkCompactor.executeCompact();

    // 2. 将脏页写入新 Chunk（COW 语义：新版本写入新位置）
    //    → Page.updateChunk() 注册到新 Chunk
    //    → 旧位置的页面已在 markDirtyPage() 时加入 removedPages

    // 3. 写入 Checkpoint（rootPagePos, pageLocs）
    // 4. 删除完全空的旧 Chunk 文件
}
```

### 2.7 BTreeGC：内存驱逐（不同于空间回收）

**文件**：`lealone-aose/.../btree/BTreeGC.java`

BTreeGC 负责从 **JVM 堆内存** 中驱逐 Page 对象和 ByteBuffer（释放堆内存），与 ChunkCompactor 的 **磁盘空间** 回收是独立的两层：

```
BTreeGC:      驱逐 Page 对象、ByteBuffer → 释放 JVM 堆
ChunkCompactor: 重写低填充率 Chunk → 释放 .ao 文件磁盘空间
```

---

## 三、NexKV 适配方案

### 3.1 Lealone Chunk 模型 vs NexKV mmap 池

| 维度 | Lealone | NexKV |
|------|---------|-------|
| 存储单位 | Chunk（~256MB，含多页） | mmap offheap 页池（~512MB，逐页管理） |
| 页面位置 | `long pos = chunkId(30) + offset(32)` | `uint32 pageID` |
| 空间回收粒度 | Chunk 级（整块 free 或 rewrite） | Page 级（逐页 free） |
| 已删除跟踪 | `ConcurrentSkipListSet<Long> removedPages` | 需要类似结构 |
| 写入模型 | 批量 save（多页写入一个 Chunk） | 逐页 CAS |

### 3.2 适配策略：Page-level Epoch-based Reclamation

NexKV 不需要 Chunk 概念，但需要一个 **延迟释放机制** 来解决 1.2 节的竞态问题。

```
┌──────────────────────────────────────────────────────────────┐
│  CAS 成功后不立即 FreePage(oldPageID)                        │
│  而是加入 EpochManager.deferFreeList[globalEpoch]            │
│  仅当所有使用该 epoch 的 reader 都退出后，才真正 FreePage    │
└──────────────────────────────────────────────────────────────┘
```

### 3.3 EpochManager 设计

```go
// btree/epoch.go

type EpochManager struct {
    global    atomic.Uint64          // 全局 epoch（每写递增）
    readers   [64]atomic.Uint64      // per-CPU reader epoch 登记
    deferred  []deferredBatch        // 按 epoch 分组的待释放页
    mu        sync.Mutex             // 保护 deferred drain
    freeFunc  func(model.PageID)     // storage.FreePage
}

type deferredBatch struct {
    epoch   uint64
    pageIDs []model.PageID
}
```

### 3.4 写路径集成

```go
// operations.go — writeOperation CAS 成功路径
if leafRef.CAS(oldInfo, newInfo) {
    if b.epochMgr != nil {
        b.epochMgr.deferFree(oldInfo.PageID)
    }
    path.ReleaseAll()
    b.size.Add(result.delta)
    return nil
}
```

### 3.5 读路径集成

```go
// search.go — searchPath
func searchPath(rootRef *RootPageRef, key []byte) (SearchPath, error) {
    slot := cpuID() % 64
    em.readers[slot].Store(em.global.Load())      // 注册
    defer em.readers[slot].Store(0)                // 退出
    // ... 现有搜索逻辑 ...
}
```

### 3.6 安全回收

```go
func (em *EpochManager) tryReclaim() {
    safeEpoch := em.computeSafeEpoch()  // min(所有活跃 reader epoch)
    for i, batch := range em.deferred {
        if batch.epoch < safeEpoch {
            for _, pageID := range batch.pageIDs {
                em.freeFunc(pageID)     // ★ 安全：无 reader 引用此 epoch
            }
            em.deferred[i].pageIDs = nil
        }
    }
}
```

### 3.7 与现有 ChunkCompactor 的关系

Phase 4.4 实现的 `ChunkCompactor` 处理 **AO 文件级别** 的空间回收（重写低填充 .ao 文件）。EpochManager 处理 **mmap 页面池** 的 COW 旧页回收。两者互补：

| | ChunkCompactor | EpochManager |
|---|---|---|
| 层级 | .ao 文件 (Chunk) | mmap 页面池 (Page) |
| 触发 | Checkpoint 后异步 | 每次 COW 写后 |
| 回收目标 | removedPages → 低填充 Chunk | COW 旧页 → FreePage |
| 安全保证 | compacting 标记 + 写屏障 | epoch-based 延迟释放 |

---

## 四、实现计划

### Step 1: EpochManager 骨架

```go
// btree/epoch.go — 新文件 (~120 行)
// EpochManager + deferredBatch + EnterRead/ExitRead + deferFree + tryReclaim
```

### Step 2: searchPath 集成 reader epoch

```go
// search.go — register reader slot before traversal, unregister on return
```

### Step 3: writeOperation 集成 deferFree

```go
// operations.go — CAS 成功后 deferFree(oldPageID) + tryReclaim()
```

### Step 4: 测试验证

- `TestEpochManager_DeferAndReclaim`: 基础延迟释放/回收
- `TestEpochManager_SafeEpoch`: reader 未退出 → 不回收
- `TestCOWWrite_NoPageLeak`: 基准测试不再 OOM
- `TestConcurrentReadWrite_EpochSafety`: epoch 竞态安全

---

## 五、风险与缓解

| 风险 | 缓解 |
|------|------|
| per-reader slot 增加 2 atomic ops | per-CPU slot 避免 cache line 竞争 |
| epoch 长时间不推进 → 延迟页堆积 | 每次写后 tryReclaim；可加定期强制 GC |
| reader 长时间持有 → 阻塞 safeEpoch 推进 | reader timeout（如 5s），超时视为退出 |
| 与 refCount 机制重叠 | epoch 延迟释放是正交的：refCount 管理 PageRef 生命周期，epoch 管理物理页面生命周期 |

---

## 六、Lealone 源码对应

### ChunkManager：全局 removedPages + Chunk 生命周期

```java
// lealone-aose/.../chunk/ChunkManager.java
public class ChunkManager {
    // 全局已删除页集合 — ConcurrentSkipListSet 天然并发安全
    private final ConcurrentSkipListSet<Long> removedPages = new ConcurrentSkipListSet<>();
    private final ConcurrentHashMap<Integer, Chunk> chunks = new ConcurrentHashMap<>();
    private final BitField chunkIds = new BitField();
    private Chunk lastChunk;

    // PageReference 标记脏页时调用此方法
    public void addRemovedPage(long pos) {
        removedPages.add(pos);
    }

    // ChunkCompactor 获取全局 + lastChunk 的已删除页并集
    public HashSet<Long> getAllRemovedPages() {
        HashSet<Long> removedPages = new HashSet<>(this.removedPages);
        if (lastChunk != null)
            removedPages.addAll(lastChunk.getRemovedPages());
        return removedPages;
    }
}
```

### Chunk：fillRate + per-chunk removedPages

```java
// lealone-aose/.../chunk/Chunk.java
public class Chunk {
    public int pageCount;
    public long sumOfPageLength;
    public long sumOfLivePageLength;
    public final ConcurrentHashMap<Long, Integer> pagePositionToLengthMap;
    private HashSet<Long> removedPages;     // per-chunk removed

    // fillRate = 0 (空) ~ 100 (满)，含边界处理
    int getFillRate() {
        if (sumOfLivePageLength <= 0) return 0;
        if (sumOfLivePageLength == sumOfPageLength) return 100;
        return 1 + (int) (98 * sumOfLivePageLength / sumOfPageLength);
    }
}
```

### ChunkCompactor：三阶段压缩

```java
// lealone-aose/.../chunk/ChunkCompactor.java
public class ChunkCompactor {
    void executeCompact() {
        HashSet<Long> removedPages = chunkManager.getAllRemovedPages();
        if (removedPages.isEmpty()) return;

        // Phase 1: readChunks — 读取被删除页涉及的所有 Chunk
        List<Chunk> chunks = readChunks(removedPages);

        // Phase 2: findUnusedChunks — 计算 sumOfLivePageLength，筛选完全空的 Chunk
        List<Chunk> unusedChunks = findUnusedChunks(chunks, removedPages);

        // Phase 3: prepareRewrite — 筛选 fillRate <= minFillRate 的 Chunk，贪心选择重写
        chunks.removeAll(unusedChunks);
        prepareRewrite(chunks, removedPages);
    }

    // 按 fillRate 升序排序，贪心选择直到累计 liveSize > MAX_SIZE
    private List<Chunk> getRewritableChunks(List<Chunk> chunks) { ... }
}
```

### PageReference：reclamation 入口

```java
// lealone-aose/.../page/PageReference.java
public class PageReference {
    private static final AtomicReferenceFieldUpdater<PageReference, PageInfo>
        pageInfoUpdater = AtomicReferenceFieldUpdater
            .newUpdater(PageReference.class, PageInfo.class, "pInfo");
    private volatile PageInfo pInfo;

    // ★ 页面回收的入口：标记脏页 → 将旧位置加入 removedPages
    private int markDirtyPage1(PageListener oldPageListener) {
        while (true) {
            PageInfo pInfoOld = this.pInfo;
            // ... 状态检查 ...
            PageInfo pInfoNew = pInfoOld.copy(0);
            pInfoNew.buff = null;
            if (replacePage(pInfoOld, pInfoNew)) {      // CAS 替换 PageInfo
                if (pInfoOld.getPos() != 0) {
                    addRemovedPage(pInfoOld.getPos());   // ★ 旧位置 → removedPages
                }
                return 0;
            }
        }
    }

    // 写入后更新位置：已解锁 → 更新 pos；锁中 → 新位置也标记为 removed
    public void updatePage(long newPos, PageInfo pInfoOld, boolean isLocked, ...) {
        if (isLocked) {
            addRemovedPage(newPos);
            return;
        }
        // ... CAS 更新 PageInfo ...
    }
}
```

### 与 NexKV 的映射

| Lealone | NexKV 当前 | NexKV 适配后 |
|---------|-----------|-------------|
| `ChunkManager.removedPages` (ConcurrentSkipListSet) | 无全局集合 | `EpochManager.deferred` (epoch-batched) |
| `ChunkManager.addRemovedPage(pos)` | 无调用 | `EpochManager.deferFree(pageID)` |
| `PageReference.markDirtyPage1()` | `writeOperation` CAS 成功路径 | CAS 后调用 `deferFree` |
| `ChunkCompactor.executeCompact()` | `ChunkCompactor` (Phase 4.4，AO 文件级) | `ChunkCompactor` 继续处理 .ao 文件；`EpochManager.tryReclaim()` 处理 mmap 页池 |
| `Chunk.fillRate` → chunk 回收 | `removedPages` per-ChunkFile | 不需要 fillRate（无 Chunk 概念）；直接 `FreePage` |

---

## 七、参考

- Lealone `Chunk.java`: `thoughts/Lealone/lealone-aose/.../chunk/Chunk.java`
- Lealone `ChunkManager.java`: `thoughts/Lealone/lealone-aose/.../chunk/ChunkManager.java`
- Lealone `ChunkCompactor.java`: `thoughts/Lealone/lealone-aose/.../chunk/ChunkCompactor.java`
- Lealone `PageReference.java`: `thoughts/Lealone/lealone-aose/.../page/PageReference.java`
- Lealone `Page.java`: `thoughts/Lealone/lealone-aose/.../page/Page.java`
- Lealone `BTreeStorage.java`: `thoughts/Lealone/lealone-aose/.../btree/BTreeStorage.java`
- Lealone 深度分析: `docs/07_spike/btree-refactor/2026-04-01-lealone-btree-deep-dive.md`
- BTree 路线图: `docs/07_spike/btree-refactor/2026-04-02-btree-refactor-roadmap.md`
- Tombstone 缺口分析: `docs/07_spike/btree-refactor/2026-05-20-btree-delete-tombstone-gaps.md`

---

**文档版本**：v2.0
**状态**：Planning
