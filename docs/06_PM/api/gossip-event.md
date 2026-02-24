# Gossip Event API 文档

> 事件驱动的 Gossip 同步机制

## 概述

Gossip Event API 提供了事件驱动的元数据同步机制，结合事件触发和定时触发两种方式，实现低延迟的最终一致性同步。

### 核心特性

- **事件驱动**：写入操作后立即触发 Gossip，减少同步延迟
- **定时兜底**：周期性 Gossip，保证最终一致性
- **防风暴机制**：通道满时丢弃事件，定时器兜底
- **树感知传播**：基于节点类型（Leaf/Middle/Root）优化传播路径
- **优先级传播**：三级优先级队列（High/Normal/Low）

## 事件类型

### GossipEventType

```go
type GossipEventType int

const (
    EventWrite           // 写入事件
    EventNamespaceChange // Namespace 变更事件
    EventPeerJoin        // 节点加入事件
    EventPeerLeave       // 节点离开事件
    EventBatch           // 批量事件
)
```

### GossipEvent

```go
type GossipEvent struct {
    Type      GossipEventType // 事件类型
    Namespace string          // Namespace（可选）
    Key       string          // Key（可选）
    Value     []byte          // Value（可选）
    NodeID    string          // 节点 ID（可选）
    Timestamp time.Time       // 事件时间戳
}
```

## EventDrivenGossipSync API

### 配置

```go
type EventDrivenConfig struct {
    LocalNodeID   string        // 本地节点 ID
    EventChanSize int           // 事件通道大小（默认 1000）
    TickerDelay   time.Duration // 定时器间隔（默认 10s）
}
```

### 创建实例

```go
sync := NewEventDrivenGossipSync(config)
```

### 事件触发方法

#### OnWrite 写入事件

```go
sync.OnWrite(namespace, key)
```

**参数**：
- `namespace`: 命名空间
- `key`: 键

**行为**：立即触发 Gossip 同步

#### OnNamespaceChange Namespace 变更

```go
sync.OnNamespaceChange(namespace)
```

**参数**：
- `namespace`: 变更的命名空间

#### OnPeerJoin 节点加入

```go
sync.OnPeerJoin(nodeID)
```

**参数**：
- `nodeID`: 加入的节点 ID

#### OnPeerLeave 节点离开

```go
sync.OnPeerLeave(nodeID)
```

**参数**：
- `nodeID`: 离开的节点 ID

### 统计信息

```go
stats := sync.GetStats()
```

**返回**：

```go
type EventDrivenStats struct {
    EventChanSize    int           // 事件通道大小
    EventChanUsed    int           // 已使用事件槽
    TickerDelay      time.Duration // 定时器间隔
    TotalEvents      uint64        // 总事件数
    EventsProcessed  uint64        // 已处理事件数
    EventsDropped    uint64        // 丢弃事件数
    LastEventTime    time.Time     // 最后事件时间
    LastGossipTime   time.Time     // 最后 Gossip 时间
}
```

## TreeAwareGossipSync API

### 节点类型

```go
type NodeType int

const (
    NodeTypeUnknown NodeType = iota // 未知类型
    NodeTypeLeaf                    // 叶子节点
    NodeTypeMiddle                  // 中间节点
    NodeTypeRoot                    // Root 节点
)
```

### 优先级级别

```go
type PriorityLevel int

const (
    PriorityHigh   // 高优先级：向上传播
    PriorityNormal // 普通优先级：向下广播
    PriorityLow    // 低优先级：跨子树同步
)
```

### 配置

```go
type TreeAwareConfig struct {
    *EventDrivenConfig
    HighPriorityChanSize   int // 高优先级通道大小（默认 500）
    NormalPriorityChanSize int // 普通优先级通道大小（默认 300）
    LowPriorityChanSize    int // 低优先级通道大小（默认 200）
    StarvationPrevention   int // 饥饿预防（默认 10）
}
```

### 创建实例

```go
sync := NewTreeAwareGossipSync(config)
```

### 拓扑管理

#### 更新拓扑

```go
sync.UpdateTopology(parent, children, depth)
```

**参数**：
- `parent`: 父节点 ID
- `children`: 子节点 ID 列表
- `depth`: 树深度

**行为**：自动推断节点类型

#### 获取节点类型

```go
nodeType := sync.GetNodeType()
```

#### 传播事件

```go
sync.Propagate(event)
```

**行为**：根据节点类型选择传播策略

| 节点类型 | 传播策略 |
|---------|---------|
| Leaf | 只向父节点传播（高优先级） |
| Middle | 向父节点 + 广播子节点 |
| Root | 只广播子节点（普通优先级） |

### 统计信息

```go
stats := sync.GetTreeAwareStats()
```

**返回**：

```go
type TreeAwareStats struct {
    NodeType             NodeType
    TreeDepth            int
    HighPrioritySent     uint64
    NormalPrioritySent   uint64
    LowPrioritySent      uint64
    HighPriorityDrop     uint64
    NormalPriorityDrop   uint64
    LowPriorityDrop      uint64
    ExpectedDelay        time.Duration
    HighPriorityQueued   int
    NormalPriorityQueued int
    LowPriorityQueued    int
}
```

## BandwidthOptimizer API

### 配置

```go
type BandwidthConfig struct {
    BatchSize            int           // 批量合并大小（默认 50）
    BatchTimeout         time.Duration // 批量等待超时（默认 100ms）
    CompressionThreshold int           // 压缩阈值（默认 1KB）
    EnableCompression    bool          // 是否启用压缩（默认 true）
    MaxBatchSize         int           // 最大批次大小（默认 100）
}
```

### 创建实例

```go
optimizer := NewBandwidthOptimizer(config, merkleSync)
```

### 提交事件

```go
optimizer.Submit(event)
```

### 压缩/解压

```go
compressed, wasCompressed, err := optimizer.CompressIfNeeded(data)
decompressed, err := optimizer.DecompressIfNeeded(data, wasCompressed)
```

### 合并事件

```go
merged := optimizer.MergeEvents(events)
```

**行为**：
- 空列表返回 nil
- 单事件返回原事件
- 同 Key 多事件保留最新
- 多 Key 事件返回批量事件

### 统计信息

```go
stats := optimizer.GetStats()
```

**返回**：

```go
type BandwidthStats struct {
    TotalEvents        uint64
    TotalBatches       uint64
    BatchRatio         float64
    TotalBytesBefore   uint64
    TotalBytesAfter    uint64
    CompressionRatio   float64
    CompressionCount   uint64
    AverageBatchSize   float64
    QueueDepth         int
    PendingBatchEvents int
}
```

## 使用示例

### 场景 1：基本事件驱动

```go
// 1. 创建同步器
config := &EventDrivenConfig{
    LocalNodeID:   "node-1",
    EventChanSize: 1000,
    TickerDelay:   10 * time.Second,
}
sync := NewEventDrivenGossipSync(config)
defer sync.Close()

// 2. 写入后触发
store.Put("ns1", "key1", value)
sync.OnWrite("ns1", "key1")
```

### 场景 2：树感知传播

```go
// 1. 创建树感知同步器
config := &TreeAwareConfig{
    EventDrivenConfig: &EventDrivenConfig{
        LocalNodeID: "middle-1",
    },
}
sync := NewTreeAwareGossipSync(config)
defer sync.Close()

// 2. 设置拓扑（自动推断为中间节点）
sync.UpdateTopology("root-1", []string{"leaf-1", "leaf-2"}, 1)

// 3. 传播事件
event := GossipEvent{
    Type:      EventWrite,
    Namespace: "ns1",
    Key:       "key1",
    Timestamp: time.Now(),
}
sync.Propagate(event)
// 中间节点会同时向父节点（高优先级）和子节点（普通优先级）传播
```

### 场景 3：带宽优化

```go
// 1. 创建优化器
config := &BandwidthConfig{
    BatchSize:         50,
    BatchTimeout:      100 * time.Millisecond,
    EnableCompression: true,
}
optimizer := NewBandwidthOptimizer(config, merkleSync)
defer optimizer.Close()

// 2. 提交事件（自动批处理）
for i := 0; i < 100; i++ {
    optimizer.Submit(GossipEvent{
        Type:      EventWrite,
        Namespace: "ns1",
        Key:       fmt.Sprintf("key%d", i),
    })
}

// 3. 获取批量事件
batchChan := optimizer.GetBatchChan()
for batch := range batchChan {
    // 处理批量事件
    processBatch(batch)
}
```

### 场景 4：节点加入/离开

```go
// 节点加入
sync.OnPeerJoin("new-node-1")

// 节点离开
sync.OnPeerLeave("old-node-1")
```

## 性能优化建议

### 事件通道大小

- 建议设置为预期峰值吞吐量的 2 倍
- 太小会导致事件丢弃
- 太大会增加内存占用

### 批处理配置

- `BatchSize`: 平衡延迟和吞吐
- `BatchTimeout`: 控制最大等待时间
- 压缩阈值: 小数据不压缩避免开销

### 树感知优化

- 叶子节点：带宽最低，延迟最低
- 中间节点：带宽中等，延迟中等
- Root 节点：延迟最高（需等待向上传播）

## 监控指标

建议监控以下指标：

- `gossip_events_total`: 事件总数
- `gossip_events_dropped`: 丢弃事件数
- `gossip_sync_latency_seconds`: 同步延迟
- `gossip_batch_size`: 批处理大小分布
- `gossip_compression_ratio`: 压缩率

## 相关文档

- [Fencing Token API](fencing.md)
- [2PC API](twopc.md)
- [带宽优化实现](../../internal/metadata/gossip/bandwidth.go)
