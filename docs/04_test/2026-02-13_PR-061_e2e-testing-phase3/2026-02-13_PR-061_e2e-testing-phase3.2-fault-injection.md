# PR-061 Phase 3.2: 故障注入测试

> **父文档**: [PR-061 Pre 文档](../2026-02-13_PR-061_e2e-testing-framework_Pre.md)
> **阶段**: Phase 3.2
> **目标**: 验证故障场景下的自愈能力
> **预计耗时**: 20 min
> **依赖**: Phase 3.1

---

## 1. 阶段目标

验证系统在故障场景下的自愈和容错能力：
- 节点故障检测和恢复
- 网络分区容错
- 脑裂场景处理
- 故障节点重启后数据同步

---

## 2. 测试用例清单

| 测试用例 | 验证目标 | 预计耗时 | 优先级 |
|---------|---------|---------|--------|
| TestSingleNodeFailure | 单节点故障检测 | 3 min | P0 |
| TestNodeRecovery | 节点故障后恢复 | 3 min | P0 |
| TestMultipleNodeFailures | 多节点同时故障 | 4 min | P1 |
| TestLeaderFailure | Leader 故障自动选举 | 4 min | P0 |
| TestFullClusterRestart | 集群全部重启后恢复 | 6 min | P1 |

**总计**: 5 个测试用例，预计 20 分钟

---

## 3. 框架组件需求

| 组件 | 接口 | 实现状态 |
|------|------|---------|
| **FaultInjector** | `KillNode()`, `PartitionNetwork()`, `RestoreNode()` | ⚠️ 待实现 |
| **RecoveryVerifier** | `VerifyDataSync()`, `VerifyTopologyRecovery()` | ⚠️ 待实现 |

**FaultInjector 核心接口**：
```go
type FaultInjector struct {
    cluster *TestCluster
}

func (fi *FaultInjector) KillNode(nodeID string) error
func (fi *FaultInjector) PartitionNetwork(nodeA, nodeB string) error
func (fi *FaultInjector) RestoreNode(nodeID string) error
```

---

## 4. 验收标准

- [ ] 5/5 测试用例通过
- [ ] `make test-e2e-phase3-fault` 可运行
- [ ] 节点故障能在 10s 内检测
- [ ] 节点恢复后数据自动同步
- [ ] Leader 故障后 5s 内重新选举
- [ ] 网络分区恢复后集群自动愈合

---

## 5. 实施依赖

**前置条件**：
- [ ] Phase 3.1 一致性协议测试通过
- [ ] 故障检测机制实现
- [ ] 节点重启逻辑实现

**后续阶段**：Phase 4（并发场景）

---

## 6. 相关文档

- 主文档: [PR-061 Pre 文档](../2026-02-13_PR-061_e2e-testing-framework_Pre.md)
- Phase 3.1: [一致性协议测试](./2026-02-13_PR-061_e2e-testing-phase3.1-consistency.md)
