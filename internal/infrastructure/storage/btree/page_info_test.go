package btree

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewPageInfo(t *testing.T) {
	info := NewPageInfo()

	assert.NotNil(t, info)
	assert.Equal(t, int64(0), info.pos)
	assert.Nil(t, info.page)
	assert.NotNil(t, info.pageLock)
	assert.False(t, info.isDirty)
	assert.False(t, info.isSplitted)
	assert.Equal(t, int32(0), info.metaVersion)
	assert.Equal(t, int32(PageSize), info.pageSize)
	assert.Greater(t, info.lastTime, int64(0))
	assert.Equal(t, int64(0), info.hits)
}

func TestPageInfo_GetSetPage(t *testing.T) {
	info := NewPageInfo()
	page := &Page{ID: 1}

	info.SetPage(page)
	assert.Equal(t, page, info.GetPage())
}

func TestPageInfo_GetSetPos(t *testing.T) {
	info := NewPageInfo()

	info.SetPos(12345)
	assert.Equal(t, int64(12345), info.GetPos())
}

func TestPageInfo_GetLock(t *testing.T) {
	info := NewPageInfo()

	lock := info.GetLock()
	assert.NotNil(t, lock)
	assert.Same(t, info.pageLock, lock)
}

func TestPageInfo_DirtyFlag(t *testing.T) {
	info := NewPageInfo()

	// 初始状态
	assert.False(t, info.IsDirty())

	// 标记为脏页
	info.MarkDirty()
	assert.True(t, info.IsDirty())

	// 清除脏页标记
	info.ClearDirty()
	assert.False(t, info.IsDirty())
}

func TestPageInfo_SplittedFlag(t *testing.T) {
	info := NewPageInfo()

	// 初始状态
	assert.False(t, info.IsSplitted())

	// 标记为已分裂
	info.MarkSplitted()
	assert.True(t, info.IsSplitted())
}

func TestPageInfo_Touch(t *testing.T) {
	info := NewPageInfo()
	oldTime := info.lastTime
	oldHits := info.hits

	// 等待确保时间戳不同
	time.Sleep(10 * time.Millisecond)
	info.Touch()

	assert.Greater(t, info.lastTime, oldTime)
	assert.Equal(t, oldHits+1, info.hits)
}

func TestPageInfo_GetHits(t *testing.T) {
	info := NewPageInfo()

	assert.Equal(t, int64(0), info.GetHits())

	info.Touch()
	assert.Equal(t, int64(1), info.GetHits())

	info.Touch()
	info.Touch()
	assert.Equal(t, int64(3), info.GetHits())
}

func TestPageInfo_GetLastTime(t *testing.T) {
	info := NewPageInfo()

	before := time.Now().UnixNano()
	info.Touch()
	after := time.Now().UnixNano()

	lastTime := info.GetLastTime()
	assert.GreaterOrEqual(t, lastTime, before)
	assert.LessOrEqual(t, lastTime, after)
}

func TestPageInfo_Clone(t *testing.T) {
	// 创建原始 PageInfo
	original := NewPageInfo()
	original.SetPos(12345)
	original.SetPage(&Page{ID: 1})
	original.MarkDirty()
	original.MarkSplitted()
	original.metaVersion = 5
	original.Touch()

	// 克隆
	cloned := original.Clone()

	// 验证字段复制
	assert.Equal(t, original.pos, cloned.pos)
	assert.Equal(t, original.page, cloned.page) // 浅拷贝 Page 指针
	assert.Equal(t, original.lastTime, cloned.lastTime)
	assert.Equal(t, original.hits, cloned.hits)
	assert.Equal(t, original.isDirty, cloned.isDirty)
	assert.Equal(t, original.isSplitted, cloned.isSplitted)
	assert.Equal(t, original.metaVersion, cloned.metaVersion)
	assert.Equal(t, original.pageSize, cloned.pageSize)

	// 验证锁是新的（不共享）
	assert.NotSame(t, original.pageLock, cloned.pageLock)

	// 验证独立修改
	cloned.SetPos(99999)
	assert.NotEqual(t, original.pos, cloned.pos)
}

func TestPageInfo_GetSetBuff(t *testing.T) {
	info := NewPageInfo()
	buff := []byte{1, 2, 3, 4, 5}

	info.SetBuff(buff)
	got := info.GetBuff()
	assert.Equal(t, buff, got)
	// 验证是同一个底层数组
	assert.Equal(t, cap(buff), cap(got))
}

func TestPageInfo_MetaVersion(t *testing.T) {
	info := NewPageInfo()

	assert.Equal(t, int32(0), info.GetMetaVersion())

	info.IncrementMetaVersion()
	assert.Equal(t, int32(1), info.GetMetaVersion())

	info.IncrementMetaVersion()
	info.IncrementMetaVersion()
	assert.Equal(t, int32(3), info.GetMetaVersion())
}

func TestPageInfo_GetPageSize(t *testing.T) {
	info := NewPageInfo()

	assert.Equal(t, int32(PageSize), info.GetPageSize())
}

func TestPageInfo_ConcurrentAccess(t *testing.T) {
	info := NewPageInfo()
	const goroutines = 100
	var wg sync.WaitGroup

	// 并发 Touch
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				info.Touch()
			}
		}()
	}

	wg.Wait()

	// 验证最终计数
	assert.Equal(t, int64(goroutines*100), info.GetHits())
}

func TestPageInfo_DirtyMarkingConcurrency(t *testing.T) {
	info := NewPageInfo()
	const goroutines = 50
	var wg sync.WaitGroup

	// 并发标记脏页
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				info.MarkDirty()
				info.ClearDirty()
			}
		}()
	}

	wg.Wait()

	// 最终状态不确定，但应该不会 panic
	assert.True(t, true)
}

// Benchmark PageInfo 操作性能
func BenchmarkPageInfo_NewPageInfo(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewPageInfo()
	}
}

func BenchmarkPageInfo_Touch(b *testing.B) {
	info := NewPageInfo()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		info.Touch()
	}
}

func BenchmarkPageInfo_Clone(b *testing.B) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = info.Clone()
	}
}

func BenchmarkPageInfo_GetSetPage(b *testing.B) {
	info := NewPageInfo()
	page := &Page{ID: 1}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		info.SetPage(page)
		_ = info.GetPage()
	}
}

func BenchmarkPageInfo_MarkDirty(b *testing.B) {
	info := NewPageInfo()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		info.MarkDirty()
		info.ClearDirty()
	}
}
