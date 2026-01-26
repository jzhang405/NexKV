// Package consensus 测试辅助函数
package consensus

import (
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/uuid"
	"github.com/stretchr/testify/require"
)

// newTestUUIDGenerator 创建测试用 UUID 生成器
func newTestUUIDGenerator(t *testing.T) uuid.UUIDGenerator {
	gen, err := uuid.NewSafeUUIDGenerator(1, 1, 100*time.Millisecond, 1*time.Second)
	require.NoError(t, err)
	return gen
}

// newBenchmarkUUIDGenerator 创建 benchmark 用 UUID 生成器（忽略错误）
func newBenchmarkUUIDGenerator() uuid.UUIDGenerator {
	gen, _ := uuid.NewSafeUUIDGenerator(1, 1, 100*time.Millisecond, 1*time.Second)
	return gen
}
