# BTree Phase 2A: 写性能优化 - 基准测试

**测试日期**: 2026-03-13
**分支**: phase2-write-optimization
**目标**: 优化现有架构的写性能，不引入 WAL

---

## 📋 目录

1. [性能基线](#性能基线)
2. [优化目标](#优化目标)
3. [测试计划](#测试计划)
4. [结果对比](#结果对比)

---

## 性能基线

### Phase 1 当前性能（优化前）

```
测试环境:
- CPU: 4 核心
- 内存: 16GB
- 磁盘: SSD
- Go: 1.24

基准测试结果:
┌──────────────────────────────────────────────────┐
│ 操作            QPS      P99     P95     P50     │
├──────────────────────────────────────────────────┤
│ Set             1,696    15ms    12ms    8ms    │
│ Get             2,845    8ms     6ms    3ms    │
│ Delete          1,234    18ms    15ms   10ms    │
│ Set (纯内存)    153K     6.53μs  5.2μs  3.1μs  │
│ Get (纯内存)    14.2M    70ns    65ns   50ns   │
└──────────────────────────────────────────────────┘

CPU 性能分析 (Set 操作):
├── fsync:          39.87%  ← 最大瓶颈
├── mallocgc:       15.23%  ← 内存分配压力
├── PageSerializer: 8.45%   ← 序列化开销
├── 搜索路径:        7.89%
└── 其他:           28.56%

内存分配 (Set 操作):
├── Page buffer:    4KB
├── 临时 buffer:    1.5KB
└── 总计:           5.5KB per op
```

### 瓶颈分析

**1. fsync 瓶颈**（39.87% CPU）
- **问题**: 每次 Set 都调用 fsync
- **影响**: 磁盘 I/O 等待（10ms）
- **优化方案**: 异步刷盘 + Group Commit
- **预期提升**: 2.0x QPS

**2. 内存分配**（15.23% CPU）
- **问题**: 每次操作分配 5.5KB buffer
- **影响**: GC 压力大，频繁暂停
- **优化方案**: Buffer Pool 复用
- **预期提升**: 2.5x QPS

**3. 序列化开销**（8.45% CPU）
- **问题**: 大量函数调用，动态扩展 slice
- **影响**: CPU 开销
- **优化方案**: unsafe 直接内存操作
- **预期提升**: 1.3x QPS

---

## 优化目标

### 性能目标

```
指标              基线       目标       提升
──────────────────────────────────────────
Set QPS          1,696     5,000+     3x
Set P99 延迟     15ms      <5ms       3x
Set P95 延迟     12ms      <4ms       3x
Set P50 延迟     8ms       <2ms       4x
内存分配/op      5.5KB     1KB        ↓82%
GC CPU 时间      15.23%    3%         ↓80%
```

### 功能目标

- ✅ 启用异步刷盘（已有框架）
- ✅ 实现 Buffer Pool（减少内存分配）
- ✅ 实现批量 Set API（SetBatch）
- ⚡ 可选：序列化优化（unsafe）

### 不做什么

- ❌ 不实现 WAL（留在 Phase 2B）
- ❌ 不实现崩溃恢复（留在 Phase 2B）
- ❌ 不改变存储格式（向后兼容）
- ❌ 不引入新的依赖

---

## 测试计划

### 测试场景

#### 1. 异步刷盘优化测试

**测试用例**:
```go
// 测试不同的批量大小
func BenchmarkAsyncFlush_BatchSize(b *testing.B) {
    batchSizes := []int{8, 16, 32, 64}
    for _, size := range batchSizes {
        b.Run(fmt.Sprintf("size-%d", size), func(b *testing.B) {
            tree := setupBTree(b)
            tree.SetFlushBatchSize(size)

            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                tree.Set(ctx, []byte("key"), []byte("value"))
            }
        })
    }
}

// 测试不同的刷盘间隔
func BenchmarkAsyncFlush_Interval(b *testing.B) {
    intervals := []time.Duration{50*time.Millisecond, 100*time.Millisecond, 200*time.Millisecond}
    for _, interval := range intervals {
        b.Run(fmt.Sprintf("interval-%dms", interval.Milliseconds()), func(b *testing.B) {
            tree := setupBTree(b)
            tree.SetFlushInterval(interval)

            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                tree.Set(ctx, []byte("key"), []byte("value"))
            }
        })
    }
}
```

#### 2. Buffer Pool 性能测试

**测试用例**:
```go
// 测试 Pool 命中率
func TestBufferPool_HitRate(t *testing.T) {
    pool := NewBufferPool(BufferPoolConfig{MaxCached: 1000})

    // 预热 pool
    for i := 0; i < 100; i++ {
        buf := pool.Get(4096)
        pool.Put(buf)
    }

    // 测试命中率
    hitsBefore := pool.stats.Hits.Load()
    missesBefore := pool.stats.Misses.Load()

    // 执行大量 Get/Put
    for i := 0; i < 10000; i++ {
        buf := pool.Get(4096)
        // 模拟使用 buffer
        buf[0] = byte(i)
        pool.Put(buf)
    }

    hitsAfter := pool.stats.Hits.Load()
    missesAfter := pool.stats.Misses.Load()

    hitRate := float64(hitsAfter-hitsBefore) / float64(10000) * 100

    assert.Greater(t, hitRate, 80.0, "Hit rate should be >80%%")
}

// Benchmark: Pool vs 直接分配
func BenchmarkBufferPool_Pool(b *testing.B) {
    pool := NewBufferPool(BufferPoolConfig{MaxCached: 1000})

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        buf := pool.Get(4096)
        buf[0] = byte(i)
        pool.Put(buf)
    }
}

func BenchmarkBufferPool_Direct(b *testing.B) {
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        buf := make([]byte, 4096)
        buf[0] = byte(i)
        // 无 pool，依赖 GC
    }
}
```

#### 3. 批量 Set API 测试

**测试用例**:
```go
// 测试不同批量大小的性能
func BenchmarkSetBatch_Size(b *testing.B) {
    sizes := []int{10, 50, 100, 500, 1000}

    for _, size := range sizes {
        b.Run(fmt.Sprintf("size-%d", size), func(b *testing.B) {
            tree := setupBTree(b)
            kvs := generateKVs(size)

            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                tree.SetBatch(ctx, kvs)
            }
        })
    }
}

// 对比: Set vs SetBatch
func BenchmarkSet_vs_SetBatch(b *testing.B) {
    // 单条 Set
    b.Run("Single", func(b *testing.B) {
        tree := setupBTree(b)
        kv := KeyValue{Key: []byte("key"), Value: []byte("value")}

        b.ResetTimer()
        for i := 0; i < b.N; i++ {
            tree.Set(ctx, kv.Key, kv.Value)
        }
    })

    // 批量 Set (100)
    b.Run("Batch100", func(b *testing.B) {
        tree := setupBTree(b)
        kvs := generateKVs(100)

        b.ResetTimer()
        for i := 0; i < b.N; i += 100 {
            tree.SetBatch(ctx, kvs)
        }
    })
}
```

### 测试脚本

```bash
#!/bin/bash
# run-benchmarks.sh

set -e

OUTPUT_DIR="docs/10_benchmark/2026-03-13-phase2-write-optimization"
mkdir -p "$OUTPUT_DIR"

echo "📊 运行 Phase 2A 性能基准测试..."

# 1. 异步刷盘测试
echo "1. 异步刷盘优化测试..."
go test -bench=BenchmarkAsyncFlush -benchmem -run=^$ \
    -benchtime=10s \
    ./internal/infrastructure/storage/btree/... \
    > "$OUTPUT_DIR/async_flush_results.txt"

go test -bench=BenchmarkAsyncFlush -benchmem \
    ./internal/infrastructure/storage/btree/... \
    2>&1 | tee "$OUTPUT_DIR/async_flush_profile.txt"

# 2. Buffer Pool 测试
echo "2. Buffer Pool 性能测试..."
go test -bench=BenchmarkBufferPool -benchmem -run=^$ \
    -benchtime=10s \
    ./internal/infrastructure/storage/btree/... \
    > "$OUTPUT_DIR/buffer_pool_results.txt"

# 3. 批量 Set 测试
echo "3. 批量 Set API 测试..."
go test -bench=BenchmarkSetBatch -benchmem -run=^$ \
    -benchtime=10s \
    ./internal/infrastructure/storage/btree/... \
    > "$OUTPUT_DIR/setbatch_results.txt"

# 4. 对比测试（优化前 vs 优化后）
echo "4. 对比测试..."
go test -bench=BenchmarkSet_vs_SetBatch -benchmem -run=^$ \
    ./internal/infrastructure/storage/btree/... \
    > "$OUTPUT_DIR/comparison_results.txt"

echo "✅ 基准测试完成！"
echo "📁 结果目录: $OUTPUT_DIR"
```

---

## 结果对比

### 优化前后对比（待实施后填充）

```
┌──────────────────────────────────────────────────┐
│ 测试项           优化前    优化后    提升    状态 │
├──────────────────────────────────────────────────┤
│ Set QPS         1,696    TBD      3x      ⏳    │
│ Set P99         15ms     TBD      3x      ⏳    │
│ Buffer Pool     N/A      TBD      2.5x    ⏳    │
│ SetBatch(100)   N/A      TBD      1.5x    ⏳    │
│ SetBatch(1000)  N/A      TBD      2.0x    ⏳    │
└──────────────────────────────────────────────────┘

⏳ 待实施后填充
```

### 性能分析（待实施后填充）

```
CPU Profile (优化后):
- fsync:    ?%  (目标: <10%)
- mallocgc:  ?%  (目标: <5%)
- 序列化:    ?%  (目标: <5%)

内存分配 (优化后):
- 每次 Set:  ?KB  (目标: <1KB)
- GC 频率:    ?    (目标: ↓80%)
```

---

## 📝 测试记录

### 实施前基线

- **日期**: 2026-03-13
- **分支**: main (commit: dae1479)
- **测试结果**: 见上方"性能基线"章节

### 优化后结果

- **日期**: 待填充
- **分支**: phase2-write-optimization
- **测试结果**: 待填充

---

## 🔗 相关文档

- [Phase 2A 实施计划](../../09_code-review/2026-03-13-phase2a-write-optimization.md)
- [Phase 1 性能报告](../2026-03-13_btree_page_refactor/README.md)
- [Phase 2 完整计划](../2026-03-13-phase2-implementation-plan.md)

---

**维护者**: NexKV Team
**最后更新**: 2026-03-13
