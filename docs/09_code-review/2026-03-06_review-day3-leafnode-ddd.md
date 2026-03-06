# PR-089 Phase 2.1 Week 1 Day 3 - DDD 架构审查报告

> **审查人**：DDD 架构专家
> **审查日期**：2026-03-06
> **审查范围**：LeafNode 结构定义
> **代码行数**：262 行源码 + 259 行测试

---

## 一、综合评分

| 评估维度 | 评分 | 说明 |
|---------|------|------|
| DDD 分层架构 | 6/10 | 基础设施层实现，但缺少领域层抽象 |
| 领域建模 | 7/10 | 数据结构设计合理，但缺少领域服务 |
| 职责分离 | 7/10 | 单一职责基本遵循，但耦合度较高 |
| 依赖方向 | 9/10 | 依赖方向正确，无循环依赖 |
| 接口设计 | 5/10 | 缺少接口抽象，直接实现具体类型 |

**综合评分：6.8/10**

---

## 二、优点分析 ✅

### 2.1 基础设施层职责清晰

```go
// internal/infrastructure/storage/bftree/leaf_node.go
type LeafNode struct {
    pageID  uint64
    level   PageLevel
    miniPage *MiniPage
    deltas  []*DeltaEntry
    mu      sync.RWMutex
}
```

**✅ 优点**：
- 清晰的基础设施层实现
- 数据结构定义明确
- 并发控制设计合理

### 2.2 依赖方向正确

```
Infrastructure Layer (LeafNode)
    ↓ 无依赖反向
Domain Layer (PageLevel, Config)
```

**✅ 优点**：
- 基础设施层依赖领域层类型（PageLevel）
- 无循环依赖
- 符合 DDD 依赖倒置原则

### 2.3 聚合根设计合理

```go
type LeafNode struct {
    miniPage *MiniPage  // 组合关系
    deltas  []*DeltaEntry // 组合关系
}
```

**✅ 优点**：
- LeafNode 作为聚合根
- MiniPage 和 DeltaEntry 作为值对象
- 生命周期由 LeafNode 管理

---

## 三、问题分析 ❌

### P1 - 重要问题

#### P1-1: 缺少领域层接口抽象

**问题位置**：`internal/infrastructure/storage/bftree/leaf_node.go:14`

**问题描述**：
```go
// 当前实现：直接在基础设施层定义具体类型
type LeafNode struct { ... }

func (n *LeafNode) Get(key []byte) ([]byte, bool)
func (n *LeafNode) Set(key, value []byte) error
```

**影响**：
- 无法进行单元测试（依赖具体实现）
- 无法替换实现（如 Mock、Stub）
- 违反依赖倒置原则（DIP）

**修复建议**：

```go
// 领域层：internal/domain/service/storage.go
package service

// LeafNodeRepository 叶子节点仓储接口
type LeafNodeRepository interface {
    Get(ctx context.Context, nodeID uint64, key []byte) ([]byte, error)
    Set(ctx context.Context, nodeID uint64, key, value []byte) error
    Delete(ctx context.Context, nodeID uint64, key []byte) error
    Compact(ctx context.Context, nodeID uint64) error
}

// 基础设施层：internal/infrastructure/storage/bftree/leaf_node.go
package bftree

type LeafNode struct { ... }

// 实现领域接口
func (n *LeafNode) Get(ctx context.Context, nodeID uint64, key []byte) ([]byte, error) {
    // 实现细节
}
```

**优先级**：P1（建议 Week 2 实现）

---

#### P1-2: 领域逻辑泄露到基础设施层

**问题位置**：`leaf_node.go:224-238`

**问题描述**：
```go
// shouldCompact 是业务规则，应该属于领域层
func (n *LeafNode) shouldCompact() bool {
    const maxDeltaLen = 8
    const maxDeltaSizeRatio = 0.5

    if len(n.deltas) >= maxDeltaLen {
        return true
    }

    maxDeltaSize := uint16(float64(n.miniPage.capacity) * maxDeltaSizeRatio)
    return n.deltaSize >= maxDeltaSize
}
```

**影响**：
- 业务规则硬编码在基础设施层
- 无法动态配置策略
- 违反单一职责原则

**修复建议**：

```go
// 领域层：internal/domain/model/compact_policy.go
package model

// CompactPolicy 合并策略（领域服务）
type CompactPolicy struct {
    MaxDeltaLen       int
    MaxDeltaSizeRatio float64
}

// ShouldCompact 判断是否需要合并（领域逻辑）
func (p *CompactPolicy) ShouldCompact(deltaCount int, deltaSize, capacity uint16) bool {
    if deltaCount >= p.MaxDeltaLen {
        return true
    }

    maxDeltaSize := uint16(float64(capacity) * p.MaxDeltaSizeRatio)
    return deltaSize >= maxDeltaSize
}

// 基础设施层使用领域服务
func (n *LeafNode) shouldCompact() bool {
    return n.config.CompactPolicy.ShouldCompact(
        len(n.deltas),
        n.deltaSize,
        n.miniPage.capacity,
    )
}
```

**优先级**：P1（建议 Week 2 实现）

---

#### P1-3: 缺少领域服务

**问题位置**：架构层面

**问题描述**：
当前实现只有数据结构，缺少领域服务：
- 缺少 `LeafNodeService` 领域服务
- 缺少 `CompactService` 合并服务
- 缺少 `PromotionService` 提升服务

**影响**：
- 业务逻辑分散
- 缺少事务封装
- 难以实现复杂业务流程

**修复建议**：

```go
// 领域层：internal/domain/service/leaf_node_service.go
package service

// LeafNodeService 叶子节点领域服务
type LeafNodeService struct {
    nodeRepo    LeafNodeRepository
    compactPolicy *CompactPolicy
    promotionPolicy *PromotionPolicy
}

// Compact 合并 Delta Chain（业务流程）
func (s *LeafNodeService) Compact(ctx context.Context, nodeID uint64) error {
    // 1. 获取节点
    node, err := s.nodeRepo.Get(ctx, nodeID)
    if err != nil {
        return err
    }

    // 2. 检查是否需要合并
    if !s.compactPolicy.ShouldCompact(node) {
        return nil
    }

    // 3. 执行合并（事务）
    return s.nodeRepo.Compact(ctx, nodeID)
}
```

**优先级**：P1（建议 Week 2-3 实现）

---

### P2 - 改进建议

#### P2-1: 缺少值对象验证

**问题位置**：`leaf_node.go:96-105`

**问题描述**：
```go
func NewLeafNode(pageID uint64, level PageLevel) *LeafNode {
    return &LeafNode{
        pageID: pageID,  // ❌ 无验证
        level:  level,   // ❌ 无验证
    }
}
```

**修复建议**：

```go
func NewLeafNode(pageID uint64, level PageLevel) (*LeafNode, error) {
    // 验证 pageID
    if pageID == 0 {
        return nil, ErrInvalidPageID
    }

    // 验证 level
    if level < L1 || level > PageLevel(7) {
        return nil, ErrInvalidPageLevel
    }

    return &LeafNode{
        pageID:   pageID,
        level:    level,
        version:  1,
        miniPage: NewMiniPage(level),
        deltas:   make([]*DeltaEntry, 0, 8),
        deltaSize: 0,
    }, nil
}
```

---

#### P2-2: 使用 string 比较字节数组

**问题位置**：`leaf_node.go:163, 187`

**问题描述**：
```go
if string(delta.key) == string(key) {  // ❌ 低效
```

**修复建议**：
```go
if bytes.Equal(delta.key, key) {  // ✅ 高效
```

**优先级**：P2（性能优化）

---

#### P2-3: 缺少事件发布机制

**问题位置**：架构层面

**问题描述**：
- 缺少 `DeltaAdded` 事件
- 缺少 `NodeCompacted` 事件
- 难以实现事件驱动架构

**修复建议**：

```go
// 领域层：internal/domain/events/node_events.go
package events

// DeltaAdded Delta 添加事件
type DeltaAdded struct {
    NodeID    uint64
    Delta     *DeltaEntry
    Timestamp time.Time
}

// NodeCompacted 节点合并事件
type NodeCompacted struct {
    NodeID    uint64
    DeltaCount int
    Timestamp time.Time
}

// 基础设施层：发布事件
func (n *LeafNode) Set(key, value []byte) error {
    n.mu.Lock()
    defer n.mu.Unlock()

    delta := &DeltaEntry{...}
    n.deltas = append(n.deltas, delta)

    // 发布领域事件
    n.eventBus.Publish(&events.DeltaAdded{
        NodeID: n.pageID,
        Delta:  delta,
    })

    return nil
}
```

**优先级**：P2（后续 Phase 实现）

---

## 四、架构改进建议

### 4.1 引入应用层

**当前架构**：
```
Domain Layer
    ↓
Infrastructure Layer (LeafNode)
```

**建议架构**：
```
Application Layer (LeafNodeService)
    ↓ 依赖
Domain Layer (LeafNodeRepository, CompactPolicy)
    ↓ 依赖
Infrastructure Layer (LeafNodeImpl)
```

---

### 4.2 领域模型重构

**当前模型**：
```
LeafNode (具体类型)
  ├── MiniPage (值对象)
  └── DeltaEntry (值对象)
```

**建议模型**：
```
LeafNode (聚合根)
  ├── MiniPage (值对象)
  ├── DeltaChain (值对象)
  └── CompactPolicy (领域服务)
```

---

### 4.3 接口抽象

**建议添加的接口**：

```go
// 领域层接口
type NodeRepository interface {
    Get(ctx context.Context, nodeID uint64) (*Node, error)
    Save(ctx context.Context, node *Node) error
    Delete(ctx context.Context, nodeID uint64) error
}

type CompactStrategy interface {
    ShouldCompact(node *Node) bool
    Compact(node *Node) error
}

type PromotionStrategy interface {
    ShouldPromote(node *Node) bool
    Promote(node *Node) (*Node, error)
}
```

---

## 五、最终结论

### 5.1 总体评价

LeafNode 实现是一个**功能完整但架构不完善**的基础设施层实现。

**优势**：
1. ✅ 依赖方向正确
2. ✅ 聚合根设计合理
3. ✅ 并发控制安全

**劣势**：
1. ❌ 缺少领域层接口抽象
2. ❌ 业务逻辑泄露到基础设施层
3. ❌ 缺少领域服务封装

### 5.2 评分详情

| 维度 | 评分 | 说明 |
|------|------|------|
| 分层架构 | 6/10 | 缺少应用层和领域服务 |
| 领域建模 | 7/10 | 数据结构合理，但缺少领域逻辑封装 |
| 职责分离 | 7/10 | 单一职责基本遵循 |
| 依赖方向 | 9/10 | 依赖方向正确 |
| 接口设计 | 5/10 | 缺少接口抽象 |

**综合评分：6.8/10**

### 5.3 审查结论

**⚠️ 有条件通过**

**条件**：
1. P1-1: 添加领域层接口抽象（建议 Week 2）
2. P1-2: 重构业务规则到领域层（建议 Week 2）
3. P1-3: 实现领域服务层（建议 Week 2-3）

### 5.4 下一步行动

**Week 2 行动计划**：
1. 创建 `internal/domain/service/storage.go` 接口定义
2. 创建 `internal/domain/model/compact_policy.go` 业务规则
3. 创建 `internal/application/storage/leaf_node_service.go` 应用服务

---

**审查完成时间**：2026-03-06
**审查结论**：⚠️ 有条件通过（6.8/10）
**下一步**：重构架构，添加领域层抽象
