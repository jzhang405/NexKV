// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// BenchmarkCopyPath_Shallow 基准测试 copyPathShallow（深拷贝）
func BenchmarkCopyPath_Shallow(b *testing.B) {
	config := &model.BTreeConfig{}
	tree, err := OpenBTree("", config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	// 初始化数据
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		tree.Set(ctx, key, value)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, path, err := tree.findLeafPage(ctx, []byte{byte(i % 100)})
		if err != nil {
			continue
		}
		_, _ = tree.copyPathShallow(path)
	}
}

// BenchmarkCopyPath_WithDelta 基准测试 copyPathWithDelta（零拷贝）
func BenchmarkCopyPath_WithDelta(b *testing.B) {
	config := &model.BTreeConfig{}
	tree, err := OpenBTree("", config)
	if err != nil {
		b.Fatal(err)
	}
	defer tree.Close()

	ctx := context.Background()

	// 初始化数据
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		tree.Set(ctx, key, value)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, path, err := tree.findLeafPage(ctx, []byte{byte(i % 100)})
		if err != nil {
			continue
		}
		_, _ = tree.copyPathWithDelta(path)
	}
}

// BenchmarkLeafPage_Clone_Deep 基准测试深拷贝 Clone
func BenchmarkLeafPage_Clone_Deep(b *testing.B) {
	page := NewLeafPage(1)
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		page.Insert(key, value)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = page.Clone()
	}
}

// BenchmarkLeafPage_CloneWithDelta 基准测试 Delta Chain Clone
func BenchmarkLeafPage_CloneWithDelta(b *testing.B) {
	page := NewLeafPage(1)
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		page.Insert(key, value)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = page.CloneWithDelta()
	}
}

// BenchmarkLeafPage_SequentialWrites_WithDelta 基准测试 Delta Chain 写入性能
func BenchmarkLeafPage_SequentialWrites_WithDelta(b *testing.B) {
	page := NewLeafPage(1)
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		page.Insert(key, value)
	}

	// 创建 Delta Chain 副本
	clonedPage := page.CloneWithDelta()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 每次写入不同的 key，触发增量追加
		key := []byte{byte(i % 100)}
		value := []byte("value")
		clonedPage.Insert(key, value)
	}
}
