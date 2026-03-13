# NexKV BTree 性能基准测试

**测试日期**: 2026-03-13
**代码版本**: P0/P1 重构后（常量提取 + 序列化统一 + Bug 修复）
**测试环境**: Intel(R) Core(TM) i7-8700 CPU @ 3.20GHz, 12 cores

---

## 📂 文件说明

```
docs/10_benchmark/
├── README.md                    # 本文件 - 性能测试概述
├── analysis_20260313.md         # 详细性能分析报告
├── raw_data_20260313.txt        # 原始基准测试数据
└── trends.md                    # 性能趋势分析（待添加）
```

---

## 🎯 核心性能指标

### 读操作（优秀 ✅）

| 测试场景 | 吞吐量 | 延迟 | 内存 | 分配次数 |
|---------|--------|------|------|---------|
| 单线程 Get | 8.35M ops/s | **134.7 ns/op** | 24 B/op | 1 allocs/op |
| 并发 Get | 37.46M ops/s | **34.70 ns/op** | 24 B/op | 1 allocs/op |

**亮点**：
- 🚀 并发读取 **3.9x 加速**（得益于缓存局部性）
- 💾 内存效率高：每次操作仅 24 字节
- ♻️ GC 友好：每次操作仅 1 次分配

### 写操作（可优化 ⚠️）

| 测试场景 | 吞吐量 | 延迟 | 内存 | 分配次数 |
|---------|--------|------|------|---------|
| 单线程 Set | 2,361 ops/s | **465.1 μs/op** | 607.4 KB/op | 694 allocs/op |
| 并发 Set | 2,084 ops/s | **571.5 μs/op** | 599.1 KB/op | 688 allocs/op |
| 单线程 Delete | 1,539 ops/s | **842.3 μs/op** | 1.04 MB/op | 1242 allocs/op |

**分析**：
- ⚠️ Set/Delete 较慢（微秒级），主要因为：
  - CCOW（Copy-on-Write）需要复制路径上所有节点
  - CAS 更新可能有重试开销
  - Split/Merge 操作开销
- ⚠️ 内存分配多（约 600KB-1MB/操作）
- 🔍 并发 Set 稍慢于单线程（CAS 竞争）

---

## 📊 详细数据

### 页面操作性能

| 操作 | 延迟 | 内存 | 分配次数 |
|------|------|------|---------|
| InternalPage Split | **2.546 μs/op** | 2.08 KB/op | 40 allocs/op |
| splitLeaf | **2.424 ms/op** | 2.68 MB/op | 2140 allocs/op |
| SearchPath | **120.6 ns/op** | 24 B/op | 1 allocs/op |

**关键发现**：
- ✅ InternalPage Split 非常快（微秒级）
- ⚠️ splitLeaf 较慢（毫秒级），因为递归向上分裂

### 并发工作负载

| 操作 | 吞吐量 | 延迟 | 内存 |
|------|--------|------|------|
| MixedWorkload | 20,858 ops/s | 57.5 μs/op | 60.4 KB/op |
| ConcurrentReaders | 14,454 ops/s | 88.6 μs/op | 30.4 KB/op |
| ConcurrentWriters | **21 ops/s** | **55.6 ms/op** | 60.4 MB/op |

**问题**：
- 🔴 并发写扩展性差：ConcurrentWriters 性能严重下降
- 可能原因：CAS 竞争导致大量重试

---

## 🐛 已修复问题

### P0: Delete 崩溃 ✅

**问题**：
```
panic: runtime error: index out of range [8] with length 8
at redistributeInternalLeft() line 1658
```

**修复**：
- 添加防御性检查验证 B+ 树不变性
- 添加边界检查防止访问无效索引
- 影响：`redistributeInternalLeft` 和 `redistributeInternalRight`

**结果**：
- ✅ BenchmarkBTree_Delete 通过
- ✅ 所有测试用例通过

---

## 📈 性能优化建议

### 高优先级 🔴

1. **优化 Set/Delete 内存分配**
   - 当前：~600KB-1MB/操作
   - 建议：使用 `sync.Pool` 对象池
   - 预期：减少 50%+ 内存分配

2. **改善并发写性能**
   - 当前：ConcurrentWriters 仅 21 ops/s
   - 建议：实现批量 CAS 或分段锁
   - 预期：10x 并发写性能提升

3. **优化 splitLeaf 性能**
   - 当前：2.4 ms/op
   - 建议：减少递归深度，延迟分裂
   - 预期：50% 性能提升

### 中优先级 🟡

4. **实现真正的懒惰分裂**
   - 提前检测并预防满节点
   - 减少运行时分裂开销

5. **优化 Merge 策略**
   - 当前 Delete 比 Set 慢 2x
   - 建议：延迟合并，批量处理
   - 预期：30% 性能提升

---

## 🔍 如何运行测试

### 运行所有基准测试
```bash
go test -bench=. -benchmem -run=^$ ./internal/infrastructure/storage/btree/
```

### 运行特定测试
```bash
# 只测试 Get 操作
go test -bench=BenchmarkBTree_Get -benchmem -run=^$ ./internal/infrastructure/storage/btree/

# 只测试 Set 操作
go test -bench=BenchmarkBTree_Set -benchmem -run=^$ ./internal/infrastructure/storage/btree/

# 只测试 Delete 操作
go test -bench=BenchmarkBTree_Delete -benchmem -run=^$ ./internal/infrastructure/storage/btree/
```

### 生成 CPU 性能分析
```bash
go test -bench=. -cpuprofile=cpu.prof -memprofile=mem.prof ./internal/infrastructure/storage/btree/
go tool pprof cpu.prof
```

---

## 📝 更新日志

### 2026-03-13
- ✅ 初始性能基准测试
- ✅ 修复 Delete 操作崩溃问题
- ✅ P0/P1 代码重构（常量提取 + 序列化统一）
- ✅ 生成性能分析报告

---

## 🔗 相关文档

- [性能分析详细报告](./analysis_20260313.md)
- [原始测试数据](./raw_data_20260313.txt)
- [P0/P1 重构总结](../09_development/phases.md)

---

**维护者**: NexKV 开发团队
**最后更新**: 2026-03-13
