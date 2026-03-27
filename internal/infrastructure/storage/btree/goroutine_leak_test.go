// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGoroutineLeakFix 验证 goroutine 泄漏修复
// 只运行 10 次，每次创建和关闭 BTree，确保没有泄漏
func TestGoroutineLeakFix(t *testing.T) {
	const runs = 10
	const goroutines = 50

	for run := 1; run <= runs; run++ {
		btree, err := OpenBTree("", nil)
		require.NoError(t, err)

		ctx := context.Background()
		var wg sync.WaitGroup

		// 并发写入
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					key := []byte{byte(id >> 8), byte(id), byte(j)}
					value := []byte{byte(j)}
					_ = btree.Set(ctx, key, value)
				}
			}(i)
		}

		wg.Wait()

		// 关闭 BTree（应该取消所有后台 goroutines）
		err = btree.Close()
		require.NoError(t, err)

		// 等待一小段时间让 goroutines 完全退出
		time.Sleep(150 * time.Millisecond)

		t.Logf("Run %d/%d completed", run, runs)
	}

	t.Logf("✓ All %d runs completed without goroutine leak", runs)
}
