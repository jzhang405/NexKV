# Day 1 上午：DDD 架构原则培训

> **培训时间**: 3小时（09:00-12:00）
> **培训内容**: DDD 领域驱动设计 + NexKV 5层架构

---

## 一、DDD 领域驱动设计概述（30分钟）

### 1.1 什么是 DDD？

**领域驱动设计（Domain-Driven Design，DDD）** 是一种软件开发方法论，强调：
- **以业务领域为中心**：代码结构反映业务领域
- **统一语言（Ubiquitous Language）**：开发人员和业务人员使用相同的术语
- **分层架构**：将系统分为不同的层次，每层有明确的职责

### 1.2 DDD 核心概念

| 概念 | 说明 | NexKV 示例 |
|------|------|-----------|
| **领域（Domain）** | 业务问题的范围 | 分布式 KV 存储 |
| **子域（Subdomain）** | 领域的分解 | 存储引擎、网络通信、一致性管理 |
| **限界上下文（Bounded Context）**| 子域的边界 | 基础设施层、存储引擎层、数据平面层 |
| **聚合（Aggregate）** | 一组关联的对象 | Shard（分片）包含多个 Replica |
| **聚合根（Aggregate Root）** | 聚合的入口 | Shard 是聚合根，Replica 是实体 |
| **领域事件（Domain Event）** | 领域中发生的事情 | ShardCreated、ReplicaAdded |

### 1.3 为什么要用 DDD？

**传统架构的问题**：
- ❌ 代码按技术分层（Controller、Service、DAO），不反映业务
- ❌ 业务逻辑分散，难以理解
- ❌ 依赖关系混乱，难以测试和维护

**DDD 架构的优势**：
- ✅ 代码按业务领域组织，易于理解
- ✅ 业务逻辑集中在领域层，易于维护
- ✅ 依赖关系清晰，易于测试

---

## 二、NexKV 5层架构设计（60分钟）

### 2.1 5层架构概览

```
┌─────────────────────────────────────┐
│      Layer 1: API 层（2个接口）      │  ← 对外接口
├─────────────────────────────────────┤
│  Layer 2: 控制平面层（14个接口）     │  ← 集群管理
├─────────────────────────────────────┤
│  Layer 3: 数据平面层（6个接口）      │  ← 数据一致性
├─────────────────────────────────────┤
│  Layer 4: 存储引擎层（9个接口）      │  ← 数据持久化
├─────────────────────────────────────┤
│ Layer 5: 基础设施层（16个接口）      │  ← 网络和通信
└─────────────────────────────────────┘
```

### 2.2 各层职责

#### Layer 1: API 层

**职责**: 对外提供统一的 API 接口

**接口**（2个）:
- `KVClient`: KV 操作接口（Get、Put、Delete）
- `TxClient`: 事务接口（Begin、Commit、Rollback）

**示例代码**:
```go
// KVClient KV 客户端接口
type KVClient interface {
    // Get 获取键值
    Get(ctx context.Context, key string) AsyncOperation[[]byte]
    
    // Put 写入键值
    Put(ctx context.Context, key string, value []byte) AsyncOperation[error]
    
    // Delete 删除键值
    Delete(ctx context.Context, key string) AsyncOperation[error]
}
```

---

#### Layer 2: 控制平面层

**职责**: 集群管理和协调

**接口**（14个）:
- `TreeCoordinator`: 元数据树协调器
- `NodeManager`: 节点管理器
- `Topology`: 拓扑管理器
- `HAController`: 高可用控制器
- `MetadataStore`: 元数据存储
- `Heartbeat`: 心跳管理
- `GroupManager`: 组管理器
- `Partitioner`: 分区器
- `Election`: 选举管理
- `LoadBalancer`: 负载均衡
- `Broadcaster`: 广播器
- `SecurityLayer`: 安全层

**示例代码**:
```go
// NodeManager 节点管理器接口
type NodeManager interface {
    // AddNode 添加节点
    AddNode(ctx context.Context, node *Node) error
    
    // RemoveNode 移除节点
    RemoveNode(ctx context.Context, nodeID string) error
    
    // GetNode 获取节点信息
    GetNode(nodeID string) (*Node, error)
}
```

---

#### Layer 3: 数据平面层

**职责**: 数据一致性管理

**接口**（6个）:
- `Replicator`: 副本复制器
- `ReplicationFuture`: 复制 Future
- `QuorumReplicator`: Quorum 复制器
- `ECReplicator`: EC 复制器
- `TxManager`: 事务管理器
- `TxCoordinator`: 事务协调器

**示例代码**:
```go
// Replicator 副本复制器接口
type Replicator interface {
    // Replicate 复制数据到多个副本
    Replicate(ctx context.Context, shard *Shard, data []byte) AsyncOperation[ReplicationResult]
}
```

---

#### Layer 4: 存储引擎层

**职责**: 数据持久化

**接口**（9个）:
- `KVStore`: KV 存储接口
- `WAL`: Write-Ahead Log
- `BTree`: B+ 树索引
- `Iterator`: 迭代器
- `StorageFuture`: 存储 Future
- `LocalTransaction`: 本地事务
- `LocalStorage`: 本地存储
- `BlockDevice`: 块设备
- `CloudStorage`: 云存储

**示例代码**:
```go
// KVStore KV 存储接口
type KVStore interface {
    // Get 获取键值
    Get(key string) ([]byte, error)
    
    // Put 写入键值
    Put(key string, value []byte) error
    
    // Delete 删除键值
    Delete(key string) error
    
    // Scan 范围扫描
    Scan(start, end string) (Iterator, error)
}
```

---

#### Layer 5: 基础设施层

**职责**: 网络通信和底层支持

**接口**（16个）:
- `Transport`: 网络传输
- `Message`: 消息抽象
- `Stream`: 流抽象
- `Channel`: 通道抽象
- `Requestor`: 请求器
- `Codec`: 编解码器
- `SecurityLayer`: 安全层
- `TLSManager`: TLS 管理器
- `NoiseManager`: Noise 管理器
- `Middleware`: 中间件链
- `BatchReplicator`: 批量复制器
- `Pipeline`: 流水线
- `CacheLayer`: 缓存层
- `CircuitBreaker`: 熔断器
- `RetryPolicy`: 重试策略
- `ChaosMonkey`: 混沌测试

**示例代码**:
```go
// Transport 网络传输接口
type Transport interface {
    // Listen 监听连接
    Listen(ctx context.Context) error
    
    // Close 关闭连接
    Close() error
    
    // Send 发送消息
    Send(ctx context.Context, target string, msg Message) AsyncOperation[Message]
}
```

---

### 2.3 依赖倒置原则（DIP）

**核心原则**：高层模块不应该依赖低层模块，两者都应该依赖其抽象。

**错误示例**（违反 DIP）:
```go
// ❌ 错误：控制平面直接依赖具体的存储实现
type NodeManagerImpl struct {
    store *BTreeStore  // 直接依赖具体实现
}
```

**正确示例**（遵循 DIP）:
```go
// ✅ 正确：控制平面依赖抽象接口
type NodeManagerImpl struct {
    store KVStore  // 依赖接口，不依赖具体实现
}
```

**依赖关系图**:
```
API 层
  ↓ 依赖
控制平面层
  ↓ 依赖
数据平面层
  ↓ 依赖
存储引擎层（接口）
  ↓ 依赖
基础设施层（接口）

实现层（infrastructure/）实现存储引擎层和基础设施层接口
```

---

## 三、聚合根和领域事件设计（45分钟）

### 3.1 聚合根设计

**聚合根（Aggregate Root）** 是聚合的入口点，负责维护聚合的一致性。

**NexKV 核心聚合**:
- `Shard`（分片）：聚合根
- `Replica`（副本）：实体
- `Node`（节点）：实体

**示例代码**:
```go
// Shard 分片聚合根
type Shard struct {
    ID          string              // 分片 ID
    Range       KeyRange            // 键范围
    Replicas    []*Replica          // 副本列表
    Status      ShardStatus         // 状态
    Version     int64               // 版本号
    
    // 领域事件
    events      []DomainEvent
}

// AddReplica 添加副本（聚合根负责维护一致性）
func (s *Shard) AddReplica(replica *Replica) error {
    // 业务规则：每个分片最多 3 个副本
    if len(s.Replicas) >= 3 {
        return errors.New("replica limit exceeded")
    }
    
    // 业务规则：副本必须分布在不同节点
    for _, r := range s.Replicas {
        if r.NodeID == replica.NodeID {
            return errors.New("replica already exists on this node")
        }
    }
    
    s.Replicas = append(s.Replicas, replica)
    
    // 发布领域事件
    s.events = append(s.events, ReplicaAddedEvent{
        ShardID:   s.ID,
        ReplicaID: replica.ID,
        NodeID:    replica.NodeID,
        Timestamp: time.Now(),
    })
    
    return nil
}

// GetUncommittedEvents 获取未提交的领域事件
func (s *Shard) GetUncommittedEvents() []DomainEvent {
    return s.events
}

// MarkEventsAsCommitted 标记事件为已提交
func (s *Shard) MarkEventsAsCommitted() {
    s.events = nil
}
```

---

### 3.2 领域事件设计

**领域事件（Domain Event）** 表示领域中发生的事情，用于解耦和通信。

**NexKV 核心领域事件**:
- `ShardCreatedEvent`: 分片创建事件
- `ReplicaAddedEvent`: 副本添加事件
- `ReplicaRemovedEvent`: 副本移除事件
- `NodeJoinedEvent`: 节点加入事件
- `NodeLeftEvent`: 节点离开事件

**示例代码**:
```go
// DomainEvent 领域事件接口
type DomainEvent interface {
    // GetAggregateID 获取聚合 ID
    GetAggregateID() string
    
    // GetEventType 获取事件类型
    GetEventType() string
    
    // GetTimestamp 获取时间戳
    GetTimestamp() time.Time
}

// ReplicaAddedEvent 副本添加事件
type ReplicaAddedEvent struct {
    ShardID   string    `json:"shard_id"`
    ReplicaID string    `json:"replica_id"`
    NodeID    string    `json:"node_id"`
    Timestamp time.Time `json:"timestamp"`
}

func (e ReplicaAddedEvent) GetAggregateID() string {
    return e.ShardID
}

func (e ReplicaAddedEvent) GetEventType() string {
    return "ReplicaAdded"
}

func (e ReplicaAddedEvent) GetTimestamp() time.Time {
    return e.Timestamp
}
```

---

### 3.3 事件发布和订阅

**事件发布**:
```go
// EventBus 事件总线接口
type EventBus interface {
    // Publish 发布事件
    Publish(ctx context.Context, event DomainEvent) error
    
    // Subscribe 订阅事件
    Subscribe(eventType string, handler EventHandler) error
}

// EventHandler 事件处理器
type EventHandler func(ctx context.Context, event DomainEvent) error
```

**使用示例**:
```go
// 发布事件
shard := &Shard{ID: "shard-001"}
shard.AddReplica(&Replica{ID: "replica-001", NodeID: "node-001"})

// 将事件发布到事件总线
for _, event := range shard.GetUncommittedEvents() {
    eventBus.Publish(ctx, event)
}

shard.MarkEventsAsCommitted()
```

---

## 四、接口与实现分离（30分钟）

### 4.1 接口定义（domain 层）

**位置**: `internal/domain/service/`

**示例**: Transport 接口
```go
// internal/domain/service/transport.go
package service

// Transport 网络传输接口
type Transport interface {
    // Listen 监听连接
    Listen(ctx context.Context) error
    
    // Close 关闭连接
    Close() error
    
    // Send 发送消息
    Send(ctx context.Context, target string, msg Message) AsyncOperation[Message]
}
```

---

### 4.2 接口实现（infrastructure 层）

**位置**: `internal/infrastructure/transport/`

**示例**: libp2p Transport 实现
```go
// internal/infrastructure/transport/libp2p_transport_impl.go
package transport

import (
    "context"
    "github.com/libp2p/go-libp2p"
    "github.com/libp2p/go-libp2p/core/host"
)

// Libp2pTransportImpl libp2p 传输实现
type Libp2pTransportImpl struct {
    host host.Host
}

// NewLibp2pTransport 创建 libp2p 传输实例
func NewLibp2pTransport() (*Libp2pTransportImpl, error) {
    h, err := libp2p.New()
    if err != nil {
        return nil, err
    }
    
    return &Libp2pTransportImpl{host: h}, nil
}

// Listen 监听连接（实现 Transport 接口）
func (t *Libp2pTransportImpl) Listen(ctx context.Context) error {
    // libp2p 具体实现
    return nil
}

// Close 关闭连接（实现 Transport 接口）
func (t *Libp2pTransportImpl) Close() error {
    return t.host.Close()
}

// Send 发送消息（实现 Transport 接口）
func (t *Libp2pTransportImpl) Send(ctx context.Context, target string, msg service.Message) service.AsyncOperation[service.Message] {
    // libp2p 具体实现
    return nil
}
```

---

### 4.3 依赖注入（Wire）

**使用 Wire 进行依赖注入**:
```go
// wire/wire.go
//go:build wireinject

package main

import (
    "github.com/google/wire"
    "github.com/jzhang405/NexKV/internal/domain/service"
    "github.com/jzhang405/NexKV/internal/infrastructure/transport"
)

// InitializeTransport 初始化 Transport（Wire 自动生成）
func InitializeTransport() service.Transport {
    wire.Build(
        transport.NewLibp2pTransport,
    )
    return nil
}
```

**生成的代码**（`wire/wire_gen.go`）:
```go
// Code generated by Wire. DO NOT EDIT.

//go:generate go run github.com/google/wire/cmd/wire
//go:build !wireinject

package main

import (
    "github.com/jzhang405/NexKV/internal/domain/service"
    "github.com/jzhang405/NexKV/internal/infrastructure/transport"
)

// InitializeTransport 初始化 Transport（Wire 自动生成）
func InitializeTransport() service.Transport {
    t, err := transport.NewLibp2pTransport()
    if err != nil {
        panic(err)
    }
    return t
}
```

---

## 五、实践环节（30分钟）

### 5.1 设计一个聚合根

**练习**: 设计 `Node` 聚合根

**要求**:
1. Node 包含以下属性：
   - ID: 节点 ID
   - Address: 节点地址
   - Status: 节点状态（Online、Offline）
   - Shards: 该节点上的分片列表

2. 实现以下方法：
   - `AddShard(shardID string)`: 添加分片
   - `RemoveShard(shardID string)`: 移除分片
   - `MarkOffline()`: 标记节点为离线

3. 发布以下领域事件：
   - `ShardAssignedEvent`: 分片分配事件
   - `NodeOfflineEvent`: 节点离线事件

---

### 5.2 设计一个领域服务

**练习**: 设计 `ShardManager` 领域服务

**要求**:
1. `ShardManager` 负责管理分片的创建、迁移和删除
2. 实现以下方法：
   - `CreateShard(keyRange KeyRange)`: 创建分片
   - `MigrateShard(shardID, targetNode string)`: 迁移分片
   - `DeleteShard(shardID string)`: 删除分片

---

## 六、总结和 Q&A（15分钟）

### 6.1 关键要点

1. ✅ **DDD 以业务领域为中心**：代码结构反映业务领域
2. ✅ **5层架构依赖倒置**：高层依赖抽象，不依赖具体实现
3. ✅ **聚合根维护一致性**：聚合根负责维护聚合的业务规则
4. ✅ **领域事件解耦**：通过事件实现模块间的松耦合
5. ✅ **接口与实现分离**：domain 层定义接口，infrastructure 层实现

### 6.2 常见问题

**Q1: 为什么要用 DDD？**
A: 代码按业务领域组织，易于理解和维护，适合复杂业务系统。

**Q2: 5层架构会不会太复杂？**
A: 层次分明，职责清晰，反而降低了理解难度。每层独立开发，易于测试。

**Q3: 依赖倒置的好处是什么？**
A: 高层不依赖低层具体实现，可以轻松替换底层实现，易于测试和维护。

---

## 七、课后阅读

**推荐阅读**:
1. 《领域驱动设计》- Eric Evans
2. 《实现领域驱动设计》- Vaughn Vernon
3. NexKV Pre 文档 v1.5（`docs/06_PM/feature/2026-02-18_PR-nexkv-ddd-architecture_Pre.md`）
4. NexKV 接口定义 v18.0（`docs/07_spike/2026-02-18_spike-nexkv-ddd-interface.md`）

---

**培训师**: 架构师
**培训日期**: 2026-02-18
**文档版本**: v1.0
