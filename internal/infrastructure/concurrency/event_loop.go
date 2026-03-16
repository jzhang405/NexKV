package concurrency

//
// 本包实现了 Event Loop 模式的任务调度和执行器，支持：
//   - 基础任务提交（TrySubmit, SubmitAndWait）
//   - 批量任务提交（SubmitBatch）
//   - 增强任务提交，支持异步结果（SubmitWithResult, SubmitWithContext）
//   - 对象池优化，减少内存分配
//
// EventLoop 特性：
//   - 单 goroutine Event Loop 模式
//   - CPU 亲和性绑定（可选）
//   - 优雅关闭和资源清理
//
// 适用场景：
//   - 高频短任务执行
//   - 需要低延迟的任务调度
//   - 单独核绑定的计算密集型任务

//
// 本包实现了 Event Loop 模式的任务调度和执行器

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/sirupsen/logrus"
)

// ==========================================
// Request 接口和实现
// ==========================================

// taskRequest 任务请求接口
type taskRequest interface {
	execute(w *EventLoop)
}

// ==========================================
// Request 结构（基础版）
// ==========================================

// Request 基础任务请求
type Request struct {
	Fn     func()
	Result chan struct{}
}

// requestPool Request 对象池

// drainRequestResult 清空 Request 的 Result channel 中可能的残留数据
// 在从对象池获取 Request 后调用，确保 channel 处于干净状态
//
//nolint:unused
func drainRequestResult(req *Request) {
	select {
	case <-req.Result:
	default:
	}
}

var requestPool = sync.Pool{
	New: func() any {
		return &Request{
			Result: make(chan struct{}, 1),
		}
	},
}

// execute 执行基础请求
func (r *Request) execute(w *EventLoop) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logrus.Errorf("[EventLoop] Worker %d panic recovered: %v", w.coreID, recovered)
		}
		// 通知完成
		select {
		case r.Result <- struct{}{}:
		default:
		}
		// 回收 Request
		requestPool.Put(r)
	}()

	// 执行任务
	r.Fn()
}

// ==========================================
// EnhancedRequest 结构（增强版）
// ==========================================

// EnhancedRequest 增强的任务请求（支持异步结果）
type EnhancedRequest struct {
	Fn       func() any
	ResultCh chan any
	ErrorCh  chan error
	Context  context.Context
}

// enhancedRequestPool EnhancedRequest 对象池
var enhancedRequestPool = sync.Pool{
	New: func() any {
		return &EnhancedRequest{
			ResultCh: make(chan any, 1),
			ErrorCh:  make(chan error, 1),
		}
	},
}

// execute 执行增强请求
func (r *EnhancedRequest) execute(w *EventLoop) {
	var result any
	var err error

	// 执行任务并捕获 panic
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				logrus.Errorf("[EventLoopEnh] Worker %d panic recovered: %v", w.coreID, recovered)
				err = errors.ErrTaskPanic
			}
		}()

		// 检查是否已取消
		if r.Context != nil {
			select {
			case <-r.Context.Done():
				err = r.Context.Err()
				return
			default:
			}
		}

		// 执行任务
		result = r.Fn()
	}()

	// 发送结果或错误
	if err != nil {
		if r.ErrorCh != nil {
			select {
			case r.ErrorCh <- err:
			case <-w.ctx.Done():
			}
		}
	} else if r.ResultCh != nil {
		select {
		case r.ResultCh <- result:
		case <-w.ctx.Done():
		}
	}

	// 回收 EnhancedRequest
	enhancedRequestPool.Put(r)
}

// Wait 等待 EnhancedRequest 完成
func (r *EnhancedRequest) Wait() (any, error) {
	ctx := r.Context
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case result := <-r.ResultCh:
		return result, nil
	case err := <-r.ErrorCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// WaitWithTimeout 带超时的等待
func (r *EnhancedRequest) WaitWithTimeout(timeout time.Duration) (any, error) {
	ctx := r.Context
	if ctx == nil {
		ctx = context.Background()
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case result := <-r.ResultCh:
		return result, nil
	case err := <-r.ErrorCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ==========================================
// EventLoop 实现
// ==========================================

// EventLoop 事件循环
type EventLoop struct {
	coreID int

	// 任务 Channel
	taskCh chan taskRequest

	// 上下文
	ctx    context.Context
	cancel context.CancelFunc

	// 状态
	running atomic.Bool
	wg      sync.WaitGroup
}

// NewEventLoop 创建事件循环
func NewEventLoop(coreID int, queueSize int) *EventLoop {
	ctx, cancel := context.WithCancel(context.Background())

	return &EventLoop{
		coreID: coreID,
		taskCh: make(chan taskRequest, queueSize),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start 启动 EventLoop
func (w *EventLoop) Start() {
	if !w.running.CompareAndSwap(false, true) {
		logrus.Warnf("[EventLoop] Worker %d already started", w.coreID)
		return
	}

	w.wg.Add(1)
	go w.runLoop()

	logrus.Infof("[EventLoop] Worker %d started", w.coreID)
}

// runLoop 统一事件循环
func (w *EventLoop) runLoop() {
	defer w.wg.Done()
	defer w.running.Store(false)

	// 绑核
	runtime.LockOSThread()
	if err := pinToCore(w.coreID); err != nil {
		logrus.WithField("coreID", w.coreID).
			WithField("error", err).
			Warn("Failed to pin EventLoop worker to core")
	}

	for {
		select {
		case req := <-w.taskCh:
			if req != nil {
				req.execute(w)
			}
		case <-w.ctx.Done():
			return
		}
	}
}

// ==========================================
// 基础 API 方法
// ==========================================

// TrySubmit 提交任务但不等待完成（fire-and-forget）
func (w *EventLoop) TrySubmit(task func()) error {
	if !w.running.Load() {
		return errors.ErrExecutorClosed
	}

	req := requestPool.Get().(*Request)
	req.Fn = task

	select {
	case w.taskCh <- req:
		return nil
	default:
		requestPool.Put(req)
		return errors.ErrQueueFull
	}
}

// SubmitAndWait 提交任务并等待完成
func (w *EventLoop) SubmitAndWait(task func()) error {
	if !w.running.Load() {
		return errors.ErrExecutorClosed
	}

	req := requestPool.Get().(*Request)
	req.Fn = task

	select {
	case w.taskCh <- req:
		<-req.Result
		return nil
	default:
		requestPool.Put(req)
		return errors.ErrQueueFull
	}
}

// SubmitBatch 批量提交任务并等待全部完成
func (w *EventLoop) SubmitBatch(tasks []func()) error {
	if !w.running.Load() {
		return errors.ErrExecutorClosed
	}

	if len(tasks) == 0 {
		return nil
	}

	// 批量发送
	requests := make([]*Request, 0, len(tasks))
	for _, task := range tasks {
		req := requestPool.Get().(*Request)
		req.Fn = task
		// 清空 Result channel 中可能的残留数据
		select {
		case <-req.Result:
		default:
		}

		requests = append(requests, req)

		select {
		case w.taskCh <- req:
		default:
			// 队列满，回收已提交的请求
			for _, r := range requests {
				requestPool.Put(r)
			}
			return errors.ErrQueueFull
		}
	}

	// 批量等待
	for _, req := range requests {
		<-req.Result
	}

	return nil
}

// Submit 提交任务（向后兼容）
func (w *EventLoop) Submit(task func()) error {
	return w.SubmitAndWait(task)
}

// ==========================================
// 增强版 API 方法
// ==========================================

// SubmitWithResult 提交任务并异步接收结果
func (w *EventLoop) SubmitWithResult(task func() any) (*EnhancedRequest, error) {
	if !w.running.Load() {
		return nil, errors.ErrExecutorClosed
	}

	req := enhancedRequestPool.Get().(*EnhancedRequest)
	req.Fn = task
	req.Context = context.Background() // 直接设置，避免池中复用残留

	select {
	case w.taskCh <- req:
		return req, nil
	default:
		enhancedRequestPool.Put(req)
		return nil, errors.ErrQueueFull
	}
}

// SubmitWithContext 提交任务并异步接收结果（带上下文）
func (w *EventLoop) SubmitWithContext(ctx context.Context, task func() any) (*EnhancedRequest, error) {
	if !w.running.Load() {
		return nil, errors.ErrExecutorClosed
	}

	req := enhancedRequestPool.Get().(*EnhancedRequest)
	req.Fn = task
	req.Context = ctx

	select {
	case w.taskCh <- req:
		return req, nil
	default:
		enhancedRequestPool.Put(req)
		return nil, errors.ErrQueueFull
	}
}

// ==========================================
// 通用方法
// ==========================================

// Close 关闭 EventLoop
func (w *EventLoop) Close() error {
	if !w.running.CompareAndSwap(true, false) {
		return nil
	}

	w.cancel()
	w.wg.Wait()

	logrus.Infof("[EventLoop] Worker %d closed", w.coreID)
	return nil
}

// IsRunning 检查是否运行中
func (w *EventLoop) IsRunning() bool {
	return w.running.Load()
}

// QueueLength 返回当前队列长度
func (w *EventLoop) QueueLength() int {
	return len(w.taskCh)
}
