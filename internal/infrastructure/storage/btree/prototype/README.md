# Phase 0.5: PageReference 原型验证

> **目标**：验证 `atomic.Pointer` 性能是否满足 <1μs 读延迟目标
> **工期**：1 周（Week 1）
> **决策点**：通过则继续 Phase 1，失败则启用备选方案

## 📁 文件结构

```
prototype/
├── page.go               # Page 简化结构
├── page_info.go          # PageInfo 简化结构（无缓存逻辑）
├── page_reference.go     # PageReference 原型（atomic.Pointer）
├── benchmark_test.go     # 性能基准测试
├── concurrent_test.go    # 并发安全测试
├── README.md             # 本文档
└── run.sh                # 自动化测试脚本
```

## 🎯 核心问题

**关键疑问**：PageReference 的间接寻址（`atomic.Pointer[PageInfo]`）是否会导致性能回退？

- **当前架构**：直接指针访问（`Node *Children[]`）
- **目标架构**：间接寻址（`PageReference → PageInfo → Page`）
- **担忧**：每次读取需要通过原子指针加载，可能影响 L1 缓存命中率

## 📊 成功标准

| 测试项 | 成功标准 | 失败触发 |
|--------|----------|----------|
| **读延迟** | <100ns | >500ns |
| **并发吞吐** | >8M ops/sec | <5M ops/sec |
| **Cache miss 率** | <8% | >15% |
| **Race detector** | 无警告 | 发现数据竞争 |
| **内存占用** | <300% | >400% |

## 🚀 快速开始

### 1. 运行所有测试

```bash
cd internal/infrastructure/storage/btree/prototype
./run.sh
```

### 2. 单独运行基准测试

```bash
# 运行所有基准测试
go test -bench=. -benchmem -cpuprofile=cpu.prof

# 运行特定基准测试
go test -bench=Benchmark_AtomicPointer_Read -benchmem

# 生成 CPU profile
go test -bench=. -cpuprofile=cpu.prof
go tool pprof -http=:8080 cpu.prof
```

### 3. 单独运行并发测试

```bash
# 运行所有并发测试
go test -v -run=Test_Concurrent

# 启用 race detector
go test -race -v -run=Test_Concurrent
```

### 4. 性能分析

```bash
# 生成火焰图
go test -bench=. -cpuprofile=cpu.prof
go tool pprof -png cpu.prof > cpu-flamegraph.png

# Cache miss 分析（需要 perf）
perf stat -e cache-references,cache-misses,instructions,cycles go test -bench=. -benchmem
```

## 📈 基准测试说明

### Benchmark_DirectPointer_Read
直接指针读取（对比基准）
- **预期**：~10ns/op
- **说明**：1-2 条 CPU 指令，L1 缓存命中

### Benchmark_AtomicPointer_Read
原子指针读取
- **成功标准**：<100ns/op
- **优秀目标**：<50ns/op
- **说明**：3-5 条 CPU 指令 + L1 缓存

### Benchmark_AtomicPointer_CAS
CAS 操作（CompareAndSwap）
- **成功标准**：<200ns/op
- **说明**：原子操作，比普通读取慢

### Benchmark_PageReference_ConcurrentRead
并发读取（多 goroutine）
- **成功标准**：>8M ops/sec
- **说明**：验证高并发场景性能

### Benchmark_PageReference_MixedReadWrite
混合读写（90% 读，10% 写）
- **成功标准**：>5M ops/sec
- **说明**：模拟真实场景

## 🔧 并发测试说明

### Test_ConcurrentRead_1000Goroutines
1000 goroutines 并发读取
- **验证**：高并发性能 >8M ops/sec
- **验证**：无数据竞争

### Test_ConcurrentReadWrite_NoRace
并发读写（启用 race detector）
- **运行**：`go test -race -v`
- **验证**：无 DATA RACE 警告

### Test_CAS_Atomicity
CAS 原子性验证
- **验证**：只有一个 goroutine 能成功 CAS
- **说明**：确保原子操作正确性

### Test_PageReference_StressTest
压力测试（长时间运行）
- **运行**：`go test -v -timeout=10m`
- **说明**：5 秒钟持续并发访问

## 🎯 决策标准

### ✅ 通过条件（满足以下任一）

- 读延迟 <100ns（L1 缓存命中场景）
- 并发吞吐 >8M ops/sec
- Cache miss 率 <8%

**决策**：进入 Phase 1（完整实现）

### ⚠️ 有条件通过

性能介于成功和失败标准之间，例如：

- 读延迟 100-500ns
- 并发吞吐 5-8M ops/sec
- Cache miss 率 8-15%

**决策**：进入 Phase 1，但启用备选方案 A（混合架构）

### ❌ 失败条件（触发备选方案）

- 读延迟 >500ns（10x 性能回退）
- 并发吞吐 <5M ops/sec
- Cache miss 率 >15%
- 发现严重的 false sharing 问题

**决策**：启用备选方案 B（Mutex）或 C（降低目标）

## 🔄 备选方案

### 方案 A：混合架构（热数据直接指针，冷数据 PageReference）

```go
type PageReference struct {
    hotPage  *Page                      // 热数据：直接指针
    coldInfo atomic.Pointer[PageInfo]   // 冷数据：PageReference
}
```

**优势**：热数据路径无原子操作开销
**劣势**：增加复杂度（需要判断冷热）

### 方案 B：使用 `sync.Mutex` + `unsafe.Pointer`（Go 1.18 兼容）

```go
type PageReference struct {
    mu   sync.Mutex
    pInfo unsafe.Pointer
}
```

**优势**：简单直接，兼容旧版本
**劣势**：锁竞争可能更严重

### 方案 C：降低性能目标

- 读延迟：<1μs → **<3μs**
- 并发吞吐：>10M ops/sec → **>3M ops/sec**
- 内存占用：200-300% → **150-200%**

## 📝 下一步

1. **Day 1-2**：运行所有测试，收集基线数据
2. **Day 3-4**：分析性能瓶颈，优化关键路径
3. **Day 5**：运行并发测试，验证安全性
4. **Day 6-7**：汇总结果，做出决策

## 📚 相关文档

- [详细设计文档](../../../../../../thoughts/2026-03-12-btree-page-refactor-phase0.5-prototype.md)
- [PR 文档](../../../../../../docs/06_PM/feature/2026-03-12_PR-XXX_BTree-Page-重构-Phase1_全流程.md)
- [Phase 1-5 完整计划](../../../../../../thoughts/2026-03-12-btree-page-refactor-plan-v3.md)
