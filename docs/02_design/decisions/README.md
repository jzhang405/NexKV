# 架构决策记录 (ADR)

> **维护者**: NexKV 开发团队
> **更新日期**: 2026-02-09

---

## 📋 ADR 索引

| ADR | 标题 | 状态 | 日期 |
|-----|------|------|------|
| **005** | External KV 存储引擎选型 - Bf-Tree | ✅ 已批准 | 2026-02-09 |
| **006** | Bf-Tree MVP 方案批准 | ✅ 已批准 | 2026-02-09 |

---

## ADR 005: External KV 存储引擎选型 - Bf-Tree

### 决策概述

NexKV 采用 **双引擎架构**：

| 组件 | 存储引擎 | 数据类型 | 状态 |
|------|----------|----------|------|
| **Metadata KV** | sync.Map + MVStore | 元数据（节点、分片、副本） | ✅ 已实现 |
| **External KV** | Bf-Tree | 业务数据（应用数据） | 🔄 待实现 |

### 核心结论

1. **Metadata KV 使用 sync.Map 是合理的**
   - 元数据是键值对模式（`host:{hostID}` → Host）
   - 点查询占 90%，sync.Map O(1) 最优
   - 高并发场景，sync.Map 无锁竞争
   - 不需要复杂关系查询

2. **External KV 使用 Bf-Tree**
   - 写入吞吐：200万 ops/s
   - 读取延迟：O(1) 内存命中
   - Lock-free SMR 并发控制
   - 支持范围查询和大容量存储

### 相关文档

- **预研究报告**：`docs/07_spike/kv-storage-engine-arch-analysis.md`
- **详细 ADR**：`docs/02_design/decisions/005_external_kv_engine_selection.md`
- **设计文档更新**：
  - `docs/02_design/modules/02_存储引擎设计.md`（新增第 7 章）
  - `docs/02_design/modules/01_详细设计文档.md`（新增第 7 章）
  - `CLAUDE.md`（新增双引擎架构说明）

---

## ADR 006: Bf-Tree MVP 方案批准

### 决策概述

批准 Bf-Tree Go 移植 **MVP 简化方案**，采用为期 **2-3 个月** 的开发计划。

| 维度 | 完整移植方案 | **MVP 方案（批准）** |
|------|------------|---------------------|
| **周期** | 4-6 个月 | **2-3 个月** |
| **并发控制** | Lock-free SMR | **sync.RWMutex** |
| **内存管理** | FreeList + 手动 | **sync.Pool + GC** |
| **Mini-Page** | 6+ 级 | **3 级（64B, 512B, 2KB）** |
| **性能目标** | 150-200万 ops/s | **50-100万 ops/s** |

### 核心决策

1. **元数据存储**：统一存储在 Metadata KV
2. **版本控制**：双版本号（LSN + HLC）
3. **路由感知**：Bf-Tree 知道自己属于哪个分片

### 相关文档

- **详细 ADR**：`docs/02_design/decisions/006_bftree_mvp_approval.md`
- **MVP 实施计划**：`docs/07_spike/bftree-mvp-implementation-plan.md`
- **元数据集成方案**：`docs/07_spike/bftree-metadata-integration.md`
- **Delta Chain 分析**：`docs/07_spike/bftree-delta-chain-promotion-analysis.md`

---

**文档版本**: v1.0
**创建日期**: 2026-02-09
**维护者**: NexKV 开发团队
