// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"runtime"
)

// MemoryMonitor 内存监控
// 用于检测系统内存压力，触发自动物化
type MemoryMonitor struct {
	memoryThreshold float64 // 内存使用率阈值（0-1）
}

// NewMemoryMonitor 创建新的内存监控
// threshold: 内存使用率阈值（0-1），如 0.8 表示 80%
func NewMemoryMonitor(threshold float64) *MemoryMonitor {
	return &MemoryMonitor{
		memoryThreshold: threshold,
	}
}

// GetMemoryUsage 获取当前内存使用率
// 返回：内存使用率（0-1），如 0.65 表示 65%
func (m *MemoryMonitor) GetMemoryUsage() float64 {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// 返回堆内存使用率（已分配 / 系统内存）
	// 注意：这是简化版本，生产环境可能需要更精确的计算
	if memStats.Sys > 0 {
		return float64(memStats.Alloc) / float64(memStats.Sys)
	}
	return 0
}

// IsUnderPressure 检查是否处于内存压力状态
// 返回：true 表示内存使用率超过阈值
func (m *MemoryMonitor) IsUnderPressure() bool {
	return m.GetMemoryUsage() > m.memoryThreshold
}

// GetMemoryStats 获取详细内存统计（用于调试和监控）
func (m *MemoryMonitor) GetMemoryStats() runtime.MemStats {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	return memStats
}

// SetThreshold 更新内存压力阈值
func (m *MemoryMonitor) SetThreshold(threshold float64) {
	if threshold >= 0 && threshold <= 1 {
		m.memoryThreshold = threshold
	}
}

// GetThreshold 获取当前内存压力阈值
func (m *MemoryMonitor) GetThreshold() float64 {
	return m.memoryThreshold
}
