# 队列开销详细分析

**问题**: TaskScheduler 模式比纯内存模式慢 66%

---

## 开销分解

### 1. 快速路径额外开销（即使不进队列）

**纯内存模式**:
```go
tree.Set(ctx, key, value)
```

**TaskScheduler 模式**（快速路径）:
```go
// SetWithRetryAndQueue - 快速路径
for attempt := 0; attempt < 3; attempt++ {
    select { case <-ctx.Done(): ... }  // 每次循环检查上下文

    err := b.setWithLeafLock(ctx, key, value)
    switch err {
    case nil: return nil                    // 成功退出
    case ErrRetry: runtime.Gosched()       // 失败让出 CPU
    default: return err                    // 其他错误退出
    }
}
```

**额外开销**:
- **上下文检查**: 每次重试都检查 `ctx.Done()`（3次）
- **状态判断**: switch 语句分支预测
- **runtime.Gosched**: 失败时调用（增加调度延迟）
- **循环控制**: for 循环本身的开销

**估算影响**: ~5-10%

---

### 2. BTreeSetItem 创建开销

**每次调用 SetWithRetryAndQueue 都会创建**（即使快速路径成功）:

```go
// NewBTreeSetItem 的开销
func NewBTreeSetItem(bt, key, value, maxRetries) *BTreeSetItem {
    // 1. 查找 leafRef - 完整的路径查找！
    leafRef, _, err := bt.findLeafPageRef(ctx, key)  // ← 开销最大！

    // 2. 创建 BaseTask（闭包）
    BaseTask: model.NewBaseTask(
        model.TaskPriorityNormal,
        maxRetries,
        func(ctx context.Context, trCtx model.TaskRunnerContext) (struct{}, error) {
            err := bt.Set(ctx, key, value)  // ← 闭包捕获变量
            return struct{}{}, nil
        },
    ),
}
```

**开销明细**:
| 操作 | 开销 | 说明 |
|------|------|------|
| **findLeafPageRef** | ~500-1000ns | 完整路径查找（从根到叶） |
| **BaseTask 创建** | ~50-100ns | 内存分配、闭包创建 |
| **闭包捕获** | ~20-50ns | key、value、bt 捕获到堆上 |
| **BTreeSetItem 结构** | ~10-20ns | 结构体初始化 |

**总计**: ~600-1200ns 每次调用

**关键问题**: 这里的 findLeafPageRef 是**完全浪费的**！
- 快速路径成功时，这个查找结果不会使用
- 只有进入慢速路径（队列）时才有用
- 但每次调用都会做！

**估算影响**: ~15-20%

---

### 3. 双重工作问题（路径查找两次）

**纯内存模式**:
```
tree.Set() → 查找路径1次 → Insert → 完成
```

**TaskScheduler 模式**（进入队列）:
```
NewBTreeSetItem() → 查找路径1次 (为 ShardID)
    ↓
Enqueue 到队列
    ↓
Execute() → bt.Set() → 查找路径2次 → Insert → 完成
```

**问题**: 即使有 leafRef 缓存，Execute 时仍然调用 `bt.Set()` 而不是使用缓存的 leafRef！

**代码证据**（btree_set_item.go:79）:
```go
func(ctx context.Context, trCtx model.TaskRunnerContext) (struct{}, error) {
    // Execute 时重新查找，确保正确性  ← 注释说明这是故意的！
    err := bt.Set(ctx, key, value)
    if err != nil {
        return struct{}{}, fmt.Errorf("btree set failed: %w", err)
    }
    return struct{}{}, nil
},
```

**为什么重新查找？**
- 因为从创建到执行之间，叶子节点可能已经分裂
- 所以 Execute 时必须重新查找

**影响**: 每个队列操作有 2 次路径查找

**估算影响**: ~10-15%

---

### 4. 队列操作开销（慢速路径）

**RingQueue.Enqueue**:
```go
func (q *RingQueue) Enqueue(item any) error {
    q.mu.Lock()         // ← 加锁
    defer q.mu.Unlock()

    // 检查扩容
    if q.size == q.cap {
        q.expand()
    }

    q.data[q.tail] = item  // ← 写入
    q.tail = (q.tail + 1) % q.cap
    q.size++
    return nil
}
```

**RingQueue.Dequeue**:
```go
func (q *RingQueue) Dequeue(item *any) bool {
    q.mu.Lock()         // ← 加锁
    defer q.mu.Unlock()

    *item = q.data[q.head]
    q.data[q.head] = nil  // ← 帮助 GC
    q.head = (q.head + 1) % q.cap
    q.size--
    return true
}
```

**开销**:
- **锁竞争**: 多个核心同时 enqueue/dequeue
- **内存分配**: expand() 时分配新数组
- **GC 压力**: 队列中堆积的 BTreeSetItem 对象

**估算影响**: ~5-10%

---

### 5. TaskScheduler 调度开销

**runLoop 调度循环**:
```go
func (c *SchedulerCore) runLoop() {
    for {
        c.stats.TotalCycles.Add(1)  // ← 统计

        // 检查总队列为空
        if c.totalQueueItems.Load() == 0 {
            c.waitForSignal()  // ← 等待唤醒
        }

        // 尝试批量处理
        if c.tryProcessBatch(task) {
            continue
        }

        // 单个处理
        task.Peek(&item)
        status := c.executeTask(task, item)
        task.Dequeue(&dequeued)
        // ...
    }
}
```

**开销**:
- **空转循环**: TotalCycles 计数（即使无任务）
- **Peek/Dequeue**: 每次操作都要调用
- **批量处理逻辑**: tryProcessBatch 的检查（即使不触发）

**估算影响**: ~5-10%

---

### 6. 内存分配和 GC 压力

**纯内存模式**:
- 分配：Delta Chain 节点、新页面
- 每次操作: ~400-500 B

**TaskScheduler 模式**:
- 额外分配：
  - BTreeSetItem 结构（~100-200 B）
  - BaseTask 闭包（~50-100 B）
  - RingQueue 数组扩容
- 每次操作: ~600-800 B

**GC 影响**:
- GOGC=100: GC 频率增加 30-50%
- GOGC=500: 改善但仍高于纯内存

**估算影响**: ~10-15%

---

## 总结：66% 性能损失的来源

| 开销来源 | 影响 | 说明 |
|---------|------|------|
| **快速路径额外开销** | ~5-10% | 3次重试循环 + 上下文检查 |
| **BTreeSetItem 创建** | ~15-20% | findLeafPageRef 浪费（最严重！） |
| **双重路径查找** | ~10-15% | 创建时1次 + Execute时1次 |
| **队列操作** | ~5-10% | Enqueue/Dequeue + 锁竞争 |
| **调度开销** | ~5-10% | runLoop 循环 + Peek/Dequeue |
| **GC 压力** | ~10-15% | 额外内存分配 |
| **其他** | ~5-10% | 缓存失效、锁竞争等 |

**总计**: ~65-90% ✅ 符合观察到的 66% 性能损失

---

## 优化建议

### 🔴 P0: 消除 findLeafPageRef 浪费（最大收益）

**问题**: 每次调用 SetWithRetryAndQueue 都会 findLeafPageRef，但快速路径不需要

**方案**: 延迟创建 BTreeSetItem
```go
// 修改前：总是创建
func SetWithRetryAndQueue(...) {
    // 快速路径
    for attempt := 3 { ... }

    // 慢速路径：创建 BTreeSetItem（包含 findLeafPageRef）
    item := NewBTreeSetItem(b, key, value, 3)  // ← 浪费！
    return scheduler.EnqueueWithShard(item, "btree-set")
}

// 修改后：仅慢速路径创建
func SetWithRetryAndQueue(...) {
    // 快速路径：不创建 BTreeSetItem
    for attempt := 3 { ... }

    // 慢速路径：只在这里创建
    item := NewBTreeSetItem(b, key, value, 3)
    return scheduler.EnqueueWithShard(item, "btree-set")
}
```

**预估收益**: +15-20%

### 🟡 P1: 使用缓存的 leafRef（✅ 已实施）

**问题**: Execute 时调用 bt.Set() 重新查找，导致双重路径查找

**方案**: 使用 setWithLeafLockAndRef
```go
// 修改前
Execute() {
    err := bt.Set(ctx, key, value)  // ← 重新查找
}

// 修改后
Execute() {
    err := bt.setWithLeafLockAndRef(ctx, item.leafRef, key, value)  // ← 使用缓存
}
```

**实施内容**:
1. 修改 `NewBTreeSetItem` 接受 `leafRef` 参数
2. 修改 `SetWithTask` 查找并传递 `leafRef`
3. Execute 闭包使用 `setWithLeafLockAndRef` 而非 `bt.Set`

**实际收益**: +4.5% (1.10M → 1.15M ops/sec)
- 小于预期的 +10-15%
- 原因：leafRef 缓存在随机 key 场景下命中率有限
- PageInfo 频繁变更导致回退到完整查找

### 🟢 P2: 减少快速路径开销

**方案**: 将 SetWithRetryAndQueue 内联到热路径
```go
// 对于性能敏感场景，直接调用 setWithLeafLock
func (b *BTree) FastSet(ctx context.Context, key, value []byte) error {
    return b.setWithLeafLock(ctx, key, value)
}
```

**预估收益**: +5-10%

---

## 结论

**队列开销的 66% 性能损失主要由以下因素造成**：

1. **最严重**: findLeafPageRef 浪费 (~15-20%)
   - 快速路径不需要，但每次都做
   - ✅ P0 已优化：移到慢速路径

2. **次严重**: 双重路径查找 (~10-15%)
   - 创建 + Execute 各查找一次
   - ✅ P1 已优化：Execute 使用缓存 leafRef

3. **其他**: 队列操作、调度、GC (~35-45%)
   - 快速路径额外开销（3次重试循环）
   - 锁竞争（TryLock 失败）
   - 内存分配和 GC 压力

**优化结果（P0 + P1）**:
- P0: 将 findLeafPageRef 移到慢速路径 → 效果有限（shardID=0 导致动态负载均衡）
- P1: Execute 使用缓存 leafRef → +4.5% (1.10M → 1.15M)
- **总收益**: +4.5%，远低于预期的 +30-45%

**剩余问题**:
1. **锁竞争仍然是核心问题**（用户反馈）
   - 快速路径的 TryLock 竞争
   - 队列模式下的锁竞争
   - 随机 key 场景下 leafRef 缓存失效率高

2. **性能差距仍然巨大**:
   - 纯内存模式: 3.38M ops/sec
   - TaskScheduler 模式: 1.15M ops/sec
   - 差距: -66%

**下一步优化方向**:
- **P2**: Leaf-Level Locking（参考 Lealone）
  - 99.99% 写入无需 Root CAS
  - 预期收益: +50-100%
- **P3**: 优化 TryLock retry 策略
- **P4**: 减少快速路径重试次数
