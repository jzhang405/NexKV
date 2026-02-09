# ADR 005: External KV 存储引擎选型 - Bf-Tree

> **状态**: ✅ 已批准
> **决策日期**: 2026-02-09
> **决策者**: 架构师 + AI 团队
> **相关文档**: `docs/07_spike/kv-storage-engine-arch-analysis.md`

---

## 📋 决策概述

**决策**：NexKV External KV 采用 **Bf-Tree** 作为存储引擎

| 组件 | 存储引擎 | 状态 |
|------|----------|------|
| **Metadata KV** | sync.Map + MVStore | ✅ 已实现 |
| **External KV** | Bf-Tree | 🔄 待实现 |

---

## 🎯 背景与问题

### 问题陈述

NexKV 需要为 External KV（业务数据存储）选择合适的存储引擎，要求：
- 支持范围查询和有序扫描
- 高吞吐写入能力（> 100万 ops/s）
- 高并发场景（> 1000 QPS）
- 数据可超出内存（> 10GB）
- 低延迟读取（< 20μs）

### 候选方案

| 方案 | 优点 | 缺点 |
|------|------|------|
| **BTree** | 成熟稳定 | 并发性能一般 |
| **Bε-tree** | 写入优化 | 读取延迟高 |
| **Bf-Tree** | 综合性能最优 | 实现复杂 |

---

## 💡 决策内容

### 选择 Bf-Tree 的理由

| 维度 | 优势 | 说明 |
|------|------|------|
| **性能最优** | 写入吞吐 200万 ops/s | 是 BTree 的 6 倍 |
| **读取优化** | 内存命中 O(1) | 比 Bε-tree 快 3-5 倍 |
| **并发控制** | Lock-free SMR | 无锁竞争，高并发 |
| **大容量** | 数据可超出内存 | SSTable 持久化 |
| **范围查询** | 有序扫描 | 性能优秀 |

### 技术对比

| 指标 | BTree | Bε-tree | **Bf-Tree（选择）** |
|------|-------|---------|---------------------|
| 点查询 | O(log N) | 30-50μs | **O(1)（内存命中）** |
| 写入吞吐 | 30万 ops/s | 100万 ops/s | **200万 ops/s** |
| 并发性能 | 读写锁 | 读写锁 | **Lock-free SMR** |
| 写放大 | 1.0 | 1.5-3.0 | **1.2-1.5** |
| 内存占用 | 中等 | 低 | 较高 |

---

## 📊 实施计划

### Phase 1: 核心移植（1.5 个月）
- 移植 Bf-Tree 核心逻辑（Rust → Go）
- 实现 Mini-Page 管理（64B-4KB）

### Phase 2: 并发控制（1 个月）
- 实现 Lock-free SMR（Safe Memory Reclamation）
- 实现 epoch-based reclamation

### Phase 3: 持久化（1 个月）
- 对接 WAL（复用现有实现）
- 实现 SSTable 管理

### Phase 4: 集成测试（0.5 个月）
- 性能基准测试
- 并发压力测试

**总周期**：约 4 个月

---

## ⚠️ 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 实现复杂 | 开发周期长 | 参考 Rust 源码，分阶段实施 |
| 并发编程 | 容易出错 | 使用 TLA+ 验证，充分测试 |
| 性能验证 | 需要实测 | 建立性能基准，持续监控 |

---

## ✅ 决策依据

1. **预研究报告**：`docs/07_spike/kv-storage-engine-arch-analysis.md`
2. **Bf-Tree 论文**：[Microsoft Research](https://www.microsoft.com/en-us/research/publication/bf-tree/)
3. **用户研究笔记**：`/Users/zhangcz/Documents/obsidian/jzh-lifeos-pro-vault/1.Project/database-Bf-Tree/`

---

## 📝 后续行动

- [x] 架构师批准决策
- [ ] 创建技术设计文档
- [ ] 开始 Bf-Tree 移植工作
- [ ] 建立性能基准测试

---

**ADR 版本**: v1.0
**创建日期**: 2026-02-09
**维护者**: NexKV 开发团队
