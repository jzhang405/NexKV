package btree

import (
	"context"
	"fmt"
	"testing"
	"github.com/stretchr/testify/require"
	"github.com/jzhang405/NexKV/internal/domain/model"
)

// TestInspectTreeStructure 检查树的结构和父节点索引
func TestInspectTreeStructure(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过耗时测试")
	}

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 插入到 key-05654
	t.Log("=== 插入 5655 个 keys ===")
	for i := 0; i < 5655; i++ {
		key := fmt.Sprintf("key-%05d", i)
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, []byte(key), value)
		require.NoError(t, err)
	}

	// 检查树的结构
	t.Log("\n=== 树的结构（插入 5655 个 keys 后） ===")
	rootInfo := tree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo)
	
	rootPageID := model.PageID(rootInfo.GetPageID())
	t.Logf("Root pageID: %d", rootPageID)
	t.Logf("Root is leaf: %v", tree.offheapAdapter.IsLeaf(rootPageID))
	
	// 检查根节点的子节点数量
	rootCount := tree.offheapAdapter.pa.GetCount(uint32(rootPageID))
	t.Logf("Root count (keys): %d", rootCount)
	
	// 打印根节点的所有 keys 和 children
	t.Logf("Root keys and children:")
	for i := 0; i <= int(rootCount); i++ {
		if i < int(rootCount) {
			keyOff, keyLen, _ := tree.offheapAdapter.pa.GetIndexEntryOffset(uint32(rootPageID), i)
			key := tree.offheapAdapter.pa.GetKey(uint32(rootPageID), keyOff, keyLen)
			encodedChild := tree.offheapAdapter.pa.GetChild(uint32(rootPageID), i)
			child, _ := tree.offheapAdapter.DecodeChildWithVersion(encodedChild)
			t.Logf("  [%d] key=%s child=%d", i, string(key), child)
		} else {
			// extraChild
			encodedChild := tree.offheapAdapter.pa.GetChild(uint32(rootPageID), i)
			child, _ := tree.offheapAdapter.DecodeChildWithVersion(encodedChild)
			t.Logf("  [%d] (extraChild)=%d", i, child)
		}
	}

	// 验证 key-05654
	t.Log("\n=== 验证 key-05654（分裂前边界） ===")
	key05654 := []byte("key-05654")
	got, err := tree.Get(ctx, key05654)
	t.Logf("Get(key-05654): err=%v, value=%v", err, got)
	require.NoError(t, err)

	// 插入 key-05655
	t.Log("\n=== 插入 key-05655（触发叶子分裂） ===")
	key05655 := []byte("key-05655")
	value05655 := []byte("value-5655")
	
	err = tree.Set(ctx, key05655, value05655)
	t.Logf("Set(key-05655) 返回: err=%v", err)

	// 检查分裂后的树结构
	t.Log("\n=== 树的结构（插入 key-05655 后） ===")
	rootInfo = tree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo)
	
	rootPageID = model.PageID(rootInfo.GetPageID())
	t.Logf("Root pageID: %d", rootPageID)
	
	rootCount = tree.offheapAdapter.pa.GetCount(uint32(rootPageID))
	t.Logf("Root count (keys): %d", rootCount)
	
	// 打印根节点的所有 keys 和 children
	t.Logf("Root keys and children:")
	for i := 0; i <= int(rootCount); i++ {
		if i < int(rootCount) {
			keyOff, keyLen, _ := tree.offheapAdapter.pa.GetIndexEntryOffset(uint32(rootPageID), i)
			key := tree.offheapAdapter.pa.GetKey(uint32(rootPageID), keyOff, keyLen)
			encodedChild := tree.offheapAdapter.pa.GetChild(uint32(rootPageID), i)
			child, _ := tree.offheapAdapter.DecodeChildWithVersion(encodedChild)
			t.Logf("  [%d] key=%s child=%d", i, string(key), child)
		} else {
			// extraChild
			encodedChild := tree.offheapAdapter.pa.GetChild(uint32(rootPageID), i)
			child, _ := tree.offheapAdapter.DecodeChildWithVersion(encodedChild)
			t.Logf("  [%d] (extraChild=%d)", i, child)
		}
	}

	// 验证 key-05655
	t.Log("\n=== 验证 key-05655 ===")
	got, err = tree.Get(ctx, key05655)
	t.Logf("Get(key-05655): err=%v, value=%v", err, got)

	if err != nil {
		t.Logf("\n❌ 问题：key-05655 丢失")
		t.Logf("\n=== 根本原因推断 ===")
		t.Logf("叶子节点分裂后，父节点（可能是根节点 646）的索引未更新")
		t.Logf("具体来说：")
		t.Logf("1. 叶子节点已满（例如 pageID=X），需要分裂")
		t.Logf("2. SplitOffHeapLeafPage 创建 leftPageID 和 rightPageID")
		t.Logf("3. UpdateIndexEntry 应该更新父节点，添加 splitKey 和新的子节点引用")
		t.Logf("4. 但 UpdateIndexEntry 可能失败了，或者返回成功但实际未更新")
		t.Logf("5. 导致父节点的索引仍指向旧的叶子节点")
		t.Logf("6. 新插入的 key-05655 在新的叶子节点中，但搜索路径找不到")
	}
}
