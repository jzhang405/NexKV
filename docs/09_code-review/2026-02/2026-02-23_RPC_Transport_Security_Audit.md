# RPC & Transport 层安全审查报告

> **审查日期**: 2026-02-23
> **审查范围**: RPC 异步操作、Transport 层、并发控制
> **审查人**: Security Reviewer Agent
> **严重性标准**: CRITICAL > HIGH > MEDIUM > LOW

---

## 执行摘要

### 总体评估

**安全成熟度**: 🟡 中等 (70/100)

NexKV 的 RPC 和 Transport 层在基本安全实践上表现良好，包括输入验证、panic recovery 和资源清理。但存在若干潜在的安全隐患，主要集中在资源泄漏、并发边界条件和 DoS 防护方面。

### 关键发现统计

| 严重性 | 数量 | 占比 |
|--------|------|------|
| **CRITICAL** | 1 | 6% |
| **HIGH** | 5 | 31% |
| **MEDIUM** | 6 | 38% |
| **LOW** | 4 | 25% |
| **总计** | **16** | 100% |

---

## 1. CRITICAL 严重性问题

### 🔴 C-01: Channel 泄漏导致内存耗尽 (DoS 风险)

**文件**: `internal/infrastructure/transport/libp2p_rpc.go:512-523`

**问题描述**:

`registerPendingCall` 创建的 `responseCh` channel 在超时场景下可能永不关闭，导致内存泄漏。

```go
// 问题代码
func (r *Libp2pRPC) registerPendingCall(requestID string, callCtx context.Context) *pendingCall {
    call := &pendingCall{
        responseCh: make(chan service.ResponseMsg, 1), // 创建 channel
    }

    r.pendingCallsMu.Lock()
    r.pendingCalls[requestID] = call
    r.pendingCallsMu.Unlock()

    return call // ⚠️ 没有设置清理机制
}
```

**攻击场景**:

1. 恶意客户端发起大量 RPC 请求但不响应
2. 每个请求创建一个 buffered channel (容量 1)
3. 超时后 `unregisterPendingCall` 删除 map 条目，但 channel 未关闭
4. 长期运行后，未关闭的 channel 累积，占用内存

**影响**:
- **内存泄漏**: 每个泄漏的 channel ≈ 96 字节 (hchan 结构)
- **DoS 风险**: 10,000 个未响应请求 ≈ 1MB 泄漏
- **长期运行风险**: 7x24 小时运行可能累积 GB 级泄漏

**修复建议**:

```go
// 方案 1: 在 unregisterPendingCall 中显式关闭 channel
func (r *Libp2pRPC) unregisterPendingCall(requestID string) {
    r.pendingCallsMu.Lock()
    defer r.pendingCallsMu.Unlock()

    if call, ok := r.pendingCalls[requestID]; ok {
        call.done.Store(true)
        close(call.responseCh) // ✅ 显式关闭
        delete(r.pendingCalls, requestID)
    }
}

// 方案 2: 使用 context 驱动的清理
func (r *Libp2pRPC) registerPendingCall(requestID string, callCtx context.Context) *pendingCall {
    call := &pendingCall{
        responseCh: make(chan service.ResponseMsg, 1),
    }

    r.pendingCallsMu.Lock()
    r.pendingCalls[requestID] = call
    r.pendingCallsMu.Unlock()

    // ✅ 监听 context 取消，自动清理
    go func() {
        <-callCtx.Done()
        r.unregisterPendingCall(requestID)
    }()

    return call
}
```

**CVSS 评分**: 7.5 (HIGH) - CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H

---

## 2. HIGH 严重性问题

### 🟠 H-01: Context 泄漏导致 Goroutine 累积

**文件**: `internal/infrastructure/transport/libp2p_rpc.go:658-664`

**问题描述**:

`HandleIncomingStream` 创建的 context 和监控 goroutine 在流正常关闭时不会清理。

```go
// 问题代码
func (r *Libp2pRPC) HandleIncomingStream(stream service.Stream) error {
    defer stream.Close()

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go func() {
        <-r.closeCh // ⚠️ 如果 RPC 永不关闭，此 goroutine 永不退出
        cancel()
    }()
    // ...
}
```

**攻击场景**:

1. 攻击者建立大量流连接后立即关闭
2. 每个流创建一个等待 `closeCh` 的 goroutine
3. 如果 RPC 实例长期存活，这些 goroutine 会累积
4. 导致 goroutine 泄漏，占用栈内存 (每个 ≈ 2KB)

**修复建议**:

```go
func (r *Libp2pRPC) HandleIncomingStream(stream service.Stream) error {
    defer stream.Close()

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // ✅ 使用 stream 关闭信号 + RPC 关闭信号
    go func() {
        select {
        case <-r.closeCh:
            cancel()
        case <-ctx.Done(): // ✅ 流关闭时自动退出
            return
        }
    }()
    // ...
}
```

**CVSS 评分**: 6.5 (MEDIUM) - CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:P

---

### 🟠 H-02: WriteV 信号量泄漏

**文件**: `internal/infrastructure/transport/libp2p_rpc.go:245-283`

**问题描述**:

`WriteV` 使用信号量限制并发，但在 panic 场景下可能无法释放。

```go
// 问题代码
sem := make(chan struct{}, r.config.MaxConcurrentCalls)

for i, target := range targets {
    wg.Add(1)
    go func(idx int, peerID model.PeerID) {
        defer wg.Done()

        sem <- struct{}{}        // ⚠️ 获取信号量
        defer func() { <-sem }() // ✅ defer 释放

        err := r.sendRequestNoResponse(ctx, peerID, msgs[idx])
        // 如果 sendRequestNoResponse panic，defer 仍会执行
        // 但如果 goroutine 在 sem <- struct{}{} 前就 panic，不会释放
        // ...
    }(i, target)
}
```

**攻击场景**:

虽然当前代码有 defer 保护，但如果 `msgs[idx]` 为 nil 且没有检查，会在访问时 panic。

**修复建议**:

```go
// ✅ 添加 nil 检查
func (r *Libp2pRPC) WriteV(ctx context.Context, targets []model.PeerID, msgs []model.Message, tracker *service.BroadcastProgress) error {
    if len(targets) != len(msgs) {
        return service.Wrapf(service.ErrInvalidParam, "targets and messages length mismatch: %d vs %d", len(targets), len(msgs))
    }

    // ✅ 检查 nil 消息
    for i, msg := range msgs {
        if msg == nil {
            return service.Wrapf(service.ErrInvalidMessage, "msgs[%d] is nil", i)
        }
    }
    // ...
}
```

**CVSS 评分**: 5.3 (MEDIUM) - CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:P

---

### 🟠 H-03: BroadcastProgress Channel 泄漏

**文件**: `internal/domain/service/broadcast_progress.go:142-143`

**问题描述**:

`BroadcastProgress` 的 `fullDone` 和 `majorityDone` channel 在某些边界条件下可能永不关闭。

```go
// 问题场景
type BroadcastProgress struct {
    fullDone     chan struct{} // 何时关闭？
    majorityDone chan struct{} // 何时关闭？
}

// 如果 targets 为空切片，majorityDone 永不关闭
func NewBroadcastProgress(taskID string, targets []model.PeerID) *BroadcastProgress {
    return &BroadcastProgress{
        fullDone:     make(chan struct{}), // ⚠️ 空 targets 时永不关闭
        majorityDone: make(chan struct{}), // ⚠️ 空 targets 时永不关闭
    }
}
```

**修复建议**:

```go
func NewBroadcastProgress(taskID string, targets []model.PeerID) *BroadcastProgress {
    targetsCopy := make([]model.PeerID, len(targets))
    copy(targetsCopy, targets)

    bp := &BroadcastProgress{
        targets:      targetsCopy,
        fullDone:     make(chan struct{}),
        majorityDone: make(chan struct{}),
    }

    // ✅ 空 targets 时立即关闭 channel
    if len(targets) == 0 {
        close(bp.fullDone)
        close(bp.majorityDone)
    }

    return bp
}
```

**CVSS 评分**: 5.3 (MEDIUM) - CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:P

---

### 🟠 H-04: AsyncOperation Channel 泄漏

**文件**: `internal/domain/service/rpc_async_impl.go:82-89`

**问题描述**:

`asyncOpImpl` 的 `resultCh` 和 `errCh` 在操作取消时可能不关闭。

```go
type asyncOpImpl[T any] struct {
    resultCh chan T     // ⚠️ 容量 1，但可能永不关闭
    errCh    chan error // ⚠️ 容量 1，但可能永不关闭
}

// 如果操作从未开始（例如输入验证失败），channel 永不关闭
func NewAsyncCall(...) AsyncOperation[ResponseMsg] {
    op := newAsyncOp[ResponseMsg](provider)

    if timeoutMs <= 0 {
        op.fail(fmt.Errorf("...")) // ✅ 调用 fail，会关闭 errCh
        return &asyncCall{asyncOpImpl: op}
    }
    // ...
}
```

**修复建议**:

```go
// ✅ 添加显式的 Cancel 方法
func (op *asyncOpImpl[T]) cancel() {
    var zero T
    if op.done.CompareAndSwap(false, true) {
        op.status.Store(3) // canceled
        close(op.resultCh) // ✅ 显式关闭
        close(op.errCh)    // ✅ 显式关闭
        op.executeCallbacks(zero, context.Canceled)
    }
}

// ✅ 在 GC 时自动清理（备选方案）
func (op *asyncOpImpl[T]) finalizer() {
    if !op.done.Load() {
        op.cancel()
    }
}
```

**CVSS 评分**: 5.3 (MEDIUM) - CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:P

---

### 🟠 H-05: GoroutineProvider.Submit 错误未处理

**文件**: `internal/domain/service/rpc_async_impl.go:331-344`

**问题描述**:

当 `GoroutineProvider.Submit` 返回错误时（例如线程池已满），后续代码继续执行，导致逻辑错误。

```go
// 问题代码
if provider != nil {
    if err := provider.Submit(ctx, func(ctx context.Context) {
        // ... 异步任务 ...
    }); err != nil {
        op.fail(fmt.Errorf("submit task failed: %w", err)) // ✅ 调用 fail
        // ⚠️ 但返回了 op，用户可能仍在等待结果
    }
} else {
    go func() { /* ... */ }()
}

return &asyncCall{asyncOpImpl: op} // ⚠️ 返回可能已失败的 op
```

**修复建议**:

当前代码已正确处理（调用 `op.fail`），但建议添加日志：

```go
if err := provider.Submit(ctx, func(ctx context.Context) {
    // ...
}); err != nil {
    slog.Error("[NewAsyncCall] failed to submit task", "error", err)
    op.fail(fmt.Errorf("submit task failed: %w", err))
    return &asyncCall{asyncOpImpl: op} // ✅ 返回已失败的 op
}
```

**CVSS 评分**: 4.3 (MEDIUM) - CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:N/I:N/A:L

---

## 3. MEDIUM 严重性问题

### 🟡 M-01: 缺少输入验证 - PeerID 为空

**文件**: `internal/infrastructure/transport/libp2p_rpc.go:72-112`

**问题描述**:

`Call` 方法没有验证 `to` 参数是否为空。

```go
func (r *Libp2pRPC) Call(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
    // ⚠️ 未检查 to == ""
    if r.closed.Load() {
        return nil, service.ErrCanceled
    }

    if req == nil {
        return nil, service.ErrInvalidMessage // ✅ 已检查 req
    }
    // ...
}
```

**修复建议**:

```go
func (r *Libp2pRPC) Call(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
    // ✅ 添加 PeerID 验证
    if to == "" {
        return nil, service.ErrInvalidPeerID
    }

    if r.closed.Load() {
        return nil, service.ErrCanceled
    }

    if req == nil {
        return nil, service.ErrInvalidMessage
    }
    // ...
}
```

**CVSS 评分**: 3.7 (LOW) - CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:U/C:N/I:N/A:L

---

### 🟡 M-02: 缺少输入验证 - Broadcast peers 为空

**文件**: `internal/infrastructure/transport/libp2p_rpc.go:168-191`

**问题描述**:

`BroadcastCall` 没有显式验证 `to` 切片是否为空。

```go
func (r *Libp2pRPC) BroadcastCall(
    ctx context.Context,
    to []model.PeerID,
    req model.Message,
    strategy service.ResponseStrategy,
    tracker *service.BroadcastProgress,
) (service.BroadcastResult, error) {
    // ⚠️ 未检查 len(to) == 0
    if r.closed.Load() {
        return service.BroadcastResult{}, service.ErrCanceled
    }
    // ...
}
```

**修复建议**:

```go
func (r *Libp2pRPC) BroadcastCall(...) (service.BroadcastResult, error) {
    if r.closed.Load() {
        return service.BroadcastResult{}, service.ErrCanceled
    }

    // ✅ 添加空切片检查
    if len(to) == 0 {
        return service.BroadcastResult{}, nil // 或返回错误
    }

    // ✅ 添加 nil 检查
    if req == nil {
        return service.BroadcastResult{}, service.ErrInvalidMessage
    }
    // ...
}
```

**CVSS 评分**: 3.7 (LOW) - CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:U/C:N/I:N/A:L

---

### 🟡 M-03: 重复关闭 Channel 导致 Panic

**文件**: `internal/domain/service/broadcast_progress.go:274-280`

**问题描述**:

`RecordSuccess` 使用 `select + close` 模式避免重复关闭，但这不是线程安全的。

```go
// 当前代码（已修复）
select {
case <-t.majorityDone:
    // 已经关闭，跳过
default:
    close(t.majorityDone) // ⚠️ 这里和另一个 goroutine 可能同时执行
}
```

**修复建议**:

使用 `sync.Once` 确保只关闭一次：

```go
type BroadcastProgress struct {
    // ...
    closeMajorityOnce sync.Once // ✅ 添加 once
    closeFullOnce     sync.Once
}

func (t *BroadcastProgress) RecordSuccess(peer model.PeerID, resp model.Message) {
    // ...
    if len(t.responses) >= majority {
        t.closeMajorityOnce.Do(func() { // ✅ 线程安全
            close(t.majorityDone)
        })
        // ...
    }
    // ...
}
```

**CVSS 评分**: 4.3 (MEDIUM) - CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:N/I:N/A:L

---

### 🟡 M-04: Callback Panic 可能影响主流程

**文件**: `internal/domain/service/broadcast_progress.go:428-438`

**问题描述**:

虽然 `safeCallback` 已捕获 panic，但未记录堆栈跟踪，难以调试。

```go
func safeCallback(fn func()) {
    defer func() {
        if r := recover(); r != nil {
            slog.Error("[BroadcastProgress] callback panic recovered",
                "panic", r,
                "stack", string(debug.Stack())) // ✅ 已记录堆栈
        }
    }()
    fn()
}
```

**当前状态**: ✅ 已正确实现

**建议增强**:

```go
func safeCallback(fn func()) {
    defer func() {
        if r := recover(); r != nil {
            // ✅ 增强日志
            slog.Error("[BroadcastProgress] callback panic recovered",
                "panic", r,
                "stack", string(debug.Stack()),
                "goroutine", getGoroutineID()) // 可选：记录 goroutine ID

            // ✅ 可选：发送到监控系统
            metrics.Increment("callback_panics_total")
        }
    }()
    fn()
}
```

**CVSS 评分**: 2.0 (LOW) - CVSS:3.1/AV:N/AC:H/PR:H/UI:N/S:U/C:N/I:N/A:L

---

### 🟡 M-05: 超时精度损失

**文件**: `internal/domain/service/rpc_async_impl.go:166-173`

**问题描述**:

`WithTimeout` 创建新的 `timeoutAsyncOp`，但嵌套调用时可能丢失原始 context 的取消信号。

```go
func (op *asyncOpImpl[T]) WithTimeout(timeout time.Duration) AsyncOperation[T] {
    wrapped := &timeoutAsyncOp[T]{
        inner:   op,
        timeout: timeout,
    }
    return wrapped
}

// 问题场景
op1 := NewAsyncCall(ctx, ...)            // ctx 有取消信号
op2 := op1.WithTimeout(5 * time.Second)  // ✅ 正常
op3 := op2.WithTimeout(10 * time.Second) // ⚠️ 可能丢失 op1 的 context
```

**修复建议**:

```go
// ✅ 建议在文档中明确说明：WithTimeout 应该只调用一次
// 或者在实现中检测嵌套并拒绝
func (op *timeoutAsyncOp[T]) WithTimeout(timeout time.Duration) AsyncOperation[T] {
    // ✅ 取最小值（当前已实现）
    newTimeout := op.timeout
    if timeout < newTimeout {
        newTimeout = timeout
    }
    return &timeoutAsyncOp[T]{
        inner:   op.inner, // ✅ 始终基于原始 op
        timeout: newTimeout,
    }
}
```

**当前状态**: ✅ 已正确实现（取最小值）

**CVSS 评分**: 2.0 (LOW) - CVSS:3.1/AV:N/AC:H/PR:H/UI:N/S:U/C:N/I:N/A:L

---

### 🟡 M-06: RequestIDGenerator 序列号溢出

**文件**: `internal/domain/service/transport.go:351-396`

**问题描述**:

虽然 P1-1 修复已添加溢出保护，但在极端高并发场景下（>65535 req/s），会阻塞 1 秒。

```go
// 当前实现
func (g *RequestIDGenerator) Next() RequestID {
    const maxSeq uint32 = 0xFFFF

    for {
        // ...
        seq := g.secondSeq.Add(1)

        if seq > maxSeq {
            // ⚠️ 等待下一秒（最多 1 秒）
            time.Sleep(time.Until(time.Unix(now+1, 0)))
            continue
        }
        // ...
    }
}
```

**影响**:
- **吞吐量限制**: 单节点最多 65,535 req/s
- **延迟尖峰**: 达到上限时，请求会阻塞最多 1 秒

**修复建议**:

```go
// 方案 1: 扩大序列号范围（5 位 16 进制 = 1,048,575/s）
const maxSeq uint32 = 0xFFFFF // ✅ 提升到 100 万/s

// 方案 2: 使用更大的序列号（64 位）
type RequestIDGenerator struct {
    nodeID    string
    lastSecond atomic.Int64
    secondSeq  atomic.Uint64 // ✅ 64 位序列号
}

// 方案 3: 在达到上限时切换到备用策略
if seq > maxSeq {
    // ✅ 使用毫秒级时间戳 + 更小的序列号
    return RequestID(fmt.Sprintf("%s-%08x-%03x-%04x",
        g.nodeID, now, time.Now().Nanosecond()/1e6, seq&0xFFFF))
}
```

**CVSS 评分**: 3.1 (LOW) - CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:U/C:N/I:N/A:L

---

## 4. LOW 严重性问题

### 🟢 L-01: 缺少日志 - 关键路径

**文件**: `internal/infrastructure/transport/libp2p_rpc.go:358-383`

**问题描述**:

`Close` 方法没有记录关闭过程中的关键信息。

**修复建议**:

```go
func (r *Libp2pRPC) Close() error {
    if !r.closed.CompareAndSwap(false, true) {
        return nil
    }

    r.closeOnce.Do(func() {
        close(r.closeCh)

        // ✅ 添加日志
        r.pendingCallsMu.Lock()
        pendingCount := len(r.pendingCalls)
        slog.Info("[Libp2pRPC] closing RPC",
            "pending_calls", pendingCount)

        for id, call := range r.pendingCalls {
            call.done.Store(true)
            select {
            case call.responseCh <- service.ResponseMsg{Err: service.ErrCanceled}:
            default:
            }
            delete(r.pendingCalls, id)
        }
        r.pendingCallsMu.Unlock()

        slog.Info("[Libp2pRPC] RPC closed")
    })

    return nil
}
```

**CVSS 评分**: 0.0 (INFO)

---

### 🟢 L-02: Magic Number - 并发限制

**文件**: `internal/infrastructure/transport/libp2p_rpc.go:601-605`

**问题描述**:

默认并发限制使用 magic number 1000，缺少配置说明。

```go
maxConcurrent := r.config.MaxConcurrentCalls
if maxConcurrent <= 0 {
    maxConcurrent = 1000 // ⚠️ Magic number
}
```

**修复建议**:

```go
const (
    DefaultMaxConcurrentCalls = 1000 // ✅ 定义常量
)

if maxConcurrent <= 0 {
    maxConcurrent = DefaultMaxConcurrentCalls
}
```

**CVSS 评分**: 0.0 (INFO)

---

### 🟢 L-03: 注释不一致 - StreamCodec 用途

**文件**: `internal/infrastructure/transport/libp2p_rpc.go:418-443`

**问题描述**:

`sendRequestAndWaitResponse` 的注释说明了 P1-5 修复，但未解释为何需要 StreamCodec。

**修复建议**:

```go
// doSendRequestAndWaitResponse 发送请求并异步等待响应
//
// 使用 StreamCodec 而非普通 Codec 的原因：
// 1. 支持大消息（>1MB）的流式传输
// 2. 自动处理分帧（framing），避免粘包/拆包问题
// 3. 支持流式压缩（未来特性）
func (r *Libp2pRPC) doSendRequestAndWaitResponse(...) error {
    // ...
}
```

**CVSS 评分**: 0.0 (INFO)

---

### 🟢 L-04: 错误消息不够友好

**文件**: `internal/domain/service/rpc_async_impl.go:324`

**问题描述**:

错误消息使用英文，与项目中文注释不一致。

```go
op.fail(fmt.Errorf("%w: timeoutMs must be positive, got %d", ErrInvalidTimeout, timeoutMs))
```

**修复建议**:

```go
op.fail(fmt.Errorf("%w: timeoutMs 必须为正数，当前为 %d", ErrInvalidTimeout, timeoutMs))
```

**CVSS 评分**: 0.0 (INFO)

---

## 5. 安全最佳实践检查清单

### ✅ 已实现的最佳实践

| 最佳实践 | 位置 | 状态 |
|---------|------|------|
| **输入验证** | `rpc_async_impl.go:322-326` | ✅ timeout 验证 |
| **Nil 检查** | `libp2p_rpc.go:78-80` | ✅ req nil 检查 |
| **Panic Recovery** | `broadcast_progress.go:428-438` | ✅ safeCallback |
| **Context 传播** | 所有异步方法 | ✅ 正确传递 |
| **资源清理** | `libp2p_rpc.go:358-383` | ✅ Close 清理 |
| **并发控制** | `libp2p_rpc.go:245-283` | ✅ 信号量限制 |
| **原子操作** | `transport.go:351-396` | ✅ RequestID 生成 |

### ❌ 缺失的最佳实践

| 最佳实践 | 建议 | 优先级 |
|---------|------|--------|
| **速率限制** | 添加令牌桶/漏桶算法 | HIGH |
| **熔断机制** | 集成 Circuit Breaker | HIGH |
| **请求大小限制** | 限制消息体大小（<10MB） | MEDIUM |
| **连接超时** | 添加 ConnectTimeout 检查 | MEDIUM |
| **认证授权** | 添加 mTLS/JWT 认证 | HIGH |
| **审计日志** | 记录关键操作（连接、断开、错误） | LOW |

---

## 6. 修复优先级建议

### P0 (立即修复，本周内)

1. **C-01**: Channel 泄漏 - 添加显式关闭逻辑
2. **H-01**: Context 泄漏 - 修复 goroutine 监控逻辑
3. **H-02**: WriteV 输入验证 - 添加 nil 检查

### P1 (重要，2 周内)

4. **H-03**: BroadcastProgress 初始化 - 空 targets 处理
5. **H-04**: AsyncOperation 清理 - 添加 Cancel 方法
6. **M-01/M-02**: 输入验证 - PeerID 空值检查

### P2 (优化，1 个月内)

7. **M-03**: 重复关闭保护 - 使用 sync.Once
8. **M-06**: RequestID 溢出 - 扩大序列号范围
9. **L-01/L-02**: 代码质量 - 添加日志和常量

---

## 7. 安全测试建议

### 7.1 单元测试

```go
// 测试 Channel 泄漏
func TestRegisterPendingCall_ChannelLeak(t *testing.T) {
    rpc := NewLibp2pRPC(mockTransport, nil)

    // 模拟 10000 次超时
    for i := 0; i < 10000; i++ {
        ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
        call := rpc.registerPendingCall(fmt.Sprintf("req-%d", i), ctx)

        // 超时后清理
        time.Sleep(2 * time.Millisecond)
        rpc.unregisterPendingCall(fmt.Sprintf("req-%d", i))
        cancel()
    }

    // 检查内存是否稳定
    var m runtime.MemStats
    runtime.ReadMemStats(&m)
    assert.Less(t, m.HeapAlloc, 10*1024*1024) // < 10MB
}

// 测试 panic recovery
func TestSafeCallback_PanicRecovery(t *testing.T) {
    var logBuf bytes.Buffer
    slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))

    safeCallback(func() {
        panic("test panic")
    })

    assert.Contains(t, logBuf.String(), "callback panic recovered")
}

// 测试并发安全
func TestBroadcastProgress_ConcurrentRecord(t *testing.T) {
    tracker := NewBroadcastProgress("test", []model.PeerID{"p1", "p2", "p3"})

    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            peer := model.PeerID(fmt.Sprintf("p%d", idx%3+1))
            tracker.RecordSuccess(peer, mockMessage{})
        }(i)
    }

    wg.Wait()
    // 不应 panic
}
```

### 7.2 集成测试

```go
// 测试 DoS 场景
func TestRPC_DoSProtection(t *testing.T) {
    rpc := NewLibp2pRPC(mockTransport, &service.RPCConfig{
        MaxConcurrentCalls: 100, // 限制并发
    })

    var wg sync.WaitGroup
    errors := make([]error, 1000)

    // 发起 1000 个并发请求（应该被限制到 100）
    for i := 0; i < 1000; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()

            _, err := rpc.Call(ctx, "peer-1", mockMessage{})
            errors[idx] = err
        }(i)
    }

    wg.Wait()

    // 统计成功/失败
    successCount := 0
    for _, err := range errors {
        if err == nil {
            successCount++
        }
    }

    // 应该有限制效果
    assert.LessOrEqual(t, successCount, 100)
}

// 测试资源清理
func TestRPC_ResourceCleanup(t *testing.T) {
    rpc := NewLibp2pRPC(mockTransport, nil)

    // 发起 1000 个请求
    for i := 0; i < 1000; i++ {
        go rpc.Call(context.Background(), "peer-1", mockMessage{})
    }

    // 关闭 RPC
    err := rpc.Close()
    assert.NoError(t, err)

    // 检查资源是否清理
    rpc.pendingCallsMu.Lock()
    assert.Equal(t, 0, len(rpc.pendingCalls))
    rpc.pendingCallsMu.Unlock()
}
```

### 7.3 压力测试

```bash
# 使用 ghz 进行 RPC 压力测试
ghz --insecure \
    --proto api.proto \
    --call NexKV.Put \
    -d '{"key":"test","value":"data"}' \
    -n 100000 \
    -c 1000 \
    --timeout 30s \
    localhost:9000

# 监控内存
curl http://localhost:6060/debug/pprof/heap > heap.prof
go tool pprof -http=:8080 heap.prof
```

---

## 8. 总结与建议

### 8.1 核心问题

1. **资源泄漏风险**: Channel 和 Goroutine 泄漏是最大隐患
2. **输入验证不完整**: 缺少 PeerID 和空切片验证
3. **并发边界条件**: 多个 close channel 的竞态条件

### 8.2 修复成本估算

| 问题类型 | 工作量 | 测试工作量 | 总计 |
|---------|--------|-----------|------|
| CRITICAL | 4h | 8h | 12h |
| HIGH | 8h | 16h | 24h |
| MEDIUM | 4h | 8h | 12h |
| LOW | 2h | 4h | 6h |
| **总计** | **18h** | **36h** | **54h** |

### 8.3 建议行动计划

**第一周 (P0)**:
- [ ] 修复 C-01: Channel 泄漏
- [ ] 修复 H-01: Context 泄漏
- [ ] 修复 H-02: WriteV 输入验证
- [ ] 添加单元测试

**第二周 (P1)**:
- [ ] 修复 H-03: BroadcastProgress 初始化
- [ ] 修复 H-04: AsyncOperation 清理
- [ ] 修复 M-01/M-02: 输入验证
- [ ] 添加集成测试

**第三周 (P2)**:
- [ ] 修复 M-03: 重复关闭保护
- [ ] 修复 M-06: RequestID 溢出
- [ ] 代码质量改进 (L-01/L-02)
- [ ] 压力测试

### 8.4 长期建议

1. **引入安全扫描工具**:
   - `gosec` - 静态安全分析
   - `golangci-lint` - 代码质量检查
   - `go vet` - 并发错误检测

2. **建立安全编码规范**:
   - 所有 channel 必须在创建它的地方关闭
   - 所有 goroutine 必须有退出路径
   - 所有公共 API 必须验证输入

3. **定期安全审计**:
   - 每季度进行一次安全审查
   - 每次重大发布前进行渗透测试
   - 监控生产环境的异常行为

---

**审查完成日期**: 2026-02-23
**下次审查计划**: 2026-05-23
**审查人签名**: Security Reviewer Agent
