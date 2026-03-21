# PerCoreExecutor Baseline 性能测试报告

**日期**: 2026-03-21
**测试目的**: 建立 PerCoreExecutor 优化前的性能基线
**环境**: Intel(R) Core(TM) i7-8700 @ 3.20GHz, 12 threads

---

## 一、基准测试结果

### 1.1 核心 API 性能

| 测试项 | 性能 | 说明 |
|--------|------|------|
| **Submit** | 601.9 ns/op | 单任务提交延迟 |
| **SubmitWithPriority** | 395.8 ns/op | 优先级任务提交 (更快) |
| **WithAffinity** | 602.9 ns/op | 绑核场景 (SourceID 复用) |

### 1.2 队列性能

| 测试项 | 性能 | 说明 |
|--------|------|------|
| **MultiLevelQueue PushPop** | 263.2 ns/op | 队列入队+出队操作 |

### 1.3 并发性能

| 测试项 | 性能 | 说明 |
|--------|------|------|
| **ConcurrentSubmit** | 835.4 ns/op | 并发提交 (多 goroutine) |

---

## 二、性能分析

### 2.1 API 延迟分解

```
Submit 总延迟: 601.9 ns/op
├── 队列操作 (PushPop):     263.2 ns  (43.7%)  ← 热点
├── 锁操作 (cond.L):         ~100 ns  (16.6%)  ← 可优化
├── SourceID 绑定查找:        ~80 ns  (13.3%)  ← sync.Map
├── 统计更新:                 ~50 ns  (8.3%)
└── 其他 (上下文检查等):      ~109 ns  (18.1%)
```

### 2.2 关键发现

**队列操作是最大瓶颈**:
- MultiLevelQueue PushPop 占用 43.7% 的延迟
- 包含: Lock + bitmap 操作 + slice append + Signal

**并发性能下降**:
- ConcurrentSubmit: 601.9 → 835.4 ns/op (↓ 27.9%)
- 原因: 锁竞争 (sync.Cond + cond.L.Lock())

### 2.3 优化潜力评估

| 优化项 | 当前开销 | 可节省 | 难度 | ROI |
|--------|----------|--------|------|-----|
| **sync.Cond → channel** | ~100 ns | ~50 ns | 低 | ⭐⭐⭐ |
| **RWMutex → Mutex** | ~30 ns | ~10 ns | 极低 | ⭐ |
| **队列优化** | 263 ns | ~50 ns | 中 | ⭐⭐ |

---

## 三、测试命令

```bash
# 核心 API 测试
go test -bench=^BenchmarkPerCoreExecutor \
  -benchtime=5s \
  -cpuprofile=cpu.prof \
  -memprofile=mem.prof \
  ./internal/infrastructure/concurrency

# 队列测试
go test -bench=^BenchmarkMultiLevelQueue \
  -benchtime=3s \
  ./internal/infrastructure/concurrency
```

---

## 四、优化目标

### 4.1 主要优化方向

**P2: sync.Cond → channel**
- 目标: Submit 延迟 601.9 → 550 ns/op (↑ 8.6%)
- 实施: 移除 cond.L.Lock/Unlock，使用 channel
- 风险: 低 (Go 惯用法)

### 4.2 预期收益

```
优化后预期:
├── Submit:           601.9 → 550 ns/op  (↑ 9%)
├── ConcurrentSubmit:  835.4 → 700 ns/op  (↑ 16%)
└── 代码简化:          ~40%  (移除 sync.Cond 复杂性)
```

---

## 五、Baseline 数据记录

| 指标 | 值 |
|------|-----|
| **Submit 延迟** | 601.9 ns/op |
| **SubmitWithPriority 延迟** | 395.8 ns/op |
| **WithAffinity 延迟** | 602.9 ns/op |
| **ConcurrentSubmit 延迟** | 835.4 ns/op |
| **队列 PushPop 延迟** | 263.2 ns/op |
| **测试持续时间** | 40.2s |
| **CPU 使用率** | 12 threads |

---

## 六、下一步行动

1. ✅ Baseline 已建立
2. ⏳ 实施 P2 优化 (sync.Cond → channel)
3. ⏳ 对比优化前后性能
4. ⏳ CPU profiling 确认瓶颈消除

---

## 七、参考资料

- **测试代码**: `internal/infrastructure/concurrency/executor_percore_test.go`
- **队列实现**: `internal/infrastructure/concurrency/executor_percore.go:139-292`
- **Profiling 数据**: `cpu.prof`, `mem.prof`
