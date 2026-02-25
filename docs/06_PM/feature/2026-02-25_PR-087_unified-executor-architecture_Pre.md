# 【PR全流程文档】Feature - 统一执行器架构

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从需求对齐到开发完成的全流程，一个PR对应一份全流程文档，归档后作为项目追溯依据。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 重构（Refactor）- Feature 范畴 |
| PR编号 | PR-087（创建GitHub PR后补充完整） |
| 分支名称 | `feature/PR-087-unified-executor-architecture` |
| 工作主题 | 统一执行器架构 - GoroutineProvider 接口拆分 + Per-Core 无锁执行器 + 可暂停调度器 |
| 负责人 | 🤖 核心开发 A |
| 分支创建日期 | 2026-02-25 |
| 计划开工日期 | 2026-02-25 |
| 计划CI通过日期 | 2026-03-15（2-3 周） |
| 关联需求单号 | M2 存储引擎 - 可暂停调度器核心依赖 |
| 架构师评审状态 | ⏳ 待评审 |
| 预审批结果 | ⏳ 待审批 |
| 参考文档 | [Spike 文档 v2.6](../../07_spike/2026-02-25_spike-glm-unified-executor.md) |

### 2. 背景与目标（为什么干）

#### 2.1 背景

**业务场景**：
- NexKV 需要构建**可暂停调度器**架构
- 将分布式 KV 请求的完整执行流程从「隐式的 goroutine 栈/回调链」变成「**可序列化、可持久化、可中断、可迁移、可回溯的显式状态机**」

**现有问题**：

| 问题 | 现状 | 影响 |
|------|------|------|
| **接口过大** | `GoroutineProvider` 13 个方法，职责混杂 | 难以测试、耦合度高 |
| **执行引擎性能** | ants 协程池有锁竞争，延迟抖动 | 高并发场景性能受限 |
| **可暂停能力缺失** | 无暂停/恢复/迁移机制 | 无法支持跨节点迁移 |

**价值**：

1. **接口拆分收益**：
   - 提升可测试性
   - 降低模块耦合
   - 100% 向后兼容

2. **Per-Core 执行器收益**：
   - 5-10x 吞吐量提升
   - 消除锁竞争
   - P99 延迟降低到 <10μs

3. **可暂停调度器收益**：
   - 支持任务暂停/恢复
   - 支持跨节点迁移
   - 支持执行状态回溯

#### 2.2 核心目标（可量化、可验证）

1. **功能目标**：
   - ✅ 实现接口拆分：GoroutineProvider 13 方法 → 4 核心 + 2 组合 + 4 可暂停调度器（10个）
   - ✅ 实现 PerCoreExecutor：每核单 goroutine，绑核执行，无锁设计
   - ✅ 实现向后兼容：100% 兼容现有代码

2. **性能目标**：
   - ✅ 吞吐量：≥ 2M ops/s（Ants 的 4x）
   - ✅ P99 延迟：< 10μs（Ants 的 5-10x）
   - ✅ 锁竞争：消除

3. **兼容性目标**：
   - ✅ 100% 向后兼容
   - ✅ 支持 Ants 降级

### 3. 技术方案（怎么干）

#### 3.1 接口拆分方案

> ⭐ **命名规范**: 采用与现有代码一致的 `TaskXxx` 命名模式
> ⭐ **简化设计**: 从 14 个接口简化为 10 个核心接口

```
Level 1: 核心接口（4个）
├── TaskExecutor: Execute(fn)                              // 基础任务执行
├── TaskScheduler: Schedule(delay, fn)                    // 延迟调度
├── TaskBatcher: SubmitBatch(tasks)                       // 批量提交
└── TaskManager: Stats/Resize/Release                    // 池管理

Level 2: 组合接口（2个）
├── AsyncTaskExecutor = TaskExecutor + TaskScheduler + TaskBatcher
└── FullTaskExecutor = AsyncTaskExecutor + TaskManager

Level 3: 可暂停调度器（4个）
├── StepExecutor: ExecuteSteps/PauseStep/ResumeStep      // 步骤执行
├── StepHandler: Execute/Rollback/IsPausable            // 步骤处理
├── CheckpointHandler: ExecuteToCheckpoint/ExecuteFromCheckpoint  // 检查点
└── MigrationHandler: PrepareMigrate/CommitMigrate/RollbackMigrate  // 迁移处理

Level 4: 向后兼容
└── GoroutineProvider = FullTaskExecutor (接口+工厂函数)
```

> 📝 **接口对比**: 现有 `TaskExecutor` 用于普通任务，新 `StepExecutor` 用于可暂停任务，两者是**互补关系**而非替代
> 📝 **向后兼容**: 通过工厂函数 `NewGoroutineProvider()` 运行时选择 Per-Core 或 Ants 实现

#### 3.2 领域事件设计

> ⭐ **新增**：定义领域事件，实现事件驱动架构

```go
// 任务事件
type TaskSubmittedEvent struct {
    TaskID    string
    Priority  Priority
    Timestamp time.Time
}

type TaskCompletedEvent struct {
    TaskID    string
    Duration  time.Duration
    Timestamp time.Time
}

type TaskFailedEvent struct {
    TaskID    string
    Error     error
    Timestamp time.Time
}

// 步骤执行事件
type StepPausedEvent struct {
    OpID      string
    StepIndex int
    Timestamp time.Time
}

type StepResumedEvent struct {
    OpID      string
    StepIndex int
    Timestamp time.Time
}

type StepCompletedEvent struct {
    OpID      string
    StepIndex int
    Duration  time.Duration
    Timestamp time.Time
}

// 检查点事件
type CheckpointCreatedEvent struct {
    CheckpointID string
    LSN          uint64
    ShardID      int
    Timestamp    time.Time
}

type CheckpointRestoredEvent struct {
    CheckpointID string
    LSN          uint64
    Timestamp    time.Time
}

// 背压事件
type QueueFullEvent struct {
    CoreID      int
    QueueLength int
    Strategy    string  // 触发的背压策略
    Timestamp   time.Time
}

// 暂停超时事件
type PauseTimeoutEvent struct {
    OpID        string
    StepIndex   int
    Timeout     time.Duration
    Timestamp   time.Time
}
```

#### 3.3 Per-Core 执行器设计

```go
type PerCoreExecutor struct {
    cpus   int
    workers []*coreWorker  // 每核心一个 worker
}

type coreWorker struct {
    taskC chan func(context.Context)  // 任务通道
    coreID int
}
```

**核心特性**：
- 每 CPU 核心一个 goroutine
- LockOSThread 绑核执行
- 一对一 channel 原生暂停语义
- 无锁设计

**优雅关闭流程**：
```
1. 接收关闭信号（SIGINT/SIGTERM）
2. 设置关闭标志，拒绝新任务提交
3. 等待正在执行的任务完成（默认超时 30s，可配置）
4. 超时后强制取消（context cancel）
5. 清理资源（关闭 channel，释放内存）
6. 发布 ShutdownCompleteEvent 事件
```

**资源清理策略**：
| 资源类型 | 策略 | 配置 |
|---------|------|------|
| pausedOps | TTL 1小时自动清理 | `PausedOpTTL` |
| 任务队列 | 每核最大 100MB | `MaxQueueSizePerCore` |
| goroutine | 空闲 5 分钟释放 | `IdleTimeout` |
| Checkpoint | 保留最近 N 个 | `MaxCheckpointCount` |

#### 3.3 可暂停调度器设计

```go
type StepExecutor interface {
    ExecuteSteps(ctx context.Context, steps []Step, stepCtx *StepContext) error
    PauseStep(opID string) error
    ResumeStep(opID string) error
}

type CheckpointHandler interface {
    ExecuteToCheckpoint(ctx context.Context, stepCtx *StepContext, cp Checkpoint) error
    ExecuteFromCheckpoint(ctx context.Context, stepCtx *StepContext, cp Checkpoint) error
}
```

**Checkpoint 持久化方案**：
```go
// Checkpoint 结构
type Checkpoint struct {
    ID        string    // 唯一标识
    LSN       uint64    // 日志序列号
    ShardID   int       // 分片 ID
    Step      Step      // 当前步骤
    Data      []byte    // 序列化状态
    Timestamp time.Time // 创建时间
    Term      uint64    // 任期号
}

// 持久化策略
// 1. 每个 Checkpoint 写入独立文件（checkpoint_{shard}_{lsn}.cp）
// 2. 保留最近 N 个 Checkpoint，定期清理旧文件
// 3. 启动时加载最新 Checkpoint，从 LSN 位置重放 WAL
```

**迁移恢复机制**：
```go
// MigrationRecovery 迁移恢复
type MigrationRecovery struct {
    MigrationID string    // 迁移 ID
    LastPhase   Phase    // 最后完成的阶段 (1-5)
    Snapshot    []byte   // 已传输的数据
    Term        uint64   // 当时的 Term
    Timestamp   time.Time // 更新时间
}

// 恢复流程
// 1. 启动时检查是否存在未完成的迁移
// 2. 根据 LastPhase 决定从哪个阶段继续
// 3. 验证 Term 是否有效（防止过期迁移）
// 4. 继续执行剩余阶段（幂等操作）
```

**背压策略**：
```go
// 背压策略接口
type BackpressureStrategy interface {
    OnQueueFull(ctx context.Context, task Task) error
}

// 策略实现
type DropOldestStrategy struct{}
type DropNewestStrategy struct{}
type BlockStrategy struct {
    MaxWait time.Duration
}
type RejectStrategy struct{}

// 使用示例
config := &ExecutorConfig{
    Backpressure: DropOldestStrategy{}, // 默认策略
}
```

| 策略 | 适用场景 | 特点 |
|------|---------|------|
| **DropOldest** | 高吞吐场景 | 丢弃等待最久的任务 |
| **DropNewest** | 实时性场景 | 丢弃最新任务，保护已有任务 |
| **Block** | 低延迟场景 | 阻塞等待，有超时限制 |
| **Reject** | 金融场景 | 直接拒绝，保证不丢数据 |

#### 3.4 监控指标设计

> ⭐ **新增**：Prometheus 监控指标，完整可观测性支持

```go
// Prometheus 指标定义
var (
    // 任务计数器
    tasksSubmittedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nexkv_tasks_submitted_total",
            Help: "Total number of tasks submitted",
        },
        []string{"priority", "executor_type"},
    )

    tasksCompletedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nexkv_tasks_completed_total",
            Help: "Total number of tasks completed",
        },
        []string{"priority", "executor_type"},
    )

    tasksFailedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nexkv_tasks_failed_total",
            Help: "Total number of tasks failed",
        },
        []string{"priority", "error_type"},
    )

    // 任务延迟
    taskDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "nexkv_task_duration_seconds",
            Help:    "Task execution duration",
            Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5},
        },
        []string{"priority"},
    )

    // 当前状态
    activeTasks = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "nexkv_active_tasks",
            Help: "Number of currently executing tasks",
        },
        []string{"executor_type"},
    )

    queueLength = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "nexkv_queue_length",
            Help: "Current queue length per core",
        },
        []string{"core"},
    )

    goroutineCount = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "nexkv_goroutine_count",
            Help: "Current number of goroutines in pool",
        },
    )

    // === 步骤执行指标 ===
    stepPausedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nexkv_steps_paused_total",
            Help: "Total number of steps paused",
        },
        []string{"op_id"},
    )

    stepResumedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nexkv_steps_resumed_total",
            Help: "Total number of steps resumed",
        },
        []string{"op_id"},
    )

    stepDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "nexkv_step_duration_seconds",
            Help:    "Step execution duration",
            Buckets: []float64{0.001, 0.01, 0.1, 1, 10},
        },
        []string{"op_id", "step_index"},
    )

    // === 检查点指标 ===
    checkpointCreatedTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "nexkv_checkpoints_created_total",
            Help: "Total number of checkpoints created",
        },
    )

    checkpointRestoredTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "nexkv_checkpoints_restored_total",
            Help: "Total number of checkpoints restored",
        },
    )

    // === 背压指标 ===
    backpressureTriggeredTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "nexkv_backpressure_triggered_total",
            Help: "Total number of backpressure events",
        },
        []string{"strategy", "core"},
    )
)
```

| 指标名称 | 类型 | 说明 |
|---------|------|------|
| nexkv_tasks_submitted_total | Counter | 任务提交总数 |
| nexkv_tasks_completed_total | Counter | 任务完成总数 |
| nexkv_tasks_failed_total | Counter | 任务失败总数 |
| nexkv_task_duration_seconds | Histogram | 任务执行延迟 |
| nexkv_active_tasks | Gauge | 当前执行中任务数 |
| nexkv_queue_length | Gauge | 每核队列长度 |
| nexkv_goroutine_count | Gauge | goroutine 池数量 |
| nexkv_steps_paused_total | Counter | 步骤暂停总数 |
| nexkv_steps_resumed_total | Counter | 步骤恢复总数 |
| nexkv_step_duration_seconds | Histogram | 步骤执行延迟 |
| nexkv_checkpoints_created_total | Counter | 检查点创建总数 |
| nexkv_checkpoints_restored_total | Counter | 检查点恢复总数 |
| nexkv_backpressure_triggered_total | Counter | 背压触发总数 |

#### 3.5 配置详细设计

> ⭐ **新增**：完整的配置结构设计

```go
// ExecutorConfig 执行器配置
type ExecutorConfig struct {
    // === 核心配置 ===
    CoreCount int           // 0 = auto (runtime.NumCPU())，默认 0
    QueueSize int          // 每核队列大小，默认 1000

    // === 优雅关闭配置 ===
    ShutdownTimeout time.Duration  // 关闭超时，默认 30s

    // === 资源清理配置 ===
    PausedOpTTL     time.Duration  // 暂停任务 TTL，默认 1h
    MaxQueueMemory  int64           // 每核最大内存，默认 100MB
    IdleTimeout     time.Duration   // goroutine 空闲超时，默认 5m
    MaxCheckpointCount int          // Checkpoint 保留数量，默认 10

    // === 背压策略 ===
    Backpressure BackpressureStrategy  // 默认 DropOldest

    // === 监控配置 ===
    EnableMetrics bool               // 是否启用监控，默认 true
    MetricsPort  string             // 监控端口，默认 ":9090"，支持环境变量 NEXKV_METRICS_PORT
}

// MetricsPort 配置说明
// 支持三种方式配置：
// 1. 默认 ":9090" - 监听所有网卡
// 2. 环境变量 NEXKV_METRICS_PORT 优先级更高
// 3. ":0" 表示动态端口，由系统分配

// 默认配置
var DefaultConfig = &ExecutorConfig{
    CoreCount:         0,
    QueueSize:        1000,
    ShutdownTimeout:   30 * time.Second,
    PausedOpTTL:       1 * time.Hour,
    MaxQueueMemory:    100 * 1024 * 1024, // 100MB
    IdleTimeout:       5 * time.Minute,
    MaxCheckpointCount: 10,
    Backpressure:      DropOldestStrategy{},
    EnableMetrics:     true,
    MetricsPort:       ":9090",
}
```

#### 3.6 Benchmark 测试设计

> ⭐ **新增**：具体的性能测试代码路径和场景

**测试文件**：`internal/infrastructure/concurrency/percore_executor_test.go`

```go
// === 吞吐量测试 ===

// BenchmarkPerCoreExecutor_Throughput_Simple 纯任务提交吞吐量
func BenchmarkPerCoreExecutor_Throughput_Simple(b *testing.B) {
    executor := NewPerCoreExecutor(DefaultConfig)
    defer executor.Shutdown()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        executor.Submit(context.Background(), func(ctx context.Context) {
            // 空任务
        })
    }
}

// BenchmarkPerCoreExecutor_Throughput_WithResult 带结果任务吞吐量
func BenchmarkPerCoreExecutor_Throughput_WithResult(b *testing.B) {
    executor := NewPerCoreExecutor(DefaultConfig)
    defer executor.Shutdown()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        executor.SubmitWithResult(context.Background(), func(ctx context.Context) int {
            return i
        })
    }
}

// BenchmarkPerCoreExecutor_Throughput_Batch 批量任务吞吐量
func BenchmarkPerCoreExecutor_Throughput_Batch(b *testing.B) {
    executor := NewPerCoreExecutor(DefaultConfig)
    defer executor.Shutdown()

    tasks := make([]func(context.Context), 100)
    for i := range tasks {
        tasks[i] = func(ctx context.Context) {}
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        executor.SubmitBatch(context.Background(), tasks)
    }
}

// === 延迟测试 ===

// BenchmarkPerCoreExecutor_Latency_P50_P95_P99 延迟分布
func BenchmarkPerCoreExecutor_Latency_P50_P95_P99(b *testing.B) {
    executor := NewPerCoreExecutor(DefaultConfig)
    defer executor.Shutdown()

    var wg sync.WaitGroup
    ch := make(chan time.Duration, b.N)

    for i := 0; i < b.N; i++ {
        wg.Add(1)
        start := time.Now()
        executor.Submit(context.Background(), func(ctx context.Context) {
            defer wg.Done()
            ch <- time.Since(start)
        })
    }
    wg.Wait()
    close(ch)

    // 计算 P50/P95/P99
    var latencies []time.Duration
    for d := range ch {
        latencies = append(latencies, d)
    }
    sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

    b.Logf("P50: %v", latencies[len(latencies)*50/100])
    b.Logf("P95: %v", latencies[len(latencies)*95/100])
    b.Logf("P99: %v", latencies[len(latencies)*99/100])
}

// === 对比测试 ===

// BenchmarkPerCoreExecutor_vs_Ants 与 Ants 对比
func BenchmarkPerCoreExecutor_vs_Ants(b *testing.B) {
    // Per-Core
    executor := NewPerCoreExecutor(DefaultConfig)
    defer executor.Shutdown()

    b.Run("PerCore", func(b *testing.B) {
        for i := 0; i < b.N; i++ {
            executor.Submit(context.Background(), func(ctx context.Context) {})
        }
    })

    // Ants
    antsPool, _ := ants.NewPool(DefaultAntsSize)
    defer antsPool.Release()

    b.Run("Ants", func(b *testing.B) {
        for i := 0; i < b.N; i++ {
            antsPool.Submit(func() {})
        }
    })
}
```

**性能目标**：

| 指标 | Per-Core 目标 | Ants 基准 | 提升 |
|------|--------------|----------|------|
| 吞吐量 | ≥ 2M ops/s | ~500K ops/s | **4x** |
| P50 延迟 | < 1μs | ~5μs | **5x** |
| P99 延迟 | < 10μs | ~50μs | **5x** |
| 内存占用 | 固定 | 动态 | **更可控** |

#### 3.4 分布式一致性设计

> ⚠️ **范围调整**：本 PR **专注于单机部分**，分布式设计移至后续 PR

**本 PR 范围（单机）**：
- [x] Per-Core 执行器实现
- [x] TaskExecutor 接口定义
- [x] 优雅关闭和资源清理
- [x] 队列满策略

**TODO（后续 PR）**：
- [ ] TermManager（Fencing Token 管理）
- [ ] Quorum（多数派确认）
- [ ] 5 阶段迁移协议
- [ ] 跨节点迁移

> 📝 **职责边界**: 本 PR Per-Core 执行器专注于**单机任务执行**，分布式一致性由后续 PR 实现

#### 3.5 向后兼容实现（适配器模式）

```go
// 适配器：组合多个小接口实现 GoroutineProvider
type GoroutineProviderAdapter struct {
    executor TaskExecutor
    scheduler ScheduledTaskExecutor
    prioritizer PriorityTaskExecutor
    batcher Batcher
    manager PoolManager
}

// 委托模式实现 GoroutineProvider 的 13 个方法
func (g *GoroutineProviderAdapter) Submit(ctx context.Context, task func(context.Context)) error {
    return g.executor.Execute(ctx, task)
}

func (g *GoroutineProviderAdapter) SubmitWithPriority(ctx context.Context, priority Priority, task func(context.Context)) error {
    return g.prioritizer.ExecuteWithPriority(ctx, priority, task)
}

// ... 其他方法类似委托
```

**接口+工厂函数实现运行时选择（支持降级）**:
```go
// GoroutineProvider 接口定义
type GoroutineProvider interface {
    FullTaskExecutor
}

// 工厂函数实现运行时选择
func NewGoroutineProvider(cfg ProviderConfig) GoroutineProvider {
    if cfg.UsePerCore && perCoreAvailable() {
        return NewPerCoreAdapter(perCoreExecutor)
    }
    // 降级到 Ants
    return NewAntsAdapter(antsPool)
}

// 降级检测
func perCoreAvailable() bool {
    // 检测 Per-Core 执行器是否可用
    return runtime.GOMAXPROCS(0) >= 2
}
```

#### 3.6 领域模型定义位置

> ⭐ **修正**：根据 DDD 原则调整模型放置位置

| 模型 | 定义文件 | 说明 |
|------|----------|------|
| `Step`, `StepContext` | `internal/domain/aggregate/step_aggregate.go` | 领域聚合根 |
| `MigrationState` | `internal/domain/service/migration_state.go` | 领域服务 |
| `Checkpoint` | `internal/infrastructure/persistence/checkpoint.go` | 基础设施（持久化） |
| `Priority` | 复用现有 `concurrency.go` | 已存在 |

**分层原则**：
- **domain/aggregate/**：包含业务规则的领域实体
- **domain/service**：跨实体的业务流程
- **infrastructure/persistence**：持久化机制（不属于核心域）

### 4. 实施计划（谁干、什么时候干）

#### 4.1 四阶段实施

```
Phase 1: 接口层准备（1-2 天）
├── 定义 TaskExecutor/CoreTaskExecutor/ScheduledTaskExecutor 接口
├── 定义 Step/Checkpoint/MigrationState 模型
├── 在 domain/model/ 下创建 step.go, checkpoint.go, migration.go
├── 实现适配器模式 GoroutineProviderAdapter
├── 添加向后兼容别名
└── 编译验证

Phase 2: Per-Core 执行器实现（2-3 天）
├── 实现 PerCoreExecutor
├── 实现队列满策略
├── 实现优雅关闭
├── 实现 pausedOps TTL 清理
└── 性能基准测试

Phase 3: 可暂停调度器集成（3-5 天）
├── 实现 PerCoreStepExecutor
├── 实现 Checkpoint 级别暂停/恢复
├── 实现跨节点迁移集成
├── RPC 模块迁移到小接口
└── 集成测试

Phase 4: 生产验证（1-2 周）
├── WAL 刷盘模块试点
├── 性能对比测试
├── 故障恢复测试
└── 全链路验证
```

#### 4.2 时间线

| 阶段 | 内容 | 周期 | 交付物 |
|------|------|------|--------|
| Phase 1 | 接口层准备 | 1-2 天 | `internal/domain/service/executor.go` |
| Phase 2 | Per-Core 实现 | 2-3 天 | `internal/infrastructure/concurrency/percore_executor.go` |
| Phase 3 | 调度器集成 | 3-5 天 | `internal/infrastructure/scheduler/percore_step_executor.go` |
| Phase 4 | 生产验证 | 1-2 周 | 测试报告 |

**总计**: 2-3 周（不含 Phase 4）

### 5. 验收标准（怎么算干完）

#### 5.1 功能验收

| 验收项 | 标准 |
|--------|------|
| 接口拆分 | 4 核心 + 2 组合 + 4 可暂停调度器（10个）接口定义完成 |
| Per-Core 执行器 | 每核单 goroutine，绑核执行 |
| 向后兼容 | 100% 兼容现有代码 |
| 暂停/恢复 | 支持 Checkpoint 级别暂停/恢复 |

#### 5.2 性能验收

| 指标 | 目标 | 测量方法 |
|------|------|----------|
| 吞吐量 | ≥ 2M ops/s | Benchmark 测试 |
| P99 延迟 | < 10μs | Latency 分布 |
| 锁竞争 | 消除 | -race 检测 |

#### 5.3 质量验收

| 指标 | 目标 |
|------|------|
| 测试覆盖率 | ≥ 80% |
| 编译通过 | `go build ./...` |
| 静态分析 | `go vet ./...` |
| 格式检查 | `gofmt -l ./...` |

#### 5.4 关键验收项

| 验收项 | 验收标准 | 测试方法 |
|--------|---------|----------|
| **向后兼容** | 100% 兼容现有 GoroutineProvider 调用 | 现有 47 个文件回归测试 |
| **降级方案** | Per-Core 不可用时自动切换到 Ants | 故障注入测试 |
| **优雅关闭** | 关闭时等待正在执行的任务完成 | 关闭超时测试 |
| **内存泄漏** | 长时间运行无内存泄漏 | 24h 压力测试 + pprof |
| **并发安全** | 多 goroutine 并发调用无数据竞争 | `go test -race` 全量测试 |
| **队列满策略** | 队列满时正确返回错误或阻塞 | 背压测试 |
| **CPU 绑核** | 任务绑定到指定 CPU 核心 | 绑核验证测试 |

### 6. 风险评估（可能遇到的问题）

| 风险 | 影响 | 缓解措施 | 范围 |
|------|------|----------|------|
| **LockOSThread CPU 亲和性** | 绑核不保证 CPU 亲和性 | 使用 runtime.GOMAXPROCS 限制 | 本PR |
| **Per-Core 内存隔离** | 单核 OOM 影响所有任务 | 实现内存限制和背压 | 本PR |
| **Context 传递** | 取消机制失效 | 使用独立 Context | 本PR |
| **迁移原子性** | 迁移中断可能数据不一致 | 2PC 协议 + 回滚 | TODO |
| **脑裂防护** | 旧节点继续写入 | Fencing Token + Term | TODO |
| **分布式一致性** | Per-Core 与 Quorum 集成 | 明确职责边界 | TODO |

### 7. 专家评审记录

#### 7.1 DDD 专家评审（第四轮 - 最终版）

> ⭐ **已优化**：接口简化 + 领域事件 + 向后兼容 + 监控指标 + 配置设计

| 严重程度 | 问题描述 | 修复建议 | 修复状态 |
|----------|----------|----------|----------|
| **P0** | 领域模型定义位置错误 | Checkpoint → infrastructure/persistence | ✅ 已修复 |
| **P0** | 向后兼容实现缺陷 | 接口+工厂函数实现运行时降级 | ✅ 已修复 |
| **P1** | 接口粒度过细 | 14个 → 10个（4核心+2组合+4可暂停） | ✅ 已修复 |
| **P1** | 缺少领域事件 | 添加 TaskSubmitted/Completed/StepPaused/QueueFull 等事件 | ✅ 已修复 |
| **P1** | 配置设计缺失 | 补充完整 ExecutorConfig + DefaultConfig | ✅ 已修复 |
| **P1** | 监控指标缺失 | 添加 Prometheus 指标（13个，含步骤级） | ✅ 已修复 |

**设计评分**: 4.8/5 ⬆️
**评审日期**: 2026-02-25

#### 7.2 分布式系统专家评审（第四轮 - 最终版）

> ⭐ **已优化**：优雅关闭 + 资源清理 + 背压策略 + 监控指标 + 配置设计 + Benchmark

| 严重程度 | 问题描述 | 修复建议 | 修复状态 |
|----------|----------|----------|----------|
| **P0** | Checkpoint 持久化方案不完整 | 补充结构和恢复流程 | ✅ 已修复 |
| **P1** | 优雅关闭缺失 | 添加 6 步关闭流程 | ✅ 已修复 |
| **P1** | 资源清理策略缺失 | 添加 TTL/内存/goroutine 回收策略 | ✅ 已修复 |
| **P1** | 背压策略缺失 | 添加 DropOldest/DropNewest/Block/Reject | ✅ 已修复 |
| **P1** | 监控指标缺失 | 添加 Prometheus 指标（13个，含步骤级） | ✅ 已修复 |
| **P1** | 配置设计缺失 | 补充完整 ExecutorConfig | ✅ 已修复 |
| **P1** | MetricsPort 端口冲突 | 改用环境变量 + 动态端口支持 | ✅ 已修复 |
| **P1** | Benchmark 设计缺失 | 添加测试代码路径和场景 | ✅ 已修复 |
| - | 分布式设计 | Quorum/TermManager/迁移协议 | 🔲 TODO |

**设计评分**: 4.8/5 ⬆️（单机范围）
**评审日期**: 2026-02-25
**核心结论**: ✅ 单机部分设计完整，分布式设计移至后续 PR

### 7.3 评审结论

> **设计方向**: ✅ 正确 - 接口拆分 + Per-Core 无锁执行器
> **范围调整**: 🎯 专注于单机部分，分布式移至 TODO
> **实施建议**: PR-087a（接口拆分）→ PR-087b（Per-Core）→ 分布式（后续）

### 7.4 修复优先级（本PR范围）

| 优先级 | 问题数 | 关键修复项 |
|--------|--------|-----------|
| **P0** | 3 | 领域模型位置、运行时降级、Checkpoint持久化 |
| **P1** | 2 | 接口粒度、领域事件 |

**分布式问题（后续PR）**：
- Quorum 多数派确认
- TermManager Fencing Token
- 5 阶段迁移协议
- 跨节点迁移

### 8. PR 拆分实施建议

> 🎯 **本轮目标**: 专注于单机部分，分布式移至后续

| 子 PR | 名称 | 范围 | 周期 状态 |
|---- |---|------|------|------|------|
| **PR-087a** | 接口拆分 | 4 核心 + 2 组合接口定义 + 适配器模式 | 1 周 | 🔲 待启动 |
| **PR-087b** | Per-Core 执行器 | PerCoreExecutor 实现 + 性能基准测试 | 1 周 | 🔲 待启动 |
| **PR-087c** | 可暂停调度器 | StepExecutor + Checkpoint 暂停/恢复 | 1 周 | 🔲 待启动 |
| **PR-087d** | 分布式迁移 | 跨节点迁移 + Quorum + TermManager | TODO | 🔲 待规划 |

**当前范围（本PR）**：
- PR-087a + PR-087b + PR-087c（单机部分）

**后续范围**：
- PR-087d（分布式部分）

**依赖关系**：
```
PR-087a (接口拆分)
    ↓
PR-087b (Per-Core) ← PR-087a
PR-087c (调度器)   ← PR-087a
```

### 9. 关联文档

| 文档 | 说明 |
|------|------|
| [Spike 统一执行器架构 v2.6](./2026-02-25_spike-glm-unified-executor.md) | 完整技术方案 |
| [DDD 实施路线图](./2026-02-18_spike_nexkv-ddd-roadmap.md) | DDD 整体路线 |
| [M2 存储引擎路线图](./2026-02-21_spike_m2-storage-engine-roadmap.md) | M2 依赖关系 |
| [M2 异步编程模型](./2026-02-22_spike_m2-async-programming-model-refactor.md) | 异步基础 |

---

## 第二部分：后置部分（开发完成后填写）

### 12. 开发总结

> 开发完成后填写

#### 12.1 实际实施情况

| 阶段 | 计划周期 | 实际周期 | 偏差原因 |
|------|----------|----------|----------|
| Phase 1 | 1-2 天 | - | - |
| Phase 2 | 2-3 天 | - | - |
| Phase 3 | 3-5 天 | - | - |
| Phase 4 | 1-2 周 | - | - |

#### 12.2 技术决策

> 记录实施过程中的关键决策

### 13. 测试报告

> 测试结果

#### 13.1 单元测试

| 模块 | 覆盖率 | 通过率 |
|------|--------|--------|
| executor.go | - | - |
| percore_executor.go | - | - |

#### 13.2 性能测试

| 指标 | 目标 | 实际 |
|------|------|------|
| 吞吐量 | ≥ 2M ops/s | - |
| P99 延迟 | < 10μs | - |

### 14. CI 通过记录

| 检查项 | 状态 | 日期 |
|--------|------|------|
| go build | ⏳ | - |
| go vet | ⏳ | - |
| go test | ⏳ | - |
| go test -race | ⏳ | - |
| gofmt | ⏳ | - |

### 15. 合并记录

| 项目 | 内容 |
|------|------|
| PR 链接 | - |
| 合并日期 | - |
| 合并 commit | - |
