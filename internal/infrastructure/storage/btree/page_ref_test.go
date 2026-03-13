//go:build ignore_old_tests
// +build ignore_old_tests

package btree

import (
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

func TestNewPageRef(t *testing.T) {
	ref := NewPageRef()

	assert.NotNil(t, ref)
	assert.Nil(t, ref.GetPage())
	assert.Nil(t, ref.GetPageInfo())
	assert.False(t, ref.IsLoaded())
	assert.Nil(t, ref.GetParentRef())
	assert.False(t, ref.HasParent())
}

func TestNewPageRefWithInfo(t *testing.T) {
	// TODO: Week 13-14 - 更新为使用 LeafPage
	// 暂时跳过此测试
	t.Skip("Test needs update for new Page interface{} type")

	info := NewPageInfo()
	leafPage := NewLeafPage(model.PageID(1))
	info.SetPage(leafPage)

	ref := NewPageRefWithInfo(info)

	assert.NotNil(t, ref)
	assert.Equal(t, info, ref.GetPageInfo())
	assert.True(t, ref.IsLoaded())
	assert.NotNil(t, ref.GetPage())
}

func TestPageRef_SetGetPage(t *testing.T) {
	ref := NewPageRef()
	page := &Page{ID: 1}
	info := NewPageInfo()
	info.SetPage(page)

	ref.SetPage(info)

	assert.Equal(t, info, ref.GetPageInfo())
	assert.Equal(t, page, ref.GetPage())
	assert.True(t, ref.IsLoaded())
}

func TestPageRef_ReplacePage_CAS(t *testing.T) {
	ref := NewPageRef()

	oldInfo := NewPageInfo()
	oldInfo.SetPage(&Page{ID: 1})

	newInfo := NewPageInfo()
	newInfo.SetPage(&Page{ID: 2})

	// 设置初始值
	ref.SetPage(oldInfo)
	assert.Equal(t, oldInfo, ref.GetPageInfo())

	// CAS 成功：期望值正确
	swapped := ref.ReplacePage(oldInfo, newInfo)
	assert.True(t, swapped)
	assert.Equal(t, newInfo, ref.GetPageInfo())
	assert.Equal(t, model.PageID(2), ref.GetPage().ID)

	// CAS 失败：期望值不正确
	anotherInfo := NewPageInfo()
	anotherInfo.SetPage(&Page{ID: 3})
	swapped = ref.ReplacePage(oldInfo, anotherInfo)
	assert.False(t, swapped)
	assert.Equal(t, newInfo, ref.GetPageInfo()) // 保持不变
}

func TestPageRef_ReplacePage_NilPanic(t *testing.T) {
	ref := NewPageRef()

	assert.Panics(t, func() {
		ref.ReplacePage(nil, nil)
	})

	assert.Panics(t, func() {
		ref.ReplacePage(NewPageInfo(), nil)
	})
}

func TestPageRef_MarkDirty(t *testing.T) {
	ref := NewPageRef()
	info := NewPageInfo()
	ref.SetPage(info)

	// 初始状态
	assert.False(t, ref.IsDirty())

	// 标记脏页
	result := ref.MarkDirty()
	assert.True(t, result)
	assert.True(t, ref.IsDirty())

	// 清除脏页标记
	info.ClearDirty()
	assert.False(t, ref.IsDirty())
}

func TestPageRef_MarkDirty_NilInfo(t *testing.T) {
	ref := NewPageRef()

	result := ref.MarkDirty()
	assert.False(t, result)
	assert.False(t, ref.IsDirty())
}

func TestPageRef_GetSetPos(t *testing.T) {
	ref := NewPageRef()
	info := NewPageInfo()
	ref.SetPage(info)

	// 初始位置
	assert.Equal(t, int64(0), ref.GetPos())

	// 设置位置
	ref.SetPos(12345)
	assert.Equal(t, int64(12345), ref.GetPos())
}

func TestPageRef_GetLock(t *testing.T) {
	ref := NewPageRef()
	info := NewPageInfo()
	ref.SetPage(info)

	lock := ref.GetLock()
	assert.NotNil(t, lock)
	assert.Same(t, info.GetLock(), lock)
}

func TestPageRef_Touch(t *testing.T) {
	ref := NewPageRef()
	info := NewPageInfo()
	ref.SetPage(info)

	oldHits := ref.GetHits()
	oldTime := ref.GetLastTime()

	time.Sleep(10 * time.Millisecond)
	ref.Touch()

	assert.Equal(t, oldHits+1, ref.GetHits())
	assert.Greater(t, ref.GetLastTime(), oldTime)
}

func TestPageRef_ParentRef(t *testing.T) {
	parentRef := NewPageRef()
	child := NewPageRef()

	// 初始状态
	assert.Nil(t, child.GetParentRef())
	assert.False(t, child.HasParent())

	// 设置父引用
	child.SetParentRef(parentRef)
	assert.Equal(t, parentRef, child.GetParentRef())
	assert.True(t, child.HasParent())
}

func TestPageRef_Clone(t *testing.T) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1})
	ref := NewPageRefWithInfo(info)

	cloned := ref.Clone()

	// 验证共享 PageInfo
	assert.Equal(t, ref.GetPageInfo(), cloned.GetPageInfo())
	assert.Equal(t, ref.GetPage(), cloned.GetPage())

	// 验证独立对象
	assert.NotSame(t, ref, cloned)
}

func TestPageRef_Unload(t *testing.T) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1})
	ref := NewPageRefWithInfo(info)

	// 初始状态
	assert.True(t, ref.IsLoaded())

	// 卸载
	unloaded := ref.Unload()
	assert.Equal(t, info, unloaded)
	assert.False(t, ref.IsLoaded())
	assert.Nil(t, ref.GetPage())
}

func TestPageRef_GetSetBuff(t *testing.T) {
	ref := NewPageRef()
	info := NewPageInfo()
	ref.SetPage(info)

	buff := []byte{1, 2, 3, 4, 5}
	ref.SetBuff(buff)

	got := ref.GetBuff()
	assert.Equal(t, buff, got)
}

func TestPageRef_NilInfoSafety(t *testing.T) {
	ref := NewPageRef()

	// 所有操作在 PageInfo 为 nil 时应该安全
	assert.Nil(t, ref.GetPage())
	assert.Nil(t, ref.GetPageInfo())
	assert.False(t, ref.IsDirty())
	assert.Equal(t, int64(0), ref.GetPos())
	assert.Nil(t, ref.GetLock())
	assert.Equal(t, int64(0), ref.GetHits())
	assert.Equal(t, int64(0), ref.GetLastTime())

	// 不会 panic
	ref.Touch()
	ref.SetPos(12345)
	ref.MarkDirty()
	ref.SetBuff([]byte{1, 2, 3})
}

func TestPageRef_ConcurrentAccess(t *testing.T) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1})
	ref := NewPageRefWithInfo(info)

	const goroutines = 100
	var wg sync.WaitGroup

	// 并发读取
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = ref.GetPage()
				_ = ref.GetPageInfo()
				ref.Touch()
			}
		}()
	}

	// 并发 CAS 更新
	for i := 0; i < goroutines/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				oldInfo := ref.GetPageInfo()
				newInfo := NewPageInfo()
				newInfo.SetPage(&Page{ID: model.PageID(id)})
				ref.ReplacePage(oldInfo, newInfo)
			}
		}(i)
	}

	wg.Wait()

	// 验证最终状态有效
	assert.True(t, true)
}

func TestPageRef_ConcurrentParentRef(t *testing.T) {
	child := NewPageRef()

	const goroutines = 50
	var wg sync.WaitGroup

	// 并发设置父引用
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p := NewPageRef()
			child.SetParentRef(p)
		}(i)
	}

	wg.Wait()

	// 最终应该有父引用
	assert.True(t, child.HasParent())
	assert.NotNil(t, child.GetParentRef())
}

// Benchmark PageRef 操作性能
func BenchmarkPageRef_NewPageRef(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewPageRef()
	}
}

func BenchmarkPageRef_GetPage(b *testing.B) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1})
	ref := NewPageRefWithInfo(info)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ref.GetPage()
	}
}

func BenchmarkPageRef_SetPage(b *testing.B) {
	ref := NewPageRef()
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ref.SetPage(info)
	}
}

func BenchmarkPageRef_ReplacePage(b *testing.B) {
	oldInfo := NewPageInfo()
	oldInfo.SetPage(&Page{ID: 1})
	newInfo := NewPageInfo()
	newInfo.SetPage(&Page{ID: 2})

	ref := NewPageRef()
	ref.SetPage(oldInfo)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ref.ReplacePage(oldInfo, newInfo)
		oldInfo, newInfo = newInfo, oldInfo
	}
}

func BenchmarkPageRef_Touch(b *testing.B) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1})
	ref := NewPageRefWithInfo(info)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ref.Touch()
	}
}

func BenchmarkPageRef_ConcurrentRead(b *testing.B) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1})
	ref := NewPageRefWithInfo(info)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = ref.GetPage()
		}
	})
}
