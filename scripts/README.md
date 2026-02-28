# 自动化测试脚本

## macOS 对比测试

```bash
./scripts/run_macos_comparison.sh
```

### 测试内容

- PerCoreExecutor (LockOSThread) vs Ants 各模式
- 并发性能测试
- 不同任务时长测试

### 结果输出

```
benchmark_results/macos_*/
├── benchmark_*.log       # 基准测试结果
├── parallel_test.log     # 并行测试结果
├── cpu_*.prof            # CPU profile
└── mem_*.prof            # 内存 profile
```

## 手动运行

```bash
# 基础测试
go test -v -bench=. -benchmem -benchtime=3s \
    ./internal/infrastructure/concurrency/...

# 并行测试
go test -v -bench=Parallel -benchmem -benchtime=5s \
    ./internal/infrastructure/concurrency/...
```

## 性能分析

```bash
go tool pprof cpu.prof
```

## 其他脚本

- `scripts/cache_analysis.sh` - 缓存分析
- `scripts/perf_analysis.sh` - 性能分析
- `scripts/run_perf_analysis.sh` - 运行性能分析
