// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"testing"
)

// 基准测试：页面头部访问
func BenchmarkPageAccessor_GetHeader(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, _ := pm.Alloc()
	pa.InitLeafPage(pageID, 1)

	b.ResetTimer()
	for b.Loop() {
		_ = pa.GetHeader(pageID)
	}
}

// 基准测试：二分查找
func BenchmarkPageAccessor_SearchKey(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, _ := pm.Alloc()
	pa.InitLeafPage(pageID, 1)
	var dataEnd uint16 = 0

	// 插入 100 个条目
	for i := range 100 {
		key := []byte{byte(i >> 8), byte(i & 0xFF)}
		value := make([]byte, 30)
		_ = pa.InsertLeafEntry(pageID, i, key, value, &dataEnd)
	}

	// 搜索最后一个 key
	searchKey := []byte{99, 0}

	b.ResetTimer()
	for b.Loop() {
		_, _, _ = pa.SearchKey(pageID, searchKey, true)
	}
}

// 基准测试：插入叶子条目
func BenchmarkPageAccessor_InsertLeafEntry(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	pa := NewPageAccessor(pm)

	b.ResetTimer()
	for b.Loop() {
		// 每次使用新页面
		pageID, _ := pm.Alloc()
		pa.InitLeafPage(pageID, 1)
		var dataEnd uint16 = 0

		key := []byte("test_key")
		value := make([]byte, 30)
		_ = pa.InsertLeafEntry(pageID, 0, key, value, &dataEnd)

		pm.Free(pageID)
	}
}

// 基准测试：插入索引条目
func BenchmarkPageAccessor_InsertIndexEntry(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	pa := NewPageAccessor(pm)

	b.ResetTimer()
	for b.Loop() {
		// 每次使用新页面
		pageID, _ := pm.Alloc()
		pa.InitIndexPage(pageID, 1)
		var dataEnd uint16 = 0

		key := []byte("test_key")
		child := uint32(100)
		_ = pa.InsertIndexEntry(pageID, 0, key, child, &dataEnd)

		pm.Free(pageID)
	}
}

// 基准测试：获取 key（指针转换）
func BenchmarkPageAccessor_GetKey(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, _ := pm.Alloc()
	pa.InitLeafPage(pageID, 1)
	var dataEnd uint16 = 0

	key := []byte("test_key")
	value := make([]byte, 30)
	_ = pa.InsertLeafEntry(pageID, 0, key, value, &dataEnd)

	entry := pa.GetLeafEntry(pageID, 0)

	b.ResetTimer()
	for b.Loop() {
		_ = pa.GetKey(pageID, entry.keyOff, entry.keyLen)
	}
}

// 基准测试：获取 value（指针转换）
func BenchmarkPageAccessor_GetValue(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, _ := pm.Alloc()
	pa.InitLeafPage(pageID, 1)
	var dataEnd uint16 = 0

	key := []byte("test_key")
	value := make([]byte, 30)
	_ = pa.InsertLeafEntry(pageID, 0, key, value, &dataEnd)

	entry := pa.GetLeafEntry(pageID, 0)

	b.ResetTimer()
	for b.Loop() {
		_ = pa.GetValue(pageID, entry.valOff, entry.valLen)
	}
}

// 基准测试：版本号访问
func BenchmarkPageAccessor_Version(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, _ := pm.Alloc()
	pa.InitLeafPage(pageID, 42)

	b.ResetTimer()
	for b.Loop() {
		_ = pa.GetVersion(pageID)
	}
}
