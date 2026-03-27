// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package offheap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInitPage_ClearsExtraChild 验证页面初始化清空 extraChild
func TestInitPage_ClearsExtraChild(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)

	// 1. 分配并初始化一个索引页面，设置 extraChild
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitIndexPage(pageID, 1)

	// 手动设置 extraChild
	header := pa.GetHeader(pageID)
	header.extraChild = 12345 // 设置一个非零值

	// 2. 释放页面
	err = pm.Free(pageID)
	require.NoError(t, err)

	// 3. 重新分配同一个页面ID
	newPageID, err := pm.Alloc()
	require.NoError(t, err)
	// 可能会重新分配到同一个页面ID（取决于 freeList 的实现）

	// 4. 初始化新页面
	pa.InitIndexPage(newPageID, 2)

	// 5. 验证 extraChild 被清空
	newHeader := pa.GetHeader(newPageID)
	assert.Equal(t, uint32(0), newHeader.extraChild,
		"extraChild should be 0 after InitPage")
}

// TestInitIndexPage_CreatesValidPage 验证索引页面初始化创建有效页面
func TestInitIndexPage_CreatesValidPage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)

	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitIndexPage(pageID, 1)

	header := pa.GetHeader(pageID)
	assert.Equal(t, uint8(PageTypeIndex), header.pageType)
	assert.Equal(t, uint16(0), header.count)
	assert.Equal(t, uint32(0), header.extraChild, "extraChild should be 0")
	assert.Equal(t, uint64(1), header.version)
}
