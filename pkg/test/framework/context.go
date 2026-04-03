package framework

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/panjf2000/ants/v2"
)

// DefaultGoroutinePoolSize 默认 goroutine 池大小
const DefaultGoroutinePoolSize = 100

// IsolationLevel 隔离级别
type IsolationLevel int

const (
	// IsolationNone 无隔离（共享全局状态）
	IsolationNone IsolationLevel = iota
	// IsolationProcess 进程级隔离（独立进程）
	IsolationProcess
	// IsolationContainer 容器级隔离（Docker 容器）
	IsolationContainer
)

// String 返回隔离级别的字符串表示
func (l IsolationLevel) String() string {
	switch l {
	case IsolationNone:
		return "none"
	case IsolationProcess:
		return "process"
	case IsolationContainer:
		return "container"
	default:
		return "unknown"
	}
}

// TestContext 测试上下文，支持测试隔离
// 每个测试用例应创建独立的 TestContext
//
// v2.12 更新：集成 ants 并发控制（同步 Pre v1.6）
type TestContext struct {
	// 组件注册表
	Registry *ComponentRegistry

	// 并发控制（v2.12 新增）
	GoroutinePool *ants.Pool // 高性能任务池（复用 goroutine）

	// 测试资源
	TempDir        string
	IsolationLevel IsolationLevel

	// 清理管理
	cleanupFuncs []func()
	cleanupMu    sync.Mutex
}

// NewTestContext 创建测试上下文（默认无隔离）
func NewTestContext() (*TestContext, error) {
	return newTestContext(IsolationNone, 0)
}

// NewTestContextWithSeed 创建指定随机种子的测试上下文（用于可重复测试）
func NewTestContextWithSeed(seed int64) (*TestContext, error) {
	ctx, err := newTestContext(IsolationNone, seed)
	if err != nil {
		return nil, err
	}
	// 使用种子重新生成临时目录
	if ctx.TempDir, err = generateTempDirWithSeed(seed); err != nil {
		ctx.Close()
		return nil, errors.Wrap(err, "failed to create temp directory")
	}
	return ctx, nil
}

// NewTestContextWithIsolation 创建指定隔离级别的测试上下文
func NewTestContextWithIsolation(level IsolationLevel) (*TestContext, error) {
	return newTestContext(level, 0)
}

// newTestContext 内部构造函数，统一处理公共初始化逻辑
func newTestContext(level IsolationLevel, _ int64) (*TestContext, error) {
	goroutinePool, err := ants.NewPool(DefaultGoroutinePoolSize, ants.WithPreAlloc(true))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create goroutine pool")
	}

	tempDir, err := generateTempDir()
	if err != nil {
		goroutinePool.Release()
		return nil, errors.Wrap(err, "failed to create temp directory")
	}

	return &TestContext{
		Registry:       NewComponentRegistry(),
		GoroutinePool:  goroutinePool,
		TempDir:        tempDir,
		IsolationLevel: level,
	}, nil
}

// SubmitTask 提交任务到 goroutine 池
// 使用 ants 池执行异步任务
func (ctx *TestContext) SubmitTask(task func()) error {
	return ctx.GoroutinePool.Submit(task)
}

// SubmitTaskWithError 提交带错误返回的任务到 goroutine 池
// 使用 channel 同步等待任务完成并返回错误
func (ctx *TestContext) SubmitTaskWithError(task func() error) error {
	errCh := make(chan error, 1)
	if err := ctx.GoroutinePool.Submit(func() { errCh <- task() }); err != nil {
		return err
	}
	return <-errCh
}

// AddCleanup 添加清理函数
// 清理函数会在 Cleanup() 调用时按LIFO 顺序执行
func (ctx *TestContext) AddCleanup(fn func()) {
	ctx.cleanupMu.Lock()
	defer ctx.cleanupMu.Unlock()
	ctx.cleanupFuncs = append(ctx.cleanupFuncs, fn)
}

// Cleanup 执行所有清理函数（LIFO 顺序）
// 捕获所有 panic 确保所有清理函数都能执行
func (ctx *TestContext) Cleanup() {
	ctx.cleanupMu.Lock()
	defer ctx.cleanupMu.Unlock()

	var panics []any

	// LIFO 顺序执行，捕获每个清理函数的 panic
	for i := len(ctx.cleanupFuncs) - 1; i >= 0; i-- {
		func() {
			defer func() {
				if r := recover(); r != nil {
					panics = append(panics, r)
				}
			}()
			ctx.cleanupFuncs[i]()
		}()
	}
	ctx.cleanupFuncs = nil

	// 重新抛出第一个 panic（保持原有行为）
	if len(panics) > 0 {
		panic(fmt.Sprintf("cleanup panics: %v", panics))
	}
}

// Close 关闭测试上下文并释放所有资源
func (ctx *TestContext) Close() error {
	// 执行清理函数
	ctx.Cleanup()

	// 释放 goroutine 池
	if ctx.GoroutinePool != nil {
		ctx.GoroutinePool.Release()
		ctx.GoroutinePool = nil
	}

	// 清理临时目录
	if ctx.TempDir != "" {
		os.RemoveAll(ctx.TempDir)
		ctx.TempDir = ""
	}

	return nil
}

// generateTempDir 生成临时目录
// P0 修复：返回错误以便调用者处理
func generateTempDir() (string, error) {
	baseDir := os.Getenv("NEXKV_TEST_BASE_DIR")
	if baseDir == "" {
		baseDir = os.TempDir()
	}

	// 使用UUIDv7 风格的 test-id
	testID := fmt.Sprintf("nexkv-test-%d-%d", time.Now().UnixNano(), os.Getpid())
	tempDir := filepath.Join(baseDir, testID)

	// 创建目录（检查错误）
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", errors.Wrap(err, fmt.Sprintf("failed to create temp directory %s", tempDir))
	}

	return tempDir, nil
}

// generateTempDirWithSeed 使用种子生成临时目录（可重复）
// P0 修复：返回错误以便调用者处理
func generateTempDirWithSeed(seed int64) (string, error) {
	baseDir := os.Getenv("NEXKV_TEST_BASE_DIR")
	if baseDir == "" {
		baseDir = os.TempDir()
	}

	testID := fmt.Sprintf("nexkv-test-seed-%d", seed)
	tempDir := filepath.Join(baseDir, testID)

	// 创建目录（检查错误）
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", errors.Wrap(err, fmt.Sprintf("failed to create temp directory %s", tempDir))
	}

	return tempDir, nil
}
