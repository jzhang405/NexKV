# BTree Set CPU Profile 分析 (Phase 3 — 错误优化后)

日期: 2026-03-28
测试条件: 1 线程, 50K ops, init=200, 纯内存模式

## 性能基线

| 指标 | 值 |
|------|-----|
| 吞吐量 | 23,998 ops/s |
| 延迟 | 41.67 μs/op |
| CPU 采样时间 | 2.08s |
| 总采样 | 3.08s (147.82%) |

## 优化效果

| 指标 | Phase 2 (优化前) | Phase 3 (优化后) | 变化 |
|------|-----------------|-----------------|------|
| errors.Wrapf/Join CPU | ~6% | **0%** | **完全消除** |
| 1T 吞吐量 | ~22.7K ops/s | ~27.5K ops/s | **+21%** |
| 延迟 | ~44 μs | ~36 μs | **-18%** |

## CPU 热点分布

### 按累计时间 (cum) 排序

| 排名 | 函数 | flat | cum | 占比 | 说明 |
|------|------|------|-----|------|------|
| 1 | `main` → `Set` → `setWithLeafLock` | 0% | 42.5% | Set 主路径 |
| 2 | `handleSplitOffHeapSync` | 0% | 28.6% | 叶子分裂 |
| 3 | `SplitOffHeapLeafPage` | **2.6%** | **26.3%** | 分裂核心 |
| 4 | `runtime.futex` | **17.5%** | 17.5% | 锁等待/调度 |
| 5 | `runtime.mallocgc` | 3.6% | 17.9% | 堆分配 |
| 6 | `runtime.mallocgcTiny` | 4.2% | 26.0% | 小对象分配 |
| 7 | `gcBgMarkWorker` | 0% | 20.1% | GC 后台标记 |

### 按自身时间 (flat) 排序

| 排名 | 函数 | flat | 说明 |
|------|------|------|------|
| 1 | `runtime.futex` | **17.5%** | 锁等待/线程阻塞 |
| 2 | `runtime.mallocgcTiny` | 4.2% | 微小对象分配 |
| 3 | `PageIDToPtr` | 3.6% | 页面地址转换 |
| 4 | `runtime.mallocgc` | 3.6% | 堆内存分配 |
| 5 | `SplitOffHeapLeafPage` | 2.6% | 分裂核心 |
| 6 | `runtime.memmove` | 2.3% | 内存拷贝 |
| 7 | `bytes.Compare` | 0.7% | 键比较 |

## 瓶颈分类

### 1. 锁竞争/调度: ~17.5% CPU

**`runtime.futex` 占 17.5% flat CPU**

单线程下仍有 17.5% futex，说明分裂操作持锁时间长，其他后台 goroutine（GC、epoch 清理）被阻塞。

### 2. 内存分配 + GC: ~24% CPU

**`mallocgc` + `mallocgcTiny` + `gcBgMarkWorker` = ~24%**

- mallocgc: 3.6% — 堆分配
- mallocgcTiny: 4.2% — 小对象（<16B）分配
- gcBgMarkWorker: 20.1% — GC 后台标记

根因：分裂中 `make([]byte)` + `append` 产生大量短生命周期对象，触发频繁 GC

### 3. 分裂开销: ~26% CPU (cum)

**`handleSplitOffHeapSync` + `SplitOffHeapLeafPage` 占 26.3% 皴计**

`SplitOffHeapLeafPage` 内部热点:
- `mallocgc` + `mallocgcTiny`: ~13% — KV 拷贝产生临时 `[]byte`
- `PageIDToPtr`: 3.6% — 页面地址映射
- `GetLeafEntryOffset` + `GetKey`: ~2% — 遍历叶子条目

### 4. 错误构造: 0% (已消除)

**`errors.Wrapf` 已从 CPU profile 中完全消失**（Phase 2 时占 ~6%）。

## 与 Phase 2 对比

| 阶段 | 主要变化 | 1T ops/s |
|------|---------|----------|
| Phase 0 | DebugPrintf → no-op | 19,703 |
| Phase 0.1 | offheap DebugPrintf 移除 | 51,746 |
| Phase 2 | Wrapf → stderrors.Join | ~25,000 |
| **Phase 3** | **stderrors.Join → 直接返回** | **~27,500** |

## 下一步优化建议

### P0: 分裂中的内存分配 (~24% 总开销)

**问题**: `SplitOffHeapLeafPage` 中 `make([]byte)` + copy 创建临时 slice

**方案**:
1. 使用 `sync.Pool` 复用 key/value 临时 buffer
2. 分裂时直接在 offheap 内存操作，避免拷贝到 Go 堆
3. 预期收益: 减少 ~15% mallocgc + ~10% GC = **25% 总提升**

### P1: 锁持有时间

**问题**: 单线程下 17.5% futex 皆待

**方案**:
1. 分裂操作不全程持锁 — 分裂后释放锁再更新父节点
2. 使用 tryLock 替代 lock，失败直接返回 ErrRetry
3. 预期收益: 高并发下显著，单线程收益有限
