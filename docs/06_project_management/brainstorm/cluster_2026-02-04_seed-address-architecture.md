# 种子节点架构改进建议

> **文档类型**: 💡 技术建议 / 📊 架构改进
> **创建日期**: 2026-02-04
> **状态**: 📋 待讨论
> **优先级**: P0 (高) - 影响节点自动发现和拓扑形成

---

## 背景说明

当前多节点测试中发现节点间无法互相发现。每个节点独立运行，只能看到自己（self）。

## 问题描述

### 当前配置问题

**`configs/config.yaml` 当前配置**：
```yaml
cluster:
  hosts:
    - host_id: "host-1"
      seed_node: "/ip4/127.0.0.1/tcp/9211"  # 指向自己
      nodes:
        - node_id: "node-1"
          node_addr_tcp: "/ip4/127.0.0.1/tcp/9211"

    - host_id: "host-2"
      seed_node: "/ip4/127.0.0.1/tcp/9213"  # 指向自己 ❌ 错误
      nodes:
        - node_id: "node-2"
          node_addr_tcp: "/ip4/127.0.0.1/tcp/9213"
```

**问题**：
- 每个 host 的 `seed_node` 都指向自己
- TreeCoordinator 的 `getKnownNodes()` 会过滤掉自身地址
- 结果：每个节点都没有其他节点可以连接
- 节点间无法互相发现，无法形成树形拓扑

**代码逻辑**（`internal/metadata/cluster/tree_coordinator.go:644-662`）：
```go
// 从配置中读取种子节点
seedNodes := extractSeedNodesFromConfig(tc.clusterConfig)
for _, addr := range seedNodes {
    // 决策 3: 自动过滤自身地址
    selfAddr := tc.localNode.Addr.TCPAddr()
    if parsedAddr.TCPAddr() == selfAddr {
        continue  // 跳过自己
    }
    // 添加到已知节点列表
}
```

**结果**：每个节点过滤掉自己后，已知节点列表为空。

## 建议方案

### 方案：单一 Well-Known Seed Address

**核心思想**：
1. **第一个节点（host-1）**作为种子节点（Bootstrap Node）
2. **其他节点（host-2 ~ host-15）**都连接到第一个节点
3. 通过第一个节点发现集群拓扑并建立连接

### 修正后的配置

```yaml
cluster:
  hosts:
    # Host-1: 种子节点（第一个节点）
    - host_id: "host-1"
      seed_node: "/ip4/127.0.0.1/tcp/9211"  # 可以指向自己或留空
      nodes:
        - node_id: "node-1"
          node_addr_tcp: "/ip4/127.0.0.1/tcp/9211"
          node_addr_udp: "/ip4/127.0.0.1/udp/9212"

    # Host-2: 连接到种子节点
    - host_id: "host-2"
      seed_node: "/ip4/127.0.0.1/tcp/9211"  # ✅ 指向 host-1
      nodes:
        - node_id: "node-2"
          node_addr_tcp: "/ip4/127.0.0.1/tcp/9213"
          node_addr_udp: "/ip4/127.0.0.1/udp/9214"

    # Host-3: 连接到种子节点
    - host_id: "host-3"
      seed_node: "/ip4/127.0.0.1/tcp/9211"  # ✅ 指向 host-1
      nodes:
        - node_id: "node-3"
          node_addr_tcp: "/ip4/127.0.0.1/tcp/9215"
          node_addr_udp: "/ip4/127.0.0.1/udp/9216"

    # ... 其他节点同样指向 host-1
```

### 节点启动流程

```
┌──────────────────────────────────────────────────────────────────┐
│                    节点启动与发现流程                              │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Host-1 (种子节点)                Host-2 (普通节点)                │
│  ┌─────────────────────┐          ┌─────────────────────┐        │
│  │ 1. 启动 Daemon      │          │ 1. 启动 Daemon      │        │
│  │    绑定 9211 端口   │          │    绑定 9213 端口   │        │
│  └──────────┬──────────┘          └──────────┬──────────┘        │
│             │                                │                   │
│  ┌──────────▼──────────┐          ┌──────────▼──────────┐        │
│  │ 2. 读取配置         │          │ 2. 读取配置         │        │
│  │    seed_node: 自己 │          │    seed_node: 9211  │        │
│  └──────────┬──────────┘          └──────────┬──────────┘        │
│             │                                │                   │
│  ┌──────────▼──────────┐          ┌──────────▼──────────┐        │
│  │ 3. 提取种子节点     │          │ 3. 提取种子节点     │        │
│  │    过滤自己 = []    │          │    [9211]           │        │
│  └──────────┬──────────┘          └──────────┬──────────┘        │
│             │                                │                   │
│  ┌──────────▼──────────┐          ┌──────────▼──────────┐        │
│  │ 4. 等待其他节点     │          │ 4. 连接到 host-1    │        │
│  │    连接             │          │    获取拓扑信息      │        │
│  └──────────┬──────────┘          └──────────┬──────────┘        │
│             │                                │                   │
│  ┌──────────▼──────────┐          ┌──────────▼──────────┐        │
│  │ 5. 形成 1 层树      │          │ 5. 加入树形结构     │        │
│  │    [node-1]        │◄─────────│    [node-1, node-2] │        │
│  └─────────────────────┘          └─────────────────────┘        │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

### 命名改进建议

| 当前命名 | 问题 | 建议命名 | 理由 |
|---------|------|---------|------|
| `seed_node` | 语义不清晰，像是节点ID而非地址 | `seed_address` 或 `bootstrap_address` | 明确表示这是一个地址 |
| `seed_nodes` | 复数形式，但通常只需一个地址 | `seed_addresses` 或 `bootstrap_servers` | 更准确的语义 |

## 实施建议

### 步骤 1: 修改配置文件

1. 将 host-2 ~ host-15 的 `seed_node` 统一改为 `/ip4/127.0.0.1/tcp/9211`
2. host-1 的 `seed_node` 保持不变或留空

### 步骤 2: 重命名字段（可选）

1. 将 `seed_node` 重命名为 `seed_address`
2. 更新相关代码和文档

### 步骤 3: 验证节点自动发现

1. 启动 host-1
2. 启动 host-2，验证其连接到 host-1
3. 检查 `node list` 输出，应该显示两个节点
4. 检查 `cluster topology`，应该显示树形结构

### 步骤 4: 环境变量支持

添加 `NEXKV_SEED_NODE` 环境变量支持，方便容器部署：

```bash
# Host-1（种子节点）
export NEXKV_HOST_ID=host-1
export NEXKV_SEED_NODE=/ip4/127.0.0.1/tcp/9211  # 可选

# Host-2 ~ Host-15
export NEXKV_HOST_ID=host-2
export NEXKV_SEED_NODE=/ip4/127.0.0.1/tcp/9211  # 指向种子节点
```

## 预期效果

修复后应该能够实现：

1. **节点间自动发现** ✅
   - host-2 启动后自动连接到 host-1
   - host-1 返回集群拓扑信息
   - host-2 更新本地拓扑视图

2. **树形拓扑形成** ✅
   - host-1 成为根节点（ROOT）
   - host-2 成为 host-1 的子节点（CHILD）
   - 后续节点依次加入，形成完整的树形结构

3. **node add/remove 正常工作** ✅
   - 节点已互相发现
   - 可以正常添加和移除节点

4. **cluster health 正确识别** ✅
   - 可以 ping 其他节点
   - 正确统计健康节点数量

## 参考资料

- **相关代码**: `internal/metadata/cluster/tree_coordinator.go:644-674`
- **配置文件**: `configs/config.yaml`
- **TreeCoordinator**: 负责节点发现和拓扑管理
- **Bootstrap 协议**: 类似 Bitcoin、Ethereum 的种子节点发现机制

---

**维护者**: 🤖 AI 团队 + 👤 架构师
**最后更新**: 2026-02-04
**状态**: 📋 待讨论
