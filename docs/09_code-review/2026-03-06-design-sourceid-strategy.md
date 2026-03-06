# SourceID 策略配置设计方案（未来扩展参考）

> **文档类型**: 技术设计文档
> **创建日期**: 2026-03-06
> **状态**: 📚 未来扩展参考（当前已满足基本需求）
> **优先级**: P2 - 未来优化
> **说明**: 本文档记录了 SourceID 策略的可选设计方案，供未来需要时参考

---

## ⚠️ 当前状态说明

**当前实现已满足基本需求**：

1. ✅ **RPC 专用 SourceID 常量已定义** (`internal/domain/model/source_id_rpc.go`)
   - `SourceRPC` - 通用 RPC 调用
   - `SourceRPCCallback` - RPC 回调执行
   - `SourceBroadcast` - 广播回调
   - `SourceRPCClient` - RPC 客户端调用

2. ✅ **任务使用正确的 SourceID**
   - `RPCCallTask` 使用 `SourceRPCCallback`
   - `RPCBroadcastTask` 使用 `SourceBroadcast`
   - `RPCQuorumTask` 使用 `SourceBroadcast`
   - `RPCWriteVTask` 使用 `SourceBroadcast`

3. ✅ **代码一致性和可维护性良好**

**何时需要实施本设计**：
- 需要让调用方自定义 SourceID 时
- 需要实现更复杂的策略选择时
- 需要支持配置化的 SourceID 选择时

---

## 设计方案（供未来参考）

### 核心思路

**原则**: 由调用方决定使用什么 SourceID

- RPC 层只提供**传递 SourceID 的能力**
- 不在 RPC 层实现复杂的策略选择
- 各上层模块（Raft、Storage 等）根据自己的需求选择

### 可选参数模式

```go
// CallAsyncOption 可选参数类型
type CallAsyncOption func(*callAsyncConfig)

// WithSourceID 指定 SourceID
func WithSourceID(sourceID model.SourceID) CallAsyncOption {
    return func(c *callAsyncConfig) {
        c.sourceID = sourceID
    }
}

// CallAsync 支持 SourceID 可选参数
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

    // ... 使用 config.sourceID
}
```

### 使用示例

```go
// 场景 1: 使用默认 SourceID（向后兼容）
task := rpc.CallAsync(ctx, peerID, req)

// 场景 2: 指定分片亲和 SourceID（由 Raft 模块决定）
task := rpc.CallAsync(ctx, peerID, req,
    WithSourceID(model.NewSourceShard("shard-123")))

// 场景 3: 指定客户端亲和 SourceID
task := rpc.CallAsync(ctx, peerID, req,
    WithSourceID(model.NewSourceClient("client-abc")))
```

### 后扩展示例

#### Raft 层示例

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

---

## 设计优势

| 方面 | 说明 |
|------|------|
| **简单** | 使用 Go 惯用的可选参数模式 |
| **灵活** | 调用方可以指定任何 SourceID |
| **向后兼容** | 不指定 opts 时保持现有行为 |
| **职责分离** | RPC 层只负责传递，不做决策 |
| **渐进增强** | 后续需要时再添加更多选项 |

---

## 不做什么

### ❌ 不实现复杂的策略接口

```go
// ❌ 不需要这样的复杂接口
type SourceIDStrategy interface {
    GetSourceID(req model.Message, peer model.PeerID) model.SourceID
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

**文档版本**: v3.0 (标注为未来扩展)
**创建日期**: 2026-03-06
**最后更新**: 2026-03-06
