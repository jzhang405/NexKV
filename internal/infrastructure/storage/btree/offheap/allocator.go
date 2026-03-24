// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"fmt"
	"unsafe"
)

// OffHeapAllocator 跨平台 Off-Heap 内存分配接口
type OffHeapAllocator interface {
	// Alloc 分配指定大小的 Off-Heap 内存
	Alloc(size int) (uintptr, error)

	// Free 释放 Off-Heap 内存
	Free(ptr uintptr, size int) error

	// Platform 返回支持的平台名称
	Platform() string

	// PageSize 返回平台内存页大小
	PageSize() int
}

// NewAllocator 创建当前平台的 Off-Heap 分配器
func NewAllocator(size int) (OffHeapAllocator, error) {
	if size <= 0 {
		return nil, fmt.Errorf("allocator size must be positive: %d", size)
	}

	return newPlatformAllocator(size)
}

// platformPageSize 返回当前平台的内存页大小
func platformPageSize() int {
	// 默认 4KB，各平台实现会覆盖
	return 4096
}

// uintptrToSlice 将 uintptr 转换为 byte slice
// 注意：slice 不会影响底层数据的生命周期
func uintptrToSlice(ptr uintptr, length int) []byte {
	if length == 0 {
		return []byte{}
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(ptr)), length)
}
