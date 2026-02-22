# M2 存储引擎性能基准测试文档

> **预研类型**: Spike
> **创建日期**: 2026-02-21
> **最后更新**: 2026-02-22
> **状态**: 🔄 待实施
> **关联文档**:
>   - [M2 接口定义](./2026-02-21_spike_m2-storage-engine-interface.md)
>   - [M2 实现方案](./2026-02-21_spike_m2-storage-engine-implement.md)
>   - [Bf-Tree 术语澄清](./bftree/2026-02-22_spike_btree-variants-clarification.md)
>   - [DDD 架构 - GoroutineProvider](./2026-02-18_spike_nexkv-ddd-interface.md#13-b4-goroutineprovider)

---

## 📋 目录

1. [测试目标](#一测试目标)
2. [测试环境](#二测试环境)
3. [测试配置](#三测试配置)
4. [性能指标](#四性能指标)
5. [测试场景](#五测试场景)
6. [对比基线](#六对比基线)
7. [测试代码结构](#七测试代码结构)
8. [结果模板](#八结果模板)
9. [问题排查](#九问题排查)
10. [附录](#附录)

---

## 一、测试目标

### 1.1 验证性能目标分级

| 操作 | P0（最低） | P1（推荐） | P2（理想） |
|------|-----------|-----------|-----------|
| **点查询（同步）** | < 50μs | < 30μs | < 20μs |
| **点查询（异步）** | < 60μs | < 40μs | < 25μs |
| **写入吞吐（同步）** | > 5万 ops/s | > 10万 ops/s | > 20万 ops/s |
| **写入吞吐（异步）** | > 8万 ops/s | > 15万 ops/s | > 30万 ops/s |
| **范围查询** | O(log N + M) | O(log N + M) | O(log N + M) |

**与 Rust 原版对比**：

| 操作 | Rust 原版 | Go MVP P0 | 差距 |
|------|----------|----------|------|
| 点查询 | 10μs | 50μs | 5x |
| 写入吞吐 | 200万 ops/s | 5万 ops/s | 40x |

**性能差距分析**：
- **GC 开销**：Go 的 GC 暂停（10-50ms）
- **无 Lock-free SMR**：RWMutex vs Lock-free
- **内存管理**：sync.Pool vs 手动内存池

### 1.2 验证 Bf-Tree 核心特性

1. **Mini-Page 动态扩容**：验证 64B→128B→256B→512B→1KB→2KB→4KB 扩容路径
2. **Promotion 策略**：验证 Read 1% + Scan 100% 生效
3. **Delta Chain 合并**：验证懒合并机制的正确性和性能
4. **页面级锁并发**：验证页面级 RWMutex 的并发性能

### 1.3 建立对比基线

- **vs google/btree**：标准 B 树实现（基准参考）
- **vs tidwall/btree**：高性能 B 树实现（性能对比）
- **vs Bf-Tree Rust 原版**：性能差距分析（预期 5x-40x）
- **vs Metadata KV (sync.Map)**：双层存储引擎对比

---

## 二、测试环境

### 2.1 硬件配置

| 维度 | 最低配置 | 推荐配置 | 理想配置 |
|------|---------|---------|---------|
| **操作系统** | macOS 12+ / Ubuntu 20.04+ | macOS 14+ / Ubuntu 22.04+ | Linux 5.15+ (性能优化内核) |
| **CPU** | 4 核 | 8 核+ | 16 核+ (支持 AVX-512) |
| **内存** | 8GB | 16GB+ | 64GB+ |
| **磁盘** | SSD | NVMe SSD | NVMe SSD (Direct I/O) |
| **网络** | 本地回环 | 本地回环 | 10Gbps 网络 |

### 2.2 软件版本

| 组件 | 版本要求 |
|------|---------|
| **Go 版本** | 1.21+ |
| **操作系统** | macOS 12+ / Linux 5.10+ |
| **测试框架** | testing + testify |
| **监控工具** | pprof + trace |

### 2.3 环境准备

```bash
# 1. 禁用 CPU 频率调节（Linux）
sudo cpupower frequency-set -g performance

# 2. 关闭 NUMA 自动平衡（Linux）
echo 0 | sudo tee /proc/sys/kernel/numa_balancing

# 3. 绑定 CPU 核心（可选）
taskset -c 0-7 go test -bench=.

# 4. 设置 Go 性能环境变量
export GODEBUG=gctrace=1  # 查看 GC 日志
export GOMAXPROCS=8      # 限制 P 数量
```

---

## 三、测试配置

### 3.1 数据规模配置

| 测试规模 | 记录数 | 键大小 | 值大小 | 总数据量 |
|---------|-------|--------|--------|---------|
| **小规模** | 10万 | 16B | 100B | ~11MB |
| **中规模** | 100万 | 16B | 100B | ~110MB |
| **大规模** | 1000万 | 16B | 100B | ~1.1GB |
| **超大规模** | 1亿 | 16B | 100B | ~11GB |

### 3.2 并发级别配置

| 并发级别 | Goroutine 数量 | 适用场景 |
|---------|---------------|---------|
| **单线程** | 1 | 延迟基准测试 |
| **低并发** | 10 | 小规模应用 |
| **中并发** | 100 | 中等负载 |
| **高并发** | 1000 | 高负载压测 |

### 3.3 Mini-Page 配置

| 配置项 | 默认值 | 测试值 |
|--------|--------|--------|
| **max_mini_page_size** | 2048B | 512B, 1024B, 2048B, 4096B |
| **read_promotion_rate** | 1% | 0%, 1%, 5%, 10% |
| **scan_promotion_rate** | 100% | 50%, 100% |

### 3.4 WAL 配置

| 配置项 | 测试值 |
|--------|--------|
| **WAL 开启/关闭** | true, false |
| **WAL 同步模式** | sync, async |
| **WAL 批量大小** | 1, 10, 100 |

---

## 四、性能指标

### 4.1 写入性能指标

| 指标 | 说明 | 目标（P0） | 目标（P1） | 目标（P2） |
|------|------|-----------|-----------|-----------|
| **写入吞吐量** | ops/sec | > 5万 | > 10万 | > 20万 |
| **写放大** | 实际写入/逻辑写入 | < 20x | < 10x | < 5x |
| **Promotion 频率** | 每秒提升次数 | - | - | - |
| **Mini-Page 扩容次数** | 各级别扩容统计 | - | - | - |
| **WAL 写入延迟** | 平均延迟 | < 1ms | < 500μs | < 100μs |

### 4.2 读取性能指标

| 指标 | 说明 | 目标（P0） | 目标（P1） | 目标（P2） |
|------|------|-----------|-----------|-----------|
| **点查询延迟 P50** | 中位数延迟 | < 50μs | < 30μs | < 20μs |
| **点查询延迟 P90** | 90 分位延迟 | < 100μs | < 60μs | < 40μs |
| **点查询延迟 P95** | 95 分位延迟 | < 150μs | < 80μs | < 50μs |
| **点查询延迟 P99** | 99 分位延迟 | < 300μs | < 150μs | < 100μs |
| **缓存命中率** | 内存命中率 | > 90% | > 95% | > 98% |
| **Delta Chain 合并开销** | 平均合并时间 | < 50μs | < 30μs | < 20μs |

### 4.3 资源占用指标

| 指标 | 说明 | 目标 |
|------|------|------|
| **内存占用（100万记录）** | RSS 峰值 | < 500MB |
| **CPU 使用率** | 峰值 CPU% | < 80% |
| **GC 暂停时间** | P99 GC 暂停 | < 10ms |
| **Goroutine 数量** | 峰值数量 | < 2000 |
| **堆内存分配** | 每秒分配 | < 100MB/s |

### 4.4 并发性能指标

| 指标 | 说明 | 目标（100 并发） |
|------|------|-----------------|
| **吞吐量提升** | vs 单线程 | > 50x |
| **锁竞争率** | 锁等待时间占比 | < 10% |
| **P99 延迟增长** | vs 单线程 | < 2x |

---

## 五、测试场景

### 5.1 场景 1：纯写入

**目的**：验证写入吞吐量和写放大

```go
func BenchmarkBfTree_Write_Sequential(b *testing.B) {
    tree := NewBfTree(config)
    defer tree.Close()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        key := fmt.Sprintf("key-%08d", i)
        value := make([]byte, 100)
        tree.Insert(context.Background(), []byte(key), value)
    }
}
```

**指标**：
- 写入吞吐量（ops/sec）
- 写放大倍数
- Promotion 频率
- 内存占用增长

### 5.2 场景 2：纯读取

**目的**：验证读取延迟和缓存命中率

```go
func BenchmarkBfTree_Read_Random(b *testing.B) {
    tree := NewBfTree(config)
    defer tree.Close()

    // 预加载 100 万条数据
    for i := 0; i < 1000000; i++ {
        key := fmt.Sprintf("key-%08d", i)
        tree.Insert(context.Background(), []byte(key), make([]byte, 100))
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        key := fmt.Sprintf("key-%08d", rand.Intn(1000000))
        tree.Get(context.Background(), []byte(key))
    }
}
```

**指标**：
- 读取吞吐量
- 延迟分布（P50/P90/P95/P99）
- 缓存命中率
- Delta Chain 合并次数

### 5.3 场景 3：混合负载

**目的**：模拟真实场景（80% 读 + 20% 写）

```go
func BenchmarkBfTree_Mixed(b *testing.B) {
    tree := NewBfTree(config)
    defer tree.Close()

    // 预加载数据
    for i := 0; i < 500000; i++ {
        key := fmt.Sprintf("key-%08d", i)
        tree.Insert(context.Background(), []byte(key), make([]byte, 100))
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        if rand.Float64() < 0.8 {
            // 读取
            key := fmt.Sprintf("key-%08d", rand.Intn(500000))
            tree.Get(context.Background(), []byte(key))
        } else {
            // 写入
            key := fmt.Sprintf("key-%08d", rand.Intn(500000))
            tree.Insert(context.Background(), []byte(key), make([]byte, 100))
        }
    }
}
```

**指标**：
- 混合吞吐量
- 读写延迟分布
- 资源占用

### 5.4 场景 4：范围扫描

**目的**：验证范围查询性能和 Promotion 策略

```go
func BenchmarkBfTree_Scan(b *testing.B) {
    tree := NewBfTree(config)
    defer tree.Close()

    // 预加载 100 万条数据
    for i := 0; i < 1000000; i++ {
        key := fmt.Sprintf("key-%08d", i)
        tree.Insert(context.Background(), []byte(key), make([]byte, 100))
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        start := rand.Intn(900000)
        iter, _ := tree.Scan(context.Background(),
            []byte(fmt.Sprintf("key-%08d", start)),
            []byte(fmt.Sprintf("key-%08d", start+1000)))
        for iter.Next() {
            // 遍历
        }
        iter.Close()
    }
}
```

**指标**：
- 扫描吞吐量（条目/秒）
- 扫描延迟
- Promotion 触发频率（应为 100%）

### 5.5 场景 5：高并发

**目的**：验证并发性能和锁竞争

```go
func BenchmarkBfTree_Concurrent(b *testing.B) {
    tree := NewBfTree(config)
    defer tree.Close()

    // 预加载数据
    for i := 0; i < 500000; i++ {
        key := fmt.Sprintf("key-%08d", i)
        tree.Insert(context.Background(), []byte(key), make([]byte, 100))
    }

    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            if i%2 == 0 {
                // 读取
                key := fmt.Sprintf("key-%08d", rand.Intn(500000))
                tree.Get(context.Background(), []byte(key))
            } else {
                // 写入
                key := fmt.Sprintf("key-%08d", rand.Intn(500000))
                tree.Insert(context.Background(), []byte(key), make([]byte, 100))
            }
            i++
        }
    })
}
```

**指标**：
- 并发吞吐量
- 锁竞争率（pprof mutex profile）
- P99 延迟增长

### 5.6 场景 6：WAL 性能影响

**目的**：验证 WAL 开启/关闭的性能差异

```go
func BenchmarkBfTree_WAL_Enabled(b *testing.B) {
    // 1. 开启 WAL
    config := Config{
        EnableWAL: true,
        WALDir:    "/tmp/bftree_wal",
    }
    tree := NewBfTree(config)
    defer tree.Close()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        key := fmt.Sprintf("key-%08d", i)
        tree.Insert(context.Background(), []byte(key), make([]byte, 100))
    }
}

func BenchmarkBfTree_WAL_Disabled(b *testing.B) {
    // 2. 关闭 WAL
    config := Config{
        EnableWAL: false,
    }
    tree := NewBfTree(config)
    defer tree.Close()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        key := fmt.Sprintf("key-%08d", i)
        tree.Insert(context.Background(), []byte(key), make([]byte, 100))
    }
}
```

**指标**：
- 吞吐量差异（预期下降 20-30%）
- 延迟差异
- WAL 写入延迟

### 5.7 场景 7：异步 vs 同步

**目的**：验证 AsyncOperation 的性能优势

```go
func BenchmarkBfTree_Sync(b *testing.B) {
    tree := NewBfTree(config)
    defer tree.Close()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        key := fmt.Sprintf("key-%08d", i)
        tree.Set(context.Background(), []byte(key), make([]byte, 100))
    }
}

func BenchmarkBfTree_Async(b *testing.B) {
    tree := NewBfTree(config)
    defer tree.Close()

    futures := make([]WriteFuture, 0, b.N)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        key := fmt.Sprintf("key-%08d", i)
        future := tree.SetAsync(context.Background(), []byte(key), make([]byte, 100))
        futures = append(futures, future)
    }

    // 等待所有 Future 完成
    for _, f := range futures {
        f.Get(context.Background())
    }
}
```

**指标**：
- 异步 vs 同步吞吐量（预期提升 1.5x-2x）
- Context 传播开销
- Future 管理开销

### 5.8 场景 8：Mini-Page 级别影响

**目的**：验证不同 max_mini_page_size 的性能影响

```go
func BenchmarkBfTree_MiniPageSize(b *testing.B) {
    configs := []int{512, 1024, 2048, 4096}

    for _, maxPageSize := range configs {
        b.Run(fmt.Sprintf("MaxSize_%d", maxPageSize), func(b *testing.B) {
            config := Config{
                MaxMiniPageSize: maxPageSize,
            }
            tree := NewBfTree(config)
            defer tree.Close()

            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                key := fmt.Sprintf("key-%08d", i)
                tree.Insert(context.Background(), []byte(key), make([]byte, 100))
            }
        })
    }
}
```

**指标**：
- 写入吞吐量（预期 2KB 最优）
- 写放大
- Promotion 频率

---

## 六、对比基线

### 6.1 vs google/btree

**测试方法**：统一适配器接口

```go
// TreeAdapter 统一适配器接口
type TreeAdapter interface {
    Insert(key, value []byte) error
    Get(key []byte) ([]byte, error)
    Delete(key []byte) error
    Scan(start, end []byte) (Iterator, error)
}

// google/btree 适配器
type GoogleBtreeAdapter struct {
    tree *btree.BTree
}

func (a *GoogleBtreeAdapter) Insert(key, value []byte) error {
    a.tree.ReplaceOrInsert(&Item{Key: key, Value: value})
    return nil
}
```

**对比指标**：

| 指标 | google/btree | Bf-Tree MVP | 目标差距 |
|------|-------------|------------|---------|
| 顺序写入吞吐量 | 基准 | > 基准 50% | 1.5x |
| 随机写入吞吐量 | 基准 | > 基准 100% | 2x |
| 点查询延迟 | 基准 | < 基准 20% | 0.8x |
| 范围扫描吞吐量 | 基准 | > 基准 30% | 1.3x |

### 6.2 vs tidwall/btree

**对比指标**：

| 指标 | tidwall/btree | Bf-Tree MVP | 目标差距 |
|------|--------------|------------|---------|
| 并发写入吞吐量 | 基准 | > 基准 30% | 1.3x |
| 并发读取吞吐量 | 基准 | ≈ 基准 | 1.0x |

### 6.3 vs Bf-Tree Rust 原版

**对比指标**：

| 操作 | Rust 原版 | Go MVP P0 | 差距 | 理由 |
|------|----------|----------|------|------|
| 点查询 | 10μs | 50μs | 5x | GC + RWMutex 开销 |
| 写入吞吐 | 200万 ops/s | 5万 ops/s | 40x | GC + 无 SMR 优化 |

**性能差距分析**：
1. **GC 开销**：Go 的 GC 暂停（10-50ms）
2. **无 Lock-free SMR**：RWMutex vs Lock-free
3. **内存管理**：sync.Pool vs 手动内存池

### 6.4 vs Metadata KV (sync.Map)

**对比指标**：

| 操作 | Metadata KV | External KV (Bf-Tree) | 差距 |
|------|------------|---------------------|------|
| 点查询（10万记录） | ~5μs | ~30μs | 6x |
| 范围扫描 | 不支持 | O(log N + M) | - |
| 写入吞吐 | ~50万 ops/s | ~10万 ops/s | 5x |

**结论**：Metadata KV 适合小数据量点查询，External KV 适合大数据量范围查询。

### 6.5 工业级三者性能对比方案

> **目标**：对标 Bf-Tree VLDB 2024 论文，验证 Go Porting 的可行性和性能损失

#### 6.5.1 对比对象与配置（严格对齐）

| 对比项 | Rust Bf-Tree (Orig) | Bf-Tree Go Porting (MVP) | Pebble (RocksDB Go 版) |
|--------|---------------------|---------------------------|-------------------------|
| **代码来源** | 微软研究院官方 Rust 实现 | Go MVP 简化版 | `github.com/cockroachdb/pebble` |
| **内存预算** | 2GB（与论文一致） | 2GB | 2GB（memtable + block cache + row cache 按 1:1:1） |
| **数据量** | 200M 条 KV | 200M 条 KV | 200M 条 KV |
| **KV 大小** | 16B Key + 16B Value | 16B Key + 16B Value | 16B Key + 16B Value |
| **页大小** | 4KB（论文默认） | 4KB | 4KB（Pebble 默认） |
| **WAL** | 启用 | 启用（复用现有 WAL） | 启用 |

**对比原则**：
1. **公平性**：三者在相同硬件、相同数据量、相同内存预算下对比
2. **针对性**：重点测 Bf-Tree 的核心优势场景（点查、写入、范围扫描同时优化）
3. **可复现性**：所有测试用 YCSB 标准负载，参数公开，代码可开源
4. **工程化**：不仅测 QPS，还要测延迟分布、写放大、内存占用等生产级指标

#### 6.5.2 核心测试指标（覆盖生产级需求）

| 指标类型 | 具体指标 | 说明 |
|----------|----------|------|
| **性能指标** | 峰值 QPS | 每秒操作数，越高越好 |
| | 平均延迟 | 平均操作延迟，越低越好 |
| | P50/P95/P99/P999 延迟 | 延迟分布，生产级核心指标 |
| **资源指标** | 写放大 | 写入字节数 / 逻辑写入字节数，越低越好（Bf-Tree 核心优势） |
| | 读放大 | 读取字节数 / 逻辑读取字节数，越低越好 |
| | 内存占用 | 运行时 RSS，越低越好 |
| | 磁盘 IOPS | 实际磁盘 IO 数，越低越好 |
| **稳定性指标** | 长时间运行 QPS 抖动 | 1 小时测试的 QPS 标准差，越低越好 |
| | 长时间运行延迟抖动 | P99 延迟的波动，越低越好 |

#### 6.5.3 YCSB 标准负载测试

**基础负载（YCSB-A/B/C/D/E/F）**：

| 负载 | 读写比例 | 访问分布 | 核心测试目标 |
|------|---------|---------|--------------|
| **YCSB-A** | 50% 读 / 50% 写 | Zipfian (0.9) | 混合负载性能（Bf-Tree 核心优势场景） |
| **YCSB-B** | 95% 读 / 5% 写 | Zipfian (0.9) | 读密集性能 |
| **YCSB-C** | 100% 读 | Zipfian (0.9) | 纯点查性能 |
| **YCSB-D** | 95% 读 / 5% 插入 | Latest | 最新数据读取性能 |
| **YCSB-E** | 95% 范围扫描 / 5% 插入 | Zipfian (0.9) | 范围扫描性能（Bf-Tree 核心优势场景） |
| **YCSB-F** | 50% 读 / 50% 读-修改-写 | Zipfian (0.9) | 事务性负载性能 |

**针对性扩展负载（验证 Bf-Tree 特性）**：

| 扩展负载 | 测试内容 | 核心目标 |
|----------|---------|---------|
| **范围扫描长度** | 扫描 100/1000/10000 条 KV | 验证 Bf-Tree 范围扫描的可扩展性 |
| **内存预算变化** | 512MB/1GB/2GB/4GB 内存 | 验证 Bf-Tree 在不同内存下的缓存效率 |
| **数据量变化** | 10M/100M/500M/1B 条 KV | 验证 Bf-Tree 在数据量增长时的性能稳定性 |
| **写放大专项** | 写入 100M 条 KV，统计实际磁盘写入量 | 直接验证 Bf-Tree 低写放大的核心优势 |

#### 6.5.4 测试环境配置（与论文对齐，保证可复现）

| 硬件配置 | 规格 |
|----------|------|
| **CPU** | 32 核 / 64 线程（与论文 CloudLab sm110p 一致） |
| **内存** | 128GB |
| **SSD** | 1TB NVMe PCIe 4.0 SSD（与论文一致） |
| **操作系统** | Ubuntu 22.04 LTS（与论文一致） |
| **文件系统** | ext4（noatime, discard 挂载） |
| **Go 版本** | Go 1.22+ |
| **Rust 版本** | Rust 1.75+ |

#### 6.5.5 测试步骤（严谨可复现）

**1. 数据准备**：

```bash
# 生成 200M 条 YCSB 测试数据
go run github.com/brianfrankcooper/YCSB/go/bin/ycsb load basic \
    -P workloads/workloada \
    -p recordcount=200000000 \
    -p operationcount=0 \
    -p exportfile=testdata.dat
```

**2. 预热**：

```bash
# 运行 10M 次操作预热缓存
go run ycsb run [引擎] -P workloads/workloada \
    -p recordcount=200000000 \
    -p operationcount=10000000
```

**3. 正式测试**：

```bash
# 每个负载运行 5 次，取最好结果
for i in {1..5}; do
  go run ycsb run [引擎] -P workloads/workloada \
    -p recordcount=200000000 \
    -p operationcount=100000000 \
    -p threadcount=32 > results/workloada_run$i.txt
done
```

**4. 资源监控**：

```bash
# 测试期间监控 CPU/内存/磁盘 IO
pidstat -u -r -d -p [引擎PID] 1 > results/resource_usage.txt
iostat -x -d 1 > results/disk_io.txt
```

**测试规模调整**：

> ⚠️ **注意**：200M 条 KV + 2GB 内存配置不现实（实际需要 > 10GB）

| 测试规模 | 记录数 | 内存需求 | 说明 |
|---------|-------|---------|------|
| 小规模 | 100万 | 500MB | 开发测试 |
| 中规模 | 1000万 | 2GB | 基准测试（推荐）|
| 大规模 | 5000万 | 8GB | 压力测试 |
| 超大规模 | 2亿 | 32GB | 生产模拟（与论文对齐）|

**建议测试流程**：
1. ✅ 先用 1000万 条 KV + 2GB 内存做基准测试
2. ✅ 再用 5000万 条 KV + 8GB 内存做压力测试
3. ⚠️ 2亿 条 KV + 32GB 内存在 Phase 3 进行

#### 6.5.6 结果分析维度（直接验证选型）

**1. 核心性能对比**：

| 场景 | 预期结果 | 验证目标 |
|------|---------|---------|
| **YCSB-A（混合负载）** | Rust Bf-Tree > Go Bf-Tree > Pebble | Bf-Tree 读写同时优化的优势 |
| **YCSB-C（纯点查）** | Rust Bf-Tree ≈ Go Bf-Tree > Pebble | Bf-Tree 高效缓存的优势 |
| **YCSB-E（范围扫描）** | Rust Bf-Tree > Go Bf-Tree >> Pebble | Bf-Tree 范围扫描的核心优势 |
| **写放大** | Rust Bf-Tree < Go Bf-Tree << Pebble | Bf-Tree 低写放大的核心优势 |

**2. 性能损失评估**：

重点看 **Go Bf-Tree 相比 Rust Bf-Tree 的性能损失**：
- 若损失在 **30%-50%** 以内：完全可接受，MVP 选型合理
- 若损失在 **50%-70%**：需要优化 Go 实现（如内存管理、并发控制）
- 若损失超过 **70%**：再考虑混合架构（Rust 核心 + Go 封装）

**3. 合理性评估**：

✅ **完全可行，且是工业级标准做法**：
1. **与论文对齐**：测试配置、数据量、负载完全对标 Bf-Tree VLDB 2024 论文，结果有学术说服力
2. **覆盖核心场景**：不仅测 QPS，还测延迟分布、写放大、资源占用，符合生产级需求
3. **可复现性强**：用 YCSB 标准负载，参数公开，任何人都能复现结果
4. **直接验证选型**：结果能直接回答「Go Porting 是否可行」「性能损失是否可接受」「Bf-Tree 是否比成熟引擎有优势」这三个核心问题

**4. 实施建议**：

1. **先跑小数据量验证**：先用 10M 条 KV 跑通测试流程，再上 200M 大数据量
2. **重点测 YCSB-A/E 和写放大**：这三个是 Bf-Tree 的核心优势场景，能最快验证选型合理性
3. **保留原始数据**：所有测试结果、监控数据都要保留，方便后续分析和优化

---

## 七、测试代码结构

### 7.1 目录结构

```
internal/infrastructure/storage/benchmark/
├── benchmark_test.go          # 基准测试主文件
├── adapter.go                 # 统一适配器接口
├── google_btree_adapter.go    # google/btree 适配器
├── tidwall_btree_adapter.go   # tidwall/btree 适配器
├── bftree_adapter.go          # Bf-Tree 适配器
├── metadata_adapter.go        # Metadata KV 适配器
├── benchmark_suite.go         # 基准测试套件
├── report_generator.go        # 报告生成器
└── results/                   # 测试结果
    ├── benchmark_*.json
    └── benchmark_*.md
```

### 7.2 基准测试框架

```go
// benchmark/benchmark_suite.go
package benchmark

import (
    "testing"
    "time"
)

// BenchmarkSuite 基准测试套件
type BenchmarkSuite struct {
    config BenchmarkConfig
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
    LatencyP90   float64  // μs
    LatencyP95   float64  // μs
    LatencyP99   float64  // μs
    MemoryUsed   int64    // bytes
    AllocsPerOp  float64
}

// RunBenchmark 运行基准测试
func (s *BenchmarkSuite) RunBenchmark(
    b *testing.B,
    tree TreeAdapter,
    config BenchmarkConfig,
) BenchmarkResult {
    // ... 实现细节
}
```

### 7.3 运行脚本

```bash
#!/bin/bash
# scripts/run_storage_benchmark.sh

set -e

echo "=== M2 存储引擎性能基准测试 ==="
echo "开始时间: $(date)"

# 创建结果目录
mkdir -p internal/infrastructure/storage/benchmark/results

# 1. 运行 Bf-Tree 基准测试
echo ">>> 运行 Bf-Tree 基准测试..."
go test -bench=BenchmarkBfTree -benchtime=10s -benchmem \
    ./internal/infrastructure/storage/benchmark/ | \
    tee internal/infrastructure/storage/benchmark/results/bftree.txt

# 2. 运行对比测试
echo ">>> 运行对比测试..."
go test -bench=BenchmarkComparison -benchtime=10s -benchmem \
    ./internal/infrastructure/storage/benchmark/ | \
    tee internal/infrastructure/storage/benchmark/results/comparison.txt

# 3. 生成报告
echo ">>> 生成报告..."
go run internal/infrastructure/storage/benchmark/report_generator.go

echo "=== 测试完成 ==="
echo "结束时间: $(date)"
```

---

## 八、结果模板

### 8.1 性能对比表格

```markdown
# M2 存储引擎性能基准测试报告

## 1. 写入性能

| 实现 | 顺序写入 (ops/s) | 随机写入 (ops/s) | P99 延迟 (μs) | 写放大 |
|------|-----------------|-----------------|---------------|--------|
| **Bf-Tree MVP** | X | X | X | X |
| google/btree | 基准 | 基准 | 基准 | - |
| tidwall/btree | 基准 | 基准 | 基准 | - |
| **vs 基准差距** | +Y% | +Y% | -Y% | - |

## 2. 读取性能

| 实现 | 点查询 (ops/s) | 范围扫描 (条目/s) | P99 延迟 (μs) | 缓存命中率 |
|------|---------------|-----------------|---------------|-----------|
| **Bf-Tree MVP** | X | X | X | X% |
| google/btree | 基准 | 基准 | 基准 | - |
| tidwall/btree | 基准 | 基准 | 基准 | - |

## 3. 并发性能

| 实现 | 并发写入 (ops/s) | 并发读取 (ops/s) | 锁竞争 (%) |
|------|-----------------|-----------------|-----------|
| **Bf-Tree MVP** | X | X | X% |
| google/btree | 基准 | 基准 | 基准 |
| tidwall/btree | 基准 | 基准 | 基准 |

## 4. 资源占用

| 实现 | 内存占用 (MB) | CPU 使用率 (%) | GC 暂停 P99 (ms) |
|------|-------------|---------------|-----------------|
| **Bf-Tree MVP** | X | X% | X |
| google/btree | 基准 | 基准% | 基准 |
| tidwall/btree | 基准 | 基准% | 基准 |
```

### 8.2 Bf-Tree 特性分析

```markdown
## 5. Bf-Tree 核心特性验证

### 5.1 Mini-Page 扩容路径

| 级别 | 大小 | 扩容次数 | 占比 |
|------|------|---------|------|
| Level 1 | 64B | X | Y% |
| Level 2 | 128B | X | Y% |
| Level 3 | 256B | X | Y% |
| Level 4 | 512B | X | Y% |
| Level 5 | 1KB | X | Y% |
| Level 6 | 2KB | X | Y% |
| Full-Page | 4KB | X | Y% |

**结论**：扩容路径正常，大部分数据在 2KB 级别触发 Promotion。

### 5.2 Promotion 策略

| 场景 | Promotion 频率 | 预期 | 结果 |
|------|---------------|------|------|
| **Read Promotion** | X 次/秒 | ~1% | ✅ 正常 |
| **Scan Promotion** | X 次/秒 | 100% | ✅ 正常 |

### 5.3 Delta Chain 性能

| 指标 | 平均值 | P99 |
|------|--------|-----|
| **Delta Chain 长度** | X | X |
| **合并时间** | X μs | X μs |
```

### 8.3 性能瓶颈分析

```markdown
## 6. 性能瓶颈分析

### 6.1 CPU Profile 热点

| 函数 | CPU 占比 | 优化方向 |
|------|---------|---------|
| runtime.mallocgc | X% | sync.Pool 优化 |
| sync.(*RWMutex).Lock | X% | 减小锁粒度 |
| runtime.memmove | X% | 减少内存拷贝 |

### 6.2 内存分配热点

| 函数 | 分配次数/秒 | 分配大小/秒 | 优化方向 |
|------|-----------|-----------|---------|
| bytes.Clone | X | X MB/s | 复用 buffer |
| make([]byte) | X | X MB/s | sync.Pool |

### 6.3 锁竞争分析

| 锁 | 竞争次数/秒 | 等待时间 P99 | 优化方向 |
|------|-----------|-------------|---------|
| pageLock | X | X μs | Mini-Page 独立锁 |
```

---

## 九、问题排查

### 9.1 常见性能问题

| 问题 | 症状 | 可能原因 | 排查方法 | 解决方案 |
|------|------|---------|---------|---------|
| **吞吐量低** | < 5万 ops/s | 锁竞争、GC 频繁 | pprof mutex + trace | 减小锁粒度、优化内存 |
| **P99 延迟高** | > 1ms | GC 暂停、Delta Chain 长 | trace + GC 日志 | 减少 Delta Chain 长度 |
| **内存泄漏** | RSS 持续增长 | Delta Chain 未清理 | pprof heap | 定期 Promotion |
| **写放大高** | > 20x | Mini-Page 配置不当 | 监控 Promotion 频率 | 调整 max_mini_page_size |

### 9.2 监控指标

```go
// 监控指标采集
type BfTreeMetrics struct {
    // 写入指标
    WriteTotal          int64   // 总写入次数
    WriteLatencyP50     int64   // 写入延迟 P50 (μs)
    WriteLatencyP99     int64   // 写入延迟 P99 (μs)
    WriteAmplification  float64 // 写放大倍数

    // 读取指标
    ReadTotal           int64   // 总读取次数
    ReadLatencyP50      int64   // 读取延迟 P50 (μs)
    ReadLatencyP99      int64   // 读取延迟 P99 (μs)
    CacheHitRate        float64 // 缓存命中率

    // Bf-Tree 特性指标
    MiniPageLevelCount  [7]int64 // 各级别 Mini-Page 数量
    PromotionCount      int64    // Promotion 次数
    DeltaChainLength    int64    // 平均 Delta Chain 长度
    DeltaMergeTime      int64    // Delta 合并时间 (μs)

    // 资源指标
    MemoryRSS           int64   // 内存 RSS (MB)
    GCPauseTotal        int64   // GC 暂停总时间
    GoroutineCount      int64   // Goroutine 数量
}
```

### 9.3 性能调优建议

#### 9.3.1 写入优化

| 优化方向 | 方法 | 预期提升 |
|---------|------|---------|
| **减小写放大** | 调整 max_mini_page_size = 2KB | 10-20% |
| **批量写入** | 使用 BatchSet | 30-50% |
| **异步 WAL** | 使用 AppendAsync | 20-30% |

#### 9.3.2 读取优化

| 优化方向 | 方法 | 预期提升 |
|---------|------|---------|
| **提高缓存命中率** | 增加内存缓存 | 20-30% |
| **预读优化** | PrefetchPages | 10-20% |
| **减小 Delta Chain** | 提高 Promotion 频率 | 15-25% |

#### 9.3.3 并发优化

| 优化方向 | 方法 | 预期提升 |
|---------|------|---------|
| **减小锁粒度** | Mini-Page 独立锁 | 30-50% |
| **乐观锁** | 版本号机制 | 50-100% |
| **Lock-free SMR** | 移植 Rust 实现 | 100-200% |

---

## 附录

### 附录 A：测试命令速查

```bash
# 运行所有基准测试
go test -bench=. -benchtime=10s -benchmem ./internal/infrastructure/storage/benchmark/

# 运行特定测试
go test -bench=BenchmarkBfTree_Write -benchtime=10s ./internal/infrastructure/storage/benchmark/

# CPU Profile
go test -bench=. -cpuprofile=cpu.prof ./internal/infrastructure/storage/benchmark/
go tool pprof -http=:8080 cpu.prof

# 内存 Profile
go test -bench=. -memprofile=mem.prof ./internal/infrastructure/storage/benchmark/
go tool pprof -http=:8080 mem.prof

# Trace
go test -bench=. -trace=trace.out ./internal/infrastructure/storage/benchmark/
go tool trace trace.out

# Mutex Profile
go test -bench=. -mutexprofile=mutex.prof ./internal/infrastructure/storage/benchmark/
go tool pprof -http=:8080 mutex.prof
```

### 附录 B：参考资源

| 资源 | 链接 |
|------|------|
| **Bf-Tree 论文 (VLDB 2024)** | [badrish.net/papers/bftree-vldb2024.pdf](https://badrish.net/papers/bftree-vldb2024.pdf) |
| **Go Benchmark 官方文档** | [golang.org/cmd/go/#hdr-Benchmark_functions](https://golang.org/cmd/go/#hdr-Benchmark_functions) |
| **pprof 使用指南** | [golang.org/pkg/runtime/pprof/](https://golang.org/pkg/runtime/pprof/) |
| **YCSB 基准测试** | [github.com/brianfrankcooper/YCSB](https://github.com/brianfrankcooper/YCSB) |

---

**文档版本**: v1.0
**创建日期**: 2026-02-21
**最后更新**: 2026-02-22
**维护者**: NexKV 开发团队
**状态**: 🔄 待实施
