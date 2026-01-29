# Node 结构字段类型确认

> **文档类型**: 💡 技术建议
> **创建日期**: 2026-01-29
> **状态**: 📋 待讨论
> **优先级**: P0 (高)

---

## 背景说明

在进行架构调整时，用户提供了 Node 结构的新定义，但与当前实现存在不一致，需要确认字段的类型和命名。

## 问题描述

### 1. Node.Addr 字段类型不一致

**用户提供的定义**：
```go
type Node struct {
    NodeID   string              // 节点唯一标识
    HostID   string              // 所属物理机器 ID (新增字段)
    Addr      string              // 节点地址 (实际网络地址，从 Host.Addr 映射)
    Role      NodeRole           // 节点角色: Leaf, Parent
    // ...
}
```

**当前实现（已修改）**：
```go
type Node struct {
    NodeID       string
    Addr         NodeAddress  // 使用 NodeAddress 结构类型，而非 string
    ParentID     string
    ChildrenIDs  []string
    Level        int
    Status       NodeStatus
    // 缺少：HostID 和 Role 字段
}
```

**NodeAddress 结构**：
```go
type NodeAddress struct {
    IPAddress string
    TCPPort   int
    UDPPort   int
}
```

### 2. 缺少 HostID 和 Role 字段

当前 Node 结构缺少以下字段：
- `HostID string` - 所属物理机器 ID
- `Role NodeRole` - 节点角色（Leaf, Parent, ParentStandby）

### 3. YAML 配置中的 Host.Role 映射问题

YAML 示例中的 Host 角色类型：
```yaml
hosts:
  - host_id: "host-1"
    role: "leaf_parent"           # 单 Host 运行 Leaf + Parent
    role: "leaf_parent_standby"   # 单 Host 运行 Leaf + Parent Standby
```

但之前定义的 HostRole 枚举只有 3 种：
```go
type HostRole int

const (
    HostRoleLeaf         HostRole = iota  // 只运行 Leaf 节点
    HostRoleParent       HostRole = iota  // 只运行 Parent 节点
    HostRoleParentStandby HostRole = iota  // 只运行 Parent Standby 节点
)
```

缺少 `leaf_parent`（Leaf + Parent 组合）和 `leaf_parent_standby`（Leaf + Parent Standby 组合）的映射。

## 建议方案

### 方案 A：保持 NodeAddress 结构类型

**优点**：
- 类型安全，强类型约束
- 便于 IPFS 格式转换（TCPAddr() 和 UDPAddr() 方法）
- 端口类型明确（int）

**缺点**：
- 需要在所有使用 Addr 的地方调用 `ParseNodeAddress()` 进行转换
- YAML 解析需要额外处理

**适用场景**：
- 强调类型安全的架构设计

### 方案 B：使用 string 类型

**优点**：
- 简化代码，直接使用字符串
- YAML 解析更简单

**缺点**：
- 失去类型安全
- 端口解析需要运行时处理

**适用场景**：
- 追求简化的实现

### 方案 C：添加 HostID 和 Role 字段到 Node 结构

在 Node 结构中添加这两个字段：

```go
type Node struct {
    NodeID       string
    HostID       string        // 新增：所属物理机器 ID
    Addr          NodeAddress  // 或 string，取决于方案 A/B
    Role          NodeRole     // 新增：节点角色
    ParentID     string
    ChildrenIDs  []string
    Level        int
    Status       NodeStatus
    Priority     int
    LastHeartbeat time.Time
    Metadata     map[string]string
}
```

### 方案 D：扩展 HostRole 枚举以支持组合模式

添加新的组合角色类型：

```go
type HostRole int

const (
    HostRoleLeaf             HostRole = iota  // 只运行 Leaf 节点
    HostRoleParent           HostRole = iota  // 只运行 Parent 节点
    HostRoleParentStandby    HostRole = iota  // 只运行 Parent Standby 节点
    HostRoleLeafParent       HostRole = iota  // 单 Host 运行 Leaf + Parent
    HostRoleLeafParentStandby HostRole = iota  // 单 Host 运行 Leaf + Parent Standby
)
```

## 实施建议

**优先级：P0（高）**

需要用户确认以下问题：

1. **Node.Addr 字段类型**：
   - 选项 A：保持 `NodeAddress` 结构类型（推荐，类型安全）
   - 选项 B：改为 `string` 类型（简化，但失去类型安全）

2. **是否添加 HostID 和 Role 字段**：
   - 用户定义中明确包含这两个字段
   - 当前实现缺少这两个字段

3. **Host.Role 枚举是否需要支持组合模式**：
   - YAML 示例中有 `leaf_parent` 和 `leaf_parent_standby`
   - 需要确认是否需要在 HostRole 中添加这两个组合类型

## 参考资料

- `docs/06_project_management/brainstorm/cluster_2026-01-29_host-based-architecture-proposal.md` - Host 基础架构提案
- `internal/metadata/cluster/tree_coordinator.go` - 当前 Node 结构定义

---

**维护者**: NexKV 开发团队
**最后更新**: 2026-01-29
