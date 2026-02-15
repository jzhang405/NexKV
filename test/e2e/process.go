// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
)

// ProcessState 进程状态
type ProcessState int

const (
	ProcessStateStopped ProcessState = iota
	ProcessStateStarting
	ProcessStateRunning
	ProcessStateStopping
	ProcessStateFailed
)

func (s ProcessState) String() string {
	switch s {
	case ProcessStateStopped:
		return "stopped"
	case ProcessStateStarting:
		return "starting"
	case ProcessStateRunning:
		return "running"
	case ProcessStateStopping:
		return "stopping"
	case ProcessStateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// ProcessConfig 进程配置
type ProcessConfig struct {
	ID          string            // 进程标识
	Binary      string            // 可执行文件路径
	Args        []string          // 命令行参数
	Env         map[string]string // 环境变量
	WorkDir     string            // 工作目录
	StopTimeout time.Duration     // 停止超时（默认 10s）
}

// ProcessStatus 进程状态信息
type ProcessStatus struct {
	ID        string
	State     ProcessState
	PID       int
	StartedAt time.Time
	ExitCode  int
	Error     error
}

// ManagedProcess 管理的进程
type ManagedProcess struct {
	config    ProcessConfig
	cmd       *exec.Cmd
	state     ProcessState
	startedAt time.Time
	exitCode  int
	err       error
	stdout    io.Reader
	stderr    io.Reader
	mu        sync.RWMutex
}

// ProcessManager 进程管理器
type ProcessManager struct {
	processes map[string]*ManagedProcess
	logger    *log.Logger
	mu        sync.RWMutex
}

// NewProcessManager 创建进程管理器
func NewProcessManager(logger *log.Logger) *ProcessManager {
	if logger == nil {
		logger = log.New(os.Stderr, "[ProcessManager] ", log.LstdFlags)
	}
	return &ProcessManager{
		processes: make(map[string]*ManagedProcess),
		logger:    logger,
	}
}

// Start 启动进程
func (pm *ProcessManager) Start(config ProcessConfig) error {
	if config.StopTimeout == 0 {
		config.StopTimeout = 10 * time.Second
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, exists := pm.processes[config.ID]; exists {
		return fmt.Errorf("process %s already exists", config.ID)
	}

	// 创建命令
	cmd := exec.Command(config.Binary, config.Args...)
	cmd.Dir = config.WorkDir

	// 设置进程组（用于优雅停止）- 仅 Unix 系统
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,  // 创建新进程组
			Pgid:    0,     // 子进程成为进程组 leader
		}
	}

	// 设置环境变量
	if len(config.Env) > 0 {
		env := os.Environ()
		for k, v := range config.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}

	// 创建管道
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// 启动进程
	pm.logger.Printf("Starting process %s: %s %v", config.ID, config.Binary, config.Args)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	// 记录进程
	process := &ManagedProcess{
		config:    config,
		cmd:       cmd,
		state:     ProcessStateRunning,
		startedAt: time.Now(),
		stdout:    stdout,
		stderr:    stderr,
	}

	pm.processes[config.ID] = process

	// 监控进程
	go pm.monitorProcess(config.ID)

	return nil
}

// Stop 停止进程（优雅停止）
func (pm *ProcessManager) Stop(ctx context.Context, id string) error {
	pm.mu.Lock()
	process, exists := pm.processes[id]
	if !exists {
		pm.mu.Unlock()
		return fmt.Errorf("process %s not found", id)
	}

	// 更新状态
	process.mu.Lock()
	if process.state != ProcessStateRunning {
		process.mu.Unlock()
		pm.mu.Unlock()
		return nil
	}
	process.state = ProcessStateStopping
	process.mu.Unlock()

	pm.mu.Unlock()

	pm.logger.Printf("Stopping process %s (PID: %d)", id, process.cmd.Process.Pid)

	// 发送中断信号
	if err := pm.sendInterruptSignal(process); err != nil {
		pm.logger.Printf("Failed to send interrupt signal: %v", err)
	}

	// 等待进程退出或超时
	done := make(chan error, 1)
	go func() {
		done <- process.cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// 上下文超时，强制杀死
		pm.logger.Printf("Process %s timeout, force killing", id)
		pm.forceKill(process)
		<-done
	case err := <-done:
		if err != nil {
			pm.logger.Printf("Process %s exited with error: %v", id, err)
		}
	}

	// 更新状态
	process.mu.Lock()
	process.state = ProcessStateStopped
	if process.cmd.ProcessState != nil {
		process.exitCode = process.cmd.ProcessState.ExitCode()
	}
	process.mu.Unlock()

	return nil
}

// sendInterruptSignal 发送中断信号
func (pm *ProcessManager) sendInterruptSignal(process *ManagedProcess) error {
	if runtime.GOOS == "windows" {
		// Windows 不支持 SIGTERM，使用 Interrupt
		return process.cmd.Process.Signal(os.Interrupt)
	}
	// Unix 系统使用 SIGTERM
	return process.cmd.Process.Signal(syscall.SIGTERM)
}

// forceKill 强制杀死进程
func (pm *ProcessManager) forceKill(process *ManagedProcess) {
	if runtime.GOOS != "windows" {
		// Unix 系统：杀死整个进程组
		if process.cmd.Process != nil {
			// 使用负 PID 杀死进程组
			syscall.Kill(-process.cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	// 作为后备，直接杀死进程
	process.cmd.Process.Kill()
}

// StopAll 停止所有进程
func (pm *ProcessManager) StopAll(ctx context.Context) error {
	pm.mu.RLock()
	ids := make([]string, 0, len(pm.processes))
	for id := range pm.processes {
		ids = append(ids, id)
	}
	pm.mu.RUnlock()

	var lastErr error
	for _, id := range ids {
		if err := pm.Stop(ctx, id); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// Status 获取进程状态
func (pm *ProcessManager) Status(id string) ProcessStatus {
	pm.mu.RLock()
	process, exists := pm.processes[id]
	pm.mu.RUnlock()

	if !exists {
		return ProcessStatus{ID: id, State: ProcessStateStopped}
	}

	process.mu.RLock()
	defer process.mu.RUnlock()

	pid := 0
	if process.cmd != nil && process.cmd.Process != nil {
		pid = process.cmd.Process.Pid
	}

	return ProcessStatus{
		ID:        id,
		State:     process.state,
		PID:       pid,
		StartedAt: process.startedAt,
		ExitCode:  process.exitCode,
		Error:     process.err,
	}
}

// ProcessCount 返回管理的进程数量
func (pm *ProcessManager) ProcessCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.processes)
}

// monitorProcess 监控进程状态
func (pm *ProcessManager) monitorProcess(id string) {
	pm.mu.RLock()
	process, exists := pm.processes[id]
	pm.mu.RUnlock()

	if !exists {
		return
	}

	err := process.cmd.Wait()

	process.mu.Lock()
	if err != nil {
		process.state = ProcessStateFailed
		process.err = err
	} else {
		process.state = ProcessStateStopped
	}
	if process.cmd.ProcessState != nil {
		process.exitCode = process.cmd.ProcessState.ExitCode()
	}
	process.mu.Unlock()

	pm.logger.Printf("Process %s exited with code %d", id, process.exitCode)
}
