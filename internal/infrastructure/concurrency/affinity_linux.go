//go:build linux

// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	// CPU_SET_SIZE 定义 CPU 掩码大小
	CPU_SET_SIZE = 1024
)

// cpuSet_t 表示 CPU 集合
type cpuSet_t [CPU_SET_SIZE / unsafe.Sizeof(uint64(0))]uint64

// CPU_SET 将 CPU 添加到集合
func CPU_SET(cpu int, set *cpuSet_t) {
	const uint64Size = 8
	idx := cpu / (uint64Size * int(unsafe.Sizeof(uint64(0))))
	bit := uint64(1) << (cpu % (uint64Size * int(unsafe.Sizeof(uint64(0)))))
	set[idx] |= bit
}

// CPU_ZERO 清空 CPU 集合
func CPU_ZERO(set *cpuSet_t) {
	for i := range set {
		set[i] = 0
	}
}

// pinToCore 将当前 goroutine 绑定到指定 CPU 核心
// 使用 Linux sched_setaffinity 系统调用
// 注意：调用者（run() 方法）已经负责 LockOSThread/UnlockOSThread
func pinToCore(coreID int) error {
	// 验证 coreID 有效性
	numCPU := runtime.NumCPU()
	if coreID < 0 || coreID >= numCPU {
		return fmt.Errorf("invalid core ID %d, must be in [0, %d)", coreID, numCPU)
	}

	// 创建 CPU 集合
	var set cpuSet_t
	CPU_ZERO(&set)
	CPU_SET(coreID, &set)

	// 调用 sched_setaffinity
	// 注意：必须在 LockOSThread 的上下文中调用
	_, _, errno := syscall.Syscall(
		syscall.SYS_SCHED_SETAFFINITY,
		uintptr(0),                    // pid (0 = 当前线程)
		uintptr(unsafe.Sizeof(set)),   // cpusetsize
		uintptr(unsafe.Pointer(&set)), // mask
	)

	if errno != 0 {
		return fmt.Errorf("sched_setaffinity failed: %v", errno)
	}

	return nil
}

// isAffinitySupported 检查平台是否支持 CPU 绑核
func isAffinitySupported() bool {
	return true // Linux 平台支持
}
