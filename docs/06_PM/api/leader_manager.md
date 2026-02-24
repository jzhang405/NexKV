# Leader Manager API 文档

> Leader 选举与管理机制

## 概述

NexKV 采用去中心化的 Leader 选举机制，通过 Fencing Token 防止脑裂，通过 Quorum 机制保证 Leader 变更的一致性。本文档描述 Leader 选举、切换和管理的核心 API。

### 核心概念

- **Leader**：负责处理写入请求的节点
- **Term**：任期号，每次 Leader 选举后递增
- **Fencing Token**：包含 Term 的令牌，用于防止旧 Leader 的写入
- **Quorum**：多数派确认，用于 Leader 选举和变更

## QuorumCoordinator API

QuorumCoordinator 负责需要强一致性的操作，包括 Leader 变更。

### 配置

```go
type QuorumCoordinator struct {
    participants []string            // 参与者节点 ID 列表
    quorum       int                 // Quorum 阈值（多数派）
    timeout      time.Duration       // 超时时间
    metadataKV   *kvstore.MetadataKV // 元数据存储
}
```

### 创建实例

```go
coordinator := NewQuorumCoordinator(participants, metadataKV)
```

**参数**：
- `participants`: 参与者节点 ID 列表
- `metadataKV`: 元数据存储实例

**Quorum 计算**：`quorum = ⌊n/2⌋ + 1`

### 带选项创建

```go
coordinator := NewQuorumCoordinatorWithOptions(participants, metadataKV, opts)
```

**选项结构**：

```go
type PutOptions struct {
    Timeout int // 超时时间（毫秒）
    // ... 其他选项
}
```

### Quorum 写入

```go
err := coordinator.PutWithQuorum(ctx, namespace, key, value, opts)
```

**参数**：
- `ctx`: 上下文
- `namespace`: 命名空间
- `key`: 键
- `value`: 值
- `opts`: 选项（可为 nil）

**返回**：
- `error`: 错误信息

**行为**：
1. 本地写入
2. 等待多数派 ACK
3. 超时返回错误

### 获取 Quorum 阈值

```go
quorum := coordinator.GetQuorum()
```

### 获取参与者列表

```go
participants := coordinator.GetParticipants()
```

## TreeCoordinator API

TreeCoordinator 负责管理集群的树形拓扑结构，包括 Leader 相关的元数据管理。

### 拓扑管理

```go
type TreeCoordinator struct {
    // 拓扑信息
    localNodeID string
    parentNode  string
    childNodes  []string
    nodeType    NodeType // Leaf/Middle/Root

    // 元数据管理
    metadataKV *kvstore.MetadataKV

    // 一致性协调
    quorumCoordinator *quorum.QuorumCoordinator
}
```

### 更新拓扑

```go
err := coordinator.UpdateTopology(parent, children, depth)
```

**参数**：
- `parent`: 父节点 ID
- `children`: 子节点 ID 列表
- `depth`: 树深度

### 获取节点类型

```go
nodeType := coordinator.GetNodeType()
```

**返回**：
- `NodeTypeLeaf`: 叶子节点
- `NodeTypeMiddle`: 中间节点
- `NodeTypeRoot`: Root 节点

## Leader 选举流程

### 1. 发起选举

当检测到当前 Leader 不可用时：

```go
// 1. 推进 Term
newTerm, err := termStorage.AdvanceTerm(ctx)
if err != nil {
    return err
}

// 2. 创建 Fencing Token
token := NewFencingToken(newTerm, localNodeID)

// 3. 请求投票（Quorum 确认）
err := quorumCoordinator.PutWithQuorum(ctx, "cluster", "leader", localNodeID, nil)
if err != nil {
    // 选举失败，重试
    return err
}

// 4. 成为 Leader
currentLeader = localNodeID
```

### 2. 选举验证

其他节点验证选举结果：

```go
// 收到新 Leader 通知
func (n *Node) OnLeaderChange(newLeader string, term uint64) {
    // 验证 Term
    currentTerm, _ := n.termStorage.GetCurrentTerm(ctx)
    if term < currentTerm {
        // 拒绝旧 Term 的 Leader
        return
    }

    // 更新本地 Term
    n.termStorage.AdvanceTerm(ctx)

    // 接受新 Leader
    n.currentLeader = newLeader
}
```

### 3. Fencing Token 使用

新 Leader 执行写入时携带 Token：

```go
// Leader 执行写入
func (l *Leader) Write(ctx context.Context, ns, key string, value []byte) error {
    // 获取当前 Token
    token := l.GetCurrentToken()

    // 携带 Token 写入
    return l.store.PutWithToken(ctx, ns, key, value, token)
}
```

## Leader 健康检查

### 心跳机制

```go
// Leader 定期发送心跳
func (l *Leader) SendHeartbeat(ctx context.Context) error {
    heartbeat := &Heartbeat{
        LeaderID:  l.nodeID,
        Term:      l.currentTerm,
        Timestamp: time.Now(),
    }

    return l.quorumCoordinator.PutWithQuorum(ctx, "cluster", "heartbeat", heartbeat, nil)
}
```

### 心跳超时检测

```go
// Follower 检测 Leader 心跳超时
func (f *Follower) CheckHeartbeat() {
    lastHeartbeat := f.getLastHeartbeat()
    if time.Since(lastHeartbeat) > f.electionTimeout {
        // 触发选举
        f.StartElection()
    }
}
```

## Leader 切换流程

### 优雅切换

```go
// 1. 当前 Leader 发起切换
func (l *Leader) TransferLeadership(ctx context.Context, newLeader string) error {
    // 验证新 Leader 有效
    if !l.isValidNode(newLeader) {
        return ErrInvalidNode
    }

    // 2. 推进 Term
    newTerm, _ := l.termStorage.AdvanceTerm(ctx)

    // 3. Quorum 确认切换
    transferReq := &LeadershipTransfer{
        OldLeader: l.nodeID,
        NewLeader: newLeader,
        Term:      newTerm,
    }

    err := l.quorumCoordinator.PutWithQuorum(ctx, "cluster", "leader_transfer", transferReq, nil)
    if err != nil {
        return err
    }

    // 4. 降级为 Follower
    l.becomeFollower()

    return nil
}
```

### 故障切换

```go
// Follower 检测到 Leader 故障，发起选举
func (f *Follower) StartElection() error {
    // 1. 推进 Term
    newTerm, _ := f.termStorage.AdvanceTerm(ctx)

    // 2. 向其他节点请求投票
    votes := f.requestVotes(newTerm)

    // 3. 检查是否获得多数票
    if votes >= f.quorum {
        // 成为新 Leader
        f.becomeLeader(newTerm)
        return nil
    }

    return ErrElectionFailed
}
```

## 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| ElectionTimeout | 5s | 选举超时时间 |
| HeartbeatInterval | 1s | 心跳间隔 |
| QuorumTimeout | 5s | Quorum 操作超时 |
| MaxElectionRetries | 3 | 最大选举重试次数 |

## 错误处理

### 错误类型

```go
var (
    ErrElectionFailed    = errors.New("leader election failed")
    ErrNotLeader         = errors.New("not the current leader")
    ErrInvalidNode       = errors.New("invalid node for leadership")
    ErrQuorumNotReached  = errors.New("quorum not reached")
)
```

### 错误处理建议

| 错误 | 处理建议 |
|------|---------|
| `ErrElectionFailed` | 等待随机退避后重试 |
| `ErrNotLeader` | 重定向到当前 Leader |
| `ErrInvalidNode` | 验证节点状态后重试 |
| `ErrQuorumNotReached` | 检查网络连通性 |

## 监控指标

建议监控以下指标：

- `leader_term_current`: 当前 Term
- `leader_elections_total`: 选举总次数
- `leader_elections_failed`: 失败选举次数
- `leader_heartbeat_total`: 心跳总次数
- `leader_switch_duration_seconds`: 切换耗时

## 使用示例

### 场景 1：获取当前 Leader

```go
leader, err := cluster.GetLeader(ctx)
if err != nil {
    return err
}

if leader != localNodeID {
    // 重定向到 Leader
    return redirect(leader)
}

// 作为 Leader 处理请求
return handleAsLeader(ctx, request)
```

### 场景 2：检测 Leader 变更

```go
// 注册 Leader 变更回调
cluster.OnLeaderChange(func(oldLeader, newLeader string, term uint64) {
    log.Printf("Leader changed: %s -> %s (term=%d)", oldLeader, newLeader, term)

    // 更新本地状态
    if newLeader == localNodeID {
        becomeLeader()
    } else {
        becomeFollower(newLeader)
    }
})
```

### 场景 3：优雅关闭 Leader

```go
// Leader 优雅关闭
func (l *Leader) Shutdown(ctx context.Context) error {
    // 1. 停止接收新请求
    l.stopAcceptingRequests()

    // 2. 等待进行中的请求完成
    l.waitForPendingRequests()

    // 3. 转移 Leadership
    newLeader := l.selectNewLeader()
    return l.TransferLeadership(ctx, newLeader)
}
```

## 故障场景处理

### 脑裂恢复

```go
// 旧 Leader 尝试写入
oldToken := &FencingToken{Term: 5, NodeID: "old-leader"}
err := store.PutWithToken(ctx, ns, key, value, oldToken)
// 返回 ErrStaleToken，因为当前 Term = 6
```

### 网络分区

```go
// 分区恢复后同步状态
func (n *Node) OnPartitionRecover() {
    // 1. 获取当前 Term
    term, _ := n.termStorage.GetCurrentTerm(ctx)

    // 2. 查询当前 Leader
    leader, _ := n.cluster.GetLeader(ctx)

    // 3. 更新本地状态
    n.currentTerm = term
    n.currentLeader = leader
}
```

## 相关文档

- [Fencing Token API](fencing.md)
- [2PC API](twopc.md)
- [一致性协议设计](../02_design/protocols/01_一致性协议设计.md)
