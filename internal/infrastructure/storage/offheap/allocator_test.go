// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAllocator_Success(t *testing.T) {
	const size = 64 << 20 // 64MB
	alloc, err := NewAllocator(size)
	require.NoError(t, err)
	require.NotNil(t, alloc)
}

func TestNewAllocator_InvalidSize(t *testing.T) {
	_, err := NewAllocator(0)
	assert.Error(t, err, "size=0 should fail")

	_, err = NewAllocator(-1)
	assert.Error(t, err, "negative size should fail")
}

func TestAllocator_AllocFree(t *testing.T) {
	const size = 4 << 20 // 4MB
	alloc, err := NewAllocator(size)
	require.NoError(t, err)

	ptr, err := alloc.Alloc(size)
	require.NoError(t, err)
	assert.NotEqual(t, unsafe.Pointer(nil), ptr)

	// 写入数据验证内存可用
	slice := unsafe.Slice((*byte)(ptr), size)
	slice[0] = 0xAA
	slice[size-1] = 0xBB
	assert.Equal(t, byte(0xAA), slice[0])
	assert.Equal(t, byte(0xBB), slice[size-1])

	err = alloc.Free(ptr, size)
	assert.NoError(t, err)
}

func TestAllocator_AllocExceedsSize(t *testing.T) {
	const size = 4096
	alloc, err := NewAllocator(size)
	require.NoError(t, err)

	_, err = alloc.Alloc(size + 1)
	assert.Error(t, err, "allocating more than capacity should fail")
}

func TestAllocator_Platform(t *testing.T) {
	alloc, err := NewAllocator(4096)
	require.NoError(t, err)

	platform := alloc.Platform()
	assert.NotEmpty(t, platform)
}

func TestAllocator_PageSize(t *testing.T) {
	alloc, err := NewAllocator(4096)
	require.NoError(t, err)

	ps := alloc.PageSize()
	assert.Greater(t, ps, 0)
}
