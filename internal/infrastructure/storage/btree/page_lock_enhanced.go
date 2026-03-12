package btree

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// EnhancedPageLock 支持重入和超时的轻量级锁
// 状态编码: (owner_id << 32) | lock_count
type EnhancedPageLock struct {
	state atomic.Int64 // 状态编码
	cond  *sync.Cond   // 条件变量（用于等待/唤醒）
}

const (
	unlockedState   = 0
	maxRecurseCount = 1000 // 最大重入次数
	ownerIDShift    = 32
)

// NewEnhancedPageLock 创建新的增强型页面锁
func NewEnhancedPageLock() *EnhancedPageLock {
	l := &EnhancedPageLock{
		cond: sync.NewCond(&sync.Mutex{}),
	}
	l.state.Store(int64(unlockedState))
	return l
}

// TryLock 非阻塞加锁
func (l *EnhancedPageLock) TryLock() bool {
	// 尝试 CAS 设置为 locked (owner_id=1, count=1)
	newState := int64(1)<<ownerIDShift | 1
	return l.state.CompareAndSwap(int64(unlockedState), newState)
}

// Lock 阻塞加锁（支持重入）
func (l *EnhancedPageLock) Lock() {
	l.LockWithContext(context.Background())
}

// LockWithContext 带上下文的加锁
func (l *EnhancedPageLock) LockWithContext(ctx context.Context) error {
	const ownerID = 1 // 简化：使用固定的 owner ID

	for {
		// 尝试加锁
		oldState := l.state.Load()
		var newState int64

		if oldState == int64(unlockedState) {
			newState = int64(ownerID)<<ownerIDShift | 1
			if l.state.CompareAndSwap(oldState, newState) {
				return nil
			}
		} else {
			// 检查是否是重入
			currentOwner := oldState >> ownerIDShift
			if currentOwner == int64(ownerID) {
				lockCount := oldState & ((1 << ownerIDShift) - 1)
				if lockCount < maxRecurseCount {
					newState = oldState + 1
					if l.state.CompareAndSwap(oldState, newState) {
						return nil
					}
				}
			}
		}

		// 等待
		if err := l.waitForNotify(ctx); err != nil {
			return err
		}
	}
}

// LockWithTimeout 带超时的加锁
func (l *EnhancedPageLock) LockWithTimeout(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return l.LockWithContext(ctx) == nil
}

// Unlock 解锁（支持重入）
func (l *EnhancedPageLock) Unlock() error {
	oldState := l.state.Load()
	if oldState == int64(unlockedState) {
		return fmt.Errorf("cannot unlock unlocked lock")
	}

	lockCount := oldState & ((1 << ownerIDShift) - 1)
	if lockCount > 1 {
		// 重入计数减 1
		newState := oldState - 1
		if !l.state.CompareAndSwap(oldState, newState) {
			return fmt.Errorf("unlock failed: state changed")
		}
		return nil
	}

	// 完全解锁
	if !l.state.CompareAndSwap(oldState, int64(unlockedState)) {
		return fmt.Errorf("unlock failed: state changed")
	}

	// 唤醒一个等待者
	l.notifyOne()
	return nil
}

// IsLocked 检查是否已锁定
func (l *EnhancedPageLock) IsLocked() bool {
	return l.state.Load() != int64(unlockedState)
}

// waitForNotify 等待唤醒信号
func (l *EnhancedPageLock) waitForNotify(ctx context.Context) error {
	// 启动一个 goroutine 监听 context 取消
	done := make(chan struct{})
	stopCh := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			l.cond.Broadcast()
		case <-stopCh:
			return
		}
	}()

	l.cond.L.Lock()
	l.cond.Wait()
	l.cond.L.Unlock()

	close(stopCh)
	close(done)

	return ctx.Err()
}

// notifyOne 唤醒一个等待者
func (l *EnhancedPageLock) notifyOne() {
	l.cond.Signal()
}
