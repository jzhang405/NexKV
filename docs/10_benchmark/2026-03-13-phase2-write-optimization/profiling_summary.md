# Phase 2B Profiling 分析摘要

## 关键发现

### 🎯 性能瓶颈 Top 3

1. **深拷贝开销** - 73.5% CPU 时间
   - CloneDeep: 32.6%
   - Leaf.Insert: 40.9%

2. **CAS 失败重试** - 47.5% CPU 浪费
   - 87.5% 失败率
   - 8 goroutines 竞争 1 个 root

3. **GC 压力** - 50% CPU 时间
   - 112 allocs/op
   - 频繁内存分配

### 📊 当前性能

| 场景 | QPS | vs 目标 |
|------|-----|---------|
| Concurrent_8 | 100K | -83% ❌ |
| Serial | 690K | +15% ✅ |

### 🚀 推荐优化（P0）

**限制并发 Writer + 指数退避**

- 代码改动：~30 行
- 预期提升：+80-130%
- 目标达成：180-230K QPS

### 🔥 根本原因

不是锁的问题（已无锁化），而是：
- COW 深拷贝本质问题
- CAS 竞争（8 竞 1）
- 内存分配过多

---

**完整报告**: docs/10_benchmark/2026-03-13-phase2-write-optimization/profiling_analysis.md
