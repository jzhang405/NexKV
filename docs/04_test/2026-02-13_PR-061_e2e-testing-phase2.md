# PR-061 Phase 2: 多节点集群测试

> **父文档**: [PR-061 Pre 文档](../2026-02-13_PR-061_e2e-testing-framework_Pre.md)
> **阶段**: Phase 2
> **目标**: 验证多节点集群的协同工作
> **预计耗时**: 10 min
> **依赖**: Phase 1

---

## 1. 阶段目标

验证多节点集群的形成、发现和拓扑管理：
- 集群自动形成
- 节点间相互发现
- 拓扑结构正确建立
- Leader 选举
- 节点动态加入/离开

---

## 2. 测试用例清单

| 测试用例 | 验证目标 | 预计耗时 | 优先级 |
|---------|---------|---------|--------|
| TestAllNodesRunning | 所有节点正常运行 | 2 min | P0 |
| TestGossipSync | Gossip 元数据同步 | 2 min | P0 |
| TestTopologyFormation | 拓扑结构形成 | 2 min | P0 |
| TestNodeDiscovery | 节点自动发现 | 2 min | P0 |
| TestLeaderElection | Leader 自动选举 | 2 min | P1 |
| TestAddNodeToRunningCluster | 动态添加节点 | 2 min | P1 |
| TestRemoveNodeFromCluster | 移除节点 | 2 min | P1 |

**总计**: 7 个测试用例，预计 10 分钟

---

## 3. 框架组件需求

| 组件 | 接口 | 实现状态 |
|------|------|---------|
| **TestCluster** | `Start()`, `Stop()`, `WaitStable()` | ⚠️ 待实现 |
| **GossipSync** | 基于 libp2p 的 Gossip 同步 | ⚠️ 待实现 |

**TestCluster 核心接口**：
```go
type TestCluster struct {
    Nodes     []*DaemonProcess
    ConfigDir string
    LogDir    string
}

func (c *TestCluster) Start(ctx context.Context) error
func (c *TestCluster) Stop() error
func (c *TestCluster) WaitStable(ctx context.Context, timeout time.Duration) error
```

---

## 4. 验收标准

- [ ] 7/7 测试用例通过
- [ ] `make test-e2e-phase2` 可运行
- [ ] 3 节点集群能正常形成
- [ ] Gossip 同步收敛 < 30s
- [ ] 拓扑结构正确

---

## 5. 实施依赖

**前置条件**：
- [ ] Phase 1 单节点测试通过
- [ ] Daemon 进程管理稳定
- [ ] CLI 基础命令可用

**后续阶段**：Phase 3.1（一致性协议）

---

## 6. 相关文档

- 主文档: [PR-061 Pre 文档](../2026-02-13_PR-061_e2e-testing-framework_Pre.md)
- 架构设计: [E2E 测试架构设计](../07_E2E测试架构设计.md)
- Phase 1: [单节点基础测试](../2026-02-13_PR-061_e2e-testing-phase1.md)
- Phase 3: [一致性协议测试](../2026-02-13_PR-061_e2e-testing-phase3.md)
- 架构设计: [E2E 测试架构设计](../07_E2E测试架构设计.md)
- Phase 1: [单节点基础测试](./2026-02-13_PR-061_e2e-testing-phase1.md)
