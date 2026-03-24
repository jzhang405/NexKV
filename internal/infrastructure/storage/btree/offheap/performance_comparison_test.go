// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"testing"
)

// ============================================================================
// 性能对比测试：Go 堆 vs Off-Heap mmap
// ============================================================================

// Benchmark_GoHeap_AllocMany KV 数据分配
func Benchmark_GoHeap_AllocMany(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// 模拟 Go 堆分配 100 个 KV 对
			keys := make([][]byte, 100)
			values := make([][]byte, 100)

			for i := 0; i < 100; i++ {
				keys[i] = []byte{byte(i), byte(i >> 8)}
				values[i] = make([]byte, 30)
			}

			// 防止优化
			_ = keys
			_ = values
		}
	})
}

// Benchmark_OffHeap_MaterializeMany Off-Heap 物化
func Benchmark_OffHeap_MaterializeMany(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)

	// 准备测试数据
	keys := make([][]byte, 100)
	values := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte{byte(i), byte(i >> 8)}
		values[i] = make([]byte, 30)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			pageID, _ := pm.Alloc()
			_ = m.MaterializePageFromBytes(pageID, keys, values)
			pm.Free(pageID)
		}
	})
}

// Benchmark_GoHeap_SearchMany Go 堆二分查找
func Benchmark_GoHeap_SearchMany(b *testing.B) {
	// 准备测试数据
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte{byte(i), byte(i >> 8)}
	}

	searchKey := keys[50]

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// 二分查找
			left, right := 0, len(keys)-1
			for left <= right {
				mid := left + (right-left)/2

				// 简化比较（实际应该用 bytes.Compare）
				cmp := compareKeys(keys[mid], searchKey)

				if cmp == 0 {
					break
				} else if cmp < 0 {
					right = mid - 1
				} else {
					left = mid + 1
				}
			}
		}
	})
}

// Benchmark_OffHeap_SearchMany Off-Heap 页面二分查找
func Benchmark_OffHeap_SearchMany(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)

	// 准备测试数据
	keys := make([][]byte, 100)
	values := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte{byte(i), byte(i >> 8)}
		values[i] = make([]byte, 30)
	}

	pageID, _ := pm.Alloc()
	_ = m.MaterializePageFromBytes(pageID, keys, values)
	searchKey := keys[50]

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _, _ = m.BinarySearchInPage(pageID, searchKey)
		}
	})

	pm.Free(pageID)
}

// ============================================================================
// 吞吐量对比测试
// ============================================================================

// Benchmark_GoHeap_Throughput Go 堆吞吐量
// 模拟：分配 100 个 KV + 100 次查找
func Benchmark_GoHeap_Throughput(b *testing.B) {
	// 准备搜索数据
	searchKeys := make([][]byte, 100)
	for i := range searchKeys {
		searchKeys[i] = []byte{byte(i), byte(i >> 8)}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// 分配 100 个 KV
			keys := make([][]byte, 100)
			values := make([][]byte, 100)

			for i := 0; i < 100; i++ {
				keys[i] = []byte{byte(i), byte(i >> 8)}
				values[i] = make([]byte, 30)
			}

			// 100 次查找
			for _, searchKey := range searchKeys {
				left, right := 0, len(keys)-1
				for left <= right {
					mid := left + (right-left)/2
					cmp := compareKeys(keys[mid], searchKey)

					if cmp == 0 {
						break
					} else if cmp < 0 {
						right = mid - 1
					} else {
						left = mid + 1
					}
				}
			}
		}
	})
}

// Benchmark_OffHeap_Throughput Off-Heap 吞吐量
// 模拟：物化 100 个 KV 到 mmap + 100 次查找
func Benchmark_OffHeap_Throughput(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)

	// 准备测试数据
	keys := make([][]byte, 100)
	values := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte{byte(i), byte(i >> 8)}
		values[i] = make([]byte, 30)
	}

	searchKeys := make([]byte, 0, 100*2)
	for _, k := range keys {
		searchKeys = append(searchKeys, k...)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			pageID, _ := pm.Alloc()

			// 物化 100 个 KV
			_ = m.MaterializePageFromBytes(pageID, keys, values)

			// 100 次查找
			for i := 0; i < 100; i++ {
				searchKey := searchKeys[i*2 : i*2+2]
				_, _, _ = m.BinarySearchInPage(pageID, searchKey)
			}

			pm.Free(pageID)
		}
	})
}

// ============================================================================
// 内存分配对比
// ============================================================================

// Benchmark_MemoryAllocation_GoHeap Go 堆内存分配
func Benchmark_MemoryAllocation_GoHeap(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 分配 100 个 KV 对
		keys := make([][]byte, 100)
		values := make([][]byte, 100)

		for j := 0; j < 100; j++ {
			keys[j] = []byte{byte(j), byte(j >> 8)}
			values[j] = make([]byte, 30)
		}

		// 防止优化
		_ = keys[0]
		_ = values[0]
	}
}

// Benchmark_MemoryAllocation_OffHeap Off-Heap 内存分配
func Benchmark_MemoryAllocation_OffHeap(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)

	keys := make([][]byte, 100)
	values := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte{byte(i), byte(i >> 8)}
		values[i] = make([]byte, 30)
	}

	b.ReportAllocs()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		pageID, _ := pm.Alloc()
		_ = m.MaterializePageFromBytes(pageID, keys, values)
		pm.Free(pageID)
	}
}

// ============================================================================
// 辅助函数
// ============================================================================

// compareKeys 比较两个 key（简化版本）
func compareKeys(a, b []byte) int {
	// 简化实现，实际应该用 bytes.Compare
	if len(a) != len(b) {
		return len(a) - len(b)
	}

	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return int(a[i]) - int(b[i])
		}
	}

	return 0
}
