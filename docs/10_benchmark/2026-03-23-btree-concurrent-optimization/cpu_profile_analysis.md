# CPU Profile 性能瓶颈分析

**测试日期**: 2026-03-23
**测试工具**: go test -cpuprofile + pprof
**测试场景**: BenchmarkShardRouting_LoadBalance (LoadBalance 模式)

---

## 测试配置

```bash
# 基准测试命令
go test -bench=BenchmarkShardRouting_LoadBalance \
         -benchmem -cpuprofile=/tmp/cpu.prof \
         -run=^$ ./internal/infrastructure/concurrency/...

# 性能结果
BenchmarkShardRouting_LoadBalance-12: 2729294	       447.1 ns/op
```

---

## 瓶颈分解（Top 10）

| 排名 | 函数 | Flat | Cum | 说明 |
|------|------|------|-----|------|
| 1 | `runtime.futex` | 0.24s (12.63%) | 0.24s (12.63%) | **锁竞争** ← 最大瓶颈！ |
| 2 | `(*SchedulerCore).GetTaskByName` | 0.08s (4.21%) | **0.54s (28.42%)** | **Map 查找** ← 第二大瓶颈 |
| 3 | `runtime.mapaccess2_faststr` | 0.08s (4.21%) | 0.44s (23.16%) | Map 访问 |
| 4 | `runtime.strhash` | 0.09s (4.74%) | 0.09s (4.74%) | Hash 计算 |
| 5 | `(*TaskScheduler).EnqueueWithShard` | 0.03s (1.58%) | **0.28s (14.74%)** | Enqueue 入口 |
| 6 | `(*TaskScheduler).selectLeastLoadedCore` | 0.03s (1.58%) | 0.08s (4.21%) | 负载均衡路由 |
| 7 | `runtime.schedule` | 0.01s (0.53%) | 0.30s (15.79%) | Go 调度器 |

---

## 关键函数详细分析

### 1. GetTaskByName (28.42% Cum, 540ms)

```go
func (c *SchedulerCore) GetTaskByName(name string) (*ShardTask, error) {
    taskMap := c.taskMap.Load().(map[string]*ShardTask)
    task, exists := taskMap[name]  // ← 440ms (77%)
    if !exists {
        return nil, errors.TaskNotFound(name)
    }
    return task, nil
}
```

**问题**:
- `taskMap[name]` 占 77% 时间 (440ms)
- 即使是 `getWithoutKeySmallFastStr` 优化版本，仍有 hash 计算 + map 查找

### 2. EnqueueWithShard (14.74% Cum, 280ms)

```go
func (m *TaskScheduler) EnqueueWithShard(item ShardItem, taskName string) error {
    shardID := item.ShardID()          // 10ms
    coreIndex = m.selectLeastLoadedCore()  // 80ms (28%)
    task, err := core.GetTaskByName(taskName)  // 50ms
    task.Enqueue(item)                   // 100ms
}
```

**瓶颈分布**:
- `selectLeastLoadedCore`: 80ms (28%)
- `GetTaskByName`: 50ms (18%)
- `task.Enqueue`: 100ms (36%)

### 3. selectLeastLoadedCore (4.21% Flat, 80ms)

```go
func (m *TaskScheduler) selectLeastLoadedCore() int {
    // 计算 interval (RLock)
    interval = m.calculateDynamicInterval(...)

    // atomic.Value.Load (无锁)
    cached := m.loadBalanceCache.Load().(*loadBalanceCache)

    // 遍历 cores 找最小队列
    for i, core := range m.cores {
        queueLen := core.totalQueueItems.Load()
        // ...
    }
}
```

**问题**: 遍历 6 个 cores 的原子计数器，虽然快速但仍有开销

---

## 性能瓶颈总结

| 类别 | 瓶颈 | 影响 | 优化难度 |
|------|------|------|----------|
| **锁竞争** | `runtime.futex` | 12.63% | 困难（Go runtime） |
| **Map 查找** | `GetTaskByName.map[]` | 23% | 中等（可优化） |
| **原子操作** | `totalQueueItems.Load()` | 少量 | 低（已优化） |
| **调度开销** | Go scheduler | 15.79% | 不可控 |

---

## 优化建议

### 🔴 P0: 优化 GetTaskByName Map 查找

**问题**: 每次 Enqueue 都要查找 map

**方案**: Task 指针缓存
```go
// 当前
task, _ := core.taskMap.Load().(map[string]*ShardTask)
task, exists := taskMap[name]

// 优化：存储 task 指针
type TaskScheduler struct {
    // ...
    taskPointers atomic.Value  // map[string]*ShardTask
}
```

**预期收益**: -20% (447ns → ~360ns)

### 🟡 P1: 减少 core 遍历

**问题**: selectLeastLoadedCore 遍历所有 cores

**方案**: 使用环形索引（RoundRobin）
```go
type loadBalanceCache struct {
    index    int     // 下次使用的 core index
    counter  int64   // 上次计算时的 counter
    interval int64
    // 移除 index，使用环形索引
}
```

**预期收益**: -5% (447ns → ~425ns)

### 🟢 P2: 减少锁竞争

**问题**: runtime.futex 占 12.63%

**来源**: `loadBalanceMu.RLock()` 在 calculateDynamicInterval 中

**方案**: 使用 atomic.Value 存储动态 interval
```go
type dynamicInterval struct {
    interval   int64
    maxQueueLen int64
    timestamp   int64
}
var cachedInterval atomic.Value
```

**预期收益**: -3% (447ns → ~434ns)

---

## 测试环境

- **CPU**: Intel Core i7-8700 @ 3.20GHz
- **Cores**: 12
- **Go Version**: go1.24
- **Test**: BenchmarkShardRouting_LoadBalance
- **Samples**: 1.90s (155.51% scaling)

---

## 附录：原始 pprof 输出

```
Showing nodes accounting for 1.90s, 100% of 1.90s total
      flat  flat%   sum%        cum   cum%
     0.24s 12.63% 12.63%      0.24s 12.63%  runtime.futex
     0.08s  4.21% 40.53%      0.54s 28.42%  (*SchedulerCore).GetTaskByName
     0.08s  4.21% 44.74%      0.44s 23.16%  runtime.mapaccess2_faststr
     0.03s  1.58% 71.05%      0.28s 14.74%  (*TaskScheduler).EnqueueWithShard
     0.03s  1.58% 72.63%      0.08s  4.21%  (*TaskScheduler).selectLeastLoadedCore
```
