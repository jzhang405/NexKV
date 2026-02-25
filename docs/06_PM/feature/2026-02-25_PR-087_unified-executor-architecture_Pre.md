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
| 参考文档 | [Spike 文档 v2.6](./2026-02-25_spike-glm-unified-executor.md) |

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
   - ✅ 实现接口拆分：GoroutineProvider 13 方法 → 5 原子 + 3 组合 + 4 可暂停调度器
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

```
Level 1: 原子接口（最小粒度）
├── Executor: Execute(ctx, fn)
├── CoreBinder: ExecuteOnCore(ctx, core, fn)
├── ExecutorWithResult: ExecuteWithResult(ctx, fn)
├── Scheduler: Schedule(ctx, delay, fn)
└── Prioritizer: ExecuteWithPriority(ctx, priority, fn)

Level 2: 组合接口
├── TaskExecutor = Executor + ExecutorWithResult
├── AsyncTaskExecutor = TaskExecutor + Scheduler + Prioritizer
└── FullTaskExecutor = AsyncTaskExecutor + Batcher + Manager

Level 3: 向后兼容
└── GoroutineProvider = FullTaskExecutor (类型别名)
```

#### 3.2 Per-Core 执行器设计

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

### 4. 实施计划（谁干、什么时候干）

#### 4.1 四阶段实施

```
Phase 1: 接口层准备（1-2 天）
├── 定义 Executor/CoreBinder/StepExecutor 接口
├── 定义 Step/Checkpoint/Priority 类型
├── 定义 MigrationState 迁移状态
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
| 接口拆分 | 5 原子 + 3 组合 + 4 可暂停调度器接口定义完成 |
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

### 6. 风险评估（可能遇到的问题）

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| **LockOSThread CPU 亲和性** | 绑核不保证 CPU 亲和性 | 使用 runtime.GOMAXPROCS 限制 |
| **Per-Core 内存隔离** | 单核 OOM 影响所有任务 | 实现内存限制和背压 |
| **迁移原子性** | 迁移中断可能数据不一致 | 2PC 协议 + 回滚 |
| **Context 传递** | 取消机制失效 | 使用独立 Context |
| **脑裂防护** | 旧节点继续写入 | Fencing Token + Term |
| **分布式一致性** | Per-Core 与 Quorum 集成 | 明确职责边界 |

### 7. 专家评审记录

#### 7.1 DDD 专家评审

| 严重程度 | 问题描述 | 修复状态 |
|----------|----------|----------|
| **P0** | 向后兼容实现细节缺失 | 待实现时补充 |
| **P1** | 接口职责重叠：TaskExecutor vs StepExecutor | 已明确区分 |
| **P1** | 接口命名不一致 | 采用 TaskXxx 模式 |
| **P2** | 交付物路径模糊 | 已在实施计划明确 |
| **P2** | 缺少 Step/Checkpoint 模型定义 | 在 domain/model/ 定义 |

**设计评分**: 3.5/5

#### 7.2 分布式系统专家评审

| 严重程度 | 问题描述 | 修复状态 |
|----------|----------|----------|
| **P0** | 缺少分布式一致性设计 | 已补充到风险评估 |
| **P0** | 迁移协议 2PC 描述不清晰 | 已明确多数派定义 |
| **P1** | Checkpoint 持久化方案不完整 | 依赖 Spike 文档 |
| **P1** | 崩溃恢复方案缺失 | Phase 4 验证 |
| **P1** | 脑裂防护机制不明确 | 已补充 Term 说明 |
| **P2** | 性能目标需明确测试方法 | 实施后验证 |

**设计评分**: 3/5
**核心结论**: 接口拆分方向正确，但分布式一致性设计需与现有架构对齐

### 7.3 评审结论

> **设计方向**: ✅ 正确 - 接口拆分 + Per-Core 无锁执行器
> **实施建议**: 分离为多个小 PR（接口拆分 → PerCore → Checkpoint → 迁移）

### 8. 关联文档

| 文档 | 说明 |
|------|------|
| [Spike 统一执行器架构 v2.6](./2026-02-25_spike-glm-unified-executor.md) | 完整技术方案 |
| [DDD 实施路线图](./2026-02-18_spike_nexkv-ddd-roadmap.md) | DDD 整体路线 |
| [M2 存储引擎路线图](./2026-02-21_spike_m2-storage-engine-roadmap.md) | M2 依赖关系 |
| [M2 异步编程模型](./2026-02-22_spike_m2-async-programming-model-refactor.md) | 异步基础 |

---

## 第二部分：后置部分（开发完成后填写）

### 8. 开发总结

> 开发完成后填写

#### 8.1 实际实施情况

| 阶段 | 计划周期 | 实际周期 | 偏差原因 |
|------|----------|----------|----------|
| Phase 1 | 1-2 天 | - | - |
| Phase 2 | 2-3 天 | - | - |
| Phase 3 | 3-5 天 | - | - |
| Phase 4 | 1-2 周 | - | - |

#### 8.2 技术决策

> 记录实施过程中的关键决策

### 9. 测试报告

> 测试结果

#### 9.1 单元测试

| 模块 | 覆盖率 | 通过率 |
|------|--------|--------|
| executor.go | - | - |
| percore_executor.go | - | - |

#### 9.2 性能测试

| 指标 | 目标 | 实际 |
|------|------|------|
| 吞吐量 | ≥ 2M ops/s | - |
| P99 延迟 | < 10μs | - |

### 10. CI 通过记录

| 检查项 | 状态 | 日期 |
|--------|------|------|
| go build | ⏳ | - |
| go vet | ⏳ | - |
| go test | ⏳ | - |
| go test -race | ⏳ | - |
| gofmt | ⏳ | - |

### 11. 合并记录

| 项目 | 内容 |
|------|------|
| PR 链接 | - |
| 合并日期 | - |
| 合并 commit | - |
