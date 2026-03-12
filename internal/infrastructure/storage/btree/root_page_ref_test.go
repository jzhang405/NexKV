//go:build ignore_old_tests
// +build ignore_old_tests

package btree

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

func TestNewRootPageRef(t *testing.T) {
	ref := NewRootPageRef()

	assert.NotNil(t, ref)
	assert.NotNil(t, ref.PageRef)
	assert.Nil(t, ref.GetPage())
	assert.Nil(t, ref.GetRootPage())
	assert.Nil(t, ref.GetRootPageInfo())
}

func TestNewRootPageRefWithInfo(t *testing.T) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1, Type: model.InternalPage})

	ref := NewRootPageRefWithInfo(info)

	assert.NotNil(t, ref)
	assert.Equal(t, info, ref.GetRootPageInfo())
	assert.NotNil(t, ref.GetRootPage())
	assert.Equal(t, model.PageID(1), ref.GetRootPage().ID)
}

func TestRootPageRef_ReplacePage_CAS(t *testing.T) {
	ref := NewRootPageRef()

	oldInfo := NewPageInfo()
	oldInfo.SetPage(&Page{ID: 1, Type: model.InternalPage})

	newInfo := NewPageInfo()
	newInfo.SetPage(&Page{ID: 2, Type: model.InternalPage})

	// 设置初始值
	ref.SetPage(oldInfo)
	assert.Equal(t, oldInfo, ref.GetRootPageInfo())

	// CAS 成功：期望值正确
	swapped := ref.ReplacePage(oldInfo, newInfo)
	assert.True(t, swapped)
	assert.Equal(t, newInfo, ref.GetRootPageInfo())
	assert.Equal(t, model.PageID(2), ref.GetRootPage().ID)

	// CAS 失败：期望值不正确
	anotherInfo := NewPageInfo()
	anotherInfo.SetPage(&Page{ID: 3, Type: model.InternalPage})
	swapped = ref.ReplacePage(oldInfo, anotherInfo)
	assert.False(t, swapped)
	assert.Equal(t, newInfo, ref.GetRootPageInfo()) // 保持不变
}

func TestRootPageRef_ReplacePage_NilPanic(t *testing.T) {
	ref := NewRootPageRef()

	assert.Panics(t, func() {
		ref.ReplacePage(nil, nil)
	})

	assert.Panics(t, func() {
		ref.ReplacePage(NewPageInfo(), nil)
	})
}

func TestRootPageRef_ReplacePageWithContext(t *testing.T) {
	ref := NewRootPageRef()

	oldInfo := NewPageInfo()
	oldInfo.SetPage(&Page{ID: 1, Type: model.InternalPage})

	newInfo := NewPageInfo()
	newInfo.SetPage(&Page{ID: 2, Type: model.InternalPage})

	ref.SetPage(oldInfo)

	// 正常上下文
	ctx := context.Background()
	err := ref.ReplacePageWithContext(ctx, oldInfo, newInfo)
	assert.NoError(t, err)
	assert.Equal(t, newInfo, ref.GetRootPageInfo())

	// 已取消的上下文
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	err = ref.ReplacePageWithContext(cancelledCtx, newInfo, oldInfo)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestRootPageRef_ReplacePageWithContext_CASFailure(t *testing.T) {
	ref := NewRootPageRef()

	currentInfo := NewPageInfo()
	currentInfo.SetPage(&Page{ID: 1, Type: model.InternalPage})
	ref.SetPage(currentInfo)

	wrongInfo := NewPageInfo()
	newInfo := NewPageInfo()
	newInfo.SetPage(&Page{ID: 2, Type: model.InternalPage})

	ctx := context.Background()
	err := ref.ReplacePageWithContext(ctx, wrongInfo, newInfo)
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidState, err)
}

func TestRootPageRef_DelayedRelease(t *testing.T) {
	ref := NewRootPageRef()

	oldInfo := NewPageInfo()
	oldInfo.SetPage(&Page{ID: 1, Type: model.InternalPage})

	newInfo := NewPageInfo()
	newInfo.SetPage(&Page{ID: 2, Type: model.InternalPage})

	ref.SetPage(oldInfo)

	// 替换页面（会触发延迟释放）
	swapped := ref.ReplacePage(oldInfo, newInfo)
	assert.True(t, swapped)

	// 等待延迟释放完成
	time.Sleep(200 * time.Millisecond)

	// 验证新页面仍然有效
	assert.Equal(t, newInfo, ref.GetRootPageInfo())
	assert.Equal(t, model.PageID(2), ref.GetRootPage().ID)
}

func TestRootPageRef_UpdateChildrenParentRef(t *testing.T) {
	ref := NewRootPageRef()

	page := &Page{
		ID:   1,
		Type: model.InternalPage,
	}

	// Phase 1: 此方法为预留接口，不会 panic
	// 实际实现在后续 Phase
	ref.UpdateChildrenParentRef(page)

	// 验证不会发生 panic
	assert.True(t, true)
}

func TestRootPageRef_InheritedMethods(t *testing.T) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1, Type: model.InternalPage})
	ref := NewRootPageRefWithInfo(info)

	// 验证继承的 PageRef 方法可用
	assert.NotNil(t, ref.GetPage())
	assert.NotNil(t, ref.GetPageInfo())
	assert.True(t, ref.IsLoaded())
	assert.Equal(t, int64(0), ref.GetPos())
	assert.Nil(t, ref.GetParentRef())
	assert.False(t, ref.HasParent())
}

func TestRootPageRef_GetRootPage(t *testing.T) {
	info := NewPageInfo()
	page := &Page{ID: 1, Type: model.InternalPage}
	info.SetPage(page)

	ref := NewRootPageRefWithInfo(info)

	// GetRootPage 和 GetPage 应该返回相同的值
	rootPage := ref.GetRootPage()
	assert.Equal(t, page, rootPage)
	assert.Equal(t, ref.GetPage(), rootPage)
}

func TestRootPageRef_GetRootPageInfo(t *testing.T) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1, Type: model.InternalPage})

	ref := NewRootPageRefWithInfo(info)

	// GetRootPageInfo 和 GetPageInfo 应该返回相同的值
	rootInfo := ref.GetRootPageInfo()
	assert.Equal(t, info, rootInfo)
	assert.Equal(t, ref.GetPageInfo(), rootInfo)
}

func TestIsRootPageType(t *testing.T) {
	tests := []struct {
		name     string
		page     *Page
		expected bool
	}{
		{
			name:     "nil page",
			page:     nil,
			expected: false,
		},
		{
			name: "internal page",
			page: &Page{
				ID:   1,
				Type: model.InternalPage,
			},
			expected: true,
		},
		{
			name: "leaf page",
			page: &Page{
				ID:   2,
				Type: model.LeafPage,
			},
			expected: false,
		},
		{
			name: "meta page",
			page: &Page{
				ID:   3,
				Type: model.MetaPage,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRootPageType(tt.page)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRootPageRef_ConcurrentAccess(t *testing.T) {
	ref := NewRootPageRef()

	info := NewPageInfo()
	info.SetPage(&Page{ID: 1, Type: model.InternalPage})
	ref.SetPage(info)

	const goroutines = 50
	done := make(chan bool, goroutines)

	// 并发读取
	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- true }()
			for j := 0; j < 100; j++ {
				_ = ref.GetRootPage()
				_ = ref.GetRootPageInfo()
			}
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < goroutines; i++ {
		<-done
	}

	// 验证最终状态有效
	assert.NotNil(t, ref.GetRootPage())
}

// Benchmark RootPageRef 操作性能
func BenchmarkRootPageRef_NewRootPageRef(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewRootPageRef()
	}
}

func BenchmarkRootPageRef_GetRootPage(b *testing.B) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1, Type: model.InternalPage})
	ref := NewRootPageRefWithInfo(info)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ref.GetRootPage()
	}
}

func BenchmarkRootPageRef_ReplacePage(b *testing.B) {
	oldInfo := NewPageInfo()
	oldInfo.SetPage(&Page{ID: 1, Type: model.InternalPage})

	newInfo := NewPageInfo()
	newInfo.SetPage(&Page{ID: 2, Type: model.InternalPage})

	ref := NewRootPageRef()
	ref.SetPage(oldInfo)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ref.ReplacePage(oldInfo, newInfo)
		oldInfo, newInfo = newInfo, oldInfo
	}
}

func BenchmarkRootPageRef_ConcurrentRead(b *testing.B) {
	info := NewPageInfo()
	info.SetPage(&Page{ID: 1, Type: model.InternalPage})
	ref := NewRootPageRefWithInfo(info)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = ref.GetRootPage()
		}
	})
}
