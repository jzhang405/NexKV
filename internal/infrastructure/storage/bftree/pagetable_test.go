package bftree

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPageTable(t *testing.T) {
	pt := NewPageTable()

	assert.NotNil(t, pt)
	assert.NotNil(t, pt.pages)
	stats := pt.GetStats()
	assert.Equal(t, int64(0), stats.TotalAllocated)
	assert.Equal(t, int64(0), stats.TotalFreed)
	assert.Equal(t, int64(0), stats.CurrentCount)
}

func TestPageTable_Alloc(t *testing.T) {
	pt := NewPageTable()

	// 分配第一个页面
	pageID1, err := pt.Alloc(PageTypeLeaf, L1)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), pageID1)

	// 验证页面已创建
	entry, found := pt.Get(pageID1)
	assert.True(t, found)
	assert.NotNil(t, entry)
	assert.Equal(t, pageID1, entry.pageID)
	assert.Equal(t, PageTypeLeaf, entry.pageType)
	assert.Equal(t, L1, entry.level)
	assert.Equal(t, int32(1), entry.refCount)

	// 分配第二个页面
	_, err = pt.Alloc(PageTypeInner, L2)
	require.NoError(t, err)

	// 验证统计
	stats := pt.GetStats()
	assert.Equal(t, int64(2), stats.TotalAllocated)
	assert.Equal(t, int64(2), stats.CurrentCount)
}

func TestPageTable_Alloc_IncrementingID(t *testing.T) {
	pt := NewPageTable()

	// 分配多个页面，验证 ID 递增
	for i := 1; i <= 10; i++ {
		pageID, err := pt.Alloc(PageTypeLeaf, L1)
		require.NoError(t, err)
		assert.Equal(t, uint64(i), pageID)
	}
}

func TestPageTable_Free(t *testing.T) {
	pt := NewPageTable()

	// 分配页面
	pageID, err := pt.Alloc(PageTypeLeaf, L1)
	require.NoError(t, err)

	// 验证页面存在
	_, found := pt.Get(pageID)
	assert.True(t, found)

	// Unref 引用（Alloc 时 refCount = 1）
	err = pt.Unref(pageID)
	require.NoError(t, err)

	// 释放页面
	err = pt.Free(pageID)
	require.NoError(t, err)

	// 验证页面已删除
	_, found = pt.Get(pageID)
	assert.False(t, found)

	// 验证统计
	stats := pt.GetStats()
	assert.Equal(t, int64(1), stats.TotalAllocated)
	assert.Equal(t, int64(1), stats.TotalFreed)
	assert.Equal(t, int64(0), stats.CurrentCount)
}

func TestPageTable_Free_NotFound(t *testing.T) {
	pt := NewPageTable()

	// 释放不存在的页面
	err := pt.Free(999)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPageNotFound)
}

func TestPageTable_Free_StillReferenced(t *testing.T) {
	pt := NewPageTable()

	// 分配页面
	pageID, err := pt.Alloc(PageTypeLeaf, L1)
	require.NoError(t, err)

	// 增加引用计数
	err = pt.Ref(pageID)
	require.NoError(t, err)

	// 验证引用计数为 2
	refCount, _ := pt.GetRefCount(pageID)
	assert.Equal(t, int32(2), refCount)

	// 尝试释放（应该失败，因为还有引用）
	err = pt.Free(pageID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPageStillReferenced)

	// 减少引用计数
	err = pt.Unref(pageID)
	require.NoError(t, err)

	// 验证引用计数为 1
	refCount, _ = pt.GetRefCount(pageID)
	assert.Equal(t, int32(1), refCount)

	// 再次释放（仍失败，因为 refCount = 1）
	err = pt.Free(pageID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPageStillReferenced)
}

func TestPageTable_Ref_Unref(t *testing.T) {
	pt := NewPageTable()

	// 分配页面
	pageID, err := pt.Alloc(PageTypeLeaf, L1)
	require.NoError(t, err)

	// 初始引用计数为 1
	refCount, _ := pt.GetRefCount(pageID)
	assert.Equal(t, int32(1), refCount)

	// 增加引用
	err = pt.Ref(pageID)
	require.NoError(t, err)
	refCount, _ = pt.GetRefCount(pageID)
	assert.Equal(t, int32(2), refCount)

	// 再次增加引用
	err = pt.Ref(pageID)
	require.NoError(t, err)
	refCount, _ = pt.GetRefCount(pageID)
	assert.Equal(t, int32(3), refCount)

	// 减少引用
	err = pt.Unref(pageID)
	require.NoError(t, err)
	refCount, _ = pt.GetRefCount(pageID)
	assert.Equal(t, int32(2), refCount)

	// 再减少两次（回到初始值 1）
	_ = pt.Unref(pageID)
	_ = pt.Unref(pageID)
	refCount, _ = pt.GetRefCount(pageID)
	assert.Equal(t, int32(0), refCount)
}

func TestPageTable_Ref_NotFound(t *testing.T) {
	pt := NewPageTable()

	// 引用不存在的页面
	err := pt.Ref(999)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPageNotFound)
}

func TestPageTable_Unref_NotFound(t *testing.T) {
	pt := NewPageTable()

	// 解除不存在的页面的引用
	err := pt.Unref(999)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPageNotFound)
}

func TestPageTable_Pin_Unpin(t *testing.T) {
	pt := NewPageTable()

	// 分配页面
	pageID, err := pt.Alloc(PageTypeLeaf, L1)
	require.NoError(t, err)

	// 固定页面
	err = pt.Pin(pageID)
	require.NoError(t, err)

	// 验证固定计数
	pinCount, _ := pt.GetPinCount(pageID)
	assert.Equal(t, int32(1), pinCount)

	// 再次固定
	err = pt.Pin(pageID)
	require.NoError(t, err)
	pinCount, _ = pt.GetPinCount(pageID)
	assert.Equal(t, int32(2), pinCount)

	// 解除固定
	err = pt.Unpin(pageID)
	require.NoError(t, err)
	pinCount, _ = pt.GetPinCount(pageID)
	assert.Equal(t, int32(1), pinCount)

	// 再次解除
	err = pt.Unpin(pageID)
	require.NoError(t, err)
	pinCount, _ = pt.GetPinCount(pageID)
	assert.Equal(t, int32(0), pinCount)
}

func TestPageTable_Pin_NotFound(t *testing.T) {
	pt := NewPageTable()

	// 固定不存在的页面
	err := pt.Pin(999)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPageNotFound)
}

func TestPageTable_Unpin_NotFound(t *testing.T) {
	pt := NewPageTable()

	// 解除不存在的页面的固定
	err := pt.Unpin(999)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPageNotFound)
}

func TestPageTable_ListPages(t *testing.T) {
	pt := NewPageTable()

	// 初始状态：空列表
	pageIDs := pt.ListPages()
	assert.Equal(t, 0, len(pageIDs))

	// 分配几个页面
	pageID1, _ := pt.Alloc(PageTypeLeaf, L1)
	pageID2, _ := pt.Alloc(PageTypeInner, L2)
	pageID3, _ := pt.Alloc(PageTypeLeaf, L3)

	// 列出所有页面
	pageIDs = pt.ListPages()
	assert.Equal(t, 3, len(pageIDs))
	assert.Contains(t, pageIDs, pageID1)
	assert.Contains(t, pageIDs, pageID2)
	assert.Contains(t, pageIDs, pageID3)
}

func TestPageTable_GetStats(t *testing.T) {
	pt := NewPageTable()

	// 初始统计
	stats := pt.GetStats()
	assert.Equal(t, int64(0), stats.TotalAllocated)
	assert.Equal(t, int64(0), stats.TotalFreed)
	assert.Equal(t, int64(0), stats.CurrentCount)

	// 分配页面
	pageID1, _ := pt.Alloc(PageTypeLeaf, L1)
	_, _ = pt.Alloc(PageTypeInner, L2)

	stats = pt.GetStats()
	assert.Equal(t, int64(2), stats.TotalAllocated)
	assert.Equal(t, int64(2), stats.CurrentCount)

	// 释放一个页面（需要先 Unref 到 0）
	_ = pt.Unref(pageID1) // refCount: 1 → 0
	err := pt.Free(pageID1)
	require.NoError(t, err)

	stats = pt.GetStats()
	assert.Equal(t, int64(2), stats.TotalAllocated)
	assert.Equal(t, int64(1), stats.TotalFreed)
	assert.Equal(t, int64(1), stats.CurrentCount)
}

func TestPageTable_ConcurrentAlloc(t *testing.T) {
	pt := NewPageTable()

	const goroutines = 10
	const pagesPerGoroutine = 10

	var wg sync.WaitGroup

	// 并发分配页面
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < pagesPerGoroutine; j++ {
				_, err := pt.Alloc(PageTypeLeaf, L1)
				require.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()

	// 验证统计
	stats := pt.GetStats()
	assert.Equal(t, int64(goroutines*pagesPerGoroutine), stats.TotalAllocated)
	assert.Equal(t, int64(goroutines*pagesPerGoroutine), stats.CurrentCount)
}

func TestPageTable_ConcurrentReadWrite(t *testing.T) {
	pt := NewPageTable()

	// 先分配一些页面
	var pageIDs []uint64
	for i := 0; i < 10; i++ {
		pageID, _ := pt.Alloc(PageTypeLeaf, L1)
		pageIDs = append(pageIDs, pageID)
	}

	const goroutines = 10
	var wg sync.WaitGroup

	// 并发读取
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, pageID := range pageIDs {
				entry, found := pt.Get(pageID)
				assert.True(t, found)
				assert.NotNil(t, entry)
			}
		}()
	}

	wg.Wait()
}

// TestPageTable_ErrorPaths_Coverage 提升 PageTable 错误路径覆盖率
func TestPageTable_ErrorPaths_Coverage(t *testing.T) {
	pt := NewPageTable()

	// Free 不存在的页面
	err := pt.Free(999)
	assert.Error(t, err)

	// Get 不存在的页面
	_, found := pt.Get(999)
	assert.False(t, found)

	// Unref 不存在的页面
	err = pt.Unref(999)
	assert.Error(t, err)

	// Ref 不存在的页面
	err = pt.Ref(999)
	assert.Error(t, err)
}
