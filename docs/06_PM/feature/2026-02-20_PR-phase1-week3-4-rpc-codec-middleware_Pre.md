# 【PR全流程文档】Feature - Phase 1 Week 3-4 RPC/Codec/Middleware 实现

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从需求对齐到开发完成的全流程，一个PR对应一份全流程文档，归档后作为项目追溯依据。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 功能开发（Feature） |
| PR编号 | PR-XXX（创建GitHub PR后补充完整） |
| 分支名称 | feature/phase1-week3-4-rpc-codec-middleware |
| 工作主题 | Phase 1 Week 3-4 RPC/Codec/Middleware 实现 |
| 负责人 | 🤖 核心开发 A + B |
| 分支创建日期 | 2026-02-20 |
| 计划开工日期 | 2026-02-20 |
| 计划CI通过日期 | 2026-03-06 |
| 关联需求单号 | [NexKV DDD 架构实施 PR](../2026-02-18_PR-nexkv-ddd-architecture_Pre.md) |
| 架构师评审状态 | 🔄 待评审 |

**参考文档**：
- [NexKV DDD 架构实施 Pre 文档](../2026-02-18_PR-nexkv-ddd-architecture_Pre.md)
- [DDD 接口定义 Spike 文档](../../07_spike/2026-02-18_spike-nexkv-ddd-interface.md)

---

### 2. 背景与目标

#### 2.1 当前状态

**Week 1-2 已完成**（已合并到 mainline）：
- ✅ `Transport` 接口及 `Libp2pTransport` 实现
- ✅ `Stream` / `Channel` / `AsyncStream` / `AsyncChannel` 接口
- ✅ `Message` / `Extensions` 领域模型
- ✅ `LengthPrefixCodec` 长度前缀协议
- ✅ 核心单元测试（164 个测试 + 13 个集成测试）

#### 2.2 现有接口局限性

| 局限性 | 具体问题 | 影响 |
|--------|----------|------|
| **缺乏请求-响应语义** | Stream/Channel 只提供底层数据传输，无请求 ID 和响应自动匹配 | 业务层需手动管理请求-响应关联 |
| **编解码抽象不统一** | LengthPrefixCodec 只处理字节流，不处理 Message 对象 | 无法支持多种序列化格式 |
| **缺乏横切关注点处理** | 无日志、监控、限流等通用处理机制 | 每个功能点需重复实现 |

#### 2.3 为什么需要新增这些接口

| 接口 | 解决的问题 | 核心价值 |
|------|-----------|---------|
| **RPC（统一接口）** | 简化 RPC 调用（单播+广播），自动管理请求-响应匹配 | 提升开发效率，减少样板代码 |
| **Codec** | 统一编解码抽象，支持多格式 | 协议协商，向后兼容 |
| **Middleware** | 横切关注点分离（日志、监控、限流） | 可观测性，可扩展性 |

#### 2.4 核心目标

**目标 1：实现统一 RPC 接口**
- [ ] 定义统一的 `RPC` 接口（单播 + 广播）
- [ ] 实现 `Libp2pRPC`

**目标 2：实现 Codec 接口**
- [ ] 定义 `Codec` / `StreamCodec` 接口
- [ ] 实现 `MessagePackCodec`（默认）

**目标 3：实现 Middleware 接口**
- [ ] 定义 `Middleware` / `MiddlewareChain` 接口
- [ ] 实现 `LoggingMiddleware` / `MetricsMiddleware`

**目标 4：扩展 POC 验证**
- [ ] 3 节点集群通信测试
- [ ] 性能基准测试（吞吐量 ≥ 10K ops/sec）
- [ ] 连接建立时间测试（< 2 秒）

---

### 3. 明确边界

#### 本次做（Week 3-4）

- ✅ 统一 RPC 接口定义和实现（单播 + 广播）
- ✅ Codec 接口定义
- ✅ MessagePack 实现（默认编解码器）
- ✅ Middleware 框架（接口 + Logging/Metrics 示例）
- ✅ 扩展 POC 验证

#### 本次不做（延后到后续阶段）

- ❌ Protobuf 编解码器
- ❌ Thrift 编解码器
- ❌ 熔断器中间件（CircuitBreaker）
- ❌ 重试中间件（Retry）
- ❌ 压缩中间件（Compression）
- ❌ 安全传输层（TLS/Noise）
- ❌ 迁移 `internal/rpc/` 代码

---

### 4. 接口设计

#### 4.1 RPC 统一接口（单播 + 广播）⭐

```go
// internal/domain/service/transport.go

// ResponseStrategy 广播响应策略
type ResponseStrategy int

const (
    // ResponseAll 等待所有节点响应（默认）
    // 适用场景：事务提交、配置变更（强一致性）
    ResponseAll ResponseStrategy = iota

    // ResponseMajority 等待多数派响应（> N/2）
    // 适用场景：3副本写入（W=2）、分片同步
    ResponseMajority

    // ResponseNone 不等待响应（单向发送）
    // 适用场景：日志广播、监控数据（高吞吐）
    ResponseNone
)

// BroadcastResult 广播结果（同消息广播）
type BroadcastResult struct {
    Responses    []model.Message  // 成功响应（有序列表）
    SuccessPeers []model.PeerID   // 成功节点
    FailedPeers  []model.PeerID   // 失败/超时节点
}

// WriteVResult 批量写入结果（不同消息）
type WriteVResult struct {
    Responses    map[model.PeerID]model.Message // 成功响应（按节点映射）
    SuccessPeers []model.PeerID                 // 成功节点
    FailedPeers  []model.PeerID                 // 失败/超时节点
}

// BroadcastTracker 可选的广播追踪器（一次性使用）
//
// 设计原则：Tracker 是一次性的，不复用
// - 避免 channel 泄漏风险
// - 简化并发模型
// - Tracker 本身很轻量（几十字节），不复用没有性能问题
type BroadcastTracker struct {
    taskID       string                              // 任务 ID（用于日志）
    targets      []model.PeerID                      // 目标节点列表
    responses    map[model.PeerID]model.Message      // 成功响应
    failures     map[model.PeerID]error              // 失败记录
    mu           sync.RWMutex                        // 保护并发访问
    done         chan struct{}                       // 策略满足时关闭
    fullDone     chan struct{}                       // 全部完成时关闭
    majorityDone chan struct{}                       // 多数派完成时关闭
}

// NewBroadcastTracker 创建广播追踪器
func NewBroadcastTracker(taskID string, targets []model.PeerID) *BroadcastTracker {
    // 保护性拷贝，防止外部修改
    targetsCopy := make([]model.PeerID, len(targets))
    copy(targetsCopy, targets)

    return &BroadcastTracker{
        taskID:       taskID,
        targets:      targetsCopy, // 使用拷贝
        responses:    make(map[model.PeerID]model.Message),
        failures:     make(map[model.PeerID]error),
        done:         make(chan struct{}),
        fullDone:     make(chan struct{}),
        majorityDone: make(chan struct{}),
    }
}

// ====== 等待方法 ======

// WaitFull 等待所有节点响应（包括失败的）
// 适用场景：集群关闭、全局同步
func (t *BroadcastTracker) WaitFull(ctx context.Context) error {
    select {
    case <-t.fullDone:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}

// WaitMajority 等待多数派（> N/2）节点响应
// 适用场景：与 ResponseMajority 策略配合
// 优化：使用 channel 而非轮询，零 CPU 开销
func (t *BroadcastTracker) WaitMajority(ctx context.Context) error {
    // 快速路径：先检查当前状态
    t.mu.RLock()
    majority := len(t.targets)/2 + 1
    if len(t.responses) >= majority || len(t.targets) == 0 {
        t.mu.RUnlock()
        return nil
    }
    t.mu.RUnlock()

    // 等待 majorityDone channel（零 CPU 开销）
    select {
    case <-t.majorityDone:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}

// WaitStrategy 等待指定策略满足
// 通用方法，根据传入的 strategy 等待
func (t *BroadcastTracker) WaitStrategy(ctx context.Context, strategy ResponseStrategy) error {
    switch strategy {
    case ResponseAll:
        return t.WaitFull(ctx)
    case ResponseMajority:
        return t.WaitMajority(ctx)
    case ResponseNone:
        return nil // 立即返回
    default:
        return ErrInvalidStrategy
    }
}

// ====== 状态查询方法 ======

// Stats 获取实时统计信息
func (t *BroadcastTracker) Stats() (success, failed, pending int) {
    t.mu.RLock()
    defer t.mu.RUnlock()
    return len(t.responses), len(t.failures),
        len(t.targets) - len(t.responses) - len(t.failures)
}

// IsDone 检查策略是否已满足
func (t *BroadcastTracker) IsDone() bool {
    select {
    case <-t.done:
        return true
    default:
        return false
    }
}

// IsMajorityReached 检查是否已达到多数派
func (t *BroadcastTracker) IsMajorityReached() bool {
    t.mu.RLock()
    defer t.mu.RUnlock()
    majority := len(t.targets)/2 + 1
    return len(t.responses) >= majority
}

// IsFullDone 检查是否全部完成
func (t *BroadcastTracker) IsFullDone() bool {
    select {
    case <-t.fullDone:
        return true
    default:
        return false
    }
}

// ====== 内部方法（由 RPC 实现调用）======

// RecordSuccess 记录成功响应（由 RPC 实现调用）
// 线程安全，自动更新状态并关闭 channel
func (t *BroadcastTracker) RecordSuccess(peer model.PeerID, resp model.Message) {
    t.mu.Lock()
    defer t.mu.Unlock()

    // 1. 记录响应
    t.responses[peer] = resp

    // 2. 检查是否满足 Majority 策略
    majority := len(t.targets)/2 + 1
    if len(t.responses) >= majority {
        // 关闭 majorityDone channel（仅关闭一次）
        select {
        case <-t.majorityDone:
            // 已经关闭，跳过
        default:
            close(t.majorityDone)
        }
    }

    // 3. 检查是否全部完成
    t.checkFullDone()
}

// RecordFailure 记录失败响应（由 RPC 实现调用）
// 线程安全，自动更新状态并关闭 channel
func (t *BroadcastTracker) RecordFailure(peer model.PeerID, err error) {
    t.mu.Lock()
    defer t.mu.Unlock()

    // 1. 记录失败
    t.failures[peer] = err

    // 2. 检查是否全部完成
    t.checkFullDone()
}

// checkFullDone 检查是否全部完成（内部方法，需持锁调用）
func (t *BroadcastTracker) checkFullDone() {
    if len(t.responses)+len(t.failures) == len(t.targets) {
        // 关闭 fullDone channel
        select {
        case <-t.fullDone:
        default:
            close(t.fullDone)
        }

        // 检查是否满足策略（关闭 done channel）
        majority := len(t.targets)/2 + 1
        if len(t.responses) >= majority {
            select {
            case <-t.done:
            default:
                close(t.done)
            }
        }
    }
}

// ============================================================================
// RPC 错误码定义
// ============================================================================

var (
    // ErrMajorityFailed 多数派未达成（ResponseMajority 策略）
    // 场景：3 节点广播，仅 1 个成功（需要 > N/2）
    ErrMajorityFailed = errors.New("rpc: majority quorum not reached")

    // ErrAllFailed 全部节点失败（ResponseAll 策略）
    // 场景：任一节点失败，请求整体失败
    ErrAllFailed = errors.New("rpc: all nodes failed")

    // ErrTimeout 请求超时
    ErrTimeout = errors.New("rpc: request timeout")

    // ErrCanceled 请求被取消（context 取消）
    ErrCanceled = errors.New("rpc: request canceled")

    // ErrPeerUnreachable 节点不可达
    ErrPeerUnreachable = errors.New("rpc: peer unreachable")

    // ErrNoHandler 无处理器（服务端未注册）
    ErrNoHandler = errors.New("rpc: no handler registered")

    // ErrMessageTooLarge 消息过大
    ErrMessageTooLarge = errors.New("rpc: message too large")

    // ErrCodecFailure 编解码失败
    ErrCodecFailure = errors.New("rpc: codec failure")

    // ErrStrategyNotMajority 策略满足但不是 Majority
    // 场景：WaitMajority 被调用，但策略满足时不是多数派
    ErrStrategyNotMajority = errors.New("rpc: strategy satisfied but not majority")

    // ErrInvalidStrategy 无效的策略
    ErrInvalidStrategy = errors.New("rpc: invalid response strategy")
)

// RPC 统一的 RPC 接口（合并原 RPC 和 MultiRPC）
//
// 统一了单播和广播两种通信模式，简化接口设计。
// - 单播：Call/CallAsync/OnRequest/OnRequestChan
// - 广播：BroadcastCall/BroadcastAsync/WriteV/WriteVCall（支持 ResponseStrategy + BroadcastTracker）
type RPC interface {
    // ====== 单播 ======
    // 同步调用（阻塞等响应）
    Call(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error)

    // 异步调用（不阻塞，回调返回）
    CallAsync(ctx context.Context, to model.PeerID, req model.Message, cb func(model.Message, error)) error

    // 函数式处理（服务端注册处理器）
    OnRequest(handler func(ctx context.Context, from model.PeerID, req model.Message) model.Message) error

    // Channel 模式接收请求
    OnRequestChan() <-chan RequestMsg

    // ====== 广播 ======
    // 同消息广播：支持响应策略 + 可选追踪器
    // - strategy: 响应策略（All/Majority/None）
    // - tracker: 可选追踪器，nil 表示不追踪
    BroadcastCall(
        ctx context.Context,
        to []model.PeerID,
        req model.Message,
        strategy ResponseStrategy,
        tracker *BroadcastTracker,
    ) (BroadcastResult, error)

    // 同消息广播：异步回调 + 可选追踪器
    BroadcastAsync(
        ctx context.Context,
        to []model.PeerID,
        req model.Message,
        strategy ResponseStrategy,
        tracker *BroadcastTracker,
        cb func(from model.PeerID, resp model.Message, err error),
    ) error

    // 不同消息群发：WriteV（单向，不等待响应，等价于 ResponseNone）
    // 注意：WriteV 是 "Write Vector" 的缩写，表示批量写入多个目标节点
    WriteV(ctx context.Context, targets []model.PeerID, msgs []model.Message, tracker *BroadcastTracker) error

    // 不同消息群发：支持响应策略 + 可选追踪器
    WriteVCall(
        ctx context.Context,
        targets []model.PeerID,
        msgs []model.Message,
        strategy ResponseStrategy,
        tracker *BroadcastTracker,
    ) (WriteVResult, error)

    // ====== 生命周期 ======
    Close() error
}

// RequestMsg 用于 Channel 接收请求
type RequestMsg struct {
    Ctx    context.Context
    From   model.PeerID
    Req    model.Message
    RespCh chan ResponseMsg
}

// ResponseMsg 响应消息
type ResponseMsg struct {
    Msg model.Message
    Err error
}
```

**设计说明**：
- **单播和广播统一**：单播和广播是 RPC 的不同调用方式，不是不同的抽象
- **接口数量减少**：从 2 个（RPC + MultiRPC）减少到 1 个
- **ResponseStrategy**：支持 All/Majority/None 三种响应策略，贴合分布式 KV 场景
- **与 spike 文档一致**：对齐 `docs/07_spike/2026-02-18_spike-nexkv-ddd-interface.md` v18.4

**ResponseStrategy 适用场景**：

| 策略 | 等待条件 | 适用场景 | 示例 |
|------|---------|---------|------|
| `ResponseAll` | 全部成功 | 事务提交、配置变更 | 3 节点全部确认 |
| `ResponseMajority` | > N/2 成功 | 3副本写入（W=2） | 3 节点 2 成功即可 |
| `ResponseNone` | 不等待 | 日志广播、监控数据 | 立即返回 |

**请求 ID 生成策略**：
```go
// RequestID 请求唯一标识符
// 格式: {NodeID}-{Timestamp:08x}-{Sequence:04x}
// 示例: node-001-65d4a3f0-0001
//
// 设计说明：
// - nodeID: 节点唯一标识，确保跨节点不冲突
// - timestamp: Unix 时间戳（16 进制，8 位），支持跨节点时间排序
// - sequence: 自增序列号（16 进制，4 位），每秒最多 65535 个请求
//
// 优势：
// - 固定宽度：便于解析和索引
// - 16 进制：减少长度（vs 10 进制）
// - 时间排序：支持分布式追踪按时间排序
type RequestID string

// 请求 ID 生成器（线程安全 + 时钟漂移保护）
type RequestIDGenerator struct {
    nodeID       string         // 节点 ID（启动时分配）
    lastSecond   atomic.Int64   // 上次生成时间戳（秒）
    secondSeq    atomic.Uint32  // 当前秒内序列号
}

// NewRequestIDGenerator 创建请求 ID 生成器
func NewRequestIDGenerator(nodeID string) *RequestIDGenerator {
    return &RequestIDGenerator{
        nodeID:     nodeID,
        lastSecond: atomic.Int64{},
        secondSeq:  atomic.Uint32{},
    }
}

// Next 生成下一个请求 ID（线程安全 + 时钟漂移保护）
//
// 时钟回退处理策略：
// - 当检测到系统时间回退（now < lastSecond）时，使用 lastSecond 作为时间戳
// - 这保证了 RequestID 单调递增，避免 ID 冲突
// - 场景：NTP 同步、闰秒、手动修改系统时间
func (g *RequestIDGenerator) Next() RequestID {
    now := time.Now().Unix()

    // 时钟漂移保护：检测时间回退
    for {
        lastSec := g.lastSecond.Load()

        if now > lastSec {
            // 时间前进：正常跨秒
            if g.lastSecond.CompareAndSwap(lastSec, now) {
                g.secondSeq.Store(0)
                break
            }
            // CAS 失败，重试
            continue
        }

        if now == lastSec {
            // 同一秒：继续递增序列号
            break
        }

        // now < lastSec：时间回退！
        // 策略：使用 lastSec 保证单调递增
        now = lastSec
        break
    }

    // 原子递增序列号
    seq := g.secondSeq.Add(1)

    // 格式化：{NodeID}-{Timestamp:08x}-{Sequence:04x}
    return RequestID(fmt.Sprintf("%s-%08x-%04x", g.nodeID, now, seq))
}

// ParseRequestID 解析请求 ID（用于日志和调试）
func ParseRequestID(id RequestID) (nodeID string, timestamp int64, sequence uint32, err error) {
    parts := strings.Split(string(id), "-")
    if len(parts) < 3 {
        return "", 0, 0, errors.New("invalid request ID format: expected {NodeID}-{Timestamp}-{Sequence}")
    }

    // 解析时间戳（倒数第二部分）
    tsHex := parts[len(parts)-2]
    ts, err := strconv.ParseInt(tsHex, 16, 64)
    if err != nil {
        return "", 0, 0, fmt.Errorf("invalid timestamp: %w", err)
    }

    // 解析序列号（最后一部分）
    seqHex := parts[len(parts)-1]
    seq, err := strconv.ParseUint(seqHex, 16, 32)
    if err != nil {
        return "", 0, 0, fmt.Errorf("invalid sequence: %w", err)
    }

    // 节点 ID（前面所有部分）
    nodeID = strings.Join(parts[:len(parts)-2], "-")

    return nodeID, ts, uint32(seq), nil
}

// Time 返回请求 ID 中的时间戳（用于排序）
func (id RequestID) Time() time.Time {
    _, ts, _, err := ParseRequestID(id)
    if err != nil {
        return time.Time{}
    }
    return time.Unix(ts, 0)
}
```

**性能特性**：
- 纯内存操作，CAS 保证线程安全
- 每秒最多 65,535 个请求（超过后序列号继续递增，只是 16 进制显示超过 4 位）
- 支持跨节点时间排序（分布式追踪友好）

**默认配置**：
```go
// RPCConfig RPC 默认配置
type RPCConfig struct {
    // 超时配置
    CallTimeout         time.Duration // 单播调用超时，默认 30s
    BroadcastTimeout    time.Duration // 广播调用超时，默认 60s
    ConnectTimeout      time.Duration // 连接超时，默认 10s

    // 重试配置
    MaxRetries          int           // 最大重试次数，默认 3
    RetryBackoff        time.Duration // 重试退避时间，默认 1s

    // 并发配置
    MaxConcurrentCalls  int           // 最大并发调用数，默认 1000
    RequestBufferSize   int           // 请求缓冲区大小，默认 256
}

// DefaultRPCConfig 返回默认配置
func DefaultRPCConfig() *RPCConfig {
    return &RPCConfig{
        CallTimeout:        30 * time.Second,
        BroadcastTimeout:   60 * time.Second,
        ConnectTimeout:     10 * time.Second,
        MaxRetries:         3,
        RetryBackoff:       time.Second,
        MaxConcurrentCalls: 1000,
        RequestBufferSize:  256,
    }
}
```

#### 4.2 Codec 接口

```go
// Codec 消息编解码接口
type Codec interface {
    // Encode 编码消息为字节切片
    Encode(msg model.Message) ([]byte, error)

    // Decode 解码字节切片为消息
    Decode(data []byte) (model.Message, error)

    // Name 返回编解码器名称（如 "msgpack"）
    Name() string

    // Version 返回编解码器版本（如 "v1"），用于协议协商
    Version() string
}

// StreamCodec 流式编解码接口（支持分帧）
type StreamCodec interface {
    Codec

    // EncodeToWriter 编码并写入 Writer
    EncodeToWriter(w io.Writer, msg model.Message) error

    // DecodeFromReader 从 Reader 解码
    DecodeFromReader(r io.Reader) (model.Message, error)
}
```

**协议协商说明**：
- 客户端连接时发送支持的 Codec 列表：`["msgpack/v1", "json/v1"]`
- 服务端选择第一个支持的 Codec 返回
- 如果没有匹配，返回错误并断开连接

#### 4.3 Middleware 接口

```go
// SendFunc 发送函数签名
type SendFunc func(ctx context.Context, peer model.PeerID, msg model.Message) error

// ReceiveFunc 接收函数签名
type ReceiveFunc func(ctx context.Context, peer model.PeerID, msg model.Message) error

// Middleware 中间件接口（拦截器模式）
type Middleware interface {
    // Name 中间件名称
    Name() string

    // InterceptSend 拦截发送消息
    InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next SendFunc) error

    // InterceptReceive 拦截接收消息
    InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next ReceiveFunc) error
}

// MiddlewareChain 中间件链管理器
//
// 并发安全策略：
// 1. 使用读写锁（sync.RWMutex）保护中间件列表
// 2. Execute 时获取快照执行，避免持锁时间过长
// 3. 提供 Freeze 方法，冻结后禁止修改（高性能场景）
type MiddlewareChain interface {
    // Use 添加中间件到链尾
    Use(middleware Middleware) error

    // UseFirst 添加中间件到链头（优先执行）
    // 场景：日志中间件通常需要在最外层
    UseFirst(middleware Middleware) error

    // UseAt 在指定位置插入中间件
    // index=0 表示链头，index=len 表示链尾
    UseAt(index int, middleware Middleware) error

    // Remove 移除指定名称的中间件
    Remove(name string) error

    // List 获取所有中间件列表（返回快照）
    List() []Middleware

    // Freeze 冻结中间件链，禁止后续修改
    // 冻结后 Use/UseFirst/UseAt/Remove/Clear 返回 ErrChainFrozen
    // 适用场景：启动完成后调用，避免运行时修改开销
    Freeze()

    // IsFrozen 检查是否已冻结
    IsFrozen() bool

    // ExecuteSend 执行发送中间件链
    ExecuteSend(ctx context.Context, peer model.PeerID, msg model.Message, final SendFunc) error

    // ExecuteReceive 执行接收中间件链
    ExecuteReceive(ctx context.Context, peer model.PeerID, msg model.Message, final ReceiveFunc) error

    // Clear 清空所有中间件（冻结后返回错误）
    Clear() error
}

// ErrChainFrozen 中间件链已冻结错误
var ErrChainFrozen = errors.New("middleware chain is frozen")
```

**实现示例（并发安全）**：
```go
// middlewareChain 中间件链实现
type middlewareChain struct {
    mu          sync.RWMutex
    middlewares []Middleware
    frozen      bool
}

// ExecuteSend 执行发送中间件链（使用快照，避免持锁）
func (c *middlewareChain) ExecuteSend(ctx context.Context, peer model.PeerID, msg model.Message, final SendFunc) error {
    // 1. 获取快照（读锁）
    c.mu.RLock()
    middlewares := make([]Middleware, len(c.middlewares))
    copy(middlewares, c.middlewares)
    c.mu.RUnlock()

    // 2. 构建责任链（从后向前）
    next := final
    for i := len(middlewares) - 1; i >= 0; i-- {
        mw := middlewares[i]
        next = func(ctx context.Context, peer model.PeerID, msg model.Message, current Middleware, n SendFunc) SendFunc {
            return func(ctx context.Context, peer model.PeerID, msg model.Message) error {
                return current.InterceptSend(ctx, peer, msg, n)
            }
        }(ctx, peer, msg, mw, next)
    }

    // 3. 执行链
    return next(ctx, peer, msg)
}

// Use 添加中间件（写锁）
func (c *middlewareChain) Use(middleware Middleware) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    if c.frozen {
        return ErrChainFrozen
    }

    c.middlewares = append(c.middlewares, middleware)
    return nil
}

// Freeze 冻结中间件链
func (c *middlewareChain) Freeze() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.frozen = true
}
```

---

### 5. 使用场景

#### 5.1 RPC 单播场景

```go
// 场景 1：KV 读写
resp, err := rpc.Call(ctx, targetPeer, NewGetRequest("user:123"))
if err != nil {
    return err
}
getValue := resp.(*GetResponse)

// 场景 2：分布式事务
resp, err := rpc.Call(ctx, coordinator, NewPrepareRequest(tx))

// 场景 3：异步调用（高并发）
for i := 0; i < 1000; i++ {
    rpc.CallAsync(ctx, targetPeer, reqs[i], func(resp Message, err error) {
        // 处理响应
    })
}

// 场景 4：服务端注册处理器
rpc.OnRequest(func(ctx context.Context, from PeerID, req Message) Message {
    // 处理请求
    return NewResponse(result)
})
```

#### 5.2 RPC 广播场景

```go
// 场景 1：3 副本写入（ResponseMajority，W=2）
// 只需 2/3 成功即可返回，提高写入吞吐
replicas := []PeerID{"node-1", "node-2", "node-3"}
result, err := rpc.BroadcastCall(ctx, replicas, putRequest, ResponseMajority, nil)
if err == ErrMajorityFailed {
    // 多数派未达成，需要处理
    return err
}
// result.SuccessPeers 包含成功的节点（如 ["node-1", "node-2"]）
log.Printf("write succeeded on %d nodes", len(result.SuccessPeers))

// 场景 2：事务提交（ResponseAll，全部成功）
participants := []PeerID{"node-1", "node-2", "node-3"}
result, err := rpc.BroadcastCall(ctx, participants, commitRequest, ResponseAll, nil)
if err != nil {
    // 任一节点失败，事务回滚
    return err
}
// 所有节点都成功
log.Printf("transaction committed on all %d nodes", len(result.Responses))

// 场景 3：日志广播（ResponseNone，不等待）
// 高吞吐场景，立即返回
allPeers := []PeerID{"node-1", "node-2", "node-3", "node-4", "node-5"}
_, err := rpc.BroadcastCall(ctx, allPeers, logEntry, ResponseNone, nil)
// 不关心响应，立即返回

// 场景 4：集群状态同步（异步 + ResponseMajority）
rpc.BroadcastAsync(ctx, allPeers, stateUpdate, ResponseMajority, nil, func(from PeerID, resp Message, err error) {
    if err != nil {
        log.Printf("node %s failed: %v", from, err)
    }
})

// 场景 5：批量写入（不同消息，ResponseMajority）
msgs := []Message{put1, put2, put3}
targets := []PeerID{"node-1", "node-2", "node-3"}
result, err := rpc.WriteVCall(ctx, targets, msgs, ResponseMajority, nil)
if err != nil {
    return err
}
// result.Responses[node-1] = 对应响应
log.Printf("batch write succeeded on %d nodes", len(result.SuccessPeers))

// 场景 6：单向批量写入（等价于 ResponseNone）
err := rpc.WriteV(ctx, targets, msgs, nil)  // 内部使用 ResponseNone

// 场景 7：使用 BroadcastTracker 追踪广播进度（可选）
// 适用场景：需要监控实时进度或等待全部节点响应
tracker := NewBroadcastTracker("write-shard-001", replicas)

// 发起广播（ResponseMajority 策略）
go func() {
    result, _ := rpc.BroadcastCall(ctx, replicas, putRequest, ResponseMajority, tracker)
    // 策略满足后返回（如 2/3 成功）
}()

// 实时监控进度
for !tracker.IsDone() {
    success, failed, pending := tracker.Stats()
    log.Printf("progress: %d success, %d failed, %d pending",
        success, failed, pending)
    time.Sleep(100 * time.Millisecond)
}

// 等待全部完成（包括失败的节点）
// 适用场景：需要在所有节点响应后执行清理操作
err := tracker.WaitFull(ctx)
if err != nil {
    log.Printf("wait full timeout: %v", err)
}
log.Printf("all nodes responded, broadcast completed")

// 场景 8：异步广播 + 追踪器
tracker := NewBroadcastTracker("async-broadcast-001", allPeers)
rpc.BroadcastAsync(ctx, allPeers, stateUpdate, ResponseMajority, tracker, func(from PeerID, resp Message, err error) {
    if err != nil {
        log.Printf("node %s failed: %v", from, err)
    }
})

// 后台监控进度
go func() {
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            if tracker.IsDone() {
                success, failed, _ := tracker.Stats()
                log.Printf("broadcast done: %d success, %d failed", success, failed)
                return
            }
        case <-ctx.Done():
            return
        }
    }
}()

// 场景 9：3副本写入后异步同步第3个副本
// ResponseMajority 策略返回后，后台继续等待全部完成
replicas := []PeerID{"node-1", "node-2", "node-3"}
tracker := NewBroadcastTracker("write-001", replicas)

result, err := rpc.BroadcastCall(ctx, replicas, putRequest, ResponseMajority, tracker)
if err != nil {
    return err
}
// 此时已满足 Majority（2/3），可以返回给客户端

// 异步等待全部完成（后台同步第3个副本）
go func() {
    waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    if err := tracker.WaitFull(waitCtx); err == nil {
        log.Printf("All replicas synced: %s", tracker.taskID)
    }
}()

// 场景 10：使用 WaitStrategy 通用方法
// 根据策略自动选择等待方式
strategy := ResponseMajority // 可配置
tracker := NewBroadcastTracker("configurable-write", replicas)

result, err := rpc.BroadcastCall(ctx, replicas, putRequest, strategy, tracker)
if err != nil {
    return err
}

// 通用等待（根据 strategy 自动选择 WaitFull/WaitMajority/立即返回）
go func() {
    if err := tracker.WaitStrategy(ctx, strategy); err == nil {
        log.Printf("Strategy %v satisfied", strategy)
    }
}()

// 场景 11：非阻塞状态检查
tracker := NewBroadcastTracker("poll-check", replicas)

// 发起广播...
go func() {
    rpc.BroadcastCall(ctx, replicas, putRequest, ResponseMajority, tracker)
}()

// 定期轮询状态（非阻塞）
for {
    if tracker.IsMajorityReached() {
        log.Println("Majority reached, safe to proceed")
        break
    }
    if tracker.IsFullDone() {
        log.Println("All nodes responded")
        break
    }
    time.Sleep(50 * time.Millisecond)
}
```

#### 5.3 Codec 场景

```go
// 场景 1：MessagePack 编解码（默认）
codec := NewMessagePackCodec()
data, err := codec.Encode(msg)
decoded, err := codec.Decode(data)
// codec.Name() = "msgpack"
// codec.Version() = "v1"

// 场景 2：流式编解码（网络传输）
streamCodec := NewMessagePackStreamCodec()
err := streamCodec.EncodeToWriter(conn, msg)
decoded, err := streamCodec.DecodeFromReader(conn)
```

#### 5.4 Middleware 场景

```go
// 场景 1：生产环境标配
transport.Use(NewLoggingMiddleware(logger))
transport.Use(NewMetricsMiddleware(metrics))
transport.Use(NewRateLimitMiddleware(limiter))

// 场景 2：高可用场景
transport.Use(NewCircuitBreakerMiddleware(config))
transport.Use(NewRetryMiddleware(3, time.Second))

// 中间件执行流程：
// 发送请求 → Logging → Metrics → RateLimit → CircuitBreaker → Retry → 实际发送
// 响应返回 ← Logging ← Metrics ← RateLimit ← CircuitBreaker ← Retry ← 实际响应
```

---

### 6. 测试方案

#### 6.1 单元测试

| 测试项 | 测试文件 | 测试内容 | 验收标准 |
|--------|----------|----------|---------|
| RPC Call | `libp2p_rpc_test.go` | 同步单播调用 | 返回正确响应 |
| RPC CallAsync | `libp2p_rpc_test.go` | 异步单播调用 | 回调正确触发 |
| **ResponseAll 策略** | `libp2p_rpc_test.go` | 3 节点全部响应 | 返回 3 个响应 |
| **ResponseAll 策略** | `libp2p_rpc_test.go` | 1 节点失败 | 返回错误 |
| **ResponseMajority 策略** | `libp2p_rpc_test.go` | 3 节点 2 成功 | 返回成功，2 个响应 |
| **ResponseMajority 策略** | `libp2p_rpc_test.go` | 3 节点 1 成功 | 返回 ErrMajorityFailed |
| **ResponseNone 策略** | `libp2p_rpc_test.go` | 立即返回 | 无阻塞，返回空结果 |
| RPC WriteV | `libp2p_rpc_test.go` | 单向群发 | 消息发送成功 |
| RPC 超时控制 | `libp2p_rpc_test.go` | 超时场景 | 返回 ErrTimeout |
| RPC 并发安全 | `libp2p_rpc_test.go` | 并发请求 | 无数据竞争 |
| **BroadcastTracker Stats** | `broadcast_tracker_test.go` | 实时统计 | 正确返回成功/失败/待处理数量 |
| **BroadcastTracker WaitFull** | `broadcast_tracker_test.go` | 等待全部完成 | 所有节点响应后返回 |
| **BroadcastTracker WaitMajority** | `broadcast_tracker_test.go` | 等待多数派 | 2/3 成功后返回 |
| **BroadcastTracker WaitMajority** | `broadcast_tracker_test.go` | targets 为空 | 立即返回 nil（0/0 = Majority） |
| **BroadcastTracker WaitStrategy** | `broadcast_tracker_test.go` | ResponseAll | 等同于 WaitFull |
| **BroadcastTracker WaitStrategy** | `broadcast_tracker_test.go` | ResponseMajority | 等同于 WaitMajority |
| **BroadcastTracker WaitStrategy** | `broadcast_tracker_test.go` | ResponseNone | 立即返回 nil |
| **BroadcastTracker WaitStrategy** | `broadcast_tracker_test.go` | 无效策略 | 返回 ErrInvalidStrategy |
| **BroadcastTracker IsDone** | `broadcast_tracker_test.go` | 策略满足检查 | 策略满足后返回 true |
| **BroadcastTracker IsMajorityReached** | `broadcast_tracker_test.go` | 2/3 成功 | 返回 true |
| **BroadcastTracker IsMajorityReached** | `broadcast_tracker_test.go` | 1/3 成功 | 返回 false |
| **BroadcastTracker IsFullDone** | `broadcast_tracker_test.go` | 全部完成 | 返回 true |
| **BroadcastTracker IsFullDone** | `broadcast_tracker_test.go` | 部分完成 | 返回 false |
| **BroadcastTracker Stats** | `broadcast_tracker_test.go` | 并发调用 | 结果一致 |
| **BroadcastTracker WaitFull** | `broadcast_tracker_test.go` | ctx 取消 | 返回 ctx.Err() |
| **BroadcastTracker WaitMajority** | `broadcast_tracker_test.go` | ctx 取消 | 返回 ctx.Err() |
| **BroadcastTracker RecordSuccess** | `broadcast_tracker_test.go` | 记录成功响应 | 更新 responses，关闭 majorityDone |
| **BroadcastTracker RecordSuccess** | `broadcast_tracker_test.go` | 2/3 成功后 | majorityDone channel 关闭 |
| **BroadcastTracker RecordFailure** | `broadcast_tracker_test.go` | 记录失败响应 | 更新 failures |
| **BroadcastTracker RecordFailure** | `broadcast_tracker_test.go` | 全部完成后 | fullDone/done channel 关闭 |
| **BroadcastTracker 并发记录** | `broadcast_tracker_test.go` | 并发 RecordSuccess | 无数据竞争，channel 仅关闭一次 |
| **BroadcastTracker nil** | `libp2p_rpc_test.go` | 不追踪 | tracker=nil 正常工作 |
| **RequestIDGenerator** | `request_id_test.go` | 时钟回退检测 | 使用 lastSec 保证单调 |
| **RequestIDGenerator** | `request_id_test.go` | 并发生成 | 无重复 ID |
| **MessagePack 编解码** | `messagepack_codec_test.go` | 一致性 | Encode→Decode 原值 |
| **MessagePack 大消息** | `messagepack_codec_test.go` | 10MB 消息 | 正确处理 |
| Middleware 链 | `middleware_chain_test.go` | 顺序执行 | 按注册顺序执行 |
| Middleware 错误 | `middleware_chain_test.go` | 错误传播 | 错误正确终止链 |
| Middleware 并发安全 | `middleware_chain_test.go` | 运行时修改 | 读写锁保护正确 |
| LoggingMiddleware | `logging_middleware_test.go` | 日志记录 | 记录所有消息 |
| MetricsMiddleware | `metrics_middleware_test.go` | 指标收集 | 延迟/QPS 正确 |

#### 6.2 负面测试（异常场景）

| 测试项 | 测试文件 | 测试内容 | 验收标准 |
|--------|----------|----------|---------|
| 网络分区 | `libp2p_rpc_negative_test.go` | 模拟网络断开 | 返回 ErrPeerUnreachable |
| 部分节点无响应 | `libp2p_rpc_negative_test.go` | 广播时部分节点超时 | 返回部分成功结果 |
| 超大消息 | `libp2p_rpc_negative_test.go` | 发送 > MaxMessageSize | 返回 ErrMessageTooLarge |
| 格式错误消息 | `libp2p_rpc_negative_test.go` | 发送无效编码数据 | 返回 ErrCodecFailure |
| 请求取消 | `libp2p_rpc_negative_test.go` | Context 取消 | 返回 ErrCanceled |
| 服务端无处理器 | `libp2p_rpc_negative_test.go` | 未注册处理器 | 返回 ErrNoHandler |

#### 6.3 基准测试

**测试条件**：
- 消息大小：1KB（默认）、10KB、100KB
- 并发连接数：10、100、1000
- 测试持续时间：60 秒

| 测试项 | 指标 | 目标 | 测试条件 |
|--------|------|------|---------|
| RPC 单播延迟 | P99 | < 5ms | 本地回环，1KB 消息，100 并发 |
| RPC 单播延迟 | P99 | < 50ms | 局域网，1KB 消息，100 并发 |
| RPC 广播延迟 | P99 | < 100ms | 3 节点，1KB 消息，50 并发 |
| **MessagePack 编解码延迟** | 平均 | < 50μs/op | 1KB 消息 |
| Middleware 开销 | 额外延迟 | < 10% | 3 个中间件 |
| 单播吞吐 | QPS | ≥ 10K ops/sec | 本地回环，1KB 消息，100 并发 |
| 广播吞吐 | QPS | ≥ 5K ops/sec | 3 节点，1KB 消息，50 并发 |

#### 6.4 POC 验证

| 验证项 | 测试方法 | 目标 |
|--------|----------|------|
| 3 节点集群通信 | E2E 测试 | 所有节点可互相通信 |
| 节点发现时间 | 集成测试 | < 5 秒（mDNS） |
| 性能基准 | Benchmark | ≥ 10K ops/sec |
| 连接建立时间 | 集成测试 | < 2 秒 |

---

### 7. 目录结构

```
internal/
├── domain/
│   ├── model/
│   │   ├── peer.go               # ✅ 已存在
│   │   └── message.go            # ✅ 已存在
│   └── service/
│       └── transport.go          # ⭐ 扩展：添加 RPC, Codec, Middleware 接口定义
│                                  #    - RPC interface
│                                  #    - Codec interface
│                                  #    - StreamCodec interface
│                                  #    - Middleware interface
│                                  #    - MiddlewareChain interface
│                                  #    - RequestID Generator
│
├── infrastructure/transport/
│   ├── libp2p_transport.go       # ✅ 已存在
│   ├── libp2p_stream.go          # ✅ 已存在
│   ├── libp2p_channel.go         # ✅ 已存在
│   ├── libp2p_async_stream.go    # ✅ 已存在
│   ├── libp2p_async_channel.go   # ✅ 已存在
│   ├── libp2p_discovery.go       # ✅ 已存在
│   ├── async_common.go           # ✅ 已存在
│   ├── length_prefix_codec.go    # ✅ 已存在
│   │
│   ├── libp2p_rpc.go             # ⭐ 新增：RPC 实现
│   ├── libp2p_rpc_test.go        # ⭐ 新增：RPC 测试
│   │
│   ├── messagepack_codec.go      # ⭐ 新增：MessagePack 编解码实现
│   ├── messagepack_codec_test.go # ⭐ 新增：MessagePack 测试
│   │
│   ├── middleware_chain.go       # ⭐ 新增：MiddlewareChain 实现
│   ├── middleware_chain_test.go  # ⭐ 新增：中间件链测试
│   ├── logging_middleware.go     # ⭐ 新增：日志中间件实现
│   └── metrics_middleware.go     # ⭐ 新增：监控中间件实现
│
└── transport/                    # 现有代码（保留）
```

**DDD 分层说明**：

| 层 | 目录 | 职责 | 本 PR 内容 |
|----|------|------|-----------|
| **领域层** | `domain/service/` | 接口定义（抽象） | RPC/Codec/Middleware 接口 |
| **基础设施层** | `infrastructure/transport/` | 具体实现 | Libp2pRPC、MessagePackCodec、MiddlewareChain |

**新增文件说明**：

| 文件 | 层 | 说明 |
|------|----|----|
| `transport.go`（扩展） | 领域层 | 接口定义 |
| `libp2p_rpc.go` | 基础设施层 | RPC 实现 |
| `messagepack_codec.go` | 基础设施层 | MessagePack 实现 |
| `middleware_chain.go` | 基础设施层 | MiddlewareChain 实现 |
| `logging_middleware.go` | 基础设施层 | 日志中间件实现 |
| `metrics_middleware.go` | 基础设施层 | 监控中间件实现 |

---

### 8. 依赖与资源

#### 8.1 外部依赖

| 依赖 | 版本 | 用途 | 状态 |
|------|------|------|------|
| github.com/vmihailenco/msgpack/v5 | v5.4.1 | MessagePack 编解码 | 待添加 |
| github.com/sourcegraph/conc | 已存在 | 并发控制 | ✅ 已有 |
| github.com/sirupsen/logrus | 已存在 | 结构化日志 | ✅ 已有 |

#### 8.2 人力需求

| 角色 | 工作量 | 说明 |
|------|--------|------|
| 核心开发 A | 3 天 | RPC 实现 |
| 核心开发 B | 2 天 | MessagePack Codec + Middleware 实现 |
| 测试工程师 | 1 天 | 扩展 POC 验证 |

---

### 9. 风险评估

| 风险项 | 风险等级 | 影响程度 | 缓解措施 |
|--------|---------|---------|---------|
| MessagePack 性能不达标 | 中 | 中 | 基准测试验证，必要时优化编解码逻辑 |
| 中间件链开销过大 | 低 | 中 | 基准测试验证，必要时优化 |
| 3 节点集群测试复杂 | 中 | 低 | 先单节点测试，再扩展到 3 节点 |
| 请求 ID 冲突 | 低 | 高 | 使用 atomic 自增 ID + 节点 ID |

---

### 10. 验收标准（M1 里程碑）

#### 10.1 功能验收

- [ ] RPC Call/CallAsync/OnRequest/OnRequestChan 功能正常
- [ ] RPC BroadcastCall/BroadcastAsync/WriteV/WriteVCall 功能正常
- [ ] MessagePack 编解码一致性验证通过
- [ ] Middleware 链式执行正确
- [ ] Middleware 并发安全验证通过
- [ ] LoggingMiddleware/MetricsMiddleware 功能正常

#### 10.2 质量验收

- [ ] 所有单元测试通过（覆盖率 ≥ 80%）
- [ ] 代码 lint 检查通过
- [ ] 代码格式化检查通过
- [ ] 无内存泄漏（race detector 通过）

#### 10.3 性能验收

- [ ] 扩展 POC 验证通过
  - 3 节点集群通信正常
  - 吞吐量 ≥ 10K ops/sec
  - 连接建立时间 < 2 秒
- [ ] CI 流水线通过

---

## 第二部分：后置部分（开发完成后填写）

### 11. 开发总结

> 待补充

### 12. 测试报告

> 待补充

### 13. Code Review 报告

> 待补充

---

**文档状态**: 🔄 草稿（待架构师评审）
**文档版本**: v2.4（BroadcastTracker 状态更新方法）
**最后更新**: 2026-02-20
**维护者**: 🤖 核心开发 A + B

---

## 文档修订历史

| 版本 | 日期 | 变更内容 |
|------|------|----------|
| v1.0 | 2026-02-20 | 初始版本 |
| v1.1 | 2026-02-20 | 合并 RPC + MultiRPC 为统一接口 |
| v1.2 | 2026-02-20 | 根据 Review 意见修复 |
| v1.3 | 2026-02-20 | 添加 Thrift/Protobuf 编解码器支持 |
| v1.4 | 2026-02-20 | RequestID 设计优化（去掉 Timestamp） |
| v1.5 | 2026-02-20 | 目录结构修复（DDD 分层正确） |
| v1.6 | 2026-02-20 | 聚焦 MessagePack 实现 |
| v1.7 | 2026-02-20 | RequestID 恢复时间戳（支持分布式追踪排序） |
| v1.8 | 2026-02-20 | Middleware 并发安全设计 |
| v1.9 | 2026-02-20 | 删除 Protobuf/Thrift 预留 |
| v2.0 | 2026-02-20 | ResponseStrategy 优化 |
| v2.1 | 2026-02-20 | BroadcastTracker + WriteVResult 优化 |
| v2.2 | 2026-02-20 | BroadcastTracker 完整方法设计 |
| v2.3 | 2026-02-20 | Code Review 问题修复（P0/P1） |
| v2.4 | 2026-02-20 | BroadcastTracker 状态更新方法：<br/>- **新增 `RecordSuccess()`**：RPC 实现调用，记录成功响应<br/>- **新增 `RecordFailure()`**：RPC 实现调用，记录失败响应<br/>- **新增 `checkFullDone()`**：内部方法，自动关闭 channel<br/>- **完整的生命周期管理**：状态更新 → channel 关闭 → 等待返回 |
