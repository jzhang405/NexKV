# 旧 Transport PR 文档归档

> **归档日期**: 2026-02-09
> **归档原因**: 技术栈从自研 TLV 协议迁移到 libp2p
> **状态**: ⚠️ 历史 PR，仅供参考

---

## 归档背景

这些 PR 文档记录了基于 **自研 TCP/UDP TLV 协议** 的 Transport 层实现工作流。

随着项目迁移到 **libp2p** 框架，这些 PR 的实现已不再适用。

---

## 已归档的 PR 文档列表

### UDP Transport 实现

| PR | 说明 |
|----|------|
| `2026-01-20_PR-012_UDP-Transport_Pre.md` | UDP Transport 实现（设计阶段） |
| `2026-01-20_PR-012_UDP-Transport_Post.md` | UDP Transport 实现（完成阶段） |

### TLV 协议测试

| PR | 说明 |
|----|------|
| `2026-01-20_PR-TLV-005_TLV-协议测试完善_全流程.md` | TLV 协议测试完善 |

### TCP/UDP 可靠性

| PR | 说明 |
|----|------|
| `2026-01-21_PR-013_TCP-UDP-Transport-Reliability_全流程.md` | TCP/UDP Transport 可靠性 |

### Hop-Count/TTL 机制

| PR | 说明 |
|----|------|
| `2026-01-21_PR-014_Transport-Hop-Count-TTL_全流程.md` | Transport Hop-Count/TTL 机制 |

### MsgExt/SendOpt 优化

| PR | 说明 |
|----|------|
| `2026-01-21_PR-015_Transport-MsgExt-SendOpt_全流程.md` | Transport MsgExt/SendOpt |

### ForwardMessage 实现

| PR | 说明 |
|----|------|
| `2026-01-22_PR-016_Transport-ForwardMessage_Pre.md` | ForwardMessage 实现（设计阶段） |
| `2026-01-22_PR-016_Transport-ForwardMessage_Post.md` | ForwardMessage 实现（完成阶段） |

### BatchForwardMessage 性能测试

| PR | 说明 |
|----|------|
| `2026-01-22_PR-017_Transport-BatchForwardMessage_PerfTest_Pre.md` | BatchForwardMessage 性能测试（设计） |
| `2026-01-22_PR-017_Transport-BatchForwardMessage_PerfTest_Post.md` | BatchForwardMessage 性能测试（完成） |

### MsgFrame 优化

| PR | 说明 |
|----|------|
| `2026-01-22_PR-018_MsgFrame-Optimization_Pre.md` | MsgFrame 优化 |

### MultiTransport 实现

| PR | 说明 |
|----|------|
| `2026-01-23_PR-019_MultiTransport-Implementation_Pre.md` | MultiTransport 实现（设计阶段） |
| `2026-01-23_PR-019_MultiTransport-Implementation_Post.md` | MultiTransport 实现（完成阶段） |

### RPC Transport 实现

| PR | 说明 |
|----|------|
| `2026-01-24_PR-021_RPCTransport_Implementation_Pre.md` | RPC Transport 实现（设计阶段） |

---

## 当前有效的 Transport PR 文档

### libp2p 迁移相关

| PR | 说明 |
|----|------|
| `2026-02-06_PR-Libp2p-TransportCleanup_全流程.md` | libp2p Transport 清理 |

---

## 迁移历史

| 日期 | PR | 事件 |
|------|----|----|
| 2026-01-20 | PR-012 | UDP Transport 实现 |
| 2026-01-20 | PR-TLV-005 | TLV 协议测试 |
| 2026-01-21 | PR-013 | TCP/UDP 可靠性 |
| 2026-01-21 | PR-014 | Hop-Count/TTL |
| 2026-01-21 | PR-015 | MsgExt/SendOpt |
| 2026-01-22 | PR-016 | ForwardMessage |
| 2026-01-22 | PR-017 | BatchForwardMessage |
| 2026-01-23 | PR-019 | MultiTransport |
| 2026-01-24 | PR-021 | RPC Transport |
| 2026-02-06 | PR-Libp2p | libp2p 迁移 |
| 2026-02-09 | - | 归档旧 PR 文档 |

---

## 参考价值

虽然这些 PR 的实现已被替代，但其中包含的**工作流程经验**仍有参考价值：

1. **PR 工作流模板**：Pre 文档 → 开发 → Post 文档
2. **测试策略**：单元测试、集成测试、性能测试
3. **代码审查流程**：发现问题、修复、验证

**注意**：新的 libp2p 相关 PR 应参考 `workflow.md` 规范执行。

---

**维护者**: NexKV 开发团队
**归档版本**: v1.0
**最后更新**: 2026-02-09
