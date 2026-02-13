# Porcupine 扩展验证预研报告

> **预研分支**: `spike/porcupine-extended-verification`
> **创建日期**: 2026-02-13
> **预研目标**: 分析 PR-063 后续工作的技术可行性和实施方案

---

## 1. 预研背景

### 1.1 当前状态

PR-063 已完成 Porcupine 线性一致性验证框架的基础集成：

| 完成项 | 状态 |
|--------|------|
| NexKVModel 状态模型 | ✅ |
| HistoryRecorder 事件记录器 | ✅ |
| ConsistencyChecker 检查器 | ✅ |
| RecordingClient 记录客户端 | ✅ |
| 测试覆盖率 88.2% | ✅ |

### 1.2 待解决问题

根据架构师和分布式系统专家评审，存在以下问题：

| 问题 | 优先级 | 说明 |
|------|--------|------|
| **Gossip/Quorum 模型混淆** | P1 | 两种协议共享线性一致性模型，Gossip 操作可能产生误报 |
| **缺乏故障场景测试** | P1 | 节点故障、网络分区等场景未覆盖 |
| **2PC 验证未实现** | P2 | 跨分片事务的一致性验证 |
| **真实 E2E 测试** | P2 | 当前使用 mock，未在真实网络环境验证 |

---

## 2. 技术方案分析

### 2.1 分层一致性验证方案

#### 问题分析

NexKV 采用**分层一致性**设计：

```
┌─────────────────────────────────────────────────┐
│ Layer 3: 2PC 协议（强一致）                     │
│   - 全员 commit/rollback                        │
│   - 线性一致性验证适用 ✅                       │
├─────────────────────────────────────────────────┤
│ Layer 2: Quorum 机制（增强最终一致）            │
│   - 多数派确认                                  │
│   - 线性一致性验证适用 ✅                       │
├─────────────────────────────────────────────────┤
│ Layer 1: Gossip 协议（最终一致）                │
│   - 10s 内收敛                                  │
│   - 线性一致性验证不适用 ❌                     │
└─────────────────────────────────────────────────┘
```

**核心问题**：当前 NexKVModel 对所有操作都使用线性一致性验证，但 Gossip 协议只保证最终一致性。

#### 解决方案

**方案 A：分离测试策略（推荐）**

```go
// 策略：不同协议使用不同的验证方法

// Quorum 操作：使用 Porcupine 线性一致性验证
func TestQuorumLinearizability(t *testing.T) {
    client.QuorumPut(ctx, "ns", "key", value)
    client.QuorumGet(ctx, "ns", "key")
    result := scenario.VerifyLinearizability()
    require.True(t, result.Ok)
}

// Gossip 操作：使用收敛性测试
func TestGossipConvergence(t *testing.T) {
    // 执行 Gossip 操作
    client.Put(ctx, "ns", "key", value)  // 普通 Put

    // 等待收敛（10s）
    time.Sleep(10 * time.Second)

    // 验证所有节点最终一致
    for _, node := range nodes {
        val := node.Get(ctx, "ns", "key")
        require.Equal(t, value, val)
    }
}
```

**方案 B：扩展模型支持多种一致性级别**

```go
// 定义一致性级别
type ConsistencyLevel int

const (
    LinearizableConsistency ConsistencyLevel = iota  // 线性一致
    EventualConsistency                               // 最终一致
)

// 扩展操作输入
type NexKVInput struct {
    Op          OpType
    Namespace   string
    Key         string
    Value       []byte
    Consistency ConsistencyLevel  // 新增：一致性级别
}

// 扩展模型 Step 函数
func Step(state, input, output interface{}) []interface{} {
    kvInput := input.(NexKVInput)

    switch kvInput.Consistency {
    case LinearizableConsistency:
        // 严格的线性一致性验证
        return strictLinearizableStep(state, input, output)
    case EventualConsistency:
        // 最终一致性验证（允许读到旧值）
        return eventualConsistencyStep(state, input, output)
    }
}
```

**方案对比**：

| 方案 | 优点 | 缺点 | 推荐度 |
|------|------|------|--------|
| A: 分离测试策略 | 简单、清晰、易维护 | 需要手动区分测试 | ⭐⭐⭐⭐⭐ |
| B: 扩展模型 | 统一框架、自动化 | 复杂、Porcupine 不原生支持最终一致 | ⭐⭐⭐☆☆ |

**建议**：采用**方案 A**，分离测试策略。

---

### 2.2 故障注入测试方案

#### 需求分析

需要验证以下故障场景的一致性：

| 故障类型 | 场景描述 | 验证目标 |
|---------|---------|---------|
| **节点故障** | 单节点 crash 后恢复 | 数据不丢失、一致性保持 |
| **网络分区** | 脑裂场景 | Quorum 操作正确性 |
| **网络延迟** | 高延迟环境 | 时间戳排序正确性 |
| **消息丢失** | 部分消息丢失 | 重试机制正确性 |

#### 技术方案

**方案：故障注入框架集成**

```go
// 故障注入控制器
type FaultInjector struct {
    mu              sync.Mutex
    nodeFailures    map[string]bool      // 故障节点
    partitions      []*NetworkPartition  // 网络分区
    latency         map[string]time.Duration  // 延迟注入
}

// 网络分区配置
type NetworkPartition struct {
    GroupA []string  // 分区 A 节点
    GroupB []string  // 分区 B 节点
}

// 故障注入测试场景
type FaultInjectionScenario struct {
    *RecordingE2ETestScenario
    injector *FaultInjector
}

// 注入节点故障
func (s *FaultInjectionScenario) InjectNodeFailure(nodeID string) {
    s.injector.nodeFailures[nodeID] = true
    // 停止节点
    s.Nodes[nodeID].Stop()
}

// 恢复节点
func (s *FaultInjectionScenario) RecoverNode(nodeID string) {
    delete(s.injector.nodeFailures, nodeID)
    s.Nodes[nodeID].Start()
}

// 注入网络分区
func (s *FaultInjectionScenario) InjectPartition(groupA, groupB []string) {
    partition := &NetworkPartition{GroupA: groupA, GroupB: groupB}
    s.injector.partitions = append(s.injector.partitions, partition)
    // 阻断组间通信
}
```

**测试用例示例**：

```go
// 测试：节点故障后的一致性
func TestLinearizability_NodeFailure(t *testing.T) {
    scenario := NewFaultInjectionScenario([]string{"node-1", "node-2", "node-3"})

    // 正常操作
    client.QuorumPut(ctx, "ns", "key", []byte("value1"))

    // 注入故障
    scenario.InjectNodeFailure("node-2")

    // 故障期间操作
    client.QuorumPut(ctx, "ns", "key", []byte("value2"))

    // 恢复节点
    scenario.RecoverNode("node-2")

    // 验证线性化
    result := scenario.VerifyLinearizability()
    require.True(t, result.Ok)
}

// 测试：网络分区场景
func TestLinearizability_NetworkPartition(t *testing.T) {
    scenario := NewFaultInjectionScenario([]string{"node-1", "node-2", "node-3", "node-4", "node-5"})

    // 注入分区：{1,2} vs {3,4,5}
    scenario.InjectPartition(
        []string{"node-1", "node-2"},
        []string{"node-3", "node-4", "node-5"},
    )

    // 少数派操作（应该失败或超时）
    client1.QuorumPut(ctx, "ns", "key", []byte("value-from-minority"))

    // 多数派操作（应该成功）
    client3.QuorumPut(ctx, "ns", "key", []byte("value-from-majority"))

    // 恢复分区
    scenario.HealPartition()

    // 验证最终一致性和线性化
}
```

**实现难度评估**：

| 组件 | 难度 | 工作量 | 依赖 |
|------|------|--------|------|
| FaultInjector 核心框架 | ⭐⭐⭐ | 2-3 天 | 无 |
| 节点故障注入 | ⭐⭐ | 1 天 | Node 生命周期管理 |
| 网络分区注入 | ⭐⭐⭐⭐ | 3-4 天 | Transport 层拦截 |
| 延迟注入 | ⭐⭐ | 1 天 | Transport 层延迟 |
| 测试用例编写 | ⭐⭐ | 2 天 | 无 |

---

### 2.3 2PC 跨分片事务验证

#### 需求分析

NexKV 的 2PC 协议特点：
- 发起节点兼任协调者
- 砍掉 Prepare 阶段（直接预提交）
- Gossip 同步事务状态
- 故障自动补偿

#### 技术方案

**扩展操作类型**：

```go
const (
    // 现有操作
    OpGet OpType = iota
    OpPut
    OpDelete
    OpQuorumGet
    OpQuorumPut

    // 2PC 操作
    Op2PCBegin      // 开始事务
    Op2PCPrepare    // 预提交（单分片）
    Op2PCCommit     // 提交
    Op2PCRollback   // 回滚
)

// 扩展输入
type NexKVInput struct {
    Op        OpType
    Namespace string
    Key       string
    Value     []byte

    // 2PC 相关
    TxID      string        // 事务 ID
    Shards    []string      // 涉及的分片
}

// 扩展状态
type NexKVState struct {
    KVData  map[string]string       // KV 数据
    TxState map[string]TxStatus     // 事务状态表
}

type TxStatus struct {
    Status   string  // pending/prepared/committed/aborted
    Shards   []string
    Writes   map[string]string
}
```

**状态转移函数**：

```go
func Step(state, input, output interface{}) []interface{} {
    kvInput := input.(NexKVInput)
    kvState := state.(NexKVState)

    switch kvInput.Op {
    case Op2PCBegin:
        // 创建事务状态
        newState := copyState(kvState)
        newState.TxState[kvInput.TxID] = TxStatus{
            Status: "pending",
            Shards: kvInput.Shards,
        }
        return []interface{}{newState}

    case Op2PCPrepare:
        // 检查事务状态
        tx, exists := kvState.TxState[kvInput.TxID]
        if !exists || tx.Status != "pending" {
            return nil  // 无效
        }
        // 标记为 prepared
        newState := copyState(kvState)
        newState.TxState[kvInput.TxID] = TxStatus{Status: "prepared", ...}
        return []interface{}{newState}

    case Op2PCCommit:
        // 检查所有分片是否 prepared
        // 应用写入
        // 标记为 committed

    case Op2PCRollback:
        // 清理事务状态
        // 标记为 aborted
    }
}
```

**挑战分析**：

| 挑战 | 说明 | 解决方案 |
|------|------|---------|
| **状态爆炸** | 2PC 涉及多分片，状态空间巨大 | 限制事务规模、使用抽象 |
| **超时场景** | Prepare/Commit 超时 | 建模超时为显式状态 |
| **Gossip 同步** | 事务状态通过 Gossip 传播 | 最终一致性模型 |
| **故障恢复** | 协调者故障后恢复 | 幂等操作设计 |

**建议**：2PC 验证应结合 **TLA+** 进行设计验证，Porcupine 用于运行时验证。

---

### 2.4 真实网络 E2E 测试

#### 需求分析

当前测试使用 mock KV，需要在真实网络环境验证：
- 真实的网络延迟
- 真实的并发场景
- 真实的故障场景

#### 技术方案

**集成到现有 E2ETestScenario**：

```go
// 真实集群测试场景
type RealClusterScenario struct {
    *RecordingE2ETestScenario

    // 真实节点
    Nodes    []*RealNode
    Transport *TCPTransport

    // 测试配置
    Config   TestConfig
}

type TestConfig struct {
    NodeCount       int
    NetworkLatency  time.Duration
    FailureRate     float64  // 故障注入概率
}

// 启动真实集群
func (s *RealClusterScenario) Start() error {
    for i := 0; i < s.Config.NodeCount; i++ {
        node := NewRealNode(i)
        s.Nodes = append(s.Nodes, node)
        node.Start()
    }
    // 等待集群就绪
    return s.WaitForClusterReady()
}
```

**测试用例**：

```go
func TestRealCluster_QuorumLinearizability(t *testing.T) {
    scenario := NewRealClusterScenario(TestConfig{
        NodeCount:      5,
        NetworkLatency: 10 * time.Millisecond,
    })
    defer scenario.Stop()

    scenario.Start()

    // 并发客户端操作
    var wg sync.WaitGroup
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(clientID int) {
            defer wg.Done()
            client := scenario.GetClient(clientID % 5)
            for j := 0; j < 100; j++ {
                key := fmt.Sprintf("key-%d", j)
                client.QuorumPut(ctx, "ns", key, []byte(fmt.Sprintf("value-%d", j)))
            }
        }(i)
    }
    wg.Wait()

    // 验证线性化
    result := scenario.VerifyLinearizability()
    require.True(t, result.Ok)
}
```

---

## 3. 实施计划建议

### 3.1 优先级排序

| 优先级 | 任务 | 工作量 | 价值 | 建议 |
|--------|------|--------|------|------|
| **P1** | 分离 Gossip/Quorum 测试策略 | 2 天 | 高 | 立即实施 |
| **P1** | 故障注入测试框架 | 5-7 天 | 高 | 本迭代 |
| **P2** | 真实网络 E2E 测试 | 3-4 天 | 中 | 下迭代 |
| **P2** | 2PC 验证 | 7-10 天 | 中 | 需要设计先行 |

### 3.2 建议 PR 分拆

```
PR-064: 分离 Gossip/Quorum 测试策略
├── 添加 GossipConvergenceChecker
├── 添加 QuorumLinearizabilityChecker
└── 更新测试用例

PR-065: 故障注入测试框架
├── FaultInjector 核心框架
├── 节点故障注入
├── 网络分区注入
└── 故障场景测试用例

PR-066: 真实网络 E2E 测试
├── RealClusterScenario
├── CI 集成
└── 性能基准

PR-067: 2PC 验证（需要 TLA+ 设计先行）
├── 扩展操作类型
├── 扩展状态模型
└── 2PC 测试用例
```

---

## 4. 风险评估

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| Gossip 测试误报 | 高 | 中 | 分离测试策略 |
| 故障注入影响稳定性 | 中 | 中 | 添加清理机制 |
| 2PC 状态爆炸 | 高 | 高 | 结合 TLA+ 验证 |
| 真实测试环境不稳定 | 中 | 中 | 添加重试机制 |

---

## 5. 结论与建议

### 5.1 推荐方案

1. **立即实施**：分离 Gossip/Quorum 测试策略（PR-064）
2. **本迭代**：故障注入测试框架（PR-065）
3. **下迭代**：真实网络 E2E 测试（PR-066）
4. **待设计**：2PC 验证需要先完成 TLA+ 规约

### 5.2 技术决策

- ✅ 采用**分离测试策略**而非扩展模型
- ✅ 故障注入框架与现有 E2ETestScenario 集成
- ✅ 2PC 验证结合 TLA+ 进行设计验证

---

## 文档信息

| 项目 | 内容 |
|------|------|
| 预研状态 | ✅ 完成 |
| 下一步 | 启动 PR-064 或等待架构师审批 |
| 维护人 | 🤖 核心开发 A |
