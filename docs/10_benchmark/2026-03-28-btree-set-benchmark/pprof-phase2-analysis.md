# BTree Set CPU Profile 分析 (Phase 2)

日期: 2026-03-28
测试条件: 1 线程, 50K ops, init=200, 纯内存模式

## 性能基线

| 指标 | 值 |
|------|-----|
| 吞吐量 | 22,724 ops/s |
| 延迟 | 44.01 μs/op |
| CPU 采样时间 | 2.20s |
| 总采样 | 3.17s |

## CPU 热点分布

### 按累计时间 (cum) 排序

| 排名 | 函数 | flat | cum | 占比 | 说明 |
|------|------|------|-----|------|------|
| 1 | `setWithLeafLock` | 0.32% | 44.16% | 44% | Set 主路径 |
| 2 | `handleSplitOffHeapSync` | 0% | 35.33% | 35% | 叶子分裂 |
| 3 | `SplitOffHeapLeafPage` | **5.99%** | **31.86%** | **32%** | 分裂核心逻辑 |
| 4 | `runtime.futex` | **18.93%** | 18.93% | 19% | 锁等待 |
| 5 | `runtime.mallocgc` | **5.68%** | 19.56% | 20% | 内存分配 |
| 6 | `gcBgMarkWorker` | 0% | 17.35% | 17% | GC 后台标记 |
| 7 | `runtime.mallocgcTiny` | **4.10%** | 8.52% | 9% | 小对象分配 |
| 8 | `errors.Wrapf` | 0% | 5.99% | 6% | 错误包装 |
| 9 | `errors.BTreeAllocLeftPage` | 0% | 3.79% | 4% | 分配错误构造 |

### 按自身时间 (flat) 排序

| 排名 | 函数 | flat | 说明 |
|------|------|------|------|
| 1 | `runtime.futex` | 18.93% | **锁等待/线程阻塞** |
| 2 | `SplitOffHeapLeafPage` | 5.99% | 分裂核心 |
| 3 | `runtime.mallocgc` | 5.68% | 堆内存分配 |
| 4 | `runtime.mallocgcTiny` | 4.10% | 微小对象分配 |
| 5 | `runtime.scanObjectsSmall` | 3.47% | GC 扫描 |
| 6 | `PageIDToPtr` | 3.15% | 页面地址转换 |
| 7 | `NewPageInfo` | 1.58% | PageInfo 对象分配 |
| 8 | `GetLeafEntry` | 1.58% | 叶子条目读取 |
| 9 | `cmpbody` | 1.89% | bytes.Compare |

## 瓶颈分类

### 1. 分裂开销: ~35% CPU

**`handleSplitOffHeapSync` + `SplitOffHeapLeafPage` 占 35% 累计 CPU**

`SplitOffHeapLeafPage` 内部热点:
- `mallocgc` + `mallocgcTiny`: ~13% — KV 拷贝产生大量临时 `[]byte`
- `GetLeafEntry` / `GetLeafEntryOffset`: ~2% — 遍历所有条目
- `makeslice`: ~3% — `make([]byte)` 创建临时 key/value

### 2. 锁竞争/调度: ~19% CPU

**`runtime.futex` 占 18.93% flat CPU**

- `notesleep` / `futexsleep`: 14% — 线程在 futex 上等待
- `stopm` / `schedule` / `park_m`: 线程调度开销
- 单线程下仍有 19% futex，说明分裂操作持锁时间长，导致其他后台 goroutine（GC、epoch 清理）被阻塞

### 3. GC 压力: ~17% CPU

**`gcBgMarkWorker` 占 17.35% 累计 CPU**

- GC 专用 worker: 11%
- GC idle worker: 6%
- 根因：分裂中 `make([]byte)` + `append` 产生大量短生命周期对象
- `mallocgc` + `mallocgcTiny` = 10% 分配开销 → 触发频繁 GC

### 4. 错误包装: ~6% CPU

**`errors.Wrapf` + `errors.BTreeAllocLeftPage` 占 6%**

- `SplitOffHeapLeafPage` 内部调用 `errors.BTreeAllocLeftPage(err)` 包装错误
- `fmt.Sprintf` 占 4.7% — 全部来自 `errors.Wrapf`
- `NexError.Error()` 占 2.5%
- 建议：热路径错误直接返回 sentinel error，不用 Wrapf

### 5. 页面管理: ~5% CPU

- `PageIDToPtr`: 3.15% — 页面 ID 到指针的映射
- `NewPageInfo`: 1.58% — 每次分裂/更新创建新 PageInfo
- `InitPage`: 0.95% — 新页面初始化（含 `memclrNoHeapPointers`）

## 优化建议（按优先级排序）

### P0: 分裂中的内存分配 (~30% 总开销)

**问题**: `SplitOffHeapLeafPage` 中遍历所有 KV 时用 `make([]byte)` + copy 创建临时 slice

**方案**:
1. 使用 `sync.Pool` 复用 key/value 临时 buffer
2. 分裂时直接在 offheap 内存操作，避免拷贝到 Go 堆
3. 预期收益: 减少 ~15% mallocgc + ~10% GC = **25% 总提升**

### P1: 错误包装开销 (~6%)

**问题**: 热路径用 `errors.Wrapf` + `fmt.Sprintf` 构造错误消息

**方案**:
1. 热路径改用 `errors.New()` 或 sentinel error
2. 只在真正需要上下文时用 Wrapf
3. 预期收益: **~6% 提升**

### P2: PageInfo 对象池 (~2%)

**问题**: `NewPageInfo` 每次分裂/更新都分配

**方案**: `sync.Pool` 复用 PageInfo 对象
预期收益: ~2%

### P3: 锁持有时间

**问题**: 单线程下仍有 19% futex 等待

**方案**:
1. 分裂操作不全程持锁 — 分裂后释放锁再更新父节点
2. 使用 tryLock 替代 lock，失败直接返回 ErrRetry
3. 预期收益: 高并发下显著，单线程收益有限

## 与 Phase 0.1 对比

| 阶段 | 主要变化 | 8T ops/s |
|------|---------|----------|
| Phase 0 | DebugPrintf → no-op | 19,703 |
| Phase 0.1 | offheap DebugPrintf 移除 | 51,746 (+162%) |
| Phase 2 (当前) | btree.go/leaf_lock_set.go 清理 | 117,472 (1T) |

当前 1T 基线: **117K ops/s (8.51μs)**，主要瓶颈从 DebugPrintf 转移到:
1. 分裂内存分配 (30%)
2. 锁/futex 等待 (19%)
3. GC 压力 (17%)
