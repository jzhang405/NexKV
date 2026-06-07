# PR 文档 — Fix：PersistCheckpoint 增加时间 + 内存阈值保底 Save

> **文档类型**：Bug Fix 前置规划  
> **日期**：2026-06-07  
> **作者**：jzhang405  
> **关联分支**：`fix/persist-checkpoint-idle-save`  
> **关联 PR**：`feat(persist)` (#fa18f8c)

---

## 一、问题描述

### 当前状态

`PersistCheckpoint` 装饰器通过 `ckptInterval` 控制 Checkpoint 触发频率——每 N 条 Set() 触发一次异步 `save()`。

### 缺失机制

**只有一个触发条件（数量阈值），缺少时间阈值和脏页量阈值。对标 Lealone 的 `LogSyncService`（loopInterval=3000ms + checkpoint 周期 + 脏页量判断 `collectDirtyMemory()`），当前设计存在三个安全漏洞：**

| 触发条件 | Lealone | NexKV 当前 | 风险 |
|---------|---------|:--------:|------|
| 数量阈值 | — | `ckptInterval` | ✅ 已有 |
| 时间阈值 | `LogSyncService.loopInterval = 3000ms` | ❌ 无 | 如果写入停止，数据永不落盘 |
| 内存/脏页阈值 | `map.collectDirtyMemory()` → 脏页量 | ❌ 无 | 高频写入时脏页积压、内存放大、save 耗时爆炸 |

### 漏洞场景

```
场景 A（空闲丢失）:
  Set() 9,999 条后停止。ckptInterval=10000 → 永不触发 save
  进程崩溃 → 9,999 条全部丢失（最大丢失窗口=∞）

场景 B（写入积压 — 脏页膨胀）:
  Set() 持续 1M QPS。每次 save() 遍历并序列化所有脏页 ~5-50ms
  高频写入导致脏页快速累积，下一次 save 时脏页量翻倍
  → 内存压力增大、单次 save 耗时暴涨、save 排队积压
  → 极端情况下 OOM 或 save 阻塞所有后续写入

场景 C（复合 — 最常见）:
  高峰期写入 → 脏页量远超 ckptInterval 覆盖范围 → save 耗时失控
  空闲期 → 无人触发 save → 最后一批数据悬浮在内存中
```

### 根本原因

对标 Lealone 源码分析：

```java
// LogSyncService.java:106-142 — 后台线程
while (running) {
    // ① 有事务 RedoLog → 立即写入+sync
    if (redoLogRecordCount.get() > 0)
        redoLog.save();           // fwrite + fsync

    // ② 定时触发 checkpoint（时间阈值）
    if (lastCheckedAt + cpLoopInterval < now || hasForceCheckpoint()) {
        checkpointService.run();  // 脏页序列化 + fsync + 截断 RedoLog
        redoLog.clearIdleBuffers(now);
        lastCheckedAt = now;
    }

    // ③ 空闲等待
    if (redoLogRecordCount.get() > 0)
        continue;
    awaiter.doAwait(loopInterval); // 默认 3s
}

// BTreeStorage.save() 中的脏页量阈值:
// collectDirtyMemory() 返回脏页字节数，决定创建新 chunk 还是 append
// 等效于「脏页量超过阈值 → 立即触发 save」
```

NexKV 当前只有 **数量 → save**，缺少 **时间 → save** 和 **脏页量 → save**。

---

## 二、修复方案

### 核心思路

在 `PersistCheckpoint` 中增加**后台 goroutine**，对标 Lealone 的 `LogSyncService.run()`，统一管理三个触发维度。

```
触发条件（三个维度，本次全部实现）:

  ① 数量阈值（已有）: setCount % ckptInterval == 0 → asyncSave
  ② 时间阈值（新增）: 超过 maxIdleDuration 无写入 → 强制 save
  ③ 脏页阈值（新增）: save() 内部迭代中脏页量超过 maxDirtyBytes → 截断分批
```

### 机制②：时间阈值 — 后台 idle check goroutine

```go
// 对标 Lealone LogSyncService.run() 的循环 + awaiter.doAwait(loopInterval)

type PersistCheckpoint struct {
    // ... 现有字段 ...
    maxIdleDuration time.Duration  // 默认 3s（对标 Lealone 3000ms）
    lastSavedCount  atomic.Uint64  // 上次 save 成功时的 setCount（修复：防止 idle check 误判）
    wg              sync.WaitGroup
    ctx             context.Context
    cancel          context.CancelFunc
}

// 启动时:
func NewPersistCheckpoint(...) *PersistCheckpoint {
    // ...
    p.ctx, p.cancel = context.WithCancel(context.Background())
    p.wg.Add(1)
    go p.runIdleCheckLoop()
    return p
}

func (p *PersistCheckpoint) runIdleCheckLoop() {
    defer p.wg.Done()
    ticker := time.NewTicker(p.maxIdleDuration)
    defer ticker.Stop()

    lastTickCount := p.setCount.Load()
    for {
        select {
        case <-p.ctx.Done():
            return
        case <-ticker.C:
            cur := p.setCount.Load()
            // 修复: 比较上次 save 成功时的计数，而非上次 tick 时的计数
            // cur == lastTickCount → 无新写入
            // cur > p.lastSavedCount → 有未持久化数据
            // 防止: 正常写入（CAS失败/正在save中）被误判为 idle
            if cur == lastTickCount && cur > p.lastSavedCount.Load() &&
                p.saving.CompareAndSwap(false, true) {
                go func() {
                    defer p.saving.Store(false)
                    p.asyncSave()
                }()
            }
            lastTickCount = cur
        }
    }
}

// saveInternal 成功后:
func (p *PersistCheckpoint) saveInternal() error {
    // ... 原有逻辑 ...
    p.lastSavedCount.Store(p.setCount.Load())  // 记录本次 save 时的 setCount
    return nil
}

// Close 确保 idle goroutine 退出:
func (p *PersistCheckpoint) Close() error {
    // ... 原有 timeout save 逻辑 ...
    p.cancel()
    p.wg.Wait()  // 等待 idle goroutine 退出
    return p.KVStore.Close()
}
```

效果：
- 场景 A: 9,999 条 → 停止 → 3s 后强制 save → **崩溃最多丢 3s 的数据**
- 场景 C 的空闲部分: waiter 保底触发，不依赖新写入
- **修复**: `lastSavedCount` 防止「写入正在进行但 CAS 暂失败」被误判为 idle

### 机制③：脏页阈值 — Lealone 方式（page allocator 计数器, O(1)）

> **核心思路**：对标 Lealone 的 `page.setDirty() → dirtyMemory += pageSize → collectDirtyMemory()`。每次 COW 分配新页面时在 page allocator 层累加计数器，外层 O(1) 读取。

#### 新增 BTree 方法（`OffheapBTreeStorage`，3 处 insertion point）

> **关键**：COW 分配有三条路径，都需计数：
> | 路径 | 触发场景 | `dirtyBytes` 插入点 |
> |------|---------|-------------------|
> | `AllocLeafPage()` | 首次分配叶子页（INSERT） | `return pageID, nil` 前 |
> | `AllocNodePage()` | 首次分配内部页（split） | `return pageID, nil` 前 |
> | `copyPage()` | **COW 复制已有页（大热点）** | `pm.Alloc()` 后 |
>
> `TryInPlace`（CAS 原地替换）不分配新页面——不计入。`LoadPage()`（AO 恢复）不计入——非脏写。

```go
// offheap_storage.go — 新增字段
type OffheapBTreeStorage struct {
    // ... 现有字段 ...
    dirtyBytes atomic.Uint64  // COW 分配的脏页字节数（对标 Lealone dirtyMemory）
}

// ① AllocLeafPage: 首次分配叶子页
func (s *OffheapBTreeStorage) AllocLeafPage() (model.PageID, error) {
    if err := s.checkOpen(); err != nil {
        return 0, err
    }
    rawID, err := s.pm.Alloc()
    if err != nil {
        return 0, errpkg.BTreeAllocLeafPage(err)
    }
    s.pa.InitLeafPage(rawID, 1)
    s.dirtyBytes.Add(uint64(offheap.PageSize)) // ← 新增
    return model.PageID(rawID), nil
}

// ② AllocNodePage: 首次分配内部页
func (s *OffheapBTreeStorage) AllocNodePage() (model.PageID, error) {
    if err := s.checkOpen(); err != nil {
        return 0, err
    }
    rawID, err := s.pm.Alloc()
    if err != nil {
        return 0, errpkg.BTreeAllocNodePage(err)
    }
    s.pa.InitIndexPage(rawID, 1)
    s.dirtyBytes.Add(uint64(offheap.PageSize)) // ← 新增
    return model.PageID(rawID), nil
}

// ③ copyPage: COW 复制已有页面（大热点——每次 Update/Insert 都走这里）
func (s *OffheapBTreeStorage) copyPage(rawSrcID uint32) (uint32, error) {
    srcVersion := s.pa.GetVersion(rawSrcID)
    newRawID, err := s.pm.Alloc()
    if err != nil {
        return 0, errpkg.BTreeAllocForCOW(err)
    }
    // ... 拷贝逻辑 ...
    s.dirtyBytes.Add(uint64(offheap.PageSize)) // ← 新增：COW 热点
    return newRawID, nil
}

// DirtyBytes 返回自上次 Reset 以来的脏页字节数（O(1)）
func (b *BTree) DirtyBytes() uint64 {
    return b.storage.dirtyBytes.Load()
}

// ResetDirtyBytes 在 save 成功后重置（对标 Lealone save 后截断）
func (b *BTree) ResetDirtyBytes() {
    b.storage.dirtyBytes.Store(0)
}
```

> TryInPlace（原地 CAS）不分配新页面 → 不计入 `dirtyBytes`。处理现有 key 的 UPDATE 场景走 `copyPage()`（大热点）——这是 dirtyBytes 计数的主要来源。

#### PersistCheckpoint 中使用

```go
// 内置 dirtyBytesReader 接口（与 DirtyPageProvider 分离）
type dirtyBytesReader interface{ DirtyBytes() uint64 }

// 改造前: totalBytes += len(item.PageData) (遍历中统计)
// 改造后: O(1) 原子读
func (p *PersistCheckpoint) saveInternal() error {
    // ... enumerate 后写入逻辑 ...

    // 脏页量检查（O(1)，对标 Lealone collectDirtyMemory）
    totalBytes := int64(p.dirtyReader.DirtyBytes())
    if totalBytes > maxDirtyBytesPerSave && p.dirtyWarned.CompareAndSwap(false, true) {
        log.Printf("persist checkpoint: dirty bytes %d > max %d, "+
            "consider reducing ckptInterval (current=%d)", totalBytes, maxDirtyBytesPerSave, p.ckptInterval)
    } else if totalBytes <= maxDirtyBytesPerSave {
        p.dirtyWarned.Store(false)
    }

    // ... 写入完成后重置
    p.dirtyReader.ResetDirtyBytes()
    p.lastSavedCount.Store(p.setCount.Load())
}
```

#### 与 Lealone 对标

```
Lealone                                NexKV（方案 C）
───────                                ──────────────
put() → page.setDirty()                COW alloc → dirtyBytes.Add(4KB)
        dirtyMemory += pageSize

save() → collectDirtyMemory()          DirtyBytes() ← O(1) 原子读
       → dirtyMemory < maxChunkSize     → totalBytes > 256MB → warn
       → dirtyMemory >= maxChunkSize    → totalBytes <= 256MB → ok
       → new Chunk / append             → (append Phase 2)

save 后 → dirtyMemory 重置              ResetDirtyBytes()
```

### 默认参数

| 参数 | 默认值 | 对标 Lealone |
|------|--------|------------|
| `ckptInterval` | 10000 | — |
| `maxIdleDuration` | **3s** | `LogSyncService.loopInterval = 3000ms` |
| `maxDirtyBytesPerSave` | **256MB** | `MAX_CHUNK_SIZE = 256MB` |

### 修复后效果

```
场景 A（修复前）: 9,999 条 → 停止 → 永不 save → 崩溃全丢
场景 A（修复后）: 9,999 条 → 停止 → 3s 后强制 save → 崩溃最多丢 3s

场景 B（修复前）: 1M QPS → 脏页快速膨胀 → save 耗时失控
场景 B（修复后）: 1M QPS → maxDirtyBytes 告警 → 用户降低 ckptInterval
                 → 更频繁的 save 控制脏页量在合理范围

场景 C（修复前）: 高峰期膨胀 + 空闲期悬浮
场景 C（修复后）: 高峰期告警 + 空闲期 3s 保底 → 全生命周期覆盖
```

### 三种机制对照 Lealone

```
Lealone                                NexKV（修复后）
───────                                ────────────────
① LogSyncService.run() 循环            runIdleCheckLoop()  ← 机制②
    ↓
   redoLog.save() on record count       maybeTriggerCkpt() ← 机制①
   checkpointService.run() on timer     runIdleCheckLoop() ← 机制②
   awaiter.doAwait(loopInterval)        ticker.C @ 3s       ← 机制②

② BTreeStorage.save()
    ↓
   collectDirtyMemory()                 enumerateFn 遍历   ← 机制③
   dirtyMemory < maxChunkSize → append  totalBytes vs 256MB ← 机制③
   dirtyMemory >= maxChunkSize → new chunk  (chunk append Phase 2)
```

---

## 三、改动范围

| 文件 | 改动量 | 说明 |
|------|:------:|------|
| `persist/persist_checkpoint.go` | +70 行 | +idle goroutine + maxDirtyBytes 告警 + dirtyBytesReader 接口 + Close 更新 |
| `btree/offheap_storage.go` | **+8 行** | AllocLeafPage/AllocNodePage/copyPage 中 `dirtyBytes.Add(PageSize)` + DirtyBytes/ResetDirtyBytes（3 处 COW 入口全覆盖） |
| `persist/persist_checkpoint_test.go` | +50 行 | TestPersistCheckpoint_IdleSave + TestPersistCheckpoint_DirtyBytes |

---

## 四、风险

| 风险 | 缓解 |
|------|------|
| 后台 goroutine 泄漏 | Close() 中 `cancel()` + `wg.Wait()` |
| 空闲 save 在非繁忙时段引入不必要的 IO | 仅当 `setCount > 0` 且自上次 tick 无变化时才触发 |
| 脏页告警频率过高 | 仅在首次超过阈值时打日志（加 `atomic.Bool warned` guard） |

---

## 五、决策记录

### 决策 1：时间阈值 + 脏页量阈值全部实现，不拆分 Phase

**理由**：
- 对标 Lealone 三个维度，只实现一个维度仍有两个安全漏洞
- 脏页量阈值在本次只需告警 + 日志提示，不需要 chunk append 机制
- 两个机制共用一个后台 goroutine，总改动量仅 +60 行

### 决策 2：脏页量阈值采用「告警」而非「截断」

**理由**：
- 截断需要 ChunkManager 支持 append 模式（Lealone `lastChunk.size() + dirtyMemory < maxChunkSize`）
- 告警已覆盖脏页膨胀的监控需求——用户看到告警后调整 `ckptInterval`
- Chunk append 作为后续优化（对标 Lealone chunk append 逻辑）

### 决策 3：脏页统计采用方案 C — Lealone 方式（page allocator 计数器）

> **前置分析**：三种脏页统计方式对比

| 方案 | 统计方式 | 复杂度 | 精度 | 改动 |
|------|---------|:---:|:---:|------|
| A. 遍历统计 | `EnumeratePages` 中 `ChunkPos==0` 判断 | O(N) | 精确 | 0（当前） |
| B. 操作数估算 | `setCount - lastSavedCount` 估算 | O(1) | 近似 | persist +3行 |
| C. Lealone 方式（✅） | page allocator 每次 COW 分配 `dirtyBytes += pageSize` | **O(1)** | **精确** | BTree + persist |

**选 C 的理由**：
- Lealone 通过 `map.collectDirtyMemory()` 在 save 时直接读计数器，O(1) 判断——完全对标
- BTree 的 `OffheapBTreeStorage.AllocLeafPage/AllocInternalPage` 是 C 方案的天然插入点——每次 COW 分配新页面时累加
- 外层 PersistCheckpoint 通过 `DirtyBytes() uint64` 方法读取原子变量，无需遍历、无需估算
- 改动集中在 page allocator（+6 行）+ persist（调用 `DirtyBytes()` 替代遍历统计）

**实现方式**：
```
BTree 层:
  OffheapBTreeStorage.dirtyBytes atomic.Uint64    ← 新增字段
  AllocLeafPage()    → dirtyBytes.Add(pageSize)  ← 每次 COW 分配
  AllocInternalPage() → dirtyBytes.Add(pageSize)  ← 每次 COW 分配
  DirtyBytes() uint64  → dirtyBytes.Load()         ← 新增方法
  ResetDirtyBytes()    → dirtyBytes.Store(0)       ← save 成功后重置

Persist 层:
  改造前: totalBytes += len(item.PageData) (遍历中统计)
  改造后: totalBytes = tree.DirtyBytes() (O(1) 原子读)
  save 成功后: tree.ResetDirtyBytes()

与 Lealone 对标:
  Lealone: page.setDirty() → dirtyMemory += pageSize → collectDirtyMemory()
  NexKV:   AllocLeafPage() → dirtyBytes.Add(4KB)   → DirtyBytes()
```

> 注意：TryInPlace（原地 CAS）不分配新页面，不计入 `dirtyBytes`。这是因为页面在第一次 COW 分配时已计入——后续原地替换不改变页面状态。

---

> **文档版本**：v2.0（时间 + 内存阈值完整方案）  
> **下一步**：评审通过后启动实现（约 3h）
