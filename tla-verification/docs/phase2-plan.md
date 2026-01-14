# NexKV TLA+ 验证 - Phase 2

> **状态**：🚀 进行中（2026-01-14 ~ 2026-01-31）
> **目标**：扩展模型规模、添加故障注入、验证 Liveness、Go 实现
> **前置**：Phase 1 成功完成（10 个性质全部通过）

---

## 📋 Phase 2 任务清单

### Week 1：模型扩展（2026-01-14 ~ 2026-01-20）

| 任务 | 描述 | 状态 | 产出 |
|------|------|------|------|
| T2.1 | 扩展 QuorumWithGossip 到 5 节点 | ⏳ | QuorumWithGossip5Nodes.tla |
| T2.2 | 扩展 TwoPhaseCommit 到 5 节点 | ⏳ | TwoPhaseCommit5Nodes.tla |
| T2.3 | 创建故障注入模型（节点崩溃） | ⏳ | CrashRecovery.tla |
| T2.4 | 创建故障注入模型（网络分区） | ⏳ | NetworkPartition.tla |
| T2.5 | 添加 Liveness 性质验证 | ⏳ | 添加 Termination、EventualConsistency |

### Week 2：Go 实现（2026-01-21 ~ 2026-01-27）

| 任务 | 描述 | 状态 | 产出 |
|------|------|------|------|
| T2.6 | 实现 QuorumWithGossip Go 版本 | ⏳ | quorum_gossip.go |
| T2.7 | 实现 TwoPhaseCommit Go 版本 | ⏳ | two_phase_commit.go |
| T2.8 | 编写 10 个核心测试用例 | ⏳ | *_test.go (10 个) |

### Week 3：集成验证（2026-01-28 ~ 2026-01-31）

| 任务 | 描述 | 状态 | 产出 |
|------|------|------|------|
| T2.9 | TLA+ 模型 vs Go 实现对比 | ⏳ | 对比报告 |
| T2.10 | 性能测试（TPS、延迟） | ⏳ | 性能报告 |
| T2.11 | 编写 Phase 2 验证报告 | ⏳ | phase2-report.md |

---

## 🎯 详细计划

### T2.1：扩展到 5 节点集群

**目标**：验证协议在更大规模集群下的正确性

**修改内容**：
```tla
(* 3 节点 -> 5 节点 *)
Nodes == {"n1", "n2", "n3", "n4", "n5"}
Majority == 3  (* 5节点的多数派是 3 *)
```

**预期状态空间增长**：
- 3 节点：~15,000 状态
- 5 节点：预计 ~150,000 状态（10 倍增长）

**验证性质**：
- 保持原有的 4/6 个安全性质
- 添加新性质：QuorumReachability（可达性）

---

### T2.3 & T2.4：故障注入模型

#### 节点崩溃模型（CrashRecovery）

**新增变量**：
```tla
VARIABLES crashed  \* 崩溃的节点集合: SUBSET Nodes
```

**新增动作**：
```tla
\* 节点崩溃
NodeCrash(n) ==
    /\ n \notin crashed
    /\ crashed' = crashed \cup {n}
    /\ UNCHANGED <<knowledge, version, decision>>

\* 节点恢复
NodeRecover(n) ==
    /\ n \in crashed
    /\ crashed' = crashed \ {n}
    /\ UNCHANGED <<knowledge, version, decision>>
```

**验证性质**：
- **RecoveryCorrectness**：恢复后节点能同步最新状态
- **NoDoubleCommit**：崩溃恢复不会导致重复提交

#### 网络分区模型（NetworkPartition）

**新增变量**：
```tla
VARIABLES network_status  \* 网络状态: "normal" | "partitioned"
                partitions  \* 分区映射: [Nodes -> SUBSET Nodes]
```

**新增动作**：
```tla
\* 触发网络分区
NetworkPartition(partition1, partition2) ==
    /\ partition1 \cup partition2 = Nodes
    /\ partition1 \cap partition2 = {}
    /\ network_status' = "partitioned"
    /\ partitions' = [n \in partition1 |-> partition2]
    /\ UNCHANGED <<knowledge, version, decision>>

\* 恢复网络
NetworkHeal ==
    /\ network_status = "partitioned"
    /\ network_status' = "normal"
    /\ UNCHANGED <<knowledge, version, decision, partitions>>
```

**修改 GossipExchange**：
```tla
GossipExchange(p, q) ==
    /\ p # q
    /\ network_status = "normal"  \* 分区时禁止跨区通信
       \/ q \in partitions[p]    \* 或在同一个分区
    ...
```

**验证性质**：
- **PartitionSafety**：分区期间不会产生脑裂
- **PartitionRecovery**：分区恢复后能收敛到一致状态

---

### T2.5：Liveness 性质验证

#### 新增 Liveness 属性

```tla
\* 终止性：如果持续执行，最终会达到某个稳定状态
Termination == <>(phase = "done")

\* 最终一致性：如果持续执行 Gossip，所有节点最终会收敛
EventualConsistency ==
    <>(/\ version > 0
       /\ \A n1, n2 \in Nodes : knowledge[n1].version = knowledge[n2].version)

\* 决策收敛性：如果所有节点都决定，最终决策会一致
DecisionConvergence ==
    [] (\A n1, n2 \in Nodes :
        decision[n1] # "undecided" /\ decision[n2] # "undecided"
        => decision[n1] = decision[n2])
```

#### 使用 TLA+ 时序逻辑

```tla
\* 使用 <> (eventually) 和 [] (always) 运算符
LivenessSpec == Spec /\ SF_<<_荒年>> Next

\* 公平性假设（确保每个动作都有机会执行）
Fairness ==
    /\ WF_<<knowledge, decision, version>> (ProposeVote("n1", version))
    /\ WF_<<knowledge, decision, version>> (E n \in Nodes : GossipExchange(n, n))
    ...
```

---

## 📁 Phase 2 目录结构

```
tla-verification/
├── models/
│   ├── QuorumWithGossip.tla              (Phase 1 - 3节点)
│   ├── QuorumWithGossip5Nodes.tla        (Phase 2 - 5节点)
│   ├── QuorumWithGossipCrash.tla         (Phase 2 - 崩溃恢复)
│   ├── QuorumWithGossipPartition.tla     (Phase 2 - 网络分区)
│   ├── TwoPhaseCommitQuorumGossip.tla    (Phase 1 - 3节点)
│   └── TwoPhaseCommit5Nodes.tla          (Phase 2 - 5节点)
├── implementations/
│   ├── quorum_gossip.go                  (Go 实现)
│   ├── two_phase_commit.go               (Go 实现)
│   └── *_test.go                         (测试用例)
├── reports/
│   ├── QuorumWithGossip5Nodes_REPORT.md
│   ├── CrashRecovery_REPORT.md
│   └── ...
├── docs/
│   ├── phase1-report.md                  (Phase 1 报告)
│   └── phase2-plan.md                    (本文件)
└── README.md
```

---

## 🔧 技术细节

### 5 节点 Quorum 计算

| 集群大小 | Quorum (Majority) | 容错能力 |
|----------|-------------------|----------|
| 3 节点   | 2                 | 1 节点故障 |
| 5 节点   | 3                 | 2 节点故障 |

**公式**：`Quorum = floor(N/2) + 1`

### 状态空间增长预估

| 模型 | 节点数 | 预估状态数 | 增长因子 |
|------|--------|-----------|----------|
| QuorumWithGossip | 3 | 1,868 | 1x |
| QuorumWithGossip | 5 | ~15,000 | 8x |
| TwoPhaseCommit | 3 | 15,906 | 1x |
| TwoPhaseCommit | 5 | ~120,000 | 7.5x |
| + Crash | 3 | ~50,000 | 3x |
| + Partition | 3 | ~80,000 | 5x |

### 故障模型分类

1. **Crash Fault（崩溃故障）**
   - 节点停止响应
   - 不产生错误响应
   - 最常见的故障类型

2. **Byzantine Fault（拜占庭故障）**（超出 Scope）
   - 节点产生任意错误响应
   - 需要额外验证机制

3. **Network Fault（网络故障）**
   - 消息丢失
   - 消息延迟
   - 网络分区

---

## 📈 验证计划

### 安全性质（Safety）

1. **DecisionSafety**：决策安全性
2. **VersionConsistency**：版本一致性
3. **Atomicity**：原子性（2PC）
4. **CoordinatorConsistency**：协调者一致性

### 活性性质（Liveness）

1. **Termination**：终止性
2. **EventualConsistency**：最终一致性
3. **RecoveryCorrectness**：恢复正确性

---

## 🚀 执行顺序

1. **首先**：扩展 3 节点模型到 5 节点，验证基础正确性
2. **其次**：添加故障注入，验证容错能力
3. **然后**：添加 Liveness 验证，确保协议会终止
4. **最后**：Go 实现和测试用例

---

## 📚 参考资料

- [TLA+ 超简洁入门](https://lamport.azurewebsites.net/tla/overview.html)
- [Raft TLA+ 模型](https://github.com/tlaplus/raft-distilled)
- [Paxos TLA+ 模型](https://lamport.azurewebsites.net/paxos.html)
- [分布式系统故障模型](https://en.wikipedia.org/wiki/Fault_tolerance)

---

## 📝 更新日志

| 日期 | 版本 | 变更 |
|------|------|------|
| 2026-01-14 | v1.0 | 初始版本 |

---

**文档版本**：v1.0
**创建日期**：2026-01-14
**维护者**：NexKV 开发团队
