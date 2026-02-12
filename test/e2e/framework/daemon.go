// Package framework 提供 E2E 测试框架
// 用于测试 nexkvd daemon 进程和 nexkv CLI 的端到端交互
package framework

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/stretchr/testify/require"
)

// DaemonProcess 表示一个 nexkvd daemon 进程
type DaemonProcess struct {
	// NodeID 节点唯一标识
	NodeID string
	// Addr 监听地址
	Addr string
	// ConfigPath 配置文件路径
	ConfigPath string
	// LogPath 日志文件路径
	LogPath string
	// PidPath PID 文件路径
	PidPath string
	// cmd 底层命令
	cmd *exec.Cmd
	// started 是否已启动
	started bool
	// mu 保护并发访问
	mu sync.RWMutex
}

// NewDaemonProcess 创建一个新的 daemon 进程
func NewDaemonProcess(nodeID, addr, configDir, logDir string) *DaemonProcess {
	return &DaemonProcess{
		NodeID:     nodeID,
		Addr:       addr,
		ConfigPath: filepath.Join(configDir, fmt.Sprintf("%s.yaml", nodeID)),
		LogPath:    filepath.Join(logDir, fmt.Sprintf("%s.log", nodeID)),
		PidPath:    filepath.Join(logDir, fmt.Sprintf("%s.pid", nodeID)),
	}
}

// Start 启动 daemon 进程
func (d *DaemonProcess) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.started {
		return fmt.Errorf("daemon %s already started", d.NodeID)
	}

	// 打开日志文件
	logFile, err := os.OpenFile(d.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	// 构建命令
	d.cmd = exec.CommandContext(ctx, "./nexkvd",
		"--config", d.ConfigPath,
		"--node-id", d.NodeID,
		"--addr", d.Addr,
	)

	// 设置标准输出和错误输出
	d.cmd.Stdout = logFile
	d.cmd.Stderr = logFile

	// 启动进程
	if err := d.cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// 等待进程启动
	if err := d.waitForStart(ctx); err != nil {
		d.cmd.Process.Kill()
		logFile.Close()
		return err
	}

	// 写入 PID
	if err := os.WriteFile(d.PidPath, []byte(fmt.Sprintf("%d", d.cmd.Process.Pid)), 0644); err != nil {
		d.cmd.Process.Kill()
		logFile.Close()
		return fmt.Errorf("failed to write pid file: %w", err)
	}

	d.started = true
	return nil
}

// Stop 停止 daemon 进程
func (d *DaemonProcess) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		return nil
	}

	if d.cmd != nil && d.cmd.Process != nil {
		// 发送 SIGTERM 信号
		if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("failed to send SIGTERM: %w", err)
		}

		// 等待进程退出
		done := make(chan error, 1)
		go func() {
			_, err := d.cmd.Process.Wait()
			done <- err
		}()

		select {
		case <-time.After(10 * time.Second):
			// 超时，强制杀死
			d.cmd.Process.Kill()
			return fmt.Errorf("daemon stop timeout")
		case err := <-done:
			if err != nil {
				return fmt.Errorf("daemon wait error: %w", err)
			}
		}
	}

	// 清理 PID 文件
	os.Remove(d.PidPath)

	d.started = false
	return nil
}

// IsRunning 检查进程是否运行
func (d *DaemonProcess) IsRunning() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.started || d.cmd == nil || d.cmd.Process == nil {
		return false
	}

	// 检查进程是否存在
	err := d.cmd.Process.Signal(syscall.Signal(0))
	return err == nil
}

// HealthCheck 执行健康检查
func (d *DaemonProcess) HealthCheck() error {
	if !d.IsRunning() {
		return fmt.Errorf("daemon %s is not running", d.NodeID)
	}

	// TODO: 实现真实的健康检查逻辑
	// 可以通过 RPC 调用检查节点状态
	return nil
}

// waitForStart 等待 daemon 启动完成
func (d *DaemonProcess) waitForStart(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("daemon start timeout")
		case <-ticker.C:
			if d.cmd.Process != nil {
				// 进程已创建，等待日志中出现启动成功标志
				// TODO: 检查日志文件中的启动成功标志
				return nil
			}
		}
	}
}

// RequireStart 在测试中启动 daemon，失败则终止测试
func (d *DaemonProcess) RequireStart(t require.TestingT, ctx context.Context) {
	require.NoError(t, d.Start(ctx), "failed to start daemon %s", d.NodeID)
}

// RequireStop 在测试中停止 daemon，失败则终止测试
func (d *DaemonProcess) RequireStop(t require.TestingT) {
	require.NoError(t, d.Stop(), "failed to stop daemon %s", d.NodeID)
}
