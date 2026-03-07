package bftree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBfTree_BitmapLockIntegration 测试 BitmapLock 集成
func TestBfTree_BitmapLockIntegration(t *testing.T) {
	tests := []struct {
		name          string
		useBitmapLock bool
	}{
		{
			name:          "RWMutex mode",
			useBitmapLock: false,
		},
		{
			name:          "BitmapLock mode",
			useBitmapLock: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultConfig()
			config.UseBitmapLock = tt.useBitmapLock
			config.EnableWAL = false
			config.DataDir = t.TempDir() // 禁用 WAL 简化测试
			config.DataDir = t.TempDir() // 设置临时目录

			tree, err := NewBfTree(config)
			assert.NoError(t, err)
			assert.NotNil(t, tree)
			assert.Equal(t, tt.useBitmapLock, tree.useBitmapLock)

			// 如果启用 BitmapLock，检查是否正确初始化
			if tt.useBitmapLock {
				assert.NotNil(t, tree.bitmapLock)
			} else {
				assert.Nil(t, tree.bitmapLock)
			}

			// 清理
			_ = tree.Close()
		})
	}
}

// TestBfTree_LockHelperMethods 测试锁辅助方法
func TestBfTree_LockHelperMethods(t *testing.T) {
	tests := []struct {
		name          string
		useBitmapLock bool
	}{
		{name: "RWMutex mode", useBitmapLock: false},
		{name: "BitmapLock mode", useBitmapLock: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultConfig()
			config.UseBitmapLock = tt.useBitmapLock
			config.EnableWAL = false
			config.DataDir = t.TempDir()
			config.DataDir = t.TempDir()

			tree, err := NewBfTree(config)
			assert.NoError(t, err)
			defer tree.Close()

			pageID := uint64(100)

			// 测试写锁
			tree.lockPage(pageID)
			tree.unlockPage(pageID)

			// 测试读锁
			tree.rlockPage(pageID)
			tree.runlockPage(pageID)

			// 测试多次锁定/解锁
			for i := 0; i < 10; i++ {
				tree.lockPage(pageID)
				tree.unlockPage(pageID)

				tree.rlockPage(pageID)
				tree.rlockPage(pageID)
				tree.runlockPage(pageID)
				tree.runlockPage(pageID)
			}
		})
	}
}

// TestBfTree_BitmapLockWithBasicOperations 测试 BitmapLock 下的基本操作
func TestBfTree_BitmapLockWithBasicOperations(t *testing.T) {
	tests := []struct {
		name          string
		useBitmapLock bool
	}{
		{name: "RWMutex mode", useBitmapLock: false},
		{name: "BitmapLock mode", useBitmapLock: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultConfig()
			config.UseBitmapLock = tt.useBitmapLock
			config.EnableWAL = false
			config.DataDir = t.TempDir()

			tree, err := NewBfTree(config)
			assert.NoError(t, err)
			defer tree.Close()

			// 测试基本的 Set/Get 操作
			err = tree.Set(context.Background(), []byte("key1"), []byte("value1"))
			assert.NoError(t, err)

			value, err := tree.Get(context.Background(), []byte("key1"))
			assert.NoError(t, err)
			assert.Equal(t, []byte("value1"), value)

			// 测试多个键
			for i := 0; i < 100; i++ {
				key := []byte{byte(i)}
				value := []byte{byte(i + 100)}
				err := tree.Set(context.Background(), key, value)
				assert.NoError(t, err)
			}

			// 验证数据
			for i := 0; i < 100; i++ {
				key := []byte{byte(i)}
				expectedValue := []byte{byte(i + 100)}
				value, err := tree.Get(context.Background(), key)
				assert.NoError(t, err)
				assert.Equal(t, expectedValue, value)
			}

			// 测试删除
			err = tree.Delete(context.Background(), []byte("key1"))
			assert.NoError(t, err)

			_, err = tree.Get(context.Background(), []byte("key1"))
			assert.Error(t, err)
		})
	}
}

// TestBfTree_BitmapLockConcurrency 测试 BitmapLock 并发性能
func TestBfTree_BitmapLockConcurrency(t *testing.T) {
	tests := []struct {
		name          string
		useBitmapLock bool
	}{
		{name: "RWMutex mode", useBitmapLock: false},
		{name: "BitmapLock mode", useBitmapLock: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultConfig()
			config.UseBitmapLock = tt.useBitmapLock
			config.EnableWAL = false
			config.DataDir = t.TempDir()

			tree, err := NewBfTree(config)
			assert.NoError(t, err)
			defer tree.Close()

			// 并发写入测试
			const goroutines = 100
			const operations = 100

			// 先写入数据
			for i := 0; i < goroutines; i++ {
				key := []byte{byte(i)}
				value := make([]byte, 100)
				err := tree.Set(context.Background(), key, value)
				assert.NoError(t, err)
			}

			// 并发读取测试
			done := make(chan bool, goroutines)
			for i := 0; i < goroutines; i++ {
				go func(id int) {
					for j := 0; j < operations; j++ {
						key := []byte{byte(id)}
						_, _ = tree.Get(context.Background(), key)
					}
					done <- true
				}(i)
			}

			// 等待所有 goroutine 完成
			for i := 0; i < goroutines; i++ {
				<-done
			}
		})
	}
}

// BenchmarkBfTree_RWMutex_BasicOperations RWMutex 模式基准测试
func BenchmarkBfTree_RWMutex_BasicOperations(b *testing.B) {
	config := DefaultConfig()
	config.UseBitmapLock = false
	config.EnableWAL = false
	config.DataDir = b.TempDir()

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	// 预填充数据
	for i := 0; i < 1000; i++ {
		key := []byte{byte(i % 256), byte(i / 256)}
		value := make([]byte, 100)
		_ = tree.Set(context.Background(), key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte{byte(i % 256), byte(i / 256)}
		_, _ = tree.Get(context.Background(), key)
	}
}

// BenchmarkBfTree_BitmapLock_BasicOperations BitmapLock 模式基准测试
func BenchmarkBfTree_BitmapLock_BasicOperations(b *testing.B) {
	config := DefaultConfig()
	config.UseBitmapLock = true
	config.EnableWAL = false
	config.DataDir = b.TempDir()

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	// 预填充数据
	for i := 0; i < 1000; i++ {
		key := []byte{byte(i % 256), byte(i / 256)}
		value := make([]byte, 100)
		_ = tree.Set(context.Background(), key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte{byte(i % 256), byte(i / 256)}
		_, _ = tree.Get(context.Background(), key)
	}
}

// BenchmarkBfTree_RWMutex_ConcurrentReads RWMutex 并发读基准测试
func BenchmarkBfTree_RWMutex_ConcurrentReads(b *testing.B) {
	config := DefaultConfig()
	config.UseBitmapLock = false
	config.EnableWAL = false
	config.DataDir = b.TempDir()

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	// 预填充数据
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := make([]byte, 100)
		_ = tree.Set(context.Background(), key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte{byte(i % 100)}
			_, _ = tree.Get(context.Background(), key)
			i++
		}
	})
}

// BenchmarkBfTree_BitmapLock_ConcurrentReads BitmapLock 并发读基准测试
func BenchmarkBfTree_BitmapLock_ConcurrentReads(b *testing.B) {
	config := DefaultConfig()
	config.UseBitmapLock = true
	config.EnableWAL = false
	config.DataDir = b.TempDir()

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	// 预填充数据
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := make([]byte, 100)
		_ = tree.Set(context.Background(), key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte{byte(i % 100)}
			_, _ = tree.Get(context.Background(), key)
			i++
		}
	})
}
