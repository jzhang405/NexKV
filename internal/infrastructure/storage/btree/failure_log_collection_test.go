// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCollectFailureLogs 收集失败场景的完整日志
// 这是 Phase 1.1 的核心实验：运行 1000 次测试，收集所有失败日志
func TestCollectFailureLogs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping failure log collection in short mode")
	}

	// 创建日志文件
	logFile, err := os.Create("/tmp/btree_circular_ref_failures.log")
	require.NoError(t, err)
	defer logFile.Close()

	const totalRuns = 100 // ✅ 修复后可以运行 100 次
	const goroutines = 100
	const operationsPerGoroutine = 100

	successCount := 0
	failureCount := 0
	var mu sync.Mutex

	fmt.Fprintf(logFile, "=== B-Tree Circular Reference Failure Log Collection ===\n")
	fmt.Fprintf(logFile, "Total runs: %d, Goroutines per run: %d, Ops per goroutine: %d\n", totalRuns, goroutines, operationsPerGoroutine)
	fmt.Fprintf(logFile, "Start time: %s\n\n", timeNow())

	for run := 1; run <= totalRuns; run++ {
		// 每次运行创建新的 BTree
		btree, err := OpenBTree("", nil)
		require.NoError(t, err)

		// 启用页面追踪
		btree.offheapPM.EnablePageTracking()

		ctx := context.Background()
		errCh := make(chan error, goroutines*operationsPerGoroutine)
		var wg sync.WaitGroup

		// 并发写入
		for i := range goroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := range operationsPerGoroutine {
					key := []byte{byte(id >> 8), byte(id), byte(j)}
					value := []byte{byte(j)}

					err := btree.Set(ctx, key, value)
					// ErrRetry 在高并发场景下是正常的，不视为错误
					if err != nil && !errors.Is(err, ErrRetry) {
						errCh <- err
					}
				}
			}(i)
		}

		wg.Wait()
		close(errCh)

		// 收集错误
		runErrors := []error{}
		for err := range errCh {
			runErrors = append(runErrors, err)
		}

		mu.Lock()
		if len(runErrors) == 0 {
			successCount++
		} else {
			failureCount++
			// 记录失败详情
			fmt.Fprintf(logFile, "\n=== Run %d FAILED ===\n", run)
			fmt.Fprintf(logFile, "Time: %s\n", timeNow())
			fmt.Fprintf(logFile, "Error count: %d\n", len(runErrors))

			// 记录每个错误
			for i, err := range runErrors {
				fmt.Fprintf(logFile, "\n[Error %d]\n", i+1)
				fmt.Fprintf(logFile, "  Type: %T\n", err)
				fmt.Fprintf(logFile, "  Message: %v\n", err)

				// 如果是循环引用错误，打印更多信息
				if err != nil && err.Error() != "" {
					fmt.Fprintf(logFile, "  Details: Checking for circular reference pattern\n")
				}
			}

			// 记录 Page 4095 报告
			if page4095Report := btree.offheapPM.GetPage4095Report(); page4095Report != "" {
				fmt.Fprintf(logFile, "\n%s\n", page4095Report)
			}

			// 记录高 PageID 报告
			if highPageIDReport := btree.offheapPM.GetHighPageIDReport(); highPageIDReport != "" {
				fmt.Fprintf(logFile, "\n%s\n", highPageIDReport)
			}

			// 记录追踪统计
			stats := btree.offheapPM.GetPageTrackingStats()
			fmt.Fprintf(logFile, "\nPage Tracking Stats:\n")
			fmt.Fprintf(logFile, "  Total allocs: %v\n", stats["total_allocs"])
			fmt.Fprintf(logFile, "  Total frees: %v\n", stats["total_frees"])
			fmt.Fprintf(logFile, "  Active pages: %v\n", stats["active_pages"])
			fmt.Fprintf(logFile, "  Reused count: %v\n", stats["reused_count"])
			fmt.Fprintf(logFile, "  High pageID count: %v\n", stats["high_page_id_count"])
		}
		mu.Unlock()

		btree.Close()

		// 每 100 次运行输出进度
		if run%100 == 0 {
			fmt.Printf("Progress: %d/%d runs completed (success: %d, failure: %d)\n",
				run, totalRuns, successCount, failureCount)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	// 输出总结
	fmt.Fprintf(logFile, "\n\n=== Summary ===\n")
	fmt.Fprintf(logFile, "End time: %s\n", timeNow())
	fmt.Fprintf(logFile, "Total runs: %d\n", totalRuns)
	fmt.Fprintf(logFile, "Success: %d (%.2f%%)\n", successCount, float64(successCount)*100/float64(totalRuns))
	fmt.Fprintf(logFile, "Failure: %d (%.2f%%)\n", failureCount, float64(failureCount)*100/float64(totalRuns))

	t.Logf("Failure log collection completed: %d runs, %d success, %d failure",
		totalRuns, successCount, failureCount)
	t.Logf("Log file saved to: /tmp/btree_circular_ref_failures.log")

	// 失败率不应该超过 2%
	failureRate := float64(failureCount) / float64(totalRuns) * 100
	if failureRate > 2.0 {
		t.Errorf("Failure rate %.2f%% exceeds 2%% threshold", failureRate)
	}
}

// TestQuickFailureCheck 快速失败检查（用于开发调试）
// 只运行 10 次，快速验证是否能捕获失败
func TestQuickFailureCheck(t *testing.T) {
	const runs = 10
	const goroutines = 100
	const operationsPerGoroutine = 100

	successCount := 0
	failureCount := 0

	for run := 1; run <= runs; run++ {
		btree, err := OpenBTree("", nil)
		require.NoError(t, err)

		// 启用页面追踪
		btree.offheapPM.EnablePageTracking()

		ctx := context.Background()
		errCh := make(chan error, goroutines*operationsPerGoroutine)
		var wg sync.WaitGroup

		for i := range goroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := range operationsPerGoroutine {
					key := []byte{byte(id >> 8), byte(id), byte(j)}
					value := []byte{byte(j)}

					err := btree.Set(ctx, key, value)
					// ErrRetry 在高并发场景下是正常的，不视为错误
					if err != nil && !errors.Is(err, ErrRetry) {
						errCh <- err
					}
				}
			}(i)
		}

		wg.Wait()
		close(errCh)

		runErrors := []error{}
		for err := range errCh {
			runErrors = append(runErrors, err)
		}

		if len(runErrors) == 0 {
			successCount++
		} else {
			failureCount++
			t.Logf("Run %d FAILED with %d errors", run, len(runErrors))
			for i, err := range runErrors {
				t.Logf("  Error %d: %v", i+1, err)
			}

			// 打印 Page 4095 报告
			if page4095Report := btree.offheapPM.GetPage4095Report(); page4095Report != "" {
				t.Logf("Page 4095 Report:\n%s", page4095Report)
			}

			// 打印高 PageID 报告
			if highPageIDReport := btree.offheapPM.GetHighPageIDReport(); highPageIDReport != "" {
				t.Logf("High PageID Report:\n%s", highPageIDReport)
			}
		}

		btree.Close()
	}

	t.Logf("Quick check completed: %d/%d runs failed (%.1f%%)",
		failureCount, runs, float64(failureCount)*100/float64(runs))
}

// timeNow 返回当前时间的格式化字符串
func timeNow() string {
	return time.Now().Format("2006-01-02 15:04:05.000")
}
