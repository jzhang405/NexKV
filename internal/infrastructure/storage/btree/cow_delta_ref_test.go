// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCOWDeltaRef_Basic 测试基本的引用计数功能
func TestCOWDeltaRef_Basic(t *testing.T) {
	keys := [][]byte{[]byte("key1"), []byte("key2")}
	values := [][]byte{[]byte("val1"), []byte("val2")}

	ref := NewCOWDeltaRef(keys, values)

	// 初始引用计数应该是 1（创建者）
	assert.Equal(t, int32(1), ref.GetRefCount())

	// 初始增量链应该为空
	assert.Equal(t, 0, ref.GetDeltaCount())

	// Retain 应该增加引用计数
	ref.Retain()
	assert.Equal(t, int32(2), ref.GetRefCount())

	// Release 第一个引用
	last := ref.Release()
	assert.False(t, last) // 不是最后一个
	assert.Equal(t, int32(1), ref.GetRefCount())

	// Release 第二个引用
	last = ref.Release()
	assert.True(t, last) // 最后一个引用
}

// TestCOWDeltaRef_AppendDelta 测试增量链操作
func TestCOWDeltaRef_AppendDelta(t *testing.T) {
	keys := [][]byte{[]byte("key1"), []byte("key2")}
	values := [][]byte{[]byte("val1"), []byte("val2")}

	ref := NewCOWDeltaRef(keys, values)

	// 添加 Insert 增量
	ref.AppendDelta(Delta{op: DeltaInsert, key: []byte("key3"), value: []byte("val3")})
	assert.Equal(t, 1, ref.GetDeltaCount())

	// 添加 Update 增量
	ref.AppendDelta(Delta{op: DeltaUpdate, key: []byte("key1"), value: []byte("val1-updated")})
	assert.Equal(t, 2, ref.GetDeltaCount())

	// 添加 Delete 增量
	ref.AppendDelta(Delta{op: DeltaDelete, key: []byte("key2")})
	assert.Equal(t, 3, ref.GetDeltaCount())

	// 验证增量链内容
	deltas := ref.GetDeltas()
	assert.Equal(t, 3, len(deltas))
	assert.Equal(t, DeltaInsert, deltas[0].op)
	assert.Equal(t, []byte("key3"), deltas[0].key)
	assert.Equal(t, DeltaUpdate, deltas[1].op)
	assert.Equal(t, DeltaDelete, deltas[2].op)
}

// TestCOWDeltaRef_GetDeltasSnapshot 测试 GetDeltas 返回快照而非引用
func TestCOWDeltaRef_GetDeltasSnapshot(t *testing.T) {
	keys := [][]byte{[]byte("key1")}
	values := [][]byte{[]byte("val1")}

	ref := NewCOWDeltaRef(keys, values)

	// 获取增量快照
	ref.AppendDelta(Delta{op: DeltaInsert, key: []byte("key2"), value: []byte("val2")})
	deltas1 := ref.GetDeltas()

	// 修改快照不应影响原增量链
	deltas1[0] = Delta{op: DeltaDelete, key: []byte("key3")}

	// 获取新的快照，验证原增量链未变
	deltas2 := ref.GetDeltas()
	assert.Equal(t, 1, len(deltas2))
	assert.Equal(t, DeltaInsert, deltas2[0].op)
	assert.Equal(t, []byte("key2"), deltas2[0].key)
}

// TestCOWDeltaRef_ShouldMaterialize 测试物化判断逻辑
func TestCOWDeltaRef_ShouldMaterialize(t *testing.T) {
	tests := []struct {
		name       string
		baseSize   int
		refCount   int32
		deltaCount int
		maxDeltas  int
		want       bool
	}{
		{
			name:       "少量增量",
			baseSize:   100,
			refCount:   1,
			deltaCount: 2,
			maxDeltas:  10,
			want:       false,
		},
		{
			name:       "增量数量超限",
			baseSize:   100,
			refCount:   1,
			deltaCount: 11,
			maxDeltas:  10,
			want:       true,
		},
		{
			name:       "增量比例超限 (20%)",
			baseSize:   100,
			refCount:   1,
			deltaCount: 21,
			maxDeltas:  100,
			want:       true,
		},
		{
			name:       "引用计数高",
			baseSize:   100,
			refCount:   11,
			deltaCount: 2,
			maxDeltas:  10,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := make([][]byte, tt.baseSize)
			values := make([][]byte, tt.baseSize)
			ref := NewCOWDeltaRef(keys, values)
			ref.maxDeltas = tt.maxDeltas
			ref.refCount.Store(tt.refCount)

			// 添加增量
			for i := range tt.deltaCount {
				ref.AppendDelta(Delta{op: DeltaInsert, key: []byte{byte(i)}, value: []byte("val")})
			}

			got := ref.ShouldMaterialize(tt.baseSize, tt.refCount)
			assert.Equal(t, tt.want, got, "ShouldMaterialize() = %v, want %v", got, tt.want)
		})
	}
}

// TestCOWDeltaRef_CompactDeltas 测试压缩增量链
func TestCOWDeltaRef_CompactDeltas(t *testing.T) {
	keys := [][]byte{[]byte("key1")}
	values := [][]byte{[]byte("val1")}

	ref := NewCOWDeltaRef(keys, values)

	// 添加多个增量（包含重复 key）
	ref.AppendDelta(Delta{op: DeltaInsert, key: []byte("key2"), value: []byte("val2")})
	ref.AppendDelta(Delta{op: DeltaUpdate, key: []byte("key2"), value: []byte("val2-updated")})
	ref.AppendDelta(Delta{op: DeltaInsert, key: []byte("key3"), value: []byte("val3")})
	ref.AppendDelta(Delta{op: DeltaUpdate, key: []byte("key3"), value: []byte("val3-updated")})
	ref.AppendDelta(Delta{op: DeltaDelete, key: []byte("key1")})

	assert.Equal(t, 5, ref.GetDeltaCount())

	// 压缩增量链
	ref.CompactDeltas()

	// 应该只剩下 3 个（每个 key 保留最新操作）
	deltas := ref.GetDeltas()
	assert.Equal(t, 3, len(deltas))

	// 验证每个 key 的最新操作
	deltaMap := make(map[string]Delta)
	for _, d := range deltas {
		deltaMap[string(d.key)] = d
	}

	// key2 应该是 Update（最新）
	assert.Equal(t, DeltaUpdate, deltaMap["key2"].op)
	assert.Equal(t, []byte("val2-updated"), deltaMap["key2"].value)

	// key3 应该是 Update（最新）
	assert.Equal(t, DeltaUpdate, deltaMap["key3"].op)
	assert.Equal(t, []byte("val3-updated"), deltaMap["key3"].value)

	// key1 应该是 Delete（最新）
	assert.Equal(t, DeltaDelete, deltaMap["key1"].op)
}

// TestCOWDeltaRef_Concurrent 测试并发安全性
func TestCOWDeltaRef_Concurrent(t *testing.T) {
	keys := [][]byte{[]byte("key1"), []byte("key2")}
	values := [][]byte{[]byte("val1"), []byte("val2")}

	ref := NewCOWDeltaRef(keys, values)

	const goroutines = 100
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 2) // 读者 + 写者

	// 并发写入
	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			for j := range opsPerGoroutine {
				key := []byte{byte(id)}
				value := []byte{byte(j)}
				ref.AppendDelta(Delta{op: DeltaInsert, key: key, value: value})
			}
		}(i)
	}

	// 并发读取
	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			for range opsPerGoroutine {
				_ = ref.GetDeltaCount()
				_ = ref.GetDeltas()
				_ = ref.GetVersion()
			}
		}(i)
	}

	wg.Wait()

	// 验证最终状态
	finalCount := ref.GetDeltaCount()
	assert.Equal(t, goroutines*opsPerGoroutine, finalCount)

	// 验证引用计数
	assert.Equal(t, int32(1), ref.GetRefCount())
}

// TestCOWDeltaRef_Version 测试版本号递增
func TestCOWDeltaRef_Version(t *testing.T) {
	keys := [][]byte{[]byte("key1")}
	values := [][]byte{[]byte("val1")}

	ref := NewCOWDeltaRef(keys, values)

	// 初始版本号应该是 0
	assert.Equal(t, uint64(0), ref.GetVersion())

	// 每次 AppendDelta 应该递增版本号
	ref.AppendDelta(Delta{op: DeltaInsert, key: []byte("key2"), value: []byte("val2")})
	assert.Equal(t, uint64(1), ref.GetVersion())

	ref.AppendDelta(Delta{op: DeltaUpdate, key: []byte("key1"), value: []byte("val1-new")})
	assert.Equal(t, uint64(2), ref.GetVersion())
}

// TestCOWDeltaRef_GetSharedData 测试获取共享数据
func TestCOWDeltaRef_GetSharedData(t *testing.T) {
	keys := [][]byte{[]byte("key1"), []byte("key2")}
	values := [][]byte{[]byte("val1"), []byte("val2")}

	ref := NewCOWDeltaRef(keys, values)

	// 获取的应该是原始引用（非拷贝）
	sharedKeys := ref.GetSharedKeys()
	sharedValues := ref.GetSharedValues()

	// 验证内容相同（虽然不能直接比较切片引用）
	assert.Equal(t, len(keys), len(sharedKeys))
	assert.Equal(t, len(values), len(sharedValues))
	for i := range keys {
		assert.Equal(t, keys[i], sharedKeys[i])
		assert.Equal(t, values[i], sharedValues[i])
	}

	// 修改返回的切片应该影响原始数据（因为是共享引用）
	// 注意：这里测试的是底层数组共享，不是切片头
	sharedKeys[0][0] = 'm' // 修改第一个字节
	assert.Equal(t, byte('m'), keys[0][0])
}
