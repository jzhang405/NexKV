package framework

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// IsolationLevel 测试
// ==========================================

func TestIsolationLevel_String(t *testing.T) {
	tests := []struct {
		level    IsolationLevel
		expected string
	}{
		{IsolationNone, "none"},
		{IsolationProcess, "process"},
		{IsolationContainer, "container"},
		{IsolationLevel(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.level.String())
		})
	}
}

// ==========================================
// TestContext 测试
// ==========================================

func TestNewTestContext(t *testing.T) {
	ctx, err := NewTestContext()
	require.NoError(t, err)
	defer ctx.Close()

	assert.NotNil(t, ctx.Registry)
	assert.NotNil(t, ctx.GoroutinePool)
	assert.NotEmpty(t, ctx.TempDir)
	assert.Equal(t, IsolationNone, ctx.IsolationLevel)

	// 验证临时目录存在
	_, err = os.Stat(ctx.TempDir)
	assert.NoError(t, err)
}

func TestNewTestContextWithSeed(t *testing.T) {
	ctx, err := NewTestContextWithSeed(12345)
	require.NoError(t, err)
	defer ctx.Close()

	assert.NotNil(t, ctx.Registry)
	assert.NotNil(t, ctx.GoroutinePool)
	assert.NotEmpty(t, ctx.TempDir)
	assert.Contains(t, ctx.TempDir, "seed-12345")
}

func TestNewTestContextWithIsolation(t *testing.T) {
	ctx, err := NewTestContextWithIsolation(IsolationProcess)
	require.NoError(t, err)
	defer ctx.Close()

	assert.Equal(t, IsolationProcess, ctx.IsolationLevel)
}

func TestTestContext_SubmitTask(t *testing.T) {
	ctx, err := NewTestContext()
	require.NoError(t, err)
	defer ctx.Close()

	var executed bool
	var mu sync.Mutex

	err = ctx.SubmitTask(func() {
		mu.Lock()
		executed = true
		mu.Unlock()
	})
	assert.NoError(t, err)

	// 等待任务执行
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	assert.True(t, executed)
	mu.Unlock()
}

func TestTestContext_SubmitTaskWithError(t *testing.T) {
	ctx, err := NewTestContext()
	require.NoError(t, err)
	defer ctx.Close()

	// 成功的任务
	err = ctx.SubmitTaskWithError(func() error {
		return nil
	})
	assert.NoError(t, err)

	// 失败的任务
	expectedErr := assert.AnError
	err = ctx.SubmitTaskWithError(func() error {
		return expectedErr
	})
	assert.Error(t, err)
}

func TestTestContext_AddCleanup(t *testing.T) {
	ctx, err := NewTestContext()
	require.NoError(t, err)

	var cleanupOrder []int
	ctx.AddCleanup(func() { cleanupOrder = append(cleanupOrder, 1) })
	ctx.AddCleanup(func() { cleanupOrder = append(cleanupOrder, 2) })
	ctx.AddCleanup(func() { cleanupOrder = append(cleanupOrder, 3) })

	ctx.Cleanup()

	// LIFO 顺序
	assert.Equal(t, []int{3, 2, 1}, cleanupOrder)
}

func TestTestContext_Cleanup_Panic(t *testing.T) {
	ctx, err := NewTestContext()
	require.NoError(t, err)

	ctx.AddCleanup(func() { panic("cleanup panic 1") })
	ctx.AddCleanup(func() {})

	// 应该捕获 panic
	assert.Panics(t, func() {
		ctx.Cleanup()
	})
}

func TestTestContext_Close(t *testing.T) {
	ctx, err := NewTestContext()
	require.NoError(t, err)

	tempDir := ctx.TempDir

	// 验证临时目录存在
	_, err = os.Stat(tempDir)
	require.NoError(t, err)

	err = ctx.Close()
	assert.NoError(t, err)

	// 验证资源被清理
	assert.Nil(t, ctx.GoroutinePool)
	assert.Empty(t, ctx.TempDir)

	// 验证临时目录被删除
	_, err = os.Stat(tempDir)
	assert.True(t, os.IsNotExist(err))
}

func TestTestContext_Close_MultipleTimes(t *testing.T) {
	ctx, err := NewTestContext()
	require.NoError(t, err)

	// 第一次关闭
	err = ctx.Close()
	assert.NoError(t, err)

	// 第二次关闭应该安全
	err = ctx.Close()
	assert.NoError(t, err)
}

func TestTestContext_ConcurrentAccess(t *testing.T) {
	ctx, err := NewTestContext()
	require.NoError(t, err)
	defer ctx.Close()

	var wg sync.WaitGroup

	// 并发添加清理函数
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx.AddCleanup(func() {})
		}()
	}

	// 并发提交任务
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ctx.SubmitTask(func() {})
		}()
	}

	wg.Wait()
}

// ==========================================
// generateTempDir 测试
// ==========================================

func TestGenerateTempDir(t *testing.T) {
	tempDir, err := generateTempDir()
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// 验证目录存在
	_, err = os.Stat(tempDir)
	assert.NoError(t, err)

	// 验证目录名包含前缀
	assert.Contains(t, tempDir, "nexkv-test-")
}

func TestGenerateTempDirWithSeed(t *testing.T) {
	tempDir, err := generateTempDirWithSeed(99999)
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// 验证目录存在
	_, err = os.Stat(tempDir)
	assert.NoError(t, err)

	// 验证目录名包含种子
	assert.Contains(t, tempDir, "nexkv-test-seed-99999")
}

func TestGenerateTempDir_CustomBaseDir(t *testing.T) {
	// 设置自定义基础目录
	customBase := "/tmp/nexkv-test-custom"
	_ = os.MkdirAll(customBase, 0755)
	defer os.RemoveAll(customBase)

	_ = os.Setenv("NEXKV_TEST_BASE_DIR", customBase)
	defer os.Unsetenv("NEXKV_TEST_BASE_DIR")

	tempDir, err := generateTempDir()
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// 验证目录在自定义基础目录下
	assert.Contains(t, tempDir, customBase)
}
