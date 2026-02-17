// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getSleepBinary 获取 sleep 命令的绝对路径
func getSleepBinary(t *testing.T) string {
	path, err := exec.LookPath("sleep")
	require.NoError(t, err, "sleep command should be available")
	return path
}

func TestProcessManager_StartAndStop(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要真实进程的测试")
	}

	pm := NewProcessManager(nil)

	config := ProcessConfig{
		ID:      "test-node-1",
		Binary:  getSleepBinary(t),
		Args:    []string{"10"},
		WorkDir: t.TempDir(),
	}

	// 启动进程
	err := pm.Start(config)
	require.NoError(t, err)

	// 验证进程状态
	status := pm.Status("test-node-1")
	assert.Equal(t, ProcessStateRunning, status.State)
	assert.Greater(t, status.PID, 0)

	// 停止进程
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = pm.Stop(ctx, "test-node-1")
	require.NoError(t, err)

	// 验证进程已停止
	status = pm.Status("test-node-1")
	assert.Equal(t, ProcessStateStopped, status.State)
}

func TestProcessManager_GracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要真实进程的测试")
	}

	pm := NewProcessManager(nil)

	config := ProcessConfig{
		ID:          "test-graceful",
		Binary:      getSleepBinary(t),
		Args:        []string{"30"},
		WorkDir:     t.TempDir(),
		StopTimeout: 2 * time.Second,
	}

	err := pm.Start(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err = pm.Stop(ctx, "test-graceful")
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 3*time.Second, "优雅停止应在超时时间内完成")
}

func TestProcessManager_StopAll(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要真实进程的测试")
	}

	pm := NewProcessManager(nil)

	// 启动多个进程
	sleepBin := getSleepBinary(t)
	for i := 1; i <= 3; i++ {
		config := ProcessConfig{
			ID:      fmt.Sprintf("test-node-%d", i),
			Binary:  sleepBin,
			Args:    []string{"10"},
			WorkDir: t.TempDir(),
		}
		err := pm.Start(config)
		require.NoError(t, err)
	}

	// 等待进程启动
	time.Sleep(100 * time.Millisecond)

	// 验证所有进程都在运行
	assert.Equal(t, 3, pm.ProcessCount())

	// 停止所有
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := pm.StopAll(ctx)
	require.NoError(t, err)

	// 等待进程停止
	time.Sleep(100 * time.Millisecond)

	// 验证所有进程都已停止（状态为 Stopped）
	for i := 1; i <= 3; i++ {
		status := pm.Status(fmt.Sprintf("test-node-%d", i))
		assert.Equal(t, ProcessStateStopped, status.State, "进程 test-node-%d 应已停止", i)
	}
}

func TestProcessManager_StartInvalidBinary(t *testing.T) {
	pm := NewProcessManager(nil)

	config := ProcessConfig{
		ID:      "test-invalid",
		Binary:  "/nonexistent/binary",
		Args:    []string{},
		WorkDir: t.TempDir(),
	}

	err := pm.Start(config)
	assert.Error(t, err)
	// 错误可能来自文件不存在检查或进程启动失败
	assert.Contains(t, err.Error(), "binary not found")
}

func TestProcessManager_StartDuplicate(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要真实进程的测试")
	}

	pm := NewProcessManager(nil)

	config := ProcessConfig{
		ID:      "test-dup",
		Binary:  getSleepBinary(t),
		Args:    []string{"10"},
		WorkDir: t.TempDir(),
	}

	// 第一次启动
	err := pm.Start(config)
	require.NoError(t, err)

	// 重复启动应该失败
	err = pm.Start(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// 清理
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = pm.Stop(ctx, "test-dup")
}

func TestProcessManager_StopNonExistent(t *testing.T) {
	pm := NewProcessManager(nil)

	ctx := context.Background()
	err := pm.Stop(ctx, "non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestProcessManager_StatusNonExistent(t *testing.T) {
	pm := NewProcessManager(nil)

	status := pm.Status("non-existent")
	assert.Equal(t, ProcessStateStopped, status.State)
	assert.Equal(t, 0, status.PID)
}

func TestProcessManager_ProcessCount(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要真实进程的测试")
	}

	pm := NewProcessManager(nil)

	assert.Equal(t, 0, pm.ProcessCount())

	// 启动进程
	config := ProcessConfig{
		ID:      "test-count",
		Binary:  getSleepBinary(t),
		Args:    []string{"10"},
		WorkDir: t.TempDir(),
	}
	err := pm.Start(config)
	require.NoError(t, err)

	assert.Equal(t, 1, pm.ProcessCount())

	// 停止进程
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = pm.Stop(ctx, "test-count")

	// 验证进程状态为 Stopped
	status := pm.Status("test-count")
	assert.Equal(t, ProcessStateStopped, status.State)
}

// =============================================================================
// 边界条件测试
// =============================================================================

func TestProcessManager_EmptyProcessID(t *testing.T) {
	pm := NewProcessManager(nil)

	config := ProcessConfig{
		ID:     "", // 空 ID
		Binary: getSleepBinary(t),
		Args:   []string{"1"},
	}

	err := pm.Start(config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be empty")
}

func TestProcessManager_SpecialCharProcessID(t *testing.T) {
	pm := NewProcessManager(nil)

	// 特殊字符 ID 应该被接受（只要路径验证通过）
	specialIDs := []string{
		"test-with-dash",
		"test_with_underscore",
		"test.with.dots",
	}

	sleepBin := getSleepBinary(t)
	for _, id := range specialIDs {
		t.Run(id, func(t *testing.T) {
			if testing.Short() {
				t.Skip("跳过需要真实进程的测试")
			}

			config := ProcessConfig{
				ID:      id,
				Binary:  sleepBin,
				Args:    []string{"1"},
				WorkDir: t.TempDir(),
			}

			err := pm.Start(config)
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = pm.Stop(ctx, id)
		})
	}
}

func TestProcessManager_EmptyEnvKey(t *testing.T) {
	pm := NewProcessManager(nil)

	config := ProcessConfig{
		ID:     "test-empty-env",
		Binary: getSleepBinary(t),
		Args:   []string{"1"},
		Env:    map[string]string{"": "value"}, // 空键名
	}

	err := pm.Start(config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "env key cannot be empty")
}

func TestProcessManager_InvalidEnvKey(t *testing.T) {
	pm := NewProcessManager(nil)

	invalidKeys := []string{"KEY=VALUE", "KEY\x00NAME"}

	for _, key := range invalidKeys {
		t.Run(fmt.Sprintf("key=%q", key), func(t *testing.T) {
			config := ProcessConfig{
				ID:     "test-invalid-env",
				Binary: getSleepBinary(t),
				Args:   []string{"1"},
				Env:    map[string]string{key: "value"},
			}

			err := pm.Start(config)
			require.Error(t, err)
		})
	}
}

func TestProcessManager_SensitiveEnvWarning(t *testing.T) {
	pm := NewProcessManager(nil)

	config := ProcessConfig{
		ID:     "test-sensitive-env",
		Binary: getSleepBinary(t),
		Args:   []string{"1"},
		Env:    map[string]string{"API_KEY": "secret123"},
	}

	// 默认模式下应该只是警告，不阻止
	err := pm.Start(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = pm.Stop(ctx, "test-sensitive-env")
}

func TestProcessManager_StrictEnvCheck(t *testing.T) {
	pm := NewProcessManager(nil, WithStrictEnvCheck())

	config := ProcessConfig{
		ID:     "test-strict-env",
		Binary: getSleepBinary(t),
		Args:   []string{"1"},
		Env:    map[string]string{"SECRET_KEY": "secret123"},
	}

	// 严格模式下应该拒绝
	err := pm.Start(config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sensitive env var not allowed")
}

// =============================================================================
// 并发安全测试
// =============================================================================

func TestProcessManager_ConcurrentStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要真实进程的测试")
	}

	pm := NewProcessManager(nil)
	sleepBin := getSleepBinary(t)

	const numGoroutines = 10
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines*2)

	// 并发启动和停止
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			processID := fmt.Sprintf("concurrent-%d", id)
			config := ProcessConfig{
				ID:      processID,
				Binary:  sleepBin,
				Args:    []string{"10"},
				WorkDir: t.TempDir(),
			}

			// 启动
			if err := pm.Start(config); err != nil {
				errCh <- fmt.Errorf("start failed for %s: %w", processID, err)
				return
			}

			// 立即停止
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := pm.Stop(ctx, processID); err != nil {
				errCh <- fmt.Errorf("stop failed for %s: %w", processID, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("并发操作失败: %v", err)
	}
}

func TestProcessManager_ConcurrentStatusQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要真实进程的测试")
	}

	pm := NewProcessManager(nil)
	sleepBin := getSleepBinary(t)

	// 启动一个进程
	config := ProcessConfig{
		ID:      "status-test",
		Binary:  sleepBin,
		Args:    []string{"5"},
		WorkDir: t.TempDir(),
	}
	require.NoError(t, pm.Start(config))

	const numQueries = 100
	var wg sync.WaitGroup

	// 并发查询状态
	for i := 0; i < numQueries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = pm.Status("status-test")
			_ = pm.ProcessCount()
		}()
	}

	wg.Wait()

	// 停止进程
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = pm.Stop(ctx, "status-test")
}

func TestProcessManager_StopTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要真实进程的测试")
	}

	pm := NewProcessManager(nil)

	// 设置很短的超时
	config := ProcessConfig{
		ID:          "test-timeout",
		Binary:      getSleepBinary(t),
		Args:        []string{"60"},
		WorkDir:     t.TempDir(),
		StopTimeout: 500 * time.Millisecond, // 500ms 超时
	}

	require.NoError(t, pm.Start(config))

	// 使用没有超时的 context，应该使用配置的 StopTimeout
	ctx := context.Background()
	start := time.Now()
	err := pm.Stop(ctx, "test-timeout")
	elapsed := time.Since(start)

	require.NoError(t, err)
	// 应该在配置的超时时间内完成（加上一些余量）
	assert.Less(t, elapsed, 2*time.Second, "应使用配置的 StopTimeout")
}
