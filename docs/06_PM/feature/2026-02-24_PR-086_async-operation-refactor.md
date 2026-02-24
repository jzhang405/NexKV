# 【PR全流程文档】Refactor - P0-2 AsyncOperation 激进重构

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从需求对齐到开发完成的全流程，一个PR对应一份全流程文档，归档后作为项目追溯依据。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 重构（Refactor） |
| PR编号 | PR-086（创建GitHub PR后补充完整） |
| 分支名称 | feature/P0-2-async-operation-refactor |
| 工作主题 | P0-2 AsyncOperation 激进重构（一次性完成） |
| 负责人 | Claude Code |
| 分支创建日期 | 2026-02-24 |
| 计划开工日期 | 2026-02-24 |
| 计划CI通过日期 | 2026-02-24 |
| 关联需求单号 | P0-2 技术债务（见 `docs/06_PM/doc/2026-02-24_P0-2_technical_debt.md`） |
| 架构师评审状态 | □ 待评审 □ 评审中 ☑ 评审通过 □ 需优化（循环记录） |
| 预审批结果 | □ 未通过 ☑ 已通过（DDD Agent 2026-02-24 同意开工） |

### 2. 背景与目标（为什么干）

#### 2.1 背景

**业务场景**：
- NexKV 项目采用 DDD（领域驱动设计）分层架构
- 当前 `internal/domain/service/rpc_async_impl.go` 包含 850 行实现代码

**现有问题**：

1. **违反分层架构原则**：领域层（domain/service）包含具体实现代码
2. **违反单一职责原则**：领域层承担了基础设施层的实现职责
3. **违反依赖倒置原则**：部分实现直接依赖并发原语
4. **AsyncOperation 双重实现问题**：
   - `pkg/async/async_op.go` - 通用异步操作包
   - `internal/domain/service/rpc_async_impl.go` - 领域层 RPC 异步实现
   - 导致代码重复、维护困难

**价值**：
- 符合 DDD 分层架构规范
- 领域层职责清晰，只定义接口
- 消除双重实现，遵循 DRY 原则
- 更好的可测试性和可维护性

#### 2.2 核心目标（可量化、可验证）

1. **重构目标**（根据评估报告调整）：
   - **✅ 已完成：修复 P2-2** - GoroutinePriority → TaskPriority 命名业务化
   - **✅ 已完成：修复 P2-1** - 拆分 GoroutineProvider 接口（添加 TaskExecutor 等小接口）
   - **✅ 已完成：修复 P1-2** - 使用 GoroutineProvider 替代直接 goroutine 创建
   - **添加 BroadcastProgress Builder 模式**（简化版）
   - **统一回调命名**（OnMajorityReached → OnMajority）
   - **删除 pkg/async 包**（无生产代码引用）
   - 保持现有 API 兼容，不破坏现有调用方
   - 所有测试通过

2. **质量目标**：
   - 代码覆盖率不降低
   - 不引入新的 lint 错误
   - 不引入新的竞态条件

#### 2.3 明确边界（不做什么，避免范围蔓延）

**本次实施范围（简化版）：**

| 内容 | 状态 | 说明 |
|------|------|------|
| `GoroutinePriority` → `TaskPriority` | ✅ 已完成 | 命名业务化 |
| `GoroutineProvider` 接口拆分 | ✅ 已完成 | 添加 TaskExecutor 等小接口 |
| 直接 goroutine 创建修复 | ✅ 已完成 | 使用 GoroutineProvider 替代 |
| BroadcastProgress Builder | 待实施 | 链式配置 API |
| 回调命名统一 | 待实施 | OnMajorityReached → OnMajority |
| 删除 pkg/async | 待实施 | 无生产代码引用 |

**保持不变：**
- `rpc_async_impl.go` 保留在领域层（分层重构暂缓）
- `asyncOpImpl[T]` 保留在领域层
- BroadcastProgress 追踪器职责不变

**明确不做：**
- SingleTaskProgress（已有 asyncOpImpl）
- MultiTaskProgress（BroadcastProgress 已支持）
- AsyncCoordinator（职责与 BroadcastProgress 冲突）

---

### 3. 实现方案（怎么干，核心设计）

#### 3.1 整体流程设计

采用**激进重构方案**：一次性完成所有步骤，预计 ~6.5 小时

| 步骤 | 内容 | 预计时间 |
|------|------|----------|
| Step 1 | 创建目录结构 `internal/infrastructure/rpc/` | 10分钟 |
| Step 2 | 迁移实现代码（asyncOpImpl[T]、工厂函数、辅助函数） | 2小时 |
| Step 3 | 创建适配器（RPCAsyncAdapter） | 30分钟 |
| Step 4 | 重构领域层接口定义 | 30分钟 |
| Step 5 | 删除原实现文件 `rpc_async_impl.go` | 5分钟 |
| Step 6 | 更新所有引用（grep + 修改 import） | 1.5小时 |
| Step 7 | 删除 `pkg/async` 包 | 30分钟 |
| Step 8 | 运行测试验证 | 1.5小时 |

**总计** | | **~6.5小时** |

**分层架构示意：**
```
┌─────────────────────────────────────────────────────────┐
│  领域层 (internal/domain/service)                        │
│  ─────────────────────────────────                      │
│  • RPCAsync 接口        ← 保持不变                      │
│  • AsyncOperation[T]    ← 保持不变                      │
│  • 结果类型定义         ← 保持不变                      │
│  • BroadcastOption      ← 保持不变                      │
└─────────────────────────────────────────────────────────┘
                            ↑
                            │ 依赖（领域层定义接口）
                            ↓
┌─────────────────────────────────────────────────────────┐
│  基础设施层 (internal/infrastructure/rpc)                │
│  ────────────────────────────────────────               │
│  • async_rpc_impl.go     ← 新建，移动实现               │
│  • async_rpc_adapter.go  ← 新建，移动适配器             │
└─────────────────────────────────────────────────────────┘
```

#### 3.2 关键设计点

**依赖关系分析**：

正确的依赖方向（无循环依赖）：
```
调用方向:
  Application → domain/service (接口)
                      ↑
                      │ 实现
                      ↓
            infrastructure/rpc (实现类)

导入方向:
  domain/service  ←  (被依赖)
            ↑
  infrastructure/rpc (导入并实现领域层接口)
```

**说明**：
- `domain/model`: 定义领域模型（如 PeerID, Message）
- `domain/service`: 定义服务接口（如 RPCAsync, AsyncOperation），不依赖其他层
- `infrastructure/rpc`: 实现接口，导入 domain/service，依赖领域层

**无需担心循环依赖**：
1. 领域层只定义接口，不实现
2. 基础设施层实现接口，导入领域层
3. 调用方负责组装依赖

**具体实施步骤**：

| 步骤 | 内容 | 预计时间 |
|------|------|----------|
| Step 1 | 创建目录结构 `internal/infrastructure/rpc/` | 10分钟 |
| Step 2 | 迁移实现代码（asyncOpImpl[T]、工厂函数、辅助函数） | 2小时 |
| Step 3 | 创建适配器（RPCAsyncAdapter） | 30分钟 |
| Step 4 | 重构领域层接口定义 | 30分钟 |
| Step 5 | 删除原实现文件 `rpc_async_impl.go` | 5分钟 |
| Step 6 | 更新所有引用（grep + 修改 import） | 1.5小时 |
| Step 7 | 删除 `pkg/async` 包 | 30分钟 |
| Step 8 | 运行测试验证 | 1.5小时 |
| **总计** | | **~6.5小时** |

---

### 3.3 重构后 AsyncOperation 使用方式

重构完成后，调用方式保持不变，但 import 路径会变：

**重构前（当前）**：
```go
import "github.com/jzhang405/NexKV/internal/domain/service"

// 调用方
adapter := service.NewRPCAsyncAdapter(rpc, provider)
op := service.NewAsyncCall(ctx, rpc, peer, req, timeout, provider)
```

**重构后（目标）**：
```go
import (
    "github.com/jzhang405/NexKV/internal/domain/service"
    rpcinfra "github.com/jzhang405/NexKV/internal/infrastructure/rpc"
)

// 调用方 - 通过适配器（推荐）
adapter := rpcinfra.NewRPCAsyncAdapter(rpc, provider)
op := adapter.CallAsync(ctx, peer, req)

// 或直接使用工厂函数
op := rpcinfra.NewAsyncCall(ctx, rpc, peer, req, timeout, provider)
```

**关键变化**：
1. `NewRPCAsyncAdapter` 移动到 `internal/infrastructure/rpc/`
2. `NewAsyncCall` 等工厂函数移动到 `internal/infrastructure/rpc/`
3. 调用方通过适配器使用，接口保持不变
4. 领域层接口（`service.RPCAsync`）保持不变

---

### 3.4 关联 P1/P2 问题（DDD 架构审查）

本 PR 解决的 P0-2 问题，同时关联以下问题：

**P1 - 中等风险（建议修复）**：

| 问题 | 文件 | 说明 | 本次是否解决 |
|------|------|------|-------------|
| P1-1: 应用层重复实现基础设施层功能 | `internal/application/clock/clock_service.go` | 应用层实现了 HLCProvider，与基础设施层重复 | ❌ 否 |
| P1-2: 领域服务包含过多基础设施关注点 | `internal/domain/service/rpc_async_impl.go` | submitTask 直接创建 goroutine | ✅ 是（P0-2 的一部分） |

**P2 - 低风险（可选优化）**：

| 问题 | 文件 | 说明 | 本次是否解决 |
|------|------|------|-------------|
| P2-1: 领域服务接口定义过于庞大 | `internal/domain/service/concurrency.go` | GoroutineProvider 接口 65 行，18 个方法 | ❌ 否 |
| P2-2: 领域对象命名不够业务化 | `internal/domain/model/goroutine.go` | GoroutinePriority 应改为 TaskPriority | ❌ 否 |

---

### 4. 风险评估与应对措施

| 风险点 | 影响等级（高/中/低） | 应对措施 |
|--------|----------------------|----------|
| 循环导入问题 | 中 | 使用接口抽象，避免直接依赖 |
| 破坏现有 API | 高 | 保持接口签名不变，通过类型别名桥接 |
| 编译错误 | 高 | 逐步修复，使用 IDE 自动重构 |
| 测试失败 | 中 | 保留原测试，迁移后对比结果 |
| 性能退化 | 低 | 添加基准测试，对比重构前后 |
| 功能丢失 | 低 | 完整代码审查，逐行对比 |
| 并发竞态条件 | 高 | 使用 `go test -race` 检测，代码审查重点关注并发逻辑 |

---

### 4.1 接口兼容性检查清单

| 接口/类型 | 原位置 | 新位置 | 是否变化 | 验证方式 |
|-----------|--------|--------|----------|----------|
| `AsyncOperation[T]` | `domain/service` | `domain/service` | 不变 | 类型检查 |
| `RPCAsync` | `domain/service` | `domain/service` | 不变 | 类型检查 |
| `AsyncResult[T]` | `domain/service` | `domain/service` | 不变 | 类型检查 |
| `NewAsyncCall` | `rpc_async_impl.go` | `infrastructure/rpc` | 签名不变 | 编译检查 |
| `NewAsyncBroadcast` | `rpc_async_impl.go` | `infrastructure/rpc` | 签名不变 | 编译检查 |
| `RPCAsyncAdapter` | `rpc_async_impl.go` | `infrastructure/rpc` | 移动 | 单元测试 |

### 4.2 测试策略

1. **编译时检查**: `go build ./...` 确保无编译错误
2. **单元测试**: 迁移现有测试，确保全部通过 `go test ./internal/infrastructure/rpc/...`
3. **集成测试**: 验证端到端功能 `go test ./test/integration/...`
4. **基准测试**: 对比重构前后性能 `go test -bench=. ./...`
5. **竞态检测**: `go test -race ./internal/infrastructure/rpc/...`
6. **覆盖率检查**: 确保覆盖率不降低 `go test -cover ./...`

### 4.3 回滚方案

如果重构出现问题：
1. **快速回滚**: `git revert <commit>` 或切换回主分支
2. **数据无损**: 无持久化数据变更，仅代码移动
3. **备份保留**: 原文件保留在 git 历史中，可随时恢复
4. **回滚时间**: < 5 分钟

---

### 5. 架构师评审记录（循环优化，直至通过）

| 评审轮次 | 评审日期 | 评审人（架构师） | 核心评审意见 | 优化措施 | 优化结果 |
|----------|----------|------------------|-------------|----------|----------|
| 第1轮 | 2026-02-24 | DDD Agent | 1. 循环依赖描述错误（实际无循环）2. 时间估计偏乐观（4h→6.5h）3. 需确认 pkg/async 无其他依赖 | 1. 修正依赖方向2. 调整时间3. 确认无依赖 | 已修改 |
| 第2轮 | 2026-02-24 | DDD Agent | 1. 导入方向描述语义错误 2. 附录时间表格冗余 3. 数据丢失风险评估不当 | 1. 修正依赖方向描述 2. 删除冗余表格 3. 移除不适用风险项 | 已通过 |

---

### 6. 预审批确认

> **架构师签字/备注**：______ 2026-02-24 该重构方案可行，风险可控，同意启动开发，需严格按照文档落地，确保CI通过后提交Post总结。

---

### 7. 方案评估与调整（2026-02-24）

#### 7.1 方案评估结果

评估对象：AsyncOperation 增强方案（基于评审意见修订版）

| 维度 | 评分 | 说明 |
|------|------|------|
| **创新性** | ⭐⭐⭐⭐ | 方案务实 |
| **完整性** | ⭐⭐⭐⭐ | 覆盖核心需求 |
| **可行性** | ⭐⭐⭐⭐⭐ | 风险可控 |
| **兼容性** | ⭐⭐⭐⭐⭐ | 向后兼容 |
| **推荐度** | ⭐⭐⭐⭐⭐ | **强烈推荐** |

#### 7.2 方案调整

**原方案问题**：设计 AsyncCoordinator 统一执行器和追踪器，职责混乱

**调整后方案**：
1. **保留 BroadcastProgress**（追踪器职责不变）
2. **添加 Builder 模式**（更优雅的 API）
3. **删除 pkg/async**（无生产代码引用）
4. **统一回调命名**（OnMajorityReached → OnMajority）

#### 7.3 推荐实施范围（简化版）

| 任务 | 工期 | 风险 |
|------|------|------|
| BroadcastProgress Builder | 1 天 | 低 |
| 统一回调命名 | 0.5 天 | 低 |
| 删除 pkg/async | 0.5 天 | 低 |

**跳过**：
- SingleTaskProgress（已有 asyncOpImpl）
- MultiTaskProgress（BroadcastProgress 已支持）

---

## 第二部分：流程节点记录（开发/CI过程追溯）

### 1. 开发过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| 启动开发 | 2026-02-24 | [描述开发内容] | [代码提交至分支] |
| 本地测试 | 2026-02-24 | [描述测试内容] | [测试报告/覆盖率数据] |
| Post文档编写 | 2026-02-24 | [编写后置总结文档] | [第三部分：后置部分] |
| 架构师Post批准 | 2026-02-24 | [架构师评审Post文档] | [批准签字/备注] |
| 提交GitHub | 2026-02-24 | [推送分支，创建PR] | [GitHub PR链接] |

### 2. CI流程记录（修复Bug直至通过）

| CI轮次 | 触发时间 | 结果 | 问题详情 | 修复措施 | 修复结果 |
|--------|----------|------|----------|----------|----------|
| 第1轮 | 2026-02-24 | 待运行 | - | - | - |

### 3. 合并记录

| 合并时间 | 合并方式 | 审批人 | 备注 |
|----------|----------|--------|------|
| - | - | - | - |

---

## 第三部分：后置部分（CI通过后编写，总结/成果/ToDo）

### 1. 核心成果总结（开发了啥，结果怎样）

#### 1.1 功能成果
- **已完成**：[列出完成的功能点]
- **与Pre文档差异**：[说明实际实现与计划的差异]

#### 1.2 性能/数据成果
- **性能数据**：[列出实际测试数据]
- **测试成果**：[说明测试覆盖情况]

#### 1.3 代码/文档交付物

| 类型 | 具体内容 | 链接/路径 |
|------|----------|-----------|
| 代码变更 | [列出主要变更文件] | [GitHub PR链接] |
| 文档更新 | [列出更新的文档] | [文档路径] |

### 2. 未完成项与ToDo清单（有哪些没干，后续规划）

#### 2.1 本次PR未完成项
- **未支持**：[列出未实现但相关的功能]
- **遗留问题**：[列出已知问题]

#### 2.2 ToDo清单（优先级排序）

| 优先级 | 任务内容 | 预估工期 | 关联PR/需求 | 备注 |
|--------|----------|----------|-------------|------|
| 高/中/低 | [任务描述] | X个工作日 | PR-XXX | [补充说明] |

### 3. 下一步工作建议（建议干啥）
1. **优先推进**：[列出高优先级任务]
2. **监控要点**：[列出需要关注的生产指标]
3. **运维补充**：[需要补充的运维文档或操作]
4. **后续规划**：[后续功能迭代方向]
5. **反馈收集**：[需要收集的使用反馈]

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档最终版本 | V1.0 |
| 归档日期 | 2026-02-24 |
| 归档路径 | `docs/06_PM/feature/2026-02-24_PR-086_async-operation-refactor.md` |
| 后续维护人 | Claude Code |

---

## 附录：技术债务参考

### 原始技术债务文档
- **文件**：`docs/06_PM/doc/2026-02-24_P0-2_technical_debt.md`
- **状态**：推迟（记录为技术债务）
- **优先级**：中（非阻塞）

### 采用方案：激进重构（Radical Refactor）

一次性完成所有重构，不留技术债务。详细步骤见主文档 3.1 节。

### 相关文档
- `docs/09_code-review/2026-02-24_DDD_Architecture_Review_PR-073.md`
- `docs/06_PM/doc/2026-02-24_PR-073_fix_progress.md`
