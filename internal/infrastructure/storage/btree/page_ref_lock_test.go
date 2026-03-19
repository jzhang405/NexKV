package btree

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPageRef_GetLock_Idempotent 验证多次调用 GetLock() 返回同一锁实例
func TestPageRef_GetLock_Idempotent(t *testing.T) {
	ref := NewPageRef()

	// 第一次调用：创建锁
	lock1 := ref.GetLock()
	assert.NotNil(t, lock1, "第一次调用 GetLock() 应该返回非 nil 锁")

	// 第二次调用：返回同一锁
	lock2 := ref.GetLock()
	assert.Same(t, lock1, lock2, "多次调用 GetLock() 应该返回同一锁实例")

	// 第三次调用：仍然是同一锁
	lock3 := ref.GetLock()
	assert.Same(t, lock1, lock3, "第三次调用仍应返回同一锁实例")
}

// TestPageRef_GetLock_ConcurrentInit 验证并发调用 GetLock() 的 CAS 初始化安全性
func TestPageRef_GetLock_ConcurrentInit(t *testing.T) {
	ref := NewPageRef()

	const goroutines = 100
	locks := make([]*PageLock, goroutines)
	var wg sync.WaitGroup

	// 并发调用 GetLock()
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			locks[idx] = ref.GetLock()
		}(i)
	}

	wg.Wait()

	// 验证所有 goroutine 获得的是同一锁
	firstLock := locks[0]
	assert.NotNil(t, firstLock, "第一个锁不应该为 nil")

	for i := 1; i < goroutines; i++ {
		assert.Same(t, firstLock, locks[i], "所有 goroutine 应该获得同一锁实例")
	}
}

// TestPageRef_GetLock_DifferentPageRefs 验证不同 PageRef 有独立的锁
func TestPageRef_GetLock_DifferentPageRefs(t *testing.T) {
	ref1 := NewPageRef()
	ref2 := NewPageRef()

	lock1 := ref1.GetLock()
	lock2 := ref2.GetLock()

	assert.NotNil(t, lock1, "ref1 的锁不应该为 nil")
	assert.NotNil(t, lock2, "ref2 的锁不应该为 nil")
	assert.NotSame(t, lock1, lock2, "不同 PageRef 应该有独立的锁")
}

// TestPageRef_Clone_IndependentLocks 验证 Clone 后的 PageRef 有独立的锁
func TestPageRef_Clone_IndependentLocks(t *testing.T) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1})
	ref := NewPageRefWithInfo(info)

	// 原始 PageRef 获取锁
	originalLock := ref.GetLock()
	assert.NotNil(t, originalLock)

	// Clone 创建新 PageRef
	cloned := ref.Clone()

	// Clone 的 PageRef 有自己独立的锁
	clonedLock := cloned.GetLock()
	assert.NotNil(t, clonedLock)
	assert.NotSame(t, originalLock, clonedLock, "Clone 后的 PageRef 应该有独立的锁")
}

// BenchmarkPageRef_GetLock 性能基准测试
func BenchmarkPageRef_GetLock(b *testing.B) {
	ref := NewPageRef()
	// 预热：确保锁已创建
	_ = ref.GetLock()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ref.GetLock()
	}
}

// BenchmarkPageRef_GetLock_Concurrent 并发 GetLock 性能基准测试
func BenchmarkPageRef_GetLock_Concurrent(b *testing.B) {
	ref := NewPageRef()
	// 预热：确保锁已创建
	_ = ref.GetLock()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = ref.GetLock()
		}
	})
}

// BenchmarkPageRef_GetLock_ParallelInit 并发初始化性能基准测试
func BenchmarkPageRef_GetLock_ParallelInit(b *testing.B) {
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		ref := NewPageRef()
		for pb.Next() {
			_ = ref.GetLock()
		}
	})
}
