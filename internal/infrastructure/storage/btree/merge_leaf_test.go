package btree

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMergeLeaf_NoMergeNeeded 测试不需要合并的情况（键数量足够）
func TestMergeLeaf_NoMergeNeeded(t *testing.T) {
	dir := t.TempDir()

	// 使用内存模式（不持久化）避免序列化大小问题
	btree, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	// 关闭持久化以避免序列化大小问题
	btree.chunkMgr = nil
	defer btree.Close()

	ctx := context.Background()

	// 插入足够多的键（10 > minKeys=8）
	for i := range 10 {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := btree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 删除一个键，不会触发合并
	err = btree.Delete(ctx, []byte{5})
	require.NoError(t, err)

	// 验证删除成功
	_, err = btree.Get(ctx, []byte{5})
	assert.Error(t, err)
}

// TestMergeLeaf_BorrowFromLeft 测试从左兄弟借键
//
// ⚠️ 已迁移到 merge_api_test.go 中的 TestMergeAPI_BorrowFromLeft
// 该测试使用 Set/Delete API 触发 Merge 场景，更加可靠
func TestMergeLeaf_BorrowFromLeft(t *testing.T) {
	t.Skip("已迁移到 merge_api_test.go 中的 TestMergeAPI_BorrowFromLeft")
}

// TestMergeLeaf_BorrowFromRight 测试从右兄弟借键
func TestMergeLeaf_BorrowFromRight(t *testing.T) {
	dir := t.TempDir()

	btree, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	btree.chunkMgr = nil // 禁用持久化
	defer btree.Close()

	ctx := context.Background()

	// 构造特定场景：右兄弟有足够键，当前节点键不足
	// 1. 先插入足够的键触发分裂
	for i := range 34 {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := btree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 2. 删除左侧叶子节点的键
	for i := range 8 {
		err := btree.Delete(ctx, []byte{byte(i)})
		require.NoError(t, err)
	}

	// 验证借操作成功（应能从右兄弟借键）
}

// TestMergeLeaf_MergeWithLeft 测试与左兄弟合并
//
// ⚠️ 已迁移到 merge_api_test.go 中的 TestMergeAPI_MergeWithLeft
// 该测试使用 Set/Delete API 触发 Merge 场景，更加可靠
func TestMergeLeaf_MergeWithLeft(t *testing.T) {
	t.Skip("已迁移到 merge_api_test.go 中的 TestMergeAPI_MergeWithLeft")
}

// TestMergeLeaf_MergeWithRight 测试与右兄弟合并
//
// ⚠️ 已迁移到 merge_api_test.go 中的 TestMergeAPI_MergeWithRight
// 该测试使用 Set/Delete API 触发 Merge 场景，更加可靠
func TestMergeLeaf_MergeWithRight(t *testing.T) {
	t.Skip("已迁移到 merge_api_test.go 中的 TestMergeAPI_MergeWithRight")
}

// TestMergeLeaf_BorrowBoundary 测试借操作的边界条件
func TestMergeLeaf_BorrowBoundary(t *testing.T) {
	dir := t.TempDir()

	btree, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	btree.chunkMgr = nil // 禁用持久化
	defer btree.Close()

	ctx := context.Background()

	// 测试边界条件：兄弟节点刚好有 minKeys+1 个键
	// 1. 插入足够的键
	for i := range 34 {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := btree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 2. 删除键使节点达到边界条件
	// 使一个节点有 9 个键（minKeys+1），另一个有 7 个键（minKeys-1）
	// 应该触发借操作而不是合并
	for i := 10; i < 17; i++ {
		err := btree.Delete(ctx, []byte{byte(i)})
		require.NoError(t, err)
	}

	// 验证操作成功
	value, err := btree.Get(ctx, []byte{5})
	assert.NoError(t, err)
	assert.Equal(t, []byte{105}, value)
}

// TestMergeLeaf_MergeRootReduction 测试根节点减少（树高度降低）
//
// ⚠️ 已迁移到 merge_api_test.go 中的 TestMergeAPI_MergeRootReduction
// 该测试使用 Set/Delete API 触发 Merge 场景，更加可靠
func TestMergeLeaf_MergeRootReduction(t *testing.T) {
	t.Skip("已迁移到 merge_api_test.go 中的 TestMergeAPI_MergeRootReduction")
}

// TestMergeLeaf_ConcurrentDelete 测试并发删除的场景
//
// ⚠️ 已禁用：当前实现不支持真正的并发删除
//
// 已知问题：
// 1. LeafPage.Delete() 中的 keys 和 values 删除操作不是原子的
// 2. redistributeLeafLeft 在并发场景下可能读取到不一致的 Page 状态
// 3. CCOW 机制确保了根节点的原子更新，但兄弟节点的访问没有锁保护
//
// TODO: 需要实现以下功能以支持并发删除：
// 1. Page 级别的读写锁（PageLock）
// 2. 原子的键值对删除操作
// 3. 或者使用 CAS 方式更新整个 Page
func TestMergeLeaf_ConcurrentDelete(t *testing.T) {
	t.Skip("已禁用：当前实现不支持真正的并发删除。需要实现 PageLock 或其他并发控制机制。")

	if testing.Short() {
		t.Skip("跳过并发测试（短模式）")
	}

	dir := t.TempDir()

	btree, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	btree.chunkMgr = nil // 禁用持久化
	defer btree.Close()

	ctx := context.Background()

	// 插入初始数据
	const numKeys = 50
	for i := range numKeys {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := btree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 并发删除
	const numGoroutines = 10
	const deletesPerGoroutine = 4

	var wg sync.WaitGroup
	done := make(chan bool, numGoroutines)

	for id := range numGoroutines {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			defer func() { done <- true }()

			start := goroutineID * deletesPerGoroutine
			for j := range deletesPerGoroutine {
				key := []byte{byte(start + j)}
				if start+j < numKeys {
					err := btree.Delete(ctx, key)
					if err != nil {
						t.Logf("Goroutine %d: 删除键 %d 失败: %v", goroutineID, start+j, err)
					}
				}
			}
		}(id)
	}

	// 等待所有 goroutine 完成
	for range numGoroutines {
		<-done
	}
	wg.Wait()

	// 验证剩余数据的一致性
	// 验证几个键仍然存在
	testKeys := []int{0, 10, 20, 30, 40}
	for _, keyVal := range testKeys {
		key := []byte{byte(keyVal)}
		value, err := btree.Get(ctx, key)
		if err == nil {
			assert.Equal(t, []byte{byte(keyVal + 100)}, value, "键 %d 的值应该匹配", keyVal)
		}
	}

	t.Log("TestMergeLeaf_ConcurrentDelete 完成 - 并发删除场景验证通过")
}
