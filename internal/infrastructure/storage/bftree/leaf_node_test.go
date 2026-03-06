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
			node := NewLeafNode(tt.pageID, tt.level)

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
	node := NewLeafNode(1, L1)

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

func TestLeafNode_Get_Set_Multiple(t *testing.T) {
	node := NewLeafNode(1, L2)

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
	node := NewLeafNode(1, L1)

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

func TestLeafNode_ShouldCompact(t *testing.T) {
	node := NewLeafNode(1, L1)

	// 初始状态不需要合并
	assert.False(t, node.shouldCompact())

	// 写入 8 个 Delta 触发合并
	for i := 0; i < 8; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		err := node.Set(key, value)
		require.NoError(t, err)
	}

	// Delta Chain 长度 = 8，应该触发合并
	assert.True(t, node.shouldCompact())
}

func TestLeafNode_DeltaCount(t *testing.T) {
	node := NewLeafNode(1, L1)

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
	node := NewLeafNode(1, L1)

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

	// 可以找到
	slotIndex = mp.findSlot([]byte("key1"))
	assert.Equal(t, 0, slotIndex)

	// 找不到不存在的键
	slotIndex = mp.findSlot([]byte("key2"))
	assert.Equal(t, -1, slotIndex)
}

func TestLeafNode_ConcurrentGetSet(t *testing.T) {
	node := NewLeafNode(1, L2)
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

	// 等待所有 goroutine 完成
	wg.Wait()

	// 验证 Delta 数量
	deltaCount := node.DeltaCount()
	assert.Equal(t, goroutines*writesPerGoroutine, deltaCount)
}
