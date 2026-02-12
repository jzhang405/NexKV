# PR-061 Phase 3.1: 一致性协议测试

> **父文档**: [PR-061 Pre 文档](../2026-02-13_PR-061_e2e-testing-framework_Pre.md)
> **阶段**: Phase 3.1（原 Phase 2.5）
> **目标**: 验证 Gossip、Quorum、2PC 协议正确性
> **预计耗时**: 15 min
> **依赖**: Phase 2

---

## 1. 阶段目标

验证 NexKV 三层一致性协议的正确性：
- **Gossip 协议**：最终一致性，元数据异步扩散
- **Quorum 机制**：增强最终一致性，关键变更多数派确认
- **2PC 协议**：强一致性，分布式事务原子提交

---

## 2. 测试用例清单

### Gossip 测试（3 个）

| 测试用例 | 验证目标 | 预计耗时 | 优先级 |
|---------|---------|---------|--------|
| TestGossipConvergence7Nodes | 7 节点 Gossip 收敛 < 50s | 2 min | P0 |
| TestGossipWithNodeFailure | 节点故障时 Gossip 继续工作 | 2 min | P0 |
| TestGossipWithNetworkDelay | 网络延迟时 Gossip 最终收敛 | 2 min | P1 |

### Quorum 测试（5 个）

| 测试用例 | 验证目标 | 预计耗时 | 优先级 |
|---------|---------|---------|--------|
| TestQuorumMajority | 3 节点 Quorum 多数派达成 | 1 min | P0 |
| TestQuorumMinorityBlock | 2/5 节点无法达成 Quorum（阻塞） | 1 min | P0 |
| TestQuorumTimeout | Quorum 超时正确回滚 | 2 min | P1 |
| TestQuorumSplitBrain | 脑裂场景验证 | 3 min | P1 |
| TestQuorumWithPartialFailure | 部分节点故障时 Quorum 行为正确 | 2 min | P1 |

### 2PC 测试（5 个）

| 测试用例 | 验证目标 | 预计耗时 | 优先级 |
|---------|---------|---------|--------|
| Test2PCAllCommit | 所有节点正常提交 | 2 min | P0 |
| Test2PCOneRollback | 单个节点回滚不影响其他节点 | 2 min | P0 |
| Test2PCCoordinatorCrash | 协调者崩溃后可恢复 | 3 min | P1 |
| Test2PCParticipantCrash | 参与者崩溃后可补偿 | 3 min | P1 |
| Test2PCGossipSync | 2PC 状态通过 Gossip 同步 | 2 min | P1 |

**总计**: 13 个测试用例，预计 15 分钟

---

## 3. 框架组件需求

| 组件 | 接口 | 实现状态 |
|------|------|---------|
| **Libp2pFaultInjector** | `InjectConnClose()`, `InjectStreamLatency()` | ⚠️ 待实现 |
| **DataVerifier** | `VerifyConsistency()`, `GetData()` | ⚠️ 待实现 |
| **GossipSyncVerifier** | `WaitConvergence()` | ⚠️ 待实现 |

**Libp2pFaultInjector 核心接口**：
```go
type Libp2pFaultInjector struct {
    host    libp2p.Host
    enabled bool
}

func (fi *Libp2pFaultInjector) InjectFault(
    faultType FaultType,
    target peer.ID,
    params ...interface{},
) error
```

**DataVerifier 核心接口**：
```go
type DataVerifier struct {
    clients []DataClient
    timeout time.Duration
}

func (dv *DataVerifier) VerifyFinalConsistency(
    ctx context.Context,
) *ConsistencyResult
```

---

## 4. 验收标准

**Gossip 测试**:
- [ ] 7 节点 Gossip 收敛时间 < 50s
- [ ] 节点故障时 Gossip 继续工作
- [ ] 网络延迟下 Gossip 最终收敛

**Quorum 测试**:
- [ ] 3 节点 Quorum 多数派达成
- [ ] 2/5 节点无法达成 Quorum（阻塞）
- [ ] Quorum 超时正确回滚
- [ ] 脑裂场景下少数派独立运行
- [ ] 部分节点故障时 Quorum 行为正确

**2PC 测试**:
- [ ] 所有节点正常提交
- [ ] 单个节点回滚不影响其他节点
- [ ] 协调者崩溃后可恢复
- [ ] 参与者崩溃后可补偿
- [ ] 2PC 状态通过 Gossip 同步

---

## 5. 实施依赖

**前置条件**：
- [ ] Phase 2 多节点集群测试通过
- [ ] Gossip 协议实现完成
- [ ] Quorum 机制实现完成
- [ ] 2PC 协议实现完成

**后续阶段**：Phase 3.2（故障注入）

---

## 6. 相关文档

- 主文档: [PR-061 Pre 文档](../2026-02-13_PR-061_e2e-testing-framework_Pre.md)
- 架构设计: [E2E 测试架构设计](../07_E2E测试架构设计.md)
- Phase 2: [多节点集群测试](../2026-02-13_PR-061_e2e-testing-phase2.md)
