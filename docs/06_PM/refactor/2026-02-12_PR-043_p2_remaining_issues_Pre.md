# PR-043: 剩余 P2 问题修复 - Pre 文档

> **文档类型**: Pre 文档（需求+设计+风险评估）  
> **创建日期**: 2026-02-12  
> **目标分支**: main  
> **工作分支**: feature/fix-p2-remaining-issues

---

## 1. 需求背景

### 1.1 问题来源

根据 `docs/09-code-review/2026-02-12-findings-report.md` 和 `docs/09-code-review/2026-02-12-remaining-issues-report.md`，剩余待修复问题：

| 问题编号 | 问题描述 | 优先级 | 预估工作量 |
|---------|---------|--------|-----------|
| **P1-2** | HLC 实现未审查 | P1 | 1 天 |
| **P2-8** | 缺少实际负载测量 | P2 | 0.5 天 |
| **P2-9** | Gossip 消息队列深度监控 | P2 | 0.5 天 |

### 1.2 P1-2: HLC 实现审查（已完成）

**审查结果**: ✅ HLC 实现正确

| 检查项 | 状态 |
|--------|------|
| 物理时钟单调性 | ✅ 正确 |
| 偏移量计算逻辑 | ✅ 正确 |
| 最大时钟偏移处理 | ✅ 正确 |
| 并发安全 | ✅ 正确 |
| 测试覆盖 | ✅ 完善 |

**HLC 核心算法**:
```go
// pt' = max(now, pt, eventTime, remoteHLC.pt)
// c' = (pt' == pt && pt' == remoteHLC.pt) ? max(c, remoteHLC.c) + 1 : 0
```

---

## 2. P2-8: 实际负载测量

### 2.1 问题详情

**当前状态** (`internal/metadata/gossip/peer_selector.go:82-92`):
```go
// TODO: 实际测量延迟和负载
// 当前使用默认值
if _, exists := p.latency[peerID]; !exists {
    p.latency[peerID] = 50 * time.Millisecond // 默认 50ms
}
if _, exists := p.successRate[peerID]; !exists {
    p.successRate[peerID] = 0.8 // 默认 80% 成功率
}
if _, exists := p.load[peerID]; !exists {
    p.load[peerID] = 1000 // 默认负载 1000
}
```

**影响**: 健康度评分不准确，影响节点选择质量

### 2.2 修复方案

#### 方案 A: 基于 SyncResult 更新指标

```go
func (p *PeerHealthMetrics) Update(peerID string, result *SyncResult) {
    if result == nil || !result.IsSynced() {
        // 同步失败，降低成功率
        p.updateSuccessRate(peerID, false)
        return
    }

    p.mu.Lock()
    defer p.mu.Unlock()

    // 更新延迟（实际测量）
    if result.Latency > 0 {
        p.latency[peerID] = result.Latency
    }

    // 更新负载（基于同步的 Key 数量）
    if result.KeyCount > 0 {
        // 估算负载: Key 数量 * 系数
        estimatedLoad := uint64(result.KeyCount * 10)
        p.load[peerID] = estimatedLoad
    }

    // 更新成功率
    p.updateSuccessRate(peerID, true)

    // 重新计算评分
    p.scores[peerID] = p.calculateScore(peerID)
}
```

#### 方案 B: 添加定时主动探测

```go
func (p *PeerHealthMetrics) StartProber(ctx context.Context, peers []string, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            for _, peerID := range peers {
                go p.probePeer(ctx, peerID)
            }
        case <-ctx.Done():
            return
        }
    }
}

func (p *PeerHealthMetrics) probePeer(ctx context.Context, peerID string) {
    start := time.Now()
    
    // 发送 ping 请求
    // ... 测量延迟 ...
    
    latency := time.Since(start)
    p.mu.Lock()
    p.latency[peerID] = latency
    p.mu.Unlock()
}
```

### 2.3 建议方案

**推荐方案 A**: 基于 SyncResult 更新指标
- 简单直接，利用现有数据
- 无需额外的网络开销
- 与 Gossip 同步自然集成

---

## 3. P2-9: Gossip 消息队列深度监控

### 3.1 问题详情

**当前状态**: 缺少队列深度监控指标

**影响**: 消息积压可能导致同步延迟

### 3.2 修复方案

#### 添加队列监控指标

```go
type GossipSync struct {
    // ... 现有字段 ...
    
    // 队列监控指标
    queueDepthGauge prometheus.Gauge
    queueDepthHist  prometheus.Histogram
}

func (gs *GossipSync) getQueueDepth() int {
    gs.rateLimiterMu.Lock()
    defer gs.rateLimiterMu.Unlock()
    
    // 计算等待处理的队列深度
    // 可以基于 pendingMessages 或其他指标
    return len(gs.pendingMessages)
}

func (gs *GossipSync) monitorQueueDepth(ctx context.Context) {
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            depth := gs.getQueueDepth()
            
            // 更新 Prometheus 指标
            gs.queueDepthGauge.Set(float64(depth))
            
            // 检查是否需要告警
            if depth > 1000 {
                logging.WithField("queue_depth", depth).Warn("Gossip 队列深度过高")
            }
            
        case <-ctx.Done():
            return
        }
    }
}
```

#### 添加队列深度告警

```go
const (
    // QueueDepthWarning 告警阈值
    QueueDepthWarning = 1000
    // QueueDepthCritical 严重告警阈值
    QueueDepthCritical = 5000
)

func (gs *GossipSync) checkQueueDepth(depth int) {
    if depth > QueueDepthCritical {
        logging.WithField("queue_depth", depth).Error("Gossip 队列深度严重过高")
        // 触发告警
    } else if depth > QueueDepthWarning {
        logging.WithField("queue_depth", depth).Warn("Gossip 队列深度过高")
    }
}
```

---

## 4. 实施计划

### 4.1 P1-2: HLC 审查（已完成）

| 步骤 | 操作 | 状态 |
|------|------|------|
| 1 | 代码审查 | ✅ 完成 |
| 2 | 运行测试 | ✅ 通过 |
| 3 | 并发测试 | ✅ 通过 |

### 4.2 P2-8: 负载测量

| 步骤 | 操作 | 预期产出 |
|------|------|---------|
| 1 | 扩展 SyncResult 结构 | 添加 Latency 和 KeyCount 字段 |
| 2 | 实现 Update 方法 | 实际测量延迟和负载 |
| 3 | 添加单元测试 | 验证指标更新逻辑 |

### 4.3 P2-9: 队列监控

| 步骤 | 操作 | 预期产出 |
|------|------|---------|
| 1 | 添加队列深度计数器 | int 字段 |
| 2 | 实现监控 Goroutine | 定时报告队列深度 |
| 3 | 添加告警逻辑 | 阈值告警 |

---

## 5. 风险评估

### 5.1 技术风险

| 风险项 | 风险等级 | 缓解措施 |
|--------|---------|---------|
| **负载测量不准** | 低 | 基于实际同步数据，持续更新 |
| **队列监控开销** | 低 | 仅读取计数器，性能影响小 |
| **告警误报** | 低 | 合理设置阈值 |

### 5.2 兼容性风险

无兼容性风险，仅内部优化。

---

## 6. 验收标准

### 6.1 P1-2 HLC 审查

- [x] HLC 算法实现正确
- [x] 测试覆盖完整
- [x] 并发安全验证通过

### 6.2 P2-8 负载测量

- [ ] 延迟基于实际测量
- [ ] 负载基于 Key 数量估算
- [ ] 指标自动更新

### 6.3 P2-9 队列监控

- [ ] 队列深度可观测
- [ ] 超阈值自动告警
- [ ] 监控开销可忽略

---

## 7. 预估工作量

| 任务 | 预估时间 |
|------|---------|
| P1-2 HLC 审查 | 1 小时 ✅ 已完成 |
| P2-8 负载测量 | 0.5 小时 |
| P2-9 队列监控 | 0.5 小时 |
| **总计** | **1.5 小时** |

---

**Pre 文档状态**: ⏸️ 待架构师评审

---

**文档版本**: v1.0  
**创建者**: 🤖 AI 核心开发  
**评审状态**: ⏳ 待评审
