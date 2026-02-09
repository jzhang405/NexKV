# TreeCoordinator 元数据管理简化设计方案

> **文档类型**: 📚 技术设计方案
> **创建日期**: 2026-02-09
> **状态**: 📝 草稿（架构讨论后简化）
> **优先级**: P0 (高)
> **替代文档**: `cluster_2026-02-09_tree-coordinator-metadata-management.md`（已废弃）

---

## 📊 执行摘要

**设计目标**: 为 TreeCoordinator 设计一套简单、高效的元数据管理系统。

**核心原则**: **元数据本质上也是 KV**，通过命名空间前缀隔离，使用 MVStore 存储。

**关键决策**:
- 存储方式：**KV 存储**（非复杂接口体系）
- 序列化格式：**MsgPack**（二进制高效）
- 版本控制：**MVCC**（Multi-Version Concurrency Control）
- 查询接口：**封装一层**（内部调用 KV）
- 现有代码：**替换** `map[string]string`

---

## 🎯 核心架构

### 元数据 = 特殊 KV

```
┌─────────────────────────────────────────────────────────────┐
│  元数据 KV 存储架构                                          │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  命名空间前缀（9 类）:                                         │
│  ┌────────────────────────────────────────────────────────┐  │
│  │ meta:cluster:global     → ClusterInfo  (集群级全局)    │  │
│  │ meta:node:{nodeID}      → NodeInfo                    │  │
│  │ meta:role:{nodeID}      → RoleInfo（含 Standby）      │  │
│  │ meta:topo:{nodeID}      → TopologyInfo               │  │
│  │ meta:shard:{shardID}    → ShardInfo                   │  │
│  │ meta:static:{nodeID}    → StaticInfo                  │  │
│  │ meta:dynamic:{nodeID}   → DynamicInfo                 │  │
│  │ meta:op:{nodeID}        → OperationInfo               │  │
│  │ meta:version:{key}      → VersionInfo (MVCC)          │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                              │
│  底层存储: MVStore (已有组件)                               │
│  ├─ WAL 支持                                                 │
│  ├─ 快照恢复                                                 │
│  └─ MVCC 支持                                                │
│                                                              │
│  上层封装:                                                   │
│  ┌────────────────────────────────────────────────────────┐  │
│  │ GetClusterInfo()        → Get("meta:cluster:global")  │  │
│  │ GetNodeInfo(nodeID)     → Get("meta:node:node-001")   │  │
│  │ SetNodeInfo(nodeID, i)  → Put("meta:node:node-001", i) │  │
│  │ ListNodes()             → ListPrefix("meta:node:")       │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## 📁 目录

1. [命名空间设计](#1-命名空间设计)
2. [数据结构定义](#2-数据结构定义)
3. [存储层实现](#3-存储层实现)
4. [封装接口](#4-封装接口)
5. [MVCC 版本控制](#5-mvcc-版本控制)
6. [与 TreeCoordinator 集成](#6-与-treecoordinator-集成)
7. [代码实现](#7-代码实现)
8. [测试计划](#8-测试计划)

---

## 1. 命名空间设计

### 1.1 命名空间前缀规范

| 命名空间 | 前缀 | 数据类型 | 说明 |
|---------|------|----------|------|
| **集群元数据** | `meta:cluster:` | ClusterInfo | 集群配置、状态、全局拓扑 |
| **节点元数据** | `meta:node:` | NodeInfo | 节点基本信息、状态、资源 |
| **角色元数据** | `meta:role:` | RoleInfo | 角色、端口、Standby、转换历史 |
| **拓扑元数据** | `meta:topo:` | TopologyInfo | 父子关系、层级、树管理 |
| **分片元数据** | `meta:shard:` | ShardInfo | 分片分配、副本、迁移 |
| **静态元数据** | `meta:static:` | StaticInfo | 启动后不变的信息 |
| **动态元数据** | `meta:dynamic:` | DynamicInfo | 高频变化的指标 |
| **运维元数据** | `meta:op:` | OperationInfo | 标签、位置、配置 |
| **版本元数据** | `meta:version:` | VersionInfo | MVCC 版本控制 |

### 1.2 Key 格式规范

```go
// 元数据 Key 格式
// 格式: {namespace}:{identifier}
// 示例: meta:node:node-001

const (
    NamespaceNode     = "meta:node:"
    NamespaceRole     = "meta:role:"
    NamespaceTopo     = "meta:topo:"
    NamespaceShard    = "meta:shard:"
    NamespaceStatic   = "meta:static:"
    NamespaceDynamic  = "meta:dynamic:"
    NamespaceOp       = "meta:op:"
    NamespaceVersion  = "meta:version:"
)

// BuildKey 构建 KV Key
func BuildKey(namespace, identifier string) string {
    return namespace + identifier
}

// ParseKey 解析 KV Key
func ParseKey(key string) (namespace, identifier string) {
    idx := strings.Index(key, ":")
    if idx == -1 {
        return "", key
    }
    return key[:idx+1], key[idx+1:]
}
```

---

## 2. 数据结构定义

### 2.1 集群元数据 (ClusterInfo) ⭐ 集群级

```go
// ClusterInfo 集群级元数据
//
// **设计说明** (2026-02-09):
// - 用于存储集群级别的全局配置和状态
// - 全局唯一，使用固定标识符 "global"
// - 与节点级元数据分离，避免混淆
type ClusterInfo struct {
    // ===== 集群标识 =====
    ClusterID     string    `msgpack:"cluster_id"`
    ClusterName   string    `msgpack:"cluster_name"`

    // ===== 集群状态 =====
    State           ClusterState `msgpack:"state"`
    ClusterVersion  string       `msgpack:"cluster_version"` // 集群版本号

    // ===== 全局拓扑信息 =====
    RootNodeIDs   []string  `msgpack:"root_node_ids"`   // 根节点列表
    TreeDepth     int       `msgpack:"tree_depth"`      // 树的深度
    TotalNodes    int       `msgpack:"total_nodes"`     // 节点总数
    TotalShards   int       `msgpack:"total_shards"`    // 分片总数

    // ===== 全局配置 =====
    QuorumThreshold int           `msgpack:"quorum_threshold"` // Quorum 阈值
    GossipInterval  time.Duration `msgpack:"gossip_interval"`  // Gossip 间隔
    HeartbeatTimeout time.Duration `msgpack:"heartbeat_timeout"` // 心跳超时

    // ===== 时间戳 =====
    CreatedAt     time.Time `msgpack:"created_at"`
    UpdatedAt     time.Time `msgpack:"updated_at"`

    // ===== MVCC 版本号 =====
    Version       uint64 `msgpack:"version"`
}

// ClusterState 集群状态
type ClusterState int

const (
    ClusterStateInit        ClusterState = iota // 初始状态
    ClusterStateForming                         // 集群形成中
    ClusterStateActive                          // 活跃状态
    ClusterStateDegraded                        // 降级状态（部分节点故障）
    ClusterStateDissolving                      // 解散中
)
```

### 2.2 节点元数据 (NodeInfo)

### 2.1 节点元数据 (NodeInfo)

```go
// NodeInfo 节点信息（合并 Static + Dynamic）
type NodeInfo struct {
    // 基本信息
    NodeID       string       `msgpack:"node_id"`
    HostID       string       `msgpack:"host_id"`
    Hostname     string       `msgpack:"hostname"`
    Addr         NodeAddress  `msgpack:"addr"`

    // 状态信息
    Status       NodeStatus   `msgpack:"status"`
    LastHeartbeat time.Time    `msgpack:"last_heartbeat"`

    // 资源使用率
    CPUUsage     float64  `msgpack:"cpu_usage"`      // CPU 使用率 (0-100)
    MemoryUsage  float64  `msgpack:"memory_usage"`   // 内存使用率 (0-100)
    DiskUsage    float64  `msgpack:"disk_usage"`     // 磁盘使用率 (0-100)
    NetworkIn    int64    `msgpack:"network_in"`     // 网络入流量 (bytes/s)
    NetworkOut   int64    `msgpack:"network_out"`    // 网络出流量 (bytes/s)

    // 负载指标
    ActiveConnections int     `msgpack:"active_connections"`
    RequestCount       int64  `msgpack:"request_count"`
    RequestRate        float64 `msgpack:"request_rate"`     // 请求速率 (req/s)
    AvgLatency         float64 `msgpack:"avg_latency"`       // 平均延迟 (ms)

    // 健康指标
    HealthScore  float64 `msgpack:"health_score"`  // 健康分数 (0-100)
    ErrorRate    float64 `msgpack:"error_rate"`    // 错误率 (0-1)

    // MVCC 版本号
    Version      uint64  `msgpack:"version"`
    UpdatedAt    time.Time `msgpack:"updated_at"`
}
```

### 2.2 角色元数据 (RoleInfo)

```go
// RoleInfo 角色信息（包含 Standby 管理）
//
// **设计决策** (2026-02-09):
// - Role 变动少但重要，需要专门管理
// - NodeRole: Leaf 静态, Parent/ParentStandby 动态
// - HostRole: 全部动态
// - Standby 合并入 RoleInfo
// - 转换历史保留 100 条（可配置）
type RoleInfo struct {
    // ===== 当前角色 =====
    CurrentHostRole  HostRole   `msgpack:"host_role"`  // 物理机器角色
    CurrentNodeRole  NodeRole   `msgpack:"node_role"`  // 逻辑节点角色

    // ===== 角色状态 =====
    RoleState        RoleState  `msgpack:"role_state"`       // 角色状态
    LastStateChange  time.Time  `msgpack:"last_state_change"` // 最后状态变更时间

    // ===== 配置参数 =====
    MaxChildren      int        `msgpack:"max_children"` // 最大子节点数
    MaxLevel         int        `msgpack:"max_level"`    // 最大层级

    // ===== 端口分配 =====
    LeafNodeTCPPort     int        `msgpack:"leaf_tcp_port"`      // 叶子节点 TCP 端口（静态）
    ParentNodeTCPPort   int        `msgpack:"parent_tcp_port"`    // 父节点 TCP 端口（动态，0表示无）
    StandbyNodeTCPPort  int        `msgpack:"standby_tcp_port"`   // Standby 节点 TCP 端口（动态，0表示无）

    // ===== Standby 管理 =====
    StandbyState         StandbyState      `msgpack:"standby_state"`
    PrimaryNodeID        string            `msgpack:"primary_node_id"`
    LastPrimaryHeartbeat time.Time         `msgpack:"last_primary_heartbeat"`
    MissedHeartbeats     int               `msgpack:"missed_heartbeats"`
    FailoverThreshold    int               `msgpack:"failover_threshold"`     // 默认 3
    FailoverTimeout      time.Duration     `msgpack:"failover_timeout"`       // 默认 15s
    FailoverHistory      []*FailoverRecord `msgpack:"failover_history"`      // 最近 100 条

    // ===== 转换历史（可配置，默认 100 条）=====
    TransitionHistory    []*RoleTransition `msgpack:"transition_history"`
    MaxHistorySize       int               `msgpack:"max_history_size"`      // 配置项

    // ===== MVCC 版本号 =====
    Version              uint64            `msgpack:"version"`
}

// RoleState 角色状态
type RoleState int

const (
    RoleStateStable       RoleState = iota // 稳定状态
    RoleStatePromoting                      // 升级中（Leaf → Parent）
    RoleStateDemoting                       // 降级中（Parent → Leaf）
    RoleStateStandbyActivating             // 激活 Standby 中
)

// StandbyState Standby 节点状态
type StandbyState int

const (
    StandbyStateActive     StandbyState = iota // 活跃（正常监控）
    StandbyStatePromoting                       // 升级中（接管中）
    StandbyStatePromoted                        // 已接管
    StandbyStateDemoting                        // 降级中
)

// RoleTransition 角色转换记录
type RoleTransition struct {
    TransitionID   string    `msgpack:"transition_id"`
    HostID         string    `msgpack:"host_id"`
    OldRole        HostRole  `msgpack:"old_role"`
    NewRole        HostRole  `msgpack:"new_role"`
    OldNodeRole    NodeRole  `msgpack:"old_node_role"`
    NewNodeRole    NodeRole  `msgpack:"new_node_role"`
    Timestamp      time.Time `msgpack:"timestamp"`
    Reason         string    `msgpack:"reason"`
    Initiator      string    `msgpack:"initiator"`      // local | remote
    Success        bool      `msgpack:"success"`
}

// FailoverRecord 故障切换记录
type FailoverRecord struct {
    Timestamp   time.Time     `msgpack:"timestamp"`
    FromNodeID  string        `msgpack:"from_node_id"`
    ToNodeID    string        `msgpack:"to_node_id"`
    Reason      string        `msgpack:"reason"`
    Success     bool          `msgpack:"success"`
    Duration    time.Duration `msgpack:"duration"`
    TriggeredBy string        `msgpack:"triggered_by"`
}
```

### 2.3 拓扑元数据 (TopologyInfo)

```go
// TopologyInfo 拓扑信息（树形结构管理分组）
//
// **设计决策** (2026-02-09):
// - 不使用独立的 GroupMetadata，树形结构本身管理分组
// - 父节点 = 组中心，子节点列表 = 组成员
// - 通过父节点判断是否在某个分组
type TopologyInfo struct {
    // ===== 树形结构 =====
    NodeID        string   `msgpack:"node_id"`        // 节点 ID
    ParentID      string   `msgpack:"parent_id"`
    ChildrenIDs   []string `msgpack:"children_ids"`
    Level         int      `msgpack:"level"`
    Path          []string `msgpack:"path"`           // 从根节点到当前节点的路径
    SubtreeSize   int      `msgpack:"subtree_size"`   // 子树节点数

    // ===== 一致性配置（分组内）=====
    ConsistencyMode ConsistencyMode `msgpack:"consistency_mode"` // 组内一致性模式
    QuorumThreshold int             `msgpack:"quorum_threshold"` // Quorum 阈值

    // ===== MVCC 版本号 =====
    Version       uint64 `msgpack:"version"`
    UpdatedAt     time.Time `msgpack:"updated_at"`
}

// ConsistencyMode 一致性模式
type ConsistencyMode int

const (
    ConsistencyQuorum  ConsistencyMode = iota // Quorum 强一致
    ConsistencyGossip                       // Gossip 最终一致
    ConsistencyHybrid                        // 混合模式
)
```

### 2.4 分片元数据 (ShardInfo)

```go
// ShardInfo 分片信息
type ShardInfo struct {
    // 基本信息
    ShardID           string   `msgpack:"shard_id"`
    TableID           string   `msgpack:"table_id"`

    // 副本节点
    ReplicaNodeIDs    []string `msgpack:"replica_node_ids"` // 副本节点 ID 列表
    ReplicationFactor int     `msgpack:"replication_factor"`
    IsPrimary         bool    `msgpack:"is_primary"`

    // 数据统计
    DataSize          int64   `msgpack:"data_size"`    // 数据大小（bytes）
    KeyCount          int64   `msgpack:"key_count"`    // 键数量

    // 迁移状态
    MigrationState    *ShardMigrationState `msgpack:"migration_state"` // 迁移中才有值

    // ===== MVCC 版本号 =====
    Version           uint64  `msgpack:"version"`
    UpdatedAt         time.Time `msgpack:"updated_at"`
}

// ShardMigrationState 分片迁移状态
type ShardMigrationState struct {
    MigrationID       string        `msgpack:"migration_id"`
    SourceNodeID      string        `msgpack:"source_node_id"`
    TargetNodeID      string        `msgpack:"target_node_id"`
    Phase             MigrationPhase `msgpack:"phase"`
    Progress          int           `msgpack:"progress"`          // 0-100
    StartTime         time.Time     `msgpack:"start_time"`
    EstimatedEndTime   time.Time     `msgpack:"estimated_end_time"`
}

// MigrationPhase 迁移阶段
type MigrationPhase int

const (
    MigrationPhasePending   MigrationPhase = iota // 待开始
    MigrationPhaseBootstrapping                   // 启动中
    MigrationPhaseSyncing                         // 同步中
    MigrationPhaseCutover                         // 切换中
    MigrationPhaseComplete                        // 已完成
    MigrationPhaseFailed                          // 已失败
)
```

### 2.5 其他元数据类型

```go
// StaticInfo 静态信息（启动后不变）
type StaticInfo struct {
    NodeID       string    `msgpack:"node_id"`
    Addr         string    `msgpack:"addr"`
    Hostname     string    `msgpack:"hostname"`
    StartupTime  time.Time `msgpack:"startup_time"`
    Version      string    `msgpack:"version"`      // 版本信息
    GitCommit    string    `msgpack:"git_commit"`
    BuildTime    time.Time `msgpack:"build_time"`
    CPUCount     int       `msgpack:"cpu_count"`
    MemoryBytes  int64     `msgpack:"memory_bytes"`
    DiskBytes    int64     `msgpack:"disk_bytes"`
    Version      uint64    `msgpack:"mvcc_version"`
}

// DynamicInfo 动态信息（高频变化，TTL 5 分钟）
type DynamicInfo struct {
    NodeID       string    `msgpack:"node_id"`
    Status       NodeStatus `msgpack:"status"`
    LastHeartbeat time.Time `msgpack:"last_heartbeat"`
    CPUUsage     float64   `msgpack:"cpu_usage"`
    MemoryUsage  float64   `msgpack:"memory_usage"`
    DiskUsage    float64   `msgpack:"disk_usage"`
    NetworkIn    int64     `msgpack:"network_in"`
    NetworkOut   int64     `msgpack:"network_out"`
    ActiveConnections int    `msgpack:"active_connections"`
    RequestCount     int64  `msgpack:"request_count"`
    RequestRate      float64 `msgpack:"request_rate"`
    AvgLatency       float64 `msgpack:"avg_latency"`
    HealthScore     float64 `msgpack:"health_score"`
    ErrorRate        float64 `msgpack:"error_rate"`
    TTL             time.Duration `msgpack:"ttl"` // 过期时间
    Version          uint64    `msgpack:"version"`
}

// OperationInfo 运维信息（低频变化）
type OperationInfo struct {
    NodeID            string            `msgpack:"node_id"`
    Labels            map[string]string `msgpack:"labels"`
    Datacenter        string            `msgpack:"datacenter"`
    Rack              string            `msgpack:"rack"`
    Zone              string            `msgpack:"zone"`
    Host              string            `msgpack:"host"`
    Weight            int               `msgpack:"weight"`
    Priority          int               `msgpack:"priority"`
    EnabledFeatures   []string          `msgpack:"enabled_features"`
    CustomProperties  map[string]string `msgpack:"custom_properties"`
    Version           uint64            `msgpack:"version"`
}

// VersionInfo 版本信息（MVCC）
type VersionInfo struct {
    Key           string    `msgpack:"key"`            // 业务 KV Key
    Version       uint64    `msgpack:"version"`       // 版本号
    CreatedAt     time.Time `msgpack:"created_at"`
    UpdatedAt     time.Time `msgpack:"updated_at"`
    Creator       string    `msgpack:"creator"`        // 创建者
}
```

---

## 3. 存储层实现

### 3.1 MetadataKV 核心

```go
// internal/metadata/cluster/metadata_kv.go

package cluster

import (
    "context"
    "fmt"
    "strings"
    "sync"

    "github.com/jzhang405/NexKV/internal/clock"
    "github.com/jzhang405/NexKV/internal/metadata/types"
    "github.com/jzhang405/NexKV/internal/wal"
    "github.com/vmihailenco/msgpack/v5"
)

// MetadataKV 元数据 KV 存储（简化版）
type MetadataKV struct {
    store   store.MVStore  // 底层 KV 存储
    hlc     *clock.HLC      // 混合逻辑时钟
    mu      sync.RWMutex     // 读写锁
    config  *MetadataKVConfig
}

// MetadataKVConfig 配置
type MetadataKVConfig struct {
    // TTL 配置
    DynamicMetadataTTL time.Duration `yaml:"dynamic_metadata_ttl"` // 默认 5 分钟

    // 历史记录配置
    MaxTransitionHistory int `yaml:"max_transition_history"` // 默认 100
    MaxFailoverHistory   int `yaml:"max_failover_history"`   // 默认 100
}

// NewMetadataKV 创建元数据 KV 存储
func NewMetadataKV(store store.MVStore, hlc *clock.HLC, config *MetadataKVConfig) *MetadataKV {
    return &MetadataKV{
        store:  store,
        hlc:    hlc,
        config: config,
    }
}
```

### 3.2 基础 KV 操作

```go
// ===== 基础 KV 操作 =====

// Get 获取元数据
func (kv *MetadataKV) Get(namespace, identifier string, value interface{}) error {
    kv.mu.RLock()
    defer kv.mu.RUnlock()

    kvKey := BuildKey(namespace, identifier)
    data, err := kv.store.Get(kvKey)
    if err != nil {
        return fmt.Errorf("获取元数据失败: %s, 错误: %w", kvKey, err)
    }

    if err := msgpack.Unmarshal(data, value); err != nil {
        return fmt.Errorf("反序列化元数据失败: %s, 错误: %w", kvKey, err)
    }

    return nil
}

// Put 设置元数据（自动添加版本号和更新时间）
func (kv *MetadataKV) Put(namespace, identifier string, value interface{}) error {
    kv.mu.Lock()
    defer kv.mu.Unlock()

    kvKey := BuildKey(namespace, identifier)

    // 序列化
    data, err := msgpack.Marshal(value)
    if err != nil {
        return fmt.Errorf("序列化元数据失败: %s, 错误: %w", kvKey, err)
    }

    // 存储到 MVStore
    if err := kv.store.Put(kvKey, data); err != nil {
        return fmt.Errorf("存储元数据失败: %s, 错误: %w", kvKey, err)
    }

    return nil
}

// Delete 删除元数据
func (kv *MetadataKV) Delete(namespace, identifier string) error {
    kv.mu.Lock()
    defer kv.mu.Unlock()

    kvKey := BuildKey(namespace, identifier)
    if err := kv.store.Delete(kvKey); err != nil {
        return fmt.Errorf("删除元数据失败: %s, 错误: %w", kvKey, err)
    }

    return nil
}

// ListPrefix 列举指定前缀的所有 Key
func (kv *MetadataKV) ListPrefix(namespace string) ([]string, error) {
    kv.mu.RLock()
    defer kv.mu.RUnlock()

    keys, err := kv.store.ListPrefix(namespace, 0, -1)
    if err != nil {
        return nil, fmt.Errorf("列举元数据失败: %s, 错误: %w", namespace, err)
    }

    return keys, nil
}

// Exists 检查元数据是否存在
func (kv *MetadataKV) Exists(namespace, identifier string) bool {
    kv.mu.RLock()
    defer kv.mu.RUnlock()

    kvKey := BuildKey(namespace, identifier)
    _, err := kv.store.Get(kvKey)
    return err == nil
}
```

### 3.3 批量操作

```go
// ===== 批量操作 =====

// GetBatch 批量获取元数据
func (kv *MetadataKV) GetBatch(namespace string, identifiers []string) (map[string]interface{}, error) {
    result := make(map[string]interface{})

    for _, id := range identifiers {
        var value interface{}
        // 根据命名空间确定类型
        switch namespace {
        case NamespaceNode:
            value = &NodeInfo{}
        case NamespaceRole:
            value = &RoleInfo{}
        case NamespaceTopo:
            value = &TopologyInfo{}
        case NamespaceShard:
            value = &ShardInfo{}
        case NamespaceStatic:
            value = &StaticInfo{}
        case NamespaceDynamic:
            value = &DynamicInfo{}
        case NamespaceOp:
            value = &OperationInfo{}
        default:
            return nil, fmt.Errorf("未知命名空间: %s", namespace)
        }

        if err := kv.Get(namespace, id, value); err != nil {
            // 记录错误但继续处理其他项
            continue
        }
        result[id] = value
    }

    return result, nil
}

// PutBatch 批量设置元数据
func (kv *MetadataKV) PutBatch(namespace string, items map[string]interface{}) error {
    for id, value := range items {
        if err := kv.Put(namespace, id, value); err != nil {
            return fmt.Errorf("设置元数据失败: %s:%s, 错误: %w", namespace, id, err)
        }
    }
    return nil
}
```

---

## 4. 封装接口

### 4.1 集群元数据接口 ⭐ 集群级

```go
// ===== 集群元数据封装接口 =====

// GetClusterInfo 获取集群信息
func (kv *MetadataKV) GetClusterInfo() (*ClusterInfo, error) {
    var info ClusterInfo
    if err := kv.Get(NamespaceCluster, "global", &info); err != nil {
        return nil, fmt.Errorf("获取集群信息失败: %w", err)
    }
    return &info, nil
}

// SetClusterInfo 设置集群信息
func (kv *MetadataKV) SetClusterInfo(info *ClusterInfo) error {
    info.Version = kv.hlc.Now().Value()
    info.UpdatedAt = time.Now()
    return kv.Put(NamespaceCluster, "global", info)
}

// InitClusterInfo 初始化集群信息
func (kv *MetadataKV) InitClusterInfo(clusterID, clusterName string) error {
    info := &ClusterInfo{
        ClusterID:     clusterID,
        ClusterName:   clusterName,
        State:         ClusterStateInit,
        Version:       0,
        RootNodeIDs:   []string{},
        TotalNodes:    0,
        TotalShards:   0,
        CreatedAt:     time.Now(),
        UpdatedAt:     time.Now(),
    }
    return kv.SetClusterInfo(info)
}

// GetClusterState 获取集群状态
func (kv *MetadataKV) GetClusterState() (ClusterState, error) {
    info, err := kv.GetClusterInfo()
    if err != nil {
        return ClusterStateInit, err
    }
    return info.State, nil
}

// UpdateClusterState 更新集群状态
func (kv *MetadataKV) UpdateClusterState(state ClusterState) error {
    info, err := kv.GetClusterInfo()
    if err != nil {
        return err
    }
    info.State = state
    return kv.SetClusterInfo(info)
}

// AddRootNode 添加根节点
func (kv *MetadataKV) AddRootNode(nodeID string) error {
    info, err := kv.GetClusterInfo()
    if err != nil {
        return err
    }
    info.RootNodeIDs = append(info.RootNodeIDs, nodeID)
    return kv.SetClusterInfo(info)
}

// RemoveRootNode 移除根节点
func (kv *MetadataKV) RemoveRootNode(nodeID string) error {
    info, err := kv.GetClusterInfo()
    if err != nil {
        return err
    }
    for i, id := range info.RootNodeIDs {
        if id == nodeID {
            info.RootNodeIDs = append(info.RootNodeIDs[:i], info.RootNodeIDs[i+1:]...)
            break
        }
    }
    return kv.SetClusterInfo(info)
}
```

### 4.2 节点元数据接口

```go
// ===== 节点元数据封装接口 =====

// GetNodeInfo 获取节点信息
func (kv *MetadataKV) GetNodeInfo(nodeID string) (*NodeInfo, error) {
    var info NodeInfo
    if err := kv.Get(NamespaceNode, nodeID, &info); err != nil {
        return nil, fmt.Errorf("获取节点信息失败: %s, 错误: %w", nodeID, err)
    }
    return &info, nil
}

// SetNodeInfo 设置节点信息
func (kv *MetadataKV) SetNodeInfo(info *NodeInfo) error {
    info.Version = kv.hlc.Now().Value()
    info.UpdatedAt = time.Now()
    return kv.Put(NamespaceNode, info.NodeID, info)
}

// ListNodes 列出所有节点
func (kv *MetadataKV) ListNodes() ([]*NodeInfo, error) {
    keys, err := kv.ListPrefix(NamespaceNode)
    if err != nil {
        return nil, err
    }

    nodes := make([]*NodeInfo, 0, len(keys))
    for _, key := range keys {
        // 提取 nodeID（去掉前缀）
        nodeID := strings.TrimPrefix(key, NamespaceNode)

        info, err := kv.GetNodeInfo(nodeID)
        if err != nil {
            continue
        }
        nodes = append(nodes, info)
    }

    return nodes, nil
}
```

### 4.3 角色元数据接口

```go
// ===== 角色元数据封装接口 =====

// GetRoleInfo 获取角色信息
func (kv *MetadataKV) GetRoleInfo(nodeID string) (*RoleInfo, error) {
    var info RoleInfo
    if err := kv.Get(NamespaceRole, nodeID, &info); err != nil {
        return nil, fmt.Errorf("获取角色信息失败: %s, 错误: %w", nodeID, err)
    }
    return &info, nil
}

// SetRoleInfo 设置角色信息
func (kv *MetadataKV) SetRoleInfo(info *RoleInfo) error {
    info.Version = kv.hlc.Now().Value()
    return kv.Put(NamespaceRole, info.NodeID, info)
}

// AddRoleTransition 添加角色转换记录
func (kv *MetadataKV) AddRoleTransition(nodeID string, transition *RoleTransition) error {
    info, err := kv.GetRoleInfo(nodeID)
    if err != nil {
        return err
    }

    info.TransitionHistory = append(info.TransitionHistory, transition)

    // 清理超过限制的历史记录
    if len(info.TransitionHistory) > info.MaxHistorySize {
        info.TransitionHistory = info.TransitionHistory[len(info.TransitionHistory)-info.MaxHistorySize:]
    }

    return kv.SetRoleInfo(info)
}

// GetRoleTransitionHistory 获取角色转换历史
func (kv *MetadataKV) GetRoleTransitionHistory(nodeID string) ([]*RoleTransition, error) {
    info, err := kv.GetRoleInfo(nodeID)
    if err != nil {
        return nil, err
    }
    return info.TransitionHistory, nil
}
```

### 4.4 拓扑元数据接口

```go
// ===== 拓扑元数据封装接口 =====

// GetTopologyInfo 获取拓扑信息
func (kv *MetadataKV) GetTopologyInfo(nodeID string) (*TopologyInfo, error) {
    var info TopologyInfo
    if err := kv.Get(NamespaceTopo, nodeID, &info); err != nil {
        return nil, fmt.Errorf("获取拓扑信息失败: %s, 错误: %w", nodeID, err)
    }
    return &info, nil
}

// SetTopologyInfo 设置拓扑信息
func (kv *MetadataKV) SetTopologyInfo(info *TopologyInfo) error {
    info.Version = kv.hlc.Now().Value()
    info.UpdatedAt = time.Now()
    return kv.Put(NamespaceTopo, info.NodeID, info)
}

// GetGroupMembers 获取同组成员（通过父节点获取）
func (kv *MetadataKV) GetGroupMembers(nodeID string) ([]string, error) {
    info, err := kv.GetTopologyInfo(nodeID)
    if err != nil {
        return nil, err
    }

    if info.ParentID == "" {
        // 根节点，独立分组
        return []string{nodeID}, nil
    }

    // 获取父节点的拓扑信息
    parentInfo, err := kv.GetTopologyInfo(info.ParentID)
    if err != nil {
        return nil, err
    }

    return parentInfo.ChildrenIDs, nil
}
```

---

## 5. MVCC 版本控制

### 5.1 版本控制设计

```go
// ===== MVCC 版本控制 =====

// GetVersion 获取元数据版本
func (kv *MetadataKV) GetVersion(namespace, identifier string) (uint64, error) {
    // 构造版本 Key
    versionKey := BuildKey(NamespaceVersion, namespace+identifier)

    data, err := kv.store.Get(versionKey)
    if err != nil {
        return 0, err
    }

    var versionInfo struct {
        Version uint64 `msgpack:"version"`
    }
    if err := msgpack.Unmarshal(data, &versionInfo); err != nil {
        return 0, err
    }

    return versionInfo.Version, nil
}

// UpdateVersion 更新元数据版本
func (kv *MetadataKV) UpdateVersion(namespace, identifier string) (uint64, error) {
    newVersion := kv.hlc.Now().Value()

    // 构造版本 Key
    versionKey := BuildKey(NamespaceVersion, namespace+identifier)

    versionInfo := struct {
        Version: newVersion,
    }

    data, err := msgpack.Marshal(versionInfo)
    if err != nil {
        return 0, err
    }

    if err := kv.store.Put(versionKey, data); err != nil {
        return 0, err
    }

    return newVersion, nil
}
```

### 5.2 版本比较

```go
// CompareVersions 比较两个版本
// 返回值: 1 = v1 > v2, -1 = v1 < v2, 0 = v1 == v2
func CompareVersions(v1, v2 uint64) int {
    if v1 > v2 {
        return 1
    } else if v1 < v2 {
        return -1
    }
    return 0
}
```

---

## 6. 与 TreeCoordinator 集成

### 6.1 替换现有 Metadata map

```go
// internal/metadata/cluster/tree_coordinator.go

type TreeCoordinator struct {
    // ... 其他字段 ...

    // 旧的元数据（将被替换）
    // Metadata map[string]string  // ❌ 删除

    // 新的元数据 KV 存储
    metadataKV *MetadataKV  // ✅ 新增
}

// 替换现有使用 Metadata 的代码
func (tc *TreeCoordinator) GetNodeMetadata(nodeID string) (*NodeInfo, error) {
    return tc.metadataKV.GetNodeInfo(nodeID)
}

func (tc *TreeCoordinator) SetNodeMetadata(info *NodeInfo) error {
    return tc.metadataKV.SetNodeInfo(info)
}
```

### 6.2 初始化 MetadataKV

```go
// NewTreeCoordinator 创建树形协调器
func NewTreeCoordinator(
    localNodeID string,
    localAddr string,
    config *TreeCoordinatorConfig,
    clusterConfig *metadataconfig.ClusterConfig,
    libp2pHost host.Host,
) (*TreeCoordinator, error) {
    // ... 现有初始化代码 ...

    // 初始化 MetadataKV
    mvstore := // ... 获取 MVStore 实例
    hlc := clock.NewHLC()

    metadataKVConfig := &MetadataKVConfig{
        DynamicMetadataTTL:  5 * time.Minute,
        MaxTransitionHistory: 100,
        MaxFailoverHistory:   100,
    }

    metadataKV := NewMetadataKV(mvstore, hlc, metadataKVConfig)

    coordinator := &TreeCoordinator{
        // ... 其他字段 ...
        metadataKV: metadataKV,
    }

    return coordinator, nil
}
```

---

## 7. 代码实现

### 7.1 文件结构

```
internal/metadata/cluster/
├── metadata_kv.go           # MetadataKV 核心实现
├── metadata_api.go           # 封装接口（节点、角色、拓扑等）
├── metadata_mvcc.go          # MVCC 版本控制
├── metadata_sync.go          # 元数据同步（Gossip）
└── metadata_types.go         # 数据结构定义
```

### 7.2 数据结构定义文件

```go
// internal/metadata/cluster/metadata_types.go

package cluster

// ===== 命名空间常量 =====

const (
    NamespaceCluster  = "meta:cluster:"
    NamespaceNode     = "meta:node:"
    NamespaceRole     = "meta:role:"
    NamespaceTopo     = "meta:topo:"
    NamespaceShard    = "meta:shard:"
    NamespaceStatic   = "meta:static:"
    NamespaceDynamic  = "meta:dynamic:"
    NamespaceOp       = "meta:op:"
    NamespaceVersion  = "meta:version:"
)

// BuildKey 构建 KV Key
func BuildKey(namespace, identifier string) string {
    return namespace + identifier
}

// ParseKey 解析 KV Key
func ParseKey(key string) (namespace, identifier string) {
    idx := strings.Index(key, ":")
    if idx == -1 {
        return "", key
    }
    return key[:idx+1], key[idx+1:]
}

// ===== 数据结构定义 =====

// ClusterInfo 集群信息
type ClusterInfo struct {
    ClusterID     string         `msgpack:"cluster_id"`
    ClusterName   string         `msgpack:"cluster_name"`
    State         ClusterState   `msgpack:"state"`
    Version       string         `msgpack:"version"`
    RootNodeIDs   []string       `msgpack:"root_node_ids"`
    TreeDepth     int            `msgpack:"tree_depth"`
    TotalNodes    int            `msgpack:"total_nodes"`
    TotalShards   int            `msgpack:"total_shards"`
    QuorumThreshold int          `msgpack:"quorum_threshold"`
    GossipInterval  time.Duration `msgpack:"gossip_interval"`
    HeartbeatTimeout time.Duration `msgpack:"heartbeat_timeout"`
    CreatedAt     time.Time      `msgpack:"created_at"`
    UpdatedAt     time.Time      `msgpack:"updated_at"`
    Version       uint64         `msgpack:"version"`
}

// ClusterState 集群状态
type ClusterState int

const (
    ClusterStateInit        ClusterState = iota
    ClusterStateForming
    ClusterStateActive
    ClusterStateDegraded
    ClusterStateDissolving
)

// NodeInfo 节点信息
type NodeInfo struct {
    NodeID       string      `msgpack:"node_id"`
    HostID       string      `msgpack:"host_id"`
    Hostname     string      `msgpack:"hostname"`
    Addr         NodeAddress `msgpack:"addr"`
    Status       NodeStatus  `msgpack:"status"`
    LastHeartbeat time.Time   `msgpack:"last_heartbeat"`
    CPUUsage     float64    `msgpack:"cpu_usage"`
    MemoryUsage  float64    `msgpack:"memory_usage"`
    DiskUsage    float64    `msgpack:"disk_usage"`
    NetworkIn    int64      `msgpack:"network_in"`
    NetworkOut   int64      `msgpack:"network_out"`
    ActiveConnections int     `msgpack:"active_connections"`
    RequestCount       int64   `msgpack:"request_count"`
    RequestRate        float64 `msgpack:"request_rate"`
    AvgLatency         float64 `msgpack:"avg_latency"`
    HealthScore     float64   `msgpack:"health_score"`
    ErrorRate        float64   `msgpack:"error_rate"`
    Version          uint64    `msgpack:"version"`
    UpdatedAt        time.Time `msgpack:"updated_at"`
}

// RoleInfo 角色信息
type RoleInfo struct {
    CurrentHostRole     HostRole         `msgpack:"host_role"`
    CurrentNodeRole     NodeRole         `msgpack:"node_role"`
    RoleState           RoleState        `msgpack:"role_state"`
    LastStateChange     time.Time        `msgpack:"last_state_change"`
    MaxChildren         int              `msgpack:"max_children"`
    MaxLevel            int              `msgpack:"max_level"`
    LeafNodeTCPPort     int              `msgpack:"leaf_tcp_port"`
    ParentNodeTCPPort   int              `msgpack:"parent_tcp_port"`
    StandbyNodeTCPPort  int              `msgpack:"standby_tcp_port"`
    StandbyState        StandbyState     `msgpack:"standby_state"`
    PrimaryNodeID       string           `msgpack:"primary_node_id"`
    LastPrimaryHeartbeat time.Time        `msgpack:"last_primary_heartbeat"`
    MissedHeartbeats     int              `msgpack:"missed_heartbeats"`
    FailoverThreshold    int              `msgpack:"failover_threshold"`
    FailoverTimeout      time.Duration    `msgpack:"failover_timeout"`
    FailoverHistory      []*FailoverRecord `msgpack:"failover_history"`
    TransitionHistory    []*RoleTransition `msgpack:"transition_history"`
    MaxHistorySize       int              `msgpack:"max_history_size"`
    Version              uint64           `msgpack:"version"`
}

// RoleState 角色状态
type RoleState int

const (
    RoleStateStable       RoleState = iota
    RoleStatePromoting
    RoleStateDemoting
    RoleStateStandbyActivating
)

// StandbyState Standby 节点状态
type StandbyState int

const (
    StandbyStateActive     StandbyState = iota
    StandbyStatePromoting
    StandbyStatePromoted
    StandbyStateDemoting
)

// RoleTransition 角色转换记录
type RoleTransition struct {
    TransitionID   string    `msgpack:"transition_id"`
    HostID         string    `msgpack:"host_id"`
    OldRole        HostRole  `msgpack:"old_role"`
    NewRole        HostRole  `msgpack:"new_role"`
    OldNodeRole    NodeRole  `msgpack:"old_node_role"`
    NewNodeRole    NodeRole  `msgpack:"new_node_role"`
    Timestamp      time.Time `msgpack:"timestamp"`
    Reason         string    `msgpack:"reason"`
    Initiator      string    `msgpack:"initiator"`
    Success        bool      `msgpack:"success"`
}

// FailoverRecord 故障切换记录
type FailoverRecord struct {
    Timestamp   time.Time     `msgpack:"timestamp"`
    FromNodeID  string        `msgpack:"from_node_id"`
    ToNodeID    string        `msgpack:"to_node_id"`
    Reason      string        `msgpack:"reason"`
    Success     bool          `msgpack:"success"`
    Duration    time.Duration `msgpack:"duration"`
    TriggeredBy string        `msgpack:"triggered_by"`
}

// TopologyInfo 拓扑信息
type TopologyInfo struct {
    NodeID            string           `msgpack:"node_id"`
    ParentID          string           `msgpack:"parent_id"`
    ChildrenIDs       []string         `msgpack:"children_ids"`
    Level             int              `msgpack:"level"`
    Path              []string         `msgpack:"path"`
    SubtreeSize       int              `msgpack:"subtree_size"`
    ConsistencyMode   ConsistencyMode  `msgpack:"consistency_mode"`
    QuorumThreshold   int              `msgpack:"quorum_threshold"`
    Version           uint64           `msgpack:"version"`
    UpdatedAt         time.Time        `msgpack:"updated_at"`
}

// ConsistencyMode 一致性模式
type ConsistencyMode int

const (
    ConsistencyQuorum  ConsistencyMode = iota
    ConsistencyGossip
    ConsistencyHybrid
)

// ShardInfo 分片信息
type ShardInfo struct {
    ShardID            string              `msgpack:"shard_id"`
    TableID            string              `msgpack:"table_id"`
    ReplicaNodeIDs     []string            `msgpack:"replica_node_ids"`
    ReplicationFactor  int                 `msgpack:"replication_factor"`
    IsPrimary          bool                `msgpack:"is_primary"`
    DataSize           int64               `msgpack:"data_size"`
    KeyCount           int64               `msgpack:"key_count"`
    MigrationState     *ShardMigrationState `msgpack:"migration_state"`
    Version            uint64              `msgpack:"version"`
    UpdatedAt          time.Time           `msgpack:"updated_at"`
}

// ShardMigrationState 分片迁移状态
type ShardMigrationState struct {
    MigrationID      string        `msgpack:"migration_id"`
    SourceNodeID    string        `msgpack:"source_node_id"`
    TargetNodeID    string        `msgpack:"target_node_id"`
    Phase           MigrationPhase `msgpack:"phase"`
    Progress        int           `msgpack:"progress"`
    StartTime       time.Time     `msgpack:"start_time"`
    EstimatedEndTime time.Time     `msgpack:"estimated_end_time"`
}

// MigrationPhase 迁移阶段
type MigrationPhase int

const (
    MigrationPhasePending   MigrationPhase = iota
    MigrationPhaseBootstrapping
    MigrationPhaseSyncing
    MigrationPhaseCutover
    MigrationPhaseComplete
    MigrationPhaseFailed
)

// StaticInfo 静态信息
type StaticInfo struct {
    NodeID      string    `msgpack:"node_id"`
    Addr        string    `msgpack:"addr"`
    Hostname    string    `msgpack:"hostname"`
    StartupTime time.Time `msgpack:"startup_time"`
    Version     string    `msgpack:"version"`
    GitCommit   string    `msgpack:"git_commit"`
    BuildTime   time.Time `msgpack:"build_time"`
    CPUCount    int       `msgpack:"cpu_count"`
    MemoryBytes int64     `msgpack:"memory_bytes"`
    DiskBytes   int64     `msgpack:"disk_bytes"`
    Version     uint64    `msgpack:"version"`
}

// DynamicInfo 动态信息
type DynamicInfo struct {
    NodeID            string            `msgpack:"node_id"`
    Status            NodeStatus        `msgpack:"status"`
    LastHeartbeat     time.Time         `msgpack:"last_heartbeat"`
    CPUUsage          float64           `msgpack:"cpu_usage"`
    MemoryUsage       float64           `msgpack:"memory_usage"`
    DiskUsage         float64           `msgpack:"disk_usage"`
    NetworkIn         int64              `msgpack:"network_in"`
    NetworkOut        int64              `msgpack:"network_out"`
    ActiveConnections int               `msgpack:"active_connections"`
    RequestCount      int64             `msgpack:"request_count"`
    RequestRate       float64           `msgpack:"request_rate"`
    AvgLatency        float64           `msgpack:"avg_latency"`
    HealthScore       float64           `msgpack:"health_score"`
    ErrorRate         float64           `msgpack:"error_rate"`
    TTL               time.Duration     `msgpack:"ttl"`
    Version           uint64            `msgpack:"version"`
}

// OperationInfo 运维信息
type OperationInfo struct {
    NodeID            string            `msgpack:"node_id"`
    Labels            map[string]string `msgpack:"labels"`
    Datacenter        string            `msgpack:"datacenter"`
    Rack              string            `msgpack:"rack"`
    Zone              string            `msgpack:"zone"`
    Host              string            `msgpack:"host"`
    Weight            int               `msgpack:"weight"`
    Priority          int               `msgpack:"priority"`
    EnabledFeatures   []string          `msgpack:"enabled_features"`
    CustomProperties  map[string]string `msgpack:"custom_properties"`
    Version           uint64            `msgpack:"version"`
}

// VersionInfo 版本信息（MVCC）
type VersionInfo struct {
    Key       string    `msgpack:"key"`
    Version   uint64    `msgpack:"version"`
    CreatedAt time.Time `msgpack:"created_at"`
    UpdatedAt time.Time `msgpack:"updated_at"`
    Creator   string    `msgpack:"creator"`
}
```

---

## 8. 测试计划

### 8.1 单元测试

| 用例ID | 测试场景 | 验证目标 |
|-------|---------|---------|
| MK-001 | 基础 KV 操作 | Get/Put/Delete/Exists |
| MK-002 | 命名空间隔离 | 不同命名空间 Key 不冲突 |
| MK-003 | MsgPack 序列化 | 数据正确序列化/反序列化 |
| MK-004 | 批量操作 | GetBatch/PutBatch |
| MK-005 | 版本控制 | MVCC 版本号递增 |
| MK-006 | 封装接口 | GetNodeInfo/SetNodeInfo 等 |
| MK-007 | 转换历史 | AddRoleTransition/历史清理 |
| MK-008 | 分组管理 | GetGroupMembers |

### 8.2 集成测试

| 用例ID | 测试场景 | 验证目标 |
|-------|---------|---------|
| MK-101 | MVStore 集成 | 持久化和恢复 |
| MK-102 | TreeCoordinator 集成 | 替换现有 Metadata |
| MK-103 | Gossip 同步 | 元数据变更同步 |
| MK-104 | 角色转换 | Leaf → Parent 转换 |

### 8.3 性能测试

| 用例ID | 测试场景 | 目标指标 |
|-------|---------|---------|
| MK-P01 | 读取性能 | < 1ms |
| MK-P02 | 写入性能 | < 5ms |
| MK-P03 | 批量读取 | 1000 条 < 50ms |

---

## 📝 与原方案对比

| 对比项 | 原方案（复杂接口） | 简化方案（KV Mapping） |
|--------|------------------|----------------------|
| **复杂度** | 高（MetadataManager + Registry + Store + Sync） | 低（直接 KV 操作） |
| **代码量** | 多（~2000 行） | 少（~800 行） |
| **学习曲线** | 陡峭 | 平缓 |
| **灵活性** | 高（结构化接口） | 中（KV 约束） |
| **性能** | 中（多层封装） | 高（直接访问） |
| **一致性** | 强（类型安全） | 中（MsgPack 约束） |

---

## 🔖 相关文档

- `cluster_2026-02-09_metadata-design-decisions.md` - 架构讨论决策
- `2026-02-09-PR-metadata-kv-mapping.md` - KV Mapping 参考方案
- `cluster_2026-01-29_tree-coordinator-complete-reference.md` - TreeCoordinator 参考

---

**文档版本**: v1.0
**创建日期**: 2026-02-09
**维护者**: NexKV 开发团队
**状态**: 📝 草稿
