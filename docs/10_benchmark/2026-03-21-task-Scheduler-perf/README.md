# TaskScheduler 性能分析报告

**日期**: 2026-03-21
**测试文件**: `internal/infrastructure/concurrency/task_scheduler_e2e_test.go`
**环境**: Intel(R) Core(TM) i7-8700 CPU @ 3.20GHz, Linux x86_64

---

## 一、基准测试结果

### 1.1 路由策略性能对比

| 策略 | 性能 | 相对倍数 | 最大队列长度 | 状态 |
|------|------|----------|-------------|------|
| **LoadBalance** | 596.5 ns/op | 1.00x (基准) | 3,900 | ✅ 通过 |
| **RoundRobin** | 611.0 ns/op | 1.02x | 10,004 | ✅ 通过 |
| **Fixed** | 超时 | N/A | 1,589,148+ | ❌ 超时 |

### 1.2 详细数据

```
BenchmarkShardRouting_RoundRobin-12     8946571    611.0 ns/op
RoundRobin (pre-allocated): max queue length = 10004

BenchmarkShardRouting_LoadBalance-12    9542395    596.5 ns/op
LoadBalance: max queue length = 3900

BenchmarkShardRouting_Fixed             超时
Fixed (Core 1): max queue length = 1589148
timeout waiting for tasks to complete, processed=86151, expected=1672857
```

---

## 二、CPU 性能分析

### 2.1 热点函数 (Top 20)

| 排名 | 函数 | Flat | Flat% | Cum | Cum% |
|------|------|------|-------|-----|------|
| 1 | `runtime.memmove` | 14.26s | 38.62% | 14.26s | 38.62% |
| 2 | `sync/atomic.(*Int32).Add` | 2.02s | 5.47% | 2.02s | 5.47% |
| 3 | `runtime.nanotime` | 1.65s | 4.47% | 1.65s | 4.47% |
| 4 | `internal/sync.(*Mutex).Unlock` | 1.54s | 4.17% | 1.66s | 4.50% |
| 5 | `internal/sync.(*Mutex).Lock` | 1.38s | 3.74% | 2.12s | 5.74% |
| 6 | `runtime.tryDeferToSpanScan` | 1.05s | 2.84% | 1.25s | 3.39% |
| 7 | `runtime.futex` | 0.91s | 2.46% | 0.91s | 2.46% |
| 8 | `runtime.unlock2` | 0.80s | 2.17% | 0.94s | 2.55% |
| 9 | `runtime.lock2` | 0.78s | 2.11% | 0.82s | 2.22% |
| 10 | `(*SchedulerCore).GetTaskByName` | 0.63s | 1.71% | 3.52s | 9.53% |
| 11 | `runtime.(*timer).unlock` | 0.60s | 1.63% | 1.57s | 4.25% |
| 12 | `runtime.mallocgcSmallScanNoHeader` | 0.54s | 1.46% | 1.40s | 3.79% |
| 13 | `(*ShardTask).QueueLen` | 0.52s | 1.41% | 2.49s | 6.74% |
| 14 | `aeshashbody` | 0.48s | 1.30% | 0.48s | 1.30% |
| 15 | `(*TaskScheduler).EnqueueWithShard` | 0.48s | 1.30% | 2.25s | 6.09% |
| 16 | `internal/runtime/maps.(*Map).getWithoutKeySmallFastStr` | 0.47s | 1.27% | 1.12s | 3.03% |
| 17 | `internal/sync.(*Mutex).lockSlow` | 0.38s | 1.03% | 0.74s | 2.00% |
| 18 | `runtime.procyieldAsm` | 0.37s | 1.00% | 0.37s | 1.00% |
| 19 | `runtime.nanotime1` | 0.36s | 0.98% | 0.36s | 0.98% |
| 20 | `runtime.typePointers.next` | 0.33s | 0.89% | 0.40s | 1.08% |

### 2.2 CPU 时间分布

```
总采样时间: 36.92s (实际运行 22.18s, CPU 使用率 166.45%)

主要耗时分布:
├── 内存操作 (memmove)        38.62%  ████████████████████████████
├── 原子操作                   5.47%  ██
├── 锁操作 (Lock/Unlock)       8.67%  ███
├── GetTaskByName              9.53%  ███
├── QueueLen                   6.74%  ██
└── 其他                      31.00%  ████████████
```

### 2.3 GetTaskByName 详细分析

```
ROUTINE: (*SchedulerCore).GetTaskByName
Total: 3.52s (9.53% of Total)

     630ms      3.52s (flat, cum)
     150ms      150ms    func (c *SchedulerCore) GetTaskByName(name string) (*ShardTask, error) {
          .      940ms        c.mu.RLock()       # 读锁开销
      40ms       40ms        defer c.mu.RUnlock()
          .          .
      80ms      1.39s        task, exists := c.taskMap[name]  # map 查找
          .          .
     360ms         1s         return task, nil
```

**分析**:
- 读锁开销: 940ms (26.7%)
- map 查找: 1.39s (39.5%)
- 函数调用开销: 1s (28.4%)

---

## 三、内存性能分析

### 3.1 内存分配热点 (Top 10)

| 排名 | 函数 | Flat | Flat% | Cum | Cum% |
|------|------|------|-------|-----|------|
| 1 | `(*SchedulerCore).getOrderedTasks` | 574.01MB | 80.04% | 574.01MB | 80.04% |
| 2 | `(*ShardTask).Enqueue` | 133.97MB | 18.68% | 134.97MB | 18.82% |
| 3 | `(*SchedulerCore).runLoop` | 0 | 0% | 563.01MB | 78.51% |
| 4 | `(*TaskScheduler).EnqueueWithShard` | 0 | 0% | 145.97MB | 20.35% |
| 5 | `(*TaskScheduler).selectLeastLoadedCore` | 0 | 0% | 11MB | 1.53% |

### 3.2 getOrderedTasks 详细分析

```
ROUTINE: (*SchedulerCore).getOrderedTasks
Total: 717.16MB

  574.01MB   574.01MB (flat, cum) 80.04% of Total

  137.50MB   137.50MB    tasks := make([]*ShardTask, len(c.tasks))
                          copy(tasks, c.tasks)     # 切片复制

  436.51MB   436.51MB    sort.Slice(tasks, func(i, j int) bool {
                          return tasks[i].ExecutionOrder() < tasks[j].ExecutionOrder()
                      })                         # 排序分配
```

**分析**:
- 切片复制: 137.50MB (23.96%)
- 排序分配: 436.51MB (76.04%)

### 3.3 内存分配总览

```
总分配内存: 717.16MB

主要分配来源:
├── getOrderedTasks (切片复制)     137.50MB (19.18%)  ████
├── getOrderedTasks (排序)         436.51MB (60.86%)  ████████████
├── ShardTask.Enqueue              133.97MB (18.68%)  ███
└── 其他                             9.18MB  (1.28%)  ▌
```

---

## 四、关键发现与瓶颈

### 4.1 性能瓶颈

| 瓶颈 | 影响 | 优先级 | 优化方向 |
|------|------|--------|----------|
| **getOrderedTasks 排序分配** | 436.51MB (60.86%) | P0 | 预排序缓存 |
| **getOrderedTasks 切片复制** | 137.50MB (19.18%) | P1 | 避免每次复制 |
| **GetTaskByName map 查找** | 1.39s (3.77%) | P2 | Task 缓存 |
| **Lock/Unlock 开销** | 8.67% CPU | P2 | 减少锁粒度 |
| **memmove (内存拷贝)** | 38.62% CPU | P3 | 优化数据结构 |

### 4.2 LoadBalance vs RoundRobin

**为什么 LoadBalance 更快?**
- **缓存优化**: LoadBalance 使用缓存 (interval=100), 避免每次计算负载
- **队列管理**: 最大队列长度 3,900 vs 10,004 (负载更均衡)

**RoundRobin 分析**:
- 理论上应该最快 (固定路由, 无计算)
- 实际测试中稍慢 (611.0 vs 596.5 ns/op)
- 原因: 队列长度较大 (10,004), 可能导致更多锁竞争

### 4.3 Fixed 策略问题

**问题**: 单核心瓶颈导致队列积压
- 超过 150 万项队列积压
- 仅处理 86,151/1,672,857 (5.15%)
- 单核无法处理高并发入队

**结论**: Fixed 适用于**低并发、绑定特定分片**场景, 不适合通用高并发场景

---

## 五、优化建议

### 5.1 P0 优化 (预期收益 >30%)

#### 建议 1: 预排序任务列表
```go
// 当前问题: 每次调度循环都排序 (436.51MB 分配)
func (c *SchedulerCore) getOrderedTasks() []*ShardTask {
    tasks := make([]*ShardTask, len(c.tasks))
    copy(tasks, c.tasks)
    sort.Slice(tasks, func(i, j int) bool { ... })  // ← 热点!
    return tasks
}

// 优化方案: 注册时预排序, 返回只读切片
func (c *SchedulerCore) getOrderedTasks() []*ShardTask {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.tasksSorted  // 返回预排序的只读切片
}
```
**预期收益**: 减少 60% 内存分配, 提升调度吞吐量

#### 建议 2: 缓存 Task 查找
```go
// 当前问题: 每次 Enqueue 都要 map 查找 (1.39s CPU)
func (m *TaskScheduler) EnqueueWithShard(item ShardItem, taskName string) error {
    task, err := core.GetTaskByName(taskName)  // ← 热点!
    ...
}

// 优化方案: 缓存 Task 指针
type TaskScheduler struct {
    // ...
    cachedTasks map[string]*ShardTask  // coreID:taskName → *ShardTask
}

func (m *TaskScheduler) EnqueueWithShard(item ShardItem, taskName string) error {
    task := m.cachedTasks[fmt.Sprintf("%d:%s", coreIndex, taskName)]
    ...
}
```
**预期收益**: 减少 9.53% CPU 时间

### 5.2 P1 优化 (预期收益 10-20%)

#### 建议 3: 减少锁粒度
```go
// 当前: RWMutex 保护整个 taskMap
func (c *SchedulerCore) GetTaskByName(name string) (*ShardTask, error) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    task, exists := c.taskMap[name]
    ...
}

// 优化: 使用 sync.Map (读多写少场景)
type SchedulerCore struct {
    taskMap sync.Map  // string → *ShardTask
}

func (c *SchedulerCore) GetTaskByName(name string) (*ShardTask, error) {
    task, exists := c.taskMap.Load(name)
    ...
}
```
**预期收益**: 减少锁竞争, 提升并发性能

#### 建议 4: 避免切片复制
```go
// 当前: 每次都复制切片
func (c *SchedulerCore) getOrderedTasks() []*ShardTask {
    tasks := make([]*ShardTask, len(c.tasks))
    copy(tasks, c.tasks)  // ← 137.50MB 分配
    ...
}

// 优化: 返回只读切片 + COW
func (c *SchedulerCore) getOrderedTasks() []*ShardTask {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.tasks  // 返回只读视图
}

// RegisterTask 时创建新切片 (COW)
func (c *SchedulerCore) RegisterTask(taskTemplate *ShardTask) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    newTasks := make([]*ShardTask, len(c.tasks)+1)
    copy(newTasks, c.tasks)
    newTasks[len(c.tasks)] = task

    c.tasks = newTasks
    ...
}
```
**预期收益**: 减少 19% 内存分配

### 5.3 P2 优化 (预期收益 5-10%)

#### 建议 5: 减少 memmove
```go
// 问题分析: runtime.memmove 占 38.62% CPU
// 主要来源:
// 1. 切片复制 (copy(tasks, c.tasks))
// 2. 队列操作 (copy(t.queue, t.queue[1:]))

// 优化: 使用环形队列
type RingQueue struct {
    data []any
    head int
    tail int
    size int
}

func (q *RingQueue) Dequeue() any {
    if q.size == 0 {
        return nil
    }
    item := q.data[q.head]
    q.head = (q.head + 1) % len(q.data)
    q.size--
    return item
}
```
**预期收益**: 减少内存拷贝开销

---

## 六、结论

### 6.1 性能评估

| 指标 | 当前状态 | 评级 |
|------|----------|------|
| **路由延迟** | ~600 ns/op | ✅ 优秀 |
| **内存分配** | 717 MB/22s | ⚠️ 可优化 |
| **CPU 效率** | 166% (多核) | ✅ 良好 |
| **LoadBalance** | 596.5 ns/op | ✅ 最优 |
| **RoundRobin** | 611.0 ns/op | ✅ 良好 |
| **Fixed** | 超时 | ❌ 不适用 |

### 6.2 适用场景

| 策略 | 适用场景 | 推荐度 |
|------|----------|--------|
| **LoadBalance** | 通用高并发、动态负载 | ⭐⭐⭐⭐⭐ |
| **RoundRobin** | 固定分片数、均匀分布 | ⭐⭐⭐⭐ |
| **Fixed** | 低并发、单分片绑定 | ⭐⭐ |

### 6.3 优先级建议

1. **立即行动 (P0)**: 实现 getOrderedTasks 预排序缓存
2. **短期优化 (P1)**: Task 查找缓存 + 减少锁粒度
3. **长期改进 (P2)**: 环形队列 + sync.Map 迁移

### 6.4 预期收益

| 优化项 | 当前 | 优化后 | 提升 |
|--------|------|--------|------|
| 内存分配 | 717 MB | ~200 MB | 3.6x |
| 路由延迟 | 600 ns | ~400 ns | 1.5x |
| 吞吐量 | 1.6M ops/s | ~2.5M ops/s | 1.56x |

---

## 七、测试环境

```bash
# 测试命令
go test -bench=^BenchmarkShardRouting -run=^$ -benchtime=5s \
  -cpuprofile=cpu.prof \
  -memprofile=mem.prof \
  ./internal/infrastructure/concurrency

# 系统信息
CPU: Intel(R) Core(TM) i7-8700 @ 3.20GHz (6 cores, 12 threads)
OS: Linux x86_64
Go: 1.26
GOMAXPROCS: 12 (automaxprocs)
```

---

## 八、参考资料

- **测试代码**: `internal/infrastructure/concurrency/task_scheduler_e2e_test.go`
- **核心实现**: `internal/infrastructure/concurrency/task_scheduler.go`
- **类型定义**: `internal/infrastructure/concurrency/types.go`
- **Profiling 数据**: `cpu.prof`, `mem.prof`
