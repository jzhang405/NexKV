# PR-088 测试时间报告

> **日期**: 2026-02-27
> **状态**: ✅ 已修复

## 测试时间分析

### 最终测试时间

| 测试包 | 耗时 | 状态 |
|--------|------|------|
| internal/infrastructure/concurrency | 19s | ✅ PASS |
| test/integration/scenarios | 29.5s | ✅ PASS |
| **总计** | **~52s** | ✅ **全部通过** |

### 已修复的慢测试

| 测试名称 | 原耗时 | 修复后 | 问题原因 | 解决方案 |
|----------|--------|--------|----------|----------|
| `TestAntsFuncExecutor_Submit` | 49s (超时) | <1s | handler 为空，任务不执行 | 使用 `funcTask` 包装任务 |
| `TestAntsFuncExecutor_Invoke` | 600s | 1.72s | `wg.Done()` 位置错误 | 移到 handler 内部 |
| `TestIntegration_GracefulShutdown` | 30s (超时) | <1s | SourceID 路由错误 + 缺少默认执行器 | 注册默认执行器 + 使用正确的 SourceID |
| `TestIntegration_FullWorkflow` | 30s (超时) | <5s | AntsFuncExecutor handler 为空 | 添加 `funcTask` 处理 |

### 正常耗时的测试

| 测试名称 | 耗时 | 说明 |
|----------|------|------|
| `TestIntegration_ThreeNodesCluster_*` | ~29s | 多节点集群集成测试，需要时间建立连接 |
| `TestIntegration_PanicRecovery` | ~5s | 包含 `time.Sleep` 等待 panic 恢复 |
| `TestIntegration_GracefulShutdown` | ~1.5s | 100 个任务 + 10ms sleep |

## 修复详情

### 1. AntsFuncExecutor.Submit() 死锁问题

**问题代码**：
```go
// executor_ants.go
func (e *AntsFuncExecutor) Submit(ctx context.Context, task func(context.Context)) error {
    return e.pool.Invoke(func() {  // ❌ 传入 func()，但 handler 不执行它
        task(ctx)
    })
}
```

**修复方案**：
```go
// 定义 funcTask 包装
type funcTask struct {
    ctx  context.Context
    task func(context.Context)
}

func (e *AntsFuncExecutor) Submit(ctx context.Context, task func(context.Context)) error {
    // 将任务包装为可调用的形式
    return e.pool.Invoke(&funcTask{ctx: ctx, task: task})
}

// 测试中的 handler 需要识别并执行 funcTask
handler := func(i interface{}) {
    if ft, ok := i.(*funcTask); ok {
        ft.task(ft.ctx)
    }
}
```

### 2. TestIntegration_GracefulShutdown 路由问题

**问题代码**：
```go
// 只注册了 ModePerCore
executor, _ := NewPerCoreExecutor(WithNumCores(2), WithQueueSize(1000))
selector.RegisterExecutor(model.ModePerCore, executor)

// 但使用的是 "test:shutdown:task"，它会路由到 ModeAntsPool
sourceID, _ := model.ParseSourceID("test:shutdown:task")  // ❌
```

**修复方案**：
```go
// 注册默认池作为 fallback
selector.RegisterExecutor(model.ModeAntsPool, NewAntsDefaultExecutor())

// 使用 PerCore 模式的 SourceID
sourceID, _ := model.ParseSourceID("hlc:clock:tick")  // ✅
```

## 优化建议（未来改进）

1. **使用 mock timer**：对于 `time.Sleep` 相关测试，可以使用接口注入时间控制
2. **减少 sleep 时间**：将 10ms sleep 减少到 1ms
3. **并行测试**：独立测试可以并行运行

## 测试命令

```bash
# 快速验证
make test

# 单独运行 concurrency 包测试
go test ./internal/infrastructure/concurrency/... -timeout 60s

# 详细输出
go test -v ./internal/infrastructure/concurrency/... -timeout 60s
```
