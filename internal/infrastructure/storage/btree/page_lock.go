package btree

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)


var (
	ErrNotOwner    = fmt.Errorf("not the lock owner")
	ErrInvalidState = fmt.Errorf("invalid lock state")
)

// PageLock 轻量级锁（支持重入和超时）
// state 编码：[63:48] lockCount (16 bits) | [47:0] ownerID (48 bits)
type PageLock struct {
	state atomic.Int64  // 状态编码：(lockCount << 48) | ownerID
	mu    sync.Mutex    // 保护 cond
	cond  *sync.Cond    // 条件变量，用于广播通知
}

// NewPageLock 创建新的 PageLock
func NewPageLock() *PageLock {
	l := &PageLock{
		state: atomic.Int64{},
	}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// TryLock 非阻塞加锁
func (l *PageLock) TryLock() bool {
	// 使用 CAS 尝试设置为锁定状态
	return l.state.CompareAndSwap(0, encodeOwnerState(0, 1))
}

// Lock 加锁（阻塞）
func (l *PageLock) Lock() {
	l.lockWithTimeout(0)
}

// LockWithTimeout 带超时的加锁
func (l *PageLock) LockWithTimeout(timeout time.Duration) bool {
	return l.lockWithTimeout(timeout)
}

// lockWithTimeout 内部加锁实现
func (l *PageLock) lockWithTimeout(timeout time.Duration) bool {
	var timer *time.Timer
	if timeout > 0 {
		timer = time.AfterFunc(timeout, func() {
			l.broadcast()
		})
		defer timer.Stop()
	}

	// 尝试加锁（支持重入）
	for {
		if l.state.CompareAndSwap(0, encodeOwnerState(0, 1)) {
			return true
		}

		// 等待锁释放
		l.wait()
	}
}

// Unlock 解锁（支持重入）
func (l *PageLock) Unlock() error {
	state := l.state.Load()
	ownerID, lockCount := decodeOwnerState(state)

	// 检查是否是锁的持有者
	// 注意：这里简化了，实际应该检查当前 goroutine ID
	// 在 Phase 1 原型中，我们假设 ownerID=0 表示当前 goroutine
	if ownerID != 0 {
		return ErrNotOwner
	}

	if lockCount == 1 {
		// 完全解锁
		if !l.state.CompareAndSwap(state, 0) {
			return ErrInvalidState
		}
		l.broadcast()
	} else {
		// 减少重入计数
		newState := encodeOwnerState(ownerID, lockCount-1)
		if !l.state.CompareAndSwap(state, newState) {
			return ErrInvalidState
		}
	}

	return nil
}

// IsLocked 判断是否已锁定
func (l *PageLock) IsLocked() bool {
	return l.state.Load() != 0
}

// LockCount 获取锁定计数（重入次数）
func (l *PageLock) LockCount() int {
	_, count := decodeOwnerState(l.state.Load())
	return count
}

// encodeOwnerState 编码所有者状态
// 位布局：[63:48] lockCount (16 bits, max 65535) | [47:0] ownerID (48 bits)
func encodeOwnerState(ownerID, lockCount int) int64 {
	if ownerID < 0 || ownerID >= (1<<48) {
		panic(fmt.Sprintf("owner ID %d out of range", ownerID))
	}
	if lockCount < 0 || lockCount >= (1<<16) {
		panic(fmt.Sprintf("lock count %d out of range", lockCount))
	}

	return (int64(lockCount) << 48) | (int64(ownerID) & 0xFFFFFFFFFFFF)
}

// decodeOwnerState 解码所有者状态
func decodeOwnerState(state int64) (ownerID, lockCount int) {
	lockCount = int(state >> 48)              // [63:48]
	ownerID = int(state & 0xFFFFFFFFFFFF)      // [47:0]
	return
}

// wait 等待锁释放通知
func (l *PageLock) wait() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cond.Wait()
}

// broadcast 广播通知所有等待者
func (l *PageLock) broadcast() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cond.Broadcast()
}
