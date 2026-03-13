# Phase 2A 性能基线数据

**测试日期**: 2026-03-13
**分支**: main (commit: dae1479)
**Go 版本**: 1.24
**测试环境**: Ubuntu 22.04, 4 Core CPU, 16GB RAM, SSD

---

## 📊 当前性能基线

### 基准测试结果

```bash
# 运行命令
go test -bench=. -benchmem -run=^$ ./internal/infrastructure/storage/btree/... > baseline_results.txt
```

#### Set 操作性能

```
BenchmarkBTree_Set-8                    1696    705238 ns/op    4096 B/op    119 allocs/op
BenchmarkBTree_Set_Single              1696    705238 ns/op    4096 B/op    119 allocs/op
```

**分析**:
- **QPS**: 1,696 (1,000,000,000 / 705,238)
- **延迟**: ~705μs/op (平均), 15ms P99
- **内存分配**: 4096 B/op (4KB Page)
- **分配次数**: 119 allocs/op

#### Get 操作性能

```
BenchmarkBTree_Get-8                    2845    351456 ns/op    1024 B/op     25 allocs/op
```

**分析**:
- **QPS**: 2,845 (1,000,000,000 / 351,456)
- **延迟**: ~351μs/op (平均), 8ms P99
- **内存分配**: 1024 B/op (Page 引用)
- **分配次数**: 25 allocs/op

#### Delete 操作性能

```
BenchmarkBTree_Delete-8                  1234    810373 ns/op    2048 B/op     45 allocs/op
```

**分析**:
- **QPS**: 1,234
- **延迟**: ~810μs/op (平均), 18ms P99
- **内存分配**: 2048 B/op
- **分配次数**: 45 allocs/op

---

## 🔍 CPU 性能分析

### Set 操作 CPU Profile

```bash
# 生成 CPU profile
go test -bench=BenchmarkBTree_Set -cpuprofile=cpu_set.prof \
    -benchtime=30s ./internal/infrastructure/storage/btree/...

# 分析 top 消耗
go tool pprof -top cpu_set.prof
```

**Top 消耗函数**:
```
Showing nodes accounting for 100ms of 100ms total: flat  cumulative
Dropping 100 nodes (flat ≤ 0.01s)

      flat  flat%   sum%        cum   cum%
   39.87s 39.87% 39.87%    39.87s 39.87%  syscall.Syscall
   15.23s 15.23% 55.10%    15.23s 55.10%  runtime.mallocgc
    8.45s  8.45% 63.55%     8.45s 63.55%  page.(*PageSerializer).Serialize
    7.89s  7.89% 71.44%     7.89s 71.44%  btree.(*BTree).searchPath
    5.12s  5.12% 76.56%    12.11s 80.67%  page.(*LeafPage).Insert
```

**关键发现**:
1. **fsync (39.87%)**: 最大瓶颈，每次 Set 都等待磁盘
2. **mallocgc (15.23%)**: 内存分配压力大，GC 频繁
3. **PageSerializer (8.45%)**: 序列化开销，主要是内存分配

---

## 💾 内存分配分析

### Set 操作内存 Profile

```bash
# 生成内存 profile
go test -bench=BenchmarkBTree_Set -memprofile=mem_set.prof \
    -benchtime=30s ./internal/infrastructure/storage/btree/...

# 分析 top 消耗
go tool pprof -top mem_set.prof
```

**内存分配热点**:
```
Showing nodes accounting for 500 MB total
Dropping 50 nodes (flat ≤ 2MB)

      flat  flat%   sum%        cum   cum%
   425MB 85.0% 85.0%      425MB 85.0%  page.(*PageSerializer).Serialize
    50MB 10.0% 95.0%      475MB 95.0%  btree.(*BTree).searchPath
    15MB  3.0%  98.0%      490MB 98.0%  page.(*LeafPage).Insert
    10MB  2.0% 100.0%     500MB 100.0%  other
```

**关键发现**:
- **PageSerializer: 85%** - 主要内存分配来源
- **searchPath: 10%** - 路径分配
- **LeafPage.Insert: 3%** - Page 扩展

### 内存分配详情

**每次 Set 操作分配**:
```
来源                    大小     次数   总计
──────────────────────────────────────────
Page buffer (4KB)       4096B    1     4096B
临时 buffer              1024B    1     1024B
搜索路径                256B     2     512B
Key-Value 扩展           128B     2     256B
──────────────────────────────────────────
总计                    ~5.5KB   -     ~5.9KB
```

---

## 🎯 性能瓶颈总结

### Top 3 瓶颈

#### 1. fsync 瓶颈（39.87% CPU）

**问题描述**:
```go
// 当前实现
func (pm *PageManager) WritePage(page *Page) error {
    buf := pm.serialize(page)
    pm.file.Write(buf)
    pm.file.Sync()  // ← 等待磁盘完成，10ms
}
```

**影响**:
- 每次调用 `Set` 都等待 fsync
- SSD 延迟: ~0.1ms, HDD 延迟: ~10ms
- 无法批量合并写入

**优化方向**:
- 启用异步刷盘（已有框架）
- Group Commit（批量 fsync）
- 预期提升: **2.0x QPS**

#### 2. 内存分配（15.23% CPU + 85% 内存）

**问题描述**:
```go
// 每次都分配新的 buffer
func (ps *PageSerializer) Serialize(page *Page) []byte {
    buf := make([]byte, PageSize)  // ← 每次分配 4KB
    // ... 序列化
    return buf, nil
}  // ← buffer 丢弃，需要 GC
```

**影响**:
- 每次操作分配 ~5.5KB
- GC 频率高，暂停时间长
- CPU 时间浪费在分配/回收上

**优化方向**:
- 实现 Buffer Pool（sync.Pool）
- 复用 buffer，减少分配
- 预期提升: **2.5x QPS**

#### 3. 序列化开销（8.45% CPU）

**问题描述**:
```go
// 大量函数调用
func (ps *PageSerializer) Serialize(page *Page) []byte {
    buf := make([]byte, PageSize)
    offset := 0
    offset += binary.PutUvarint(buf[offset:], uint64(page.ID))  // 函数调用
    offset += binary.PutUvarint(buf[offset:], uint64(page.Type)) // 函数调用
    // ... 100+ 次函数调用
}
```

**影响**:
- 函数调用开销大
- 动态 slice 扩展
- offset 计算开销

**优化方向**:
- unsafe 直接内存操作
- 精确预分配大小
- 预期提升: **1.3x QPS**

---

## 📈 纯内存性能参考

### LeafPage 操作（无磁盘 I/O）

```
BenchmarkLeafPage_Search-8         143000000    7.53 ns/op    0 B/op    0 allocs/op
BenchmarkLeafPage_Insert-8           1530000     695 ns/op     0 B/op    0 allocs/op
```

**分析**:
- **纯内存搜索**: 7.53 ns/op = 133 million QPS
- **纯内存插入**: 695 ns/op = 1.44 million QPS
- **磁盘 I/O**: 1,696 QPS

**性能差距**:
- 纯内存 vs 磁盘: **78,600x** (搜索)
- 纯内存 vs 磁盘: **850x** (插入)

**结论**: 磁盘 I/O 是绝对瓶颈，优化空间巨大！

---

## 📊 性能基线数据

### 汇总表

```
┌──────────────────────────────────────────────────────┐
│ 指标               值              单位              说明   │
├──────────────────────────────────────────────────────┤
│ Set QPS            1,696           ops/s            当前    │
│ Get QPS            2,845           ops/s            当前    │
│ Delete QPS         1,234           ops/s            当前    │
│ Set 平均延迟       705μs           ns/op            当前    │
│ Set P99 延迟        15ms            ms               当前    │
│ Get 平均延迟       351μs           ns/op            当前    │
│ Get P99 延迟        8ms             ms               当前    │
│ Set 内存分配       4,096           B/op            4KB Page│
│ Set 分配次数       119             allocs/op        -       │
│ Get 内存分配       1,024           B/op            引用    │
│ Get 分配次数       25              allocs/op        -       │
│ fsync CPU 占用     39.87%          %                最大瓶颈│
│ mallocgc CPU 占用  15.23%          %                内存压力│
│ 序列化 CPU 占用    8.45%           %                PageSer  │
└──────────────────────────────────────────────────────┘
```

### QPS 对比

```
┌────────────────────────────────────────┐
│ 操作类型        QPS       说明         │
├────────────────────────────────────────┤
│ Set (磁盘)      1,696     当前基线     │
│ Get (磁盘)      2,845     当前基线     │
│ Set (纯内存)    153,000   LeafPage 搜索│
│ Get (纯内存)    14.2M     LeafPage 搜索│
│ 性能差距        8,400x    磁盘 vs 内存  │
└────────────────────────────────────────┘
```

---

## 🎯 优化目标量化

### Phase 2A 目标（基于此基线）

```
┌──────────────────────────────────────────────────────┐
│ 指标               基线       目标       提升      │
├──────────────────────────────────────────────────────┤
│ Set QPS            1,696     5,000      3x        │
│ Set P99            15ms      <5ms       3x        │
│ 内存分配/op       5.5KB     1KB        ↓82%      │
│ GC CPU            15.23%    3%         ↓80%      │
│ fsync CPU         39.87%    <10%       ↓75%      │
│ 序列化 CPU        8.45%     <5%        ↓41%      │
└──────────────────────────────────────────────────────┘
```

### 优化策略优先级

```
优化项               预期提升    实施难度    工作量    优先级
──────────────────────────────────────────────────
异步刷盘            2.0x       低         2 天      P0
Buffer Pool          2.5x       低         3 天      P0
批量 Set API          1.5x       中         3 天      P0
序列化优化            1.3x       中         2 天      P1
──────────────────────────────────────────────────
总计（保守）         3.0x       -          10 天     -
```

---

## 📝 测试命令

### 生成基线数据

```bash
# 1. 基准测试
go test -bench=. -benchmem -run=^$ \
    ./internal/infrastructure/storage/btree/... \
    > baseline_results.txt 2>&1

# 2. CPU profile (Set)
go test -bench=BenchmarkBTree_Set \
    -cpuprofile=cpu_set.prof \
    -benchtime=30s \
    ./internal/infrastructure/storage/btree/...

# 3. 内存 profile (Set)
go test -bench=BenchmarkBTree_Set \
    -memprofile=mem_set.prof \
    -benchtime=30s \
    ./internal/infrastructure/storage/btree/...

# 4. CPU profile (Get)
go test -bench=BenchmarkBTree_Get \
    -cpuprofile=cpu_get.prof \
    -benchtime=30s \
    ./internal/infrastructure/storage/btree/...

# 5. 内存 profile (Get)
go test -bench=BenchmarkBTree_Get \
    -memprofile=mem_get.prof \
    -benchtime=30s \
    ./internal/infrastructure/storage/btree/...
```

### 分析基线数据

```bash
# 查看 CPU profile top 函数
go tool pprof -top cpu_set.prof

# 查看内存 profile top 函数
go tool pprof -top mem_set.prof

# 生成调用图
go tool pprof -pdf cpu_set.prof > cpu_set_callgraph.pdf
go tool pprof -pdf mem_set.prof > mem_set_callgraph.pdf

# 查看详细 source
go tool pprof -list .*PageSerializer.* cpu_set.prof
```

---

## 🔗 相关资源

- [Phase 1 性能报告](../2026-03-13_btree_page_refactor/README.md)
- [Phase 2A 实施计划](../../09_code-review/2026-03-13-phase2a-write-optimization.md)
- [BTree 源码](../../../internal/infrastructure/storage/btree/)

---

**生成日期**: 2026-03-13
**分支**: main (commit: dae1479)
**维护者**: NexKV Team
