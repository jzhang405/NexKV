# RPC 性能测试方案 - V4 改造前后对比

> **测试日期**: 2026-03-05
> **测试目标**: 对比 RPC 层改造前后的性能差异
> **测试范围**: Transport RPC 层
> **优先级**: P1

---

## 1. 测试目标

### 1.1 核心指标

| 指标 | 说明 | 目标改进 |
|------|------|---------|
| **延迟 (Latency)** | P50/P99 响应时间 | 降低 10-20% |
| **吞吐 (Throughput)** | 每秒请求数 (QPS) | 提升 20-30% |
| **CPU 亲和性** | 同 SourceID 任务在同一 Worker | 提升 15% |
| **内存分配** | 每次请求的内存分配 | 减少 20% |
| **Goroutine 数量** | 并发时的 goroutine 数 | 减少 30% |

### 1.2 测试场景

| 场景 | 描述 | 重要性 |
|------|------|--------|
| **单节点点对点** | 1 Client → 1 Server | 基准测试 |
| **广播发送** | 1 → N 广播 | 核心场景 |
| **并发请求** | 多 Client 并发 | 压力测试 |
| **异步回调** | AsyncOp 回调性能 | 稳定性 |

---

## 2. 测试环境

### 2.1 硬件环境

```yaml
CPU: 8 cores / 16 threads
Memory: 16GB
Network: localhost (排除网络变量)
OS: Linux 5.15
Go: 1.21+
```

### 2.2 软件配置

```go
// Executor 配置
AntsPoolExecutor:
  - MaxWorkers: 100
  - QueueSize: 1000

PerCoreExecutor:
  - WorkersPerCore: 2
  - QueueSizePerWorker: 100
```

---

## 3. 测试代码

### 3.1 基准测试框架

```go
// rpc_benchmark_test.go
package rpc_test

import (
    "context"
    "testing"
    "time"
    "sync"
    "sync/atomic"
)

// BenchmarkSuite 测试套件
type BenchmarkSuite struct {
    name      string
    setup     func() RPCManager
    teardown  func()
}

// Run 执行测试套件
func (s *BenchmarkSuite) Run(b *testing.B) {
    b.Run(s.name, func(b *testing.B) {
        manager := s.setup()
        defer s.teardown()
        
        b.ResetTimer()
        for i := 0; i < b.N; i++ {
            // 执行测试逻辑
        }
    })
}
```

### 3.2 场景 1：单节点点对点延迟

```go
// 改造前：使用 AsyncOp + GoroutineProvider
func BenchmarkRPCCallAsync_Old(b *testing.B) {
    rpc := setupOldRPC()
    defer rpc.Close()
    
    ctx := context.Background()
    peerID := model.PeerID("test-peer")
    req := &model.Message{
        Type: model.MsgTypePing,
        Data: make([]byte, 1024), // 1KB payload
    }
    
    b.ResetTimer()
    b.ReportAllocs()
    
    for i := 0; i < b.N; i++ {
        asyncOp := rpc.CallAsync(ctx, peerID, req)
        _, err := asyncOp.Await(ctx)
        if err != nil {
            b.Fatal(err)
        }
    }
    
    b.ReportMetric(float64(b.Elapsed())/float64(b.N), "ns/op")
}

// 改造后：使用 Task[Result] + Pipeline
func BenchmarkRPCCallAsync_New(b *testing.B) {
    rpc := setupNewRPC()
    defer rpc.Close()
    
    ctx := context.Background()
    peerID := model.PeerID("test-peer")
    req := &model.Message{
        Type: model.MsgTypePing,
        Data: make([]byte, 1024),
    }
    
    b.ResetTimer()
    b.ReportAllocs()
    
    for i := 0; i < b.N; i++ {
        asyncOp := rpc.CallAsync(ctx, peerID, req)
        _, err := asyncOp.Await(ctx)
        if err != nil {
            b.Fatal(err)
        }
    }
    
    b.ReportMetric(float64(b.Elapsed())/float64(b.N), "ns/op")
}
```

### 3.3 场景 2：广播发送吞吐

```go
// 改造前：信号量 + Submit
func BenchmarkRPCBroadcast_Old(b *testing.B) {
    rpc := setupOldRPC()
    defer rpc.Close()
    
    ctx := context.Background()
    peers := generatePeers(100) // 100 个 peer
    req := &model.Message{
        Type: model.MsgTypeBroadcast,
        Data: make([]byte, 512),
    }
    
    b.ResetTimer()
    b.ReportAllocs()
    
    for i := 0; i < b.N; i++ {
        err := rpc.Broadcast(ctx, peers, req)
        if err != nil {
            b.Fatal(err)
        }
    }
}

// 改造后：批量提交
func BenchmarkRPCBroadcast_New(b *testing.B) {
    rpc := setupNewRPC()
    defer rpc.Close()
    
    ctx := context.Background()
    peers := generatePeers(100)
    req := &model.Message{
        Type: model.MsgTypeBroadcast,
        Data: make([]byte, 512),
    }
    
    b.ResetTimer()
    b.ReportAllocs()
    
    for i := 0; i < b.N; i++ {
        asyncOp := rpc.BroadcastAsync(ctx, peers, req)
        _, err := asyncOp.Await(ctx)
        if err != nil {
            b.Fatal(err)
        }
    }
}
```

### 3.4 场景 3：并发压力测试

```go
// 改造前：并发控制
func BenchmarkRPCConcurrent_Old(b *testing.B) {
    rpc := setupOldRPC()
    defer rpc.Close()
    
    ctx := context.Background()
    peerID := model.PeerID("test-peer")
    req := &model.Message{Type: model.MsgTypePing, Data: make([]byte, 256)}
    
    concurrency := 100 // 100 并发
    
    b.ResetTimer()
    b.ReportAllocs()
    
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            asyncOp := rpc.CallAsync(ctx, peerID, req)
            _, err := asyncOp.Await(ctx)
            if err != nil {
                b.Fatal(err)
            }
        }
    })
}

// 改造后：并发控制
func BenchmarkRPCConcurrent_New(b *testing.B) {
    rpc := setupNewRPC()
    defer rpc.Close()
    
    ctx := context.Background()
    peerID := model.PeerID("test-peer")
    req := &model.Message{Type: model.MsgTypePing, Data: make([]byte, 256)}
    
    b.ResetTimer()
    b.ReportAllocs()
    
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            asyncOp := rpc.CallAsync(ctx, peerID, req)
            _, err := asyncOp.Await(ctx)
            if err != nil {
                b.Fatal(err)
            }
        }
    })
}
```

### 3.5 场景 4：异步回调性能

```go
// 改造前：回调执行不一致
func BenchmarkRPCCallback_Old(b *testing.B) {
    rpc := setupOldRPC()
    defer rpc.Close()
    
    ctx := context.Background()
    peerID := model.PeerID("test-peer")
    req := &model.Message{Type: model.MsgTypePing, Data: make([]byte, 128)}
    
    callbackCount := int64(0)
    
    b.ResetTimer()
    b.ReportAllocs()
    
    for i := 0; i < b.N; i++ {
        asyncOp := rpc.CallAsync(ctx, peerID, req)
        asyncOp.OnComplete(func(resp ResponseMsg, err error) {
            atomic.AddInt64(&callbackCount, 1)
        })
    }
    
    // 等待所有回调完成
    time.Sleep(time.Second)
    b.ReportMetric(float64(callbackCount), "callbacks")
}

// 改造后：统一走 Executor
func BenchmarkRPCCallback_New(b *testing.B) {
    rpc := setupNewRPC()
    defer rpc.Close()
    
    ctx := context.Background()
    peerID := model.PeerID("test-peer")
    req := &model.Message{Type: model.MsgTypePing, Data: make([]byte, 128)}
    
    callbackCount := int64(0)
    
    b.ResetTimer()
    b.ReportAllocs()
    
    for i := 0; i < b.N; i++ {
        asyncOp := rpc.CallAsync(ctx, peerID, req)
        asyncOp.OnComplete(func(resp ResponseMsg, err error) {
            atomic.AddInt64(&callbackCount, 1)
        })
    }
    
    time.Sleep(time.Second)
    b.ReportMetric(float64(callbackCount), "callbacks")
}
```

### 3.6 场景 5：CPU 亲和性测试

```go
// 改造前：全部使用 SourceNetwork
func BenchmarkRPCAffinity_Old(b *testing.B) {
    rpc := setupOldRPC()
    defer rpc.Close()
    
    ctx := context.Background()
    
    // 模拟 10 个分片的请求
    for i := 0; i < b.N; i++ {
        shardID := i % 10
        req := &model.Message{
            Type:   model.MsgTypeRaft,
            ShardID: uint64(shardID),
        }
        
        // 旧实现：全部使用 SourceNetwork，无亲和性
        asyncOp := rpc.CallAsync(ctx, peerID, req)
        _, _ = asyncOp.Await(ctx)
    }
}

// 改造后：按分片选择 SourceID
func BenchmarkRPCAffinity_New(b *testing.B) {
    rpc := setupNewRPC()
    defer rpc.Close()
    
    ctx := context.Background()
    
    for i := 0; i < b.N; i++ {
        shardID := i % 10
        req := &model.Message{
            Type:    model.MsgTypeRaft,
            ShardID: uint64(shardID),
        }
        
        // 新实现：按分片选择 SourceID，有亲和性
        asyncOp := rpc.CallWithAffinity(ctx, peerID, req)
        _, _ = asyncOp.Await(ctx)
    }
}

// 验证亲和性效果
func TestRPCAffinityEffect(t *testing.T) {
    rpc := setupNewRPC()
    defer rpc.Close()
    
    // 记录每个 SourceID 被分配到哪个 Worker
    workerAssignments := make(map[model.SourceID]int)
    var mu sync.Mutex
    
    // 发送 1000 个请求，每个分片 100 个
    for shardID := 0; shardID < 10; shardID++ {
        for i := 0; i < 100; i++ {
            req := &model.Message{
                Type:    model.MsgTypeRaft,
                ShardID: uint64(shardID),
            }
            
            asyncOp := rpc.CallWithAffinity(context.Background(), peerID, req)
            resp, _ := asyncOp.Await(context.Background())
            
            // 记录 Worker ID
            mu.Lock()
            workerAssignments[model.SourceShard(shardID)] = resp.WorkerID
            mu.Unlock()
        }
    }
    
    // 验证：同一分片的所有请求应该在同一 Worker
    for shardID := 0; shardID < 10; shardID++ {
        sourceID := model.SourceShard(shardID)
        workerID := workerAssignments[sourceID]
        
        // 检查是否所有该分片的请求都在同一 Worker
        t.Logf("Shard %d -> Worker %d", shardID, workerID)
    }
}
```

---

## 4. 测试执行

### 4.1 运行所有测试

```bash
# 运行所有基准测试
go test -bench=. -benchmem -count=5 ./internal/transport/rpc/... > benchmark_results.txt

# 只运行改造前测试
go test -bench="Old" -benchmem -count=5 ./internal/transport/rpc/...

# 只运行改造后测试
go test -bench="New" -benchmem -count=5 ./internal/transport/rpc/...
```

### 4.2 性能对比工具

```go
// compare_benchmarks.go
package main

import (
    "fmt"
    "os"
    "strings"
    "strconv"
)

// BenchmarkResult 测试结果
type BenchmarkResult struct {
    Name        string
    NsPerOp     float64
    AllocsPerOp int64
    BytesPerOp  int64
}

func main() {
    oldResults := parseBenchmarkFile("benchmark_old.txt")
    newResults := parseBenchmarkFile("benchmark_new.txt")
    
    fmt.Println("═══════════════════════════════════════════════════════════")
    fmt.Println("           RPC 性能对比报告")
    fmt.Println("═══════════════════════════════════════════════════════════")
    fmt.Println()
    
    for name, old := range oldResults {
        if new, ok := newResults[name]; ok {
            compareAndPrint(name, old, new)
        }
    }
}

func compareAndPrint(name string, old, new BenchmarkResult) {
    latencyImprovement := ((old.NsPerOp - new.NsPerOp) / old.NsPerOp) * 100
    allocImprovement := ((old.AllocsPerOp - new.AllocsPerOp) / old.AllocsPerOp) * 100
    
    fmt.Printf("测试场景: %s\n", name)
    fmt.Printf("  延迟: %.2f ns/op → %.2f ns/op (%.1f%% %s)\n",
        old.NsPerOp, new.NsPerOp,
        latencyImprovement,
        improvementText(latencyImprovement))
    fmt.Printf("  内存分配: %d allocs/op → %d allocs/op (%.1f%% %s)\n",
        old.AllocsPerOp, new.AllocsPerOp,
        allocImprovement,
        improvementText(allocImprovement))
    fmt.Println()
}

func improvementText(percent float64) string {
    if percent > 0 {
        return fmt.Sprintf("提升 %.1f%%", percent)
    }
    return fmt.Sprintf("下降 %.1f%%", -percent)
}
```

---

## 5. 预期结果

### 5.1 改造前基准

| 场景 | 延迟 (P50) | 延迟 (P99) | QPS | 内存分配 |
|------|-----------|-----------|-----|---------|
| 点对点 | 500μs | 2ms | 2000 | 5 allocs/op |
| 广播 (100) | 50ms | 100ms | 20 | 500 allocs/op |
| 并发 (100) | 1ms | 5ms | 5000 | 8 allocs/op |
| 回调 | - | - | 10000 | 3 allocs/op |

### 5.2 改造后目标

| 场景 | 延迟 (P50) | 延迟 (P99) | QPS | 内存分配 | 改进 |
|------|-----------|-----------|-----|---------|------|
| 点对点 | 400μs | 1.6ms | 2500 | 4 allocs/op | +25% |
| 广播 (100) | 40ms | 80ms | 25 | 400 allocs/op | +25% |
| 并发 (100) | 800μs | 4ms | 6000 | 6 allocs/op | +20% |
| 回调 | - | - | 12000 | 2 allocs/op | +20% |

### 5.3 成功标准

- [ ] 点对点延迟降低 ≥ 10%
- [ ] 广播吞吐提升 ≥ 20%
- [ ] 内存分配减少 ≥ 15%
- [ ] CPU 亲和性验证通过

---

## 6. 报告模板

```markdown
# RPC 性能测试报告

## 测试信息
- 测试日期: 2026-03-XX
- 测试版本: 改造前 (commit: xxx) vs 改造后 (commit: yyy)
- 测试环境: 8C16G, Go 1.21

## 测试结果

### 1. 点对点延迟
| 指标 | 改造前 | 改造后 | 改进 |
|------|-------|-------|------|
| P50 | 500μs | 400μs | -20% ✅ |
| P99 | 2ms | 1.6ms | -20% ✅ |

### 2. 广播吞吐
| 指标 | 改造前 | 改造后 | 改进 |
|------|-------|-------|------|
| QPS | 20 | 25 | +25% ✅ |

### 3. 内存分配
| 指标 | 改造前 | 改造后 | 改进 |
|------|-------|-------|------|
| allocs/op | 5 | 4 | -20% ✅ |

## 结论
改造达到预期目标，建议合并。
```

---

**文档版本**: v1.0
**最后更新**: 2026-03-05
