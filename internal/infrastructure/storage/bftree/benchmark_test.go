// Package bftree 性能基准测试 - P3-2
package bftree

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkCompact_SmallDeltaChain 测试小 Delta Chain 的 compact 性能
func BenchmarkCompact_SmallDeltaChain(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	// 插入少量数据，触发小 Delta Chain
	const numKeys = 10
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value := []byte(fmt.Sprintf("value-%03d", i))
		_ = tree.Set(ctx, key, value)
	}

	b.ResetTimer()

	// 通过插入更多数据触发 compact
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("bench-%d", i))
		value := []byte("benchmark-value")
		_ = tree.Set(ctx, key, value)
	}
}

// BenchmarkCompact_LargeDeltaChain 测试大 Delta Chain 的 compact 性能
func BenchmarkCompact_LargeDeltaChain(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 每次迭代都插入足够的数据触发 compact
		for j := 0; j < 20; j++ {
			key := []byte(fmt.Sprintf("bench-%d-%d", i, j))
			value := []byte("benchmark-value-padding")
			_ = tree.Set(ctx, key, value)
		}
	}
}

// BenchmarkSet_Sequential 测试顺序插入性能
func BenchmarkSet_Sequential(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("seq-key-%010d", i))
		value := []byte("sequential-value")
		_ = tree.Set(ctx, key, value)
	}
}

// BenchmarkSet_Random 测试随机插入性能
func BenchmarkSet_Random(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 使用素数步长来避免模式
		key := []byte(fmt.Sprintf("rand-key-%010d", i*17))
		value := []byte("random-value")
		_ = tree.Set(ctx, key, value)
	}
}

// BenchmarkGet_Existing 测试读取存在的键性能
func BenchmarkGet_Existing(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	// 预填充数据
	const numKeys = 1000
	keys := make([][]byte, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%06d", i))
		value := []byte(fmt.Sprintf("value-%06d", i))
		_ = tree.Set(ctx, keys[i], value)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := keys[i%numKeys]
		_, _ = tree.Get(ctx, key)
	}
}

// BenchmarkGet_NonExisting 测试读取不存在的键性能
func BenchmarkGet_NonExisting(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("nonexistent-%010d", i))
		_, _ = tree.Get(ctx, key)
	}
}

// BenchmarkDelete_Existing 测试删除存在的键性能
func BenchmarkDelete_Existing(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		config := DefaultConfig()
		config.DataDir = b.TempDir()
		config.EnableWAL = false

		tree, err := NewBfTree(config)
		if err != nil {
			b.Fatal(err)
		}
		defer tree.Close()

		ctx := context.Background()

		// 插入数据
		const numKeys = 100
		keys := make([][]byte, numKeys)
		for j := 0; j < numKeys; j++ {
			keys[j] = []byte(fmt.Sprintf("del-key-%03d", j))
			value := []byte(fmt.Sprintf("value-%03d", j))
			_ = tree.Set(ctx, keys[j], value)
		}

		b.StartTimer()

		// 删除所有键
		for j := 0; j < numKeys; j++ {
			_ = tree.Delete(ctx, keys[j])
		}
	}
}

// BenchmarkUpdate_Existing 测试更新存在的键性能
func BenchmarkUpdate_Existing(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	// 预填充数据
	const numKeys = 100
	keys := make([][]byte, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = []byte(fmt.Sprintf("upd-key-%03d", i))
		value := []byte(fmt.Sprintf("value-%03d", i))
		_ = tree.Set(ctx, keys[i], value)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := keys[i%numKeys]
		newValue := []byte(fmt.Sprintf("updated-%d", i))
		_ = tree.Update(ctx, key, newValue)
	}
}

// BenchmarkScan_All 测试全表扫描性能
func BenchmarkScan_All(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	// 预填充数据
	const numKeys = 100
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("scan-key-%03d", i))
		value := []byte(fmt.Sprintf("value-%03d", i))
		_ = tree.Set(ctx, key, value)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		iter := tree.Scan(ctx, nil, nil)
		count := 0
		for {
			valid, _, _, _ := iter.Next()
			if !valid {
				break
			}
			count++
		}
		// 验证扫描到所有数据
		if count != numKeys {
			b.Fatalf("expected %d keys, got %d", numKeys, count)
		}
	}
}

// BenchmarkScan_Range 测试范围扫描性能
func BenchmarkScan_Range(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	// 预填充数据
	const numKeys = 1000
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("range-key-%04d", i))
		value := []byte(fmt.Sprintf("value-%04d", i))
		_ = tree.Set(ctx, key, value)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		start := []byte("range-key-0200")
		end := []byte("range-key-0300")
		iter := tree.Scan(ctx, start, end)

		count := 0
		for {
			valid, _, _, _ := iter.Next()
			if !valid {
				break
			}
			count++
		}
		// 应该扫描到 100 个键 (0200-0299)
		if count != 100 {
			b.Fatalf("expected 100 keys, got %d", count)
		}
	}
}

// BenchmarkIterator_Order 测试迭代器顺序验证性能
func BenchmarkIterator_Order(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()

		// 插入有序键
		const numKeys = 100
		for j := 0; j < numKeys; j++ {
			key := []byte(fmt.Sprintf("iter-key-%03d", j))
			value := []byte(fmt.Sprintf("value-%03d", j))
			_ = tree.Set(ctx, key, value)
		}

		// 使用迭代器遍历
		b.StartTimer()
		iter := tree.Scan(ctx, nil, nil)
		prevKey := ""

		for {
			valid, key, _, _ := iter.Next()
			if !valid {
				break
			}
			keyStr := string(key)

			// 验证顺序
			if prevKey != "" && keyStr < prevKey {
				b.Fatalf("顺序错误: %s < %s", keyStr, prevKey)
			}
			prevKey = keyStr
		}
	}
}

// BenchmarkConcurrent_Reads 测试并发读性能
func BenchmarkConcurrent_Reads(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	// 预填充数据
	const numKeys = 100
	keys := make([][]byte, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = []byte(fmt.Sprintf("read-key-%03d", i))
		value := []byte(fmt.Sprintf("value-%03d", i))
		_ = tree.Set(ctx, keys[i], value)
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := keys[i%numKeys]
			_, _ = tree.Get(ctx, key)
			i++
		}
	})
}

// BenchmarkConcurrent_Writes 测试并发写性能
func BenchmarkConcurrent_Writes(b *testing.B) {
	config := DefaultConfig()
	config.DataDir = b.TempDir()
	config.EnableWAL = false

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		config := DefaultConfig()
		config.DataDir = b.TempDir()
		config.EnableWAL = false

		tree, err := NewBfTree(config)
		if err != nil {
			b.Fatal(err)
		}
		defer tree.Close()

		ctx := context.Background()

		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("conc-write-%d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			_ = tree.Set(ctx, key, value)
			i++
		}
	})
}
