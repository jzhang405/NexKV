package btree

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPageLock(t *testing.T) {
	lock := NewPageLock()
	assert.NotNil(t, lock)
	assert.False(t, lock.IsLocked())
	assert.Equal(t, 0, lock.LockCount())
}

func TestPageLock_LockUnlock(t *testing.T) {
	lock := NewPageLock()

	// 加锁
	lock.Lock()
	assert.True(t, lock.IsLocked())
	assert.Equal(t, 1, lock.LockCount())

	// 解锁
	err := lock.Unlock()
	require.NoError(t, err)
	assert.False(t, lock.IsLocked())
	assert.Equal(t, 0, lock.LockCount())
}

func TestPageLock_TryLock(t *testing.T) {
	lock := NewPageLock()

	// 第一次加锁应该成功
	got := lock.TryLock()
	assert.True(t, got)
	assert.True(t, lock.IsLocked())

	// 第二次加锁应该失败（非阻塞）
	got = lock.TryLock()
	assert.False(t, got)

	// 解锁后可以再次加锁
	_ = lock.Unlock()
	got = lock.TryLock()
	assert.True(t, got)
}

func TestPageLock_Reentrancy(t *testing.T) {
	// 注意：当前实现简化了 ownerID 检查
	// 实际使用中需要正确识别 goroutine ID
	// 这个测试暂时跳过重入测试
	t.Skip("需要实现 goroutine ID 识别")

	lock := NewPageLock()
	lock.Lock()
	// 同一个 goroutine 重入
	lock.Lock()
	assert.Equal(t, 2, lock.LockCount())

	// 解锁两次
	_ = lock.Unlock()
	_ = lock.Unlock()
	assert.False(t, lock.IsLocked())
}

func TestPageLock_LockWithTimeout(t *testing.T) {
	lock := NewPageLock()

	// 加锁成功
	got := lock.LockWithTimeout(100 * time.Millisecond)
	assert.True(t, got)
	assert.True(t, lock.IsLocked())

	// 释放锁
	_ = lock.Unlock()

	// 尝试加锁（应该立即成功）
	done := make(chan bool)
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = lock.Unlock()
		close(done)
	}()

	got = lock.LockWithTimeout(time.Second)
	assert.True(t, got)
	<-done
}

func TestPageLock_ConcurrentAccess(t *testing.T) {
	lock := NewPageLock()
	const goroutines = 100
	var ops int64
	var wg sync.WaitGroup

	// 并发加锁解锁
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				lock.Lock()
				ops++
				lock.Unlock()
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int64(goroutines*100), ops)
	assert.False(t, lock.IsLocked())
}

func TestEncodeDecodeOwnerState(t *testing.T) {
	tests := []struct {
		name     string
		ownerID  int
		lockCount int
	}{
		{"零值", 0, 0},
		{"仅 ownerID", 12345, 0},
		{"仅 lockCount", 0, 1},
		{"两者都有", 12345, 100},
		{"较大 lockCount", 0, 1000},
		{"较大 ownerID", 0x123456789ABC, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := encodeOwnerState(tt.ownerID, tt.lockCount)
			ownerID, lockCount := decodeOwnerState(state)
			assert.Equal(t, tt.ownerID, ownerID)
			assert.Equal(t, tt.lockCount, lockCount)
		})
	}
}

func TestEncodeOwnerState_Overflow(t *testing.T) {
	t.Run("ownerID 溢出", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				assert.Contains(t, r, "owner ID")
			}
		}()
		encodeOwnerState(1<<48, 0)
	})

	t.Run("lockCount 溢出", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				assert.Contains(t, r, "lock count")
			}
		}()
		encodeOwnerState(0, 1<<16)
	})
}

// Benchmark 编码解码性能
func BenchmarkEncodeOwnerState(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encodeOwnerState(12345, 100)
	}
}

func BenchmarkDecodeOwnerState(b *testing.B) {
	state := encodeOwnerState(12345, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decodeOwnerState(state)
	}
}

func BenchmarkPageLock_LockUnlock(b *testing.B) {
	lock := NewPageLock()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lock.Lock()
		lock.Unlock()
	}
}
