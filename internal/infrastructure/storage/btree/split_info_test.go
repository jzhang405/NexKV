// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package btree

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// TestSplitInfo_BasicFunctionality 测试 SplitInfo 基本功能
func TestSplitInfo_BasicFunctionality(t *testing.T) {
	// 创建 SplitInfo
	originalPageID := model.PageID(100)
	newPageRef := model.PageID(200)
	splitKey := []byte("key-0050")
	epoch := uint64(1)

	splitInfo := NewSplitInfo(originalPageID, newPageRef, splitKey, epoch)
	assert.NotNil(t, splitInfo)
	assert.Equal(t, originalPageID, splitInfo.OriginalPageID)
	assert.Equal(t, newPageRef, splitInfo.NewPageRef)
	assert.Equal(t, splitKey, splitInfo.SplitKey)
	assert.Equal(t, epoch, splitInfo.GetSplitEpoch())
}

// TestSplitInfo_IsRedirecting 测试 key 重定向判断
func TestSplitInfo_IsRedirecting(t *testing.T) {
	tests := []struct {
		name     string
		key      []byte
		splitKey []byte
		expected bool
	}{
		{
			name:     "key 小于分裂点，应在左页面",
			key:      []byte("key-0030"),
			splitKey: []byte("key-0050"),
			expected: false,
		},
		{
			name:     "key 等于分裂点，应在左页面",
			key:      []byte("key-0050"),
			splitKey: []byte("key-0050"),
			expected: false,
		},
		{
			name:     "key 大于分裂点，应在右页面",
			key:      []byte("key-0060"),
			splitKey: []byte("key-0050"),
			expected: true,
		},
		{
			name:     "空 SplitInfo 不重定向",
			key:      []byte("key-0060"),
			splitKey: nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var splitInfo *SplitInfo
			if tt.splitKey != nil {
				splitInfo = NewSplitInfo(100, 200, tt.splitKey, 1)
			}
			result := splitInfo.IsRedirecting(tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSplitInfo_GlobalMap 测试全局分裂信息映射
func TestSplitInfo_GlobalMap(t *testing.T) {
	// 清理：测试结束后删除
	defer DeleteSplitInfo(model.PageID(100))

	// 初始状态：没有分裂信息
	info, ok := GetSplitInfo(model.PageID(100))
	assert.Nil(t, info)
	assert.False(t, ok)

	// 设置分裂信息
	splitInfo := NewSplitInfo(100, 200, []byte("key-0050"), 1)
	SetSplitInfo(model.PageID(100), splitInfo)

	// 读取分裂信息
	info, ok = GetSplitInfo(model.PageID(100))
	assert.NotNil(t, info)
	assert.True(t, ok)
	assert.Equal(t, model.PageID(200), info.GetNewPageRef())

	// 删除分裂信息
	DeleteSplitInfo(model.PageID(100))

	// 再次检查：应该不存在
	info, ok = GetSplitInfo(model.PageID(100))
	assert.Nil(t, info)
	assert.False(t, ok)
}

// TestSplitInfoExtension_Basic 测试 SplitInfoExtension 基本操作
func TestSplitInfoExtension_Basic(t *testing.T) {
	ext := &SplitInfoExtension{}

	// 初始状态
	assert.Nil(t, ext.GetSplitInfo())
	assert.Equal(t, uint64(0), ext.GetSplitEpoch())

	// 设置 SplitInfo
	splitInfo := NewSplitInfo(100, 200, []byte("key-0050"), 1)
	ext.SetSplitInfo(splitInfo)
	ext.SetSplitEpoch(5)

	assert.NotNil(t, ext.GetSplitInfo())
	assert.Equal(t, uint64(5), ext.GetSplitEpoch())

	// 检查 epoch 变化
	assert.False(t, ext.IsSplitEpochChanged(5))
	assert.True(t, ext.IsSplitEpochChanged(3))

	// 清除 SplitInfo
	ext.SetSplitInfo(nil)
	assert.Nil(t, ext.GetSplitInfo())
}

// TestCompareKeys 测试 key 比较函数
func TestCompareKeys(t *testing.T) {
	tests := []struct {
		name     string
		a        []byte
		b        []byte
		expected int
	}{
		{
			name:     "a < b",
			a:        []byte("key-0030"),
			b:        []byte("key-0050"),
			expected: -1,
		},
		{
			name:     "a == b",
			a:        []byte("key-0050"),
			b:        []byte("key-0050"),
			expected: 0,
		},
		{
			name:     "a > b",
			a:        []byte("key-0060"),
			b:        []byte("key-0050"),
			expected: 1,
		},
		{
			name:     "a 短于 b",
			a:        []byte("key-5"),
			b:        []byte("key-50"),
			expected: -1,
		},
		{
			name:     "a 长于 b",
			a:        []byte("key-500"),
			b:        []byte("key-50"),
			expected: 1,
		},
		{
			name:     "空 key",
			a:        []byte{},
			b:        []byte("key-50"),
			expected: -1,
		},
		{
			name:     "都为空",
			a:        []byte{},
			b:        []byte{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compareKeys(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}
