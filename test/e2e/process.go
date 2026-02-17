// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
	config     ProcessConfig
	cmd        *exec.Cmd
	state      ProcessState
	startedAt  time.Time
	exitCode   int
	err        error
	stdout     io.Reader
	stderr     io.Reader
	mu         sync.RWMutex
	stateOnce  sync.Once     // 确保状态只更新一次
	stopped    atomic.Bool   // 标记是否已停止
	monitoring atomic.Bool   // 标记是否正在监控
	waitDone   chan struct{} // Wait 完成信号
}

// updateStateOnce 安全地更新进程状态（仅一次）
func (mp *ManagedProcess) updateStateOnce(newState ProcessState, err error) {
	mp.stateOnce.Do(func() {
		mp.mu.Lock()
		mp.state = newState
		if err != nil {
			mp.err = err
		}
		if mp.cmd.ProcessState != nil {
			mp.exitCode = mp.cmd.ProcessState.ExitCode()
		}
		mp.mu.Unlock()
	})
}

// ProcessManagerOption 进程管理器选项
type ProcessManagerOption func(*ProcessManager)

// WithStrictEnvCheck 启用严格环境变量检查
func WithStrictEnvCheck() ProcessManagerOption {
	return func(pm *ProcessManager) {
		pm.strictEnvCheck = true
	}
}

// ProcessManager 进程管理器
type ProcessManager struct {
	processes      map[string]*ManagedProcess
	logger         *log.Logger
	mu             sync.RWMutex
	strictEnvCheck bool // 严格模式下拒绝敏感环境变量
}

// NewProcessManager 创建进程管理器
func NewProcessManager(logger *log.Logger, opts ...ProcessManagerOption) *ProcessManager {
	if logger == nil {
		logger = log.New(os.Stderr, "[ProcessManager] ", log.LstdFlags)
	}
	pm := &ProcessManager{
		processes: make(map[string]*ManagedProcess),
		logger:    logger,
	}
	for _, opt := range opts {
		opt(pm)
	}
	return pm
}

// Start 启动进程
func (pm *ProcessManager) Start(config ProcessConfig) error {
	// 验证配置（防止命令注入）
	if err := pm.validateConfig(&config); err != nil {
		return err
	}

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
			Setpgid: true, // 创建新进程组
			Pgid:    0,    // 子进程成为进程组 leader
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
		waitDone:  make(chan struct{}),
	}

	pm.processes[config.ID] = process

	// 监控进程
	process.monitoring.Store(true)
	go pm.monitorProcess(config.ID, process)

	return nil
}

// Stop 停止进程（优雅停止）
func (pm *ProcessManager) Stop(ctx context.Context, id string) error {
	pm.mu.RLock()
	process, exists := pm.processes[id]
	pm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("process %s not found", id)
	}

	// 检查是否已停止
	if process.stopped.Load() {
		return nil
	}

	// 更新状态
	process.mu.Lock()
	if process.state != ProcessStateRunning {
		process.mu.Unlock()
		return nil
	}
	process.state = ProcessStateStopping
	process.mu.Unlock()

	// 如果 ctx 没有截止时间，使用配置的超时
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok && process.config.StopTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, process.config.StopTimeout)
		defer cancel()
	}

	pm.logger.Printf("Stopping process %s (PID: %d)", id, process.cmd.Process.Pid)

	// 发送中断信号
	if err := pm.sendInterruptSignal(process); err != nil {
		pm.logger.Printf("Failed to send interrupt signal: %v", err)
	}

	// 等待进程退出或超时（使用 waitDone channel 避免重复调用 Wait）
	select {
	case <-ctx.Done():
		// 上下文超时，强制杀死
		pm.logger.Printf("Process %s timeout, force killing", id)
		pm.forceKill(process)
		<-process.waitDone // 等待 monitorProcess 完成
		process.updateStateOnce(ProcessStateStopped, ctx.Err())
	case <-process.waitDone:
		// 进程已退出
		process.mu.RLock()
		exitErr := process.err
		process.mu.RUnlock()
		if exitErr != nil {
			pm.logger.Printf("Process %s exited with error: %v", id, exitErr)
		}
		// 状态已由 monitorProcess 更新
	}

	process.stopped.Store(true)
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
			if err := syscall.Kill(-process.cmd.Process.Pid, syscall.SIGKILL); err != nil {
				pm.logger.Printf("Failed to kill process group %d: %v", process.cmd.Process.Pid, err)
			}
		}
	}
	// 作为后备，直接杀死进程
	if err := process.cmd.Process.Kill(); err != nil {
		pm.logger.Printf("Failed to kill process: %v", err)
	}
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

// Close 清理进程管理器（停止所有进程）
func (pm *ProcessManager) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return pm.StopAll(ctx)
}

// validateConfig 验证进程配置（防止命令注入）
func (pm *ProcessManager) validateConfig(config *ProcessConfig) error {
	// 验证基本信息
	if config.ID == "" {
		return errors.New("process ID cannot be empty")
	}

	// 验证二进制路径
	if !filepath.IsAbs(config.Binary) {
		return fmt.Errorf("binary path must be absolute: %s", config.Binary)
	}
	if _, err := os.Stat(config.Binary); err != nil {
		return fmt.Errorf("binary not found: %s", config.Binary)
	}

	// 验证工作目录
	if config.WorkDir != "" {
		if _, err := os.Stat(config.WorkDir); err != nil {
			return fmt.Errorf("work directory invalid: %w", err)
		}
	}

	// 验证环境变量和参数
	if err := pm.validateEnvAndArgs(config.Env, config.Args); err != nil {
		return err
	}

	return nil
}

// validateEnvAndArgs 统一验证环境变量和命令行参数
func (pm *ProcessManager) validateEnvAndArgs(env map[string]string, args []string) error {
	// 验证环境变量
	if env != nil {
		sensitivePrefixes := []string{"API_KEY", "SECRET", "PASSWORD", "TOKEN", "PRIVATE"}

		for key := range env {
			// 验证键名格式（合并验证逻辑）
			if key == "" {
				return errors.New("env key cannot be empty")
			}
			if strings.ContainsAny(key, "=\x00") {
				return fmt.Errorf("env key contains invalid characters: %s", key)
			}

			// 检查敏感环境变量
			upperKey := strings.ToUpper(key)
			for _, prefix := range sensitivePrefixes {
				if strings.HasPrefix(upperKey, prefix) {
					if pm.strictEnvCheck {
						return fmt.Errorf("sensitive env var not allowed in strict mode: %s", key)
					}
					pm.logger.Printf("WARNING: Sensitive env var detected: %s", key)
					break // 只需记录一次
				}
			}
		}
	}

	// 验证命令行参数（记录可疑内容但不阻止）
	shellMetachars := []string{";", "|", "&", "$", "`", "(", ")", "<", ">", "\n"}
	for _, arg := range args {
		for _, char := range shellMetachars {
			if strings.Contains(arg, char) {
				pm.logger.Printf("WARNING: Arg contains shell metachar '%s': %s", char, arg)
				break // 每个 arg 只需记录一次
			}
		}
	}

	return nil
}

// monitorProcess 监控进程状态
// 接收 process 参数避免竞态条件
func (pm *ProcessManager) monitorProcess(id string, process *ManagedProcess) {
	err := process.cmd.Wait()

	// 判断退出原因
	var state ProcessState
	if err == nil {
		state = ProcessStateStopped
	} else if isSignalTermination(err) {
		// 被信号终止（SIGTERM/SIGKILL）视为正常停止
		state = ProcessStateStopped
	} else {
		state = ProcessStateFailed
	}

	// 使用 updateStateOnce 确保状态只更新一次（避免与 Stop 竞争）
	process.updateStateOnce(state, err)

	process.mu.RLock()
	exitCode := process.exitCode
	process.mu.RUnlock()

	pm.logger.Printf("Process %s exited with code %d", id, exitCode)
	process.monitoring.Store(false)

	// 通知 Wait 完成
	close(process.waitDone)
}

// isSignalTermination 判断是否为信号终止
func isSignalTermination(err error) bool {
	if err == nil {
		return false
	}

	// 检查是否为 signal: terminated 或 signal: killed
	errStr := err.Error()
	signalKeywords := []string{"signal:", "terminated", "killed"}
	for _, keyword := range signalKeywords {
		if strings.Contains(errStr, keyword) {
			return true
		}
	}
	return false
}
