# 旧 Transport 设计文档归档

> **归档日期**: 2026-02-09
> **归档原因**: 技术栈从自研 TLV 协议迁移到 libp2p
> **状态**: ⚠️ 历史参考，不再维护

---

## 归档背景

NexKV 项目最初采用 **自研 TCP/UDP TLV 协议** 作为传输层实现，后续迁移到 **libp2p** 框架。

### 技术决策对比

| 特性 | 自研 TLV 协议 | libp2p |
|------|--------------|---------|
| **协议设计** | 固定头 + TLV 扩展头 + MessagePack + CRC32 | yamux/mplex 多路复用 |
| **UDP 分片** | 应用层分片（>1400 字节） | 无 UDP 支持 |
| **加密** | 需自建 | 内置 SECIO/Noise |
| **NAT 穿透** | 无 | 内置 AutoNAT/Relay |
| **成熟度** | 原型阶段 | 生产验证（IPFS/Filecoin） |

---

## 已归档文档列表

### 协议设计类

| 文档 | 说明 |
|------|------|
| `transport_2026-01-20_tcp-udp-unified-tlv-protocol.md` | TLV 协议详细设计 |
| `transport_2026-01-20_udp-fragmentation-improvements.md` | UDP 分片改进方案 |
| `transport_2026-01-21_ttl-vs-hop-count-reliability.md` | TTL vs Hop Count 可靠性分析 |
| `transport_2026-01-22_dual-transport-tcp-udp-proposal.md` | 双传输架构提案 |
| `transport_2026-01-25_frame-format-design-decision.md` | 帧格式设计决策 |

### 实现改进类

| 文档 | 说明 |
|------|------|
| `transport_2026-01-24_code-simplification-report.md` | 代码简化报告 |
| `transport_2026-01-24_flags-design-improvement.md` | Flags 设计改进 |
| `transport_2026-01-24_simplification-summary.md` | 简化总结 |
| `transport_2026-01-20_message-deduplication-design.md` | 消息去重设计 |

### RPC 接口类

| 文档 | 说明 |
|------|------|
| `transport_2026-01-23_rpc-transport-proposal.md` | RPC Transport 提案 |
| `transport_2026-01-28_rpc-interface-design.md` | Transport 目录重构方案（旧） |

### 评估报告类

| 文档 | 说明 |
|------|------|
| `transport_2026-01-23_Assessment-Report.md` | 评估报告 |
| `transport_2026-01-19_code-review-fix-priority.md` | 代码审查修复优先级 |

---

## 当前技术栈

### 传输层（libp2p）

**核心组件**：
- `internal/transport/p2p_service.go` - libp2p 服务
- `internal/transport/libp2p_transport_adapter.go` - 传输适配器
- `internal/transport/nexkv_protocol.go` - NexKV 协议

**协议栈**：
```
Application Layer (RPC/Gossip/Quorum)
        ↓
libp2p Stream Protocol (/nexkv/1.0.0)
        ↓
libp2p Transport (TCP)
        ↓
Network Layer (IP)
```

### 相关文档（当前有效）

- `/docs/08_api/transport.md` - Transport API 文档（libp2p）
- `/docs/06_project_management/brainstorm/transport_2026-02-07_p2pservice-started-race-condition.md` - libp2p 服务启动问题
- `/docs/06_project_management/brainstorm/message_2026-01-28_rpc-message-layer-design.md` - RPC 消息层次设计

---

## 迁移历史

| 日期 | 事件 | 文档 |
|------|------|------|
| 2026-01-20 | 自研 TLV 协议设计完成 | `transport_2026-01-20_tcp-udp-unified-tlv-protocol.md` |
| 2026-01-23 | RPC Transport 提案 | `transport_2026-01-23_rpc-transport-proposal.md` |
| 2026-01-24 | 代码简化阶段 1 完成 | `transport_2026-01-24_simplification-summary.md` |
| 2026-02-09 | 迁移到 libp2p，归档旧文档 | 本文档 |

---

## 参考价值

虽然这些文档描述的实现已被替代，但其中包含的**设计思想**仍有参考价值：

1. **协议设计经验**：TLV 格式、CRC32 校验、分片重组
2. **性能优化思路**：零拷贝序列化、批量操作
3. **代码简化实践**：消除重复、统一接口

**注意**：如需参考这些设计，请结合 libp2p 的特性进行适配。

---

**维护者**: NexKV 开发团队
**归档版本**: v1.0
**最后更新**: 2026-02-09
