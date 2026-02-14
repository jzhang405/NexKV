# 【预研报告】CosmosDB 一致性层级深度研究

> **预研目标**：研究 Azure CosmosDB 的五层一致性模型，为 NexKV 提供工业界最佳实践参考

---

## 📋 预研信息

| 项目 | 内容 |
|------|------|
| **预研主题** | CosmosDB 五层一致性模型 |
| **预研日期** | 2026-02-14 |
| **预研负责人** | 🤖 核心开发 A |
| **官方文档** | [Azure CosmosDB Consistency Levels](https://learn.microsoft.com/en-us/azure/cosmos-db/consistency-levels) |
| **预研状态** | ✅ 已完成 |

---

## 1. CosmosDB 一致性层级概述

### 1.1 五层一致性谱系

```mermaid
graph LR
    subgraph "CosmosDB 五层一致性（从强到弱）"
        C1[Strong<br/>强一致]
        C2[Bounded Staleness<br/>有界滞后]
        C3[Session<br/>会话一致]
        C4[Consistent Prefix<br/>一致前缀]
        C5[Eventual<br/>最终一致]
    end

    C1 -->|保证递减| C2 -->|保证递减| C3 -->|保证递减| C4 -->|保证递减| C5

    style C1 fill:#ffcdd2
    style C2 fill:#ffccbc
    style C3 fill:#fff59d
    style C4 fill:#c8e6c9
    style C5 fill:#e0f7fa
```

### 1.2 一致性级别对比

| 级别 | 读保证 | 写保证 | 延迟 | 可用性 | 适用场景 |
|------|--------|--------|------|--------|---------|
| **Strong** | 线性一致 | 同步复制 | 最高 | 最低 | 金融交易 |
| **Bounded Staleness** | 最多落后 K 版本/T 时间 | 同步 + 异步 | 高 | 中 | 实时分析 |
| **Session** | 会话内单调读写 | 异步 | 中 | 高 | 用户会话 |
| **Consistent Prefix** | 保序但可能滞后 | 异步 | 低 | 高 | 消息队列 |
| **Eventual** | 最终收敛 | 异步 | 最低 | 最高 | 社交动态 |

---

## 2. 五层一致性详解

### 2.1 Strong（强一致）

```mermaid
sequenceDiagram
    participant C as Client
    participant R1 as Replica 1 (Leader)
    participant R2 as Replica 2
    participant R3 as Replica 3

    Note over C,R3: Strong 一致性：线性化读写

    C->>R1: Write(x=1)
    R1->>R2: Replicate(x=1)
    R1->>R3: Replicate(x=1)
    R2-->>R1: ACK
    R3-->>R1: ACK
    R1-->>C: Write OK

    Note over C,R3: 所有副本确认后才返回

    C->>R2: Read(x)
    R2-->>C: x=1 (guaranteed)

    Note over C,R3: 任何副本读都返回最新值
```

**特点**：
- ✅ 线性一致性（Linearizable）
- ✅ 任何副本的读都返回最新写入
- ❌ 延迟最高（需要同步复制）
- ❌ 可用性最低（任何副本故障都会阻塞）

**数学定义**：
```
Strong Consistency:
  ∀ read(r): r returns the latest committed write
  ∀ ops: total order exists
```

### 2.2 Bounded Staleness（有界滞后）

```mermaid
sequenceDiagram
    participant C as Client
    participant R1 as Replica 1
    participant R2 as Replica 2
    participant R3 as Replica 3

    Note over C,R3: Bounded Staleness: 最多落后 K 版本或 T 时间

    C->>R1: Write(v=100) at t0
    R1-->>C: Write OK (立即返回)

    Note over R2,R3: R2, R3 可能落后

    C->>R2: Read() at t1
    Note over R2: 如果 t1 - t0 < T 且 version_diff < K
    R2-->>C: v >= 100 - K (有界滞后)

    Note over C,R3: 保证: 不会无限落后
```

**特点**：
- ✅ 有界的一致性保证
- ✅ 可配置的滞后阈值（K 版本或 T 时间）
- ⚠️ 延迟和可用性的平衡点
- ⚠️ 实现复杂度高

**数学定义**：
```
Bounded Staleness:
  ∀ read(r) at time t:
    |t - t_latest_commit| ≤ T  ∨
    |version(r) - version_latest| ≤ K
```

**配置参数**：
| 参数 | 说明 | 典型值 |
|------|------|--------|
| **K (Versions)** | 允许落后的版本数 | 10-1000 |
| **T (Time)** | 允许落后的时间 | 100ms-5min |

### 2.3 Session（会话一致）

```mermaid
sequenceDiagram
    participant C as Client (Session)
    participant R1 as Replica 1
    participant R2 as Replica 2

    Note over C,R2: Session 一致性: 会话内保证

    C->>R1: Write(x=1)
    R1-->>C: Write OK

    Note over C: Session 记录: x=1, version=1

    C->>R2: Read(x)
    Note over R2: 检查 Session 状态
    R2-->>C: x=1 (保证读自己的写)

    C->>R1: Write(x=2)
    R1-->>C: Write OK

    C->>R2: Read(x)
    Note over R2: 单调读保证
    R2-->>C: x=2 (不会读到旧值)

    Note over C,R2: 会话内保证: 单调读、写后读
```

**Session 一致性保证**：

| 保证 | 说明 |
|------|------|
| **Read Your Writes** | 读到自己最新的写 |
| **Monotonic Reads** | 后续读不会比之前读旧 |
| **Monotonic Writes** | 写顺序保持 |
| **Writes Follow Reads** | 写在相关的读之后 |

**数学定义**：
```
Session Consistency:
  Let S be a session, op1, op2 ∈ S, op1 → op2 (causality):
    - If op1 = Write(x), op2 = Read(x): Read returns value ≥ Write(x).value
    - If op1 = Read(x), op2 = Read(x): op2.value ≥ op1.value
```

### 2.4 Consistent Prefix（一致前缀）

```mermaid
sequenceDiagram
    participant W as Writer
    participant R1 as Replica 1
    participant R2 as Replica 2

    Note over W,R2: Consistent Prefix: 保证顺序，可能滞后

    W->>R1: Write(x=1) at t1
    W->>R1: Write(x=2) at t2
    W->>R1: Write(x=3) at t3

    Note over R2: R2 可能只收到部分更新

    loop Reader
        R2->>R2: 读取状态
        alt 收到 t1
            R2-->>R2: x=1 ✅ (前缀一致)
        else 收到 t1, t2
            R2-->>R2: x=2 ✅ (前缀一致)
        else 未收到任何
            R2-->>R2: 初始值 ✅ (前缀一致)
        else 收到 t1, t3 (跳过 t2)
            R2-->>R2: ❌ 不允许!
        end
    end
```

**特点**：
- ✅ 保证更新顺序一致
- ✅ 不会看到"未来"的状态
- ⚠️ 可能看到旧状态
- ⚠️ 不保证时效性

**数学定义**：
```
Consistent Prefix:
  If a read sees version v_n, it must also see all v_i where i < n
  (reads see a prefix of the write sequence)
```

### 2.5 Eventual（最终一致）

```mermaid
sequenceDiagram
    participant C1 as Client 1
    participant C2 as Client 2
    participant R1 as Replica 1
    participant R2 as Replica 2
    participant R3 as Replica 3

    Note over C1,R3: Eventual Consistency: 最终收敛

    C1->>R1: Write(x=1)
    R1-->>C1: OK (立即返回)

    C2->>R2: Read(x)
    R2-->>C2: x=0 (可能旧值)

    Note over R1,R3: 后台异步复制

    R1->>R2: Replicate(x=1)
    R1->>R3: Replicate(x=1)

    Note over R1,R3: 经过一段时间后...

    C2->>R2: Read(x)
    R2-->>C2: x=1 (最终一致)

    Note over C1,R3: 保证: 无新写入时最终收敛
```

**特点**：
- ✅ 最高可用性
- ✅ 最低延迟
- ❌ 可能读到陈旧数据
- ❌ 无顺序保证

**数学定义**：
```
Eventual Consistency:
  If no new updates are made, eventually all accesses return the last updated value
  (no guarantee on when convergence happens)
```

---

## 3. 一致性级别选择指南

### 3.1 决策树

```mermaid
flowchart TD
    Start[需要什么一致性?] --> Q1{强一致必须?}

    Q1 -->|是| Strong[Strong<br/>金融、库存]

    Q1 -->|否| Q2{有界滞后可接受?}

    Q2 -->|是| Q3{需要多少滞后?}
    Q3 -->|版本数| BoundedV[Bounded Staleness<br/>按版本]
    Q3 -->|时间| BoundedT[Bounded Staleness<br/>按时间]

    Q2 -->|否| Q4{会话保证需要?}

    Q4 -->|是| Session[Session<br/>用户交互]

    Q4 -->|否| Q5{顺序保证需要?}

    Q5 -->|是| Prefix[Consistent Prefix<br/>消息队列]

    Q5 -->|否| Eventual[Eventual<br/>社交动态]

    style Strong fill:#ffcdd2
    style BoundedV fill:#ffccbc
    style BoundedT fill:#ffccbc
    style Session fill:#fff59d
    style Prefix fill:#c8e6c9
    style Eventual fill:#e0f7fa
```

### 3.2 场景映射

| 场景 | 推荐级别 | 理由 |
|------|---------|------|
| **银行转账** | Strong | 余额必须准确 |
| **库存管理** | Strong/Bounded | 超卖不可接受 |
| **用户资料** | Session | 用户看到自己的更新 |
| **消息队列** | Consistent Prefix | 顺序重要 |
| **点赞计数** | Eventual | 最终准确即可 |
| **日志分析** | Bounded Staleness | 允许一定滞后 |
| **购物车** | Session | 用户体验优先 |

---

## 4. NexKV 与 CosmosDB 对比

### 4.1 架构对比

```mermaid
graph TB
    subgraph "CosmosDB: 请求级配置"
        CO1[Client Request<br/>+ Consistency Level]
        CO2[Consistency Router]
        CO3[Replica Set]

        CO1 -->|指定级别| CO2 -->|路由到副本| CO3
    end

    subgraph "NexKV: Namespace 级固定"
        NO1[Client Request]
        NO2[Namespace Router]
        NO3[Layer1: 2PC]
        NO4[Layer2: Quorum]
        NO5[Layer3: Gossip]

        NO1 -->|写数据| NO2
        NO2 -->|cluster/shard| NO3
        NO2 -->|role/topo| NO4
        NO2 -->|其他| NO5
    end

    style CO1 fill:#bbdefb
    style NO2 fill:#c8e6c9
```

### 4.2 一致性映射

| CosmosDB 级别 | NexKV Layer | 对应关系 |
|--------------|-------------|---------|
| **Strong** | Layer1 (2PC) | 完全对应 |
| **Bounded Staleness** | Layer2 (Quorum) | 部分对应（Quorum 提供有界滞后） |
| **Session** | - | 未实现 |
| **Consistent Prefix** | Layer3 (Gossip) | Gossip 保证顺序传播 |
| **Eventual** | Layer3 (Gossip) | 完全对应 |

### 4.3 差异分析

| 维度 | CosmosDB | NexKV |
|------|----------|-------|
| **配置粒度** | 请求级别 | Namespace 级别 |
| **级别数量** | 5 种 | 3 种 |
| **Session 支持** | ✅ | ❌ |
| **Bounded Staleness** | ✅ 可配置 K/T | ⚠️ 隐式（Quorum） |
| **动态切换** | ✅ | ❌ |
| **SLA 保证** | ✅ 99.999% | ❌ |
| **适用场景** | 多租户云服务 | 单租户内部系统 |

---

## 5. NexKV 优化建议

### 5.1 短期：保持简化

```go
// 当前设计：Namespace 级固定
func (c *TreeTopologyCoordinator) GetLayerForNamespace(ns string) Layer {
    switch ns {
    case "cluster", "shard", "static", "version":
        return Layer1 // ~ Strong
    case "role", "topo":
        return Layer2 // ~ Bounded Staleness
    default:
        return Layer3 // ~ Eventual
    }
}
```

### 5.2 中期：添加 Bounded Staleness

```go
// 增强设计：Layer2 支持配置
type Layer2Config struct {
    // 允许落后的最大版本数
    MaxVersionLag int

    // 允许落后的最大时间
    MaxTimeLag time.Duration

    // 是否启用 Session 保证
    SessionGuarantees bool
}

func (c *TreeTopologyCoordinator) PutWithLayer2Config(
    ctx context.Context,
    ns, key string,
    value any,
    config *Layer2Config,
) error {
    // 检查是否在允许的滞后范围内
    if c.checkLag(config) {
        return c.quorumCoordinator.Put(ctx, ns, key, value)
    }
    // 超出范围，等待同步
    return c.waitForSync(ctx, config)
}
```

### 5.3 长期：Session 支持

```go
// Session 一致性支持
type SessionContext struct {
    SessionID   string
    LastWrite   map[string]Version  // key -> last write version
    LastRead    map[string]Version  // key -> last read version
}

func (c *TreeTopologyCoordinator) ReadWithSession(
    ctx context.Context,
    session *SessionContext,
    key string,
) (any, error) {
    // 1. 获取最后写入版本
    lastWriteVersion := session.LastWrite[key]

    // 2. 确保读到 >= lastWriteVersion
    value, version, err := c.readAtLeastVersion(ctx, key, lastWriteVersion)
    if err != nil {
        return nil, err
    }

    // 3. 更新 session 状态
    session.LastRead[key] = version

    return value, nil
}
```

---

## 6. 一致性权衡矩阵

### 6.1 延迟 vs 一致性

```mermaid
graph LR
    subgraph "延迟 vs 一致性权衡"
        A[Strong<br/>延迟: 100ms+]
        B[Bounded<br/>延迟: 50-100ms]
        C[Session<br/>延迟: 20-50ms]
        D[Prefix<br/>延迟: 10-20ms]
        E[Eventual<br/>延迟: <10ms]
    end

    A -->|延迟降低| B -->|延迟降低| C -->|延迟降低| D -->|延迟降低| E

    style A fill:#ffcdd2
    style E fill:#c8e6c9
```

### 6.2 可用性 vs 一致性

| 级别 | 分区时可用性 | 分区时一致性 |
|------|------------|------------|
| **Strong** | ❌ 不可用 | ✅ 完全一致 |
| **Bounded** | ⚠️ 部分可用 | ⚠️ 有界滞后 |
| **Session** | ✅ 可用 | ⚠️ 会话内一致 |
| **Prefix** | ✅ 可用 | ⚠️ 顺序一致 |
| **Eventual** | ✅ 可用 | ❌ 可能不一致 |

---

## 7. 参考资料

### 7.1 官方文档

- [Azure CosmosDB - Consistency Levels](https://learn.microsoft.com/en-us/azure/cosmos-db/consistency-levels)
- [Consistency Level Tradeoffs](https://learn.microsoft.com/en-us/azure/cosmos-db/consistency-level-tradeoffs)

### 7.2 学术论文

- [Principles of Eventual Consistency - Microsoft Research](https://www.microsoft.com/en-us/research/wp-content/uploads/2016/02/final-printversion-10-5-14.pdf)
- [Consistency Models Survey - arXiv](https://arxiv.org/abs/1902.03305)

### 7.3 相关博客

- [CosmosDB Consistency Levels Explained](https://blog.nashtechglobal.com/global-data-distribution-and-consistency-levels-in-cosmos-db/)
- [Bounded Staleness Deep Dive](https://devblogs.microsoft.com/cosmosdb/bounded-staleness-consistency-in-azure-cosmos-db/)

---

## 8. 结论

### 8.1 核心要点

1. **CosmosDB 提供 5 种一致性级别**：从 Strong 到 Eventual
2. **请求级配置**：每个请求可以选择不同的一致性级别
3. **SLA 驱动**：99.999% 可用性保证
4. **NexKV 采用简化设计**：Namespace 级固定，3 种级别

### 8.2 对 NexKV 的启示

| 启示 | 建议 |
|------|------|
| **简化优于灵活** | 当前 3 层设计足够 |
| **Session 一致性有价值** | 可作为中期优化 |
| **Bounded Staleness 可配置** | 可增强 Layer2 |
| **按需演进** | 不需要一次性实现所有级别 |

---

**文档版本**: v1.0
**创建日期**: 2026-02-14
**最后更新**: 2026-02-14
**维护者**: 🤖 核心开发 A
**状态**: ✅ 已完成
