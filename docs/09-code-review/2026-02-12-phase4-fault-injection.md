# 阶段 4：故障注入测试（验证鲁棒性）

> 证明系统在面对真实故障时不会崩溃或丢失数据。

**预计时间**：3-4 小时
**状态**：⏳ 待开始

---

## 📋 任务清单

### Step 4.1：单点故障测试（1h）

**任务**：
1. [ ] 启动多节点集群
2. [ ] 模拟各种故障场景
3. [ ] 观察系统行为

#### 测试场景

| 场景 | 操作 | 预期行为 | 检查方法 |
|------|------|----------|----------|
| Leader 宕机 | kill leader 进程 | 新 leader 被选出 | 观察日志 |
| Follower 宕机 | kill 1 个 follower | 集群继续服务 | 检查可用性 |
| 网络分区 | iptables 隔离节点 | 多数派侧继续 | 检查两边状态 |

**命令参考**：
```bash
# 启动 3 节点集群
make start-cluster NODES=3

# 找到 leader PID
pgrep -f "node.*leader" | head -1

# 杀掉 leader
kill -9 <PID>

# 观察日志
tail -f logs/node-*.log | grep -E "leader|election"

# 网络分区模拟
sudo iptables -A INPUT -s <node_ip> -j DROP
sudo iptables -A OUTPUT -d <node_ip> -j DROP

# 恢复网络
sudo iptables -D INPUT -s <node_ip> -j DROP
sudo iptables -D OUTPUT -d <node_ip> -j DROP
```

---

### Step 4.2：恢复测试（1h）

**任务**：
1. [ ] 在 Step 4.1 的基础上，重启被杀掉的节点
2. [ ] 验证数据同步和一致性

#### 恢复验证清单

| 验证项 | 方法 | 预期结果 |
|--------|------|----------|
| 节点加入集群 | 检查日志 | 成功加入，无报错 |
| 数据自动同步 | 对比数据 | 完全一致 |
| 无数据丢失 | 检查校验和 | 一致 |
| 无数据损坏 | 运行验证 | 通过 |

**命令参考**：
```bash
# 重启节点
make start-node NODE_ID=node-1

# 检查数据一致性
make verify-consistency

# 对比数据目录
diff -r data/node-1/ data/node-2/
```

---

### Step 4.3：边界条件测试（1h）

**任务**：
1. [ ] 测试极端场景
2. [ ] 验证系统边界行为

#### 极端场景

| 场景 | 操作 | 预期行为 |
|------|------|----------|
| 最小集群 | 2 节点部署 | 正常工作，性能降低 |
| 快速启停 | 连续 kill/start | 不崩溃，数据一致 |
| 网络延迟 | 用 tc 模拟延迟 | 降级但可用 |
| 内存压力 | 限制内存可用 | 不 OOM，优雅降级 |

**命令参考**：
```bash
# 网络延迟模拟
sudo tc qdisc add dev eth0 root netem delay 100ms 20ms

# 移除延迟
sudo tc qdisc del dev eth0 root

# 内存限制
ulimit -v 1048576  # 限制内存 1GB

# 快速启停测试
for i in {1..10}; do
  make start-cluster NODES=3
  sleep 2
  make stop-cluster
done
```

---

## 📝 测试报告模板

```markdown
## 故障注入测试报告

### 单点故障测试结果

#### Leader 宕机
- 测试时间：2026-02-12 14:30
- 故障前状态：5 节点集群，node-1 为 leader
- 操作：kill -9 node-1
- 恢复时间：8.5 秒
- 新 leader：node-3
- 数据丢失：无
- 结论：✅ 通过

#### Follower 宕机
- 测试时间：2026-02-12 14:35
- 故障前状态：5 节点集群
- 操作：kill node-2
- 集群状态：继续服务
- 性能影响：无明显下降
- 结论：✅ 通过

### 网络分区测试结果

#### 场景：多数派 vs 少数派
- 测试时间：2026-02-12 14:40
- 分区方式：隔离 node-1, node-2
- 多数派（3 节点）：继续服务 ✅
- 少数派（2 节点）：停止写入 ✅
- 网络恢复后：自动同步 ✅
- 结论：✅ 通过

---

## 边界条件测试结果

| 场景 | 结果 | 备注 |
|------|------|------|
| 最小集群（2 节点） | ✅ 通过 | 性能略降 |
| 快速启停（10 次） | ✅ 通过 | 无数据损坏 |
| 网络延迟（100ms） | ⚠️ 警告 | 超时较多 |
| 内存限制（1GB） | ✅ 通过 | 优雅降级 |

---

## 发现的问题

### P0 - 严重
无

### P1 - 中等
1. 网络延迟场景下超时较多
   - 建议：调整超时配置或增加重试次数

### P2 - 低
1. 快速启停时日志较多警告
   - 建议：优化启动流程
```

---

## ✅ 完成自检

- [ ] 单点故障下集群能自动恢复
- [ ] 节点重启后能自动同步数据
- [ ] 极端场景下系统要么继续服务，要么优雅降级，不会 panic

---

## 📌 本阶段产出文件

- `phase4_fault_injection_results.md` - 故障注入测试报告
- `phase4_edge_cases.md` - 边界条件测试报告
