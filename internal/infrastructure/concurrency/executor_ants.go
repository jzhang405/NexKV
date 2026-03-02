// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/jzhang405/NexKV/pkg/recovery"
	"github.com/panjf2000/ants/v2"
	"github.com/sirupsen/logrus"
)

// ==========================================
// AntsPoolExecutor 实现
// ==========================================

// AntsPoolExecutor 基于 ants 库的任务池实现
// 实现 domain/service.TaskExecutor 接口
type AntsPoolExecutor struct {
	pool    *ants.Pool
	config  *ProviderConfig
	stats   service.TaskPoolStats
	statsMu sync.RWMutex
	closed  atomic.Bool
	// 缩容相关
	scaleCheckTicker *time.Ticker // 缩容检查定时器
	currentCapacity  int          // 当前实际容量
	// 扩容检查计数器
	submitCounter atomic.Int64 // Submit 调用计数器
}

// ProviderConfig 任务池配置
type ProviderConfig struct {
	// Capacity 任务池容量
	Capacity int
	// MaxCapacity 最大容量（自动扩容上限）
	MaxCapacity int
	// EnablePriority 是否启用优先级
	EnablePriority bool
	// EnableDelayed 是否启用延迟任务
	EnableDelayed bool
	// EnableAutoScale 是否启用自动扩容
	EnableAutoScale bool
	// ScaleThreshold 扩容阈值（运行中 goroutine 占比）
	ScaleThreshold float64
	// ScaleStep 每次扩容步长
	ScaleStep int
	// ScaleCheckInterval 扩容检查间隔（每N次Submit检查一次）
	ScaleCheckInterval int64
	// EnableAutoShrink 是否启用自动缩容
	EnableAutoShrink bool
	// ShrinkThreshold 缩容阈值（运行中 goroutine 占比）
	ShrinkThreshold float64
	// ShrinkStep 每次缩容步长
	ShrinkStep int
	// ShrinkCheckInterval 缩容检查间隔
	ShrinkCheckInterval time.Duration
	// ShrinkCooldown 缩容冷却时间（扩容后多久才能缩容）
	ShrinkCooldown time.Duration
	// OnError 错误回调函数（可选）
	// 当延迟任务内部提交失败时调用
	// taskType: "delayed", "priority", "batch" 等
	OnError func(err error, taskType string)
}

// DefaultProviderConfig 默认配置
// 起步内存约 80MB（10000 * 8KB），自动扩容到最大 10 万，支持自动缩容
func DefaultProviderConfig() *ProviderConfig {
	return &ProviderConfig{
		Capacity:            10000,  // 起步容量 1 万（约 80MB）
		MaxCapacity:         100000, // 最大容量 10 万
		EnablePriority:      true,
		EnableDelayed:       true,
		EnableAutoScale:     true,             // 启用自动扩容
		ScaleThreshold:      0.8,              // 使用率超过 80% 时扩容
		ScaleStep:           5000,             // 每次扩容 5000
		ScaleCheckInterval:  100,              // 每 100 次 Submit 检查一次扩容
		EnableAutoShrink:    true,             // 启用自动缩容
		ShrinkThreshold:     0.3,              // 使用率低于 30% 时缩容
		ShrinkStep:          5000,             // 每次缩容 5000
		ShrinkCheckInterval: 5 * time.Minute,  // 每 5 分钟检查一次缩容
		ShrinkCooldown:      10 * time.Minute, // 扩容后 10 分钟才能缩容
	}
}

// NewAntsExecutor 创建 ants 任务池执行器
func NewAntsExecutor(config *ProviderConfig) (*AntsPoolExecutor, error) {
	if config == nil {
		config = DefaultProviderConfig()
	}

	// P0-04: 容量验证
	if config.Capacity < MinPoolCapacity || config.Capacity > MaxPoolCapacity {
		return nil, errors.Wrapf(errors.ErrInvalidParam, "invalid pool capacity: %d (must be between %d and %d)",
			config.Capacity, MinPoolCapacity, MaxPoolCapacity)
	}

	pool, err := ants.NewPool(
		config.Capacity,
		ants.WithPreAlloc(true),           // 预分配，起步内存 80MB
		ants.WithNonblocking(false),       // 阻塞提交，避免任务丢失
		ants.WithMaxBlockingTasks(100000), // 最大阻塞任务数
		ants.WithPanicHandler(func(i any) {
			logrus.WithField("panic", i).Error("ants pool panic recovered")
		}),
	)
	if err != nil {
		return nil, err
	}

	provider := &AntsPoolExecutor{
		pool:            pool,
		config:          config,
		currentCapacity: config.Capacity,
		stats: service.TaskPoolStats{
			Capacity:   config.Capacity,
			ByPriority: make(map[service.TaskPriority]int),
		},
	}

	// 启动缩容检查协程
	if config.EnableAutoShrink {
		provider.startShrinkChecker()
	}

	return provider, nil
}

// ======================================
// P0-01: Panic 恢复包装器（统一使用 pkg/recovery）
// ======================================

// safeExecute 安全执行任务，捕获 panic
// 使用统一的 recovery 包处理 panic
func (p *AntsPoolExecutor) safeExecute(task func()) {
	// 使用自定义处理器保留 logrus 格式
	_ = recovery.Safe(task, func(r any, stack []byte) {
		logrus.WithFields(logrus.Fields{
			"panic": r,
			"stack": string(stack),
		}).Error("panic recovered in goroutine task")
	})
}

// ======================================
// 基础方法实现
// ======================================

// Submit 实现接口（带优先级）
func (p *AntsPoolExecutor) Submit(ctx context.Context, priority service.TaskPriority, task func(context.Context)) error {
	if p.isClosed() {
		return errors.ErrPoolClosed
	}

	// 自动扩容检查（每 N 次 Submit 检查一次）
	if p.config.ScaleCheckInterval > 0 && p.submitCounter.Add(1)%p.config.ScaleCheckInterval == 0 {
		p.autoScale()
	}

	// P0-01: 添加 panic 恢复
	return p.pool.Submit(func() {
		p.safeExecute(func() {
			task(ctx)
		})
	})
}

// SubmitWithArg 实现接口
func (p *AntsPoolExecutor) SubmitWithArg(
	ctx context.Context,
	task func(context.Context, any),
	arg any,
) error {
	if p.isClosed() {
		return errors.ErrPoolClosed
	}

	return p.pool.Submit(func() {
		p.safeExecute(func() {
			task(ctx, arg)
		})
	})
}

// SubmitWithPriority 实现接口（保留以兼容性，实际通过 Submit 调用）
func (p *AntsPoolExecutor) SubmitWithPriority(
	ctx context.Context,
	priority service.TaskPriority,
	task func(context.Context),
) error {
	if p.isClosed() {
		return errors.ErrPoolClosed
	}

	// ants 不支持原生优先级，这里通过统计记录
	p.statsMu.Lock()
	p.stats.ByPriority[priority]++
	p.statsMu.Unlock()

	return p.pool.Submit(func() {
		p.safeExecute(func() {
			task(ctx)
		})
	})
}

// ======================================
// 管理方法实现
// ======================================

// Stats 实现接口
func (p *AntsPoolExecutor) Stats() service.TaskPoolStats {
	p.statsMu.RLock()
	defer p.statsMu.RUnlock()

	stats := p.stats
	stats.Running = p.pool.Running()
	stats.Waiting = p.pool.Waiting()
	stats.Total = p.pool.Free() + stats.Running
	return stats
}

// Health 实现接口
func (p *AntsPoolExecutor) Health() service.TaskHealthStatus {
	if p.isClosed() {
		return model.TaskHealthStatusUnhealthy
	}

	// 检查任务池健康状态
	if p.pool.IsClosed() {
		return model.TaskHealthStatusUnhealthy
	}

	return model.TaskHealthStatusHealthy
}

// SetCapacity 实现接口
func (p *AntsPoolExecutor) SetCapacity(capacity int) error {
	if p.isClosed() {
		return errors.ErrPoolClosed
	}

	// P0-04: 容量验证
	if capacity < MinPoolCapacity || capacity > MaxPoolCapacity {
		return errors.Wrapf(errors.ErrInvalidParam, "invalid capacity: %d (must be between %d and %d)",
			capacity, MinPoolCapacity, MaxPoolCapacity)
	}

	p.pool.Tune(capacity)

	p.statsMu.Lock()
	p.stats.Capacity = capacity
	p.statsMu.Unlock()

	return nil
}

// Close 实现接口
func (p *AntsPoolExecutor) Close() error {
	// 使用 atomic 确保幂等性
	if p.closed.Swap(true) {
		return nil
	}

	// 停止缩容检查定时器
	if p.scaleCheckTicker != nil {
		p.scaleCheckTicker.Stop()
	}

	// 释放池资源
	p.pool.Release()
	return nil
}

// CloseWithTimeout 实现接口
func (p *AntsPoolExecutor) CloseWithTimeout(timeout time.Duration) error {
	done := make(chan error, 1)

	go func() {
		done <- p.Close()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		// 超时后强制标记为关闭
		p.closed.Store(true)
		return errors.Wrapf(errors.ErrTaskTimeout, "close timeout after %v", timeout)
	}
}

// ======================================
// 内部方法
// ======================================

func (p *AntsPoolExecutor) isClosed() bool {
	return p.closed.Load()
}

// ======================================
// 自动扩缩容支持
// ======================================

// autoScale 检查并执行自动扩容
func (p *AntsPoolExecutor) autoScale() {
	if !p.config.EnableAutoScale {
		return
	}

	stats := p.pool.Running()
	capacity := p.pool.Cap()

	// 计算使用率
	usage := float64(stats) / float64(capacity)

	// 超过阈值且未达到最大容量时扩容
	if usage >= p.config.ScaleThreshold && capacity < p.config.MaxCapacity {
		newCapacity := min(capacity+p.config.ScaleStep, p.config.MaxCapacity)

		// 使用 Tune 动态调整容量
		p.pool.Tune(newCapacity)
		p.currentCapacity = newCapacity

		logrus.WithFields(logrus.Fields{
			"old_capacity": capacity,
			"new_capacity": newCapacity,
			"running":      stats,
			"usage":        usage,
		}).Info("goroutine pool auto scaled up")
	}
}

// startShrinkChecker 启动缩容检查协程
func (p *AntsPoolExecutor) startShrinkChecker() {
	p.scaleCheckTicker = time.NewTicker(p.config.ShrinkCheckInterval)

	go func() {
		for range p.scaleCheckTicker.C {
			if p.isClosed() {
				return
			}
			p.checkAndShrink()
		}
	}()
}

// checkAndShrink 检查并执行缩容
func (p *AntsPoolExecutor) checkAndShrink() {
	if !p.config.EnableAutoShrink || p.isClosed() {
		return
	}

	running := p.pool.Running()
	capacity := p.pool.Cap()

	// 计算使用率
	usage := float64(running) / float64(capacity)

	// 使用率低于阈值且高于初始容量时缩容
	if usage < p.config.ShrinkThreshold && capacity > p.config.Capacity {
		newCapacity := max(capacity-p.config.ShrinkStep, p.config.Capacity)

		// 确保不会缩到小于当前运行中的数量
		newCapacity = max(newCapacity, running+p.config.ShrinkStep/2)

		// 使用 Tune 调整容量（ants 会释放多余的 worker）
		p.pool.Tune(newCapacity)
		p.currentCapacity = newCapacity

		logrus.WithFields(logrus.Fields{
			"old_capacity": capacity,
			"new_capacity": newCapacity,
			"running":      running,
			"usage":        usage,
		}).Info("goroutine pool auto scaled down")
	}
}
