# Go B 树库对比实验 Spike

> **预研类型**: Spike
> **创建日期**: 2026-02-21
> **分支**: `spike/phase2-storage-engine`
> **状态**: 🔄 进行中
> **关联文档**: [Phase 2 存储引擎层预研报告](./2026-02-21_phase2-storage-engine-spike.md)

---

## 一、预研目标

在 NexKV Phase 2 存储引擎层实现之前，对主流 Go B 树库进行对比实验，评估：

1. **性能基准**：读写吞吐量、延迟、内存占用
2. **功能特性**：持久化、MVCC、事务、范围查询
3. **并发性能**：多协程读写性能
4. **代码复杂度**：集成难度、维护成本
5. **与 Bf-Tree 的对比**：为 Bf-Tree 移植提供参考基准

---

## 二、候选库选型

### 2.1 选型标准

| 维度 | 要求 | 权重 |
|------|------|------|
| **维护状态** | 活跃维护（最近 1 年内有更新） | ⭐⭐⭐ |
| **Star 数** | > 500（社区认可度） | ⭐⭐ |
| **功能覆盖** | 支持持久化/MVCC/事务 | ⭐⭐⭐ |
| **性能** | 高吞吐、低延迟 | ⭐⭐⭐⭐ |
| **并发安全** | 内置锁或无锁设计 | ⭐⭐⭐ |
| **变形类型** | B 树/B+ 树/B* 树 | ⭐⭐ |

### 2.2 候选库列表

| 库 | 变形类型 | Star | 特性 | 优先级 | 选择理由 |
|----|----------|------|------|--------|----------|
| **google/btree** | 标准B树 | 3.5K | 官方级、稳定、轻量 | ⭐⭐⭐⭐⭐ | 基准参考 |
| **tidwall/btree** | B树/B+树 | 1.2K | 高性能、支持迭代器 | ⭐⭐⭐⭐⭐ | 高性能候选 |
| **cznic/b** | B/B+/B*树 | 300+ | 持久化、MVCC、事务 | ⭐⭐⭐⭐ | 持久化候选 |
| **dgraph-io/badger** | B+树+LSM | 13K | 分布式、事务、快照 | ⭐⭐⭐ | 分布式参考 |
| **blevesearch/bleve** | B+树 | 9.8K | 搜索引擎优化、范围查询 | ⭐⭐ | 可选 |

### 2.3 最终对比选择

**核心对比组（必选）**：

| 序号 | 库 | 用途 |
|------|-----|------|
| 1 | `google/btree` | 性能基准参考（标准实现） |
| 2 | `tidwall/btree` | 高性能 B 树候选 |
| 3 | `cznic/b` | 持久化 + MVCC 候选 |

**扩展对比组（可选）**：

| 序号 | 库 | 用途 |
|------|-----|------|
| 4 | `dgraph-io/badger` | 分布式 KV 参考实现 |

**对照组**：

| 序号 | 实现 | 用途 |
|------|------|------|
| 5 | **Bf-Tree MVP**（待实现） | 目标实现对比 |

---

## 三、对比实验设计

### 3.1 实验环境

| 维度 | 规格 |
|------|------|
| **操作系统** | macOS / Linux |
| **CPU** | 8 核+ |
| **内存** | 16GB+ |
| **Go 版本** | 1.21+ |
| **数据规模** | 10 万 / 100 万 / 1000 万条记录 |

### 3.2 测试场景

#### 场景 1：顺序写入

```go
// 顺序插入 100 万条 KV
for i := 0; i < 1000000; i++ {
    key := fmt.Sprintf("key-%08d", i)
    value := make([]byte, 100) // 100 字节值
    tree.Insert(key, value)
}
```

**指标**：
- 写入吞吐量（ops/sec）
- 写入延迟（P50/P95/P99）
- 内存占用峰值

#### 场景 2：随机写入

```go
// 随机插入 100 万条 KV
for i := 0; i < 1000000; i++ {
    key := fmt.Sprintf("key-%08d", rand.Intn(1000000))
    value := make([]byte, 100)
    tree.Insert(key, value)
}
```

**指标**：
- 写入吞吐量（ops/sec）
- 写入延迟分布

#### 场景 3：点查询

```go
// 随机读取 100 万次
for i := 0; i < 1000000; i++ {
    key := fmt.Sprintf("key-%08d", rand.Intn(totalKeys))
    tree.Get(key)
}
```

**指标**：
- 读取吞吐量（ops/sec）
- 读取延迟分布（P50/P95/P99）

#### 场景 4：范围扫描

```go
// 范围扫描 1000 次，每次 1000 条
for i := 0; i < 1000; i++ {
    start := rand.Intn(totalKeys - 1000)
    tree.Scan(start, start+1000)
}
```

**指标**：
- 扫描吞吐量（条目/sec）
- 扫描延迟

#### 场景 5：并发读写

```go
// 10 协程并发写入 + 10 协程并发读取
var wg sync.WaitGroup
for i := 0; i < 10; i++ {
    wg.Add(2)
    go writeWorker(&wg, tree)
    go readWorker(&wg, tree)
}
wg.Wait()
```

**指标**：
- 并发吞吐量（ops/sec）
- 并发延迟分布
- 锁竞争情况（通过 pprof 分析）

#### 场景 6：持久化性能（仅 cznic/b、badger）

```go
// 写入后刷盘
for i := 0; i < 100000; i++ {
    tree.Insert(key, value)
}
tree.Sync() // 或 tree.Flush()
```

**指标**：
- 刷盘延迟
- 恢复时间
- 磁盘占用

### 3.3 性能目标对比

| 场景 | google/btree | tidwall/btree | cznic/b | Bf-Tree MVP 目标 |
|------|-------------|---------------|---------|-----------------|
| **顺序写入** | 基准 | > 基准 20% | > 基准 10% | > 50万 ops/s |
| **随机写入** | 基准 | > 基准 20% | > 基准 10% | > 50万 ops/s |
| **点查询** | 基准 | > 基准 10% | < 基准 10% | < 30μs |
| **范围扫描** | 基准 | > 基准 15% | ≈ 基准 | O(log N + M) |
| **并发读写** | 基准 | > 基准 30% | > 基准 20% | > 30万 ops/s |

---

## 四、实验代码结构

### 4.1 目录结构

```
spike/btree-comparison/
├── README.md                      # 实验说明
├── go.mod                         # 依赖管理
├── benchmarks/
│   ├── benchmark_test.go          # 通用基准测试框架
│   ├── google_btree_test.go       # google/btree 测试
│   ├── tidwall_btree_test.go      # tidwall/btree 测试
│   ├── cznic_b_test.go            # cznic/b 测试
│   └── results/                   # 测试结果
│       ├── benchmark_results.json
│       └── benchmark_results.md
├── analysis/
│   ├── memory_profile.go          # 内存分析
│   ├── cpu_profile.go             # CPU 分析
│   └── report_generator.go        # 报告生成
└── scripts/
    ├── run_benchmarks.sh          # 运行所有基准测试
    └── generate_report.sh         # 生成对比报告
```

### 4.2 基准测试框架

```go
// benchmarks/benchmark_test.go
package benchmarks

import (
    "testing"
    "time"
)

// TreeAdapter 统一的树接口适配器
type TreeAdapter interface {
    Name() string
    Insert(key, value []byte) error
    Get(key []byte) ([]byte, bool)
    Delete(key []byte) error
    Scan(start, end []byte) (Iterator, error)
    Close() error
}

// BenchmarkConfig 基准测试配置
type BenchmarkConfig struct {
    NumOps       int           // 操作数量
    KeySize      int           // 键大小
    ValueSize    int           // 值大小
    Concurrency  int           // 并发数
    Duration     time.Duration // 持续时间
}

// BenchmarkResult 基准测试结果
type BenchmarkResult struct {
    Name         string
    Throughput   float64  // ops/sec
    LatencyP50   float64  // μs
    LatencyP95   float64  // μs
    LatencyP99   float64  // μs
    MemoryUsed   int64    // bytes
    AllocsPerOp  float64
}

// RunBenchmark 运行基准测试
func RunBenchmark(b *testing.B, tree TreeAdapter, config BenchmarkConfig) BenchmarkResult {
    // ... 实现细节
}
```

### 4.3 实验执行脚本

```bash
#!/bin/bash
# scripts/run_benchmarks.sh

set -e

echo "=== Go B 树库对比实验 ==="
echo "开始时间: $(date)"

# 创建结果目录
mkdir -p benchmarks/results

# 运行基准测试
echo ">>> 运行 google/btree 基准测试..."
go test -bench=BenchmarkGoogleBtree -benchtime=10s -benchmem ./benchmarks/ | tee benchmarks/results/google_btree.txt

echo ">>> 运行 tidwall/btree 基准测试..."
go test -bench=BenchmarkTidwallBtree -benchtime=10s -benchmem ./benchmarks/ | tee benchmarks/results/tidwall_btree.txt

echo ">>> 运行 cznic/b 基准测试..."
go test -bench=BenchmarkCznicB -benchtime=10s -benchmem ./benchmarks/ | tee benchmarks/results/cznic_b.txt

# 生成对比报告
echo ">>> 生成对比报告..."
go run analysis/report_generator.go

echo "=== 实验完成 ==="
echo "结束时间: $(date)"
```

---

## 五、评估指标体系

### 5.1 性能指标

| 指标 | 单位 | 权重 | 测量方法 |
|------|------|------|----------|
| **写入吞吐量** | ops/sec | 30% | 基准测试 |
| **读取吞吐量** | ops/sec | 25% | 基准测试 |
| **写入延迟 P99** | μs | 15% | 基准测试 |
| **读取延迟 P99** | μs | 15% | 基准测试 |
| **内存占用** | MB | 10% | runtime.MemStats |
| **GC 压力** | GC 次数/秒 | 5% | runtime.GCStats |

### 5.2 功能指标

| 功能 | google/btree | tidwall/btree | cznic/b | 权重 |
|------|-------------|---------------|---------|------|
| **基本 CRUD** | ✅ | ✅ | ✅ | 必需 |
| **范围扫描** | ✅ | ✅ | ✅ | 必需 |
| **并发安全** | ❌ | ✅ | ✅ | 30% |
| **持久化** | ❌ | ❌ | ✅ | 25% |
| **MVCC** | ❌ | ❌ | ✅ | 20% |
| **事务** | ❌ | ❌ | ✅ | 15% |
| **迭代器** | ✅ | ✅ | ✅ | 10% |

### 5.3 代码质量指标

| 指标 | 权重 | 评估方法 |
|------|------|----------|
| **代码复杂度** | 30% | 圈复杂度分析 |
| **测试覆盖率** | 30% | go test -cover |
| **文档完整性** | 20% | 文档字数/代码字数比 |
| **依赖数量** | 20% | go mod graph |

---

## 六、预期输出

### 6.1 性能对比报告

```markdown
# Go B 树库性能对比报告

## 1. 写入性能

| 库 | 顺序写入 (ops/s) | 随机写入 (ops/s) | P99 延迟 (μs) |
|----|-----------------|-----------------|---------------|
| google/btree | X | X | X |
| tidwall/btree | X | X | X |
| cznic/b | X | X | X |

## 2. 读取性能

| 库 | 点查询 (ops/s) | 范围扫描 (条目/s) | P99 延迟 (μs) |
|----|---------------|-----------------|---------------|
| google/btree | X | X | X |
| tidwall/btree | X | X | X |
| cznic/b | X | X | X |

## 3. 并发性能

| 库 | 并发写入 (ops/s) | 并发读取 (ops/s) | 锁竞争 (%) |
|----|-----------------|-----------------|-----------|
| google/btree | X | X | X |
| tidwall/btree | X | X | X |
| cznic/b | X | X | X |

## 4. 内存占用

| 库 | 10 万条 (MB) | 100 万条 (MB) | 1000 万条 (MB) |
|----|-------------|--------------|---------------|
| google/btree | X | X | X |
| tidwall/btree | X | X | X |
| cznic/b | X | X | X |
```

### 6.2 决策建议

基于实验结果，输出以下决策建议：

| 场景 | 推荐库 | 理由 |
|------|--------|------|
| **纯内存索引** | X | 基于性能数据 |
| **需要持久化** | X | 基于持久化测试 |
| **高并发场景** | X | 基于并发测试 |
| **NexKV 存储** | Bf-Tree MVP | 基于综合对比 |

---

## 七、时间计划

| 阶段 | 任务 | 预计时间 |
|------|------|----------|
| **Day 1** | 搭建实验框架、集成 google/btree | 4h |
| **Day 2** | 集成 tidwall/btree、cznic/b | 4h |
| **Day 3** | 运行基准测试、收集数据 | 4h |
| **Day 4** | 分析结果、生成报告 | 2h |

**总计**: 14 小时（约 2 天）

---

## 八、风险与缓解

| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|------|----------|
| 某个库无法编译/运行 | 中 | 高 | 提前验证编译 |
| 测试环境不稳定 | 中 | 中 | 多次运行取平均值 |
| 内存溢出 | 低 | 高 | 限制数据规模 |

---

## 九、下一步行动

1. **创建实验目录** - `spike/btree-comparison/`
2. **编写基准测试代码** - 实现统一适配器
3. **运行实验** - 收集性能数据
4. **生成报告** - 对比分析与决策建议

---

**文档版本**: v1.0
**创建日期**: 2026-02-21
**维护者**: AI Agent
**状态**: 🔄 进行中
