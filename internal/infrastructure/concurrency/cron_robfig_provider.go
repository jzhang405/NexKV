// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
)

// ==========================================
// RobfigCronProvider 实现
// ==========================================

// RobfigCronProvider 基于 robfig/cron 的定时任务提供者
var _ service.CronJobProvider = (*RobfigCronProvider)(nil)

// RobfigCronProvider 定时任务提供者实现
type RobfigCronProvider struct {
	mu                sync.RWMutex
	cron              *cron.Cron
	goroutineProvider service.ExecutorManager
	jobs              map[string]*cronJobEntry
	nameToID          map[string]string
	nextID            int64
}

// cronJobEntry 定时任务条目
type cronJobEntry struct {
	id        string
	name      string
	entryID   cron.EntryID
	spec      service.CronSpec
	status    service.CronJobStatus
	priority  service.TaskPriority
	taskFunc  func(context.Context)
	createdAt time.Time
}

// NewRobfigCronProvider 创建 robfig/cron 定时任务提供者
func NewRobfigCronProvider(goroutineProvider service.ExecutorManager) *RobfigCronProvider {
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

// ======================================
// 生命周期
// ======================================

// Start 启动定时任务调度器
func (r *RobfigCronProvider) Start() {
	r.cron.Start()
}

// Stop 停止定时任务调度器
func (r *RobfigCronProvider) Stop() context.Context {
	return r.cron.Stop()
}

// ======================================
// 基础方法实现
// ======================================

// Register 注册定时任务（无参数）
func (r *RobfigCronProvider) Register(
	spec service.CronSpec,
	name string,
	task func(context.Context),
) (string, error) {
	return r.RegisterWithPriority(spec, name, service.PriorityNormal, task)
}

// RegisterWithPriority 注册带优先级的定时任务（无参数）
func (r *RobfigCronProvider) RegisterWithPriority(
	spec service.CronSpec,
	name string,
	priority service.TaskPriority,
	task func(context.Context),
) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 检查名称是否已存在
	if _, exists := r.nameToID[name]; exists {
		return "", errors.Wrapf(errors.ErrComponentExists, "job with name '%s' already exists", name)
	}

	// 生成 ID
	id := r.generateID()

	// 创建任务包装器
	wrappedTask := r.wrapTask(task, priority)

	// 添加到 cron
	entryID, err := r.cron.AddFunc(string(spec), wrappedTask)
	if err != nil {
		return "", errors.Wrapf(errors.ErrInvalidParam, "invalid cron spec: %v", err)
	}

	// 记录任务
	entry := &cronJobEntry{
		id:        id,
		name:      name,
		entryID:   entryID,
		spec:      spec,
		status:    service.CronJobStatusScheduled,
		priority:  priority,
		taskFunc:  task,
		createdAt: time.Now(),
	}
	r.jobs[id] = entry
	r.nameToID[name] = id

	return id, nil
}

// RegisterWithArg 注册带参数的定时任务（使用 any 类型）
func (r *RobfigCronProvider) RegisterWithArg(
	spec service.CronSpec,
	name string,
	task func(context.Context, any),
	arg any,
) (string, error) {
	return r.RegisterWithPriorityAndArg(spec, name, service.PriorityNormal, task, arg)
}

// RegisterWithPriorityAndArg 注册带参数和优先级的定时任务（使用 any 类型）
func (r *RobfigCronProvider) RegisterWithPriorityAndArg(
	spec service.CronSpec,
	name string,
	priority service.TaskPriority,
	task func(context.Context, any),
	arg any,
) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 检查名称是否已存在
	if _, exists := r.nameToID[name]; exists {
		return "", errors.Wrapf(errors.ErrComponentExists, "job with name '%s' already exists", name)
	}

	// 生成 ID
	id := r.generateID()

	// 创建任务包装器（带参数）
	wrappedTask := r.wrapTaskWithArg(task, arg, priority)

	// 添加到 cron
	entryID, err := r.cron.AddFunc(string(spec), wrappedTask)
	if err != nil {
		return "", errors.Wrapf(errors.ErrInvalidParam, "invalid cron spec: %v", err)
	}

	// 记录任务（存储无参数版本用于信息查询）
	entry := &cronJobEntry{
		id:        id,
		name:      name,
		entryID:   entryID,
		spec:      spec,
		status:    service.CronJobStatusScheduled,
		priority:  priority,
		taskFunc:  func(ctx context.Context) { task(ctx, arg) },
		createdAt: time.Now(),
	}
	r.jobs[id] = entry
	r.nameToID[name] = id

	return id, nil
}

// ======================================
// 类型安全方法（供泛型辅助函数调用）
// ======================================

// RegisterWithArgTyped 注册带参数的定时任务（类型安全）
func RegisterWithArgTyped[T any](
	provider *RobfigCronProvider,
	spec service.CronSpec,
	name string,
	task func(context.Context, T),
	arg T,
) (string, error) {
	return RegisterWithPriorityAndArgTyped(provider, spec, name, service.PriorityNormal, task, arg)
}

// RegisterWithPriorityAndArgTyped 注册带参数和优先级的定时任务（类型安全）
func RegisterWithPriorityAndArgTyped[T any](
	provider *RobfigCronProvider,
	spec service.CronSpec,
	name string,
	priority service.TaskPriority,
	task func(context.Context, T),
	arg T,
) (string, error) {
	return provider.RegisterWithPriorityAndArg(spec, name, priority, func(ctx context.Context, a any) {
		task(ctx, a.(T))
	}, arg)
}

// ======================================
// 任务控制
// ======================================

// Pause 暂停定时任务
func (r *RobfigCronProvider) Pause(jobID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.jobs[jobID]
	if !exists {
		return errors.Wrapf(errors.ErrComponentNotFound, "job '%s' not found", jobID)
	}

	if entry.status == service.CronJobStatusPaused {
		return nil // 已经暂停
	}

	// 从 cron 中移除
	r.cron.Remove(entry.entryID)
	entry.status = service.CronJobStatusPaused

	return nil
}

// Resume 恢复定时任务
func (r *RobfigCronProvider) Resume(jobID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.jobs[jobID]
	if !exists {
		return errors.Wrapf(errors.ErrComponentNotFound, "job '%s' not found", jobID)
	}

	if entry.status != service.CronJobStatusPaused {
		return errors.Wrapf(errors.ErrInvalidState, "job '%s' is not paused", jobID)
	}

	// 重新添加到 cron
	wrappedTask := r.wrapTask(entry.taskFunc, entry.priority)
	entryID, err := r.cron.AddFunc(string(entry.spec), wrappedTask)
	if err != nil {
		return errors.Wrapf(errors.ErrAsyncExecFailed, "failed to resume job: %v", err)
	}

	entry.entryID = entryID
	entry.status = service.CronJobStatusScheduled

	return nil
}

// Unregister 取消注册定时任务
func (r *RobfigCronProvider) Unregister(jobID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.jobs[jobID]
	if !exists {
		return errors.Wrapf(errors.ErrComponentNotFound, "job '%s' not found", jobID)
	}

	// 从 cron 中移除
	r.cron.Remove(entry.entryID)

	// 从记录中删除
	delete(r.jobs, jobID)
	delete(r.nameToID, entry.name)
	entry.status = service.CronJobStatusStopped

	return nil
}

// ======================================
// 任务查询
// ======================================

// GetJob 获取定时任务信息
func (r *RobfigCronProvider) GetJob(jobID string) (*service.CronJobInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, exists := r.jobs[jobID]
	if !exists {
		return nil, errors.Wrapf(errors.ErrComponentNotFound, "job '%s' not found", jobID)
	}

	return r.entryToInfo(entry), nil
}

// ListJobs 列出所有定时任务
func (r *RobfigCronProvider) ListJobs() []*service.CronJobInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	infos := make([]*service.CronJobInfo, 0, len(r.jobs))
	for _, entry := range r.jobs {
		infos = append(infos, r.entryToInfo(entry))
	}
	return infos
}

// ======================================
// 内部方法
// ======================================

// generateID 生成唯一 ID
func (r *RobfigCronProvider) generateID() string {
	r.nextID++
	return fmt.Sprintf("cron-%d", r.nextID)
}

// wrapTask 包装任务函数（无参数）
func (r *RobfigCronProvider) wrapTask(task func(context.Context), priority service.TaskPriority) func() {
	return func() {
		ctx := context.Background()
		if r.goroutineProvider != nil {
			_ = r.goroutineProvider.SubmitWithPriority(ctx, priority, task)
		} else {
			// P0-01: 没有 TaskPoolProvider，直接执行（添加 panic 恢复）
			defer func() {
				if err := recover(); err != nil {
					// cron.Recover 已经处理了 panic，但这里作为额外保护
					logrus.WithField("panic", err).Error("panic recovered in cron task")
				}
			}()
			task(ctx)
		}
	}
}

// wrapTaskWithArg 包装任务函数（带参数）
func (r *RobfigCronProvider) wrapTaskWithArg(task func(context.Context, any), arg any, priority service.TaskPriority) func() {
	return func() {
		ctx := context.Background()
		if r.goroutineProvider != nil {
			_ = r.goroutineProvider.SubmitWithPriority(ctx, priority, func(ctx context.Context) {
				task(ctx, arg)
			})
		} else {
			// P0-01: 没有 TaskPoolProvider，直接执行（添加 panic 恢复）
			defer func() {
				if err := recover(); err != nil {
					// cron.Recover 已经处理了 panic，但这里作为额外保护
					logrus.WithField("panic", err).Error("panic recovered in cron task with arg")
				}
			}()
			task(ctx, arg)
		}
	}
}

// entryToInfo 将任务条目转换为信息
func (r *RobfigCronProvider) entryToInfo(entry *cronJobEntry) *service.CronJobInfo {
	// 获取下次执行时间
	cronEntry := r.cron.Entry(entry.entryID)
	var nextRun time.Time
	if cronEntry.ID != 0 {
		nextRun = cronEntry.Next
	}

	return &service.CronJobInfo{
		ID:        entry.id,
		Name:      entry.name,
		Spec:      entry.spec,
		Status:    entry.status,
		NextRun:   nextRun,
		LastRun:   nil, // robfig/cron 不直接提供上次执行时间
		CreatedAt: entry.createdAt,
	}
}
