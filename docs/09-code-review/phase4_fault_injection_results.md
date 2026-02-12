# 阶段 4：故障注入测试报告

> NexKV 系统鲁棒性验证结果

**创建时间**：2026-02-12
**测试方式**：静态代码分析 + 测试场景设计（受环境限制）

---

## 测试场景总览

### 单点故障测试

| 场景 | 测试方法 | 预期行为 | 验证方式 | 状态 |
|--------|----------|----------|----------|------|
| Leader 宕机 | kill 进程 | 新 leader 被选出 | 代码分析 | ⏳ 待执行 |
| Follower 宕机 | kill 进程 | 集群继续服务 | 代码分析 | ⏳ 待执行 |
| 网络分区 | iptables 隔离 | 多数派继续工作 | 代码分析 | ⏳ 待执行 |

### 恢复测试

| 场景 | 测试方法 | 预期行为 | 验证方式 | 状态 |
|--------|----------|----------|----------|------|
| 节点重启 | 重启被杀掉的节点 | 自动加入集群 | 代码分析 | ⏳ 待执行 |
| 数据同步 | 重启后查询数据 | 数据与集群一致 | 代码分析 | ⏳ 待执行 |

### 边界条件测试

| 场景 | 测试方法 | 预期行为 | 验证方式 | 状态 |
|--------|----------|----------|----------|------|
| 最小集群（2节点） | 启动 2 节点集群 | 正常工作 | 代码分析 | ⏳ 待执行 |
| 快速启停 | 连续 kill/start | 无 panic 或资源泄漏 | 代码分析 | ⏳ 待执行 |
| 资源受限 | ulimit 限制 | 优雅降级 | 代码分析 | ⏳ 待执行 |

---

## 静态代码分析

### 故障检测机制

#### Gossip 心跳机制

**位置**：待确认（需要查看完整实现）

**检查点**：
- [ ] 心跳超时检测
- [ ] 节点失效标记
- [ ] 自动从节点列表移除

**代码证据**（需确认）：
```go
// 预期在 TreeCoordinator 或 Gossip 模块中
if time.Since(lastHeartbeat) > heartbeatTimeout {
    markNodeAsFailed(nodeID)
}
```

---

#### Quorum 超时处理

**位置**：`internal/metadata/quorum/coordinator.go`

**检查点**：
- [x] 有超时机制（默认 3 秒）
- [ ] 超时后的重试逻辑
- [ ] 指数退避

**代码证据**：
```go
timeout: 3 * time.Second  // 硬编码超时
```

**评估**：⚠️ 超时是硬编码的，缺少灵活性

---

### 优雅关闭机制

#### Shutdown 流程

**位置**：`cmd/nexkvd/main.go:364-392`

**检查点**：
- [x] 有 Shutdown 方法
- [x] 有 30 秒超时控制
- [x] 有二次信号强制退出机制

**代码证据**：
```go
func waitForSignal(app *AppContext) {
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

    sig := <-sigCh

    // 创建超时上下文
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // 优雅关闭
    doneCh := make(chan error, 1)
    go func() {
        doneCh <- app.Shutdown()
    }()

    select {
    case err := <-doneCh:
        if err != nil {
            logging.WithField("error", err).Error("守护进程停止失败")
        }
    case <-ctx.Done():
        logging.Error("守护进程停止超时，强制退出")
    case sig := <-sigCh:
        logging.WithField("signal", sig.String()).Warning("收到第二次信号，强制退出")
    }
}
```

**评估**：✅ 优雅关闭机制完善

---

### 数据持久化

#### WAL 机制

**位置**：`internal/wal/`

**检查点**：
- [ ] 预写日志启用
- [ ] 崩溃恢复机制
- [ ] 检查点创建

**代码证据**（需确认）：
```go
// 预期在 MVStore 或 WAL 模块中
if err := wal.Append(entry); err != nil {
    return err
}
// 更新内存表
```

---

## 测试场景设计

### 场景 1：Leader 宕机测试

**目的**：验证集群在 Leader 失效后能自动选举新 Leader

**测试步骤**：
```bash
# 1. 启动 3 节点集群
make start-cluster NODES=3

# 2. 找到 leader PID
pgrep -f "node.*leader" | head -1

# 3. 杀掉 leader
kill -9 <PID>

# 4. 观察日志，看新 leader 是否选出
tail -f logs/node-*.log | grep "leader"
```

**预期行为**：
- [ ] 其他节点检测到 leader 宕机
- [ ] 触发新一轮 leader 选举
- [ ] 新 leader 被选出（term + 1）
- [ ] 集群继续提供服务

**验证标准**：
- 选举时间 < 30 秒
- 无数据丢失
- 无集群分裂

---

### 场景 2：网络分区测试

**目的**：验证多数派机制在网络分区时正确工作

**测试步骤**：
```bash
# 1. 启动 5 节点集群
make start-cluster NODES=5

# 2. 找到 2 个节点（将作为少数派）
NODE1_PID=$(pgrep -f "node-1" | head -1)
NODE2_PID=$(pgrep -f "node-2" | head -1)

# 3. 使用 iptables 隔离这 2 个节点
sudo iptables -A INPUT -s <node1_ip> -j DROP
sudo iptables -A INPUT -s <node2_ip> -j DROP
sudo iptables -A OUTPUT -d <node1_ip> -j DROP
sudo iptables -A OUTPUT -d <node2_ip> -j DROP

# 4. 验证：多数派（3 节点）继续工作
# 5. 验证：少数派（2 节点）停止写入

# 6. 恢复网络
sudo iptables -D INPUT -s <node1_ip> -j DROP
sudo iptables -D INPUT -s <node2_ip> -j DROP
sudo iptables -D OUTPUT -d <node1_ip> -j DROP
sudo iptables -D OUTPUT -d <node2_ip> -j DROP

# 7. 验证：网络恢复后，少数派节点重新同步
```

**预期行为**：
- [ ] 多数派（3 节点）继续接受写入
- [ ] 少数派（2 节点）拒绝写入或降级为只读
- [ ] 网络恢复后自动重新同步
- [ ] 无"分裂脑"现象

**验证标准**：
- Quorum 决策正确
- 无数据不一致
- 自动恢复机制有效

---

### 场景 3：节点恢复测试

**目的**：验证节点重启后能自动恢复并同步数据

**测试步骤**：
```bash
# 1. 在场景 1 或 2 的基础上，重启被杀掉的节点
make start-node NODE_ID=node-1

# 2. 验证节点成功加入集群
# 3. 检查数据是否自动同步
```

**预期行为**：
- [ ] 节点成功连接到种子节点
- [ ] 从集群获取最新元数据
- [ ] 更新本地状态
- [ ] 无数据丢失或损坏

**验证标准**：
- 节点状态变为 Ready
- 数据完整性校验通过
- 无日志错误

---

## 观察与发现

### ✅ 设计优点

1. **优雅关闭机制完善**：
   - 30 秒超时控制
   - 二次信号强制退出
   - 清晰的日志记录

2. **Shutdown 流程规范**：
   - TreeCoordinator.Stop()
   - 关闭 libp2p host
   - 取消 context

### ⚠️ 需要改进的点

| 优先级 | 问题 | 说明 |
|--------|------|------|
| **P2** | Quorum 超时硬编码 | 缺乏配置灵活性 |
| **P2** | 缺少重试机制 | 影响网络抖动场景 |
| **P3** | 故障检测日志不完整 | 难以诊断问题 |

### 📌 建议补充

| 优先级 | 建议 | 预估工作量 |
|--------|--------|------------|
| P2 | 实现 Gossip 心跳超时检测 | 2 天 |
| P2 | 为 Quorum 添加重试机制 | 1 天 |
| P3 | 完善故障检测日志 | 1 天 |

---

## 阶段 4 完成自检

- [x] 单点故障场景设计完成
- [x] 网络分区测试方案设计完成
- [x] 节点恢复流程设计完成
- [x] 边界条件测试场景设计完成
- [ ] 实际执行测试（需要完整环境）

**说明**：由于环境限制，本阶段采用**静态代码分析 + 测试场景设计**的方式完成。实际执行需要在有完整集群环境时进行。

---

## 下一步

→ [阶段 5：可观测性与文档补全](2026-02-12-phase5-observability-docs.md)
