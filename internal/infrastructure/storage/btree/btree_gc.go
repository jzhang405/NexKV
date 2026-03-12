package btree

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// GC 类型
const (
	GCTypeFull = 0 // 完全释放（page + buff）
	GCTypePage = 1 // 仅释放 page 对象
	GCTypeBuff = 2 // 仅释放 buff 缓存
)

// BTreeGC 渐进式垃圾回收
type BTreeGC struct {
	chunkManager *ChunkManager

	// 内存管理
	maxMemory     int64        // 内存上限 (64MB)
	lowWaterMark  int64        // 低水位 (70% = 44.8MB)
	highWaterMark int64        // 高水位 (90% = 57.6MB)
	usedMemory    atomic.Int64 // 已使用内存

	// 分层 GC 策略
	pageEvictionRate   float64 // 页面淘汰率 (0.1)
	bufferEvictionRate float64 // 缓冲区淘汰率 (0.3)

	// 智能触发
	memoryPressure   chan bool      // 内存压力信号
	adaptiveInterval atomic.Int64   // 自适应间隔 (纳秒)
	stopCh           chan struct{}  // 停止信号
	wg               sync.WaitGroup // 等待组

	// 统计
	stats GCStats
	mu    sync.Mutex // 保护 stats
}

// GCStats GC 统计信息
type GCStats struct {
	TotalGCs       int64         // 总 GC 次数
	PageReleases   int64         // 页面释放次数
	BufferReleases int64         // 缓冲区释放次数
	LastGCTime     time.Time     // 最后一次 GC 时间
	AvgGCDuration  time.Duration // 平均 GC 耗时
}

// NewBTreeGC 创建新的 BTreeGC
func NewBTreeGC(chunkManager *ChunkManager, maxMemory int64) *BTreeGC {
	gc := &BTreeGC{
		chunkManager:       chunkManager,
		maxMemory:          maxMemory,
		lowWaterMark:       int64(float64(maxMemory) * 0.7),
		highWaterMark:      int64(float64(maxMemory) * 0.9),
		pageEvictionRate:   0.1,
		bufferEvictionRate: 0.3,
		memoryPressure:     make(chan bool, 1),
		stopCh:             make(chan struct{}),
		adaptiveInterval:   atomic.Int64{},
	}

	// 初始间隔：1 秒
	gc.adaptiveInterval.Store(int64(time.Second))

	return gc
}

// Start 启动 GC
func (gc *BTreeGC) Start() {
	gc.wg.Add(1)
	go gc.run()
}

// Stop 停止 GC
func (gc *BTreeGC) Stop() {
	close(gc.stopCh)
	gc.wg.Wait()
}

// run GC 主循环
func (gc *BTreeGC) run() {
	defer gc.wg.Done()

	ticker := time.NewTicker(time.Duration(gc.adaptiveInterval.Load()))
	defer ticker.Stop()

	for {
		select {
		case <-gc.stopCh:
			return
		case <-gc.memoryPressure:
			// 内存压力触发立即 GC
			gc.collect()
		case <-ticker.C:
			// 定期 GC（如果需要）
			if gc.shouldGC() {
				gc.collect()
			}
		}
	}
}

// shouldGC 判断是否需要 GC
func (gc *BTreeGC) shouldGC() bool {
	used := gc.usedMemory.Load()
	return used >= gc.lowWaterMark
}

// collect 执行垃圾回收
func (gc *BTreeGC) collect() {
	startTime := time.Now()

	// 根据内存使用情况选择 GC 策略
	used := gc.usedMemory.Load()
	var gcType int

	if used >= gc.highWaterMark {
		// 高水位：完全释放
		gcType = GCTypeFull
	} else if used >= gc.lowWaterMark {
		// 低水位：仅释放 buff
		gcType = GCTypeBuff
	} else {
		// 正常：仅释放 page 对象
		gcType = GCTypePage
	}

	// 执行 GC
	gc.releasePages(gcType)

	// 更新统计
	duration := time.Since(startTime)
	gc.updateStats(duration)

	// 调整自适应间隔
	gc.adjustInterval(duration)
}

// releasePages 释放页面
func (gc *BTreeGC) releasePages(gcType int) {
	// TODO: 实现 LRU 淘汰逻辑
	// 这里需要访问 BTree 的页面缓存，根据 LRU 策略释放页面

	// 暂时实现：减少 usedMemory 计数
	switch gcType {
	case GCTypeFull:
		// 完全释放
		gc.usedMemory.Add(-gc.usedMemory.Load() / 10) // 释放 10%
	case GCTypePage:
		// 仅释放 page 对象
		gc.usedMemory.Add(-gc.usedMemory.Load() / 20) // 释放 5%
	case GCTypeBuff:
		// 仅释放 buff
		gc.usedMemory.Add(-gc.usedMemory.Load() / 15) // 释放约 6.7%
	}
}

// collectDirtyPages 收集脏页并写入
func (gc *BTreeGC) collectDirtyPages(dirtyPages map[*PageInfo]bool) error {
	if len(dirtyPages) == 0 {
		return nil
	}

	// TODO: 实现自底向上写入逻辑
	// 1. 按深度排序（叶子节点优先）
	// 2. 自底向上写入
	// 3. 更新父节点引用

	// 暂时实现：直接清除脏页标记
	for pageInfo := range dirtyPages {
		if pageInfo.IsDirty() {
			pageInfo.ClearDirty()
		}
	}

	return nil
}

// updateStats 更新统计信息
func (gc *BTreeGC) updateStats(duration time.Duration) {
	gc.mu.Lock()
	defer gc.mu.Unlock()

	gc.stats.TotalGCs++
	gc.stats.LastGCTime = time.Now()

	// 更新平均耗时
	if gc.stats.TotalGCs == 1 {
		gc.stats.AvgGCDuration = duration
	} else {
		// 指数移动平均
		alpha := 0.2
		gc.stats.AvgGCDuration = time.Duration(float64(gc.stats.AvgGCDuration)*(1-alpha) + float64(duration)*alpha)
	}
}

// adjustInterval 调整自适应间隔
func (gc *BTreeGC) adjustInterval(lastDuration time.Duration) {
	// 如果 GC 耗时较长，增加间隔
	// 如果 GC 耗时较短，减少间隔（更频繁地 GC）

	baseInterval := time.Second
	maxInterval := 5 * time.Minute
	minInterval := 1 * time.Second

	var newInterval time.Duration

	if lastDuration > 100*time.Millisecond {
		// GC 较慢，增加间隔
		newInterval = baseInterval * 2
	} else if lastDuration < 10*time.Millisecond {
		// GC 很快，减少间隔
		newInterval = baseInterval / 2
	} else {
		// 保持当前间隔
		newInterval = time.Duration(gc.adaptiveInterval.Load())
	}

	// 限制在 [minInterval, maxInterval] 范围内
	if newInterval < minInterval {
		newInterval = minInterval
	}
	if newInterval > maxInterval {
		newInterval = maxInterval
	}

	gc.adaptiveInterval.Store(int64(newInterval))
}

// NotifyMemoryPressure 通知内存压力
func (gc *BTreeGC) NotifyMemoryPressure() {
	select {
	case gc.memoryPressure <- true:
	default:
		// 已经有压力信号在处理
	}
}

// AllocateMemory 分配内存
func (gc *BTreeGC) AllocateMemory(size int64) error {
	// 检查是否超过内存上限
	newUsed := gc.usedMemory.Load() + size
	if newUsed > gc.maxMemory {
		// 尝试 GC
		gc.collect()

		// 再次检查
		newUsed = gc.usedMemory.Load() + size
		if newUsed > gc.maxMemory {
			return fmt.Errorf("out of memory: used=%d, requesting=%d, limit=%d",
				gc.usedMemory.Load(), size, gc.maxMemory)
		}
	}

	gc.usedMemory.Add(size)
	return nil
}

// FreeMemory 释放内存
func (gc *BTreeGC) FreeMemory(size int64) {
	gc.usedMemory.Add(-size)
	if gc.usedMemory.Load() < 0 {
		gc.usedMemory.Store(0)
	}
}

// GetStats 获取统计信息
func (gc *BTreeGC) GetStats() GCStats {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	return gc.stats
}

// GetMemoryUsage 获取内存使用情况
func (gc *BTreeGC) GetMemoryUsage() (used, max int64) {
	return gc.usedMemory.Load(), gc.maxMemory
}
