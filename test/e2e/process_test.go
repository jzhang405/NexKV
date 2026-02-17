// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"context"
	"fmt"
	"os/exec"
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
