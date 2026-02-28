# CPU 绑核优化快速使用指南

## 📋 内容目录

1. [Perf 权限设置](#1-perf-权限设置)
2. [快速分析命令](#2-快速分析命令)
3. [优化建议](#3-优化建议)
4. [实际案例](#4-实际案例)

---

## 1. Perf 权限设置

### 检查当前权限

```bash
cat /proc/sys/kernel/perf_event_paranoid
```

**输出为 4（当前值）**：需要调整

### 临时调整权限（推荐）

```bash
sudo sysctl -w kernel.perf_event_paranoid=1
```

### 永久生效

```bash
echo "kernel.perf_event_paranoid = 1" | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

---

## 2. 快速分析命令

### 方式 A: 缓存命中率对比

```bash
cd /home/jzh/ws/go/src/github.com/jzhang405/NexKV

# 绑核版本
echo "=== 绑核版本 ==="
perf stat -e cache-references,cache-misses,L1-dcache-loads,L1-dcache-load-misses,cycles,instructions \
  go test -bench="BenchmarkPerCoreExecutor_WithAffinity_SimulatedWorkload" \
    -benchtime=5s ./internal/infrastructure/concurrency/ -run=^$

# 无绑核版本
echo "=== 无绑核版本 ==="
perf stat -e cache-references,cache-misses,L1-dcache-loads,L1-dcache-load-misses,cycles,instructions \
  go test -bench="BenchmarkPerCoreExecutor_WithoutAffinity_SimulatedWorkload" \
    -benchtime=5s ./internal/infrastructure/concurrency/ -run=^$
```

**关键指标**：
- `cache-misses %`：越低越好（理想 < 5%）
- `cycles / instructions`：越接近 1.0 越好

### 方式 B: 内存访问模式对比

```bash
# 共享内存（性能差）
perf stat -e cache-misses,cycles \
  go test -bench="BenchmarkBadSharedData" \
    -benchtime=5s ./internal/infrastructure/concurrency/ -run=^$

# 核心本地数据（性能好）
perf stat -e cache-misses,cycles \
  go test -bench="BenchmarkGoodCoreLocalData" \
    -benchtime=5s ./internal/infrastructure/concurrency/ -run=^$
```

**预期结果**：`BenchmarkGoodCoreLocalData` 应该有更低的 `cache-misses`

### 方式 C: 自动化完整分析

```bash
# 使用包装脚本（推荐 - 自动处理环境变量）
./scripts/run_perf_analysis.sh

# 或者手动设置环境后运行
export GOROOT=/home/jzh/go
export GOPATH=/home/jzh/ws/go
export PATH=$GOROOT/bin:$PATH
sudo -E ./scripts/perf_analysis.sh

# 查看结果
cat /tmp/perf_data_*/results.txt
```

**注意**：脚本需要 sudo 权限来使用 perf，使用 `sudo -E` 保留环境变量。

---

## 3. 优化建议

### ✅ 推荐做法

1. **使用核心本地数据**
   ```go
   // 每个 worker 有独立的数据区域
   type WorkerLocal struct {
       data [1024]byte
   }
   ```

2. **避免伪共享**
   ```go
   // 使用 padding 分离频繁修改的字段
   type Counter struct {
       value1 int64
       _      [7]int64 // 填充一个缓存行
       value2 int64
   }
   ```

3. **批量处理**
   ```go
   // 积累到一定量再处理，减少锁竞争
   if len(batch) >= batchSize {
       mu.Lock()
       processBatch(batch)
       mu.Unlock()
   }
   ```

### ❌ 避免的做法

1. ❌ 所有 worker 共享数据
2. ❌ 频繁加锁/解锁
3. ❌ 逐个处理小任务

---

## 4. 实际案例

### 案例：WAL 写入优化

**优化前**（共享缓冲区）：
```go
type BadWALWriter struct {
    mu     sync.Mutex
    buffer []byte  // 所有 worker 共享
}
```

**优化后**（独立缓冲区）：
```go
type GoodWALWriter struct {
    coreID int
    buffer []byte  // 每个 worker 独立
}

type WALWriterManager struct {
    writers []*GoodWALWriter  // 每个核心一个
}
```

**性能对比**：
- 缓存未命中率：从 15% → 4%
- 吞吐量：提升 40%

---

## 📚 相关文档

- **详细指南**: `docs/06_PM/performance_analysis/CPU_Affinity_Perf_Guide.md`
- **优化示例**: `internal/infrastructure/concurrency/affinity_optimization_example.go`
- **分析脚本**: `scripts/perf_analysis.sh`
- **缓存分析**: `scripts/cache_analysis.sh`

---

## 🔧 故障排查

### 问题：perf 提示权限不足

```bash
# 查看当前设置
cat /proc/sys/kernel/perf_event_paranoid

# 调整为 1（需要 sudo）
sudo sysctl -w kernel.perf_event_paranoid=1
```

### 问题：性能改进不明显

**可能原因**：
1. 测试时间太短 → 增加到 10s
2. Warm-up 不充分 → 增加 sleep 时间
3. 工作负载不适合绑核 → 选择计算密集型任务

**解决方法**：
```go
// 延长 warm-up
time.Sleep(500 * time.Millisecond) // 从 100ms 增加

// 增加任务数量
numTasks := 10000000 // 从 1000000 增加
```

---

**文档版本**: v1.0
**最后更新**: 2026-02-28
