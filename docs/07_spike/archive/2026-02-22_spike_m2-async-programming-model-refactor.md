# 异步编程模型重构方案 v2.2

> **预研类型**: Spike
> **创建日期**: 2026-02-22
> **最后更新**: 2026-02-22
> **分支**: `spike/async-programming-model`
> **状态**: ✅ 已批准（作为 M2 存储引擎前置依赖）
> **文档版本**: v2.2（新增方案B：并行实现策略）
> **设计原则**: 与现有架构对齐、方案B并行实现、生产级可用
> **参考文档**:
>   - [DDD架构 - AsyncOperation](./2026-02-18_spike_nexkv-ddd-interface.md#13-b3-asyncoperation)
>   - [DDD架构 - GoroutineProvider](./2026-02-18_spike_nexkv-ddd-interface.md#13-b4-goroutineprovider)
>   - [M2存储引擎 - 异步接口](./2026-02-21_spike_m2-storage-engine-interface.md#11-asyncoperation)
>   - [M2存储引擎 - 实施路线图](./2026-02-21_spike_m2-storage-engine-roadmap.md)

---

## 一、现状分析

### 1.1 现有异步模式盘点

NexKV 目前已存在多种异步编程模式：

| 组件 | 模式 | 特点 |
|------|------|------|
| `AsyncChannel` | Channel 风格 | `SendChan() chan<- []byte`, `RecvChan() <-chan MsgOrError` |
| `AsyncStream` | Channel 风格 | `ReadChan() <-chan ReadResult`, `WriteChan() chan<- WriteRequest` |
| `BroadcastProgress` | 回调 + Channel | `BroadcastListener` 接口 + `WaitFull()/WaitMajority()` |
| `Libp2pRPC.CallAsync` | 回调函数 | `cb func(model.Message, error)` |
| `pendingCall` | Channel | `responseCh chan service.ResponseMsg` |
| `AsyncOperation[T]` | 泛型异步 | `Get(ctx)/Status()/Cancel()/OnComplete()` |

### 1.2 核心问题

1. **风格不一致**：Channel 风格 vs 回调风格 vs 阻塞风格混用
2. **缺乏统一抽象**：相同概念（如"等待完成"）在不同组件中重复实现
3. **类型不安全**：部分接口使用 `interface{}` 或 `any`
4. **生命周期管理分散**：`AsyncLifecycle`、`closeCh`、`done chan struct{}` 多种模式

---

## 二、设计目标

### 2.1 核心原则

1. **与现有架构对齐**：复用 `BroadcastListener`、`AsyncLifecycle`、`pkg/errors` 等已有组件
2. **方案B并行实现**：新旧实现并存，内部统一，逐步替换（详见第九章）
3. **Channel 优先**：与 NexKV 现有 Channel 风格保持一致
4. **类型安全**：利用 Go 泛型，但避免过度复杂

### 2.2 非目标

- ❌ 不追求完全的泛型抽象（避免 `Async[T, S Status]` 这种复杂约束）
- ❌ 不强制统一所有异步场景（允许特定场景保留专用接口）
- ❌ 不引入新的依赖（继续使用 `conc`、`logrus/slog` 等现有库）

---

## 三、统一接口设计

### 3.1 标准接口定义（来自DDD文档）

#### AsyncOperation[T] 接口（v19.0）

```go
// AsyncOperation 统一泛型异步操作接口
// 设计原则：
// 1. 语义精确：每个方法的行为明确无歧义
// 2. 非阻塞查询：Status()/IsStarted() 都是非阻塞的
// 3. 资源管理：Discard() 用于提前释放资源
// 4. 回调安全：OnComplete 回调带 panic recover
//
// 📖 参考: docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md
type AsyncOperation[T any] interface {
    // Get 等待异步操作完成并返回结果
    // ctx: 用于超时控制和取消
    // 返回: 泛型结果 T 和可能的错误
    Get(ctx context.Context) (T, error)

    // Status 返回操作当前状态（非阻塞，无歧义）
    // 返回: OperationStatus 枚举值
    Status() OperationStatus

    // Cancel 取消异步操作（语义精确）
    // 返回:
    //   - canceled: 是否成功取消（true=成功取消，false=无法取消）
    //   - err: 取消失败的原因（如操作已完成或已取消）
    Cancel() (canceled bool, err error)

    // Discard 放弃结果，释放资源（v19.0新增）
    // 用于不再需要结果时提前释放资源
    // 返回: 可能的错误（如操作已完成）
    Discard() error

    // IsStarted 返回是否已启动（v19.0新增）
    // 返回: true=已启动，false=未启动
    IsStarted() bool

    // OnComplete 注册回调函数（结果就绪时调用）
    // 回调函数接收结果 T 和错误 error
    // 回调执行带 recover() 隔离 panic，不会影响主流程
    // 返回: 回调ID，用于后续注销
    OnComplete(callback func(T, error)) string

    // OffComplete 注销回调函数
    // 如果 cbID 不存在，返回 ErrCallbackNotFound
    OffComplete(cbID string) error
}

// OperationStatus 操作状态枚举（v19.0）
const (
    StatusPending   OperationStatus = iota // 待执行（v19.0更新）
    StatusRunning                          // 执行中（v19.0新增）
    StatusCompleted                        // 操作成功完成
    StatusFailed                           // 操作失败
    StatusCanceled                         // 操作被取消
    StatusDiscarded                        // 操作被丢弃（v19.0新增）
    StatusTimeout                          // 操作超时
)

// IsTerminal 返回是否为终态（终态不可变更）
func (s OperationStatus) IsTerminal() bool {
    switch s {
    case StatusCompleted, StatusFailed, StatusCanceled, StatusDiscarded, StatusTimeout:
        return true
    default:
        return false
    }
}
```

#### GoroutineProvider 接口（v19.0）

```go
// GoroutineProvider 协程池提供者接口
// 设计原则：
// 1. 优先级控制：支持 Critical/High/Normal/Low 四级优先级
// 2. 泛型支持：SubmitWithResult[T] 提供类型安全
// 3. 批量操作：SubmitBatch* 支持批量任务提交
// 4. 资源管理：Close/CloseWithTimeout 优雅关闭
//
// 📖 参考: docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md
type GoroutineProvider interface {
    // ========================================
    // 基础提交方法
    // ========================================
    
    // Submit 提交无返回值任务
    Submit(task func()) error
    
    // SubmitWithContext 提交带 context 的任务
    SubmitWithContext(ctx context.Context, task func(context.Context)) error
    
    // ========================================
    // 泛型结果提交方法（类型安全）
    // ========================================
    
    // SubmitWithResult 提交有返回值的任务（泛型）
    SubmitWithResult[T any](task func() (T, error)) Result[T]
    
    // SubmitWithPriority 按优先级提交任务
    // priority: Critical/High/Normal/Low
    SubmitWithPriority(priority Priority, task func()) error
    
    // SubmitDelayed 延迟提交任务
    SubmitDelayed(delay time.Duration, task func()) error
    
    // ========================================
    // 批量操作
    // ========================================
    
    // SubmitBatch 批量提交任务（快速失败）
    SubmitBatch(tasks []func()) error
    
    // SubmitBatchAllErrors 批量提交任务（返回所有错误）
    SubmitBatchAllErrors(tasks []func()) []error
    
    // SubmitBatchWithResult 批量提交任务（返回所有结果）
    SubmitBatchWithResult[T any](tasks []func() (T, error)) []Result[T]
    
    // ========================================
    // 管理方法
    // ========================================
    
    // Stats 返回协程池统计信息
    Stats() PoolStats
    
    // Health 返回健康状态
    Health() HealthStatus
    
    // SetCapacity 动态调整协程池容量
    SetCapacity(capacity int) error
    
    // Close 优雅关闭协程池
    Close() error
    
    // CloseWithTimeout 带超时的关闭
    CloseWithTimeout(timeout time.Duration) error
}

// Priority 任务优先级
const (
    PriorityCritical Priority = iota // 关键任务（如元数据同步）
    PriorityHigh                     // 高优先级（如客户端请求）
    PriorityNormal                   // 普通优先级（默认）
    PriorityLow                      // 低优先级（如后台压缩）
)

// Result[T] 泛型结果包装器
type Result[T any] struct {
    Value T
    Err   error
}
```

#### AntsGoroutineProvider 实现（基于 ants 库）

```go
// pkg/concurrency/ants_provider.go

package concurrency

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/panjf2000/ants/v2"
)

// AntsGoroutineProvider 基于 ants 的 GoroutineProvider 实现
type AntsGoroutineProvider struct {
	pools map[Priority]*ants.Pool  // ✅ 4个优先级池
	mu    sync.RWMutex
}

// ProviderConfig 配置
type ProviderConfig struct {
	PoolSizes map[Priority]int // 每个优先级的池大小
}

// NewAntsGoroutineProvider 创建 ants provider
func NewAntsGoroutineProvider(config *ProviderConfig) *AntsGoroutineProvider {
	p := &AntsGoroutineProvider{
		pools: make(map[Priority]*ants.Pool),
	}

	// ✅ 创建4个优先级池
	for priority, size := range config.PoolSizes {
		priority := priority // 捕获循环变量
		pool, err := ants.NewPool(size, ants.WithPanicHandler(func(err interface{}) {
			slog.Error("[GoroutineProvider] goroutine panic",
				"priority", priority,
				"error", err)
		}))
		if err != nil {
			panic(fmt.Sprintf("failed to create pool for priority %v: %v", priority, err))
		}
		p.pools[priority] = pool
	}

	return p
}

// Submit 提交无返回值任务
func (p *AntsGoroutineProvider) Submit(task func()) error {
	return p.SubmitWithPriority(PriorityNormal, task)
}

// SubmitWithContext 提交带 context 的任务
func (p *AntsGoroutineProvider) SubmitWithContext(ctx context.Context, task func(context.Context)) error {
	return p.SubmitWithPriority(PriorityNormal, func() {
		task(ctx)
	})
}

// SubmitWithResult 提交有返回值的任务（泛型）
func SubmitWithResult[T any](p *AntsGoroutineProvider, task func() (T, error)) Result[T] {
	result := make(chan Result[T], 1)

	err := p.SubmitWithPriority(PriorityNormal, func() {
		value, err := task()
		result <- Result[T]{Value: value, Err: err}
	})

	if err != nil {
		var zero T
		return Result[T]{Value: zero, Err: err}
	}

	return <-result
}

// SubmitWithPriority 按优先级提交任务
func (p *AntsGoroutineProvider) SubmitWithPriority(priority Priority, task func()) error {
	p.mu.RLock()
	pool, ok := p.pools[priority]
	p.mu.RUnlock()

	if !ok {
		return errors.Wrapf(errors.ErrInvalidParam,
			"invalid priority: %v", priority)
	}

	return pool.Submit(task)
}

// SubmitDelayed 延迟提交任务
func (p *AntsGoroutineProvider) SubmitDelayed(delay time.Duration, task func()) error {
	time.AfterFunc(delay, func() {
		if err := p.Submit(task); err != nil {
			slog.Error("[GoroutineProvider] delayed task submit failed", "error", err)
		}
	})
	return nil
}

// SubmitBatch 批量提交任务（快速失败）
func (p *AntsGoroutineProvider) SubmitBatch(tasks []func()) error {
	for _, task := range tasks {
		if err := p.Submit(task); err != nil {
			return err
		}
	}
	return nil
}

// SubmitBatchAllErrors 批量提交任务（返回所有错误）
func (p *AntsGoroutineProvider) SubmitBatchAllErrors(tasks []func()) []error {
	var errs []error
	for _, task := range tasks {
		if err := p.Submit(task); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// SubmitBatchWithResult 批量提交任务（返回所有结果）
func SubmitBatchWithResult[T any](p *AntsGoroutineProvider, tasks []func() (T, error)) []Result[T] {
	results := make([]Result[T], len(tasks))
	var wg sync.WaitGroup

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, t func() (T, error)) {
			defer wg.Done()
			results[idx] = SubmitWithResult(p, t)
		}(i, task)
	}

	wg.Wait()
	return results
}

// Stats 返回协程池统计信息
func (p *AntsGoroutineProvider) Stats() PoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := PoolStats{}
	for priority, pool := range p.pools {
		stats.Total += pool.Running()
		stats.ByPriority[priority] = pool.Running()
	}
	return stats
}

// Health 返回健康状态
func (p *AntsGoroutineProvider) Health() HealthStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, pool := range p.pools {
		if pool.IsClosed() {
			return HealthStatusUnhealthy
		}
	}
	return HealthStatusHealthy
}

// SetCapacity 动态调整协程池容量
func (p *AntsGoroutineProvider) SetCapacity(priority Priority, size int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	pool, ok := p.pools[priority]
	if !ok {
		return errors.Wrapf(errors.ErrInvalidParam,
			"invalid priority: %v", priority)
	}

	pool.Tune(size)
	return nil
}

// Close 优雅关闭协程池
func (p *AntsGoroutineProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var errs []error
	for priority, pool := range p.pools {
		if err := pool.Release(); err != nil {
			errs = append(errs, errors.Wrapf(err, "failed to close pool %v", priority))
		}
	}

	if len(errs) > 0 {
		return errors.New("failed to close some pools")
	}
	return nil
}

// CloseWithTimeout 带超时的关闭
func (p *AntsGoroutineProvider) CloseWithTimeout(timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- p.Close()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return errors.New("close timeout")
	}
}

// PoolStats 协程池统计信息
type PoolStats struct {
	Total       int
	ByPriority  map[Priority]int
}

// HealthStatus 健康状态
type HealthStatus int

const (
	HealthStatusHealthy   HealthStatus = iota
	HealthStatusUnhealthy
)

// ✅ 使用示例
func ExampleUsage() {
	// 创建 provider
	config := &ProviderConfig{
		PoolSizes: map[Priority]int{
			PriorityCritical: 100,
			PriorityHigh:     200,
			PriorityNormal:   500,
			PriorityLow:      1000,
		},
	}
	provider := NewAntsGoroutineProvider(config)
	defer provider.Close()

	// 提交任务
	provider.Submit(func() {
		// 执行任务
	})

	// 按优先级提交
	provider.SubmitWithPriority(PriorityHigh, func() {
		// 高优先级任务
	})

	// 提交带返回值的任务
	result := SubmitWithResult(provider, func() (string, error) {
		return "result", nil
	})
	if result.Err != nil {
		// 处理错误
	}
	value := result.Value
	_ = value
}
```

### 3.2 核心实现：AsyncOp

基于标准 `AsyncOperation[T]` 接口的实现：

```go
// pkg/async/async_op.go

package async

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/infrastructure/transport"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// AsyncOp 统一异步操作实现
// 实现 AsyncOperation[T] 接口，提供 Channel + 回调双风格支持
type AsyncOp[T any] struct {
	lifecycle *transport.AsyncLifecycle
	resultCh  chan Result[T]
	done      chan struct{}

	// 结果（仅一次写入）
	value T
	err   error

	// 回调管理
	callbacks map[string]func(T, error)
	cbMu      sync.RWMutex
	cbSeq     int64

	// 执行函数
	execFunc func(ctx context.Context) (T, error)

	// 状态
	status   OperationStatus
	statusMu sync.RWMutex
	started  bool

	// ✅ 超时控制（v19.0 新增）
	cancel context.CancelFunc
}

// ============================================================================
// 选项模式（v19.0 新增）
// ============================================================================

// OpOption 异步操作选项
type OpOption func(*opConfig)

// opConfig 异步操作配置
type opConfig struct {
	timeout time.Duration
}

// WithTimeout 设置超时时间
func WithTimeout(timeout time.Duration) OpOption {
	return func(c *opConfig) {
		c.timeout = timeout
	}
}

// NewOp 创建新的异步操作
func NewOp[T any](
	ctx context.Context,
	execFunc func(ctx context.Context) (T, error),
	opts ...OpOption,
) AsyncOperation[T] {
	// ✅ 应用默认选项（v19.0 新增）
	config := &opConfig{
		timeout: 30 * time.Second, // ✅ 默认超时 30 秒
	}
	for _, opt := range opts {
		opt(config)
	}

	// ✅ 创建带超时的上下文
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
		cancel:    cancel, // ✅ 保存 cancel 函数
	}

	// 启动执行
	lifecycle.Go(func() {
		defer close(op.done)

		// 更新状态为 Running
		op.statusMu.Lock()
		op.status = StatusRunning
		op.started = true
		op.statusMu.Unlock()

		// 执行
		value, err := execFunc(lifecycle.Context())

		// ✅ 检查超时（v19.0 新增）
		op.statusMu.Lock()
		if ctx.Err() == context.DeadlineExceeded {
			op.status = StatusTimeout
			op.err = ctx.Err()
			var zero T
			op.value = zero
		} else {
			// 更新状态和结果
			op.value = value
			op.err = err
			if err != nil {
				op.status = StatusFailed
			} else {
				op.status = StatusCompleted
			}
		}
		op.statusMu.Unlock()

		// 发送结果到 channel
		select {
		case op.resultCh <- Result[T]{Value: op.value, Err: op.err}:
		default:
		}

		// 执行回调
		op.executeCallbacks(op.value, op.err)
	})

	return op
}

// Get 实现 AsyncOperation 接口
func (op *AsyncOp[T]) Get(ctx context.Context) (T, error) {
	select {
	case result := <-op.resultCh:
		return result.Value, result.Err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Status 实现 AsyncOperation 接口
func (op *AsyncOp[T]) Status() OperationStatus {
	op.statusMu.RLock()
	defer op.statusMu.RUnlock()
	return op.status
}

// Cancel 实现 AsyncOperation 接口
func (op *AsyncOp[T]) Cancel() (bool, error) {
	op.statusMu.Lock()
	defer op.statusMu.Unlock()

	// ✅ 使用 IsTerminal() 统一检查所有终态（v19.0 修复）
	if op.status.IsTerminal() {
		switch op.status {
		case StatusCompleted:
			return false, errors.New("operation already completed")
		case StatusCanceled:
			return false, errors.New("operation already canceled")
		case StatusDiscarded:
			return false, errors.New("operation already discarded")
		case StatusTimeout:
			return false, errors.New("operation already timeout")
		case StatusFailed:
			return false, errors.New("operation already failed")
		}
	}

	op.status = StatusCanceled
	op.lifecycle.Cancel()
	return true, nil
}

// Discard 实现 AsyncOperation 接口（v19.0）
func (op *AsyncOp[T]) Discard() error {
	op.statusMu.Lock()
	defer op.statusMu.Unlock()

	// ✅ 使用 IsTerminal() 统一检查（v19.0 修复）
	// 将终态检查放在最前面，避免重复检查
	if op.status.IsTerminal() {
		return errors.Wrapf(errors.ErrCompleted,
			"cannot discard operation in terminal state: %v", op.status)
	}

	op.status = StatusDiscarded
	op.lifecycle.Cancel()
	return nil
}

// IsStarted 实现 AsyncOperation 接口（v19.0）
func (op *AsyncOp[T]) IsStarted() bool {
	op.statusMu.RLock()
	defer op.statusMu.RUnlock()
	return op.started
}

// OnComplete 实现 AsyncOperation 接口
func (op *AsyncOp[T]) OnComplete(callback func(T, error)) string {
	op.cbMu.Lock()
	defer op.cbMu.Unlock()
	
	// 生成回调ID
	op.cbSeq++
	cbID := fmt.Sprintf("cb-%d", op.cbSeq)
	
	// 检查是否已完成
	select {
	case <-op.done:
		// 已完成，立即异步执行回调
		go safeCallback(callback, op.value, op.err)
	default:
		// 未完成，添加到回调列表
		op.callbacks[cbID] = callback
	}
	
	return cbID
}

// OffComplete 实现 AsyncOperation 接口
func (op *AsyncOp[T]) OffComplete(cbID string) error {
	op.cbMu.Lock()
	defer op.cbMu.Unlock()

	if _, exists := op.callbacks[cbID]; !exists {
		return errors.New("callback not found")
	}

	delete(op.callbacks, cbID)
	return nil
}

// ResultChan 返回结果通道（Channel 风格）
// 这是 AsyncOp 的扩展方法，不是 AsyncOperation 接口的一部分
// 用于支持 Channel 风格的使用
//
// 示例：
//   op := NewOp(ctx, execFunc)
//   select {
//   case result := <-op.ResultChan():
//       // 处理结果
//   case <-ctx.Done():
//       // 处理超时或取消
//   }
//
// 注意：
//   - 返回的是只读 channel，不能向其发送数据
//   - channel 缓冲区大小为 1，确保结果不会丢失
//   - 适合与 select 语句配合使用
func (op *AsyncOp[T]) ResultChan() <-chan Result[T] {
	return op.resultCh
}

// executeCallbacks 执行所有回调
func (op *AsyncOp[T]) executeCallbacks(value T, err error) {
	op.cbMu.RLock()
	callbacks := make([]func(T, error), 0, len(op.callbacks))
	for _, cb := range op.callbacks {
		callbacks = append(callbacks, cb)
	}
	op.cbMu.RUnlock()
	
	for _, cb := range callbacks {
		cb := cb // 捕获循环变量
		go safeCallback(cb, value, err)
	}
}

// safeCallback 安全执行回调，防止 panic
func safeCallback[T any](cb func(T, error), value T, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("[AsyncOp] callback panic recovered", "panic", r)
		}
	}()
	cb(value, err)
}
```

### 3.3 批量操作：AsyncGroup

```go
// pkg/async/async_group.go

package async

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// AsyncGroup 批量异步操作组
// 设计原则：
// 1. 与 BroadcastProgress 风格一致（回调接口、统计信息）
// 2. 支持多种等待策略（All/Majority/Any）
// 3. 复用现有的 ResponseStrategy
type AsyncGroup[T any] struct {
	lifecycle *transport.AsyncLifecycle
	targets   []model.PeerID
	
	// 子操作
	ops map[model.PeerID]AsyncOperation[T]
	
	// 结果收集
	results   map[model.PeerID]T
	errors    map[model.PeerID]error
	mu        sync.RWMutex
	
	// 信号
	anyDone       chan struct{}
	majorityDone  chan struct{}
	allDone       chan struct{}
	
	// 回调
	callback service.BroadcastListener
	
	// 时间统计
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
	
	// 保护性拷贝
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
	
	// 为每个 target 创建子操作
	for _, target := range targets {
		target := target // 捕获循环变量
		op := NewOp[T](lifecycle.Context(), func(ctx context.Context) (T, error) {
			return execFunc(ctx, target)
		})
		g.ops[target] = op
		
		// 注册完成回调
		op.OnComplete(func(value T, err error) {
			g.handleResult(target, value, err)
		})
	}
	
	return g
}

// handleResult 处理单个操作结果
func (g *AsyncGroup[T]) handleResult(peer model.PeerID, value T, err error) {
	var callback service.BroadcastListener
	var stats service.BroadcastStats
	var shouldTriggerMajority bool
	var shouldTriggerAllDone bool
	
	// === 锁内：状态更新 ===
	g.mu.Lock()
	
	// 记录首个响应时间
	if !g.firstResponseRecorded {
		g.firstResponseTime = time.Now()
		g.firstResponseRecorded = true
		close(g.anyDone)
	}
	
	// 记录结果
	if err != nil {
		g.errors[peer] = err
	} else {
		g.results[peer] = value
	}
	
	successCount := len(g.results)
	failedCount := len(g.errors)
	totalCount := len(g.targets)
	
	// 检查 Majority
	majority := totalCount/2 + 1
	if successCount >= majority {
		select {
		case <-g.majorityDone:
		default:
			close(g.majorityDone)
			g.majorityReachTime = time.Now()
			shouldTriggerMajority = true
		}
	}
	
	// 检查 AllDone
	if successCount+failedCount == totalCount {
		select {
		case <-g.allDone:
		default:
			close(g.allDone)
			shouldTriggerAllDone = true
		}
	}
	
	// 准备回调
	callback = g.callback
	stats = g.buildStatsLocked()
	g.mu.Unlock()
	// === 锁外 ===
	
	// 执行回调
	if callback == nil {
		return
	}

	// ✅ 回调执行顺序（v19.0 明确保证）：
	// 1. OnSuccess/OnFailure（每次响应）
	// 2. OnMajorityReached（仅一次，在达到 Majority 时）
	// 3. OnFullDone（仅一次，在全部完成时）

	// 1. 先执行成功/失败回调
	if err != nil {
		safeBroadcastListener(func() {
			callback.OnFailure(peer, err, stats)
		})
	} else {
		safeBroadcastListener(func() {
			callback.OnSuccess(peer, nil, stats)
		})
	}

	// 2. 再执行 Majority 回调（仅一次）
	if shouldTriggerMajority {
		safeBroadcastListener(func() {
			callback.OnMajorityReached(stats)
		})
	}

	// 3. 最后执行 FullDone 回调（仅一次）
	if shouldTriggerAllDone {
		safeBroadcastListener(func() {
			callback.OnFullDone(stats)
		})
	}
}

// WaitAll 等待所有完成
func (g *AsyncGroup[T]) WaitAll(ctx context.Context) (GroupResult[T], error) {
	select {
	case <-g.allDone:
		return g.GetResult(), nil
	case <-ctx.Done():
		return g.GetResult(), ctx.Err()
	}
}

// WaitMajority 等待多数派完成
func (g *AsyncGroup[T]) WaitMajority(ctx context.Context) (GroupResult[T], error) {
	select {
	case <-g.majorityDone:
		return g.GetResult(), nil
	case <-ctx.Done():
		return g.GetResult(), ctx.Err()
	}
}

// WaitAny 等待任意一个完成（v19.0 语义明确）
// 语义：
// - 返回第一个成功的响应
// - 如果全部失败，返回第一个失败
// - 如果部分失败且还有 pending，继续等待
func (g *AsyncGroup[T]) WaitAny(ctx context.Context) (model.PeerID, T, error) {
	for {
		select {
		case <-g.anyDone:
			g.mu.RLock()
			// 优先返回成功结果
			for peer, value := range g.results {
				g.mu.RUnlock()
				return peer, value, nil
			}
			// 如果全部失败，返回第一个失败
			if len(g.errors) == len(g.targets) {
				for peer, err := range g.errors {
					var zero T
					g.mu.RUnlock()
					return peer, zero, err
				}
			}
			g.mu.RUnlock()
			// 部分失败，但还有 pending，继续等待
			// 重新等待 anyDone 信号
		case <-ctx.Done():
			var zero T
			return "", zero, ctx.Err()
		}
	}
}

// SetCallback 设置回调（v19.0 新增）
// 与 BroadcastProgress 风格一致，允许在操作运行中设置回调
func (g *AsyncGroup[T]) SetCallback(callback service.BroadcastListener) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.callback = callback
}

// GetResult 获取当前结果（非阻塞）
func (g *AsyncGroup[T]) GetResult() GroupResult[T] {
	g.mu.RLock()
	defer g.mu.RUnlock()
	
	// 拷贝结果
	values := make(map[model.PeerID]T, len(g.results))
	for k, v := range g.results {
		values[k] = v
	}
	
	errs := make(map[model.PeerID]error, len(g.errors))
	for k, v := range g.errors {
		errs[k] = v
	}
	
	// 构建成功/失败列表
	successPeers := make([]model.PeerID, 0, len(g.results))
	for peer := range g.results {
		successPeers = append(successPeers, peer)
	}
	
	failedPeers := make([]model.PeerID, 0, len(g.errors))
	for peer := range g.errors {
		failedPeers = append(failedPeers, peer)
	}
	
	return GroupResult[T]{
		Values:       values,
		Errors:       errs,
		SuccessPeers: successPeers,
		FailedPeers:  failedPeers,
	}
}

// safeBroadcastListener 安全执行广播回调
func safeBroadcastListener(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("[AsyncGroup] callback panic recovered", "panic", r)
		}
	}()
	fn()
}
```

---

## 四、与现有组件的集成

### 4.1 包装现有 RPC 调用

```go
// internal/domain/service/rpc_async.go

package service

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/async"
)

// RPCAsync 异步 RPC 接口（基于 AsyncOperation 包装）
type RPCAsync interface {
	// CallAsync 单播异步调用
	CallAsync(ctx context.Context, to model.PeerID, req model.Message) async.AsyncOperation[model.Message]
	
	// BroadcastAsync 广播异步调用
	BroadcastAsync(ctx context.Context, to []model.PeerID, req model.Message, strategy ResponseStrategy) *async.AsyncGroup[model.Message]
}

// rpcAsyncImpl RPCAsync 实现
type rpcAsyncImpl struct {
	rpc RPC
	gp  async.GoroutineProvider
}

// NewRPCAsync 创建异步 RPC 包装器
func NewRPCAsync(rpc RPC, gp async.GoroutineProvider) RPCAsync {
	return &rpcAsyncImpl{rpc: rpc, gp: gp}
}

// CallAsync 单播异步调用
func (r *rpcAsyncImpl) CallAsync(ctx context.Context, to model.PeerID, req model.Message) async.AsyncOperation[model.Message] {
	return async.NewOp[model.Message](ctx, func(ctx context.Context) (model.Message, error) {
		return r.rpc.Call(ctx, to, req)
	})
}

// BroadcastAsync 广播异步调用
func (r *rpcAsyncImpl) BroadcastAsync(
	ctx context.Context,
	to []model.PeerID,
	req model.Message,
	strategy ResponseStrategy,
) *async.AsyncGroup[model.Message] {
	return async.NewGroup[model.Message](ctx, to, func(ctx context.Context, target model.PeerID) (model.Message, error) {
		return r.rpc.Call(ctx, target, req)
	})
}
```

### 4.2 与 BroadcastProgress 的桥接

```go
// pkg/async/bridge.go

package async

import (
	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ToBroadcastListener 将 AsyncOp 回调转换为 BroadcastListener
// 用于兼容现有使用 BroadcastProgress 的代码
func ToBroadcastListener(
	onSuccess func(peer model.PeerID, resp model.Message, stats service.BroadcastStats),
	onFailure func(peer model.PeerID, err error, stats service.BroadcastStats),
) service.BroadcastListener {
	return &callbackBridge{
		onSuccess: onSuccess,
		onFailure: onFailure,
	}
}

type callbackBridge struct {
	onSuccess func(peer model.PeerID, resp model.Message, stats service.BroadcastStats)
	onFailure func(peer model.PeerID, err error, stats service.BroadcastStats)
}

func (c *callbackBridge) OnSuccess(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
	if c.onSuccess != nil {
		c.onSuccess(peer, resp, stats)
	}
}

func (c *callbackBridge) OnFailure(peer model.PeerID, err error, stats service.BroadcastStats) {
	if c.onFailure != nil {
		c.onFailure(peer, err, stats)
	}
}

func (c *callbackBridge) OnMajorityReached(stats service.BroadcastStats) {}
func (c *callbackBridge) OnFullDone(stats service.BroadcastStats)       {}
```

---

## 五、使用示例

### 5.1 单播异步调用

```go
// 使用 AsyncOperation[T]
func exampleSingleAsync(rpc RPCAsync) {
	ctx := context.Background()
	peer := model.PeerID("node-1")
	req := model.NewMessage("req-1", model.MessageTypeRequest, "", peer, []byte("hello"))

	// 创建异步操作
	op := rpc.CallAsync(ctx, peer, req)

	// 方式1: 阻塞等待（带超时）
	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := op.Get(ctxTimeout)
	if err != nil {
		// 处理错误
		return
	}
	// 处理响应 resp

	// 方式2: 回调风格
	op.OnComplete(func(resp model.Message, err error) {
		if err != nil {
			// 处理错误
			return
		}
		// 处理响应
	})

	// 方式3: Channel 风格（扩展方法）
	// 注意：ResultChan() 是 AsyncOp 的扩展方法，需要类型断言
	if asyncOp, ok := op.(*async.AsyncOp[model.Message]); ok {
		select {
		case result := <-asyncOp.ResultChan():
			if result.Err != nil {
				// 处理错误
			} else {
				// 处理响应 result.Value
			}
		case <-ctx.Done():
			// 处理超时或取消
		}
	}
}
```

### 5.2 广播异步调用

```go
// 使用 AsyncGroup[T]
func exampleBroadcastAsync(rpc RPCAsync) {
	ctx := context.Background()
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	req := model.NewMessage("req-1", model.MessageTypeRequest, "", "", []byte("hello"))
	
	// 创建批量操作
	group := rpc.BroadcastAsync(ctx, peers, req, service.ResponseMajority)
	
	// 设置回调（与 BroadcastProgress 风格一致）
	group.SetCallback(&MyCallback{})
	
	// 等待多数派
	ctxTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := group.WaitMajority(ctxTimeout)
	if err != nil {
		// 处理错误
		return
	}
	
	// 处理结果
	for peer, resp := range result.Values {
		// 处理每个成功响应
		_ = peer
		_ = resp
	}
}
```

### 5.3 使用 GoroutineProvider

```go
// 使用 GoroutineProvider 提交任务
func exampleGoroutineProvider(gp async.GoroutineProvider) {
	ctx := context.Background()
	
	// 提交无返回值任务
	gp.Submit(func() {
		// 执行任务
	})
	
	// 提交带返回值任务（泛型）
	result := gp.SubmitWithResult(func() (string, error) {
		return "result", nil
	})
	if result.Err != nil {
		// 处理错误
	}
	value := result.Value
	
	// 按优先级提交任务
	gp.SubmitWithPriority(async.PriorityHigh, func() {
		// 高优先级任务
	})
	
	// 批量提交任务
	tasks := []func(){
		func() { /* 任务1 */ },
		func() { /* 任务2 */ },
		func() { /* 任务3 */ },
	}
	err := gp.SubmitBatch(tasks)
	if err != nil {
		// 处理错误
	}
}
```

### 5.4 错误处理最佳实践（v19.0 新增）

```go
// 完整的错误处理示例
func exampleWithErrorHandling(rpc RPCAsync) {
	// ✅ 1. 使用带超时的 context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	peer := model.PeerID("node-1")
	req := model.NewMessage("req-1", model.MessageTypeRequest, "", peer, []byte("hello"))

	// ✅ 2. 创建异步操作（带自定义超时）
	op := rpc.CallAsync(ctx, peer, req)

	// ✅ 3. 阻塞等待结果
	resp, err := op.Get(ctx)
	if err != nil {
		// ✅ 4. 根据错误类型进行不同处理
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			// ✅ 处理超时
			slog.Warn("operation timeout", "peer", peer, "error", err)
			// ✅ 释放资源
			if discardErr := op.Discard(); discardErr != nil {
				slog.Error("failed to discard operation", "error", discardErr)
			}

		case errors.Is(err, context.Canceled):
			// ✅ 处理取消
			slog.Info("operation canceled by user", "peer", peer)

		case errors.Is(err, errors.ErrTransportClosed):
			// ✅ 处理传输层错误
			slog.Error("transport error", "peer", peer, "error", err)

		default:
			// ✅ 处理其他错误
			slog.Error("operation failed", "peer", peer, "error", err)
		}
		return
	}

	// ✅ 5. 处理成功响应
	slog.Info("operation succeeded", "peer", peer, "response_size", len(resp.Payload))
	_ = resp
}

// 使用回调的错误处理示例
func exampleCallbackWithErrorHandling(rpc RPCAsync) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	peer := model.PeerID("node-1")
	req := model.NewMessage("req-1", model.MessageTypeRequest, "", peer, []byte("hello"))

	op := rpc.CallAsync(ctx, peer, req)

	// ✅ 使用回调处理结果（适合不需要立即阻塞的场景）
	cbID := op.OnComplete(func(resp model.Message, err error) {
		if err != nil {
			// ✅ 在回调中处理错误
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				slog.Warn("callback: operation timeout", "peer", peer)
			case errors.Is(err, context.Canceled):
				slog.Info("callback: operation canceled", "peer", peer)
			default:
				slog.Error("callback: operation failed", "peer", peer, "error", err)
			}
			return
		}

		// ✅ 处理成功响应
		slog.Info("callback: operation succeeded", "peer", peer)
	})

	// ✅ 如果不再需要回调，可以注销（避免回调泄漏）
	// 注意：如果操作已完成，注销会返回错误，这是正常的
	if err := op.OffComplete(cbID); err != nil {
		// 操作可能已完成，忽略错误
		slog.Debug("failed to unregister callback", "error", err)
	}

	// ✅ 检查操作状态
	switch op.Status() {
	case StatusPending:
		slog.Debug("operation is pending")
	case StatusRunning:
		slog.Debug("operation is running")
	case StatusCompleted:
		slog.Debug("operation completed")
	case StatusFailed:
		slog.Debug("operation failed")
	case StatusTimeout:
		slog.Debug("operation timeout")
	case StatusCanceled:
		slog.Debug("operation canceled")
	case StatusDiscarded:
		slog.Debug("operation discarded")
	}
}

// AsyncGroup 的错误处理示例
func exampleGroupWithErrorHandling(rpc RPCAsync) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	req := model.NewMessage("req-1", model.MessageTypeRequest, "", "", []byte("hello"))

	group := rpc.BroadcastAsync(ctx, peers, req, service.ResponseMajority)

	// ✅ 设置回调处理部分失败
	group.SetCallback(&errorHandlingCallback{})

	// ✅ 等待多数派，带超时
	result, err := group.WaitMajority(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("majority wait timeout",
				"success_count", len(result.SuccessPeers),
				"failed_count", len(result.FailedPeers))
		} else {
			slog.Error("majority wait failed", "error", err)
		}
		return
	}

	// ✅ 处理成功结果
	for peer, resp := range result.Values {
		slog.Info("peer responded successfully", "peer", peer, "size", len(resp.Payload))
	}

	// ✅ 处理失败节点
	for _, peer := range result.FailedPeers {
		if err, ok := result.Errors[peer]; ok {
			slog.Warn("peer failed", "peer", peer, "error", err)
		}
	}
}

// errorHandlingCallback 实现 BroadcastListener 接口
type errorHandlingCallback struct{}

func (c *errorHandlingCallback) OnSuccess(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
	slog.Info("callback: success", "peer", peer, "stats", stats)
}

func (c *errorHandlingCallback) OnFailure(peer model.PeerID, err error, stats service.BroadcastStats) {
	slog.Warn("callback: failure", "peer", peer, "error", err, "stats", stats)
}

func (c *errorHandlingCallback) OnMajorityReached(stats service.BroadcastStats) {
	slog.Info("callback: majority reached", "stats", stats)
}

func (c *errorHandlingCallback) OnFullDone(stats service.BroadcastStats) {
	slog.Info("callback: all done", "stats", stats)
}
```

**错误处理最佳实践总结**：

1. ✅ **始终使用带超时的 context** - 避免无限等待
2. ✅ **使用 `errors.Is()` 判断错误类型** - 支持错误链
3. ✅ **及时调用 `Discard()` 释放资源** - 避免资源泄漏
4. ✅ **记录详细的错误上下文** - 便于调试
5. ✅ **处理所有可能的错误场景** - 提高健壮性
6. ✅ **回调中也要处理错误** - 避免遗漏
7. ✅ **区分不同的错误类型** - 针对性处理
8. ✅ **使用结构化日志** - 便于问题排查

---

## 六、实施路线图

### Phase 1: 基础实现（Week 1）

- [ ] 实现 `AsyncOp[T]` 结构体
- [ ] 实现 `AsyncGroup[T]` 结构体
- [ ] 实现 `GoroutineProvider` 接口（基于 ants 或 conc）
- [ ] 单元测试（覆盖所有方法）

### Phase 2: 集成测试（Week 2）

- [ ] 与现有 RPC 层集成测试
- [ ] 与 BroadcastProgress 对比测试
- [ ] 性能基准测试（vs 现有实现）

**性能基准测试计划（v19.0）**：

| 测试场景 | 性能目标 | 基准对比 | 说明 |
|---------|---------|---------|------|
| **AsyncOp.Get() 延迟** | < 10ns | vs channel 读取 | 零开销抽象 |
| **AsyncOp.OnComplete() 注册** | < 100ns | vs map 插入 | 回调注册延迟 |
| **AsyncOp 状态查询** | < 5ns | vs atomic.Load | Status()/IsStarted() |
| **AsyncGroup.WaitMajority()** | 零 CPU 开销 | vs channel 等待 | 使用 channel 原生等待 |
| **1000 并发 AsyncOp** | < 10MB 内存 | vs 独立 goroutine | 内存占用测试 |
| **10000 并发 AsyncOp** | < 100MB 内存 | vs ants 池 | 大规模并发测试 |

**性能测试代码示例**：

```go
// BenchmarkAsyncOpGet AsyncOp.Get() 性能测试
func BenchmarkAsyncOpGet(b *testing.B) {
	execFunc := func(ctx context.Context) (string, error) {
		return "result", nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op := NewOp(context.Background(), execFunc)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _ = op.Get(ctx)
		cancel()
	}
}

// BenchmarkAsyncOpOnComplete AsyncOp.OnComplete() 性能测试
func BenchmarkAsyncOpOnComplete(b *testing.B) {
	op := NewOp(context.Background(), func(ctx context.Context) (string, error) {
		return "result", nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cbID := op.OnComplete(func(s string, err error) {})
		_ = op.OffComplete(cbID)
	}
}

// BenchmarkAsyncGroupWaitMajority AsyncGroup.WaitMajority() 性能测试
func BenchmarkAsyncGroupWaitMajority(b *testing.B) {
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		group := NewGroup[string](context.Background(), targets,
			func(ctx context.Context, target model.PeerID) (string, error) {
				return "result", nil
			})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _ = group.WaitMajority(ctx)
		cancel()
	}
}

// BenchmarkConcurrentAsyncOps 并发性能测试
func BenchmarkConcurrentAsyncOps(b *testing.B) {
	for n := 1; n <= 10000; n *= 10 {
		b.Run(fmt.Sprintf("%d-ops", n), func(b *testing.B) {
			var wg sync.WaitGroup
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				wg.Add(n)
				for j := 0; j < n; j++ {
					go func() {
						defer wg.Done()
						op := NewOp(context.Background(), func(ctx context.Context) (string, error) {
							return "result", nil
						})
						ctx, cancel := context.WithTimeout(context.Background(), time.Second)
						_, _ = op.Get(ctx)
						cancel()
					}()
				}
				wg.Wait()
			}
		})
	}
}
```

**性能对比基准（vs 现有实现）**：

```go
// BenchmarkVsBroadcastProgress 与 BroadcastProgress 性能对比
func BenchmarkVsBroadcastProgress(b *testing.B) {
	// 现有 BroadcastProgress 实现
	b.Run("BroadcastProgress", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			tracker := service.NewBroadcastProgress([]model.PeerID{"node-1", "node-2", "node-3"})
			// ... 执行广播操作
			_ = tracker
		}
	})

	// 新 AsyncGroup 实现
	b.Run("AsyncGroup", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			group := NewGroup[string](context.Background(),
				[]model.PeerID{"node-1", "node-2", "node-3"},
				func(ctx context.Context, target model.PeerID) (string, error) {
					return "result", nil
				})
			_ = group
		}
	})
}

// BenchmarkVsLibp2pRPCAsync 与 Libp2pRPC.CallAsync 性能对比
func BenchmarkVsLibp2pRPCAsync(b *testing.B) {
	// 现有 Libp2pRPC.CallAsync 实现
	b.Run("Libp2pRPC.CallAsync", func(b *testing.B) {
		// ... 现有实现基准测试
	})

	// 新 AsyncOperation 实现
	b.Run("AsyncOperation", func(b *testing.B) {
		// ... 新实现基准测试
	})
}
```

**性能验证清单**：

- [ ] **AsyncOp.Get() 延迟 < 10ns**
  - [ ] 单次 Get() 操作延迟测试
  - [ ] 1000 次 Get() 操作平均延迟测试
  - [ ] 与 channel 读取对比测试

- [ ] **AsyncOp.OnComplete() 注册延迟 < 100ns**
  - [ ] 单次 OnComplete() 注册延迟测试
  - [ ] 1000 次注册/注销循环测试
  - [ ] 与 map 插入对比测试

- [ ] **AsyncGroup.WaitMajority() 零 CPU 开销**
  - [ ] channel 等待 CPU 占用测试
  - [ ] 多数派达成时间测试
  - [ ] 与 select-case 对比测试

- [ ] **1000 并发 AsyncOp 内存占用 < 10MB**
  - [ ] 1000 个 AsyncOp 内存占用测试
  - [ ] 10000 个 AsyncOp 内存占用测试
  - [ ] 与独立 goroutine 对比测试

- [ ] **与现有实现性能对比**
  - [ ] vs BroadcastProgress 性能对比
  - [ ] vs Libp2pRPC.CallAsync 性能对比
  - [ ] vs pendingCall 性能对比

**性能分析工具**：

```bash
# 运行性能测试
go test -bench=. -benchmem

# 生成 CPU profile
go test -bench=. -cpuprofile=cpu.prof
go tool pprof cpu.prof

# 生成内存 profile
go test -bench=. -memprofile=mem.prof
go tool pprof mem.prof

# 生成 trace
go test -bench=. -trace=trace.out
go tool trace trace.out
```

### Phase 3: 渐进式迁移（Week 3）

- [ ] 元数据同步：使用 AsyncGroup 替代现有 BroadcastProgress
- [ ] KV 读写：使用 AsyncOperation 实现异步读写
- [ ] 监控指标：验证回调机制和统计信息

### Phase 4: 文档和优化（Week 4）

- [ ] 完善使用文档
- [ ] 性能基准测试
- [ ] 内存泄漏检查
- [ ] 错误处理完善

**文档和代码注释规范（v19.0）**：

#### 1. Go Doc 注释规范

所有公开接口必须有 Go Doc 注释，遵循以下格式：

```go
// AsyncOperation 统一的异步操作接口（泛型设计）
//
// 设计原则：
// 1. 语义精确：每个方法的行为明确无歧义
// 2. 非阻塞查询：Status()/IsStarted() 都是非阻塞的
// 3. 资源管理：Discard() 用于提前释放资源
// 4. 回调安全：OnComplete 回调带 panic recover
//
// 使用示例：
//   op := NewOp(ctx, execFunc)
//   defer op.Discard()  // 确保资源释放
//
//   if canceled, _ := op.Cancel(); canceled {
//       log.Info("operation canceled")
//   }
//
// 📖 参考: docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md
type AsyncOperation[T any] interface {
    // Get 等待异步操作完成并返回结果
    //
    // 参数：
    //   - ctx: 用于超时控制和取消
    //
    // 返回：
    //   - T: 泛型结果
    //   - error: 可能的错误（context.DeadlineExceeded, context.Canceled 等）
    //
    // 示例：
    //   ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    //   defer cancel()
    //   result, err := op.Get(ctx)
    Get(ctx context.Context) (T, error)

    // ... 其他方法注释
}
```

#### 2. 代码注释规范

##### 2.1 包级注释

```go
// Package async 提供统一的异步编程抽象
//
// 本包实现 AsyncOperation[T] 和 AsyncGroup[T] 泛型接口，
// 提供类型安全的异步操作能力。
//
// 主要类型：
//   - AsyncOperation[T]: 单个异步操作接口
//   - AsyncOp[T]: AsyncOperation 的默认实现
//   - AsyncGroup[T]: 批量异步操作组
//
// 设计文档：
//   - docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md
//   - thoughts/2026-02-22-doubao-异步编程模型重构方案-v2.md
//
// 使用示例：
//   op := async.NewOp(ctx, execFunc)
//   defer op.Discard()
//   result, err := op.Get(ctx)
package async
```

##### 2.2 函数/方法注释

```go
// NewOp 创建新的异步操作
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - execFunc: 执行函数，返回泛型结果和错误
//   - opts: 可选配置（如 WithTimeout）
//
// 返回：
//   - AsyncOperation[T]: 异步操作接口
//
// 示例：
//   // 使用默认配置（30秒超时）
//   op := NewOp(ctx, execFunc)
//
//   // 使用自定义超时
//   op := NewOp(ctx, execFunc, WithTimeout(10*time.Second))
//
// 注意：
//   - 默认超时 30 秒，可通过 WithTimeout 自定义
//   - 执行函数应尊重 context 取消信号
//   - 建议使用 defer op.Discard() 确保资源释放
func NewOp[T any](
    ctx context.Context,
    execFunc func(ctx context.Context) (T, error),
    opts ...OpOption,
) AsyncOperation[T] {
    // ... 实现
}
```

##### 2.3 复杂逻辑注释

```go
func (g *AsyncGroup[T]) handleResult(peer model.PeerID, value T, err error) {
    var callback service.BroadcastListener
    var stats service.BroadcastStats
    var shouldTriggerMajority bool
    var shouldTriggerAllDone bool

    // === 锁内：状态更新 ===
    // 关键：所有状态更新必须在锁内完成，避免竞态条件
    g.mu.Lock()

    // 记录首个响应时间（仅一次）
    // 使用 firstResponseRecorded 标志确保只记录一次
    if !g.firstResponseRecorded {
        g.firstResponseTime = time.Now()
        g.firstResponseRecorded = true
        close(g.anyDone)  // 触发 WaitAny() 返回
    }

    // 记录结果
    // 注意：成功和失败分开存储，便于后续统计分析
    if err != nil {
        g.errors[peer] = err
    } else {
        g.results[peer] = value
    }

    // 检查 Majority（> N/2）
    // 使用 select-case 确保 channel 只关闭一次
    majority := totalCount/2 + 1
    if successCount >= majority {
        select {
        case <-g.majorityDone:
            // 已触发，跳过
        default:
            close(g.majorityDone)
            g.majorityReachTime = time.Now()
            shouldTriggerMajority = true
        }
    }

    // ... 其他逻辑
}
```

##### 2.4 设计理由注释

```go
// ✅ 为什么使用选项模式？
// 1. 向后兼容：添加新选项不影响现有代码
// 2. 可读性：WithTimeout(10*time.Second) 比 NewOp(ctx, fn, 10*time.Second) 更清晰
// 3. 灵活性：可以轻松添加更多选项（如 WithPriority, WithRetry）
type OpOption func(*opConfig)

// ✅ 为什么使用 IsTerminal() 而不是列举所有终态？
// 1. 可扩展：添加新终态时不需要修改所有检查代码
// 2. 一致性：所有终态判断使用同一方法，减少错误
// 3. 可维护性：终态定义集中在一处，易于修改
func (s OperationStatus) IsTerminal() bool {
    switch s {
    case StatusCompleted, StatusFailed, StatusCanceled, StatusDiscarded, StatusTimeout:
        return true
    default:
        return false
    }
}
```

#### 3. 文档结构规范

##### 3.1 README 文档

每个包都应该有 README.md，包含：

```markdown
# Package async

## 概述
统一的异步编程抽象，提供 AsyncOperation[T] 和 AsyncGroup[T] 接口。

## 快速开始

### 单个异步操作
\`\`\`go
op := async.NewOp(ctx, execFunc)
defer op.Discard()
result, err := op.Get(ctx)
\`\`\`

### 批量异步操作
\`\`\`go
group := async.NewGroup(ctx, targets, execFunc)
result, err := group.WaitMajority(ctx)
\`\`\`

## 核心概念

### AsyncOperation[T]
- Get(ctx): 阻塞等待结果
- Status(): 查询操作状态
- Cancel(): 取消操作
- Discard(): 丢弃结果
- OnComplete(): 注册回调

### AsyncGroup[T]
- WaitAll(): 等待所有完成
- WaitMajority(): 等待多数派
- WaitAny(): 等待任意一个
- SetCallback(): 设置回调

## 设计文档
- [DDD 架构](../../docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md)
- [重构方案](../../thoughts/2026-02-22-doubao-异步编程模型重构方案-v2.md)

## 性能基准
- AsyncOp.Get(): < 10ns
- AsyncOp.OnComplete(): < 100ns
- AsyncGroup.WaitMajority(): 零 CPU 开销

## 许可证
AGPL-3.0
```

##### 3.2 API 文档

使用 godoc 生成 API 文档：

```bash
# 生成本地文档
godoc -http=:6060

# 访问文档
# http://localhost:6060/pkg/github.com/jzhang405/NexKV/pkg/async/
```

#### 4. 注释质量检查清单

- [ ] **包级注释**
  - [ ] 描述包的用途
  - [ ] 列出主要类型
  - [ ] 提供使用示例
  - [ ] 引用设计文档

- [ ] **接口注释**
  - [ ] 描述接口职责
  - [ ] 列出设计原则
  - [ ] 提供使用示例
  - [ ] 引用参考文档

- [ ] **方法注释**
  - [ ] 描述方法功能
  - [ ] 列出参数说明
  - [ ] 说明返回值
  - [ ] 提供使用示例
  - [ ] 说明注意事项

- [ ] **复杂逻辑注释**
  - [ ] 说明逻辑流程
  - [ ] 解释设计理由
  - [ ] 标注关键点
  - [ ] 说明边界情况

- [ ] **设计理由注释**
  - [ ] 解释为什么这样设计
  - [ ] 说明权衡考虑
  - [ ] 列出替代方案
  - [ ] 说明未来扩展方向

#### 5. 文档生成工具

```bash
# 使用 golint 检查注释质量
golint ./...

# 使用 go doc 生成文档
go doc -all ./pkg/async

# 使用 godoc 启动本地文档服务器
godoc -http=:6060
```

#### 6. 示例代码规范

所有示例代码必须：
1. ✅ 可编译运行
2. ✅ 包含错误处理
3. ✅ 有清晰的注释
4. ✅ 遵循 Go 惯例

```go
// Example_newOp 演示如何创建异步操作
func Example_newOp() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // 创建异步操作
    op := NewOp(ctx, func(ctx context.Context) (string, error) {
        // 模拟耗时操作
        time.Sleep(100 * time.Millisecond)
        return "result", nil
    })
    defer op.Discard() // ✅ 确保资源释放

    // 等待结果
    result, err := op.Get(ctx)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(result)
    // Output: result
}
```

---

## 七、与旧方案对比

| 维度 | 旧方案 | 新方案 v2.2 |
|------|--------|-------------|
| **接口标准** | 自定义 AsyncOp | 复用 `AsyncOperation[T]`（DDD标准） |
| **协程管理** | 自建 goroutine | 复用 `GoroutineProvider`（DDD标准） |
| **泛型复杂度** | `Async[T, S Status]` 双泛型参数 | `AsyncOp[T]` 单泛型参数 |
| **Channel 风格** | 不支持 | 原生支持（通过 ResultChan） |
| **回调风格** | 函数式 `Callback[T]` | 函数式 + 接口式 `BroadcastListener` |
| **生命周期** | 自建 `done chan struct{}` | 复用 `AsyncLifecycle` |
| **批量操作** | `MultiAsync[T, P any]` 复杂 | `AsyncGroup[T]` 简化 |
| **状态枚举** | `SingleStatus`/`MultiStatus` 两套 | 复用 `OperationStatus`（v19.0） |
| **错误处理** | 原生 `error` | 复用 `pkg/errors` |
| **迁移策略** | 渐进式演进（包装层） | **方案B并行实现**（适配层） |
| **迁移成本** | 高（接口不兼容） | 低（与DDD文档对齐） |

---

## 八、参考文档

1. **DDD架构 - AsyncOperation**: [2026-02-18_spike_nexkv-ddd-interface.md](../docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md#13-b3-asyncoperation)
2. **DDD架构 - GoroutineProvider**: [2026-02-18_spike_nexkv-ddd-interface.md](../docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md#13-b4-goroutineprovider)
3. **DDD架构 - 实现方案**: [2026-02-18_spike_nexkv-ddd-implement.md](../docs/07_spike/2026-02-18_spike_nexkv-ddd-implement.md)
4. **M2存储引擎 - 异步接口**: [2026-02-21_spike_m2-storage-engine-interface.md](../docs/07_spike/2026-02-21_spike_m2-storage-engine-interface.md#11-asyncoperation)
5. **M2存储引擎 - 实现方案**: [2026-02-21_spike_m2-storage-engine-implement.md](../docs/07_spike/2026-02-21_spike_m2-storage-engine-implement.md)

---

## 九、实施策略：方案B（并行实现）

### 9.1 核心思想

**新旧实现并存，内部统一，逐步替换**

```
┌─────────────────────────────────────────┐
│           RPC Interface                 │
│  CallAsync() -> 返回 AsyncOperation     │
├─────────────────────────────────────────┤
│  适配层 (Adapter)                       │
│  旧回调风格 -> 新 AsyncOperation        │
├─────────────────────────────────────────┤
│  ┌─────────────┐    ┌─────────────┐   │
│  │  旧实现      │    │  新实现      │   │
│  │  pendingCall│    │  AsyncOp[T] │   │
│  │  Channel    │    │  泛型接口    │   │
│  │  (冻结维护)  │    │  (活跃开发)  │   │
│  └─────────────┘    └─────────────┘   │
├─────────────────────────────────────────┤
│  共享基础设施                            │
│  AsyncLifecycle / GoroutineProvider     │
└─────────────────────────────────────────┘
```

### 9.2 目录结构

```
internal/infrastructure/transport/
├── async_common.go              # 共享: AsyncLifecycle (已有)
├── async_operation.go           # 新增: AsyncOp[T] 实现
├── async_group.go               # 新增: AsyncGroup[T] 实现
├── goroutine_provider.go        # 新增: GoroutineProvider 接口
├── ants_provider.go             # 新增: AntsGoroutineProvider 实现
│
├── libp2p_rpc.go                # 修改: 使用新 AsyncOperation
├── libp2p_rpc_adapter.go        # 新增: 旧接口适配层
│
├── libp2p_async_channel.go      # 冻结: 旧实现，仅维护
├── libp2p_async_stream.go       # 冻结: 旧实现，仅维护
│
└── legacy/                      # 可选: 旧实现归档
    ├── libp2p_async_channel.go
    └── libp2p_async_stream.go
```

### 9.3 适配层实现

```go
// internal/infrastructure/transport/libp2p_rpc_adapter.go

package transport

import (
    "context"
    "time"

    "github.com/jzhang405/NexKV/internal/domain/model"
    "github.com/jzhang405/NexKV/internal/domain/service"
)

// RPCAdapter 为旧代码提供兼容层
type RPCAdapter struct {
    rpc *Libp2pRPC
}

// NewRPCAdapter 创建适配器
func NewRPCAdapter(rpc *Libp2pRPC) *RPCAdapter {
    return &RPCAdapter{rpc: rpc}
}

// CallAsyncOld 旧风格调用（回调式）
// 内部使用新的 AsyncOperation 实现
func (a *RPCAdapter) CallAsyncOld(
    ctx context.Context,
    to model.PeerID,
    req model.Message,
    cb func(model.Message, error),
) {
    // 使用新实现
    op := a.rpc.CallAsync(ctx, to, req)
    
    // 注册回调
    op.OnComplete(cb)
}

// CallAsyncWithTimeoutOld 旧风格调用（带超时）
func (a *RPCAdapter) CallAsyncWithTimeoutOld(
    ctx context.Context,
    to model.PeerID,
    req model.Message,
    timeout time.Duration,
    cb func(model.Message, error),
) {
    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    
    op := a.rpc.CallAsync(ctx, to, req)
    op.OnComplete(cb)
}

// BroadcastAsyncOld 旧风格广播
func (a *RPCAdapter) BroadcastAsyncOld(
    ctx context.Context,
    to []model.PeerID,
    req model.Message,
    strategy service.ResponseStrategy,
    onSuccess func(peer model.PeerID, resp model.Message),
    onFailure func(peer model.PeerID, err error),
) {
    group := NewAsyncGroup(ctx, to, func(ctx context.Context, target model.PeerID) (model.Message, error) {
        return a.rpc.Call(ctx, target, req)
    })
    
    // 设置回调适配
    group.SetCallback(&legacyBroadcastListener{
        onSuccess: onSuccess,
        onFailure: onFailure,
    })
}

// legacyBroadcastListener 旧回调适配
type legacyBroadcastListener struct {
    onSuccess func(peer model.PeerID, resp model.Message)
    onFailure func(peer model.PeerID, err error)
}

func (c *legacyBroadcastListener) OnSuccess(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
    if c.onSuccess != nil {
        c.onSuccess(peer, resp)
    }
}

func (c *legacyBroadcastListener) OnFailure(peer model.PeerID, err error, stats service.BroadcastStats) {
    if c.onFailure != nil {
        c.onFailure(peer, err)
    }
}

func (c *legacyBroadcastListener) OnMajorityReached(stats service.BroadcastStats) {}
func (c *legacyBroadcastListener) OnFullDone(stats service.BroadcastStats)       {}
```

### 9.4 迁移策略

#### 阶段1：并行开发（Week 1）

```go
// 新代码使用新接口
op := rpc.CallAsync(ctx, peer, req)
resp, err := op.Get(ctx)

// 旧代码继续使用（通过适配层）
adapter := NewRPCAdapter(rpc)
adapter.CallAsyncOld(ctx, peer, req, func(resp Message, err error) {
    // 旧回调风格
})
```

#### 阶段2：逐步替换（Week 2-3）

```bash
# 查找所有使用旧接口的地方
grep -r "CallAsyncOld" --include="*.go" .

# 逐个文件替换
# 1. 修改调用代码
# 2. 运行测试
# 3. 提交 PR
```

#### 阶段3：清理旧代码（Week 4）

```go
// 删除适配层
// 删除旧实现
// 删除 libp2p_rpc_adapter.go
```

### 9.5 优缺点分析

#### 优点

| 优点 | 说明 |
|------|------|
| **零停机迁移** | 旧代码继续工作，新代码使用新接口 |
| **风险可控** | 可以随时回滚到旧实现 |
| **并行开发** | 团队成员可以同时修改不同模块 |
| **测试友好** | 可以对比新旧实现的行为 |
| **学习曲线平缓** | 开发者可以逐步学习新接口 |

#### 缺点与缓解措施

| 缺点 | 说明 | 缓解措施 |
|------|------|---------|
| **代码重复** | 两套实现并存 | 明确标记旧实现为废弃，设定删除时间 |
| **包体积增加** | 约增加 30% 代码 | 最终清理后会恢复 |
| **概念混淆** | 开发者不知道用哪个 | 文档明确推荐新接口，IDE 标记废弃 |
| **维护成本** | 需要维护两套测试 | 旧测试冻结，仅维护新测试 |
| **性能开销** | 适配层有微小开销 | 适配层仅在旧代码使用，新代码无开销 |

### 9.6 实施检查清单

- [ ] **Week 1**: 实现 AsyncOp[T] 和 AsyncGroup[T]
- [ ] **Week 1**: 实现 GoroutineProvider
- [ ] **Week 1**: 创建适配层
- [ ] **Week 2**: 标记旧接口为 `@Deprecated`
- [ ] **Week 2**: 编写迁移指南文档
- [ ] **Week 3**: 逐个模块迁移（优先级：元数据 > KV > 其他）
- [ ] **Week 4**: 删除适配层和旧实现
- [ ] **Week 4**: 全量回归测试

### 9.7 关键决策点

1. **是否保留旧实现文件？**
   - 建议：保留但移动到 `legacy/` 目录，4周后删除

2. **适配层放在哪里？**
   - 建议：`libp2p_rpc_adapter.go`，与 RPC 实现同目录

3. **如何处理测试？**
   - 建议：新实现需要完整测试，旧测试冻结不修改

4. **迁移优先级？**
   - 建议：核心路径优先（元数据同步 > KV读写 > 监控）

---

## 十、总结

新方案 v2.2 的核心改进：

1. **与DDD文档深度对齐**：完全复用 `AsyncOperation[T]` 和 `GoroutineProvider` 标准接口
2. **v19.0特性支持**：完整支持 `Discard()`、`IsStarted()`、7个OperationStatus状态
3. **Channel + 回调双风格**：既支持 Channel 风格，也支持回调风格
4. **类型安全**：利用 Go 泛型，在使用处提供编译时类型检查
5. **方案B（并行实现）**：内部统一使用 AsyncOperation，外部通过适配层保持兼容
6. **生产级可用**：内置 panic 保护、生命周期管理、统计信息

**核心思想**：**"内部统一，外部兼容"**
- 内部：统一使用 AsyncOperation 实现
- 外部：通过适配层保持旧接口兼容

这个方案可以在 4 周内完成落地，既实现了技术目标，又降低了迁移风险。

---

**文档更新记录**：
- v2.0: 初始版本
- v2.1: 添加 ResultChan() 方法，完善使用示例
- **v2.2: 新增第九章"方案B：并行实现"，详细阐述实施策略**
