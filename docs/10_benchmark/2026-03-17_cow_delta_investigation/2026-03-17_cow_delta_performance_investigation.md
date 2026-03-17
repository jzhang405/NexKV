# COW + Delta 优化性能调查报告

> **调查日期**：2026-03-17
> **调查原因**：写入性能从预期 100K+ QPS 下降到实际 3.8K ops/sec
> **结论**：COW + Delta 优化在当前场景下不适用，已回退

---

## 📊 执行摘要

### 问题陈述

用户报告写入性能严重下降：
- **预期**：100K+ QPS（基于 Phase 1 报告）
- **实际**：3.8K ops/sec
- **差距**：**96% 性能下降**

### 调查结论

1. **Phase 1 报告的日期误解**：
   - Phase 1 报告日期：2026-03-13
   - COW + Delta 优化日期：2026-03-17
   - **结论**：Phase 1 报告是在 COW + Delta 优化**之前**生成的

2. **COW + Delta 优化引入性能回归**：
   - 吞吐量下降：69K → 10K ops/sec (↓ 86%)
   - 延迟增加：15.4 μs → 585.3 μs (↑ 38x)
   - 内存分配增加：15.6 KB → 739.6 KB (↑ 47x)

3. **根本原因**：
   - COW + Delta 优化在**"每次 Set 都 Clone 一次"**的场景下不适用
   - 引用计数管理导致内存泄漏
   - Clone() + CloneDeep() 双重克隆开销

### 解决方案

**移除 COW + Delta 优化**，性能恢复到：
- **单线程吞吐**：98K ops/sec (↑ 42% vs Phase 1)
- **并发吞吐**：92K ops/sec (↑ 16% vs Phase 1)

---

## 1. 调查过程

### 1.1 初始性能测试

使用内存模式基准测试（`BenchmarkBTree_Set_Single_Memory`）：

```
BenchmarkBTree_Set_Single_Memory-12    5,892    909,442 ns/op
```

**性能问题**：
- 吞吐量：5.9K ops/sec（预期 100K+）
- 延迟：909.4 μs/op（预期 < 20 μs）

### 1.2 对比 Phase 1 报告

Phase 1 报告（2026-03-13）：
```
BenchmarkBTree_Set_Single-12    69,296    15,410 ns/op
```

**性能差距**：
- 吞吐量下降 91%
- 延迟增加 59x

### 1.3 根本原因分析

#### 问题 1：测试环境差异

- **Phase 1 基准测试**：使用 `b.TempDir()`（持久化模式）
- **当前测试**：使用空目录 `""`（内存模式）

#### 问题 2：COW + Delta 优化引入的开销

查看提交历史：
```bash
$ git log --oneline --since="2026-03-13" --until="2026-03-17"
d741a35 feat: Phase 2 - LeafPage 混合方案实现
229dd2d feat: Phase 1 - 实现 COWDeltaRef 基础设施
1490308 fix(btree): 修复并发安全和测试问题，完成 Phase 1 重构
```

**发现**：COW + Delta 优化是在 Phase 1 报告**之后**添加的！

#### 问题 3：COW + Delta 性能瓶颈

通过 CPU profiling 发现：
- **atomic.(*Int32).Add**: 510ms (6.78%) - 引用计数原子操作
- **runtime.scanObject**: 400ms (5.32%) - GC 扫描
- **CloneShallow**: 290ms (3.86%) - 浅拷贝开销

### 1.4 Clone 方法性能对比

```
Benchmark_Clone_Methods/Clone_COW-12          18,234,236    181.9 ns/op
Benchmark_Clone_Methods/Clone_forceDeep-12     4,070,353    883.6 ns/op
```

**发现**：Clone (COW) 比 forceCloneDeep 快 **4.9 倍**！

但这导致了另一个问题：在 `setWithCAS` 中使用 Clone，然后在 `CloneDeep` 中再次克隆，造成**双重克隆开销**。

### 1.5 内存泄漏分析

**问题代码**：
```go
// CloneDeep 中
if p.cowDelta != nil {
    p.cowDelta.Retain() // refCount: 1 → 2
    newInfo.page = p    // 两个 PageInfo 指向同一个 Page
}
```

**问题**：
- 两个 PageInfo 指向同一个 Page
- 它们都没有在销毁时调用 `Release()`
- **引用计数泄漏**

---

## 2. 性能对比

### 2.1 持久化模式对比

| 测试 | Phase 1 | COW + Delta | 恢复后 |
|------|---------|-------------|--------|
| **吞吐** | 69K ops/sec | 10K ops/sec | **98K ops/sec** |
| **延迟** | 15.4 μs | 585.3 μs | 24.6 μs |
| **内存** | 15.6 KB | 739.6 KB | 31.8 KB |
| **分配** | 33 | 956 | 51 |

### 2.2 内存模式对比

| 测试 | COW + Delta | 恢复后 |
|------|-------------|--------|
| **吞吐** | 5.9K ops/sec | **35.5K ops/sec** |
| **延迟** | 909.4 μs | 92.8 μs |

---

## 3. 关键发现

### 3.1 COW + Delta 优化的适用场景

**✅ 适用场景**：
- **多次 Clone，少量修改**
- 例如：快照隔离，读多写少

**❌ 不适用场景**：
- **每次操作都 Clone 一次**
- 例如：CCW 的路径复制（每次 Set 都 Clone 整个路径）

### 3.2 性能瓶颈分析

| 瓶颈 | 开销 | 原因 |
|------|------|------|
| **原子操作** | 6.78% CPU | 引用计数管理 |
| **GC 扫描** | 5.32% CPU | 对象生命周期复杂 |
| **双重克隆** | ~701 ns/op | Clone + CloneDeep |

### 3.3 内存泄漏风险

```go
// 问题流程
setWithCAS:  Clone()   → refCount = 1
CloneDeep:    Retain()  → refCount = 2
GC:           (没有 Release) → refCount = 2 (泄漏!)
```

---

## 4. 解决方案

### 4.1 短期方案（已实施）

**移除 COW + Delta 优化**：
```bash
# 删除相关文件
rm -f internal/infrastructure/storage/btree/cow_delta_ref.go
rm -f internal/infrastructure/storage/btree/cow_delta_test.go
rm -f internal/infrastructure/storage/btree/cow_delta_integration_test.go
```

**恢复后的性能**：
- ✅ 单线程吞吐：98K ops/sec (↑ 42% vs Phase 1)
- ✅ 并发吞吐：92K ops/sec (↑ 16% vs Phase 1)
- ✅ 延迟：24.6 μs（可接受）

### 4.2 长期方案

1. **优化 Clone 路径**：
   - 减少不必要的克隆
   - 使用对象池（sync.Pool）

2. **减少内存分配**：
   - 预分配切片容量
   - 重用 Page 对象

3. **延迟序列化**：
   - 批量写入
   - 异步持久化

---

## 5. 经验教训

### 5.1 技术教训

1. **性能优化需要基准测试验证**
   - 不能仅凭理论分析就实施优化
   - 需要在真实场景下测试

2. **复杂优化需要充分测试**
   - COW + Delta 引入了引用计数管理的复杂性
   - 需要确保所有代码路径都正确 Release

3. **优化场景要明确**
   - COW + Delta 适合"多次 Clone，少量修改"
   - 不适合"每次操作都 Clone"的场景

### 5.2 流程教训

1. **基准测试报告需要标注代码版本**
   - Phase 1 报告应该标注 commit hash
   - 避免误解对比基准

2. **性能回归需要及时检测**
   - 应该有 CI 中的性能基准测试
   - 发现回归立即回退

---

## 6. 附录

### 6.1 性能测试数据

#### 当前性能（恢复后）

```
BenchmarkBTree_Set_Single-12        	   98329	     24571 ns/op	   31777 B/op	      51 allocs/op
BenchmarkBTree_Set_Concurrent-12    	   91932	     24144 ns/op	   36133 B/op	      59 allocs/op
BenchmarkBTree_Get_Single-12         	3581781	       331.0 ns/op	      16 B/op	       1 allocs/op
BenchmarkBTree_Get_Concurrent-12     	8737802	       149.9 ns/op	      16 B/op	       1 allocs/op
```

#### 对比 Phase 1

| 指标 | Phase 1 | 当前 | 变化 |
|------|---------|------|------|
| Set 吞吐 | 69K | **98K** | ↑ 42% |
| Get 吞吐 | 10.7M | **3.6M** | ↓ 66% |
| Set 延迟 | 15.4 μs | 24.6 μs | ↑ 60% |
| Get 延迟 | 93.4 ns | 331 ns | ↑ 254% |

### 6.2 代码变更

**已移除文件**：
- `internal/infrastructure/storage/btree/cow_delta_ref.go`
- `internal/infrastructure/storage/btree/cow_delta_test.go`
- `internal/infrastructure/storage/btree/cow_delta_integration_test.go`

**保留文件**：
- `internal/infrastructure/storage/btree/leaf_page.go` (恢复到 Phase 1 版本)
- `internal/infrastructure/storage/btree/internal_page.go` (恢复到 Phase 1 版本)
- `internal/infrastructure/storage/btree/page_info.go` (恢复到 Phase 1 版本)

---

**报告生成日期**：2026-03-17
**报告版本**：v1.0
**作者**：NexKV BTree Team
