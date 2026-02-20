# BroadcastTracker Callback 机制 - Pre 文档

> **PR 类型**: feature
> **创建日期**: 2026-02-21
> **状态**: 📋 待评审
> **关联 Spike**: `docs/07_spike/2026-02-20_broadcast-tracker-callback-mechanism.md`

---

## 1. 任务范围

### 1.1 背景

`BroadcastTracker` 用于追踪广播调用的进度，支持等待多数派 (`WaitMajority`) 和全部完成 (`WaitFull`)。但在实际使用中，用户可能需要在达到关键进度节点时**立即执行某些操作**，而不是被动等待。

通过 Spike 研究已明确技术方案，现在需要正式实现 Callback 机制，支持：
- 实时监控广播进度
- 达到关键节点时触发业务逻辑
- 支持日志记录、指标上报、流水线触发等场景

### 1.2 目标

为 `BroadcastTracker` 添加 Callback 机制，实现：
- ✅ 支持在每次成功/失败响应时触发回调
- ✅ 支持在达到多数派时触发回调
- ✅ 支持在全部完成时触发回调
- ✅ 保证线程安全（避免死锁）
- ✅ 支持 Panic 保护

### 1.3 不包含的内容

- ❌ 不实现 Channel 模式（复杂度高，暂不需要）
- ❌ 不实现运行时修改回调（简化设计，避免并发问题）
- ❌ 不实现异步回调队列（性能优化留待后续）

---

## 2. 背景与问题

### 2.1 当前问题

**问题 1：缺少实时进度感知**
- 当前 `BroadcastTracker` 只能被动等待（`WaitMajority`/`WaitFull`）
- 用户无法实时感知每次响应的进度
- 无法在达到关键节点时立即触发业务逻辑

**问题 2：日志记录困难**
- 需要在外部轮询 `BroadcastTracker` 状态
- 无法精确记录每个响应的到达时间
- 日志记录代码与业务代码耦合

**问题 3：指标上报不便**
- 无法实时上报广播进度指标
- 无法精确统计多数派达成时间
- 缺少失败率等关键指标

### 2.2 影响范围

**直接影响**：
- `internal/transport/tracker.go` - 需要添加 Callback 支持
- `internal/transport/broadcast.go` - 需要在广播调用时支持 Callback

**间接影响**：
- 使用 `BroadcastTracker` 的业务代码可选择性使用 Callback
- 测试代码需要覆盖新的回调逻辑

---

## 3. 目标与验收标准

### 3.1 功能目标

- [ ] 定义 `BroadcastCallback` 接口（4 个回调方法）
- [ ] 定义 `BroadcastStats` 统计信息结构
- [ ] 在 `BroadcastTracker` 中添加 `SetCallback` 方法
- [ ] 在 `RecordSuccess` 中触发回调（OnSuccess、OnMajorityReached、OnFullDone）
- [ ] 在 `RecordFailure` 中触发回调（OnFailure、OnFullDone）
- [ ] 实现线程安全（锁外执行回调）
- [ ] 实现 Panic 保护（`safeCallback` 包装）

### 3.2 质量目标

- **单元测试覆盖率**: ≥ 80%
  - 回调触发测试（成功、失败、多数派、全部完成）
  - 线程安全测试（并发调用）
  - Panic 保护测试（回调 panic 不影响主流程）

- **代码质量**: 0 lint issues
  - 遵循 Go 编码规范
  - 无代码重复
  - 注释清晰

- **性能指标**:
  - 回调执行不阻塞主流程（锁外执行）
  - 回调执行时间 < 10ms（建议值，非强制，便于测试和性能优化）

### 3.3 验收标准

**功能验收**:
1. ✅ 回调正确触发（成功、失败、多数派、全部完成）
2. ✅ 统计信息准确（Total、Success、Failed、Pending、SuccessRate）
3. ✅ 线程安全（无死锁、无竞态）
4. ✅ Panic 保护（回调 panic 不影响主流程）

**测试验收**:
1. ✅ 所有单元测试通过
2. ✅ 竞态检测通过（`make test-race`）
3. ✅ 覆盖率 ≥ 80%

**文档验收**:
1. ✅ 代码注释完整（接口、方法、字段）
2. ✅ 使用示例清晰（日志记录、指标上报、错误处理）
3. ✅ 性能基准测试结果（回调开销 < 100μs）

---

## 4. 实施方案

### 4.1 技术方案

**核心设计原则**：
- **接口模式**：定义 `BroadcastCallback` 接口，用户实现该接口
- **可选性**：Callback 为可选功能，不影响现有代码
- **线程安全**：回调在锁外执行，避免死锁
- **Panic 保护**：使用 `safeCallback` 包装，防止 panic 影响主流程

**接口定义**：

```go
// BroadcastCallback 广播进度回调接口
type BroadcastCallback interface {
    // OnSuccess 每次收到成功响应时调用
    // 参数说明：
    //   - peer: 响应节点 ID
    //   - resp: 成功响应消息（不会为 nil）
    //   - stats: 当前统计信息
    OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats)

    // OnFailure 每次收到失败响应时调用
    // 参数说明：
    //   - peer: 失败节点 ID
    //   - err: 错误信息（不会为 nil，包含具体错误类型）
    //          - 超时错误：context.DeadlineExceeded
    //          - 网络错误：net.Error
    //          - 业务错误：业务逻辑返回的错误
    //   - stats: 当前统计信息
    OnFailure(peer model.PeerID, err error, stats BroadcastStats)

    // OnMajorityReached 达到多数派时调用（仅调用一次）
    // 触发条件：
    //   - 成功响应数 >= majority（len(targets)/2 + 1）
    //   - 只在 RecordSuccess 时检查，RecordFailure 不会触发
    //   - 例如：3 个节点，2 个成功即触发（即使 1 个失败）
    // 参数说明：
    //   - stats: 达到多数派时的统计信息
    OnMajorityReached(stats BroadcastStats)

    // OnFullDone 全部完成时调用（仅调用一次）
    // 触发条件：
    //   - 成功数 + 失败数 == 总节点数
    // 参数说明：
    //   - stats: 全部完成时的统计信息
    OnFullDone(stats BroadcastStats)
}

// BroadcastStats 广播统计信息
type BroadcastStats struct {
    TaskID             string
    Total              int           // 总节点数
    Success            int           // 成功数
    Failed             int           // 失败数
    Pending            int           // 待响应数
    SuccessRate        float64       // 成功率
    ElapsedTime        time.Duration // 已耗时（从任务开始到现在）
    FirstResponseTime  time.Duration // 首个响应耗时（从任务开始到首个响应）
    MajorityReachTime  time.Duration // 达到多数派耗时（从任务开始到多数派达成）
}
```

**设计决策**：
- **方法命名**：保留 `OnSuccess/OnFailure`（而非 `OnResponse/OnError`），因为：
  1. 语义更清晰（成功/失败 vs 响应/错误）
  2. 已通过 Spike 验证
  3. 与 Spike 文档保持一致
- **Nil 参数处理**：
  - `OnSuccess` 的 `resp` 不会为 nil（成功响应一定有内容）
  - `OnFailure` 的 `err` 不会为 nil（失败一定有错误信息）

**回调执行顺序**（针对每个响应）：
```
1. OnSuccess / OnFailure（每次响应）
   ↓
2. OnMajorityReached（达到多数派时，仅一次）
   ↓
3. OnFullDone（全部完成时，仅一次）
```

**特殊场景**：
- `OnMajorityReached` 和 `OnFullDone` 可能在**同一次** `RecordSuccess` 中顺序触发
- **示例**：3 个节点，前 2 个成功后触发 `OnMajorityReached`，第 3 个成功后同时触发 `OnMajorityReached`（已触发，跳过）和 `OnFullDone`
- **关键**：无论哪种场景，都保证顺序执行（OnSuccess → OnMajorityReached → OnFullDone）

**回调实现注意事项**：
1. **避免死锁**：回调在锁外执行，但应避免调用 `BroadcastTracker` 的方法（防止死锁）
2. **快速返回**：回调应快速返回（< 10ms），长时间处理应启动 goroutine
3. **线程安全**：回调可能被并发调用（`OnSuccess`/`OnFailure`），实现需线程安全
4. **顺序独立性**：不要依赖回调的调用顺序（除文档明确保证的之外）

**实现要点**：

1. **锁外执行回调**（P0 修复）：
   ```go
   type BroadcastTracker struct {
       // ... 现有字段 ...
       callback                  BroadcastCallback
       callbacksEnabled          bool // 回调启用/禁用开关

       // 新增：确保"仅触发一次"的标志位
       majorityCallbackTriggered bool // OnMajorityReached 是否已触发
       fullDoneCallbackTriggered bool // OnFullDone 是否已触发
       firstResponseRecorded     bool // 是否已记录首个响应
   }

   func (t *BroadcastTracker) RecordSuccess(peer model.PeerID, resp model.Message) {
       var callback BroadcastCallback
       var stats BroadcastStats
       var shouldTriggerMajority bool
       var shouldTriggerFullDone bool

       // === 锁内：只做状态更新 ===
       t.mu.Lock()
       t.responses[peer] = resp

       // 记录首个响应时间（仅一次）
       if !t.firstResponseRecorded {
           t.firstResponseTime = time.Now()
           t.firstResponseRecorded = true
       }

       // 检查 Majority（仅在成功时检查）
       majority := len(t.targets)/2 + 1
       if len(t.responses) >= majority && !t.majorityCallbackTriggered {
           t.majorityCallbackTriggered = true
           shouldTriggerMajority = true
       }

       // 检查 FullDone
       if len(t.responses)+len(t.failures) == len(t.targets) && !t.fullDoneCallbackTriggered {
           t.fullDoneCallbackTriggered = true
           shouldTriggerFullDone = true
       }

       // 准备回调数据
       callback = t.callback
       stats = t.buildStatsLocked()
       t.mu.Unlock()
       // === 锁外：执行回调，避免死锁 ===

       if callback == nil || !t.callbacksEnabled {
           return
       }

       // 触发 OnSuccess 回调
       safeCallback(func() {
           callback.OnSuccess(peer, resp, stats)
       })

       // 触发 OnMajorityReached 回调（仅一次）
       if shouldTriggerMajority {
           safeCallback(func() {
               callback.OnMajorityReached(stats)
           })
       }

       // 触发 OnFullDone 回调（仅一次）
       if shouldTriggerFullDone {
           safeCallback(func() {
               callback.OnFullDone(stats)
           })
       }
   }
   ```

2. **Panic 保护**：
   ```go
   func safeCallback(fn func()) {
       defer func() {
           if r := recover(); r != nil {
               // 使用 slog.Error 记录 panic，便于监控和告警
               slog.Error("[BroadcastTracker] callback panic recovered",
                   "panic", r,
                   "stack", string(debug.Stack()))
           }
       }()
       fn()
   }
   ```

3. **除零保护**：
   ```go
   func (t *BroadcastTracker) buildStatsLocked() BroadcastStats {
       success := len(t.responses)
       failed := len(t.failures)
       total := len(t.targets)

       // P2 修复：避免除零
       var successRate float64
       if total > 0 {
           successRate = float64(success) / float64(total)
       }

       return BroadcastStats{
           TaskID:      t.taskID,
           Total:       total,
           Success:     success,
           Failed:      failed,
           Pending:     total - success - failed,
           SuccessRate: successRate,
           ElapsedTime: time.Since(t.startTime),
           // 其他时间戳字段...
       }
   }
   ```

### 4.2 实施计划

**阶段 1：接口定义（1 小时）**
- [ ] 定义 `BroadcastCallback` 接口
- [ ] 定义 `BroadcastStats` 结构（含时间戳字段）
- [ ] 在 `BroadcastTracker` 中添加 `callback` 和 `callbacksEnabled` 字段
- [ ] 实现 `SetCallback` 方法
- [ ] 实现 `EnableCallbacks` 方法（可选，便于测试）

**阶段 2：回调触发逻辑（3 小时）**
- [ ] 修改 `RecordSuccess`，添加回调触发
- [ ] 修改 `RecordFailure`，添加回调触发
- [ ] 实现 `buildStatsLocked` 方法（含除零保护）
- [ ] 实现 `safeCallback` 包装函数（含 slog.Error 日志）
- [ ] 添加时间戳统计（FirstResponseTime、MajorityReachTime）
- [ ] 实现"仅触发一次"机制（标志位 + 双重检查）
- [ ] 添加 `EnableCallbacks` 方法（可选，便于测试）

**阶段 3：单元测试（2.5 小时）**
- [ ] 测试成功响应回调（OnSuccess）
- [ ] 测试失败响应回调（OnFailure）
- [ ] 测试多数派回调（OnMajorityReached）
- [ ] 测试全部完成回调（OnFullDone）
- [ ] 测试并发安全性（并发 RecordSuccess）
- [ ] 测试 Panic 保护（safeCallback）
- [ ] **边界场景测试**：
  - [ ] 空 targets（targets=[]）
  - [ ] 全部失败（验证 OnFailure + OnFullDone 触发）
  - [ ] 先达到 Majority 后全部完成（验证两个回调顺序触发）
  - [ ] 并发 RecordSuccess（验证回调只触发一次）

**阶段 4：集成测试（1.5 小时）**
- [ ] 在实际广播调用中测试回调
- [ ] 性能测试（确保回调不阻塞主流程）
- [ ] 基准测试（BenchmarkBroadcastCallback_Overhead）
- [ ] 并发性能测试（BenchmarkBroadcastCallback_Concurrent）

**阶段 5：文档与示例（0.5 小时）**
- [ ] 编写代码注释
- [ ] 编写使用示例（日志记录、指标上报、错误处理）

**预计总时间**: 7.5 小时

**可选功能**（非必须，可根据实际需求添加）：
- `EnableCallbacks(enabled bool)` - 启用/禁用回调开关
  - 用途：便于测试时禁用回调，或在运行时临时关闭
  - 优先级：P2（可在后续 PR 中添加）

### 4.3 风险评估

| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|------|----------|
| **死锁风险** | 中 | 高 | ✅ 锁外执行回调，避免在回调中调用 BroadcastTracker 方法 |
| **Panic 影响** | 低 | 中 | ✅ 使用 `safeCallback` 包装，捕获 panic |
| **性能影响** | 低 | 中 | ✅ 回调应快速返回，长时间处理应启动 goroutine |
| **回调执行慢** | 中 | 低 | ✅ 文档中明确建议回调应快速返回 |
| **除零错误** | 低 | 低 | ✅ 在计算 SuccessRate 时添加 `if total > 0` 检查 |
| **回调日志级别错误** | 低 | 低 | ✅ Panic 使用 `slog.Error` 级别，便于监控告警 |
| **回调重复触发** | 低 | 高 | ✅ 使用标志位（majorityCallbackTriggered、fullDoneCallbackTriggered）确保仅触发一次 |
| **Nil 参数处理** | 低 | 中 | ✅ 文档明确 resp 和 err 不会为 nil，回调实现无需检查 |

---

## 5. 测试计划

### 5.1 单元测试

**测试用例清单**：
1. **TestBroadcastCallback_OnSuccess** - 测试成功响应回调
2. **TestBroadcastCallback_OnFailure** - 测试失败响应回调
3. **TestBroadcastCallback_OnMajorityReached** - 测试多数派回调
4. **TestBroadcastCallback_OnFullDone** - 测试全部完成回调
5. **TestBroadcastCallback_ConcurrentSafety** - 测试并发安全性
6. **TestBroadcastCallback_PanicRecovery** - 测试 Panic 保护
7. **TestBroadcastStats_Accuracy** - 测试统计信息准确性

**边界场景测试**：
8. **TestBroadcastCallback_EmptyTargets** - 测试空 targets（targets=[]）
9. **TestBroadcastCallback_AllFailed** - 测试全部失败（验证 OnFailure + OnFullDone 触发）
10. **TestBroadcastCallback_MajorityThenFullDone** - 测试先达到 Majority 后全部完成（验证两个回调顺序触发）
11. **TestBroadcastCallback_ConcurrentRecordSuccess** - 测试并发 RecordSuccess（验证回调只触发一次）

### 5.2 集成测试

**测试场景**：
1. 日志记录场景 - 验证回调日志输出
2. 指标上报场景 - 验证指标数据正确
3. 流水线触发场景 - 验证多数派触发下一阶段

### 5.3 性能测试

**基准测试**：
1. **BenchmarkBroadcastCallback_Overhead** - 测试回调开销
   - 目标：回调开销 < 100μs（99 分位）
   - 场景：1000 次广播调用，对比有无回调的性能差异

2. **BenchmarkBroadcastCallback_Concurrent** - 测试并发性能
   - 目标：并发回调不阻塞主流程
   - 场景：10 个并发广播调用，每个广播 10 个节点

**测试指标**：
- 回调执行时间 < 10ms（平均值，更实际的阈值）
- 回调开销 < 100μs（99 分位）
- 回调不阻塞主流程（吞吐量下降 < 5%）

---

## 6. 使用示例

### 6.1 日志记录场景

```go
type LogCallback struct {
    logger *slog.Logger
}

func (cb *LogCallback) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
    cb.logger.Info("response received",
        "peer", peer,
        "progress", fmt.Sprintf("%d/%d", stats.Success+stats.Failed, stats.Total))
}

func (cb *LogCallback) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
    cb.logger.Warn("request failed",
        "peer", peer,
        "error", err,
        "progress", fmt.Sprintf("%d/%d", stats.Success+stats.Failed, stats.Total))
}

func (cb *LogCallback) OnMajorityReached(stats BroadcastStats) {
    cb.logger.Info("🎯 MAJORITY REACHED!",
        "success_rate", stats.SuccessRate,
        "elapsed", stats.ElapsedTime)
}

func (cb *LogCallback) OnFullDone(stats BroadcastStats) {
    cb.logger.Info("✅ ALL DONE",
        "success", stats.Success,
        "failed", stats.Failed,
        "total_time", stats.ElapsedTime)
}

// 使用
tracker := NewBroadcastTracker("task-001", replicas)
tracker.SetCallback(&LogCallback{logger: slog.Default()})
rpc.BroadcastCall(ctx, replicas, req, ResponseMajority, tracker)
```

### 6.2 指标上报场景

```go
type MetricsCallback struct {
    metricsClient MetricsClient
}

func (cb *MetricsCallback) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
    cb.metricsClient.Gauge("broadcast.progress", stats.SuccessRate)
}

func (cb *MetricsCallback) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
    cb.metricsClient.Counter("broadcast.failures", 1)
}

func (cb *MetricsCallback) OnMajorityReached(stats BroadcastStats) {
    cb.metricsClient.Counter("broadcast.majority_reached", 1)
    cb.metricsClient.Histogram("broadcast.majority_latency", stats.ElapsedTime)
}

func (cb *MetricsCallback) OnFullDone(stats BroadcastStats) {
    cb.metricsClient.Histogram("broadcast.total_latency", stats.ElapsedTime)
}
```

### 6.3 错误处理场景

```go
type ErrorHandlingCallback struct {
    retryQueue chan model.PeerID
    logger     *slog.Logger
}

func (cb *ErrorHandlingCallback) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
    cb.logger.Info("request succeeded", "peer", peer)
}

func (cb *ErrorHandlingCallback) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
    cb.logger.Warn("request failed, adding to retry queue",
        "peer", peer,
        "error", err)

    // 失败时加入重试队列（非阻塞）
    select {
    case cb.retryQueue <- peer:
        cb.logger.Info("peer added to retry queue", "peer", peer)
    default:
        cb.logger.Warn("retry queue is full, dropping peer", "peer", peer)
    }
}

func (cb *ErrorHandlingCallback) OnMajorityReached(stats BroadcastStats) {
    cb.logger.Info("majority reached, starting retry processing")
    // 可以在这里触发重试逻辑
}

func (cb *ErrorHandlingCallback) OnFullDone(stats BroadcastStats) {
    cb.logger.Info("all requests completed",
        "success", stats.Success,
        "failed", stats.Failed,
        "success_rate", stats.SuccessRate)

    // 如果失败率过高，触发告警
    if stats.SuccessRate < 0.8 {
        cb.logger.Warn("low success rate detected", "success_rate", stats.SuccessRate)
        // 触发告警逻辑
    }
}

// 使用
retryQueue := make(chan model.PeerID, 100)
tracker := NewBroadcastTracker("task-001", replicas)
tracker.SetCallback(&ErrorHandlingCallback{
    retryQueue: retryQueue,
    logger:     slog.Default(),
})
rpc.BroadcastCall(ctx, replicas, req, ResponseMajority, tracker)

// 后台重试处理
go func() {
    for peer := range retryQueue {
        // 重试逻辑
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        err := rpc.RetryCall(ctx, peer, req)
        cancel()
        if err != nil {
            slog.Warn("retry failed", "peer", peer, "error", err)
        }
    }
}()
```

### 6.4 流水线触发场景

```go
type PipelineCallback struct {
    nextStage chan<- string
    logger    *slog.Logger
}

func (cb *PipelineCallback) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
    // 可选：处理每个响应
}

func (cb *PipelineCallback) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
    // 可选：处理失败
}

func (cb *PipelineCallback) OnMajorityReached(stats BroadcastStats) {
    cb.logger.Info("majority reached, triggering next stage")
    // 多数派达成，触发下一阶段处理
    cb.nextStage <- "majority_ready"
}

func (cb *PipelineCallback) OnFullDone(stats BroadcastStats) {
    cb.logger.Info("all done, triggering final confirmation")
    // 全部完成，触发最终确认
    cb.nextStage <- "all_done"
}
```

---

## 7. 参考资料

### 6.1 相关文档

- **Spike 文档**: `docs/07_spike/2026-02-20_broadcast-tracker-callback-mechanism.md`
- **RPC 中间件设计**: `docs/06_PM/feature/2026-02-18_PR-phase1-week3-4-rpc-codec-middleware_Pre.md`
- **编码规范**: `docs/03_development/01_编码规范文档.md`

### 6.2 相关代码

- `internal/transport/tracker.go` - BroadcastTracker 实现
- `internal/transport/broadcast.go` - 广播调用逻辑
- `internal/model/types.go` - 类型定义

---

## 8. 后续工作（可选）

### 8.1 可选增强（非本次 PR 范围）

**函数模式支持**：
```go
type (
    OnSuccessFunc  func(peer model.PeerID, resp model.Message, stats BroadcastStats)
    OnFailureFunc  func(peer model.PeerID, err error, stats BroadcastStats)
    OnMajorityFunc func(stats BroadcastStats)
    OnFullDoneFunc func(stats BroadcastStats)
)

func (t *BroadcastTracker) SetOnSuccess(fn OnSuccessFunc) { ... }
```

**适用场景**：简单日志、指标上报，不需要定义结构体

### 8.2 后续优化方向

1. **异步回调队列**（如果回调执行慢）
2. **回调超时控制**（防止回调长时间阻塞）
3. **回调重试机制**（处理临时失败）

---

## 九、版本历史

### v1.4（2026-02-21）⭐ **补充执行顺序和注意事项**

**修订原因**：补充回调执行顺序和实现注意事项（架构师评审意见）

**新增内容**：

1. ✅ **回调执行顺序**（针对每个响应）：
   ```
   1. OnSuccess / OnFailure（每次响应）
      ↓
   2. OnMajorityReached（达到多数派时，仅一次）
      ↓
   3. OnFullDone（全部完成时，仅一次）
   ```

2. ✅ **特殊场景说明**：
   - `OnMajorityReached` 和 `OnFullDone` 可能在**同一次** `RecordSuccess` 中顺序触发
   - 示例：3 个节点，第 3 个成功后同时触发两个回调
   - 保证顺序执行（OnSuccess → OnMajorityReached → OnFullDone）

3. ✅ **回调实现注意事项**（4 点）：
   - **避免死锁**：回调在锁外执行，但应避免调用 `BroadcastTracker` 的方法
   - **快速返回**：回调应快速返回（< 10ms），长时间处理应启动 goroutine
   - **线程安全**：回调可能被并发调用，实现需线程安全
   - **顺序独立性**：不要依赖回调的调用顺序（除文档明确保证的之外）

**关键改进**：
- ✅ 明确了回调执行顺序（避免实现者误解）
- ✅ 说明了特殊场景（Majority 和 FullDone 同时触发）
- ✅ 提供了实现注意事项（防止常见陷阱）

---

### v1.3（2026-02-21）⭐ **采纳全部建议并澄清关键问题**

**修订原因**：采纳架构师全部建议，澄清 Nil 参数行为、触发条件、仅触发一次机制

**新增内容**：

1. ✅ **Nil 参数行为明确**：
   - `OnSuccess` 的 `resp` 不会为 nil
   - `OnFailure` 的 `err` 不会为 nil（含具体错误类型说明）

2. ✅ **OnMajorityReached 触发条件明确**：
   - 只在 RecordSuccess 时检查，RecordFailure 不会触发
   - 3 个节点，2 个成功即触发（即使 1 个失败）

3. ✅ **"仅触发一次"机制**：
   - 添加标志位（`majorityCallbackTriggered`、`fullDoneCallbackTriggered`）
   - 锁内判断 + 锁外触发双重检查

4. ✅ **SuccessRate 除零保护**：
   - 添加 `if total > 0` 检查

5. ✅ **测试用例补充**：
   - 新增 4 个边界场景测试（空 targets、全部失败、先 Majority 后 FullDone、并发）

6. ✅ **时间估算调整**：
   - 回调触发逻辑：2h → 3h
   - 单元测试：2h → 2.5h
   - 集成测试：1h → 1.5h
   - 总时间：6.5h → 7.5h

7. ✅ **风险评估补充**：
   - 新增：回调重复触发、Nil 参数处理、并发安全问题

---

### v1.2（2026-02-21）⭐ **采纳架构师建议 1 和 2**

**修订原因**：采纳架构师建议，补充错误处理示例和性能基准测试

**新增内容**：

1. ✅ **错误处理场景**：添加 `ErrorHandlingCallback` 示例
2. ✅ **性能基准测试**：添加 `BenchmarkBroadcastCallback_Overhead` 和 `BenchmarkBroadcastCallback_Concurrent`

---

### v1.1（2026-02-21）⭐ **采纳架构师初步建议**

**修订原因**：采纳架构师初步建议，补充使用示例和时间戳统计

**新增内容**：

1. ✅ **第 6 章使用示例**：补充 4 个场景（日志、指标、错误处理、流水线）
2. ✅ **BroadcastStats 时间戳**：添加 `FirstResponseTime`、`MajorityReachTime`

---

### v1.0（2026-02-21）⭐ **初始版本**

**创建日期**：初始版本，基于 Spike 研究文档

**核心内容**：
- 接口定义（BroadcastCallback + BroadcastStats）
- 实施计划（5 个阶段，6.5 小时）
- 测试计划（7 个测试用例）
- 风险评估（5 个风险）

---

**Pre 文档版本**: v1.4
**创建日期**: 2026-02-21
**最后更新**: 2026-02-21
**作者**: 🤖 AI Agent
**状态**: ✅ 已通过架构师评审（已采纳全部建议并补充执行顺序和注意事项）
