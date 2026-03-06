# SourceID 策略配置设计方案（简化版）

> **文档类型**: 技术设计文档
> **创建日期**: 2026-03-06
> **状态**: 📝 待评审
> **优先级**: P1 - 性能优化
> **预估工期**: 2 天

---

## 1. 背景与目标

### 1.1 背景

当前 RPC 层在创建任务时，所有任务都使用硬编码的 `SourceRPCCallback` 作为 SourceID：

```go
task := NewRPCCallTask(
    a.rpc,
    to,
    req,
    model.SourceRPCCallback,  // ❌ 硬编码
    timeout,
)
```

**问题**:
- 调用方无法指定 SourceID
- 无法利用 PerCoreExecutor 的 CPU 亲和性

### 1.2 目标

1. **灵活性** - 支持调用方指定 SourceID
2. **简洁** - 使用可选参数模式，不引入复杂策略
3. **渐进** - 后续根据实际需求扩展

### 1.3 设计原则

**核心原则**: 由调用方决定使用什么 SourceID

- ✅ RPC 层只提供**传递 SourceID 的能力**
- ✅ 不在 RPC 层实现复杂的策略选择逻辑
- ✅ 各上层模块（Raft、Storage 等）根据自己的需求选择

---

## 2. 设计方案

### 2.1 使用可选参数模式

```go
// CallAsyncOption 可选参数类型
type CallAsyncOption func(*callAsyncConfig)

type callAsyncConfig struct {
    sourceID model.SourceID
}

// WithSourceID 指定 SourceID
func WithSourceID(sourceID model.SourceID) CallAsyncOption {
    return func(c *callAsyncConfig) {
        c.sourceID = sourceID
    }
}
```

### 2.2 修改 CallAsync 接口

```go
func (a *RPCAsyncAdapter) CallAsync(
    ctx context.Context,
    to model.PeerID,
    req model.Message,
    opts ...CallAsyncOption,  // ✅ 新增可选参数
) model.Task[service.ResponseMsg] {
    // 默认配置
    config := &callAsyncConfig{
        sourceID: model.SourceRPCCallback,  // 保持现有默认行为
    }

    // 应用可选参数
    for _, opt := range opts {
        opt(config)
    }

    timeoutMs := a.getTimeout()
    task := NewRPCCallTask(
        a.rpc,
        to,
        req,
        config.sourceID,  // ✅ 使用配置的 SourceID
        time.Duration(timeoutMs)*time.Millisecond,
    )

    a.submitTask(task)
    return task
}
```

### 2.3 使用示例

```go
// 场景 1: 使用默认 SourceID（向后兼容）
task := rpc.CallAsync(ctx, peerID, req)

// 场景 2: 指定分片亲和 SourceID
task := rpc.CallAsync(ctx, peerID, req,
    WithSourceID(model.NewSourceShard("shard-123")))

// 场景 3: 指定客户端亲和 SourceID
task := rpc.CallAsync(ctx, peerID, req,
    WithSourceID(model.NewSourceClient("client-abc")))

// 场景 4: 指定节点亲和 SourceID
task := rpc.CallAsync(ctx, peerID, req,
    WithSourceID(model.NewSourceNode(peerID.String())))
```

---

## 3. 其他方法修改

同样的模式应用到其他 RPC 方法：

```go
// BroadcastAsync
func (a *RPCAsyncAdapter) BroadcastAsync(
    ctx context.Context,
    peers []model.PeerID,
    req model.Message,
    opts ...CallAsyncOption,  // ✅ 新增
) model.Task[service.AsyncBroadcastResult]

// QuorumAsync
func (a *RPCAsyncAdapter) QuorumAsync(
    ctx context.Context,
    peers []model.PeerID,
    req model.Message,
    quorum int,
    opts ...CallAsyncOption,  // ✅ 新增
) model.Task[service.QuorumResult]

// WriteVAsync
func (a *RPCAsyncAdapter) WriteVAsync(
    ctx context.Context,
    targets []model.PeerID,
    msgs []model.Message,
    opts ...CallAsyncOption,  // ✅ 新增
) model.Task[service.WriteVResult]
```

---

## 4. 实施计划

### Day 1: 核心实现（4-6小时）

- [ ] 创建 `internal/infrastructure/rpc/options.go`
- [ ] 定义 `CallAsyncOption` 和 `WithSourceID`
- [ ] 修改 `RPCAsyncAdapter.CallAsync`
- [ ] 修改 `BroadcastAsync`、`QuorumAsync`、`WriteVAsync`
- [ ] 修改对应的 Task 构造函数

### Day 2: 测试（2-4小时）

- [ ] 单元测试：`TestWithSourceID`
- [ ] 集成测试：`TestCallAsync_WithSourceID`
- [ ] 向后兼容测试：确保未指定 opts 时行为不变
- [ ] 更新文档

---

## 5. 测试策略

### 5.1 单元测试

```go
func TestWithSourceID(t *testing.T) {
    sourceID := model.NewSourceShard("123")

    config := &callAsyncConfig{}
    WithSourceID(sourceID)(config)

    assert.True(t, config.sourceID.Equals(sourceID))
}

func TestApplyOptions_Default(t *testing.T) {
    // 未指定 opts 时使用默认值
    config := applyOptions(nil)

    assert.True(t, config.sourceID.Equals(model.SourceRPCCallback))
}
```

### 5.2 集成测试

```go
func TestCallAsync_WithSourceID(t *testing.T) {
    rpc := mockRPC{}
    adapter := NewRPCAsyncAdapter(rpc, nil, nil)

    sourceID := model.NewSourceShard("123")
    task := adapter.CallAsync(
        context.Background(),
        "peer-1",
        req,
        WithSourceID(sourceID),
    )

    assert.True(t, task.SourceID().Equals(sourceID))
}

func TestCallAsync_DefaultSourceID(t *testing.T) {
    // 向后兼容测试
    rpc := mockRPC{}
    adapter := NewRPCAsyncAdapter(rpc, nil, nil)

    task := adapter.CallAsync(
        context.Background(),
        "peer-1",
        req,
        // 未指定 opts
    )

    // 应该使用默认值
    assert.True(t, task.SourceID().Equals(model.SourceRPCCallback))
}
```

---

## 6. 后续扩展（由各模块自行实现）

### 6.1 Raft 层示例

```go
// internal/raft/transport.go

func (t *Transport) SendRaftMessage(to PeerID, msg RaftMessage) error {
    // Raft 层决定使用分片亲和 SourceID
    shardID := t.getShardID(msg)
    sourceID := model.NewSourceShard(shardID)

    task := t.rpc.CallAsync(
        context.Background(),
        to,
        msg,
        WithSourceID(sourceID),  // ✅ Raft 层决定
    )

    return nil
}
```

### 6.2 存储层示例

```go
// internal/storage/replication.go

func (r *Replication) ReplicateToPeer(to PeerID, data []byte) error {
    // 存储层决定使用节点亲和 SourceID
    sourceID := model.NewSourceNode(to.String())

    task := r.rpc.CallAsync(
        context.Background(),
        to,
        req,
        WithSourceID(sourceID),  // ✅ 存储层决定
    )

    return nil
}
```

---

## 7. 设计优势

| 方面 | 说明 |
|------|------|
| **简单** | 使用 Go 惯用的可选参数模式 |
| **灵活** | 调用方可以指定任何 SourceID |
| **向后兼容** | 不指定 opts 时保持现有行为 |
| **职责分离** | RPC 层只负责传递，不做决策 |
| **渐进增强** | 后续需要时再添加更多选项 |

---

## 8. 不做什么

### ❌ 不实现复杂的策略接口

```go
// ❌ 不需要这样的复杂接口
type SourceIDStrategy interface {
    GetSourceID(req model.Message, peer model.PeerID) model.SourceID
}

type DefaultSourceIDStrategy struct {
    // ... 复杂逻辑
}
```

**原因**:
- RPC 层不应该知道业务逻辑（Raft 分片、客户端 ID 等）
- 策略选择应该由知道业务的上层模块决定
- 过早优化，等实际需要时再实现

### ❌ 不考虑 CPU 核心数限制

**原因**:
- 这是 Executor 层应该处理的问题
- 当前 PerCoreExecutor 已经有队列管理
- SourceID 只是提供"建议"，Executor 负责最终调度

---

**文档版本**: v2.0 (简化版)
**创建日期**: 2026-03-06
**作者**: AI Assistant
**状态**: 📝 待评审
