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

### 机制③：脏页阈值 — save 内部告警 + 脏页跟踪（当前限制）

> **⚠️ 当前限制**：`EnumeratePages` 遍历全树，通过 `ChunkPos == 0` 判断脏页。**没有独立的脏页计数器**——`totalBytes` 是在 save **遍历过程中**计算的，不是 save 之前。这与 Lealone 的 `collectDirtyMemory()` 不同：Lealone 在 `save()` 调用时已经知道脏了多少字节（通过 BTree 页面写操作时标记），可以提前决定 append vs new chunk。

```
当前实现（遍历时统计）:
  saveInternal()
    → EnumeratePages(root)     ← 遍历全树, 每个页面检查 ChunkPos==0
      收集 items               ← 同时统计 totalBytes
    → 告警/写入

Lealone 做法（写操作时标记）:
  put()
    → setPageDirty(page)       ← 每次写入标记脏页
    → dirtyMemory += pageSize  ← 累加脏页字节数
  save()
    → dirtyMemory = collectDirtyMemory()  ← 读计数器
    → dirtyMemory < maxChunkSize → append
    → dirtyMemory >= maxChunkSize → new chunk
```

**本次实现**：在 `saveInternal` 的遍历中统计并告警（不阻塞）。独立的脏页计数器和基于计数的前置触发（对标 Lealone）需要 BTree 页面写操作时标记，是 Phase 2 优化项。

```go
// 对标 Lealone BTreeStorage.executeSave() 中的 dirtyMemory + maxChunkSize 判断

const (
    maxDirtyBytesPerSave = 256 * 1024 * 1024 // 256MB，对标 Lealone MAX_CHUNK_SIZE
)

type PersistCheckpoint struct {
    // ... 现有字段 ...
    dirtyWarned atomic.Bool  // 防止日志洪水（修复：仅首次超标告警）
}

func (p *PersistCheckpoint) saveInternal() error {
    // ... enumerate 后:

    // 遍历中统计脏页量（当前限制：ChunkPos==0 遍历时判断，非前置计数器）
    var totalBytes int64
    for _, item := range items {
        totalBytes += int64(len(item.PageData))
    }

    // 脏页总量超标 → 日志告警（不截断本次，通过调整 ckptInterval 控制）
    if totalBytes > maxDirtyBytesPerSave && p.dirtyWarned.CompareAndSwap(false, true) {
        log.Printf("persist checkpoint: dirty bytes %d > max %d, "+
            "consider reducing ckptInterval (current=%d)", totalBytes, maxDirtyBytesPerSave, p.ckptInterval)
    } else if totalBytes <= maxDirtyBytesPerSave {
        p.dirtyWarned.Store(false) // 恢复正常，重置告警标记
    }
    // ... 继续正常写入
}
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
| `persist/persist_checkpoint.go` | +60 行 | +idle goroutine + maxDirtyBytes 告警 + Close 更新 |
| `persist/persist_checkpoint_test.go` | +50 行 | TestPersistCheckpoint_IdleSave + TestPersistCheckpoint_DirtyBytesWarning |

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

---

> **文档版本**：v2.0（时间 + 内存阈值完整方案）  
> **下一步**：评审通过后启动实现（约 3h）
