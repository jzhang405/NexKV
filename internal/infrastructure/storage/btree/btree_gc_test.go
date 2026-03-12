package btree

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBTreeGC(t *testing.T) {
	cm, err := NewChunkManager(t.TempDir())
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 64*1024*1024) // 64MB

	assert.NotNil(t, gc)
	assert.Equal(t, int64(64*1024*1024), gc.maxMemory)
	// 检查水位线大约在正确范围（允许浮点误差）
	assert.InDelta(t, float64(64*1024*1024)*0.7, float64(gc.lowWaterMark), 1.0)
	assert.InDelta(t, float64(64*1024*1024)*0.9, float64(gc.highWaterMark), 1.0)
}

func TestBTreeGC_AllocateMemory(t *testing.T) {
	cm, err := NewChunkManager(t.TempDir())
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024) // 1KB

	// 分配内存
	err = gc.AllocateMemory(512)
	assert.NoError(t, err)

	used, _ := gc.GetMemoryUsage()
	assert.Equal(t, int64(512), used)

	// 分配超过限制
	err = gc.AllocateMemory(1024)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "out of memory")
}

func TestBTreeGC_FreeMemory(t *testing.T) {
	cm, err := NewChunkManager(t.TempDir())
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)

	// 分配然后释放
	err = gc.AllocateMemory(512)
	require.NoError(t, err)

	gc.FreeMemory(200)
	used, _ := gc.GetMemoryUsage()
	assert.Equal(t, int64(312), used)

	// 释放所有
	gc.FreeMemory(312)
	used, _ = gc.GetMemoryUsage()
	assert.Equal(t, int64(0), used)

	// 过度释放不会变成负数
	gc.FreeMemory(100)
	used, _ = gc.GetMemoryUsage()
	assert.Equal(t, int64(0), used)
}

func TestBTreeGC_ShouldGC(t *testing.T) {
	cm, err := NewChunkManager(t.TempDir())
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1000) // 1000 bytes
	assert.Equal(t, int64(700), gc.lowWaterMark)

	// 低于低水位
	gc.usedMemory.Store(500)
	assert.False(t, gc.shouldGC())

	// 达到低水位
	gc.usedMemory.Store(700)
	assert.True(t, gc.shouldGC())

	// 超过高水位
	gc.usedMemory.Store(900)
	assert.True(t, gc.shouldGC())
}

func TestBTreeGC_NotifyMemoryPressure(t *testing.T) {
	cm, err := NewChunkManager(t.TempDir())
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)
	gc.Start()
	defer gc.Stop()

	// 通知内存压力
	gc.NotifyMemoryPressure()

	// 等待 GC 执行
	time.Sleep(100 * time.Millisecond)

	// 验证 GC 至少执行了一次
	stats := gc.GetStats()
	assert.Greater(t, stats.TotalGCs, int64(0))
}

func TestBTreeGC_CollectDirtyPages(t *testing.T) {
	cm, err := NewChunkManager(t.TempDir())
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)

	// 创建脏页
	pageInfo1 := NewPageInfo()
	pageInfo1.MarkDirty()
	pageInfo2 := NewPageInfo()
	pageInfo2.MarkDirty()

	dirtyPages := map[*PageInfo]bool{
		pageInfo1: true,
		pageInfo2: true,
	}

	// 收集脏页
	err = gc.collectDirtyPages(dirtyPages)
	require.NoError(t, err)

	// 验证脏页标记被清除
	assert.False(t, pageInfo1.IsDirty())
	assert.False(t, pageInfo2.IsDirty())
}

func TestBTreeGC_GetStats(t *testing.T) {
	cm, err := NewChunkManager(t.TempDir())
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)

	stats := gc.GetStats()
	assert.Equal(t, int64(0), stats.TotalGCs)
	assert.Equal(t, int64(0), stats.PageReleases)
	assert.Equal(t, int64(0), stats.BufferReleases)
}

func TestBTreeGC_AdaptiveInterval(t *testing.T) {
	cm, err := NewChunkManager(t.TempDir())
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)

	// 初始间隔
	assert.Equal(t, int64(time.Second), gc.adaptiveInterval.Load())

	// 模拟快速 GC
	gc.adjustInterval(5 * time.Millisecond)
	newInterval := gc.adaptiveInterval.Load()
	assert.LessOrEqual(t, newInterval, int64(time.Second))

	// 模拟慢速 GC
	gc.adjustInterval(200 * time.Millisecond)
	newInterval = gc.adaptiveInterval.Load()
	assert.GreaterOrEqual(t, newInterval, int64(time.Second))
}

func TestBTreeGC_StartStop(t *testing.T) {
	cm, err := NewChunkManager(t.TempDir())
	require.NoError(t, err)

	gc := NewBTreeGC(cm, 1024)

	// 启动 GC
	gc.Start()

	// 等待一小段时间
	time.Sleep(50 * time.Millisecond)

	// 停止 GC
	gc.Stop()

	// 验证可以正常停止（没有死锁）
	assert.True(t, true)
}
