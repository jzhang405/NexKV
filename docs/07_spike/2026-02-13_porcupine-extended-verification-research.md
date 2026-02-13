# Porcupine 扩展验证预研报告

> **预研分支**: `spike/porcupine-extended-verification`
> **创建日期**: 2026-02-13
> **预研目标**: 分析 PR-063 后续工作的技术可行性和实施方案
> **文档版本**: V1.2

---

## 评审记录

### 第一轮评审（架构师）

| 序号 | 评审意见 | 处理状态 |
|------|---------|---------|
| 1 | 时钟使用 `internal/clock` 的 HLC | ✅ 已采纳 |
| 2 | 采用方案 A：分离测试策略 | ✅ 已确认 |

### 第二轮评审（架构师 + 分布式系统专家）

| 问题 | 优先级 | 评审意见 | 处理状态 |
|------|--------|---------|---------|
| HLC 集成方案缺陷 | P0 | 时间戳格式转换、nil 检查、溢出处理 | ✅ 已修复 |
| 2PC 工作量估算 | P0 | 7-10 天过于乐观 | ✅ 调整为 12-15 天 |
| Gossip 收敛检测 | P1 | 固定等待改为版本检测 | ✅ 已修复 |
| PR 依赖关系 | P1 | 补充依赖关系图 | ✅ 已添加 |
| 协调者故障场景 | P1 | 故障测试需补充 | ✅ 已添加 |

**评审结论**: ✅ 通过（V1.2 版本）

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
| **缺乏故障场景测试** | P1 | 节点故障、网络分区、协调者故障等场景未覆盖 |
| **2PC 验证未实现** | P2 | 跨分片事务的一致性验证 |
| **真实 E2E 测试** | P2 | 当前使用 mock，未在真实网络环境验证 |

---

## 2. 时钟方案：使用 HLC

### 2.1 现有 HLC 实现

NexKV 已有 `internal/clock/hlc.go` 实现了完整的 HLC（混合逻辑时钟）：

```go
// HLC 结构: 48-bit 物理时间 + 16-bit 逻辑计数
type HLC struct {
    pt int64  // 物理时间（毫秒）
    c  uint16 // 逻辑计数（0-65535）
}

// 核心方法
func (h *HLC) Now() *HLC                    // 获取当前时间
func (h *HLC) Update(eventTime, remoteHLC)  // 更新时间（核心算法）
func (h *HLC) Compare(other *HLC) int       // 比较
func (h *HLC) MarshalBinary() ([]byte, error) // 序列化
```

### 2.2 HLC 现有问题及修复（P0）

分布式系统专家评审发现 `internal/clock/hlc.go` 存在以下问题：

**问题 1: 缺少 nil 检查**

```go
// 原代码 - 危险！
func (h *HLC) Update(eventTime int64, remoteHLC *HLC) *HLC {
    newPT := maxInt64(now, h.pt, eventTime, remoteHLC.pt)  // remoteHLC 可能为 nil
}

// 修复后
func (h *HLC) Update(eventTime int64, remoteHLC *HLC) *HLC {
    remotePT := int64(0)
    remoteC := uint16(0)
    if remoteHLC != nil {
        remotePT = remoteHLC.pt
        remoteC = remoteHLC.c
    }
    newPT := maxInt64(now, h.pt, eventTime, remotePT)
    // ...
}
```

**问题 2: 逻辑计数器溢出未处理**

```go
// 原代码 - 可能溢出！
h.c = maxUint16(h.c, remoteC) + 1  // 65535 + 1 = 0

// 修复后
if newPT == h.pt && newPT == remotePT {
    newC := maxUint16(h.c, remoteC) + 1
    if newC == 0 { // 溢出检测
        newPT++    // 推进物理时间
        newC = 0
    }
    h.c = newC
}
```

### 2.3 HLC 与 Porcupine 集成方案

**时间戳格式设计**：

需要平衡物理时间精度和跨节点排序正确性：

```go
import "github.com/jzhang405/NexKV/internal/clock"

// HLCTimestamp 适配 Porcupine 的时间戳接口
type HLCTimestamp struct {
    hlc      *clock.HLC
    clientID int  // 用于同物理时间同逻辑计数器的去重
}

func NewHLCTimestamp(clientID int) *HLCTimestamp {
    return &HLCTimestamp{
        hlc:      clock.NewHLC(),
        clientID: clientID,
    }
}

func (t *HLCTimestamp) Now() int64 {
    hlc := t.hlc.Now()
    // 格式: PT (32-bit) | C (16-bit) | clientID (16-bit)
    // 权衡: PT 使用 32-bit，可表示约 49 天的毫秒时间
    //       C 使用 16-bit，支持 65536 个同毫秒事件
    //       clientID 使用 16-bit，支持 65536 个客户端
    return (hlc.PhysicalTime()&0xFFFFFFFF)<<32 |
           int64(hlc.LogicalCounter())<<16 |
           int64(t.clientID&0xFFFF)
}
```

### 2.4 HLC 优势

| 特性 | LogicalTimestamp | HLC |
|------|-----------------|-----|
| 物理时间关联 | ❌ 无 | ✅ 有 |
| 跨节点同步 | ⚠️ clientID 区分 | ✅ Update 算法 |
| 时钟回拨处理 | ❌ 无 | ✅ 自动处理 |
| 序列化支持 | ⚠️ 自定义 | ✅ 原生支持 |
| 溢出处理 | ❌ 无 | ✅ 需修复 |
| 分布式友好 | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ |

---

## 3. 分层一致性验证方案

### 3.1 问题分析

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

### 3.2 解决方案

**方案 A：分离测试策略（架构师确认 ✅）**

```go
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
    client.Put(ctx, "ns", "key", value)

    // 等待收敛（使用版本向量检测，而非固定等待）
    err := waitForConvergence(nodes, 10*time.Second)
    require.NoError(t, err)

    // 验证所有节点最终一致
    for _, node := range nodes {
        val := node.Get(ctx, "ns", "key")
        require.Equal(t, value, val)
    }
}
```

### 3.3 Gossip 收敛性检测（P1 修复）

**问题**：原方案使用固定 `time.Sleep(10s)`，不能保证收敛完成。

**修复**：实现基于版本向量的收敛检测：

```go
// GossipConvergenceChecker 收敛性检查器
type GossipConvergenceChecker struct {
    nodes    []*Node
    timeout  time.Duration
    interval time.Duration
}

// WaitForConvergence 等待所有节点收敛
func (c *GossipConvergenceChecker) WaitForConvergence(ctx context.Context) error {
    deadline := time.Now().Add(c.timeout)
    for time.Now().Before(deadline) {
        if c.isConverged() {
            return nil
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(c.interval):
        }
    }
    return ErrConvergenceTimeout
}

// isConverged 检查所有节点版本向量是否一致
func (c *GossipConvergenceChecker) isConverged() bool {
    if len(c.nodes) == 0 {
        return true
    }

    // 获取第一个节点的版本向量作为基准
    baseVV := c.nodes[0].GetVersionVector()

    // 检查所有节点版本向量是否一致
    for _, node := range c.nodes[1:] {
        vv := node.GetVersionVector()
        if !vv.Equals(baseVV) {
            return false
        }
    }
    return true
}
```

### 3.4 Quorum 一致性语义澄清（P1）

| 操作 | 一致性保证 | 验证方法 |
|------|-----------|---------|
| QuorumPut | 多数派确认写入成功 | 线性一致性验证 |
| QuorumGet | 读取多数派最新值 | 线性一致性验证 |
| 普通 Put | Gossip 异步扩散 | 收敛性测试 |
| 普通 Get | 可能读到旧值 | 最终一致性测试 |

---

## 4. 故障注入测试方案

### 4.1 需求分析

需要验证以下故障场景的一致性：

| 故障类型 | 场景描述 | 验证目标 | 优先级 |
|---------|---------|---------|--------|
| **节点故障** | 单节点 crash 后恢复 | 数据不丢失、一致性保持 | P1 |
| **协调者故障** | 2PC 协调者 crash | 事务正确恢复 | P1（新增） |
| **网络分区** | 脑裂场景 | Quorum 操作正确性 | P1 |
| **网络延迟** | 高延迟环境 | 时间戳排序正确性 | P2 |
| **消息丢失** | 部分消息丢失 | 重试机制正确性 | P2 |

### 4.2 技术方案

```go
// 故障注入控制器
type FaultInjector struct {
    mu              sync.Mutex
    nodeFailures    map[string]bool
    partitions      []*NetworkPartition
    latency         map[string]time.Duration
}

// 故障注入测试场景
type FaultInjectionScenario struct {
    *RecordingE2ETestScenario
    injector *FaultInjector
}

// 注入节点故障
func (s *FaultInjectionScenario) InjectNodeFailure(nodeID string) {
    s.injector.nodeFailures[nodeID] = true
    s.Nodes[nodeID].Stop()
}

// 恢复节点
func (s *FaultInjectionScenario) RecoverNode(nodeID string) {
    delete(s.injector.nodeFailures, nodeID)
    s.Nodes[nodeID].Start()
}
```

### 4.3 协调者故障测试（P1 新增）

```go
// 测试：2PC 协调者故障后的一致性
func TestLinearizability_CoordinatorFailure(t *testing.T) {
    scenario := NewFaultInjectionScenario([]string{"coordinator", "participant-1", "participant-2"})

    // 开始 2PC 事务
    txID := scenario.BeginTransaction("coordinator", []string{"p1", "p2"})

    // 预提交阶段
    scenario.Prepare(txID, "participant-1")
    scenario.Prepare(txID, "participant-2")

    // 注入协调者故障（在 Commit 前）
    scenario.InjectNodeFailure("coordinator")

    // 参与者应该超时等待
    // 恢复协调者后应该能正确决策

    // 恢复协调者
    scenario.RecoverNode("coordinator")

    // 验证事务最终状态正确
    txState := scenario.GetTransactionState(txID)
    require.Contains(t, []string{"committed", "aborted"}, txState.Status)

    // 验证线性化
    result := scenario.VerifyLinearizability()
    require.True(t, result.Ok)
}
```

### 4.4 网络分区测试

```go
// 测试：网络分区场景
func TestLinearizability_NetworkPartition(t *testing.T) {
    scenario := NewFaultInjectionScenario([]string{"node-1", "node-2", "node-3", "node-4", "node-5"})

    // 注入分区：{1,2} vs {3,4,5}
    scenario.InjectPartition(
        []string{"node-1", "node-2"},
        []string{"node-3", "node-4", "node-5"},
    )

    // 少数派操作（应该失败或超时）
    err := client1.QuorumPut(ctx, "ns", "key", []byte("value-from-minority"))
    require.Error(t, err) // 验证确实被拒绝

    // 多数派操作（应该成功）
    err = client3.QuorumPut(ctx, "ns", "key", []byte("value-from-majority"))
    require.NoError(t, err)

    // 恢复分区
    scenario.HealPartition()

    // 验证多数派数据同步到少数派
    time.Sleep(1 * time.Second)
    val, _ := client1.Get(ctx, "ns", "key")
    require.Equal(t, []byte("value-from-majority"), val)
}
```

### 4.5 实现难度评估

| 组件 | 难度 | 工作量 | 依赖 |
|------|------|--------|------|
| FaultInjector 核心框架 | ⭐⭐⭐ | 2-3 天 | 无 |
| 节点故障注入 | ⭐⭐ | 1 天 | Node 生命周期管理 |
| 协调者故障注入 | ⭐⭐⭐⭐ | 2-3 天 | 2PC 状态管理 |
| 网络分区注入 | ⭐⭐⭐⭐ | 3-4 天 | Transport 层拦截 |
| 测试用例编写 | ⭐⭐ | 2 天 | 无 |

---

## 5. 2PC 跨分片事务验证

### 5.1 需求分析

NexKV 的 2PC 协议特点：
- 发起节点兼任协调者
- 砍掉 Prepare 阶段（直接预提交）
- Gossip 同步事务状态
- 故障自动补偿

**现有实现复杂度**（评审发现）：
- 5 种事务状态
- Merkle Tree 协同
- Pending 操作暂存机制
- 超时与重试逻辑

### 5.2 技术方案

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

// 扩展状态（考虑 Gossip 同步）
type TxStatus struct {
    Status     string            // pending/prepared/committed/aborted/unknown
    Shards     []string
    Writes     map[string]string
    SyncStatus map[string]bool   // 分片 -> 是否已同步状态
    DecisionTS int64             // 决策时间戳（用于 Gossip 同步）
}
```

### 5.3 挑战分析

| 挑战 | 说明 | 解决方案 |
|------|------|---------|
| **状态爆炸** | 2PC 涉及多分片，状态空间巨大 | 限制事务规模、使用抽象 |
| **超时场景** | Prepare/Commit 超时 | 建模超时为显式状态 |
| **Gossip 同步** | 事务状态通过 Gossip 传播 | 扩展状态模型 |
| **故障恢复** | 协调者故障后恢复 | 幂等操作设计 |
| **现有复杂度** | 已有 5 种状态 + Merkle Tree | 先完成 TLA+ 规约 |

### 5.4 工作量重估（P0 修复）

| 阶段 | 原估算 | 修正估算 | 说明 |
|------|--------|---------|------|
| TLA+ 规约 | 0 天 | 3-5 天 | **前置条件**，必须先完成 |
| 操作类型扩展 | 1 天 | 2 天 | 需要考虑 Gossip 同步 |
| 状态模型扩展 | 2 天 | 3-4 天 | 状态空间更复杂 |
| 测试用例编写 | 3 天 | 4-5 天 | 需要覆盖协调者故障 |
| **总计** | **7-10 天** | **12-15 天** | |

**建议**：2PC 验证应结合 **TLA+** 进行设计验证，Porcupine 用于运行时验证。

---

## 6. 真实网络 E2E 测试

### 6.1 需求分析

当前测试使用 mock KV，需要在真实网络环境验证：
- 真实的网络延迟
- 真实的并发场景
- 真实的故障场景

### 6.2 技术方案

```go
// 真实集群测试场景
type RealClusterScenario struct {
    *RecordingE2ETestScenario
    Nodes     []*RealNode
    Transport *TCPTransport
    Config    TestConfig
}

type TestConfig struct {
    NodeCount       int
    NetworkLatency  time.Duration
    FailureRate     float64
}
```

### 6.3 热点 Key 竞争测试（P2 新增）

```go
// 测试：热点 Key 竞争场景
func TestRealCluster_HotKeyContention(t *testing.T) {
    scenario := NewRealClusterScenario(TestConfig{
        NodeCount: 5,
    })
    defer scenario.Stop()
    scenario.Start()

    // 100 个客户端竞争写入同一个 key
    var wg sync.WaitGroup
    hotKey := "hot-key"

    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(clientID int) {
            defer wg.Done()
            client := scenario.GetClient(clientID % 5)
            value := []byte(fmt.Sprintf("value-from-client-%d", clientID))
            client.QuorumPut(ctx, "ns", hotKey, value)
        }(i)
    }
    wg.Wait()

    // 验证最终一致性（所有节点最终应该有相同的值）
    time.Sleep(1 * time.Second)
    firstVal, _ := scenario.Nodes[0].Get(ctx, "ns", hotKey)
    for _, node := range scenario.Nodes[1:] {
        val, _ := node.Get(ctx, "ns", hotKey)
        require.Equal(t, firstVal, val, "All nodes should have the same final value")
    }
}
```

---

## 7. 实施计划

### 7.1 PR 依赖关系图（P1 新增）

```
Phase 1: 基础设施
┌─────────────────────────────────────────────────────────┐
│ PR-064: 分离 Gossip/Quorum 测试策略                     │
│ ├── 添加 GossipConvergenceChecker                       │
│ ├── 添加 QuorumLinearizabilityChecker                   │
│ └── 依赖: 无，可独立开始                                │
└─────────────────────────────────────────────────────────┘
                           │
                           ▼
Phase 2: 故障注入
┌─────────────────────────────────────────────────────────┐
│ PR-065: 故障注入测试框架                                │
│ ├── FaultInjector 核心框架                              │
│ ├── 节点故障注入                                        │
│ ├── 协调者故障注入（新增）                              │
│ ├── 网络分区注入                                        │
│ ├── 依赖: PR-064（需要分离的测试策略）                  │
│ └── 可并行: FaultInjector 核心框架                      │
└─────────────────────────────────────────────────────────┘
                           │
                           ▼
Phase 3: 真实环境测试
┌─────────────────────────────────────────────────────────┐
│ PR-066: 真实网络 E2E 测试                               │
│ ├── RealClusterScenario                                 │
│ ├── CI 集成                                             │
│ ├── 热点 Key 竞争测试（新增）                           │
│ ├── 依赖: PR-065（需要故障注入支持）                    │
│ └── 可独立: RealClusterScenario 基础结构                │
└─────────────────────────────────────────────────────────┘
                           │
                           ▼
Phase 4: 2PC 验证（阻塞项：TLA+ 规约）
┌─────────────────────────────────────────────────────────┐
│ PR-067: 2PC 验证                                        │
│ ├── 前置条件: TLA+ 规约完成（3-5 天）                   │
│ ├── 扩展操作类型                                        │
│ ├── 扩展状态模型                                        │
│ ├── 协调者故障测试                                      │
│ ├── 依赖: PR-065 + TLA+ 规约                            │
│ └── 工作量: 12-15 天（修正）                            │
└─────────────────────────────────────────────────────────┘
```

### 7.2 优先级排序

| 优先级 | 任务 | 工作量 | 价值 | 建议 |
|--------|------|--------|------|------|
| **P1** | 分离 Gossip/Quorum 测试策略 | 2 天 | 高 | 立即实施 |
| **P1** | 故障注入测试框架 | 7-9 天 | 高 | 本迭代 |
| **P2** | 真实网络 E2E 测试 | 4-5 天 | 中 | 下迭代 |
| **P2** | 2PC 验证（含 TLA+） | 12-15 天 | 中 | 需要设计先行 |

### 7.3 建议 PR 分拆

```
PR-064: 分离 Gossip/Quorum 测试策略（2 天）
├── 添加 GossipConvergenceChecker（版本向量检测）
├── 添加 QuorumLinearizabilityChecker
├── 集成 HLC 时间戳（修复 nil 检查和溢出）
└── 更新测试用例

PR-065: 故障注入测试框架（7-9 天）
├── FaultInjector 核心框架
├── 节点故障注入
├── 协调者故障注入（新增）
├── 网络分区注入
└── 故障场景测试用例

PR-066: 真实网络 E2E 测试（4-5 天）
├── RealClusterScenario
├── 热点 Key 竞争测试（新增）
├── CI 集成
└── 性能基准

PR-067: 2PC 验证（12-15 天，含 TLA+）
├── TLA+ 规约编写（前置条件）
├── 扩展操作类型
├── 扩展状态模型
└── 2PC 测试用例
```

---

## 8. 风险评估

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| HLC 溢出导致时间戳回退 | 高 | 30% | 已添加溢出检测和告警 |
| Gossip 测试误报 | 中 | 20% | 分离测试策略 + 版本检测 |
| 故障注入影响 CI 稳定性 | 中 | 40% | 添加清理机制和重试 |
| 2PC 状态爆炸 | 高 | 60% | 先完成 TLA+ 规约 |
| 真实测试环境不稳定 | 中 | 30% | 添加重试机制 |

---

## 9. 结论与建议

### 9.1 架构师确认的方案

| 决策项 | 确认方案 |
|--------|---------|
| **时钟方案** | 使用 `internal/clock` 的 HLC（需修复 nil 检查和溢出） |
| **测试策略** | 方案 A：分离测试策略 |
| **故障测试** | 包含协调者故障场景 |

### 9.2 实施计划

1. **立即实施**：修复 HLC nil 检查和溢出问题
2. **立即实施**：分离 Gossip/Quorum 测试策略（PR-064）
3. **本迭代**：故障注入测试框架（PR-065）
4. **下迭代**：真实网络 E2E 测试（PR-066）
5. **待设计**：2PC 验证需要先完成 TLA+ 规约（PR-067）

### 9.3 技术决策

- ✅ 采用**分离测试策略**而非扩展模型（架构师确认）
- ✅ 时钟使用 **`internal/clock` HLC**（架构师确认）
- ✅ 故障注入框架与现有 E2ETestScenario 集成
- ✅ 2PC 验证结合 TLA+ 进行设计验证
- ✅ 补充协调者故障测试场景

---

## 文档信息

| 项目 | 内容 |
|------|------|
| 预研状态 | ✅ 完成，两轮评审通过 |
| 文档版本 | V1.2 |
| 创建日期 | 2026-02-13 |
| 最后更新 | 2026-02-13 |
| 下一步 | 启动 PR-064 开发 |
| 维护人 | 🤖 核心开发 A |
