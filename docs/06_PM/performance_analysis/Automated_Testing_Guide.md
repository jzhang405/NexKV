# 自动化测试脚本指南

## 快速开始

### 一键运行所有测试

```bash
./scripts/run_macos_comparison.sh
```

### 测试内容

脚本自动运行以下测试：

1. **基础对比测试**（5次迭代）
   - PerCoreExecutor (LockOSThread)
   - Ants Default/Custom/Func/Multi Pool

2. **并行性能测试**
   - 多goroutine并发提交
   - 吞吐量测试

3. **不同任务时长测试**
   - Short: 10μs
   - Medium: 100μs
   - Long: 1ms

### 输出结果

```
benchmark_results/macos_20260228_143026/
├── benchmark_1.log          # 第1次迭代结果
├── benchmark_2.log          # 第2次迭代结果
├── ...
├── parallel_test.log        # 并行测试结果
├── task_duration_test.log   # 任务时长测试结果
├── cpu_1.prof               # CPU profile
└── mem_1.prof               # 内存 profile
```

## 手动运行

### 基础测试
```bash
go test -v -bench=. -benchmem -benchtime=3s \
    ./internal/infrastructure/concurrency/...
```

### 并行测试
```bash
go test -v -bench=Parallel -benchmem -benchtime=5s \
    ./internal/infrastructure/concurrency/...
```

### 生成Profile
```bash
# CPU profile
go test -bench=Benchmark_PerCore_Affinity \
    -cpuprofile=cpu.prof \
    ./internal/infrastructure/concurrency/...

# 内存 profile
go test -bench=Benchmark_PerCore_Affinity \
    -memprofile=mem.prof \
    ./internal/infrastructure/concurrency/...
```

## 性能分析

### 查看Profile
```bash
go tool pprof cpu.prof
(pprof) top10    # Top 10消耗
(pprof) web      # 可视化
```

### 对比结果
```bash
# 提取PerCoreExecutor性能
grep "Benchmark_PerCore_Affinity" benchmark_results/macos_*/benchmark_*.log

# 提取Ants性能
grep "Benchmark_Ants_Default" benchmark_results/macos_*/benchmark_*.log
```

## 预期结果

```
Benchmark_PerCore_Affinity-8      1500 ns/op    6.7 MB/s
Benchmark_Ants_Default-8          1800 ns/op    5.5 MB/s
Benchmark_Ants_FuncPool-8         2200 ns/op    4.5 MB/s
```

## macOS vs Linux

| 平台 | CPU绑核 | LockOSThread | 性能提升 |
|------|--------|--------------|---------|
| macOS | ❌ | ✅ | ~15-20% |
| Linux | ✅ | ✅ | ~30-40% |

## 故障排查

### 测试失败
```bash
# 检查Go版本
go version  # 需要 >= 1.21
```

### 性能异常
```bash
# 检查系统负载
top -l 1
```

## 相关文档

- [完整测试指南](./macOS_Testing_Guide.md)
- [快速参考](./macOS_QuickRef.md)
- [测试脚本](../../scripts/run_macos_comparison.sh)
