# UDP 分片传输改进建议

> **文档类型**: 💡 技术建议 (Proposals)
> **创建日期**: 2026-01-20
> **状态**: 📋 待讨论
> **优先级**: P2 (低优先级 - 未来改进)

---

## 背景

在 PR-012 UDP Transport 实现代码审查中，发现了两个需要协议级别的改进项。这些改进需要重新设计分片协议，建议作为未来的优化方向。

---

## U-003: 分片重组顺序依赖问题

### 问题描述

**位置**: `internal/metadata/transport/udp_transport.go:386-404`

当前实现假设分片按顺序到达，使用 `received == total` 来判断重组完成。但如果中间某个分片丢失，重组永远不会触发。

**代码示例**:
```go
// 当前实现
partial.fragments[index] = data
partial.received++

if partial.received == partial.total {  // 假设分片按顺序到达
    // 重组消息
}
```

**问题场景**:
```
发送端: [分片0] [分片1] [分片2] [分片3]
接收端: [分片0] [分片2] [分片3]  <- 分片1 丢失
结果: received=3, total=4，永远无法重组
```

### 建议改进方案

#### 方案 A: 使用位图跟踪接收状态
```go
type partialMessage struct {
    total      uint16
    received   uint16
    fragments  [][]byte
    bitmap     *big.Int  // 跟踪已接收的分片
    lastUpdate time.Time
}

// 检查是否收齐所有分片（不依赖顺序）
func (p *partialMessage) isComplete() bool {
    // 检查位图的前 total 位是否全部为 1
    for i := 0; i < int(p.total); i++ {
        if p.bitmap.Bit(i) == 0 {
            return false
        }
    }
    return true
}
```

#### 方案 B: 添加超时 + 部分重组
```go
// 设置重组超时（如 5 秒）
if time.Since(partial.lastUpdate) > 5*time.Second {
    // 尝试部分重组或请求重传丢失的分片
    t.requestRetransmission(key, partial.getMissingIndexes())
}
```

#### 方案 C: 添加 NACK 机制
```go
// 接收端主动请求丢失的分片
type NACKMessage struct {
    NodeID  uint64
    MsgID   uint64
    Missing []uint16  // 丢失的分片索引
}
```

### 推荐方案

**短期**: 方案 A（位图跟踪）- 实现简单，不改变协议
**长期**: 方案 C（NACK 机制）- 需要协议升级，但更可靠

---

## U-004: 缺少流量控制机制

### 问题描述

**位置**: `internal/metadata/transport/udp_transport.go:505-547`

发送端可以无限制地发送数据，导致接收端的 `recvCh` 溢出（`ChannelSendTimeout` 后丢弃消息）。

**代码示例**:
```go
// 当前实现 - 无流量控制
func (t *UDPTransport) Send(ctx context.Context, addr string, msg Message) error {
    // 直接发送，没有考虑接收端缓冲区状态
    _, err := t.conn.WriteToUDP(data, addr)
}
```

**问题场景**:
```
发送端: [消息1] [消息2] [消息3] ... [消息1000]
接收端: recvCh (缓冲区大小 4096) -> 溢出
结果: 大量消息被丢弃，`channelBlockCount` 增加
```

### 建议改进方案

#### 方案 A: 令牌桶限流
```go
type UDPTransport struct {
    // ... 现有字段
    tokenBucket *tokenBucket  // 令牌桶
    sendRate    int           // 发送速率 (字节/秒)
}

func (t *UDPTransport) Send(ctx context.Context, addr string, msg Message) error {
    // 等待令牌
    if err := t.tokenBucket.Wait(ctx, len(data)); err != nil {
        return err
    }
    // 发送数据
    return t.sendDirect(addr, data)
}
```

#### 方案 B: 反压机制（基于通道大小）
```go
func (t *UDPTransport) Send(ctx context.Context, addr string, msg Message) error {
    // 检查接收端通道是否接近满载
    if len(t.recvCh) > cap(t.recvCh)*3/4 {
        // 暂停发送或返回错误
        return types.NewTransportTimeoutError("接收端缓冲区接近满载")
    }
    // 正常发送
    return t.sendDirect(addr, data)
}
```

#### 方案 C: 动态窗口机制
```go
type UDPTransport struct {
    sendWindow   uint16  // 发送窗口大小
    lastAckTime  time.Time
    unackedCount uint16
}

// 接收端确认后扩大窗口
func (t *UDPTransport) onAckReceived() {
    t.sendWindow = min(t.sendWindow*2, MaxWindowSize)
    t.unackedCount = 0
}

// 超时未确认则缩小窗口
func (t *UDPTransport) onAckTimeout() {
    t.sendWindow = max(t.sendWindow/2, MinWindowSize)
}
```

### 推荐方案

**短期**: 方案 B（反压机制）- 实现简单，有效防止溢出
**长期**: 方案 C（动态窗口）- 类似 TCP 的拥塞控制，性能最优

---

## 实施建议

### 阶段 1: 短期改进 (1-2 周)
- [ ] 实现位图跟踪（U-003 方案 A）
- [ ] 实现反压机制（U-004 方案 B）
- [ ] 添加单元测试验证改进效果

### 阶段 2: 长期优化 (1-2 月)
- [ ] 设计 NACK 协议（U-003 方案 C）
- [ ] 实现动态窗口机制（U-004 方案 C）
- [ ] 性能测试和调优

### 阶段 3: 协议升级
- [ ] 协议版本号管理
- [ ] 向后兼容性处理
- [ ] 文档更新

---

## 风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 协议变更不兼容 | 高 | 使用版本号，支持多版本共存 |
| 性能下降 | 中 | 充分的性能测试和调优 |
| 实现复杂度增加 | 低 | 充分的设计评审和代码审查 |

---

## 参考资料

- UDP 分片协议: `internal/metadata/transport/udp_transport.go:26-45`
- 分片重组逻辑: `internal/metadata/transport/udp_transport.go:349-415`
- 代码审查报告: PR-012 Code Review v2.0

---

**文档维护者**: NexKV 开发团队
**最后更新**: 2026-01-20
