package pipeline

import (
	"context"

	"github.com/sirupsen/logrus"
)

// Pipeline[T] 管道组装器
//
// 使用 Builder 模式组装 Stage
type Pipeline[T any] struct {
	stages []Stage[T]
	config *Config
}

// NewPipeline 创建新 Pipeline
func NewPipeline[T any]() *Pipeline[T] {
	return &Pipeline[T]{
		config: DefaultConfig(),
	}
}

// AddStage 添加 Stage（顺序决定执行顺序）
//
// 示例：
//
//	pipeline.NewPipeline[Request]().
//	   AddStage(&ValidationStage{}).
//	   AddStage(&ProcessStage{}).
//	   AddStage(&SaveStage{})
func (p *Pipeline[T]) AddStage(stage Stage[T]) *Pipeline[T] {
	p.stages = append(p.stages, stage)
	return p
}

// WithConfig 配置 Pipeline
func (p *Pipeline[T]) WithConfig(config *Config) *Pipeline[T] {
	p.config = config
	return p
}

// Build 构建 Pipeline（启动所有 Stage）
//
// 返回 RunningPipeline，可以提交数据
func (p *Pipeline[T]) Build() (*RunningPipeline[T], error) {
	if len(p.stages) == 0 {
		return nil, ErrNoStages
	}

	ctx, cancel := context.WithCancel(context.Background())

	running := &RunningPipeline[T]{
		stages:  p.stages,
		config:  p.config,
		metrics: newMetrics(),
		ctx:     ctx,
		cancel:  cancel,
	}

	// 启动各个 Stage 的 Worker
	if err := running.start(); err != nil {
		cancel()
		return nil, err
	}

	logrus.Infof("[Pipeline] Pipeline built with %d stages", len(p.stages))
	return running, nil
}

// RunningPipeline[T] 运行中的 Pipeline
type RunningPipeline[T any] struct {
	stages  []Stage[T]
	workers []*stageWorker[T]
	config  *Config
	metrics *Metrics

	ctx    context.Context
	cancel context.CancelFunc
}

// start 启动所有 Worker
func (p *RunningPipeline[T]) start() error {
	p.workers = make([]*stageWorker[T], len(p.stages))

	// 按顺序创建 Worker
	for i, stage := range p.stages {
		worker := newStageWorker[T](i, stage, p.config, p.metrics)
		p.workers[i] = worker

		if err := worker.Start(p.ctx); err != nil {
			return err
		}

		// 设置下一个 Worker
		if i > 0 {
			p.workers[i-1].SetNext(worker)
		}
	}

	// p.wg 不需要 Add，worker 会管理自己的生命周期
	return nil
}

// Submit 提交数据到 Pipeline（fire-and-forget）
//
// 数据会按顺序经过各个 Stage：
//
//	Stage1 → Stage2 → Stage3 → ... → Result
//
// 返回：
//
//	error: 提交失败（如队列满）
func (p *RunningPipeline[T]) Submit(ctx context.Context, item T) error {
	// 背压检查
	if p.config.EnableBackpressure {
		if p.workers[0].QueueLength() >= p.config.MaxQueueLength {
			return ErrBackpressure
		}
	}

	// 创建 wrapper（不创建 resultCh，因为是 fire-and-forget）
	wrapper := &itemWrapper[T]{
		item:      item,
		resultCh:  nil, // 不需要结果，避免 channel 分配
		startTime: p.metrics.now(),
		ctx:       ctx,
	}

	// 提交到第一个 Stage
	return p.workers[0].submitInternal(wrapper)
}

// SubmitWithResult 提交并等待结果
func (p *RunningPipeline[T]) SubmitWithResult(ctx context.Context, item T) (T, error) {
	resultCh := make(chan result[T], 1)

	// 包装 item，添加结果回调
	wrapper := &itemWrapper[T]{
		item:      item,
		resultCh:  resultCh,
		startTime: p.metrics.now(),
		ctx:       ctx,
	}

	// 直接提交到第一个 Worker（避免双重包装）
	if err := p.workers[0].submitInternal(wrapper); err != nil {
		var zero T
		return zero, err
	}

	select {
	case res := <-resultCh:
		return res.item, res.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Close 关闭 Pipeline
func (p *RunningPipeline[T]) Close() error {
	logrus.Infof("[Pipeline] Closing pipeline with %d stages", len(p.stages))

	// 停止所有 Worker
	for _, w := range p.workers {
		w.Stop()
	}

	p.cancel()

	logrus.Infof("[Pipeline] Pipeline closed")
	return nil
}

// Stats 获取统计信息
func (p *RunningPipeline[T]) Stats() *Stats {
	return p.metrics.Snapshot()
}
