# BTree Set CPU Profile 分析 (Phase 4 — PageIDToPtr 内联优化后)

日期: 2026-03-28
测试条件: 1 线程, 50K ops, init=200, 纯内存模式

## 性能基线

| 指标 | 值 |
|------|-----|
| 吞吐量 | 25,399 ops/s |
| 延迟 | 39.37 μs/op |
| CPU 采样时间 | 1.97s |
| 总采样 | 2.94s (149.34%) |

## 优化效果

| 指标 | Phase 3 (优化前) | Phase 4 (优化后) | 变化 |
|------|-----------------|-----------------|------|
| PageIDToPtr flat CPU | 3.6% | **1.36%** | **-62%** |
| 1T 吞吐量 | ~27.5K ops/s | ~29.5K ops/s | **+7.3%** |
| 延迟 | ~36 μs | ~33.5 μs | **-7%** |

## 改动内容

1. **P0**: 移除 `PageIDToPtr` 中的 `fmt.Sprintf`，inline cost 87→57，编译器自动内联
2. **P1**: 给 `pageIDToPtrUnchecked` 添加 `//go:inline` 强制内联
3. **P2**: 移除 `getPtr()` 的 `lastID`/`lastPtr` 缓存（修复 pageID=0 返回 nil 的 bug + 并发竞态）
4. **P3**: SearchKey 循环内联指针计算（已测试，收益不显著，**未提交**）

## CPU 热点分布

### 按累计时间 (cum) 排序

| 排名 | 函数 | flat | cum | 占比 | 说明 |
|------|------|------|-----|------|------|
| 1 | `Set` → `SetWithRetryAndQueue` | 0% | 34.0% | 写入主路径 |
| 2 | `setWithLeafLock` | 0% | 31.0% | 叶子级锁定写入 |
| 3 | `handleSplitOffHeapSync` | 0% | 23.8% | 叶子分裂 |
| 4 | `gcBgMarkWorker` | 0% | 23.5% | GC 后台标记 |
| 5 | `SplitOffHeapLeafPage` | **3.1%** | **20.4%** | 分裂核心 |
| 6 | `mallocgc` + `mallocgcTiny` | 6.1% | 15.9% | 堆分配 |

### 按自身时间 (flat) 排序

| 排名 | 函数 | flat | 说明 |
|------|------|------|------|
| 1 | `runtime.futex` | **20.75%** | 锁等待/线程阻塞 |
| 2 | `runtime.mallocgcTiny` | 4.1% | 微小对象分配 |
| 3 | `runtime.stealWork` | 4.1% | GC 工作窃取 |
| 4 | `runtime.nextFreeFast` | 3.7% | 空闲内存查找 |
| 5 | `runtime.tryDeferToSpanScan` | 3.4% | GC 扫描 |
| 6 | `SplitOffHeapLeafPage` | **3.1%** | 分裂核心 |
| 7 | `runtime.mallocgc` | 2.0% | 堆内存分配 |
| 8 | `runtime.gcDrain` | 2.0% | GC 标记排水 |
| 9 | `pageIDToPtrUnchecked` | **1.4%** | 页面地址转换 (已内联) |
| 10 | `runtime.memmove` | 1.7% | 内存拷贝 |

## 瓶颈分类

### 1. 锁竞争/调度: ~20.75% CPU

**`runtime.futex` 占 20.75% flat CPU**

单线程下仍有 20.75% futex，较 Phase 3 (17.5%) 略有上升。分裂操作持锁时间长，其他后台 goroutine（GC、epoch 清理）被阻塞。

### 2. 内存分配 + GC: ~25% CPU

**`mallocgc` + `mallocgcTiny` + `gcBgMarkWorker` + `gcDrain` = ~25%**

- mallocgc: 2.0% — 堆分配
- mallocgcTiny: 4.1% — 小对象（<16B）分配
- stealWork: 4.1% — GC 工作窃取
- gcBgMarkWorker: 23.5% (cum) — GC 后台标记
- gcDrain: 2.0% — GC 标记排水

根因：分裂中 `make([]byte)` + `copy` 产生大量短生命周期对象，触发频繁 GC

### 3. 分裂开销: ~20% CPU (cum)

**`handleSplitOffHeapSync` + `SplitOffHeapLeafPage` 占 23.8% 累计**

`SplitOffHeapLeafPage` 内部热点:
- `mallocgc` + `mallocgcTiny`: ~6% — KV 拷贝产生临时 `[]byte`
- `pageIDToPtrUnchecked`: 1.4% — 页面地址映射（已优化）
- `memmove`: 1.7% — 内存拷贝

### 4. PageIDToPtr: 1.4% (Phase 3: 3.6% → Phase 4: 1.4%)

**成功从 3.6% 降至 1.4%，减少 62%**。归因于：
- `fmt.Sprintf` 移除使编译器自动内联
- `//go:inline` 强制内联
- 移除 `getPtr()` 缓存消除间接调用层

剩余 1.4% 是不可避免的指针算术开销（`unsafe.Add` + 乘法）。

## 与 Phase 3 对比

| 阶段 | 主要变化 | 1T ops/s | PageIDToPtr |
|------|---------|----------|-------------|
| Phase 0 | DebugPrintf → no-op | 19,703 | — |
| Phase 0.1 | offheap DebugPrintf 移除 | 51,746 | — |
| Phase 2 | Wrapf → stderrors.Join | ~25,000 | — |
| Phase 3 | stderrors.Join → 直接返回 | ~27,500 | 3.6% |
| **Phase 4** | **PageIDToPtr 内联+缓存修复** | **~29,500** | **1.4%** |

## 下一步优化建议

### P0: 分裂中的内存分配 (~25% 总开销)

**问题**: `SplitOffHeapLeafPage` 中 `make([]byte)` + copy 创建临时 slice

**方案**:
1. 使用 `sync.Pool` 复用 key/value 临时 buffer
2. 分裂时直接在 offheap 内存操作，避免拷贝到 Go 堆
3. 预期收益: 减少 ~15% mallocgc + ~10% GC = **25% 总提升**

### P1: 锁持有时间 (~20% futex)

**问题**: 单线程下 20.75% futex 等待

**方案**:
1. 分裂操作不全程持锁 — 分裂后释放锁再更新父节点
2. 使用 tryLock 替代 lock，失败直接返回 ErrRetry
3. 预期收益: 高并发下显著，单线程收益有限
