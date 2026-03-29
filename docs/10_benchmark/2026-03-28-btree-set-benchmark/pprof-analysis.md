# BTree Set pprof CPU 热点分析

## 测试配置

| 参数 | 值 |
|------|-----|
| 并发 | 4 线程 |
| 每线程 | 50,000 ops |
| 预热数据 | 1,000 条 |
| 总操作 | 200,000 |
| GOGC | 500 |

## 测试结果

| 指标 | 值 |
|------|-----|
| 成功率 | **14.17%** |
| 成功 | 28,341 |
| 失败 | 171,659 |
| 吞吐量 | 7,993 ops/s |
| 延迟 | 125.11 μs/op |
| 耗时 | 3.55s |

## CPU 热点排名 (Top 15)

| # | 函数 | 自身 | 占比 | 累计 | 说明 |
|---|------|------|------|------|------|
| 1 | `runtime.futex` | 1.84s | 13.50% | 13.50% | 线程挂起/唤醒（锁竞争） |
| 2 | `runtime.memclrNoHeapPointers` | 1.27s | 9.32% | 22.82% | 内存清零（新页分配） |
| 3 | `runtime.getMCache` | 1.27s | 9.32% | 32.14% | 内存分配器竞争 |
| 4 | `runtime.procyieldAsm` | 1.27s | 9.32% | 41.46% | CAS 自旋等待 |
| 5 | `runtime.tryDeferToSpanScan` | 1.27s | 9.32% | 50.79% | GC 扫描 |
| 6 | `BTree.searchPathWithRefs` | 1.01s | 7.41% | 58.20% | 搜索路径遍历 |
| 7 | `runtime.unlock2` | 0.84s | 6.16% | 64.36% | 解锁开销 |
| 8 | `PageAccessor.SearchKey` | 0.78s | 5.72% | 70.08% | 页内二分搜索 |
| 9 | `PageManager.PageIDToPtr` | 0.77s | 5.65% | 75.73% | PageID→指针转换 |
| 10 | `runtime.mallocgc` | 0.53s | 3.89% | 79.62% | 堆分配 |
| 11 | `runtime.mallocgcTiny` | 0.89s | 6.53% | 86.15% | 小对象分配 |
| 12 | `runtime.schedule` | 0.68s | 4.99% | 91.14% | 调度器开销 |
| 13 | `BTree.findLeafPageRef` | 1.02s | 7.48% | 98.62% | 叶子页查找 |
| 14 | `OffHeapAdapter.linearSearchLeaf` | 0.82s | 6.02% | 104.64% | **线性搜索**（未排序？） |
| 15 | `OffHeapAdapter.GetLeafEntry` | 0.78s | 5.72% | 110.36% | 叶子条目读取 |

## 调用链累计耗时 (Cumulative)

| # | 调用链 | 累计 | 占比 |
|---|--------|------|------|
| 1 | `main → BTree.Set → setWithLeafLock` | 8.63s | 63.3% |
| 2 | `setWithLeafLock → handleSplitOffHeapSync` | 6.55s | 48.1% |
| 3 | `handleSplitOffHeapSync → SplitOffHeapLeafPage` | 6.05s | 44.4% |
| 4 | `SplitOffHeapLeafPage → runtime.mallocgc` | 3.09s | 22.7% |

---

## 详细 Code Path（含源文件位置）

### Code Path 1: 写入主路径 (63.3% CPU)

```
main.func1                                              goroutine 入口
  └─ BTree.Set                                          btree.go:696
       └─ BTree.SetWithRetryAndQueue                     btree_ops.go:241
            └─ BTree.setWithLeafLock                     leaf_lock_set.go:211
                 ├─ BTree.handleSplitOffHeapSync          leaf_lock_set.go:728  (48.1%)
                 ├─ BTree.searchPathWithRefs             search_path.go:208  (7.4%)
                 └─ BTree.findLeafPageRef                search_path.go:297  (7.5%)
```

**文件位置**:
- `internal/infrastructure/storage/btree/btree.go:696` — `Set()` 入口
- `internal/infrastructure/storage/btree/btree_ops.go:241` — `SetWithRetryAndQueue()` 重试循环
- `internal/infrastructure/storage/btree/leaf_lock_set.go:211` — `setWithLeafLock()` 核心写入逻辑

### Code Path 2: 叶子分裂路径 (48.1% CPU — 最大瓶颈)

```
BTree.setWithLeafLock                                   leaf_lock_set.go:211
  └─ BTree.handleSplitOffHeapSync                       leaf_lock_set.go:728  (6.55s)
       └─ OffHeapAdapter.SplitOffHeapLeafPage           offheap_adapter.go:704 (6.05s)
            ├─ runtime.mallocgc                         分配新页面         (3.09s, 22.7%)
            ├─ runtime.memclrNoHeapPointers             清零 4KB 新页面    (1.27s, 9.3%)
            ├─ runtime.procyieldAsm                     CAS 自旋等待       (1.27s, 9.3%)
            ├─ OffHeapAdapter.linearSearchLeaf          线性搜索分裂点     (0.82s, 6.0%)
            ├─ PageAccessor.GetValue                    读取 KV 数据       offheap_adapter.go:600
            ├─ PageAccessor.GetKey                      读取 Key           offheap_adapter.go:595
            ├─ runtime.lock2                            获取内部锁         leaf_lock_set.go:645
            └─ runtime.mallocgcTiny                     小对象分配         offheap_adapter.go:627-631
```

**文件位置**:
- `internal/infrastructure/storage/btree/leaf_lock_set.go:728` — `handleSplitOffHeapSync()` 分裂入口
- `internal/infrastructure/storage/btree/leaf_lock_set.go:568` — 分裂期间的 CAS 操作
- `internal/infrastructure/storage/btree/leaf_lock_set.go:645` — 分裂期间获取 runtime lock
- `internal/infrastructure/storage/btree/leaf_lock_set.go:822` — 分裂完成后的页面访问
- `internal/infrastructure/storage/btree/leaf_lock_set.go:359` — 索引页插入
- `internal/infrastructure/storage/btree/offheap_adapter.go:704-705` — `SplitOffHeapLeafPage()` 核心
- `internal/infrastructure/storage/btree/offheap_adapter.go:629` — 新页分配+清零
- `internal/infrastructure/storage/btree/offheap_adapter.go:600` — GetValue（分裂时拷贝数据）
- `internal/infrastructure/storage/btree/offheap_adapter.go:595` — GetKey（分裂时比较 key）

### Code Path 3: 搜索路径 (14.9% CPU)

```
BTree.setWithLeafLock                                    leaf_lock_set.go:39
  └─ BTree.setWithLeafLockAndRef                        btree_ops.go:339
       ├─ BTree.findLeafPageRef                         search_path.go:297  (7.5%)
       │    └─ BTree.searchPathWithRefs                 search_path.go:208  (7.4%)
       │         ├─ OffHeapAdapter.SearchChild          offheap_adapter.go (3.96%)
       │         │    └─ PageAccessor.SearchKey         offheap/page_layout.go (5.72%)
       │         │         └─ PageManager.PageIDToPtr   offheap/page_manager.go:152 (5.65%)
       │         └─ PageAccessor.GetLeafEntryOffset     offheap/page_layout.go (7.34%)
       │              └─ PageManager.PageIDToPtr        offheap/page_manager.go:152
       └─ OffHeapAdapter.linearSearchLeaf               offheap_adapter.go:189 (6.02%)
            ├─ PageAccessor.GetLeafEntry                offheap/page_layout.go (5.72%)
            ├─ PageAccessor.GetKey                      offheap/page_layout.go (4.04%)
            └─ PageManager.PageIDToPtr                  offheap/page_manager.go:152 (5.65%)
```

**文件位置**:
- `internal/infrastructure/storage/btree/search_path.go:297` — `findLeafPageRef()`
- `internal/infrastructure/storage/btree/search_path.go:208` — `searchPathWithRefs()`
- `internal/infrastructure/storage/btree/offheap_adapter.go:189-190` — `linearSearchLeaf()` — **应改用二分搜索**
- `internal/infrastructure/storage/btree/offheap/page_layout.go:245` — `GetKey()`
- `internal/infrastructure/storage/btree/offheap/page_layout.go:252` — `GetValue()`
- `internal/infrastructure/storage/btree/offheap/page_manager.go:152` — `PageIDToPtr()`

### Code Path 4: TaskScheduler 路径 (5.1% CPU)

```
SchedulerCore.runLoop                                   task_scheduler.go:417
  └─ coreWorker.run
       └─ coreWorker.executeTask
            └─ recovery.Safe
                 └─ coreWorker.executeTask.func1
                      └─ BaseTask[go.shape.struct {}].Run              task.go:277
                           └─ ShardTask.Execute
                                └─ BTree.SetWithTask                  btree_ops.go
                                     └─ BTreeSetItem.func1             btree_set_item.go:78
                                          └─ setWithLeafLockAndRef    btree_ops.go:339
```

**文件位置**:
- `internal/infrastructure/concurrency/task_scheduler.go:417` — `runLoop()`
- `internal/domain/model/task.go:277` — `BaseTask.Run()`
- `internal/infrastructure/storage/btree/btree_set_item.go:78` — `SetWithTask` 的闭包

### Code Path 5: 锁竞争路径 (25%+ CPU)

```
锁等待：
  runtime.futex                                         1.84s (13.5%)  — 系统级挂起
  runtime.park_m                                        2.55s (18.7%)  — goroutine 挂起
  runtime.notesleep                                     1.22s (8.95%)  — note 睡眠
  runtime.futexsleep                                    1.20s (8.80%)  — futex 睡眠

锁唤醒：
  runtime.unlock2                                       0.84s (6.16%)  — 解锁
  runtime.notewakeup                                    0.70s (5.14%)  — note 唤醒
  runtime.futexwakeup                                   0.68s (4.99%)  — futex 唤醒
  runtime.startlockedm                                  0.72s (5.28%)  — 启动锁定的 M

调度器开销：
  runtime.schedule                                      2.68s (19.7%)  — 调度
  runtime.findRunnable                                  1.11s (8.14%)  — 查找可运行 G
  runtime.stopm                                         0.95s (6.97%)  — 停止 M
```

**锁竞争触发点**:
- `leaf_lock_set.go:645` — `handleSplitOffHeapSync` 中 `runtime.lock2` 调用
- `leaf_lock_set.go:568` — 分裂期间 CAS 操作
- `btree.go:697` — `Set()` 入口处的线性搜索触发的 `runtime.lock2`

### Code Path 6: 内存分配路径 (22.7% CPU)

```
OffHeapAdapter.SplitOffHeapLeafPage                     offheap_adapter.go:629
  ├─ runtime.mallocgc                                   3.09s (22.7%)
  │    └─ runtime.getMCache                             1.27s (9.3%)  — 分配器竞争
  ├─ runtime.memclrNoHeapPointers                       1.27s (9.3%)  — 4KB 页清零
  ├─ runtime.mallocgcTiny                               0.89s (6.5%)  — 小对象分配
  │    (offheap_adapter.go:627, 631) — SplitOffHeapLeafPage 中的临时对象
  └─ runtime.tryDeferToSpanScan                         1.27s (9.3%)  — GC 触发
```

**文件位置**:
- `internal/infrastructure/storage/btree/offheap_adapter.go:629` — 新页分配点
- `internal/infrastructure/storage/btree/offheap_adapter.go:627` — mallocgcTiny 调用点
- `internal/infrastructure/storage/btree/offheap_adapter.go:631` — mallocgcTiny 调用点

---

## 关键发现

### 1. 叶子分裂是最大瓶颈（44.4% CPU）

`handleSplitOffHeapSync` 占了 48.1% 的 CPU 时间，其中：
- **内存清零**（`memclrNoHeapPointers`）：9.32% — 每次分裂分配新页需要清零 4KB
- **GC 开销**（`mallocgc` + `getMCache`）：13% — 分配触发 GC
- **锁等待**（`futex` + `procyieldAsm`）：22.8% — 分裂期间持锁阻塞其他线程

### 2. 高 CAS 失败率（成功率仅 14.17%）

200K 操作中 171K 失败，说明：
- TryLock 竞争激烈
- setDirect 的 10 次重试不够
- 大量 goroutine 在 futex 上等待

### 3. 线性搜索浪费（`linearSearchLeaf`）

`linearSearchLeaf` 占 6.02%，存在两个问题：
- 应该用二分搜索（`SearchKey` 已有但也在热点中）
- 叶子分裂后数据可能未保持有序

### 4. `PageIDToPtr` 开销（5.65%）

每次页访问都要做 PageID → 指针转换，频繁调用。可以考虑缓存或内联。

## 优化建议（按优先级）

### P0: 降低叶子分裂频率（预期提升 40%+）

分裂操作占 48% CPU，且持锁时间长阻塞并发：
- **增大叶子页容量**：考虑 8KB 或 16KB 页
- **批量分裂**：积攒多个写入后一次性分裂
- **分裂期间释放锁**：当前分裂全程持锁，阻塞同页其他写入

**影响文件**:
- `internal/infrastructure/storage/btree/leaf_lock_set.go:728` — `handleSplitOffHeapSync`
- `internal/infrastructure/storage/btree/offheap_adapter.go:704` — `SplitOffHeapLeafPage`

### P1: 修复 CAS 重试策略（预期提升 30%+）

成功率仅 14.17%，86% 操作浪费 CPU：
- **增加重试次数**：从 10 提高到 50-100
- **指数退避**：避免空转
- **记录重试失败原因**：区分 TryLock 失败 vs CAS 失败

**影响文件**:
- `internal/infrastructure/storage/btree/btree.go:712` — `setDirect` 重试循环（maxRetries=10）

### P2: 减少内存分配（预期提升 15%）

`mallocgc` + `memclr` 合计 22%：
- **页面池**：预分配页面，避免运行时 malloc
- **零填充优化**：新页使用 `memset` 批量清零代替逐字节

**影响文件**:
- `internal/infrastructure/storage/btree/offheap_adapter.go:629` — 新页分配
- `internal/infrastructure/storage/btree/offheap/page_manager.go:152` — PageIDToPtr

### P3: 优化搜索（预期提升 5-10%）

`linearSearchLeaf` + `SearchKey` 合计 12%：
- **确保二分搜索路径优先**：消除线性搜索回退
- **缓存热点页**：减少 `PageIDToPtr` 调用

**影响文件**:
- `internal/infrastructure/storage/btree/offheap_adapter.go:189-190` — `linearSearchLeaf`
- `internal/infrastructure/storage/btree/offheap/page_layout.go:245` — `SearchKey`

## Profile 文件

- `cpu_4t_50k.prof` — 4 线程 50K ops CPU profile
- 查看: `go tool pprof -http=:8080 docs/10_benchmark/2026-03-28-btree-set-benchmark/cpu_4t_50k.prof`
