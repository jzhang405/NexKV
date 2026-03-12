package btree

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnhancedPageLock_LockUnlock(t *testing.T) {
	lock := NewEnhancedPageLock()

	// 加锁
	lock.Lock()
	assert.True(t, lock.IsLocked())

	// 解锁
	err := lock.Unlock()
	require.NoError(t, err)
	assert.False(t, lock.IsLocked())
}

func TestEnhancedPageLock_TryLock(t *testing.T) {
	lock := NewEnhancedPageLock()

	// 第一次加锁应该成功
	assert.True(t, lock.TryLock())

	// 第二次加锁（同一个 goroutine）应该失败（非重入）
	assert.False(t, lock.TryLock())

	// 解锁后可以重新加锁
	err := lock.Unlock()
	require.NoError(t, err)
	assert.True(t, lock.TryLock())
}

func TestEnhancedPageLock_Reentrancy(t *testing.T) {
	lock := NewEnhancedPageLock()

	// 第一次加锁
	lock.Lock()

	// 第二次加锁（重入）应该成功
	lock.Lock()

	// 需要解锁两次
	err := lock.Unlock()
	require.NoError(t, err)
	assert.True(t, lock.IsLocked()) // 仍然锁定

	err = lock.Unlock()
	require.NoError(t, err)
	assert.False(t, lock.IsLocked()) // 完全解锁
}

func TestEnhancedPageLock_ConcurrentAccess(t *testing.T) {
	lock := NewEnhancedPageLock()
	var wg sync.WaitGroup
	counter := 0
	const goroutines = 100
	const incrementsPerGoroutine = 100

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				lock.Lock()
				counter++
				lock.Unlock()
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, goroutines*incrementsPerGoroutine, counter)
}

func TestEnhancedPageLock_LockWithTimeout(t *testing.T) {
	lock := NewEnhancedPageLock()

	// 在另一个 goroutine 中持有锁
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		lock.Lock()
		time.Sleep(100 * time.Millisecond)
		lock.Unlock()
	}()

	// 等待锁被持有
	time.Sleep(10 * time.Millisecond)

	// 带超时尝试加锁
	success := lock.LockWithTimeout(50 * time.Millisecond)
	assert.False(t, success, "Should not acquire lock within timeout")

	// 等待锁释放后再尝试
	wg.Wait()
	success = lock.LockWithTimeout(50 * time.Millisecond)
	assert.True(t, success, "Should acquire lock after it's released")
	lock.Unlock()
}

func TestEnhancedPageLock_LockWithContext(t *testing.T) {
	lock := NewEnhancedPageLock()

	// 在另一个 goroutine 中持有锁
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		lock.Lock()
		time.Sleep(100 * time.Millisecond)
		lock.Unlock()
	}()

	// 等待锁被持有
	time.Sleep(10 * time.Millisecond)

	// 带取消的上下文
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := lock.LockWithContext(ctx)
	assert.Error(t, err, "Should fail to acquire lock with timeout")
	assert.Equal(t, context.DeadlineExceeded, err)

	// 等待锁释放后再尝试
	wg.Wait()
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = lock.LockWithContext(ctx)
	assert.NoError(t, err, "Should acquire lock after it's released")
	lock.Unlock()
}

func TestEnhancedPageLock_UnlockError(t *testing.T) {
	lock := NewEnhancedPageLock()

	// 解锁未锁定的锁
	err := lock.Unlock()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot unlock unlocked lock")
}

func TestEnhancedPageLock_MultipleWaiters(t *testing.T) {
	lock := NewEnhancedPageLock()
	var wg sync.WaitGroup
	const goroutines = 10

	// 第一个 goroutine 持有锁
	lock.Lock()

	// 启动多个等待的 goroutine
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			lock.Lock()
			// 持有锁一小段时间
			time.Sleep(10 * time.Millisecond)
			lock.Unlock()
		}(i)
	}

	// 等待所有 goroutine 启动
	time.Sleep(50 * time.Millisecond)

	// 释放锁，让等待的 goroutine 竞争
	lock.Unlock()

	// 等待所有 goroutine 完成
	wg.Wait()

	assert.False(t, lock.IsLocked(), "Lock should be released after all goroutines complete")
}
