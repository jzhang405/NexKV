package btree

import (
	"context"
	"fmt"
	"testing"
	"github.com/stretchr/testify/require"
	"github.com/jzhang405/NexKV/internal/domain/model"
)

// TestTraceRootSplit 追踪根节点分裂过程
// 目标：确认 key-05655 是否触发了根节点分裂
func TestTraceRootSplit(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过耗时测试")
	}

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 检查初始根节点
	t.Log("=== 初始状态 ===")
	rootInfo := tree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo)
	
	initialRootPageID := model.PageID(rootInfo.GetPageID())
	t.Logf("初始 Root pageID: %d", initialRootPageID)
	t.Logf("初始 Root is leaf: %v", tree.offheapAdapter.IsLeaf(initialRootPageID))

	// 插入到 key-05654
	t.Log("\n=== 插入 key-00000 到 key-05654 ===")
	for i := 0; i < 5655; i++ {
		key := fmt.Sprintf("key-%05d", i)
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, []byte(key), value)
		require.NoError(t, err, "插入 key %s 失败", key)
	}

	// 检查分裂前的根节点
	t.Log("\n=== 分裂前状态（插入 5655 个 keys 后） ===")
	rootInfo = tree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo)
	
	beforeRootPageID := model.PageID(rootInfo.GetPageID())
	t.Logf("分裂前 Root pageID: %d", beforeRootPageID)
	t.Logf("分裂前 Root is leaf: %v", tree.offheapAdapter.IsLeaf(beforeRootPageID))
	
	// 验证 key-05654 存在
	key05654 := []byte("key-05654")
	got, err := tree.Get(ctx, key05654)
	t.Logf("Get(key-05654): err=%v, value=%v", err, got)
	require.NoError(t, err, "key-05654 应该存在")

	// 插入 key-05655（触发分裂）
	t.Log("\n=== 插入 key-05655（触发根节点分裂） ===")
	key05655 := []byte("key-05655")
	value05655 := []byte("value-5655")
	
	err = tree.Set(ctx, key05655, value05655)
	t.Logf("Set(key-05655) 返回: err=%v", err)

	// 检查分裂后的根节点
	t.Log("\n=== 分裂后状态（插入 key-05655 后） ===")
	rootInfo = tree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo)
	
	afterRootPageID := model.PageID(rootInfo.GetPageID())
	t.Logf("分裂后 Root pageID: %d", afterRootPageID)
	t.Logf("分裂后 Root is leaf: %v", tree.offheapAdapter.IsLeaf(afterRootPageID))

	// 对比
	t.Log("\n=== 对比分析 ===")
	if beforeRootPageID != afterRootPageID {
		t.Logf("✓ 确认：Root pageID 发生变化 %d -> %d（发生了根节点分裂）", beforeRootPageID, afterRootPageID)
	} else {
		t.Logf("✗ Root pageID 未变化 (%d），可能不是根节点分裂", beforeRootPageID)
	}

	// 验证 key-05655
	t.Log("\n=== 验证 key-05655 ===")
	got, err = tree.Get(ctx, key05655)
	t.Logf("Get(key-05655): err=%v, value=%v", err, got)

	if err != nil {
		t.Logf("\n❌ 问题确认：根节点分裂后，key-05655 丢失了")
		t.Logf("\n=== 根本原因分析 ===")
		t.Logf("根节点分裂过程：")
		t.Logf("1. 旧根节点（pageID=%d）已满", beforeRootPageID)
		t.Logf("2. 创建新的根节点（pageID=%d）", afterRootPageID)
		t.Logf("3. 旧根节点分裂为 leftChild 和 rightChild")
		t.Logf("4. 新根节点应该包含 splitKey 和指向 leftChild/rightChild 的索引")
		t.Logf("\n可能的失败点：")
		t.Logf("1. 新根节点的 splitKey 索引未正确设置")
		t.Logf("2. leftChild 或 rightChild 的 PageRef 未正确更新")
		t.Logf("3. PageRefCache 中旧根节点的引用未更新")
		t.Logf("4. 搜索路径仍指向旧根节点，无法找到新插入的 key")
	}
}
