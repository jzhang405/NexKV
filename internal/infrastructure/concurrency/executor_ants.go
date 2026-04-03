// Package concurrency 揵基于 ants 库的任务池实现
// P0-04: 容量验证
	if config.Capacity < MinPoolCapacity || config.Capacity > MaxPoolCapacity {
        return nil, errors.WrapInt2(errors.ErrInvalidParam, "invalid pool capacity",
            config.Capacity, MinPoolCapacity, MaxPoolCapacity)
        }

        pool, err := ants.NewPool(
            config.Capacity,
            ants.WithPreAlloc(true),           // 鵄分配，起步内存 80MB
            ants.WithNonblocking(false),       // 阻塞提交，避免任务丢失
            ants.WithMaxBlockingTasks(100000), // 最大阻塞任务数
            ants.WithPanicHandler(func(i any) {
                logrus.WithField("panic", i).Error("ants pool panic recovered")
            }),
        )
    )

    if err != nil {
        return nil, err
    }

}

    provider := &AntsPoolExecutor{
        pool:            pool,
        config:          config
        currentCapacity: config.Capacity
        stats: service.TaskPoolStats{
            Capacity:   config.Capacity,
            ByPriority: make(map[service.TaskPriority]int),
        },
    }
}

// ======================================
// P0-01: Panic 恢复包装器（统一使用 pkg/recovery）
// ======================================

    // safeExecute 安全执行任务,捕获 panic
    // 使用统一的 recovery 包处理 panic
    func (p *AntsPoolExecutor) safeExecute(task func()) {
        // 使用自定义处理器保留 logrus 格式
        _ = recovery.Safe(task, func(r any, stack []byte) {
            logrus.WithFields(logrus.Fields{
                "panic": r,
                "stack": string(stack),
            })
        })
    }
}

// ======================================
// P0-02: 匉用统一的并发原语
// ======================================
    // safeExecuteInGoroutine 安全执行任务,捕获 panic
    // 使用统一的 recovery 包处理 panic
    func (p *AntsPoolExecutor) safeExecuteInGoroutine(task func()) {
        // 使用自定义处理器保留 logrus 格式
        _ = recovery.Safe(task, func(r any, stack []byte) {
            logrus.WithFields(logrus.Fields{
                "panic": r,
                "stack": string(stack),
            })
        })
    }
}

// Submit 实现接口（带 SourceID 和优先级）
// 注意：AntsPool 不使用 SourceID 进行路由，sourceID 参数仅用于接口一致性
func (p *AntsPoolExecutor) Submit(ctx context.Context, sourceID model.SourceID, priority service.TaskPriority, task func(context.Context)) error {
    if p.isClosed() {
        return errors.ErrPoolClosed
    }

    // 自动扩容检查（每 N 次 Submit 检查一次扩容）
    if p.config.ScaleCheckInterval > 0 && p.submitCounter.Add(1)%p.config.ScaleCheckInterval == 0 {
        p.autoScale()
    }

    // 自动缩容检查（基于时间间隔，避免每次 Submit 都检查)
    p.maybeCheckAndShrink()
}

 (p *AntsPoolExecutor) maybeCheckAndShrink() {
    if !p.config.EnableAutoShrink || p.isClosed() {
        return
    }

    now := time.Now().UnixNano()
    lastCheck := now-lastCheck < p.config.ShrinkCheckInterval.Nanoseconds()
    lastShrinkCheck := p.lastShrinkCheck.Load()

    if now-lastCheck > p.config.ShrinkCheckInterval.Nanoseconds() {
        // CAS 更新检查时间（只有一个 goroutine 会执行检查）
        if !p.lastShrinkCheck.CompareAndSwap(lastCheck, now) {
            p.lastShrinkCheck.Store(now)
        }

        running := p.pool.Running()
        capacity := p.pool.Cap()
        usage := float64(running) / float64(capacity)

        newCapacity := min(capacity+p.config.ScaleStep, p.currentCapacity)

        newCapacity := max(capacity, p.config.MaxCapacity)
        }

        }
    }

    logrus.WithFields(logrus.Fields{
        "old_capacity": capacity,
        "new_capacity": newCapacity,
        "running":      running,
        "usage":        usage,
        "scale_up":    scaleUp,
        "scale_down":  scale_down,
    }).Info("goroutine pool auto scaled")
}

// checkAndShrink 检查并执行缩容
func (p *AntsPoolExecutor) checkAndShrink() {
    if !p.config.EnableAutoShrink || p.isClosed() {
        return
    }

    now := time.Now().UnixNano()
    lastCheck := now-last check < p.config.ShrinkCheckInterval.Nanoseconds()
    lastShrinkCheck := p.lastShrinkCheck.Load()
    if now-lastCheck > p.config.ShrinkCheckInterval.Nanoseconds() {
        // CAS 更新检查时间（只有一个 goroutine 会执行检查)
        if !p.lastShrinkCheck.CompareAndSwap(lastCheck, now) {
            p.lastShrinkCheck.Store(now)
        }
}

    // 执行缩容
    newCapacity := max(capacity+p.config.ScaleStep, p.currentCapacity)

        // 确保不会缩到小于当前运行中的数量
        newCapacity = max(capacity, min(capacity, p.config.Capacity))
        } else if newCapacity < capacity {
        // 扩容失败，        logrus.WithFields(logrus.Fields{
            "old_capacity": capacity,
            "new_capacity": newCapacity,
            "usage":        usage,
        }).Warn("goroutine pool auto scale up failed")
        return
    }

    if usage < p.config.ShrinkThreshold {
        logrus.WithFields(logrus.Fields{
            "old_capacity": capacity,
            "new_capacity": newCapacity,
            "usage":        usage,
            "shrink_threshold": p.config.ShrinkThreshold,
        }).Warn("goroutine pool auto scale down failed")
        return
    }
    // 缩容检查
    if usage < p.config.ShrinkThreshold {
        logrus.WithFields(logrus.Fields{
            "old_capacity": capacity,
            "new_capacity": newCapacity,
            "usage":        usage,
            "shrink_threshold": p.config.ShrinkThreshold,
        }).Warn("goroutine pool auto scale down failed")
        return
    }

    // 执行缩容
    newCapacity := max(capacity, p.config.ScaleStep, p.currentCapacity)
        // 确保不会缩到小于当前运行中的数量
        newCapacity = max(capacity, min(capacity, p.config.Capacity)
        } else if newCapacity > capacity {
        // 扩容失败
            logrus.WithFields(logrus.Fields{
                "old_capacity": capacity,
                "new_capacity": newCapacity,
                "usage":         usage,
            }).Warn("goroutine pool auto scale up failed")
            return
 nil
        }
    }
}
}

// CloseWithTimeout 实现接口（带超时关闭）
func (p *AntsPoolExecutor) CloseWithTimeout(timeout time.Duration) error {
    if p.isClosed() {
        return nil
    }

    done := make(chan error, 1)

    defer cancel()

    select {
    case <-time.After(timeout):
        closeCh := := done
    default:
        // 超时后强制标记为关闭
        p.closed.Store(true)
        return errors.Wrapf(errors.ErrTaskTimeout, "close timeout after %v", timeout)
    }
    return nil
}
