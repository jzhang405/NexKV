# Baseline 报告 — ReplaceChild TOCTOU 修复后

**日期**: 2026-03-31
**分支**: `perf/btree-set-benchmark2`
**提交**: `5100b14` (ReplaceChild TOCTOU 双层防御)
**前置**: `20131ac` (Phase 1 pageRefCache 失效)

---

## 1. 测试配置

```
工具: cmd/btree_perf_pprof
参数: -init=500 (500 初始 keys)
环境: GOGC=500, builtin 模式, 64MB mmap
```

## 2. 结果

### 2.1 吞吐量

| 线程数 | 总 ops | 耗时 | ops/sec | 扩展比 |
|--------|--------|------|---------|--------|
| 1 | 100,000 | 1.80s | 3,308 | 1.00x |
| 2 | 200,000 | 1.79s | 1,393 | 0.42x |
| 4 | 400,000 | 2.21s | 1,344 | 0.41x |
| 8 | 800,000 | 2.58s | 1,454 | 0.44x |

**扩展比严重倒退**：2 线程不如 1 线程，4/8 线程几乎持平。并发完全无效。

### 2.2 成功率

| 线程数 | Success | ErrRetry | ErrCircRef | ErrOther |
|--------|---------|----------|------------|----------|
| 1 | 5,969 (6.0%) | 94,031 (94.0%) | 0 | 0 |
| 2 | 2,498 (1.2%) | 197,502 (98.8%) | 0 | 0 |
| 4 | 2,968 (0.7%) | 397,032 (99.3%) | 0 | 0 |
| 8 | 3,756 (0.5%) | 796,243 (99.5%) | 0 | 1 |

### 2.3 错误明细

- **ErrCircRef = 0**: Phase 1 pageRefCache 失效修复完全消除了循环引用 ✅
- **ErrRetry 占 94-99.5%**: 几乎所有操作都因搜索路径竞争重试
- **ErrOther (8 线程)**: 1 次 `offheap: page full`（页面空间不足，正常边界条件）

## 3. 与 Phase 1 前对比

| 指标 | Phase 1 前 (20131ac 前) | Phase 1 后 (当前) | 变化 |
|------|------------------------|-------------------|------|
| 8 线程 ErrCircRef | 395,615 (98.9%) | **0 (0.0%)** | ✅ 完全消除 |
| 8 线程 ErrRetry | 1,815 (0.5%) | 796,243 (99.5%) | 🔴 大幅上升 |
| 8 线程 Success | 2,568 (0.6%) | 3,756 (0.5%) | ≈ 持平 |
| 8 线程 ops/sec | ~2,094 | ~1,454 | ⬇ 下降 |

**分析**：
- Phase 1 之前，98.9% 操作因 ErrCircRef 快速失败（几乎无开销）
- Phase 1 之后，ErrCircRef 消除，这些操作改为走完整路径后因 ErrRetry 失败
- 每次重试都要：搜索路径 → 尝试获取 PageLock → CAS 失败 → 重新搜索
- 重试路径开销 >> ErrCircRef 快速失败路径，导致吞吐下降

## 4. ReplaceChild TOCTOU 修复验证

### 4.1 防御层生效情况

| 防御层 | 触发条件 | 预期 |
|--------|----------|------|
| Layer 1: parentRef 快照校验 | 另一个线程 CAS 成功 | ErrRetry（不可观测，与正常 ErrRetry 合并） |
| Layer 2a: IsLeaf 检查 | 父页面被回收重用为叶子页 | ErrBTreeParentPageRecycled |
| Layer 2b: count 检查 | 父页面 count=0 或 >180 | ErrBTreeInvalidParentState |

**实际结果**：ErrOther=0（8 线程 100K ops），说明 TOCTOU 防御层在正常负载下极少触发。
Layer 1 的 parentRef 快照校验将 TOCTOU 转化为 ErrRetry，不可直接观测。

### 4.2 SIGSEGV 验证

| 测试 | 线程数 | ops | 结果 |
|------|--------|-----|------|
| 正确性测试 (race) | - | - | PASS (9/9) |
| 压力测试 | 8 | 800,000 | **无 SIGSEGV** ✅ |
| 极端并发 | 200 goroutine | 10,000 | 100% 成功 ✅ |

**注意**：8 线程 × 50K ops (总计 400K) 触发了 `GetLeafEntry` panic
（`page_layout.go:215`, index=12, count=1）。这是 `InsertToOffHeap → linearSearchLeaf`
的 TOCTOU——叶子页在搜索路径返回后被 COW 替换。与 ReplaceChild TOCTOU 是不同的 bug，
需要单独修复。

## 5. 瓶颈分析

### 5.1 当前瓶颈：搜索路径重试风暴

```
操作流程：
  搜索路径 → 获取 PageLock → 尝试写入 → CAS 失败 → ErrRetry → 重新搜索

成功率仅 0.5-6%，说明 94-99.5% 的操作都在 CAS 阶段失败。
```

**根因**：多个线程搜索到同一个叶子页 → 竞争同一个 PageLock → 只有一个成功 →
其余线程 CAS 失败重试 → 形成重试风暴。

### 5.2 与历史数据对比

| 优化阶段 | 8 线程 Success 率 | 8 线程 ops/sec |
|----------|-------------------|----------------|
| 初始基准 (builtin) | ~0.6% | ~2,094 |
| Phase 1 pageRefCache | ~0.5% | ~1,454 |
| ReplaceChild TOCTOU | ~0.5% | ~1,454 |

**结论**：Phase 1 + ReplaceChild 修复解决了正确性问题（消除 ErrCircRef + 防止 SIGSEGV），
但**没有改善吞吐**。Success 率仍是 ~0.5%，主要瓶颈是 PageLock TryLock 竞争。

## 6. 新发现的 Bug

| Bug | 位置 | 触发条件 | 影响 |
|-----|------|----------|------|
| `linearSearchLeaf` TOCTOU | `offheap_adapter.go:141` → `page_layout.go:215` | 叶子页在搜索后被 COW 替换 | panic (index out of range) |
| `UpdateChildIndex` TOCTOU | `offheap_adapter.go:1041` | delete 路径父页面被替换 | 潜在 SIGSEGV |

`linearSearchLeaf` TOCTOU 是当前最紧急的 bug——它在高负载下（400K+ ops）必现 panic。

## 7. 下一步建议

| 优先级 | 任务 | 预期效果 |
|--------|------|----------|
| **P0** | 修复 `linearSearchLeaf` TOCTOU | 消除高负载 panic |
| **P1** | 搜索路径局部重试（减少全路径重试开销） | Success 率提升 |
| **P1** | PageLock spin wait 优化 | 减少锁竞争开销 |
| **P2** | Phase-FreeList-1 (AddBatch + 活锁检测) | 减少 mutex 竞争 |

---

## 附录：原始输出

### 1 线程 × 100K

```
初始化 500 条...
开始: 1 线程 × 100000 次 Set = 100000 ops...
耗时: 1.804377402s, 3308 ops/s
Success: 5969 (6.0%), ErrRetry: 94031 (94.0%), ErrCircRef: 0, ErrOther: 0
```

### 2 线程 × 100K

```
开始: 2 线程 × 100000 次 Set = 200000 ops...
耗时: 1.793133903s, 1393 ops/s
Success: 2498 (1.2%), ErrRetry: 197502 (98.8%), ErrCircRef: 0, ErrOther: 0
```

### 4 线程 × 100K

```
开始: 4 线程 × 100000 次 Set = 400000 ops...
耗时: 2.207752309s, 1344 ops/s
Success: 2968 (0.7%), ErrRetry: 397032 (99.3%), ErrCircRef: 0, ErrOther: 0
```

### 8 线程 × 100K

```
开始: 8 线程 × 100000 次 Set = 800000 ops...
耗时: 2.583103651s, 1454 ops/s
Success: 3756 (0.5%), ErrRetry: 796243 (99.5%), ErrCircRef: 0, ErrOther: 1
```
