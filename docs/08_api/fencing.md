# Fencing Token API 文档

> 防止脑裂（Split-Brain）导致的数据损坏

## 概述

Fencing Token 是一种分布式系统中防止脑裂问题的机制。当集群发生网络分区时，可能出现多个节点同时认为自己是 Leader 的情况。Fencing Token 通过单调递增的 Term（任期号）来确保只有当前有效的 Leader 才能执行写入操作。

### 核心原理

1. **Term 单调递增**：每次 Leader 选举产生新 Leader 时，Term 必须递增
2. **写入防护**：存储层拒绝 `Token.Term <= current.Term` 的写入
3. **持久化保证**：Term 持久化到磁盘，节点重启后防护依然有效

## API 结构

### FencingToken

```go
type FencingToken struct {
    Term     uint64    // 任期号（全局单调递增）
    NodeID   string    // 签发节点 ID
    IssuedAt time.Time // 签发时间
}
```

#### 字段说明

| 字段 | 类型 | 说明 |
|------|------|------|
| `Term` | `uint64` | 任期号，每次 Leader 选举后 +1 |
| `NodeID` | `string` | 签发此 Token 的节点 ID |
| `IssuedAt` | `time.Time` | Token 签发时间 |

### 创建 Token

```go
token := NewFencingToken(term, nodeID)
```

**参数**：
- `term`: 任期号
- `nodeID`: 节点 ID

**返回**：
- `*FencingToken`: 新创建的 Token

### 比较 Token

```go
isNewer := token.IsNewerThan(otherToken)
```

**参数**：
- `other`: 要比较的另一个 Token

**返回**：
- `bool`: 如果当前 Token 更新（Term 更高），返回 `true`

## TermStorage API

TermStorage 负责管理 Term 的持久化存储。

### 创建实例

```go
storage := NewTermStorage(kvStore)
```

**参数**：
- `kv`: 实现 `kvstore.Store` 接口的存储实例

### 获取当前 Term

```go
term, err := storage.GetCurrentTerm(ctx)
```

**参数**：
- `ctx`: 上下文

**返回**：
- `uint64`: 当前 Term
- `error`: 错误信息

**行为**：
- 优先从内存缓存读取
- 缓存未命中时从持久化存储读取
- 首次访问返回 0

### 推进 Term

```go
newTerm, err := storage.AdvanceTerm(ctx)
```

**参数**：
- `ctx`: 上下文

**返回**：
- `uint64`: 新的 Term 值
- `error`: 错误信息

**行为**：
1. 读取当前 Term
2. Term + 1
3. 持久化新 Term
4. 返回新 Term

## FencingValidator API

FencingValidator 负责验证写入请求的 Token。

### 创建实例

```go
validator := NewFencingValidator(termStorage)
```

**参数**：
- `termStorage`: Term 存储实例

### 验证 Token

```go
err := validator.Validate(token)
```

**参数**：
- `token`: 要验证的 Token

**返回**：
- `error`: 验证失败时返回错误

**错误类型**：
- `ErrStaleToken`: Token 已过期（Term 小于当前值）
- `ErrTokenNotFromLeader`: Token 不是来自当前 Leader

## 使用示例

### 场景 1：新 Leader 上任

```go
// 1. 新 Leader 上任时推进 Term
newTerm, err := termStorage.AdvanceTerm(ctx)
if err != nil {
    return err
}

// 2. 创建新的 Fencing Token
token := NewFencingToken(newTerm, localNodeID)

// 3. 后续写入操作携带此 Token
err = store.PutWithToken(ctx, namespace, key, value, token)
```

### 场景 2：写入验证

```go
// 存储层收到写入请求
func (s *Store) PutWithToken(ctx context.Context, ns, key string, value []byte, token *FencingToken) error {
    // 验证 Token
    if err := s.validator.Validate(token); err != nil {
        return err // 拒绝过期 Token 的写入
    }

    // 执行写入
    return s.put(ctx, ns, key, value)
}
```

### 场景 3：脑裂恢复

```go
// 旧 Leader 尝试写入（Term = 5）
oldToken := NewFencingToken(5, "old-leader")
err := validator.Validate(oldToken)
// 返回 ErrStaleToken，因为当前 Term = 6

// 新 Leader 的写入（Term = 6）
newToken := NewFencingToken(6, "new-leader")
err := validator.Validate(newToken)
// 验证通过
```

## 错误处理

### 错误类型

```go
var (
    ErrStaleToken           = errors.New("fencing token is stale")
    ErrTokenNotFromLeader   = errors.New("token is not from current leader")
    ErrTermPersistenceFailed = errors.New("failed to persist term")
)
```

### 错误处理建议

| 错误 | 处理建议 |
|------|---------|
| `ErrStaleToken` | 拒绝写入，要求重新获取 Token |
| `ErrTokenNotFromLeader` | 拒绝写入，触发 Leader 选举 |
| `ErrTermPersistenceFailed` | 重试持久化，失败则拒绝上任 |

## 持久化

### 存储键

```
Namespace: meta:cluster
Key: current_term
```

### 数据格式

```
[8 bytes] Term (Big Endian uint64)
```

## 性能考量

### 内存缓存

- Term 值缓存在内存中
- 减少 90%+ 的磁盘读取
- 写入时更新缓存

### 并发安全

- 使用 `sync.RWMutex` 保护
- 读操作使用读锁（高并发友好）
- 写操作使用写锁

## 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| Term 初始值 | 0 | 集群首次启动时的 Term |
| 缓存启用 | true | 是否启用内存缓存 |

## 监控指标

建议监控以下指标：

- `fencing_term_current`: 当前 Term 值
- `fencing_validation_total`: Token 验证总次数
- `fencing_validation_failed`: Token 验证失败次数
- `fencing_term_advance_total`: Term 推进次数

## 相关文档

- [Leader Manager API](leader_manager.md)
- [2PC API](twopc.md)
- [一致性协议设计](../02_design/protocols/01_一致性协议设计.md)
