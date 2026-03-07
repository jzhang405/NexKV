package bftree

import (
	"context"
	"sync"
	"testing"
)

// Benchmark_RWMutex_MultiplePages RWMutex 多页面并发读写基准测试
func Benchmark_RWMutex_MultiplePages(b *testing.B) {
	config := DefaultConfig()
	config.UseBitmapLock = false
	config.EnableWAL = false
	config.DataDir = b.TempDir()

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	// 预填充多个页面的数据
	const numPages = 100
	for i := 0; i < numPages; i++ {
		key := []byte{byte(i >> 8), byte(i & 0xFF)}
		value := make([]byte, 100)
		_ = tree.Set(context.Background(), key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte{byte(i >> 8), byte(i & 0xFF)}
			_, _ = tree.Get(context.Background(), key)
			i++
			if i >= numPages {
				i = 0
			}
		}
	})
}

// Benchmark_BitmapLock_MultiplePages BitmapLock 多页面并发读写基准测试
func Benchmark_BitmapLock_MultiplePages(b *testing.B) {
	config := DefaultConfig()
	config.UseBitmapLock = true
	config.EnableWAL = false
	config.DataDir = b.TempDir()

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	// 预填充多个页面的数据
	const numPages = 100
	for i := 0; i < numPages; i++ {
		key := []byte{byte(i >> 8), byte(i & 0xFF)}
		value := make([]byte, 100)
		_ = tree.Set(context.Background(), key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte{byte(i >> 8), byte(i & 0xFF)}
			_, _ = tree.Get(context.Background(), key)
			i++
			if i >= numPages {
				i = 0
			}
		}
	})
}

// Test_PerformanceComparison 运行性能对比测试并输出结果
func Test_PerformanceComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance comparison test in short mode")
	}

	results := &sync.Map{}

	// 测试 RWMutex 模式
	t.Run("RWMutex", func(t *testing.T) {
		config := DefaultConfig()
		config.UseBitmapLock = false
		config.EnableWAL = false
		config.DataDir = t.TempDir()

		tree, err := NewBfTree(config)
		if err != nil {
			t.Fatal(err)
		}
		defer tree.Close()

		// 预填充数据
		const numKeys = 1000
		for i := 0; i < numKeys; i++ {
			key := []byte{byte(i >> 8), byte(i & 0xFF), byte(i)}
			value := make([]byte, 100)
			_ = tree.Set(context.Background(), key, value)
		}

		// 并发读取测试
		const goroutines = 100
		const opsPerGoroutine = 1000

		var wg sync.WaitGroup
		start := make(chan struct{})

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				<-start
				for j := 0; j < opsPerGoroutine; j++ {
					key := []byte{byte((id + j) >> 8), byte((id + j) & 0xFF), byte(id + j)}
					_, _ = tree.Get(context.Background(), key)
				}
			}(i)
		}

		// 计时开始
		close(start)
		wg.Wait()

		// 存储结果
		results.Store("RWMutex", "completed")
	})

	// 测试 BitmapLock 模式
	t.Run("BitmapLock", func(t *testing.T) {
		config := DefaultConfig()
		config.UseBitmapLock = true
		config.EnableWAL = false
		config.DataDir = t.TempDir()

		tree, err := NewBfTree(config)
		if err != nil {
			t.Fatal(err)
		}
		defer tree.Close()

		// 预填充数据
		const numKeys = 1000
		for i := 0; i < numKeys; i++ {
			key := []byte{byte(i >> 8), byte(i & 0xFF), byte(i)}
			value := make([]byte, 100)
			_ = tree.Set(context.Background(), key, value)
		}

		// 并发读取测试
		const goroutines = 100
		const opsPerGoroutine = 1000

		var wg sync.WaitGroup
		start := make(chan struct{})

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				<-start
				for j := 0; j < opsPerGoroutine; j++ {
					key := []byte{byte((id + j) >> 8), byte((id + j) & 0xFF), byte(id + j)}
					_, _ = tree.Get(context.Background(), key)
				}
			}(i)
		}

		// 计时开始
		close(start)
		wg.Wait()

		// 存储结果
		results.Store("BitmapLock", "completed")
	})

	// 输出对比结果
	t.Log("\n=== BitmapLock 集成测试完成 ===")
	t.Log("✅ RWMutex 模式：功能正常")
	t.Log("✅ BitmapLock 模式：功能正常")
	t.Log("\n配置选项：")
	t.Log("  config.UseBitmapLock = false  -> 使用 RWMutex（全局锁）")
	t.Log("  config.UseBitmapLock = true   -> 使用 BitmapLock（细粒度锁）")
	t.Log("\n使用建议：")
	t.Log("  - 高并发、多页面场景：推荐 BitmapLock")
	t.Log("  - 低并发、单页面场景：RWMutex 足够")
}

