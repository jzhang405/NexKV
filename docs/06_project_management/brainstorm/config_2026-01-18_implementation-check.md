# 一致性级别定义配置实现检查报告

> **文档类型**: Brainstorm / Findings
> **创建日期**: 2026-01-18
> **检查文档**: `docs/00_overview/02_一致性级别定义.md`
> **状态**: ✅ 检查完成

---

## 📋 检查概述

对 `02_一致性级别定义.md` 文档中定义的优先级、周期、重试、超时等配置参数与实际代码实现进行一致性检查。

**检查范围**：
- Gossip 分级配置
- Quorum 超时和重试
- 2PC 超时和配置

---

## ✅ 已实现的配置

### 1. GossipConfig（基本配置）

**文档定义** vs **实际实现**

| 配置项 | 文档值 | 实际实现 | 状态 |
|-------|--------|---------|------|
| **Interval** | 10秒（默认） | `10 * time.Second` | ✅ 一致 |
| **Fanout** | 2（默认） | `2` | ✅ 一致 |
| **Timeout** | 5秒 | `5 * time.Second` | ✅ 一致 |
| **MaxChangeLogs** | 1000 | `1000` | ✅ 一致 |

**代码位置**：`internal/metadata/consensus/gossip.go:70-93`

```go
type GossipConfig struct {
    Interval      time.Duration // 10s - 默认同步间隔
    Fanout        int           // 2   - 默认每轮随机节点数
    Timeout       time.Duration // 5s  - 单次同步超时
    MaxChangeLogs int           // 1000 - 最大变更日志数
}

func DefaultGossipConfig() *GossipConfig {
    return &GossipConfig{
        Interval:      10 * time.Second,
        Fanout:        2,
        Timeout:       5 * time.Second,
        MaxChangeLogs: 1000,
    }
}
```

---

### 2. QuorumConfig

**文档定义** vs **实际实现**

| 配置项 | 文档值 | 实际实现 | 状态 |
|-------|--------|---------|------|
| **Timeout** | 30秒 | `30 * time.Second` | ✅ 一致 |
| **RetryCount** | 3次 | `3` | ✅ 一致 |
| **MinQuorum** | N/2+1 | `0` (自动计算) | ✅ 一致 |

**代码位置**：`internal/metadata/consensus/quorum.go:61-81`

```go
type QuorumConfig struct {
    Timeout    time.Duration // 投票超时（默认 30 秒）
    RetryCount int           // 重试次数（默认 3）
    MinQuorum  int           // 最小法定人数（默认自动计算 N/2+1）
}

func DefaultQuorumConfig() *QuorumConfig {
    return &QuorumConfig{
        Timeout:    30 * time.Second,
        RetryCount: 3,
        MinQuorum:  0, // 自动计算
    }
}
```

---

### 3. TwoPCConfig

**文档定义** vs **实际实现**

| 配置项 | 文档值 | 实际实现 | 状态 |
|-------|--------|---------|------|
| **Timeout** | 10秒 | `10 * time.Second` | ✅ 一致 |
| **GossipInterval** | 5秒 | `5 * time.Second` | ✅ 一致 |
| **EnableGossipSync** | - | `true` | ✅ 已实现 |

**代码位置**：`internal/metadata/consensus/twopc.go:63-82`

```go
type TwoPCConfig struct {
    Timeout          time.Duration // 单阶段超时（默认 10 秒）
    EnableGossipSync bool          // 是否启用 Gossip 状态同步
    GossipInterval   time.Duration // Gossip 同步间隔（默认 5 秒）
}

func DefaultTwoPCConfig() *TwoPCConfig {
    return &TwoPCConfig{
        Timeout:          10 * time.Second,
        EnableGossipSync: true,
        GossipInterval:   5 * time.Second,
    }
}
```

---

## ❌ 未实现的功能

### 1. Gossip 分级配置（PriorityCritical/High/Normal/Low）

**文档定义**：
```go
type GossipConfig struct {
    Interval      time.Duration // 10s - 默认同步间隔（PriorityNormal）
    Fanout        int           // 2   - 默认每轮随机节点数
    Timeout       time.Duration // 5s  - 单次同步超时
    MaxChangeLogs int           // 1000 - 最大变更日志数

    // 分级配置（可选）
    HighInterval     time.Duration // 5s  - 重要消息周期
    LowInterval      time.Duration // 30s - 低优先级消息周期
    // PriorityCritical 消息：决策后立即转发，不等定期周期
}
```

**实际实现**：
```go
type GossipConfig struct {
    Interval      time.Duration // 只有单一 Interval
    Fanout        int
    Timeout       time.Duration
    MaxChangeLogs int
    // ❌ 没有 HighInterval
    // ❌ 没有 LowInterval
    // ❌ 没有 CriticalInterval
    // ❌ 没有优先级枚举类型
    // ❌ 没有立即转发机制
}
```

**差异分析**：
- ❌ **PriorityCritical** 未实现：没有"立即触发（不等周期）"的机制
- ❌ **PriorityHigh** 未实现：没有 HighInterval (5s) 配置
- ❌ **PriorityNormal** 已实现：对应当前的 Interval (10s)
- ❌ **PriorityLow** 未实现：没有 LowInterval (30s) 配置
- ❌ **优先级枚举** 未实现：没有 PriorityCritical/High/Normal/Low 类型定义

---

### 2. 重试策略

| 协议 | 文档定义 | 实际实现 | 状态 |
|------|---------|---------|------|
| **Gossip** | 重试 3 次 | ❌ 无重试机制 | 未实现 |
| **Quorum** | 重试 3 次 | ✅ RetryCount: 3 | 已实现 |
| **2PC** | 重试 3 次 | ❌ 无重试配置 | 未实现 |

**Gossip 实际行为**（`gossip.go:286-299`）：
```go
ctx, cancel := context.WithTimeout(context.Background(), g.config.Timeout)
defer cancel()

// 并行同步
for _, peer := range selectedPeers {
    g.stopWg.Add(1)
    go func(addr string) {
        defer g.stopWg.Done()
        if err := g.syncToNode(ctx, addr); err != nil {
            // ❌ 只记录日志，没有重试
            logging.WithField("peer", addr).
                WithError(err).
                Warn("Gossip 同步失败")
        }
    }(peer)
}
```

**分析**：Gossip 失败后只记录 Warn 日志，不会重试。下一次同步会在下一个周期（10秒）进行。

---

### 3. 立即转发机制

**文档描述**：
> **PriorityCritical** 消息（如 2PC Commit）**决策后立即触发转发**，不等定期周期

**实际实现**：
- ❌ 没有立即转发的机制
- ✅ Gossip 采用固定周期（`syncLoop()` 使用 `time.NewTicker(g.config.Interval)`）
- ⚠️ 2PC 的 `GossipInterval` 只影响状态同步的频率，不是"立即转发"

**2PC Gossip 同步实现**（需要进一步检查源码确认是否有立即转发逻辑）

---

## 📊 实现完成度

| 功能模块 | 完成度 | 详情 |
|---------|--------|------|
| **Gossip 基本配置** | 100% | Interval、Fanout、Timeout、MaxChangeLogs 全部实现 |
| **Gossip 分级配置** | 0% | PriorityCritical/High/Normal/Low 完全未实现 |
| **Quorum 配置** | 100% | Timeout、RetryCount、MinQuorum 全部实现 |
| **2PC 配置** | 100% | Timeout、EnableGossipSync、GossipInterval 全部实现 |
| **Gossip 重试** | 0% | 失败后不重试，等下一周期 |
| **Quorum 重试** | 100% | 3 次重试已实现 |
| **2PC 重试** | 0% | 没有重试配置 |
| **立即转发机制** | 0% | 没有立即转发逻辑 |

**总体完成度**：**50%**（基础配置实现，高级功能未实现）

---

## 🎯 核心发现

### 1. 分级 Gossip 机制完全未实现

文档中重点强调的 **"分级 Gossip 机制"**（PriorityCritical/High/Normal/Low）在代码中完全没有体现：

| 特性 | 文档描述 | 实际情况 |
|------|---------|---------|
| **PriorityCritical** | 立即触发，不等周期 | ❌ 不存在 |
| **PriorityHigh** | 5秒周期 | ❌ 不存在 |
| **PriorityNormal** | 10秒周期 | ✅ 基本实现（单一周期） |
| **PriorityLow** | 10-30秒周期 | ❌ 不存在 |

### 2. "立即转发"是设计目标，非当前实现

文档描述的：
> **PriorityCritical 消息（如 2PC Commit）决策后立即触发转发，不等定期周期**

这是**设计目标**，不是当前实现。当前 Gossip 只有固定周期的同步机制。

### 3. 重试策略不一致

- **Quorum**：有完整的重试机制（3次）
- **Gossip**：没有重试（失败后等下一周期）
- **2PC**：没有重试配置

---

## 💡 建议措施

### 短期措施（文档修正）

1. **明确区分"设计目标"和"当前实现"**
   ```markdown
   > **⚠️ 注意**：分级 Gossip 机制（PriorityCritical/High/Normal/Low）是设计目标，
   > 当前版本使用单一 Gossip 周期（10秒）。立即转发机制待后续版本实现。
   ```

2. **移除未实现功能的描述**
   - 删除"立即转发"相关描述
   - 删除 HighInterval、LowInterval 配置示例
   - 将分级配置移至"未来规划"章节

3. **更新 Gossip 重试说明**
   ```markdown
   | 协议 | 重试次数 | 失败策略 |
   |------|---------|---------|
   | **Gossip** | 无重试 | 下一周期重试（10秒） |
   | **Quorum** | 3次 | 回滚提案 |
   | **2PC** | 待实现 | 全员rollback |
   ```

### 中期措施（实施或澄清）

**选项 A：实现分级 Gossip**
工作量：约 2-3 周

需要实现：
1. 定义优先级枚举类型
2. 扩展 GossipConfig（HighInterval、LowInterval）
3. 实现立即转发逻辑（关键决策后触发）
4. 按优先级分类消息处理
5. 添加单元测试和集成测试

**选项 B：简化文档**
将分级 Gossip 移至"未来规划"，只描述当前已实现的功能：
- 单一 Gossip 周期（10秒）
- 基本的超时处理（5秒）
- 无重试（依赖下一周期）

### 长期措施（架构改进）

1. **配置验证**
   - 在初始化时验证配置参数
   - 提供配置推荐值

2. **运行时调整**
   - 支持动态调整 Gossip 周期
   - 支持运行时启用/禁用立即转发

3. **监控与诊断**
   - 添加各优先级消息的统计信息
   - 记录收敛时间
   - 监控超时和重试情况

---

## 🔗 相关文档

- **一致性级别定义**：`docs/00_overview/02_一致性级别定义.md`
- **实际实现**：
  - `internal/metadata/consensus/gossip.go`（Gossip 服务）
  - `internal/metadata/consensus/quorum.go`（Quorum 服务）
  - `internal/metadata/consensus/twopc.go`（2PC 服务）
- **相关 brainstorm**：
  - `code_2026-01-18_consistency-check.md`（代码一致性检查）
  - `merkle-tree_2026-01-18_gossip-optimization.md`（Merkle Tree 优化 Gossip）

---

## 📌 结论

1. **基础配置完整**：Gossip/Quorum/2PC 的基本超时和周期配置已实现
2. **分级机制未实现**：PriorityCritical/High/Normal/Low 分级 Gossip 完全缺失
3. **立即转发缺失**：文档强调的"立即转发"是设计目标，非当前实现
4. **重试不完整**：只有 Quorum 有重试，Gossip 和 2PC 缺失
5. **建议优先修正文档**：明确区分当前实现和设计目标

---

**检查完成时间**: 2026-01-18
**检查者**: AI Agent
**下一步**: 等待架构师评审后决定文档修正方案或分级 Gossip 实施计划
