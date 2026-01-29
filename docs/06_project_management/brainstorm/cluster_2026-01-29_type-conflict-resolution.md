# Host/Node 类型冲突解决方案

> **文档类型**: 🔍 发现 + 💡 建议
> **创建日期**: 2026-01-29
> **状态**: 📋 待讨论
> **优先级**: P0（高 - 阻塞 PR-033 开发）

---

## 背景说明

PR-033 引入了新的 Host/Node 双层架构设计，但现有 `internal/metadata/cluster/tree_coordinator.go` 已有相关类型定义，导致类型重复定义编译错误。

---

## 问题描述

### 现有类型（tree_coordinator.go）

| 类型 | 定义 | 关键特征 |
|------|------|---------|
| **NodeAddress** | 包含 Host、TCPPort、UDPPort | Host 字段存储完整地址 |
| **HostRole** | int 类型（LeafOnly, LeafParent, LeafParentStandby） | 使用 iota |
| **NodeRole** | int 类型（Leaf, Parent, ParentStandby） | 使用 iota |
| **Host** | 包含 ID、Role、NodeAddr、Status | ID 字段（非 HostID） |
| **Node** | 包含 NodeID、HostID、Role、Addr、ParentID、ChildrenIDs 等 | 完整的树形节点 |
| **NodeStatus** | int 类型（Init, Ready, Joining, Leaving, Failed） | 使用 iota |

### PR-033 新设计（host.go, node.go, node_address.go）

| 类型 | 定义 | 关键差异 |
|------|------|---------|
| **NodeAddress** | 仅包含 TCPPort、UDPPort | **无 Host 字段**（IP 来自 Host.Hostname） |
| **HostRole** | string 类型（leaf_only, leaf_parent, leaf_parent_standby） | **字符串类型**，MsgPack 友好 |
| **NodeRole** | int 类型（Leaf=0, Parent=1, ParentStandby=2） | 一致 |
| **Host** | HostID、Hostname、Role、LeafNodeID、ParentNodeID、ParentStandbyNodeID、状态等 | **更详细**，包含 NodeID 关联 |
| **Node** | NodeID、HostID、Addr（NodeAddress）、Role、host *Host 引用 | **host 引用**，支持地址组装 |
| **NodeStatus** | int 类型（Offline, Starting, Online, Degraded, Draining, Maintenance） | **更多状态** |

---

## 冲突点

### 1. NodeAddress 结构冲突

```go
// 现有（tree_coordinator.go）
type NodeAddress struct {
    Host     string // 完整地址（IP 或域名）
    TCPPort  int
    UDPPort  int
}

// PR-033 新设计
type NodeAddress struct {
    TCPPort  int // 仅端口
    UDPPort  int // UDP = TCP + 1
}
```

**核心差异**：PR-033 的 NodeAddress **不包含 Host 字段**，因为完整地址 = Host.Hostname + Node.Addr

### 2. Host 结构冲突

```go
// 现有（tree_coordinator.go）
type Host struct {
    ID       string      // 物理机器 ID
    Role     HostRole    // 角色
    NodeAddr NodeAddress // 网络地址
    Status   string      // 状态
}

// PR-033 新设计
type Host struct {
    HostID              string     // 机器唯一标识
    Hostname            string     // 物理机器地址
    Role                HostRole   // 角色
    LeafNodeID          string     // 关联的 Node ID
    ParentNodeID        string
    ParentStandbyNodeID string
    Status              HostStatus // 状态（枚举）
    LastHeartbeat       int64
    CPUUsage            float64
    MemUsage            float64
    ExistingNodes       int
}
```

**核心差异**：
- PR-033 使用 **HostID**（现有用 ID）
- PR-033 使用 **Hostname**（现有没有）
- PR-033 通过 NodeID 字符串关联节点（现有用 NodeAddr）
- PR-033 有更多状态和监控字段

### 3. HostRole 类型冲突

```go
// 现有（tree_coordinator.go）
type HostRole int
const (
    LeafOnly          HostRole = iota
    LeafParent
    LeafParentStandby
)

// PR-033 新设计
type HostRole string
const (
    HostRoleLeafOnly        HostRole = "leaf_only"
    HostRoleLeafParent      HostRole = "leaf_parent"
    HostRoleLeafParentStandby HostRole = "leaf_parent_standby"
)
```

**核心差异**：
- 现有：int 类型（iota）
- PR-033：string 类型（MsgPack 友好）

---

## 建议方案

### 方案 A：渐进式迁移（推荐）

**优点**：
- 最小化对现有代码的影响
- 可以分阶段迁移
- 保持向后兼容

**步骤**：

1. **阶段 1**：移除新文件中的重复定义
   - 删除 `node_address.go` 中的 NodeAddress（使用现有）
   - 删除 `host.go` 中的 HostRole（使用现有）
   - 删除 `node.go` 中的 NodeRole、NodeStatus（使用现有）

2. **阶段 2**：扩展现有类型
   - 在现有 NodeAddress 添加 Validate() 方法（UDP = TCP + 1 验证）
   - 在现有 Host 添加新字段（Hostname、LeafNodeID 等）
   - 在现有 Node 添加 host 引用和地址组装方法

3. **阶段 3**：标记旧字段为废弃
   - 添加 `// Deprecated:` 注释
   - 迁移代码使用新字段

4. **阶段 4**：完全迁移
   - 删除废弃字段
   - 统一使用 PR-033 设计

**代码示例**：

```go
// 扩展现有 NodeAddress（tree_coordinator.go）
type NodeAddress struct {
    Host     string // Deprecated: 使用 Host.Hostname 代替
    TCPPort  int
    UDPPort  int
}

// Validate 验证 UDP = TCP + 1
func (na *NodeAddress) Validate() error {
    if na.UDPPort != na.TCPPort + 1 {
        return fmt.Errorf("UDPPort must equal TCPPort + 1")
    }
    return nil
}
```

```go
// 扩展现有 Host（tree_coordinator.go）
type Host struct {
    ID       string      // Deprecated: 使用 HostID 代替
    Role     HostRole
    NodeAddr NodeAddress // Deprecated: 使用 Hostname + Node.Addr 代替
    Status   string      // Deprecated: 使用 HostStatus 代替

    // PR-033 新增字段
    HostID     string     // 机器唯一标识
    Hostname   string     // 物理机器地址（替代 NodeAddr.Host）
    LeafNodeID string     // 关联的叶子节点 ID
}
```

### 方案 B：新建子包

**优点**：
- 完全隔离新设计
- 不影响现有代码

**缺点**：
- 包路径变长（`cluster/v2`）
- 类型转换复杂
- 维护两套代码

**示例**：
```
internal/metadata/cluster/v2/host.go
internal/metadata/cluster/v2/node.go
internal/metadata/cluster/v2/node_address.go
```

### 方案 C：完全重写

**优点**：
- 清爽的架构
- 无历史包袱

**缺点**：
- 破坏性变更
- 需要大量迁移工作
- 风险较高

---

## 实施建议

**推荐方案 A**：渐进式迁移

**理由**：
1. **最小化风险**：不破坏现有代码
2. **可控节奏**：可以分 PR 迁移
3. **保持兼容**：现有功能不受影响

**具体步骤**：

1. **立即行动**（本 PR）：
   - 删除 `host.go`、`node.go`、`node_address.go` 中的重复类型定义
   - 扩展现有 `tree_coordinator.go` 中的类型
   - 添加 PR-033 需要的字段和方法

2. **后续 PR**：
   - PR-034: 迁移 NodeAddress 使用模式（Host 字段废弃）
   - PR-035: 迁移 Host 结构（添加新字段）
   - PR-036: 迁移 HostRole 类型（int → string）
   - PR-037: 清理废弃字段

---

## 参考资料

- **PR-033 文档**: `docs/06_project_management/pr_documents/feature/2026-01-29_PR-033_cluster-host-based-architecture_全流程.md`
- **现有代码**: `internal/metadata/cluster/tree_coordinator.go`
- **新代码**: `internal/metadata/cluster/host.go`, `node.go`, `node_address.go`

---

**维护者**: 👤 架构师 + 🤖 AI 团队
**最后更新**: 2026-01-29
**状态**: 📋 待讨论
