# 【M2 阶段 0 实施前置文档】异步流水线基础设施重构

> **文档说明**：本文档为 M2 存储引擎的阶段 0 前置规划文档，定义异步流水线基础设施重构的实施目标、范围、验收标准和风险控制。
>
> **实施顺序**：阶段 0 必须在 M2 存储引擎实施前完成，为存储层异步能力提供基础。

---

## 文档版本历史

| 版本 | 日期 | 变更内容 | 作者 |
|------|------|----------|------|
| v1.0 | 2026-03-02 | 初始版本：AsyncOp 重命名 + 泛型锁包装器 | PM |
| v1.1 | 2026-03-02 | 调整 Week 4：ADR 002 补充而非重新创建 | PM |
| v1.2 | 2026-03-03 | 新增：TaskExecutor 接口增强（SourceID 支持） | PM |
| v1.3 | 2026-03-03 | 修正：TaskExecutor 接口未修改，使用 SubmitWithSource() 方法（独占绑定策略） | PM |
| **v1.4** | **2026-03-03** | **调整：流水线集成到 BTree/WAL 内部，不单独实现** | **PM** |

---

## 第一部分：前置部分（开工前必完成）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 代码实施 + 文档更新（Code + Doc） |
| PR编号 | PR-XXX（创建GitHub PR后补充完整） |
| 分支名称 | feature/phase0-async-refactor |
| 工作主题 | M2 阶段 0: AsyncOp 重命名 + 泛型锁包装器 + ADR 002 补充 |
| 负责人 | Claude Code Agent |
| 分支创建日期 | 2026-03-02 |
| 计划完成日期 | 2026-03-30（4周） |
| 关联需求/Issue | M2 存储引擎实施前置条件 |
| 实施类型 | ☑ 代码重构 ☑ 新增功能 ☑ 文档更新 |

### 2. 背景与目标（为什么需要阶段 0）

#### 2.1 更新原因

**触发因素**：
- M2 存储引擎需要完整的异步操作支持
- 现有 `AsyncOp[T]` 命名过长，代码冗长
- 缺少泛型锁包装器，每次手动实现锁逻辑
- 存储层缺少流水线架构设计
- **TaskExecutor 缺少 SourceID 支持，无法实现 CPU 亲和性调度**

**现状问题**：
- ❌ 接口命名不够简洁：`AsyncOp[T]` → 代码冗长
- ❌ 缺少统一锁管理：每次都需要手动实现 `sync.RWMutex`
- ❌ 存储层异步能力不足：缺少流水线设计
- ❌ 无法支持无锁/有锁模式切换
- ❌ **TaskExecutor 随机调度，缓存局部性差**

**更新价值**：
- ✅ 接口命名简洁：`AsyncOp[T]` 更易读易写
- ✅ 统一锁管理：`Locked[T]` 一行切换无锁/有锁模式
- ✅ 流水线架构：为存储层提供异步能力
- ✅ 性能优化基础：为后续优化铺路
- ✅ **Per-Core 优化：基于 SourceID 的 CPU 亲和性调度**

#### 2.2 实施目标

1. **简洁性**：接口命名更简洁，代码更易读
2. **通用性**：泛型锁包装器可复用
3. **扩展性**：流水线架构为存储层提供基础
4. **完整性**：全面替换，确保无遗漏
5. **性能优化**：基于 SourceID 的 Per-Core 调度优化

#### 2.3 明确边界

**本次更新**（阶段 0）：
- ✅ AsyncOperation → AsyncOp 重命名
- ✅ 泛型锁包装器 Locked[T] 实现
- ✅ TaskExecutor 接口增强（SourceID 支持）
- ✅ PerCoreExecutor SourceID 路由实现
- ✅ 流水线框架设计文档
- ✅ 单元测试 + 基准测试
- ✅ DDD/M2 文档更新到 v3.0（已完成）

**暂不更新**（后续阶段 - M2 存储引擎）：
- ⏸ Bf-Tree 异步接口（M2 阶段，集成 ReadPipeline + WritePipeline）
- ⏸ WAL 异步批量写入（M2 阶段，集成 FlushPipeline）
- ⏸ 流水线代码实现（直接集成到存储引擎内部，不单独实现 Pipeline 类）

> **设计调整说明**：
> 流水线模式将直接集成到 BTree 和 WAL 组件内部，而不是单独实现独立的 Pipeline 类。
> 这样可以减少抽象层开销，提升性能，简化维护。
>
> - **BTree 内部**：集成 ReadPipeline + WritePipeline 逻辑
> - **WAL 内部**：集成 FlushPipeline 批量刷新逻辑
> - **优势**：减少跨组件调用、更好的封装、便于优化

### 3. 实施内容（做什么）

#### 3.1 Week 1-2: AsyncOp 重命名

| 文件 | 更新类型 | 更新内容 | 优先级 |
|------|---------|---------|--------|
| `internal/domain/service/rpc_async.go` | 修改 | 使用 AsyncOp 接口 | 🔴 P0 |
| `internal/infrastructure/rpc/async_impl.go` | 修改 | 更新实现类名和类型引用 | 🔴 P0 |
| `internal/infrastructure/rpc/adapter.go` | 修改 | 更新类型引用 | 🟡 P1 |
| `internal/infrastructure/rpc/broadcast_progress.go` | 修改 | 更新类型引用 | 🟡 P1 |
| 测试文件 (4个) | 修改 | 更新测试代码 | 🟡 P1 |

**主要变更点**：

新增内容：
- `AsyncOp[T]` 接口定义（新接口）

修改内容：
- 所有 `AsyncOperation[T]` 引用更新为 `AsyncOp[T]`
- 测试代码同步更新

删除内容：
- 删除旧的 AsyncOperation[T] 接口定义

#### 3.2 Week 3: 泛型锁包装器 + TaskExecutor 接口修改 ⭐

| 文件 | 新增/修改 | 更新内容 | 优先级 |
|------|----------|---------|--------|
| `internal/domain/service/task.go` | 修改 | TaskExecutor 接口修改（添加 sourceID） | 🔴 P0 |
| `internal/infrastructure/concurrency/executor_percore.go` | 修改 | Submit() 实现独占绑定 | 🔴 P0 |
| `internal/infrastructure/concurrency/executor_ants.go` | 修改 | Submit() 实现（如需要） | 🟡 P1 |
| `internal/domain/model/source_id_defaults.go` | 新增 | SourceID 默认常量 | 🔴 P0 |
| 调用点文件（多个） | 修改 | 更新所有 Submit() 调用 | 🔴 P0 |
| `internal/infrastructure/concurrent/locked.go` | 新增 | Locked[T] 实现 | 🔴 P0 |
| `internal/infrastructure/concurrent/locked_test.go` | 新增 | 单元测试 | 🔴 P0 |
| `internal/infrastructure/concurrent/locked_bench_test.go` | 新增 | 基准测试 | 🟡 P1 |

**主要变更点**：

**修改内容**：
- `TaskExecutor` 接口修改（添加 sourceID 参数）
  ```go
  // 修改前
  Submit(ctx context.Context, priority TaskPriority, task func(context.Context)) error

  // 修改后
  Submit(ctx context.Context, sourceID model.SourceID, priority TaskPriority, task func(context.Context)) error
  ```
- `PerCoreExecutor.Submit()` 实现（独占绑定策略）
  - 删除 `Submit(ctx, task)` 方法
  - 删除 `SubmitWithPriority()` 方法
  - 删除 `SubmitWithSource()` 方法
  - 修改 `Submit()` 方法实现独占绑定

**新增内容**：
- `Locked[T]` 泛型锁包装器
  - `View()` 方法：读视图（自动加读锁）
  - `Modify()` 方法：写视图（自动加写锁）
  - `GetDirect()` 方法：直接访问（无锁）
  - `Get()`/`Set()` 方法：基础访问器
- `SourceID` 默认常量
  - `SourceBTree`、`SourceWAL`、`SourceNetwork` 等
  - `SourceDefault`（用于不需要 CPU 亲和性的场景）

**原因说明**：
- **接口简洁**：统一使用 `Submit()` 方法
- **强制最佳实践**：所有调用必须提供 SourceID
- **Per-Core 优化**：相同 SourceID 的任务总是路由到同一 Worker
- **缓存局部性**：减少跨核通信，提升缓存命中率
- **参考文档**：附录 B（独占绑定策略）

#### 3.3 Week 4: 补充 ADR 002 + 集成测试准备 ⭐ v1.1 更新

> **更新原因**：ADR 002 已存在，无需重复创建设计文档
>
> **调整内容**：补充 ADR 002 缺失内容，准备集成测试

| 任务 | 内容 | 优先级 | 交付物 |
|------|------|--------|--------|
| 补充 ReadPipeline 设计 | 添加 ReadPipeline 结构定义、worker 逻辑 | 🔴 P0 | ADR 002 更新 |
| 补充 FlushPipeline 设计 | 添加 FlushPipeline 结构定义、WAL 批量逻辑 | 🔴 P0 | ADR 002 更新 |
| 集成任务类型定义 | 引用/复制 SyncPolicy、ReadTask、TransactionTask | 🟡 P1 | ADR 002 更新 |
| 添加集成测试计划 | 定义流水线集成测试场景 | 🟡 P1 | 测试计划文档 |
| 更新交叉引用 | 引用 thoughts 文档和 M2 文档 | 🟢 P2 | ADR 002 更新 |

**主要变更点**：

新增内容：
- ReadPipeline 结构定义和实现框架
- FlushPipeline（WAL）结构定义和批量写入逻辑
- 任务类型定义（SyncPolicy、ReadTask、TransactionTask、IsolationLevel）
- 集成测试计划

更新内容：
- ADR 002 补充缺失的设计
- 添加与 `docs/07_spike/2026-02-18_spike_nexkv-ddd-implement.md`（流水线模式实现）的交叉引用
- 添加与 `docs/07_spike/2026-02-21_spike_m2-storage-engine-interface.md`（流水线任务类型定义）的交叉引用

### 4. 验收标准（如何验证完成）

#### 4.1 Week 1-2: AsyncOp 重命名验收

| 验证项 | 验收标准 | 验证方式 |
|--------|----------|----------|
| **编译通过** | 所有代码可以编译 | `go build ./...` |
| **测试通过** | 所有测试通过 | `go test ./internal/domain/service/... ./internal/infrastructure/rpc/...` |
| **引用更新** | 所有 AsyncOperation 已更新为 AsyncOp | `grep -r "AsyncOp"` 检查 |

#### 4.2 Week 3: 泛型锁包装器 + TaskExecutor 接口验收

| 验证项 | 验收标准 | 验证方式 |
|--------|----------|----------|
| **Locked[T] 编译** | 代码可以编译 | `go build ./...` |
| **Locked[T] 并发安全** | 并发安全测试通过 | `go test -race ./internal/infrastructure/concurrent/...` |
| **Locked[T] 性能** | 性能优于手动加锁 | `go test -bench=. ./...` |
| **GetDirect 性能** | 优于 View 10 倍以上 | 基准测试对比 |
| **TaskExecutor 接口更新** | 所有调用点已更新 | `grep -r "Submit(ctx"` 检查 |
| **SourceID 路由** | 相同 SourceID 路由到同一 Worker | 单元测试验证 |
| **Per-Core 性能** | 优于随机调度 20% 以上 | 基准测试对比 |

**新增验收命令**：
```bash
# 检查 TaskExecutor.Submit 调用点
grep -r "\.Submit(ctx," internal/ | wc -l

# 检查 SourceID 参数
grep -r "Submit(ctx.*SourceID" internal/ | wc -l

# 运行并发测试
go test -race ./internal/infrastructure/concurrency/...

# 运行性能测试
go test -bench=BenchmarkPerCoreExecutor ./internal/infrastructure/concurrency/...
```

#### 4.3 Week 4: 补充 ADR 002 + 集成测试准备验收 ⭐ v1.1 更新

| 验证项 | 验收标准 | 验证方式 |
|--------|----------|----------|
| **ADR 002 更新** | ReadPipeline 和 FlushPipeline 设计已补充 | ADR 002 完整性检查 |
| **类型定义** | SyncPolicy、ReadTask、TransactionTask 已添加 | 代码审查 |
| **集成测试计划** | 测试场景和测试用例定义 | 测试计划评审 |
| **交叉引用** | DDD/M2 文档交叉引用正确 | 链接检查 |
| **设计一致性** | ADR 与 DDD/M2 文档一致 | 架构师评审 |

### 5. 风险控制

#### 5.1 技术风险

| 风险 | 影响 | 缓解措施 | 负责人 |
|------|------|----------|--------|
| **编译错误** | 代码无法编译 | 全面搜索替换，确保无遗漏 | 开发者 |
| **并发安全** | 数据竞争 | `-race` 检测 + 单元测试 | 开发者 |
| **性能退化** | 性能下降 | 基准测试对比 | 开发者 |
| **测试覆盖不足** | 隐患 | 强制 80% 覆盖率 | 开发者 |
| **接口不兼容** | TaskExecutor 调用点遗漏 | 使用编译器检查 + grep 验证 | 开发者 |
| **SourceID 路由错误** | 任务路由不正确 | 单元测试验证路由逻辑 | 开发者 |

#### 5.2 进度风险

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| **低估工作量** | 延期 | 已预留 50% 缓冲时间 |
| **意外问题** | 阻塞 | 每周进度检查 |
| **优先级冲突** | 资源不足 | 阶段 0 为 P0 优先级 |

### 6. 依赖关系

#### 6.1 前置依赖

| 依赖项 | 状态 | 说明 |
|--------|------|------|
| Go 1.21+ | ✅ 满足 | 泛型支持 |
| 现有测试框架 | ✅ 满足 | testify |
| TaskExecutor | ✅ 满足 | Per-Core/Ants |

#### 6.2 后续依赖

| 后续任务 | 依赖阶段 0 的内容 | 实施方式 |
|----------|-------------------|----------|
| M2 存储引擎 | AsyncOp + Locked[T] + 流水线设计 | 集成到 BTree/WAL 内部 |
| BfTree 异步接口 | AsyncOp 接口 + 流水线设计 | 内部集成 ReadPipeline + WritePipeline |
| WAL 异步批量写入 | 流水线设计 + SyncPolicy | 内部集成 FlushPipeline |

### 7. 评审要点

| 评审项 | 检查内容 | 评审人 | 评审结果 |
|--------|---------|--------|---------|
| **接口设计** | AsyncOp 接口定义是否合理 | 架构师 | ⬜ 待评审 |
| **重命名完整性** | 所有引用是否已更新 | 架构师 | ⬜ 待评审 |
| **TaskExecutor 接口** | SourceID 参数设计是否合理 | 架构师 | ⬜ 待评审 |
| **Per-Core 调度** | 路由算法是否正确 | 性能专家 | ⬜ 待评审 |
| **性能测试** | 基准测试结果是否符合预期 | 性能专家 | ⬜ 待评审 |
| **流水线设计** | 设计是否合理可行 | 架构师 | ⬜ 待评审 |

---

## 第二部分：流程节点记录

### 1. 实施过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| Pre 文档编写 | 2026-03-02 | 编写前置规划文档 | 本文档 |
| 架构师评审 | 202X-XX-XX | 评审 Pre 文档 | 评审意见 |
| Week 1-2 实施 | 202X-XX-XX | AsyncOp 重命名 | 代码 + 测试 |
| Week 3 实施 | 202X-XX-XX | 泛型锁包装器 | 代码 + 测试 |
| Week 4 实施 | 202X-XX-XX | 流水线设计文档 | 设计文档 |
| Post 文档编写 | 202X-XX-XX | 编写后置总结文档 | Post 文档 |
| 架构师批准 | 202X-XX-XX | 架构师评审批准 | 批准签字 |
| 提交 GitHub | 202X-XX-XX | 创建 PR | PR 链接 |

### 2. 文档更新记录

| 文档 | 更新类型 | 更新内容 | 状态 |
|------|---------|---------|------|
| DDD Interface v3.0 | 更新 | 接口优先级、依赖关系图、测试策略 | ✅ 已完成 |
| DDD Implement v3.0 | 更新 | 流水线实现、泛型锁包装器、测试策略 | ✅ 已完成 |
| DDD Roadmap v3.0 | 更新 | 阶段 0 规划、验收标准、风险缓解 | ✅ 已完成 |
| M2 Interface v2.3 | 更新 | 流水线任务类型定义、AsyncOp 来源 | ✅ 已完成 |
| M2 Implement v2.1 | 更新 | 版本引用更新 | ✅ 已完成 |
| M2 Roadmap v2.0 | 更新 | 阶段 0 时间说明、验收标准 | ✅ 已完成 |
| M2 Benchmark v2.0 | 更新 | 版本引用更新 | ✅ 已完成 |

---

## 第三部分：后置部分（PR合并后编写）

> **注**：此部分在 PR 合并后填写，当前留空。

### 1. 实施成果总结

#### 1.1 完成情况
- **已完成任务**：[待填写]
- **变更统计**：[待填写]
- **与 Pre 文档差异**：[待填写]

#### 1.2 质量验证
- **测试覆盖率**：[待填写]
- **性能测试**：[待填写]
- **代码审查**：[待填写]

#### 1.3 交付物清单

| 类型 | 具体内容 | 链接/路径 |
|------|----------|-----------|
| 代码文件 | [待填写] | [待填写] |
| 测试文件 | [待填写] | [待填写] |
| 设计文档 | [待填写] | [待填写] |

### 2. 后续优化建议

#### 2.1 未完成项
- **暂缓实施**：[待填写]
- **需要补充**：[待填写]

#### 2.2 ToDo 清单

| 优先级 | 任务内容 | 预估时间 | 关联 PR | 备注 |
|--------|----------|---------|---------|------|
| 高/中/低 | [待填写] | X 小时 | PR-XXX | [待填写] |

### 3. 经验总结

#### 3.1 成功经验
- [待填写]

#### 3.2 改进建议
- [待填写]

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档版本 | V1.3 (Pre) |
| 创建日期 | 2026-03-02 |
| 最后更新 | 2026-03-03 |
| 文档路径 | `docs/06_PM/feature/2026-03-02_pre-m2-phase0-async-pipeline.md` |
| 实施分支 | `feature/phase0-async-refactor` |
| 后续维护人 | Claude Code Agent |
| 状态 | ⬜ 待评审 |

**v1.3 更新说明**：
- **修正附录 B**：TaskExecutor 基础接口未修改，使用 `SubmitWithSource()` 方法
- **修正调度策略**：说明实际使用独占绑定策略（非哈希路由）
- **修正 Week 3**：删除 TaskExecutor 接口修改任务（已完成）
- **补充限制说明**：独占绑定策略的限制和注意事项

---

## 附录：参考文档

### 设计文档

| 文档 | 路径 | 说明 |
|------|------|------|
| DDD Interface v3.0 | `docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md` | 接口定义（47个接口） |
| DDD Implement v3.0 | `docs/07_spike/2026-02-18_spike_nexkv-ddd-implement.md` | 实施方案（含流水线模式实现 3.7节、泛型锁包装器 14章） |
| M2 Interface v2.3 | `docs/07_spike/2026-02-21_spike_m2-storage-engine-interface.md` | 流水线任务类型定义（SyncPolicy、WriteTask、ReadTask、TransactionTask、IsolationLevel） |
| M2 Roadmap v2.0 | `docs/07_spike/2026-02-21_spike_m2-storage-engine-roadmap.md` | M2 实施路线（含阶段 0 规划） |

### ADR 文档

| ADR | 路径 | 说明 |
|-----|------|------|
| ADR 001 | `docs/08_adr/001-dual-storage-engine.md` | 双存储引擎策略 |
| ADR 002 | `docs/08_adr/002-async-pipeline.md` | 异步流水线架构 |
| ADR 003 | `docs/08_adr/003-5layer-ddd.md` | 5层 DDD 架构 |

---

## 附录 B：TaskExecutor 接口修改详细说明

> **重要说明**：直接修改 `TaskExecutor.Submit()` 接口，添加 `sourceID` 参数。删除 `SubmitWithSource()` 方法。

### B.1 接口变更

#### 变更前
```go
type TaskExecutor interface {
    Submit(ctx context.Context, priority TaskPriority, task func(context.Context)) error
    Close() error
}
```

#### 变更后
```go
type TaskExecutor interface {
    Submit(ctx context.Context, sourceID model.SourceID, priority TaskPriority, task func(context.Context)) error
    Close() error
}
```

**新增参数**：
- `sourceID model.SourceID`：数据源标识符，用于 CPU 亲和性调度

**设计原则**：
- ✅ **接口简洁**：统一使用 `Submit()` 方法
- ✅ **强制最佳实践**：所有调用必须提供 SourceID
- ✅ **易于使用**：提供 SourceID 默认常量

### B.2 SourceID 定义

**实际代码位置**：`internal/domain/model/source.go`

```go
// SourceID 数据源标识符
// 用于 TaskExecutor 的 CPU 亲和性调度
type SourceID string

// Validate 验证 SourceID 有效性
func (s SourceID) Validate() error {
    if s == "" {
        return errors.New("sourceID cannot be empty")
    }
    return nil
}

// Hash 返回 SourceID 的哈希值（用于绑定映射）
func (s SourceID) Hash() string {
    return string(s)
}
```

**建议添加默认常量**（`internal/domain/model/source_id_defaults.go` - 待创建）：
```go
const (
    SourceBTree      SourceID = "btree"      // BTree 数据源
    SourceWAL        SourceID = "wal"        // WAL 数据源
    SourceNetwork    SourceID = "network"    // 网络数据源
    SourceGC         SourceID = "gc"         // 垃圾回收数据源
    SourceReplication SourceID = "replication" // 复制数据源
    SourceCompaction SourceID = "compaction"  // 压缩数据源
)
```

### B.3 Per-Core 调度原理（独占绑定策略）

**核心思想**：相同 SourceID 的任务总是路由到同一 Worker（独占绑定）

**实现方式**（修改 `Submit()` 方法）：

```go
// internal/infrastructure/concurrency/executor_percore.go

// Submit 提交任务（带 SourceID）
func (e *PerCoreExecutor) Submit(
    ctx context.Context,
    sourceID model.SourceID,
    priority model.TaskPriority,
    task func(context.Context),
) error {
    sourceKey := sourceID.Hash()
    now := time.Now().UnixNano()

    // 规则 B：快速路径 - 尝试从 map 获取已绑定的 WorkerID（无锁）
    if bindingValue, ok := e.sourceBindings.Load(sourceKey); ok {
        binding := bindingValue.(*sourceIDBinding)
        workerID := int(binding.workerID)
        worker := e.workers[workerID]

        // 更新最后使用时间
        atomic.StoreInt64(&binding.lastUsedTime, now)

        // 提交到绑定的 Worker
        return e.submitToWorker(ctx, workerID, worker, priority, task)
    }

    // 规则 A：首次绑定 - 慢速路径（需要保护 selectIdleWorker + Store 的原子性）
    e.bindingMu.Lock()
    defer e.bindingMu.Unlock()

    // 双重检查：等待锁期间其他 goroutine 可能已绑定
    if bindingValue, ok := e.sourceBindings.Load(sourceKey); ok {
        binding := bindingValue.(*sourceIDBinding)
        workerID := int(binding.workerID)
        worker := e.workers[workerID]
        atomic.StoreInt64(&binding.lastUsedTime, now)
        return e.submitToWorker(ctx, workerID, worker, priority, task)
    }

    // 选择未绑定的 Worker（持锁，保证独占）
    workerID, err := e.selectIdleWorker()
    if err != nil {
        logrus.Warnf("[PerCore] All workers busy, rejecting SourceID=%s", sourceKey)
        return err
    }

    // 创建并存储新的绑定关系（持锁，保证原子性）
    newBinding := &sourceIDBinding{
        workerID:     int64(workerID),
        lastUsedTime: now,
    }
    e.sourceBindings.Store(sourceKey, newBinding)

    worker := e.workers[workerID]
    logrus.Debugf("[PerCore] SourceID %s bound to Worker %d", sourceKey, workerID)

    return e.submitToWorker(ctx, workerID, worker, priority, task)
}

// selectIdleWorker 选择未绑定的 Worker（独占绑定）
func (e *PerCoreExecutor) selectIdleWorker() (int, error) {
    // 收集已绑定的 WorkerID
    boundWorkers := make(map[int]bool)
    e.sourceBindings.Range(func(_, value interface{}) bool {
        binding := value.(*sourceIDBinding)
        boundWorkers[int(binding.workerID)] = true
        return true
    })

    // 找第一个未绑定的 Worker
    for i := range e.workers {
        if !boundWorkers[i] {
            return i, nil
        }
    }

    return 0, errors.ErrAllWorkersBusy
}
```

**关键特性**：
1. **独占绑定**：每个 Worker 只能绑定一个 SourceID
2. **首次绑定**：选择未绑定的 Worker，建立 `sourceID -> workerID` 映射
3. **后续复用**：从映射表获取 WorkerID，更新最后使用时间
4. **超时解绑**：30 秒未使用自动解绑（可通过 `WithBindingTimeout()` 配置）
5. **严格限制**：最大并发 SourceID 数量 = Worker 数量
6. **错误处理**：全部 Worker 被绑定后返回 `ErrAllWorkersBusy`

**限制说明**：
- ⚠️ **Worker 数量 = 最大并发 SourceID 数量**
- ⚠️ **超过限制时返回错误**：需要监控 `ErrAllWorkersBusy`
- ⚠️ **不适合大量 SourceID 场景**：建议使用共享绑定模式（待实现）

### B.4 性能优势

| 优化点 | 原理 | 性能提升 |
|--------|------|----------|
| **CPU 缓存局部性** | 相同数据源任务在相同 CPU 核心 | 20-30% |
| **无锁队列** | 每个 Worker 独立队列 | 减少锁竞争 |
| **缓存命中率** | 数据结构复用 | 15-25% |
| **跨核通信减少** | 同源任务同核处理 | 10-20% |

### B.5 使用示例

```go
// 创建 Per-Core 执行器
executor, err := NewPerCoreExecutor(
    WithQueueSize(10000),
    WithBindingTimeout(30*time.Second),  // 30秒未使用自动解绑
)
if err != nil {
    log.Fatal(err)
}
defer executor.Close()

// 提交任务（带 SourceID）
executor.Submit(ctx, model.SourceBTree, TaskPriorityHigh, func(ctx context.Context) {
    // BTree 操作（总是路由到同一 Worker）
})

executor.Submit(ctx, model.SourceWAL, TaskPriorityCritical, func(ctx context.Context) {
    // WAL 操作（总是路由到同一 Worker）
})

// 使用默认 SourceID（不需要 CPU 亲和性时）
executor.Submit(ctx, model.SourceDefault, TaskPriorityNormal, func(ctx context.Context) {
    // 普通任务
})
```

### B.6 迁移指南

#### Step 1：更新接口定义
```go
// internal/domain/service/task.go
type TaskExecutor interface {
    Submit(ctx context.Context, sourceID model.SourceID, priority TaskPriority, task func(context.Context)) error
    Close() error
}
```

#### Step 2：更新实现
```go
// internal/infrastructure/concurrency/executor_percore.go
func (e *PerCoreExecutor) Submit(
    ctx context.Context,
    sourceID model.SourceID,
    priority TaskPriority,
    task func(context.Context),
) error {
    // 实现独占绑定路由逻辑
}
```

#### Step 3：更新所有调用点
```bash
# 查找所有调用点
grep -rn "\.Submit(ctx," internal/ | grep -v "_test.go"

# 示例调用点更新
# Before:
executor.Submit(ctx, TaskPriorityHigh, task)

# After:
executor.Submit(ctx, model.SourceBTree, TaskPriorityHigh, task)
```

#### Step 4：编译验证
```bash
# 编译检查
go build ./...

# 预期：编译错误会指出所有遗漏的调用点
```

#### Step 5：删除旧方法
```go
// 删除以下方法（已合并到 Submit）
// - Submit(ctx, task)
// - SubmitWithPriority(ctx, priority, task)
// - SubmitWithSource(ctx, sid, priority, task)
```

### B.7 测试验证

**实际测试位置**：`internal/infrastructure/concurrency/executor_percore_test.go`

```go
// 测试 SourceID 路由正确性
func TestPerCoreExecutor_SubmitWithSource(t *testing.T) {
    executor, err := NewPerCoreExecutor(WithQueueSize(100))
    require.NoError(t, err)
    defer executor.Close()

    ctx := context.Background()

    // 测试相同 SourceID 路由到同一 Worker
    var mu sync.Mutex
    workerIDs := make(map[int]bool)

    for i := 0; i < 100; i++ {
        err := executor.SubmitWithSource(ctx, "test-source", model.TaskPriorityNormal, func(ctx context.Context) {
            // 获取当前 goroutine 绑定的 CPU ID（用于验证）
            // 注意：实际测试需要更可靠的方法
            mu.Lock()
            workerIDs[getWorkerID()] = true
            mu.Unlock()
        })
        require.NoError(t, err)
    }

    time.Sleep(100 * time.Millisecond) // 等待任务完成

    // 验证：所有任务应该在同一 Worker 上执行
    assert.Equal(t, 1, len(workerIDs), "All tasks should execute on same worker")
}

// 测试独占绑定限制
func TestPerCoreExecutor_ExclusiveBinding(t *testing.T) {
    numWorkers := 4
    executor, err := NewPerCoreExecutor(WithQueueSize(100))
    require.NoError(t, err)
    defer executor.Close()

    ctx := context.Background()

    // 绑定所有 Worker
    for i := 0; i < numWorkers; i++ {
        sourceID := model.SourceID(fmt.Sprintf("source-%d", i))
        err := executor.SubmitWithSource(ctx, sourceID, model.TaskPriorityNormal, func(ctx context.Context) {})
        require.NoError(t, err)
    }

    // 尝试绑定第 5 个 SourceID（应该失败）
    err = executor.SubmitWithSource(ctx, "source-extra", model.TaskPriorityNormal, func(ctx context.Context) {})
    assert.Error(t, err)
    assert.ErrorIs(t, err, errors.ErrAllWorkersBusy)
}

// 测试超时解绑
func TestPerCoreExecutor_BindingTimeout(t *testing.T) {
    executor, err := NewPerCoreExecutor(
        WithQueueSize(100),
        WithBindingTimeout(1*time.Second), // 1秒超时
    )
    require.NoError(t, err)
    defer executor.Close()

    ctx := context.Background()

    // 绑定 SourceID
    err = executor.SubmitWithSource(ctx, "test-source", model.TaskPriorityNormal, func(ctx context.Context) {})
    require.NoError(t, err)

    // 等待超时
    time.Sleep(2 * time.Second)

    // 现在应该可以绑定新的 SourceID（旧绑定已解绑）
    for i := 0; i < runtime.NumCPU(); i++ {
        sourceID := model.SourceID(fmt.Sprintf("new-source-%d", i))
        err = executor.SubmitWithSource(ctx, sourceID, model.TaskPriorityNormal, func(ctx context.Context) {})
        require.NoError(t, err, "Should be able to bind new sources after timeout")
    }
}

// 性能基准测试
func BenchmarkPerCoreExecutor_SubmitWithSource(b *testing.B) {
    executor, err := NewPerCoreExecutor(WithQueueSize(10000))
    require.NoError(b, err)
    defer executor.Close()

    ctx := context.Background()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        sourceID := model.SourceID(fmt.Sprintf("source-%d", i%10)) // 10 个不同的 SourceID
        executor.SubmitWithSource(ctx, sourceID, model.TaskPriorityNormal, func(ctx context.Context) {})
    }
}
```

### B.8 风险评估

| 风险 | 影响 | 可能性 | 缓解措施 |
|------|------|--------|----------|
| **接口不兼容** | 编译错误 | **低**（接口未修改） | ✅ 无需缓解（向后兼容） |
| **调用点遗漏** | 运行时错误 | **低**（可选增强） | ✅ 无需强制迁移 |
| **SourceID 数量超限** | `ErrAllWorkersBusy` 错误 | **中** | 监控绑定数量，调整 Worker 数量 |
| **负载不均** | 某些 Worker 过载 | **中** | 监控 Worker 队列长度 |
| **绑定泄漏** | Worker 无法释放 | **低** | 超时自动解绑（默认 30 秒） |
| **性能退化** | 缓存局部性未提升 | **低** | 基准测试验证 |

### B.9 参考文档

- `internal/infrastructure/concurrency/executor_percore.go` - Per-Core 执行器实现
- `internal/domain/model/source.go` - SourceID 定义
- `docs/07_spike/2026-02-25_spike-glm-unified-executor.md` - Per-Core 优化原理
- `thoughts/2026-03-03-percore-executor-analysis.md` - 性能分析
