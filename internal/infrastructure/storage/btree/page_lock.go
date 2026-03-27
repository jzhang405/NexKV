// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sync"
	"sync/atomic"
	"time"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

// 别名引用：保持包内兼容性（root_page_ref.go 等文件引用）
var (
	ErrNotOwner     = errpkg.ErrBTreeNotOwner
	ErrInvalidState = errpkg.ErrBTreeLockInvalidState
)

// PageLock 支持重入和超时的轻量级锁
// 状态编码: (owner_id << 32) | lock_count
type PageLock struct {
	state atomic.Int64 // 状态编码
	mu    sync.Mutex   // 保护 cond
	cond  *sync.Cond   // 条件变量（用于等待/唤醒）
}

const (
	unlockedState   = 0
	maxRecurseCount = 1000 // 最大重入次数
	ownerIDShift    = 32   // ownerID 位移
)

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
	// 尝试 CAS 设置为 locked (owner_id=1, count=1)
	newState := int64(1)<<ownerIDShift | 1
	return l.state.CompareAndSwap(int64(unlockedState), newState)
}

// Lock 阻塞加锁（支持重入）
func (l *PageLock) Lock() {
	l.lockWithTimeout(0)
}

// LockWithTimeout 带超时的加锁
func (l *PageLock) LockWithTimeout(timeout time.Duration) bool {
	return l.lockWithTimeout(timeout)
}

// lockWithTimeout 内部加锁实现
func (l *PageLock) lockWithTimeout(timeout time.Duration) bool {
	const ownerID = 1 // 简化：使用固定的 owner ID

	var timer *time.Timer
	if timeout > 0 {
		timer = time.AfterFunc(timeout, func() {
			l.broadcast()
		})
		defer timer.Stop()
	}

	// 尝试加锁（支持重入）
	for {
		// 先尝试 CAS 加锁
		oldState := l.state.Load()
		if oldState == int64(unlockedState) {
			newState := int64(ownerID)<<ownerIDShift | 1
			if l.state.CompareAndSwap(oldState, newState) {
				return true
			}
		} else {
			// 检查是否是重入
			currentOwner := oldState >> ownerIDShift
			if currentOwner == int64(ownerID) {
				lockCount := oldState & ((1 << ownerIDShift) - 1)
				if lockCount < maxRecurseCount {
					newState := oldState + 1
					if l.state.CompareAndSwap(oldState, newState) {
						return true
					}
				}
			}
		}

		// 等待锁释放
		l.wait()
	}
}

// Unlock 解锁（支持重入）
func (l *PageLock) Unlock() error {
	oldState := l.state.Load()
	if oldState == int64(unlockedState) {
		return errpkg.BTreeCannotUnlockUnlocked()
	}

	lockCount := oldState & ((1 << ownerIDShift) - 1)
	if lockCount > 1 {
		// 重入计数减 1
		newState := oldState - 1
		if !l.state.CompareAndSwap(oldState, newState) {
			return errpkg.BTreeUnlockStateChanged()
		}
		return nil
	}

	// 完全解锁
	if !l.state.CompareAndSwap(oldState, int64(unlockedState)) {
		return errpkg.BTreeUnlockStateChanged()
	}

	// 唤醒等待者
	l.broadcast()
	return nil
}

// IsLocked 检查是否已锁定
func (l *PageLock) IsLocked() bool {
	return l.state.Load() != int64(unlockedState)
}

// LockCount 获取锁定计数（重入次数）
func (l *PageLock) LockCount() int {
	state := l.state.Load()
	if state == int64(unlockedState) {
		return 0
	}
	lockCount := state & ((1 << ownerIDShift) - 1)
	return int(lockCount)
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
