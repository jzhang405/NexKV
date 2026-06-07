# PR 文档 — Fix：PersistCheckpoint 增加时间阈值保底 Save

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

**只有一个触发条件（数量阈值），缺少时间阈值和脏页量阈值。对标 Lealone 的 `LogSyncService`（loopInterval=3000ms + checkpoint 周期 + 脏页量判断），当前设计存在三个安全漏洞：**

| 触发条件 | Lealone | NexKV 当前 | 风险 |
|---------|---------|:--------:|------|
| 数量阈值 | — | `ckptInterval` | ✅ 已有 |
| 时间阈值 | `LogSyncService.loopInterval = 3000ms` | ❌ 无 | 数据永不落盘 |
| 内存/脏页阈值 | `map.collectDirtyMemory()` | ❌ 无 | 脏页积压 |

### 两个漏洞场景

```
场景 A（空闲丢失）:
  Set() 9,999 条，停止。ckptInterval=10000 → 永不触发 save
  进程崩溃 → 9,999 条全部丢失（最大窗口=∞）

场景 B（写入积压）:
  Set() 持续 1M QPS。save() ~5-50ms/次
  上一个 save 未完成，下一个 10K 已到达 → save 排队积压
```

### 根本原因

对标 Lealone 源码分析：

```java
// LogSyncService.java:106-142 — 后台线程
while (running) {
    // ① 有事务 RedoLog → 立即 sync
    if (redoLogRecordCount.get() > 0)
        redoLog.save();           // fwrite + fsync

    // ② 定时触发 checkpoint
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
```

Lealone 有三个维度保障：**事务数量 → sync + 时间 → checkpoint + 空闲 → await**。
NexKV 只有 **数量 → sync**。

---

## 二、修复方案

### 核心思路

在 `PersistCheckpoint` 中增加**后台保底定时器 goroutine**，对标 Lealone 的 `LogSyncService.run()` 循环。

```
触发条件（三个维度）:

  ① 数量阈值（已有）: setCount % ckptInterval == 0 → asyncSave
  ② 时间阈值（新增）: 超过 maxIdleDuration 无 Set → 强制 save
  ③ 脏页阈值（预留）: dirtyPages > maxDirtyPages → 触发 save
```

### 新增字段

```go
type PersistCheckpoint struct {
    // ... 现有字段 ...

    // 后台保底定时器（对标 Lealone LogSyncService）
    maxIdleDuration time.Duration  // 超过此间隔无写入 → 强制 save (默认 3s)
    idleTicker      *time.Ticker
    wg              sync.WaitGroup
    ctx             context.Context
    cancel          context.CancelFunc
}
```

### 后台协程

```go
func (p *PersistCheckpoint) runIdleCheck() {
    defer p.wg.Done()
    ticker := time.NewTicker(p.maxIdleDuration)
    defer ticker.Stop()

    lastCount := p.setCount.Load()
    for {
        select {
        case <-p.ctx.Done():
            return
        case <-ticker.C:
            cur := p.setCount.Load()
            if cur == lastCount && cur > 0 && p.saving.CompareAndSwap(false, true) {
                // 写入停止 → 强制保底 save
                go func() {
                    defer p.saving.Store(false)
                    p.asyncSave()
                }()
            }
            lastCount = cur
        }
    }
}
```

### 效果

```
场景 A（修复前）: 9,999 条 → 停止 → 永不 save → 崩溃全丢
场景 A（修复后）: 9,999 条 → 停止 → 3s 后强制 save → 崩溃最多丢 3s

场景 B（修复前）: 持续写入 → save 排队积压
场景 B（修复后）: 持续写入 → 以 ckptInterval 间隔正常 save（不变）
```

### 默认参数

| 参数 | 默认值 | 对标 Lealone |
|------|--------|------------|
| `ckptInterval` | 10000 | — |
| `maxIdleDuration` | **3s** | `LogSyncService.loopInterval = 3000ms` |
| `maxDirtyPages` | 预留（Phase 2） | `map.collectDirtyMemory()` |

---

## 三、改动范围

| 文件 | 改动量 | 说明 |
|------|:------:|------|
| `persist/persist_checkpoint.go` | +40 行 | +3 字段 + runIdleCheck() + Close 更新 |
| `persist/persist_checkpoint_test.go` | +30 行 | 新增 TestPersistCheckpoint_IdleSave |

---

## 四、风险

| 风险 | 缓解 |
|------|------|
| 后台 goroutine 泄漏 | Close() 中 `cancel()` + `wg.Wait()` |
| 空闲 save 在非繁忙时段引入不必要的 IO | 仅当 `setCount > 0` 且有变化时才触发 |
| 定时器精度 | 3s ticker，Go 标准库精度足够 |

---

## 五、决策记录

### 决策 1：使用时间阈值（3s），不使用脏页量阈值（Phase 2）

**理由**：
- 脏页量阈值需要 `collectDirtyMemory()` 或等效机制——当前 BTree 未实现，需额外开发
- 3s 时间阈值已覆盖最常见的「写入停止 → 数据永不落盘」漏洞
- 对标 Lealone `loopInterval=3000ms`，生产验证充分

---

> **文档版本**：v1.0  
> **下一步**：评审通过后启动实现（约 2h）
