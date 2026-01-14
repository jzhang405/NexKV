# NexKV Phase 3: 故障恢复与性能优化实施指南

**创建日期**：2026-01-14
**目标**：在 TLA+ 验证的基础上，实现生产级的故障恢复和性能优化
**预计工期**：2-3 周

---

## 📋 目录

- [0. 相关文档](#0-相关文档)
- [1. 任务概览](#1-任务概览)
- [2. TLA+ 验证经验总结](#2-tla-验证经验总结)
- [3. 任务 1：崩溃恢复实现](#3-任务-1崩溃恢复实现)
- [4. 任务 2：网络分区实现](#4-任务-2网络分区实现)
- [5. 任务 3：性能基准测试](#5-任务-3性能基准测试)
- [6. Go 实现通用原则](#6-go-实现通用原则)
- [7. 验收标准](#7-验收标准)
- [8. 风险与应对](#8-风险与应对)

---

## 0. 相关文档

### 必读文档

在开始 Phase 3 实施之前，建议先阅读以下文档以了解背景：

#### [implementation-comparison.md](./implementation-comparison.md)
**Phase 2 验证报告** - 必读

**内容概要**：
- ✅ QuorumWithGossip 基础协议的 TLA+ vs Go 对应关系
- ✅ 详细的代码映射（数据结构、核心动作、安全性质）
- ✅ 已发现的 2 个设计 Bug 及其修复
- ✅ 测试用例覆盖情况（16 个测试用例，76.9% 覆盖率）
- ✅ 性能对比数据（TLA+ vs Go 测试）

**为什么必读**：
1. 理解 TLA+ 形式化规范如何映射到 Go 实现
2. 了解基础协议的现有实现细节
3. 学习已发现的设计陷阱和解决方案
4. 建立代码映射的思维模式（对后续实现故障恢复很重要）

**阅读建议**：
- 重点阅读：第 2 节（架构对比）、第 6 节（发现的问题与修复）
- 参考：第 3 节（测试用例对比）、第 7 节（经验总结）

#### [phase2-report.md](./phase2-report.md)
**Phase 2 项目报告** - 推荐

**内容概要**：
- Phase 2 的完整工作总结
- 5 节点模型状态空间爆炸问题及决策
- Go 实现验证结果
- 未完成的工作清单

**阅读价值**：
- 了解项目历史和决策背景
- 理解为什么 5 节点 TLA+ 验证被放弃
- 明确 Phase 3 需要补充的内容

#### [phase2-plan.md](./phase2-plan.md)
**Phase 2 计划文档** - 可选

**内容概要**：
- Phase 2 的原始计划
- 任务分解和时间估算
- 验证方法说明

**阅读价值**：
- 了解项目的总体规划
- 参考任务分解方法

### TLA+ 模型文件

#### [QuorumWithGossipCrash.tla](../models/QuorumWithGossipCrash.tla)
**崩溃恢复模型** - 实现时参考

**验证结果**：
- 状态数：45,169
- 验证性质：CrashedNodeNoDecision, ActiveNodesCanDecide, RecoveryCorrectness
- 结论：✅ 所有性质通过

**关键协议动作**：
```tla
NodeCrash(n) == ...      \* 节点崩溃
NodeRecover(n) == ...    \* 节点恢复
GossipExchange(p, q) == ... \* 崩溃节点不参与
```

**实现时参考**：
- 理解崩溃节点的约束（不参与 Gossip 和决策）
- 理解恢复节点的状态同步机制
- 参考不变式设计（CrashedNodeNoDecision）

#### [QuorumWithGossipPartition.tla](../models/QuorumWithGossipPartition.tla)
**网络分区模型** - 实现时参考

**验证结果**：
- 状态数：20,903
- 验证性质：PartitionSafety, MinorityCannotDecide, PartitionRecovery
- 结论：✅ 所有性质通过

**关键协议动作**：
```tla
NetworkPartition(partition1, partition2) == ... \* 触发分区
NetworkHeal == ...                            \* 愈合分区
GossipExchange(p, q) == ...                  \* 跨分区通信被阻止
```

**实现时参考**：
- 理解分区约束（只允许同分区通信）
- 理解多数派 vs 少数派分区
- 参考不变式设计（PartitionSafety, MinorityCannotDecide）

### 验证报告

#### [QuorumWithGossipCrash_REPORT.md](../reports/QuorumWithGossipCrash_REPORT.md)
**崩溃恢复模型验证报告** - 实现时参考

**内容概要**：
- 模型设计详解
- 安全性质和恢复性质
- 状态空间分析（45,169 状态）
- 典型场景验证

**阅读价值**：
- 理解崩溃恢复的理论基础
- 参考测试场景设计

#### [QuorumWithGossipPartition_REPORT.md](../reports/QuorumWithGossipPartition_REPORT.md)
**网络分区模型验证报告** - 实现时参考

**内容概要**：
- 模型设计详解
- 分区安全性和恢复性质
- 状态空间分析（20,903 状态）
- 典型场景验证（3-2 分区、3-3 分区）

**阅读价值**：
- 理解网络分区的理论基础
- 参考测试场景设计

### Go 实现文件

#### [quorum_gossip.go](../implementations/quorum_gossip.go)
**基础协议实现** - 需要修改

**当前实现**：
- Node 结构体（基础字段）
- ProposeVote、GossipExchange、DecideCommit
- Cluster 管理

**Phase 3 需要添加**：
- 崩溃恢复相关字段和方法
- 网络分区相关字段和方法
- WAL 持久化

#### [quorum_gossip_test.go](../implementations/quorum_gossip_test.go)
**基础协议测试** - 需要扩展

**当前测试**：
- 11 个基础测试用例
- 76.9% 代码覆盖率

**Phase 3 需要添加**：
- 崩溃恢复测试（5 个用例）
- 网络分区测试（5 个用例）
- 性能基准测试

---

## 1. 任务概览

### 1.1 背景

**Phase 1 & 2 成果**：
- ✅ TLA+ 模型验证了协议正确性（7,369 ~ 45,169 状态）
- ✅ Go 实现了基础协议（563 行代码，76.9% 覆盖率）
- ✅ 发现并修复了 2 个设计 Bug

**当前缺失**：
- ❌ 节点崩溃恢复机制
- ❌ 网络分区处理
- ❌ 性能基准数据
- ❌ 故障注入测试

### 1.2 Phase 3 目标

| 任务 | 目标 | 优先级 | 预计工时 |
|------|------|--------|---------|
| **任务 1** | 实现崩溃恢复 | P0 | 2-3 天 |
| **任务 2** | 实现网络分区 | P0 | 2-3 天 |
| **任务 3** | 性能基准测试 | P0 | 1-2 天 |
| 任务 4 | 故障注入测试 | P1 | 1-2 天 |
| 任务 5 | 边界条件测试 | P1 | 1 天 |
| 任务 6 | 压力测试 | P1 | 1 天 |

### 1.3 成功标准

**功能完整性**：
- ✅ 节点崩溃后能自动恢复
- ✅ 网络分区后能自动愈合
- ✅ 故障期间数据一致性不破坏

**性能指标**：
- ✅ 决策延迟 < 50ms（3 节点局域网）
- ✅ 吞吐量 > 1000 ops/sec
- ✅ 内存占用 < 100MB per node

**质量标准**：
- ✅ 测试覆盖率 > 85%
- ✅ 所有安全性质测试通过
- ✅ 24 小时稳定性测试通过

---

## 2. TLA+ 验证经验总结

### 2.1 TLA+ 已验证的性质

#### QuorumWithGossipCrash 模型（45,169 状态）

**验证通过的性质**：

```tla
(* 1. 崩溃节点不参与决策 *)
CrashedNodeNoDecision ==
    \A n \in Nodes :
        n \in crashed => decision[n] = "undecided"

(* 2. 活跃节点能达成决策 *)
ActiveNodesCanDecide ==
    \A n \in Nodes :
        n \notin crashed /\ Cardinality(Nodes \ crashed) >= Majority
        => <> (decision[n] # "undecided")

(* 3. 恢复节点最终同步 *)
RecoveryCorrectness ==
    \A n \in Nodes :
        /\ n \in crashed
        => [](n \notin crashed => <>(\A m \in Nodes : knowledge[n].version = knowledge[m].version))
```

**关键发现**：
- ✅ 崩溃节点不会导致决策冲突
- ✅ 多数派节点可以继续决策
- ✅ 恢复节点通过 Gossip 能自动同步

#### QuorumWithGossipPartition 模型（20,903 状态）

**验证通过的性质**：

```tla
(* 1. 分区安全性（防脑裂） *)
PartitionSafety ==
    \A n1, n2 \in Nodes :
        (decision[n1] = "committed" /\ decision[n2] = "committed")
        => \E p \in Nodes :
            n1 \in partitions[p] /\ n2 \in partitions[p]

(* 2. 少数派无法决策 *)
MinorityCannotDecide ==
    LET minority == {n \in Nodes : Cardinality(partitions[n]) < Majority}
    IN  \A n \in minority :
            decision[n] = "undecided"

(* 3. 分区恢复后最终一致 *)
PartitionRecovery ==
    [](network_status = "partitioned" /\
      <> (network_status = "normal" /\
          <>(\A n1, n2 \in Nodes : knowledge[n1].version = knowledge[n2].version)))
```

**关键发现**：
- ✅ Quorum 机制有效防止脑裂
- ✅ 只有多数派分区能做出决策
- ✅ 分区恢复后 Gossip 能自动同步

### 2.2 TLA+ 模型设计经验

#### 经验 1：状态空间控制

**问题**：5 节点模型状态空间爆炸（30M+ 状态，OOM）

**原因**：
- 每个节点有多种状态（undecided/committed，seen/decided 集合）
- 5 节点的组合数是指数级增长
- TLC 模型检查器需要穷举所有状态组合

**解决方案**：
```tla
(* 使用对称性减少状态 *)
CONSTANT Nodes = {n1, n2, n3}  \* 3节点足够验证核心逻辑

(* 使用约束限制状态空间 *)
CONSTRAINT Cardinality(crashed) <= 1  \* 最多1个节点崩溃
```

**经验教训**：
- ✅ 3 节点模型已能验证核心逻辑
- ✅ 5 节点以上应该用 Go 测试，不用 TLA+
- ✅ 使用约束可以显著降低状态空间

#### 经验 2：不变式设计

**常见错误**：不变式太弱，无法捕获所有问题

**示例**：
```tla
(* ❌ 太弱：只检查决策状态 *)
WeakInvariant ==
    \A n \in Nodes :
        decision[n] \in {"undecided", "committed"}

(* ✅ 适中：检查决策和 seen 集合的关系 *)
StrongInvariant ==
    \A n \in Nodes :
        decision[n] = "committed" =>
        Cardinality(knowledge[n].seen) >= Majority
```

**经验教训**：
- ✅ 不变式应该检查状态之间的**关系**，而非单个状态
- ✅ 安全性质通常涉及多个变量的组合约束
- ✅ 每个协议动作都应该保持不变式为真

#### 经验 3：时序性质验证

**TLA+ 的时序算子**：
- `[]P`：总是 P（不变式）
- `<>P`：最终 P（活性）
- `[]<>P`：无限次 P（公平性）
- `P ~> Q`：P 最终导致 Q

**示例**：
```tla
(* 不变式：总是成立 *)
DecisionSafety ==
    \A n \in Nodes :
        decision[n] = "committed" =>
        Cardinality(knowledge[n].seen) >= Majority

(* 活性：最终会达成决策 *)
Liveness ==
    <> (\E n \in Nodes : decision[n] = "committed")

(* 响应性：投票后最终会决策 *)
ProposeDecision ==
    [](\E n \in Nodes : n \in knowledge[n].seen ~> <>(\E n : decision[n] = "committed"))
```

**经验教训**：
- ✅ 不变式用 `[]` 省略（TLC 默认检查）
- ✅ 活性性质用 `<>` 表示最终性
- ✅ 响应性用 `~>` 连接前置条件和后置条件

#### 经验 4：模型检查技巧

**技巧 1：分层验证**
```
第1层：基础模型（QuorumWithGossip.tla）
  └─ 验证基本协议流程

第2层：故障模型（QuorumWithGossipCrash.tla）
  └─ 验证崩溃恢复

第3层：分区模型（QuorumWithGossipPartition.tla）
  └─ 验证网络分区
```

**技巧 2：使用配置文件**
```tla
(* QuorumWithGossip.cfg *)
INIT Init
NEXT Next
INVARIANT TypeOK
INVARIANT DecisionSafety
INVARIANT VersionConsistency
CONSTANTS NULL = NULL
```

**技巧 3：状态空间分析**
```bash
# 查看状态空间统计
grep "States found" QuorumWithGossip.out
grep "Distinct states" QuorumWithGossip.out

# 查看执行时间
grep "Time spent" QuorumWithGossip.out
```

### 2.3 Go 实现必须注意的点

#### 注意点 1：TLA+ 与 Go 的语义差异

**TLA+**：
```tla
(* TLA+ 的变量更新是原子的 *)
DecideCommit(n) ==
    /\ decision[n] = "undecided"
    /\ Cardinality(knowledge[n].seen) >= Majority
    /\ decision' = [decision EXCEPT ![n] = "committed"]
    /\ knowledge' = [knowledge EXCEPT ![n].decided = @ \cup {n}]
```

**Go 实现问题**：
```go
// ❌ 错误：两步操作不是原子的
func (n *Node) DecideCommit(majority int) bool {
    n.Decision = Committed           // 步骤1
    n.Knowledge.Decided[n.ID] = true // 步骤2
    // 如果步骤1和步骤2之间发生崩溃，状态不一致
}

// ✅ 正确：使用锁保证原子性
func (n *Node) DecideCommit(majority int) bool {
    n.mu.Lock()
    defer n.mu.Unlock()

    // 检查前置条件
    if n.Decision != Undecided {
        return false
    }
    if !n.Knowledge.Seen[n.ID] {
        return false
    }
    if len(n.Knowledge.Seen) < majority {
        return false
    }

    // 原子更新
    n.Decision = Committed
    n.Knowledge.Decided[n.ID] = true
    return true
}
```

**经验教训**：
- ✅ TLA+ 假设所有动作都是原子的
- ✅ Go 实现必须用锁保证原子性
- ✅ 检查条件 + 更新状态必须在同一个临界区

#### 注意点 2：并发控制的粒度

**问题**：锁粒度过大导致性能问题

```go
// ❌ 错误：锁粒度过大，阻塞整个 GossipExchange
func (n *Node) GossipExchange(other *Node) {
    n.mu.Lock()
    other.mu.Lock()

    // ... 大量计算 ...

    time.Sleep(100 * time.Millisecond) // ❌ 持锁太久
}
```

**改进**：
```go
// ✅ 正确：缩小锁粒度
func (n *Node) GossipExchange(other *Node) error {
    // 1. 快速读取状态（加锁）
    n.mu.RLock()
    other.mu.RLock()
    myVersion := n.Knowledge.Version
    otherVersion := other.Knowledge.Version
    n.mu.RUnlock()
    other.mu.RUnlock()

    // 版本不匹配，无需交换
    if myVersion != otherVersion {
        return nil
    }

    // 2. 计算新状态（无锁）
    newSeen := mergeMaps(n.Knowledge.Seen, other.Knowledge.Seen)
    newDecided := mergeMaps(n.Knowledge.Decided, other.Knowledge.Decided)

    // 3. 批量写入（加锁）
    n.mu.Lock()
    other.mu.Lock()
    n.Knowledge.Seen = newSeen
    n.Knowledge.Decided = newDecided
    other.Knowledge.Seen = newSeen
    other.Knowledge.Decided = newDecided
    n.mu.Unlock()
    other.mu.Unlock()

    return nil
}
```

**经验教训**：
- ✅ 持锁时间尽量短
- ✅ 计算密集型操作不要持锁
- ✅ 使用读写锁（RLock）优化读多写少场景

#### 注意点 3：错误处理和幂等性

**TLA+ 假设**：动作要么完全成功，要么完全失败（无副作用）

**Go 实现问题**：
```go
// ❌ 错误：非幂等操作
func (n *Node) Crash() error {
    if n.IsCrashed {
        return fmt.Errorf("already crashed") // 重复调用报错
    }
    n.IsCrashed = true
    return nil
}

// ✅ 正确：幂等操作
func (n *Node) Crash() error {
    n.mu.Lock()
    defer n.mu.Unlock()

    // 幂等性：重复调用返回成功（不报错）
    if n.IsCrashed {
        return nil
    }

    n.IsCrashed = true
    n.CrashTime = time.Now()
    return nil
}
```

**经验教训**：
- ✅ 所有公开 API 应该是幂等的
- ✅ 状态变更操作要检查当前状态
- ✅ 网络操作要支持重试

#### 注意点 4：Gossip 协议的收敛性

**TLA+ 假设**：
```tla
(* 假设 Gossip 最终会覆盖所有节点 *)
GossipLiveness ==
    <> (\A p, q \in Nodes : knowledge[p].version = knowledge[q].version)
```

**Go 实现问题**：
```go
// ❌ 错误：固定的 Gossip 次数可能不够
func (c *Cluster) RunGossip() {
    for round := 0; round < 5; round++ { // 为什么是5次？
        c.GossipRound()
    }
    // 5轮后可能还没收敛
}
```

**改进**：
```go
// ✅ 正确：检测收敛性
func (c *Cluster) RunGossip(timeout time.Duration) error {
    startTime := time.Now()
    lastChange := time.Now()

    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    for time.Since(startTime) < timeout {
        select {
        case <-ticker.C:
            // 执行一轮 Gossip
            changed := c.GossipRound()

            // 检查是否收敛
            if !changed {
                if time.Since(lastChange) > 1*time.Second {
                    // 1秒内无变化，认为已收敛
                    return nil
                }
            } else {
                lastChange = time.Now()
            }
        }
    }

    return fmt.Errorf("gossip timeout after %v", timeout)
}
```

**经验教训**：
- ✅ 不要假设固定的 Gossip 轮数
- ✅ 使用收敛检测（连续N轮无变化）
- ✅ 添加超时机制防止无限等待

---

## 3. 任务 1：崩溃恢复实现

### 3.1 目标

实现节点崩溃后的自动恢复机制，确保：
1. 崩溃节点不参与决策
2. 恢复节点能自动同步最新状态
3. 崩溃恢复不影响其他节点

### 3.2 TLA+ 验证依据

**已验证的性质**：
```tla
(* QuorumWithGossipCrash.tla *)

(* 节点崩溃动作 *)
NodeCrash(n) ==
    /\ n \notin crashed
    /\ crashed' = crashed \cup {n}
    /\ UNCHANGED <<knowledge, version, decision>>

(* 节点恢复动作 *)
NodeRecover(n) ==
    /\ n \in crashed
    /\ crashed' = crashed \ {n}
    /\ UNCHANGED <<knowledge, version, decision>>

(* Gossip 约束：崩溃节点不参与 *)
GossipExchange(p, q) ==
    /\ p # q
    /\ p \notin crashed  \* 新增约束
    /\ q \notin crashed
    /\ knowledge[p].version = knowledge[q].version
    /\ LET newSeen == knowledge[p].seen \cup knowledge[q].seen
           newDecided == knowledge[p].decided \cup knowledge[q].decided
       IN  knowledge' = [knowledge EXCEPT
                           ![p].seen = newSeen,
                           ![q].seen = newSeen,
                           ![p].decided = newDecided,
                           ![q].decided = newDecided]
    /\ UNCHANGED <<decision, version, crashed>>
```

**验证结果**：
- ✅ 45,169 状态全部检查通过
- ✅ 崩溃节点不决策
- ✅ 恢复节点最终同步

### 3.3 Go 实现方案

#### 步骤 1：扩展 Node 结构体

**文件**：`implementations/quorum_gossip.go`

```go
// 在现有 Node 结构体基础上添加
type Node struct {
    ID        string
    Knowledge Knowledge
    Decision  DecisionState
    mu        sync.RWMutex

    // ===== 新增字段 =====
    IsCrashed    bool      // 节点是否已崩溃
    CrashTime    time.Time // 崩溃时间
    RecoveredAt  time.Time // 恢复时间
    CrashCount   int       // 崩溃次数（用于监控）
    WAL          *WAL      // 写前日志（持久化）
}

// WAL (Write-Ahead Log) 结构
type WAL struct {
    file       *os.File
    path       string
    mu         sync.Mutex
    encoder    *gob.Encoder
    decoder    *gob.Decoder
}

// WAL 日志条目
type WALEntry struct {
    Timestamp   time.Time
    NodeID      string
    Knowledge   Knowledge
    Decision    DecisionState
    Version     int
}

// 新建 WAL
func NewWAL(dataDir string) (*WAL, error) {
    if err := os.MkdirAll(dataDir, 0755); err != nil {
        return nil, err
    }

    path := filepath.Join(dataDir, "wal.log")
    file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
    if err != nil {
        return nil, err
    }

    return &WAL{
        file:    file,
        path:    path,
        encoder: gob.NewEncoder(file),
        decoder: gob.NewDecoder(file),
    }, nil
}

// 持久化状态到 WAL
func (w *WAL) Append(entry WALEntry) error {
    w.mu.Lock()
    defer w.mu.Unlock()

    if err := w.encoder.Encode(entry); err != nil {
        return fmt.Errorf("failed to encode WAL entry: %w", err)
    }

    // 立即刷盘
    return w.file.Sync()
}

// 从 WAL 恢复状态
func (w *WAL) Recover() ([]WALEntry, error) {
    w.mu.Lock()
    defer w.mu.Unlock()

    // 重置文件指针到开头
    if _, err := w.file.Seek(0, 0); err != nil {
        return nil, err
    }

    var entries []WALEntry
    for {
        var entry WALEntry
        if err := w.decoder.Decode(&entry); err != nil {
            if err == io.EOF {
                break
            }
            return nil, err
        }
        entries = append(entries, entry)
    }

    return entries, nil
}

// 关闭 WAL
func (w *WAL) Close() error {
    return w.file.Close()
}
```

**实现要点**：
- ✅ 使用 WAL 确保崩溃后状态可恢复
- ✅ WAL 采用 append-only 模式（顺序写，性能好）
- ✅ 使用 gob 编码（Go 原生支持）
- ✅ 每次写入后立即刷盘（`file.Sync()`）

#### 步骤 2：实现 Crash 方法

```go
// Crash 模拟节点崩溃
// 前置条件：节点当前未崩溃
// 后置条件：节点标记为崩溃，状态持久化到 WAL
func (n *Node) Crash() error {
    n.mu.Lock()
    defer n.mu.Unlock()

    // 幂等性检查
    if n.IsCrashed {
        return nil // 重复崩溃不报错
    }

    // 1. 持久化当前状态到 WAL
    entry := WALEntry{
        Timestamp: time.Now(),
        NodeID:    n.ID,
        Knowledge: n.Knowledge,
        Decision:  n.Decision,
        Version:   n.Knowledge.Version,
    }

    if err := n.WAL.Append(entry); err != nil {
        return fmt.Errorf("failed to persist state before crash: %w", err)
    }

    // 2. 更新崩溃状态
    n.IsCrashed = true
    n.CrashTime = time.Now()
    n.CrashCount++

    log.Printf("[Node %s] Crashed at %v (count=%d)",
        n.ID, n.CrashTime.Format(time.RFC3339), n.CrashCount)

    return nil
}
```

**实现要点**：
- ✅ 先持久化，再崩溃（WAL 原则）
- ✅ 幂等性：重复调用不报错
- ✅ 记录崩溃时间（用于监控）
- ✅ 线程安全（使用 mu.Lock）

#### 步骤 3：实现 Recover 方法

```go
// Recover 恢复崩溃的节点
// 前置条件：节点当前处于崩溃状态
// 后置条件：节点恢复到之前持久化的状态，并启动增量同步
func (n *Node) Recover(cluster *Cluster) error {
    n.mu.Lock()
    defer n.mu.Unlock()

    // 检查前置条件
    if !n.IsCrashed {
        return fmt.Errorf("node %s is not crashed", n.ID)
    }

    // 1. 从 WAL 恢复状态
    entries, err := n.WAL.Recover()
    if err != nil {
        return fmt.Errorf("failed to recover from WAL: %w", err)
    }

    if len(entries) == 0 {
        return fmt.Errorf("no WAL entries found for node %s", n.ID)
    }

    // 2. 使用最后一个条目恢复状态
    lastEntry := entries[len(entries)-1]
    n.Knowledge = lastEntry.Knowledge
    n.Decision = lastEntry.Decision

    log.Printf("[Node %s] Recovered from WAL (version=%d, decision=%s)",
        n.ID, n.Knowledge.Version, n.Decision)

    // 3. 更新恢复状态
    n.IsCrashed = false
    n.RecoveredAt = time.Now()

    // 4. 启动后台增量同步（在 goroutine 中）
    go n.incrementalSync(cluster)

    return nil
}

// incrementalSync 后台增量同步
func (n *Node) incrementalSync(cluster *Cluster) {
    log.Printf("[Node %s] Starting incremental sync", n.ID)

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    synced := false

    for !synced {
        select {
        case <-ctx.Done():
            log.Printf("[Node %s] Incremental sync timeout", n.ID)
            return

        case <-ticker.C:
            // 尝试与其他节点同步
            changed := false
            for _, other := range cluster.Nodes {
                if other.ID == n.ID || other.IsCrashed {
                    continue
                }

                // 尝试 Gossip 交换
                if err := n.GossipExchange(other); err == nil {
                    changed = true
                }
            }

            // 检查是否已收敛
            if !changed {
                log.Printf("[Node %s] Incremental sync completed", n.ID)
                synced = true
            }
        }
    }
}
```

**实现要点**：
- ✅ 从 WAL 恢复到最后一个状态
- ✅ 启动后台 goroutine 进行增量同步
- ✅ 使用超时防止无限等待
- ✅ 周期性尝试 Gossip 直到收敛

#### 步骤 4：修改 GossipExchange 方法

```go
// GossipExchange 交换知识（修改版）
// 新增：崩溃节点不参与 Gossip
func (n *Node) GossipExchange(other *Node) error {
    // ===== 新增：崩溃节点检查 =====
    if n.IsCrashed {
        return fmt.Errorf("node %s is crashed, cannot gossip", n.ID)
    }
    if other.IsCrashed {
        return fmt.Errorf("peer %s is crashed, cannot gossip", other.ID)
    }

    n.mu.Lock()
    other.mu.Lock()

    // 版本检查
    if n.Knowledge.Version != other.Knowledge.Version {
        other.mu.Unlock()
        n.mu.Unlock()
        return nil
    }

    // 合并 knowledge
    newSeen := mergeMaps(n.Knowledge.Seen, other.Knowledge.Seen)
    newDecided := mergeMaps(n.Knowledge.Decided, other.Knowledge.Decided)

    // 双向更新
    n.Knowledge.Seen = newSeen
    n.Knowledge.Decided = newDecided
    other.Knowledge.Seen = newSeen
    other.Knowledge.Decided = newDecided

    other.mu.Unlock()
    n.mu.Unlock()

    return nil
}
```

**实现要点**：
- ✅ 在方法入口检查崩溃状态
- ✅ 崩溃节点拒绝参与 Gossip
- ✅ 返回错误供调用方处理

#### 步骤 5：修改 DecideCommit 方法

```go
// DecideCommit 决策提交（修改版）
// 新增：崩溃节点不能决策
func (n *Node) DecideCommit(majority int) (bool, error) {
    n.mu.Lock()
    defer n.mu.Unlock()

    // ===== 新增：崩溃节点检查 =====
    if n.IsCrashed {
        return false, fmt.Errorf("node %s is crashed, cannot decide", n.ID)
    }

    // 检查决策状态
    if n.Decision != Undecided {
        return false, nil
    }

    // 检查自投
    if !n.Knowledge.Seen[n.ID] {
        return false, nil
    }

    // 检查多数派
    if len(n.Knowledge.Seen) < majority {
        return false, nil
    }

    // 提交决策
    n.Decision = Committed
    n.Knowledge.Decided[n.ID] = true

    // 持久化到 WAL
    entry := WALEntry{
        Timestamp: time.Now(),
        NodeID:    n.ID,
        Knowledge: n.Knowledge,
        Decision:  n.Decision,
        Version:   n.Knowledge.Version,
    }

    if err := n.WAL.Append(entry); err != nil {
        return false, fmt.Errorf("failed to persist decision: %w", err)
    }

    return true, nil
}
```

**实现要点**：
- ✅ 崩溃节点不能决策
- ✅ 决策后立即持久化到 WAL
- ✅ 返回错误供上层处理

#### 步骤 6：更新 Cluster 结构体

```go
type Cluster struct {
    Nodes    []*Node
    mu       sync.RWMutex
    Majority int
}

// NewCluster 创建集群（修改版）
// 新增：为每个节点初始化 WAL
func NewCluster(nodeIDs []string, dataDir string) *Cluster {
    nodes := make([]*Node, len(nodeIDs))

    for i, id := range nodeIDs {
        // 为每个节点创建独立的 WAL 目录
        walDir := filepath.Join(dataDir, id)
        wal, err := NewWAL(walDir)
        if err != nil {
            log.Printf("Failed to create WAL for node %s: %v", id, err)
            // 继续创建，但 WAL 为 nil
        }

        nodes[i] = &Node{
            ID:     id,
            Knowledge: Knowledge{
                Seen:    make(map[string]bool),
                Version: 0,
                Decided: make(map[string]bool),
            },
            Decision: Undecided,
            WAL:      wal,
        }
    }

    return &Cluster{
        Nodes:    nodes,
        Majority: len(nodeIDs)/2 + 1,
    }
}

// Close 关闭集群资源
func (c *Cluster) Close() error {
    var errs []error

    for _, node := range c.Nodes {
        if node.WAL != nil {
            if err := node.WAL.Close(); err != nil {
                errs = append(errs, err)
            }
        }
    }

    if len(errs) > 0 {
        return fmt.Errorf("errors closing cluster: %v", errs)
    }
    return nil
}
```

### 3.4 测试用例

**文件**：`implementations/quorum_gossip_crash_test.go`

```go
package main

import (
    "os"
    "path/filepath"
    "testing"
    "time"
)

// 辅助函数：创建临时测试目录
func setupTempDir(t *testing.T) string {
    dir := filepath.Join(os.TempDir(), "nexkv-test", t.Name())
    if err := os.RemoveAll(dir); err != nil {
        t.Fatalf("Failed to cleanup temp dir: %v", err)
    }
    if err := os.MkdirAll(dir, 0755); err != nil {
        t.Fatalf("Failed to create temp dir: %v", err)
    }
    return dir
}

// TestTC036_SingleNodeCrash 测试单个节点崩溃恢复
// 对应 TLA+ 验证场景：CrashRecovery
func TestTC036_SingleNodeCrash(t *testing.T) {
    tempDir := setupTempDir(t)
    defer os.RemoveAll(tempDir)

    // 1. 创建 3 节点集群
    cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
    defer cluster.Close()

    // 2. 所有节点发起投票
    for _, node := range cluster.Nodes {
        node.ProposeVote(0)
    }

    // 3. 多轮 Gossip 达到决策
    for round := 0; round < 5; round++ {
        cluster.GossipRound()
    }

    // 4. 所有节点提交
    majority := cluster.GetMajority()
    for _, node := range cluster.Nodes {
        success, err := node.DecideCommit(majority)
        if err != nil {
            t.Fatalf("Failed to commit: %v", err)
        }
        if !success {
            t.Errorf("Expected node %s to commit", node.ID)
        }
    }

    // 5. n1 崩溃
    err := cluster.Nodes[0].Crash()
    if err != nil {
        t.Fatalf("Failed to crash n1: %v", err)
    }

    // 验证：n1 被标记为崩溃
    if !cluster.Nodes[0].IsCrashed {
        t.Error("Expected n1 to be marked as crashed")
    }

    // 验证：n1 不能决策
    _, err = cluster.Nodes[0].DecideCommit(majority)
    if err == nil {
        t.Error("Expected crashed node to fail decision")
    }

    // 6. n1 恢复
    err = cluster.Nodes[0].Recover(cluster)
    if err != nil {
        t.Fatalf("Failed to recover n1: %v", err)
    }

    // 等待增量同步完成
    time.Sleep(3 * time.Second)

    // 验证：n1 恢复正常状态
    if cluster.Nodes[0].IsCrashed {
        t.Error("Expected n1 to be recovered")
    }

    // 验证：n1 的决策状态与其他节点一致
    for _, node := range cluster.Nodes[1:] {
        if cluster.Nodes[0].Decision != node.Decision {
            t.Errorf("Expected n1 decision=%s, got %s",
                node.Decision, cluster.Nodes[0].Decision)
        }
    }
}

// TestTC037_MajorityCrash 测试多数派崩溃
// 对应 TLA+ 性质：ActiveNodesCanDecide
func TestTC037_MajorityCrash(t *testing.T) {
    tempDir := setupTempDir(t)
    defer os.RemoveAll(tempDir)

    // 1. 创建 5 节点集群
    cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
    defer cluster.Close()

    // 2. 多数派（3个节点）崩溃
    for i := 0; i < 3; i++ {
        err := cluster.Nodes[i].Crash()
        if err != nil {
            t.Fatalf("Failed to crash node %s: %v", cluster.Nodes[i].ID, err)
        }
    }

    // 3. 剩余少数派尝试决策
    majority := cluster.GetMajority()

    for i := 3; i < 5; i++ {
        // 发起投票
        cluster.Nodes[i].ProposeVote(0)

        // 尝试决策（应该失败）
        success, err := cluster.Nodes[i].DecideCommit(majority)
        if err != nil {
            t.Fatalf("Unexpected error: %v", err)
        }
        if success {
            t.Errorf("Expected minority node %s to fail decision", cluster.Nodes[i].ID)
        }
    }
}

// TestTC038_CrashDuringGossip 测试 Gossip 期间崩溃
func TestTC038_CrashDuringGossip(t *testing.T) {
    tempDir := setupTempDir(t)
    defer os.RemoveAll(tempDir)

    cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
    defer cluster.Close()

    // 1. 所有节点发起投票
    for _, node := range cluster.Nodes {
        node.ProposeVote(0)
    }

    // 2. 第一轮 Gossip
    cluster.GossipRound()

    // 3. n1 崩溃
    err := cluster.Nodes[0].Crash()
    if err != nil {
        t.Fatalf("Failed to crash n1: %v", err)
    }

    // 4. 尝试与崩溃节点 Gossip（应该失败）
    err = cluster.Nodes[1].GossipExchange(cluster.Nodes[0])
    if err == nil {
        t.Error("Expected gossip with crashed node to fail")
    }

    // 5. n2, n3 继续 Gossip（应该成功）
    err = cluster.Nodes[1].GossipExchange(cluster.Nodes[2])
    if err != nil {
        t.Errorf("Expected gossip between healthy nodes to succeed: %v", err)
    }
}

// TestTC039_CrashRecoveryIdempotent 测试崩溃恢复幂等性
func TestTC039_CrashRecoveryIdempotent(t *testing.T) {
    tempDir := setupTempDir(t)
    defer os.RemoveAll(tempDir)

    cluster := NewCluster([]string{"n1"}, tempDir)
    defer cluster.Close()

    node := cluster.Nodes[0]

    // 1. 重复崩溃（应该不报错）
    err := node.Crash()
    if err != nil {
        t.Fatalf("First crash failed: %v", err)
    }

    err = node.Crash()
    if err != nil {
        t.Errorf("Second crash should be idempotent: %v", err)
    }

    // 2. 恢复
    err = node.Recover(cluster)
    if err != nil {
        t.Fatalf("Recover failed: %v", err)
    }

    // 3. 重复恢复（应该不报错）
    err = node.Recover(cluster)
    if err == nil {
        t.Error("Expected error when recovering non-crashed node")
    }
}

// TestTC040_WALPersistence 测试 WAL 持久化
func TestTC040_WALPersistence(t *testing.T) {
    tempDir := setupTempDir(t)
    defer os.RemoveAll(tempDir)

    cluster := NewCluster([]string{"n1"}, tempDir)

    // 1. 节点发起投票并提交
    node := cluster.Nodes[0]
    node.ProposeVote(0)
    node.DecideCommit(1)

    // 2. 崩溃（会持久化到 WAL）
    err := node.Crash()
    if err != nil {
        t.Fatalf("Crash failed: %v", err)
    }

    // 3. 模拟进程重启：重新加载 WAL
    newCluster := NewCluster([]string{"n1"}, tempDir)
    defer newCluster.Close()

    recoveredNode := newCluster.Nodes[0]

    // 4. 恢复（从 WAL 加载状态）
    err = recoveredNode.Recover(newCluster)
    if err != nil {
        t.Fatalf("Recover failed: %v", err)
    }

    // 5. 验证恢复的状态
    if recoveredNode.Decision != Committed {
        t.Errorf("Expected decision=%s, got %s", Committed, recoveredNode.Decision)
    }

    if !recoveredNode.Knowledge.Seen["n1"] {
        t.Error("Expected n1 to be in seen set")
    }
}
```

### 3.5 验收标准

**功能验收**：
- ✅ 崩溃节点不参与 Gossip 和决策
- ✅ 恢复节点能从 WAL 加载状态
- ✅ 恢复节点通过 Gossip 同步到最新状态
- ✅ Crash 和 Recover 操作是幂等的

**性能验收**：
- ✅ Crash 操作延迟 < 10ms（包括 WAL 写入）
- ✅ Recover 操作延迟 < 50ms（从 WAL 读取）
- ✅ 增量同步收敛时间 < 10s

**测试覆盖率**：
- ✅ 新增代码覆盖率 > 90%
- ✅ 所有测试用例通过
- ✅ 无数据竞争（`go test -race`）

---

## 4. 任务 2：网络分区实现

### 4.1 目标

实现网络分区的检测、处理和恢复机制，确保：
1. 分区期间不会脑裂
2. 只有多数派分区能做出决策
3. 分区恢复后能自动同步

### 4.2 TLA+ 验证依据

**已验证的性质**：
```tla
(* QuorumWithGossipPartition.tla *)

(* 网络分区动作 *)
NetworkPartition(partition1, partition2) ==
    /\ partition1 \cup partition2 = Nodes
    /\ partition1 \cap partition2 = {}
    /\ network_status' = "partitioned"
    /\ partitions' = [n \in partition1 |-> partition2]
    /\ UNCHANGED <<knowledge, version, decision>>

(* 网络愈合动作 *)
NetworkHeal ==
    /\ network_status = "partitioned"
    /\ network_status' = "normal"
    /\ UNCHANGED <<knowledge, version, decision, partitions>>

(* Gossip 约束：只在正常网络或同分区内通信 *)
GossipExchange(p, q) ==
    /\ p # q
    /\ network_status = "normal"  \* 正常网络
       \/ q \in partitions[p]     \* 或在同一分区
    /\ knowledge[p].version = knowledge[q].version
    /\ LET newSeen == knowledge[p].seen \cup knowledge[q].seen
           newDecided == knowledge[p].decided \cup knowledge[q].decided
       IN  knowledge' = [knowledge EXCEPT
                           ![p].seen = newSeen,
                           ![q].seen = newSeen,
                           ![p].decided = newDecided,
                           ![q].decided = newDecided]
    /\ UNCHANGED <<decision, version, network_status, partitions>>
```

**验证结果**：
- ✅ 20,903 状态全部检查通过
- ✅ 分区期间不会脑裂
- ✅ 分区恢复后最终一致

### 4.3 Go 实现方案

#### 步骤 1：扩展 Cluster 结构体

**文件**：`implementations/quorum_gossip.go`

```go
// Cluster 集群（修改版）
type Cluster struct {
    Nodes    []*Node
    mu       sync.RWMutex
    Majority int

    // ===== 新增字段 =====
    NetworkStatus  string                    // "normal" | "partitioned"
    Partitions     map[string][]string       // 分区映射：nodeID -> partitionMembers
    PartitionMap   map[string]string         // 反向映射：nodeID -> partitionID
    HeartbeatMap   map[string]time.Time      // 心跳时间戳
    HeartbeatTimeout time.Duration           // 心跳超时阈值
    lastPartitionCheck time.Time             // 上次分区检查时间
}

// NewCluster 创建集群（修改版）
func NewClusterWithPartition(nodeIDs []string, dataDir string) *Cluster {
    nodes := make([]*Node, len(nodeIDs))

    for i, id := range nodeIDs {
        walDir := filepath.Join(dataDir, id)
        wal, err := NewWAL(walDir)
        if err != nil {
            log.Printf("Failed to create WAL for node %s: %v", id, err)
        }

        nodes[i] = &Node{
            ID:     id,
            Knowledge: Knowledge{
                Seen:    make(map[string]bool),
                Version: 0,
                Decided: make(map[string]bool),
            },
            Decision: Undecided,
            WAL:      wal,
        }
    }

    return &Cluster{
        Nodes:           nodes,
        Majority:        len(nodeIDs)/2 + 1,
        NetworkStatus:   "normal",
        Partitions:      make(map[string][]string),
        PartitionMap:    make(map[string]string),
        HeartbeatMap:    make(map[string]time.Time),
        HeartbeatTimeout: 5 * time.Second,
    }
}
```

#### 步骤 2：实现分区检测

```go
// DetectPartition 检测网络分区
// 原理：通过心跳超时检测节点是否可达
func (c *Cluster) DetectPartition() error {
    c.mu.Lock()
    defer c.mu.Unlock()

    now := time.Now()

    // 1. 检查所有节点的心跳
    var unreachable []string
    for _, node := range c.Nodes {
        if node.IsCrashed {
            continue // 跳过崩溃节点
        }

        lastHeartbeat, ok := c.HeartbeatMap[node.ID]
        if !ok || now.Sub(lastHeartbeat) > c.HeartbeatTimeout {
            unreachable = append(unreachable, node.ID)
        }
    }

    // 2. 如果没有不可达节点，说明网络正常
    if len(unreachable) == 0 {
        if c.NetworkStatus == "partitioned" {
            log.Printf("[Cluster] Network healed")
            c.NetworkStatus = "normal"
            c.Partitions = make(map[string][]string)
            c.PartitionMap = make(map[string]string)
        }
        return nil
    }

    // 3. 如果已经处于分区状态，不重复检测
    if c.NetworkStatus == "partitioned" {
        return nil
    }

    // 4. 检测到新的分区
    log.Printf("[Cluster] Network partition detected: unreachable nodes = %v", unreachable)

    // 5. 构建分区映射
    reachable := make([]string, 0)
    for _, node := range c.Nodes {
        if !contains(unreachable, node.ID) && !node.IsCrashed {
            reachable = append(reachable, node.ID)
        }
    }

    // 6. 更新分区状态
    c.NetworkStatus = "partitioned"

    // 多数派分区
    for _, nodeID := range reachable {
        c.Partitions[nodeID] = reachable
        c.PartitionMap[nodeID] = "majority"
    }

    // 少数派分区
    for _, nodeID := range unreachable {
        c.Partitions[nodeID] = unreachable
        c.PartitionMap[nodeID] = "minority"
    }

    log.Printf("[Cluster] Partition created: majority=%v, minority=%v",
        reachable, unreachable)

    return nil
}

// contains 辅助函数
func contains(slice []string, item string) bool {
    for _, s := range slice {
        if s == item {
            return true
        }
    }
    return false
}
```

**实现要点**：
- ✅ 基于心跳超时检测分区
- ✅ 自动区分多数派和少数派分区
- ✅ 分区恢复后自动愈合
- ✅ 幂等性：重复检测不会重复创建分区

#### 步骤 3：实现分区创建

```go
// CreatePartition 手动创建网络分区（用于测试）
// partition1 和 partition2 必须不相交，且并集为所有节点
func (c *Cluster) CreatePartition(partition1, partition2 []string) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    // 1. 验证分区合法性
    allNodes := make(map[string]bool)
    for _, node := range c.Nodes {
        allNodes[node.ID] = true
    }

    p1Set := make(map[string]bool)
    for _, id := range partition1 {
        if !allNodes[id] {
            return fmt.Errorf("node %s not found in cluster", id)
        }
        p1Set[id] = true
    }

    p2Set := make(map[string]bool)
    for _, id := range partition2 {
        if !allNodes[id] {
            return fmt.Errorf("node %s not found in cluster", id)
        }
        if p1Set[id] {
            return fmt.Errorf("node %s appears in both partitions", id)
        }
        p2Set[id] = true
    }

    // 2. 检查并集是否为全集
    for _, node := range c.Nodes {
        if !p1Set[node.ID] && !p2Set[node.ID] {
            return fmt.Errorf("node %s not in any partition", node.ID)
        }
    }

    // 3. 创建分区
    c.NetworkStatus = "partitioned"
    c.Partitions = make(map[string][]string)

    // partition1
    partitionID1 := "partition1"
    for _, id := range partition1 {
        c.Partitions[id] = partition1
        c.PartitionMap[id] = partitionID1
    }

    // partition2
    partitionID2 := "partition2"
    for _, id := range partition2 {
        c.Partitions[id] = partition2
        c.PartitionMap[id] = partitionID2
    }

    log.Printf("[Cluster] Manual partition created: partition1=%v, partition2=%v",
        partition1, partition2)

    return nil
}
```

**实现要点**：
- ✅ 验证分区合法性（不相交、并集为全集）
- ✅ 支持手动创建分区（用于测试）
- ✅ 记录分区映射关系

#### 步骤 4：实现分区愈合

```go
// HealPartition 治愈网络分区
// 后置条件：网络恢复正常，触发全集群 Gossip
func (c *Cluster) HealPartition() error {
    c.mu.Lock()
    defer c.mu.Unlock()

    // 检查前置条件
    if c.NetworkStatus != "partitioned" {
        return fmt.Errorf("network is not partitioned")
    }

    log.Printf("[Cluster] Healing partition...")

    // 1. 重置网络状态
    c.NetworkStatus = "normal"
    c.Partitions = make(map[string][]string)
    c.PartitionMap = make(map[string]string)

    // 2. 重置心跳时间戳（避免误判）
    now := time.Now()
    for _, node := range c.Nodes {
        if !node.IsCrashed {
            c.HeartbeatMap[node.ID] = now
        }
    }

    // 3. 触发全集群 Gossip（在后台）
    go c.triggerGlobalGossip()

    log.Printf("[Cluster] Partition healed")

    return nil
}

// triggerGlobalGossip 触发全局 Gossip 同步
func (c *Cluster) triggerGlobalGossip() {
    log.Printf("[Cluster] Starting global gossip sync...")

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()

    round := 0
    for {
        select {
        case <-ctx.Done():
            log.Printf("[Cluster] Global gossip timeout after %d rounds", round)
            return

        case <-ticker.C:
            round++
            changed := c.GossipRound()

            if !changed {
                // 已收敛
                log.Printf("[Cluster] Global gossip converged after %d rounds", round)
                return
            }
        }
    }
}
```

**实现要点**：
- ✅ 重置网络状态为 normal
- ✅ 触发全局 Gossip 同步
- ✅ 使用超时防止无限等待
- ✅ 检测收敛（连续无变化）

#### 步骤 5：修改 GossipExchange 方法

```go
// GossipExchange 交换知识（修改版）
// 新增：只允许正常网络或同分区节点通信
func (n *Node) GossipExchange(other *Node, cluster *Cluster) error {
    n.mu.Lock()
    other.mu.Lock()

    // ===== 新增：崩溃节点检查 =====
    if n.IsCrashed {
        other.mu.Unlock()
        n.mu.Unlock()
        return fmt.Errorf("node %s is crashed", n.ID)
    }
    if other.IsCrashed {
        other.mu.Unlock()
        n.mu.Unlock()
        return fmt.Errorf("node %s is crashed", other.ID)
    }

    // ===== 新增：分区检查 =====
    cluster.mu.RLock()
    canCommunicate := false

    if cluster.NetworkStatus == "normal" {
        // 正常网络：所有节点可以通信
        canCommunicate = true
    } else {
        // 分区网络：只允许同分区节点通信
        nPartition, ok1 := cluster.PartitionMap[n.ID]
        otherPartition, ok2 := cluster.PartitionMap[other.ID]

        if ok1 && ok2 && nPartition == otherPartition {
            // 同一分区
            canCommunicate = true
        } else {
            // 不同分区
            canCommunicate = false
        }
    }
    cluster.mu.RUnlock()

    if !canCommunicate {
        other.mu.Unlock()
        n.mu.Unlock()
        return fmt.Errorf("nodes %s and %s are in different partitions", n.ID, other.ID)
    }

    // 版本检查
    if n.Knowledge.Version != other.Knowledge.Version {
        other.mu.Unlock()
        n.mu.Unlock()
        return nil
    }

    // 合并 knowledge
    newSeen := mergeMaps(n.Knowledge.Seen, other.Knowledge.Seen)
    newDecided := mergeMaps(n.Knowledge.Decided, other.Knowledge.Decided)

    // 双向更新
    n.Knowledge.Seen = newSeen
    n.Knowledge.Decided = newDecided
    other.Knowledge.Seen = newSeen
    other.Knowledge.Decided = newDecided

    other.mu.Unlock()
    n.mu.Unlock()

    return nil
}
```

**实现要点**：
- ✅ 检查网络状态（normal/partitioned）
- ✅ 分区状态下只允许同分区通信
- ✅ 拒绝跨分区 Gossip

#### 步骤 6：修改 GossipRound 方法

```go
// GossipRound 执行一轮 Gossip（修改版）
// 新增：考虑分区状态，只允许同分区节点通信
func (c *Cluster) GossipRound() bool {
    c.mu.RLock()
    networkStatus := c.NetworkStatus
    partitions := make(map[string][]string)
    for k, v := range c.Partitions {
        partitions[k] = v
    }
    c.mu.RUnlock()

    changed := false

    if networkStatus == "normal" {
        // 正常网络：所有节点可以通信
        for i := 0; i < len(c.Nodes); i++ {
            for j := i + 1; j < len(c.Nodes); j++ {
                err := c.Nodes[i].GossipExchange(c.Nodes[j], c)
                if err == nil {
                    changed = true
                }
            }
        }
    } else {
        // 分区网络：只允许同分区节点通信
        // 按分区分组
        partitionGroups := make(map[string][]*Node)
        for _, node := range c.Nodes {
            if node.IsCrashed {
                continue
            }

            partitionID, ok := partitions[node.ID]
            if !ok {
                continue
            }

            partitionGroups[partitionID] = append(partitionGroups[partitionID], node)
        }

        // 在每个分组内执行 Gossip
        for _, nodes := range partitionGroups {
            for i := 0; i < len(nodes); i++ {
                for j := i + 1; j < len(nodes); j++ {
                    err := nodes[i].GossipExchange(nodes[j], c)
                    if err == nil {
                        changed = true
                    }
                }
            }
        }
    }

    return changed
}
```

**实现要点**：
- ✅ 正常网络：全局 Gossip
- ✅ 分区网络：每个分组独立 Gossip
- ✅ 跨分区通信被阻止

#### 步骤 7：实现心跳机制

```go
// SendHeartbeat 发送心跳（用于分区检测）
func (n *Node) SendHeartbeat(cluster *Cluster) error {
    if n.IsCrashed {
        return fmt.Errorf("node %s is crashed", n.ID)
    }

    cluster.mu.Lock()
    defer cluster.mu.Unlock()

    cluster.HeartbeatMap[n.ID] = time.Now()
    return nil
}

// StartHeartbeat 启动心跳发送器
func (n *Node) StartHeartbeat(cluster *Cluster, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            if err := n.SendHeartbeat(cluster); err != nil {
                log.Printf("[Node %s] Failed to send heartbeat: %v", n.ID, err)
            }
        }
    }
}
```

### 4.4 测试用例

**文件**：`implementations/quorum_gossip_partition_test.go`

```go
package main

import (
    "os"
    "path/filepath"
    "testing"
    "time"
)

// TestTC041_Partition3vs2 测试 3 vs 2 分区
// 对应 TLA+ 验证场景：多数派 vs 少数派分区
func TestTC041_Partition3vs2(t *testing.T) {
    tempDir := setupTempDir(t)
    defer os.RemoveAll(tempDir)

    // 1. 创建 5 节点集群
    cluster := NewClusterWithPartition([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
    defer cluster.Close()

    // 2. 创建分区：{n1,n2,n3} vs {n4,n5}
    err := cluster.CreatePartition(
        []string{"n1", "n2", "n3"}, // 多数派
        []string{"n4", "n5"},       // 少数派
    )
    if err != nil {
        t.Fatalf("Failed to create partition: %v", err)
    }

    // 3. 多数派分区发起投票
    for i := 0; i < 3; i++ {
        cluster.Nodes[i].ProposeVote(0)
    }

    // 4. 多数派分区 Gossip（应该成功）
    for round := 0; round < 5; round++ {
        cluster.GossipRound()
    }

    // 5. 多数派节点提交（应该成功）
    majority := cluster.GetMajority()
    for i := 0; i < 3; i++ {
        success, err := cluster.Nodes[i].DecideCommit(majority)
        if err != nil {
            t.Fatalf("Failed to commit: %v", err)
        }
        if !success {
            t.Errorf("Expected majority node %s to commit", cluster.Nodes[i].ID)
        }
    }

    // 6. 少数派节点尝试提交（应该失败）
    for i := 3; i < 5; i++ {
        success, _ := cluster.Nodes[i].DecideCommit(majority)
        if success {
            t.Errorf("Expected minority node %s to fail commit", cluster.Nodes[i].ID)
        }
    }
}

// TestTC042_PartitionHealing 测试分区愈合
// 对应 TLA+ 性质：PartitionRecovery
func TestTC042_PartitionHealing(t *testing.T) {
    tempDir := setupTempDir(t)
    defer os.RemoveAll(tempDir)

    // 1. 创建 5 节点集群
    cluster := NewClusterWithPartition([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
    defer cluster.Close()

    // 2. 多数派达成决策
    for i := 0; i < 3; i++ {
        cluster.Nodes[i].ProposeVote(0)
    }

    for round := 0; round < 5; round++ {
        cluster.GossipRound()
    }

    majority := cluster.GetMajority()
    for i := 0; i < 3; i++ {
        cluster.Nodes[i].DecideCommit(majority)
    }

    // 3. 创建分区
    err := cluster.CreatePartition(
        []string{"n1", "n2", "n3"},
        []string{"n4", "n5"},
    )
    if err != nil {
        t.Fatalf("Failed to create partition: %v", err)
    }

    // 4. 愈合分区
    err = cluster.HealPartition()
    if err != nil {
        t.Fatalf("Failed to heal partition: %v", err)
    }

    // 5. 等待全局 Gossip 完成
    time.Sleep(3 * time.Second)

    // 6. 验证所有节点状态一致
    for i := 3; i < 5; i++ {
        if cluster.Nodes[i].Decision != cluster.Nodes[0].Decision {
            t.Errorf("Expected node %s decision=%s, got %s",
                cluster.Nodes[i].ID,
                cluster.Nodes[0].Decision,
                cluster.Nodes[i].Decision)
        }
    }
}

// TestTC043_CrossPartitionGossipBlocked 测试跨分区 Gossip 被阻止
func TestTC043_CrossPartitionGossipBlocked(t *testing.T) {
    tempDir := setupTempDir(t)
    defer os.RemoveAll(tempDir)

    cluster := NewClusterWithPartition([]string{"n1", "n2", "n3"}, tempDir)
    defer cluster.Close()

    // 1. 创建分区：{n1,n2} vs {n3}
    err := cluster.CreatePartition(
        []string{"n1", "n2"},
        []string{"n3"},
    )
    if err != nil {
        t.Fatalf("Failed to create partition: %v", err)
    }

    // 2. 尝试跨分区 Gossip（应该失败）
    err = cluster.Nodes[0].GossipExchange(cluster.Nodes[2], cluster)
    if err == nil {
        t.Error("Expected cross-partition gossip to fail")
    }

    // 3. 同分区 Gossip（应该成功）
    err = cluster.Nodes[0].GossipExchange(cluster.Nodes[1], cluster)
    if err != nil {
        t.Errorf("Expected same-partition gossip to succeed: %v", err)
    }
}

// TestTC044_AutoPartitionDetection 测试自动分区检测
func TestTC044_AutoPartitionDetection(t *testing.T) {
    tempDir := setupTempDir(t)
    defer os.RemoveAll(tempDir)

    cluster := NewClusterWithPartition([]string{"n1", "n2", "n3"}, tempDir)
    defer cluster.Close()

    // 1. 发送心跳
    for _, node := range cluster.Nodes {
        node.SendHeartbeat(cluster)
    }

    // 2. 检测分区（应该正常）
    err := cluster.DetectPartition()
    if err != nil {
        t.Fatalf("Failed to detect partition: %v", err)
    }

    if cluster.NetworkStatus != "normal" {
        t.Errorf("Expected network status=normal, got %s", cluster.NetworkStatus)
    }

    // 3. 模拟 n3 超时（不发送心跳）
    time.Sleep(6 * time.Second) // 超过 HeartbeatTimeout (5s)

    // 4. 再次检测分区（应该发现分区）
    err = cluster.DetectPartition()
    if err != nil {
        t.Fatalf("Failed to detect partition: %v", err)
    }

    if cluster.NetworkStatus != "partitioned" {
        t.Errorf("Expected network status=partitioned, got %s", cluster.NetworkStatus)
    }

    // 5. 验证分区映射
    n3Partition := cluster.PartitionMap["n3"]
    if n3Partition != "minority" {
        t.Errorf("Expected n3 to be in minority partition, got %s", n3Partition)
    }
}

// TestTC045_MultiplePartitions 测试多次分区
func TestTC045_MultiplePartitions(t *testing.T) {
    tempDir := setupTempDir(t)
    defer os.RemoveAll(tempDir)

    cluster := NewClusterWithPartition([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
    defer cluster.Close()

    // 第一次分区：{n1,n2} vs {n3,n4,n5}
    err := cluster.CreatePartition(
        []string{"n1", "n2"},
        []string{"n3", "n4", "n5"},
    )
    if err != nil {
        t.Fatalf("Failed to create first partition: %v", err)
    }

    // 愈合
    err = cluster.HealPartition()
    if err != nil {
        t.Fatalf("Failed to heal first partition: %v", err)
    }

    time.Sleep(1 * time.Second)

    // 第二次分区：{n1,n2,n3} vs {n4,n5}
    err = cluster.CreatePartition(
        []string{"n1", "n2", "n3"},
        []string{"n4", "n5"},
    )
    if err != nil {
        t.Fatalf("Failed to create second partition: %v", err)
    }

    // 验证第二次分区正常工作
    if cluster.NetworkStatus != "partitioned" {
        t.Error("Expected network to be partitioned")
    }

    // 愈合
    err = cluster.HealPartition()
    if err != nil {
        t.Fatalf("Failed to heal second partition: %v", err)
    }
}
```

### 4.5 验收标准

**功能验收**：
- ✅ 分区期间跨分区通信被阻止
- ✅ 只有多数派分区能做出决策
- ✅ 分区愈合后自动同步
- ✅ 支持多次分区和愈合

**性能验收**：
- ✅ 分区检测延迟 < 6s（心跳超时 + 检测间隔）
- ✅ 分区愈合收敛时间 < 10s
- ✅ Gossip 性能不受影响

**测试覆盖率**：
- ✅ 新增代码覆盖率 > 85%
- ✅ 所有测试用例通过
- ✅ 无死锁（`go test -deadlock`）

---

## 5. 任务 3：性能基准测试

### 5.1 目标

建立性能基准数据，为后续优化提供依据：
- 测量决策延迟
- 测量吞吐量
- 测量资源占用
- 测量可扩展性

### 5.2 基准测试方案

#### 基准 1：决策延迟

**文件**：`implementations/quorum_gossip_bench_test.go`

```go
package main

import (
    "os"
    "path/filepath"
    "testing"
    "time"
)

// BenchmarkDecisionLatency_3Nodes 测试 3 节点决策延迟
func BenchmarkDecisionLatency_3Nodes(b *testing.B) {
    tempDir := filepath.Join(os.TempDir(), "nexkv-bench")
    defer os.RemoveAll(tempDir)

    cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
    defer cluster.Close()

    majority := cluster.GetMajority()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        b.StopTimer()

        // 重置状态
        for _, node := range cluster.Nodes {
            node.Knowledge = Knowledge{
                Seen:    make(map[string]bool),
                Version: 0,
                Decided: make(map[string]bool),
            }
            node.Decision = Undecided
        }

        // 所有节点发起投票
        for _, node := range cluster.Nodes {
            node.ProposeVote(0)
        }

        b.StartTimer()

        // 执行 Gossip 直到决策
        for round := 0; round < 10; round++ {
            cluster.GossipRound()

            // 尝试决策
            committed := 0
            for _, node := range cluster.Nodes {
                if success, _ := node.DecideCommit(majority); success {
                    committed++
                }
            }

            if committed == len(cluster.Nodes) {
                break
            }
        }
    }
}

// BenchmarkDecisionLatency_5Nodes 测试 5 节点决策延迟
func BenchmarkDecisionLatency_5Nodes(b *testing.B) {
    tempDir := filepath.Join(os.TempDir(), "nexkv-bench")
    defer os.RemoveAll(tempDir)

    cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
    defer cluster.Close()

    majority := cluster.GetMajority()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        b.StopTimer()

        // 重置状态
        for _, node := range cluster.Nodes {
            node.Knowledge = Knowledge{
                Seen:    make(map[string]bool),
                Version: 0,
                Decided: make(map[string]bool),
            }
            node.Decision = Undecided
        }

        // 所有节点发起投票
        for _, node := range cluster.Nodes {
            node.ProposeVote(0)
        }

        b.StartTimer()

        // 执行 Gossip 直到决策
        for round := 0; round < 15; round++ {
            cluster.GossipRound()

            // 尝试决策
            committed := 0
            for _, node := range cluster.Nodes {
                if success, _ := node.DecideCommit(majority); success {
                    committed++
                }
            }

            if committed == len(cluster.Nodes) {
                break
            }
        }
    }
}

// BenchmarkGossipRound 测试单轮 Gossip 性能
func BenchmarkGossipRound_3Nodes(b *testing.B) {
    tempDir := filepath.Join(os.TempDir(), "nexkv-bench")
    defer os.RemoveAll(tempDir)

    cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
    defer cluster.Close()

    // 预热
    for _, node := range cluster.Nodes {
        node.ProposeVote(0)
    }
    cluster.GossipRound()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        cluster.GossipRound()
    }
}

// BenchmarkGossipRound_7Nodes 测试 7 节点 Gossip 性能
func BenchmarkGossipRound_7Nodes(b *testing.B) {
    tempDir := filepath.Join(os.TempDir(), "nexkv-bench")
    defer os.RemoveAll(tempDir)

    nodeIDs := []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7"}
    cluster := NewCluster(nodeIDs, tempDir)
    defer cluster.Close()

    // 预热
    for _, node := range cluster.Nodes {
        node.ProposeVote(0)
    }
    cluster.GossipRound()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        cluster.GossipRound()
    }
}
```

#### 基准 2：吞吐量测试

```go
// BenchmarkThroughput_3Nodes 测试 3 节点吞吐量
func BenchmarkThroughput_3Nodes(b *testing.B) {
    tempDir := filepath.Join(os.TempDir(), "nexkv-bench")
    defer os.RemoveAll(tempDir)

    cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
    defer cluster.Close()

    majority := cluster.GetMajority()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        // 发起投票
        for _, node := range cluster.Nodes {
            node.ProposeVote(0)
        }

        // Gossip
        cluster.GossipRound()

        // 决策
        for _, node := range cluster.Nodes {
            node.DecideCommit(majority)
        }

        // 重置（模拟下一轮）
        for _, node := range cluster.Nodes {
            node.Knowledge.Version++
            node.Knowledge.Seen = make(map[string]bool)
            node.Knowledge.Decided = make(map[string]bool)
            node.Decision = Undecided
        }
    }
}
```

#### 基准 3：内存占用

```go
// BenchmarkMemory_3Nodes 测试 3 节点内存占用
func BenchmarkMemory_3Nodes(b *testing.B) {
    tempDir := filepath.Join(os.TempDir(), "nexkv-bench")
    defer os.RemoveAll(tempDir)

    var m1, m2 runtime.MemStats

    b.Run("Before", func(b *testing.B) {
        runtime.ReadMemStats(&m1)
    })

    cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)

    b.Run("After", func(b *testing.B) {
        runtime.ReadMemStats(&m2)
    })

    b.ReportMetric(float64(m2.TotalAlloc-m1.TotalAlloc), "bytes")
}

// BenchmarkMemory_10Nodes 测试 10 节点内存占用
func BenchmarkMemory_10Nodes(b *testing.B) {
    tempDir := filepath.Join(os.TempDir(), "nexkv-bench")
    defer os.RemoveAll(tempDir)

    var m1, m2 runtime.MemStats

    b.Run("Before", func(b *testing.B) {
        runtime.ReadMemStats(&m1)
    })

    nodeIDs := make([]string, 10)
    for i := 0; i < 10; i++ {
        nodeIDs[i] = fmt.Sprintf("n%d", i+1)
    }
    cluster := NewCluster(nodeIDs, tempDir)

    b.Run("After", func(b *testing.B) {
        runtime.ReadMemStats(&m2)
    })

    b.ReportMetric(float64(m2.TotalAlloc-m1.TotalAlloc), "bytes")
}
```

#### 基准 4：可扩展性测试

```go
// BenchmarkScalability 测试不同节点数的性能
func BenchmarkScalability(b *testing.B) {
    nodeCounts := []int{3, 5, 7, 10}

    for _, count := range nodeCounts {
        b.Run(fmt.Sprintf("%dNodes", count), func(b *testing.B) {
            tempDir := filepath.Join(os.TempDir(), "nexkv-bench")
            defer os.RemoveAll(tempDir)

            nodeIDs := make([]string, count)
            for i := 0; i < count; i++ {
                nodeIDs[i] = fmt.Sprintf("n%d", i+1)
            }

            cluster := NewCluster(nodeIDs, tempDir)
            defer cluster.Close()

            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                cluster.GossipRound()
            }
        })
    }
}
```

### 5.3 运行基准测试

```bash
# 运行所有基准测试
cd implementations
go test -bench=. -benchmem -benchtime=10s > ../benchmarks/results.txt

# 运行特定基准测试
go test -bench=BenchmarkDecisionLatency -benchmem

# CPU 性能分析
go test -bench=. -cpuprofile=cpu.prof
go tool pprof cpu.prof

# 内存性能分析
go test -bench=. -memprofile=mem.prof
go tool pprof mem.prof
```

### 5.4 性能目标

| 指标 | 3节点 | 5节点 | 7节点 | 10节点 |
|------|-------|-------|-------|--------|
| 决策延迟 | < 50ms | < 100ms | < 150ms | < 200ms |
| 吞吐量 | > 1000 ops/s | > 800 ops/s | > 600 ops/s | > 400 ops/s |
| 内存占用 | < 50MB | < 80MB | < 120MB | < 200MB |
| Gossip 延迟 | < 5ms | < 10ms | < 20ms | < 30ms |

### 5.5 验收标准

- ✅ 所有基准测试运行成功
- ✅ 性能指标达到目标
- ✅ 生成性能报告文档
- ✅ 识别性能瓶颈

---

## 6. Go 实现通用原则

### 6.1 并发控制

#### 原则 1：锁的粒度

**好的实践**：
```go
// ✅ 锁粒度适中
func (n *Node) DecideCommit(majority int) (bool, error) {
    n.mu.Lock()
    defer n.mu.Unlock()

    // 检查 + 更新在同一个临界区
    if n.Decision != Undecided {
        return false, nil
    }
    n.Decision = Committed
    return true, nil
}
```

**坏的实践**：
```go
// ❌ 锁粒度过大
func (n *Node) DecideCommit(majority int) (bool, error) {
    n.mu.Lock()

    // ... 大量计算 ...

    time.Sleep(100 * time.Millisecond) // ❌ 持锁太久

    n.mu.Unlock()
}
```

#### 原则 2：读写分离

```go
// ✅ 读操作使用读锁
func (n *Node) GetState() (DecisionState, int, map[string]bool, map[string]bool) {
    n.mu.RLock()
    defer n.mu.RUnlock()

    return n.Decision, n.Knowledge.Version, n.Knowledge.Seen, n.Knowledge.Decided
}

// ✅ 写操作使用写锁
func (n *Node) ProposeVote(version int) bool {
    n.mu.Lock()
    defer n.mu.Unlock()
    // ...
}
```

#### 原则 3：避免死锁

```go
// ❌ 错误：可能导致死锁
func (n *Node) GossipExchange(other *Node) {
    n.mu.Lock()
    other.mu.Lock()
    // 如果两个 goroutine 同时调用，会死锁
}

// ✅ 正确：按固定顺序加锁
func (n *Node) GossipExchange(other *Node) {
    // 总是按 ID 顺序加锁
    if n.ID < other.ID {
        n.mu.Lock()
        other.mu.Lock()
    } else {
        other.mu.Lock()
        n.mu.Lock()
    }
    defer other.mu.Unlock()
    defer n.mu.Unlock()
    // ...
}
```

### 6.2 错误处理

#### 原则 1：明确的错误信息

```go
// ❌ 错误：错误信息不明确
return errors.New("error")

// ✅ 正确：包含上下文
return fmt.Errorf("node %s failed to commit: majority check failed (seen=%d, required=%d)",
    n.ID, len(n.Knowledge.Seen), majority)
```

#### 原则 2：错误传播

```go
// ❌ 错误：忽略错误
func (c *Cluster) GossipRound() {
    for _, node := range c.Nodes {
        node.ProposeVote(0) // 忽略返回值
    }
}

// ✅ 正确：处理或传播错误
func (c *Cluster) GossipRound() error {
    for _, node := range c.Nodes {
        if err := node.ProposeVote(0); err != nil {
            return fmt.Errorf("failed to propose vote for node %s: %w", node.ID, err)
        }
    }
    return nil
}
```

#### 原则 3：幂等性

```go
// ✅ 幂等操作：重复调用不报错
func (n *Node) Crash() error {
    n.mu.Lock()
    defer n.mu.Unlock()

    if n.IsCrashed {
        return nil // 重复调用返回成功
    }

    n.IsCrashed = true
    return nil
}
```

### 6.3 资源管理

#### 原则 1：使用 defer 释放资源

```go
// ✅ 正确：使用 defer
func (c *Cluster) Close() error {
    var errs []error

    for _, node := range c.Nodes {
        if node.WAL != nil {
            if err := node.WAL.Close(); err != nil {
                errs = append(errs, err)
            }
        }
    }

    if len(errs) > 0 {
        return fmt.Errorf("errors closing cluster: %v", errs)
    }
    return nil
}
```

#### 原则 2：避免资源泄漏

```go
// ❌ 错误：goroutine 泄漏
func (n *Node) incrementalSync(cluster *Cluster) {
    for {
        // 无限循环，没有退出条件
        n.syncWithPeers(cluster)
        time.Sleep(1 * time.Second)
    }
}

// ✅ 正确：使用 context 控制
func (n *Node) incrementalSync(cluster *Cluster) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if !n.syncWithPeers(cluster) {
                return // 已收敛，退出
            }
        }
    }
}
```

### 6.4 测试原则

#### 原则 1：测试独立性

```go
// ✅ 每个测试独立
func TestSingleNodeCrash(t *testing.T) {
    tempDir := setupTempDir(t) // 每个测试独立的临时目录
    defer os.RemoveAll(tempDir)

    cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
    defer cluster.Close()

    // 测试逻辑...
}
```

#### 原则 2：表驱动测试

```go
// ✅ 使用表驱动测试多个场景
func TestMajorityCalculation(t *testing.T) {
    tests := []struct {
        name         string
        nodeCount    int
        expected     int
    }{
        {"3Nodes", 3, 2},
        {"5Nodes", 5, 3},
        {"7Nodes", 7, 4},
        {"10Nodes", 10, 6},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            cluster := NewCluster(make([]string, tt.nodeCount), "")
            if cluster.Majority != tt.expected {
                t.Errorf("expected majority=%d, got %d", tt.expected, cluster.Majority)
            }
        })
    }
}
```

#### 原则 3：并发测试

```go
// ✅ 使用 -race 标志检测数据竞争
// go test -race ./...

// ✅ 并发测试
func TestConcurrentGossip(t *testing.T) {
    cluster := NewCluster([]string{"n1", "n2", "n3"}, "")
    defer cluster.Close()

    // 并发 Gossip
    var wg sync.WaitGroup
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            cluster.GossipRound()
        }()
    }

    wg.Wait()
    // 验证没有数据竞争
}
```

---

## 7. 验收标准

### 7.1 功能验收

| 功能 | 验收标准 |
|------|---------|
| 崩溃恢复 | ✅ 节点崩溃后不参与决策<br/>✅ 恢复节点能从 WAL 加载状态<br/>✅ 恢复节点通过 Gossip 同步到最新状态<br/>✅ Crash/Recover 操作幂等 |
| 网络分区 | ✅ 分区期间跨分区通信被阻止<br/>✅ 只有多数派分区能决策<br/>✅ 分区愈合后自动同步<br/>✅ 支持多次分区和愈合 |
| 性能基准 | ✅ 决策延迟 < 50ms（3节点）<br/>✅ 吞吐量 > 1000 ops/s<br/>✅ 内存占用 < 100MB per node |

### 7.2 质量验收

| 指标 | 验收标准 |
|------|---------|
| 测试覆盖率 | > 85% |
| 单元测试 | 所有测试通过 |
| 数据竞争 | `go test -race` 无警告 |
| 代码风格 | `go vet` 无警告 |
| 文档完整性 | 所有公开 API 有注释 |

### 7.3 性能验收

运行基准测试并生成报告：

```bash
# 运行基准测试
cd implementations
go test -bench=. -benchmem -benchtime=10s > ../benchmarks/phase3-results.txt

# 生成性能报告
go test -bench=. -cpuprofile=cpu.prof -memprofile=mem.prof
go tool pprof -text cpu.prof > ../benchmarks/cpu-profile.txt
go tool pprof -text mem.prof > ../benchmarks/memory-profile.txt
```

---

## 8. 风险与应对

### 8.1 技术风险

| 风险 | 概率 | 影响 | 应对措施 |
|------|------|------|---------|
| WAL 性能问题 | 中 | 高 | 使用 batch 写入，异步刷盘 |
| 分区检测误报 | 中 | 中 | 引入超时容错机制 |
| 死锁 | 低 | 高 | 代码审查 + `-race` 测试 |
| 内存泄漏 | 低 | 中 | 使用 pprof 定期检查 |

### 8.2 进度风险

| 风险 | 概率 | 影响 | 应对措施 |
|------|------|------|---------|
| 工期延误 | 中 | 中 | 分阶段交付，优先 P0 任务 |
| 测试覆盖不足 | 低 | 高 | 强制代码审查，覆盖率门禁 |
| 性能不达标 | 中 | 高 | 预留性能优化时间（1周） |

---

## 9. 附录

### 9.1 检查清单

#### 崩溃恢复实现检查清单

- [ ] Node 结构体添加崩溃相关字段
- [ ] 实现 WAL 结构和接口
- [ ] 实现 Crash 方法
- [ ] 实现 Recover 方法
- [ ] 实现 incrementalSync 方法
- [ ] 修改 GossipExchange 检查崩溃状态
- [ ] 修改 DecideCommit 检查崩溃状态
- [ ] 编写单元测试（5 个测试用例）
- [ ] 运行 `go test -race` 检查数据竞争
- [ ] 测试覆盖率 > 90%

#### 网络分区实现检查清单

- [ ] Cluster 结构体添加分区相关字段
- [ ] 实现 DetectPartition 方法
- [ ] 实现 CreatePartition 方法
- [ ] 实现 HealPartition 方法
- [ ] 实现 triggerGlobalGossip 方法
- [ ] 修改 GossipExchange 检查分区状态
- [ ] 修改 GossipRound 支持分组 Gossip
- [ ] 实现心跳机制
- [ ] 编写单元测试（5 个测试用例）
- [ ] 测试覆盖率 > 85%

#### 性能测试检查清单

- [ ] 实现决策延迟基准测试
- [ ] 实现吞吐量基准测试
- [ ] 实现内存占用基准测试
- [ ] 实现可扩展性基准测试
- [ ] 运行基准测试生成报告
- [ ] 分析性能瓶颈
- [ ] 性能指标达到目标
- [ ] 编写性能报告文档

### 9.2 参考资料

**TLA+ 相关**：
- [TLA+ 官方文档](https://lamport.azurewebsites.net/tla/tla.html)
- [Specifying Systems](https://lamport.azurewebsites.net/tla/book.html)（TLA+ 入门书籍）
- [TLC 模型检查器手册](https://tla.msr-inria.inria.fr/tlatoolbox/doc/model/model.html)

**Go 并发编程**：
- [Go 并发模式](https://go.dev/doc/effective_go#concurrency)
- [Sync 包文档](https://pkg.go.dev/sync)
- [Context 包文档](https://pkg.go.dev/context)

**分布式系统**：
- [Raft 论文](https://raft.github.io/)
- [Paxos Made Simple](https://www.microsoft.com/en-us/research/wp-content/uploads/2016/12/paxos-simple-Copy.pdf)
- [CAP 定理](https://www.ibm.com/topics/cap-theorem)

---

**文档版本**：v1.0
**创建日期**：2026-01-14
**最后更新**：2026-01-14
**维护者**：NexKV 开发团队
