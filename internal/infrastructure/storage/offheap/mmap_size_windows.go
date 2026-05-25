// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build windows

package offheap

import (
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// GetRecommendedMmapSize 根据系统物理内存计算推荐 mmap 大小
// ratio: 使用物理内存的比例（推荐 0.6）
// 最小 64MB，最大 6GB（受 MaxMmapSize 约束）
func GetRecommendedMmapSize(ratio float64) (int, error) {
	totalMem, err := getPhysicalMemoryWindows()
	if err != nil {
		fmt.Fprintf(os.Stderr, "offheap: failed to get system memory info: %v, fallback to 64MB\n", err)
		return 64 << 20, nil
	}

	size := uint64(float64(totalMem) * ratio)

	const minSize = 64 << 20 // 64MB

	if size < minSize {
		size = minSize
	}
	if size > uint64(MaxMmapSize) {
		size = uint64(MaxMmapSize)
	}
	return int(size), nil
}

// getPhysicalMemoryWindows 通过 GlobalMemoryStatusEx 获取 Windows 物理内存总量
func getPhysicalMemoryWindows() (uint64, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

	// MEMORYSTATUSEX 结构体：64 字节
	// dwLength(4) + dwMemoryLoad(4) + ullTotalPhys(8) + ...
	var status [8]uint64 // 64 bytes
	status[0] = 64       // dwLength

	ret, _, _ := kernel32.Call(uintptr(unsafe.Pointer(&status[0])))
	if ret == 0 {
		return 0, errpkg.Wrapf(ErrOffHeapSystemError, "GlobalMemoryStatusEx failed")
	}

	// ullTotalPhys 位于 offset 16（dwLength[0:4] + dwMemoryLoad[4:8] + ullTotalPhys[8:16]）
	// 在 [8]uint64 表示中，index 1 对应 offset 8
	totalPhys := status[1]
	return totalPhys, nil
}
