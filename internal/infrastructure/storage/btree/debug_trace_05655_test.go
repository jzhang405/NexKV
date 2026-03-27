package btree

import (
	"context"
	"fmt"
	"testing"
	"github.com/stretchr/testify/require"
	"github.com/jzhang405/NexKV/internal/domain/model"
)

// TestDebugTraceKey05655 追踪 key-05655 的完整执行路径
// 目标：找到 Set() 返回成功但数据未写入的根本原因
func TestDebugTraceKey05655(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过耗时测试")
	}

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// Step 1: 插入到 key-05654（5655 个 keys）
	t.Log("=== Step 1: 插入 key-00000 到 key-05654 ===")
	for i := 0; i < 5655; i++ {
		key := fmt.Sprintf("key-%05d", i)
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, []byte(key), value)
		require.NoError(t, err, "插入 key %s 失败", key)
	}
	t.Logf("✓ 成功插入 5655 个 keys")

	// Step 2: 验证所有 keys 都可检索
	t.Log("\n=== Step 2: 验证所有 keys ===")
	for i := 0; i < 5655; i++ {
		key := fmt.Sprintf("key-%05d", i)
		_, err := tree.Get(ctx, []byte(key))
		require.NoError(t, err, "key %s 应该存在", key)
	}
	t.Logf("✓ 所有 5655 个 keys 都可以检索")

	// Step 3: 插入 key-05655（关键点）
	t.Log("\n=== Step 3: 插入 key-05655（触发点） ===")
	key05655 := []byte("key-05655")
	value05655 := []byte("value-5655")
	
	err = tree.Set(ctx, key05655, value05655)
	t.Logf("Set(key-05655) 返回: err=%v", err)
	require.NoError(t, err, "Set(key-05655) 应该返回成功")

	// Step 4: 立即验证 key-05655
	t.Log("\n=== Step 4: 验证 key-05655 ===")
	got, err := tree.Get(ctx, key05655)
	t.Logf("Get(key-05655) 返回: err=%v, value=%v", err, got)
	
	if err != nil {
		t.Logf("❌ 确认：Set() 返回成功，但 Get() 返回错误: %v", err)
		t.Logf("根本原因：数据未真正写入，但 Set() 误报成功")
		
		// 尝试理解为什么会这样
		t.Log("\n=== 可能的原因 ===")
		t.Log("1. handleSplitOffHeapSync 返回成功，但实际上父节点索引更新失败")
		t.Log("2. PageRefCache 更新失败，导致搜索路径指向旧页面")
		t.Log("3. 根节点分裂后，新的根节点未正确设置")
		t.Log("4. 叶子节点分裂后，父节点未更新索引")
	}
	
	// 尝试查找 key-05655 应该在哪里
	t.Log("\n=== 尝试搜索 key-05655 的位置 ===")
	
	// 检查树的结构
	rootInfo := tree.rootRef.GetRootPageInfo()
	if rootInfo != nil {
		t.Logf("Root pageID: %d", rootInfo.GetPageID())
		
		// 检查根节点是否是叶子节点
		isRootLeaf := tree.offheapAdapter.IsLeaf(model.PageID(rootInfo.GetPageID()))
		t.Logf("Root is leaf: %v", isRootLeaf)
		
		if isRootLeaf {
			t.Log("树只有一层（根节点就是叶子节点）")
			t.Log("在这种情况下，数据应该直接写入根节点")
		} else {
			t.Log("树有多层，需要检查父节点索引")
		}
	}
}
