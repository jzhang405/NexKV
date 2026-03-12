# Phase 0.5: PageReference 原型性能测试报告

> **测试日期**：2026-03-12
> **测试环境**：Go 1.24, Linux 6.17.0-14-generic
> **测试目标**：验证 `atomic.Pointer` 性能是否满足 <1μs 读延迟目标

---

## 1. 测试概述

### 1.1 测试目的

验证 PageReference 的间接寻址（`atomic.Pointer[PageInfo]`）是否会导致性能回退：

- **当前架构**：直接指针访问（`Node *Children[]`）
- **目标架构**：间接寻址（`PageReference → PageInfo → Page`）

### 1.2 测试范围

- ✅ **实现**：PageReference（仅 `atomic.Pointer[PageInfo]`）、PageInfo（简化版）、Page（测试用）
- ❌ **不实现**：LRU 缓存、脏页写入、Chunk Manager、PageLock、RootPageReference

### 1.3 成功标准

| 测试项 | 成功标准 | 失败触发 |
|--------|----------|----------|
| **读延迟** | <100ns | >500ns |
| **并发吞吐** | >8M ops/sec | <5M ops/sec |
| **Cache miss 率** | <8% | >15% |
| **Race detector** | 无警告 | 发现数据竞争 |
| **内存占用** | <300% | >400% |

---

## 2. 测试方法

### 2.1 运行命令

```bash
# 进入测试目录
cd internal/infrastructure/storage/btree/prototype

# 运行所有测试
./run.sh

# 单独运行基准测试
go test -bench=. -benchmem -cpuprofile=cpu.prof

# 单独运行并发测试
go test -race -v -run=Test_Concurrent

# 性能分析
go tool pprof -http=:8080 cpu.prof
```

### 2.2 测试场景

#### 场景 1：直接指针 vs 原子指针

```go
// 直接指针（基准）
ptr.page  // ~10ns/op

// 原子指针（测试目标）
ref.GetPage()  // 目标 <100ns/op
```

#### 场景 2：并发读取（1000 goroutines）

```go
const goroutines = 1000
const readsPerGoroutine = 10000
// 目标：吞吐 >8M ops/sec
```

#### 场景 3：混合读写（90% 读，10% 写）

```go
// 模拟真实场景
if rand.Intn(10) == 0 {
    // 写（CAS）
    ref.UpdatePage(newPage)
} else {
    // 读
    _ = ref.GetPage()
}
// 目标：吞吐 >5M ops/sec
```

---

## 3. 测试结果

### 3.1 基准测试结果

**运行日期**：待测试
**测试命令**：`go test -bench=. -benchmem`

| 测试项 | 延迟 (ns/op) | 内存 (B/op) | 分配次数 | 状态 |
|--------|--------------|-------------|----------|------|
| Benchmark_DirectPointer_Read | ~10 | 0 | 0 | ✓ 基准 |
| Benchmark_AtomicPointer_Read | **待测试** | 0 | 0 | **关键指标** |
| Benchmark_AtomicPointer_CAS | **待测试** | 0 | 0 | 待验证 |
| Benchmark_PageReference_ConcurrentRead | **待测试** | 0 | 0 | **关键指标** |
| Benchmark_PageReference_MixedReadWrite | **待测试** | 0 | 0 | **关键指标** |

**关键指标**：
- **Benchmark_AtomicPointer_Read**: 需 <100ns/op（成功），<50ns/op（优秀）
- **Benchmark_PageReference_ConcurrentRead**: 需 >8M ops/sec

### 3.2 并发测试结果

**运行日期**：待测试
**测试命令**：`go test -race -v -run=Test_Concurrent`

| 测试项 | 结果 | 状态 |
|--------|------|------|
| Test_ConcurrentRead_1000Goroutines | **待测试** | **关键指标** |
| Test_ConcurrentReadWrite_NoRace | **待测试** | 需通过 race detector |
| Test_CAS_Atomicity | **待测试** | 需验证原子性 |
| Test_PageReference_StressTest | **待测试** | 需稳定运行 5 秒 |

**关键指标**：
- **Test_ConcurrentRead_1000Goroutines**: 吞吐 >8M ops/sec
- **Test_ConcurrentReadWrite_NoRace**: 无 DATA RACE 警告

### 3.3 性能分析结果

**运行日期**：待测试
**分析工具**：pprof, perf

| 指标 | 目标 | 实际 | 状态 |
|------|------|------|------|
| Cache miss 率 | <8% | **待测试** | **关键指标** |
| CPU 使用率 | <80% | **待测试** | 待验证 |
| 内存占用 | <300% | **待测试** | 待验证 |

---

## 4. 性能分析

### 4.1 CPU Profile 分析

**生成方法**：
```bash
go test -bench=. -cpuprofile=cpu.prof
go tool pprof -text cpu.prof
```

**关键观察**：
- 热点函数：`runtime/internal/atomic.*Load*`
- CPU 占用分布：待分析
- 优化建议：待提供

### 4.2 火焰图分析

**生成方法**：
```bash
go tool pprof -png cpu.prof > cpu-flamegraph.png
```

**关键观察**：
- 调用栈深度：待分析
- 热点路径：待识别
- False sharing：待验证

### 4.3 Cache Miss 分析

**生成方法**：
```bash
perf stat -e cache-references,cache-misses,instructions,cycles go test -bench=. -benchmem
```

**关键观察**：
- Cache references：待统计
- Cache misses：待统计
- Cache miss 率：待计算
- False sharing 指标：待验证

---

## 5. 决策建议

### 5.1 决策矩阵

| 场景 | 条件 | 决策 |
|------|------|------|
| **✅ 通过** | 读延迟 <100ns 且 并发吞吐 >8M ops/sec | 进入 Phase 1 |
| **⚠️ 有条件通过** | 读延迟 100-500ns 或 并发吞吐 5-8M ops/sec | 进入 Phase 1 + 启用备选方案 A |
| **❌ 失败** | 读延迟 >500ns 或 并发吞吐 <5M ops/sec | 启用备选方案 B 或 C |

### 5.2 备选方案

#### 方案 A：混合架构（热数据直接指针，冷数据 PageReference）

```go
type PageReference struct {
    hotPage  *Page                      // 热数据：直接指针
    coldInfo atomic.Pointer[PageInfo]   // 冷数据：PageReference
}
```

**适用场景**：有条件通过（性能可接受但不够优秀）

#### 方案 B：使用 `sync.Mutex` + `unsafe.Pointer`（Go 1.18 兼容）

```go
type PageReference struct {
    mu   sync.Mutex
    pInfo unsafe.Pointer
}
```

**适用场景**：atomic.Pointer 性能严重不达标

#### 方案 C：降低性能目标

- 读延迟：<1μs → **<3μs**
- 并发吞吐：>10M ops/sec → **>3M ops/sec**
- 内存占用：200-300% → **150-200%**

**适用场景**：所有方案都无法满足，需要调整项目目标

---

## 6. 后续行动计划

### 6.1 Phase 0.5 完成后

1. **Day 1-2**：运行所有测试，收集基线数据
2. **Day 3-4**：分析性能瓶颈，优化关键路径
3. **Day 5**：运行并发测试，验证安全性
4. **Day 6-7**：汇总结果，做出决策

### 6.2 进入 Phase 1 的前置条件

- ✅ 原子指针读延迟 <100ns（或有条件通过）
- ✅ 并发吞吐 >8M ops/sec（或有条件通过）
- ✅ 无数据竞争（race detector 通过）
- ✅ 决策明确（继续 Phase 1 或启用备选方案）

### 6.3 需要优化的点（如果测试未通过）

- [ ] Cache Line 对齐优化
- [ ] 热数据/冷数据分离
- [ ] 减少原子操作频率
- [ ] 考虑使用 Mutex 方案

---

## 7. 附录

### 7.1 测试环境

```bash
$ go version
go version go1.24 linux/amd64

$ uname -a
Linux hostname 6.17.0-14-generic #1 SMP PREEMPT_DYNAMIC x86_64 GNU/Linux

$ lscpu | grep "Model name"
Model name:          Intel(R) Core(TM) i7-12700K

$ lscpu | grep "CPU(s)"
CPU(s):              16
Thread(s) per core:  2
Core(s) per socket:  8
```

### 7.2 相关文档

- [原型设计文档](../../../../thoughts/2026-03-12-btree-page-refactor-phase0.5-prototype.md)
- [PR 文档](../../../../docs/06_PM/feature/2026-03-12_PR-XXX_BTree-Page-重构-Phase1_全流程.md)
- [Phase 1-5 完整计划](../../../../thoughts/2026-03-12-btree-page-refactor-plan-v3.md)
- [Prototype 代码](../../../../internal/infrastructure/storage/btree/prototype/README.md)

### 7.3 测试数据归档

**原始数据**：`internal/infrastructure/storage/btree/prototype/results/`
- `benchmark.txt`: 基准测试原始输出
- `cpu.prof`: CPU profile
- `cpu-profile.txt`: CPU profile 文本报告
- `cpu-flamegraph.png`: 火焰图

**分析报告**：`docs/10_benchmark/phase0.5_page_reference_prototype/`
- 本文档（测试报告）
- `performance_analysis.md`: 性能分析详细报告（待创建）
- `decision_log.md`: 决策记录（待创建）

---

**报告生成时间**：2026-03-12
**下次更新**：测试完成后
