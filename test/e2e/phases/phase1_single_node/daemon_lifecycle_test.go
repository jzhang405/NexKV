// Package phase1 单节点基础测试
// 验证 nexkvd daemon 进程的生命周期管理
package phase1

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/test/e2e/framework"
	"github.com/stretchr/testify/assert"
)

// TestDaemon_StartStop 测试 daemon 启动和停止
func TestDaemon_StartStop(t *testing.T) {
	ctx := context.Background()

	// 创建 daemon 进程
	daemon := framework.NewDaemonProcess("test-node-1", "127.0.0.1:19100", "/tmp/nexkv-e2e/config", "/tmp/nexkv-e2e/logs")

	// 启动 daemon
	daemon.RequireStart(t, ctx)
	defer daemon.RequireStop(t)

	// 验证 daemon 正在运行
	assert.True(t, daemon.IsRunning(), "daemon should be running")

	// 验证健康检查通过
	assert.NoError(t, daemon.HealthCheck(), "health check should pass")
}

// TestDaemon_GracefulShutdown 测试优雅关闭
func TestDaemon_GracefulShutdown(t *testing.T) {
	ctx := context.Background()

	daemon := framework.NewDaemonProcess("test-node-2", "127.0.0.1:19101", "/tmp/nexkv-e2e/config", "/tmp/nexkv-e2e/logs")

	daemon.RequireStart(t, ctx)

	// 记录启动时间
	startTime := time.Now()

	// 停止 daemon
	daemon.RequireStop(t)

	// 验证停止时间合理（应该快速停止，小于 5 秒）
	stopDuration := time.Since(startTime)
	assert.Less(t, stopDuration, 5*time.Second, "graceful shutdown should complete quickly")

	// 验证 daemon 已停止
	assert.False(t, daemon.IsRunning(), "daemon should be stopped")
}

// TestDaemon_MultipleStart 测试多次启动
func TestDaemon_MultipleStart(t *testing.T) {
	ctx := context.Background()

	daemon := framework.NewDaemonProcess("test-node-3", "127.0.0.1:19102", "/tmp/nexkv-e2e/config", "/tmp/nexkv-e2e/logs")

	daemon.RequireStart(t, ctx)
	defer daemon.RequireStop(t)

	// 第二次启动应该失败
	err := daemon.Start(ctx)
	assert.Error(t, err, "starting an already running daemon should fail")
}

// TestDaemon_MultipleStop 测试多次停止
func TestDaemon_MultipleStop(t *testing.T) {
	ctx := context.Background()

	daemon := framework.NewDaemonProcess("test-node-4", "127.0.0.1:19103", "/tmp/nexkv-e2e/config", "/tmp/nexkv-e2e/logs")

	daemon.RequireStart(t, ctx)

	// 第一次停止
	daemon.RequireStop(t)

	// 第二次停止应该成功（幂等）
	err := daemon.Stop()
	assert.NoError(t, err, "stopping an already stopped daemon should succeed")
}

// TestDaemon_StartTimeout 测试启动超时
func TestDaemon_StartTimeout(t *testing.T) {
	// 使用一个会立即失败的配置
	// TODO: 实现真实场景
	t.Skip("需要实现启动超时场景")
}

// TestDaemon_ConfigFileNotFound 测试配置文件不存在
func TestDaemon_ConfigFileNotFound(t *testing.T) {
	ctx := context.Background()

	// 创建一个指向不存在的配置文件的 daemon
	daemon := framework.NewDaemonProcess("test-node-invalid", "127.0.0.1:19199", "/tmp/nonexistent", "/tmp/nexkv-e2e/logs")

	// 启动应该失败
	err := daemon.Start(ctx)
	assert.Error(t, err, "daemon should fail to start with missing config file")
}

// TestDaemon_LogFileCreation 测试日志文件创建
func TestDaemon_LogFileCreation(t *testing.T) {
	ctx := context.Background()

	daemon := framework.NewDaemonProcess("test-node-log", "127.0.0.1:19104", "/tmp/nexkv-e2e/config", "/tmp/nexkv-e2e/logs")

	daemon.RequireStart(t, ctx)
	defer daemon.RequireStop(t)

	// 验证日志文件存在
	// TODO: 实现日志文件验证
}

// TestDaemon_PidFileCreation 测试 PID 文件创建
func TestDaemon_PidFileCreation(t *testing.T) {
	ctx := context.Background()

	daemon := framework.NewDaemonProcess("test-node-pid", "127.0.0.1:19105", "/tmp/nexkv-e2e/config", "/tmp/nexkv-e2e/logs")

	daemon.RequireStart(t, ctx)
	defer daemon.RequireStop(t)

	// 验证 PID 文件存在且内容正确
	// TODO: 实现 PID 文件验证
}
