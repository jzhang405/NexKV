# 预研结论汇总

> **汇总日期**: 2026-02-11
> **评审人**: 👤 架构师
> **评审状态**: ✅ 全部通过

---

## 📋 预研报告清单

| # | 预研报告 | 状态 | 决策 |
|---|---------|------|------|
| 1 | `consistency-layering-research.md` | ✅ 通过 | 启动一致性分层实施 |
| 2 | `libp2p-application-research.md` | ✅ 通过 | 继续使用 libp2p |
| 3 | `node-sync-optimization.md` | ✅ 通过 | 分阶段实施节点同步优化 |
| 4 | `transaction-scope-optimization.md` | ✅ 通过 | 实施 Transaction 范围优化 |
| 5 | `tree-coordinator-research.md` | ✅ 通过 | 完善 TreeCoordinator 元数据管理 |
| 6 | `bftree-research.md` | ✅ 通过 | 启动 Bf-Tree MVP 实施（P0→P1→P2） |
| 7 | `protocol-message-definition.md` | ✅ 通过 | 协议消息定义已确认 |

---

## 🎯 核心结论汇总

### 1️⃣ 一致性分层机制

**结论**: 采用三层一致性模型（2PC → Quorum → Gossip）

```
Layer 3: 2PC (强一致) - 关键变更：分片创建、主副本切换
   ↓
Layer 2: Quorum (增强最终一致) - 重要变更：节点角色变更
   ↓
Layer 1: Gossip (最终一致) - 普通变更：状态更新
```

**树形拓扑一致性分层**：
- 同父子节点：2PC + Quorum → 强一致（< 100ms）
- 跨父节点叶子：Gossip → 弱一致（10秒内）

**工作量**: 17 天（约 2.5 周）

---

### 2️⃣ libp2p 应用

**结论**: 继续使用 libp2p 作为 P2P 网络库

| 协议 | 用途 | 状态 |
|------|------|------|
| `/nexkv/1.0.0` | 通用消息传输 | ✅ 已实现 |
| `/nexkv/rpc/1.0.0` | TreeCoordinator 与节点通信 | ✅ 已实现 |
| `/nexkv/gossip/1.0.0` | 元数据 Gossip 同步 | 🔄 优化中 |
| `/nexkv/sync/1.0.0` | 数据同步（2PC） | ✅ 已实现 |
| `/nexkv/quorum/1.0.0` | Quorum 一致性 | ⏳ 待实现 |

---

### 3️⃣ 节点同步优化

**结论**: 分阶段实施 Bloom Filter + Merkle Tree 优化

| 阶段 | 内容 | 优先级 | 周期 | 收益 |
|------|------|--------|------|------|
| **Phase 1** | Bloom Filter 本地查询优化 | P0 | 1 周 | 减少 90% 无效远程查询 |
| **Phase 2** | Gossip 双向同步机制 | P0 | 1 周 | 仅传输变更数据 |
| **Phase 3** | Merkle Tree 差异检测 | P1 | 2 周 | 节省 85%-95% 带宽 |

---

### 4️⃣ Transaction 范围优化

**结论**: 实施 Transaction 范围识别 + 同父节点集合优化

| 优化项 | 说明 | 收益 |
|--------|------|------|
| **分片识别** | IdentifyShards 方法 | 明确 Transaction 涉及的分片 |
| **节点选择** | GetOptimalNodeSet 算法 | 减少节点数 50%-70% |
| **亲和性分析** | ShardAffinity 管理 | 分组高频访问分片 |
| **放置策略** | 同父节点优先 | 降低跨父节点 2PC |

**案例**: 9节点 → 3节点（减少 67%）

---

### 5️⃣ TreeCoordinator 元数据管理

**结论**: 使用 MetadataKV 映射方案，9 个 Namespace 分层管理

| Namespace | 说明 | 一致性级别 |
|-----------|------|-----------|
| `NamespaceCluster` | 集群元数据 | 强一致 |
| `NamespaceShard` | 分片元数据 | 强一致 |
| `NamespaceNode` | 节点元数据 | 最终一致 |
| `NamespaceRole` | 角色元数据 | 最终一致 |
| `NamespaceStatic` | 静态元数据 | 强一致 |
| `NamespaceTopo` | 拓扑元数据 | 最终一致 |
| `NamespaceDynamic` | 动态元数据 | 最终一致 |
| `NamespaceOp` | 运维元数据 | 最终一致 |
| `NamespaceVersion` | 版本号 | 强一致 |

---

### 6️⃣ Bf-Tree 存储引擎

**结论**: 启动 Bf-Tree MVP 实施，分阶段达成性能目标

| 阶段 | 性能目标 | 说明 |
|------|----------|------|
| **MVP P0** | 50万 ops/s, < 30μs | 最低目标，必须达到 |
| **MVP P1** | 75万 ops/s, < 25μs | 推荐目标，正常应达到 |
| **MVP P2** | 100万 ops/s, < 20μs | 理想目标，尽力达到 |

**工作量**: 10-12 周

**简化策略**：
- 并发控制：Lock-free SMR → `sync.RWMutex`
- 内存管理：FreeList → `sync.Pool` + GC
- Mini-Page：6+ 级 → 3 级（64B, 512B, 2KB）
- WAL：独立实现 → 扩展现有 WAL

---

### 7️⃣ 协议消息定义

**结论**: 确认 5 个 libp2p 协议及 9 种消息类型

| 协议 | 消息类型 | Payload |
|------|----------|---------|
| `/nexkv/1.0.0` | GET, PUT, DELETE | GetPayload, PutPayload, DeletePayload |
| `/nexkv/rpc/1.0.0` | CLUSTER | ClusterPayload (join/leave/status) |
| `/nexkv/gossip/1.0.0` | GOSSIP | GossipPayload (Digest + Bloom Filter) |
| `/nexkv/sync/1.0.0` | SYNC, ACK, NACK | TwoPCPreparePayload, TwoPCCommitPayload, TwoPCRollbackPayload |
| `/nexkv/quorum/1.0.0` | QUORUM | QuorumPayload (propose/vote/decide) |

---

## 📅 实施优先级建议

| 优先级 | 项目 | 工作量 | 依赖 |
|--------|------|--------|------|
| **P0** | 节点同步优化 Phase 1-2 | 2 周 | 无 |
| **P0** | Transaction 范围优化 | 1 周 | 无 |
| **P0** | Quorum 机制实现 | 1 周 | 无 |
| **P1** | 一致性分层实施 | 2.5 周 | Quorum |
| **P1** | 节点同步优化 Phase 3 | 2 周 | Phase 1-2 |
| **P2** | Bf-Tree MVP P0 | 10-12 周 | 无 |

---

## 🚀 下一步行动

### 立即启动（P0）

1. **创建开发分支**: `feature/consistency-layering`
2. **实施节点同步优化 Phase 1-2**: Bloom Filter + 双向同步
3. **实施 Transaction 范围优化**: 分片识别 + 节点选择
4. **实现 Quorum 机制**: 多数派投票 + 决策传播

### 近期计划（P1）

1. **一致性分层完整实施**: 2PC → Quorum → Gossip
2. **节点同步优化 Phase 3**: Merkle Tree 差异检测
3. **完善 TreeCoordinator 元数据管理**: MetadataKV 映射

### 中长期计划（P2）

1. **Bf-Tree MVP 实施**: 10-12 周开发周期
2. **性能优化与测试**: 达到 P1/P2 性能目标

---

## 📎 相关文档

### 预研报告

- `docs/07_spike/consistency-layering-research.md`
- `docs/07_spike/libp2p-application-research.md`
- `docs/07_spike/node-sync-optimization.md`
- `docs/07_spike/transaction-scope-optimization.md`
- `docs/07_spike/tree-coordinator-research.md`
- `docs/07_spike/bftree/2026-02-11_spike_rust_bftree-research.md`
- `docs/07_spike/protocol-message-definition.md`

### 设计文档

- `docs/02_design/protocols/01_一致性协议设计.md`
- `docs/02_design/protocols/02_二阶段提交与Gossip状态同步.md`

---

**文档版本**: v1.0
**创建日期**: 2026-02-11
**维护者**: NexKV 开发团队
**状态**: ✅ 全部通过，准备启动开发
