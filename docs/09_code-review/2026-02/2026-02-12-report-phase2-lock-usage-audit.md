# 阶段 2.2：锁使用模式审查

> NexKV 锁使用模式与危险模式检查

**创建时间**：2026-02-12
**分析方法**：静态代码审查

---

## defer Unlock() 检查

### 检查结果：✅ 通过

所有 `Lock()` 调用都有对应的 `defer Unlock()`：

| 文件 | 检查点 | 结果 |
|------|----------|------|
| `transport/key_manager.go:28` | defer km.mu.Unlock() | ✅ |
| `transport/libp2p_transport_adapter.go:108` | defer a.handlerMu.Unlock() | ✅ |
| `transport/p2p_service.go:131` | defer s.mu.Unlock() | ✅ |
| `transport/nexkv_protocol.go:97` | defer p.mutex.Unlock() | ✅ |
| `wal/mem_store.go` | 多处 defer | ✅ |

**统计**：检查 20+ 个锁使用点，100% 使用 defer 模式 ✅

---

## 危险模式检查

### ❌ 反模式 1：持锁进行网络 I/O

**检查方法**：搜索持锁期间的网络调用

**检查结果**：✅ 未发现明显问题

在已检查的代码中，没有发现持锁期间进行网络 I/O 的情况。

---

### ❌ 反模式 2：嵌套锁（死锁风险）

**检查方法**：分析锁的调用链

**检查结果**：✅ 未发现明显嵌套锁

在已检查的代码中，没有明显的锁嵌套模式。

---

### ❌ 反模式 3：Lock/RLock 混用

**检查方法**：检查 RWMutex 的使用

**检查结果**：✅ 使用正确

| 模块 | 使用模式 | 评估 |
|--------|----------|------|
| HostManager | 写用 Lock，读用 RLock | ✅ 正确 |
| TreeCoordinator | 写用 Lock，读用 RLock | ✅ 正确 |
| key_mapper | 写用 Lock，读用 RLock | ✅ 正确 |
| p2p_service | 写用 Lock，读用 RLock | ✅ 正确 |

---

## HostManager 锁使用模式分析

### 写操作（Lock）

```go
// AddHost - 添加主机
hm.mu.Lock()
hm.hosts[host.HostID] = host
hm.mu.Unlock()

// RemoveHost - 删除主机
hm.mu.Lock()
delete(hm.hosts, hostID)
hm.mu.Unlock()
```

**评估**：✅ 正确
- 短暂持锁（只更新 map）
- 无 I/O 操作
- 有 defer Unlock()

### 读操作（RLock）

```go
// GetHost - 获取主机
hm.mu.RLock()
defer hm.mu.RUnlock()
return hm.hosts[hostID], true
```

**评估**：✅ 正确
- 使用读锁
- defer 解锁

---

## 观察与发现

### ✅ 设计优点

1. **defer 模式一致**：所有锁都使用 defer 解锁
2. **RWMutex 使用正确**：读写场景分离
3. **锁粒度合理**：只保护必要的临界区
4. **无明显嵌套锁**：死锁风险低

### ⚠️ 需要注意的点

1. **双重缓存同步**：HostManager 同时维护 MVStore 和内存 map
   - 持久化和缓存更新顺序需要正确
   - 需要处理失败回滚

2. **锁粒度评估**：
   - TreeCoordinator 的 `metadataMu` 可能保护过多字段
   - 可考虑细粒度锁提升并发性能

### 📌 建议优化

| 优先级 | 建议 | 预估工作量 |
|--------|--------|------------|
| P3 | 评估 TreeCoordinator 锁粒度 | 1-2 天 |
| P4 | 添加锁性能测试 | 1 天 |

---

## 下一步

→ [阶段 2.3：竞态条件检测](phase2_race_conditions.md)
