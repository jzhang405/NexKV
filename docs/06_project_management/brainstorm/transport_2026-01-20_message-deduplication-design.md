# UDP 消息去重设计方案

> **文档类型**: 💡 技术建议
> **创建日期**: 2026-01-20
> **状态**: 📋 待讨论
> **优先级**: P0 (高)

---

## 背景说明

当前 UDP Transport 实现了基于 TLV 协议的消息传输，支持分片重组。但在以下场景存在不足：

1. **Gossip 协议**：同一个消息可能通过多个路径传播到同一节点
2. **网络重传**：UDP 不可靠，应用层可能重传消息
3. **分片乱序**：UDP 不保证顺序，同一消息的不同分片可能乱序到达

**问题**：缺乏消息去重机制，容易造成：
- 重复处理同一消息（浪费 CPU 和内存）
- 分片重组时可能受到重复分片干扰
- 无法有效识别和过滤已处理的消息

---

## 核心设计

### 1. 消息标识

**现有字段**（TLV FixedHeader）：
- `NodeID` (8 bytes): 发送节点ID
- `MsgSeq` (8 bytes): 消息序列号（单调递增）
- `MsgType` (2 bytes): 消息类型
- `CodecID` (2 bytes): 编解码器ID

**唯一标识**：`(NodeID, MsgSeq)` 组合

### 2. 去重机制

#### 2.1 数据结构

```go
// messageKey 消息唯一标识
type messageKey struct {
    nodeID  uint64
    msgSeq  uint64
}

// messageDeduplicator 消息去重器
type messageDeduplicator struct {
    mu sync.RWMutex

    // 去重缓存：最近处理的消息
    // key: (NodeID, MsgSeq)
    // value: 处理时间戳（Unix 秒）
    recentMessages map[messageKey]int64

    // 每个节点的最大 MsgSeq（用于快速判断过时消息）
    // key: NodeID
    // value: 该节点处理过的最大 MsgSeq
    nodeMaxSeq map[uint64]uint64

    // 配置
    windowSize   int           // 滑动窗口大小（秒），默认 60
    maxCacheSize int           // 最大缓存数量，默认 10000
    cleanupTick  time.Duration // 清理间隔，默认 10 秒
    stopCh       chan struct{}
    cleanupWg    sync.WaitGroup
}

// NewMessageDeduplicator 创建消息去重器
func NewMessageDeduplicator(windowSize int, maxCacheSize int) *messageDeduplicator {
    d := &messageDeduplicator{
        recentMessages: make(map[messageKey]int64),
        nodeMaxSeq:     make(map[uint64]uint64),
        windowSize:     windowSize,
        maxCacheSize:   maxCacheSize,
        cleanupTick:    10 * time.Second,
        stopCh:         make(chan struct{}),
    }

    d.cleanupWg.Add(1)
    go d.cleanupLoop()

    return d
}
```

#### 2.2 去重算法

**双重检查机制**：

1. **精确匹配**：检查 `(NodeID, MsgSeq)` 是否在 `recentMessages` 中
2. **范围检查**：检查 `MsgSeq` 是否 ≤ `nodeMaxSeq[NodeID]`

```go
// IsDuplicate 检查是否为重复消息
func (d *messageDeduplicator) IsDuplicate(nodeID, msgSeq uint64) bool {
    d.mu.RLock()
    defer d.mu.RUnlock()

    key := messageKey{nodeID: nodeID, msgSeq: msgSeq}

    // 检查 1: 精确匹配 - 是否在去重缓存中
    if timestamp, exists := d.recentMessages[key]; exists {
        // 检查是否在窗口期内
        if time.Now().Unix()-timestamp < int64(d.windowSize) {
            return true // 在窗口期内，确认为重复
        }
    }

    // 检查 2: 范围检查 - 是否小于等于已处理的最大值
    if maxSeq, ok := d.nodeMaxSeq[nodeID]; ok && msgSeq <= maxSeq {
        return true // MsgSeq 过时，确认为重复
    }

    return false // 不是重复消息
}

// MarkProcessed 标记消息已处理
func (d *messageDeduplicator) MarkProcessed(nodeID, msgSeq uint64) {
    d.mu.Lock()
    defer d.mu.Unlock()

    now := time.Now().Unix()
    key := messageKey{nodeID: nodeID, msgSeq: msgSeq}

    // 添加到去重缓存
    d.recentMessages[key] = now

    // 更新节点的最大 MsgSeq
    if msgSeq > d.nodeMaxSeq[nodeID] {
        d.nodeMaxSeq[nodeID] = msgSeq
    }

    // 检查缓存大小，超过限制时清理最老的条目
    if len(d.recentMessages) > d.maxCacheSize {
        d.evictOldest()
    }
}

// evictOldest 清理最老的缓存条目
func (d *messageDeduplicator) evictOldest() {
    oldestKey := messageKey{}
    oldestTime := int64(1<<63 - 1) // 最大值

    for key, timestamp := range d.recentMessages {
        if timestamp < oldestTime {
            oldestTime = timestamp
            oldestKey = key
        }
    }

    if oldestTime < int64(1<<63-1) {
        delete(d.recentMessages, oldestKey)
    }
}

// cleanupLoop 定期清理过期缓存
func (d *messageDeduplicator) cleanupLoop() {
    defer d.cleanupWg.Done()

    ticker := time.NewTicker(d.cleanupTick)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            d.cleanupExpired()
        case <-d.stopCh:
            return
        }
    }
}

// cleanupExpired 清理过期的缓存条目
func (d *messageDeduplicator) cleanupExpired() {
    d.mu.Lock()
    defer d.mu.Unlock()

    now := time.Now().Unix()
    expiredKeys := make([]messageKey, 0)

    // 找出所有过期的 key
    for key, timestamp := range d.recentMessages {
        if now-timestamp >= int64(d.windowSize) {
            expiredKeys = append(expiredKeys, key)
        }
    }

    // 删除过期的 key
    for _, key := range expiredKeys {
        delete(d.recentMessages, key)
    }

    if len(expiredKeys) > 0 {
        logging.Debugf("清理过期去重缓存: %d 个", len(expiredKeys))
    }
}

// Stop 停止去重器
func (d *messageDeduplicator) Stop() {
    close(d.stopCh)
    d.cleanupWg.Wait()
}
```

#### 2.3 分片消息去重

**分片级别去重**（在 `addFragment` 中）：

```go
// addFragment 添加分片并检查是否完整
func (b *fragmentBuffer) addFragment(
    key fragmentKey,
    total, index uint16,
    data []byte,
    msgType MessageType,
    codecID uint16,
    deduplicator *messageDeduplicator,
) Message {
    b.mu.Lock()
    defer b.mu.Unlock()

    // 获取或创建 partialMessage
    partial, exists := b.buffers[key]
    if !exists {
        // 检查该消息是否已处理过（完整消息级别去重）
        if deduplicator != nil && deduplicator.IsDuplicate(key.nodeID, key.msgID) {
            logging.Debugf("消息已处理，丢弃分片: nodeID=%d, msgID=%d", key.nodeID, key.msgID)
            return nil
        }

        partial = &partialMessage{
            total:      total,
            received:   0,
            fragments:  make([][]byte, total),
            lastUpdate: time.Now(),
            msgType:    msgType,
            codecID:    codecID,
        }
        b.buffers[key] = partial
    }

    // 验证分片索引是否有效
    if int(index) >= int(total) {
        logging.Warnf("分片索引越界: index=%d, total=%d", index, total)
        return nil
    }

    // 分片级别去重：检查该分片是否已存在
    if partial.fragments[index] != nil {
        logging.Debugf("重复分片: nodeID=%d, msgID=%d, index=%d", key.nodeID, key.msgID, index)
        return nil // 分片已存在，丢弃
    }

    // 存储分片
    partial.fragments[index] = data
    partial.received++
    partial.lastUpdate = time.Now()

    // 检查是否收齐所有分片
    if partial.received == partial.total {
        // 重组消息
        reassembled := b.reassembleMessage(partial)

        // 删除缓冲区
        delete(b.buffers, key)

        // 标记消息已处理（完整消息级别去重）
        if deduplicator != nil {
            deduplicator.MarkProcessed(key.nodeID, key.msgID)
        }

        // 使用保存的 MsgType 和 CodecID 解码消息
        codec, err := NewCodec(types.CodecType(partial.codecID))
        if err != nil {
            logging.Warnf("创建编解码器失败: %v", err)
            return nil
        }

        msg, err := codec.Decode(partial.msgType, reassembled)
        if err != nil {
            logging.Warnf("解码重组消息失败: %v", err)
            return nil
        }

        logging.Debugf("分片重组成功: nodeID=%d, msgID=%d, total=%d", key.nodeID, key.msgID, total)
        return msg
    }

    return nil
}
```

**去重策略**：

| 场景 | 去重级别 | 说明 |
|------|---------|------|
| 完整消息（无分片） | 消息级别 | 收到后立即 `MarkProcessed` |
| 分片消息 - 首个分片 | 消息级别 | 创建 partialMessage 前检查 `IsDuplicate` |
| 分片消息 - 后续分片 | 分片级别 | 检查 `fragments[index]` 是否已存在 |
| 分片消息 - 最后分片 | 消息级别 | 收齐后 `MarkProcessed`，删除 partialMessage |

---

## 集成到 UDPTransport

### 修改 UDPTransport 结构

```go
type UDPTransport struct {
    // 配置
    config      *TransportConfig
    codec       Codec
    localNodeID uint64

    // UDP 连接
    conn *net.UDPConn

    // 分片相关
    fragmentBuf  *fragmentBuffer
    msgIDCounter uint64

    // 新增：消息去重
    deduplicator *messageDeduplicator

    // 错误统计
    parseErrorCount    atomic.Uint64
    crcErrorCount      atomic.Uint64
    fragmentErrorCount atomic.Uint64
    channelBlockCount  atomic.Uint64
    dedupHitCount      atomic.Uint64 // 去重命中计数

    // 接收通道
    recvCh   chan Message
    recvOnce sync.Once

    // 生命周期
    started  atomic.Bool
    stopped  atomic.Bool
    stopCh   chan struct{}
    stopOnce sync.Once
    wg       sync.WaitGroup
}
```

### 修改初始化逻辑

```go
// NewUDPTransportWithConfig 创建 UDP 传输（自定义配置）
func NewUDPTransportWithConfig(config *TransportConfig) (*UDPTransport, error) {
    // ... 现有代码 ...

    t := &UDPTransport{
        config:      config,
        codec:       codec,
        localNodeID:  0,
        recvCh:      make(chan Message, config.BufferSize),
        stopCh:      make(chan struct{}),
        // 新增：创建去重器
        deduplicator: NewMessageDeduplicator(60, 10000),
    }

    return t, nil
}
```

### 修改接收流程

```go
// processReceivedData 处理接收到的数据（分片重组）
func (t *UDPTransport) processReceivedData(data []byte) Message {
    // 解析 TLV Frame
    frame, err := t.parseFrame(data)
    if err != nil {
        t.parseErrorCount.Add(1)
        logging.Warnf("解析帧失败: %v", err)
        return nil
    }

    // 提取关键字段
    nodeID := frame.FixedHeader.NodeID
    msgSeq := uint64(frame.FixedHeader.MsgSeq)

    // 检查是否有分片扩展字段
    fragmentField := frame.VarExtHeader.GetField(ExtFragment)
    if fragmentField == nil {
        // 无分片扩展，直接解码消息

        // 去重检查
        if t.deduplicator.IsDuplicate(nodeID, msgSeq) {
            t.dedupHitCount.Add(1)
            logging.Debugf("重复消息（完整）: nodeID=%d, msgSeq=%d", nodeID, msgSeq)
            return nil
        }

        msg := t.decodeMessage(frame)
        if msg != nil {
            // 标记已处理
            t.deduplicator.MarkProcessed(nodeID, msgSeq)
        }
        return msg
    }

    // 有分片扩展，进行分片重组
    return t.processFragmentFrame(frame)
}
```

### 修改停止逻辑

```go
// Stop 停止传输层
func (t *UDPTransport) Stop() error {
    if !t.stopped.CompareAndSwap(false, true) {
        return nil
    }

    t.stopOnce.Do(func() {
        logging.Info("停止 UDP 传输层...")

        close(t.stopCh)

        // 新增：停止去重器
        if t.deduplicator != nil {
            t.deduplicator.Stop()
        }

        // 关闭分片缓冲区
        if t.fragmentBuf != nil {
            close(t.fragmentBuf.stopCh)
            t.fragmentBuf.cleanupWg.Wait()
        }

        // 关闭 UDP 连接
        if t.conn != nil {
            _ = t.conn.Close()
        }

        t.wg.Wait()

        t.recvOnce.Do(func() {
            close(t.recvCh)
        })

        logging.Info("UDP 传输层已停止")
    })

    return nil
}
```

### 修改统计信息

```go
// Stats 获取统计信息
func (t *UDPTransport) Stats() map[string]any {
    stats := make(map[string]any)
    stats["started"] = t.started.Load()
    stats["stopped"] = t.stopped.Load()
    stats["listen_addr"] = t.GetLocalAddr()
    stats["local_node_id"] = t.localNodeID
    stats["msg_id_counter"] = atomic.LoadUint64(&t.msgIDCounter)

    // 分片缓冲区统计
    if t.fragmentBuf != nil {
        t.fragmentBuf.mu.RLock()
        stats["pending_fragments"] = len(t.fragmentBuf.buffers)
        t.fragmentBuf.mu.RUnlock()
    }

    // 错误统计
    stats["parse_errors"] = t.parseErrorCount.Load()
    stats["crc_errors"] = t.crcErrorCount.Load()
    stats["fragment_errors"] = t.fragmentErrorCount.Load()
    stats["channel_blocks"] = t.channelBlockCount.Load()

    // 新增：去重统计
    stats["dedup_hit"] = t.dedupHitCount.Load()
    if t.deduplicator != nil {
        t.deduplicator.mu.RLock()
        stats["dedup_cache_size"] = len(t.deduplicator.recentMessages)
        stats["dedup_tracked_nodes"] = len(t.deduplicator.nodeMaxSeq)
        t.deduplicator.mu.RUnlock()
    }

    return stats
}
```

---

## 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `DedupWindowSize` | 60 秒 | 去重缓存窗口大小（超过此时间的记录会被清理） |
| `DedupMaxCacheSize` | 10000 | 最大去重缓存数量（超过时 LRU 淘汰） |

---

## 内存占用估算

| 组件 | 内存占用 | 说明 |
|------|---------|------|
| `recentMessages` | ~800 KB | 10000 条 × (8+8+8) 字节 |
| `nodeMaxSeq` | ~8 KB | 100 节点 × (8+8) 字节 |
| **总计** | ~808 KB | 对于 100 节点集群 |

---

## 风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| **内存占用** | 大量节点导致去重缓存过大 | 限制 `maxCacheSize`，LRU 淘汰 |
| **误判** | MsgSeq 回绕导致误判去重 | 使用 `uint64`（1844亿亿，不会回绕） |
| **性能开销** | 每条消息都需要去重检查 | 使用 `sync.RWMutex`，读多写少 |

---

## 实施步骤

### 阶段 1：实现去重器（P0）

**文件**：新建 `internal/metadata/transport/message_deduplicator.go`

1. 实现 `messageDeduplicator` 结构和方法
2. 单元测试：`IsDuplicate`、`MarkProcessed`、`cleanupExpired`

### 阶段 2：集成到 UDPTransport（P0）

**文件**：修改 `internal/metadata/transport/udp_transport.go`

1. 在 `UDPTransport` 中添加 `deduplicator` 字段
2. 在 `NewUDPTransportWithConfig` 中初始化去重器
3. 在 `processReceivedData` 中添加去重检查
4. 在 `processFragmentFrame` 中传递去重器
5. 在 `addFragment` 中使用去重器
6. 在 `Stop` 中停止去重器
7. 在 `Stats` 中添加去重统计

### 阶段 3：测试验证（P0）

**文件**：修改 `internal/metadata/transport/udp_transport_test.go`

1. 测试完整消息去重
2. 测试分片消息去重
3. 测试重复分片过滤
4. 测试缓存过期清理

---

## 参考资料

- **现有代码**：
  - `internal/metadata/transport/udp_transport.go:259` (processReceivedData)
  - `internal/metadata/transport/udp_transport.go:339` (addFragment)
  - `internal/metadata/transport/udp_transport.go:76` (fragmentKey)

- **相关设计**：
  - `docs/02_design/modules/01_详细设计文档.md` (Gossip 协议设计)

---

**维护者**: AI Assistant
**最后更新**: 2026-01-20
