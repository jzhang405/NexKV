package btree

import (
	"context"
	"fmt"
	"testing"
	"github.com/stretchr/testify/require"
	"github.com/jzhang405/NexKV/internal/domain/model"
)

// TestVerifyRootSplitMissingReinsert 验证根分裂场景缺少重新插入逻辑
// 
// 根本原因（由 Kimi 发现）：
// 在 handleSplitOffHeapSync 的根分裂场景（len(path) < 2）中，
// splitRootOffHeapSync 返回后没有重新插入原始 key-value，
// 导致 key-05655（以及后续 keys）丢失。
//
// 对比：非根分裂场景有完整的"Step 16: 重新插入原始 key-value"逻辑。
func TestVerifyRootSplitMissingReinsert(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过耗时测试")
	}

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	t.Log("=== 插入 5655 个 keys ===")
	for i := 0; i < 5655; i++ {
		key := fmt.Sprintf("key-%05d", i)
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, []byte(key), value)
		require.NoError(t, err)
	}

	// 检查树的状态
	t.Log("\n=== 检查树的状态 ===")
	rootInfo := tree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo)
	
	rootPageID := model.PageID(rootInfo.GetPageID())
	t.Logf("Root pageID: %d", rootPageID)
	t.Logf("Root is leaf: %v", tree.offheapAdapter.IsLeaf(rootPageID))
	
	// 检查是否会发生根分裂
	rootCount := tree.offheapAdapter.pa.GetCount(uint32(rootPageID))
	t.Logf("Root count: %d / maxInternalKeys=%d", rootCount, maxInternalKeys)
	
	willCauseRootSplit := rootCount >= maxInternalKeys-1
	t.Logf("Will cause root split: %v", willCauseRootSplit)

	// 插入 key-05655（触发根分裂）
	t.Log("\n=== 插入 key-05655（触发根分裂） ===")
	key05655 := []byte("key-05655")
	value05655 := []byte("value-5655")
	
	err = tree.Set(ctx, key05655, value05655)
	t.Logf("Set(key-05655) 返回: err=%v", err)
	
	// 验证 key-05655
	t.Log("\n=== 验证 key-05655 ===")
	got, err := tree.Get(ctx, key05655)
	t.Logf("Get(key-05655) 返回: err=%v, value=%v", err, got)
	
	if err != nil {
		t.Logf("\n❌ 确认：key-05655 丢失")
		t.Logf("\n=== 根本原因（由 Kimi 发现） ===")
		t.Logf("在 handleSplitOffHeapSync 的根分裂场景（len(path) < 2）中，")
		t.Logf("splitRootOffHeapSync 返回后没有重新插入原始 key-value。")
		t.Logf("")
		t.Logf("对比：非根分裂场景有完整的 'Step 16: 重新插入原始 key-value' 逻辑。")
		t.Logf("位置：leaf_lock_set.go:565-578（根分裂）vs leaf_lock_set.go:997-1024（非根分裂）")
		t.Logf("")
		t.Logf("修复方案：在 splitRootOffHeapSync 返回前，添加重新插入逻辑。")
		
		// 验证假设：检查是否是根分裂场景
		t.Logf("\n=== 验证假设 ===")
		t.Logf("如果问题确实是根分裂缺少重新插入逻辑，那么：")
		t.Logf("1. key-05655 触发了根分裂")
		t.Logf("2. 根分裂成功创建了新的根节点")
		t.Logf("3. 但原始 key-05655 未重新插入")
		t.Logf("4. 导致 Get(key-05655) 找不到")
	}
}
