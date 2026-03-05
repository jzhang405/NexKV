# Transport RPC V4 异步管道改造 Proposal

> **提案日期**: 2026-03-05
> **提案人**: jzh
> **状态**: 📝 评审中
> **优先级**: P1 - 架构一致性改进
> **影响范围**: RPC Layer、Executor Layer、Storage Layer

---

## 1. 执行摘要

### 1.1 问题概述

当前 Transport RPC 层存在异步模型割裂、并发控制复杂、CPU 亲和性利用不足等问题。本提案建议将 RPC 层改造为使用 V4 异步管道架构，统一 Task 模型，提升系统整体一致性。

### 1.2 核心改进

| 改进项 | 当前状态 | 目标状态 | 预期收益 |
|--------|---------|---------|---------|
| **Task 模型统一** | AsyncOp + GoroutineProvider | V4 Task[Result] | 架构一致性 |
| **并发控制优化** | 信号量 + Submit 双重控制 | TaskExecutor 批量提交 | 简化代码 |
| **CPU 亲和性** | 全部使用 SourceNetwork | 按场景选择 SourceID | 性能提升 |
| **回调执行** | Executor/Goroutine 混合 | 统一走 Executor | 稳定性 |

### 1.3 实施成本

- **预估工期**: 1-1.5 周（新项目，无需向后兼容）
- **影响文件**: ~6 个核心文件
- **向后兼容**: 无需考虑（新项目直接采用新设计）

---

## 2. 现状分析

### 2.1 当前架构

```
┌─────────────────────────────────────────────────────────────┐
│  RPC Layer                                                   │
│  ├── RPCSync (同步) ──► Libp2pRPC                           │
│  └── RPCAsync (异步) ──► RPCAsyncAdapter ──► RPCSync        │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼ uses TaskExecutor
┌─────────────────────────────────────────────────────────────┐
│  Executor Layer                                              │
│  ├── AntsPoolExecutor ──► 高吞吐、自动扩缩容                 │
│  └── PerCoreExecutor ───► CPU 亲和、优先级队列               │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 使用场景

| 场景 | 当前实现 | Executor 类型 |
|------|---------|--------------|
| 广播并发发送 | `for + Submit` | AntsPool |
| 异步响应读取 | `Submit` 后台读取 | AntsPool |
| AsyncOp 回调 | `Submit` 或 `go` 回退 | AntsPool |
| 入站流处理 | `Submit` 监听关闭 | AntsPool |

---

## 3. 问题诊断

### 3.1 问题 1：AsyncOp 回调执行不一致

```go
// asyncop_impl.go
func (op *asyncOpImpl[T]) safeExecuteCallback(callback func(T, error), v T, err error) {
    if op.goroutineProvider != nil {
        if submitErr := op.goroutineProvider.Submit(...); submitErr != nil {
            go callback(v, err)  // 回退到 goroutine
        }
    } else {
        go callback(v, err)  // 直接 goroutine
    }
}
```

**问题**:
- 回调可能在 Executor 执行，也可能在独立 goroutine
- 无法保证回调的 CPU 亲和性
- 缺乏背压控制

### 3.2 问题 2：广播时的并发控制过于简单

```go
// libp2p_rpc.go
sem := make(chan struct{}, r.config.MaxConcurrentCalls)
for _, peer := range to {
    sem <- struct{}{}
    _ = r.provider.Submit(ctx, model.SourceNetwork, service.PriorityNormal, func(ctx context.Context) {
        defer func() { <-sem }()
        // ...
    })
}
```

**问题**:
- 信号量 + Submit 双重控制，复杂
- MaxConcurrentCalls 与 Executor 容量脱节
- 没有优先级区分（广播内部所有任务同优先级）

### 3.3 问题 3：RPCSync 与 RPCAsync 的割裂

```go
// RPCAsyncAdapter 只是简单包装
func (a *RPCAsyncAdapter) CallAsync(ctx context.Context, to model.PeerID, req model.Message) AsyncOp[ResponseMsg] {
    return NewAsyncCall(ctx, a.rpc, to, req, timeoutMs, provider)
}
```

**问题**:
- 没有利用 Pipeline 的 Task 抽象
- AsyncOp 与 V4 设计的 Task[Result] 不统一
- 两套异步模型并存

### 3.4 问题 4：SourceID 使用不一致

```go
// 网络请求用 SourceNetwork（无亲和性）
_ = r.provider.Submit(ctx, model.SourceNetwork, ...)

// 但文档说分片应该用 SourceShard（有亲和性）
model.SourceShard(shardID)
```

**问题**:
- 所有 RPC 都用 SourceNetwork，没有利用 PerCoreExecutor 的亲和性
- 无法保证同一连接/分片的请求路由到同一 Worker

---

## 4. 改进方案

### 4.1 方案 1：统一 Task 模型（推荐）

将 RPC 层的 AsyncOp 改为使用 V4 的 Task[Result] 模型：

```go
// ═══════════════════════════════════════════════════════════════
// 当前：AsyncOp 模型
// ═══════════════════════════════════════════════════════════════
type AsyncOp[T any] interface {
    Await(ctx context.Context) (T, error)
    OnComplete(callback func(T, error)) string
    // ...
}

// ═══════════════════════════════════════════════════════════════
// 改进：统一为 Task[Result] 模型
// ═══════════════════════════════════════════════════════════════

// RPCTask 基础任务
type RPCTask struct {
    BaseTask[ResponseMsg]
    rpc     service.RPCSync
    to      model.PeerID
    req     model.Message
    timeout time.Duration
}

func (t *RPCTask) Execute(ctx context.Context, p *Pipeline) (ResponseMsg, error) {
    ctx, cancel := context.WithTimeout(ctx, t.timeout)
    defer cancel()
    return t.rpc.Call(ctx, t.to, t.req)
}

// RPCCallTask 具体实现
type RPCCallTask struct {
    RPCTask
}

func NewRPCCallTask(rpc service.RPCSync, to model.PeerID, req model.Message, 
    sourceID model.SourceID, timeout time.Duration) *RPCCallTask {
    return &RPCCallTask{
        RPCTask: RPCTask{
            BaseTask: NewBaseTask[ResponseMsg](OpRPC, PriorityNormal, sourceID),
            rpc:      rpc,
            to:       to,
            req:      req,
            timeout:  timeout,
        },
    }
}

// 使用方式
func (r *RPCManager) CallAsync(ctx context.Context, to model.PeerID, 
    req model.Message) AsyncOp[ResponseMsg] {
    
    sourceID := r.getSourceID(req)  // 根据请求类型选择 SourceID
    task := NewRPCCallTask(r.rpc, to, req, sourceID, r.timeout)
    
    if err := r.pipeline.Submit(task); err != nil {
        return NewFailedAsyncOp[ResponseMsg](err)
    }
    
    return NewAsyncOpFromTask(task)
}
```

**好处**:
- 与存储层统一模型
- 复用 Pipeline 的优先级、亲和性
- 可以使用 sync.Pool 优化
- 避免 AsyncOp 和 Task[Result] 两套模型

### 4.2 方案 2：优化广播并发控制

使用 TaskExecutor 的批量提交接口：

```go
// ═══════════════════════════════════════════════════════════════
// 当前：信号量 + Submit
// ═══════════════════════════════════════════════════════════════
sem := make(chan struct{}, maxConcurrent)
for _, peer := range peers {
    sem <- struct{}{}
    go func(p model.PeerID) {
        defer func() { <-sem }()
        r.Call(ctx, p, req)
    }(peer)
}

// ═══════════════════════════════════════════════════════════════
// 改进：批量提交到 Executor
// ═══════════════════════════════════════════════════════════════

// TaskExecutor 扩展批量接口
type TaskExecutor interface {
    Submit(ctx context.Context, sourceID model.SourceID, priority TaskPriority, 
        task func(context.Context)) error
    
    // 新增：批量提交（原子性）
    SubmitBatch(ctx context.Context, tasks []TaskItem) error
}

type TaskItem struct {
    SourceID model.SourceID
    Priority TaskPriority
    Task     func(context.Context)
}

// 广播实现
func (r *RPCManager) BroadcastAsync(ctx context.Context, 
    peers []model.PeerID, req model.Message) AsyncOp[[]ResponseMsg] {
    
    // 创建批量任务
    items := make([]TaskItem, len(peers))
    results := make([]ResponseMsg, len(peers))
    var wg sync.WaitGroup
    
    for i, peer := range peers {
        wg.Add(1)
        idx := i
        items[i] = TaskItem{
            SourceID: r.getSourceID(req, peer),
            Priority: PriorityNormal,
            Task: func(ctx context.Context) {
                defer wg.Done()
                resp, err := r.rpc.Call(ctx, peer, req)
                if err == nil {
                    results[idx] = resp
                }
            },
        }
    }
    
    // 批量提交
    if err := r.executor.SubmitBatch(ctx, items); err != nil {
        return NewFailedAsyncOp[[]ResponseMsg](err)
    }
    
    // 返回聚合结果
    return NewBatchAsyncOp(results, &wg)
}
```

**好处**:
- 减少 Submit 调用开销
- Executor 内部统一调度
- 更好的优先级管理
- 简化代码（移除信号量）

### 4.3 方案 3：SourceID 策略优化

根据场景选择合适的 SourceID：

```go
// ═══════════════════════════════════════════════════════════════
// SourceID 选择策略
// ═══════════════════════════════════════════════════════════════

type SourceIDStrategy int

const (
    SourceStrategyNetwork SourceIDStrategy = iota  // 默认：无亲和性
    SourceStrategyShard                             // 按分片亲和
    SourceStrategyClient                            // 按客户端亲和
    SourceStrategyRaft                              // 按 Raft 节点亲和
)

func (r *RPCManager) getSourceID(req model.Message, peer model.PeerID) model.SourceID {
    switch req.Type {
    case MsgTypeRaft:
        // Raft 消息：按分片亲和，保证同一分片的请求在同一 Worker
        return model.SourceShard(req.ShardID)
        
    case MsgTypeClient:
        // 客户端请求：按客户端亲和，保证同一客户端的请求在同一 Worker
        return model.SourceClient(req.ClientID)
        
    case MsgTypeInternal:
        // 内部消息：按目标节点亲和
        return model.SourceNode(peer.String())
        
    default:
        // 其他：使用网络默认（无亲和性）
        return model.SourceNetwork
    }
}

// 使用示例
func (r *RPCManager) CallWithAffinity(ctx context.Context, 
    to model.PeerID, req model.Message) AsyncOp[ResponseMsg] {
    
    sourceID := r.getSourceID(req, to)
    task := NewRPCCallTask(r.rpc, to, req, sourceID, r.timeout)
    
    if err := r.pipeline.Submit(task); err != nil {
        return NewFailedAsyncOp[ResponseMsg](err)
    }
    
    return NewAsyncOpFromTask(task)
}
```

**好处**:
- 利用 PerCoreExecutor 的 CPU 亲和性
- 相同分片/客户端的请求在同一 Worker 处理
- 更好的缓存局部性
- 减少 Worker 切换开销

### 4.4 方案 4：AsyncOp 回调统一走 Executor

```go
// ═══════════════════════════════════════════════════════════════
// 当前：回调可能在 Executor 或 Goroutine 执行
// ═══════════════════════════════════════════════════════════════
func (op *asyncOpImpl[T]) safeExecuteCallback(callback func(T, error), v T, err error) {
    if op.goroutineProvider != nil {
        if submitErr := op.goroutineProvider.Submit(...); submitErr != nil {
            go callback(v, err)  // 回退到 goroutine
        }
    } else {
        go callback(v, err)
    }
}

// ═══════════════════════════════════════════════════════════════
// 改进：强制走 Executor，添加重试
// ═══════════════════════════════════════════════════════════════

func (op *asyncOpImpl[T]) safeExecuteCallback(callback func(T, error), v T, err error) {
    if op.executor == nil {
        // 没有 Executor 才用 goroutine（降级）
        go callback(v, err)
        return
    }
    
    // 强制走 Executor，添加重试
    ctx := context.Background()
    for i := 0; i < 3; i++ {
        submitErr := op.executor.Submit(ctx, model.SourceCallback, PriorityNormal, 
            func(ctx context.Context) {
                callback(v, err)
            })
        if submitErr == nil {
            return
        }
        // 短暂退避
        time.Sleep(time.Millisecond * 10 * time.Duration(i+1))
    }
    
    // 最终回退到 goroutine（避免死等）
    go callback(v, err)
}
```

**好处**:
- 回调执行位置可预测
- 保证回调的 CPU 亲和性
- 更好的资源控制
- 避免 goroutine 泄漏

---

## 5. 实施计划

### 5.1 优先级排序

| 优先级 | 改进项 | 影响 | 预估工期 |
|--------|-------|------|---------|
| **P1** | 统一 Task 模型 | 架构一致性 | 3 天 |
| **P2** | SourceID 策略优化 | 性能提升 | 2 天 |
| **P3** | 批量提交接口 | 高并发优化 | 3 天 |
| **P4** | 回调统一走 Executor | 稳定性 | 2 天 |

### 5.2 实施步骤

#### Phase 1: Task 模型统一（3天）

```
Day 1: 设计 RPCTask 结构
  - 定义 RPCTask 基础结构
  - 实现 RPCCallTask
  - 单元测试

Day 2-3: 替换 RPCAsync
  - 删除 RPCAsyncAdapter
  - 全局替换 AsyncOp -> Task[Result]
  - 使用 Pipeline.Submit
  - 集成测试
```

#### Phase 2: SourceID 优化（2天）

```
Day 1: 设计 SourceID 策略
  - 定义策略枚举
  - 实现选择函数

Day 2: 集成到 RPC
  - 修改 Call/CallAsync
  - 添加配置选项
  - 性能测试
```

#### Phase 3: 批量提交（3天）

```
Day 1: TaskExecutor 扩展
  - 添加 SubmitBatch 接口
  - AntsPoolExecutor 实现

Day 2: 广播改造
  - 修改 BroadcastAsync
  - 移除信号量

Day 3: 集成测试
```

### 5.3 风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 性能回退 | 中 | 充分基准测试，保留回滚方案 |
| SourceID 选择不当 | 中 | 可配置策略，默认保守 |
| 回调延迟 | 低 | 添加超时和降级机制 |

---

## 6. 接口定义

### 6.1 新增接口

```go
// RPCTask RPC 任务接口
type RPCTask interface {
    Task[ResponseMsg]
    SetTimeout(timeout time.Duration)
    GetPeerID() model.PeerID
    GetRequest() model.Message
}

// RPCManager RPC 管理器
type RPCManager interface {
    // 同步调用
    Call(ctx context.Context, to model.PeerID, req model.Message) (ResponseMsg, error)

    // 异步调用（统一使用 Task[Result]）
    CallAsync(ctx context.Context, to model.PeerID, req model.Message) Task[ResponseMsg]

    // 批量调用
    CallBatch(ctx context.Context, peers []model.PeerID, req model.Message) Task[[]ResponseMsg]

    // 广播
    Broadcast(ctx context.Context, peers []model.PeerID, req model.Message) error
    BroadcastAsync(ctx context.Context, peers []model.PeerID, req model.Message) Task[[]ResponseMsg]
}

// SourceIDStrategy SourceID 选择策略
type SourceIDStrategy interface {
    GetSourceID(req model.Message, peer model.PeerID) model.SourceID
}
```

### 6.2 修改接口

```go
// TaskExecutor 扩展
type TaskExecutor interface {
    // 现有方法...
    Submit(ctx context.Context, sourceID model.SourceID, priority TaskPriority, 
        task func(context.Context)) error
    
    // 新增批量提交
    SubmitBatch(ctx context.Context, tasks []TaskItem) error
}
```

---

## 7. 测试策略

### 7.1 单元测试

```go
// RPCTask 测试
func TestRPCCallTask(t *testing.T) {
    task := NewRPCCallTask(mockRPC, peerID, req, sourceID, timeout)
    
    // 测试 Execute
    resp, err := task.Execute(ctx, pipeline)
    assert.NoError(t, err)
    assert.Equal(t, expectedResp, resp)
}

// SourceID 策略测试
func TestSourceIDStrategy(t *testing.T) {
    strategy := NewDefaultSourceIDStrategy()
    
    // Raft 消息
    raftReq := &model.Message{Type: MsgTypeRaft, ShardID: 1}
    assert.Equal(t, model.SourceShard(1), strategy.GetSourceID(raftReq, peer))
    
    // 客户端消息
    clientReq := &model.Message{Type: MsgTypeClient, ClientID: "client-1"}
    assert.Equal(t, model.SourceClient("client-1"), strategy.GetSourceID(clientReq, peer))
}
```

### 7.2 集成测试

```go
// 端到端测试
func TestRPCManagerIntegration(t *testing.T) {
    manager := NewRPCManager(pipeline, executor)
    
    // 异步调用
    asyncOp := manager.CallAsync(ctx, peerID, req)
    resp, err := asyncOp.Await(ctx)
    assert.NoError(t, err)
    
    // 批量调用
    batchOp := manager.CallBatch(ctx, peers, req)
    resps, err := batchOp.Await(ctx)
    assert.NoError(t, err)
    assert.Len(t, resps, len(peers))
}
```

### 7.3 性能测试

```go
// 基准测试
func BenchmarkRPCCallAsync(b *testing.B) {
    manager := NewRPCManager(pipeline, executor)
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        asyncOp := manager.CallAsync(ctx, peerID, req)
        _, _ = asyncOp.Await(ctx)
    }
}

// 对比测试
func BenchmarkRPCBroadcast(b *testing.B) {
    // 旧实现
    b.Run("Old", func(b *testing.B) {
        // ...
    })
    
    // 新实现
    b.Run("New", func(b *testing.B) {
        // ...
    })
}
```

---

## 8. 总结

### 9.1 预期收益

| 指标 | 当前 | 目标 | 提升 |
|------|------|------|------|
| 架构一致性 | 低（两套模型） | 高（统一 Task） | +50% |
| 代码复杂度 | 高（信号量+Submit） | 低（批量提交） | -30% |
| CPU 亲和性 | 低（SourceNetwork） | 高（策略选择） | +20% |
| 回调稳定性 | 中（混合执行） | 高（统一 Executor） | +15% |

### 9.2 关键决策

1. **统一 Task 模型**: 将 RPC 层的 AsyncOp 改为使用 V4 Task[Result]
2. **批量提交**: TaskExecutor 添加 SubmitBatch 接口
3. **SourceID 策略**: 根据请求类型选择合适的 SourceID
4. **回调统一**: 强制回调走 Executor，添加重试机制

### 9.3 下一步行动

- [ ] 评审本 Proposal
- [ ] 创建实施任务
- [ ] 设计详细接口
- [ ] 开发原型验证
- [ ] 性能基准测试

---

## 附录

### A. 参考文档

- [V4 异步管道架构](../07_spike/2026-03-04-spike-async-pipeline-v4.md)
- [DDD Interface v3.1](../07_spike/2026-02-18_spike_nexkv-ddd-interface.md)
- [统一执行器架构](../07_spike/2026-02-25_spike-glm-unified-executor.md)

### B. V4 基础组件详解

本 Proposal 依赖以下 V4 异步管道基础组件：

#### B.1 TaskRunner - 非泛型任务接口（Executor 视角）

```go
type TaskRunner interface {
    Run(ctx context.Context, p *Pipeline)
    Priority() Priority
    SourceID() model.SourceID
}
```

**职责**: Executor 只看到这个接口，用于任务调度和执行。

#### B.2 Task[Result] - 泛型任务接口（用户视角）

```go
type Task[Result any] interface {
    TaskRunner  // 嵌入 TaskRunner（类型擦除点）
    Execute(ctx context.Context, p *Pipeline) (Result, error)
}
```

**职责**: 用户实现此接口，提供类型安全的任务执行。

#### B.3 BaseTask[Result] - 任务基类

```go
type BaseTask[Result any] struct {
    opType   OpType
    priority Priority
    sourceID model.SourceID
    done     chan struct{}
    result   Result
    err      error
}

func (b *BaseTask[Result]) Run(ctx context.Context, p *Pipeline)
func (b *BaseTask[Result]) Wait() (Result, error)
func (b *BaseTask[Result]) Done() <-chan struct{}
```

**职责**: 提供通用实现，用户只需实现 Execute 方法。

#### B.4 Pipeline - 流水线上下文

```go
type Pipeline struct {
    ctx      context.Context
    cancel   context.CancelFunc
    executor service.TaskExecutor
}

func (p *Pipeline) Submit(task TaskRunner) error
```

**职责**: 提供任务提交入口，聚合执行器。

#### B.5 PerCoreExecutor - 每核心执行器

```go
type PerCoreExecutor struct {
    workers []Worker
    queues  map[model.SourceID]*PriorityQueue
}

func (e *PerCoreExecutor) Submit(ctx context.Context, sourceID model.SourceID, 
    priority TaskPriority, task func(context.Context)) error
```

**关键约束**: Task 内部禁止嵌套 Submit（死锁风险）

#### B.6 Priority - 任务优先级

```go
type Priority int

const (
    PriorityCritical Priority = iota  // 0: 最高优先级
    PriorityHigh      Priority = 1    // 1: 高优先级
    PriorityNormal    Priority = 2    // 2: 普通优先级
    PriorityLow       Priority = 3    // 3: 低优先级
)
```

**职责**: 统一优先级定义，用于任务调度。

### C. 术语表

| 术语 | 说明 |
|------|------|
| Task[Result] | V4 泛型任务接口 |
| TaskRunner | V4 非泛型任务接口（Executor 视角） |
| BaseTask | V4 任务基类 |
| AsyncOp | 异步操作句柄 |
| SourceID | 任务来源标识，用于 CPU 亲和性 |
| Pipeline | V4 异步流水线上下文 |
| PerCoreExecutor | 每核心执行器，提供 CPU 亲和性 |
| Priority | 任务优先级（Critical/High/Normal/Low） |

---

**文档版本**: v1.1
**最后更新**: 2026-03-05

---

## 10. 专家审查结果

> **审查日期**: 2026-03-05  
> **审查专家**: DDD架构专家 + Go并发编程专家  
> **审查方法**: 代码库探索 + 架构分析 + 最佳实践对照  
> **总体评级**: **B+ (良好，需要针对性优化)**

### 10.1 审查概述

本提案经过**DDD架构专家**和**Go并发编程专家**的全面审查。审查范围包括：
- ✅ 代码库深度探索（RPC层、V4管道、执行器、性能测试）
- ✅ DDD架构评估（聚合根、领域服务、限界上下文、领域事件）
- ✅ Go并发审查（goroutine安全、channel使用、死锁预防、性能优化）

### 10.2 Proposal优点确认

专家审查确认了本提案的以下优点：

#### ✅ 架构设计优点
1. **架构统一性提升** - 统一Task[Result]模型，消除双重模型是正确方向
2. **性能优化方向正确** - CPU亲和性策略、批量提交、SourceID优化策略合理
3. **问题诊断准确** - 识别了回调不一致、并发控制复杂等核心问题
4. **架构简洁性** - 新项目直接采用Task[Result]模型，无需适配器层

#### ✅ DDD架构优点
1. **值对象设计优秀** - `SourceID` 不可变设计，包含完整验证逻辑
2. **接口隔离原则得当** - `TaskExecutor` 拆分为多个子接口，职责清晰
3. **异步操作抽象完整** - `AsyncOp[T]` 泛型接口设计类型安全
4. **限界上下文边界清晰** - 5层DDD架构职责明确

### 10.3 发现的关键问题

#### 🔴 P0 - 严重问题（立即修复）

专家审查发现了3个需要立即修复的严重问题，这些问题**不在原proposal中**：

##### 问题 P0-1: AsyncOp回调执行死锁风险

**位置**: `internal/infrastructure/rpc/asyncop_impl.go:154-182`

```go
// ❌ 问题代码（当前实现）
func (op *asyncOpImpl[T]) registerCallback(callback func(T, error)) string {
    op.cbMu.Lock()
    defer op.cbMu.Unlock()
    
    if op.done.Load() {
        v := op.value
        e := op.err
        defer op.safeExecuteCallback(callback, v, e)  // ⚠️ 持锁执行回调
        return ""
    }
    // ...
}
```

**风险分析**:
- 回调函数内部如果再次获取 `cbMu` 锁，会导致死锁
- 违反"不要在持锁期间执行不可控代码"原则
- 这是**现有代码**的问题，需要在V4改造前修复

**修复方案**:
```go
// ✅ 正确实现
if op.done.Load() {
    v := op.value
    e := op.err
    op.cbMu.Unlock()  // 先解锁
    op.safeExecuteCallback(callback, v, e)  // 再执行回调
    return ""
}
```

**影响**: 
- 必须在**V4改造前**修复此问题
- 否则方案4（回调统一走Executor）会放大死锁风险
- 工作量：1小时

**专家审查补充说明**:

> **注意**: Go并发专家审查发现，当前代码实际上已经部分修复了死锁问题：
> 
> ```go
> // 当前实际代码（已部分修复）
> if op.done.Load() {
>     op.cbMu.Lock()
>     v := op.value
>     e := op.err
>     op.cbMu.Unlock()  // ✅ 第一重检查已显式解锁
>     op.safeExecuteCallback(callback, v, e)
>     return ""
> }
> ```
> 
> 第二重检查仍使用defer，虽然执行时机正确（函数返回时已解锁），但为了代码更直观，建议改为显式解锁：
> 
> ```go
> // 建议优化（更直观）
> op.cbMu.Lock()
> if op.done.Load() {
>     v := op.value
>     e := op.err
>     op.cbMu.Unlock()  // 显式解锁
>     op.safeExecuteCallback(callback, v, e)
>     return ""
> }
> ```
> 
> **优先级**: P1（建议优化，非必须）
> **实际工作量**: 10分钟（代码优化）

---

##### 问题 P0-2: PerCoreExecutor CPU绑核失效

**位置**: `internal/infrastructure/concurrency/executor_percore.go:803-806`

**问题代码（当前实现）**:
```go
func (w *coreWorker) run() {
    defer w.executor.wg.Done()
    
    // 启用绑核
    if !w.pinned {
        runtime.LockOSThread()
        defer runtime.UnlockOSThread()  // ❌ 这会在worker退出时解锁
        
        if err := pinToCore(w.coreID); err == nil {
            w.pinned = true
        }
    }
    // ... 主循环 ...
}
```

**问题分析**:
1. **违反设计意图**: Worker应该永久绑定到OS线程，在**整个生命周期内永不解锁**
2. **defer执行时机错误**: `defer runtime.UnlockOSThread()` 会在worker goroutine退出时执行
3. **后果严重**:
   - 如果worker panic后恢复，goroutine继续运行但已解锁
   - 解锁后的goroutine可能被Go调度器迁移到其他OS线程
   - CPU亲和性失效，PerCoreExecutor的性能优势丧失

**修复方案**:
```go
// ✅ 正确实现
func (w *coreWorker) run() {
    defer w.executor.wg.Done()
    
    // 永久绑定到OS线程
    runtime.LockOSThread()
    // ❌ 移除 defer runtime.UnlockOSThread()
    // Worker在整个生命周期内保持绑定，goroutine退出时自动失效
    
    // 尝试绑核（每次都尝试，幂等操作）
    if err := pinToCore(w.coreID); err != nil {
        logrus.Warnf("[PerCore] Failed to pin worker %d to core %d: %v", 
            w.coreID, w.coreID, err)
    }
    
    // 主循环
    for {
        // ... worker主循环 ...
    }
}
```

**关键点说明**:
1. **为什么不需要UnlockOSThread**: 
   - Worker goroutine在Close()前不会退出
   - 即使退出，LockOSThread也会自动失效，无需显式Unlock
   - 显式Unlock反而会在worker生命周期内解除绑定，违反设计意图

2. **为什么移除pinned标志检查**:
   - `run()` 方法只在worker启动时调用一次
   - 每次都尝试绑核是幂等操作，不会造成问题
   - 如果worker重启（例如panic恢复），会重新绑核

3. **平台兼容性**:
   - Linux/Windows: `pinToCore` 内部会调用系统API进行绑核
   - macOS: 可能不支持绑核，但LockOSThread仍然有效

**影响**:
- 🔴 **必须在V4改造前修复** - 否则SourceID策略的CPU亲和性无法生效
- ✅ 修复简单，风险低
- ⏱️ **工作量**: 5分钟（仅需删除一行代码并添加注释）

---

##### 问题 P0-3: pendingCalls内存泄漏

**位置**: `internal/infrastructure/transport/libp2p_rpc.go:595-616`

**风险分析**:
- 超时的调用没有清理 `pendingCalls` map
- 长时间运行会导致内存泄漏
- 影响RPC层稳定性

**修复方案**:
```go
// 添加超时清理
go func() {
    <-callCtx.Done()
    if callCtx.Err() == context.DeadlineExceeded {
        r.unregisterPendingCall(requestID)
    }
}()
```

**影响**:
- 建议在**V4改造前**修复
- 工作量：2小时

---

#### 🟡 P1 - Proposal需补充的内容

专家审查发现proposal中缺少以下关键设计：

##### 问题 P1-1: 缺少Task聚合根设计

**现状**: Proposal中的Task仅是数据结构，缺少完整的聚合根设计

**DDD建议**: 需要补充以下设计：
```go
// ✅ 建议的聚合根设计
type Task struct {
    id          TaskID           // 值对象
    sourceID    model.SourceID   // 值对象
    priority    TaskPriority     // 值对象
    status      TaskStatus       // 实体状态
    createdAt   time.Time
    startedAt   *time.Time
    completedAt *time.Time
    result      interface{}
    err         error
}

// TaskID 值对象
type TaskID struct {
    value string
}

// 状态机方法
func (t *Task) Start() error { ... }
func (t *Task) Complete(result interface{}) error { ... }
func (t *Task) Fail(err error) error { ... }
```

**建议**: 在Phase 1实施计划中补充聚合根设计

---

##### 问题 P1-2: 缺少TaskRepository接口

**现状**: Task持久化和查询逻辑散落各处

**DDD建议**: 引入Repository模式：
```go
type TaskRepository interface {
    Save(ctx context.Context, task *Task) error
    FindByID(ctx context.Context, id TaskID) (*Task, error)
    FindByStatus(ctx context.Context, status TaskStatus) ([]*Task, error)
}
```

**建议**: 补充Repository接口设计

---

##### 问题 P1-3: 批量提交缺少并发安全保证

**现状**: 方案2的 `SubmitBatch` 缺少并发安全说明

**需要补充**:
1. 是否原子性提交？
2. 部分失败如何处理？
3. 是否支持事务回滚？

**建议**: 补充批量提交的并发安全设计和背压控制策略：
```go
type BackpressurePolicy int
const (
    BackpressureReject BackpressurePolicy = iota
    BackpressureBlock
    BackpressureDrop
)
```

---

##### 问题 P1-4: SourceID策略缺少配置接口

**现状**: 方案3的策略是硬编码的

**需要补充**:
```go
type SourceIDStrategyConfig struct {
    DefaultStrategy SourceIDStrategy
    RaftStrategy    SourceIDStrategy
    ClientStrategy  SourceIDStrategy
}
```

**建议**: 补充策略配置接口和验证测试

---

##### 问题 P1-5: 缺少领域事件机制

**现状**: Proposal中完全没有提到领域事件

**DDD建议**: 引入关键领域事件：
- `TaskCompleted` - 任务完成事件
- `TaskFailed` - 任务失败事件
- `RPCCompleted` - RPC调用完成事件
- `BroadcastMajorityReached` - 广播多数派达成事件

**建议**: 在Phase 2或单独迭代中引入事件驱动架构

---

#### 🟢 P2 - 性能优化建议

##### 问题 P2-1: 缺少sync.Pool优化

**现状**: 当前实现中没有使用 `sync.Pool` 复用高频对象

**优化机会**:
1. `taskItem` 对象池 - 减少任务分配GC压力
2. `Message` 缓冲区池 - 减少序列化内存分配
3. `ResponseMsg` 对象池 - 高频响应对象复用

**建议**: 在P1问题修复后引入sync.Pool优化

---

##### 问题 P2-2: 全局绑定锁性能瓶颈

**位置**: `executor_percore.go:490-516`

**问题**: `bindingMu` 是全局锁，所有首次绑定操作串行化

**优化方案**:
- 使用分片锁（sharded lock）
- 或使用无锁CAS操作

---

### 10.4 Proposal改进建议

#### 建议1: 调整实施优先级

根据专家审查结果，建议调整实施优先级：

**新的优先级排序**:

| 优先级 | 改进项 | 调整原因 | 预估工期 |
|--------|-------|---------|---------|
| **P0** | 修复AsyncOp回调死锁 | 阻塞V4改造 | 10分钟（P1优化） |
| **P0** | 修复PerCoreExecutor绑核 | 影响性能 | 5分钟 |
| **P0** | 修复pendingCalls泄漏 | 影响稳定性 | 2小时 |
| **P1** | 统一Task模型（含聚合根） | 架构一致性 | 1.5周 |
| **P1** | SourceID策略（含配置） | 性能提升 | 4天 |
| **P1** | 批量提交（含并发安全） | 高并发优化 | 5天 |
| **P1** | 回调统一走Executor | 稳定性 | 2天 |
| **P2** | 引入sync.Pool优化 | GC优化 | 6小时 |
| **P2** | 引入领域事件 | 事件驱动 | 2天 |

#### 建议2: 补充DDD设计

在Phase 1中补充以下DDD设计：
1. Task聚合根设计（TaskID值对象、状态机）
2. TaskRepository接口定义
3. 领域事件定义（可选，可延后到P2）

#### 建议3: 补充并发安全设计

在方案2和方案3中补充：
1. 批量提交的原子性保证
2. 背压控制策略
3. SourceID策略的配置接口

#### 建议4: 补充测试策略

在测试策略章节补充：
1. **并发安全测试** - 竞态条件、goroutine泄漏
2. **死锁检测测试** - 回调嵌套调用场景
3. **CPU亲和性验证** - SourceID路由正确性

```go
// 补充的测试用例
func TestAsyncOp_RaceCondition(t *testing.T) {
    // 100个goroutine并发注册回调、完成、等待
}

func TestRPC_DeadlockDetection(t *testing.T) {
    // 在回调中再次调用RPC（死锁场景）
}

func TestSourceID_CPU_Affinity(t *testing.T) {
    // 验证相同SourceID路由到同一Worker
}
```

### 10.5 更新的风险评估

基于专家审查，更新风险评估：

| 风险 | 评级 | 说明 |
|------|------|------|
| 性能回退 | **中** | 充分基准测试，保留回滚方案 |
| SourceID选择不当 | **中** | 添加配置接口后降低 |
| 回调延迟 | **中** | 需添加超时和降级 |
| **并发安全问题** | **高** | P0问题必须先修复 |
| **DDD设计缺陷** | **中** | 补充聚合根设计 |
| **内存泄漏** | **高** | P0问题必须先修复 |

### 10.6 实施前置条件

根据专家审查，V4改造前**必须完成**以下工作：

#### ✅ 必须完成（P0）

1. **删除AsyncOp，直接替换为Task[Result]** (`asyncop_impl.go`)
   - 工作量：30分钟（直接删除，无需修复）
   - 阻塞：Phase 1实施
   - 说明：新项目无需向后兼容，直接删除AsyncOp相关代码

2. **修复PerCoreExecutor绑核** (`executor_percore.go:803-806`)
   - 工作量：30分钟
   - 阻塞：方案3效果验证

3. **修复pendingCalls泄漏** (`libp2p_rpc.go:595-616`)
   - 工作量：2小时
   - 阻塞：稳定性验证

#### ✅ 强烈建议（P1）

4. **补充Task聚合根设计**
   - 工作量：4小时
   - 收益：架构完整性

5. **补充批量提交并发安全设计**
   - 工作量：2小时
   - 收益：避免生产问题

6. **补充测试策略**
   - 工作量：1天
   - 收益：保证改造质量

### 10.7 专家建议总结

#### DDD架构专家建议

**必须实施**：
1. ✅ 引入Task聚合根，明确生命周期管理
2. ✅ 补充TaskRepository接口定义
3. ⚠️ 拆分RPCAsync接口（可延后到P2）

**强烈建议**：
1. 引入领域事件机制（可延后到P2）
2. 使用规格模式封装业务规则（可选）
3. 完善值对象体系（PeerID等）

**实施风险**：
- 聚合根重构影响面大，需分阶段迁移
- 事件驱动架构引入复杂度，先在非关键路径试点

#### Go并发专家建议

**立即修复（P0）**：
1. ✅ AsyncOp回调持锁执行死锁风险
2. ✅ PerCoreExecutor绑核失效
3. ✅ pendingCalls内存泄漏

**性能优化（P2）**：
1. 优化全局绑定锁，使用分片锁
2. 引入sync.Pool减少GC压力
3. 优化taskQueue数据结构

**测试建议**：
1. ✅ 添加并发压力测试
2. ✅ 添加goroutine泄漏检测
3. ✅ 添加死锁检测测试

### 10.8 下一步行动计划

#### 立即行动（24小时内）

```bash
# 1. 修复P0并发安全问题
编辑 internal/infrastructure/rpc/asyncop_impl.go
编辑 internal/infrastructure/concurrency/executor_percore.go
编辑 internal/infrastructure/transport/libp2p_rpc.go

# 2. 补充Proposal设计
补充 Task聚合根设计章节
补充 批量提交并发安全章节
补充 测试策略章节

# 3. 更新实施计划
调整 优先级排序
添加 前置条件说明
```

#### 本周行动（P1）

- 补充DDD设计（Task聚合根、Repository）
- 补充并发安全设计（批量提交、背压控制）
- 补充测试策略（并发、死锁、亲和性）

#### 下周行动（开始V4改造）

- P0问题验证通过后，开始Phase 1实施
- 按照新的优先级排序执行

### 10.9 验收标准更新

基于专家审查，更新验收标准：

#### 代码质量标准

- ✅ 修复所有P0并发安全问题（**新增**）
- ✅ Task聚合根设计完整（**新增**）
- ✅ RPC接口拆分符合SRP
- ✅ 回调执行无死锁风险（**新增**）
- ✅ CPU绑核逻辑正确（**新增**）
- ✅ 批量提交并发安全（**新增**）

#### 测试覆盖率标准

- ✅ 并发测试覆盖率 ≥ 90%（**新增**）
- ✅ 竞态条件测试完整（**新增**）
- ✅ 死锁检测测试通过（**新增**）
- ✅ Goroutine泄漏测试通过（**新增**）
- ✅ CPU亲和性验证通过（**新增**）

#### 性能基准标准

- ✅ RPC吞吐量 ≥ 10K ops/sec（保持）
- ✅ Task提交延迟 ≤ 200 ns/op
- ✅ 广播并发度提升 ≥ 20%
- ✅ GC压力降低 ≥ 30%（**新增**）

---

## 11. 专家审查结论

### 11.1 总体评价

**评级**: B+ (良好，需要针对性优化)

本Proposal的**方向正确、设计合理**，但需要：
1. 🔴 **先修复P0并发安全问题**（前置条件）
2. 🟡 **补充DDD设计细节**（Task聚合根、Repository）
3. 🟡 **补充并发安全保证**（批量提交、背压控制）
4. 🟢 **优化性能**（sync.Pool、分片锁）

### 11.2 关键决策

经过专家审查，确认以下关键决策：

1. ✅ **统一Task模型** - 方向正确，需补充聚合根设计
2. ✅ **批量提交接口** - 方向正确，需补充并发安全保证
3. ✅ **SourceID策略** - 方向正确，需补充配置接口
4. ⚠️ **回调统一走Executor** - 需先修复P0死锁风险

### 11.3 最终建议

**建议批准本Proposal**，但需按以下顺序执行：

1. **Phase 0**: 修复P0并发安全问题（约2.5小时）
2. **Phase 1**: 补充Proposal设计细节（1天）
3. **Phase 2**: 开始V4改造实施（1-1.5周，无需向后兼容）

---

## 12. 性能测试方案

> **测试日期**: 2026-03-05
> **测试目标**: 对比 RPC 层改造前后的性能差异
> **测试范围**: Transport RPC 层
> **优先级**: P1

### 12.1 测试目标

#### 核心指标

| 指标 | 说明 | 目标改进 |
|------|------|---------|
| **延迟 (Latency)** | P50/P99 响应时间 | 降低 10-20% |
| **吞吐 (Throughput)** | 每秒请求数 (QPS) | 提升 20-30% |
| **CPU 亲和性** | 同 SourceID 任务在同一 Worker | 提升 15% |
| **内存分配** | 每次请求的内存分配 | 减少 20% |
| **Goroutine 数量** | 并发时的 goroutine 数 | 减少 30% |

#### 测试场景

| 场景 | 描述 | 重要性 |
|------|------|--------|
| **单节点点对点** | 1 Client → 1 Server | 基准测试 |
| **广播发送** | 1 → N 广播 | 核心场景 |
| **并发请求** | 多 Client 并发 | 压力测试 |
| **异步回调** | Task 回调性能 | 稳定性 |

### 12.2 测试环境

#### 硬件环境

```yaml
CPU: 8 cores / 16 threads
Memory: 16GB
Network: localhost (排除网络变量)
OS: Linux 5.15
Go: 1.21+
```

#### 软件配置

```go
// Executor 配置
AntsPoolExecutor:
  - MaxWorkers: 100
  - QueueSize: 1000

PerCoreExecutor:
  - WorkersPerCore: 2
  - QueueSizePerWorker: 100
```

### 12.3 测试代码

#### 场景 1：单节点点对点延迟

```go
// 改造前：使用 AsyncOp + GoroutineProvider
func BenchmarkRPCCallAsync_Old(b *testing.B) {
    rpc := setupOldRPC()
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
        task := rpc.CallAsync(ctx, peerID, req)
        _, err := task.Wait(ctx)
        if err != nil {
            b.Fatal(err)
        }
    }
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
        task := rpc.CallAsync(ctx, peerID, req)
        _, err := task.Wait(ctx)
        if err != nil {
            b.Fatal(err)
        }
    }
}
```

#### 场景 2：广播发送吞吐

```go
// 改造前：信号量 + Submit
func BenchmarkRPCBroadcast_Old(b *testing.B) {
    rpc := setupOldRPC()
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
        task := rpc.BroadcastAsync(ctx, peers, req)
        _, err := task.Wait(ctx)
        if err != nil {
            b.Fatal(err)
        }
    }
}
```

#### 场景 3：并发压力测试

```go
// 并发压力测试
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
            task := rpc.CallAsync(ctx, peerID, req)
            _, err := task.Wait(ctx)
            if err != nil {
                b.Fatal(err)
            }
        }
    })
}
```

#### 场景 4：CPU 亲和性测试

```go
// 验证亲和性效果
func TestRPCAffinityEffect(t *testing.T) {
    rpc := setupNewRPC()
    defer rpc.Close()

    workerAssignments := make(map[model.SourceID]int)
    var mu sync.Mutex

    for shardID := 0; shardID < 10; shardID++ {
        for i := 0; i < 100; i++ {
            req := &model.Message{
                Type:    model.MsgTypeRaft,
                ShardID: uint64(shardID),
            }

            task := rpc.CallWithAffinity(context.Background(), peerID, req)
            resp, _ := task.Wait(context.Background())

            mu.Lock()
            workerAssignments[model.SourceShard(shardID)] = resp.WorkerID
            mu.Unlock()
        }
    }

    for shardID := 0; shardID < 10; shardID++ {
        sourceID := model.SourceShard(shardID)
        workerID := workerAssignments[sourceID]
        t.Logf("Shard %d -> Worker %d", shardID, workerID)
    }
}
```

### 12.4 测试执行

```bash
# 运行所有基准测试
go test -bench=. -benchmem -count=5 ./internal/transport/rpc/... > benchmark_results.txt

# 只运行改造前测试
go test -bench="Old" -benchmem -count=5 ./internal/transport/rpc/...

# 只运行改造后测试
go test -bench="New" -benchmem -count=5 ./internal/transport/rpc/...
```

### 12.5 预期结果

#### 改造后目标

| 场景 | 延迟 (P50) | 延迟 (P99) | QPS | 内存分配 | 改进 |
|------|-----------|-----------|-----|---------|------|
| 点对点 | 400μs | 1.6ms | 2500 | 4 allocs/op | +25% |
| 广播 (100) | 40ms | 80ms | 25 | 400 allocs/op | +25% |
| 并发 (100) | 800μs | 4ms | 6000 | 6 allocs/op | +20% |
| 回调 | - | - | 12000 | 2 allocs/op | +20% |

#### 成功标准

- [ ] 点对点延迟降低 ≥ 10%
- [ ] 广播吞吐提升 ≥ 20%
- [ ] 内存分配减少 ≥ 15%
- [ ] CPU 亲和性验证通过

---

**审查完成时间**: 2026-03-05  
**下次审查时间**: P0修复完成后  
**负责人**: 架构组 + 并发组  
**审查文档**: `/home/jzh/.claude/plans/harmonic-dazzling-quokka.md`
