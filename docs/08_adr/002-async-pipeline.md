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

### ReadPipeline 读流水线

**架构图**：
```
┌─────────────────────────────────────────────────────────┐
│                    读流水线架构                           │
├─────────────────────────────────────────────────────────┤
│                                                         │
│   Client.GetAsync()                                      │
│       ↓                                                 │
│   AsyncOp[[]byte]                                       │
│       ↓                                                 │
│   ReadTask{Key, Callback}                               │
│       ↓                                                 │
│   ┌─────────────────────────────────┐                  │
│   │  readCh (Channel, 背压控制)       │                  │
│   └─────────────────────────────────┘                  │
│       ↓                                                 │
│   ┌─────────────────────────────────┐                  │
│   │  TaskExecutor (Per-Core/Ants)   │                  │
│   │  └─ BTree.Get() (内存查询)       │                  │
│   └─────────────────────────────────┘                  │
│       ↓                                                 │
│   Callback(value, err) → AsyncOp.Complete()             │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

**设计原则**：
- **读操作直接使用 TaskExecutor**：最大化并发性能
- **无顺序要求**：多个读操作可以并行执行
- **背压控制**：通过 readCh 缓冲区限制

**代码示例**：
```go
// internal/infrastructure/storage/pipeline/read_pipeline.go
package pipeline

type ReadTask struct {
    Key       []byte
    Result    chan []byte
    Err       chan error
    Timestamp hlc.Timestamp
}

type ReadPipeline struct {
    btree    BTree
    readCh   chan *ReadTask
    executor TaskExecutor
}

func (p *ReadPipeline) Submit(ctx context.Context, key []byte) AsyncOp[[]byte] {
    op := NewAsyncOp[[]byte](p.executor)

    task := &ReadTask{
        Key:    key,
        Result: make(chan []byte, 1),
        Err:    make(chan error, 1),
    }

    // 提交到读队列
    p.readCh <- task

    // 异步等待结果
    go func() {
        select {
        case value := <-task.Result:
            op.Complete(value, nil)
        case err := <-task.Err:
            op.Complete(nil, err)
        case <-ctx.Done():
            op.Complete(nil, ctx.Err())
        }
    }()

    return op
}

func (p *ReadPipeline) worker(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case task := <-p.readCh:
            // 直接使用 TaskExecutor 执行读操作（高并发）
            _ = p.executor.Submit(ctx, model.SourceBTree, service.PriorityHigh, func(ctx context.Context) {
                value, err := p.btree.Get(task.Key)
                if err != nil {
                    task.Err <- err
                } else {
                    task.Result <- value
                }
            })
        }
    }
}
```

### FlushPipeline 刷新流水线（WAL）

**架构图**：
```
┌─────────────────────────────────────────────────────────┐
│                  WAL 刷新流水线架构                       │
├─────────────────────────────────────────────────────────┤
│                                                         │
│   WritePipeline                                         │
│       ↓                                                 │
│   WAL.Append(entries)                                   │
│       ↓                                                 │
│   ┌─────────────────────────────────┐                  │
│   │  walCh (Channel, 批量累积)        │                  │
│   └─────────────────────────────────┘                  │
│       ↓                                                 │
│   ┌─────────────────────────────────┐                  │
│   │  Batch Worker (单 goroutine)     │                  │
│   │  └─ 累积批量 (100条或10ms)        │                  │
│   │  └─ WAL.Flush()                  │                  │
│   └─────────────────────────────────┘                  │
│       ↓                                                 │
│   Callbacks (通知所有等待的写操作)                       │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

**设计原则**：
- **单 Worker 串行化**：保证 WAL 顺序写入
- **批量优化**：累积一定数量或时间后统一 flush
- **同步策略**：支持 Async/Batch/Always 三种模式

**代码示例**：
```go
// internal/infrastructure/storage/pipeline/flush_pipeline.go
package pipeline

type WALEntry struct {
    Key       []byte
    Value     []byte
    Timestamp hlc.Timestamp
    SyncMode  SyncPolicy
    Callback  func(error)
}

type FlushPipeline struct {
    wal       WAL
    walCh     chan *WALEntry
    batch     []*WALEntry
    batchSize int
    flushTicker *time.Ticker
}

func (p *FlushPipeline) worker(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            // 关闭前 flush 剩余批次
            if len(p.batch) > 0 {
                p.flushBatch()
            }
            return

        case entry := <-p.walCh:
            p.batch = append(p.batch, entry)

            // 批量满或同步模式立即 flush
            if len(p.batch) >= p.batchSize || entry.SyncMode == SyncAlways {
                p.flushBatch()
            }

        case <-p.flushTicker.C:
            // 定时 flush（避免延迟过高）
            if len(p.batch) > 0 {
                p.flushBatch()
            }
        }
    }
}

func (p *FlushPipeline) flushBatch() {
    if len(p.batch) == 0 {
        return
    }

    // 批量写入 WAL
    entries := p.batch
    p.batch = make([]*WALEntry, 0, p.batchSize)

    // 序列化并写入
    err := p.wal.WriteBatch(entries)

    // 回调所有等待的操作
    for _, entry := range entries {
        if entry.Callback != nil {
            entry.Callback(err)
        }
    }
}
```

### 任务类型定义

#### SyncPolicy 同步策略

```go
// SyncPolicy 定义 WAL 同步策略
type SyncPolicy int

const (
    // SyncAsync 异步模式，不等待 fsync（最高性能）
    // 适用场景：日志写入、非关键数据
    // 风险：崩溃后可能丢失最后一批数据
    SyncAsync SyncPolicy = iota

    // SyncBatch 批量 fsync（默认）
    // 适用场景：一般业务数据
    // 行为：累积一定数量或时间后统一 fsync
    SyncBatch

    // SyncAlways 每次操作都 fsync（最安全）
    // 适用场景：关键数据、事务提交
    // 性能：最低，但数据安全性最高
    SyncAlways
)

func (s SyncPolicy) String() string {
    switch s {
    case SyncAsync:
        return "async"
    case SyncBatch:
        return "batch"
    case SyncAlways:
        return "always"
    default:
        return "unknown"
    }
}
```

**策略对比**：

| 策略 | 延迟 | 吞吐量 | 数据安全 | 适用场景 |
|------|------|--------|----------|----------|
| SyncAsync | ~5μs | 最高 | 可能丢失 | 日志、缓存 |
| SyncBatch | ~500μs | 高 | 批量丢失 | 一般业务 |
| SyncAlways | ~5ms | 低 | 不丢失 | 关键数据、事务 |

#### ReadTask 读任务

```go
// ReadTask 用于读操作的任务结构
type ReadTask struct {
    // Key 键
    Key []byte

    // Result 结果返回通道
    Result chan []byte

    // Err 错误返回通道
    Err chan error

    // TxnID 事务 ID（0 表示非事务读）
    TxnID uint64

    // Snapshot 快照版本（用于 MVCC）
    // 0 表示读取最新版本
    Snapshot uint64

    // Timestamp 读操作时间戳
    Timestamp hlc.Timestamp
}

// IsSnapshotRead 判断是否为快照读
func (t *ReadTask) IsSnapshotRead() bool {
    return t.Snapshot != 0
}
```

#### TransactionTask 事务任务

```go
// TxnMode 事务模式
type TxnMode int

const (
    // TxnModeReadWrite 读写事务（默认）
    TxnModeReadWrite TxnMode = iota

    // TxnModeReadOnly 只读事务
    TxnModeReadOnly
)

// TransactionTask 用于事务操作的任务结构
type TransactionTask struct {
    // TxnID 事务唯一标识
    TxnID uint64

    // Mode 事务模式
    Mode TxnMode

    // Isolation 隔离级别
    Isolation IsolationLevel

    // Writes 事务内的写操作列表
    Writes []*WriteTask

    // Reads 事务内的读操作列表
    Reads []*ReadTask

    // Done 完成通知通道
    Done chan error

    // StartTime 事务开始时间
    StartTime time.Time

    // Timeout 事务超时时间
    Timeout time.Duration
}

// IsReadOnly 判断是否为只读事务
func (t *TransactionTask) IsReadOnly() bool {
    return t.Mode == TxnModeReadOnly || len(t.Writes) == 0
}

// HasTimeout 判断是否设置了超时
func (t *TransactionTask) HasTimeout() bool {
    return t.Timeout > 0
}
```

#### IsolationLevel 事务隔离级别

```go
// IsolationLevel 事务隔离级别
type IsolationLevel int

const (
    // IsolationReadCommitted 已提交读
    // - 只能读取已提交的数据
    // - 可能遇到不可重复读和幻读
    // - 性能最好
    IsolationReadCommitted IsolationLevel = iota

    // IsolationRepeatableRead 可重复读（默认）
    // - 同一事务内多次读取结果一致
    // - 避免不可重复读
    // - 可能遇到幻读
    IsolationRepeatableRead

    // IsolationSerializable 串行化
    // - 完全隔离，避免所有并发问题
    // - 性能最低
    IsolationSerializable
)

func (l IsolationLevel) String() string {
    switch l {
    case IsolationReadCommitted:
        return "READ_COMMITTED"
    case IsolationRepeatableRead:
        return "REPEATABLE_READ"
    case IsolationSerializable:
        return "SERIALIZABLE"
    default:
        return "UNKNOWN"
    }
}
```

---

## 迁移路径

### 阶段 0：基础设施（4周） - ✅ 已完成

- [x] AsyncOperation → AsyncOp 重命名
- [x] 实现 Locked[T] 泛型锁包装器
- [x] TaskExecutor 接口增强（SourceID 支持）
- [x] 设计流水线架构（本文档）

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

### 集成测试计划

#### 测试场景 1：端到端异步写入流程

**目标**：验证 WritePipeline → BTree → WAL 完整链路

**测试步骤**：
1. 启动 WritePipeline 和 FlushPipeline
2. 提交 1000 个异步写入操作
3. 验证所有操作完成（Await）
4. 验证 BTree 数据正确性
5. 验证 WAL 日志完整性

**验收标准**：
- ✅ 所有写入操作成功
- ✅ BTree 数据与预期一致
- ✅ WAL 日志按顺序写入
- ✅ 无 goroutine 泄漏

#### 测试场景 2：高并发读写混合

**目标**：验证 ReadPipeline 和 WritePipeline 并发正确性

**测试步骤**：
1. 启动 ReadPipeline 和 WritePipeline
2. 启动 100 个并发 goroutine
3. 50% 写入，50% 读取
4. 运行 10 秒
5. 验证数据一致性

**验收标准**：
- ✅ 无数据竞争（`-race` 检测通过）
- ✅ 读写结果一致
- ✅ 吞吐量 > 10万 ops/s
- ✅ P99 延迟 < 100μs

#### 测试场景 3：批量写入优化

**目标**：验证 FlushPipeline 批量优化效果

**测试步骤**：
1. 配置 SyncBatch 策略
2. 提交 10000 个写入操作
3. 测量吞吐量和延迟
4. 对比 SyncAlways 策略性能

**验收标准**：
- ✅ SyncBatch 吞吐量 > SyncAlways 2倍
- ✅ SyncBatch 延迟 < 1ms
- ✅ 批量大小符合预期（~100 条/批次）

#### 测试场景 4：事务正确性

**目标**：验证 TransactionTask 的 ACID 特性

**测试步骤**：
1. 启动事务流水线
2. 提交多个并发事务（读写混合）
3. 模拟冲突场景
4. 验证隔离级别

**验收标准**：
- ✅ ReadCommitted：只读已提交数据
- ✅ RepeatableRead：同一事务内读取一致
- ✅ Serializable：完全串行化
- ✅ 死锁检测和回滚正常

#### 测试场景 5：背压控制

**目标**：验证 Channel 背压控制有效性

**测试步骤**：
1. 配置小容量 Channel（buffer=100）
2. 快速提交 10000 个操作
3. 观察系统行为
4. 验证无资源耗尽

**验收标准**：
- ✅ Channel 满时自动阻塞
- ✅ 内存使用稳定
- ✅ 无 OOM 错误
- ✅ 系统优雅降级

#### 测试场景 6：故障恢复

**目标**：验证 WAL 恢复机制

**测试步骤**：
1. 写入 1000 条数据
2. 模拟崩溃（kill -9）
3. 重启系统
4. 从 WAL 恢复
5. 验证数据完整性

**验收标准**：
- ✅ 成功从 WAL 恢复
- ✅ 数据无丢失（SyncAlways 模式）
- ✅ 恢复时间 < 1s
- ✅ 恢复后系统正常

#### 测试场景 7：性能基准

**目标**：建立性能基线

**测试步骤**：
1. 点查询性能测试
2. 写入吞吐量测试
3. 批量写入性能测试
4. 事务性能测试

**验收标准**：
| 操作 | 目标 | 测试方法 |
|------|------|----------|
| 点查询 | < 30μs | BenchmarkReadPipeline_Get |
| 写入吞吐 | > 20万 ops/s | BenchmarkWritePipeline_Throughput |
| 批量写入(100) | > 100万 ops/s | BenchmarkBatchWrite |
| 事务提交 | < 1ms | BenchmarkTransaction_Commit |

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

### 内部文档

**设计文档**：
- `docs/07_spike/2026-02-18_spike_nexkv-ddd-implement.md` - DDD 实现和流水线模式
- `docs/07_spike/2026-02-21_spike_m2-storage-engine-interface.md` - M2 存储引擎接口定义
- `docs/06_PM/feature/2026-03-02_pre-m2-phase0-async-pipeline.md` - Phase 0 实施计划
- `thoughts/2026-03-02-idea-async-pipeline-refactor.md` - 异步流水线重构思路

**代码实现**：
- `internal/domain/service/rpc_async.go` - AsyncOp[T] 接口定义
- `internal/infrastructure/concurrency/locked.go` - Locked[T] 泛型锁包装器
- `internal/infrastructure/concurrency/executor_percore.go` - Per-Core 执行器

### 外部资源

- [Go 泛型最佳实践](https://go.dev/blog/intro-generics)
- [Per-Core 无锁执行器设计](./2026-02-25_spike-glm-unified-executor.md)

### 交叉引用说明

本文档中的任务类型定义（SyncPolicy、ReadTask、TransactionTask、IsolationLevel）参考自：
- `docs/07_spike/2026-02-21_spike_m2-storage-engine-interface.md` 第 12 节

流水线架构设计参考自：
- `docs/07_spike/2026-02-18_spike_nexkv-ddd-implement.md` 第 835-884 行

---

**相关 ADR**:
- [ADR 001: 双存储引擎策略](./001-dual-storage-engine.md)
- [ADR 003: 5层 DDD 架构](./003-5layer-ddd.md)
