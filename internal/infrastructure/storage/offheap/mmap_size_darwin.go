// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build darwin

package offheap

import (
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// GetRecommendedMmapSize 根据系统物理内存计算推荐 mmap 大小
// ratio: 使用物理内存的比例（推荐 0.6）
// 最小 64MB，最大 6GB（受 MaxMmapSize 约束）
func GetRecommendedMmapSize(ratio float64) (int, error) {
	totalMem, err := getPhysicalMemoryDarwin()
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

// getPhysicalMemoryDarwin 通过 sysctl 获取 macOS 物理内存总量
func getPhysicalMemoryDarwin() (uint64, error) {
	val, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, errpkg.Wrapf(err, "sysctl hw.memsize")
	}
	return val, nil
}
