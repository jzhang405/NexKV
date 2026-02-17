// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DataDirManager 数据目录管理器
// 为每个测试创建独立的数据目录，支持自动清理
type DataDirManager struct {
	baseDir     string              // 基础目录
	testDirs    map[string][]string // testID -> []dirPath（支持重复创建）
	subDirs     []string            // 子目录列表
	mu          sync.RWMutex
	autoCleanup bool                // 是否自动清理
}

// NewDataDirManager 创建数据目录管理器
func NewDataDirManager(baseDir string) *DataDirManager {
	if baseDir == "" {
		baseDir = os.TempDir()
	}
	return &DataDirManager{
		baseDir:     baseDir,
		testDirs:    make(map[string][]string),
		subDirs:     []string{"data", "wal", "logs"},
		autoCleanup: true,
	}
}

// SetSubDirs 设置子目录列表
func (dm *DataDirManager) SetSubDirs(subDirs []string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.subDirs = subDirs
}

// CreateTestDir 为测试创建独立数据目录
func (dm *DataDirManager) CreateTestDir(testID string) (string, error) {
	// 验证 testID（防止路径遍历）
	if err := dm.validateTestID(testID); err != nil {
		return "", err
	}

	// 生成带时间戳的目录名
	timestamp := time.Now().Format("20060102-150405.000")
	testDir := filepath.Join(dm.baseDir, testID, timestamp)

	// 创建主目录
	if err := os.MkdirAll(testDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create test dir: %w", err)
	}

	// 创建子目录
	dm.mu.RLock()
	subDirs := dm.subDirs
	dm.mu.RUnlock()

	for _, subDir := range subDirs {
		path := filepath.Join(testDir, subDir)
		if err := os.MkdirAll(path, 0755); err != nil {
			return "", fmt.Errorf("failed to create subdir %s: %w", subDir, err)
		}
	}

	// 记录
	dm.mu.Lock()
	dm.testDirs[testID] = append(dm.testDirs[testID], testDir)
	dm.mu.Unlock()

	return testDir, nil
}

// CleanupTestDir 清理指定测试的数据目录
func (dm *DataDirManager) CleanupTestDir(testID string) error {
	dm.mu.Lock()
	dirs, exists := dm.testDirs[testID]
	delete(dm.testDirs, testID)
	dm.mu.Unlock()

	if !exists || len(dirs) == 0 {
		return nil // 不存在则忽略
	}

	// 删除所有相关目录
	var lastErr error
	for _, testDir := range dirs {
		if err := os.RemoveAll(testDir); err != nil {
			lastErr = fmt.Errorf("failed to cleanup test dir %s: %w", testDir, err)
		}
	}

	// 尝试删除父目录（如果为空）
	parentDir := filepath.Dir(dirs[0])
	os.Remove(parentDir) // 忽略错误，可能不为空

	return lastErr
}

// CleanupAll 清理所有测试目录
func (dm *DataDirManager) CleanupAll() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	var lastErr error
	for testID := range dm.testDirs {
		for _, testDir := range dm.testDirs[testID] {
			if err := os.RemoveAll(testDir); err != nil {
				lastErr = err
			}
		}
	}
	dm.testDirs = make(map[string][]string)

	return lastErr
}

// GetTestDir 获取指定测试的目录（返回最后一个）
func (dm *DataDirManager) GetTestDir(testID string) string {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	dirs := dm.testDirs[testID]
	if len(dirs) == 0 {
		return ""
	}
	return dirs[len(dirs)-1]
}

// ActiveCount 返回活跃测试目录数量
func (dm *DataDirManager) ActiveCount() int {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	count := 0
	for _, dirs := range dm.testDirs {
		count += len(dirs)
	}
	return count
}

// validateTestID 验证测试 ID（防止路径遍历攻击）
func (dm *DataDirManager) validateTestID(testID string) error {
	if testID == "" {
		return fmt.Errorf("testID cannot be empty")
	}

	// 检查路径遍历字符
	if strings.Contains(testID, "..") {
		return fmt.Errorf("testID contains invalid path traversal: %s", testID)
	}

	// 检查绝对路径
	if filepath.IsAbs(testID) {
		return fmt.Errorf("testID cannot be absolute path: %s", testID)
	}

	// 清理路径并比较
	cleaned := filepath.Clean(testID)
	if cleaned != testID {
		return fmt.Errorf("testID contains suspicious path elements: %s", testID)
	}

	return nil
}
