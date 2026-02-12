package framework

import (
	"context"
	"fmt"
	"time"

	"github.com/stretchr/testify/assert"
)

// E2EAssert E2E 测试断言辅助函数
type E2EAssert struct {
	t assert.TestingT
}

// NewE2EAssert 创建 E2E 断言器
func NewE2EAssert(t assert.TestingT) *E2EAssert {
	return &E2EAssert{t: t}
}

// DaemonRunning 断言 daemon 正在运行
func (a *E2EAssert) DaemonRunning(daemon *DaemonProcess, msgAndArgs ...interface{}) bool {
	return assert.True(a.t, daemon.IsRunning(), msgAndArgs...)
}

// DaemonStopped 断言 daemon 已停止
func (a *E2EAssert) DaemonStopped(daemon *DaemonProcess, msgAndArgs ...interface{}) bool {
	return assert.False(a.t, daemon.IsRunning(), msgAndArgs...)
}

// ClusterOnlineNodes 断言集群在线节点数
func (a *E2EAssert) ClusterOnlineNodes(cluster *TestCluster, expected int, msgAndArgs ...interface{}) bool {
	return assert.Equal(a.t, expected, cluster.OnlineNodeCount(), msgAndArgs...)
}

// CLICommandSuccess 断言 CLI 命令成功
func (a *E2EAssert) CLICommandSuccess(result *CLIResult, msgAndArgs ...interface{}) bool {
	if !assert.Equal(a.t, 0, result.ExitCode, msgAndArgs...) {
		return false
	}
	if result.Stderr != "" {
		return assert.Empty(a.t, result.Stderr, msgAndArgs...)
	}
	return true
}

// CLICommandContains 断言 CLI 输出包含指定内容
func (a *E2EAssert) CLICommandContains(result *CLIResult, substr string, msgAndArgs ...interface{}) bool {
	return assert.Contains(a.t, result.Stdout, substr, msgAndArgs...)
}

// Eventually 持续断言直到超时或成功
func (a *E2EAssert) Eventually(condition func() bool, waitFor time.Duration, tick time.Duration, msgAndArgs ...interface{}) bool {
	assert.Eventually(a.t, condition, waitFor, tick, msgAndArgs...)
	return true
}

// Never 持续断言在超时时间内永不满足
func (a *E2EAssert) Never(condition func() bool, waitFor time.Duration, tick time.Duration, msgAndArgs ...interface{}) bool {
	assert.Never(a.t, condition, waitFor, tick, msgAndArgs...)
	return true
}

// WaitForClusterStable 等待集群稳定
func (a *E2EAssert) WaitForClusterStable(ctx context.Context, cluster *TestCluster, timeout time.Duration) bool {
	err := cluster.WaitStable(ctx, timeout)
	return assert.NoError(a.t, err, "cluster should become stable")
}

// WaitForNodeOnline 等待节点上线
func (a *E2EAssert) WaitForNodeOnline(ctx context.Context, cluster *TestCluster, nodeID string, timeout time.Duration) bool {
	return a.Eventually(func() bool {
		node := cluster.findNode(nodeID)
		return node != nil && node.IsRunning()
	}, timeout, 1*time.Second, fmt.Sprintf("node %s should come online", nodeID))
}

// WaitForNodeOffline 等待节点下线
func (a *E2EAssert) WaitForNodeOffline(ctx context.Context, cluster *TestCluster, nodeID string, timeout time.Duration) bool {
	return a.Eventually(func() bool {
		node := cluster.findNode(nodeID)
		return node == nil || !node.IsRunning()
	}, timeout, 1*time.Second, fmt.Sprintf("node %s should go offline", nodeID))
}
