# BroadcastProgress Callback 机制设计

> **文档类型**: Spike 研究文档
> **创建日期**: 2026-02-20
> **最后更新**: 2026-02-20
> **文档版本**: v1.1
> **关联文档**:
> - `docs/06_PM/feature/2026-02-18_PR-phase1-week3-4-rpc-codec-middleware_Pre.md`

---

## 一、背景与目标

### 1.1 背景

`BroadcastProgress` 用于追踪广播调用的进度，支持等待多数派 (`WaitMajority`) 和全部完成 (`WaitFull`)。但在实际使用中，用户可能需要在达到关键进度节点时**立即执行某些操作**，而不是被动等待。

### 1.2 目标

为 `BroadcastProgress` 添加 callback/handler 机制，支持在以下节点触发用户自定义逻辑：
- 每次收到成功响应时
- 每次收到失败响应时
- 达到多数派时
- 全部完成时

---

## 二、设计方案

### 2.1 方案对比

| 方案 | 优点 | 缺点 | 适用场景 |
|------|------|------|----------|
| **A: 接口模式** | 类型安全，可扩展 | 需要定义结构体 | 复杂回调逻辑 |
| **B: 函数模式** | 简洁，匿名函数方便 | 多个回调需多次设置 | 简单回调逻辑 |
| **C: Channel 模式** | 与 Go 并发模型契合 | 增加复杂度 | 需要复杂协调 |

**推荐**: 方案 A（接口模式）+ 方案 B（函数模式）作为备选

---

## 三、详细设计

### 3.1 接口模式（推荐）

```go
// BroadcastListener 广播进度回调接口
type BroadcastListener interface {
    // OnSuccess 每次收到成功响应时调用
    OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats)

    // OnFailure 每次收到失败响应时调用
    OnFailure(peer model.PeerID, err error, stats BroadcastStats)

    // OnMajorityReached 达到多数派时调用（仅调用一次）
    OnMajorityReached(stats BroadcastStats)

    // OnFullDone 全部完成时调用（仅调用一次）
    OnFullDone(stats BroadcastStats)
}

// BroadcastStats 广播统计信息
type BroadcastStats struct {
    TaskID       string
    Total        int           // 总节点数
    Success      int           // 成功数
    Failed       int           // 失败数
    Pending      int           // 待响应数
    SuccessRate  float64       // 成功率
    ElapsedTime  time.Duration // 已耗时
}
```

#### 修改 BroadcastProgress

```go
type BroadcastProgress struct {
    taskID       string
    targets      []model.PeerID
    responses    map[model.PeerID]model.Message
    failures     map[model.PeerID]error
    mu           sync.RWMutex
    fullDone     chan struct{}
    majorityDone chan struct{}

    // 新增：回调（可选）
    callback     BroadcastListener

    // 新增：记录开始时间
    startTime    time.Time
}

// SetCallback 设置进度回调（必须在开始之前设置）
func (t *BroadcastProgress) SetCallback(cb BroadcastListener) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.callback = cb
}
```

#### 触发回调（P0 修复：锁外执行，避免死锁）

```go
// safeCallback 安全执行回调，防止 panic 影响主流程
func safeCallback(fn func()) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("[BroadcastProgress] callback panic recovered: %v", r)
        }
    }()
    fn()
}

func (t *BroadcastProgress) RecordSuccess(peer model.PeerID, resp model.Message) {
    var callback BroadcastListener
    var stats BroadcastStats
    var shouldTriggerMajority bool
    var shouldTriggerFullDone bool

    // === 锁内：只做状态更新 ===
    t.mu.Lock()
    t.responses[peer] = resp

    // 检查 Majority
    majority := len(t.targets)/2 + 1
    if len(t.responses) >= majority {
        select {
        case <-t.majorityDone:
            // 已经触发过
        default:
            close(t.majorityDone)
            shouldTriggerMajority = true
        }
    }

    // 检查 FullDone
    if len(t.responses)+len(t.failures) == len(t.targets) {
        select {
        case <-t.fullDone:
            // 已经触发过
        default:
            close(t.fullDone)
            shouldTriggerFullDone = true
        }
    }

    // 准备回调数据
    callback = t.callback
    stats = t.buildStatsLocked()
    t.mu.Unlock()
    // === 锁外：执行回调，避免死锁 ===

    if callback == nil {
        return
    }

    // 触发 OnSuccess 回调
    safeCallback(func() {
        callback.OnSuccess(peer, resp, stats)
    })

    // 触发 OnMajorityReached 回调
    if shouldTriggerMajority {
        safeCallback(func() {
            callback.OnMajorityReached(stats)
        })
    }

    // 触发 OnFullDone 回调
    if shouldTriggerFullDone {
        safeCallback(func() {
            callback.OnFullDone(stats)
        })
    }
}

func (t *BroadcastProgress) RecordFailure(peer model.PeerID, err error) {
    var callback BroadcastListener
    var stats BroadcastStats
    var shouldTriggerFullDone bool

    // === 锁内：只做状态更新 ===
    t.mu.Lock()
    t.failures[peer] = err

    // 检查 FullDone
    if len(t.responses)+len(t.failures) == len(t.targets) {
        select {
        case <-t.fullDone:
            // 已经触发过
        default:
            close(t.fullDone)
            shouldTriggerFullDone = true
        }
    }

    // 准备回调数据
    callback = t.callback
    stats = t.buildStatsLocked()
    t.mu.Unlock()
    // === 锁外：执行回调，避免死锁 ===

    if callback == nil {
        return
    }

    // 触发 OnFailure 回调
    safeCallback(func() {
        callback.OnFailure(peer, err, stats)
    })

    // 触发 OnFullDone 回调
    if shouldTriggerFullDone {
        safeCallback(func() {
            callback.OnFullDone(stats)
        })
    }
}

func (t *BroadcastProgress) buildStatsLocked() BroadcastStats {
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
    }
}
```

---

### 3.2 函数模式（备选）

```go
type (
    OnSuccessFunc      func(peer model.PeerID, resp model.Message, stats BroadcastStats)
    OnFailureFunc      func(peer model.PeerID, err error, stats BroadcastStats)
    OnMajorityFunc     func(stats BroadcastStats)
    OnFullDoneFunc     func(stats BroadcastStats)
)

func (t *BroadcastProgress) SetOnSuccess(fn OnSuccessFunc) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.onSuccess = fn
}

func (t *BroadcastProgress) SetOnFailure(fn OnFailureFunc) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.onFailure = fn
}

func (t *BroadcastProgress) SetOnMajority(fn OnMajorityFunc) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.onMajority = fn
}

func (t *BroadcastProgress) SetOnFullDone(fn OnFullDoneFunc) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.onFullDone = fn
}
```

---

## 四、使用示例

### 4.1 日志记录场景

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
tracker := NewBroadcastProgress("task-001", replicas)
tracker.SetCallback(&LogCallback{logger: slog.Default()})
rpc.BroadcastCall(ctx, replicas, req, ResponseMajority, tracker)
```

### 4.2 流水线触发场景

```go
type PipelineCallback struct {
    nextStage chan<- string
}

func (cb *PipelineCallback) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
    // 可选：处理每个响应
}

func (cb *PipelineCallback) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
    // 可选：处理失败
}

func (cb *PipelineCallback) OnMajorityReached(stats BroadcastStats) {
    // 多数派达成，触发下一阶段处理
    cb.nextStage <- "majority_ready"
}

func (cb *PipelineCallback) OnFullDone(stats BroadcastStats) {
    // 全部完成，触发最终确认
    cb.nextStage <- "all_done"
}
```

### 4.3 指标上报场景

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

---

## 五、注意事项

### 5.1 线程安全（P0 已修复）

- ✅ **回调在锁外执行**，避免死锁风险
- ✅ 回调实现可以安全调用 `BroadcastProgress` 的其他方法
- ✅ `safeCallback` 防止回调 panic 影响主流程
- 回调实现仍应**快速返回**，避免阻塞
- 如需长时间处理，应在回调中启动 goroutine

### 5.2 回调执行顺序

```
RecordSuccess
    ↓
OnSuccess (每次成功响应)
    ↓
OnMajorityReached (达到多数派时，仅一次)
    ↓
OnFullDone (全部完成时，仅一次)

RecordFailure
    ↓
OnFailure (每次失败响应)
    ↓
OnFullDone (全部完成时，仅一次)
```

### 5.3 回调设置时机

- 必须在 `BroadcastCall` 之前设置
- 不支持运行时修改（简化设计）

### 5.4 Panic 保护

- 所有回调都通过 `safeCallback` 包装执行
- 回调 panic 会被 recover 并记录日志
- Panic 不会影响 `BroadcastProgress` 的正常运作

---

## 六、决策建议

| 场景 | 推荐方案 |
|------|----------|
| 需要多个回调协调 | 接口模式 |
| 简单日志/指标 | 函数模式 |
| 需要状态管理 | 接口模式 |
| 临时/一次性使用 | 函数模式 |

**建议实现**: 先实现接口模式，函数模式作为语法糖后续添加。

---

## 七、变更记录

| 版本 | 日期 | 变更内容 |
|------|------|----------|
| v1.0 | 2026-02-20 | 初始版本 |
| v1.1 | 2026-02-20 | P0/P1/P2 修复：<br>- P0: 回调改为锁外执行，避免死锁<br>- P1: 修正关联文档路径<br>- P1: 补充 RecordFailure 回调逻辑<br>- P1: 统一方法命名为 buildStatsLocked<br>- P2: 添加 OnFailure 回调<br>- P2: SuccessRate 除零保护<br>- P2: 添加 safeCallback panic 保护 |

---

**文档版本**: v1.1
**创建日期**: 2026-02-20
**最后更新**: 2026-02-20
**作者**: AI Agent
