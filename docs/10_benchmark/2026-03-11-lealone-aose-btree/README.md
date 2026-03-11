# Lealone AOSE BTree 性能基准测试

> **测试日期**: 2026-03-11
> **测试人**: jzhang405
> **Lealone 版本**: Based on Lealone Database AOSE Engine
> **测试类型**: BTree 内存操作性能（未启用持久化）

---

## 测试概述

### 测试环境

**硬件**: TBD（运行时补充）

**软件环境**:
```yaml
JVM: OpenJDK (版本 TBD)
JVM参数: -Xms2g -Xmx2g -XX:+AlwaysPreTouch -XX:+UseG1GC
JMH参数:
  - Warmup: 10 iterations, 1s
  - Measurement: 20 iterations, 5s
  - Forks: 2
```

### 测试范围

**测试类**:
- `BTreeWriteBenchmark` - 写性能测试
- `BTreeReadBenchmark` - 读性能测试
- `BTreeConcurrentBenchmark` - 并发读写测试

**数据集大小**: 100,000 条记录

---

## 测试场景

### 1. 写性能测试

**测试方法**: `BTreeWriteBenchmark`

**测试场景**:
- `sequentialWrite` - 顺序写入
- `randomWrite` - 随机写入
- `update` - 更新操作
- `singleWrite` - 单次写入延迟（Mode.AverageTime）
- `putIfAbsent` - 条件写入
- `batchWrite` - 批量写入

**关键指标**:
- 吞吐量（ops/ms）
- 平均延迟（ns/op）
- 内存分配

---

### 2. 读性能测试

**测试方法**: `BTreeReadBenchmark`

**测试场景**:
- `sequentialRead` - 顺序读取
- `randomRead` - 随机读取
- `singleRead` - 单次读取延迟

**关键指标**:
- 吞吐量（ops/ms）
- 平均延迟（ns/op）

---

### 3. 并发读写测试

**测试方法**: `BTreeConcurrentBenchmark`

**测试场景**:
- `concurrentWrite` - 并发写入
- `concurrentRead` - 并发读取
- `mixedReadWrite` - 混合读写

**关键指标**:
- 吞吐量（ops/ms）
- 线程数：可配置

---

## 重要说明

### ⚠️ 测试局限性

**持久化状态**:
- **未启用持久化**（未调用 `save()`）
- 所有操作都在内存中进行
- 不包含磁盘 I/O 开销
- 不包含 Chunk 写入开销

**与生产环境的差异**:
- 生产环境会定期调用 `save()` 持久化到 Chunk 文件
- Chunk 写入是批量操作（默认 256MB）
- 实际性能会包含磁盘 I/O 开销

**适用场景**:
- ✅ 对比不同 BTree 实现的内存操作性能
- ✅ 优化算法和数据结构
- ❌ 不代表完整生产环境性能

---

## 运行测试

### 运行所有测试

```bash
cd thoughts/Lealone
./run-benchmarks.sh
```

### 运行单个测试

```bash
# 写性能测试
java -jar lealone-aose-benchmark/target/benchmarks.jar BTreeWriteBenchmark

# 读性能测试
java -jar lealone-aose-benchmark/target/benchmarks.jar BTreeReadBenchmark

# 并发测试
java -jar lealone-aose-benchmark/target/benchmarks.jar BTreeConcurrentBenchmark
```

---

## 结果文件

测试结果将保存在 `results/` 目录：

- `write_perf.txt` - 写性能结果
- `read_perf.txt` - 读性能结果
- `concurrent_perf.txt` - 并发测试结果

---

## 相关文档

- [性能分析报告](analysis/summary.md)
- [NexKV BTree 性能测试](../../../NexKV/docs/10_benchmark/2026-03-11-btree-perf-phase1/)

---

**文档版本**: v1.0
**最后更新**: 2026-03-11
**状态**: ⏳ 待运行测试
