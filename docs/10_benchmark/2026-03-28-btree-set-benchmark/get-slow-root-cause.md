# Get 性能问题根因分析

日期: 2026-03-28

## 问题描述

benchmark 工具 `cmd/btree_perf_scheduler` 中，1 线程 Get 操作仅 **11,367 ops/s (87.98μs)**，而 Set 为 **108,662 ops/s (9.20μs)**。Get 比 Set 慢 9.6 倍，违反直觉（Get 是只读操作，应更快）。

## 根因

**Benchmark 工具 key 生成 Bug — 96% Get miss**

### Bug 位置

`cmd/btree_perf_scheduler/main.go` Get 模式 key 生成：

```go
// BUG: randBytes[j] 用 j 而非 j%initCount 作为前缀索引
key := fmt.Sprintf("%ckey-%d", randBytes[j], j%initCount)
```

### 初始化 vs 查询的 key 不匹配

**初始化**（200 条）：
- key = `fmt.Sprintf("%ckey-%d", randBytes[i], i)` 其中 i=0..199
- 例：`\x00key-0`, `\x01key-1`, ..., `\xc7key-199`

**Get 查询**（10000 次）：
- key = `fmt.Sprintf("%ckey-%d", randBytes[j], j%initCount)` 其中 j=0..9999
- j=0: `\x00key-0` ✓ 命中
- j=199: `\xc7key-199` ✓ 命中
- j=200: `randBytes[200]=\xc8`, 查询 `\xc8key-0` ✗ miss（初始化的是 `\x00key-0`）
- j=201: `randBytes[201]=\xc9`, 查询 `\xc9key-1` ✗ miss

**结果**：只有 j=0..199 和 j=200..399 中 j%200 恰好匹配的 400 次命中（4%），其余 9600 次 miss。

### 修复

```go
// 用 j%initCount 索引确保和初始化时相同的前缀
idx := j % initCount
key := fmt.Sprintf("%ckey-%d", randBytes[idx], idx)
```

## 验证数据

| 测试方式 | 命中率 | 吞吐量 | 延迟 |
|----------|--------|--------|------|
| Bug 版 benchmark 工具 | 4% | 11,367 ops/s | 87.98μs |
| 修复版 benchmark 工具 | 100% | 2,101,043 ops/s | 0.48μs |
| 单元测试（直接调用） | 100% | 2,349,698 ops/s | 0.43μs |
| 预生成 key 单元测试 | 100% | 3,149,006 ops/s | 0.32μs |

## 性能对比（修复后）

| 操作 | 1T 吞吐量 | 1T 延迟 | Set/Get 比值 |
|------|-----------|---------|-------------|
| Set | 95,348 ops/s | 10.49μs | 1x |
| Get | 2,101,043 ops/s | 0.48μs | **22x 更快** |

## Get 正常性能基线

修复后 Get 单线程性能约 **2.1M ops/s (0.48μs)**，这是合理的：
- 只读路径无锁竞争
- 二分查找 O(log n)
- 无分裂开销
- 无 CAS 重试

## 附加发现

| 开销来源 | 延迟 | 占 Get 总延迟比例 |
|----------|------|------------------|
| `fmt.Sprintf` key 生成 | ~0.09μs | ~19% |
| `[]byte()` 转换 | ~0.00μs | <1% |
| BTree.Get 实际逻辑 | ~0.32μs | ~67% |
| 其他（context、接口调用等）| ~0.07μs | ~14% |

`fmt.Sprintf` 占 Get 操作约 19% 的时间。如果需要进一步优化 Get 延迟，可以考虑预生成 key 或使用更轻量的 key 生成方式。
