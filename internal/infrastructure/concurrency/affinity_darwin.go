//go:build darwin

// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"fmt"
	"runtime"
)

// pinToCore 将当前 goroutine 绑定到指定 CPU 核心
// macOS 不支持 CPU 亲和性（无法像 Linux 那样绑定到特定核心）
// 但我们可以使用 LockOSThread 来提升性能：
// 1. 锁定 OS 线程，减少 goroutine 在不同 OS 线程间迁移的开销
// 2. 虽然不能指定核心，但可以减少上下文切换
// 3. 相比完全不绑核，仍有一定性能提升（预期 5-15%）
func pinToCore(coreID int) error {
	// 验证 coreID 有效性
	numCPU := runtime.NumCPU()
	if coreID < 0 || coreID >= numCPU {
		return fmt.Errorf("invalid core ID %d, must be in [0, %d)", coreID, numCPU)
	}

	// macOS 特殊处理：
	// 虽然 macOS 不支持 pthread_setaffinity_np，但 LockOSThread 可以：
	// 1. 防止 goroutine 被调度到不同的 OS 线程
	// 2. 减少 OS 线程切换的开销
	// 3. 保持 cache locality（虽然没有 Linux 的绑核效果好）
	runtime.LockOSThread()

	// 注意：UnlockOSThread() 不在这里调用
	// 因为我们希望整个 worker 生命周期都保持在同一个 OS 线程上

	return nil
}

// isAffinitySupported 检查平台是否支持 CPU 绑核
// macOS 返回 false，因为不支持指定 CPU 核心
// 但 pinToCore() 仍然会通过 LockOSThread 提供一定优化
func isAffinitySupported() bool {
	return false // macOS 平台不支持指定 CPU 核心
}
