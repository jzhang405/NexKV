# 剩余任务实施计划

> **创建日期**: 2026-02-10
> **状态**: 待实施
> **优先级**: HIGH

---

## 📋 任务概览

| 任务 | 优先级 | 预计时间 | 复杂度 |
|------|--------|---------|--------|
| 依赖倒置问题修复 | 🔴 HIGH | 4-6 小时 | 高 |
| 测试文件拆分 | 🟡 MEDIUM | 2-3 小时 | 中 |

---

## 1. 依赖倒置问题修复

### 问题描述

`MetadataKV` 和 `MetadataAPI` 接口定义在 `tree_coordinator.go` 中，违反依赖倒置原则。

### 当前状态

```go
// internal/metadata/cluster/tree_coordinator.go (错误位置)
type MetadataKV interface {
    Put(ctx context.Context, ns, key string, value any) error
    Get(ctx context.Context, ns, key string, value any) error
    // ...
}
```

### 目标状态

```go
// internal/metadata/kvstore/interface.go (正确位置)
type Store interface {
    Put(ctx context.Context, ns, key string, value any) error
    Get(ctx context.Context, ns, key string, value any) error
    // ...
}

// internal/metadata/api/interface.go (正确位置)
type Provider interface {
    GetNodeInfo(ctx context.Context, nodeID string) (*types.NodeInfo, error)
    SetNodeInfo(ctx context.Context, info *types.NodeInfo) error
    // ...
}
```

### 实施步骤

#### 步骤 1: 创建 Store 接口 (kvstore 包)
```bash
# 创建文件
touch internal/metadata/kvstore/interface.go
```

```go
// Package kvstore 定义存储接口
package kvstore

import "context"

// Store 元数据存储接口
type Store interface {
    // 基础 CRUD
    Put(ctx context.Context, ns, key string, value any) error
    Get(ctx context.Context, ns, key string, value any) error
    Delete(ctx context.Context, ns, key string) error
    Exists(ctx context.Context, ns, key string) (bool, error)

    // 批量操作
    ListPrefix(ctx context.Context, ns, prefix string) ([]string, error)

    // 原始字节访问（用于同步）
    GetRaw(ctx context.Context, ns, key string) ([]byte, error)
    PutRaw(ctx context.Context, ns, key string, data []byte) error
    BatchGetRaw(ctx context.Context, ns string, keys []string) (map[string][]byte, error)

    // 生命周期
    Close() error
}
```

#### 步骤 2: 创建 Provider 接口 (api 包)
```bash
# 创建文件
touch internal/metadata/api/interface.go
```

```go
// Package api 定义元数据 API 接口
package api

import (
    "context"

    "github.com/jzhang405/NexKV/internal/metadata/types"
)

// Provider 元数据提供者接口
type Provider interface {
    // 节点操作
    GetNodeInfo(ctx context.Context, nodeID string) (*types.NodeInfo, error)
    SetNodeInfo(ctx context.Context, info *types.NodeInfo) error
    ListNodes(ctx context.Context) ([]*types.NodeInfo, error)
    DeleteNode(ctx context.Context, nodeID string) error

    // 角色操作
    GetRoleInfo(ctx context.Context, roleID string) (*types.RoleInfo, error)
    SetRoleInfo(ctx context.Context, info *types.RoleInfo) error
    ListRoles(ctx context.Context) ([]*types.RoleInfo, error)

    // 拓扑操作
    GetTopologyInfo(ctx context.Context, nodeID string) (*types.TopologyInfo, error)
    SetTopologyInfo(ctx context.Context, info *types.TopologyInfo) error

    // 分片操作
    GetShardInfo(ctx context.Context, shardID string) (*types.ShardInfo, error)
    SetShardInfo(ctx context.Context, info *types.ShardInfo) error
}
```

#### 步骤 3: 更新 TreeCoordinator 使用新接口

```go
// internal/metadata/cluster/tree_coordinator.go

// 删除旧接口定义（第 56-100 行）

// 更新字段类型
type TreeCoordinator struct {
    // ...
    metadataKV  kvstore.Store      // 使用抽象接口
    metadataAPI api.Provider        // 使用抽象接口
    // ...
}
```

#### 步骤 4: 更新 MetadataKV 实现 Store 接口

```go
// internal/metadata/kvstore/metadata_kv.go

// 添加显式接口实现检查
var _ kvstore.Store = (*MetadataKV)(nil)
```

#### 步骤 5: 更新 MetadataAPI 实现 Provider 接口

```go
// internal/metadata/api/metadata_api.go

// 添加显式接口实现检查
var _ api.Provider = (*MetadataAPI)(nil)
```

### 验证清单

- [ ] 所有接口文件创建完成
- [ ] TreeCoordinator 字段类型更新
- [ ] 编译通过 (`go build ./...`)
- [ ] 所有测试通过 (`go test ./...`)
- [ ] 无循环依赖 (`go list -f '{{.ImportPath}} {{.Imports}}' ./...`)

---

## 2. 测试文件拆分

### 问题描述

`tree_coordinator_test.go` 有 2757 行，违反 CLAUDE.md 800 行上限。

### 拆分方案

| 新文件 | 测试内容 | 预计行数 |
|--------|---------|---------|
| `tree_coordinator_core_test.go` | 核心功能（创建、启动、停止） | ~200 |
| `tree_coordinator_node_test.go` | 节点管理 | ~400 |
| `tree_coordinator_topology_test.go` | 拓扑管理 | ~400 |
| `tree_coordinator_failover_test.go` | 故障转移 | ~300 |
| `tree_coordinator_address_test.go` | 地址解析 | ~300 |
| `tree_coordinator_gossip_test.go` | Gossip 协议 | ~300 |
| `tree_coordinator_rpc_test.go` | RPC 通信 | ~300 |
| `tree_coordinator_config_test.go` | 配置和辅助 | ~300 |
| `tree_coordinator_helpers_test.go` | 辅助函数 | ~200 |

### 实施步骤

#### 步骤 1: 按功能提取测试函数

```bash
# 创建新测试文件
cd internal/metadata/cluster

# 提取地址测试（行 293-377, 1914-1931）
sed -n '293,377p;1914,1931p' tree_coordinator_test.go > tree_coordinator_address_test.go

# 提取核心测试（行 21-62, 1707-1724）
sed -n '21,62p;1707,1724p' tree_coordinator_test.go > tree_coordinator_core_test.go
```

#### 步骤 2: 为每个文件添加必要的导入

```go
// Package cluster 树形协调器测试 - 地址解析
package cluster

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)
```

#### 步骤 3: 从原文件删除已移动的测试

```bash
# 验证新文件测试通过
go test -v ./internal/metadata/cluster -run TestNodeAddress
go test -v ./internal/metadata/cluster -run TestParseNodeAddress

# 如果通过，从原文件删除相应行号
```

#### 步骤 4: 逐步拆分所有测试

使用相同的模式拆分其他测试组。

### 验证清单

- [ ] 所有新测试文件创建完成
- [ ] 每个文件 < 800 行
- [ ] 所有测试通过
- [ ] 测试覆盖率未下降 (`go test -cover`)

---

## 🔄 执行顺序

1. **先执行依赖倒置修复**（影响架构，需要更多测试）
2. **后执行测试文件拆分**（机械操作，风险较低）

---

## 📝 备注

- 两个任务都建议在 feature 分支上执行
- 完成后需要进行完整的回归测试
- 建议使用 code-reviewer agent 进行审查
