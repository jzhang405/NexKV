# macOS 对比测试 - 快速参考

## 一键运行

```bash
./scripts/run_macos_comparison.sh
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

### CPU Profile
```bash
go test -bench=Benchmark_PerCore_Affinity \
    -cpuprofile=cpu.prof \
    ./internal/infrastructure/concurrency/...

go tool pprof cpu.prof
```

## macOS vs Linux 对比

| 特性 | macOS | Linux |
|------|-------|-------|
| **CPU 绑核** | ❌ 不支持 | ✅ sched_setaffinity |
| **LockOSThread** | ✅ 支持 | ✅ 自动 |
| **性能提升** | ~15-20% | ~30-40% |
| **适用场景** | 开发测试 | 生产部署 |

## 预期结果

```
Benchmark_PerCore_Affinity-8      1500 ns/op
Benchmark_Ants_Default-8          1800 ns/op
Benchmark_Ants_FuncPool-8         2200 ns/op
```

**说明**：
- macOS 上 PerCoreExecutor 使用 LockOSThread
- 性能提升不如 Linux 明显，但仍优于 Ants

## 关键文件

- 📄 [完整测试指南](./macOS_Testing_Guide.md)
- 🔧 [测试脚本](../../scripts/run_macos_comparison.sh)
- 📊 [源码](../../internal/infrastructure/concurrency/executor_comparison_benchmark_test.go)
