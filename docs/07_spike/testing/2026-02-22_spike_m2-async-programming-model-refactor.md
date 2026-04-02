# 异步编程模型重构方案 v3.0

> **预研类型**: Spike
> **创建日期**: 2026-02-22
> **最后更新**: 2026-02-24
> **分支**: `spike/async-programming-model`
> **状态**: ✅ 已实施完成
> **文档版本**: v3.0
> **关键变更**: 
>   - 修复 Go 泛型限制（接口方法改用 any，辅助函数提供类型安全）
>   - 实施完成 AsyncOperation[T]、AsyncGroup[T]、GoroutineProvider、CronJobProvider
>   - 额外完成 internal/clock DDD 架构迁移
> **实施范围**: Transport 异步编程模型（AsyncOperation + GoroutineProvider + CronJobProvider）
> **参考文档**:
>   - [DDD架构 - AsyncOperation](./2026-02-18_spike_nexkv-ddd-interface.md#13-b3-asyncoperation)
>   - [DDD架构 - GoroutineProvider](./2026-02-18_spike_nexkv-ddd-interface.md#13-b4-goroutineprovider)
>   - [PR-073 全流程文档](../../06_PM/feature/2026-02-23_PR-073_feature_async-programming-model_Pre.md)

---

## 目录

1. [架构概述](#一架构概述)
2. [Infrastructure 层](#二infrastructure-层)
3. [CronJobProvider 扩展](#三cronjobprovider-扩展)
4. [实施范围：Transport 异步编程模型重构](#四实施范围transport-异步编程模型重构)
5. [实施完成总结](#五实施完成总结)
6. [架构师评审记录](#六架构师评审记录)

---

## 一、架构概述

### 1.1 Transport 异步编程模型

```
┌─────────────────────────────────────────────────────────────┐
│                  Infrastructure 层                          │
│  网络 / 协程池 / 异步操作                                    │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │  Transport   │  │  Goroutine   │  │   AsyncOp    │      │
│  │   (RPC)      │  │   Provider   │  │   [T]        │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
└─────────────────────────────────────────────────────────────┘
```

### 1.2 核心组件

| 组件 | 位置 | 异步抽象 | 说明 |
|------|------|----------|------|
| **Transport** | `internal/infrastructure/transport` | 使用 `AsyncOperation[T]` | RPC 传输实现 |
| **Goroutine Pool** | `internal/infrastructure/concurrency` | `GoroutineProvider` | 协程池管理 |
| **AsyncOp** | `pkg/async` | `AsyncOperation[T]` 实现 | 底层异步抽象 |

---

## 二、Infrastructure 层

### 2.1 职责
- 网络传输实现
- 协程池管理
- 异步操作实现
- 监控与日志

### 2.2 目录结构

```
internal/infrastructure/
├── transport/
│   ├── libp2p_rpc.go           # RPC 传输实现
│   ├── libp2p_rpc_adapter.go   # 旧接口适配器
│   └── async_lifecycle.go      # 异步生命周期管理
└── concurrency/
    ├── goroutine_provider.go   # GoroutineProvider 接口
    └── ants_provider.go        # ants 实现

pkg/async/
├── async_op.go                 # AsyncOp 实现
├── async_group.go              # AsyncGroup 实现
└── bridge.go                   # 桥接工具
```

### 2.3 GoroutineProvider 实现

```go
// internal/infrastructure/concurrency/goroutine_provider.go

package concurrency

import (
	"context"
	"time"
)

// GoroutineProvider 协程池提供者接口
// 设计：接口用 any（Go 限制）+ 辅助函数保类型安全（架构师评审通过 v2.9）
//
// ⚠️ 重要：Go 接口方法不能有类型参数（泛型），所以接口层使用 any 类型
// 类型安全通过辅助函数（helpers.go）提供，内部使用类型断言优化
type GoroutineProvider interface {
	// ======================================
	// 基础方法（接口层用 any，辅助函数提供类型安全）
	// ======================================

	// 简单任务：无参数，无返回值
	Submit(ctx context.Context, task func(context.Context)) error

	// 带参数：避免闭包陷阱（接口层用 any）
	SubmitWithArg(ctx context.Context, task func(context.Context, any), arg any) error

	// 带返回值：需要异步结果（接口层用 any）
	SubmitWithResult(ctx context.Context, task func(context.Context) (any, error)) Result[any]

	// 带参数和返回值：完整功能（接口层用 any）
	SubmitWithArgAndResult(
		ctx context.Context,
		task func(context.Context, any) (any, error),
		arg any,
	) Result[any]

	// ======================================
	// 快捷方法（高频需求，意图明确）
	// ======================================

	// 优先级任务
	SubmitWithPriority(ctx context.Context, priority Priority, task func(context.Context)) error

	// 延迟任务
	SubmitDelayed(ctx context.Context, delay time.Duration, task func(context.Context)) error

	// ======================================
	// 高级方法（复杂场景，选项模式）
	// ======================================

	// 灵活组合：优先级 + 延迟 + 未来扩展（接口层用 any）
	SubmitAdvanced(
		ctx context.Context,
		task func(context.Context, any) (any, error),
		arg any,
		opts ...SubmitOption,
	) Result[any]

	// ======================================
	// 批量方法（语义清晰，单独列出）
	// ======================================

	// 批量提交：快速执行多个任务（无参数，无返回值）
	SubmitBatch(ctx context.Context, tasks []func(context.Context)) error

	// 批量提交：快速执行多个任务（带参数，无返回值，接口层用 any）
	SubmitBatchWithArg(
		ctx context.Context,
		tasks []func(context.Context, any),
		args []any,
	) error

	// 批量提交：收集所有错误（无参数）
	SubmitBatchAllErrors(ctx context.Context, tasks []func(context.Context)) []error

	// 批量提交：收集所有错误（带参数，接口层用 any）
	SubmitBatchWithArgAllErrors(
		ctx context.Context,
		tasks []func(context.Context, any),
		args []any,
	) []error

	// 批量提交：带返回值（无参数，接口层用 any）
	SubmitBatchWithResult(
		ctx context.Context,
		tasks []func(context.Context) (any, error),
	) []Result[any]

	// 批量提交：带参数和返回值（接口层用 any）
	SubmitBatchWithArgAndResult(
		ctx context.Context,
		tasks []func(context.Context, any) (any, error),
		args []any,
	) []Result[any]

	// ======================================
	// 管理方法
	// ======================================

	Stats() PoolStats
	Health() HealthStatus
	SetCapacity(capacity int) error
	Close() error
	CloseWithTimeout(timeout time.Duration) error
}

// ======================================
// 选项模式定义（用于 SubmitAdvanced）
// ======================================

// SubmitOption 提交选项
type SubmitOption func(*submitOptions)

type submitOptions struct {
	priority Priority
	delay    time.Duration
}

// WithPriority 设置优先级
func WithPriority(priority Priority) SubmitOption {
	return func(opts *submitOptions) {
		opts.priority = priority
	}
}

// WithDelay 设置延迟
func WithDelay(delay time.Duration) SubmitOption {
	return func(opts *submitOptions) {
		opts.delay = delay
	}
}

// 未来可扩展：
// func WithTimeout(timeout time.Duration) SubmitOption
// func WithRetry(count int) SubmitOption
// func WithCallback(cb func()) SubmitOption

// Priority 任务优先级
type Priority int

const (
	PriorityCritical Priority = iota
	PriorityHigh
	PriorityNormal
	PriorityLow
)

type Result[T any] struct {
	Value T
	Err   error
}

type PoolStats struct {
	Total      int
	ByPriority map[Priority]int
}

type HealthStatus int

const (
	HealthStatusHealthy HealthStatus = iota
	HealthStatusUnhealthy
)

// ======================================
// 泛型辅助函数（提供类型安全）
// ======================================

// 注意：以下函数在 helpers.go 中定义，通过类型断言优化性能
//
// func SubmitWithArg[T any](ctx context.Context, provider GoroutineProvider, task func(context.Context, T), arg T) error
// func SubmitWithResult[T any](ctx context.Context, provider GoroutineProvider, task func(context.Context) (T, error)) *TypedResult[T]
// func SubmitWithArgAndResult[T any, R any](ctx context.Context, provider GoroutineProvider, task func(context.Context, T) (R, error), arg T) *TypedResult[R]
// func SubmitAdvanced[T any, R any](ctx context.Context, provider GoroutineProvider, task func(context.Context, T) (R, error), arg T, opts ...SubmitOption) *TypedResult[R]

// 使用示例：
//   err := concurrency.SubmitWithArg(ctx, provider, func(ctx context.Context, idx int) {
//       fmt.Println("任务", idx)
//   }, 42)

### 2.4 AsyncOperation 实现

```go
// pkg/async/async_op.go

package async

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/infrastructure/transport"
)

// AsyncOperation 统一异步操作接口
type AsyncOperation[T any] interface {
	Get(ctx context.Context) (T, error)
	Status() OperationStatus
	Cancel() (canceled bool, err error)
	Discard() error
	IsStarted() bool
	OnComplete(callback func(T, error)) string
	OffComplete(cbID string) error
}

// OperationStatus 操作状态
type OperationStatus int

const (
	StatusPending OperationStatus = iota
	StatusRunning
	StatusCompleted
	StatusFailed
	StatusCanceled
	StatusDiscarded
	StatusTimeout
)

func (s OperationStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCanceled, StatusDiscarded, StatusTimeout:
		return true
	default:
		return false
	}
}

// AsyncOp 异步操作实现
type AsyncOp[T any] struct {
	lifecycle *transport.AsyncLifecycle
	resultCh  chan Result[T]
	done      chan struct{}
	value     T
	err       error
	callbacks map[string]func(T, error)
	cbMu      sync.RWMutex
	cbSeq     int64
	execFunc  func(ctx context.Context) (T, error)
	status    OperationStatus
	statusMu  sync.RWMutex
	started   bool
	cancel    context.CancelFunc
}

// Result 结果包装器
type Result[T any] struct {
	Value T
	Err   error
}

// OpOption 选项
type OpOption func(*opConfig)

type opConfig struct {
	timeout           time.Duration
	goroutineProvider GoroutineProvider  // ✅ 新增：可选的 GoroutineProvider
}

// WithTimeout 设置超时
func WithTimeout(timeout time.Duration) OpOption {
	return func(c *opConfig) {
		c.timeout = timeout
	}
}

// WithGoroutineProvider 设置 GoroutineProvider ✅ 新增
// 如果设置，AsyncOp 将使用 GoroutineProvider 提交任务，而不是直接启动 goroutine
func WithGoroutineProvider(provider GoroutineProvider) OpOption {
	return func(c *opConfig) {
		c.goroutineProvider = provider
	}
}

// NewOp 创建异步操作
func NewOp[T any](
	ctx context.Context,
	execFunc func(ctx context.Context) (T, error),
	opts ...OpOption,
) AsyncOperation[T] {
	config := &opConfig{timeout: 30 * time.Second}
	for _, opt := range opts {
		opt(config)
	}

	var cancel context.CancelFunc
	if config.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, config.timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}

	lifecycle := transport.NewAsyncLifecycle()

	op := &AsyncOp[T]{
		lifecycle: lifecycle,
		resultCh:  make(chan Result[T], 1),
		done:      make(chan struct{}),
		callbacks: make(map[string]func(T, error)),
		execFunc:  execFunc,
		status:    StatusPending,
		cancel:    cancel,
	}

	// ✅ 执行函数包装器（复用逻辑）
	execWrapper := func() {
		defer close(op.done)

		op.statusMu.Lock()
		op.status = StatusRunning
		op.started = true
		op.statusMu.Unlock()

		value, err := execFunc(lifecycle.Context())

		op.statusMu.Lock()
		if ctx.Err() == context.DeadlineExceeded {
			op.status = StatusTimeout
			op.err = ctx.Err()
		} else if err != nil {
			op.status = StatusFailed
			op.err = err
			op.value = value
		} else {
			op.status = StatusCompleted
			op.value = value
		}
		op.statusMu.Unlock()

		select {
		case op.resultCh <- Result[T]{Value: op.value, Err: op.err}:
		default:
		}

		op.executeCallbacks(op.value, op.err)
	}

	// ✅ 根据配置选择执行方式
	if config.goroutineProvider != nil {
		// 使用 GoroutineProvider 提交任务
		config.goroutineProvider.Submit(ctx, func(ctx context.Context) {
			execWrapper()
		})
	} else {
		// 直接启动 goroutine
		lifecycle.Go(execWrapper)
	}

	return op
}

// Get 实现
func (op *AsyncOp[T]) Get(ctx context.Context) (T, error) {
	select {
	case result := <-op.resultCh:
		return result.Value, result.Err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Status 实现
func (op *AsyncOp[T]) Status() OperationStatus {
	op.statusMu.RLock()
	defer op.statusMu.RUnlock()
	return op.status
}

// Cancel 实现
func (op *AsyncOp[T]) Cancel() (bool, error) {
	op.statusMu.Lock()
	defer op.statusMu.Unlock()

	if op.status.IsTerminal() {
		return false, fmt.Errorf("operation already in terminal state: %v", op.status)
	}

	op.status = StatusCanceled
	op.lifecycle.Cancel()
	return true, nil
}

// Discard 实现
func (op *AsyncOp[T]) Discard() error {
	op.statusMu.Lock()
	defer op.statusMu.Unlock()

	if op.status.IsTerminal() {
		return fmt.Errorf("cannot discard operation in terminal state: %v", op.status)
	}

	op.status = StatusDiscarded
	op.lifecycle.Cancel()
	return nil
}

// IsStarted 实现
func (op *AsyncOp[T]) IsStarted() bool {
	op.statusMu.RLock()
	defer op.statusMu.RUnlock()
	return op.started
}

// OnComplete 实现
func (op *AsyncOp[T]) OnComplete(callback func(T, error)) string {
	op.cbMu.Lock()
	defer op.cbMu.Unlock()

	op.cbSeq++
	cbID := fmt.Sprintf("cb-%d", op.cbSeq)

	select {
	case <-op.done:
		go safeCallback(callback, op.value, op.err)
	default:
		op.callbacks[cbID] = callback
	}

	return cbID
}

// OffComplete 实现
func (op *AsyncOp[T]) OffComplete(cbID string) error {
	op.cbMu.Lock()
	defer op.cbMu.Unlock()

	if _, exists := op.callbacks[cbID]; !exists {
		return fmt.Errorf("callback not found: %s", cbID)
	}

	delete(op.callbacks, cbID)
	return nil
}

// ResultChan 返回结果通道（扩展方法）
func (op *AsyncOp[T]) ResultChan() <-chan Result[T] {
	return op.resultCh
}

// executeCallbacks 执行回调
func (op *AsyncOp[T]) executeCallbacks(value T, err error) {
	op.cbMu.RLock()
	callbacks := make([]func(T, error), 0, len(op.callbacks))
	for _, cb := range op.callbacks {
		callbacks = append(callbacks, cb)
	}
	op.cbMu.RUnlock()

	for _, cb := range callbacks {
		cb := cb
		go safeCallback(cb, value, err)
	}
}

// safeCallback 安全执行回调
func safeCallback[T any](callback func(T, error), value T, err error) {
	defer func() {
		if r := recover(); r != nil {
			// 记录 panic 但不影响主流程
		}
	}()
	callback(value, err)
}
```

### 2.5 AsyncGroup 实现

```go
// pkg/async/async_group.go

package async

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/transport"
)

// AsyncGroup 批量异步操作组
type AsyncGroup[T any] struct {
	lifecycle *transport.AsyncLifecycle
	targets   []model.PeerID
	ops       map[model.PeerID]AsyncOperation[T]
	results   map[model.PeerID]T
	errors    map[model.PeerID]error
	mu        sync.RWMutex
	anyDone       chan struct{}
	majorityDone  chan struct{}
	allDone       chan struct{}
	callback      service.BroadcastListener
	startTime             time.Time
	firstResponseTime     time.Time
	majorityReachTime     time.Time
	firstResponseRecorded bool
}

// GroupResult 批量操作结果
type GroupResult[T any] struct {
	Values       map[model.PeerID]T
	Errors       map[model.PeerID]error
	SuccessPeers []model.PeerID
	FailedPeers  []model.PeerID
}

// NewGroup 创建批量异步操作组
func NewGroup[T any](
	ctx context.Context,
	targets []model.PeerID,
	execFunc func(ctx context.Context, target model.PeerID) (T, error),
) *AsyncGroup[T] {
	lifecycle := transport.NewAsyncLifecycle()

	targetsCopy := make([]model.PeerID, len(targets))
	copy(targetsCopy, targets)

	g := &AsyncGroup[T]{
		lifecycle:    lifecycle,
		targets:      targetsCopy,
		ops:          make(map[model.PeerID]AsyncOperation[T]),
		results:      make(map[model.PeerID]T),
		errors:       make(map[model.PeerID]error),
		anyDone:      make(chan struct{}),
		majorityDone: make(chan struct{}),
		allDone:      make(chan struct{}),
		startTime:    time.Now(),
	}

	for _, target := range targets {
		target := target
		op := NewOp[T](lifecycle.Context(), func(ctx context.Context) (T, error) {
			return execFunc(ctx, target)
		})
		g.ops[target] = op

		op.OnComplete(func(value T, err error) {
			g.handleResult(target, value, err)
		})
	}

	return g
}

// handleResult 处理单个结果
func (g *AsyncGroup[T]) handleResult(peer model.PeerID, value T, err error) {
	var callback service.BroadcastListener
	var stats service.BroadcastStats
	var shouldTriggerMajority bool
	var shouldTriggerAllDone bool

	g.mu.Lock()

	if !g.firstResponseRecorded {
		g.firstResponseTime = time.Now()
		g.firstResponseRecorded = true
		close(g.anyDone)
	}

	if err != nil {
		g.errors[peer] = err
	} else {
		g.results[peer] = value
	}

	total := len(g.targets)
	success := len(g.results)
	failed := len(g.errors)
	completed := success + failed

	if success >= (total/2)+1 && g.majorityReachTime.IsZero() {
		g.majorityReachTime = time.Now()
		shouldTriggerMajority = true
		close(g.majorityDone)
	}

	if completed >= total {
		shouldTriggerAllDone = true
		close(g.allDone)
	}

	stats = service.BroadcastStats{
		TotalPeers:        total,
		SuccessCount:      success,
		FailureCount:      failed,
		StartTime:         g.startTime,
		FirstResponseTime: g.firstResponseTime,
		MajorityReachTime: g.majorityReachTime,
	}

	callback = g.callback
	g.mu.Unlock()

	if callback != nil {
		if err != nil {
			callback.OnFailure(peer, err, stats)
		} else {
			callback.OnSuccess(peer, value, stats)
		}
		if shouldTriggerMajority {
			callback.OnMajorityReached(stats)
		}
		if shouldTriggerAllDone {
			callback.OnFullDone(stats)
		}
	}
}

// SetCallback 设置回调
func (g *AsyncGroup[T]) SetCallback(callback service.BroadcastListener) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.callback = callback
}

// WaitAll 等待全部完成
func (g *AsyncGroup[T]) WaitAll(ctx context.Context) GroupResult[T] {
	select {
	case <-g.allDone:
	case <-ctx.Done():
	}

	return g.getResult()
}

// WaitMajority 等待多数完成
func (g *AsyncGroup[T]) WaitMajority(ctx context.Context) GroupResult[T] {
	select {
	case <-g.majorityDone:
	case <-ctx.Done():
	}

	return g.getResult()
}

// WaitAny 等待任意一个完成
func (g *AsyncGroup[T]) WaitAny(ctx context.Context) (model.PeerID, T, error) {
	select {
	case <-g.anyDone:
	case <-ctx.Done():
		var zero T
		return "", zero, ctx.Err()
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	for peer, value := range g.results {
		return peer, value, nil
	}

	for peer, err := range g.errors {
		var zero T
		return peer, zero, err
	}

	var zero T
	return "", zero, fmt.Errorf("no result available")
}

// getResult 获取结果
func (g *AsyncGroup[T]) getResult() GroupResult[T] {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := GroupResult[T]{
		Values: make(map[model.PeerID]T),
		Errors: make(map[model.PeerID]error),
	}

	for peer, value := range g.results {
		result.Values[peer] = value
		result.SuccessPeers = append(result.SuccessPeers, peer)
	}

	for peer, err := range g.errors {
		result.Errors[peer] = err
		result.FailedPeers = append(result.FailedPeers, peer)
	}

	return result
}
```

---

## 三、实施范围：Transport 异步编程模型重构

### 8.1 背景：Transport 已采用 DDD 架构

**当前项目状态**：
- ✅ **Transport 层已使用 DDD 方式编写**
- ✅ **目录结构已建立**：
  ```
  internal/
  ├── domain/                        # 领域层（已存在）
  │   ├── model/                     # 领域模型
  │   └── service/                   # 领域服务（RPC）
  └── infrastructure/                # 基础设施层（已存在）
      └── transport/                 # 传输层（DDD 实现）

  pkg/
  └── async/                         # 异步抽象（待实现）
  ```

### 8.2 本次实施范围

**✅ 包含内容**：
1. **AsyncOperation[T] 核心实现**（`pkg/async/`）
   - AsyncOperation[T] 接口定义
   - AsyncOp[T] 实现
   - AsyncGroup[T] 批量操作

2. **GoroutineProvider 改进版**（`internal/infrastructure/concurrency/`）
   - 统一使用 context.Context
   - SubmitWithArg[T] 泛型方法
   - 协程池管理

3. **Transport 层改造**（`internal/infrastructure/transport/` + `internal/domain/service/`）
   - 使用新的 AsyncOperation[T]
   - 集成 GoroutineProvider
   - RPC 异步服务

**❌ 不包含内容**：
- ❌ Storage Engine（独立 PR）
- ❌ Control Plane（独立 PR）
- ❌ API 层（独立 PR）
- ❌ 其他模块

### 8.3 目录结构

```
pkg/async/                           # 异步抽象层（新增）
├── async_op.go                      # AsyncOperation[T] 接口 + 实现
├── async_group.go                   # AsyncGroup[T] 批量操作
└── bridge.go                        # 桥接工具（可选）

internal/infrastructure/
├── concurrency/                     # 并发管理（新增）
│   └── goroutine_provider.go        # 协程池提供者（改进版）
└── transport/                       # 传输层（改造）
    ├── libp2p_rpc.go                # RPC 传输实现（使用新异步）
    └── async_lifecycle.go           # 异步生命周期管理

internal/domain/
├── model/                           # 领域模型（已存在）
└── service/                         # 领域服务（改造）
    ├── rpc_async.go                 # RPCAsync 接口 + 实现
    └── broadcast.go                 # BroadcastService（可选）
```

### 8.4 实施路径（2-3 周）

```
Week 1: 基础设施层（pkg/async + concurrency）
├── Day 1-2: AsyncOperation[T] 核心实现
│   ├── async_op.go（接口定义 + 实现）
│   ├── 单元测试
│   └── 基准测试
├── Day 3-4: GoroutineProvider 改进版
│   ├── goroutine_provider.go（改进接口）
│   ├── ants 集成
│   └── 单元测试
└── Day 5: AsyncGroup[T] 批量操作
    ├── async_group.go
    └── 单元测试

Week 2: Transport 层改造
├── Day 1-2: RPCAsync 接口实现
│   ├── rpc_async.go（领域服务）
│   └── 集成测试
├── Day 3-4: Transport 集成
│   ├── libp2p_rpc.go（改造）
│   ├── 使用新的 AsyncOperation[T]
│   └── 集成测试
└── Day 5: 端到端测试
    ├── 5 节点集群测试
    ├── 压力测试
    └── 性能对比

Week 3（可选）: 优化与文档
├── 性能优化
├── 文档更新
└── Code Review
```

### 8.5 关键交付物

| 交付物 | 文件路径 | 说明 |
|--------|---------|------|
| **AsyncOperation[T]** | `pkg/async/async_op.go` | 统一异步操作接口 |
| **AsyncGroup[T]** | `pkg/async/async_group.go` | 批量异步操作 |
| **GoroutineProvider** | `internal/infrastructure/concurrency/goroutine_provider.go` | 协程池管理（改进版） |
| **RPCAsync** | `internal/domain/service/rpc_async.go` | RPC 异步服务 |
| **Transport 集成** | `internal/infrastructure/transport/libp2p_rpc.go` | 使用新异步接口 |

### 8.6 测试策略

**单元测试**（覆盖率 > 80%）：
- AsyncOperation[T] 所有状态转换
- GoroutinePool 任务提交/取消/超时
- AsyncGroup[T] 批量操作

**集成测试**：
- RPCAsync 调用流程
- Transport 层集成
- 5 节点集群通信

**性能测试**：
- 吞吐量对比（新旧实现）
- 延迟对比
- 资源使用（Goroutine 数量、内存）

### 8.7 成功标准

✅ **功能标准**：
- [ ] AsyncOperation[T] 支持所有状态（Pending/Running/Completed/Failed/Canceled）
- [ ] GoroutineProvider 所有方法正常工作
- [ ] Transport 层成功集成新异步接口
- [ ] 5 节点集群测试通过

✅ **质量标准**：
- [ ] 单元测试覆盖率 > 80%
- [ ] 集成测试通过
- [ ] 性能无回退（对比旧实现）
- [ ] Code Review 通过

✅ **文档标准**：
- [ ] 接口文档完整
- [ ] 使用示例清晰
- [ ] Post 文档总结到位

---

## 三、CronJobProvider 扩展

### 3.1 设计目标

CronJobProvider 是 GoroutineProvider 的**补充扩展**，用于管理定时任务：
- **定时调度**：基于 Cron 表达式周期性执行任务
- **优先级集成**：定时任务提交到 GoroutineProvider 时保留优先级
- **生命周期管理**：支持启动、停止、暂停、恢复等操作
- **统一管理**：与 GoroutineProvider 共享协程池资源

### 3.2 接口定义

```go
package concurrency

import (
	"context"
	"time"
)

// ======================================
// CronJobProvider 定时任务提供者接口
// ======================================

// CronSpec Cron 表达式
type CronSpec string

// CronJobStatus 定时任务状态
type CronJobStatus int32

const (
	CronJobStatusScheduled CronJobStatus = iota
	CronJobStatusRunning
	CronJobStatusPaused
	CronJobStatusStopped
)

// CronJobInfo 定时任务信息
type CronJobInfo struct {
	ID        string
	Name      string
	Spec      CronSpec
	Status    CronJobStatus
	NextRun   time.Time
	LastRun   *time.Time
	CreatedAt time.Time
}

// CronJobProvider 定时任务提供者接口
type CronJobProvider interface {
	// 生命周期
	Start()
	Stop() context.Context

	// ======================================
	// 基础方法（无参数）
	// ======================================
	Register(spec CronSpec, name string, task func(context.Context)) (string, error)
	RegisterWithPriority(spec CronSpec, name string, priority Priority, task func(context.Context)) (string, error)

	// ======================================
	// 带参数方法（避免闭包陷阱）✅ 新增
	// ======================================
	RegisterWithArg[T any](spec CronSpec, name string, task func(context.Context, T), arg T) (string, error)
	RegisterWithPriorityAndArg[T any](spec CronSpec, name string, priority Priority, task func(context.Context, T), arg T) (string, error)

	// 任务控制
	Pause(jobID string) error
	Resume(jobID string) error
	Unregister(jobID string) error

	// 任务查询
	GetJob(jobID string) (*CronJobInfo, error)
	ListJobs() []*CronJobInfo
}
```

### 3.3 实现方案（基于 robfig/cron + ants）

```go
package concurrency

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// ======================================
// 基于 robfig/cron + ants 的 CronJobProvider 实现
// ======================================

var _ CronJobProvider = (*RobfigCronProvider)(nil)

type RobfigCronProvider struct {
	mu                sync.RWMutex
	cron              *cron.Cron
	goroutineProvider GoroutineProvider
	jobs              map[string]*cronJobEntry
	nameToID          map[string]string
}

type cronJobEntry struct {
	id        string
	name      string
	entryID   cron.EntryID
	spec      CronSpec
	status    CronJobStatus
	priority  Priority
	taskFunc  func(context.Context)
	createdAt time.Time
}

func NewRobfigCronProvider(goroutineProvider GoroutineProvider) *RobfigCronProvider {
	c := cron.New(
		cron.WithSeconds(),
		cron.WithChain(
			cron.Recover(cron.DefaultLogger),
		),
	)
	return &RobfigCronProvider{
		cron:              c,
		goroutineProvider: goroutineProvider,
		jobs:              make(map[string]*cronJobEntry),
		nameToID:          make(map[string]string),
	}
}

func (r *RobfigCronProvider) Start() {
	r.cron.Start()
}

func (r *RobfigCronProvider) Stop() context.Context {
	return r.cron.Stop()
}

func (r *RobfigCronProvider) Register(
	spec CronSpec,
	name string,
	taskFunc func(context.Context),
) (string, error) {
	return r.RegisterWithPriority(spec, name, PriorityNormal, taskFunc)
}

func (r *RobfigCronProvider) RegisterWithPriority(
	spec CronSpec,
	name string,
	priority Priority,
	taskFunc func(context.Context),
) (string, error) {
	return r.registerInternal(spec, name, priority, func(ctx context.Context, _ any) {
		taskFunc(ctx)
	}, nil)
}

// RegisterWithArg 注册带参数的定时任务 ✅ 新增
func (r *RobfigCronProvider) RegisterWithArg[T any](
	spec CronSpec,
	name string,
	taskFunc func(context.Context, T),
	arg T,
) (string, error) {
	return r.RegisterWithPriorityAndArg(spec, name, PriorityNormal, taskFunc, arg)
}

// RegisterWithPriorityAndArg 注册带参数和优先级的定时任务 ✅ 新增
func (r *RobfigCronProvider) RegisterWithPriorityAndArg[T any](
	spec CronSpec,
	name string,
	priority Priority,
	taskFunc func(context.Context, T),
	arg T,
) (string, error) {
	return r.registerInternal(spec, name, priority, func(ctx context.Context, a any) {
		taskFunc(ctx, a.(T))
	}, arg)
}

// registerInternal 内部注册方法（统一实现）
func (r *RobfigCronProvider) registerInternal(
	spec CronSpec,
	name string,
	priority Priority,
	taskFunc func(context.Context, any),
	arg any,
) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.nameToID[name]; exists {
		return "", fmt.Errorf("job with name %s already exists", name)
	}

	wrappedFunc := func() {
		r.mu.RLock()
		jobID, ok := r.nameToID[name]
		if !ok {
			r.mu.RUnlock()
			return
		}
		entry, ok := r.jobs[jobID]
		r.mu.RUnlock()

		if !ok || entry.status == CronJobStatusPaused {
			return
		}

		err := r.goroutineProvider.SubmitWithArgAndResult(
			context.Background(),
			func(ctx context.Context, a any) (any, error) {
				taskFunc(ctx, a)
				return nil, nil
			},
			arg,
		)
		if err != nil {
			fmt.Printf("Failed to submit cron job %s to goroutine pool: %v\n", name, err)
		}
	}

	entryID, err := r.cron.AddFunc(string(spec), wrappedFunc)
	if err != nil {
		return "", fmt.Errorf("failed to register cron job: %w", err)
	}

	jobID := fmt.Sprintf("cron-%s-%d", name, time.Now().UnixNano())

	entry := &cronJobEntry{
		id:        jobID,
		name:      name,
		entryID:   entryID,
		spec:      spec,
		status:    CronJobStatusScheduled,
		priority:  priority,
		taskFunc:  wrappedFunc,
		createdAt: time.Now(),
	}

	r.jobs[jobID] = entry
	r.nameToID[name] = jobID

	return jobID, nil
}

func (r *RobfigCronProvider) Pause(jobID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.jobs[jobID]
	if !ok {
		return fmt.Errorf("job not found: %s", jobID)
	}

	if entry.status != CronJobStatusScheduled && entry.status != CronJobStatusRunning {
		return fmt.Errorf("job cannot be paused: %s", jobID)
	}

	entry.status = CronJobStatusPaused
	return nil
}

func (r *RobfigCronProvider) Resume(jobID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.jobs[jobID]
	if !ok {
		return fmt.Errorf("job not found: %s", jobID)
	}

	if entry.status != CronJobStatusPaused {
		return fmt.Errorf("job cannot be resumed: %s", jobID)
	}

	entry.status = CronJobStatusScheduled
	return nil
}

func (r *RobfigCronProvider) Unregister(jobID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.jobs[jobID]
	if !ok {
		return fmt.Errorf("job not found: %s", jobID)
	}

	r.cron.Remove(entry.entryID)
	delete(r.jobs, jobID)
	delete(r.nameToID, entry.name)
	return nil
}

func (r *RobfigCronProvider) GetJob(jobID string) (*CronJobInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}

	cronEntry := r.cron.Entry(entry.entryID)

	var lastRun *time.Time
	if !cronEntry.Prev.IsZero() {
		lastRun = &cronEntry.Prev
	}

	return &CronJobInfo{
		ID:        entry.id,
		Name:      entry.name,
		Spec:      entry.spec,
		Status:    entry.status,
		NextRun:   cronEntry.Next,
		LastRun:   lastRun,
		CreatedAt: entry.createdAt,
	}, nil
}

func (r *RobfigCronProvider) ListJobs() []*CronJobInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	jobs := make([]*CronJobInfo, 0, len(r.jobs))
	for id := range r.jobs {
		job, _ := r.GetJob(id)
		jobs = append(jobs, job)
	}
	return jobs
}
```

### 3.4 使用示例

```go
// 1. 初始化 GoroutineProvider
goroutineProvider, _ := NewAntsGoroutineProvider(nil)

// 2. 初始化 CronJobProvider
cronProvider := NewRobfigCronProvider(goroutineProvider)
cronProvider.Start()

// 3. 注册定时任务（无参数）
jobID, _ := cronProvider.RegisterWithPriority(
	"0 */5 * * * *",           // 每 5 分钟
	"wal_cleanup",              // 任务名称
	PriorityLow,                // 低优先级
	func(ctx context.Context) {
		// 执行 WAL 清理
		cleanupWAL(ctx)
	},
)

// 3.1 注册带参数的定时任务 ✅ 新增示例
dataDirs := []string{"/var/nexkv/data1", "/var/nexkv/data2"}
for _, dir := range dataDirs {
	cronProvider.RegisterWithArg(
		"0 */10 * * * *",           // 每 10 分钟
		"cleanup_"+dir,              // 任务名称
		func(ctx context.Context, dataDir string) {
			// ✅ 直接使用参数，无闭包陷阱
			cleanupDirectory(ctx, dataDir)
		},
		dir,  // 参数传递
	)
}

// 3.2 注册带参数和优先级的定时任务 ✅ 新增示例
cronProvider.RegisterWithPriorityAndArg(
	"0 */30 * * * *",              // 每 30 分钟
	"raft_snapshot",                // 任务名称
	PriorityHigh,                   // 高优先级
	func(ctx context.Context, nodeID string) {
		// 执行 Raft 快照
		createSnapshot(ctx, nodeID)
	},
	"node-1",  // 参数：节点 ID
)

// 4. 查询任务
job, _ := cronProvider.GetJob(jobID)
fmt.Printf("Next run: %v\n", job.NextRun)

// 5. 暂停任务
cronProvider.Pause(jobID)

// 6. 恢复任务
cronProvider.Resume(jobID)

// 7. 停止所有任务
ctx := cronProvider.Stop()
<-ctx.Done()
```

### 3.5 与 GoroutineProvider 的关系

```
┌─────────────────────────────────────────────────────────────┐
│                    CronJobProvider                          │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  robfig/cron - 定时调度                              │   │
│  │  • Cron 表达式解析                                   │   │
│  │  • 任务调度触发                                      │   │
│  └─────────────────────────────────────────────────────┘   │
│                          │                                  │
│                          ▼                                  │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  GoroutineProvider - 任务执行                        │   │
│  │  • 优先级队列                                        │   │
│  │  • 协程池管理                                        │   │
│  │  • 资源限制                                          │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

**设计要点**：
1. **解耦调度与执行**：Cron 只负责调度，实际执行交给 GoroutineProvider
2. **优先级传递**：定时任务可以指定优先级，确保重要任务优先执行
3. **资源复用**：与即时任务共享协程池，避免资源浪费
4. **独立扩展**：可以替换 Cron 实现（如使用 quartz-go）而不影响执行层

### 3.6 适用场景

| 场景 | 示例 | 优先级建议 |
|------|------|-----------|
| WAL 清理 | 每 5 分钟清理过期 WAL | Low |
| 数据压缩 | 每小时压缩 SSTable | Normal |
| Raft 快照 | 每 10 分钟生成快照 | High |
| 健康检查 | 每分钟检查节点状态 | Critical |

---

## 四、总结

### 4.1 核心设计决策

1. **接口定义位置**
   - `AsyncOperation[T]` → `pkg/async`（跨层共享）
   - `GoroutineProvider` → `internal/infrastructure/concurrency`
   - `CronJobProvider` → `internal/infrastructure/concurrency`（扩展）

2. **依赖方向**
   - Transport 依赖 Infrastructure 层
   - CronJobProvider 依赖 GoroutineProvider
   - 通过接口解耦（依赖倒置）

3. **异步抽象层级**
   - Infrastructure: 底层实现（AsyncOp + GoroutineProvider + CronJobProvider）
   - Transport: 使用 AsyncOperation[T]

### 4.2 参考文档

1. **DDD架构 - AsyncOperation**: [2026-02-18_spike_nexkv-ddd-interface.md](./2026-02-18_spike_nexkv-ddd-interface.md#13-b3-asyncoperation)
2. **DDD架构 - GoroutineProvider**: [2026-02-18_spike_nexkv-ddd-interface.md](./2026-02-18_spike_nexkv-ddd-interface.md#13-b4-goroutineprovider)

---

---

## 五、实施完成总结

### 5.1 实施概况

**实施时间**: 2026-02-23 至 2026-02-24（2天）  
**实施分支**: `feature/PR-073-async-programming-model`  
**实施状态**: ✅ 已完成  
**构建状态**: ✅ `make build` 通过  
**测试状态**: ✅ `make test` 全部通过

### 5.2 交付物清单

| 交付物 | 文件路径 | 状态 | 说明 |
|--------|---------|------|------|
| AsyncOperation[T] | `pkg/async/async_op.go` | ✅ 完成 | 7种状态、取消、超时、回调 |
| AsyncGroup[T] | `pkg/async/async_group.go` | ✅ 完成 | WaitAny/WaitMajority/WaitAll |
| GoroutineProvider 接口 | `internal/domain/service/concurrency.go` | ✅ 完成 | 领域服务接口定义 |
| GoroutineProvider 实现 | `internal/infrastructure/concurrency/goroutine_ants_provider.go` | ✅ 完成 | ants 协程池实现 |
| GoroutineProvider 辅助函数 | `internal/infrastructure/concurrency/goroutine_helpers.go` | ✅ 完成 | 泛型辅助函数 |
| CronJobProvider 接口 | `internal/domain/service/cron.go` | ✅ 完成 | 领域服务接口定义 |
| CronJobProvider 实现 | `internal/infrastructure/concurrency/cron_robfig_provider.go` | ✅ 完成 | robfig/cron 实现 |
| CronJobProvider 辅助函数 | `internal/infrastructure/concurrency/cron_helpers.go` | ✅ 完成 | 泛型辅助函数 |
| HLC 值对象 | `internal/domain/model/hlc.go` | ✅ 完成 | DDD 值对象 |
| HLCProvider 实现 | `internal/infrastructure/clock/hlc.go` | ✅ 完成 | 时钟提供者 |

### 5.3 架构实现验证

**DDD 分层验证**:
- ✅ **Domain 层**: `domain/model` 定义 HLC、Goroutine/Cron 值对象；`domain/service` 定义 Provider 接口
- ✅ **Application 层**: `application/clock` 提供时钟应用服务
- ✅ **Infrastructure 层**: `infrastructure/concurrency` 和 `infrastructure/clock` 提供具体实现
- ✅ **Pkg 层**: `pkg/async` 提供跨层共享的异步抽象

**设计模式验证**:
- ✅ **泛型 + any 模式**: 接口方法使用 `any`，辅助函数提供类型安全
- ✅ **选项模式**: `OpOption`、`GoroutineSubmitOption` 等
- ✅ **依赖倒置**: Transport 依赖 `service.GoroutineProvider` 接口，而非具体实现

### 5.4 测试覆盖

| 模块 | 测试文件 | 用例数 | 覆盖率 |
|------|---------|--------|--------|
| pkg/async | async_op_test.go | 15+ | >80% |
| pkg/async | async_group_test.go | 10+ | >80% |
| infrastructure/concurrency | goroutine_provider_test.go | 20+ | >80% |
| infrastructure/concurrency | cron_robfig_provider_test.go | 10+ | >80% |
| infrastructure/clock | hlc_provider_test.go | 10+ | >80% |

### 5.5 与 Spike 文档的差异

| 项目 | Spike 规划 | 实际实现 | 差异说明 |
|------|-----------|----------|----------|
| bridge.go | 规划中 | 未实现 | 暂不需要桥接工具 |
| Transport 改造 | Week 2 | 部分完成 | RPCAsync 接口已定义，集成待续 |
| DDD 迁移 | 未规划 | 已完成 | 额外完成 clock 模块迁移 |
| 实施周期 | 2-3 周 | 2 天 | 核心接口已完成，Transport 集成后续进行 |

### 5.6 后续工作

**待完成**（后续 PR）：
- [ ] Transport 层完全集成新的异步接口
- [ ] RPCAsync 完整实现
- [ ] 5 节点集群端到端测试
- [ ] 性能基准测试对比

---

## 六、架构师评审记录

### 10.1 评审意见：GoroutineProvider 接口改进

**评审日期**: 2026-02-23
**评审人**: 👤 架构师
**评审状态**: ✅ **通过**

#### 问题描述

当前第 6.3 节的 `GoroutineProvider` 接口存在**不一致性**：

```go
// ❌ 当前设计（第 6.3 节）
type GoroutineProvider interface {
    Submit(task func()) error                                    // 无 context
    SubmitWithContext(ctx context.Context, task func(context.Context)) error  // 有 context
    SubmitWithResult[T any](task func() (T, error)) Result[T]   // 无 context
}
```

**问题**：
1. 命名不一致：`Submit` vs `SubmitWithContext`
2. 闭包捕获陷阱：`for i := 0; i < 10; i++ { gp.Submit(func(){ fmt.Println(i) }) }` 可能都打印 10
3. 无法取消任务：`Submit` 和 `SubmitWithResult` 的任务无法响应 context 取消

#### 改进方案

```go
// ✅ 改进设计（正式开发时采用）
type GoroutineProvider interface {
    // 基础方法 - 统一使用 context
    Submit(ctx context.Context, task func(context.Context)) error

    // 带参数传递（避免闭包陷阱）
    SubmitWithArg[T any](ctx context.Context, task func(context.Context, T), arg T) error

    // 带结果返回
    SubmitWithResult[T any](ctx context.Context, task func(context.Context) (T, error)) Result[T]

    // 带参数和结果
    SubmitWithArgAndResult[T any, R any](
        ctx context.Context,
        task func(context.Context, T) (R, error),
        arg T,
    ) Result[R]

    // 其他方法统一添加 ctx...
    SubmitWithPriority(ctx context.Context, priority Priority, task func(context.Context)) error
    SubmitDelayed(ctx context.Context, delay time.Duration, task func(context.Context)) error

    // 池管理
    Stats() PoolStats
    Health() HealthStatus
    Close() error
}
```

#### 改进点对比

| 改进项 | 原设计（第 6.3 节） | 改进设计 | 优势 |
|--------|-------------------|---------|------|
| 统一 Context | ❌ 部分有，部分无 | ✅ 所有方法都有 | 符合 Go 惯例 |
| 避免闭包陷阱 | ❌ 依赖闭包捕获 | ✅ `SubmitWithArg` 泛型传参 | 避免数据竞争 |
| 命名一致性 | ❌ `Submit` vs `SubmitWithContext` | ✅ 统一风格 | 降低认知负担 |
| 取消支持 | ❌ 部分任务无法取消 | ✅ 所有任务可检查 `ctx.Done()` | 支持超时/取消 |

#### 使用示例

```go
// 示例 1：简单任务（改进后）
gp.Submit(ctx, func(ctx context.Context) {
    select {
    case <-ctx.Done():
        return  // ✅ 支持取消
    default:
        // 执行任务
    }
})

// 示例 2：带参数传递（避免闭包捕获）
for i := 0; i < 10; i++ {
    gp.SubmitWithArg(ctx, func(ctx context.Context, taskID int) {
        fmt.Printf("Task %d\n", taskID)  // ✅ 正确打印 0-9
    }, i)
}

// 示例 3：带结果返回
result := gp.SubmitWithResult(ctx, func(ctx context.Context) (string, error) {
    return "success", nil
})
```

#### 评审结论

**状态**: ✅ **通过，建议采纳**

**确认点**：
1. ✅ 接口设计改进合理：统一使用 Context 符合 Go 惯例
2. ✅ 解决实际问题：避免闭包捕获陷阱，支持任务取消
3. ⚠️ 破坏性变更：原 `Submit(func())` 改为 `Submit(ctx, func(ctx))`

#### 跟进事项

- [ ] 正式开发时采用改进后的接口设计
- [ ] Pre 文档中明确标注此变更
- [ ] 同步更新 `ddd-interface.md` 文档
- [ ] 为已有代码提供迁移指南

---

**文档版本**: v2.9
**最后更新**: 2026-02-23
**Spike 状态**: ✅ 已批准（评审通过）
**变更说明**:
- v2.2: 按 DDD 5层架构重新组织文档结构
- v2.3: 添加架构师评审记录（GoroutineProvider 接口改进通过）
- v2.4: 第八章改为"直接全新方案"，删除适配器模式（Transport 已采用 DDD 架构）
- v2.5: 明确实施范围：仅 Transport 相关（AsyncOperation + GoroutineProvider + Transport 改造），不包含 Storage/Control Plane/API 层
- v2.6: 删除所有其他层章节，正文采用改进后的 GoroutineProvider 接口（统一使用 context）
- v2.7: 批量方法支持 T（输入参数）和 R（返回值），添加 SubmitBatchWithArg/SubmitBatchWithArgAllErrors/SubmitBatchWithArgAndResult；添加 CronJobProvider 扩展章节
- v2.8: 修复 CronJobProvider 实现中的类型错误（cronJobEntry.taskFunc 改为 func(context.Context)）
- v2.9: **修复 Go 泛型限制**：接口方法改用 `any` 类型（Go 接口不支持泛型方法），通过独立辅助函数提供类型安全（内部使用类型断言优化）
