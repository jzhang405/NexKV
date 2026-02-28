# CPU 绑核性能分析与优化 - 完整指南

## 🎯 总结

本目录包含 NexKV CPU 绑核功能的完整性能分析工具、优化示例和使用文档。

---

## 📦 内容清单

### 1. 核心实现

| 文件 | 说明 |
|------|------|
| `affinity_init.go` | Uber automaxprocs 初始化 |
| `affinity_linux.go` | Linux CPU 绑核实现 (`sched_setaffinity`) |
| `affinity_windows.go` | Windows CPU 绑核实现 (`SetThreadAffinityMask`) |
| `affinity_darwin.go` | macOS 兼容性处理 |
| `executor_percore.go` | 启用绑核逻辑 |

### 2. 测试和分析

| 文件 | 说明 |
|------|------|
| `affinity_test.go` | 基础功能测试 |
| `affinity_perf_test.go` | **Perf 性能分析测试** |
| `affinity_optimization_example.go` | **优化示例对比** |

### 3. 脚本工具

| 脚本 | 功能 |
|------|------|
| `perf_analysis.sh` | **完整性能分析（自动对比）** |
| `cache_analysis.sh` | 缓存命中率快速分析 |

### 4. 文档

| 文档 | 说明 |
|------|------|
| `Quick_Start.md` | **5 分钟快速上手** |
| `CPU_Affinity_Perf_Guide.md` | **完整 Perf 分析指南** |
| `README.md` | 本文档 |

---

## 🚀 快速开始

### 步骤 1: 调整 Perf 权限

```bash
sudo sysctl -w kernel.perf_event_paranoid=1
```

### 步骤 2: 运行性能分析

**方式 A: 自动化分析（推荐）**
```bash
sudo ./scripts/perf_analysis.sh
```

**方式 B: 手动分析**
```bash
# 对比缓存命中率
perf stat -e cache-references,cache-misses,cycles \
  go test -bench="BenchmarkPerCore_WithAffinity" \
    -benchtime=5s ./internal/infrastructure/concurrency/ -run=^$
```

### 步骤 3: 查看优化建议

参考 `affinity_optimization_example.go` 中的对比示例：
- ✅ 核心本地数据（缓存友好）
- ❌ 共享内存（缓存竞争）
- ✅ 避免 False Sharing
- ❌ 未优化的 Counter

---

## 📊 性能测试结果

### 基准测试对比

| 场景 | 绑核 (ns/op) | 无绑核 (ns/op) | 性能差异 | 建议 |
|------|-------------|---------------|---------|------|
| **计算密集** | 112.9 | 122.4 | **+8.4% ✅** | ✅ 使用绑核 |
| **简单提交** | 227.2 | 208.5 | -9.0% ❌ | ❌ 禁用绑核 |
| **内存密集** | 189.2 | 172.3 | -9.9% ❌ | ⚠️ 需优化内存访问 |

### 预期 Perf 指标（优化后）

| 指标 | 优化前 | 优化后 | 说明 |
|------|--------|--------|------|
| 缓存未命中率 | 12-15% | 3-5% | ✅ 降低 70% |
| CPU 迁移 | 数千/秒 | 0 | ✅ 完全消除 |
| CPI | 1.8-2.2 | 1.2-1.5 | ✅ 降低 30% |

---

## 🔧 优化建议

### 1. 使用核心本地数据

```go
// ✅ 正确：每个 worker 独立数据
type CoreLocalWorker struct {
    coreID int
    data   [1024]byte
}
```

### 2. 避免 False Sharing

```go
// ✅ 正确：使用 padding 分离频繁修改的字段
type Counter struct {
    value1 int64
    _      [7]int64 // 填充一个缓存行
    value2 int64
}
```

### 3. 批量处理

```go
// ✅ 正确：积攒一批再处理
if len(batch) >= batchSize {
    mu.Lock()
    processBatch(batch)
    mu.Unlock()
}
```

---

## 📚 使用场景

### ✅ 推荐使用绑核的场景

1. **HLC 时钟更新** - 计算密集，低延迟要求
2. **WAL 写入** - 频繁操作，需要稳定性能
3. **副本同步** - 长时间运行的任务

### ❌ 不推荐使用绑核的场景

1. **简单异步任务** - 纯提交开销，系统调用成本高
2. **IO 密集型任务** - 瓶颈在 IO，CPU 绑核无效

---

## 🎓 学习资源

### Perf 工具

- [Perf Tutorial](https://www.brendangregg.com/perf.html)
- [Linux Performance Tools](http://www.brendangregg.com/linuxperf.html)

### CPU 亲和性

- [CPU Affinity Best Practices](https://www.kernel.org/doc/html/latest/scheduler/sched-design.html)
- [False Sharing](https://en.wikipedia.org/wiki/False_sharing)

---

## 📞 获取帮助

### 问题排查

**Q: perf 提示权限不足**
```bash
sudo sysctl -w kernel.perf_event_paranoid=1
```

**Q: 性能改进不明显**
- 增加测试时间（`-benchtime=10s`）
- 优化内存访问模式（参考 `affinity_optimization_example.go`）
- 选择合适的工作负载（计算密集型）

**Q: 如何生成火焰图**
```bash
perf script | FlameGraph/stackcollapse-perf.pl | \
  FlameGraph/flamegraph.pl > flamegraph.svg
```

---

## 📝 实现清单

- ✅ Linux CPU 绑核（`sched_setaffinity`）
- ✅ Windows CPU 绑核（`SetThreadAffinityMask`）
- ✅ macOS 兼容性处理
- ✅ Uber automaxprocs 集成
- ✅ 平台检测自动启用
- ✅ 性能测试和基准
- ✅ Perf 分析工具
- ✅ 优化示例代码
- ✅ 完整文档

---

**文档版本**: v1.0
**创建日期**: 2026-02-28
**维护者**: Core Team
**状态**: ✅ 完成并测试通过
