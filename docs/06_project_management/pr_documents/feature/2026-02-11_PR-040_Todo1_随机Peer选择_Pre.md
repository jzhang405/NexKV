# 【PR Pre 文档】PR-040 Todo1 - 随机 Peer 选择优化

> **PR 类型**: feature
> **创建日期**: 2026-02-11
> **状态**: 📋 待评审
> **关联任务**: Task 3.5 后续 TODO 1

---

## 第一部分：需求与设计

### 1.1 背景与问题

**当前问题**：

在 `internal/metadata/gossip/merkle_sync.go:246` 处有 TODO 注释：

```go
// 随机选择一个 peer（简化实现）
// TODO: 实现真正的随机选择
for peerID := range s.knownPeers {
    result, err := s.SyncWithPeer(ctx, peerID)
    // ...
}
```

**核心问题**：

1. **伪随机选择**：当前使用 `for peerID := range s.knownPeers` 遍历 map，Go 语言中 map 遍历顺序是随机的，但不是真正的均匀分布

2. **无权重策略**：所有 peer 被同等对待，无法根据：
   - 网络延迟
   - 历史成功率
   - 负载情况
   - 地理位置

3. **无轮询备用**：如果随机选择的 peer 不可用，会直接跳过，没有备用机制

**依赖关系**：

- Task 3.5 (Gossip 差异响应机制) 已完成
- 本任务独立实施，不阻塞其他 TODO

---

### 1.2 目标

#### 功能目标

- [ ] 实现真正的加权随机选择算法
- [ ] 添加 Peer 健康度评分机制
- [ ] 实现 Peer 选择备用策略
- [ ] 添加 Peer 选择统计信息

#### 质量目标

| 指标 | 目标值 | 当前状态 |
|------|--------|----------|
| 测试覆盖率 | ≥ 80% | ~45% (需提升) |
| Lint issues | 0 | 待验证 |
| 编译状态 | 通过 | 待验证 |
| 性能 | 选择耗时 < 1ms | 待验证 |

**当前 metadata 目录覆盖率分析**：

| 模块 | 覆盖率 | 优先级 |
|--------|---------|--------|
| internal/metadata/api | 58.9% | 中 |
| internal/metadata/cluster | 58.9% | 中 |
| internal/metadata/gossip | 21.9% | **高** 🔴 |
| internal/metadata/kvstore | 77.4% | 低 |
| internal/metadata/kvstore/hash | 11.1% | **高** 🔴 |
| internal/metadata/quorum | 58.3% | 中 |
| internal/metadata/types | 31.4% | **高** 🔴 |

**测试覆盖率提升策略**：
- 恢复 gossip 模块单元测试（merkle_sync_test.go）
- 重点关注 gossip、kvstore/hash、types 模块
- 目标：整体提升到 80%

#### 验收标准

- [ ] 随机选择算法符合均匀分布
- [ ] 支持加权策略（可配置）
- [ ] Peer 健康度评分正确
- [ ] 单元测试覆盖率 ≥ 80%
- [ ] 集成测试通过
- [ ] 代码审查通过

---

## 第二部分：技术方案

### 2.1 架构设计

#### 2.1.1 Peer 选择策略

**策略 1: 纯随机选择（Pure Random）**

```go
// RandomPeerSelection 纯随机选择
type RandomPeerSelection struct{}

func (r *RandomPeerSelection) Select(peers []string) string {
    if len(peers) == 0 {
        return ""
    }
    return peers[rand.Intn(len(peers))]
}
```

**策略 2: 加权随机选择（Weighted Random）**

```go
// WeightedPeerSelection 加权随机选择
type WeightedPeerSelection struct {
    scores map[string]float64  // Peer ID → 权重分数
    mu     sync.RWMutex
}

func (w *WeightedPeerSelection) Select(peers []string) string {
    // 根据权重选择 peer（权重越高，选中概率越大）
    totalWeight := 0.0
    for _, peerID := range peers {
        totalWeight += w.getScore(peerID)
    }

    // 轮盘赌选择
    r := rand.Float64() * totalWeight
    for _, peerID := range peers {
        r -= w.getScore(peerID)
        if r <= 0 {
            return peerID
        }
    }
    return ""
}

// getScore 获取 peer 权重分数
func (w *WeightedPeerSelection) getScore(peerID string) float64 {
    w.mu.RLock()
    defer w.mu.RUnlock()
    if score, ok := w.scores[peerID]; ok {
        return score
    }
    return 1.0  // 默认权重
}
```

**策略 3: 轮询选择（Round Robin）**

```go
// RoundRobinPeerSelection 轮询选择
type RoundRobinPeerSelection struct {
    peers   []string
    index   int
    mu      sync.Mutex
}

func (r *RoundRobinPeerSelection) Select(peers []string) string {
    r.mu.Lock()
    defer r.mu.Unlock()

    if len(r.peers) == 0 || !equalPeers(r.peers, peers) {
        r.peers = peers
        r.index = 0
    }

    peer := r.peers[r.index]
    r.index = (r.index + 1) % len(r.peers)
    return peer
}
```

---

#### 2.1.2 Peer 健康度评分

**评分维度**：

```go
// PeerHealthMetrics Peer 健康度指标
type PeerHealthMetrics struct {
    // 网络延迟（越低越好）
    Latency map[string]time.Duration

    // 历史成功率（越高越好）
    SuccessRate map[string]float64

    // 负载情况（越低越好）
    Load map[string]uint64

    // 最后同步时间（用于判断是否过时）
    LastSyncTime map[string]time.Time
}

// CalculateScore 计算综合评分
func (p *PeerHealthMetrics) CalculateScore(peerID string) float64 {
    p.mu.RLock()
    defer p.mu.RUnlock()

    latency := p.getLatencyScore(peerID)
    success := p.getSuccessScore(peerID)
    load := p.getLoadScore(peerID)
    freshness := p.getFreshnessScore(peerID)

    // 加权平均
    return latency*0.3 + success*0.3 + load*0.2 + freshness*0.2
}

// getLatencyScore 延迟评分（0-100）
func (p *PeerHealthMetrics) getLatencyScore(peerID string) float64 {
    latency, ok := p.Latency[peerID]
    if !ok {
        return 50.0  // 默认中等分数
    }

    // < 10ms = 100 分
    // < 50ms = 80 分
    // < 100ms = 60 分
    // < 500ms = 40 分
    // >= 500ms = 20 分
    switch {
    case latency < 10*time.Millisecond:
        return 100.0
    case latency < 50*time.Millisecond:
        return 80.0
    case latency < 100*time.Millisecond:
        return 60.0
    case latency < 500*time.Millisecond:
        return 40.0
    default:
        return 20.0
    }
}
```

---

#### 2.1.3 接口设计

```go
// PeerSelector Peer 选择器接口
type PeerSelector interface {
    // Select 从候选 peer 列表中选择一个
    Select(peers []string) string

    // Update 更新 peer 健康度指标
    Update(peerID string, result *SyncResult)

    // String 返回策略名称
    String() string
}
```

---

### 2.2 实施计划

#### 2.2.1 任务分解

| 任务 | 内容 | 预计时间 |
|------|------|------------|
| **任务 1** | 定义 PeerSelector 接口和基础结构 | 0.5 天 |
| **任务 2** | 实现 RandomPeerSelection 策略 | 0.5 天 |
| **任务 3** | 实现 WeightedPeerSelection 策略 | 1 天 |
| **任务 4** | 实现 RoundRobinPeerSelection 策略 | 0.5 天 |
| **任务 5** | 实现 PeerHealthMetrics 评分 | 1 天 |
| **任务 6** | 集成到 MerkleGossipSync | 0.5 天 |
| **任务 7** | 添加单元测试 | 0.5 天 |
| **任务 8** | 添加集成测试 | 0.5 天 |

**总计**: 5 天

#### 2.2.2 文件清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `peer_selector.go` | 新建 | Peer 选择器接口和实现 |
| `peer_selector_test.go` | 新建 | Peer 选择器单元测试 |
| `merkle_sync.go` | 修改 | 集成 Peer 选择器，移除 TODO |

---

## 第三部分：风险评估

| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|--------|----------|
| 算法复杂度 | 中 | 中 | 充分测试，使用表格驱动测试 |
| 并发竞态 | 中 | 高 | 使用 sync.RWMutex 保护共享数据 |
| 性能回归 | 低 | 中 | 添加基准测试，确保选择耗时 < 1ms |
| Peer 评分不准确 | 中 | 中 | 实现评分衰减机制，定期重置 |

---

## 第四部分：里程碑与时间线

| 阶段 | 日期 | 交付物 |
|------|------|--------|
| **Phase 1** | Day 1 | 接口设计和基础结构 |
| **Phase 2** | Day 2-3 | 加权随机和轮询策略 |
| **Phase 3** | Day 4 | Peer 健康度评分 |
| **Phase 4** | Day 5 | 集成和测试 |
| **评审** | Day 5 | 架构师评审 |

---

**文档版本**: v1.0
**创建日期**: 2026-02-11
**最后更新**: 2026-02-11
**维护者**: 🤖 核心开发 A
**状态**: 📋 待架构师评审
