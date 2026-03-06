package bftree

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewInnerNode(t *testing.T) {
	tests := []struct {
		name   string
		pageID uint64
		level  PageLevel
	}{
		{"L1 InnerNode", 1, L1},
		{"L2 InnerNode", 2, L2},
		{"L3 InnerNode", 3, L3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := NewInnerNode(tt.pageID, tt.level)

			assert.Equal(t, tt.pageID, node.GetPageID())
			assert.Equal(t, tt.level, node.GetLevel())
			assert.Equal(t, uint64(1), node.GetVersion())
			assert.Equal(t, 0, node.GetChildCount())
			assert.Equal(t, 0, node.GetKeyCount())
		})
	}
}

func TestInnerNode_FindChild(t *testing.T) {
	node := NewInnerNode(1, L2)

	// 初始状态：无子节点
	childID, found := node.FindChild([]byte("key1"))
	assert.False(t, found)
	assert.Equal(t, uint64(0), childID)

	// 添加子节点（需要手动添加，因为 InsertChild 需要分隔键）
	node.mu.Lock()
	node.children = append(node.children, 100, 101, 102)
	node.keys = append(node.keys, []byte("key2"), []byte("key5"))
	node.mu.Unlock()

	// 测试查找：key1 < key2 → children[0]
	childID, found = node.FindChild([]byte("key1"))
	assert.True(t, found)
	assert.Equal(t, uint64(100), childID)

	// 测试查找：key3 在 key2 和 key5 之间 → children[1]
	childID, found = node.FindChild([]byte("key3"))
	assert.True(t, found)
	assert.Equal(t, uint64(101), childID)

	// 测试查找：key6 > key5 → children[2]（最后一个）
	childID, found = node.FindChild([]byte("key6"))
	assert.True(t, found)
	assert.Equal(t, uint64(102), childID)
}

func TestInnerNode_InsertChild(t *testing.T) {
	node := NewInnerNode(1, L2)

	// 在位置 0 插入第一个子节点（不需要分隔键）
	err := node.InsertChild(0, nil, 100)
	require.NoError(t, err)

	// 验证
	assert.Equal(t, 1, node.GetChildCount())
	assert.Equal(t, 0, node.GetKeyCount())

	// 在位置 1 插入第二个子节点（需要分隔键）
	err = node.InsertChild(1, []byte("key2"), 101)
	require.NoError(t, err)

	// 验证
	assert.Equal(t, 2, node.GetChildCount())
	assert.Equal(t, 1, node.GetKeyCount())
	assert.Equal(t, []byte("key2"), node.keys[0])

	// 在位置 2 插入第三个子节点
	err = node.InsertChild(2, []byte("key5"), 102)
	require.NoError(t, err)

	// 验证
	assert.Equal(t, 3, node.GetChildCount())
	assert.Equal(t, 2, node.GetKeyCount())
	assert.Equal(t, []byte("key5"), node.keys[1])
}

func TestInnerNode_InsertChild_NilKey(t *testing.T) {
	node := NewInnerNode(1, L1)

	err := node.InsertChild(1, nil, 100)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNilKey)
}

func TestInnerNode_InsertChild_EmptyKey(t *testing.T) {
	node := NewInnerNode(1, L1)

	err := node.InsertChild(1, []byte{}, 100)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyKey)
}

func TestInnerNode_InsertChild_InvalidIndex(t *testing.T) {
	node := NewInnerNode(1, L1)

	err := node.InsertChild(-1, []byte("key1"), 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid index")
}

func TestInnerNode_Split(t *testing.T) {
	node := NewInnerNode(1, L2)

	// 填满节点（L2 最多 3 个子节点，2 个键）
	node.mu.Lock()
	node.children = append(node.children, 100, 101, 102)
	node.keys = append(node.keys, []byte("key2"), []byte("key5"))
	node.mu.Unlock()

	// 验证已满
	assert.True(t, node.IsFull())
	assert.Equal(t, 3, node.GetChildCount())

	// 分裂节点
	newNode, splitKey, err := node.Split()
	require.NoError(t, err)
	require.NotNil(t, newNode)
	require.NotNil(t, splitKey)

	// 验证分裂结果
	// 左节点：1 个子节点，0 个键
	assert.Equal(t, 1, node.GetChildCount())
	assert.Equal(t, 0, node.GetKeyCount())

	// 右节点：2 个子节点，1 个键
	assert.Equal(t, 2, newNode.GetChildCount())
	assert.Equal(t, 1, newNode.GetKeyCount())

	// 分裂键应该是第一个键
	assert.Equal(t, []byte("key2"), splitKey)
}

func TestInnerNode_Split_NotFull(t *testing.T) {
	node := NewInnerNode(1, L2)

	// 节点未满
	newNode, splitKey, err := node.Split()
	assert.Error(t, err)
	assert.Nil(t, newNode)
	assert.Nil(t, splitKey)
	assert.Contains(t, err.Error(), "not full")
}

func TestInnerNode_Merge(t *testing.T) {
	node1 := NewInnerNode(1, L2)
	node2 := NewInnerNode(2, L2)

	// 填充两个节点
	node1.mu.Lock()
	node1.children = append(node1.children, 100, 101)
	node1.keys = append(node1.keys, []byte("key2"))
	node1.mu.Unlock()

	node2.mu.Lock()
	node2.children = append(node2.children, 102)
	node2.keys = append(node2.keys, []byte("key5"))
	node2.mu.Unlock()

	// 合并
	err := node1.Merge(node2)
	require.NoError(t, err)

	// 验证合并结果
	assert.Equal(t, 3, node1.GetChildCount())
	assert.Equal(t, 2, node1.GetKeyCount())
}

func TestInnerNode_Merge_NilSibling(t *testing.T) {
	node := NewInnerNode(1, L2)

	err := node.Merge(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sibling is nil")
}

func TestInnerNode_Merge_LevelMismatch(t *testing.T) {
	node1 := NewInnerNode(1, L2)
	node2 := NewInnerNode(2, L3)

	err := node1.Merge(node2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "level mismatch")
}

func TestInnerNode_IsFull(t *testing.T) {
	node := NewInnerNode(1, L2)

	// 初始状态：未满
	assert.False(t, node.IsFull())

	// 填满节点
	node.mu.Lock()
	node.children = append(node.children, 100, 101, 102)
	node.keys = append(node.keys, []byte("key2"), []byte("key5"))
	node.mu.Unlock()

	// 验证已满
	assert.True(t, node.IsFull())
}

func TestInnerNode_CanMerge(t *testing.T) {
	node1 := NewInnerNode(1, L2)
	node2 := NewInnerNode(2, L2)

	// 初始状态：可以合并
	assert.True(t, node1.CanMerge(node2))

	// 填充一个节点
	node1.mu.Lock()
	node1.children = append(node1.children, 100, 101)
	node1.keys = append(node1.keys, []byte("key2"))
	node1.mu.Unlock()

	// 仍然可以合并（总共 3 个子节点，maxKeys = 3）
	assert.True(t, node1.CanMerge(node2))

	// 再填充 node2
	node2.mu.Lock()
	node2.children = append(node2.children, 102)
	node2.keys = append(node2.keys, []byte("key5"))
	node2.mu.Unlock()

	// 不可以合并（总共 4 个子节点 > maxKeys）
	assert.False(t, node1.CanMerge(node2))
}

func TestInnerNode_ConcurrentRead(t *testing.T) {
	node := NewInnerNode(1, L2)

	// 填充节点
	node.mu.Lock()
	node.children = append(node.children, 100, 101, 102)
	node.keys = append(node.keys, []byte("key2"), []byte("key5"))
	node.mu.Unlock()

	// 并发读取
	const goroutines = 10
	results := make(chan uint64, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			childID, _ := node.FindChild([]byte("key3"))
			results <- childID
		}()
	}

	// 验证所有读取结果一致
	expectedChildID := uint64(101)
	for i := 0; i < goroutines; i++ {
		childID := <-results
		assert.Equal(t, expectedChildID, childID)
	}
}

func TestMaxKeysForLevel(t *testing.T) {
	tests := []struct {
		level       PageLevel
		expectedMax int
	}{
		{L1, 2},    // 2 个子节点，1 个键
		{L2, 3},    // 3 个子节点，2 个键
		{L3, 5},    // 5 个子节点，4 个键
		{L4, 9},    // 9 个子节点，8 个键
		{L5, 17},   // 17 个子节点，16 个键
		{L6, 33},   // 33 个子节点，32 个键
		{Full, 65}, // 65 个子节点，64 个键
	}

	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			maxKeys := maxKeysForLevel(tt.level)
			assert.Equal(t, tt.expectedMax, maxKeys)
		})
	}
}
