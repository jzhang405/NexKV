// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

// OffHeapMaterializer 零拷贝物化器
// 直接在 mmap 页面中操作，避免 KV 数据的深拷贝
type OffHeapMaterializer struct {
	pm *PageManager
	pa *PageAccessor
}

// NewOffHeapMaterializer 创建零拷贝物化器
func NewOffHeapMaterializer(pm *PageManager) *OffHeapMaterializer {
	return &OffHeapMaterializer{
		pm: pm,
		pa: NewPageAccessor(pm),
	}
}

// MaterializePageFromBytes 从字节数组物化到 mmap 页面
func (m *OffHeapMaterializer) MaterializePageFromBytes(
	pageID uint32,
	keys, values [][]byte,
) (uint16, error) {
	m.pa.InitLeafPage(pageID, 0)
	dataEnd := uint16(0)

	// 使用索引排序，确保 keys 和 values 同步重排
	indices := make([]int, len(keys))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return bytes.Compare(keys[indices[i]], keys[indices[j]]) < 0
	})

	for i, idx := range indices {
		err := m.pa.InsertLeafEntry(pageID, i, keys[idx], values[idx], &dataEnd)
		if err != nil {
			return 0, errpkg.OffHeapInsertEntry(i, err)
		}
	}

	return dataEnd, nil
}

// MaterializeIndexPageFromBytes 物化索引页面
func (m *OffHeapMaterializer) MaterializeIndexPageFromBytes(
	pageID uint32,
	keys [][]byte,
	children []uint32,
) (uint16, error) {
	// 安全检查：修复自环的 children
	for i, child := range children {
		if child == pageID {
			// 打印调试信息
			fmt.Fprintf(os.Stderr, "[DEBUG] MaterializeIndexPageFromBytes: fixing self-loop child at index %d, pageID=%d\n", i, pageID)
			children[i] = 0 // 用 0 替换自环的 child
		}
	}

	m.pa.InitIndexPage(pageID, 0)
	dataEnd := uint16(0)

	for i := range keys {
		if i >= len(children) {
			// children 数量不足，返回错误
			return 0, fmt.Errorf("not enough children for keys: pageID=%d, keys=%d, children=%d", pageID, len(keys), len(children))
		}
		err := m.pa.InsertIndexEntry(pageID, i, keys[i], children[i], &dataEnd)
		if err != nil {
			return 0, err
		}
	}

	if len(children) > len(keys) {
		lastChild := children[len(keys)]
		if lastChild != 0 {
			childVersion := m.pa.GetVersionSafe(lastChild)
			header := m.pa.GetHeader(pageID)
			header.extraChild = EncodeChildWithVersion(lastChild, childVersion)
		} else {
			header := m.pa.GetHeader(pageID)
			header.extraChild = 0
		}
	}

	return dataEnd, nil
}

// VerifyPage 验证页面内容是否正确
// 返回 true 如果页面中的 keys 与期望的 keys 匹配
func (m *OffHeapMaterializer) VerifyPage(
	pageID uint32,
	expectedKeys [][]byte,
) bool {
	count := m.pa.GetCount(pageID)
	if uint16(len(expectedKeys)) != count {
		return false
	}

	for i := 0; i < int(count); i++ {
		entry := m.pa.GetLeafEntry(pageID, i)
		pageKey := m.pa.GetKey(pageID, entry.keyOff, entry.keyLen)

		if !bytes.Equal(pageKey, expectedKeys[i]) {
			return false
		}
	}

	return true
}

// GetPageSnapshot 获取页面快照（用于调试）
func (m *OffHeapMaterializer) GetPageSnapshot(pageID uint32) [][]byte {
	count := m.pa.GetCount(pageID)
	result := make([][]byte, 0, count)

	for i := 0; i < int(count); i++ {
		entry := m.pa.GetLeafEntry(pageID, i)
		key := m.pa.GetKey(pageID, entry.keyOff, entry.keyLen)
		result = append(result, key)
	}

	return result
}

// GetValueSnapshot 获取页面 values 快照
func (m *OffHeapMaterializer) GetValueSnapshot(pageID uint32) [][]byte {
	count := m.pa.GetCount(pageID)
	result := make([][]byte, 0, count)

	for i := 0; i < int(count); i++ {
		entry := m.pa.GetLeafEntry(pageID, i)
		value := m.pa.GetValue(pageID, entry.valOff, entry.valLen)
		result = append(result, value)
	}

	return result
}

// ClonePageToBytes 克隆页面到字节数组（用于测试对比）
func (m *OffHeapMaterializer) ClonePageToBytes(pageID uint32) ([][]byte, [][]byte) {
	keys := m.GetPageSnapshot(pageID)
	values := m.GetValueSnapshot(pageID)

	// 深拷贝
	copiedKeys := make([][]byte, len(keys))
	for i := range keys {
		copiedKeys[i] = append([]byte(nil), keys[i]...)
	}

	copiedValues := make([][]byte, len(values))
	for i := range values {
		copiedValues[i] = append([]byte(nil), values[i]...)
	}

	return copiedKeys, copiedValues
}

// BinarySearchInPage 在页面中二分查找 key
// 返回：(索引位置，是否找到，找到时的 value)
func (m *OffHeapMaterializer) BinarySearchInPage(
	pageID uint32,
	key []byte,
) (int, bool, []byte) {
	idx, found, _ := m.pa.SearchKey(pageID, key, true)

	if !found {
		return idx, false, nil
	}

	entry := m.pa.GetLeafEntry(pageID, idx)
	value := m.pa.GetValue(pageID, entry.valOff, entry.valLen)

	return idx, true, value
}

// EstimateSpaceUsage 估算页面空间使用情况
// 返回：(已用字节数，空闲字节数，Entry 数量)
func (m *OffHeapMaterializer) EstimateSpaceUsage(pageID uint32) (used, free int, entryCount int) {
	header := m.pa.GetHeader(pageID)
	entryCount = int(header.count)

	// 估算数据区使用量
	// 需要计算最后一个 Entry 的 offset
	if entryCount > 0 {
		lastEntry := m.pa.GetLeafEntry(pageID, entryCount-1)
		// 数据从页面尾部开始，offset 是从页面起始位置计算
		minOffset := min(lastEntry.keyOff, lastEntry.valOff)
		used = int(PageSize - minOffset)
	}

	free = PageSize - SizeofPageHeader - used
	return used, free, entryCount
}
