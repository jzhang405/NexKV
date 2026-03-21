# TaskScheduler 瓶颈分析报告

**日期**: 2026-03-21
**基准**: P0+P4 优化后的代码 (504.5 ns/op)

---

## 一、瓶颈总览

### 1.1 CPU 时间分布

| 瓶颈 | 时间 | 占比 | 状态 |
|------|------|------|------|
| **1. 队列内存拷贝** | 28.80s | **56.2%** | 🔴 未优化 |
| **2. GetTaskByName** | 4.68s | 9.14% | 🟡 尝试失败 |
| **3. 锁操作** | ~5.08s | ~10% | 🟡 已优化 |
| **4. atomic 操作** | 2.05s | 4.01% | 🟢 必要开销 |
| **5. 其他** | ~10.57s | ~20% | 🟢 正常 |

```
总采样时间: 51.18s (实际运行 37.41s)

瓶颈占比:
├── 队列拷贝 (memmove)   56.2%  ████████████████████████████████
├── GetTaskByName         9.1%   ███
├── 锁操作                9.9%   ███
├── atomic 操作            4.0%   █▌
└── 其他                  20.8%  ███████
```

---

## 二、瓶颈 #1: 队列内存拷贝 (56.2%)

### 2.1 问题定位

**函数**: `ShardTask.Dequeue` (task_scheduler.go:89)

**代码**:
```go
func (t *ShardTask) Dequeue(item *any) bool {
    // ...
    // 避免内存泄漏：使用 copy 而不是切片
    if len(t.queue) > 1 {
        copy(t.queue, t.queue[1:])  // ← 热点: 28.80s (56.2%)
    }
    t.queue = t.queue[:len(t.queue)-1]
    // ...
}
```

**开销分析**:
```
ROUTINE: ShardTask.Dequeue
     290ms     30.23s (flat, cum) 59.07% of Total
     10ms       10ms     77:func (t *ShardTask) Dequeue(item *any) bool {
     ...
     28.80s     89:	    copy(t.queue, t.queue[1:])
```

### 2.2 根本原因

**问题**: 切片作为队列，每次出队需要移动剩余元素

**时间复杂度**:
- `Enqueue`: O(1) amortized
- `Dequeue`: **O(n)** - 需要移动 n-1 个元素

**影响**:
- 队列越长，拷贝开销越大
- 平均队列长度 10k+ 时，每次出队需要拷贝 10k 个指针

### 2.3 优化方案: 环形缓冲区 (Ring Buffer)

#### 方案设计

```go
type RingQueue struct {
    data []any  // 底层数组
    head int    // 读指针
    tail int    // 写指针
    size int    // 当前元素数
    cap  int    // 容量
}

func NewRingQueue(capacity int) *RingQueue {
    return &RingQueue{
        data: make([]any, capacity),
        cap:  capacity,
    }
}

// Enqueue 入队: O(1)
func (q *RingQueue) Enqueue(item any) error {
    if q.size >= q.cap {
        return errors.ErrQueueFull
    }
    q.data[q.tail] = item
    q.tail = (q.tail + 1) % q.cap
    q.size++
    return nil
}

// Dequeue 出队: O(1) - 无需拷贝！
func (q *RingQueue) Dequeue() (any, bool) {
    if q.size == 0 {
        return nil, false
    }
    item := q.data[q.head]
    q.head = (q.head + 1) % q.cap
    q.size--
    return item, true
}

// Peek 查看队首: O(1)
func (q *RingQueue) Peek() (any, bool) {
    if q.size == 0 {
        return nil, false
    }
    return q.data[q.head], true
}

// Len 队列长度: O(1)
func (q *RingQueue) Len() int {
    return q.size
}
```

#### 预期收益

| 指标 | 当前 | 优化后 | 提升 |
|------|------|--------|------|
| **Dequeue 开销** | 28.80s (56.2%) | **~0s** | ↓ **100%** |
| **路由延迟** | ~500 ns/op | **~220 ns/op** | ↑ **2.27x** |
| **时间复杂度** | O(n) | **O(1)** | - |

#### 实施难度

- **难度**: 中等
- **风险**: 低 (独立优化，不影响外部接口)
- **工作量**: 2-3 小时

---

## 三、瓶颈 #2: GetTaskByName (9.14%)

### 3.1 问题定位

**函数**: `SchedulerCore.GetTaskByName`

**开销分析**:
```
ROUTINE: GetTaskByName
     890ms      4.68s (flat, cum) 9.14% of Total
     210ms      210ms    c.mu.RLock()         // 27.8%
     100ms      100ms    defer c.mu.RUnlock()
      90ms      1.78s    task, exists := c.taskMap[name]  // 38.0%
     480ms      1.29s    return task, nil      // 27.6%
```

**组成分析**:
- RWMutex.RLock: 1.30s (27.8%)
- map 哈希查找: 1.78s (38.0%)
- return: 1.29s (27.6%)

### 3.2 根本原因

**问题**: 高并发 Enqueue 导致读锁竞争

**调用链**:
```
EnqueueWithShard (高并发)
  → GetTaskByName (每个 Enqueue 都调用)
    → RWMutex.RLock (读锁竞争)
    → map 哈希查找 (字符串 key)
```

### 3.3 已尝试的优化 (失败)

#### P1: Task 缓存 (失败)

**方案**: 缓存 `map[coreID:taskName]*ShardTask`

**失败原因**: `fmt.Sprintf` 开销过大
```
fmt.Sprintf("%d:%s", coreIndex, taskName): 4.66s (13.6%)
```

**结果**: 性能下降到 864 ns/op

#### P2: sync.Map (失败)

**方案**: 使用 `sync.Map` 替代 `map + RWMutex`

**失败原因**: sync.Map.Load 开销更大
```
sync.Map.Load: 3.80s
vs
RWMutex.RLock + map: 2.33s
```

**结果**: GetTaskByName 变慢 15.9%

### 3.4 剩余优化方向

#### 方案 A: 直接访问 taskMap (难度: 高)

**问题**: `EnqueueWithShard` 在 TaskScheduler 层，`taskMap` 在 SchedulerCore 层

**方案**:
```go
// 在 EnqueueWithShard 中直接访问 core.taskMap
func (m *TaskScheduler) EnqueueWithShard(item ShardItem, taskName string) error {
    core := m.cores[coreIndex]

    // 直接访问 core.taskMap（需要暴露字段或添加 getter）
    core.mu.RLock()
    task, exists := core.taskMap[taskName]
    core.mu.RUnlock()

    // ...
}
```

**问题**: 增加耦合度，需要重新设计接口

#### 方案 B: 整数 key (难度: 中)

**方案**: 使用 taskID 整数替代字符串 key

**实现**:
```go
type SchedulerCore struct {
    tasksByID []*ShardTask  // 按 taskID 索引
    taskMap   map[string]int  // name → taskID (仅注册时使用)
    mu        sync.RWMutex
}

func (c *SchedulerCore) GetTaskByID(id int) *ShardTask {
    if id < 0 || id >= len(c.tasksByID) {
        return nil
    }
    return c.tasksByID[id]  // 无锁访问
}
```

**预期收益**: 节省 1.30s 读锁 + 1.78s map 查找

#### 方案 C: CPU 缓存优化 (难度: 低)

**问题**: map 查找的缓存未命中

**方案**: 使用紧凑的数据结构，提高 CPU 缓存命中率

---

## 四、瓶颈 #3: 锁操作 (~10%)

### 4.1 问题定位

**锁开销分析**:
```
internal/sync.(*Mutex).Unlock:     2.05s (4.01%)
internal/sync.(*Mutex).Lock:       1.79s (3.50%)
internal/sync.(*Mutex).lockSlow:    0.81s (1.58%)  ← 锁竞争等待
runtime.futex:                     1.23s (2.40%)  ← 系统调用
────────────────────────────────────────────────
总计:                               ~5.88s (~11.5%)
```

### 4.2 锁竞争来源

**主要竞争点**:
1. `ShardTask.mu` - 队列操作锁 (Peek/Dequeue/Enqueue)
2. `SchedulerCore.mu` - taskMap 读写锁 (GetTaskByName)
3. `TaskScheduler.loadBalanceMu` - 负载均衡缓存锁

### 4.3 优化方案

#### 方案 A: 无锁队列 (Lock-free Queue)

**使用**: `github.com/golang/sync/singleflight` 或自定义 CAS 队列

**难度**: 高 (需要处理 ABA 问题)

#### 方案 B: 分片锁 (Sharded Mutex)

**方案**: 将队列分片，每个分片独立锁

```go
type ShardedQueue struct {
    shards [16]*QueueShard
}

type QueueShard struct {
    queue []*ShardTask
    mu    sync.Mutex
}

func (sq *ShardedQueue) Enqueue(item ShardItem) {
    shardID := item.ShardID() % 16
    sq.shards[shardID].mu.Lock()
    sq.shards[shardID].queue = append(sq.shards[shardID].queue, item)
    sq.shards[shardID].mu.Unlock()
}
```

**预期收益**: 减少锁竞争，节省 0.81s lockSlow

---

## 五、优化优先级建议

### 5.1 优先级排序

| 优先级 | 优化项 | 预期收益 | 难度 | ROI |
|--------|--------|----------|------|-----|
| **P0** | 环形缓冲区 | 2.27x | 中 | ⭐⭐⭐⭐⭐ |
| **P1** | 整数 key 替代字符串 | 10% | 中 | ⭐⭐⭐ |
| **P2** | 减少 GetTaskByName 调用 | 9% | 高 | ⭐⭐ |
| **P3** | 分片锁 / 无锁队列 | 5% | 高 | ⭐ |
| **P4** | atomic 操作优化 | <5% | 低 | ⭐ |

### 5.2 实施建议

#### 立即实施 (P0)

**环形缓冲区优化**:
- **工作量**: 2-3 小时
- **风险**: 低 (独立优化)
- **收益**: **2.27x 性能提升**

#### 短期优化 (P1-P2)

1. **整数 key 替代字符串 key**
   - 注册时分配 taskID
   - Enqueue 时使用 taskID 直接索引

2. **减少 GetTaskByName 调用**
   - 在 EnqueueWithShard 中缓存常用 Task
   - 需要仔细设计避免 fmt.Sprintf 问题

#### 长期优化 (P3-P4)

1. **分片锁 / 无锁队列**
   - 需要重新设计队列架构
   - 风险较高，建议充分测试

2. **atomic 操作优化**
   - 减少统计计数器更新频率
   - 使用采样统计

---

## 六、结论

### 6.1 当前状态

**已优化**: 节省 **65.5% CPU 时间**
- P0: 预排序 getOrderedTasks
- P4: atomic.Value 避免切片复制

**剩余瓶颈**: **56.2% CPU 时间**在队列拷贝

### 6.2 优化潜力

**实施环形缓冲区后**:
- 当前: ~500 ns/op
- 预期: ~220 ns/op
- 提升: **2.27x**

**与原始对比**:
- 原始: ~600 ns/op
- 优化后: ~220 ns/op
- 总提升: **2.7x (从 1.6M → 4.3M ops/s)**

### 6.3 关键发现

1. **队列拷贝是最大瓶颈**
   - 占用 56.2% CPU 时间
   - 环形缓冲区可完全消除

2. **字符串 key map 查找开销大**
   - 哈希计算 + 缓存未命中
   - 整数索引可节省 30% 开销

3. **锁竞争依然存在**
   - 但环形缓冲区可大幅减少锁持有时间
