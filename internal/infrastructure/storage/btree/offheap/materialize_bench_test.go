// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"testing"
)

// 基准测试：零拷贝物化（小数据集）
func BenchmarkOffHeapMaterializer_MaterializeSmall(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)

	// 准备测试数据（10 个条目）
	keys := make([][]byte, 10)
	values := make([][]byte, 10)
	for i := range keys {
		keys[i] = []byte{byte(i)}
		values[i] = make([]byte, 30)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pageID, _ := pm.Alloc()
		_, _ = m.MaterializePageFromBytes(pageID, keys, values)
		pm.Free(pageID)
	}
}

// 基准测试：零拷贝物化（中等数据集）
func BenchmarkOffHeapMaterializer_MaterializeMedium(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)

	// 准备测试数据（50 个条目）
	keys := make([][]byte, 50)
	values := make([][]byte, 50)
	for i := range keys {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		values[i] = make([]byte, 30)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pageID, _ := pm.Alloc()
		_, _ = m.MaterializePageFromBytes(pageID, keys, values)
		pm.Free(pageID)
	}
}

// 基准测试：页面验证
func BenchmarkOffHeapMaterializer_VerifyPage(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)

	// 准备测试数据
	keys := make([][]byte, 50)
	values := make([][]byte, 50)
	for i := range keys {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		values[i] = make([]byte, 30)
	}

	pageID, _ := pm.Alloc()
	_, _ = m.MaterializePageFromBytes(pageID, keys, values)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.VerifyPage(pageID, keys)
	}
}

// 基准测试：二分查找
func BenchmarkOffHeapMaterializer_BinarySearch(b *testing.B) {
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
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		values[i] = make([]byte, 30)
	}

	pageID, _ := pm.Alloc()
	_, _ = m.MaterializePageFromBytes(pageID, keys, values)

	// 搜索中间的 key
	searchKey := keys[50]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = m.BinarySearchInPage(pageID, searchKey)
	}
}

// 基准测试：获取页面快照
func BenchmarkOffHeapMaterializer_GetSnapshot(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)

	// 准备测试数据
	keys := make([][]byte, 50)
	values := make([][]byte, 50)
	for i := range keys {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		values[i] = make([]byte, 30)
	}

	pageID, _ := pm.Alloc()
	_, _ = m.MaterializePageFromBytes(pageID, keys, values)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.GetPageSnapshot(pageID)
	}
}

// 对比基准：传统深拷贝方式
func BenchmarkTraditionalDeepCopy(b *testing.B) {
	// 模拟传统的深拷贝物化
	keys := make([][]byte, 50)
	values := make([][]byte, 50)
	for i := range keys {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		values[i] = make([]byte, 30)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 深拷贝 keys
		newKeys := make([][]byte, len(keys))
		copy(newKeys, keys)

		// 深拷贝 values
		newValues := make([][]byte, len(values))
		for j, v := range values {
			newValues[j] = append([]byte(nil), v...)
		}

		// 防止优化掉
		_ = newKeys
		_ = newValues
	}
}
