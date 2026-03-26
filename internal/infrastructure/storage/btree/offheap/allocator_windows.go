// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build windows

package offheap

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type virtualAllocAllocator struct {
	base     unsafe.Pointer
	size     int
	pageSize int
}

const (
	MEM_COMMIT     = 0x00001000
	MEM_RESERVE    = 0x00002000
	MEM_RELEASE    = 0x8000
	PAGE_READWRITE = 0x04
)

func newPlatformAllocator(size int) (OffHeapAllocator, error) {
	// 调用 VirtualAlloc
	ptr, err := windows.VirtualAlloc(
		0,             // 地址（0 表示系统选择）
		uintptr(size), // 大小
		MEM_RESERVE|MEM_COMMIT,
		windows.PAGE_READWRITE,
	)
	if err != nil {
		return nil, fmt.Errorf("VirtualAlloc failed: %w", err)
	}

	// VirtualAlloc 返回的指针指向 OS 管理的内存，不在 Go 堆上
	// 因此可以安全地从 uintptr 转换为 unsafe.Pointer 存储
	// 参考：https://pkg.go.dev/unsafe#Pointer
	// "It is valid both to convert a pointer to uintptr and back"
	return &virtualAllocAllocator{
		base:     unsafe.Pointer(ptr),
		size:     size,
		pageSize: 4096, // Windows 默认页面大小
	}, nil
}

func (v *virtualAllocAllocator) Alloc(size int) (unsafe.Pointer, error) {
	if size > v.size {
		return nil, fmt.Errorf("alloc size %d exceeds allocator size %d", size, v.size)
	}
	return v.base, nil
}

func (v *virtualAllocAllocator) Free(ptr unsafe.Pointer, size int) error {
	err := windows.VirtualFree(uintptr(ptr), 0, MEM_RELEASE)
	if err != nil {
		return fmt.Errorf("VirtualFree failed: %w", err)
	}
	return nil
}

func (v *virtualAllocAllocator) Platform() string {
	return "windows"
}

func (v *virtualAllocAllocator) PageSize() int {
	return v.pageSize
}
