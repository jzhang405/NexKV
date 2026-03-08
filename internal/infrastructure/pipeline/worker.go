package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// itemWrapper[T] 数据包装器
type itemWrapper[T any] struct {
	item      T
	resultCh  chan result[T]
	startTime time.Time
	ctx       context.Context
}

// result[T] 处理结果
type result[T any] struct {
	item T
	err  error
}

// stageWorker[T] Stage 执行器
type stageWorker[T any] struct {
	stage   Stage[T]
	index   int             // 在 Pipeline 中的位置
	next    *stageWorker[T] // 下一个 Worker
	taskCh  chan *itemWrapper[T]
	config  *Config
	metrics *stageMetrics

	running atomic.Bool
	wg      sync.WaitGroup
	ctx     context.Context
}

// newStageWorker 创建 Stage Worker
func newStageWorker[T any](index int, stage Stage[T], config *Config, metrics *Metrics) *stageWorker[T] {
	return &stageWorker[T]{
		stage:   stage,
		index:   index,
		taskCh:  make(chan *itemWrapper[T], config.QueueSize),
		config:  config,
		metrics: metrics.getOrCreate(stage.Name()),
	}
}

// Start 启动 Worker
func (w *stageWorker[T]) Start(ctx context.Context) error {
	if !w.running.CompareAndSwap(false, true) {
		return nil // 已经启动
	}

	w.ctx = ctx
	w.wg.Add(1)

	go w.runLoop()

	logrus.Debugf("[Pipeline] Worker %d (%s) started", w.index, w.stage.Name())
	return nil
}

// Stop 停止 Worker
func (w *stageWorker[T]) Stop() {
	if !w.running.CompareAndSwap(true, false) {
		return
	}

	// 不等待 worker 退出，因为 worker 会在 context 取消后自然退出
	logrus.Debugf("[Pipeline] Worker %d (%s) stop requested", w.index, w.stage.Name())
}

// SetNext 设置下一个 Worker
func (w *stageWorker[T]) SetNext(next *stageWorker[T]) {
	w.next = next
}

// runLoop 事件循环
func (w *stageWorker[T]) runLoop() {
	defer w.wg.Done()
	defer w.running.Store(false)

	for {
		select {
		case wrapper := <-w.taskCh:
			w.process(wrapper)

		case <-w.ctx.Done():
			return
		}
	}
}

// process 处理单个数据
func (w *stageWorker[T]) process(wrapper *itemWrapper[T]) {
	var start time.Time
	if wrapper.resultCh != nil {
		// 仅在需要结果时记录时间
		start = time.Now()
	}

	// 调用 Stage.Process
	item, err := w.stage.Process(wrapper.ctx, wrapper.item)

	if wrapper.resultCh != nil {
		// 仅在需要结果时更新统计
		latency := time.Since(start)
		w.metrics.Record(latency, err)
	}

	if err != nil {
		// 错误处理
		item, err = w.stage.OnError(wrapper.ctx, wrapper.item, err)
	}

	// 决定后续处理
	if err != nil {
		// 终止 Pipeline，返回错误
		w.sendResult(wrapper, err)
	} else if w.next == nil {
		// 最后一个 Stage，返回成功
		wrapper.item = item
		w.sendResult(wrapper, nil)
	} else {
		// 传递给下一个 Stage
		wrapper.item = item
		if err := w.next.submitInternal(wrapper); err != nil {
			logrus.Warnf("[Pipeline] Worker %d failed to submit to next: %v", w.index, err)
			w.sendResult(wrapper, err)
		}
	}
}

// sendResult 发送结果
func (w *stageWorker[T]) sendResult(wrapper *itemWrapper[T], err error) {
	if wrapper.resultCh == nil {
		// fire-and-forget，不需要发送结果
		return
	}

	select {
	case wrapper.resultCh <- result[T]{item: wrapper.item, err: err}:
	case <-wrapper.ctx.Done():
	case <-w.ctx.Done():
	}
}

// Submit 提交数据（外部调用，会自动包装）
func (w *stageWorker[T]) Submit(ctx context.Context, item T) error {
	wrapper := &itemWrapper[T]{
		item:      item,
		resultCh:  nil, // fire-and-forget，不创建 channel
		startTime: time.Now(),
		ctx:       ctx,
	}

	return w.submitInternal(wrapper)
}

// submitInternal 提交已包装的数据（内部调用）
func (w *stageWorker[T]) submitInternal(wrapper *itemWrapper[T]) error {
	select {
	case w.taskCh <- wrapper:
		return nil
	default:
		return ErrQueueFull
	}
}

// QueueLength 获取队列长度
func (w *stageWorker[T]) QueueLength() int {
	return len(w.taskCh)
}
