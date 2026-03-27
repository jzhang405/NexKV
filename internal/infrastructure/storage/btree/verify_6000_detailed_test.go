package btree

import (
	"context"
	"fmt"
	"testing"
	"github.com/stretchr/testify/require"
)

// TestVerify6000KeysDetailed 详细追踪 6000 keys 插入过程
func TestVerify6000KeysDetailed(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过耗时测试")
	}

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	const keyCount = 6000
	const checkInterval = 50 // 每 50 个 keys 检查一次

	t.Logf("正在插入 %d 个 keys（每 %d 个检查一次）...", keyCount, checkInterval)
	
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("key-%05d", i)
		value := []byte(fmt.Sprintf("value-%d", i))
		
		err := tree.Set(ctx, []byte(key), value)
		require.NoError(t, err, "插入 key %s 失败", key)
		
		// 定期检查
		if (i+1)%checkInterval == 0 {
			// 检查所有已插入的 keys
			missing := []int{}
			for j := 0; j <= i; j++ {
				checkKey := fmt.Sprintf("key-%05d", j)
				_, err := tree.Get(ctx, []byte(checkKey))
				if err != nil {
					missing = append(missing, j)
				}
			}
			
			if len(missing) > 0 {
				t.Logf("在插入 %d 个 keys 后发现丢失:", i+1)
				t.Logf("  丢失数量: %d / %d (%.2f%%)", len(missing), i+1, float64(len(missing))*100/float64(i+1))
				t.Logf("  第一个丢失: key-%05d", missing[0])
				if len(missing) <= 5 {
					for _, m := range missing {
						t.Logf("    - key-%05d", m)
					}
				} else {
					t.Logf("  最后一个丢失: key-%05d", missing[len(missing)-1])
				}
				t.Fatalf("在插入 %d 个 keys 后发现数据丢失！", i+1)
			}
		}
	}
	
	t.Logf("✓ 所有 %d 个 keys 插入成功，无数据丢失！", keyCount)
}
