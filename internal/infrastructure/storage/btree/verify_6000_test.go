package btree

import (
	"context"
	"fmt"
	"testing"
	"github.com/stretchr/testify/require"
)

// TestVerify6000KeysNoLoss 验证 bug 报告中的 345 keys 丢失问题已修复
// 原问题：插入 6000 个 keys（key-00000 到 key-05999），但只能检索到 5655 个
// 丢失：key-05655 到 key-05999（345 个 keys，5.75% 丢失率）
func TestVerify6000KeysNoLoss(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过耗时测试")
	}

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	const keyCount = 6000

	t.Logf("正在插入 %d 个 keys...", keyCount)
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("key-%05d", i)
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, []byte(key), value)
		require.NoError(t, err, "插入 key %s 失败", key)
	}
	t.Logf("✓ 成功插入 %d 个 keys", keyCount)

	// 立即验证所有 keys
	t.Logf("正在验证 %d 个 keys...", keyCount)
	successCount := 0
	missingKeys := []int{}

	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("key-%05d", i)
		got, err := tree.Get(ctx, []byte(key))
		if err != nil {
			missingKeys = append(missingKeys, i)
		} else if got != nil {
			successCount++
		}
	}

	t.Logf("=== 验证结果 ===")
	t.Logf("✓ 成功检索: %d / %d", successCount, keyCount)
	
	if len(missingKeys) > 0 {
		lossRate := float64(len(missingKeys)) * 100 / float64(keyCount)
		t.Logf("✗ 丢失 keys: %d / %d (%.2f%%)", len(missingKeys), keyCount, lossRate)
		
		// 打印丢失的 keys 范围
		if len(missingKeys) <= 10 {
			for _, idx := range missingKeys {
				t.Logf("  - key-%05d", idx)
			}
		} else {
			t.Logf("丢失范围: key-%05d 到 key-%05d", missingKeys[0], missingKeys[len(missingKeys)-1])
		}
		
		t.Fatalf("发现 %d 个 keys 丢失 (%.2f%%)，bug 未修复！", len(missingKeys), lossRate)
	} else {
		t.Logf("✓ 所有 keys 都成功检索，无数据丢失！bug 已修复！")
	}
}
