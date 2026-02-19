# 【PR全流程文档】Feature - Phase 1 Week 1-2 DDD 重构与 Transport 层迁移

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从需求对齐到开发完成的全流程，一个PR对应一份全流程文档，归档后作为项目追溯依据。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 架构重构（Refactor） |
| PR编号 | PR-077（创建GitHub PR后补充完整） |
| 分支名称 | feature/phase1-week1-2-transport-poc |
| 工作主题 | Phase 1 Week 1-2 DDD 重构与 Transport 层迁移 |
| 负责人 | 🤖 核心开发 A + B |
| 分支创建日期 | 2026-02-19 |
| 计划开工日期 | 2026-02-19 |
| 计划CI通过日期 | 2026-02-26 |
| 关联需求单号 | [NexKV DDD 架构实施 PR](../2026-02-18_PR-nexkv-ddd-architecture_Pre.md) |
| 架构师评审状态 | 🔄 待评审 |
| 预审批结果 | □ 未通过 |

### 2. 背景与目标（为什么干）

#### 2.1 背景

**核心问题**：NexKV 需要从现有的功能模块架构迁移到 DDD 5层架构，这是整个架构重构的第一步。

**迁移策略**：渐进式重构（Strangler Pattern）
- 不进行一次性大重构
- 分阶段渐进迁移
- 保证系统始终可运行

**迁移范围**（对齐主 PR 2.5.2 阶段 1）：
- 现有代码：`internal/transport/`（37个文件）+ `internal/rpc/`（31个文件）
- 目标架构：DDD 5层（domain + application + infrastructure）

#### 2.2 核心目标（可量化、可验证）

**目标 1：建立 DDD 目录结构**
- [ ] 创建 domain 层接口定义
- [ ] 创建 infrastructure 层实现目录
- [ ] 保持现有代码不受影响（零破坏）

**目标 2：Transport 层迁移**
- [ ] 定义 `domain/service/transport.go` 接口（6个核心接口）
- [ ] 迁移 `internal/transport/` 到 `internal/infrastructure/transport/`
- [ ] 旧代码继续工作（164 个现有测试通过）
- [ ] 新代码使用 DDD 接口（至少 3 个单元测试）

**目标 3：核心 POC 验证**
- [ ] mDNS 节点发现（局域网，发现时间 < 5 秒）
- [ ] 节点间直连通信（TCP 连接建立时间 < 1 秒）
- [ ] RPC 调用（本地回环 P99 < 2ms，局域网 P99 < 5ms）

#### 2.3 明确边界（不做什么，避免范围蔓延）

**本次不做**（严格对齐主 PR 2.5.2 阶段 1 Week 1-4）：
- ❌ 删除现有 `internal/transport/` 代码（Week 4 任务）
- ❌ 迁移 `internal/rpc/`（Week 3-4 任务）
- ❌ 实现中间件链（Week 3 任务）
- ❌ 实现熔断器（Week 3 任务）

**本次聚焦**（Week 1-2）：
- ✅ 建立DDD目录结构
- ✅ 定义domain层接口
- ✅ 直接迁移 Transport 层（重命名 + import 路径更新）
- ✅ 核心POC验证

---

### 3. 迁移方案（核心设计）⭐

#### 3.1 迁移策略：Strangler Pattern（渐进式重构）

```mermaid
flowchart LR
    subgraph "Week 1: 准备"
        A1[创建 DDD 目录结构] --> A2[定义 domain 接口]
        A2 --> A3[直接迁移 Transport]
    end

    subgraph "Week 2: 验证"
        B1[更新 import 路径] --> B2[运行现有测试]
        B2 --> B3[POC 验证]
    end

    subgraph "Week 3-4: 扩展"
        C1[迁移 RPC → Requestor] --> C2[中间件实现]
        C2 --> C3[删除旧代码]
    end

    A3 --> B1
    B3 --> C1
```

#### 3.2 目录结构设计

**现有结构**：
```
internal/
├── transport/           # 现有 Transport 实现（37个文件）
│   ├── libp2p_transport_adapter.go
│   ├── discovery.go
│   ├── p2p_service.go
│   ├── message.go
│   └── ...
├── rpc/                 # 现有 RPC 实现（31个文件）- Week 3-4 迁移
```

**目标结构（DDD 5层）**：
```
internal/
├── domain/                        # 领域层（接口定义）
│   ├── model/                     # 领域模型
│   │   ├── peer.go               # PeerID, PeerAddr
│   │   ├── message.go            # Message, MessageType
│   │   └── hlc.go                # 混合逻辑时钟
│   └── service/                   # 领域服务接口
│       ├── transport.go          # Transport, Stream, Channel
│       ├── middleware.go         # Middleware, MiddlewareChain
│       └── codec.go              # Codec, Serializer
│
├── infrastructure/                # 基础设施层（实现）
│   └── transport/                 # Transport 实现
│       ├── libp2p_transport_impl.go    # libp2p 实现（直接迁移）
│       ├── discovery_impl.go           # mDNS 发现（直接迁移）
│       ├── message_codec_impl.go       # MessagePack 编解码
│       └── middleware_impl.go          # 中间件链（Week 3）
│
├── transport/                     # 现有代码（Week 4 删除）
└── rpc/                           # 现有代码（Week 3-4 迁移）
```

#### 3.3 迁移映射表（对齐主 PR 2.5.2）⭐

| 现有代码 | DDD 目标位置 | 迁移策略 | 阶段 |
|---------|-------------|----------|------|
| `internal/transport/libp2p_transport_adapter.go` | `infrastructure/transport/libp2p_transport_impl.go` | ✅ **直接迁移**（重命名） | Week 1-2 |
| `internal/transport/discovery.go` | `infrastructure/transport/discovery_impl.go` | ✅ **直接迁移**（重命名） | Week 1-2 |
| `internal/transport/message.go` | `domain/model/message.go` | 🔄 **抽象到 domain** | Week 1-2 |
| `internal/transport/peer_id.go` | `domain/model/peer.go` | 🔄 **抽象到 domain** | Week 1-2 |
| `internal/rpc/client.go` | `infrastructure/transport/requestor_impl.go` | 🔄 **Week 3-4 重构适配** | Week 3-4 |
| `internal/rpc/server.go` | `infrastructure/transport/server_impl.go` | 🔄 **Week 3-4 重构适配** | Week 3-4 |
| `internal/rpc/types.go` | `domain/model/message.go` | 🔄 **Week 3-4 抽象到 domain** | Week 3-4 |

**迁移策略说明**：
- ✅ **直接迁移**：文件重命名 + import 路径更新，代码逻辑不变
- 🔄 **抽象到 domain**：提取通用接口/模型到 domain 层
- 🔄 **重构适配**：需要重构以适配新接口

#### 3.3.1 测试迁移计划

| 现有测试 | DDD 目标位置 | 迁移策略 | 数量 |
|---------|-------------|----------|------|
| `internal/transport/*_test.go` | `infrastructure/transport/*_test.go` | ✅ 直接迁移 | 164 个 |
| `internal/rpc/*_test.go` | `infrastructure/transport/*_test.go` | 🔄 Week 3-4 迁移 | 169 个 |

**测试迁移步骤**：
1. Week 1：迁移 Transport 测试（直接复制）
2. Week 2：更新测试 import 路径
3. Week 3-4：迁移 RPC 测试

**验收标准**：
- [ ] 所有测试在新位置运行通过（164 + 169 = 333 个测试）
- [ ] 测试覆盖率 ≥ 80%

#### 3.4 接口设计（Week 1-2）

##### 3.4.1 Transport 接口（domain 层）

```go
// internal/domain/service/transport.go

// Transport 传输层核心接口
type Transport interface {
    // 基础传输能力
    Self() PeerID
    Connect(ctx context.Context, addr string) (PeerID, error)
    Disconnect(peer PeerID) error
    ConnectedPeers() []PeerID
    IsConnected(peer PeerID) bool
    Close() error
}

// Stream 流式通信接口
type Stream interface {
    ID() string
    Protocol() string
    RemotePeer() PeerID
    Read(p []byte) (n int, err error)
    Write(p []byte) (n int, err error)
    Close() error
}

// Channel 双向通道接口
type Channel interface {
    Send(ctx context.Context, msg Message) error
    Recv(ctx context.Context) (Message, error)
    Close() error
}
```

##### 3.4.2 实现示例（直接迁移策略）

```go
// internal/infrastructure/transport/libp2p_transport_impl.go
// 从 internal/transport/libp2p_transport_adapter.go 直接迁移

// Libp2pTransportImpl 实现 domain.Transport 接口
type Libp2pTransportImpl struct {
    host      host.Host
    protocol  *NexKVProtocol
    discovery *DiscoveryService
    // 直接使用 libp2p 原生对象
}

func NewLibp2pTransportImpl(cfg *Config) (*Libp2pTransportImpl, error) {
    // 直接实现，不包装旧代码
    h, err := libp2p.New(...)
    // ...
}

// 实现 domain.Transport 接口
func (t *Libp2pTransportImpl) Self() domain.PeerID {
    return domain.PeerID(t.host.ID().String())
}
```

#### 3.5 向后兼容性保证与清理计划 ⭐

**阶段 1（Week 1-3）：并行运行**
- 现有代码继续工作（零破坏）
- 新代码使用 DDD 接口
- 两者可以共存

```go
// 现有代码继续工作（零破坏）
legacyTransport := transport.NewLibp2pTransportAdapter(...)

// 新代码使用 DDD 接口
var transportService domain.Transport = infrastructure.NewLibp2pTransportImpl(...)
```

**阶段 2（Week 4）：清理与删除** ⭐ 对齐主 PR 2.5.2

- [ ] 验证所有引用已切换到新接口
- [ ] 删除旧代码：
  - `internal/transport/libp2p_transport_adapter.go`（已迁移）
  - `internal/transport/discovery.go`（已迁移）
  - 其他已迁移的文件
- [ ] 更新所有 import 路径
- [ ] 运行完整测试套件（164+ Transport 测试 + 169 RPC 测试）

**验收标准**：
- [ ] 旧代码目录为空或删除
- [ ] 无编译错误
- [ ] 所有测试通过

---

### 4. 风险评估与应对措施 ⭐

| 风险点 | 影响等级 | 概率 | 应对措施 | 检查点 |
|--------|----------|------|----------|--------|
| **破坏现有功能** | **高** | 中 | 1. Strangler Pattern（渐进式重构）<br/>2. 保持现有代码不变（Week 1-3）<br/>3. 充分测试（164+ 测试）<br/>4. Week 4 清理前完整验证 | Week 4 清理前 |
| **迁移周期过长** | 中 | 低 | 1. 分阶段迁移（Week 1-2 / Week 3-4）<br/>2. 自动化测试<br/>3. 每日进度跟踪 | Week 2 检查点 |
| **DDD 接口设计不合理** | 中 | 中 | 1. 参考主 PR v18.0 接口定义<br/>2. 架构师评审<br/>3. Week 1-2 迭代优化 | Week 1 架构师评审 |
| **团队学习成本** | 低 | 低 | 1. ✅ Week 0 DDD 培训已完成<br/>2. 代码示例<br/>3. 结对编程 | Week 0 培训完成 |

---

### 5. 依赖与前置条件

**前置条件**：
- [x] Go 1.21+
- [x] libp2p v0.33+
- [x] Week 0 培训已完成（DDD + Go 泛型 + libp2p）
- [x] 主 PR Pre 文档 v1.5 已批准

**技术依赖**：
- 现有代码：`internal/transport/`（37个文件）
- 现有代码：`internal/rpc/`（31个文件）- Week 3-4 迁移
- 现有测试：164个 Transport 测试 + 169个 RPC 测试

---

### 6. 架构师评审记录（循环优化，直至通过）

| 评审轮次 | 评审日期 | 评审人 | 核心评审意见 | 优化措施 | 优化结果 |
|----------|----------|--------|--------------|----------|----------|
| 第1轮 | - | 👤 架构师 | [待评审] | - | - |

### 7. 预审批确认

> **架构师签字/备注**：[待审批]

---

## 第二部分：流程节点记录（开发/CI过程追溯）

### 1. 开发过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| 创建 DDD 目录 | - | [待开发] | internal/domain/, internal/infrastructure/ |
| 定义 domain 接口 | - | [待开发] | domain/service/transport.go |
| 直接迁移 Transport | - | [待开发] | infrastructure/transport/*_impl.go |
| 本地测试 | - | [待测试] | 测试报告 |

### 2. CI流程记录

| CI轮次 | 触发时间 | 结果 | 问题详情 | 修复措施 | 修复结果 |
|--------|----------|------|----------|----------|----------|
| - | - | - | - | - | - |

---

## 第三部分：后置部分（CI通过后编写，总结/成果/ToDo）

### 1. 核心成果总结（开发了啥，结果怎样）

#### 1.1 功能成果
- **已完成**：[待填写]
- **与Pre文档差异**：[待填写]

#### 1.2 迁移成果
| 迁移项 | 状态 | 说明 |
|--------|------|------|
| DDD 目录结构 | [待完成] | domain/, infrastructure/ |
| Transport 接口定义 | [待完成] | 6个核心接口 |
| 直接迁移 Transport | [待完成] | libp2p_transport_impl.go 等 |
| 向后兼容验证 | [待完成] | 现有测试通过 |

#### 1.3 代码/文档交付物

| 类型 | 具体内容 | 链接/路径 |
|------|----------|-----------|
| 代码变更 | domain 层接口 | internal/domain/service/transport.go |
| 代码变更 | infrastructure 层实现 | internal/infrastructure/transport/ |
| 文档更新 | 本 Pre 文档 | docs/06_PM/feature/ |

### 2. 未完成项与ToDo清单

#### 2.1 本次PR未完成项
- **未支持**：[待填写]
- **遗留问题**：[待填写]

#### 2.2 ToDo清单（优先级排序）

| 优先级 | 任务内容 | 预估工期 | 关联PR/需求 | 备注 |
|--------|----------|----------|-------------|------|
| 高 | Week 3-4: RPC 迁移为 Requestor | 2周 | PR-078 | 下一阶段 |
| 高 | Week 3-4: 中间件和容错机制 | 2周 | PR-078 | 下一阶段 |
| 高 | Week 4: 删除旧代码 | 1周 | PR-079 | 清理阶段 |

### 3. 下一步工作建议
1. **优先推进**：Week 3-4 RPC 迁移（Requestor 接口）
2. **监控要点**：DDD 架构迁移进度
3. **后续规划**：M1 里程碑验收（阶段 1 完成）

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档最终版本 | V1.2 |
| 归档日期 | - |
| 归档路径 | docs/06_PM/feature/2026-02-19_PR-phase1-week1-2-transport-poc_Pre.md |
| 后续维护人 | 🤖 核心开发 A + B |

---

## 参考资料

- 📋 主 PR Pre 文档：[2026-02-18_PR-nexkv-ddd-architecture_Pre.md](../2026-02-18_PR-nexkv-ddd-architecture_Pre.md)
  - **重点参考**：2.5 现有代码迁移与重构策略
- 📚 培训材料：[Day1-AM-DDD-Architecture.md](../../08_training/2026-02-18_Day1-AM-DDD-Architecture.md)
- 📚 培训材料：[Day2-3-libp2p-Basics.md](../../08_training/2026-02-18_Day2-3-libp2p-Basics.md)
