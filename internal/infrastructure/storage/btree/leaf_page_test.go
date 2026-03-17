package btree

//nolint:errcheck // 测试代码中忽略部分返回值检查

import (
	"bytes"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLeafPage(t *testing.T) {
	page := NewLeafPage(1)

	assert.Equal(t, model.PageID(1), page.GetPageID())
	assert.Equal(t, uint64(0), page.GetVersion())
	assert.Equal(t, 0, page.NumKeys())
}

func TestLeafPage_InsertGet(t *testing.T) {
	page := NewLeafPage(1)

	// 插入第一个键值对
	key1 := []byte("key1")
	value1 := []byte("value1")
	ok, err := page.Insert(key1, value1)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, page.NumKeys())

	// 验证插入
	value, found := page.Get(key1)
	assert.True(t, found)
	assert.Equal(t, value1, value)

	// 插入第二个键值对
	key2 := []byte("key2")
	value2 := []byte("value2")
	ok, err = page.Insert(key2, value2)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 2, page.NumKeys())

	// 验证顺序（键应该有序）
	assert.Equal(t, -1, bytes.Compare(page.keys[0], page.keys[1]))
}

func TestLeafPage_InsertDuplicate(t *testing.T) {
	page := NewLeafPage(1)

	key := []byte("key")
	value1 := []byte("value1")
	value2 := []byte("value2")

	// 第一次插入
	ok, err := page.Insert(key, value1)
	require.NoError(t, err)
	assert.True(t, ok)

	// 第二次插入（更新）
	ok, err = page.Insert(key, value2)
	require.NoError(t, err)
	assert.False(t, ok) // 不是新插入

	// 验证值被更新
	value, found := page.Get(key)
	assert.True(t, found)
	assert.Equal(t, value2, value)
}

func TestLeafPage_Delete(t *testing.T) {
	page := NewLeafPage(1)

	// 插入键值对
	key1 := []byte("key1")
	value1 := []byte("value1")
	key2 := []byte("key2")
	value2 := []byte("value2")

	_, err := page.Insert(key1, value1)
	require.NoError(t, err)
	_, err = page.Insert(key2, value2)
	require.NoError(t, err)

	// 删除存在的键
	ok, err := page.Delete(key1)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, page.NumKeys())

	// 验证删除
	_, found := page.Get(key1)
	assert.False(t, found)

	// 删除不存在的键
	ok, err = page.Delete([]byte("notexist"))
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestLeafPage_Update(t *testing.T) {
	page := NewLeafPage(1)

	key := []byte("key")
	value1 := []byte("value1")
	value2 := []byte("value2")

	_, err := page.Insert(key, value1)
	require.NoError(t, err)

	// 更新存在的键
	err = page.Update(key, value2)
	require.NoError(t, err)

	value, found := page.Get(key)
	assert.True(t, found)
	assert.Equal(t, value2, value)
}

func TestLeafPage_Split(t *testing.T) {
	page := NewLeafPage(1)

	// 插入多个键值对
	for i := 0; i < 5; i++ {
		key := []byte{byte('a' + byte(i))}
		value := []byte{byte('0' + byte(i))}
		_, err := page.Insert(key, value)
		require.NoError(t, err)
	}

	// 分裂页面
	newPage, splitKey, err := page.Split()
	require.NoError(t, err)
	assert.NotNil(t, newPage)
	assert.NotNil(t, splitKey)
	assert.Equal(t, []byte("c"), splitKey) // 第3个键作为分裂键

	// 验证原页面保留前半部分（不包含分裂键）
	assert.Equal(t, 2, page.NumKeys())
	assert.Equal(t, []byte("a"), page.keys[0])
	assert.Equal(t, []byte("b"), page.keys[1])

	// ✅ Day 10-11: 新分裂逻辑 - 右子节点包含分裂键
	// 验证新页面包含后半部分（包含分裂键）
	assert.Equal(t, 3, newPage.NumKeys())
	assert.Equal(t, []byte("c"), newPage.keys[0]) // 包含分裂键
	assert.Equal(t, []byte("d"), newPage.keys[1])
	assert.Equal(t, []byte("e"), newPage.keys[2])
}

func TestLeafPage_SplitError(t *testing.T) {
	page := NewLeafPage(1)

	// 只有一个键，无法分裂
	key := []byte("key")
	value := []byte("value")
	page.Insert(key, value)

	_, _, err := page.Split()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "less than 2 keys")
}

func TestLeafPage_Clone(t *testing.T) {
	page := NewLeafPage(1)

	// 插入数据
	for i := 1; i <= 3; i++ {
		key := []byte{byte('a' + byte(i))}
		value := []byte{byte('0' + byte(i))}
		page.Insert(key, value)
	}

	// 克隆
	cloned := page.Clone()

	// 验证克隆的页面
	assert.Equal(t, page.GetPageID(), cloned.GetPageID())
	assert.Equal(t, page.GetVersion(), cloned.GetVersion())
	assert.Equal(t, page.NumKeys(), cloned.NumKeys())

	// COW 方案：克隆后共享数据引用
	// keys/values 指向同一个底层数组
	// 注意：由于切片是引用类型，page.keys 和 cloned.keys 指向相同底层数组
	assert.NotNil(t, cloned.cowDelta)
	assert.Equal(t, int32(1), cloned.cowDelta.GetRefCount()) // 只有 cloned 持有 COWDeltaRef 引用

	// 验证数据一致性（通过 Get 方法）
	for i := 1; i <= 3; i++ {
		key := []byte{byte('a' + byte(i))}
		expectedValue := []byte{byte('0' + byte(i))}
		val, found := cloned.Get(key)
		assert.True(t, found)
		assert.Equal(t, expectedValue, val)
	}

	// 验证独立性：通过 Insert 修改 clone
	newKey := []byte("new")
	newValue := []byte("new_val")
	cloned.Insert(newKey, newValue)

	// original 的 Get 应该找不到 newKey
	_, found := page.Get(newKey)
	assert.False(t, found)

	// cloned 的 Get 应该能找到 newKey
	val, found := cloned.Get(newKey)
	assert.True(t, found)
	assert.Equal(t, newValue, val)
}

func TestLeafPage_Range(t *testing.T) {
	page := NewLeafPage(1)

	// 插入数据
	keys := [][]byte{[]byte("key1"), []byte("key2"), []byte("key3")}
	values := [][]byte{[]byte("value1"), []byte("value2"), []byte("value3")}

	for i := 0; i < len(keys); i++ {
		page.Insert(keys[i], values[i])
	}

	// 遍历
	count := 0
	err := page.Range(func(key, value []byte) error {
		count++
		assert.Equal(t, keys[count-1], key)
		assert.Equal(t, values[count-1], value)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestLeafPage_Serialize(t *testing.T) {
	page := NewLeafPage(1)

	// 插入数据
	key := []byte("test")
	value := []byte("test value")
	page.Insert(key, value)

	// 序列化
	data, err := page.Serialize()
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.Greater(t, len(data), 0)

	// 反序列化
	deserialized, err := DeserializeLeafPage(data)
	require.NoError(t, err)
	assert.NotNil(t, deserialized)

	// 验证数据
	assert.Equal(t, page.GetPageID(), deserialized.GetPageID())
	assert.Equal(t, page.GetVersion(), deserialized.GetVersion())
	assert.Equal(t, page.NumKeys(), deserialized.NumKeys())

	// 验证键值对
	origValue, _ := page.Get(key)
	deserValue, _ := deserialized.Get(key)
	assert.Equal(t, origValue, deserValue)
}

func TestLeafPage_Size(t *testing.T) {
	page := NewLeafPage(1)

	// 空页面大小
	baseSize := page.Size()
	assert.Greater(t, baseSize, 0)

	// 插入数据后大小
	key := []byte("key")
	value := []byte("value")
	page.Insert(key, value)

	newSize := page.Size()
	assert.Greater(t, newSize, baseSize)
}

func TestLeafPage_IsFull(t *testing.T) {
	page := NewLeafPage(1)
	maxKeys := 16

	// 未满
	assert.False(t, page.IsFull(maxKeys))

	// 添加到 maxKeys-1
	for i := 0; i < maxKeys-1; i++ {
		key := []byte{byte('a' + byte(i%26))}
		value := []byte("value")
		page.Insert(key, value)
		assert.False(t, page.IsFull(maxKeys))
	}

	// 添加第 maxKeys 个
	key := []byte("z")
	value := []byte("last")
	page.Insert(key, value)
	assert.True(t, page.IsFull(maxKeys))
}

func TestLeafPage_GetVersion(t *testing.T) {
	page := NewLeafPage(1)

	assert.Equal(t, uint64(0), page.GetVersion())

	page.SetVersion(5)
	assert.Equal(t, uint64(5), page.GetVersion())

	page.IncrementVersion()
	assert.Equal(t, uint64(6), page.GetVersion())
}
