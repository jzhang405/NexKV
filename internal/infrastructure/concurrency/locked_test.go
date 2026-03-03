// Package concurrency_test 提供锁包装器测试
package concurrency_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/infrastructure/concurrency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocked_View(t *testing.T) {
	locked := concurrency.NewLocked(42)

	err := locked.View(func(val int) error {
		assert.Equal(t, 42, val)
		return nil
	})

	require.NoError(t, err)
}

func TestLocked_View_Error(t *testing.T) {
	locked := concurrency.NewLocked(42)
	testErr := errors.New("test error")

	err := locked.View(func(val int) error {
		return testErr
	})

	require.Error(t, err)
	assert.Equal(t, testErr, err)
}

func TestLocked_Modify(t *testing.T) {
	type MyData struct {
		Value int
	}

	lockedData := concurrency.NewLocked(MyData{Value: 10})

	err := lockedData.Modify(func(data *MyData) error {
		data.Value = 20
		return nil
	})

	require.NoError(t, err)

	// 验证修改成功
	val := lockedData.Get()
	assert.Equal(t, 20, val.Value)
}

func TestLocked_Modify_Error(t *testing.T) {
	locked := concurrency.NewLocked(42)
	testErr := errors.New("test error")

	err := locked.Modify(func(val *int) error {
		return testErr
	})

	require.Error(t, err)
	assert.Equal(t, testErr, err)
}

func TestLocked_GetSet(t *testing.T) {
	locked := concurrency.NewLocked(42)

	// Test Get
	val := locked.Get()
	assert.Equal(t, 42, val)

	// Test Set
	locked.Set(100)
	val = locked.Get()
	assert.Equal(t, 100, val)
}

func TestLocked_ConcurrentAccess(t *testing.T) {
	locked := concurrency.NewLocked(0)

	var wg sync.WaitGroup
	wg.Add(1000)

	// 500 个并发读
	for i := 0; i < 500; i++ {
		go func() {
			defer wg.Done()
			_ = locked.View(func(val int) error {
				time.Sleep(10 * time.Microsecond)
				return nil
			})
		}()
	}

	// 500 个并发写
	for i := 0; i < 500; i++ {
		go func() {
			defer wg.Done()
			_ = locked.Modify(func(val *int) error {
				// 递增计数器
				*val = *val + 1
				time.Sleep(10 * time.Microsecond)
				return nil
			})
		}()
	}

	wg.Wait()

	// 验证最终值（应该是 500）
	val := locked.Get()
	assert.Equal(t, 500, val)
}

func TestLocked_GetDirect(t *testing.T) {
	locked := concurrency.NewLocked(42)

	// 直接访问（无锁）
	val := locked.GetDirect()
	assert.Equal(t, 42, val)

	// 直接修改（不推荐，仅用于测试）
	locked.Set(100)
	val = locked.GetDirect()
	assert.Equal(t, 100, val)
}

func TestLocked_NestedView(t *testing.T) {
	// 测试嵌套读锁
	locked := concurrency.NewLocked(42)

	err := locked.View(func(val1 int) error {
		// 嵌套读锁（应该允许）
		return locked.View(func(val2 int) error {
			assert.Equal(t, 42, val1)
			assert.Equal(t, 42, val2)
			return nil
		})
	})

	require.NoError(t, err)
}

func TestLocked_NestedModify(t *testing.T) {
	// 测试嵌套写锁（会导致死锁，	// 但在这个实现中，同一 goroutine 不会死锁
	locked := concurrency.NewLocked(42)

	err := locked.Modify(func(val1 *int) error {
		// 嵌套写锁（同一 goroutine 应该允许）
		return locked.Modify(func(val2 *int) error {
			assert.Equal(t, 42, *val1)
			assert.Equal(t, 42, *val2)
			return nil
		})
	})

	require.NoError(t, err)
}

// ==========================================
// 基准测试
// ==========================================

func BenchmarkLocked_View(b *testing.B) {
	locked := concurrency.NewLocked(42)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = locked.View(func(val int) error {
			return nil
		})
	}
}

func BenchmarkLocked_Modify(b *testing.B) {
	locked := concurrency.NewLocked(42)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = locked.Modify(func(val *int) error {
			return nil
		})
	}
}

func BenchmarkLocked_GetDirect(b *testing.B) {
	locked := concurrency.NewLocked(42)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = locked.GetDirect()
	}
}

func BenchmarkLocked_Get(b *testing.B) {
	locked := concurrency.NewLocked(42)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = locked.Get()
	}
}

func BenchmarkLocked_Set(b *testing.B) {
	locked := concurrency.NewLocked(42)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		locked.Set(100)
	}
}
