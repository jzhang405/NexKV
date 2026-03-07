# 【PR全流程文档】Feature - BoltDB vs BfTree 性能基准测试对比

> **文档说明**：本文档记录 BoltDB 与 BfTree 的性能基准测试对比全流程
>
> **关联PR**: PR-090
> **分支名称**: feature/benchmark-boltdb-vs-bftree
>
> **文档版本**: V1.0

---

## 第一部分：前置部分

### 1. 基础信息

| 项目 | 内容 |
|------|------|
| 工作类型 | 性能测试（Benchmark） |
| PR编号 | PR-090 |
| 分支名称 | feature/benchmark-boltdb-vs-bftree |
| 工作主题 | BoltDB vs BfTree 性能基准测试对比 |
| 负责人 | AI + 人工评审 |
| 分支创建日期 | 2026-03-07 |
| 计划完成日期 | 2026-03-07（1天） |

### 2. 背景与目标

#### 2.1 背景

- **BfTree 是 NexKV 新的存储引擎**：基于 B+ 树变体，引入 Delta Chain、Mini-Page、BitmapLock 等优化
- **需要验证性能提升**：对比 BoltDB（标准 B+ 树实现），验证 BfTree 的性能优势
- **支持决策**：为生产环境选型提供数据支持

#### 2.2 测试目标

**1. 功能验证**：
- ✅ 基本读写操作正确性
- ✅ 并发安全性
- ✅ 数据持久化

**2. 性能指标**：
| 操作 | BoltDB（基线） | BfTree P0（RWMutex） | BfTree P1（BitmapLock） | 目标 |
|------|---------------|---------------------|----------------------|------|
| **点查询 Get** | ~100-200 ns | ~100-150 ns | ~100-150 ns | ≈ BoltDB |
| **写入 Set** | ~500-1000 ns | ~200-300 ns | ~200-300 ns | **优于 BoltDB** |
| **删除 Delete** | ~200-500 ns | ~150-250 ns | ~150-250 ns | ≈ BoltDB |
| **范围查询 Scan** | ~10-20 μs | ~8-15 μs | ~8-15 μs | ≈ BoltDB |
| **并发读** | 基线 | 基线 | **+50%~100%** | **优于 BoltDB** |
| **并发写** | 基线 | 基线 | +20%~50% | 优于 BoltDB |

**3. 资源消耗**：
- 内存占用对比
- CPU 使用率对比
- 磁盘空间对比（Delta Chain 优化）

### 3. 测试方案

#### 3.1 测试环境

```yaml
硬件:
  CPU: Intel(R) Core(TM) i7-8700 @ 3.20GHz
  内存: 16GB
  磁盘: SSD

软件:
  Go版本: 1.24
  操作系统: Linux
  BoltDB版本: go.etcd.io/bbolt@latest
  BfTree版本: 当前 main 分支
```

#### 3.2 测试场景

**场景 1: 顺序写入**
- 数据量: 10,000 条
- Key 大小: 16 字节
- Value 大小: 100 字节
- 预期: BfTree 优于 BoltDB（Delta Chain 减少写入放大）

**场景 2: 随机写入**
- 数据量: 10,000 条
- Key 大小: 16 字节
- Value 大小: 100 字节
- 预期: BfTree ≈ BoltDB

**场景 3: 点查询**
- 数据量: 10,000 条
- 查询次数: 10,000 次
- 预期: BfTree ≈ BoltDB

**场景 4: 范围查询**
- 数据量: 10,000 条
- 查询范围: 100 条
- 预期: BfTree ≈ BoltDB

**场景 5: 并发读**
- Goroutines: 10
- 每个读 1,000 次
- 预期: BfTree P1 (BitmapLock) > BfTree P0 > BoltDB

**场景 6: 并发写**
- Goroutines: 10
- 每个写 1,000 次
- 预期: BfTree P1 > BfTree P0 ≈ BoltDB

**场景 7: 混合读写**
- 读比例: 70%
- 写比例: 30%
- Goroutines: 10
- 预期: BfTree P1 > BfTree P0 > BoltDB

**场景 8: 大数据量测试**
- 数据量: 100,000 条
- 操作: 写入 + 查询
- 预期: BfTree 内存占用 < BoltDB（Mini-Page 优化）

#### 3.3 测试指标

**性能指标**：
- 操作延迟（P50, P95, P99）
- 吞吐量（ops/sec）
- 分配次数（allocs/op）
- 分配大小（B/op）

**资源指标**：
- 内存占用（MB）
- 磁盘空间（MB）
- CPU 使用率（%）

### 4. 测试代码设计

#### 4.1 测试结构

```go
// internal/infrastructure/storage/benchmark/benchmark_test.go
package benchmark_test

import (
    "testing"
    "go.etcd.io/bbolt"
    "github.com/jzhang405/NexKV/internal/infrastructure/storage/bftree"
)

// 基准测试套件
func BenchmarkBoltDB_Set(b *testing.B)
func BenchmarkBfTree_P0_Set(b *testing.B)
func BenchmarkBfTree_P1_Set(b *testing.B)

func BenchmarkBoltDB_Get(b *testing.B)
func BenchmarkBfTree_P0_Get(b *testing.B)
func BenchmarkBfTree_P1_Get(b *testing.B)

func BenchmarkBoltDB_ConcurrentReads(b *testing.B)
func BenchmarkBfTree_P0_ConcurrentReads(b *testing.B)
func BenchmarkBfTree_P1_ConcurrentReads(b *testing.B)

// ... 其他场景
```

#### 4.2 配置选项

```go
type BenchmarkConfig struct {
    DataSize     int           // 数据条数
    KeySize      int           // Key 大小
    ValueSize    int           // Value 大小
    NumReaders   int           // 并发读协程数
    NumWriters   int           // 并发写协程数
    UseBitmapLock bool         // BfTree 是否启用 BitmapLock
}
```

### 5. 实施计划

| 阶段 | 任务 | 时间估算 | 依赖 |
|------|------|----------|------|
| **阶段 1** | 创建测试框架 | 1 小时 | - |
| **阶段 2** | 实现 BoltDB 基准测试 | 1 小时 | 阶段 1 |
| **阶段 3** | 实现 BfTree 基准测试 | 1 小时 | 阶段 1 |
| **阶段 4** | 运行基准测试 | 30 分钟 | 阶段 2, 3 |
| **阶段 5** | 分析结果并生成报告 | 1 小时 | 阶段 4 |
| **阶段 6** | 更新文档 | 30 分钟 | 阶段 5 |

**总预计时间**: 5 小时（1 天）

### 6. 成功标准

- ✅ 所有测试场景执行完成
- ✅ 生成详细的性能对比报告
- ✅ 代码合并到 main 分支
- ✅ 文档更新（Pre + Post）

---

## 第二部分：实施记录

> **更新日期**: 2026-03-07
> **状态**: 进行中

### 1. 测试环境搭建

- ✅ 分支创建: `feature/benchmark-boltdb-vs-bftree`
- ⏳ BoltDB 依赖安装
- ⏳ 测试框架搭建

### 2. 测试进度

| 场景 | BoltDB | BfTree P0 | BfTree P1 | 状态 |
|------|--------|-----------|-----------|------|
| 顺序写入 | ⏳ | ⏳ | ⏳ | 待开始 |
| 随机写入 | ⏳ | ⏳ | ⏳ | 待开始 |
| 点查询 | ⏳ | ⏳ | ⏳ | 待开始 |
| 范围查询 | ⏳ | ⏳ | ⏳ | 待开始 |
| 并发读 | ⏳ | ⏳ | ⏳ | 待开始 |
| 并发写 | ⏳ | ⏳ | ⏳ | 待开始 |
| 混合读写 | ⏳ | ⏳ | ⏳ | 待开始 |
| 大数据量 | ⏳ | ⏳ | ⏳ | 待开始 |

---

## 第三部分：后置部分（测试完成后更新）

> **更新日期**: 待定
> **状态**: 待完成

### 1. 测试结果总结

### 2. 性能对比图表

### 3. 结论与建议

### 4. 提交历史

---

## 附录

### A. 参考文档

- [BoltDB 官方文档](https://github.com/etcd-io/bbolt)
- [BfTree 设计文档](../07_spike/2026-02-21_spike_m2-storage-engine-roadmap.md)
- [PR-089 完成报告](./2026-03-01_PR-089_m2-bftree-core_Pre.md)

### B. 相关链接

- 分支: https://github.com/jzhang405/NexKV/tree/feature/benchmark-boltdb-vs-bftree
- PR: https://github.com/jzhang405/NexKV/pull/090
