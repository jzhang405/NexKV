# TaskScheduler "task execution failed" 调查报告与修复提案

**日期**: 2026-03-31
**分支**: perf/btree-set-benchmark2
**状态**: 调查完成，审核通过

## 1. 症状

`btree_perf_scheduler` 单线程（1T）benchmark：
- init 阶段 5000 条数据，大量 "初始化失败"
- 测试结果：17846 ops/s，但 success 率未知（可能极低）
- 错误链：`btree: cas failed, retry operation: btree set with leaf ref failed; task execution failed`

即使 **单线程、空树** 也高概率失败，说明不是并发竞争问题。

## 2. 根因分析

### 2.1 调用链路

```
BTree.Set()
  └─ SetWithRetryAndQueue()
       ├─ setWithLeafLock() × 3 次快速重试 → 全部 ErrRetry
       └─ SetWithTask()
            ├─ NewBTreeSetItem(btree, key, value, ...)
            ├─ scheduler.EnqueueWithShard(item, "btree-set")
            │    └─ item 入队到 ShardTask 的 MPSCExtQueue
            └─ item.Wait(ctx)  ←── 阻塞等待结果
                 └─ <-done channel
```

TaskScheduler runLoop 工作线程：
```
runLoop()
  └─ tryProcessBatch(task) 或 executeTask(task, item)
       └─ task.Execute(item)  ←── 调用注册时的 executeFunc
            └─ btree.go:445 的闭包:
                 ├─ runner.Run(ctx, nil)
                 │    └─ BaseTask.Run()
                 │         ├─ CAS TaskQueued→TaskExecuting
                 │         ├─ execute(ctx, trCtx)
                 │         │    └─ bt.setWithLeafLockAndRef(ctx, leafRef, key, value)
                 │         │         └─ 返回 ErrRetry（TryLock 失败/CAS 失败）
                 │         ├─ status = TaskFailed, err = ErrRetry
                 │         └─ close(done)  ←── 唤醒 Wait()
                 └─ task.Wait(ctx)
                      └─ <-done（立即返回）
                           └─ 读取 err = ErrRetry
                                └─ errors.Is(err, ErrRetry) → return TaskRetrying
```

### 2.2 两个关键 Bug

#### Bug A: Run() + Wait() 冗余调用 ✅ 已确认

**位置**: `btree.go:444-466`

executeFunc 先调用 `runner.Run()`，再调用 `task.Wait()`。
- `Run()` 已经同步执行了 `setWithLeafLockAndRef()`，close(done)
- `Wait()` 再次读取结果 → 返回同一个 error

这不是死锁，但 `Wait()` 在这里完全多余。**`Run()` 的返回值被丢弃**。

**正确做法**: 只调用 `Run()`，从 `BaseTask` 的 `GetError()` 方法直接读取结果。

**修复**：
```go
func(item any) concurrency.TaskStatus {
    runner, ok := item.(model.TaskRunner)
    if !ok {
        return concurrency.TaskPassed
    }

    runner.Run(context.Background(), nil)

    // 直接读取 error（不做第二次 Wait）
    if errGetter, ok := item.(interface{ GetError() error }); ok {
        err := errGetter.GetError()
        if err != nil {
            if errors.Is(err, ErrRetry) || errors.Is(err, ErrCircularReference) {
                return concurrency.TaskRetrying
            }
            return concurrency.TaskFailed
        }
    }
    return concurrency.TaskPassed
}
```

#### Bug B: ~~executeFunc 错误判断逻辑错误~~ 已验证无此 Bug

**位置**: `btree.go:454-458`

~~问题：`setWithLeafLockAndRef` 返回的 error 被 `BTreeSetWithLeafRefFailed()` 包装了一层：~~

**验证结果**：`errpkg.BTreeSetWithLeafRefFailed()` 使用 `Wrapf(err, ...)`，
`Wrapf` 返回 `*NexError`，实现了 `Unwrap()` 方法。`errors.Is()` 能正确 unwrap 匹配到 `ErrRetry`。

~~如果 `BTreeSetWithLeafRefFailed()` 用 `%v` 或创建新 error，则 `errors.Is()` 永远不匹配，**所有 ErrRetry 都变成 TaskFailed**。~~

**结论**：错误链传播正确，`errors.Is(err, ErrRetry)` 能匹配。Bug B 不存在。

#### Bug C: TaskRetrying 时 item 已出队（批量路径） ✅ 已确认

**位置**: `task_scheduler.go:466-469`

```go
task.DequeueN(len(items))        // ← 无条件出队所有 items
c.handleBatchResults(task, items, results)  // ← 之后才看结果
```

批量路径先 DequeueN 出队，再看结果。如果结果是 TaskRetrying：
- item 已经从队列移除
- `handleBatchResults` 只做 `IncAttempts()`，没有重新入队
- **item 丢失**

单个处理路径（line 403-417）对 TaskRetrying 不 Dequeue（正确），但批量路径有 bug。

**修复方案**（短期）：批量路径改为逐个执行+成功才 Dequeue：
```go
func (c *SchedulerCore) tryProcessBatch(task *ShardTask) bool {
    // ... peek 逻辑不变 ...

    // P0 修复：逐个执行，成功才出队
    processed := 0
    for i := 0; i < n; i++ {
        status := c.executeTask(task, items[i])
        c.stats.TotalTasksProcessed.Add(1)

        switch status {
        case TaskPassed, TaskFailed:
            var dequeued any
            task.Dequeue(&dequeued)
            processed++

        case TaskTimeout, TaskBusy, TaskRetrying:
            // 不出队，停止批量处理
            // 后续 items 保留在队列中
            goto done
        }
    }

done:
    return processed > 0
}
```

### 2.3 为什么单线程也失败

1. init 阶段（5000 条），叶子节点逐渐填满
2. 填满后每次 insert 触发 split
3. split 需要 TryLock 获取 parent lock → 与主线程自己的 lock 冲突
4. `setWithLeafLock` 3 次快速重试全部失败
5. 转到 TaskScheduler：`setWithLeafLockAndRef` 用缓存的 leafRef，但 leafRef 可能已失效（split 后 PageInfo 变化）→ 回退到 `setWithLeafLock` → 又失败
6. **TaskScheduler 只执行一次就判定失败，没有内部重试**

## 3. 修复方案

### 3.1 Fix A: executeFunc 简化（优先级 P0）

见 Bug A 修复方案。改动范围：`btree.go` 2 处 executeFunc（btree-set, btree-split）。

### 3.2 ~~Fix B: 确保错误链正确传播~~ 已验证无需修复

`errpkg.BTreeSetWithLeafRefFailed()` 使用 `Wrapf(err, ...)`，`Wrapf` 返回 `*NexError`，
实现了 `Unwrap()` 方法。`errors.Is(err, ErrRetry)` 能正确匹配。

### 3.3 Fix C: 批量路径逐个出队（优先级 P0）

见 Bug C 修复方案。改动范围：`task_scheduler.go` 的 `tryProcessBatch`。

### 3.4 Fix D: executeFunc 内部重试（优先级 P1）

当前 executeFunc 只执行一次 `setWithLeafLockAndRef`。
在高并发场景下，一次几乎不可能成功。

建议在 executeFunc 内部加 3 次快速重试：

```go
func(item any) concurrency.TaskStatus {
    runner, ok := item.(model.TaskRunner)
    if !ok {
        return concurrency.TaskPassed
    }

    const maxInternalRetries = 3
    for attempt := range maxInternalRetries {
        runner.Run(context.Background(), nil)

        if errGetter, ok := item.(interface{ GetError() error }); ok {
            err := errGetter.GetError()
            if err == nil {
                return concurrency.TaskPassed
            }
            if errors.Is(err, ErrRetry) || errors.Is(err, ErrCircularReference) {
                runtime.Gosched()
                continue
            }
            return concurrency.TaskFailed
        }
    }
    return concurrency.TaskRetrying
}
```

**前置条件**：`BaseTask.Run()` 有 CAS 保护（TaskQueued→TaskExecuting），第二次 Run() 会 CAS 失败。
需要先实现 `Reset()` 方法（从 TaskFailed/TaskCompleted 恢复到 TaskQueued）：

```go
func (b *BaseTask[Result]) Reset() bool {
    // 从终态恢复到可执行状态
    b.mu.Lock()
    defer b.mu.Unlock()
    if b.status.Load() == int32(TaskFailed) || b.status.Load() == int32(TaskCompleted) {
        b.status.Store(int32(TaskQueued))
        b.err = nil
        var zero Result
        b.result = zero
        b.done = make(chan struct{}) // 重建 done channel
        return true
    }
    return false
}
```

## 4. Quick Fix：全 batch=1 绕过 Bug C

**原理**：`tryProcessBatch` 在 `actualBatchSize < 2` 时 return false，走单个处理路径。
单个路径正确处理 TaskRetrying（不 Dequeue），绕过 Bug C 的批量出队丢失问题。

**已改动**：

| 文件 | 变更 | 当前值 |
|------|------|--------|
| `btree_set_item.go` | `PreferredBatchSize()` | 1 |
| `task_scheduler.go` | fallback default | 1 |

注意：这只是 Quick Fix，不解决 Bug A（Run+Wait 冗余）。
等 Fix A + Fix C 实现后可恢复 batch > 1 以提升吞吐。

## 5. 修复优先级

| 优先级 | 修复项 | 影响 | 难度 | 状态 |
|--------|--------|------|------|------|
| **P0** | Fix A: executeFunc 简化 | 消除冗余 Wait()，直接读 GetError() | 低 | 待修复 |
| ~~P0~~ | ~~Fix B: 错误链传播验证~~ | ~~确保 errors.Is(ErrRetry) 正确匹配~~ | ~~低~~ | **已验证无此 Bug** |
| **P0** | Fix C: 批量路径逐个出队 | 修复 item 丢失 bug | 中 | 已通过 batch=1 绕过 |
| **P1** | Fix D: executeFunc 内部重试 | 大幅提升成功率 | 中 | 待修复（需先实现 Reset） |
| **P2** | BaseTask.Reset() 方法 | 支持 executeFunc 内部重试 | 低 | 待修复 |

## 6. 审核结论

| 审核项 | 核实结果 | 备注 |
|--------|---------|------|
| Bug A: Run+Wait 冗余 | ✅ 确认存在 | `GetError()` 已存在，可直接替代 `Wait()` |
| Bug B: errors.Is 断链 | ❌ 无此 Bug | `Wrapf` → `*NexError.Unwrap()` 保留错误链 |
| Bug C: 批量先出队 | ✅ 确认存在 | 已通过 batch=1 Quick Fix 绕过 |
| Fix C: 逐个执行+成功才 Dequeue | ✅ 方案可行 | break on retry 合理（FIFO 顺序依赖） |
| Fix D: 内部重试需 Reset() | ✅ 正确 | Reset 需从 TaskFailed/TaskCompleted 恢复，不只 TaskExecuting |
| Quick Fix: batch=1 | ✅ 有效 | 绕过 Bug C，不解决 Bug A |

## 7. 影响范围

- `internal/infrastructure/storage/btree/btree.go` — executeFunc 注册（2 处：btree-set, btree-split）
- `internal/infrastructure/concurrency/task_scheduler.go` — tryProcessBatch 出队逻辑
- `internal/domain/model/task.go` — 需要添加 Reset() 方法
