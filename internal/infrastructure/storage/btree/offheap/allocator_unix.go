// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || freebsd

package offheap

import (
	"fmt"
	"syscall"
	"unsafe"
)

type mmapAllocator struct {
	base     uintptr
	size     int
	pageSize int
}

func newPlatformAllocator(size int) (OffHeapAllocator, error) {
	// 调用 mmap 系统调用
	ptr, err := syscall.Mmap(
		-1,                   // fd = -1 (匿名映射)
		0,                    // offset = 0
		size,                 // length
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE,
	)
	if err != nil {
		return nil, fmt.Errorf("mmap failed: %w", err)
	}

	return &mmapAllocator{
		base:     uintptr(unsafe.Pointer(&ptr[0])),
		size:     size,
		pageSize: syscall.Getpagesize(),
	}, nil
}

func (m *mmapAllocator) Alloc(size int) (uintptr, error) {
	// 简单实现：返回固定基地址
	// 实际 PageManager 会管理具体的页面分配
	if size > m.size {
		return 0, fmt.Errorf("alloc size %d exceeds allocator size %d", size, m.size)
	}
	return m.base, nil
}

func (m *mmapAllocator) Free(ptr uintptr, size int) error {
	// 将 uintptr 转换为 []byte 用于 Munmap
	b := (*[1 << 30]byte)(unsafe.Pointer(ptr))[:size:size]
	return syscall.Munmap(b)
}

func (m *mmapAllocator) Platform() string {
	return "unix"
}

func (m *mmapAllocator) PageSize() int {
	return m.pageSize
}
