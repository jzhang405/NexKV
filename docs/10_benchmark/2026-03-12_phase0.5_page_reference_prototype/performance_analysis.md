# 性能分析指南

> **Phase 0.5 原型验证**性能分析工具和方法

---

## 1. 基准测试

### 1.1 运行基准测试

```bash
# 进入测试目录
cd internal/infrastructure/storage/btree/prototype

# 运行所有基准测试
go test -bench=. -benchmem -cpuprofile=cpu.prof -memprofile=mem.prof

# 运行特定基准测试
go test -bench=Benchmark_AtomicPointer_Read -benchmem

# 运行 N 次（减少噪声）
go test -bench=. -benchmem -count=10
```

### 1.2 基准测试输出说明

```
Benchmark_AtomicPointer_Read-12     10000000    85.3 ns/op    0 B/op    0 allocs/op
│                                      │          │         │         │
│                                      │          │         │         └─ 内存分配次数
│                                      │          │         └─────────── 每次操作内存分配（字节）
│                                      │          └───────────────────── 每次操作时间（纳秒）
│                                      └───────────────────────────────── 执行次数
└─────────────────────────────────────────────────────────────── 测试名称
```

**关键指标**：
- **ns/op**: 每次操作的纳秒数（越低越好）
- **B/op**: 每次操作的内存分配（越低越好）
- **allocs/op**: 每次操作的内存分配次数（越低越好）

### 1.3 关键基准测试

| 测试名 | 目标 | 说明 |
|--------|------|------|
| Benchmark_DirectPointer_Read | ~10ns/op | 对比基准（直接指针） |
| Benchmark_AtomicPointer_Read | **<100ns/op** | 原子指针读取（关键） |
| Benchmark_AtomicPointer_CAS | <200ns/op | CAS 操作 |
| Benchmark_PageReference_ConcurrentRead | **>8M ops/sec** | 并发读取（关键） |
| Benchmark_PageReference_MixedReadWrite | **>5M ops/sec** | 混合读写（关键） |

---

## 2. CPU 性能分析

### 2.1 生成 CPU Profile

```bash
# 方法 1：使用基准测试
go test -bench=. -cpuprofile=cpu.prof

# 方法 2：使用 pprof 手动采样
go tool pprof -http=:8080 [binary] [profile]
```

### 2.2 分析 CPU Profile

```bash
# 交互式分析
go tool pprof cpu.prof

# 生成文本报告
go tool pprof -text cpu.prof > cpu-profile.txt

# 生成火焰图
go tool pprof -png cpu.prof > cpu-flamegraph.png

# 生成 SVG 图
go tool pprof -svg cpu.prof > cpu-graph.svg

# Web 界面（推荐）
go tool pprof -http=:8080 cpu.prof
```

### 2.3 交互式命令

```bash
# 进入 pprof 交互界面
(pprof) top        # 显示 CPU 占用最高的函数
(pprof) top10      # 显示前 10 个函数
(pprof) list FunctionName  # 显示函数的汇编代码
(pprof) web        # 在浏览器中可视化
(pprof) pdf        # 生成 PDF 报告
(pprof) call_graph FunctionName  # 显示调用图
(pprof) quit       # 退出
```

### 2.4 关键指标解读

**Flat（自身）**：函数本身的 CPU 占用（不包括调用的子函数）
**Cum（累积）**：函数 + 调用的子函数的总 CPU 占用
**Flat%**：函数自身 CPU 占用百分比

**关注点**：
- `runtime/internal/atomic.*`: 原子操作的 CPU 占用
- `runtime/internal/atomic.goLoad64`: 加载操作的延迟
- False sharing 导致的缓存失效

---

## 3. 内存性能分析

### 3.1 生成 Memory Profile

```bash
# 使用基准测试
go test -bench=. -memprofile=mem.prof

# 分析内存 profile
go tool pprof -http=:8080 mem.prof
```

### 3.2 内存分配分析

```bash
# 查看内存分配
go tool pprof -alloc_space mem.prof

# 查看内存占用
go tool pprof -inuse_space mem.prof

# 查看分配对象数量
go tool pprof -alloc_objects mem.prof
```

### 3.3 关键指标

**alloc_space**: 总分配内存（包括已释放）
**inuse_space**: 当前占用内存
**alloc_objects**: 总分配对象数
**inuse_objects**: 当前占用对象数

**关注点**：
- PageInfo 的内存占用（目标：<300%）
- 是否有内存泄漏（持续增长）
- GC 压力（分配频率）

---

## 4. Cache Miss 分析

### 4.1 使用 perf 统计

```bash
# 安装 perf（如果未安装）
sudo apt-get install linux-tools-generic

# 运行测试并统计 cache miss
perf stat -e cache-references,cache-misses,instructions,cycles go test -bench=. -benchmem
```

### 4.2 输出说明

```
Performance counter stats for 'go test -bench=. -benchmem':

    123,456,789      cache-references      # 缓存引用次数
     12,345,678      cache-misses          # 缓存未命中次数
  1,234,567,890      instructions          # CPU 指令数
    987,654,321      cycles                # CPU 周期数

# 计算缓存命中率
cache-hit-rate = 1 - (cache-misses / cache-references)
```

**关键指标**：
- **Cache miss 率**: 目标 <8%（优秀），<15%（可接受）
- **IPC（Instructions Per Cycle）**: 越高越好（>1.0 为佳）

### 4.3 False Sharing 检测

**现象**：
- Cache miss 率异常高（>15%）
- 多核并发时性能急剧下降
- CPU profile 显示 `runtime/internal/atomic.*` 占用高

**验证方法**：
1. 查看火焰图：是否有 `atomic.Load` 热点
2. 查看 PageInfo 的内存布局：是否 Cache Line 对齐
3. 使用 `perf record` 采样：`perf record -e cache-misses go test -bench=.`

---

## 5. 并发测试

### 5.1 Race Detector

```bash
# 运行并发测试（启用 race detector）
go test -race -v -run=Test_Concurrent

# 运行所有测试（启用 race detector）
go test -race -v ./...
```

### 5.2 Race Detector 输出

**正常输出**（无数据竞争）：
```
=== RUN   Test_ConcurrentReadWrite_NoRace
--- PASS: Test_ConcurrentReadWrite_NoRace (0.50s)
PASS
```

**异常输出**（发现数据竞争）：
```
WARNING: DATA RACE
Read at 0x00c0000b4010 by goroutine 8:
  main.(*PageReference).GetPage()
      /path/to/page_reference.go:30 +0x45

Previous write at 0x00c0000b4010 by goroutine 7:
  main.(*PageReference).SetPage()
      /path/to/page_reference.go:35 +0x67
```

### 5.3 并发压力测试

```bash
# 运行压力测试（5 秒）
go test -v -run=Test_PageReference_StressTest -timeout=10m

# 长时间运行（检测内存泄漏）
go test -v -run=Test_PageReference_StressTest -timeout=30m
```

---

## 6. 性能优化建议

### 6.1 如果读延迟 >100ns

**可能原因**：
- False sharing（多个变量共享同一 Cache Line）
- 频繁的原子操作（每次访问都调用 `Load()`）
- 缓存未命中（L1/L2/L3 cache miss）

**优化措施**：
- Cache Line 对齐（PageInfo 的热数据对齐到 64 bytes）
- 减少原子操作频率（缓存读取结果）
- 使用 `sync.Pool` 复用对象

### 6.2 如果并发吞吐 <8M ops/sec

**可能原因**：
- 锁竞争（虽然使用了原子操作，但可能仍有隐式同步）
- 调度开销（goroutine 切换）
- False sharing（多核并发时缓存失效）

**优化措施**：
- 优化调度策略（GOMAXPROCS 设置）
- 减少 goroutine 数量（使用 worker pool）
- Cache Line 对齐（减少 false sharing）

### 6.3 如果发现数据竞争

**可能原因**：
- 未使用原子操作（直接访问 `pInfo`）
- CAS 操作不完整（只 Load 不 CompareAndSwap）
- 非原子地修改 PageInfo 的多个字段

**优化措施**：
- 确保所有访问都通过原子操作
- 使用 `atomic.Pointer` 替代 `unsafe.Pointer`
- PageInfo 字段打包，减少 CAS 次数

---

## 7. 工具推荐

### 7.1 Go 原生工具

- **go test**: 单元测试和基准测试
- **go tool pprof**: CPU 和内存性能分析
- **go test -race**: 数据竞争检测
- **go test -cover**: 代码覆盖率

### 7.2 Linux 系统工具

- **perf**: Linux 性能分析工具（cache miss、CPU 周期）
- **top/htop**: 实时监控 CPU 和内存使用
- **valgrind**: 内存泄漏检测（虽然 Go 有 GC，但仍可验证）

### 7.3 可视化工具

- **go tool pprof -http=:8080**: Web 界面（推荐）
- **FlameGraph**: 火焰图生成工具
- **perf report**: perf 自带的报告工具

---

## 8. 常见问题

### Q1: 为什么基准测试结果不稳定？

**A**: 基准测试受多种因素影响：
- CPU 频率动态调整（Turbo Boost）
- 后台进程干扰
- Go 的 GC 和调度器

**解决方法**：
```bash
# 多次运行取平均值
go test -bench=. -benchmem -count=10

# 禁用 CPU 频率调整
sudo cpupower frequency-set -g performance

# 锁定 CPU 亲和性
taskset -c 0-3 go test -bench=. -benchmem
```

### Q2: 如何证明 Cache Line 对齐有效？

**A**: 对比对齐前后的性能：
```bash
# 测试对齐前
go test -bench=Benchmark_PageReference_ConcurrentRead -benchmem

# 测试对齐后（修改代码，添加 Cache Line padding）
go test -bench=Benchmark_PageReference_ConcurrentRead -benchmem

# 对比结果
benchstat old.txt new.txt
```

### Q3: atomic.Pointer vs Mutex 性能对比？

**A**: 基准测试对比：
```go
// 原子指针
func Benchmark_AtomicPointer(b *testing.B) {
    ref := &PageReference{}
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            _ = ref.GetPage()
        }
    })
}

// Mutex
func Benchmark_Mutex(b *testing.B) {
    mu := sync.Mutex{}
    var page *Page
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            mu.Lock()
            _ = page
            mu.Unlock()
        }
    })
}
```

---

**最后更新**：2026-03-12
