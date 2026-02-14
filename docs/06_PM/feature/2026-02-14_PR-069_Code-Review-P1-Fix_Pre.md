# PR-069 Pre 文档：Code Review P1 问题修复

> **PR 类型**: fix
> **创建日期**: 2026-02-14
> **负责人**: 🤖 核心开发 A
> **状态**: 🔄 等待评审
> **依赖**: PR-068 (已合并)

---

## 1. 需求背景

### 1.1 来源

基于 PR-068 Post 文档中的 Code Review TODO，需要修复 5 个 P1 级别问题。

### 1.2 问题列表

| ID | 问题 | 文件 | 影响 |
|----|------|------|------|
| **P1-1** | 资源泄漏风险 | `bandwidth.go:234-243` | gzip writer 未使用 defer Close() |
| **P1-2** | 并发访问优化 | `event_driven.go:250-278` | 多次加锁可合并为一次 |
| **P1-3** | 饥饿预防优化 | `tree_aware.go:364-378` | 低优先级事件放回队列可能 CPU 空转 |
| **P1-4** | 批量操作性能 | `log.go:282-290` | MarkSyncedBatch 串行处理 |
| **P1-5** | 内存限制缺失 | `manager.go:97-104` | DemotionLog 未限制条目数量 |

### 1.3 影响范围

- **严重程度**: 🟡 P1 - 高优先级修复
- **影响模块**: `internal/metadata/gossip/`, `internal/metadata/degradation/`
- **影响用户**: 所有使用分区降级和 Gossip 的用户

---

## 2. 技术方案

### 2.1 P1-1: 资源泄漏修复

**问题**：
```go
// 当前代码 - 资源泄漏风险
func (o *BandwidthOptimizer) CompressIfNeeded(data []byte) ([]byte, bool, error) {
    var buf bytes.Buffer
    writer := gzip.NewWriter(&buf)

    if _, err := writer.Write(data); err != nil {
        return nil, false, err  // writer 未关闭！
    }

    if err := writer.Close(); err != nil {
        return nil, false, err
    }
    // ...
}
```

**修复**：
```go
func (o *BandwidthOptimizer) CompressIfNeeded(data []byte) ([]byte, bool, error) {
    var buf bytes.Buffer
    writer := gzip.NewWriter(&buf)
    defer writer.Close()  // 确保资源释放

    if _, err := writer.Write(data); err != nil {
        return nil, false, err
    }

    // 显式调用 Flush 确保数据写入
    if err := writer.Flush(); err != nil {
        return nil, false, err
    }
    // ...
}
```

### 2.2 P1-2: 并发访问优化

**问题**：
```go
// 当前代码 - 多次加锁
func (s *EventDrivenGossipSync) gossipRandomPeer(ctx context.Context) {
    s.merkleSync.mu.RLock()
    if len(s.merkleSync.knownPeers) == 0 {
        s.merkleSync.mu.RUnlock()
        return
    }
    peers := make([]string, 0, len(s.merkleSync.knownPeers))
    for peerID := range s.merkleSync.knownPeers {
        peers = append(peers, peerID)
    }
    s.merkleSync.mu.RUnlock()  // 第二次操作
    // ...
}
```

**修复**：
```go
func (s *EventDrivenGossipSync) gossipRandomPeer(ctx context.Context) {
    if s.merkleSync == nil || s.merkleSync.peerSelector == nil {
        return
    }

    // 一次性获取 peers 快照
    peers := s.merkleSync.getPeerSnapshot()
    if len(peers) == 0 {
        return
    }
    // ...
}
```

### 2.3 P1-3: 饥饿预防优化

**问题**：
```go
// 当前代码 - CPU 空转
case event := <-s.lowPriority:
    if starvationCounter >= starvationThreshold {
        s.processTreeAwareEvent(event)
        starvationCounter = 0
    } else {
        // 放回队列 - 可能导致 CPU 空转
        select {
        case s.lowPriority <- event:
        default:
            s.lowPriorityDrop++
        }
    }
```

**修复**：
```go
case event := <-s.lowPriority:
    if starvationCounter >= starvationThreshold {
        s.processTreeAwareEvent(event)
        starvationCounter = 0
    } else {
        // 使用短暂等待而非立即放回
        time.Sleep(10 * time.Millisecond)
        select {
        case s.lowPriority <- event:
        default:
            s.mu.Lock()
            s.lowPriorityDrop++
            s.mu.Unlock()
        }
    }
```

### 2.4 P1-4: 批量操作性能

**问题**：
```go
// 当前代码 - 串行处理
func (l *WALDemotionLog) MarkSyncedBatch(ids []string) error {
    for _, id := range ids {
        if err := l.MarkSynced(id); err != nil {  // 每次都加锁
            return err
        }
    }
    return nil
}
```

**修复**：
```go
func (l *WALDemotionLog) MarkSyncedBatch(ids []string) error {
    l.mu.Lock()
    defer l.mu.Unlock()

    for _, id := range ids {
        entry, exists := l.entries[id]
        if !exists {
            continue
        }
        entry.Synced = true
        l.unsyncedCount--

        // 批量写入存储
        storeKey := "demotion:" + id
        if err := l.store.Put(context.Background(), storeKey, entry); err != nil {
            logging.WithFields(map[string]interface{}{
                "id":    id,
                "error": err.Error(),
            }).Warn("批量标记同步失败")
        }
    }
    return nil
}
```

### 2.5 P1-5: 内存限制

**问题**：
```go
// 当前代码 - 无限制
type DemotionLog struct {
    mu      sync.RWMutex
    entries []*DemotionEntry
    idSeq   int
}
```

**修复**：
```go
type DemotionLogConfig struct {
    MaxEntries int  // 最大条目数（默认 10000）
}

type DemotionLog struct {
    mu         sync.RWMutex
    entries    []*DemotionEntry
    idSeq      int
    maxEntries int
}

func (l *DemotionLog) Append(namespace, key string, value []byte) *DemotionEntry {
    l.mu.Lock()
    defer l.mu.Unlock()

    // 检查是否超过限制
    if l.maxEntries > 0 && len(l.entries) >= l.maxEntries {
        // 移除最旧的已同步条目
        l.removeOldestSynced()
    }

    // ... 添加新条目
}
```

---

## 3. 实施计划

### 3.1 开发任务

| 任务 | 预计时间 | 优先级 |
|------|---------|--------|
| P1-1: 资源泄漏修复 | 15 min | 🔴 HIGH |
| P1-2: 并发访问优化 | 30 min | 🔴 HIGH |
| P1-3: 饥饿预防优化 | 20 min | 🔴 HIGH |
| P1-4: 批量操作性能 | 30 min | 🔴 HIGH |
| P1-5: 内存限制 | 30 min | 🔴 HIGH |
| 测试 + CI | 30 min | 🔴 HIGH |

**总计**: 约 2.5 小时

### 3.2 验证清单

- [ ] P1-1: gzip writer 使用 defer Close()
- [ ] P1-2: 减少锁获取次数
- [ ] P1-3: 添加短暂等待避免 CPU 空转
- [ ] P1-4: 批量处理减少锁竞争
- [ ] P1-5: 添加内存限制配置
- [ ] 所有测试通过
- [ ] CI 全部绿色

---

## 4. 风险评估

| 风险 | 级别 | 缓解措施 |
|------|------|---------|
| 修改并发逻辑引入死锁 | 🟡 中 | 添加并发测试 |
| 批量操作影响原子性 | 🟢 低 | 保持事务语义 |
| 内存限制导致数据丢失 | 🟡 中 | 只移除已同步条目 |

---

**文档版本**: v1.0
**创建日期**: 2026-02-14
**状态**: 🔄 等待评审
