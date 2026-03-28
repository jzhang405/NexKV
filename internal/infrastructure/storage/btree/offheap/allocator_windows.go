// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build windows

//nolint:staticcheck,unsafeptr // Windows 平台特定：VirtualAlloc 返回 uintptr，转换为 unsafe.Pointer 是安全的
package offheap

import (
	"unsafe"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"
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
	ptr, err := windows.VirtualAlloc(
		0,
		uintptr(size),
		MEM_RESERVE|MEM_COMMIT,
		windows.PAGE_READWRITE,
	)
	if err != nil {
		return nil, errpkg.OffHeapVirtualAllocFailed(err)
	}

	return &virtualAllocAllocator{
		//lint:ignore SA1030 we need this unsafe conversion for Windows API
		base:     unsafe.Pointer(ptr),
		size:     size,
		pageSize: PageSize,
	}, nil
}

func (v *virtualAllocAllocator) Alloc(size int) (unsafe.Pointer, error) {
	if size > v.size {
		return nil, errpkg.OffHeapAllocExceedsSize(int64(size), int64(v.size))
	}
	return v.base, nil
}

func (v *virtualAllocAllocator) Free(ptr unsafe.Pointer, size int) error {
	err := windows.VirtualFree(uintptr(ptr), 0, MEM_RELEASE)
	if err != nil {
		return errpkg.OffHeapVirtualFreeFailed(err)
	}
	return nil
}

func (v *virtualAllocAllocator) Platform() string {
	return "windows"
}

func (v *virtualAllocAllocator) PageSize() int {
	return v.pageSize
}
