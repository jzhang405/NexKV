//go:build windows

// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"fmt"
	"runtime"
	"syscall"

	"github.com/jzhang405/NexKV/pkg/errors"
)

// Windows API 声明
var (
	modkernel32               = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadAffinityMask = modkernel32.NewProc("SetThreadAffinityMask")
	procGetCurrentThread      = modkernel32.NewProc("GetCurrentThread")
)

// pinToCore 将当前 goroutine 绑定到指定 CPU 核心
// 使用 Windows SetThreadAffinityMask API
// 注意：调用者（run() 方法）已经负责 LockOSThread/UnlockOSThread
func pinToCore(coreID int) error {
	// 验证 coreID 有效性
	numCPU := runtime.NumCPU()
	if coreID < 0 || coreID >= numCPU {
		return fmt.Errorf("invalid core ID %d, must be in [0, %d)", coreID, numCPU)
	}

	// 获取当前线程句柄
	threadHandle, _, _ := procGetCurrentThread.Call()
	if threadHandle == 0 {
		return errors.ErrCPUGetCurrentThread
	}

	// 创建 CPU 亲和性掩码
	// Windows 使用 64 位掩码，每个 bit 代表一个 CPU
	mask := uint64(1) << coreID

	// 调用 SetThreadAffinityMask
	// 注意：必须在 LockOSThread 的上下文中调用
	ret, _, errno := procSetThreadAffinityMask.Call(
		threadHandle,
		uintptr(mask),
	)

	if ret == 0 {
		return errors.WrapInt(errors.ErrCPUSetAffinityMask, "errno", int(errno))
	}

	return nil
}

// isAffinitySupported 检查平台是否支持 CPU 绑核
func isAffinitySupported() bool {
	return true // Windows 平台支持
}
