# Porcupine 线性一致性验证集成

本模块为 NexKV 提供 Porcupine 线性一致性验证集成，用于验证分布式一致性协议的正确性。

## 概述

[Porcupine](https://github.com/anishathalye/porcupine) 是一个 Go 语言的线性一致性检查器，被 etcd、TiDB、MemoryDB 等知名项目使用。本模块将 Porcupine 集成到 NexKV 的测试框架中，提供数学证明级别的一致性验证。

## 核心组件

### 1. 状态模型 (`model.go`)

定义 NexKV 的状态空间和状态转移函数：

```go
// 支持的操作类型
const (
    OpGet        // 读取操作
    OpPut        // 写入操作
    OpDelete     // 删除操作
    OpQuorumGet  // Quorum 读取
    OpQuorumPut  // Quorum 写入
)

// 使用 NexKVModel 进行验证
var NexKVModel = porcupine.Model{...}
```

### 2. 历史记录器 (`recorder.go`)

记录操作的 Call/Return 事件：

```go
recorder := NewHistoryRecorder("client-1", timestamp)
opID := recorder.RecordCall(OpPut, "ns1", "key1", []byte("value"))
recorder.RecordReturn(opID, true, nil, "")
ops := recorder.GetOperations()
```

### 3. 一致性检查器 (`checker.go`)

调用 Porcupine 进行验证：

```go
checker := NewConsistencyChecker(NexKVModel, time.Minute, "/tmp/reports")
result := checker.CheckOperations(ops)
if !result.Ok {
    fmt.Println("报告路径:", result.ReportPath)
}
```

### 4. 记录客户端 (`recording_client.go`)

包装 KV 客户端，自动记录操作：

```go
client := NewRecordingClient(kvStore, recorder)
client.Put(ctx, "ns1", "key1", []byte("value1"))  // 自动记录
```

### 5. 场景适配器 (`scenario_adapter.go`)

与现有 `E2ETestScenario` 集成：

```go
recScenario := NewRecordingE2ETestScenario([]string{"node-1", "node-2"})
recScenario.AddNode("node-1", mockKV)

client := recScenario.RecordingClients["node-1"]
client.Put(ctx, "ns1", "key", []byte("value"))

result := recScenario.VerifyLinearizability()
```

## 时间戳方案

本模块支持两种时间戳方案：

| 方案 | 适用场景 | 说明 |
|------|----------|------|
| `MonotonicTimestamp` | 单机测试 | 基于系统时钟 + 原子自增 |
| `LogicalTimestamp` | 多节点测试 | 节点ID + 本地序列，不依赖物理时钟 |

```go
// 自动选择（推荐）
ts := NewTimestampGenerator("node-1", totalNodes)
```

## 使用示例

### 基础用法

```go
func TestMyKV_Linearizability(t *testing.T) {
    // 创建测试场景
    recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
    recScenario.AddNode("node-1", myKVStore)

    ctx := context.Background()
    client := recScenario.RecordingClients["node-1"]

    // 执行操作
    client.Put(ctx, "ns1", "key1", []byte("value1"))
    client.Get(ctx, "ns1", "key1")
    client.Delete(ctx, "ns1", "key1")

    // 验证线性化
    result := recScenario.VerifyLinearizability()
    require.True(t, result.Ok, "Should be linearizable")
}
```

### 并发测试

```go
func TestConcurrent_Linearizability(t *testing.T) {
    recScenario := NewRecordingE2ETestScenario([]string{"node-1"})
    recScenario.AddNode("node-1", myKVStore)

    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            client := recScenario.RecordingClients["node-1"]
            client.Put(ctx, "ns1", "key", []byte("value"))
        }()
    }
    wg.Wait()

    result := recScenario.VerifyLinearizability()
    require.True(t, result.Ok)
}
```

## 测试

运行所有测试：

```bash
go test ./internal/metadata/consistency/porcupine/... -v
```

运行线性化测试：

```bash
go test ./internal/metadata/consistency/porcupine/... -run Linearizability -v
```

运行性能测试：

```bash
go test ./internal/metadata/consistency/porcupine/... -run Performance -v
```

## 性能指标

| 操作数 | 检查时间目标 |
|--------|-------------|
| 1,000 ops | < 100ms |
| 10,000 ops | < 1s |

## 可视化报告

当线性化验证失败时，检查器会自动生成 HTML 可视化报告：

```go
checker := NewConsistencyChecker(NexKVModel, time.Minute, "/tmp/reports")
result := checker.CheckOperations(ops)
if !result.Ok {
    // 报告保存在 result.ReportPath
}
```

## 注意事项

1. **时间戳精度**：多节点测试必须使用 `LogicalTimestamp`，避免时钟同步问题
2. **并发安全**：RecordingClient 是并发安全的，可在多个 goroutine 中使用
3. **内存管理**：大量操作时使用 `recorder.Clear()` 定期清理历史
4. **测试覆盖**：推荐测试覆盖率 > 80%

## 参考文档

- [Porcupine GitHub](https://github.com/anishathalye/porcupine)
- [线性一致性论文](https://cs.brown.edu/~mph/HerlihyW90/p463-herlihy.pdf)
- [NexKV PR-063 Pre 文档](../../../docs/06_PM/feature/2026-02-13_PR-063_Porcupine-Linearizability-Verification_Pre.md)
