// Package bftree BitmapLock 性能对比测试
package bftree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// benchmarkHelper 创建用于基准测试的 BfTree
func benchmarkHelper(b *testing.B, useBitmapLock bool) *BfTree {
	config := DefaultConfig()
	config.UseBitmapLock = useBitmapLock
	config.BitmapLockShards = 16
	config.DataDir = b.TempDir()  // 设置临时目录

	tree, err := NewBfTree(config)
	require.NoError(b, err)
	require.NotNil(b, tree)
	return tree
}

// BenchmarkRWMutex_Get 基准测试 RWMutex Get 性能
func BenchmarkRWMutex_Get(b *testing.B) {
	tree := benchmarkHelper(b, false)
	defer tree.Close()

	// 预填充数据
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		key := []byte{byte(i % 256)}
		value := []byte("test-value")
		_ = tree.Set(ctx, key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte{byte(i % 1000)}
		_, _ = tree.Get(ctx, key)
	}
}

// BenchmarkBitmapLock_Get 基准测试 BitmapLock Get 性能
func BenchmarkBitmapLock_Get(b *testing.B) {
	tree := benchmarkHelper(b, true)
	defer tree.Close()

	// 预填充数据
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		key := []byte{byte(i % 256)}
		value := []byte("test-value")
		_ = tree.Set(ctx, key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte{byte(i % 1000)}
		_, _ = tree.Get(ctx, key)
	}
}

// BenchmarkRWMutex_Set 基准测试 RWMutex Set 性能
func BenchmarkRWMutex_Set(b *testing.B) {
	tree := benchmarkHelper(b, false)
	defer tree.Close()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte{byte(i % 256)}
		value := []byte("test-value")
		_ = tree.Set(ctx, key, value)
	}
}

// BenchmarkBitmapLock_Set 基准测试 BitmapLock Set 性能
func BenchmarkBitmapLock_Set(b *testing.B) {
	tree := benchmarkHelper(b, true)
	defer tree.Close()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte{byte(i % 256)}
		value := []byte("test-value")
		_ = tree.Set(ctx, key, value)
	}
}

// BenchmarkRWMutex_ConcurrentGet 并发读测试 - RWMutex
func BenchmarkRWMutex_ConcurrentGet(b *testing.B) {
	tree := benchmarkHelper(b, false)
	defer tree.Close()

	// 预填充数据
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		_ = tree.Set(ctx, key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte{byte(i % 100)}
			_, _ = tree.Get(ctx, key)
			i++
		}
	})
}

// BenchmarkBitmapLock_ConcurrentGet 并发读测试 - BitmapLock
func BenchmarkBitmapLock_ConcurrentGet(b *testing.B) {
	tree := benchmarkHelper(b, true)
	defer tree.Close()

	// 预填充数据
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		_ = tree.Set(ctx, key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte{byte(i % 100)}
			_, _ = tree.Get(ctx, key)
			i++
		}
	})
}
