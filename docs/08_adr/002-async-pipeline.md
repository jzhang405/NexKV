# ADR 002: 异步流水线架构

**状态**: 已接受 | **日期**: 2026-03-02 | **决策者**: 架构团队

---

## 上下文（Context）

NexKV 需要支持高并发、低延迟的数据操作，同时保证：

1. **高吞吐量**：写入操作 > 10万 ops/s
2. **低延迟**：点查询 < 50μs
3. **异步优先**：所有 API 优先提供异步版本
4. **资源安全**：避免 goroutine 泄漏和资源耗尽

**传统问题**：
- 直接调用：串行执行，无法充分利用并发
- 手动 goroutine：容易泄漏，难以管理
- 缺乏背压：高负载下系统崩溃

---

## 决策（Decision）

**采用异步流水线架构**：

```
┌─────────────────────────────────────────────┐
│           Client Layer                      │
│  (KVClient.SetAsync, GetAsync, etc.)        │
└──────────────┬──────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────┐
│         Channel Pipeline                    │
│  (writeCh, readCh - 带缓冲背压控制)         │
└──────────────┬──────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────┐
│      Worker Pool (TaskExecutor)            │
│  (Per-Core 或 Ants 池)                      │
└──────────────┬──────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────┐
│      Storage Engine                         │
│  (BfTree + WAL)                             │
└─────────────────────────────────────────────┘
```

**核心组件**：

1. **泛型异步接口**：`AsyncOp[T]` - 类型安全的异步操作
2. **Channel 流水线**：`writeCh`, `readCh` - 背压控制
3. **Worker 池**：复用现有 `TaskExecutor`（Per-Core/Ants）
4. **泛型锁包装器**：`Locked[T]` - 有锁/无锁模式切换

---

## 理由（Rationale）

### 优势

1. **类型安全**
   - Go 1.21+ 泛型支持
   - 编译时类型检查
   - 无需类型断言

2. **背压控制**
   - Channel 缓冲区限制
   - 自动流量控制
   - 防止资源耗尽

3. **性能优化**
   - Per-Core 无锁执行器
   - CPU 局部性优化
   - 减少跨核通信

4. **资源管理**
   - Goroutine 池复用
   - 避免泄漏
   - 优雅关闭

### 劣势与缓解

| 劣势 | 缓解措施 |
|------|----------|
| 复杂度增加 | 封装良好的接口设计 |
| 调试困难 | 详细的日志和追踪 |
| 学习曲线 | 完整的文档和示例 |

---

## 后果（Consequences）

### 正面影响

- ✅ 写入吞吐量预期提升 50-100%
- ✅ 异步 API 统一，易用性强
- ✅ 资源可控，无泄漏风险
- ✅ 支持批量操作优化

### 负面影响

- ⚠️ 代码复杂度增加
- ⚠️ 需要泛型支持（Go 1.21+）
- ⚠️ 调试异步代码更困难

### 性能预期

| 操作 | 同步 | 异步 | 提升 |
|------|------|------|------|
| 点查询 | < 50μs | < 30μs | 40% |
| 写入吞吐 | 10万 ops/s | 20万 ops/s | 100% |
| 批量写入(100) | 50万 ops/s | 100万 ops/s | 100% |

---

## 实施细节

### AsyncOp 泛型接口

```go
// internal/domain/service/rpc_async.go
package service

import "context"

type AsyncOp[T any] interface {
    // Await 等待操作完成
    Await(ctx context.Context) (T, error)

    // OnComplete 注册完成回调
    OnComplete(callback func(T, error)) string

    // Cancel 取消操作
    Cancel() (bool, error)

    // Discard 丢弃操作结果
    Discard() error

    // IsStarted 检查是否已开始
    IsStarted() bool
}
```

### 写流水线实现

```go
// internal/infrastructure/storage/pipeline/write_pipeline.go
package pipeline

type WriteTask struct {
    Key      []byte
    Value    []byte
    Callback func(error)
}

type WritePipeline struct {
    btree    BTree
    wal      WAL
    writeCh  chan *WriteTask
    executor TaskExecutor
}

func (p *WritePipeline) Start(ctx context.Context) {
    go p.worker(ctx)
}

func (p *WritePipeline) worker(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case task := <-p.writeCh:
            // 1. 写 BTree（内存更新）
            err := p.btree.Set(task.Key, task.Value)

            // 2. 异步写 WAL
            if err == nil {
                p.wal.AppendAsync(ctx, task.Key, task.Value)
            }

            // 3. 回调
            if task.Callback != nil {
                task.Callback(err)
            }
        }
    }
}
```

### 泛型锁包装器

```go
// internal/infrastructure/concurrent/locked.go
package concurrent

import "sync"

type Locked[T any] struct {
    mu   sync.RWMutex
    core T
}

// View 读视图（自动加读锁）
func (l *Locked[T]) View(fn func(core T) error) error {
    l.mu.RLock()
    defer l.mu.RUnlock()
    return fn(l.core)
}

// Modify 写视图（自动加写锁）
func (l *Locked[T]) Modify(fn func(core T) error) error {
    l.mu.Lock()
    defer l.mu.Unlock()
    return fn(l.core)
}

// GetDirect 直接访问（无锁，由调用者保证并发安全）
func (l *Locked[T]) GetDirect() T {
    return l.core
}
```

### 使用示例

```go
// 异步写入
func Example_AsyncWrite() {
    kv := NewBTreeKV()

    op := kv.SetAsync(ctx, []byte("key"), []byte("value"))

    // 方式1：等待结果
    err := op.Await(ctx)

    // 方式2：回调
    op.OnComplete(func(val struct{}, err error) {
        if err != nil {
            log.Printf("写入失败: %v", err)
        }
    })
}
```

---

## 迁移路径

### 阶段 0：基础设施（4周）

- [ ] AsyncOperation → AsyncOp 重命名
- [ ] 实现 Locked[T] 泛型锁包装器
- [ ] 设计流水线架构

### 阶段 1：流水线实现（6周）

- [ ] 实现 WritePipeline
- [ ] 实现 ReadPipeline
- [ ] 实现 FlushPipeline（WAL）

### 阶段 2：存储引擎集成（8周）

- [ ] BfTree 异步 API
- [ ] WAL 异步批量写入
- [ ] 性能测试与优化

---

## 测试策略

### 单元测试

- AsyncOp 接口测试
- 流水线并发测试
- 锁包装器线程安全测试

### 基准测试

```go
func BenchmarkAsyncOp_Await(b *testing.B) {
    op := NewAsyncOp[[]byte](executor)
    op.Complete([]byte("value"), nil)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        op.Await(context.Background())
    }
}

func BenchmarkWritePipeline_Throughput(b *testing.B) {
    pipeline := NewWritePipeline(/* ... */)

    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            pipeline.writeCh <- &WriteTask{/* ... */}
        }
    })
}
```

---

## 监控指标

| 指标 | 目标 | 说明 |
|------|------|------|
| 异步操作延迟 | P99 < 100μs | Await 调用延迟 |
| 流水线深度 | < 1000 | Channel 缓冲区使用 |
| Goroutine 数量 | 稳定 | 无泄漏 |
| 写入吞吐 | > 20万 ops/s | 异步模式 |

---

## 替代方案

### 方案 A：回调地狱

```go
// ❌ 不推荐
kv.Set(key, value, func(err error) {
    if err != nil {
        // 处理错误
    }
})
```

- ❌ 类型不安全
- ❌ 难以组合
- ❌ 错误处理复杂

### 方案 B：Promise/Future

```go
// ⚠️ 可选，但 AsyncOp 更优
promise := kv.SetPromise(key, value)
promise.Then(func(result Result) {
    // ...
})
```

- ⚠️ 需要引入新的抽象
- ✅ 但 AsyncOp[T] 已经足够

### 方案 C：纯 Channel

```go
// ❌ 不推荐
ch := make(chan Result, 1)
go func() {
    ch <- kv.Set(key, value)
}()
result := <-ch
```

- ❌ 无背压控制
- ❌ 易泄漏
- ❌ 难以管理

---

## 参考资料

- `thoughts/2026-03-02-idea-async-pipeline-refactor.md`
- `docs/07_spike/2026-02-18_spike_nexkv-ddd-implement.md`
- [Go 泛型最佳实践](https://go.dev/blog/intro-generics)
- [Per-Core 无锁执行器](./2026-02-25_spike-glm-unified-executor.md)

---

**相关 ADR**:
- [ADR 001: 双存储引擎策略](./001-dual-storage-engine.md)
- [ADR 003: 5层 DDD 架构](./003-5layer-ddd.md)
