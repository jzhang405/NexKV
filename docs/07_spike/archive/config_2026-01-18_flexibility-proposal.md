# 配置灵活性说明

> **文档类型**: Brainstorm / Proposal
> **创建日期**: 2026-01-18
> **状态**: 💡 建议简化文档

---

## ✅ 当前代码已支持直接配置

### 1. Gossip 直接配置

```go
// 方式 1：使用默认配置
service, err := NewGossipService(metaStore, transport, hlc, peers, nil)
// 默认：Interval=10s, Fanout=2, Timeout=5s

// 方式 2：自定义配置（完全灵活）
config := &GossipConfig{
    Interval:      5 * time.Second,   // 自定义周期
    Fanout:        3,                  // 自定义 Fanout
    Timeout:       10 * time.Second,  // 自定义超时
    MaxChangeLogs: 2000,              // 自定义日志数量
}
service, err := NewGossipService(metaStore, transport, hlc, peers, config)
```

### 2. Quorum 直接配置

```go
// 方式 1：使用默认配置
service, err := NewQuorumService(metaStore, transport, hlc, localAddr, nodes, nil)
// 默认：Timeout=30s, RetryCount=3

// 方式 2：自定义配置
config := &QuorumConfig{
    Timeout:    60 * time.Second,  // 自定义超时
    RetryCount: 5,                 // 自定义重试次数
    MinQuorum:  3,                 // 自定义最小法定人数
}
service, err := NewQuorumService(metaStore, transport, hlc, localAddr, nodes, config)
```

### 3. TwoPC 直接配置

```go
// 方式 1：使用默认配置
service, err := NewTwoPCService(metaStore, transport, hlc, uuidGen, localAddr, nodes, nil)
// 默认：Timeout=10s, GossipInterval=5s

// 方式 2：自定义配置
config := &TwoPCConfig{
    Timeout:          20 * time.Second,  // 自定义超时
    EnableGossipSync: false,             // 禁用 Gossip
    GossipInterval:   3 * time.Second,   // 自定义 Gossip 周期
}
service, err := NewTwoPCService(metaStore, transport, hlc, uuidGen, localAddr, nodes, config)
```

---

## 💡 建议的文档简化

### 移除"四种优先级"概念

**当前文档**（`02_一致性级别定义.md`）：
```markdown
| 优先级 | 定期周期 | Fanout | 立即转发 | 收敛时间 |
|--------|---------|--------|---------|---------|
| **PriorityCritical** | 立即触发（不等周期） | 3 | ✅ 是 | < 1秒 |
| **PriorityHigh** | 5秒 | 2 | ⚠️ 可选 | < 5秒 |
| **PriorityNormal** | 10秒 | 2 | ❌ 否 | < 10秒 |
| **PriorityLow** | 10-30秒 | 2 | ❌ 否 | < 30秒 |
```

**建议改为**（更简单、更准确）：
```markdown
## ⚙️ 配置参数

### Gossip 配置

| 参数 | 默认值 | 说明 | 推荐范围 |
|------|--------|------|---------|
| **Interval** | 10秒 | 同步周期 | 5-30秒 |
| **Fanout** | 2 | 每轮随机节点数 | 2-3 |
| **Timeout** | 5秒 | 单次同步超时 | 3-10秒 |
| **MaxChangeLogs** | 1000 | 最大变更日志数 | 500-2000 |

### 使用示例

```go
// 快速同步（适合关键元数据）
config := &GossipConfig{
    Interval: 5 * time.Second,  // 更快
    Fanout:   3,                // 更多节点
}

// 普通同步（默认配置）
config := DefaultGossipConfig()  // 10s, fanout=2

// 低频同步（适合统计信息）
config := &GossipConfig{
    Interval: 30 * time.Second,  // 更慢
}
```

### Quorum 配置

| 参数 | 默认值 | 说明 | 推荐范围 |
|------|--------|------|---------|
| **Timeout** | 30秒 | 投票超时 | 10-60秒 |
| **RetryCount** | 3 | 重试次数 | 1-5 |
| **MinQuorum** | 自动（N/2+1） | 最小法定人数 | 固定值或0自动计算 |

### 2PC 配置

| 参数 | 默认值 | 说明 | 推荐范围 |
|------|--------|------|---------|
| **Timeout** | 10秒 | 单阶段超时 | 5-30秒 |
| **EnableGossipSync** | true | 是否启用 Gossip 状态同步 | - |
| **GossipInterval** | 5秒 | Gossip 同步间隔 | 3-10秒 |
```

---

## 🎯 优势对比

### 当前"四种优先级"方案

| 维度 | 评分 | 说明 |
|------|------|------|
| **实现复杂度** | ⭐⭐⭐⭐⭐ | 需要实现优先级枚举、分类逻辑、立即转发机制 |
| **代码维护成本** | ⭐⭐⭐⭐ | 分支逻辑多，测试复杂 |
| **配置灵活性** | ⭐⭐ | 只能选择4种预设 |
| **用户理解成本** | ⭐⭐⭐ | 需要学习优先级概念 |
| **当前实现状态** | ❌ | 完全未实现 |

### 直接配置方案（推荐）

| 维度 | 评分 | 说明 |
|------|------|------|
| **实现复杂度** | ⭐ | 已实现，无需额外代码 |
| **代码维护成本** | ⭐ | 结构简单，易于维护 |
| **配置灵活性** | ⭐⭐⭐⭐⭐ | 完全自定义 |
| **用户理解成本** | ⭐ | 直观、简单 |
| **当前实现状态** | ✅ | 已完全实现 |

---

## 📋 配置场景速查

### 场景 1：关键元数据快速同步

```go
config := &GossipConfig{
    Interval: 5 * time.Second,  // 快速周期
    Fanout:   3,                // 更多节点
    Timeout:  5 * time.Second,  // 快速超时
}
```

### 场景 2：普通元数据同步（默认）

```go
config := DefaultGossipConfig()  // 10s, fanout=2, timeout=5s
```

### 场景 3：统计信息低频同步

```go
config := &GossipConfig{
    Interval: 30 * time.Second,  // 慢速周期
    Fanout:   1,                 // 最少节点
}
```

### 场景 4：大集群优化（100+ 节点）

```go
gossipConfig := &GossipConfig{
    Interval:      10 * time.Second,
    Fanout:        3,   // 更多节点，加快收敛
    MaxChangeLogs: 500, // 减少单次传输量
}

quorumConfig := &QuorumConfig{
    Timeout:    60 * time.Second,  // 大集群需要更长超时
    RetryCount: 5,                 // 更多重试
}
```

---

## 🔧 可选：配置验证工具

为了帮助用户选择合适的配置，可以提供验证工具：

```go
// ValidateConfig 验证配置参数合理性
func ValidateGossipConfig(config *GossipConfig) error {
    if config.Interval < 1*time.Second {
        return fmt.Errorf("周期过短（<1s），可能导致网络风暴")
    }
    if config.Interval > 60*time.Second {
        return fmt.Errorf("周期过长（>60s），收敛太慢")
    }
    if config.Fanout < 1 || config.Fanout > 5 {
        return fmt.Errorf("Fanout 应在 1-5 之间")
    }
    if config.Timeout > config.Interval {
        return fmt.Errorf("超时不应大于周期")
    }
    return nil
}
```

---

## 📌 结论

1. **当前代码已支持直接配置**：Gossip、Quorum、2PC 都支持完全自定义配置
2. **无需"四种优先级"**：直接配置更简单、更灵活、更易维护
3. **建议简化文档**：移除 PriorityCritical/High/Normal/Low 概念，改用"配置参数+场景示例"
4. **可配置验证工具**：帮助用户选择合理的配置值

---

**创建时间**: 2026-01-18
**创建者**: AI Agent
**状态**: 💡 建议采纳
