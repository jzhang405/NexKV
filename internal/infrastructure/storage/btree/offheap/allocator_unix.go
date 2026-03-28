// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || freebsd

package offheap

import (
	"syscall"
	"unsafe"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

type mmapAllocator struct {
	base     unsafe.Pointer
	size     int
	pageSize int
}

func newPlatformAllocator(size int) (OffHeapAllocator, error) {
	ptr, err := syscall.Mmap(
		-1,
		0,
		size,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE,
	)
	if err != nil {
		return nil, errpkg.OffHeapMMapFailed(err)
	}

	return &mmapAllocator{
		base:     unsafe.Pointer(&ptr[0]),
		size:     size,
		pageSize: syscall.Getpagesize(),
	}, nil
}

func (m *mmapAllocator) Alloc(size int) (unsafe.Pointer, error) {
	if size > m.size {
		return nil, errpkg.OffHeapAllocExceedsSize(int64(size), int64(m.size))
	}
	return m.base, nil
}

func (m *mmapAllocator) Free(ptr unsafe.Pointer, size int) error {
	// 将 unsafe.Pointer 转换为 []byte 用于 Munmap
	b := (*[1 << 30]byte)(ptr)[:size:size]
	return syscall.Munmap(b)
}

func (m *mmapAllocator) Platform() string {
	return "unix"
}

func (m *mmapAllocator) PageSize() int {
	return m.pageSize
}
