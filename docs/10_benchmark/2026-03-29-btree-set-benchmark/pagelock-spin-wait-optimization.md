# Phase-PageLock: TryLock Spin Wait 优化方案

**日期**: 2026-03-30
**分支**: `perf/btree-set-benchmark2`
**优先级**: **最高**（当前性能瓶颈根因）
**关联**: `lock-contention-analysis.md` §2.1 / §2.2
**CPU Profile**: `cpu-8t-lock-analysis.prof`

---

## 1. 问题概述

### 1.1 当前性能数据

| 指标 | 数值 | 说明 |
|------|------|------|
| 成功率 | **2.98%**（11,913 / 400,000） | 8 线程 Set 基准测试 |
| 吞吐量 | **13,079 ops/s** | 远低于单线程 16,347 ops/s |
| 扩展比 | **0.80x**（8 线程 vs 单线程） | 负扩展！ |
| 重试率 | **97%** | 几乎所有操作都在重试 |

### 1.2 CPU 分布（pprof 实测）

| 函数 | flat% | cum% | 归因 |
|------|-------|------|------|
| `runtime.futex` | 14.12% | 14.12% | 锁等待系统调用 |
| `runtime.procyieldAsm` | 5.65% | 5.65% | 自旋等待 |
| `runtime.lock2` | 1.98% | 6.21% | Go runtime 内部锁 |
| `runtime.schedule` | — | 29.94% | 调度器开销 |
| `runtime.park_m` | 0.28% | 28.25% | goroutine 挂起 |
| `runtime.findRunnable` | 0.85% | 11.58% | 调度器寻找可运行 G |
| `runtime.notesleep` | — | 8.76% | sync.Cond.Wait 底层 |
| `runtime.notewakeup` | — | 5.65% | sync.Cond.Broadcast 底层 |
| **锁+调度合计** | — | **~50-55%** | 超过一半 CPU 浪费 |

**对比**：实际业务逻辑（`linearSearchLeaf` 1.69%、`cmpbody` 4.52%、`InsertToOffHeap` 13.28%）仅占 ~20% CPU。

### 1.3 根因

**TryLock 失败 → 立即 ErrRetry → runtime.Gosched → 调度风暴**

8 个线程竞争同一叶子锁时：
- 最多 1 个成功，7 个立即返回 `ErrRetry`
- 每次 `ErrRetry` 调用 `runtime.Gosched()` 让出 CPU
- 触发 `schedule` → `park_m` → `findRunnable` 完整调度链路
- 3 次 fast retry 后进入单线程 `TaskScheduler`，8 线程串行化

---

## 2. 当前代码路径分析

### 2.1 PageLock 实现（`page_lock.go`）

```go
// page_lock.go:44-49
func (l *PageLock) TryLock() bool {
    newState := int64(1)<<ownerIDShift | 1
    return l.state.CompareAndSwap(int64(unlockedState), newState)
}
```

**特征**：纯 CAS 非阻塞，失败立即返回 `false`。无任何等待/重试。

### 2.2 PageLock 结构体中的 cond/mu/Lock/LockWithTimeout 是未使用代码

**核实结果**：PageLock 中实现了 `sync.Cond` + `sync.Mutex` + 阻塞锁方法（`page_lock.go:25-27, 52-99, 143-155`），但**在整个 btree 包中从未被调用**。

| 方法 | 是否被调用 | 调用者 | 说明 |
|------|-----------|--------|------|
| `TryLock()` | 100% 热路径 | `leaf_lock_set.go`（6+ 处）、`btree.go`（2 处） | 唯一被调用的锁获取方法 |
| `Lock()` | **从未被调用** | 无 | 方法已实现（`page_lock.go:52-54`）但无调用者 |
| `LockWithTimeout()` | **从未被调用** | 无 | 方法已实现（`page_lock.go:57-59`）但无调用者 |
| `lockWithTimeout()` | **从未被调用** | 无 | 内部实现（`page_lock.go:62-99`）包含 CAS 循环 + cond.Wait 阻塞逻辑 |
| `cond.Wait()` | **从未被调用** | 无 | 仅在 `wait()` 中调用，而 `wait()` 仅被 `lockWithTimeout()` 调用 |
| `cond.Broadcast()` | 每次 `Unlock()` 调用 | `page_lock.go:124` | 但**无等待者**，`Broadcast` 是空操作 |
| `wait()` | **从未被调用** | 无 | 方法已实现（`page_lock.go:144-148`）但无调用者 |
| `broadcast()` | 每次 `Unlock()` 调用 | `page_lock.go:150-154` | 获取 mu + 调用 cond.Broadcast，但无等待者 |

**移除影响评估**：
- 移除 `mu`/`cond`/`Lock()`/`LockWithTimeout()` 不会影响任何现有功能
- `Unlock()` 中移除 `broadcast()` 调用是安全的（无等待者）
- 如未来需要阻塞锁功能，可重新引入（此时建议使用 `sync.Mutex` 替代自定义实现）

### 2.3 锁获取点（`leaf_lock_set.go:30-38`）

```go
// setWithLeafLock — 每次写入操作的入口
pageLock := leafRef.GetLock()
if !pageLock.TryLock() {
    return ErrRetry  // ← 立即失败，无等待
}
defer pageLock.Unlock()
```

### 2.4 重试循环（`btree_ops.go:175-218`）

```go
// SetWithRetryAndQueue — 重试策略
const maxFastRetries = 3

for attempt := range maxFastRetries {
    err := b.setWithLeafLock(ctx, key, value)
    switch err {
    case nil:
        return nil
    case ErrRetry:
        if attempt < maxFastRetries-1 {
            runtime.Gosched()  // ← 让出 CPU，触发调度风暴
        }
    }
}

// 3 次失败后降级到单线程 TaskScheduler
if scheduler != nil {
    return b.SetWithTask(ctx, scheduler, key, value)  // ← 串行化
}
```

### 2.5 完整调用链

```
Set(key, value)
  └── SetWithRetryAndQueue
       ├── attempt 0: setWithLeafLock
       │    └── findLeafPageRef → TryLock() ✗ → ErrRetry
       │    └── runtime.Gosched() → schedule → park_m → findRunnable
       ├── attempt 1: setWithLeafLock
       │    └── findLeafPageRef → TryLock() ✗ → ErrRetry
       │    └── runtime.Gosched() → schedule → park_m → findRunnable
       ├── attempt 2: setWithLeafLock
       │    └── findLeafPageRef → TryLock() ✗ → ErrRetry
       └── SetWithTask (单线程 TaskScheduler)
            └── EnqueueWithShard → channel send
            └── item.Wait(ctx) → selectgo → sellock → notesleep
            └── [串行执行] setWithLeafLockAndRef
```

---

## 3. 性能损失量化

### 3.1 单次操作 CPU 开销分解

| 阶段 | 成功路径 | 失败路径 | 倍率 |
|------|----------|----------|------|
| `findLeafPageRef` | 1 次 | 3 次 | 3x |
| `searchPathWithRefs` | 1 次 | 3 次 | 3x |
| `TryLock` | 1 次 CAS | 3 次 CAS | 3x |
| `runtime.Gosched` | 0 次 | 2 次 | ∞ |
| `NewPageInfo` | 1 次 | 0 次（失败不创建） | — |
| `errors.Wrapf` | 0 次 | ~2 次 | ∞ |

### 3.2 重试成本

97% 的操作失败意味着平均重试 ~33 次/操作：

| 开销源 | 单次成本 | 重试 33 次总成本 |
|--------|----------|------------------|
| `searchPathWithRefs` | 18% CPU | **占满** |
| `linearSearchLeaf` | 9% CPU | **占满** |
| `runtime.Gosched` → 调度链 | ~30% CPU | **占满** |
| `errors.Wrapf` | 8% CPU | **占满** |

**关键洞察**：大部分 CPU 时间消耗在**重试的搜索路径 + 调度开销**上，而不是锁等待本身。即使锁持有时间很短，`TryLock` 立即失败的策略导致了大量无效工作。

### 3.3 TaskScheduler 串行化

3 次 fast retry 后进入 `SetWithTask`：
- `TaskScheduler` 是单线程消费队列
- 8 个线程的操作被串行化
- 每次 `item.Wait(ctx)` 涉及 channel 收发 + `selectgo` + `sellock`
- **这是性能从 2 线程开始下降的直接原因**

---

## 4. 优化方案

### 4.1 方案 A：TryLock + Spin Wait（推荐）

**核心思路**：`TryLock` 失败后不立即返回 `ErrRetry`，而是短暂自旋等待锁释放后再尝试。

#### 设计

```go
// page_lock.go — 新增 TryLockWithSpin
const (
    defaultSpinCount = 64  // 自旋次数（约 200-500ns on modern CPU）
)

func (l *PageLock) TryLockWithSpin() bool {
    // 快速路径：直接 CAS
    newState := int64(1)<<ownerIDShift | 1
    if l.state.CompareAndSwap(int64(unlockedState), newState) {
        return true
    }

    // 自旋路径：PAUSE 循环等待锁释放
    for i := 0; i < defaultSpinCount; i++ {
        runtime_procyield(8)  // PAUSE 指令，8 次 yield
        if l.state.Load() == int64(unlockedState) {
            if l.state.CompareAndSwap(int64(unlockedState), newState) {
                return true
            }
        }
    }
    return false
}
```

#### 调用点修改

```go
// leaf_lock_set.go:36 — 替换 TryLock
// Before:
if !pageLock.TryLock() {
    return ErrRetry
}

// After:
if !pageLock.TryLockWithSpin() {
    return ErrRetry
}
```

#### 性能预期

| 指标 | 当前 | 预期 | 提升幅度 |
|------|------|------|----------|
| TryLock 成功率 | ~12.5%（1/8） | **50-70%** | 4-6x |
| runtime.Gosched 调用 | ~28% CPU | **< 5%** | -80% |
| schedule 开销 | ~30% CPU | **< 5%** | -83% |
| TaskScheduler 降级率 | ~97% | **< 30%** | -70% |
| 有效吞吐量 | 13K ops/s | **50-100K ops/s** | 4-8x |

**计算依据**：
- 锁持有时间估算：`setWithLeafLock` 临界区约 1-5μs（InsertToOffHeap + CAS）
- Spin 64 次 × 8 PAUSE ≈ 200-500ns（远小于锁持有时间）
- 竞争 8 线程时，Spin 等待的线程有 40-60% 概率在锁释放后立即获取
- 避免了 `Gosched` → `schedule` → `park_m` 的 ~1-10μs 调度开销

### 4.2 方案 B：增加 fast retry 次数（配合方案 A）

```go
// btree_ops.go
const maxFastRetries = 10  // 从 3 增加到 10
```

**优势**：零代码改动，减少 TaskScheduler 降级率。
**问题**：每次重试仍然 `runtime.Gosched()`，不解决调度开销。
**预期提升**：+10-20%（缓解但未根治）。
**建议**：作为方案 A 的补充措施。

### 4.3 方案 C：移除未使用代码（同步实施）

移除 §2.2 确认的未使用代码。这些方法已在 `page_lock.go` 中实现，但在整个 btree 包中从未被调用。

| 移除项 | 代码位置 | 说明 |
|--------|--------|------|
| `mu sync.Mutex` 字段 | `page_lock.go:25` | 仅保护 cond，cond 移除后无用 |
| `cond *sync.Cond` 字段 | `page_lock.go:26` | 从未有等待者 |
| `broadcast()` 方法 | `page_lock.go:150-154` | 无等待者可唤醒 |
| `wait()` 方法 | `page_lock.go:144-148` | 从未被调用 |
| `Lock()` 方法 | `page_lock.go:52-54` | 从未被调用 |
| `LockWithTimeout()` 方法 | `page_lock.go:57-59` | 从未被调用 |
| `lockWithTimeout()` 方法 | `page_lock.go:62-99` | 内部实现（含 CAS 循环 + cond.Wait） |

**影响评估**：
- 移除 `mu`/`cond`/阻塞锁方法不会影响任何现有功能
- `Unlock()` 中移除 `broadcast()` 调用是安全的（无等待者）
- 如未来需要阻塞锁，建议使用 `sync.Mutex` 替代自定义实现
- `NewPageLock()` 中移除 cond 构造

**简化后 PageLock**（3 字段 → 1 字段）：

```go
type PageLock struct {
    state atomic.Int64  // 仅保留此字段
}
```

---

## 5. 推荐实施策略

### 5.1 Phase-PageLock-1（方案 A+B+C）

| 改动 | 文件 | 改动量 |
|------|------|--------|
| 新增 `TryLockWithSpin` | `page_lock.go` | ~25 行 |
| 替换 `TryLock` → `TryLockWithSpin` | `leaf_lock_set.go`（6+ 处） | ~6 行 |
| 替换 `TryLock` → `TryLockWithSpin` | `btree.go`（2 处） | ~2 行 |
| `maxFastRetries` 3 → 10 | `btree_ops.go:185` | 1 行 |
| 移除 cond/mu/Lock/LockWithTimeout/wait/broadcast | `page_lock.go` | -60 行 |
| 简化 `NewPageLock` | `page_lock.go` | 3 行 |
| 简化 `Unlock`（移除 broadcast 调用） | `page_lock.go` | ~5 行 |

**总工作量**：0.5-1 天
**预期效果**：成功率从 2.98% 提升到 30-50%，吞吐量 4-8x

---

## 6. 详细设计：TryLockWithSpin

### 6.1 runtime_procyield 说明

**使用 `go:linkname` 引用 `runtime.procyield`**：

```go
import _ "unsafe"

//go:linkname runtime_procyield runtime.procyield
func runtime_procyield(cycles uint32)
```

**风险评估**：
- `runtime.procyield` 不是公开 API，Go 团队在源码中标注了使用它的外部包为 "hall of shame"
- 但多个知名项目（WireGuard、Slack Nebula 等）使用此方式，短期内不会被移除
- 如果未来 Go 版本移除此函数，fallback 为纯 `atomic.Load` 循环（无 PAUSE，性能略降）

**Go 源码中的实现**（`runtime/asm_amd64.s`）：

```assembly
TEXT runtime·procyieldAsm(SB),NOSPLIT,$0-0
    MOVL    cycles+0(FP), AX
    TESTL   AX, AX
    JZ      done
again:
    PAUSE         // CPU hint: 正在自旋等待
    SUBL    $1, AX
    JNZ     again
done:
    RET
```

**参数含义**：`cycles` 是 **PAUSE 指令的执行次数**（不是 CPU cycles）。每次 PAUSE 约 10-20ns。

**Go sync.Mutex 的自旋策略**（参考）：
- `active_spin = 4`（最大自旋尝试次数）
- `active_spin_cnt = 30`（每次自旋的 PAUSE 循环数）
- 即 `procyield(30)` × 4 次，总等待约 1-2μs

### 6.2 自旋次数选择

| Spin 次数 | PAUSE × 8 等待时间（约） | 适用场景 |
|-----------|------------------------|---------|
| 16 | ~50-100ns | 锁持有 < 1μs |
| **64** | **~200-500ns** | **锁持有 1-5μs（推荐起点）** |
| 128 | ~400-1000ns | 锁持有 5-10μs |
| 256 | ~800-2000ns | 锁持有 > 10μs（分裂场景） |

**推荐初始值 64**：
- 正常 Set（无分裂）临界区约 1-3μs（InsertToOffHeap + CAS）
- Spin 64 次 ≈ 200-500ns，约为锁持有时间的 1/5
- 足以在锁释放后 1-2 轮 Spin 内捕获

### 6.3 完整实现代码

```go
// page_lock.go

package btree

import (
    "runtime"
    "sync/atomic"
    _ "unsafe"

    errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

//go:linkname runtime_procyield runtime.procyield
func runtime_procyield(cycles uint32)

const (
    unlockedState    = 0
    maxRecurseCount  = 1000
    ownerIDShift     = 32
    defaultSpinCount = 64 // Spin 等待次数（约 200-500ns）
)

// PageLock 轻量级叶子锁
// 状态编码: (owner_id << 32) | lock_count
type PageLock struct {
    state atomic.Int64
}

func NewPageLock() *PageLock {
    return &PageLock{}
}

// TryLock 非阻塞加锁
func (l *PageLock) TryLock() bool {
    newState := int64(1)<<ownerIDShift | 1
    return l.state.CompareAndSwap(int64(unlockedState), newState)
}

// TryLockWithSpin 非阻塞加锁，失败后短暂自旋重试
// 适用于高并发场景，减少 ErrRetry 导致的调度开销
func (l *PageLock) TryLockWithSpin() bool {
    newState := int64(1)<<ownerIDShift | 1

    // 快速路径：直接 CAS
    if l.state.CompareAndSwap(int64(unlockedState), newState) {
        return true
    }

    // 单核环境跳过 Spin
    if runtime.NumCPU() == 1 {
        return false
    }

    // 自旋路径
    for i := 0; i < defaultSpinCount; i++ {
        runtime_procyield(8)

        // 先检查状态再 CAS，减少无效 CAS 开销
        if l.state.Load() != int64(unlockedState) {
            continue
        }
        if l.state.CompareAndSwap(int64(unlockedState), newState) {
            return true
        }
    }

    return false
}

// Unlock 解锁
func (l *PageLock) Unlock() error {
    oldState := l.state.Load()
    if oldState == int64(unlockedState) {
        return errpkg.BTreeCannotUnlockUnlocked()
    }

    lockCount := oldState & ((1 << ownerIDShift) - 1)
    if lockCount > 1 {
        newState := oldState - 1
        if !l.state.CompareAndSwap(oldState, newState) {
            return errpkg.BTreeUnlockStateChanged()
        }
        return nil
    }

    // 完全解锁（无 broadcast，无等待者）
    if !l.state.CompareAndSwap(oldState, int64(unlockedState)) {
        return errpkg.BTreeUnlockStateChanged()
    }

    return nil
}

// IsLocked 检查是否已锁定
func (l *PageLock) IsLocked() bool {
    return l.state.Load() != int64(unlockedState)
}
```

---

## 7. 验证计划

### 7.1 基准测试

```bash
# 对比测试（Spin 开启 vs 关闭）
GOGC=500 go test -bench=BenchmarkBTreeSet -benchtime=5s -count=3 \
    ./internal/infrastructure/storage/btree/

# 采集新 profile
GOGC=500 go test -bench=BenchmarkBTreeSet -cpuprofile=cpu-after-spin.prof \
    ./internal/infrastructure/storage/btree/
```

### 7.2 关键指标

| 指标 | 当前 | 目标 | 验证方法 |
|------|------|------|----------|
| 成功率 | 2.98% | > 30% | 统计 SetWithRetryAndQueue 返回值 |
| 8 线程吞吐量 | 13K ops/s | > 50K ops/s | Benchmark 输出 |
| P99 延迟 | 未知（估计 > 10ms） | < 1ms | benchstat 输出 |
| P99.9 延迟 | 未知（估计 > 50ms） | < 5ms | benchstat 输出 |
| CPU 有效利用率 | ~20%（业务逻辑） | > 60% | pprof 业务逻辑占比 |
| `runtime.futex` | 14.12% | < 5% | pprof -top |
| `runtime.schedule` | 29.94% | < 10% | pprof -top |
| TaskScheduler 降级率 | ~97% | < 30% | 统计 SetWithTask 调用次数 |

### 7.3 正确性测试

- `TestSetWithLeafLock_Concurrent` — 并发写入正确性
- `TestSetWithLeafLock_ExtremeConcurrency` — 极端并发正确性
- `TestDebug6000KeysNoLoss` — 数据完整性

### 7.4 A/B 测试

> **新增结构**：当前代码中不存在 `BTreeConfig`，以下为计划新增的配置结构。

添加配置开关便于回滚和灰度。建议在 `BTree` 初始化参数中添加：

```go
// 新增字段，添加到 BTree 结构体或 OpenBTree 参数中
type SpinLockConfig struct {
    // Enabled 启用 TryLockWithSpin 替代 TryLock
    // 可在运行时通过配置切换，便于 A/B 测试和回滚
    Enabled bool

    // SpinCount 自旋次数（默认 64）
    SpinCount int
}
```

---

## 8. 风险评估

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| `go:linkname` 兼容性 | 低 | 中 | Fallback 为纯 atomic.Load 循环 |
| Spin 过度消耗 CPU | 低 | 中 | `defaultSpinCount` 可调，单核跳过 |
| 单核性能退化 | 低 | 低 | `runtime.NumCPU() == 1` 跳过 Spin |
| 活锁风险 | 极低 | 高 | Spin 是有限次数，最终仍返回 ErrRetry |
| 分裂场景 Spin 不足 | 中 | 低 | 分裂时锁持有 10-50μs，Spin 64 次不足覆盖；但不会更差（当前也是立即失败） |
| 移除 cond 影响未知调用者 | 低 | 低 | grep 确认无调用者，`EnableSpinLock` 开关可回滚 |

---

## 9. 与 Lealone 的对比

| 维度 | NexKV (Spin Wait) | Lealone (单写线程) |
|------|-------------------|-------------------|
| 复杂度 | 低（局部优化） | 高（架构重构） |
| 预期吞吐量 | 50-100K ops/s | 200K+ ops/s |
| 扩展性 | 有限（锁竞争仍存在） | 优秀（无锁竞争） |
| 实施成本 | 0.5-1 天 | 2-4 周 |
| 风险 | 低（可回滚） | 高（架构变更） |

**结论**：Phase-PageLock 是短期有效方案，长期应考虑 Lealone 式单写线程架构。

---

## 10. 后续影响

Phase-PageLock 成功后的级联效应：

```
Phase-PageLock (Spin Wait + 移除 cond)
  └── 成功率 2.98% → 30-50%
       ├── 吞吐量 13K → 50-100K ops/s
       ├── PageLock 结构简化（1 字段 vs 3 字段）
       ├── 分裂频率上升（更多操作到达临界区）
       │    └── Phase-FreeList-1 成为必要
       └── 真实瓶颈转移
            ├── 从 "调度开销" → "实际 BTree 操作"
            ├── pprof 分布将显著变化
            └── 需要新的 profile 确定下一阶段优化目标
```
