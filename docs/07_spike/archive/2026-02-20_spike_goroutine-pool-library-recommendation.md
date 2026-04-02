# NexKV 通用集成测试框架设计

> **文档类型**: Spike 研究文档
> **创建日期**: 2026-02-20
> **最后更新**: 2026-02-20
> **文档版本**: v2.10（添加 Kahn 算法背景知识）
> **关联文档**:
> - `docs/06_PM/feature/2026-02-18_PR-nexkv-ddd-architecture_Pre.md`
> - `docs/06_PM/feature/2026-02-19_PR-phase1-week1-2-transport-poc_Pre.md`
> - `docs/06_PM/milestones/2026-02-20_M1-infrastructure-layer-acceptance.md`
> - `docs/07_spike/2026-02-20_goroutine-pool-library-recommendation.md`（Goroutine 池详细选型）

---

## ⚠️ 重要说明：与现有测试框架的并存关系

> **本框架为独立设计，与现有 `test/e2e/` 测试并存，不存在取代关系。**

| 测试类型 | 位置 | 测试范围 | 目的 |
|----------|------|----------|------|
| **E2E 测试** | `test/e2e/` | 完整系统端到端测试 | 验证用户场景、API 契约 |
| **集成测试框架** | `pkg/test/framework/` | 组件级集成测试 | 验证组件交互、故障场景 |
| **单元测试** | 各模块 `*_test.go` | 单个函数/方法 | 验证逻辑正确性 |

**关系说明**:
- **互补关系**: E2E 测试关注整体功能，本框架关注组件间协作细节
- **不同粒度**: E2E 测试从外部调用，本框架从内部模拟节点
- **故障注入**: 本框架专注于网络分区、节点故障等混沌测试场景
- **并存运行**: 两者可同时存在于 CI/CD 流程中

---

## 📋 DDD 重构状态说明

> **⚠️ 重要背景**: DDD（领域驱动设计）是一次大的架构重构，目标是重构整个代码库。当前状态：

| 层级 | 新架构 (DDD) | 旧代码 | 说明 |
|------|--------------|--------|------|
| **Transport** | ✅ 已实现 | - | 新的 DDD 架构，接口定义在 `internal/domain/service/transport.go` |
| **Storage** | ❌ 未实现 | 存在 | 旧代码在 `internal/infrastructure/storage/`，未迁移到新架构 |
| **Replication** | ❌ 未实现 | 存在 | 旧代码在 `internal/infrastructure/replication/`，未迁移 |
| **Cluster** | ❌ 未实现 | 存在 | 旧代码在 `internal/infrastructure/cluster/`，未迁移 |

### 测试覆盖说明

> **本框架测试覆盖统计只计算新架构部分，旧代码不计入。**

- **Transport 接口**: 11 个方法，当前测试覆盖 ~45% (5/11)
- **Storage/Replication/Cluster**: 旧代码，待 DDD 迁移后计入

### 当前阶段

**Phase 1**: 仅 Transport POC
- 新框架适配器: `pkg/test/framework/`
- 生产接口: `internal/domain/service/transport.go`
- 旧代码 (`test/e2e/`): **不计入**本框架统计

---

**依赖关系声明**:
- `pkg/test/framework/` 是**完全独立**的，不依赖 `test/e2e/` 的任何代码
- 新框架与旧测试之间**没有共享代码**，避免相互影响
- 生产代码 (`internal/infrastructure/`) 不依赖测试框架，测试框架依赖生产代码的接口
- 如需复用工具函数，应将其提取到 `pkg/` 下的公共包，而非直接依赖旧测试代码

---

## 版本历史

| 版本 | 日期 | 变更内容 | 作者 |
|------|------|---------|------|
| v1.0 | 2026-02-20 | 初始版本，Transport POC 专用集成测试框架 | AI Agent |
| v2.0 | 2026-02-20 | 扩展为通用框架，支持多组件测试 | AI Agent |
| v2.1 | 2026-02-20 | 修正评审问题：依赖管理、测试隔离、数据生成器、指标扩展 | AI Agent |
| v2.2 | 2026-02-20 | 补充技术细节：拓扑排序、健康检查、错误处理、资源清理 | AI Agent |
| v2.3 | 2026-02-20 | 添加测试目录管理：NEXKV_BASE_DIR、UUIDv7 test-id | AI Agent |
| v2.4 | 2026-02-20 | Code Review 修复：废弃 GlobalRegistry、添加验证清单、清理策略 | AI Agent |
| v2.5 | 2026-02-20 | 最终审查版：版本号统一、CI/CD 配置、故障排查、命名规范 | AI Agent |
| v2.6 | 2026-02-20 | Code Review 修复：P0/P1/P2 问题全面修复 | AI Agent |
| v2.7 | 2026-02-20 | 添加"并发控制与 Goroutine 池"章节，推荐 ants 库 | AI Agent |
| v2.8 | 2026-02-20 | Code Review v2：P0/P1/P2 全面修复（Goroutine 泄漏、资源清理、边界条件） | AI Agent |
| v2.9 | 2026-02-20 | Code Review v3：修复 P1-6/P2-5/P2-6（百分位数排序、边界条件） | AI Agent |
| v2.10 | 2026-02-20 | 添加 Kahn 算法背景知识（拓扑排序原理、图解示例、复杂度分析） | AI Agent |

---

## ⚠️ v2.8 变更影响分析

| 变更内容 | 影响范围 | 向后兼容性 |
|---------|---------|-----------|
| NetworkPartitionController 添加 context 取消 | 🟡 使用延迟注入的测试 | ✅ 兼容（新增字段） |
| TopologicalSort 临时组件清理 | 🟡 拓扑排序调用 | ✅ 兼容（行为修复） |
| DataGenerator 边界条件处理 | 🟡 数据生成逻辑 | ✅ 兼容（行为修复） |
| isReady 优化健康检查 | 🟡 集群就绪检查 | ✅ 兼容（性能优化） |
| ForceCleanup 信号处理可配置 | 🟢 紧急清理逻辑 | ✅ 兼容（新增配置） |
| calculatePercentile 添加排序 | 🟢 指标计算 | ✅ 兼容（行为修复） |

---

## ⚠️ v2.6 变更影响分析

| 变更内容 | 影响范围 | 向后兼容性 |
|---------|---------|-----------|
| 修复拓扑排序算法 | 🔴 所有依赖 `TopologicalSort` 的代码 | ✅ 兼容（行为修复） |
| 删除 `init()` 全局注册模式 | 🔴 所有使用 `RegisterComponent()` 的代码 | ❌ **破坏性变更** |
| 健康检查超时控制 | 🟡 所有组件的健康检查实现 | ✅ 兼容（新增规范） |
| 资源泄漏修复 | 🟡 涉及节点重启的测试场景 | ✅ 兼容（行为修复） |
| 添加 Goroutine 池章节 | 🟢 性能测试实现 | ✅ 兼容（新增功能） |

---

## 目录

1. [背景与目标](#一背景与目标)
2. [架构设计](#二架构设计)
3. [核心接口设计](#三核心接口设计)
4. [组件适配器实现](#四组件适配器实现)
5. [测试集群实现](#五测试集群实现)
6. [测试场景实现](#六测试场景实现)
7. [使用示例](#七使用示例)
8. [并发控制与 Goroutine 池](#八并发控制与-goroutine-池)
9. [目录结构](#九目录结构)
10. [测试数据生成器](#十测试数据生成器)
11. [分布式系统指标](#十一分布式系统指标)
12. [总结](#十二总结)

---

## 术语表

| 术语 | 说明 |
|------|------|
| **TestCluster** | 多节点测试集群，管理一组 TestNode |
| **TestNode** | 测试节点，模拟集群中的一个节点 |
| **TestComponent** | 可测试组件接口，Transport/Storage/Replication 等都实现此接口 |
| **TestScenario** | 测试场景，定义 Setup → Execute → Verify → Teardown 生命周期 |
| **ComponentRegistry** | 组件注册表，管理组件工厂的注册和创建 |
| **DataGenerator** | 测试数据生成器，生成各种分布的测试数据 |

---

## 一、背景与目标

### 1.1 背景

NexKV 项目需要验证多个层次的组件集成：

| 层次 | 组件 | 集成测试需求 |
|------|------|-------------|
| **基础设施层** | Transport | 3节点集群通信、节点发现、性能基准 |
| **基础设施层** | RPC/Codec | 跨节点 RPC 调用、序列化兼容性 |
| **存储引擎层** | Bf-Tree/WAL | 数据持久化、故障恢复 |
| **数据平面层** | Replication | 多副本一致性、故障转移 |
| **控制平面层** | Cluster/Shard | 分片迁移、节点扩缩容 |

**问题**: 每个组件都有自己的集成测试需求，需要统一的测试框架来支持。

### 1.2 目标

构建一个**通用集成测试框架**，能够：

1. **支持多组件测试** - Transport、Storage、Replication、Cluster 等
2. **组件组合测试** - 测试多个组件协同工作
3. **多节点环境** - 在单台机器上模拟多节点集群
4. **生命周期管理** - 自动创建、启动、停止测试环境
5. **可扩展架构** - 易于添加新的组件和测试场景

---

## 二、架构设计

### 2.1 整体架构图

<!-- 架构图使用 Mermaid 渲染，符合项目规范 -->

```mermaid
graph TB
    subgraph Framework["NexKV Integration Test Framework"]
        Orchestrator["Test Orchestrator<br/>- 管理测试生命周期<br/>- 协调组件初始化<br/>- 执行测试场景"]

        Orchestrator --> Cluster["TestCluster Manager<br/>- Node管理<br/>- 网络拓扑<br/>- 资源分配"]
        Orchestrator --> Registry["TestComponent Registry<br/>- 组件注册<br/>- 依赖解析<br/>- 生命周期"]
        Orchestrator --> Executor["TestScenario Executor<br/>- 场景执行<br/>- 结果收集<br/>- 断言验证"]

        Cluster --> Adapters["Component Test Adapters"]
        Registry --> Adapters

        subgraph Adapters["Component Test Adapters"]
            Transport["Transport Adapter<br/>- libp2p节点<br/>- Stream测试<br/>- RPC测试"]
            Storage["Storage Adapter<br/>- Bf-Tree实例<br/>- WAL测试<br/>- 迭代器测试"]
            Replication["Replication Adapter<br/>- Replica管理<br/>- Quorum测试<br/>- 冲突解决"]
            ClusterAdapt["Cluster Adapter<br/>- 分片管理<br/>- 节点管理<br/>- 拓扑变更"]
        end
    end

    style Framework fill:#f5f5f5,stroke:#333,stroke-width:2px
    style Adapters fill:#e8f4f8,stroke:#0288d1,stroke-width:1px
```

### 2.2 核心组件关系图

```mermaid
graph TB
    subgraph TestEnv["TestEnvironment"]
        Node1["Node 1<br/>(Bootstrap)"]
        Node2["Node 2"]
        Node3["Node 3"]
        Node4["Node 4"]
        Node5["Node 5"]

        Node1 --- Network["Network Topology<br/>(libp2p + mDNS)"]
        Node2 --- Network
        Node3 --- Network
        Node4 --- Network
        Node5 --- Network
    end

    subgraph ComponentStack["Component Stack (每个节点)"]
        direction BT
        Transport["Transport Component<br/>- libp2p, RPC, Codec, Stream"]
        Storage["Storage Component<br/>- Bf-Tree, WAL, Iterator"]
        Replication["Replication Component<br/>- Replicator, Quorum, ConflictResolver"]
        ClusterComp["Cluster Component<br/>- NodeManager, ShardManager, Partitioner"]

        Transport --> Storage
        Storage --> Replication
        Replication --> ClusterComp
    end

    TestEnv --> ComponentStack

    style TestEnv fill:#f5f5f5,stroke:#333
    style ComponentStack fill:#e8f4f8,stroke:#0288d1
```

---

## 三、核心接口设计

### 3.1 通用测试环境接口

```go
// pkg/test/framework/test_environment.go

package framework

import (
    "context"
    "time"
)

// TestEnvironment 通用测试环境接口
// 所有组件集成测试的基础接口
type TestEnvironment interface {
    // ID 返回环境唯一标识
    ID() string
    
    // Start 启动测试环境
    Start(ctx context.Context) error
    
    // Stop 停止测试环境并清理资源
    Stop(ctx context.Context) error
    
    // Status 返回环境状态
    Status() EnvironmentStatus
    
    // GetComponent 获取指定类型的组件
    GetComponent(name string) (TestComponent, error)
    
    // ListComponents 列出所有已注册的组件
    ListComponents() []TestComponent
}

// EnvironmentStatus 环境状态
type EnvironmentStatus int

const (
    EnvironmentStatusUnknown EnvironmentStatus = iota
    EnvironmentStatusCreating
    EnvironmentStatusCreated   // P2-3 修复 (v2.8)：添加缺失的 Created 状态
    EnvironmentStatusStarting
    EnvironmentStatusRunning
    EnvironmentStatusStopping
    EnvironmentStatusStopped
    EnvironmentStatusError
)

// TestComponent 通用测试组件接口
// 所有可测试组件（Transport、Storage、Replication等）都实现此接口
type TestComponent interface {
    // Name 返回组件名称
    Name() string
    
    // Type 返回组件类型
    Type() ComponentType
    
    // Init 初始化组件
    Init(ctx context.Context, env TestEnvironment) error
    
    // Start 启动组件
    Start(ctx context.Context) error
    
    // Stop 停止组件
    Stop(ctx context.Context) error
    
    // HealthCheck 健康检查
    // 实现要求：
    // 1. 检查组件内部状态是否正常
    // 2. 检查依赖组件是否可用
    // 3. 检查资源（连接、文件句柄等）是否正常
    // 4. 超时控制：应在指定时间内返回，避免阻塞
    HealthCheck(ctx context.Context) error
    
    // GetHealthCheckConfig 返回健康检查配置
    GetHealthCheckConfig() *HealthCheckConfig
    
    // GetDependencies 返回依赖的组件类型列表（类型安全）
    // 使用 ComponentType 而非字符串，避免运行时错误
    GetDependencies() []ComponentType
}

// HealthCheckConfig 健康检查配置
type HealthCheckConfig struct {
    // Timeout 健康检查超时时间
    // 默认: 5s
    Timeout time.Duration
    
    // Interval 健康检查间隔（用于周期性检查）
    // 默认: 10s
    Interval time.Duration
    
    // RetryCount 失败时的重试次数
    // 默认: 3
    RetryCount int
    
    // RetryInterval 重试间隔
    // 默认: 1s
    RetryInterval time.Duration
    
    // Critical 是否为关键组件
    // 关键组件健康检查失败会导致整个节点标记为不健康
    // 默认: true
    Critical bool
}

// DefaultHealthCheckConfig 返回默认健康检查配置
func DefaultHealthCheckConfig() *HealthCheckConfig {
    return &HealthCheckConfig{
        Timeout:       5 * time.Second,
        Interval:      10 * time.Second,
        RetryCount:    3,
        RetryInterval: 1 * time.Second,
        Critical:      true,
    }
}

// ComponentType 组件类型
type ComponentType string

const (
    ComponentTypeTransport     ComponentType = "transport"
    ComponentTypeStorage       ComponentType = "storage"
    ComponentTypeReplication   ComponentType = "replication"
    ComponentTypeCluster       ComponentType = "cluster"
    ComponentTypeTransaction   ComponentType = "transaction"
    ComponentTypeMetadata      ComponentType = "metadata"
)
```

### 3.2 多节点测试集群接口

```go
// pkg/test/framework/cluster.go

package framework

import (
    "context"
    "time"
)

// TestCluster 多节点测试集群接口
type TestCluster interface {
    TestEnvironment
    
    // GetNode 获取指定索引的节点
    GetNode(index int) (TestNode, error)
    
    // GetNodeByID 根据ID获取节点
    GetNodeByID(nodeID string) (TestNode, error)
    
    // GetAllNodes 获取所有节点
    GetAllNodes() []TestNode
    
    // GetBootstrapNode 获取引导节点
    GetBootstrapNode() TestNode
    
    // GetNodeCount 返回节点数量
    GetNodeCount() int
    
    // WaitForReady 等待集群就绪
    WaitForReady(ctx context.Context, timeout time.Duration) error
    
    // GetNetworkTopology 获取网络拓扑
    GetNetworkTopology() *NetworkTopology
    
    // Partition 模拟网络分区
    Partition(partitionID string, nodeIndices []int) error
    
    // HealPartition 恢复网络分区
    HealPartition(partitionID string) error
    
    // KillNode 模拟节点宕机
    KillNode(nodeIndex int) error
    
    // RestartNode 重启节点
    RestartNode(nodeIndex int) error
}

// TestNode 测试节点接口
type TestNode interface {
    // ID 返回节点唯一标识
    ID() string
    
    // Address 返回节点地址
    Address() string
    
    // GetComponent 获取节点上的组件实例
    GetComponent(name string) (TestComponent, error)
    
    // IsHealthy 检查节点健康状态
    IsHealthy(ctx context.Context) bool
    
    // ConnectTo 连接到另一个节点
    ConnectTo(ctx context.Context, target TestNode) error
    
    // DisconnectFrom 断开与另一个节点的连接
    DisconnectFrom(ctx context.Context, target TestNode) error
    
    // IsConnectedTo 检查是否连接到指定节点
    IsConnectedTo(nodeID string) bool
    
    // GetConnectedPeers 获取已连接的节点列表
    GetConnectedPeers() []string
}

// NetworkTopology 网络拓扑信息
type NetworkTopology struct {
    Nodes       []NodeInfo
    Partitions  []NetworkPartition
}

type NodeInfo struct {
    ID        string
    Address   string
    Status    NodeStatus
    Peers     []string
    Components []string
}

type NodeStatus int

const (
    NodeStatusUnknown NodeStatus = iota
    NodeStatusStarting
    NodeStatusRunning
    NodeStatusStopped
    NodeStatusFailed
)

type NetworkPartition struct {
    ID      string
    Nodes   []string  // 分区内的节点ID
    Isolated bool     // 是否与其他分区隔离
}
```

### 3.3 组件注册与发现

```go
// pkg/test/framework/component_registry.go

package framework

import (
    "context"
    "fmt"
    "sync"
)

// ComponentFactory 组件工厂函数
type ComponentFactory func(config ComponentConfig) (TestComponent, error)

// ComponentRegistry 组件注册表
type ComponentRegistry struct {
    factories map[ComponentType]ComponentFactory
    mutex     sync.RWMutex
}

// NewComponentRegistry 创建组件注册表
func NewComponentRegistry() *ComponentRegistry {
    return &ComponentRegistry{
        factories: make(map[ComponentType]ComponentFactory),
    }
}

// Register 注册组件工厂
func (r *ComponentRegistry) Register(
    componentType ComponentType, 
    factory ComponentFactory,
) {
    r.mutex.Lock()
    defer r.mutex.Unlock()
    r.factories[componentType] = factory
}

// Create 创建组件实例
func (r *ComponentRegistry) Create(
    componentType ComponentType, 
    config ComponentConfig,
) (TestComponent, error) {
    r.mutex.RLock()
    factory, exists := r.factories[componentType]
    r.mutex.RUnlock()
    
    if !exists {
        return nil, fmt.Errorf("component type not registered: %s", componentType)
    }
    
    return factory(config)
}
```

### 什么是拓扑排序？

拓扑排序（Topological Sorting）是一种对有向无环图（DAG）的顶点进行排序的算法，
使得对于图中的每一条有向边 (u, v)，顶点 u 在排序结果中都出现在顶点 v 之前。

**应用场景**：
- 任务调度：确定任务的执行顺序
- 依赖解析：包管理器、构建系统
- 课程安排：先修课程顺序
- **组件启动顺序**：本框架中确保组件按依赖顺序启动

### Kahn 算法原理

Kahn 算法（1962年，Arthur B. Kahn）是一种基于入度的拓扑排序算法。

**核心思想**：
1. 维护每个顶点的**入度**（指向该顶点的边的数量）
2. 入度为 0 的顶点没有依赖，可以优先处理
3. 处理完一个顶点后，将其所有邻居的入度减 1
4. 重复直到所有顶点都被处理，或发现循环依赖

### 算法步骤

输入：有向图 G = (V, E)
输出：拓扑排序序列（如果存在）

1. 计算所有顶点的入度
2. 将所有入度为 0 的顶点加入队列 Q
3. while Q 非空:
     a. 取出队首顶点 u，加入结果序列
     b. 对于 u 的每个邻居 v:
        - 将 v 的入度减 1
        - 如果 v 的入度变为 0，将 v 加入 Q
4. 如果结果序列长度 < 顶点数，说明存在循环依赖

### 图解示例

假设组件依赖关系如下：

```
Transport (无依赖)
    ↓
Storage (依赖 Transport)
    ↓
Replication (依赖 Transport + Storage)
    ↓
Cluster (依赖 Storage + Replication)
```

**对应的 DAG 图**：

```mermaid
graph LR
    T[Transport] --> S[Storage]
    T --> R[Replication]
    S --> R
    S --> C[Cluster]
    R --> C
```

**Kahn 算法执行过程**：

```
初始入度：Transport=0, Storage=1, Replication=2, Cluster=2
队列 Q：[Transport]

Step 1: 处理 Transport
  - 结果：[Transport]
  - 更新入度：Storage=0, Replication=1
  - Q：[Storage]

Step 2: 处理 Storage
  - 结果：[Transport, Storage]
  - 更新入度：Replication=0, Cluster=1
  - Q：[Replication]

Step 3: 处理 Replication
  - 结果：[Transport, Storage, Replication]
  - 更新入度：Cluster=0
  - Q：[Cluster]

Step 4: 处理 Cluster
  - 结果：[Transport, Storage, Replication, Cluster]
  - Q：[]

最终启动顺序：Transport → Storage → Replication → Cluster
```

### 循环依赖检测

如果存在循环依赖：

```
A → B → C → A  （循环）
```

**执行过程**：
```
初始入度：A=1, B=1, C=1
队列 Q：[]  （没有入度为 0 的顶点！）

结果：无法开始，检测到循环依赖
```

### 复杂度分析

| 指标 | 复杂度 | 说明 |
|------|--------|------|
| **时间复杂度** | O(V + E) | V = 顶点数，E = 边数 |
| **空间复杂度** | O(V + E) | 邻接表 + 入度表 |

**vs DFS 拓扑排序**：
- Kahn 算法：更适合检测循环依赖（无法完成时即存在环）
- DFS 算法：更适合单次遍历，但需要额外标记数组

### 本框架中的应用

在组件启动场景中：
- **顶点**：组件（Transport, Storage, Replication, Cluster）
- **边**：依赖关系（A → B 表示 B 依赖 A，A 需要先启动）
- **入度**：组件的依赖数量
- **结果序列**：组件的启动顺序

**关键修复（v2.6）**：
- 邻接表构建方向：dep → comp（依赖完成后才能启动当前组件）
- 入度更新：减少邻居的入度，确保 O(V+E) 而非 O(V²)

```go
// TopologicalSort 对组件进行拓扑排序，检测循环依赖
// 返回按依赖顺序排序的组件类型列表
//
// P0 修复 (v2.6)：修复邻接表构建方向和入度更新逻辑
// - 邻接表构建：dep -> comp 的边（表示 dep 完成后才能启动 comp）
// - 入度更新：减少当前节点的邻居入度，而非邻居自身
// - 时间复杂度：O(V + E) 而非 O(V^2)
func (r *ComponentRegistry) TopologicalSort(
    components []ComponentType,
) ([]ComponentType, error) {
    // 构建邻接表和入度表
    graph := make(map[ComponentType][]ComponentType)
    inDegree := make(map[ComponentType]int)

    for _, comp := range components {
        inDegree[comp] = 0
        graph[comp] = []ComponentType{}
    }

    // 构建图：dep -> comp 的边（依赖完成才能启动当前组件）
    for _, comp := range components {
        factory, exists := r.factories[comp]
        if !exists {
            return nil, fmt.Errorf("component not registered: %s", comp)
        }

        // 创建临时实例获取依赖（仅用于获取依赖信息）
        tempComp, err := factory(ComponentConfig{Type: comp})
        if err != nil {
            return nil, fmt.Errorf("failed to create temp component %s: %w", comp, err)
        }

        deps := tempComp.GetDependencies()

        // P0-3 修复 (v2.8)：清理临时组件，防止资源泄漏
        if stopper, ok := tempComp.(interface{ Stop(context.Context) error }); ok {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            stopper.Stop(ctx)
            cancel()
        }

        for _, dep := range deps {
            if _, exists := inDegree[dep]; exists {
                graph[dep] = append(graph[dep], comp) // dep -> comp 的边
                inDegree[comp]++
            }
        }
    }

    // Kahn 算法进行拓扑排序
    var result []ComponentType
    queue := make([]ComponentType, 0)

    // 找到所有入度为 0 的节点（无依赖）
    for comp, degree := range inDegree {
        if degree == 0 {
            queue = append(queue, comp)
        }
    }

    for len(queue) > 0 {
        current := queue[0]
        queue = queue[1:]
        result = append(result, current)

        // 减少邻接节点的入度（O(E) 而非 O(V^2)）
        for _, neighbor := range graph[current] {
            inDegree[neighbor]--
            if inDegree[neighbor] == 0 {
                queue = append(queue, neighbor)
            }
        }
    }

    if len(result) != len(components) {
        return nil, fmt.Errorf("circular dependency detected among components: %v", components)
    }

    return result, nil
}

// topologicalSortWithDetails 带详细错误信息的拓扑排序
// 当检测到循环依赖时，返回具体的循环路径
func (r *ComponentRegistry) topologicalSortWithDetails(
    components []ComponentType,
) ([]ComponentType, error) {
    // 构建邻接表和入度表
    graph := make(map[ComponentType][]ComponentType)
    inDegree := make(map[ComponentType]int)
    
    for _, comp := range components {
        inDegree[comp] = 0
        graph[comp] = []ComponentType{}
    }
    
    // 构建图
    for _, comp := range components {
        factory, exists := r.factories[comp]
        if !exists {
            return nil, fmt.Errorf("component not registered: %s", comp)
        }
        
        tempComp, err := factory(ComponentConfig{Type: comp})
        if err != nil {
            return nil, fmt.Errorf("failed to create temp component %s: %w", comp, err)
        }
        
        deps := tempComp.GetDependencies()
        for _, dep := range deps {
            if _, exists := inDegree[dep]; exists {
                graph[dep] = append(graph[dep], comp)  // dep -> comp 的边
                inDegree[comp]++
            }
        }
    }
    
    // Kahn 算法
    var result []ComponentType
    queue := make([]ComponentType, 0)
    
    // 找到所有入度为 0 的节点
    for comp, degree := range inDegree {
        if degree == 0 {
            queue = append(queue, comp)
        }
    }
    
    for len(queue) > 0 {
        current := queue[0]
        queue = queue[1:]
        result = append(result, current)
        
        // 减少邻接节点的入度
        for _, neighbor := range graph[current] {
            inDegree[neighbor]--
            if inDegree[neighbor] == 0 {
                queue = append(queue, neighbor)
            }
        }
    }
    
    // 检测循环依赖
    if len(result) != len(components) {
        // 找到循环路径
        cycle := r.findCyclePath(graph, components)
        return nil, fmt.Errorf("circular dependency detected: %v", cycle)
    }
    
    return result, nil
}

// findCyclePath 查找循环依赖路径（使用 DFS）
func (r *ComponentRegistry) findCyclePath(
    graph map[ComponentType][]ComponentType,
    components []ComponentType,
) []ComponentType {
    visited := make(map[ComponentType]bool)
    recStack := make(map[ComponentType]bool)
    path := []ComponentType{}
    
    var dfs func(node ComponentType) bool
    dfs = func(node ComponentType) bool {
        visited[node] = true
        recStack[node] = true
        path = append(path, node)
        
        for _, neighbor := range graph[node] {
            if !visited[neighbor] {
                if dfs(neighbor) {
                    return true
                }
            } else if recStack[neighbor] {
                // 发现循环，记录循环路径
                cycleStart := 0
                for i, n := range path {
                    if n == neighbor {
                        cycleStart = i
                        break
                    }
                }
                return true
            }
        }
        
        path = path[:len(path)-1]
        recStack[node] = false
        return false
    }
    
    for _, comp := range components {
        if !visited[comp] {
            if dfs(comp) {
                return path
            }
        }
    }
    
    return nil
}

// ComponentConfig 组件配置
type ComponentConfig struct {
    Type        ComponentType
    Name        string
    NodeID      string
    NodeIndex   int
    ClusterID   string
    Properties  map[string]interface{}
}

// IsolationLevel 测试隔离级别
type IsolationLevel int

const (
    IsolationProcess IsolationLevel = iota // 进程级隔离（完全隔离）
    IsolationNetwork                       // 网络级隔离（共用进程）
    IsolationNone                          // 无隔离（仅用于单测试）
)

func (i IsolationLevel) String() string {
    switch i {
    case IsolationProcess:
        return "process"
    case IsolationNetwork:
        return "network"
    case IsolationNone:
        return "none"
    default:
        return "unknown"
    }
}

// TestContext 测试上下文，支持测试隔离
// 每个测试用例应创建独立的 TestContext
type TestContext struct {
    Registry       *ComponentRegistry
    RandomSeed     int64
    TempDir        string
    IsolationLevel IsolationLevel // 隔离级别
}

// NewTestContext 创建测试上下文（默认无隔离）
func NewTestContext() *TestContext {
    return &TestContext{
        Registry:       NewComponentRegistry(),
        RandomSeed:     time.Now().UnixNano(),
        TempDir:        generateTempDir(),
        IsolationLevel: IsolationNone,
    }
}

// NewTestContextWithSeed 创建指定随机种子的测试上下文（用于可重复测试）
func NewTestContextWithSeed(seed int64) *TestContext {
    return &TestContext{
        Registry:       NewComponentRegistry(),
        RandomSeed:     seed,
        TempDir:        generateTempDir(),
        IsolationLevel: IsolationNone,
    }
}

// NewTestContextWithIsolation 创建指定隔离级别的测试上下文
func NewTestContextWithIsolation(level IsolationLevel) *TestContext {
    return &TestContext{
        Registry:       NewComponentRegistry(),
        RandomSeed:     time.Now().UnixNano(),
        TempDir:        generateTempDir(),
        IsolationLevel: level,
    }
}

// 注意：全局注册表已废弃，请使用 TestContext.Registry 实现完全隔离
// 保留以下代码仅用于向后兼容，将在 v2.0 中移除
//
// 废弃原因：
// 1. 全局状态导致测试间耦合
// 2. 并行测试时难以调试
// 3. 无法为不同测试配置不同组件工厂
//
// 迁移方案：
//   旧代码: RegisterComponent(ComponentTypeTransport, NewTransportComponent)
//   新代码: ctx := NewTestContext(); ctx.Registry.Register(ComponentTypeTransport, NewTransportComponent)
//
// Deprecated: Use TestContext.Registry instead. Will be removed in v2.0.
var (
    globalRegistry     *ComponentRegistry
    globalRegistryOnce sync.Once
    globalRegistryMu   sync.RWMutex
)

// GetGlobalRegistry 获取全局组件注册表（已废弃）
// Deprecated: Use TestContext.Registry for test isolation.
func GetGlobalRegistry() *ComponentRegistry {
    globalRegistryOnce.Do(func() {
        globalRegistry = NewComponentRegistry()
    })
    return globalRegistry
}

// RegisterComponent 全局注册组件（已废弃，请使用 TestContext.Registry）
// Deprecated: Use TestContext.Registry.Register instead for proper test isolation.
func RegisterComponent(
    componentType ComponentType,
    factory ComponentFactory,
) {
    reg := GetGlobalRegistry()
    globalRegistryMu.Lock()
    defer globalRegistryMu.Unlock()
    reg.Register(componentType, factory)
}
```

### 3.4 通用测试场景接口

```go
// pkg/test/framework/scenario.go

package framework

import (
    "context"
    "time"
)

// TestScenario 通用测试场景接口
type TestScenario interface {
    // Name 返回场景名称
    Name() string
    
    // Description 返回场景描述
    Description() string
    
    // GetRequiredComponents 返回需要的组件类型列表
    GetRequiredComponents() []ComponentType
    
    // GetMinNodeCount 返回最小节点数量
    GetMinNodeCount() int
    
    // Setup 准备测试环境
    Setup(ctx context.Context, cluster TestCluster) error
    
    // Execute 执行测试逻辑
    Execute(ctx context.Context, cluster TestCluster) (*ScenarioResult, error)
    
    // Verify 验证测试结果
    Verify(ctx context.Context, result *ScenarioResult) error
    
    // Teardown 清理测试环境
    Teardown(ctx context.Context, cluster TestCluster) error
}

// ScenarioResult 场景执行结果
type ScenarioResult struct {
    Name       string
    Success    bool
    StartTime  time.Time
    EndTime    time.Time
    Duration   time.Duration
    Metrics    *TestMetrics
    Artifacts  map[string]interface{}  // 测试产物（如日志、快照等）
    Error      error
}

// TestMetrics 测试指标
type TestMetrics struct {
    // 时间指标
    SetupTime    time.Duration
    ExecuteTime  time.Duration
    VerifyTime   time.Duration
    TeardownTime time.Duration
    
    // 性能指标
    Throughput   float64  // ops/sec
    Latency      LatencyStats
    
    // 资源指标
    MemoryUsage  uint64
    CPUUsage     float64
    
    // 自定义指标
    CustomMetrics map[string]interface{}
}

type LatencyStats struct {
    Min    time.Duration
    Max    time.Duration
    Avg    time.Duration
    P50    time.Duration
    P95    time.Duration
    P99    time.Duration
}

// ScenarioExecutorConfig 场景执行器配置
type ScenarioExecutorConfig struct {
    ContinueOnError bool         // 遇到失败是否继续执行后续场景
    FailFast        bool         // 是否快速失败（第一个失败就停止）
    Logger          *slog.Logger // 结构化日志记录器
}

// DefaultScenarioExecutorConfig 返回默认配置
func DefaultScenarioExecutorConfig() *ScenarioExecutorConfig {
    return &ScenarioExecutorConfig{
        ContinueOnError: false,
        FailFast:        true,
        Logger:          slog.Default(),
    }
}

// ScenarioExecutor 场景执行器
type ScenarioExecutor struct {
    cluster   TestCluster
    scenarios []TestScenario
    config    *ScenarioExecutorConfig
}

// NewScenarioExecutor 创建场景执行器（使用默认配置）
func NewScenarioExecutor(cluster TestCluster) *ScenarioExecutor {
    return &ScenarioExecutor{
        cluster:   cluster,
        scenarios: make([]TestScenario, 0),
        config:    DefaultScenarioExecutorConfig(),
    }
}

// NewScenarioExecutorWithConfig 创建带配置的场景执行器
func NewScenarioExecutorWithConfig(
    cluster TestCluster,
    config *ScenarioExecutorConfig,
) *ScenarioExecutor {
    return &ScenarioExecutor{
        cluster:   cluster,
        scenarios: make([]TestScenario, 0),
        config:    config,
    }
}

// Register 注册测试场景
func (e *ScenarioExecutor) Register(scenario TestScenario) {
    e.scenarios = append(e.scenarios, scenario)
}

// Run 执行所有注册的场景
// 根据配置决定是否继续执行或快速失败
func (e *ScenarioExecutor) Run(ctx context.Context) ([]*ScenarioResult, error) {
    var results []*ScenarioResult
    var errs []error

    for _, scenario := range e.scenarios {
        result, err := e.runScenario(ctx, scenario)
        if err != nil {
            e.config.Logger.Error("scenario failed",
                "scenario", scenario.Name(),
                "error", err)

            errs = append(errs, fmt.Errorf("scenario %s: %w", scenario.Name(), err))

            // 快速失败模式：立即停止
            if e.config.FailFast {
                return results, fmt.Errorf("fast fail: scenario %s failed: %w",
                    scenario.Name(), err)
            }

            // 非继续模式：记录错误后停止
            if !e.config.ContinueOnError {
                break
            }

            // 继续模式：记录失败结果，继续执行
            result.Success = false
            result.Error = err
        }

        results = append(results, result)
    }

    if len(errs) > 0 {
        return results, fmt.Errorf("%d scenarios failed: %v", len(errs), errs)
    }

    return results, nil
}

func (e *ScenarioExecutor) runScenario(
    ctx context.Context, 
    scenario TestScenario,
) (*ScenarioResult, error) {
    result := &ScenarioResult{
        Name:      scenario.Name(),
        StartTime: time.Now(),
    }
    
    // Setup
    setupStart := time.Now()
    if err := scenario.Setup(ctx, e.cluster); err != nil {
        result.Error = fmt.Errorf("setup failed: %w", err)
        return result, result.Error
    }
    result.Metrics.SetupTime = time.Since(setupStart)
    
    // Execute
    execStart := time.Now()
    execResult, err := scenario.Execute(ctx, e.cluster)
    result.Metrics.ExecuteTime = time.Since(execStart)
    
    if err != nil {
        result.Error = fmt.Errorf("execute failed: %w", err)
        result.Success = false
    } else {
        result.Success = execResult.Success
        result.Metrics = execResult.Metrics
        result.Artifacts = execResult.Artifacts
    }
    
    // Verify
    if result.Success {
        verifyStart := time.Now()
        if err := scenario.Verify(ctx, result); err != nil {
            result.Error = fmt.Errorf("verify failed: %w", err)
            result.Success = false
        }
        result.Metrics.VerifyTime = time.Since(verifyStart)
    }
    
    // Teardown
    teardownStart := time.Now()
    if err := scenario.Teardown(ctx, e.cluster); err != nil {
        // Teardown 错误不标记为失败，但记录
        result.Error = fmt.Errorf("teardown failed: %w", err)
    }
    result.Metrics.TeardownTime = time.Since(teardownStart)
    
    result.EndTime = time.Now()
    result.Duration = result.EndTime.Sub(result.StartTime)
    
    return result, nil
}
```

---

## 四、组件适配器实现

### 4.1 Transport 组件适配器

```go
// internal/infrastructure/transport/test/transport_adapter.go

package transport_test

import (
    "context"
    "fmt"
    
    "github.com/jzhang405/NexKV/internal/domain/service"
    "github.com/jzhang405/NexKV/pkg/test/framework"
)

// TransportComponent Transport 测试组件
//
// P0 修复 (v2.6)：删除 init() 全局注册，改用 TestContext.Registry
// 旧代码（已废弃）：
//   func init() {
//       framework.RegisterComponent(framework.ComponentTypeTransport, NewTransportComponent)
//   }
//
// 新代码（在测试中使用）：
//   func TestTransport(t *testing.T) {
//       ctx := framework.NewTestContext()
//       ctx.Registry.Register(framework.ComponentTypeTransport, NewTransportComponent)
//       // ...
//   }
type TransportComponent struct {
    name      string
    nodeID    string
    transport service.Transport
    rpc       service.RPC
    config    *TransportTestConfig
}

// TransportTestConfig Transport 测试配置
type TransportTestConfig struct {
    ListenAddr    string
    EnableMDNS    bool
    MDNSInterval  int  // milliseconds
    RPCTimeout    int  // milliseconds
}

// NewTransportComponent 创建 Transport 组件
func NewTransportComponent(
    config framework.ComponentConfig,
) (framework.TestComponent, error) {
    transportConfig := &TransportTestConfig{
        ListenAddr:   getString(config.Properties, "listen_addr", "/ip4/127.0.0.1/tcp/0"),
        EnableMDNS:   getBool(config.Properties, "enable_mdns", true),
        MDNSInterval: getInt(config.Properties, "mdns_interval", 1000),
        RPCTimeout:   getInt(config.Properties, "rpc_timeout", 5000),
    }
    
    return &TransportComponent{
        name:   config.Name,
        nodeID: config.NodeID,
        config: transportConfig,
    }, nil
}

func (c *TransportComponent) Name() string {
    return c.name
}

func (c *TransportComponent) Type() framework.ComponentType {
    return framework.ComponentTypeTransport
}

func (c *TransportComponent) Init(
    ctx context.Context, 
    env framework.TestEnvironment,
) error {
    // 创建 Transport 实例
    transportConfig := &service.TransportConfig{
        ListenAddr:   c.config.ListenAddr,
        EnableMDNS:   c.config.EnableMDNS,
        MDNSInterval: time.Duration(c.config.MDNSInterval) * time.Millisecond,
    }
    
    transport, err := NewLibp2pTransport(transportConfig)
    if err != nil {
        return fmt.Errorf("failed to create transport: %w", err)
    }
    
    c.transport = transport
    
    // 创建 RPC 实例
    rpcConfig := service.DefaultRPCConfig()
    rpcConfig.Timeout = time.Duration(c.config.RPCTimeout) * time.Millisecond
    c.rpc = NewLibp2pRPC(transport, rpcConfig)
    
    return nil
}

func (c *TransportComponent) Start(ctx context.Context) error {
    if err := c.transport.Start(ctx); err != nil {
        return err
    }
    return c.rpc.Start(ctx)
}

func (c *TransportComponent) Stop(ctx context.Context) error {
    if err := c.rpc.Stop(ctx); err != nil {
        return err
    }
    return c.transport.Stop(ctx)
}

func (c *TransportComponent) HealthCheck(ctx context.Context) error {
    // 使用带超时的上下文
    config := c.GetHealthCheckConfig()
    
    ctx, cancel := context.WithTimeout(ctx, config.Timeout)
    defer cancel()
    
    // 检查 Transport 是否初始化
    if c.transport == nil {
        return fmt.Errorf("transport not initialized")
    }
    
    // 检查 RPC 是否初始化
    if c.rpc == nil {
        return fmt.Errorf("rpc not initialized")
    }
    
    // 检查 Transport 健康状态
    if err := c.transport.HealthCheck(ctx); err != nil {
        return fmt.Errorf("transport health check failed: %w", err)
    }
    
    // 检查监听地址是否有效
    if c.transport.ListenAddr() == "" {
        return fmt.Errorf("transport listen address is empty")
    }
    
    return nil
}

func (c *TransportComponent) GetHealthCheckConfig() *framework.HealthCheckConfig {
    // Transport 是关键组件，使用较短的超时
    return &framework.HealthCheckConfig{
        Timeout:       3 * time.Second,  // Transport 检查应该很快
        Interval:      5 * time.Second,
        RetryCount:    3,
        RetryInterval: 500 * time.Millisecond,
        Critical:      true,  // Transport 失败会导致节点不可用
    }
}

func (c *TransportComponent) GetDependencies() []framework.ComponentType {
    // Transport 是基础组件，无依赖
    return nil
}

// GetTransport 获取底层 Transport 实例（测试用）
func (c *TransportComponent) GetTransport() service.Transport {
    return c.transport
}

// GetRPC 获取底层 RPC 实例（测试用）
func (c *TransportComponent) GetRPC() service.RPC {
    return c.rpc
}

// CallRPC 向目标节点发起 RPC 调用（便捷方法）
func (c *TransportComponent) CallRPC(
    ctx context.Context, 
    targetID string, 
    req model.Message,
) (model.Message, error) {
    return c.rpc.Call(ctx, model.PeerID(targetID), req)
}

// BroadcastRPC 广播 RPC 调用到所有节点（便捷方法）
func (c *TransportComponent) BroadcastRPC(
    ctx context.Context, 
    req model.Message,
) (*service.BroadcastResult, error) {
    return c.rpc.Broadcast(ctx, req, service.ResponseAll)
}
```

### 4.2 Storage 组件适配器

```go
// internal/infrastructure/storage/test/storage_adapter.go

package storage_test

import (
    "context"
    "fmt"
    "path/filepath"

    "github.com/jzhang405/NexKV/internal/domain/service"
    "github.com/jzhang405/NexKV/pkg/test/framework"
)

// StorageComponent Storage 测试组件
// 注意：P0 修复 (v2.6) - 已删除 init() 全局注册，请在测试中使用 TestContext.Registry.Register
type StorageComponent struct {
    name    string
    nodeID  string
    store   service.KVStore
    wal     service.WAL
    config  *StorageTestConfig
}

// StorageTestConfig Storage 测试配置
type StorageTestConfig struct {
    DataDir        string
    MaxMemorySize  int64  // bytes
    EnableWAL      bool
    WALSyncInterval int   // milliseconds
}

// NewStorageComponent 创建 Storage 组件
func NewStorageComponent(
    config framework.ComponentConfig,
) (framework.TestComponent, error) {
    storageConfig := &StorageTestConfig{
        DataDir:         getString(config.Properties, "data_dir", ""),
        MaxMemorySize:   getInt64(config.Properties, "max_memory", 64*1024*1024),
        EnableWAL:       getBool(config.Properties, "enable_wal", true),
        WALSyncInterval: getInt(config.Properties, "wal_sync_interval", 100),
    }
    
    return &StorageComponent{
        name:   config.Name,
        nodeID: config.NodeID,
        config: storageConfig,
    }, nil
}

func (c *StorageComponent) Init(
    ctx context.Context, 
    env framework.TestEnvironment,
) error {
    // 如果没有指定数据目录，创建临时目录
    dataDir := c.config.DataDir
    if dataDir == "" {
        clusterID := ""
        if cluster, ok := env.(framework.TestCluster); ok {
            clusterID = cluster.ID()
        }
        dataDir = filepath.Join("/tmp/nexkv-test", clusterID, c.nodeID)
    }
    
    // 创建 Bf-Tree 存储实例
    storeConfig := &service.KVStoreConfig{
        DataDir:       dataDir,
        MaxMemorySize: c.config.MaxMemorySize,
    }
    
    store, err := NewBfTreeStore(storeConfig)
    if err != nil {
        return fmt.Errorf("failed to create store: %w", err)
    }
    
    c.store = store
    
    // 创建 WAL 实例
    if c.config.EnableWAL {
        walConfig := &service.WALConfig{
            DataDir:      filepath.Join(dataDir, "wal"),
            SyncInterval: time.Duration(c.config.WALSyncInterval) * time.Millisecond,
        }
        
        wal, err := NewWAL(walConfig)
        if err != nil {
            return fmt.Errorf("failed to create WAL: %w", err)
        }
        
        c.wal = wal
    }
    
    return nil
}

func (c *StorageComponent) Start(ctx context.Context) error {
    if c.wal != nil {
        if err := c.wal.Start(ctx); err != nil {
            return err
        }
    }
    return c.store.Start(ctx)
}

func (c *StorageComponent) Stop(ctx context.Context) error {
    if err := c.store.Stop(ctx); err != nil {
        return err
    }
    if c.wal != nil {
        return c.wal.Stop(ctx)
    }
    return nil
}

func (c *StorageComponent) HealthCheck(ctx context.Context) error {
    // P1 修复 (v2.6)：添加超时控制
    config := c.GetHealthCheckConfig()

    ctx, cancel := context.WithTimeout(ctx, config.Timeout)
    defer cancel()

    // 1. 检查内部状态
    if c.store == nil {
        return fmt.Errorf("store not initialized")
    }

    // 2. 检查依赖组件（Storage 无依赖，跳过）

    // 3. 检查资源
    return c.store.HealthCheck(ctx)
}

// GetHealthCheckConfig P1 修复 (v2.6)：添加健康检查配置
func (c *StorageComponent) GetHealthCheckConfig() *framework.HealthCheckConfig {
    return &framework.HealthCheckConfig{
        Timeout:       5 * time.Second, // Storage 检查可能较慢
        Interval:      15 * time.Second,
        RetryCount:    3,
        RetryInterval: 2 * time.Second,
        Critical:      true,
    }
}

func (c *StorageComponent) GetDependencies() []framework.ComponentType {
    // Storage 是基础组件，无依赖
    // 注意：Storage 与 Transport 的交互通过接口注入，而非直接依赖
    return nil
}

// GetStore 获取底层 KVStore 实例（测试用）
func (c *StorageComponent) GetStore() service.KVStore {
    return c.store
}

// GetWAL 获取底层 WAL 实例（测试用）
func (c *StorageComponent) GetWAL() service.WAL {
    return c.wal
}

// Put 存储键值对（便捷方法）
func (c *StorageComponent) Put(
    ctx context.Context, 
    key, value []byte,
) error {
    return c.store.Put(ctx, key, value)
}

// Get 获取键值（便捷方法）
func (c *StorageComponent) Get(
    ctx context.Context, 
    key []byte,
) ([]byte, error) {
    return c.store.Get(ctx, key)
}
```

### 4.3 Replication 组件适配器

```go
// internal/infrastructure/replication/test/replication_adapter.go

package replication_test

import (
    "context"
    "fmt"

    "github.com/jzhang405/NexKV/internal/domain/service"
    "github.com/jzhang405/NexKV/pkg/test/framework"
)

// ReplicationComponent Replication 测试组件
// 注意：P0 修复 (v2.6) - 已删除 init() 全局注册，请在测试中使用 TestContext.Registry.Register
type ReplicationComponent struct {
    name        string
    nodeID      string
    replicator  service.Replicator
    config      *ReplicationTestConfig
}

// ReplicationTestConfig Replication 测试配置
type ReplicationTestConfig struct {
    ReplicationFactor int
    QuorumSize        int
    SyncMode          string  // "sync" | "async"
    ConflictStrategy  string  // "last-write-wins" | "vector-clock"
}

// NewReplicationComponent 创建 Replication 组件
func NewReplicationComponent(
    config framework.ComponentConfig,
) (framework.TestComponent, error) {
    replConfig := &ReplicationTestConfig{
        ReplicationFactor: getInt(config.Properties, "replication_factor", 3),
        QuorumSize:        getInt(config.Properties, "quorum_size", 2),
        SyncMode:          getString(config.Properties, "sync_mode", "sync"),
        ConflictStrategy:  getString(config.Properties, "conflict_strategy", "last-write-wins"),
    }
    
    return &ReplicationComponent{
        name:   config.Name,
        nodeID: config.NodeID,
        config: replConfig,
    }, nil
}

func (c *ReplicationComponent) Init(
    ctx context.Context, 
    env framework.TestEnvironment,
) error {
    // 获取依赖的 Transport 和 Storage 组件
    transportComp, err := env.GetComponent("transport")
    if err != nil {
        return fmt.Errorf("transport component not found: %w", err)
    }
    
    storageComp, err := env.GetComponent("storage")
    if err != nil {
        return fmt.Errorf("storage component not found: %w", err)
    }
    
    transport := transportComp.(*transport_test.TransportComponent).GetTransport()
    storage := storageComp.(*storage_test.StorageComponent).GetStore()
    
    // 创建 Replicator 实例
    replicatorConfig := &service.ReplicatorConfig{
        ReplicationFactor: c.config.ReplicationFactor,
        QuorumSize:        c.config.QuorumSize,
        SyncMode:          c.config.SyncMode,
        ConflictStrategy:  c.config.ConflictStrategy,
    }
    
    replicator, err := NewReplicator(transport, storage, replicatorConfig)
    if err != nil {
        return fmt.Errorf("failed to create replicator: %w", err)
    }
    
    c.replicator = replicator
    
    return nil
}

func (c *ReplicationComponent) Start(ctx context.Context) error {
    return c.replicator.Start(ctx)
}

func (c *ReplicationComponent) Stop(ctx context.Context) error {
    return c.replicator.Stop(ctx)
}

func (c *ReplicationComponent) HealthCheck(ctx context.Context) error {
    return c.replicator.HealthCheck(ctx)
}

func (c *ReplicationComponent) GetDependencies() []framework.ComponentType {
    // Replication 依赖 Transport 和 Storage（类型安全）
    return []framework.ComponentType{
        framework.ComponentTypeTransport,
        framework.ComponentTypeStorage,
    }
}

// GetReplicator 获取底层 Replicator 实例（测试用）
func (c *ReplicationComponent) GetReplicator() service.Replicator {
    return c.replicator
}

// Replicate 复制数据到指定分片（便捷方法）
func (c *ReplicationComponent) Replicate(
    ctx context.Context, 
    shardID string, 
    key, value []byte,
) error {
    return c.replicator.Replicate(ctx, shardID, key, value)
}

// GetQuorum 获取指定键的 Quorum 读取结果（便捷方法）
func (c *ReplicationComponent) GetQuorum(
    ctx context.Context, 
    shardID string, 
    key []byte,
) (*service.QuorumResult, error) {
    return c.replicator.GetQuorum(ctx, shardID, key)
}
```

---

## 五、测试集群实现

### 5.1 通用测试集群

```go
// pkg/test/framework/cluster_impl.go

package framework

import (
    "context"
    "fmt"
    "sync"
    "time"
)

// DefaultTestCluster 默认测试集群实现
type DefaultTestCluster struct {
    id          string
    nodes       []*DefaultTestNode
    nodeMap     map[string]*DefaultTestNode
    components  map[string]ComponentType
    mutex       sync.RWMutex
    status      EnvironmentStatus

    // P2-1 修复 (v2.8)：添加日志字段
    logger      *slog.Logger

    // 网络模拟
    partitions  map[string]*NetworkPartition

    // 资源管理
    portAllocator *PortAllocator
    tempDir       string
}

// DefaultTestNode 默认测试节点实现
type DefaultTestNode struct {
    id         string
    index      int
    clusterID  string
    address    string
    components map[string]TestComponent
    status     NodeStatus
    mutex      sync.RWMutex
}

// NewTestCluster 创建测试集群
func NewTestCluster(
    ctx context.Context,
    size int,
    components []ComponentType,
) (*DefaultTestCluster, error) {
    cluster := &DefaultTestCluster{
        id:            generateClusterID(),
        nodes:         make([]*DefaultTestNode, 0, size),
        nodeMap:       make(map[string]*DefaultTestNode),
        components:    make(map[string]ComponentType),
        partitions:    make(map[string]*NetworkPartition),
        portAllocator: NewPortAllocator(10000, 20000),
        tempDir:       generateTempDir(),
        logger:        slog.Default(),  // P2-1 修复 (v2.8)：初始化日志字段
    }
    
    // 创建所有节点
    for i := 0; i < size; i++ {
        node, err := cluster.createNode(ctx, i, components)
        if err != nil {
            cluster.cleanup()
            return nil, fmt.Errorf("failed to create node %d: %w", i, err)
        }
        cluster.nodes = append(cluster.nodes, node)
        cluster.nodeMap[node.id] = node
    }
    
    cluster.status = EnvironmentStatusCreated
    return cluster, nil
}

func (c *DefaultTestCluster) createNode(
    ctx context.Context,
    index int,
    componentTypes []ComponentType,
) (*DefaultTestNode, error) {
    nodeID := fmt.Sprintf("%s-node-%d", c.id, index)
    
    // 分配端口
    port, err := c.portAllocator.Allocate()
    if err != nil {
        return nil, err
    }
    
    node := &DefaultTestNode{
        id:         nodeID,
        index:      index,
        clusterID:  c.id,
        address:    fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port),
        components: make(map[string]TestComponent),
    }
    
    // 创建并初始化所有组件
    for _, compType := range componentTypes {
        config := ComponentConfig{
            Type:      compType,
            Name:      string(compType),
            NodeID:    nodeID,
            NodeIndex: index,
            ClusterID: c.id,
            Properties: map[string]interface{}{
                "listen_addr": node.address,
                "data_dir":    fmt.Sprintf("%s/%s", c.tempDir, nodeID),
            },
        }
        
        component, err := GlobalRegistry.Create(compType, config)
        if err != nil {
            return nil, fmt.Errorf("failed to create component %s: %w", compType, err)
        }
        
        // 初始化组件
        if err := component.Init(ctx, c); err != nil {
            return nil, fmt.Errorf("failed to init component %s: %w", compType, err)
        }
        
        node.components[string(compType)] = component
    }
    
    return node, nil
}

// Start 启动集群
func (c *DefaultTestCluster) Start(ctx context.Context) error {
    c.mutex.Lock()
    defer c.mutex.Unlock()
    
    c.status = EnvironmentStatusStarting
    
    // 按依赖顺序启动组件
    for _, node := range c.nodes {
        if err := c.startNodeComponents(ctx, node); err != nil {
            c.status = EnvironmentStatusError
            return fmt.Errorf("failed to start node %s: %w", node.id, err)
        }
        node.status = NodeStatusRunning
    }
    
    c.status = EnvironmentStatusRunning
    return nil
}

// ComponentStartConfig 组件启动配置
type ComponentStartConfig struct {
    // MaxRetries 最大重试次数
    MaxRetries int
    
    // RetryInterval 重试间隔
    RetryInterval time.Duration
    
    // Timeout 单次启动超时
    Timeout time.Duration
    
    // ContinueOnError 某个组件启动失败时是否继续启动其他组件
    ContinueOnError bool
    
    // RollbackOnFailure 启动失败时是否回滚已启动的组件
    RollbackOnFailure bool
}

// DefaultComponentStartConfig 返回默认启动配置
func DefaultComponentStartConfig() *ComponentStartConfig {
    return &ComponentStartConfig{
        MaxRetries:        3,
        RetryInterval:     2 * time.Second,
        Timeout:           30 * time.Second,
        ContinueOnError:   false,
        RollbackOnFailure: true,
    }
}

func (c *DefaultTestCluster) startNodeComponents(
    ctx context.Context,
    node *DefaultTestNode,
) error {
    config := DefaultComponentStartConfig()
    
    // 拓扑排序：按依赖顺序启动组件
    sortedComponents, err := c.topologicalSort(node.components)
    if err != nil {
        return err
    }
    
    // 记录已启动的组件，用于回滚
    startedComponents := []TestComponent{}
    
    for _, comp := range sortedComponents {
        // 使用带超时的上下文
        startCtx, cancel := context.WithTimeout(ctx, config.Timeout)
        
        // 带重试的启动
        var startErr error
        for attempt := 0; attempt <= config.MaxRetries; attempt++ {
            if attempt > 0 {
                // 重试前等待
                select {
                case <-time.After(config.RetryInterval):
                case <-ctx.Done():
                    cancel()
                    return ctx.Err()
                }
            }
            
            startErr = comp.Start(startCtx)
            if startErr == nil {
                break  // 启动成功
            }
            
            if attempt < config.MaxRetries {
                // 记录重试日志
                c.logger.Warn("component start failed, retrying",
                    "component", comp.Name(),
                    "attempt", attempt+1,
                    "maxRetries", config.MaxRetries+1,
                    "error", startErr)
            }
        }
        
        cancel()
        
        if startErr != nil {
            // 启动失败
            errMsg := fmt.Errorf("failed to start component %s after %d attempts: %w",
                comp.Name(), config.MaxRetries+1, startErr)
            
            // 如果需要回滚
            if config.RollbackOnFailure {
                rollbackErr := c.rollbackStartedComponents(ctx, startedComponents)
                if rollbackErr != nil {
                    return fmt.Errorf("%v; rollback failed: %w", errMsg, rollbackErr)
                }
            }
            
            if !config.ContinueOnError {
                return errMsg
            }
            
            // 继续启动其他组件，但记录错误
            c.logger.Warn("continuing after component start failure", "error", errMsg)
            continue
        }
        
        // 启动成功，记录到已启动列表
        startedComponents = append(startedComponents, comp)
        
        // 启动后健康检查
        healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
        if err := comp.HealthCheck(healthCtx); err != nil {
            healthCancel()
            
            // 健康检查失败，停止该组件
            stopCtx, stopCancel := context.WithTimeout(ctx, 10*time.Second)
            comp.Stop(stopCtx)
            stopCancel()
            
            if !config.ContinueOnError {
                return fmt.Errorf("component %s health check failed: %w", comp.Name(), err)
            }
            
            c.logger.Warn("component health check failed",
                "component", comp.Name(),
                "error", err)
            continue
        }
        healthCancel()
    }
    
    return nil
}

// rollbackStartedComponents 回滚已启动的组件
func (c *DefaultTestCluster) rollbackStartedComponents(
    ctx context.Context,
    components []TestComponent,
) error {
    var errs []error
    
    // 反向停止组件（后启动的先停止）
    for i := len(components) - 1; i >= 0; i-- {
        comp := components[i]
        
        stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
        if err := comp.Stop(stopCtx); err != nil {
            errs = append(errs, fmt.Errorf("failed to stop component %s: %w", comp.Name(), err))
        }
        cancel()
    }
    
    if len(errs) > 0 {
        return fmt.Errorf("rollback errors: %v", errs)
    }
    
    return nil
}

// Stop 停止集群
func (c *DefaultTestCluster) Stop(ctx context.Context) error {
    c.mutex.Lock()
    defer c.mutex.Unlock()
    
    c.status = EnvironmentStatusStopping
    
    // 反向停止组件
    for _, node := range c.nodes {
        c.stopNodeComponents(ctx, node)
        node.status = NodeStatusStopped
    }
    
    c.cleanup()
    c.status = EnvironmentStatusStopped
    return nil
}

// CleanupConfig 资源清理配置
type CleanupConfig struct {
    // Force 是否强制清理（即使组件停止失败也继续）
    Force bool
    
    // Timeout 单个组件停止超时
    Timeout time.Duration
    
    // TotalTimeout 整体清理超时
    TotalTimeout time.Duration
    
    // RemoveTempDir 是否删除临时目录
    RemoveTempDir bool
    
    // ReleasePorts 是否释放端口
    ReleasePorts bool
}

// DefaultCleanupConfig 返回默认清理配置
func DefaultCleanupConfig() *CleanupConfig {
    return &CleanupConfig{
        Force:         true,
        Timeout:       10 * time.Second,
        TotalTimeout:  60 * time.Second,
        RemoveTempDir: true,
        ReleasePorts:  true,
    }
}

func (c *DefaultTestCluster) cleanup() {
    config := DefaultCleanupConfig()

    // 使用总体超时控制
    ctx, cancel := context.WithTimeout(context.Background(), config.TotalTimeout)
    defer cancel()

    // 使用 errgroup 管理 goroutine，确保所有清理任务完成或出错
    g, ctx := errgroup.WithContext(ctx)

    // 1. 停止所有组件（强制模式）
    for _, node := range c.nodes {
        for _, comp := range node.components {
            comp := comp // 闭包捕获
            g.Go(func() error {
                stopCtx, stopCancel := context.WithTimeout(ctx, config.Timeout)
                defer stopCancel()

                // 尝试停止组件
                if err := comp.Stop(stopCtx); err != nil {
                    if !config.Force {
                        return fmt.Errorf("failed to stop component %s: %w", comp.Name(), err)
                    }
                    // 强制模式下记录错误但继续
                    c.logger.Warn("failed to stop component", "component", comp.Name(), "error", err)
                }
                return nil
            })
        }
    }

    // 2. 释放端口
    if config.ReleasePorts {
        for _, node := range c.nodes {
            node := node // 闭包捕获
            g.Go(func() error {
                port := extractPort(node.address)
                if err := c.portAllocator.Release(port); err != nil {
                    c.logger.Warn("failed to release port", "port", port, "error", err)
                    // 端口释放失败不阻塞清理
                }
                return nil
            })
        }
    }

    // 3. 清理临时目录（同步执行，确保在端口释放后）
    g.Go(func() error {
        if config.RemoveTempDir && c.tempDir != "" {
            if err := os.RemoveAll(c.tempDir); err != nil {
                c.logger.Warn("failed to remove temp dir", "dir", c.tempDir, "error", err)
            }
        }
        return nil
    })

    // 等待所有清理任务完成或超时
    if err := g.Wait(); err != nil {
        c.logger.Error("cleanup failed", "error", err)
    }
}

// ForceCleanup 强制清理（用于测试中断时的紧急清理）
//
// P1-4 修复 (v2.8)：信号处理可选，避免影响并行测试
// 注意：并行测试时应禁用信号处理，使用 TestMain 统一管理
func (c *DefaultTestCluster) ForceCleanup() {
    c.ForceCleanupWithSignal(true)
}

// ForceCleanupWithSignal 带信号处理配置的强制清理
func (c *DefaultTestCluster) ForceCleanupWithSignal(enableSignalHandler bool) {
    // P1-4 修复 (v2.8)：信号处理可选
    if enableSignalHandler {
        sigChan := make(chan os.Signal, 1)
        signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

        go func() {
            <-sigChan
            c.logger.Info("received interrupt signal, forcing cleanup")
            c.cleanup()
            os.Exit(1)
        }()
    }

    // 执行正常清理
    c.cleanup()
}

// GetNode 获取指定索引的节点
func (c *DefaultTestCluster) GetNode(index int) (TestNode, error) {
    c.mutex.RLock()
    defer c.mutex.RUnlock()
    
    if index < 0 || index >= len(c.nodes) {
        return nil, fmt.Errorf("node index out of range: %d", index)
    }
    
    return c.nodes[index], nil
}

// GetNodeByID 根据ID获取节点
func (c *DefaultTestCluster) GetNodeByID(nodeID string) (TestNode, error) {
    c.mutex.RLock()
    defer c.mutex.RUnlock()
    
    node, exists := c.nodeMap[nodeID]
    if !exists {
        return nil, fmt.Errorf("node not found: %s", nodeID)
    }
    
    return node, nil
}

// GetAllNodes 获取所有节点
func (c *DefaultTestCluster) GetAllNodes() []TestNode {
    c.mutex.RLock()
    defer c.mutex.RUnlock()
    
    result := make([]TestNode, len(c.nodes))
    for i, node := range c.nodes {
        result[i] = node
    }
    return result
}

// WaitForReady 等待集群就绪
func (c *DefaultTestCluster) WaitForReady(
    ctx context.Context, 
    timeout time.Duration,
) error {
    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            if c.isReady(ctx) {
                return nil
            }
        }
    }
}

func (c *DefaultTestCluster) isReady(ctx context.Context) bool {
    // P1-2 修复 (v2.8)：使用统一的超时 context，避免频繁创建
    checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()

    for _, node := range c.nodes {
        if node.status != NodeStatusRunning {
            return false
        }

        // 检查所有组件健康
        for _, comp := range node.components {
            // 检查是否已取消
            select {
            case <-checkCtx.Done():
                return false
            default:
            }

            if err := comp.HealthCheck(checkCtx); err != nil {
                return false
            }
        }
    }
    return true
}

// NetworkPartitionController 网络分区控制器
// 提供完整的分区模拟能力，包括消息阻塞、延迟注入、丢包等
//
// P0-2 修复 (v2.8)：添加 context 取消机制，防止 goroutine 泄漏
type NetworkPartitionController struct {
    cluster         *DefaultTestCluster
    partitionID     string
    partitionNodes  map[string]bool

    // 分区状态
    isActive        bool
    startTime       time.Time

    // 消息控制
    blockedMessages []PartitionedMessage  // 被阻塞的消息
    droppedMessages []PartitionedMessage  // 被丢弃的消息

    // 网络模拟参数
    latencyInjection time.Duration         // 延迟注入
    packetLossRate   float64               // 丢包率 (0.0 - 1.0)
    bandwidthLimit   int64                 // 带宽限制 (bytes/sec)

    // P0-2 修复 (v2.8)：添加取消机制
    ctx    context.Context
    cancel context.CancelFunc

    mutex           sync.RWMutex
}

// PartitionedMessage 被分区的消息
type PartitionedMessage struct {
    From      string
    To        string
    Payload   []byte
    Timestamp time.Time
    Action    PartitionAction
}

// PartitionAction 分区动作类型
type PartitionAction int

const (
    PartitionActionBlock PartitionAction = iota   // 阻塞消息
    PartitionActionDrop                           // 丢弃消息
    PartitionActionDelay                          // 延迟消息
)

// NewNetworkPartitionController 创建网络分区控制器
func NewNetworkPartitionController(
    cluster *DefaultTestCluster,
    partitionID string,
    nodeIndices []int,
) (*NetworkPartitionController, error) {
    // P0-2 修复 (v2.8)：创建可取消的上下文
    ctx, cancel := context.WithCancel(context.Background())

    controller := &NetworkPartitionController{
        cluster:        cluster,
        partitionID:    partitionID,
        partitionNodes: make(map[string]bool),
        blockedMessages: make([]PartitionedMessage, 0),
        droppedMessages: make([]PartitionedMessage, 0),
        ctx:    ctx,
        cancel: cancel,
    }
    
    // 构建分区节点集合
    for _, idx := range nodeIndices {
        if idx < 0 || idx >= len(cluster.nodes) {
            return nil, fmt.Errorf("node index out of range: %d", idx)
        }
        controller.partitionNodes[cluster.nodes[idx].id] = true
    }
    
    return controller, nil
}

// Apply 应用网络分区
func (pc *NetworkPartitionController) Apply() error {
    pc.mutex.Lock()
    defer pc.mutex.Unlock()
    
    if pc.isActive {
        return fmt.Errorf("partition %s is already active", pc.partitionID)
    }
    
    pc.isActive = true
    pc.startTime = time.Now()
    
    // 1. 暂停所有正在进行的 RPC 调用
    if err := pc.pauseActiveRPCs(); err != nil {
        return fmt.Errorf("failed to pause active RPCs: %w", err)
    }
    
    // 2. 清空连接池，阻止新连接
    if err := pc.disconnectCrossPartitionPeers(); err != nil {
        return fmt.Errorf("failed to disconnect peers: %w", err)
    }
    
    // 3. 安装消息拦截器
    if err := pc.installMessageInterceptor(); err != nil {
        return fmt.Errorf("failed to install message interceptor: %w", err)
    }
    
    // 4. 记录分区事件
    pc.cluster.partitions[pc.partitionID] = &NetworkPartition{
        ID:       pc.partitionID,
        Nodes:    pc.getPartitionNodeIDs(),
        Isolated: true,
    }
    
    return nil
}

// Heal 恢复网络分区
func (pc *NetworkPartitionController) Heal() error {
    pc.mutex.Lock()
    defer pc.mutex.Unlock()

    if !pc.isActive {
        return fmt.Errorf("partition %s is not active", pc.partitionID)
    }

    // P0-2 修复 (v2.8)：取消所有延迟发送的 goroutine
    pc.cancel()

    // 1. 移除消息拦截器
    if err := pc.removeMessageInterceptor(); err != nil {
        return fmt.Errorf("failed to remove message interceptor: %w", err)
    }
    
    // 2. 恢复被阻塞的消息（可选：延迟恢复以模拟渐进恢复）
    if err := pc.restoreBlockedMessages(); err != nil {
        return fmt.Errorf("failed to restore blocked messages: %w", err)
    }
    
    // 3. 允许重新连接
    if err := pc.enableReconnection(); err != nil {
        return fmt.Errorf("failed to enable reconnection: %w", err)
    }
    
    // 4. 清理分区状态
    pc.isActive = false
    delete(pc.cluster.partitions, pc.partitionID)
    
    return nil
}

// SetLatencyInjection 设置延迟注入
func (pc *NetworkPartitionController) SetLatencyInjection(latency time.Duration) {
    pc.mutex.Lock()
    defer pc.mutex.Unlock()
    pc.latencyInjection = latency
}

// SetPacketLossRate 设置丢包率
func (pc *NetworkPartitionController) SetPacketLossRate(rate float64) error {
    if rate < 0 || rate > 1 {
        return fmt.Errorf("packet loss rate must be between 0 and 1")
    }
    pc.mutex.Lock()
    defer pc.mutex.Unlock()
    pc.packetLossRate = rate
    return nil
}

// GetBlockedMessages 获取被阻塞的消息
func (pc *NetworkPartitionController) GetBlockedMessages() []PartitionedMessage {
    pc.mutex.RLock()
    defer pc.mutex.RUnlock()
    
    result := make([]PartitionedMessage, len(pc.blockedMessages))
    copy(result, pc.blockedMessages)
    return result
}

// GetDroppedMessages 获取被丢弃的消息
func (pc *NetworkPartitionController) GetDroppedMessages() []PartitionedMessage {
    pc.mutex.RLock()
    defer pc.mutex.RUnlock()
    
    result := make([]PartitionedMessage, len(pc.droppedMessages))
    copy(result, pc.droppedMessages)
    return result
}

// 内部方法

func (pc *NetworkPartitionController) pauseActiveRPCs() error {
    // 遍历所有节点，暂停跨分区的活跃 RPC
    for _, node := range pc.cluster.nodes {
        transportComp, ok := node.components["transport"]
        if !ok {
            continue
        }
        
        transport := transportComp.(*transport_test.TransportComponent).GetTransport()
        
        // 获取所有活跃连接
        for _, peerID := range transport.ConnectedPeers() {
            inPartition := pc.partitionNodes[node.id]
            peerInPartition := pc.partitionNodes[peerID]
            
            // 如果节点和 peer 在不同分区，标记 RPC 为暂停
            if inPartition != peerInPartition {
                // 实际实现中，这里应该通知 RPC 层暂停相关调用
                // transport.PauseRPCsTo(peerID)
            }
        }
    }
    return nil
}

func (pc *NetworkPartitionController) disconnectCrossPartitionPeers() error {
    for _, node := range pc.cluster.nodes {
        transportComp, ok := node.components["transport"]
        if !ok {
            continue
        }
        
        transport := transportComp.(*transport_test.TransportComponent).GetTransport()
        
        for _, peerID := range transport.ConnectedPeers() {
            inPartition := pc.partitionNodes[node.id]
            peerInPartition := pc.partitionNodes[peerID]
            
            // 如果节点和 peer 在不同分区，断开连接
            if inPartition != peerInPartition {
                if err := transport.DisconnectFrom(peerID); err != nil {
                    // 记录错误但不中断，继续断开其他连接
                    continue
                }
            }
        }
    }
    return nil
}

func (pc *NetworkPartitionController) installMessageInterceptor() error {
    // 安装消息拦截器，拦截跨分区消息
    // 实际实现中，这里会替换 Transport 层的消息发送逻辑
    for _, node := range pc.cluster.nodes {
        transportComp, ok := node.components["transport"]
        if !ok {
            continue
        }
        
        transport := transportComp.(*transport_test.TransportComponent).GetTransport()
        
        // 设置消息拦截回调
        transport.SetMessageInterceptor(func(msg *Message) error {
            return pc.interceptMessage(node.id, msg)
        })
    }
    return nil
}

func (pc *NetworkPartitionController) interceptMessage(from string, msg *Message) error {
    // P1-5 修复 (v2.8)：快速释放锁，避免阻塞其他消息处理
    pc.mutex.RLock()
    isActive := pc.isActive
    fromInPartition := pc.partitionNodes[from]
    toInPartition := pc.partitionNodes[msg.To]
    latency := pc.latencyInjection
    lossRate := pc.packetLossRate
    partitionID := pc.partitionID
    pc.mutex.RUnlock()

    if !isActive {
        return nil // 分区未激活，不拦截
    }
    // 同分区消息不拦截
    if fromInPartition == toInPartition {
        return nil
    }
    
    // 跨分区消息处理
    partitionedMsg := PartitionedMessage{
        From:      from,
        To:        msg.To,
        Payload:   msg.Payload,
        Timestamp: time.Now(),
    }

    // P1-5 修复 (v2.8)：使用局部变量，快速释放锁
    // 根据配置决定如何处理
    if lossRate > 0 && rand.Float64() < lossRate {
        // 模拟丢包
        partitionedMsg.Action = PartitionActionDrop
        pc.mutex.Lock()
        pc.droppedMessages = append(pc.droppedMessages, partitionedMsg)
        pc.mutex.Unlock()
        return fmt.Errorf("message dropped due to partition: %s", partitionID)
    }

    if latency > 0 {
        // P0-2 修复 (v2.8)：使用 context 取消机制，避免 goroutine 泄漏
        partitionedMsg.Action = PartitionActionDelay
        go func() {
            select {
            case <-time.After(latency):
                // 延迟后尝试重新发送
                // transport.Send(msg)
            case <-pc.ctx.Done():
                // 分区恢复时取消
                return
            }
        }()
        return nil
    }

    // 默认：阻塞消息
    partitionedMsg.Action = PartitionActionBlock
    pc.mutex.Lock()
    pc.blockedMessages = append(pc.blockedMessages, partitionedMsg)
    pc.mutex.Unlock()
    return fmt.Errorf("message blocked by partition: %s", partitionID)
}

func (pc *NetworkPartitionController) removeMessageInterceptor() error {
    // 移除消息拦截器
    for _, node := range pc.cluster.nodes {
        transportComp, ok := node.components["transport"]
        if !ok {
            continue
        }
        
        transport := transportComp.(*transport_test.TransportComponent).GetTransport()
        transport.SetMessageInterceptor(nil)
    }
    return nil
}

func (pc *NetworkPartitionController) restoreBlockedMessages() error {
    // 恢复被阻塞的消息
    for _, msg := range pc.blockedMessages {
        // 实际实现中，这里应该重新发送消息
        // transport.Send(msg)
    }
    pc.blockedMessages = pc.blockedMessages[:0] // 清空
    return nil
}

func (pc *NetworkPartitionController) enableReconnection() error {
    // 允许节点重新连接
    // 实际实现中，这里应该清除连接黑名单
    return nil
}

func (pc *NetworkPartitionController) getPartitionNodeIDs() []string {
    result := make([]string, 0, len(pc.partitionNodes))
    for nodeID := range pc.partitionNodes {
        result = append(result, nodeID)
    }
    return result
}

// Partition 模拟网络分区（简化接口，内部使用 NetworkPartitionController）
func (c *DefaultTestCluster) Partition(
    partitionID string, 
    nodeIndices []int,
) error {
    controller, err := NewNetworkPartitionController(c, partitionID, nodeIndices)
    if err != nil {
        return err
    }
    
    return controller.Apply()
}

// HealPartition 恢复网络分区
func (c *DefaultTestCluster) HealPartition(partitionID string) error {
    c.mutex.Lock()
    partition, exists := c.partitions[partitionID]
    c.mutex.Unlock()
    
    if !exists {
        return fmt.Errorf("partition not found: %s", partitionID)
    }
    
    // 获取分区节点索引
    nodeIndices := make([]int, 0, len(partition.Nodes))
    for _, nodeID := range partition.Nodes {
        for i, node := range c.nodes {
            if node.id == nodeID {
                nodeIndices = append(nodeIndices, i)
                break
            }
        }
    }
    
    controller, err := NewNetworkPartitionController(c, partitionID, nodeIndices)
    if err != nil {
        return err
    }
    
    return controller.Heal()
}

// DefaultTestNode 方法实现

func (n *DefaultTestNode) ID() string {
    return n.id
}

func (n *DefaultTestNode) Address() string {
    return n.address
}

func (n *DefaultTestNode) GetComponent(name string) (TestComponent, error) {
    n.mutex.RLock()
    defer n.mutex.RUnlock()
    
    comp, exists := n.components[name]
    if !exists {
        return nil, fmt.Errorf("component not found: %s", name)
    }
    
    return comp, nil
}

func (n *DefaultTestNode) IsHealthy(ctx context.Context) bool {
    n.mutex.RLock()
    defer n.mutex.RUnlock()
    
    if n.status != NodeStatusRunning {
        return false
    }
    
    for _, comp := range n.components {
        if err := comp.HealthCheck(ctx); err != nil {
            return false
        }
    }
    
    return true
}

func (n *DefaultTestNode) ConnectTo(ctx context.Context, target TestNode) error {
    transportComp, err := n.GetComponent("transport")
    if err != nil {
        return err
    }
    
    transport := transportComp.(*transport_test.TransportComponent).GetTransport()
    return transport.ConnectTo(ctx, target.Address())
}

func (n *DefaultTestNode) IsConnectedTo(nodeID string) bool {
    transportComp, err := n.GetComponent("transport")
    if err != nil {
        return false
    }
    
    transport := transportComp.(*transport_test.TransportComponent).GetTransport()
    
    for _, peerID := range transport.ConnectedPeers() {
        if peerID == nodeID {
            return true
        }
    }
    return false
}

func (n *DefaultTestNode) GetConnectedPeers() []string {
    transportComp, err := n.GetComponent("transport")
    if err != nil {
        return nil
    }
    
    transport := transportComp.(*transport_test.TransportComponent).GetTransport()
    return transport.ConnectedPeers()
}
```

---

## 九、测试数据生成器

### 9.1 数据生成器接口

```go
// pkg/test/framework/data_generator.go

package framework

import (
	"math/rand"
	"time"
)

// DataGenerator 测试数据生成器接口
//
// P2 修复 (v2.6)：添加边界条件处理规范
//
// 边界条件处理：
//   - count <= 0: 返回空切片，不返回错误
//   - count > 1000000: 返回错误（超出内存限制）
//   - size <= 0: 使用默认值 1KB
//   - size > 10MB: 返回错误（单值过大）
//
// 使用示例：
//
//	keys, err := gen.GenerateKeys(ctx, 1000)    // 正常
//	keys, err := gen.GenerateKeys(ctx, 0)       // 返回空切片
//	keys, err := gen.GenerateKeys(ctx, -1)      // 返回空切片
//	keys, err := gen.GenerateKeys(ctx, 2000000) // 返回错误
type DataGenerator interface {
	// GenerateKeys 生成指定数量的键
	// count: 键的数量，必须 0 < count <= 1000000，否则按边界条件处理
	GenerateKeys(ctx context.Context, count int) ([][]byte, error)

	// GenerateValues 生成指定大小的值
	// count: 值的数量，必须 0 < count <= 1000000
	// size: 每个值的大小（字节），必须 0 < size <= 10MB，size <= 0 时使用默认值 1KB
	GenerateValues(ctx context.Context, count int, size int) ([][]byte, error)

	// GenerateKeyValuePairs 生成键值对
	// count: 键值对数量，必须 0 < count <= 1000000
	// valueSize: 值的大小（字节），必须 0 < valueSize <= 10MB
	GenerateKeyValuePairs(ctx context.Context, count int, valueSize int) ([]KeyValuePair, error)

	// SetDistribution 设置数据分布类型
	SetDistribution(dist DistributionType)

	// SetSeed 设置随机种子（用于可重复测试）
	SetSeed(seed int64)
}

// KeyValuePair 键值对
type KeyValuePair struct {
	Key   []byte
	Value []byte
}

// DistributionType 数据分布类型
type DistributionType int

const (
	DistributionUniform DistributionType = iota   // 均匀分布
	DistributionZipfian                           // Zipfian 分布（热点）
	DistributionHotspot                           // 热点分布
	DistributionSequential                        // 顺序分布
	DistributionRandom                            // 随机分布
)

// BaseDataGenerator 基础数据生成器
type BaseDataGenerator struct {
	rand      *rand.Rand
	dist      DistributionType
	keyPrefix string
	seed      int64
}

// NewDataGenerator 创建数据生成器
func NewDataGenerator() DataGenerator {
	return &BaseDataGenerator{
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
		dist:      DistributionUniform,
		keyPrefix: "key-",
	}
}

// NewDataGeneratorWithSeed 创建指定随机种子的数据生成器
func NewDataGeneratorWithSeed(seed int64) DataGenerator {
	return &BaseDataGenerator{
		rand:      rand.New(rand.NewSource(seed)),
		dist:      DistributionUniform,
		keyPrefix: "key-",
		seed:      seed,
	}
}

func (g *BaseDataGenerator) SetDistribution(dist DistributionType) {
	g.dist = dist
}

func (g *BaseDataGenerator) SetSeed(seed int64) {
	g.seed = seed
	g.rand = rand.New(rand.NewSource(seed))
}

func (g *BaseDataGenerator) GenerateKeys(ctx context.Context, count int) ([][]byte, error) {
	// P1-3 修复 (v2.8)：边界条件处理
	if count <= 0 {
		return [][]byte{}, nil
	}
	if count > 1000000 {
		return nil, fmt.Errorf("count %d exceeds maximum limit 1000000", count)
	}

	keys := make([][]byte, count)

	for i := 0; i < count; i++ {
		// 检查上下文是否被取消
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		switch g.dist {
		case DistributionUniform:
			keys[i] = g.generateUniformKey(i, count)
		case DistributionZipfian:
			keys[i] = g.generateZipfianKey(i, count)
		case DistributionHotspot:
			keys[i] = g.generateHotspotKey(i, count)
		case DistributionSequential:
			keys[i] = g.generateSequentialKey(i)
		case DistributionRandom:
			keys[i] = g.generateRandomKey()
		default:
			keys[i] = g.generateUniformKey(i, count)
		}
	}

	return keys, nil
}

func (g *BaseDataGenerator) GenerateValues(ctx context.Context, count int, size int) ([][]byte, error) {
	// P2-5 修复 (v2.8)：添加边界条件检查
	if count <= 0 {
		return [][]byte{}, nil
	}
	if count > 1000000 {
		return nil, fmt.Errorf("count %d exceeds maximum limit 1000000", count)
	}

	// size 边界检查
	if size <= 0 {
		size = 1024 // 默认 1KB
	}
	if size > 10*1024*1024 { // 10MB
		return nil, fmt.Errorf("size %d exceeds maximum limit 10MB", size)
	}

	values := make([][]byte, count)

	for i := 0; i < count; i++ {
		// 检查上下文是否被取消
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		values[i] = g.generateRandomValue(size)
	}

	return values, nil
}

func (g *BaseDataGenerator) GenerateKeyValuePairs(ctx context.Context, count int, valueSize int) ([]KeyValuePair, error) {
	keys, err := g.GenerateKeys(ctx, count)
	if err != nil {
		return nil, err
	}

	values, err := g.GenerateValues(ctx, count, valueSize)
	if err != nil {
		return nil, err
	}

	pairs := make([]KeyValuePair, count)
	for i := 0; i < count; i++ {
		pairs[i] = KeyValuePair{
			Key:   keys[i],
			Value: values[i],
		}
	}

	return pairs, nil
}

// 内部方法

func (g *BaseDataGenerator) generateUniformKey(index, total int) []byte {
	// 均匀分布：键空间均匀分布
	keyID := (index * 1000000) / total
	return []byte(fmt.Sprintf("%s%010d", g.keyPrefix, keyID))
}

func (g *BaseDataGenerator) generateZipfianKey(index, total int) []byte {
	// Zipfian 分布：少数键被频繁访问（热点）
	// 使用 Zipf 分布生成键 ID
	zipf := rand.NewZipf(g.rand, 1.1, 1, uint64(total))
	keyID := zipf.Uint64()
	return []byte(fmt.Sprintf("%s%010d", g.keyPrefix, keyID))
}

func (g *BaseDataGenerator) generateHotspotKey(index, total int) []byte {
	// 热点分布：80% 的访问集中在 20% 的键
	hotspotSize := total / 5 // 20% 热点
	
	var keyID int
	if g.rand.Float64() < 0.8 {
		// 80% 概率访问热点区域
		keyID = g.rand.Intn(hotspotSize)
	} else {
		// 20% 概率访问非热点区域
		keyID = hotspotSize + g.rand.Intn(total-hotspotSize)
	}
	
	return []byte(fmt.Sprintf("%s%010d", g.keyPrefix, keyID))
}

func (g *BaseDataGenerator) generateSequentialKey(index int) []byte {
	return []byte(fmt.Sprintf("%s%010d", g.keyPrefix, index))
}

func (g *BaseDataGenerator) generateRandomKey() []byte {
	// 完全随机键
	keyID := g.rand.Int63()
	return []byte(fmt.Sprintf("%s%020d", g.keyPrefix, keyID))
}

func (g *BaseDataGenerator) generateRandomValue(size int) []byte {
	value := make([]byte, size)
	g.rand.Read(value)
	return value
}
```

### 9.2 使用示例

```go
// test/data_generator_example_test.go

package test

import (
	"testing"
	
	"github.com/jzhang405/NexKV/pkg/test/framework"
)

func TestDataGenerator(t *testing.T) {
	// 创建均匀分布的数据生成器
	uniformGen := framework.NewDataGeneratorWithSeed(12345)
	uniformGen.SetDistribution(framework.DistributionUniform)
	
	// 生成 10000 个键
	keys := uniformGen.GenerateKeys(10000)
	t.Logf("Generated %d uniform keys", len(keys))
	
	// 创建 Zipfian 分布的数据生成器（模拟热点）
	zipfGen := framework.NewDataGeneratorWithSeed(12345)
	zipfGen.SetDistribution(framework.DistributionZipfian)
	
	// 生成 10000 个键值对，值大小 1KB
	pairs := zipfGen.GenerateKeyValuePairs(10000, 1024)
	t.Logf("Generated %d Zipfian key-value pairs", len(pairs))
	
	// 创建热点分布的数据生成器
	hotspotGen := framework.NewDataGeneratorWithSeed(12345)
	hotspotGen.SetDistribution(framework.DistributionHotspot)
	
	// 生成键用于性能测试
	hotspotKeys := hotspotGen.GenerateKeys(100000)
	t.Logf("Generated %d hotspot keys", len(hotspotKeys))
}
```

---

## 十、分布式系统指标

### 10.1 扩展指标定义

```go
// pkg/test/framework/metrics.go

package framework

import (
	"time"
)

// DistributedMetrics 分布式系统专用指标
type DistributedMetrics struct {
	// 基础指标（继承自 TestMetrics）
	TestMetrics
	
	// 一致性指标
	ConsistencyLatency    time.Duration              // 写入到所有副本可见的时间
	ReplicationLag        map[string]time.Duration   // 每个副本的复制延迟
	QuorumLatency         time.Duration              // Quorum 写入延迟
	
	// 故障恢复指标
	FailoverTime          time.Duration              // 故障转移时间
	RecoveryTime          time.Duration              // 完全恢复时间
	LeaderElectionTime    time.Duration              // 领导者选举时间
	
	// 分区容忍指标
	PartitionDetectionTime time.Duration             // 分区检测时间
	HealingTime            time.Duration             // 分区恢复时间
	SplitBrainDuration     time.Duration             // 脑裂持续时间
	
	// 可用性指标
	AvailabilityPercent   float64                    // 可用性百分比
	DowntimeDuration      time.Duration              // 停机时间
	DegradedDuration      time.Duration              // 降级服务时间
	
	// 性能指标
	ThroughputP99         float64                    // P99 吞吐量
	LatencyP50            time.Duration              // P50 延迟
	LatencyP99            time.Duration              // P99 延迟
	
	// 资源指标
	NetworkBytesSent      uint64                     // 发送字节数
	NetworkBytesReceived  uint64                     // 接收字节数
	DiskIOBytes           uint64                     // 磁盘 IO 字节数
	MemoryPeakUsage       uint64                     // 内存峰值
}

// MetricsCollector 指标收集器
type MetricsCollector struct {
	startTime    time.Time
	metrics      *DistributedMetrics
	latencyHist  []time.Duration
	throughputSamples []float64
}

// NewMetricsCollector 创建指标收集器
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		startTime: time.Now(),
		metrics: &DistributedMetrics{
			ReplicationLag: make(map[string]time.Duration),
		},
		latencyHist: make([]time.Duration, 0),
		throughputSamples: make([]float64, 0),
	}
}

// RecordLatency 记录延迟
func (mc *MetricsCollector) RecordLatency(latency time.Duration) {
	mc.latencyHist = append(mc.latencyHist, latency)
}

// RecordThroughput 记录吞吐量样本
func (mc *MetricsCollector) RecordThroughput(throughput float64) {
	mc.throughputSamples = append(mc.throughputSamples, throughput)
}

// RecordReplicationLag 记录复制延迟
func (mc *MetricsCollector) RecordReplicationLag(nodeID string, lag time.Duration) {
	mc.metrics.ReplicationLag[nodeID] = lag
}

// CalculatePercentiles 计算百分位数
func (mc *MetricsCollector) CalculatePercentiles() {
	if len(mc.latencyHist) > 0 {
		mc.metrics.LatencyP50 = calculatePercentile(mc.latencyHist, 0.5)
		mc.metrics.LatencyP99 = calculatePercentile(mc.latencyHist, 0.99)
	}
	
	if len(mc.throughputSamples) > 0 {
		mc.metrics.ThroughputP99 = calculateFloatPercentile(mc.throughputSamples, 0.99)
	}
}

// GetMetrics 获取收集的指标
func (mc *MetricsCollector) GetMetrics() *DistributedMetrics {
	mc.CalculatePercentiles()
	mc.metrics.Duration = time.Since(mc.startTime)
	return mc.metrics
}

// 辅助函数

func calculatePercentile(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}

	// P2-2 修复 (v2.8)：添加排序，确保百分位数计算正确
	sorted := make([]time.Duration, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	index := int(float64(len(sorted)-1) * percentile)
	return sorted[index]
}

// P1-6 修复 (v2.8)：添加排序，确保浮点数百分位数计算正确
func calculateFloatPercentile(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	index := int(float64(len(sorted)-1) * percentile)
	return sorted[index]
}
```

### 10.2 指标收集示例

```go
// test/metrics_collection_test.go

package test

import (
	"context"
	"testing"
	"time"
	
	"github.com/jzhang405/NexKV/pkg/test/framework"
)

func TestMetricsCollection(t *testing.T) {
	ctx := context.Background()
	
	// 创建集群
	cluster, err := framework.NewTestCluster(ctx, 3, []framework.ComponentType{
		framework.ComponentTypeTransport,
		framework.ComponentTypeStorage,
		framework.ComponentTypeReplication,
	})
	if err != nil {
		t.Fatalf("Failed to create cluster: %v", err)
	}
	defer cluster.Stop(ctx)
	
	// 创建指标收集器
	collector := framework.NewMetricsCollector()
	
	// 执行测试操作并记录指标
	start := time.Now()
	
	// 模拟写入操作
	for i := 0; i < 1000; i++ {
		opStart := time.Now()
		
		// 执行操作...
		
		collector.RecordLatency(time.Since(opStart))
	}
	
	// 记录复制延迟
	for _, node := range cluster.GetAllNodes() {
		// 获取节点复制延迟...
		collector.RecordReplicationLag(node.ID(), 10*time.Millisecond)
	}
	
	// 获取最终指标
	metrics := collector.GetMetrics()
	
	t.Logf("Test duration: %v", metrics.Duration)
	t.Logf("P50 latency: %v", metrics.LatencyP50)
	t.Logf("P99 latency: %v", metrics.LatencyP99)
	t.Logf("Replication lag: %v", metrics.ReplicationLag)
	
	_ = start
}
```

---

## 六、测试场景实现

### 6.1 Transport 测试场景

```go
// internal/infrastructure/transport/test/scenarios/discovery_scenario.go

package scenarios

import (
    "context"
    "fmt"
    "time"
    
    "github.com/jzhang405/NexKV/pkg/test/framework"
)

// DiscoveryScenario 节点发现测试场景
type DiscoveryScenario struct {
    maxDiscoveryTime time.Duration
    expectedPeers    int
}

func NewDiscoveryScenario() *DiscoveryScenario {
    return &DiscoveryScenario{
        maxDiscoveryTime: 5 * time.Second,
        expectedPeers:    2,  // 3节点集群，每个节点应有2个peer
    }
}

func (s *DiscoveryScenario) Name() string {
    return "Transport.NodeDiscovery"
}

func (s *DiscoveryScenario) Description() string {
    return "验证 mDNS 节点发现功能，确保所有节点能在 5 秒内互相发现"
}

func (s *DiscoveryScenario) GetRequiredComponents() []framework.ComponentType {
    return []framework.ComponentType{
        framework.ComponentTypeTransport,
    }
}

func (s *DiscoveryScenario) GetMinNodeCount() int {
    return 3
}

func (s *DiscoveryScenario) Setup(
    ctx context.Context, 
    cluster framework.TestCluster,
) error {
    // 集群已在创建时启动，无需额外设置
    return nil
}

func (s *DiscoveryScenario) Execute(
    ctx context.Context, 
    cluster framework.TestCluster,
) (*framework.ScenarioResult, error) {
    start := time.Now()
    
    // 等待节点发现完成
    if err := cluster.WaitForReady(ctx, s.maxDiscoveryTime); err != nil {
        return nil, fmt.Errorf("cluster not ready: %w", err)
    }
    
    discoveryTime := time.Since(start)
    
    // 收集指标
    metrics := &framework.TestMetrics{
        CustomMetrics: map[string]interface{}{
            "discovery_time_ms": discoveryTime.Milliseconds(),
        },
    }
    
    return &framework.ScenarioResult{
        Success: discoveryTime <= s.maxDiscoveryTime,
        Metrics: metrics,
    }, nil
}

func (s *DiscoveryScenario) Verify(
    ctx context.Context, 
    result *framework.ScenarioResult,
) error {
    // 验证已通过 Execute 中的断言完成
    return nil
}

func (s *DiscoveryScenario) Teardown(
    ctx context.Context, 
    cluster framework.TestCluster,
) error {
    return nil
}
```

### 6.2 Storage 测试场景

```go
// internal/infrastructure/storage/test/scenarios/persistence_scenario.go

package scenarios

import (
    "context"
    "fmt"
    
    "github.com/jzhang405/NexKV/pkg/test/framework"
)

// PersistenceScenario 数据持久化测试场景
type PersistenceScenario struct {
    keyCount int
}

func NewPersistenceScenario() *PersistenceScenario {
    return &PersistenceScenario{
        keyCount: 1000,
    }
}

func (s *PersistenceScenario) Name() string {
    return "Storage.Persistence"
}

func (s *PersistenceScenario) Description() string {
    return "验证数据持久化功能，确保数据在节点重启后仍然可用"
}

func (s *PersistenceScenario) GetRequiredComponents() []framework.ComponentType {
    return []framework.ComponentType{
        framework.ComponentTypeStorage,
    }
}

func (s *PersistenceScenario) GetMinNodeCount() int {
    return 1
}

func (s *PersistenceScenario) Setup(
    ctx context.Context, 
    cluster framework.TestCluster,
) error {
    return nil
}

func (s *PersistenceScenario) Execute(
    ctx context.Context,
    cluster framework.TestCluster,
) (*framework.ScenarioResult, error) {
    // 1. 获取初始节点和组件
    node, err := cluster.GetNode(0)
    if err != nil {
        return nil, err
    }

    storageComp, err := node.GetComponent("storage")
    if err != nil {
        return nil, err
    }

    storage := storageComp.(*storage_test.StorageComponent)

    // 2. 写入数据
    testData := make(map[string]string)
    for i := 0; i < s.keyCount; i++ {
        key := fmt.Sprintf("key-%d", i)
        value := fmt.Sprintf("value-%d", i)
        testData[key] = value

        if err := storage.Put(ctx, []byte(key), []byte(value)); err != nil {
            return nil, fmt.Errorf("failed to put key %s: %w", key, err)
        }
    }

    // 3. 模拟节点重启（停止并重新启动）
    if err := cluster.RestartNode(0); err != nil {
        return nil, fmt.Errorf("failed to restart node: %w", err)
    }

    // P1 修复 (v2.6)：重启后重新获取节点和组件实例
    // 修复前：使用旧的 storage 实例，可能导致访问已关闭的资源
    // 修复后：重新获取组件实例，确保使用正确的引用
    node, err = cluster.GetNode(0)
    if err != nil {
        return nil, fmt.Errorf("failed to get node after restart: %w", err)
    }

    storageComp, err = node.GetComponent("storage")
    if err != nil {
        return nil, fmt.Errorf("failed to get storage after restart: %w", err)
    }

    storage = storageComp.(*storage_test.StorageComponent)

    // 4. 验证数据仍然存在
    for key, expectedValue := range testData {
        value, err := storage.Get(ctx, []byte(key))
        if err != nil {
            return nil, fmt.Errorf("failed to get key %s: %w", key, err)
        }
        if string(value) != expectedValue {
            return nil, fmt.Errorf("value mismatch for key %s: expected %s, got %s",
                key, expectedValue, string(value))
        }
    }

    return &framework.ScenarioResult{
        Success: true,
        Metrics: &framework.TestMetrics{
            CustomMetrics: map[string]interface{}{
                "keys_written": s.keyCount,
                "keys_verified": s.keyCount,
            },
        },
    }, nil
}

func (s *PersistenceScenario) Verify(
    ctx context.Context, 
    result *framework.ScenarioResult,
) error {
    return nil
}

func (s *PersistenceScenario) Teardown(
    ctx context.Context, 
    cluster framework.TestCluster,
) error {
    return nil
}
```

### 6.3 Replication 测试场景

```go
// internal/infrastructure/replication/test/scenarios/quorum_scenario.go

package scenarios

import (
    "context"
    "fmt"
    
    "github.com/jzhang405/NexKV/pkg/test/framework"
)

// QuorumScenario Quorum 一致性测试场景
type QuorumScenario struct {
    replicationFactor int
    quorumSize        int
}

func NewQuorumScenario() *QuorumScenario {
    return &QuorumScenario{
        replicationFactor: 3,
        quorumSize:        2,
    }
}

func (s *QuorumScenario) Name() string {
    return "Replication.QuorumConsistency"
}

func (s *QuorumScenario) Description() string {
    return "验证 Quorum 一致性，确保写入后能从多数节点读取到最新值"
}

func (s *QuorumScenario) GetRequiredComponents() []framework.ComponentType {
    return []framework.ComponentType{
        framework.ComponentTypeTransport,
        framework.ComponentTypeStorage,
        framework.ComponentTypeReplication,
    }
}

func (s *QuorumScenario) GetMinNodeCount() int {
    return 3
}

func (s *QuorumScenario) Execute(
    ctx context.Context, 
    cluster framework.TestCluster,
) (*framework.ScenarioResult, error) {
    // 获取第一个节点作为主节点
    primary, err := cluster.GetNode(0)
    if err != nil {
        return nil, err
    }
    
    replComp, err := primary.GetComponent("replication")
    if err != nil {
        return nil, err
    }
    
    replicator := replComp.(*replication_test.ReplicationComponent).GetReplicator()
    
    // 1. 写入数据（应该复制到所有副本）
    shardID := "test-shard-0"
    key := []byte("test-key")
    value := []byte("test-value")
    
    if err := replicator.Replicate(ctx, shardID, key, value); err != nil {
        return nil, fmt.Errorf("replication failed: %w", err)
    }
    
    // 2. 从所有节点读取，验证 Quorum
    successCount := 0
    nodes := cluster.GetAllNodes()
    
    for _, node := range nodes {
        repl, err := node.GetComponent("replication")
        if err != nil {
            continue
        }
        
        result, err := repl.(*replication_test.ReplicationComponent).GetQuorum(ctx, shardID, key)
        if err != nil {
            continue
        }
        
        if string(result.Value) == string(value) {
            successCount++
        }
    }
    
    // 验证：至少 quorumSize 个节点返回最新值
    if successCount < s.quorumSize {
        return nil, fmt.Errorf("quorum not satisfied: got %d, need %d", successCount, s.quorumSize)
    }
    
    return &framework.ScenarioResult{
        Success: true,
        Metrics: &framework.TestMetrics{
            CustomMetrics: map[string]interface{}{
                "replication_factor": s.replicationFactor,
                "quorum_size":        s.quorumSize,
                "success_nodes":      successCount,
            },
        },
    }, nil
}
```

---

## 七、使用示例

### 7.1 基础使用

```go
// test/transport_integration_test.go

package test

import (
    "context"
    "testing"
    
    "github.com/jzhang405/NexKV/pkg/test/framework"
    transport_scenarios "github.com/jzhang405/NexKV/internal/infrastructure/transport/test/scenarios"
)

func TestTransportIntegration(t *testing.T) {
    ctx := context.Background()
    
    // 创建 3 节点集群，只包含 Transport 组件
    cluster, err := framework.NewTestCluster(
        ctx,
        3,  // 3 个节点
        []framework.ComponentType{
            framework.ComponentTypeTransport,
        },
    )
    if err != nil {
        t.Fatalf("Failed to create cluster: %v", err)
    }
    defer cluster.Stop(ctx)
    
    // 启动集群
    if err := cluster.Start(ctx); err != nil {
        t.Fatalf("Failed to start cluster: %v", err)
    }
    
    // 创建场景执行器
    executor := framework.NewScenarioExecutor(cluster)
    
    // 注册测试场景
    executor.Register(transport_scenarios.NewDiscoveryScenario())
    executor.Register(transport_scenarios.NewConnectivityScenario())
    executor.Register(transport_scenarios.NewPerformanceScenario())
    
    // 执行所有场景
    results, err := executor.Run(ctx)
    if err != nil {
        t.Fatalf("Scenario execution failed: %v", err)
    }
    
    // 验证结果
    for _, result := range results {
        if !result.Success {
            t.Errorf("Scenario %s failed: %v", result.Name, result.Error)
        }
    }
}
```

### 7.2 多组件组合测试

```go
// test/replication_integration_test.go

package test

import (
    "context"
    "testing"
    
    "github.com/jzhang405/NexKV/pkg/test/framework"
    repl_scenarios "github.com/jzhang405/NexKV/internal/infrastructure/replication/test/scenarios"
)

func TestReplicationIntegration(t *testing.T) {
    ctx := context.Background()
    
    // 创建 5 节点集群，包含 Transport + Storage + Replication
    cluster, err := framework.NewTestCluster(
        ctx,
        5,  // 5 个节点
        []framework.ComponentType{
            framework.ComponentTypeTransport,
            framework.ComponentTypeStorage,
            framework.ComponentTypeReplication,
        },
    )
    if err != nil {
        t.Fatalf("Failed to create cluster: %v", err)
    }
    defer cluster.Stop(ctx)
    
    // 启动集群
    if err := cluster.Start(ctx); err != nil {
        t.Fatalf("Failed to start cluster: %v", err)
    }
    
    // 等待集群就绪
    if err := cluster.WaitForReady(ctx, 30*time.Second); err != nil {
        t.Fatalf("Cluster not ready: %v", err)
    }
    
    // 创建场景执行器
    executor := framework.NewScenarioExecutor(cluster)
    
    // 注册 Replication 测试场景
    executor.Register(repl_scenarios.NewQuorumScenario())
    executor.Register(repl_scenarios.NewFailoverScenario())
    executor.Register(repl_scenarios.NewConflictResolutionScenario())
    
    // 执行测试
    results, err := executor.Run(ctx)
    if err != nil {
        t.Fatalf("Scenario execution failed: %v", err)
    }
    
    // 打印结果
    for _, result := range results {
        t.Logf("Scenario: %s, Success: %v, Duration: %v",
            result.Name, result.Success, result.Duration)
    }
}
```

### 7.3 故障注入测试

```go
// test/chaos_test.go

package test

import (
    "context"
    "testing"
    "time"
    
    "github.com/jzhang405/NexKV/pkg/test/framework"
)

func TestPartitionTolerance(t *testing.T) {
    ctx := context.Background()
    
    // 创建 5 节点集群
    cluster, err := framework.NewTestCluster(ctx, 5, []framework.ComponentType{
        framework.ComponentTypeTransport,
        framework.ComponentTypeStorage,
        framework.ComponentTypeReplication,
    })
    if err != nil {
        t.Fatal(err)
    }
    defer cluster.Stop(ctx)
    
    if err := cluster.Start(ctx); err != nil {
        t.Fatal(err)
    }
    
    // 模拟网络分区：将节点 0,1 与 2,3,4 分开
    if err := cluster.Partition("partition-1", []int{0, 1}); err != nil {
        t.Fatal(err)
    }
    
    // 在分区 2,3,4 上执行操作（应该仍然可用，因为有 Quorum）
    node2, _ := cluster.GetNode(2)
    // ... 执行操作
    
    // 恢复网络分区
    if err := cluster.HealPartition("partition-1"); err != nil {
        t.Fatal(err)
    }
    
    // 验证数据一致性
    // ...
}

func TestNodeFailure(t *testing.T) {
    ctx := context.Background()
    
    cluster, err := framework.NewTestCluster(ctx, 3, []framework.ComponentType{
        framework.ComponentTypeTransport,
        framework.ComponentTypeReplication,
    })
    if err != nil {
        t.Fatal(err)
    }
    defer cluster.Stop(ctx)
    
    if err := cluster.Start(ctx); err != nil {
        t.Fatal(err)
    }
    
    // 模拟节点 1 宕机
    if err := cluster.KillNode(1); err != nil {
        t.Fatal(err)
    }
    
    // 验证系统仍然可用（2/3 节点存活）
    // ...
    
    // 重启节点 1
    if err := cluster.RestartNode(1); err != nil {
        t.Fatal(err)
    }
    
    // 验证节点 1 恢复后数据同步
    // ...
}
```

---

## 八、并发控制与 Goroutine 池

### 8.1 为什么需要 Goroutine 池

在集成测试框架的性能测试场景中，需要并发执行大量 RPC 调用。无限制创建 goroutine 会导致：

| 问题 | 影响 |
|------|------|
| 内存耗尽 | 每个 goroutine 栈 ~8KB，100万 goroutine ≈ 8GB |
| 调度器压力 | Go 调度器需要管理大量 goroutine |
| 系统不稳定 | 可能导致 OOM 或响应变慢 |

### 8.2 选型对比

| 特性 | ants | tunny | workerpool | pond |
|------|------|-------|------------|------|
| **GitHub Stars** | 13k+ | 3.5k | 400 | 500 |
| **维护状态** | ✅ 活跃 | ⚠️ 较少 | ⚠️ 较少 | ✅ 活跃 |
| **性能** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ |
| **易用性** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| **动态扩缩容** | ✅ | ❌ | ❌ | ✅ |
| **任务优先级** | ✅ | ❌ | ❌ | ❌ |
| **超时控制** | ✅ | ✅ | ❌ | ✅ |
| **生产验证** | ✅ 字节跳动 | ✅ | ⚠️ | ⚠️ |

### 8.3 首选推荐：ants

> **ants** 是目前 Go 生态中最流行的 goroutine 池库，由**字节跳动**开源。

**核心特点**：
- 🚀 **高性能**：比原生 goroutine 快 10x（大量短任务场景）
- 🎯 **易用**：API 简洁，5 分钟上手
- 🔧 **功能全**：动态扩缩容、优先级、超时、取消
- 📊 **可观测**：内置指标监控
- 🏭 **生产验证**：字节跳动内部广泛使用

**安装**：
```bash
go get -u github.com/panjf2000/ants/v2
```

### 8.4 基础用法

#### 8.4.1 简单任务池

```go
package main

import (
    "fmt"
    "time"

    "github.com/panjf2000/ants/v2"
)

func main() {
    // 创建固定大小的池（100 workers）
    pool, err := ants.NewPool(100)
    if err != nil {
        panic(err)
    }
    defer pool.Release() // 程序结束时释放资源

    // 提交任务
    for i := 0; i < 1000; i++ {
        taskID := i
        err := pool.Submit(func() {
            fmt.Printf("Task %d executed\n", taskID)
            time.Sleep(100 * time.Millisecond)
        })
        if err != nil {
            fmt.Printf("Submit failed: %v\n", err)
        }
    }

    // 等待所有任务完成
    fmt.Printf("Running workers: %d\n", pool.Running())
}
```

#### 8.4.2 带参数的任务函数

```go
package main

import (
    "fmt"
    "sync"

    "github.com/panjf2000/ants/v2"
)

func main() {
    var wg sync.WaitGroup

    // 创建带参数的任务池
    pool, _ := ants.NewPoolWithFunc(10, func(i interface{}) {
        taskID := i.(int)
        fmt.Printf("Processing task %d\n", taskID)
        wg.Done()
    })
    defer pool.Release()

    // 提交任务
    for i := 0; i < 100; i++ {
        wg.Add(1)
        _ = pool.Invoke(i)
    }

    wg.Wait()
    fmt.Println("All tasks completed")
}
```

### 8.5 性能测试集成示例

```go
// pkg/test/framework/benchmark.go

package framework

import (
    "context"
    "sync"

    "github.com/panjf2000/ants/v2"
)

// RPCLoadTester RPC 负载测试器
type RPCLoadTester struct {
    pool   *ants.PoolWithFunc
    config *BenchmarkConfig
}

// BenchmarkConfig 性能测试配置
type BenchmarkConfig struct {
    WorkerCount    int           // worker 数量
    TaskQueueSize  int           // 任务队列大小
    WarmupDuration time.Duration // 预热时间
}

// RPCTask RPC 测试任务
type RPCTask struct {
    ctx      context.Context
    node     TestNode
    payload  []byte
    wg       *sync.WaitGroup
}

// Execute 执行 RPC 任务
func (t *RPCTask) Execute() {
    _, _ = t.node.SendRPC(t.ctx, "benchmark", t.payload)
}

func NewRPCLoadTester(config *BenchmarkConfig) (*RPCLoadTester, error) {
    tester := &RPCLoadTester{config: config}

    pool, err := ants.NewPoolWithFunc(config.WorkerCount, func(i interface{}) {
        task := i.(*RPCTask)
        task.Execute()
        task.wg.Done()
    }, ants.WithPreAlloc(true))
    if err != nil {
        return nil, err
    }

    tester.pool = pool
    return tester, nil
}

// Run 执行负载测试
func (t *RPCLoadTester) Run(ctx context.Context, tasks []*RPCTask) {
    var wg sync.WaitGroup

    for _, task := range tasks {
        select {
        case <-ctx.Done():
            return
        default:
            wg.Add(1)
            task.wg = &wg
            _ = t.pool.Invoke(task)
        }
    }

    wg.Wait()
}

// Close 释放资源
func (t *RPCLoadTester) Close() {
    t.pool.Release()
}
```

### 8.6 配置选项

```go
pool, err := ants.NewPoolWithFunc(100, func(i interface{}) {
    // 任务处理逻辑
},
    // 核心配置
    ants.WithPreAlloc(true),                    // 预分配 goroutine（减少动态分配开销）
    ants.WithNonblocking(true),                 // 非阻塞模式（任务队列满时不阻塞）
    ants.WithPanicHandler(func(err interface{}) { // panic 恢复
        // 处理 panic
    }),
    ants.WithExpiryDuration(10*time.Second),    // 空闲 worker 过期时间
    ants.WithMaxBlockingTasks(1000),            // 最大阻塞任务数
)
```

### 8.7 池大小选择建议

```go
import "runtime"

// CPU 密集型任务
cpuBoundPoolSize := runtime.NumCPU()

// I/O 密集型任务（RPC 调用属于此类）
ioBoundPoolSize := runtime.NumCPU() * 10

// 混合型任务
mixedPoolSize := runtime.NumCPU() * 5
```

### 8.8 最佳实践

| 实践 | 说明 |
|------|------|
| **使用 defer 释放** | `defer pool.Release()` 确保资源释放 |
| **PanicHandler** | 配置 panic 处理，避免 worker 退出 |
| **背压控制** | 使用 `ants.WithMaxBlockingTasks` 限制排队 |
| **监控指标** | 定期检查 `pool.Running()` 和 `pool.Waiting()` |

### 8.9 NexKV 使用计划

| 阶段 | 场景 | 配置 |
|------|------|------|
| Phase 1 | 集成测试性能测试 | 100 workers, 非阻塞 |
| Phase 2 | RPC 并发控制 | 动态扩缩容, 背压 |
| Phase 3 | 后台任务处理 | 优先级队列 |

> **详细文档**: `docs/07_spike/2026-02-20_goroutine-pool-library-recommendation.md`

---

## 九、目录结构

> **推荐结构**: 与现有 `test/e2e/` 物理分离，职责清晰

```
test/                               # 测试根目录
├── e2e/                            # 现有：端到端测试（保持不变）
│   ├── suite.go                    # E2E 测试套件
│   ├── cluster.go                  # E2E 集群管理
│   └── ...                         # 其他 E2E 测试文件
│
└── integrate/                      # 新建：组件级集成测试
    ├── suite.go                    # 集成测试套件基础设施
    │
    ├── adapter/                    # 组件适配器
    │   ├── transport.go            # Transport 适配器
    │   ├── storage.go              # Storage 适配器
    │   ├── replication.go          # Replication 适配器
    │   └── cluster.go              # Cluster 适配器
    │
    ├── scenario/                   # 测试场景
    │   ├── discovery.go            # 节点发现场景
    │   ├── connectivity.go         # 连通性场景
    │   ├── performance.go          # 性能基准场景
    │   ├── persistence.go          # 持久化场景
    │   ├── quorum.go               # Quorum 一致性场景
    │   └── failover.go             # 故障转移场景
    │
    └── example/                    # 示例测试（Phase 1 POC）
        └── transport_poc_test.go   # Transport POC 测试

pkg/test/                           # 框架公共库（被 integrate/ 依赖）
├── framework/                      # 通用测试框架核心
│   ├── test_environment.go         # TestEnvironment 接口
│   ├── cluster.go                  # TestCluster 接口
│   ├── component.go                # TestComponent 接口
│   ├── registry.go                 # ComponentRegistry
│   ├── scenario.go                 # TestScenario 接口
│   ├── metrics.go                  # TestMetrics
│   ├── cluster_impl.go             # DefaultTestCluster 实现
│   ├── port_allocator.go           # 端口分配器
│   ├── data_generator.go           # 数据生成器
│   └── helpers.go                  # 辅助函数
│
└── assertions/                     # 断言库
    └── cluster_assertions.go       # 集群相关断言
```

### 8.1 结构说明

| 目录 | 用途 | 依赖关系 |
|------|------|----------|
| `test/e2e/` | 端到端测试（现有） | 独立运行 |
| `test/integration/` | 组件级集成测试（新建） | 依赖 `pkg/test/framework/` |
| `pkg/test/framework/` | 框架公共库 | 被 `test/integration/` 依赖 |

### 8.2 职责分离

```
┌─────────────────────────────────────────────────────────────┐
│                     Test Layer                              │
├──────────────────────┬──────────────────────────────────────┤
│    test/e2e/         │        test/integration/             │
│    (现有)            │        (新建)                        │
├──────────────────────┼──────────────────────────────────────┤
│ • 完整系统测试       │ • 组件级集成测试                     │
│ • 用户场景验证       │ • 组件交互验证                       │
│ • API 契约测试       │ • 故障注入测试                       │
│ • 外部接口测试       │ • 性能基准测试                       │
├──────────────────────┴──────────────────────────────────────┤
│              pkg/test/framework/                            │
│              (公共框架库)                                    │
├─────────────────────────────────────────────────────────────┤
│ • TestCluster 接口                                          │
│ • TestComponent 接口                                        │
│ • 数据生成器                                                │
│ • 指标收集                                                  │
└─────────────────────────────────────────────────────────────┘
```

### 8.3 实现优先级

**Phase 1 (当前)**:
```
test/integration/
├── suite.go                    # 基础套件
├── adapter/
│   └── transport.go            # Transport 适配器（必需）
├── scenario/
│   ├── discovery.go            # 节点发现
│   └── connectivity.go         # 连通性
└── example/
    └── transport_poc_test.go   # POC 测试
```

**Phase 2-4**: 逐步添加 Storage、Replication、Cluster 适配器和场景

---

## 十一、总结

### 11.1 框架价值

| 价值点 | 说明 |
|--------|------|
| **通用性** | 支持 Transport、Storage、Replication、Cluster 等多个组件 |
| **可组合** | 可以灵活组合不同组件进行集成测试 |
| **可扩展** | 通过 ComponentRegistry 轻松添加新组件 |
| **真实模拟** | 在单台机器上模拟多节点集群环境 |
| **故障注入** | 支持网络分区、节点宕机等故障场景 |
| **自动化** | 支持 CI/CD 集成，自动执行测试 |
| **数据生成** | 支持多种分布类型的测试数据生成 |
| **指标收集** | 全面的分布式系统指标收集能力 |
| **测试隔离** | 支持测试上下文隔离，避免并行测试冲突 |

### 11.2 关键设计决策

#### 11.2.1 类型安全的依赖管理
- 使用 `ComponentType` 而非字符串声明依赖
- 框架自动检测循环依赖并报错
- 拓扑排序确保组件按正确顺序启动

#### 11.2.2 测试隔离机制
- `TestContext` 提供独立的注册表和随机种子
- 每个测试用例可以创建独立的上下文
- 避免全局状态导致的测试冲突

#### 11.2.3 网络分区模拟
- `NetworkPartitionController` 提供完整的分区控制
- 支持消息阻塞、延迟注入、丢包模拟
- 可追踪被阻塞和丢弃的消息

#### 11.2.4 数据分布支持
- 均匀分布、Zipfian 分布、热点分布等多种模式
- 支持固定随机种子，确保测试可重复
- 适用于不同场景的性能测试

### 11.3 扩展指南

添加新组件的步骤：

1. **实现 TestComponent 接口**
   ```go
   type MyComponent struct { ... }
   func (c *MyComponent) Init(ctx context.Context, env framework.TestEnvironment) error
   func (c *MyComponent) Start(ctx context.Context) error
   func (c *MyComponent) Stop(ctx context.Context) error
   func (c *MyComponent) GetDependencies() []framework.ComponentType  // 类型安全
   ```

2. **注册组件工厂**（P0 修复 v2.6：使用 TestContext.Registry）
   ```go
   // 旧代码（已废弃）：
   // func init() {
   //     framework.RegisterComponent(framework.ComponentTypeMyComponent, NewMyComponent)
   // }

   // 新代码：在测试函数中注册
   func TestMyComponent(t *testing.T) {
       testCtx := framework.NewTestContext()

       // 使用 TestContext 的注册表
       testCtx.Registry.Register(framework.ComponentTypeMyComponent, NewMyComponent)

       // 继续测试...
   }
   ```

3. **创建测试场景**
   ```go
   type MyScenario struct { ... }
   func (s *MyScenario) Execute(ctx context.Context, cluster framework.TestCluster) (*framework.ScenarioResult, error)
   ```

4. **编写集成测试（使用 TestContext）**
   ```go
   func TestMyComponent(t *testing.T) {
       // 创建独立的测试上下文
       testCtx := framework.NewTestContextWithSeed(12345)
       
       cluster, _ := framework.NewTestClusterWithContext(ctx, testCtx, 3, []framework.ComponentType{
           framework.ComponentTypeTransport,
           framework.ComponentTypeMyComponent,
       })
       // ...
   }
   ```

### 11.4 使用建议

#### 11.4.1 并行测试
```go
func TestParallel(t *testing.T) {
    t.Parallel()
    
    // 每个并行测试使用独立的 TestContext
    testCtx := framework.NewTestContext()
    cluster, _ := framework.NewTestClusterWithContext(ctx, testCtx, ...)
    // ...
}
```

#### 11.4.2 可重复测试
```go
func TestReproducible(t *testing.T) {
    // 使用固定种子确保测试可重复
    testCtx := framework.NewTestContextWithSeed(42)
    
    // 数据生成器也使用相同种子
    dataGen := framework.NewDataGeneratorWithSeed(42)
    
    cluster, _ := framework.NewTestClusterWithContext(ctx, testCtx, ...)
    // ...
}
```

#### 11.4.3 故障注入测试
```go
func TestNetworkPartition(t *testing.T) {
    cluster, _ := framework.NewTestCluster(ctx, 5, ...)
    
    // 创建高级分区控制器
    controller, _ := framework.NewNetworkPartitionController(cluster, "p1", []int{0, 1})
    controller.SetPacketLossRate(0.3)  // 30% 丢包率
    controller.SetLatencyInjection(100 * time.Millisecond)
    
    controller.Apply()
    defer controller.Heal()
    
    // 执行测试...
}
```

---

## 十二、分阶段实现计划

### 12.1 Phase 1: Transport POC（当前目标）

**目标**: 验证框架基础能力，实现 Transport 层集成测试

```
Phase 1: Transport POC
├── Week 1-2: 框架基础
│   ├── pkg/test/framework/ 核心接口实现
│   │   ├── test_environment.go
│   │   ├── cluster.go
│   │   ├── component.go
│   │   └── registry.go
│   ├── DefaultTestCluster 实现
│   └── 端口分配器、临时目录管理
│
├── Week 3-4: Transport Adapter
│   ├── internal/infrastructure/transport/test/
│   │   ├── transport_adapter.go
│   │   └── scenarios/
│   │       ├── discovery_scenario.go      # mDNS 节点发现
│   │       ├── connectivity_scenario.go   # 连接/断开/重连
│   │       └── performance_scenario.go    # 吞吐量/延迟基准
│   └── test/transport_integration_test.go
│
└── Week 5: 验证与文档
    ├── 3节点集群搭建测试
    ├── RPC 通信测试
    ├── 网络分区可行性验证 ⚠️
    └── 性能基准测试
```

**Phase 1 Week-by-Week Checkpoint**:

| Week | 任务 | 负责人 | 交付物 | 验收标准 |
|------|------|--------|--------|----------|
| **Week 1** | 框架基础搭建 | 开发团队 | `pkg/test/framework/` 核心接口 | 编译通过，单元测试覆盖 > 80% |
| **Week 2** | Transport Adapter 实现 | 开发团队 | `internal/infrastructure/transport/test/` | 单节点启动/停止测试通过 |
| **Week 3** | 网络分区验证 ⚠️ | 开发团队 | 可行性验证报告 | 确认 libp2p 方案 A/B/C 可行性 |
| **Week 4** | 3节点集群测试 | 开发团队 | `test/integration/transport_poc_test.go` | 3节点发现 + RPC 测试通过 |
| **Week 5** | 性能基准测试 | 开发团队 | 性能基准报告 | 延迟 < 10ms，吞吐量 > 1000 ops/s |

**决策点**: Week 3 结束时必须确认网络分区方案，如不可行则调整架构。

---

### 12.2 Phase 2: 扩展到 Storage

**目标**: 验证数据持久化能力，实现 Storage 层集成测试

```
Phase 2: Storage Integration
├── Week 6-7: Storage Adapter
│   ├── internal/infrastructure/storage/test/
│   │   ├── storage_adapter.go
│   │   └── scenarios/
│   │       ├── persistence_scenario.go    # 数据持久化
│   │       ├── wal_scenario.go            # WAL 恢复
│   │       └── iterator_scenario.go       # 迭代器测试
│   └── test/storage_integration_test.go
│
├── Week 8: 组合测试
│   ├── Transport + Storage 组合测试
│   ├── 数据复制基础测试
│   └── 故障恢复测试（节点重启）
│
└── Week 9: 性能基准
    ├── 写入吞吐量测试
    ├── 读取延迟测试
    └── WAL 性能测试
```

**Phase 2 成功标准**:
- [ ] 数据持久化测试通过率 100%
- [ ] 节点重启后数据不丢失
- [ ] 写入吞吐量 > 10K ops/sec（单节点）

---

### 12.3 Phase 3: 扩展到 Replication

**目标**: 验证分布式一致性，实现 Replication 层集成测试

```
Phase 3: Replication Integration
├── Week 10-11: Replication Adapter
│   ├── internal/infrastructure/replication/test/
│   │   ├── replication_adapter.go
│   │   └── scenarios/
│   │       ├── quorum_scenario.go         # Quorum 一致性
│   │       ├── failover_scenario.go       # 故障转移
│   │       └── conflict_scenario.go       # 冲突解决
│   └── test/replication_integration_test.go
│
├── Week 12: 故障注入测试
│   ├── 网络分区测试
│   ├── 节点宕机/重启测试
│   ├── 脑裂场景测试
│   └── 数据一致性验证
│
└── Week 13: 混沌测试
    ├── 随机故障注入
    ├── 长时间稳定性测试
    └── 压力测试
```

**Phase 3 成功标准**:
- [ ] Quorum 写入成功率 > 99%
- [ ] 故障转移时间 < 5 秒
- [ ] 数据一致性 100%

---

### 12.4 Phase 4: 完整框架（可选）

**目标**: 扩展到 Cluster 层，实现完整集成测试框架

```
Phase 4: Full Framework
├── Week 14-15: Cluster Adapter
│   ├── internal/infrastructure/cluster/test/
│   │   ├── cluster_adapter.go
│   │   └── scenarios/
│   │       ├── membership_scenario.go     # 成员管理
│   │       └── shard_migration_scenario.go # 分片迁移
│
├── Week 16: 多组件组合测试
│   ├── Transport + Storage + Replication + Cluster
│   ├── 端到端场景测试
│   └── 性能回归测试
│
└── Week 17: CI/CD 集成
    ├── GitHub Actions 集成
    ├── 测试报告生成
    └── 覆盖率统计
```

---

### 12.5 关键里程碑

| 里程碑 | 日期 | 交付物 | 验收标准 |
|--------|------|--------|----------|
| **M1** | Week 5 | Transport POC | 3节点集群 + RPC测试通过 |
| **M2** | Week 9 | Storage 集成 | 数据持久化测试通过 |
| **M3** | Week 13 | Replication 集成 | 故障转移测试通过 |
| **M4** | Week 17 | 完整框架 | CI/CD 集成完成 |

---

### 12.6 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| **libp2p 网络分区模拟不可行** | 高 | Phase 1 优先验证；如不可行，改用防火墙规则模拟 |
| **组件启动时间过长** | 中 | 优化启动顺序；增加并行启动支持 |
| **测试不稳定** | 中 | 增加重试机制；使用固定随机种子 |
| **资源泄漏** | 中 | 强制清理机制；defer + signal handler |

---

### 12.7 性能测试环境规格

为确保性能指标可复现，所有性能测试必须在以下规格环境中执行：

#### 12.7.1 硬件规格

| 资源 | 规格 | 说明 |
|------|------|------|
| **CPU** | 4 cores | Intel/AMD x86_64，2.5GHz+ |
| **Memory** | 8GB RAM | 可用内存 >= 6GB |
| **Disk** | SSD | 读写速度 >= 500MB/s |
| **Network** | localhost (loopback) | 127.0.0.1，无网络延迟 |

#### 12.7.2 软件环境

| 组件 | 版本 | 说明 |
|------|------|------|
| **OS** | Linux/macOS | Kernel 5.0+ / macOS 12+ |
| **Go** | 1.21+ | 最新稳定版 |
| **libp2p** | 与生产环境一致 | 使用 go.mod 指定版本 |

#### 12.7.3 测试负载定义

| 指标 | 定义 | 测量方法 |
|------|------|----------|
| **并发连接** | 100 并发 | 使用 goroutine 池（见下方说明） |
| **测试数据量** | 10K keys | 每个测试迭代 10,000 个键值对 |
| **Key 大小** | 32 bytes | 固定长度，模拟 UUID |
| **Value 大小** | 1KB - 100KB (随机) | 模拟真实数据负载 |
| **预热时间** | 30s | 正式测量前运行 30 秒预热 |
| **测量时长** | 60s | 持续运行 60 秒取平均值 |

**P1 修复 (v2.6)：并发控制细节**

| 指标 | 定义 | 说明 |
|------|------|------|
| **并发模型** | goroutine 池 | 使用固定 worker 池，避免无限创建 goroutine |
| **并发控制** | 100 workers | 固定 100 个 worker，通过 channel 分发任务 |
| **连接复用** | 启用 | 每个 worker 复用单个 RPC 连接，避免连接开销 |
| **资源限制** | Goroutine 栈 8KB | 使用 `debug.SetMaxThreads` 限制总线程数 |
| **背压机制** | Channel buffer 1000 | 任务队列满时阻塞生产者 |

**并发测试示例**:
```go
func BenchmarkRPCThroughput(b *testing.B) {
    ctx := context.Background()
    cluster, _ := framework.NewTestCluster(ctx, 3, []framework.ComponentType{
        framework.ComponentTypeTransport,
    })
    defer cluster.Stop(ctx)
    cluster.Start(ctx)

    // 创建任务 channel（带背压）
    taskCh := make(chan *RPCTask, 1000)
    var wg sync.WaitGroup

    // 启动 100 个 worker
    for i := 0; i < 100; i++ {
        go func(workerID int) {
            node, _ := cluster.GetNode(workerID % 3)
            for task := range taskCh {
                task.node.SendRPC(task.ctx, "test", []byte("payload"))
                task.wg.Done()
            }
        }(i)
    }

    // 预热
    warmup(ctx, cluster, 30*time.Second)

    // 正式测量
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        var wg sync.WaitGroup
        for pb.Next() {
            wg.Add(1)
            taskCh <- &RPCTask{
                ctx:  ctx,
                node: cluster.GetNode(0),
                wg:   &wg,
            }
        }
        wg.Wait()
    })

    close(taskCh)
}
```

**数据规模估算**:
- 最小负载: 10K keys × (32B key + 1KB value) = ~10 MB
- 典型负载: 10K keys × (32B key + 10KB value) = ~100 MB
- 最大负载: 10K keys × (32B key + 100KB value) = ~1 GB
- 内存占用: 约数据规模的 2-3 倍（含索引和缓冲）
- 磁盘占用: 约数据规模的 1.5 倍（含 WAL 和压缩）

#### 12.7.4 性能指标基线

| 阶段 | 指标 | 目标值 | 测量工具 |
|------|------|--------|----------|
| **Phase 1** | RPC 延迟 | < 10ms (p99) | `go test -bench` + custom metrics |
| **Phase 1** | RPC 吞吐量 | > 1000 ops/s | `go test -bench` |
| **Phase 2** | 写入吞吐量 | > 10K ops/s | `go test -bench` |
| **Phase 2** | 读取延迟 | < 5ms (p99) | `go test -bench` |
| **Phase 3** | Quorum 写入 | > 99% 成功率 | 场景测试统计 |
| **Phase 3** | 故障转移 | < 5s | 场景测试计时 |

**对比基准说明**:
- **延迟基准**: vs localhost TCP（典型 ~0.5ms）+ protobuf 序列化（~0.1ms）+ 业务逻辑（~1ms）= ~2ms
- **吞吐量基准**: vs 裸机 localhost HTTP/1.1（典型 ~10K ops/s）
- **目标设定**: 考虑 libp2p 开销（加密、多路复用），目标为裸机基准的 5-10 倍延迟，10% 吞吐量
- **测试环境**: 单机 loopback，无网络延迟，排除外部因素干扰

#### 12.7.5 测量方法

```go
// 性能测试示例
func BenchmarkRPCThroughput(b *testing.B) {
    ctx := context.Background()
    cluster, _ := framework.NewTestCluster(ctx, 3, []framework.ComponentType{
        framework.ComponentTypeTransport,
    })
    defer cluster.Stop(ctx)
    cluster.Start(ctx)

    // 预热
    warmup(ctx, cluster, 30*time.Second)

    // 正式测量
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            // 执行 RPC 调用
            node, _ := cluster.GetNode(0)
            node.SendRPC(ctx, "test", []byte("payload"))
        }
    })
}
```

---

### 12.8 Phase 1 Week 3 验证清单

在继续开发前，必须完成以下验证：

| 验证项 | 方法 | 通过标准 | 备选方案 |
|--------|------|----------|----------|
| **libp2p 连接控制** | `host.Network().ClosePeer(peerID)` | 能断开并重新连接 | 方案 B: 防火墙规则 |
| **消息拦截** | `network.SetStreamHandler()` + 自定义逻辑 | 能拦截、延迟、丢弃消息 | 方案 C: Transport 包装器 |
| **节点发现** | mDNS 或 DHT | 3节点在 5s 内互相发现 | 使用静态 bootstrap 列表 |
| **RPC 通信** | 自定义协议 | 延迟 < 10ms，吞吐量 > 1000 ops/s | 优化序列化/反序列化 |

**决策点**：Week 3 结束时评审，如验证失败则切换到备选方案。

---

## 十三、网络分区可行性验证

### 13.1 验证目标

在 **Phase 1** 中必须验证：libp2p 是否支持在单进程中模拟网络分区。

### 13.2 验证方案

#### 方案 A: libp2p 连接控制（首选）

```go
// 验证 libp2p 是否支持断开/恢复连接
func TestLibp2pPartitionSimulation(t *testing.T) {
    ctx := context.Background()
    
    // 创建两个 libp2p 节点
    node1, _ := libp2p.New(ctx)
    node2, _ := libp2p.New(ctx)
    
    // 建立连接
    node1.Connect(ctx, node2.Network().ListenAddresses()[0])
    
    // 验证连接建立
    require.Equal(t, 1, len(node1.Network().Peers()))
    
    // 尝试断开连接
    err := node1.Network().ClosePeer(node2.ID())
    require.NoError(t, err)
    
    // 验证连接断开
    require.Equal(t, 0, len(node1.Network().Peers()))
    
    // 验证可以重新连接
    err = node1.Connect(ctx, node2.Network().ListenAddresses()[0])
    require.NoError(t, err)
}
```

**预期结果**: 如果测试通过，说明可以通过控制 libp2p 连接来模拟网络分区。

#### 方案 B: 防火墙规则（备选）

如果 libp2p 不支持运行时连接控制，使用操作系统防火墙：

```go
// 使用 iptables/nftables 模拟网络分区
func TestFirewallPartition(t *testing.T) {
    // 获取节点端口
    port := extractPort(node.Address())
    
    // 添加防火墙规则阻断特定端口
    exec.Command("iptables", "-A", "INPUT", "-p", "tcp", 
        "--dport", strconv.Itoa(port), "-j", "DROP").Run()
    
    // 执行测试...
    
    // 清理防火墙规则
    defer exec.Command("iptables", "-D", "INPUT", "-p", "tcp",
        "--dport", strconv.Itoa(port), "-j", "DROP").Run()
}
```

#### 方案 C: 自定义 Transport 包装器（最终备选）

```go
// 创建支持消息拦截的 Transport 包装器
type PartitionAwareTransport struct {
    inner      service.Transport
    partition  *NetworkPartitionController
}

func (t *PartitionAwareTransport) Send(to string, msg []byte) error {
    // 检查是否被分区阻断
    if t.partition.IsBlocked(t.inner.ID(), to) {
        return fmt.Errorf("message blocked by partition")
    }
    return t.inner.Send(to, msg)
}
```

### 13.3 验证时间表

| 周次 | 任务 | 负责人 | 输出 |
|------|------|--------|------|
| Week 3 | 方案 A 验证 | 开发团队 | 可行性报告 |
| Week 4 | 如需要，实施方案 B/C | 开发团队 | 备选方案实现 |
| Week 5 | 集成到框架 | 开发团队 | 网络分区功能 |

### 13.4 决策点

**Week 5 评审会议**:
- 如果方案 A 可行 → 继续使用 libp2p 原生控制
- 如果方案 A 不可行但 B 可行 → 切换到防火墙方案
- 如果都不可行 → 使用自定义 Transport 包装器

---

## 十四、架构审查与改进建议

> 本节汇总架构审查反馈，记录已知问题和改进方向。

### 14.1 架构亮点 ✅

#### 14.1.1 清晰的职责分离

```
TestOrchestrator
    ├── ComponentRegistry (组件注册与依赖解析)
    ├── TestCluster Manager (多节点生命周期)
    └── Scenario Executor (测试场景编排)
```

**评价**: 架构分层合理，每个模块职责单一，符合 Go 语言项目的一般惯例。

#### 14.1.2 类型安全的依赖管理

```go
// 使用 ComponentType 而非字符串
GetDependencies() []ComponentType  // 类型安全

// 实际实现
func (c *ReplicationComponent) GetDependencies() []framework.ComponentType {
    return []framework.ComponentType{
        framework.ComponentTypeTransport,
        framework.ComponentTypeStorage,
    }
}
```

**评价**: ✅ 避免了字符串硬编码，编译时即可发现依赖错误。

#### 14.1.3 拓扑排序算法

```go
// Kahn 算法实现
func (r *ComponentRegistry) TopologicalSort(components []ComponentType) ([]ComponentType, error)
```

**评价**: ✅ 实现正确，且包含详细的循环检测 `findCyclePath`。

#### 14.1.4 健康检查配置

```go
type HealthCheckConfig struct {
    Timeout       time.Duration  // 默认 5s
    Interval      time.Duration  // 默认 10s
    RetryCount    int            // 默认 3
    RetryInterval time.Duration  // 默认 1s
    Critical      bool           // 默认 true
}
```

**评价**: ✅ 覆盖了关键配置项，且提供了默认值。

---

### 14.2 需要改进的地方 ⚠️

#### 14.2.1 并发安全问题

| 位置 | 问题 | 风险等级 |
|------|------|----------|
| `GlobalRegistry` | `var GlobalRegistry = NewComponentRegistry()` 全局单例在并行测试中有竞争条件 | 🔴 **高** |
| `startNodeComponents` | 重试逻辑没有原子性保护 | 🟡 **中** |

**问题代码**:
```go
// 问题：全局单例在并行测试中竞争
var GlobalRegistry = NewComponentRegistry()

// 并行测试时，两个测试可能同时修改 factories map
// 建议：改用 sync.Mutex 保护，或完全依赖 TestContext.Registry
```

**改进建议**:
```go
// 方案 1: 使用 sync.Once 和 sync.RWMutex
type ComponentRegistry struct {
    factories map[ComponentType]ComponentFactory
    mu        sync.RWMutex
}

var (
    globalRegistry     *ComponentRegistry
    globalRegistryOnce sync.Once
)

func GetGlobalRegistry() *ComponentRegistry {
    globalRegistryOnce.Do(func() {
        globalRegistry = NewComponentRegistry()
    })
    return globalRegistry
}

// 方案 2: 完全依赖 TestContext.Registry（推荐）
// 废弃 GlobalRegistry，强制使用 TestContext 获取隔离的 Registry
```

#### 14.2.2 资源泄漏风险

| 位置 | 问题 | 建议 |
|------|------|------|
| `NetworkPartitionController` | 延迟发送 goroutine 没有 context 控制 | 使用 `context.WithCancel` |
| `cleanup()` | 使用 `go func()` 但没有显式的 goroutine join 机制 | 改为同步执行或使用 errgroup |

**问题代码**:
```go
// 问题：延迟发送 goroutine 没有取消机制
go func() {
    time.Sleep(pc.latencyInjection)
    // 延迟后尝试重新发送 - 没有取消机制
    transport.Send(msg)
}()
```

**改进建议**:
```go
// 改进：支持取消的延迟发送
func (pc *NetworkPartitionController) scheduleDelayedSend(
    ctx context.Context,
    transport service.Transport,
    msg *Message,
    delay time.Duration,
) {
    go func() {
        select {
        case <-time.After(delay):
            // 发送消息
            if err := transport.Send(msg); err != nil {
                pc.stats.MessagesDropped++
            }
        case <-ctx.Done():
            // 可取消
            pc.stats.MessagesDropped++
            return
        }
    }()
}
```

#### 14.2.3 错误处理不一致

| 位置 | 问题 |
|------|------|
| `Run` 方法 | 遇到第一个失败就停止，但注释说"执行所有" |
| 重试日志 | 使用 `fmt.Printf` 而非日志框架 |

**问题代码**:
```go
// 问题：遇到第一个失败就停止
for _, scenario := range e.scenarios {
    result, err := e.runScenario(ctx, scenario)
    if err != nil {
        return results, fmt.Errorf("scenario %s failed: %w", scenario.Name(), err)
        // ❌ 与 Run 方法注释"执行所有"矛盾
    }
}
```

**改进建议**:
```go
// 改进：添加配置支持灵活的错误处理
type ScenarioExecutorConfig struct {
    ContinueOnError bool  // 是否遇到失败继续执行
    FailFast        bool  // 是否快速失败
    Logger          *slog.Logger  // 使用结构化日志
}

func (e *ScenarioExecutor) Run(ctx context.Context) ([]*ScenarioResult, error) {
    var results []*ScenarioResult
    var errs []error
    
    for _, scenario := range e.scenarios {
        result, err := e.runScenario(ctx, scenario)
        if err != nil {
            e.config.Logger.Error("scenario failed", 
                "scenario", scenario.Name(), 
                "error", err)
            
            if e.config.FailFast {
                return results, fmt.Errorf("scenario %s failed: %w", scenario.Name(), err)
            }
            
            errs = append(errs, fmt.Errorf("scenario %s: %w", scenario.Name(), err))
            if !e.config.ContinueOnError {
                break
            }
        }
        results = append(results, result)
    }
    
    if len(errs) > 0 {
        return results, fmt.Errorf("%d scenarios failed: %v", len(errs), errs)
    }
    
    return results, nil
}
```

#### 14.2.4 缺少关键接口实现

| 缺失项 | 影响 |
|--------|------|
| `context.Context` 传递 | `DataGenerator` 缺少 ctx 参数，无法取消长时间运行的生成任务 |
| `Close()` 方法 | `TestEnvironment` 接口缺少资源明确的 `Close()` 方法，只有 `Stop()` |
| 超时配置 | `WaitForReady` 每次健康检查使用 `context.Background()` 而非传入的 ctx |

**改进建议**:
```go
// 1. DataGenerator 添加 context 支持
type DataGenerator interface {
    GenerateKeys(ctx context.Context, count int) ([]string, error)
    GenerateValues(ctx context.Context, count int, size int) ([][]byte, error)
}

// 2. TestEnvironment 添加 Close 方法
type TestEnvironment interface {
    Init(ctx context.Context) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Close() error  // 新增：明确资源释放
}

// 3. WaitForReady 使用传入的 ctx
func (c *DefaultTestCluster) WaitForReady(ctx context.Context, timeout time.Duration) error {
    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    // ... 使用 ctx 而非 context.Background()
}
```

---

### 14.3 目录结构优化建议

当前建议 `pkg/test/framework/`，但考虑到这是通用框架，建议调整为：

```
pkg/test/
├── framework/           # 核心框架（稳定接口）
│   ├── test_environment.go
│   ├── cluster.go
│   ├── component_registry.go
│   └── ...
├── adapters/            # 组件适配器实现
│   ├── transport/
│   ├── storage/
│   ├── replication/
│   └── cluster/
└── integrate/           # 集成测试用例
test/
├── e2e/                 # 现有端到端测试
└── integrate/           # 组件级集成测试（新建）
```

**理由**:
1. `pkg/test/framework/` - 稳定的核心接口，对外暴露
2. `pkg/test/adapters/` - 适配器实现，可独立演进
3. `test/integration/` - 测试用例与框架分离

---

### 14.4 测试隔离增强建议

#### 14.4.1 隔离级别

```go
// 建议增加 TestContext 的隔离级别
type TestContext struct {
    Registry       *ComponentRegistry
    RandomSeed     int64
    TempDir        string
    IsolationLevel IsolationLevel  // 新增
}

type IsolationLevel int

const (
    IsolationProcess IsolationLevel = iota  // 进程级隔离（完全隔离）
    IsolationNetwork                        // 网络级隔离（共用进程）
    IsolationNone                           // 无隔离（仅用于单测试）
)

// 使用示例
func TestWithProcessIsolation(t *testing.T) {
    ctx := framework.NewTestContextWithIsolation(
        framework.IsolationProcess,
    )
    // 每个测试在独立进程中运行
}
```

#### 14.4.2 测试目录管理建议

**环境变量方案**:
```bash
# 设置基础目录
export NEXKV_BASE_DIR=/Users/zhangcz/.nexkv/
```

**目录结构**:
```
$NEXKV_BASE_DIR/
└── test/
    ├── TestTransportDiscovery-uuid-v7-xxx/    # 测试1的隔离目录
    │   ├── data/                               # 测试数据
    │   ├── logs/                               # 测试日志
    │   └── temp/                               # 临时文件
    ├── TestStoragePersistence-uuid-v7-yyy/    # 测试2的隔离目录
    └── ...
```

**test-id 生成规则**: `$test-name-uuid-v7`
- `test-name`: 测试函数名（如 `TestTransportDiscovery`）
- `uuid-v7`: UUID v7（时间排序，便于查找最新测试）

**实现代码**:
```go
// TestDirectoryManager 测试目录管理器
type TestDirectoryManager struct {
    baseDir  string
    testID   string
    testName string
}

// NewTestDirectoryManager 创建测试目录管理器
// testName: 测试函数名，如 "TestTransportDiscovery"
func NewTestDirectoryManager(testName string) (*TestDirectoryManager, error) {
    baseDir := os.Getenv("NEXKV_BASE_DIR")
    if baseDir == "" {
        baseDir = filepath.Join(os.TempDir(), "nexkv")
    }

    // 生成唯一测试ID: $test-name-uuid-v7
    // UUID v7 包含时间戳，可按时间排序
    testID := fmt.Sprintf("%s-%s", testName, uuid.Must(uuid.NewV7()).String())

    mgr := &TestDirectoryManager{
        baseDir:  filepath.Join(baseDir, "test"),
        testID:   testID,
        testName: testName,
    }

    // 创建测试目录
    if err := mgr.createDirectories(); err != nil {
        return nil, err
    }

    return mgr, nil
}

// createDirectories 创建测试目录结构
func (m *TestDirectoryManager) createDirectories() error {
    dirs := []string{
        m.GetDataDir(),
        m.GetLogsDir(),
        m.GetTempDir(),
    }

    for _, dir := range dirs {
        if err := os.MkdirAll(dir, 0755); err != nil {
            return fmt.Errorf("failed to create directory %s: %w", dir, err)
        }
    }

    return nil
}

// GetTestDir 获取测试根目录
func (m *TestDirectoryManager) GetTestDir() string {
    return filepath.Join(m.baseDir, m.testID)
}

// GetDataDir 获取数据目录
func (m *TestDirectoryManager) GetDataDir() string {
    return filepath.Join(m.GetTestDir(), "data")
}

// GetLogsDir 获取日志目录
func (m *TestDirectoryManager) GetLogsDir() string {
    return filepath.Join(m.GetTestDir(), "logs")
}

// GetTempDir 获取临时目录
func (m *TestDirectoryManager) GetTempDir() string {
    return filepath.Join(m.GetTestDir(), "temp")
}

// Cleanup 清理测试目录（测试结束后调用）
func (m *TestDirectoryManager) Cleanup() error {
    return os.RemoveAll(m.GetTestDir())
}

// CleanupOnSignal 注册信号处理，在测试中断时自动清理
func (m *TestDirectoryManager) CleanupOnSignal(signals ...os.Signal) {
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, signals...)

    go func() {
        <-sigChan
        slog.Warn("received signal, cleaning up test directory",
            "testID", m.testID)
        m.Cleanup()
        os.Exit(1)
    }()
}

// 使用示例
func TestTransportDiscovery(t *testing.T) {
    // 创建测试目录管理器
    // test-id: TestTransportDiscovery-uuid-v7-xxx
    dirMgr, err := NewTestDirectoryManager(t.Name())
    require.NoError(t, err)

    // 测试结束后清理
    defer dirMgr.Cleanup()

    // 注册信号处理（测试中断时清理）
    dirMgr.CleanupOnSignal(syscall.SIGINT, syscall.SIGTERM)

    // 使用目录
    dataDir := dirMgr.GetDataDir()
    logDir := dirMgr.GetLogsDir()

    t.Logf("test directory: %s", dirMgr.GetTestDir())
    // 输出: test directory: /Users/zhangcz/.nexkv/test/TestTransportDiscovery-018f3b7a-...

    // 执行测试...
}
```

**优势**:
1. **隔离性** - 每个测试有独立目录，避免相互影响
2. **可追溯** - 通过 `$test-name-uuid-v7` 可快速定位测试和查看历史
3. **可排序** - UUID v7 包含时间戳，目录按时间排序，便于查找最新测试
4. **自动清理** - 测试结束或中断时自动清理
5. **可配置** - 通过环境变量配置基础目录

**UUID v7 优势**:
- 包含 Unix 时间戳（毫秒级），天然按时间排序
- 无需额外索引即可按时间范围查询
- 相比 UUID v4，更适合作为目录名排序

#### 14.4.3 测试目录清理策略

**问题**: 测试崩溃时可能无法执行 `defer Cleanup()`，导致目录残留。

**解决方案**:

```go
// CleanupPolicy 目录清理策略
type CleanupPolicy struct {
    MaxAge       time.Duration  // 最大保留时间（如 24h）
    MaxDiskUsage int64          // 最大磁盘使用（如 1GB）
    OnCrash      CleanupAction  // 崩溃时处理方式
}

type CleanupAction int

const (
    CleanupImmediate CleanupAction = iota  // 立即清理（通过信号处理）
    CleanupOnRestart                       // 下次启动时清理
    CleanupManual                          // 手动清理
)

// GlobalCleanup 全局清理函数（在测试框架初始化时调用）
func GlobalCleanup(baseDir string, policy CleanupPolicy) error {
    // 1. 清理超过 MaxAge 的目录
    entries, err := os.ReadDir(baseDir)
    if err != nil {
        return err
    }

    now := time.Now()
    for _, entry := range entries {
        if !entry.IsDir() {
            continue
        }

        info, err := entry.Info()
        if err != nil {
            continue
        }

        // 检查目录年龄
        if now.Sub(info.ModTime()) > policy.MaxAge {
            dirPath := filepath.Join(baseDir, entry.Name())
            os.RemoveAll(dirPath) // 异步清理，忽略错误
        }
    }

    // 2. 检查磁盘使用
    // TODO: 实现磁盘使用统计和清理

    return nil
}
```

**使用方式**:

```go
// 在 TestMain 中配置全局清理
func TestMain(m *testing.M) {
    baseDir := os.Getenv("NEXKV_BASE_DIR")
    if baseDir == "" {
        baseDir = filepath.Join(os.TempDir(), "nexkv")
    }

    // 启动时清理旧目录
    framework.GlobalCleanup(filepath.Join(baseDir, "test"), framework.CleanupPolicy{
        MaxAge:       24 * time.Hour,  // 保留 24 小时
        MaxDiskUsage: 1024 * 1024 * 1024, // 最大 1GB
        OnCrash:      framework.CleanupImmediate,
    })

    os.Exit(m.Run())
}
```

**清理策略说明**:

| 场景 | 处理方式 | 说明 |
|------|----------|------|
| 正常结束 | `defer Cleanup()` | 立即清理当前测试目录 |
| 信号中断 | `CleanupOnSignal()` | 捕获 SIGINT/SIGTERM 后清理 |
| 崩溃残留 | `GlobalCleanup()` | 下次启动时清理超过 MaxAge 的目录 |
| 磁盘不足 | 自动清理最旧目录 | 保留最近 24h，删除更早的 |

---

### 14.5 CI/CD 集成配置

#### 14.5.1 GitHub Actions 配置示例

```yaml
# .github/workflows/integration-test.yml
name: Integration Tests

on:
  push:
    branches: [ main, develop ]
  pull_request:
    branches: [ main ]

jobs:
  integration-test:
    runs-on: ubuntu-latest
    
    steps:
    - uses: actions/checkout@v4
    
    - name: Set up Go
      uses: actions/setup-go@v5
      with:
        go-version: '1.21'
    
    - name: Cache Go modules
      uses: actions/cache@v4
      with:
        path: ~/go/pkg/mod
        key: ${{ runner.os }}-go-${{ hashFiles('**/go.sum') }}
        restore-keys: |
          ${{ runner.os }}-go-
    
    - name: Run Integration Tests
      run: |
        export NEXKV_BASE_DIR=${{ runner.temp }}/nexkv
        go test -v ./test/integration/... \
          -race \
          -parallel 4 \
          -timeout 30m \
          -coverprofile=coverage.out
    
    - name: Upload coverage to Codecov
      uses: codecov/codecov-action@v4
      with:
        files: ./coverage.out
        flags: integration
        name: integration-tests
    
    - name: Upload test results
      uses: actions/upload-artifact@v4
      if: always()
      with:
        name: test-results
        path: |
          ${{ runner.temp }}/nexkv/test/
          !**/*.db
        retention-days: 7
```

#### 14.5.2 Makefile 集成

```makefile
# Makefile
.PHONY: integration-test integration-test-verbose integration-test-ci

NEXKV_BASE_DIR ?= $(HOME)/.nexkv

integration-test:
	NEXKV_BASE_DIR=$(NEXKV_BASE_DIR) go test -v ./test/integration/... -timeout 30m

integration-test-verbose:
	NEXKV_BASE_DIR=$(NEXKV_BASE_DIR) go test -v ./test/integration/... -timeout 30m -race

integration-test-ci:
	NEXKV_BASE_DIR=$(NEXKV_BASE_DIR) go test -v ./test/integration/... \
		-timeout 30m \
		-race \
		-parallel 4 \
		-coverprofile=coverage.out \
		-json > test-results.json
```

#### 14.5.3 测试报告格式

框架支持 JUnit XML 格式输出，便于 CI 系统解析：

```go
// 在 ScenarioExecutor 中配置报告输出
executor := framework.NewScenarioExecutor(
    scenarios,
    framework.WithJUnitReporter("test-results.xml"),
)
```

---

### 14.6 网络分区实现风险提示

`NetworkPartitionController` 实现假设:
1. Transport 层有 `SetMessageInterceptor` 方法
2. 可以通过 `transport.DisconnectFrom(peerID)` 断开连接

**风险**: 这些方法可能不存在于生产代码中，需要在实现时确认或适配。

**缓解措施**:
- Phase 1 优先验证 libp2p 是否支持运行时连接控制
- 如不支持，准备备选方案（防火墙规则或自定义包装器）
- 在 Adapter 层封装差异，保持框架接口稳定

---

### 14.6 故障排查指南

#### 14.6.1 常见失败模式

| 症状 | 可能原因 | 排查方法 | 解决方案 |
|------|----------|----------|----------|
| **节点发现超时** | libp2p mDNS 广播失败 | 检查网络接口，查看 `~/.nexkv/test/*/logs/` | 使用静态 bootstrap 列表替代 mDNS |
| **RPC 调用失败** | 节点未就绪或网络不通 | 检查 `WaitForReady` 日志，验证连接状态 | 增加重试次数，检查防火墙规则 |
| **测试目录残留** | 测试崩溃或信号未捕获 | 检查 `NEXKV_BASE_DIR` 目录大小 | 手动清理或使用 `GlobalCleanup` |
| **端口冲突** | 多个测试同时运行 | 检查 `lsof -i :<port>` | 使用动态端口分配或串行执行 |
| **资源泄漏** | goroutine 未正确关闭 | 使用 `go tool pprof` 检查 goroutine 数量 | 确保 `defer Cleanup()` 和 `Close()` 被调用 |
| **数据竞争** | 并行测试访问共享状态 | 使用 `go test -race` 检测 | 使用 `TestContext.Registry` 替代 `GlobalRegistry` |

#### 14.6.2 调试技巧

```bash
# 1. 查看详细日志
NEXKV_LOG_LEVEL=debug go test -v ./test/integration/... -run TestTransportDiscovery

# 2. 保留测试目录（不自动清理）
NEXKV_KEEP_TEST_DIR=1 go test -v ./test/integration/...

# 3. 只运行特定场景
go test -v ./test/integration/... -run TestTransportDiscovery/TwoNodesConnect

# 4. 生成性能分析
NEXKV_CPU_PROFILE=cpu.prof go test -v ./test/integration/...
go tool pprof cpu.prof

# 5. 查看测试目录结构
ls -la ~/.nexkv/test/
tree ~/.nexkv/test/TestTransportDiscovery-*/
```

#### 14.6.3 日志分析

框架日志输出格式：
```
2024-02-20T10:30:00Z INFO transport.adapter node started node_id=12D3... listen_addr=/ip4/127.0.0.1/tcp/10000
2024-02-20T10:30:01Z DEBUG test.cluster waiting for nodes ready nodes=3 timeout=30s
2024-02-20T10:30:02Z ERROR test.scenario rpc call failed node=0 target=1 error="connection refused"
```

关键日志字段：
- `node_id`: libp2p 节点 ID
- `test_id`: 测试唯一标识（用于关联日志和目录）
- `scenario`: 场景名称
- `duration`: 操作耗时

#### 14.6.4 获取帮助

如遇到无法解决的问题：
1. 查看测试目录中的 `logs/` 子目录
2. 使用 `NEXKV_LOG_LEVEL=debug` 重新运行测试
3. 在 Issue 中提供：测试名称、环境信息、完整日志

---

### 14.7 实施优先级建议

| 优先级 | 项目 | 原因 | 建议完成时间 |
|--------|------|------|--------------|
| **P0** | 修复 `GlobalRegistry` 并发问题 | 会导致并行测试 flaky | Phase 1 Week 1 |
| **P0** | 完善 `cleanup` 的 goroutine 管理 | 资源泄漏 | Phase 1 Week 1 |
| **P1** | 统一日志输出 (`fmt.Printf` → `log/slog`) | 生产级可观测性 | Phase 1 Week 2 |
| **P1** | 为 `DataGenerator` 添加 context 支持 | 长时任务可取消 | Phase 1 Week 2 |
| **P2** | 增加 `IsolationLevel` 支持 | 测试隔离灵活性 | Phase 2 |
| **P2** | `ScenarioExecutor` 添加 `ContinueOnError` 配置 | 更好的测试编排 | Phase 2 |

---

### 14.7 架构评分总结

| 维度 | 评分 | 说明 |
|------|------|------|
| **架构设计** | ⭐⭐⭐⭐ | 分层清晰，接口设计良好 |
| **代码完整度** | ⭐⭐⭐⭐ | 核心功能完备，~3757 行代码 |
| **并发安全** | ⭐⭐ | `GlobalRegistry` 需要修复 |
| **可测试性** | ⭐⭐⭐⭐⭐ | 专为测试设计，支持混沌场景 |
| **生产就绪** | ⭐⭐⭐ | 需要日志、监控、隔离级别增强 |

---

## 十五、附录

### 15.1 测试命名规范

参考 Go 测试惯例，采用 `{Component}_{Scenario}_{Expected}` 格式：

```
格式: {Component}_{Scenario}_{Expected}

示例:
- Transport_TwoNodesConnect_Success
- Transport_PartitionRecovery_Reconnect
- Storage_WriteRead_DataIntegrity
- Replication_QuorumWrite_MajorityAck
- Cluster_NodeJoin_MembershipUpdate
```

#### 命名规则

| 部分 | 说明 | 示例 |
|------|------|------|
| **Component** | 被测组件 | `Transport`, `Storage`, `Replication`, `Cluster` |
| **Scenario** | 测试场景 | `TwoNodesConnect`, `PartitionRecovery`, `QuorumWrite` |
| **Expected** | 期望结果 | `Success`, `Failure`, `Timeout`, `DataIntegrity` |

#### 子测试命名

使用 Go 1.7+ 的子测试功能组织多场景：

```go
func TestTransport(t *testing.T) {
    t.Run("TwoNodesConnect_Success", func(t *testing.T) {
        // 测试两个节点成功连接
    })
    
    t.Run("PartitionRecovery_Reconnect", func(t *testing.T) {
        // 测试网络分区恢复后重连
    })
}
```

#### 场景文件命名

```
scenarios/
├── transport_two_nodes_connect.go      # 场景实现
├── transport_partition_recovery.go
├── storage_persistence.go
└── replication_quorum_write.go
```

---

### 15.2 术语表

| 术语 | 说明 |
|------|------|
| **Adapter** | 组件适配器，将生产代码包装为测试框架可用的组件 |
| **Scenario** | 测试场景，描述一组测试步骤和验证逻辑 |
| **TestCluster** | 测试集群，模拟多节点分布式环境 |
| **TestContext** | 测试上下文，提供测试隔离和资源管理 |
| **ComponentRegistry** | 组件注册表，管理组件工厂和依赖关系 |
| **Network Partition** | 网络分区，模拟节点间网络隔离 |

### 15.3 参考资料

1. [Go Testing patterns](https://github.com/golang/go/wiki/TestComments)
2. [Chaos Engineering Principles](https://principlesofchaos.org/)
3. [libp2p Go Documentation](https://docs.libp2p.io/reference/go/)
4. [Kahn's Algorithm](https://en.wikipedia.org/wiki/Topological_sorting#Kahn's_algorithm)

---

### 15.4 版本兼容性策略

框架接口遵循语义化版本控制（Semantic Versioning）：

| 版本变更 | 兼容性保证 | 示例 | 处理方式 |
|----------|------------|------|----------|
| **主版本** (v1 → v2) | 允许破坏性变更 | 接口签名改变 | 提供迁移指南，保留 v1 兼容层 6 个月 |
| **次版本** (v1.1 → v1.2) | 向后兼容 | 新增功能 | 直接升级，无需修改代码 |
| **修订号** (v1.1.0 → v1.1.1) | 完全兼容 | Bug 修复 | 立即升级 |

**废弃策略**:
- 标记为 `Deprecated` 的接口保留至少 2 个次版本
- 废弃通知在接口文档和编译时警告中体现
- 提供自动化迁移工具（如 `go fix` 兼容的脚本）

**当前版本状态**:
- **v1.0.0**: 初始版本，支持 Transport/Storage/Replication 适配器
- **v2.0.0 计划**: 移除 GlobalRegistry，强制使用 TestContext.Registry

---

**文档版本**: v2.6
**创建日期**: 2026-02-20
**最后更新**: 2026-02-20
**作者**: AI Agent
**审查状态**: 已审查，已修复所有 P0/P1/P2/P3
