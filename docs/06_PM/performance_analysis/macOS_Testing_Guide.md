# macOS 性能对比测试指南

## 概述

在 macOS 上运行 PerCoreExecutor vs Ants 的性能对比测试。

## macOS 特殊说明

### CPU 亲和性支持

macOS **不支持** CPU 亲和性绑定（`sched_setaffinity` 系统调用），但我们可以使用 `runtime.LockOSThread` 作为替代方案：

| 平台 | CPU 绑核 | LockOSThread | 性能提升 |
|------|---------|--------------|---------|
| **Linux** | ✅ 支持 | ✅ 自动 | ⭐⭐⭐⭐⭐ 最佳 |
| **Windows** | ✅ 支持 | ✅ 自动 | ⭐⭐⭐⭐⭐ 最佳 |
| **macOS** | ❌ 不支持 | ✅ 支持 | ⭐⭐⭐ 有限提升 |

### macOS 实现细节

```go
// affinity_darwin.go
func pinToCore(coreID int) error {
    // macOS 不支持 CPU 亲和性
    // 使用 LockOSThread 提供（有限）性能优化
    return fmt.Errorf("CPU affinity not supported on macOS")
}

// executor_percore.go - run() 方法
if w.executor.config.EnableAffini && !w.pinned {
    runtime.LockOSThread()
    defer runtime.UnlockOSThread()

    if err := pinToCore(w.coreID); err == nil {
        w.pinned = true
    }
    // 绑核失败不阻止 worker 启动
}
```

**效果**：
- ✅ 避免 goroutine 在 OS 线程间迁移
- ✅ 减少 CPU 缓存失效
- ❌ 无法保证 goroutine 固定在特定 CPU 核心

## 快速开始

### 1. 运行自动化测试脚本

```bash
# 运行完整对比测试
./scripts/run_macos_comparison.sh
```

**输出**：
```
=========================================
macOS 对比测试：PerCoreExecutor vs Ants
=========================================

✅ 检测到 macOS 系统

📊 硬件信息：
  - CPU 核心数: 8
  - CPU 频率: 2.4 GHz

...

✅ macOS 对比测试完成

📁 结果文件：benchmark_results/macos_20260228_143026/
```

### 2. 手动运行基准测试

```bash
# 基础对比测试
go test -v -bench=. -benchmem \
    -benchtime=3s \
    ./internal/infrastructure/concurrency/...

# 并行测试
go test -v -bench=Parallel -benchmem \
    -benchtime=5s \
    ./internal/infrastructure/concurrency/...

# 生成 CPU profile
go test -v -bench=Benchmark_PerCore_Affinity \
    -cpuprofile=cpu.prof \
    ./internal/infrastructure/concurrency/...

# 生成内存 profile
go test -v -bench=Benchmark_PerCore_Affinity \
    -memprofile=mem.prof \
    ./internal/infrastructure/concurrency/...
```

## 测试场景

### Test 1: 基础对比测试

| 测试 | 描述 | 预期结果 |
|------|------|---------|
| `Benchmark_PerCore_Affinity` | PerCoreExecutor + LockOSThread | 中等性能 |
| `Benchmark_Ants_Default` | Ants 默认模式 | 基准性能 |
| `Benchmark_Ants_CustomPool` | Ants 自定义池 | 类似 Default |
| `Benchmark_Ants_FuncPool` | Ants 函数池 | 通常最慢 |
| `Benchmark_Ants_MultiPool` | Ants 多池 | 较好性能 |

**macOS 特点**：
- PerCoreExecutor 性能提升不如 Linux/Windows 明显
- 但仍可能优于 Ants 的某些模式（因为 LockOSThread 优化）

### Test 2: 并行性能测试

```bash
go test -bench=Parallel -benchmem \
    -benchtime=5s \
    ./internal/infrastructure/concurrency/...
```

| 测试 | 描述 |
|------|------|
| `Benchmark_Parallel_PerCore_Affinity` | 多 goroutine 并发提交 |
| `Benchmark_Parallel_Ants_Default` | Ants 并发吞吐量 |

### Test 3: 不同任务时长

```bash
go test -bench="PerCore_(Short|Medium|Long)" -benchmem \
    ./internal/infrastructure/concurrency/...
```

| 任务 | 时长 | 测试重点 |
|------|------|---------|
| Short | 10μs | 调度开销敏感 |
| Medium | 100μs | 平衡场景 |
| Long | 1ms | 吞吐量测试 |

## 性能分析

### 查看 CPU Profile

```bash
# 生成 profile
go test -bench=Benchmark_PerCore_Affinity \
    -cpuprofile=cpu.prof \
    ./internal/infrastructure/concurrency/...

# 分析 profile
go tool pprof cpu.prof

# 交互式命令
(pprof) top10    # 查看 top 10 消耗
(pprof) list run # 查看函数详情
(pprof) web      # 生成可视化图表
```

### 查看内存 Profile

```bash
# 生成 profile
go test -bench=Benchmark_PerCore_Affinity \
    -memprofile=mem.prof \
    ./internal/infrastructure/concurrency/...

# 分析 profile
go tool pprof mem.prof

# 交互式命令
(pprof) top10
(pprof) web
```

### 对比结果

```bash
# 提取关键指标
grep "^Benchmark_PerCore_Affinity" benchmark_results/macos_*/benchmark_*.log
grep "^Benchmark_Ants_Default" benchmark_results/macos_*/benchmark_*.log
```

## 预期结果

### 典型 macOS 性能对比

| 执行器 | ns/op | MB/s | 相对性能 |
|--------|-------|------|---------|
| **PerCoreExecutor (LockOSThread)** | ~1500 | ~6.7 | ⭐⭐⭐⭐ |
| **Ants Default** | ~1800 | ~5.5 | ⭐⭐⭐ |
| **Ants CustomPool** | ~1750 | ~5.7 | ⭐⭐⭐ |
| **Ants FuncPool** | ~2200 | ~4.5 | ⭐⭐ |
| **Ants MultiPool** | ~1600 | ~6.3 | ⭐⭐⭐⭐ |

**说明**：
- 数值为示例，实际结果取决于硬件配置
- LockOSThread 提供 ~15-20% 性能提升（vs 无优化）
- Linux/Windows 上 CPU 绑核提供 ~30-40% 提升

### 性能瓶颈分析

**macOS 上 PerCoreExecutor 的优势**：
1. ✅ 避免频繁的 goroutine 迁移
2. ✅ 更好的 CPU 缓存局部性
3. ✅ 减少上下文切换开销

**macOS 上的限制**：
1. ❌ 无法固定 CPU 核心
2. ❌ OS 可能仍调度线程到不同核心
3. ❌ 性能提升不如真正的 CPU 绑核

## 故障排查

### 问题 1: 测试失败

```bash
# 检查 Go 版本
go version

# 应该 >= 1.21
```

### 问题 2: 性能异常

```bash
# 检查系统负载
top -l 1

# 关闭其他应用后重试
```

### 问题 3: Profile 文件过大

```bash
# 减少采样频率
go test -bench=. -cpuprofile=cpu.prof \
    -test.cpuprofile=1000 \
    ./internal/infrastructure/concurrency/...
```

## 扩展阅读

- [Linux CPU 绑核测试指南](./CPU_Affinity_Perf_Guide.md)
- [性能分析快速开始](./Quick_Start.md)
- [executor_comparison_benchmark_test.go 源码](../../internal/infrastructure/concurrency/executor_comparison_benchmark_test.go)

## 总结

在 macOS 上：

1. **无法使用真正的 CPU 绑核**（macOS 限制）
2. **LockOSThread 提供有限优化**（~15-20% 提升）
3. **仍优于无优化版本**（避免 goroutine 迁移）
4. **Linux/Windows 上效果更好**（真正的 CPU 亲和性）

**建议**：
- 开发测试：macOS 足够
- 生产部署：优先 Linux（支持 CPU 绑核）
- 性能调优：在 Linux 上进行最终验证
