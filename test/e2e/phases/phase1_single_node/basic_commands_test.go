// Package phase1 单节点基础测试
// 验证 nexkv CLI 基本命令的正确性
package phase1

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/test/e2e/framework"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDaemon 设置测试 daemon
func setupTestDaemon(t *testing.T) (*framework.DaemonProcess, *framework.CLIExecutor) {
	ctx := context.Background()

	// 创建并启动 daemon
	daemon := framework.NewDaemonProcess("test-cmd-node", "127.0.0.1:19200", "/tmp/nexkv-e2e/config", "/tmp/nexkv-e2e/logs")
	daemon.RequireStart(t, ctx)

	// 创建 CLI 执行器
	cli := framework.NewCLIExecutor(daemon.Addr)

	return daemon, cli
}

// TestClusterStatus 测试集群状态命令
func TestClusterStatus(t *testing.T) {
	daemon, cli := setupTestDaemon(t)
	defer daemon.RequireStop(t)

	ctx := context.Background()

	// 获取集群状态
	result, err := cli.ClusterStatus(ctx)
	require.NoError(t, err, "cluster status should succeed")

	// 验证结果
	assert.NotEmpty(t, result.ClusterID, "cluster ID should not be empty")
	assert.Equal(t, 1, result.NodeCount, "should have 1 node")
	assert.Equal(t, 1, result.OnlineNodeCount, "should have 1 online node")
}

// TestNodeList 测试节点列表命令
func TestNodeList(t *testing.T) {
	daemon, cli := setupTestDaemon(t)
	defer daemon.RequireStop(t)

	ctx := context.Background()

	// 获取节点列表
	result, err := cli.NodeList(ctx)
	require.NoError(t, err, "node list should succeed")

	// 验证结果
	assert.NotEmpty(t, result.Nodes, "should have at least one node")
	assert.Equal(t, "test-cmd-node", result.Nodes[0].NodeID, "node ID should match")
}

// TestClusterTopology 测试集群拓扑命令
func TestClusterTopology(t *testing.T) {
	daemon, cli := setupTestDaemon(t)
	defer daemon.RequireStop(t)

	ctx := context.Background()

	// 获取集群拓扑
	result, err := cli.ClusterTopology(ctx)
	require.NoError(t, err, "cluster topology should succeed")

	// 验证结果
	assert.NotEmpty(t, result.RootID, "root ID should not be empty")
}

// TestCLICommandTimeout 测试 CLI 命令超时
func TestCLICommandTimeout(t *testing.T) {
	daemon, cli := setupTestDaemon(t)
	defer daemon.RequireStop(t)

	ctx := context.Background()

	// 设置一个很短的超时
	cli.SetTimeout(1 * time.Millisecond)

	// 执行命令（应该超时）
	result := cli.Execute(ctx, "cluster", "status")

	// 验证超时
	assert.NotEqual(t, 0, result.ExitCode, "command should fail due to timeout")
}

// TestCLIInvalidCommand 测试无效命令
func TestCLIInvalidCommand(t *testing.T) {
	daemon, cli := setupTestDaemon(t)
	defer daemon.RequireStop(t)

	ctx := context.Background()

	// 执行无效命令
	result := cli.Execute(ctx, "invalid", "command")

	// 验证失败
	assert.NotEqual(t, 0, result.ExitCode, "invalid command should fail")
	assert.NotEmpty(t, result.Stderr, "should have error message")
}

// TestCLIMissingRequiredArg 测试缺少必需参数
func TestCLIMissingRequiredArg(t *testing.T) {
	daemon, cli := setupTestDaemon(t)
	defer daemon.RequireStop(t)

	ctx := context.Background()

	// node add 命令缺少必需参数
	_, err := cli.NodeAdd(ctx, "", "", 0)

	// 验证失败
	assert.Error(t, err, "command should fail with missing required args")
}

// TestConcurrentCLICommands 测试并发 CLI 命令
func TestConcurrentCLICommands(t *testing.T) {
	daemon, cli := setupTestDaemon(t)
	defer daemon.RequireStop(t)

	ctx := context.Background()

	// 并发执行多个命令
	const concurrency = 10
	results := make(chan *framework.CLIResult, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			results <- cli.Execute(ctx, "cluster", "status")
		}()
	}

	// 收集结果
	successCount := 0
	for i := 0; i < concurrency; i++ {
		result := <-results
		if result.ExitCode == 0 {
			successCount++
		}
	}

	// 验证所有命令都成功
	assert.Equal(t, concurrency, successCount, "all concurrent commands should succeed")
}

// TestCLIDaemonConnection 测试 CLI 与 daemon 连接
func TestCLIDaemonConnection(t *testing.T) {
	daemon, cli := setupTestDaemon(t)
	defer daemon.RequireStop(t)

	ctx := context.Background()

	// 验证 CLI 可以连接到 daemon
	status, err := cli.ClusterStatus(ctx)
	require.NoError(t, err)

	// 验证集群状态正常
	assert.NotEmpty(t, status.ClusterID, "cluster ID should not be empty")
	assert.Equal(t, 1, status.OnlineNodeCount, "should have 1 online node")
}
