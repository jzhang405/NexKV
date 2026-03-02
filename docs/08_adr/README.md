# Architecture Decision Records (ADRs)

本目录包含 NexKV 项目的关键架构决策记录。

## 什么是 ADR？

ADR（Architecture Decision Record）是一种记录重要架构决策的标准化文档格式，用于：

- 📝 记录决策的上下文和理由
- 🔄 追踪决策的演变历史
- 📚 为新团队成员提供架构背景
- 🤔 帮助未来回顾和评估决策

## ADR 状态

| 状态 | 说明 |
|------|------|
| **提议** (Proposed) | 待讨论和决策 |
| **已接受** (Accepted) | 已采纳，正在实施 |
| **已弃用** (Deprecated) | 不再推荐，但可能仍在使用 |
| **已替代** (Superseded) | 被新决策替代 |

## ADR 列表

| ID | 标题 | 状态 | 日期 | 优先级 |
|----|------|------|------|--------|
| [001](./001-dual-storage-engine.md) | 双存储引擎策略 | 已接受 | 2026-02-18 | 🔴 P0 |
| [002](./002-async-pipeline.md) | 异步流水线架构 | 已接受 | 2026-03-02 | 🔴 P0 |
| [003](./003-5layer-ddd.md) | 5层 DDD 架构 | 已接受 | 2026-02-18 | 🔴 P0 |

## ADR 模板

创建新 ADR 时，使用以下模板：

```markdown
# ADR XXX: [决策标题]

**状态**: [提议/已接受/已弃用/已替代] | **日期**: YYYY-MM-DD | **决策者**: [团队/个人]

---

## 上下文（Context）

[描述当前情况和问题]

## 决策（Decision）

[描述做出的决策]

## 理由（Rationale）

[解释为什么做出这个决策]

## 后果（Consequences）

[描述决策的正面和负面影响]

## 实施细节

[提供实施的技术细节]

## 替代方案

[列出考虑过的其他方案]

## 参考资料

[链接到相关文档]
```

## 如何添加新 ADR

1. 复制模板创建新文件：`XXX-decision-title.md`
2. 填写完整内容
3. 更新本 README 的 ADR 列表
4. 提交 PR 团队审查

## 关键决策分类

### 架构设计 (Architecture)

- 5层 DDD 架构
- 双存储引擎策略

### 性能优化 (Performance)

- 异步流水线架构
- Per-Core 执行器

### 技术选型 (Technology)

- Transport 层：libp2p
- 序列化：MessagePack
- 存储引擎：Bf-Tree

### 数据一致性 (Consistency)

- 复制策略：Raft
- 事务模型：分布式事务

## 相关文档

- [Spike 文档](../07_spike/)
- [接口定义](../07_spike/2026-02-18_spike_nexkv-ddd-interface.md)
- [实现方案](../07_spike/2026-02-18_spike_nexkv-ddd-implement.md)
- [实施路线图](../07_spike/2026-02-18_spike_nexkv-ddd-roadmap.md)

---

**维护说明**:
- ADR 一旦接受，不应修改其历史内容
- 如需更新决策，创建新 ADR 并标记原 ADR 为"已替代"
- 定期审查过期的 ADR
