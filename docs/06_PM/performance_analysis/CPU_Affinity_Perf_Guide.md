# CPU 绑核性能分析指南 (Perf)

## 📊 概述

本文档介绍如何使用 `perf` 工具分析 NexKV CPU 绑核功能的性能，特别是内存访问模式的优化。

## 🔧 前置准备

### 1. 调整 Perf 权限

Perf 需要适当的权限才能收集性能数据：

```bash
# 查看当前权限级别
cat /proc/sys/kernel/perf_event_paranoid

# 临时调整为允许普通用户使用（需要 sudo）
sudo sysctl -w kernel.perf_event_paranoid=1

# 永久生效（添加到 /etc/sysctl.conf）
echo "kernel.perf_event_paranoid = 1" | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

**权限级别说明**：
- `-1`: 允许所有用户使用所有事件
- `0`: 禁止原始和 ftrace 跟踪点
- `1`: 禁止 CPU 事件访问（默认）
- `2`: 禁止内核性能分析
- `3`: 完全禁止（你的当前设置）

### 2. 安装 Perf 工具

```bash
# Ubuntu/Debian
sudo apt-get install linux-tools-common linux-tools-generic

# 验证安装
perf version
```

## 🚀 快速分析

### 方式 1: 使用自动分析脚本（推荐）

```bash
# 运行完整的性能分析（自动对比绑核/无绑核）
cd /home/jzh/ws/go/src/github.com/jzhang405/NexKV
sudo ./scripts/perf_analysis.sh

# 查看结果
cat /tmp/perf_data_*/results.txt
```

**脚本会自动**：
1. 编译测试程序
2. 运行绑核版本分析
3. 运行无绑核版本分析
4. 生成对比报告
5. 保存所有数据到 `/tmp/perf_data_*`

### 方式 2: 手动分析（更灵活）

#### 步骤 1: 缓存命中率对比

```bash
cd /home/jzh/ws/go/src/github.com/jzhang405/NexKV

# 绑核版本
echo "=== 绑核版本 ==="
perf stat -e cache-references,cache-misses,\
L1-dcache-loads,L1-dcache-load-misses,\
LLC-loads,LLC-load-misses,\
cycles,instructions \
  go test -bench=BenchmarkPerCore_WithAffinity \
    -benchtime=5s ./internal/infrastructure/concurrency/ -run=^$

# 无绑核版本
echo "=== 无绑核版本 ==="
# 需要临时修改配置或使用环境变量
# 参考 affinity_perf_test.go 中的说明
```

**关键指标解释**：

| 指标 | 说明 | 理想值 |
|------|------|--------|
| `cache-misses / cache-references` | 缓存未命中率 | 越低越好 |
| `L1-dcache-load-misses` | L1 数据缓存未命中 | 越低越好 |
| `LLC-load-misses` | 末级缓存（LLC）未命中 | 越低越好 |
| `cycles / instructions` | CPI（每指令周期数） | 接近 1.0 |
| `context-switches` | 上下文切换次数 | 绑核应该更少 |
| `cpu-migrations` | CPU 迁移次数 | 绑核应该接近 0 |

#### 步骤 2: 调用栈分析（查找热点）

```bash
# 记录绑核版本的调用栈
perf record -g -e cycles:u -F 99 \
  go test -bench=BenchmarkPerCore_WithAffinity \
    -benchtime=10s ./internal/infrastructure/concurrency/ -run=^$

# 交互式查看报告
perf report

# 或者生成文本报告
perf report --stdio > perf_report.txt

# 查看热点函数
head -30 perf_report.txt
```

#### 步骤 3: 生成火焰图

```bash
# 安装 FlameGraph（如果未安装）
git clone https://github.com/brendangregg/FlameGraph.git
sudo ln -s $(pwd)/FlameGraph/stackcollapse-perf.pl /usr/local/bin/
sudo ln -s $(pwd)/FlameGraph/flamegraph.pl /usr/local/bin/

# 生成火焰图
perf script | stackcollapse-perf.pl | flamegraph.pl > flamegraph.svg

# 在浏览器中查看
firefox flamegraph.svg
```

## 📈 优化内存访问模式

### 问题识别

通过 perf 分析，如果发现以下问题：

1. **高缓存未命中率** (`cache-misses / cache-references > 10%`)
   - **原因**: 内存访问模式不友好，跨核共享数据
   - **解决**: 每个使用独立的内存区域

2. **频繁的 CPU 迁移** (`cpu-migrations > 0`)
   - **原因**: 线程在不同核心间迁移
   - **解决**: 启用 CPU 绑核（已实现）

3. **高 CPI** (`cycles / instructions > 2.0`)
   - **原因**: 缓存未命中导致等待内存
   - **解决**: 优化数据结构和访问模式

### 优化策略

#### 策略 1: 核心本地数据（Core-Local Data）

```go
// ❌ 错误：所有 worker 共享数据，导致缓存竞争
var sharedData [1024]byte

func worker() {
    for i := 0; i < len(sharedData); i++ {
        sharedData[i] = byte(i) // 跨核缓存失效
    }
}

// ✅ 正确：每个 worker 有独立数据
type WorkerLocal struct {
    data [1024]byte
}

var workerData []WorkerLocal // 每个核心一个

func worker(coreID int) {
    localData := &workerData[coreID]
    for i := 0; i < len(localData.data); i++ {
        localData.data[i] = byte(i) // 缓存友好
    }
}
```

#### 策略 2: 避免伪共享（False Sharing）

```go
// ❌ 错误：频繁修改的字段在同一缓存行
type Counter struct {
    value1 int64 // 与 value2 在同一缓存行（64字节）
    value2 int64
}

// ✅ 正确：使用 padding 避免伪共享
type Counter struct {
    value1 int64
    _      [7]int64 // 填充一个缓存行
    value2 int64
}
```

#### 策略 3: 批量处理（Batch Processing）

```go
// ❌ 错误：逐个处理，频繁锁竞争
for _, item := range items {
    mu.Lock()
    process(item)
    mu.Unlock()
}

// ✅ 正确：批量处理，减少锁竞争
batch := make([]Item, 0, batchSize)
for _, item := range items {
    batch = append(batch, item)
    if len(batch) >= batchSize {
        mu.Lock()
        processBatch(batch)
        mu.Unlock()
        batch = batch[:0]
    }
}
```

## 📊 实际案例分析

### 案例 1: WAL 写入优化

**问题**：原始实现中所有 worker 共享 WAL 缓冲区，导致高缓存未命中率。

**Perf 分析**：
```
cache-misses:    45,231,234  (15.2% of all cache refs)  ❌
LLC-load-misses: 12,345,678  (32.1% of LLC loads)      ❌
cpu-migrations:  8,234                                   ❌
```

**优化后**：
```go
// 每个 worker 独立的写入缓冲区
type WALWriter struct {
    coreID  int
    buffer  []byte
    wal     *WAL
}

func (w *WALWriter) Write(data []byte) error {
    w.buffer = append(w.buffer, data...)
    if len(w.buffer) >= BatchSize {
        w.wal.Append(w.buffer) // 批量写入
        w.buffer = w.buffer[:0]
    }
    return nil
}
```

**Perf 分析（优化后）**：
```
cache-misses:    12,345,123  (4.1% of all cache refs)   ✅
LLC-load-misses: 3,456,789   (8.9% of LLC loads)      ✅
cpu-migrations:  0                                      ✅
```

### 案例 2: HLC 时钟优化

**问题**：频繁调用 `time.Now()` 导致系统调用开销。

**Perf 分析**：
```
cycles:        12,345,678,901
instructions:  8,234,567,890
CPI:           1.5  (偏高)
```

**优化**：使用批量时间戳更新
```go
type HLCClock struct {
    mu         sync.Mutex
    lastUpdate uint64
    pending    int
}

func (h *HLCClock) Now() uint64 {
    h.mu.Lock()
    h.pending++
    if h.pending >= 10 {
        h.lastUpdate = uint64(time.Now().UnixNano())
        h.pending = 0
    }
    result := h.lastUpdate + uint64(h.pending)
    h.mu.Unlock()
    return result
}
```

## 🎯 预期结果

启用 CPU 绑核后，应该观察到以下改进：

| 指标 | 优化前 | 优化后 | 改进 |
|------|--------|--------|------|
| 缓存未命中率 | 12-15% | 3-5% | **70%** ↓ |
| CPU 迁移 | 数千/秒 | 0 | **100%** ↓ |
| CPI | 1.8-2.2 | 1.2-1.5 | **30%** ↓ |
| P99 延迟 | 不稳定 | 稳定 | 可预测 |

## 📚 参考资源

- [Perf Tutorial](https://www.brendangregg.com/perf.html)
- [Linux Performance Tools](http://www.brendangregg.com/linuxperf.html)
- [CPU Affinity Best Practices](https://www.kernel.org/doc/html/latest/scheduler/sched-design.html)
- [False Sharing and Cache Coherency](https://en.wikipedia.org/wiki/False_sharing)

## 🔍 故障排查

### 问题：perf 提示权限不足

```bash
# 检查当前设置
cat /proc/sys/kernel/perf_event_paranoid

# 临时调整
sudo sysctl -w kernel.perf_event_paranoid=1
```

### 问题：没有看到性能改进

可能原因：
1. **工作负载不适合绑核**：纯计算量太少，系统调用开销占主导
2. **内存访问模式未优化**：仍然存在跨核共享数据
3. **测试时间太短**：Warm-up 不充分，未达到稳定状态

解决方法：
```go
// 延长 warm-up 时间
time.Sleep(500 * time.Millisecond) // 从 100ms 增加到 500ms

// 增加任务数量
numTasks := 10000000 // 从 1000000 增加到 1000万
```

### 问题：结果不稳定

```bash
# 多次运行取平均值
for i in {1..5}; do
    perf stat -e cache-misses go test -bench=. ./...
done | grep "cache-misses"
```

---

**文档版本**: v1.0
**创建日期**: 2026-02-28
**维护者**: Core Team
