package bftree

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLeafNode(t *testing.T) {
	tests := []struct {
		name   string
		pageID uint64
		level  PageLevel
	}{
		{
			name:   "L1 Mini-Page",
			pageID: 1,
			level:  L1,
		},
		{
			name:   "L2 Mini-Page",
			pageID: 2,
			level:  L2,
		},
		{
			name:   "L3 Mini-Page",
			pageID: 3,
			level:  L3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := NewLeafNode(tt.pageID, tt.level, 8, 2048)

			assert.Equal(t, tt.pageID, node.pageID)
			assert.Equal(t, tt.level, node.level)
			assert.Equal(t, uint64(1), node.version)
			assert.NotNil(t, node.miniPage)
			assert.NotNil(t, node.deltas)
			assert.Equal(t, uint16(0), node.deltaSize)
		})
	}
}

func TestNewMiniPage(t *testing.T) {
	tests := []struct {
		name             string
		level            PageLevel
		expectedCapacity uint16
	}{
		{"L1", L1, 64},
		{"L2", L2, 128},
		{"L3", L3, 256},
		{"L4", L4, 512},
		{"L5", L5, 1024},
		{"L6", L6, 2048},
		{"Full", PageLevel(7), 4096},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mp := NewMiniPage(tt.level)

			assert.Equal(t, tt.level, mp.level)
			assert.Equal(t, uint64(0), mp.bitmap) // 初始无占用
			assert.NotNil(t, mp.slots)
			assert.NotNil(t, mp.slotMap) // P1-4: 验证 map 已初始化
			assert.Equal(t, uint16(0), mp.dataSize)
			assert.Equal(t, tt.expectedCapacity, mp.capacity)
		})
	}
}

func TestMaxSizeForLevel(t *testing.T) {
	tests := []struct {
		level        PageLevel
		expectedSize uint16
	}{
		{L1, 64},
		{L2, 128},
		{L3, 256},
		{L4, 512},
		{L5, 1024},
		{L6, 2048},
		{PageLevel(7), 4096},  // Full-Page
		{PageLevel(99), 4096}, // 未知级别默认 Full-Page
	}

	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			size := maxSizeForLevel(tt.level)
			assert.Equal(t, tt.expectedSize, size)
		})
	}
}

func TestLeafNode_Get_Set(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	// 测试 Get 不存在的键
	value, found := node.Get([]byte("nonexistent"))
	assert.False(t, found)
	assert.Nil(t, value)

	// 测试 Set
	err := node.Set([]byte("key1"), []byte("value1"))
	require.NoError(t, err)

	// 测试 Get 存在的键（从 Delta Chain）
	value, found = node.Get([]byte("key1"))
	assert.True(t, found)
	assert.Equal(t, []byte("value1"), value)
}

// P1-6: 测试 nil key
func TestLeafNode_Set_NilKey(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	err := node.Set(nil, []byte("value"))
	assert.Error(t, err)
	assert.Equal(t, ErrNilKey, err)
}

// P1-6: 测试空键
func TestLeafNode_Set_EmptyKey(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	err := node.Set([]byte{}, []byte("value"))
	assert.Error(t, err)
	assert.Equal(t, ErrEmptyKey, err)
}

// P1-6: 测试 nil value
func TestLeafNode_Set_NilValue(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	err := node.Set([]byte("key"), nil)
	assert.Error(t, err)
	assert.Equal(t, ErrNilValue, err)
}

func TestLeafNode_Get_Set_Multiple(t *testing.T) {
	node := NewLeafNode(1, L2, 8, 2048)

	// 写入多个键值
	pairs := [][]byte{
		[]byte("key1"), []byte("value1"),
		[]byte("key2"), []byte("value2"),
		[]byte("key3"), []byte("value3"),
	}

	for i := 0; i < len(pairs); i += 2 {
		err := node.Set(pairs[i], pairs[i+1])
		require.NoError(t, err)
	}

	// 验证读取
	for i := 0; i < len(pairs); i += 2 {
		value, found := node.Get(pairs[i])
		assert.True(t, found, "key: %s", string(pairs[i]))
		assert.Equal(t, pairs[i+1], value)
	}
}

func TestLeafNode_Set_Update(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	// 第一次写入
	err := node.Set([]byte("key1"), []byte("value1"))
	require.NoError(t, err)

	value, found := node.Get([]byte("key1"))
	assert.True(t, found)
	assert.Equal(t, []byte("value1"), value)

	// 第二次写入（更新）
	err = node.Set([]byte("key1"), []byte("value2"))
	require.NoError(t, err)

	value, found = node.Get([]byte("key1"))
	assert.True(t, found)
	assert.Equal(t, []byte("value2"), value)
}

// P1-5: 测试 Compact 触发
func TestLeafNode_Compact(t *testing.T) {
	// 使用 L2 而不是 L1（L1 容量 64B，maxDeltaSize=32B，只能容纳约 6 个 Delta）
	node := NewLeafNode(1, L2, 8, 2048)

	// 初始状态不需要合并
	assert.False(t, node.shouldCompact())

	// L2 容量 128B，maxDeltaSize=64B，可以容纳更多 Delta
	// 写入 8 个 Delta 触发合并（基于 maxDeltaLen=8）
	for i := 0; i < 8; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		err := node.Set(key, value)
		require.NoError(t, err)
	}

	// Delta Chain 应该被清空（Compact 已执行）
	assert.Equal(t, 0, node.DeltaCount())

	// 验证数据仍然可以通过 Get 获取（已合并到 Mini-Page）
	value, found := node.Get([]byte{byte(0)})
	assert.True(t, found)
	assert.Equal(t, []byte("value"), value)
}

// P1-8: 测试 Delta 操作（Delete）
func TestLeafNode_DeltaOpDelete(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	// 先写入
	_ = node.Set([]byte("key1"), []byte("value1"))

	// 添加删除 Delta
	node.mu.Lock()
	node.deltas = append(node.deltas, &DeltaEntry{
		opType: DeltaOpDelete,
		key:    []byte("key1"),
	})
	node.mu.Unlock()

	// 验证删除
	value, found := node.Get([]byte("key1"))
	assert.False(t, found)
	assert.Nil(t, value)
}

func TestLeafNode_DeltaCount(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	// 初始 Delta 为 0
	assert.Equal(t, 0, node.DeltaCount())

	// 写入 3 个 Delta
	for i := 0; i < 3; i++ {
		err := node.Set([]byte{byte(i)}, []byte("value"))
		require.NoError(t, err)
	}

	assert.Equal(t, 3, node.DeltaCount())
}

func TestLeafNode_Size(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	// 初始大小
	initialSize := node.Size()
	assert.Equal(t, uint64(0), initialSize)

	// 写入数据
	key := []byte("key")
	value := []byte("value")
	err := node.Set(key, value)
	require.NoError(t, err)

	// 大小应该增加
	newSize := node.Size()
	assert.Greater(t, newSize, initialSize)
}

func TestMiniPage_FindSlot(t *testing.T) {
	mp := NewMiniPage(L1)

	// 空页面找不到
	slotIndex := mp.findSlot([]byte("key"))
	assert.Equal(t, -1, slotIndex)

	// 添加一个槽位
	mp.slots = append(mp.slots, Slot{
		key:   []byte("key1"),
		value: []byte("value1"),
	})
	mp.slotMap["key1"] = 0

	// 可以找到
	slotIndex = mp.findSlot([]byte("key1"))
	assert.Equal(t, 0, slotIndex)

	// 找不到不存在的键
	slotIndex = mp.findSlot([]byte("key2"))
	assert.Equal(t, -1, slotIndex)
}

// P1-4: 测试 map 查找性能
func TestMiniPage_FindSlot_Performance(t *testing.T) {
	mp := NewMiniPage(L2)

	// 添加 100 个槽位
	for i := 0; i < 100; i++ {
		key := []byte{byte(i >> 8), byte(i)}
		value := []byte("value")
		mp.slots = append(mp.slots, Slot{key: key, value: value})
		mp.slotMap[string(key)] = i
	}

	// 测试查找
	t.Run("MapLookup", func(t *testing.T) {
		t.Skip("Performance test - run with -bench")
		key := []byte{0, 50}
		idx := mp.findSlot(key)
		assert.Equal(t, 50, idx)
	})
}

// P1-7: 测试返回值副本
func TestLeafNode_Get_ReturnsCopy(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	_ = node.Set([]byte("key1"), []byte("value1"))

	// 获取值
	value, _ := node.Get([]byte("key1"))

	// 修改返回值
	value[0] = 'X'

	// 再次获取，应该不受影响
	value2, _ := node.Get([]byte("key1"))
	assert.Equal(t, []byte("value1"), value2)
	assert.NotEqual(t, []byte("Xalue1"), value2)
}

// P1-8: 测试并发读写安全
func TestLeafNode_ConcurrentReadWrite(t *testing.T) {
	node := NewLeafNode(1, L2, 8, 2048)
	const goroutines = 10
	const opsPerGoroutine = 100

	var wg sync.WaitGroup

	// 并发写入
	for i := 0; i < goroutines/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := []byte{byte(id), byte(j)}
				value := []byte("value")
				_ = node.Set(key, value)
			}
		}(i)
	}

	// 并发读取
	for i := goroutines / 2; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := []byte{byte(id), byte(j)}
				_, _ = node.Get(key)
			}
		}(i)
	}

	wg.Wait()
}

func TestLeafNode_ConcurrentGetSet(t *testing.T) {
	node := NewLeafNode(1, L2, 8, 2048)
	const goroutines = 10
	const writesPerGoroutine = 100

	var wg sync.WaitGroup

	// 并发写入
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < writesPerGoroutine; j++ {
				key := []byte{byte(id), byte(j)}
				value := []byte("value")
				_ = node.Set(key, value)
			}
		}(i)
	}

	wg.Wait()

	// 注意：由于 Compact 会自动触发（每 8 个 Delta），
	// DeltaCount 不会累积到 1000，而是会被周期性清空
	// 这里只验证没有错误发生
	deltaCount := node.DeltaCount()
	assert.LessOrEqual(t, deltaCount, 8) // 最多保留 7 个（第 8 个触发 Compact）
}

// 基准测试
func BenchmarkLeafNode_Get(b *testing.B) {
	node := NewLeafNode(1, L2, 8, 2048)
	_ = node.Set([]byte("key1"), []byte("value1"))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = node.Get([]byte("key1"))
	}
}

func BenchmarkLeafNode_Set(b *testing.B) {
	node := NewLeafNode(1, L2, 8, 2048)
	key := []byte("key")
	value := []byte("value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = node.Set(key, value)
	}
}

// TestLeafNode_Delete 测试删除操作
func TestLeafNode_Delete(t *testing.T) {
	node := NewLeafNode(1, L2, 8, 2048)

	// 1. 先写入数据
	_ = node.Set([]byte("key1"), []byte("value1"))
	_ = node.Set([]byte("key2"), []byte("value2"))
	_ = node.Set([]byte("key3"), []byte("value3"))

	// 2. 验证数据存在
	value, found := node.Get([]byte("key1"))
	require.True(t, found)
	assert.Equal(t, []byte("value1"), value)

	// 3. 删除 key1
	err := node.Delete([]byte("key1"))
	require.NoError(t, err)

	// 4. 验证已删除
	value, found = node.Get([]byte("key1"))
	assert.False(t, found)
	assert.Nil(t, value)

	// 5. 验证其他键不受影响
	value, found = node.Get([]byte("key2"))
	assert.True(t, found)
	assert.Equal(t, []byte("value2"), value)

	value, found = node.Get([]byte("key3"))
	assert.True(t, found)
	assert.Equal(t, []byte("value3"), value)
}

// TestLeafNode_Delete_NotFound 测试删除不存在的键
func TestLeafNode_Delete_NotFound(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	// 删除不存在的键
	err := node.Delete([]byte("nonexistent"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

// TestLeafNode_Delete_NilKey 测试删除 nil 键
func TestLeafNode_Delete_NilKey(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	err := node.Delete(nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNilKey)
}

// TestLeafNode_Delete_EmptyKey 测试删除空键
func TestLeafNode_Delete_EmptyKey(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	err := node.Delete([]byte{})
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyKey)
}

// TestLeafNode_Delete_Twice 测试删除同一个键两次
func TestLeafNode_Delete_Twice(t *testing.T) {
	node := NewLeafNode(1, L2, 8, 2048)

	// 写入数据
	_ = node.Set([]byte("key1"), []byte("value1"))

	// 第一次删除
	err := node.Delete([]byte("key1"))
	require.NoError(t, err)

	// 第二次删除（应该返回 ErrKeyNotFound）
	err = node.Delete([]byte("key1"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

// TestLeafNode_Delete_And_Compact 测试删除后触发合并
func TestLeafNode_Delete_And_Compact(t *testing.T) {
	node := NewLeafNode(1, L2, 8, 2048)

	// 写入多个键值
	for i := 0; i < 8; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		_ = node.Set(key, value)
	}

	// Delta 应该被合并
	assert.Equal(t, 0, node.DeltaCount())

	// 删除几个键
	err := node.Delete([]byte{0})
	require.NoError(t, err)
	err = node.Delete([]byte{1})
	require.NoError(t, err)

	// 验证删除
	_, found := node.Get([]byte{0})
	assert.False(t, found)
	_, found = node.Get([]byte{1})
	assert.False(t, found)

	// 验证其他键存在
	_, found = node.Get([]byte{2})
	assert.True(t, found)
}

// TestLeafNode_Delete_Then_Set 测试删除后再设置
func TestLeafNode_Delete_Then_Set(t *testing.T) {
	node := NewLeafNode(1, L2, 8, 2048)

	// 写入数据
	_ = node.Set([]byte("key1"), []byte("value1"))

	// 删除
	err := node.Delete([]byte("key1"))
	require.NoError(t, err)

	// 验证已删除
	value, found := node.Get([]byte("key1"))
	assert.False(t, found)
	assert.Nil(t, value)

	// 重新设置
	err = node.Set([]byte("key1"), []byte("value2"))
	require.NoError(t, err)

	// 验证新值
	value, found = node.Get([]byte("key1"))
	assert.True(t, found)
	assert.Equal(t, []byte("value2"), value)
}
