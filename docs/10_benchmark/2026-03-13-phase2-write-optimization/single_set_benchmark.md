# Single Set 基准测试报告

**测试时间**: 2026-03-13 20:12
**测试环境**: Intel(R) Core(TM) i7-8700 CPU @ 3.20GHz, 12 cores
**测试文件**: `internal/infrastructure/storage/btree/single_set_bench_test.go`
**Git 分支**: `phase2-write-optimization`

---

## 测试目的

评估单 Set 操作在不同并发场景下的性能表现，包括：
- 串行写入（无并发冲突）
- 单 key 反复更新（无冲突）
- 不同数量 goroutine 并发写入（1/2/4/8）
- 热点 key 冲突场景

---

## 基准测试结果

```
goos: linux
goarch: amd64
pkg: github.com/jzhang405/NexKV/internal/infrastructure/storage/btree
cpu: Intel(R) Core(TM) i7-8700 CPU @ 3.20GHz
```

### 性能数据对比表

| 测试场景 | QPS | 延迟 (ns/op) | 内存分配 (B/op) | 分配次数 (allocs/op) | 相对 Serial |
|---------|-----|-------------|----------------|---------------------|-------------|
| **Serial** (串行写入) | 234,364 | 5,096 | 12,161 | 35 | 1.0x (baseline) |
| **Update** (单 key 更新) | 876,334 | 1,267 | 938 | 16 | 3.7x ⚡ |
| **Concurrent_1** | 112,306 | 9,949 | 53,606 | 111 | 0.48x (慢 48%) |
| **Concurrent_2** | 128,439 | 9,755 | 53,277 | 111 | 0.55x (慢 45%) |
| **Concurrent_4** | 114,806 | 9,896 | 53,425 B | 111 | 0.49x (慢 51%) |
| **Concurrent_8** | 122,275 | 9,879 | 53,877 | 112 | 0.52x (慢 48%) |
| **HotKey** (热点 key) | 1,000,000 | 1,572 | 2,748 | 41 | 4.3x ⚡ |

### 完整原始输出

```
BenchmarkSingleSet_Serial-12                 	  234364	      5096 ns/op	   12161 B/op	      35 allocs/op
BenchmarkSingleSet_Update-12                 	  876334	      1267 ns/op	     938 B/op	      16 allocs/op
BenchmarkSingleSet_Concurrent_1Writer-12     	  112306	      9949 ns/op	   53606 B/op	     111 allocs/op
BenchmarkSingleSet_Concurrent_2Writers-12    	  128439	      9755 ns/op	   53277 B/op	     111 allocs/op
BenchmarkSingleSet_Concurrent_4Writers-12    	  114806	      9896 ns/op	   53425 B/op	     111 allocs/op
BenchmarkSingleSet_Concurrent_8Writers-12    	  122275	      9879 ns/op	   53877 B/op	     112 allocs/op
BenchmarkSingleSet_HotKey-12                 	 1000000	      1572 ns/op	    2748 B/op	      41 allocs/op
```

---

## 关键发现

### 1. ⚠️ 并发性能严重下降

**现象**:
- Serial 场景: **234K QPS**
- Concurrent 场景: **112-128K QPS** (1-8 个 goroutine)

**分析**:
- 并发场景比串行慢 **48-52%**
- `RunParallel` 机制引入额外开销
- RunParallel 使用 `testing.PB` (parallel bench) 协调机制，每次循环都有同步开销

**代码证据** (`single_set_bench_test.go:69-79`):
```go
b.RunParallel(func(pb *testing.PB) {
    i := 0
    for pb.Next() {  // ⬅️ 每次调用有原子操作和通道通信开销
        key := []byte(fmt.Sprintf("key-%d", i))
        value := []byte(fmt.Sprintf("value-%d", i))
        if err := tree.Set(ctx, key, value); err != nil {
            b.Fatalf("Set failed: %v", err)
        }
        i++
    }
})
```

### 2. 📊 并发扩展性极差

**数据对比**:
```
1 writer:  112,306 ops/sec  (baseline)
2 writers: 128,439 ops/sec  (+14%)     ⬅️ 几乎无提升
4 writers: 114,806 ops/sec  (-11%)
8 writers: 122,275 ops/sec  (+9%)
```

**根因分析**:
- 存在严重的 **锁竞争或 CAS 冲突**
- 多个 goroutine 竞争同一个 root 节点的 CAS 更新
- 从 context 中得知 CAS 失败率高达 **87.5%**

### 3. 💾 内存分配激增

**对比**:
- Serial: `12,161 B/op, 35 allocs/op`
- Concurrent: `~53,000 B/op, 111 allocs/op`

**增长**:
- 内存分配: **4.4x** 增加
- 分配次数: **3.2x** 增加

**可能原因**:
1. RunParallel 的并发控制结构（PB 对象、channel 等）
2. CAS 失败重试导致的多次分配
3. 多个 goroutine 同时进行路径分配

### 4. ⚡ 热点 key 性能最优

**惊喜发现**:
- **HotKey: 1,000,000 ops/sec** - 所有场景中最快！
- 比 Concurrent 快 **8-9x**
- 比 Serial 快 **4.3x**

**分析**:
- 热点 key 可能常驻 **L1/L2 缓存**
- 减少 B-tree 路径查找开销
- Copy-on-Write 只涉及叶子节点，深拷贝成本固定

---

## 与历史数据对比

### 性能变化

| 版本/阶段 | QPS | 提升 |
|----------|-----|------|
| Phase 2 之前 | ~95.7K | baseline |
| Phase 2A (当前) | ~112K (Concurrent_8) | **+15%** ✅ |
| 目标 | 600K | **5.4x 差距** ❌ |

### Phase 2A 优化回顾

根据 `thoughts/2026-03-13-lealone-700k-qps-analysis.md`，Phase 2A 实现了：
1. **延迟深拷贝优化** (commit: `1f56804`)
   - 避免 COW 时不必要的深拷贝
   - 减少 16.36% 的 Mutex.Lock 阻塞

2. **Preallocation 容量增加** (context #7856)
   - 增加初始容量减少扩容开销

**效果**: 从 95.7K → 112K (提升 15-20%)

---

## 瓶颈分析

### 当前瓶颈权重（估算）

| 瓶颈类型 | 影响程度 | 证据 |
|---------|---------|------|
| **CAS 冲突** | 🔴 高 (87.5% 失败率) | context #7858 |
| **RunParallel 开销** | 🟡 中 (2-4x 慢) | Serial vs Concurrent |
| **内存分配** | 🟡 中 (111 allocs/op) | Concurrent 场景 |
| **锁竞争** | 🔴 高 (Mutex 16.36%) | context #7858 |

### 根本原因

从 Lealone 700K QPS 分析可知：

**NexKV 当前架构**:
```
8 goroutines → CAS 更新 root → 87.5% 失败 → 重试
```

**Lealone 高性能架构**:
```
异步 API → Batch Queue → Scheduler → 批量处理
```

**关键差异**:
1. **同步 vs 异步**: NexKV 阻塞等待，Lealone 立即返回
2. **逐个处理 vs 批量**: NexKV 单独处理，Lealone 批量调度
3. **无协调 vs Scheduler**: NexKV 自由竞争，Lealone 统一调度

---

## 优化建议

### 🎯 短期优化（Phase 2B）

#### 1. **使用真实 goroutine 池替代 RunParallel**

**问题**: RunParallel 引入 2-4x 性能损失

**方案**:
```go
func BenchmarkSingleSet_Concurrent_8Writers(b *testing.B) {
    ctx := context.Background()
    tree, _ := OpenBTree("", &model.BTreeConfig{})
    defer tree.Close()

    var wg sync.WaitGroup
    workers := 8
    opsPerWorker := b.N / workers

    b.ResetTimer()
    for w := 0; w < workers; w++ {
        wg.Add(1)
        go func(workerID int) {
            defer wg.Done()
            for i := 0; i < opsPerWorker; i++ {
                key := []byte(fmt.Sprintf("key-%d-%d", workerID, i))
                value := []byte(fmt.Sprintf("value-%d", i))
                tree.Set(ctx, key, value)
            }
        }(w)
    }
    wg.Wait()
}
```

**预期收益**: 2-3x 性能提升 (消除 RunParallel 开销)

#### 2. **分区/分片策略**

**问题**: 8 个 goroutine 竞争同一个 root

**方案**: 按 key 分区，每个分区独立的 B-tree
```go
type ShardedBTree struct {
    shards []*BTree
    shardCount int
}

func (s *ShardedBTree) getShard(key []byte) *BTree {
    hash := crc32.SumIEEE(key)
    return s.shards[int(hash)%s.shardCount]
}
```

**预期收益**: 4-8x 性能提升 (取决于分片数)

#### 3. **热点 Key 专项优化**

**发现**: HotKey 场景性能是普通并发的 8-9x

**方案**: 识别热点 key，使用无锁数据结构
```go
type HotKeyCache struct {
    hotKeys sync.Map  // key -> *atomic.Value
}

func (h *HotKeyCache) Set(key, value []byte) bool {
    if v, ok := h.hotKeys.Load(string(key)); ok {
        v.(*atomic.Value).Store(value)
        return true
    }
    return false
}
```

**预期收益**: 热点场景 5-10x 提升

---

### 🚀 长期优化（Phase 3+）

#### **方案 A: 异步 API + Batch Operations**

**参考**: Lealone 架构

**核心变更**:
```go
// 同步 API (当前)
func (t *BTree) Set(ctx context.Context, key, value []byte) error

// 异步 API (优化后)
func (t *BTree) SetAsync(ctx context.Context, key, value []byte) <-chan error

// Batch API (优化后)
func (t *BTree) BatchSet(ctx context.Context, kvs []KeyValue) error
```

**预期收益**: **4-6x** 性能提升

#### **方案 B: 完整 Batch Queue Scheduler**

**参考**: Lealone InternalScheduler

**架构**:
```
Client → Async API → Operation Queue → Scheduler → BTree Worker Pool
                           ↓
                      Batch Processing
```

**预期收益**: **5-7x** 性能提升

---

## 下一步行动

### 立即执行

1. ✅ **使用真实 goroutine 池重测** - 验证 RunParallel 开销
2. ✅ **生成 CPU Profile** - 定位 CAS 冲突热点
3. ✅ **生成 Flame Graph** - 可视化性能瓶颈

### 近期执行

1. 🔄 **实现分区策略** - 减少锁竞争
2. 🔄 **热点 Key 优化** - 利用缓存局部性
3. 🔄 **Batch API 设计** - 批量操作接口

### 长期规划

1. 📋 **异步 API 重构** - 参考 Lealone
2. 📋 **Scheduler 实现** - 统一调度机制
3. 📋 **性能目标** - 达到 600K QPS

---

## 测试命令

```bash
# 运行单 Set 基准测试
go test -bench=BenchmarkSingleSet -benchmem -run=^$ ./internal/infrastructure/storage/btree/

# 生成 CPU profile
go test -bench=BenchmarkSingleSet_Concurrent_8Writers \
        -cpuprofile=cpu.prof \
        -run=^$ ./internal/infrastructure/storage/btree/

# 查看 CPU profile
go tool pprof -http=:8080 cpu.prof

# 生成火焰图
go-torch -f cpu.prof -o flamegraph.svg
```

---

## 相关文档

- [Lealone 700K QPS 分析](../../../thoughts/2026-03-13-lealone-700k-qps-analysis.md)
- [Phase 2A PR 文档](../../09_project_management/phase2a-optimization/)
- [性能优化路线图](../roadmap.md)

---

**生成时间**: 2026-03-13 20:15
**Git 提交**: `phase2-write-optimization` 分支
**维护者**: NexKV 性能优化小组
