package bftree

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestNewBitmapLock 测试创建 BitmapLock
func TestNewBitmapLock(t *testing.T) {
	tests := []struct {
		name       string
		shardCount int
		wantErr    bool
	}{
		{
			name:       "valid shard count 1",
			shardCount: 1,
			wantErr:    false,
		},
		{
			name:       "valid shard count 16",
			shardCount: 16,
			wantErr:    false,
		},
		{
			name:       "valid shard count 64",
			shardCount: 64,
			wantErr:    false,
		},
		{
			name:       "invalid shard count 0",
			shardCount: 0,
			wantErr:    true,
		},
		{
			name:       "invalid shard count 65",
			shardCount: 65,
			wantErr:    true,
		},
		{
			name:       "invalid shard count not power of 2",
			shardCount: 3,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr {
				assert.Panics(t, func() {
					NewBitmapLock(tt.shardCount)
				})
			} else {
				bl := NewBitmapLock(tt.shardCount)
				assert.NotNil(t, bl)
				assert.Equal(t, tt.shardCount, bl.shardCount)
				assert.Equal(t, tt.shardCount, len(bl.shards))
			}
		})
	}
}

// TestBitmapLock_WriteLock 测试写锁基本操作
func TestBitmapLock_WriteLock(t *testing.T) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)

	// 初始状态：未锁定
	assert.False(t, bl.IsLocked(pageID))
	assert.False(t, bl.IsWriteLocked(pageID))

	// 获取写锁
	bl.Lock(pageID)

	// 锁定状态
	assert.True(t, bl.IsLocked(pageID))
	assert.True(t, bl.IsWriteLocked(pageID))

	// 释放写锁
	bl.Unlock(pageID)

	// 恢复未锁定状态
	assert.False(t, bl.IsLocked(pageID))
	assert.False(t, bl.IsWriteLocked(pageID))
}

// TestBitmapLock_ReadLock 测试读锁基本操作
func TestBitmapLock_ReadLock(t *testing.T) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)

	// 初始状态：未锁定
	assert.False(t, bl.IsLocked(pageID))
	assert.Equal(t, uint32(0), bl.GetReadLockCount(pageID))

	// 获取读锁
	bl.RLock(pageID)

	// 锁定状态
	assert.True(t, bl.IsLocked(pageID))
	assert.Equal(t, uint32(1), bl.GetReadLockCount(pageID))
	assert.False(t, bl.IsWriteLocked(pageID))

	// 再次获取读锁（递归）
	bl.RLock(pageID)
	assert.Equal(t, uint32(2), bl.GetReadLockCount(pageID))

	// 释放第一个读锁
	bl.RUnlock(pageID)
	assert.Equal(t, uint32(1), bl.GetReadLockCount(pageID))
	assert.True(t, bl.IsLocked(pageID))

	// 释放第二个读锁
	bl.RUnlock(pageID)
	assert.Equal(t, uint32(0), bl.GetReadLockCount(pageID))
	assert.False(t, bl.IsLocked(pageID))
}

// TestBitmapLock_TryLock 测试 TryLock 非阻塞操作
func TestBitmapLock_TryLock(t *testing.T) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)

	// 第一次 TryLock 应该成功
	assert.True(t, bl.TryLock(pageID))
	assert.True(t, bl.IsWriteLocked(pageID))

	// 第二次 TryLock 应该失败（已锁定）
	assert.False(t, bl.TryLock(pageID))

	// 释放锁
	bl.Unlock(pageID)

	// TryLock 应该再次成功
	assert.True(t, bl.TryLock(pageID))
	bl.Unlock(pageID)
}

// TestBitmapLock_TryRLock 测试 TryRLock 非阻塞操作
func TestBitmapLock_TryRLock(t *testing.T) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)

	// 第一次 TryRLock 应该成功
	assert.True(t, bl.TryRLock(pageID))
	assert.Equal(t, uint32(1), bl.GetReadLockCount(pageID))

	// 第二次 TryRLock 应该成功（递归）
	assert.True(t, bl.TryRLock(pageID))
	assert.Equal(t, uint32(2), bl.GetReadLockCount(pageID))

	// 获取写锁后，TryRLock 应该失败
	bl.RUnlock(pageID)
	bl.RUnlock(pageID)
	bl.Lock(pageID)
	assert.False(t, bl.TryRLock(pageID))

	bl.Unlock(pageID)
}

// TestBitmapLock_ReadWriteExclusion 测试读写锁互斥
func TestBitmapLock_ReadWriteExclusion(t *testing.T) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)

	// 先获取读锁，再尝试获取写锁
	bl.RLock(pageID)

	done := make(chan struct{})
	go func() {
		bl.Lock(pageID) // 应该阻塞，直到读锁释放
		close(done)
	}()

	// 等待一小段时间，确保 goroutine 已经启动并阻塞
	time.Sleep(10 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("Write lock should be blocked by read lock")
	default:
	}

	// 释放读锁
	bl.RUnlock(pageID)

	// 现在写锁应该能够获取
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Write lock should be acquired after read lock released")
	}

	bl.Unlock(pageID)
}

// TestBitmapLock_WriteReadExclusion 测试写读锁互斥
func TestBitmapLock_WriteReadExclusion(t *testing.T) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)

	// 先获取写锁，再尝试获取读锁
	bl.Lock(pageID)

	done := make(chan struct{})
	go func() {
		bl.RLock(pageID) // 应该阻塞，直到写锁释放
		close(done)
	}()

	// 等待一小段时间
	time.Sleep(10 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("Read lock should be blocked by write lock")
	default:
	}

	// 释放写锁
	bl.Unlock(pageID)

	// 现在读锁应该能够获取
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Read lock should be acquired after write lock released")
	}

	bl.RUnlock(pageID)
}

// TestBitmapLock_ConcurrentWriteLocks 测试并发写锁
func TestBitmapLock_ConcurrentWriteLocks(t *testing.T) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)
	const goroutines = 100
	var counter atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bl.Lock(pageID)
			// 临界区
			counter.Add(1)
			time.Sleep(1 * time.Microsecond)
			bl.Unlock(pageID)
		}()
	}

	wg.Wait()
	assert.Equal(t, int64(goroutines), counter.Load())
}

// TestBitmapLock_ConcurrentReadLocks 测试并发读锁
func TestBitmapLock_ConcurrentReadLocks(t *testing.T) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)
	const goroutines = 100
	var wg sync.WaitGroup

	// 所有 goroutines 应该能够同时持有读锁
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bl.RLock(pageID)
			// 读操作
			time.Sleep(1 * time.Microsecond)
			bl.RUnlock(pageID)
		}()
	}

	wg.Wait()
	assert.Equal(t, uint32(0), bl.GetReadLockCount(pageID))
}

// TestBitmapLock_MultiplePages 测试多页面独立锁定
func TestBitmapLock_MultiplePages(t *testing.T) {
	bl := NewBitmapLock(16)

	page1 := uint64(100)
	page2 := uint64(200)
	page3 := uint64(300)

	// 同时锁定多个页面
	bl.Lock(page1)
	bl.RLock(page2)
	bl.Lock(page3)

	assert.True(t, bl.IsWriteLocked(page1))
	assert.False(t, bl.IsWriteLocked(page2))
	assert.Equal(t, uint32(1), bl.GetReadLockCount(page2))
	assert.True(t, bl.IsWriteLocked(page3))

	// 释放所有锁
	bl.Unlock(page1)
	bl.RUnlock(page2)
	bl.Unlock(page3)

	assert.False(t, bl.IsLocked(page1))
	assert.False(t, bl.IsLocked(page2))
	assert.False(t, bl.IsLocked(page3))
}

// TestBitmapLock_ShardDistribution 测试分片分布
func TestBitmapLock_ShardDistribution(t *testing.T) {
	bl := NewBitmapLock(16)

	// 测试不同 pageID 映射到不同分片
	shardMap := make(map[int]bool)

	for i := uint64(0); i < 1000; i++ {
		shard := bl.getShard(i)
		shardIdx := -1
		for j, s := range bl.shards {
			if s == shard {
				shardIdx = j
				break
			}
		}
		shardMap[shardIdx] = true
	}

	// 应该使用到大部分分片
	assert.Greater(t, len(shardMap), 8, "Shards should be well-distributed")
}

// TestBitmapLock_Stats 测试统计信息
func TestBitmapLock_Stats(t *testing.T) {
	bl := NewBitmapLock(16)

	// 初始统计
	stats := bl.Stats()
	assert.Equal(t, 16, stats.ShardCount)
	assert.Equal(t, 0, stats.LockedPages)

	// 锁定一些页面
	bl.Lock(100)
	bl.RLock(200)
	bl.Lock(300)

	stats = bl.Stats()
	assert.Equal(t, 3, stats.LockedPages)

	// 释放锁
	bl.Unlock(100)
	bl.RUnlock(200)
	bl.Unlock(300)

	stats = bl.Stats()
	assert.Equal(t, 0, stats.LockedPages)
}

// TestBitmapLock_HighConcurrency 测试高并发场景
func TestBitmapLock_HighConcurrency(t *testing.T) {
	bl := NewBitmapLock(16)
	const pages = 100
	const goroutines = 1000
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup
	done := make(chan struct{}, goroutines)

	start := time.Now()

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				pageID := uint64((id + j) % pages)

				// 随机选择读或写
				if (id+j)%2 == 0 {
					bl.Lock(pageID)
					time.Sleep(10 * time.Microsecond)
					bl.Unlock(pageID)
				} else {
					bl.RLock(pageID)
					time.Sleep(10 * time.Microsecond)
					bl.RUnlock(pageID)
				}
			}
			done <- struct{}{}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	t.Logf("Completed %d operations in %v (%.2f ops/sec)",
		goroutines*operationsPerGoroutine,
		elapsed,
		float64(goroutines*operationsPerGoroutine)/elapsed.Seconds())

	// 验证所有锁都已释放
	for i := uint64(0); i < pages; i++ {
		assert.False(t, bl.IsLocked(i), "Page %d should be unlocked", i)
	}
}

// BenchmarkBitmapLock_LockUnlock 基准测试：Lock/Unlock
func BenchmarkBitmapLock_LockUnlock(b *testing.B) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bl.Lock(pageID)
		bl.Unlock(pageID)
	}
}

// BenchmarkBitmapLock_RLockRUnlock 基准测试：RLock/RUnlock
func BenchmarkBitmapLock_RLockRUnlock(b *testing.B) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bl.RLock(pageID)
		bl.RUnlock(pageID)
	}
}

// BenchmarkBitmapLock_TryLock 基准测试：TryLock
func BenchmarkBitmapLock_TryLock(b *testing.B) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if bl.TryLock(pageID) {
			bl.Unlock(pageID)
		}
	}
}

// BenchmarkBitmapLock_ConcurrentRead 基准测试：并发读
func BenchmarkBitmapLock_ConcurrentRead(b *testing.B) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			bl.RLock(pageID)
			bl.RUnlock(pageID)
		}
	})
}

// BenchmarkBitmapLock_ConcurrentWrite 基准测试：并发写
func BenchmarkBitmapLock_ConcurrentWrite(b *testing.B) {
	bl := NewBitmapLock(16)
	pageID := uint64(100)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			bl.Lock(pageID)
			bl.Unlock(pageID)
		}
	})
}

// BenchmarkBitmapLock_MultiplePages 基准测试：多页面
func BenchmarkBitmapLock_MultiplePages(b *testing.B) {
	bl := NewBitmapLock(16)
	const pages = 100

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pageID := uint64(i % pages)
		bl.Lock(pageID)
		bl.Unlock(pageID)
	}
}
