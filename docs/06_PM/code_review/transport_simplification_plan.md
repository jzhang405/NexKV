# Transport 代码简化方案

> **审查日期**: 2026-02-20
> **审查范围**: `internal/infrastructure/transport/`
> **审查重点**: libp2p_rpc.go, libp2p_rpc_test.go, async_common_test.go, messagepack_codec_test.go

---

## 一、简化目标

1. **减少重复代码**：提取公共逻辑，消除重复模式
2. **简化复杂逻辑**：拆分长方法，降低圈复杂度
3. **提取公共方法**：测试辅助函数、mock 对象复用
4. **优化测试结构**：减少冗余测试，提高测试效率

---

## 二、核心简化建议

### 2.1 libp2p_rpc.go 简化

#### 问题 1: `broadcastAndWait` 和 `WriteVCall` 代码高度相似

**当前代码** (行 523-590 和 246-311):
```go
// broadcastAndWait
result := service.BroadcastResult{
    Responses:    make([]model.Message, len(to)),
    SuccessPeers: make([]model.PeerID, 0),
    FailedPeers:  make([]model.PeerID, 0),
}
var wg sync.WaitGroup
var resultMu sync.Mutex
sem := make(chan struct{}, maxConcurrent)

for i, peer := range to {
    sem <- struct{}{}
    wg.Add(1)
    go func(idx int, p model.PeerID) {
        defer func() { <-sem }()
        defer wg.Done()
        // ... 并发调用逻辑
    }(i, peer)
}
wg.Wait()

// WriteVCall
result := service.WriteVResult{
    Responses:    make(map[model.PeerID]model.Message),
    SuccessPeers: make([]model.PeerID, 0),
    FailedPeers:  make([]model.PeerID, 0),
}
var wg sync.WaitGroup
var resultMu sync.Mutex
sem := make(chan struct{}, r.config.MaxConcurrentCalls)

for i := range targets {
    sem <- struct{}{}
    wg.Add(1)
    go func(idx int) {
        defer func() { <-sem }()
        defer wg.Done()
        // ... 并发调用逻辑（几乎相同）
    }(i)
}
wg.Wait()
```

**简化方案**: 提取通用的并发执行框架

```go
// parallelCallConfig 并发调用配置
type parallelCallConfig struct {
    ctx         context.Context
    timeout     time.Duration
    maxConcur   int
    strategy    service.ResponseStrategy
    tracker     *service.BroadcastTracker
}

// parallelCallResult 并发调用结果
type parallelCallResult struct {
    mu           sync.Mutex
    successCount int
    failedCount  int
    totalCount   int
}

// executeParallel 并发执行框架
func (r *Libp2pRPC) executeParallel(
    targets []model.PeerID,
    callFunc func(ctx context.Context, peer model.PeerID) error,
    config parallelCallConfig,
) parallelCallResult {
    result := parallelCallResult{totalCount: len(targets)}

    if len(targets) == 0 {
        return result
    }

    var wg sync.WaitGroup
    maxConcur := config.maxConcur
    if maxConcur <= 0 {
        maxConcur = 1000 // 默认值
    }
    sem := make(chan struct{}, maxConcur)

    for _, peer := range targets {
        sem <- struct{}{}
        wg.Add(1)
        go func(p model.PeerID) {
            defer func() { <-sem }()
            defer wg.Done()

            callCtx, cancel := context.WithTimeout(config.ctx, config.timeout)
            defer cancel()

            err := callFunc(callCtx, p)

            result.mu.Lock()
            if err != nil {
                result.failedCount++
                if config.tracker != nil {
                    config.tracker.RecordFailure(p, err)
                }
            } else {
                result.successCount++
                if config.tracker != nil {
                    config.tracker.RecordSuccess(p, nil)
                }
            }
            result.mu.Unlock()
        }(peer)
    }

    wg.Wait()
    return result
}

// broadcastAndWait 使用并发框架重构
func (r *Libp2pRPC) broadcastAndWait(
    ctx context.Context,
    to []model.PeerID,
    req model.Message,
    strategy service.ResponseStrategy,
    tracker *service.BroadcastTracker,
) (service.BroadcastResult, error) {
    result := service.BroadcastResult{
        Responses:    make([]model.Message, len(to)),
        SuccessPeers: make([]model.PeerID, 0),
        FailedPeers:  make([]model.PeerID, 0),
    }
    var resultMu sync.Mutex

    config := parallelCallConfig{
        ctx:       ctx,
        timeout:   r.config.BroadcastTimeout,
        maxConcur: r.config.MaxConcurrentCalls,
        strategy:  strategy,
        tracker:   tracker,
    }

    callFunc := func(callCtx context.Context, peer model.PeerID) error {
        resp, err := r.Call(callCtx, peer, req)

        resultMu.Lock()
        defer resultMu.Unlock()

        if err != nil {
            result.FailedPeers = append(result.FailedPeers, peer)
        } else {
            // 注意：这里需要索引，暂时保留原有实现或改进索引传递
            result.Responses = append(result.Responses, resp)
            result.SuccessPeers = append(result.SuccessPeers, peer)
        }
        return err
    }

    parallelResult := r.executeParallel(to, callFunc, config)

    // 根据策略验证
    if err := validateStrategy(strategy, len(to), parallelResult.successCount, parallelResult.failedCount); err != nil {
        return result, err
    }

    result.Responses = cleanNilResponses(result.Responses)
    return result, nil
}
```

**收益**:
- 减少约 40 行重复代码
- 统一并发控制逻辑
- 更容易维护和测试

---

#### 问题 2: `setMessageID` 可以内联

**当前代码** (行 349-353):
```go
// setMessageID 设置消息 ID
func (r *Libp2pRPC) setMessageID(msg model.Message, id string) model.Message {
    // 创建带有新 ID 的消息副本
    return model.NewMessage(id, msg.Type(), msg.Source(), msg.Target(), msg.Payload())
}
```

**简化方案**: 直接内联使用

```go
// 在 Call 方法中（行 79）
reqWithID := model.NewMessage(requestID, req.Type(), req.Source(), req.Target(), req.Payload())

// 在 HandleIncomingStream 方法中（行 628）
respWithID := model.NewMessage(msg.ID(), resp.Type(), resp.Source(), resp.Target(), resp.Payload())
```

**收益**:
- 减少不必要的包装方法
- 代码更直观

---

### 2.2 libp2p_rpc_test.go 简化

#### 问题 1: 重复的 mock 创建逻辑

**当前代码**: 每个测试都手动创建 `mockTransport`

**简化方案**: 提取测试辅助函数

```go
// testRPCSetup 测试 RPC 设置辅助函数
type testRPCSetup struct {
    transport *mockTransport
    rpc       *Libp2pRPC
}

// newTestRPC 创建测试 RPC
func newTestRPC(nodeID model.PeerID) *testRPCSetup {
    transport := newMockTransport(nodeID)
    rpc := NewLibp2pRPC(transport, nil)
    return &testRPCSetup{
        transport: transport,
        rpc:       rpc,
    }
}

// connectPeer 标记节点为已连接
func (s *testRPCSetup) connectPeer(peer model.PeerID) {
    s.transport.mu.Lock()
    s.transport.connected[peer] = true
    s.transport.mu.Unlock()
}

// close 清理资源
func (s *testRPCSetup) close() {
    _ = s.rpc.Close()
}

// 使用示例
func TestLibp2pRPC_Call_ConnectedPeer(t *testing.T) {
    setup := newTestRPC("node-1")
    defer setup.close()

    setup.connectPeer("node-2")

    msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))
    ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
    defer cancel()

    _, err := setup.rpc.Call(ctx, "node-2", msg)
    // ... 验证逻辑
}
```

**收益**:
- 减少约 150 行重复代码
- 测试代码更简洁
- 统一资源管理（defer close）

---

#### 问题 2: BroadcastTracker 测试可以合并

**当前代码**: 多个独立的测试函数（行 391-514）

**简化方案**: 使用表驱动测试

```go
func TestBroadcastTracker_All(t *testing.T) {
    tests := []struct {
        name     string
       	targets  []model.PeerID
        setup    func(*service.BroadcastTracker)
       	check    func(*testing.T, *service.BroadcastTracker)
    }{
        {
            name:    "new tracker",
            targets: []model.PeerID{"node-1", "node-2", "node-3"},
            setup:   nil,
            check: func(t *testing.T, tr *service.BroadcastTracker) {
                s, f, p := tr.Stats()
                if s != 0 || f != 0 || p != 3 {
                    t.Errorf("Stats() = (%d, %d, %d), want (0, 0, 3)", s, f, p)
                }
            },
        },
        {
            name:    "majority reached",
            targets: []model.PeerID{"node-1", "node-2", "node-3"},
            setup: func(tr *service.BroadcastTracker) {
                tr.RecordSuccess("node-1", nil)
                tr.RecordSuccess("node-2", nil)
            },
            check: func(t *testing.T, tr *service.BroadcastTracker) {
                if !tr.IsMajorityReached() {
                    t.Error("IsMajorityReached() = false, want true")
                }
            },
        },
        {
            name:    "full done with failures",
            targets: []model.PeerID{"node-1", "node-2"},
            setup: func(tr *service.BroadcastTracker) {
                tr.RecordSuccess("node-1", nil)
                tr.RecordFailure("node-2", service.ErrTimeout)
            },
            check: func(t *testing.T, tr *service.BroadcastTracker) {
                if !tr.IsFullDone() {
                    t.Error("IsFullDone() = false, want true")
                }
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tracker := service.NewBroadcastTracker("test-001", tt.targets)
            if tt.setup != nil {
                tt.setup(tracker)
            }
            tt.check(t, tracker)
        })
    }
}
```

**收益**:
- 减少约 80 行代码
- 测试结构更清晰
- 更容易添加新测试用例

---

#### 问题 3: validateStrategy 和 cleanNilResponses 测试重复

**当前代码**: 多个独立的测试函数（行 751-840）

**简化方案**: 合并为表驱动测试

```go
func TestValidateStrategy_All(t *testing.T) {
    tests := []struct {
        name     string
        strategy service.ResponseStrategy
        total    int
        success  int
        failed   int
        wantErr  bool
    }{
        // ResponseAll 测试
        {"ResponseAll: all success", service.ResponseAll, 3, 3, 0, false},
        {"ResponseAll: one failed", service.ResponseAll, 3, 2, 1, true},
        {"ResponseAll: all failed", service.ResponseAll, 3, 0, 3, true},

        // ResponseMajority 测试
        {"ResponseMajority: 2/3", service.ResponseMajority, 3, 2, 1, false},
        {"ResponseMajority: 1/3", service.ResponseMajority, 3, 1, 2, true},
        {"ResponseMajority: 3/5", service.ResponseMajority, 5, 3, 2, false},
        {"ResponseMajority: 2/5", service.ResponseMajority, 5, 2, 3, true},

        // ResponseNone 测试
        {"ResponseNone: always nil", service.ResponseNone, 3, 0, 3, false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := validateStrategy(tt.strategy, tt.total, tt.success, tt.failed)
            if (err != nil) != tt.wantErr {
                t.Errorf("validateStrategy() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

**收益**:
- 减少约 60 行代码
- 测试覆盖更全面

---

### 2.3 async_common_test.go 简化

#### 问题 1: 测试覆盖足够但可以优化

**当前状态**: 测试结构已经较好，使用表驱动测试

**优化建议**: 无需大幅修改，仅建议：

1. **合并相似测试**: `TestNonBlockingSendResult_*` 可以合并
2. **使用子测试**: 将相关测试组织在一起

```go
func TestNonBlockingSendResult_All(t *testing.T) {
    tests := []struct {
        name     string
       	setup    func(ch chan int, ctx context.Context) (chan int, context.CancelFunc)
        value    int
       	wantSent bool
    }{
        {
            name: "success",
            setup: func(ch chan int, ctx context.Context) (chan int, context.CancelFunc) {
                return ch, func() {}
            },
            value:    42,
            wantSent: true,
        },
        {
            name: "channel full",
            setup: func(ch chan int, ctx context.Context) (chan int, context.CancelFunc) {
                ch <- 100 // 填满
                return ch, func() {}
            },
            value:    42,
            wantSent: false,
        },
        {
            name: "context canceled",
            setup: func(ch chan int, ctx context.Context) (chan int, context.CancelFunc) {
                cancel := func() {}
                return ch, cancel
            },
            value:    42,
            wantSent: false, // 或不确定
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ch := make(chan int, 1)
            ctx, cancel := context.WithCancel(context.Background())
            defer cancel()

            testCh, testCancel := tt.setup(ch, ctx)
            if tt.name == "context canceled" {
                testCancel()
            }

            result := NonBlockingSendResult(testCh, tt.value, "test", ctx.Done())
            if result != tt.wantSent {
                t.Errorf("NonBlockingSendResult() = %v, want %v", result, tt.wantSent)
            }
        })
    }
}
```

**收益**:
- 减少约 30 行代码
- 测试结构更一致

---

### 2.4 messagepack_codec_test.go 简化

#### 问题 1: createTestMessage 可以增强

**当前代码** (行 13-15):
```go
func createTestMessage(id string, msgType model.MessageType, payload []byte) *model.BaseMessage {
    return model.NewMessage(id, msgType, "node-1", "node-2", payload)
}
```

**优化建议**: 增加可配置选项

```go
type testMessageOption struct {
    source model.PeerID
    target model.PeerID
    exts   map[string]any
}

func createTestMessageWithOpt(id string, msgType model.MessageType, payload []byte, opt *testMessageOption) *model.BaseMessage {
    if opt == nil {
        opt = &testMessageOption{
            source: "node-1",
            target: "node-2",
        }
    }

    msg := model.NewMessage(id, msgType, opt.source, opt.target, payload)

    if opt.exts != nil {
        for k, v := range opt.exts {
            msg.Exts().Set(k, v)
        }
    }

    return msg
}

// 保持向后兼容
func createTestMessage(id string, msgType model.MessageType, payload []byte) *model.BaseMessage {
    return createTestMessageWithOpt(id, msgType, payload, nil)
}
```

**收益**:
- 测试代码更灵活
- 减少重复的扩展字段设置代码

---

#### 问题 2: 性能测试可以合并

**当前代码**: 多个独立的 Benchmark 函数（行 340-424）

**优化建议**: 使用子基准测试

```go
func BenchmarkMessagePackCodec(b *testing.B) {
    codec := NewMessagePackCodec()

    benchmarks := []struct {
        name    string
       	msgSize int
    }{
        {"1KB", 1024},
        {"10KB", 10 * 1024},
        {"100KB", 100 * 1024},
    }

    for _, bm := range benchmarks {
        b.Run("Encode_"+bm.name, func(b *testing.B) {
            msg := createTestMessage("bench", model.MessageTypeRequest, make([]byte, bm.msgSize))
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                _, _ = codec.Encode(msg)
            }
        })

        b.Run("Decode_"+bm.name, func(b *testing.B) {
            msg := createTestMessage("bench", model.MessageTypeRequest, make([]byte, bm.msgSize))
            data, _ := codec.Encode(msg)
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                _, _ = codec.Decode(data)
            }
        })

        b.Run("RoundTrip_"+bm.name, func(b *testing.B) {
            msg := createTestMessage("bench", model.MessageTypeRequest, make([]byte, bm.msgSize))
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                data, _ := codec.Encode(msg)
                _, _ = codec.Decode(data)
            }
        })
    }
}
```

**收益**:
- 减少约 60 行代码
- 性能测试结构更清晰
- 更容易比较不同大小的性能

---

## 三、实施优先级

### P0（高优先级 - 立即实施）

1. **提取测试辅助函数** (libp2p_rpc_test.go)
   - 预计减少 150 行
   - 风险：低
   - 工作量：1 小时

2. **合并 BroadcastTracker 测试** (libp2p_rpc_test.go)
   - 预计减少 80 行
   - 风险：低
   - 工作量：30 分钟

### P1（中优先级 - 本周完成）

3. **提取并发执行框架** (libp2p_rpc.go)
   - 预计减少 40 行
   - 风险：中（需要仔细测试并发逻辑）
   - 工作量：2 小时

4. **合并 validateStrategy/cleanNilResponses 测试** (libp2p_rpc_test.go)
   - 预计减少 60 行
   - 风险：低
   - 工作量：30 分钟

### P2（低优先级 - 有时间再做）

5. **优化性能测试结构** (messagepack_codec_test.go)
   - 预计减少 60 行
   - 风险：低
   - 工作量：1 小时

6. **增强 createTestMessage** (messagepack_codec_test.go)
   - 提高测试代码灵活性
   - 风险：低
   - 工作量：30 分钟

---

## 四、预期收益

| 指标 | 优化前 | 优化后 | 减少 |
|------|--------|--------|------|
| **总行数** | ~2800 行 | ~2400 行 | ~400 行 (14%) |
| **测试辅助代码** | ~200 行 | ~50 行 | ~150 行 (75%) |
| **重复逻辑** | ~100 行 | ~20 行 | ~80 行 (80%) |
| **圈复杂度** | 平均 8 | 平均 5 | -37% |

---

## 五、风险评估

### 低风险
- 测试辅助函数提取
- 表驱动测试合并
- 性能测试结构优化

### 中风险
- 并发执行框架提取（需要全面回归测试）

### 缓解措施
1. **分阶段实施**: 先优化测试，再优化生产代码
2. **保留原测试**: 在新测试通过前不删除旧测试
3. **增量验证**: 每次修改后运行完整测试套件
4. **Code Review**: 所有简化代码必须经过审查

---

## 六、后续行动

### 立即执行 (今天)
- [ ] 提取测试辅助函数 (`newTestRPC`, `connectPeer`, `close`)
- [ ] 合并 BroadcastTracker 测试为表驱动测试
- [ ] 合并 validateStrategy/cleanNilResponses 测试

### 本周完成
- [ ] 提取并发执行框架 (`executeParallel`)
- [ ] 重构 `broadcastAndWait` 和 `WriteVCall`
- [ ] 运行完整测试套件验证

### 后续优化
- [ ] 优化性能测试结构
- [ ] 增强测试辅助函数
- [ ] 考虑提取公共验证逻辑

---

## 七、代码质量检查清单

简化完成后，确保：

- [ ] 所有测试通过 (`make test`)
- [ ] 测试覆盖率不降低 (≥80%)
- [ ] 无 lint 错误 (`make lint`)
- [ ] 代码格式正确 (`make fmt`)
- [ ] 编译成功 (`make build`)
- [ ] 性能无明显退化 (benchmark 对比)
- [ ] 并发安全（race detector 测试）

---

**审查人**: Code Simplifier
**审查时间**: 2026-02-20
**下次审查**: 实施完成后
