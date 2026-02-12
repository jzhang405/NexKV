# 阶段 3.1：Gossip 协议正确性验证

> Gossip 协议实现分析与验证

**创建时间**：2026-02-12
**分析文件**：`internal/metadata/gossip/peer_selector.go`

---

## Gossip 实现概览

### Peer 选择策略

| 策略 | 类名 | 说明 | 适用场景 |
|--------|--------|------|----------|
| **纯随机** | `RandomPeerSelector` | 完全随机选择 | 小规模集群 |
| **加权随机** | `WeightedRandomPeerSelector` | 基于健康度权重选择 | 生产环境 |
| **轮询** | `RoundRobinPeerSelector` | 顺序轮询 | 特定场景 |

---

## 关键逻辑验证

### 1. 节点选择随机性

**检查点**：✅ 通过

**代码证据**：
```go
// 使用密码学安全的随机数生成器
func cryptoRandInt(max int64) (int64, error) {
    val, err := rand.Int(rand.Reader, max)
    return int64(val), err
}

// RandomPeerSelector 使用
n, err := cryptoRandInt(int64(len(peers)))
return peers[n]
```

**评估**：
- ✅ 使用 `crypto/rand` 而非 `math/rand`
- ✅ 密码学安全
- ✅ 避免伪随机数问题

---

### 2. 版本向量单调性

**检查点**：⚠️ 待确认

Gossip 协议依赖版本号来决定新旧状态，需要验证：
- [ ] 版本号是否单调递增
- [ ] 并发场景下版本号是否唯一
- [ ] 时钟漂移处理

**建议验证**：
1. 查看 MVStore 中的版本号生成逻辑
2. 确认使用 HLC（混合逻辑时钟）

---

### 3. 消息去重机制

**检查点**：⚠️ 需确认

需要验证 Gossip 是否有：
- [ ] 消息 ID 生成
- [ ] 已处理消息缓存
- [ ] 去重逻辑

**建议**：查看 `merkle_sync.go` 中的去重实现

---

## Peer 健康度评分系统

### 评分维度

| 维度 | 权重 | 评分逻辑 |
|--------|--------|----------|
| **延迟** | 30% | ≤10ms=100, ≤50ms=80, ≤100ms=60, ≤500ms=40, >500ms=20 |
| **成功率** | 30% | 0% → 0分, 100% → 100分 |
| **负载** | 20% | 反向：10000 → 0分, 0 → 100分 |
| **新鲜度** | 20% | ≤1min=100, ≤5min=80, ≤30min=60, >30min=40 |

**总评分** = 延迟×0.3 + 成功率×0.3 + 负载×0.2 + 新鲜度×0.2

**评估**：
- ✅ 多维度综合评估
- ✅ 权重分配合理
- ⚠️ 负载评分可能不准确（未实际测量）

---

## 并发安全分析

### PeerHealthMetrics 并发保护

```go
type PeerHealthMetrics struct {
    mu    sync.RWMutex
    // ... 各种 map
}

func (p *PeerHealthMetrics) Update(peerID string, result *SyncResult) {
    p.mu.Lock()
    defer p.mu.Unlock()
    // 更新逻辑
}
```

**评估**：✅ 正确
- 使用 RWMutex 保护读写
- defer 解锁避免 panic 死锁

---

## 加权随机算法分析

### 轮盘赌算法实现

```go
// 计算总权重
totalWeight := 0.0
weights := make([]float64, len(peers))
for i, peerID := range peers {
    weights[i] = w.metrics.GetScore(peerID)
    totalWeight += weights[i]
}

// 归一化到 [0, totalWeight)
randMax := int64(totalWeight * 1000)
randInt, err := cryptoRandInt(randMax)
r := float64(randInt) / 1000.0

// 轮盘赌选择
for i, peerID := range peers {
    r -= weights[i]
    if r <= 0 {
        return peerID
    }
}
```

**评估**：
- ✅ 加权随机实现正确
- ✅ 使用 1000 倍精度避免浮点截断
- ✅ 使用加密安全随机数

---

## 观察与发现

### ✅ 设计优点

1. **密码学安全随机数**：使用 `crypto/rand`
2. **多维度健康评分**：延迟、成功率、负载、新鲜度
3. **策略模式可扩展**：`PeerSelector` 接口设计
4. **并发安全**：使用 RWMutex 保护共享状态

### ⚠️ 需要确认的点

1. **版本号机制**：未在当前文件中看到，需要查看 MerkleSync
2. **消息去重**：未在当前文件中看到
3. **实际负载测量**：当前使用默认值

### 📌 建议补充

| 优先级 | 建议 | 预估工时 |
|--------|--------|------------|
| P2 | 实际测量网络延迟 | 1 天 |
| P2 | 验证消息去重机制 | 0.5 天 |
| P3 | 添加更多选择策略（如地理位置） | 2 天 |

---

## 下一步

→ [阶段 3.2：Quorum 机制验证](phase3_quorum_verification.md)
