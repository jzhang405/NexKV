# Phase 0.5: PageReference 原型验证

> **目标**：验证 `atomic.Pointer` 性能是否满足 <1μs 读延迟目标
> **工期**：1 周（Week 1）
> **决策点**：通过则继续 Phase 1，失败则启用备选方案

---

## 1. 原型目标

### 1.1 核心问题

**关键疑问**：PageReference 的间接寻址（`atomic.Pointer[PageInfo]`）是否会导致性能回退？

- **当前架构**：直接指针访问（`Node *Children[]`）
- **目标架构**：间接寻址（`PageReference → PageInfo → Page`）
- **担忧**：每次读取需要通过原子指针加载，可能影响 L1 缓存命中率

### 1.2 验证目标

| 指标 | 当前实现 | 目标实现 | 可接受范围 |
|------|----------|----------|-----------|
| 读延迟（L1 命中） | ~50ns | <1μs | **<100ns**（激进：<50ns） |
| 写延迟（CAS） | N/A | N/A | <500ns |
| 并发吞吐（1000 goroutines） | ~10M ops/sec | >5M ops/sec | **>8M ops/sec** |
| 内存占用 | 100% | 200-300% | <300% |
| Cache miss 率 | ~5% | <10% | **<8%** |

### 1.3 决策标准

**✅ 通过条件**（满足以下任一）：
- 读延迟 <100ns（L1 缓存命中场景）
- 并发吞吐 >8M ops/sec
- Cache miss 率 <8%

**❌ 失败条件**（触发备选方案）：
- 读延迟 >500ns（10x 性能回退）
- 并发吞吐 <5M ops/sec
- Cache miss 率 >15%
- 发现严重的 false sharing 问题

---

## 2. 原型实现范围

### 2.1 简化设计（仅核心功能）

**实现内容**：
```go
// 最小化 PageReference（仅 CAS 更新）
type PageReference struct {
    pInfo atomic.Pointer[PageInfo]
}

// 最小化 PageInfo（无缓存逻辑）
type PageInfo struct {
    page  *Page
    pos   int64
    state int32  // 0=clean, 1=dirty
}

// 最小化 Page（仅用于测试）
type Page struct {
    ID    int
    keys  [][]byte
    values [][]byte
}
```

**不实现**：
- ❌ LRU 缓存淘汰
- ❌ 脏页写入
- ❌ Chunk Manager
- ❌ PageLock
- ❌ RootPageReference
- ❌ Copy-on-Write

**理由**：聚焦验证原子指针性能，避免其他因素干扰。

### 2.2 实现文件结构

```
thoughts/
└── prototype/
    ├── page_reference.go       # PageReference 原型
    ├── page_info.go            # PageInfo 原型
    ├── page.go                 # Page 原型
    ├── benchmark_test.go       # 性能基准测试
    ├── concurrent_test.go      # 并发安全测试
    └── main.go                 # 原型验证主程序
```

---

## 3. 性能测试方案

### 3.1 基准测试设计

#### 测试 1：直接指针 vs 原子指针对比

```go
// 测试目标：验证原子指针的开销
func Benchmark_DirectPointer_Read(b *testing.B) {
    page := &Page{ID: 1}
    ptr := &Page{page: page}  // 直接指针

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _ = ptr.page  // 直接访问
    }
}

func Benchmark_AtomicPointer_Read(b *testing.B) {
    page := &Page{ID: 1}
    info := &PageInfo{page: page}
    ref := &PageReference{}
    ref.pInfo.Store(info)  // 原子指针

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        pInfo := ref.pInfo.Load()  // 原子加载
        _ = pInfo.page
    }
}

func Benchmark_AtomicPointer_CAS(b *testing.B) {
    oldInfo := &PageInfo{page: &Page{ID: 1}}
    newInfo := &PageInfo{page: &Page{ID: 2}}
    ref := &PageReference{}
    ref.pInfo.Store(oldInfo)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        ref.pInfo.CompareAndSwap(oldInfo, newInfo)  // CAS 操作
        oldInfo, newInfo = newInfo, oldInfo  // 交换
    }
}
```

**预期结果**：
- `Benchmark_DirectPointer_Read`: ~10ns/op（1-2 条 CPU 指令）
- `Benchmark_AtomicPointer_Read`: **<50ns/op**（可接受：3-5 条 CPU 指令 + L1 缓存）
- `Benchmark_AtomicPointer_CAS`: <200ns/op

#### 测试 2：Cache Line 对齐验证

```go
// 测试目标：验证 false sharing 是否影响性能
func Benchmark_PageInfo_WithAlignment(b *testing.B) {
    var info PageInfoAligned  // Cache line 对齐版本
    info.page = &Page{ID: 1}

    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            _ = info.page  // 读取热数据
        }
    })
}

func Benchmark_PageInfo_WithoutAlignment(b *testing.B) {
    var info PageInfoNonAligned  // 非对齐版本
    info.page = &Page{ID: 1}

    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            _ = info.page
        }
    })
}
```

**预期结果**：
- 对齐版本吞吐 >8M ops/sec
- 非对齐版本吞吐降低 >20%（false sharing 影响）

#### 测试 3：并发读写场景

```go
// 测试目标：验证高并发场景下的性能
func Benchmark_ConcurrentReadWrite(b *testing.B) {
    ref := &PageReference{}
    ref.pInfo.Store(&PageInfo{page: &Page{ID: 1}})

    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            // 90% 读，10% 写
            if rand.Intn(10) == 0 {
                newInfo := &PageInfo{page: &Page{ID: rand.Int()}}
                oldInfo := ref.pInfo.Load()
                ref.pInfo.CompareAndSwap(oldInfo, newInfo)
            } else {
                pInfo := ref.pInfo.Load()
                _ = pInfo.page
            }
        }
    })
}
```

**预期结果**：
- 并发吞吐 >5M ops/sec
- 无明显性能抖动

### 3.2 性能分析方法

```bash
# 1. 运行基准测试
go test -bench=. -benchmem -cpuprofile=cpu.prof ./thoughts/prototype/

# 2. CPU 性能分析
go tool pprof -http=:8080 cpu.prof

# 3. Cache miss 分析（需要 perf）
perf stat -e cache-references,cache-misses,instructions,cycles go test -bench=. -benchmem

# 4. 火焰图生成
go tool pprof -http=:8080 -png cpu.prof > cpu-flamegraph.png
```

**关键指标**：
- **ns/op**: 每次操作的纳秒数
- **B/op**: 每次操作的内存分配字节数
- **allocs/op**: 每次操作的内存分配次数
- **cache-misses**: 缓存未命中次数

---

## 4. 并发安全测试

### 4.1 并发测试设计

```go
// 测试 1：1000 goroutines 并发读取
func Test_ConcurrentRead_1000Goroutines(t *testing.T) {
    ref := &PageReference{}
    ref.pInfo.Store(&PageInfo{page: &Page{ID: 1}})

    const goroutines = 1000
    const readsPerGoroutine = 10000

    var wg sync.WaitGroup
    start := time.Now()

    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < readsPerGoroutine; j++ {
                pInfo := ref.pInfo.Load()
                require.NotNil(t, pInfo.page)
            }
        }()
    }

    wg.Wait()
    elapsed := time.Since(start)

    // 计算吞吐
    totalOps := goroutines * readsPerGoroutine
    throughput := float64(totalOps) / elapsed.Seconds()

    t.Logf("总操作数: %d, 耗时: %v, 吞吐: %.2f ops/sec", totalOps, elapsed, throughput)
    assert.Greater(t, throughput, 8.0e6)  // >8M ops/sec
}

// 测试 2：并发读写无数据竞争
func Test_ConcurrentReadWrite_NoRace(t *testing.T) {
    ref := &PageReference{}
    ref.pInfo.Store(&PageInfo{page: &Page{ID: 1}})

    const goroutines = 100
    const opsPerGoroutine = 1000

    var wg sync.WaitGroup
    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for j := 0; j < opsPerGoroutine; j++ {
                if id%2 == 0 {
                    // 读
                    pInfo := ref.pInfo.Load()
                    assert.NotNil(t, pInfo.page)
                } else {
                    // 写（CAS）
                    newInfo := &PageInfo{page: &Page{ID: j}}
                    oldInfo := ref.pInfo.Load()
                    ref.pInfo.CompareAndSwap(oldInfo, newInfo)
                }
            }
        }(i)
    }

    wg.Wait()
    // 如果有 race condition，go test -race 会检测到
}

// 测试 3：CAS 操作原子性
func Test_CAS_Atomicity(t *testing.T) {
    ref := &PageReference{}
    ref.pInfo.Store(&PageInfo{page: &Page{ID: 1}})

    const goroutines = 100
    var successCount atomic.Int64

    var wg sync.WaitGroup
    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            oldInfo := ref.pInfo.Load()
            newInfo := &PageInfo{page: &Page{ID: id + 10}}
            if ref.pInfo.CompareAndSwap(oldInfo, newInfo) {
                successCount.Add(1)
            }
        }(i)
    }

    wg.Wait()
    // 只有一个 goroutine 应该成功
    assert.Equal(t, int64(1), successCount.Load())
}
```

### 4.2 Race Detector

```bash
# 运行并发测试（启用 race detector）
go test -race -v -run=Test_Concurrent ./thoughts/prototype/

# 预期结果：无 WARNING 或 DATA RACE 报告
```

---

## 5. 实施步骤

### Week 1, Day 1-2：原型实现

- [ ] 创建 `thoughts/prototype/` 目录
- [ ] 实现 `PageReference` 结构（仅 `atomic.Pointer[PageInfo]`）
- [ ] 实现 `PageInfo` 结构（简化版，无缓存逻辑）
- [ ] 实现 `Page` 结构（仅用于测试）

### Week 1, Day 3-4：性能测试

- [ ] 编写基准测试（`benchmark_test.go`）
- [ ] 对比直接指针 vs 原子指针
- [ ] 运行 `go test -bench=. -benchmem`
- [ ] 生成 CPU profile 和火焰图
- [ ] 分析 cache miss 率

### Week 1, Day 5：并发测试

- [ ] 编写并发测试（`concurrent_test.go`）
- [ ] 运行 `go test -race -v`
- [ ] 验证 1000 goroutines 场景
- [ ] 测试 CAS 原子性

### Week 1, Day 6-7：结果分析与决策

- [ ] 汇总测试结果
- [ ] 判断是否满足成功标准
- [ ] **如果通过**：进入 Phase 1
- [ ] **如果失败**：设计备选方案

---

## 6. 备选方案

### 方案 A：混合架构（热数据直接指针，冷数据 PageReference）

```go
type PageReference struct {
    // 热数据路径（高频访问）：直接指针
    hotPage *Page  // 未持久化的新页面

    // 冷数据路径（低频访问）：PageReference
    coldInfo atomic.Pointer[PageInfo]  // 已持久化的旧页面
}

func (r *PageReference) GetPage() *Page {
    // 优先返回热数据（避免原子操作）
    if r.hotPage != nil {
        return r.hotPage
    }
    // 降级到冷数据路径
    info := r.coldInfo.Load()
    return info.page
}
```

**优势**：
- 热数据路径无原子操作开销
- 冷数据路径保持可扩展性

**劣势**：
- 增加复杂度（需要判断冷热）
- 需要实现冷热切换逻辑

### 方案 B：使用 `sync.Mutex` + `unsafe.Pointer`（Go 1.18 兼容）

```go
type PageReference struct {
    mu   sync.Mutex
    pInfo unsafe.Pointer  // *PageInfo
}

func (r *PageReference) GetPage() *Page {
    r.mu.Lock()
    defer r.mu.Unlock()
    return (*PageInfo)(r.pInfo).page
}

func (r *PageReference) SetPage(info *PageInfo) {
    r.mu.Lock()
    defer r.mu.Unlock()
    atomic.StorePointer(&r.pInfo, unsafe.Pointer(info))
}
```

**优势**：
- 简单直接
- 兼容旧版本 Go

**劣势**：
- 锁竞争可能更严重
- 性能可能不如 atomic.Pointer

### 方案 C：降低性能目标（接受 2-3x 性能回退）

**调整**：
- 读延迟目标：<1μs → **<3μs**
- 并发吞吐目标：>10M ops/sec → **>3M ops/sec**
- 内存占用目标：200-300% → **150-200%**

**理由**：
- 如果 atomic.Pointer 开销可接受（<3μs），则继续推进
- 通过其他优化（Cache Line 对齐、内存池）补偿性能损失

---

## 7. 成功标准总结

| 测试项 | 成功标准 | 失败触发 |
|--------|----------|----------|
| **读延迟** | <100ns | >500ns |
| **并发吞吐** | >8M ops/sec | <5M ops/sec |
| **Cache miss 率** | <8% | >15% |
| **Race detector** | 无警告 | 发现数据竞争 |
| **内存占用** | <300% | >400% |

**最终决策**：
- **✅ 通过**：进入 Phase 1（完整实现 PageReference + PageInfo）
- **⚠️ 有条件通过**：进入 Phase 1，但启用方案 A（混合架构）
- **❌ 失败**：启用方案 B（Mutex）或方案 C（降低目标），重新评估项目可行性

---

## 8. 附录：完整测试代码示例

### 8.1 主程序（`main.go`）

```go
package main

import (
    "fmt"
    "time"
)

func main() {
    fmt.Println("=== Phase 0.5: PageReference 原型验证 ===")

    // 1. 性能基准测试
    fmt.Println("\n1. 性能基准测试...")
    runBenchmarks()

    // 2. 并发测试
    fmt.Println("\n2. 并发安全测试...")
    runConcurrentTests()

    // 3. 结果汇总
    fmt.Println("\n3. 测试结果汇总...")
    summarizeResults()
}

func runBenchmarks() {
    // TODO: 调用 benchmark_test.go 中的测试
}

func runConcurrentTests() {
    // TODO: 调用 concurrent_test.go 中的测试
}

func summarizeResults() {
    // TODO: 汇总所有测试结果，输出决策建议
    fmt.Println("建议：[继续 Phase 1 / 启用备选方案 A / 启用备选方案 B]")
}
```

### 8.2 运行脚本（`run.sh`）

```bash
#!/bin/bash
set -e

echo "=== Phase 0.5 原型验证 ==="

# 1. 格式化代码
echo "1. 格式化代码..."
go fmt ./thoughts/prototype/...

# 2. 静态检查
echo "2. 静态检查..."
go vet ./thoughts/prototype/...

# 3. 单元测试
echo "3. 单元测试..."
go test -v ./thoughts/prototype/

# 4. 并发测试（race detector）
echo "4. 并发安全测试..."
go test -race -v ./thoughts/prototype/

# 5. 基准测试
echo "5. 性能基准测试..."
go test -bench=. -benchmem -cpuprofile=cpu.prof ./thoughts/prototype/

# 6. 性能分析
echo "6. 生成性能报告..."
go tool pprof -text cpu.prof > cpu-profile.txt
go tool pprof -png cpu.prof > cpu-flamegraph.png

echo "=== 测试完成 ==="
echo "查看结果："
echo "  - CPU profile: cpu.prof"
echo "  - 火焰图: cpu-flamegraph.png"
echo "  - 文本报告: cpu-profile.txt"
```

---

## 9. 下一步行动

1. **立即开始**：创建 `thoughts/prototype/` 目录
2. **Day 1-2**：实现简化版 PageReference 和 PageInfo
3. **Day 3-4**：运行性能基准测试，收集数据
4. **Day 5**：运行并发测试，验证安全性
5. **Day 6-7**：分析结果，做出决策

**输出物**：
- 测试代码（`thoughts/prototype/`）
- 测试报告（包含性能数据、火焰图、并发测试结果）
- 决策建议（继续 Phase 1 或启用备选方案）
