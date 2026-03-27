package btree

import (
	"context"
	"fmt"
	"testing"
	"github.com/stretchr/testify/require"
)

// TestInvestigateKey05655 详细调查 key-05655 导致数据丢失的原因
func TestInvestigateKey05655(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过耗时测试")
	}

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// Step 1: 插入到 key-05654（应该成功）
	t.Log("=== Step 1: 插入 key-00000 到 key-05654 ===")
	for i := 0; i < 5655; i++ {
		key := fmt.Sprintf("key-%05d", i)
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, []byte(key), value)
		require.NoError(t, err, "插入 key %s 失败", key)
	}
	t.Logf("✓ 成功插入 5655 个 keys (key-00000 到 key-05654)")

	// Step 2: 验证所有已插入的 keys
	t.Log("\n=== Step 2: 验证已插入的 keys ===")
	missingBefore := []int{}
	for i := 0; i < 5655; i++ {
		key := fmt.Sprintf("key-%05d", i)
		_, err := tree.Get(ctx, []byte(key))
		if err != nil {
			missingBefore = append(missingBefore, i)
		}
	}
	if len(missingBefore) > 0 {
		t.Fatalf("在插入 key-05655 之前就有 %d 个 keys 丢失！", len(missingBefore))
	}
	t.Logf("✓ 所有 5655 个 keys 都可以检索")

	// Step 3: 插入 key-05655（触发问题的 key）
	t.Log("\n=== Step 3: 插入 key-05655（触发点） ===")
	key05655 := []byte("key-05655")
	value05655 := []byte("value-5655")
	err = tree.Set(ctx, key05655, value05655)
	if err != nil {
		t.Logf("Set() 返回错误: %v", err)
	} else {
		t.Logf("Set() 返回成功")
	}

	// Step 4: 立即验证 key-05655
	t.Log("\n=== Step 4: 验证 key-05655 ===")
	got, err := tree.Get(ctx, key05655)
	if err != nil {
		t.Logf("❌ Get(key-05655) 返回错误: %v", err)
	} else if got == nil {
		t.Logf("❌ Get(key-05655) 返回 nil")
	} else {
		t.Logf("✓ Get(key-05655) 成功: %s", string(got))
	}

	// Step 5: 验证所有 5656 个 keys
	t.Log("\n=== Step 5: 验证所有 keys ===")
	missingAfter := []int{}
	for i := 0; i < 5656; i++ {
		key := fmt.Sprintf("key-%05d", i)
		_, err := tree.Get(ctx, []byte(key))
		if err != nil {
			missingAfter = append(missingAfter, i)
		}
	}
	t.Logf("丢失的 keys 数量: %d / 5656 (%.2f%%)", len(missingAfter), float64(len(missingAfter))*100/5656)
	
	if len(missingAfter) > 0 {
		t.Logf("第一个丢失: key-%05d", missingAfter[0])
		if len(missingAfter) <= 10 {
			for _, m := range missingAfter {
				t.Logf("  - key-%05d", m)
			}
		} else {
			t.Logf("最后一个丢失: key-%05d", missingAfter[len(missingAfter)-1])
		}
	}

	// Step 6: 继续插入到 key-05999，验证丢失模式
	t.Log("\n=== Step 6: 继续插入到 key-05999 ===")
	for i := 5656; i < 6000; i++ {
		key := fmt.Sprintf("key-%05d", i)
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = tree.Set(ctx, []byte(key), value) // 忽略错误，继续插入
	}

	t.Log("\n=== Step 7: 最终验证 ===")
	finalMissing := []int{}
	for i := 0; i < 6000; i++ {
		key := fmt.Sprintf("key-%05d", i)
		_, err := tree.Get(ctx, []byte(key))
		if err != nil {
			finalMissing = append(finalMissing, i)
		}
	}
	t.Logf("最终丢失的 keys 数量: %d / 6000 (%.2f%%)", len(finalMissing), float64(len(finalMissing))*100/6000)
	
	if len(finalMissing) > 0 {
		t.Logf("丢失范围: key-%05d 到 key-%05d", finalMissing[0], finalMissing[len(finalMissing)-1])
		t.Logf("丢失的 keys 连续性检查:")
		continuous := true
		for i := 1; i < len(finalMissing); i++ {
			if finalMissing[i] != finalMissing[i-1]+1 {
				continuous = false
				t.Logf("  - 发现跳跃: key-%05d -> key-%05d", finalMissing[i-1], finalMissing[i])
			}
		}
		if continuous {
			t.Logf("  ✓ 所有丢失的 keys 是连续的")
		}
	}
}
