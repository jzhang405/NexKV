// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataDirManager_CreateTestDir(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())

	testDir, err := manager.CreateTestDir("test-001")
	require.NoError(t, err)

	// 验证目录存在
	_, err = os.Stat(testDir)
	assert.NoError(t, err)

	// 验证子目录结构
	subDirs := []string{"data", "wal", "logs"}
	for _, subDir := range subDirs {
		path := filepath.Join(testDir, subDir)
		_, err := os.Stat(path)
		assert.NoError(t, err, "子目录 %s 应存在", subDir)
	}
}

func TestDataDirManager_CreateTestDir_CustomSubDirs(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())
	manager.SetSubDirs([]string{"custom1", "custom2"})

	testDir, err := manager.CreateTestDir("test-custom")
	require.NoError(t, err)

	// 验证自定义子目录
	for _, subDir := range []string{"custom1", "custom2"} {
		path := filepath.Join(testDir, subDir)
		_, err := os.Stat(path)
		assert.NoError(t, err, "子目录 %s 应存在", subDir)
	}

	// 默认子目录不应存在
	_, err = os.Stat(filepath.Join(testDir, "data"))
	assert.True(t, os.IsNotExist(err), "默认子目录 data 不应存在")
}

func TestDataDirManager_CleanupTestDir(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())

	testDir, err := manager.CreateTestDir("test-cleanup")
	require.NoError(t, err)

	// 验证目录存在
	_, err = os.Stat(testDir)
	require.NoError(t, err)

	// 清理目录
	err = manager.CleanupTestDir("test-cleanup")
	require.NoError(t, err)

	// 验证目录已删除
	_, err = os.Stat(testDir)
	assert.True(t, os.IsNotExist(err), "目录应被删除")
}

func TestDataDirManager_CleanupTestDir_NotExist(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())

	// 清理不存在的目录应该成功
	err := manager.CleanupTestDir("not-exist")
	assert.NoError(t, err)
}

func TestDataDirManager_CleanupAll(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())

	// 创建多个测试目录
	_, err := manager.CreateTestDir("test-1")
	require.NoError(t, err)
	_, err = manager.CreateTestDir("test-2")
	require.NoError(t, err)
	_, err = manager.CreateTestDir("test-3")
	require.NoError(t, err)

	assert.Equal(t, 3, manager.ActiveCount())

	// 清理所有
	err = manager.CleanupAll()
	require.NoError(t, err)

	assert.Equal(t, 0, manager.ActiveCount())
}

func TestDataDirManager_GetTestDir(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())

	testDir, err := manager.CreateTestDir("test-get")
	require.NoError(t, err)

	// 获取已创建的目录
	dir := manager.GetTestDir("test-get")
	assert.Equal(t, testDir, dir)

	// 获取不存在的目录
	dir = manager.GetTestDir("not-exist")
	assert.Empty(t, dir)
}

func TestDataDirManager_ActiveCount(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())

	assert.Equal(t, 0, manager.ActiveCount())

	_, err := manager.CreateTestDir("test-1")
	require.NoError(t, err)
	assert.Equal(t, 1, manager.ActiveCount())

	_, err = manager.CreateTestDir("test-2")
	require.NoError(t, err)
	assert.Equal(t, 2, manager.ActiveCount())

	err = manager.CleanupTestDir("test-1")
	require.NoError(t, err)
	assert.Equal(t, 1, manager.ActiveCount())
}

func TestDataDirManager_DuplicateCreate(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())

	// 第一次创建
	dir1, err := manager.CreateTestDir("test-dup")
	require.NoError(t, err)

	// 等待一小段时间确保时间戳不同
	time.Sleep(2 * time.Millisecond)

	// 重复创建应该返回不同目录（带时间戳）
	dir2, err := manager.CreateTestDir("test-dup")
	require.NoError(t, err)

	// 两个目录应该不同
	assert.NotEqual(t, dir1, dir2, "重复创建应返回不同目录")

	// ActiveCount 应该为 2（两次创建都记录了）
	assert.Equal(t, 2, manager.ActiveCount())
}

// =============================================================================
// 路径遍历安全测试
// =============================================================================

func TestDataDirManager_PathTraversalProtection(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())

	maliciousIDs := []struct {
		id       string
		expected error
	}{
		{"../../../etc/passwd", ErrPathTraversal},
		{"..\\..\\windows", ErrPathTraversal},
		{"/etc/passwd", ErrAbsolutePath},
		{"test/../..", ErrPathTraversal}, // 包含 ".."
		{"test/../../../tmp", ErrPathTraversal},
		{"", ErrEmptyTestID},
		{"/absolute/path", ErrAbsolutePath},
		{"./relative", ErrSuspiciousPath},
		{"test/.", ErrSuspiciousPath},
		{"sub/test", ErrSuspiciousPath}, // 包含路径分隔符
	}

	for _, tc := range maliciousIDs {
		t.Run(fmt.Sprintf("id=%q", tc.id), func(t *testing.T) {
			_, err := manager.CreateTestDir(tc.id)
			require.Error(t, err, "应拒绝恶意 testID: %s", tc.id)
			assert.True(t, errors.Is(err, tc.expected),
				"错误类型应为 %v，实际: %v", tc.expected, err)
		})
	}
}

func TestDataDirManager_ValidTestIDs(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())

	validIDs := []string{
		"test-001",
		"my_test",
		"test123",
		"TEST-UPPERCASE",
		"test.with.dots",
		"test-with-dashes",
	}

	for _, id := range validIDs {
		t.Run(id, func(t *testing.T) {
			_, err := manager.CreateTestDir(id)
			require.NoError(t, err, "应接受有效 testID: %s", id)
		})
	}
}

// =============================================================================
// 并发安全测试
// =============================================================================

func TestDataDirManager_ConcurrentAccess(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())

	const numGoroutines = 50
	const numOpsPerGoroutine = 5

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*numOpsPerGoroutine)

	// 并发创建
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				testID := fmt.Sprintf("concurrent-%d-%d", id, j)
				_, err := manager.CreateTestDir(testID)
				if err != nil {
					errors <- fmt.Errorf("create failed: %w", err)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// 检查错误
	for err := range errors {
		t.Errorf("并发操作失败: %v", err)
	}

	expectedCount := numGoroutines * numOpsPerGoroutine
	assert.Equal(t, expectedCount, manager.ActiveCount(),
		"应创建 %d 个目录", expectedCount)

	// 并发清理
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				testID := fmt.Sprintf("concurrent-%d-%d", id, j)
				_ = manager.CleanupTestDir(testID)
			}
		}(i)
	}

	wg.Wait()
	assert.Equal(t, 0, manager.ActiveCount(), "所有目录应被清理")
}

func TestDataDirManager_ConcurrentCreateAndCleanup(t *testing.T) {
	manager := NewDataDirManager(t.TempDir())

	const numWorkers = 20
	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// 创建 worker
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			counter := 0
			for {
				select {
				case <-stopCh:
					return
				default:
					testID := fmt.Sprintf("worker-%d-test-%d", id, counter)
					_, err := manager.CreateTestDir(testID)
					if err == nil {
						// 稍后清理
						time.Sleep(time.Microsecond)
						_ = manager.CleanupTestDir(testID)
					}
					counter++
				}
			}
		}(i)
	}

	// 运行一段时间
	time.Sleep(100 * time.Millisecond)
	close(stopCh)
	wg.Wait()
}
