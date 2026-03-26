// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package btree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// BenchmarkDelete_OffHeap Off-Heap Delete 性能测试
func BenchmarkDelete_OffHeap(b *testing.B) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer tree.Close()

	// 准备测试数据：插入大量 key
	const keyCount = 1000
	keys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		value := make([]byte, 10)
		err := tree.Set(ctx, keys[i], value)
		require.NoError(b, err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	// 循环删除所有 key
	for i := 0; i < b.N; i++ {
		keyIdx := i % keyCount
		// 如果 key 已被删除，重新插入
		_, err := tree.Get(ctx, keys[keyIdx])
		if err == ErrKeyNotFound {
			value := make([]byte, 10)
			tree.Set(ctx, keys[keyIdx], value)
		}

		tree.Delete(ctx, keys[keyIdx])
	}
}

// BenchmarkDelete_OffHeap_Sequential 顺序删除性能测试
func BenchmarkDelete_OffHeap_Sequential(b *testing.B) {
	ctx := context.Background()

	b.Run("1K_keys", func(b *testing.B) {
		benchmarkDeleteSequential(ctx, b, 1000)
	})

	b.Run("10K_keys", func(b *testing.B) {
		benchmarkDeleteSequential(ctx, b, 10000)
	})
}

func benchmarkDeleteSequential(ctx context.Context, b *testing.B, keyCount int) {
	tree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer tree.Close()

	// 准备测试数据
	keys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		value := make([]byte, 10)
		err := tree.Set(ctx, keys[i], value)
		require.NoError(b, err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	// 每次迭代删除所有 key，然后重新插入
	for i := 0; i < b.N; i++ {
		// 删除所有 key
		for j := 0; j < keyCount; j++ {
			err := tree.Delete(ctx, keys[j])
			if err != nil {
				b.Fatalf("删除失败: keyIdx=%d err=%v", j, err)
			}
		}

		// 重新插入所有 key（为下一次迭代准备）
		for j := 0; j < keyCount; j++ {
			value := make([]byte, 10)
			err := tree.Set(ctx, keys[j], value)
			if err != nil {
				b.Fatalf("插入失败: keyIdx=%d err=%v", j, err)
			}
		}
	}
}

// BenchmarkDelete_OffHeapVsOnHeap 对比 Off-Heap 和 On-Heap Delete 性能
func BenchmarkDelete_OffHeapVsOnHeap(b *testing.B) {
	b.Run("OffHeap", func(b *testing.B) {
		ctx := context.Background()
		tree, err := OpenBTree("", nil)
		require.NoError(b, err)
		defer tree.Close()

		// 准备测试数据
		const keyCount = 1000
		keys := make([][]byte, keyCount)
		for i := 0; i < keyCount; i++ {
			keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
			value := make([]byte, 10)
			err := tree.Set(ctx, keys[i], value)
			require.NoError(b, err)
		}

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			keyIdx := i % keyCount
			_, err := tree.Get(ctx, keys[keyIdx])
			if err == ErrKeyNotFound {
				value := make([]byte, 10)
				tree.Set(ctx, keys[keyIdx], value)
			}
			tree.Delete(ctx, keys[keyIdx])
		}
	})

	b.Run("OnHeap_Serial", func(b *testing.B) {
		// On-Heap 模式（串行删除）
		// 注意：由于当前实现默认使用 Off-Heap，这里只是预留对比接口
		// 如果要测试 On-Heap 性能，需要创建不使用 offheapPM 的 BTree
		b.Skip("On-Heap 模式对比测试待实现")
	})
}

// BenchmarkDelete_OffHeap_RandomKey 随机 key 删除性能测试
func BenchmarkDelete_OffHeap_RandomKey(b *testing.B) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer tree.Close()

	// 准备测试数据：插入大量 key
	const keyCount = 1000
	keys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		value := make([]byte, 10)
		err := tree.Set(ctx, keys[i], value)
		require.NoError(b, err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	// 随机删除不同的 key
	for i := 0; i < b.N; i++ {
		keyIdx := (i * 7) % keyCount // 使用质数来随机化访问模式
		_, err := tree.Get(ctx, keys[keyIdx])
		if err == ErrKeyNotFound {
			value := make([]byte, 10)
			tree.Set(ctx, keys[keyIdx], value)
		}
		tree.Delete(ctx, keys[keyIdx])
	}
}

// BenchmarkDelete_OffHeap_MergeTrigger 触发合并操作的 Delete 性能测试
func BenchmarkDelete_OffHeap_MergeTrigger(b *testing.B) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer tree.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 每次迭代：插入 100 个 key，然后删除 50 个（触发合并）
		const insertCount = 100
		const deleteCount = 50

		// 插入
		for j := 0; j < insertCount; j++ {
			key := []byte{byte(j >> 8), byte(j & 0xFF)}
			value := make([]byte, 10)
			tree.Set(ctx, key, value)
		}

		// 删除一半（触发可能的合并操作）
		for j := 0; j < deleteCount; j++ {
			key := []byte{byte(j >> 8), byte(j & 0xFF)}
			tree.Delete(ctx, key)
		}

		// 清理：删除剩余的 key（为下一次迭代准备）
		for j := deleteCount; j < insertCount; j++ {
			key := []byte{byte(j >> 8), byte(j & 0xFF)}
			tree.Delete(ctx, key)
		}
	}
}

// BenchmarkDelete_OffHeap_Patterns 不同删除模式性能测试
func BenchmarkDelete_OffHeap_Patterns(b *testing.B) {
	ctx := context.Background()

	b.Run("Sequential", func(b *testing.B) {
		benchmarkDeletePattern(ctx, b, 100, true)
	})

	b.Run("Reverse", func(b *testing.B) {
		benchmarkDeletePattern(ctx, b, 100, false)
	})
}

func benchmarkDeletePattern(ctx context.Context, b *testing.B, keyCount int, sequential bool) {
	tree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer tree.Close()

	// 准备测试数据
	keys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		value := make([]byte, 10)
		err := tree.Set(ctx, keys[i], value)
		require.NoError(b, err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 删除所有 key
		for j := 0; j < keyCount; j++ {
			var keyIdx int
			if sequential {
				keyIdx = j // 顺序删除
			} else {
				keyIdx = keyCount - 1 - j // 逆序删除
			}

			err := tree.Delete(ctx, keys[keyIdx])
			if err != nil {
				b.Fatalf("删除失败: keyIdx=%d err=%v", keyIdx, err)
			}
		}

		// 重新插入（为下一次迭代准备）
		for j := 0; j < keyCount; j++ {
			value := make([]byte, 10)
			err := tree.Set(ctx, keys[j], value)
			if err != nil {
				b.Fatalf("插入失败: keyIdx=%d err=%v", j, err)
			}
		}
	}
}
