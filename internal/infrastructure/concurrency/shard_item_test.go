// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// mockShardItem 实现 ShardItem 接口用于测试
type mockShardItem struct {
	*model.BaseTask[struct{}]
	shardID    int
	maxRetries int
	attempts   int64
}

// NewMockShardItem 创建 mockShardItem
func NewMockShardItem(shardID, maxRetries int) *mockShardItem {
	return &mockShardItem{
		BaseTask: model.NewBaseTask[struct{}](
			model.TaskPriorityNormal,
			0,
			func(ctx context.Context, pipeline model.TaskRunnerContext) (struct{}, error) {
				return struct{}{}, nil
			},
		),
		shardID:    shardID,
		maxRetries: maxRetries,
	}
}

func (m *mockShardItem) ShardID() int {
	return m.shardID
}

func (m *mockShardItem) MaxRetries() int {
	return m.maxRetries
}

func (m *mockShardItem) IncAttempts() int {
	return int(atomic.AddInt64(&m.attempts, 1))
}

// TestMockShardItem_VerifyInterfaceImplement 验证 mockShardItem 实现了 ShardItem 接口
func TestMockShardItem_VerifyInterfaceImplement(t *testing.T) {
	var _ ShardItem = (*mockShardItem)(nil)
}

// TestShardItem_ShardID_Positive 测试正数 ShardID
func TestShardItem_ShardID_Positive(t *testing.T) {
	tests := []struct {
		name     string
		shardID  int
		expected int
	}{
		{
			name:     "shardID 为 1",
			shardID:  1,
			expected: 1,
		},
		{
			name:     "shardID 为 100",
			shardID:  100,
			expected: 100,
		},
		{
			name:     "shardID 为最大正整数",
			shardID:  1<<31 - 1,
			expected: 1<<31 - 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := NewMockShardItem(tt.shardID, 0)
			assert.Equal(t, tt.expected, item.ShardID())
		})
	}
}

// TestShardItem_ShardID_Zero 测试零值 ShardID
func TestShardItem_ShardID_Zero(t *testing.T) {
	item := NewMockShardItem(0, 0)
	assert.Equal(t, 0, item.ShardID())
}

// TestShardItem_ShardID_Negative 测试负数 ShardID
func TestShardItem_ShardID_Negative(t *testing.T) {
	tests := []struct {
		name     string
		shardID  int
		expected int
	}{
		{
			name:     "shardID 为 -1",
			shardID:  -1,
			expected: -1,
		},
		{
			name:     "shardID 为 -100",
			shardID:  -100,
			expected: -100,
		},
		{
			name:     "shardID 为最小负整数",
			shardID:  -1 << 31,
			expected: -1 << 31,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := NewMockShardItem(tt.shardID, 0)
			assert.Equal(t, tt.expected, item.ShardID())
		})
	}
}

// TestShardItem_MaxRetries 测试 MaxRetries
func TestShardItem_MaxRetries(t *testing.T) {
	tests := []struct {
		name        string
		maxRetries  int
		expected    int
		description string
	}{
		{
			name:        "不重试",
			maxRetries:  0,
			expected:    0,
			description: "MaxRetries 为 0 表示不重试",
		},
		{
			name:        "重试 1 次",
			maxRetries:  1,
			expected:    1,
			description: "MaxRetries 为 1 表示最多重试 1 次",
		},
		{
			name:        "重试 3 次",
			maxRetries:  3,
			expected:    3,
			description: "MaxRetries 为 3 表示最多重试 3 次",
		},
		{
			name:        "重试 10 次",
			maxRetries:  10,
			expected:    10,
			description: "MaxRetries 为 10 表示最多重试 10 次",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := NewMockShardItem(0, tt.maxRetries)
			assert.Equal(t, tt.expected, item.MaxRetries(), tt.description)
		})
	}
}

// TestShardItem_IncAttempts 测试 IncAttempts
func TestShardItem_IncAttempts(t *testing.T) {
	t.Run("初始为 0，每次调用 +1", func(t *testing.T) {
		item := NewMockShardItem(0, 3)

		// 初始 attempts 应该为 0
		assert.Equal(t, int64(0), atomic.LoadInt64(&item.attempts))

		// 第 1 次调用
		attempts := item.IncAttempts()
		assert.Equal(t, 1, attempts)
		assert.Equal(t, int64(1), atomic.LoadInt64(&item.attempts))

		// 第 2 次调用
		attempts = item.IncAttempts()
		assert.Equal(t, 2, attempts)
		assert.Equal(t, int64(2), atomic.LoadInt64(&item.attempts))

		// 第 3 次调用
		attempts = item.IncAttempts()
		assert.Equal(t, 3, attempts)
		assert.Equal(t, int64(3), atomic.LoadInt64(&item.attempts))
	})
}

// TestShardItem_IncAttempts_ThreadSafety 测试 IncAttempts 的线程安全性
func TestShardItem_IncAttempts_ThreadSafety(t *testing.T) {
	const goroutines = 100
	const callsPerGoroutine = 100

	item := NewMockShardItem(0, goroutines*callsPerGoroutine)

	done := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < callsPerGoroutine; j++ {
				item.IncAttempts()
			}
			done <- struct{}{}
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < goroutines; i++ {
		<-done
	}

	// 验证总调用次数
	expectedTotal := goroutines * callsPerGoroutine
	actualTotal := atomic.LoadInt64(&item.attempts)
	assert.Equal(t, int64(expectedTotal), actualTotal)
}

// TestShardItem_RetryLogic 测试重试逻辑
func TestShardItem_RetryLogic(t *testing.T) {
	tests := []struct {
		name          string
		maxRetries    int
		callCount     int
		shouldFail    bool
		finalAttempts int
		description   string
	}{
		{
			name:          "不重试，第 1 次失败",
			maxRetries:    0,
			callCount:     1,
			shouldFail:    true,
			finalAttempts: 1,
			description:   "MaxRetries=0，第 1 次调用 IncAttempts() 就超过 MaxRetries()",
		},
		{
			name:          "重试 3 次，第 4 次失败",
			maxRetries:    3,
			callCount:     4,
			shouldFail:    true,
			finalAttempts: 4,
			description:   "MaxRetries=3，前 3 次重试，第 4 次调用 IncAttempts() 后 attempts > MaxRetries()",
		},
		{
			name:          "重试 3 次，第 3 次成功",
			maxRetries:    3,
			callCount:     2,
			shouldFail:    false,
			finalAttempts: 2,
			description:   "MaxRetries=3，第 2 次调用 IncAttempts() 后 attempts=2 <= MaxRetries()=3",
		},
		{
			name:          "重试 10 次，第 11 次失败",
			maxRetries:    10,
			callCount:     11,
			shouldFail:    true,
			finalAttempts: 11,
			description:   "MaxRetries=10，前 10 次重试，第 11 次调用 IncAttempts() 后失败",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := NewMockShardItem(0, tt.maxRetries)

			// 模拟调用 IncAttempts() 多次
			for i := 0; i < tt.callCount; i++ {
				attempts := item.IncAttempts()

				// 最后一次调用时检查是否应该失败
				if i == tt.callCount-1 {
					if tt.shouldFail {
						assert.Greater(t, attempts, tt.maxRetries, tt.description)
					} else {
						assert.LessOrEqual(t, attempts, tt.maxRetries, tt.description)
					}
				}
			}

			// 验证最终 attempts
			assert.Equal(t, tt.finalAttempts, int(atomic.LoadInt64(&item.attempts)))
		})
	}
}

// TestShardItem_ShardID_ResourceAffinity 测试 ShardID 资源亲和性场景
func TestShardItem_ShardID_ResourceAffinity(t *testing.T) {
	tests := []struct {
		name        string
		shardID     int
		coreCount   int
		expectedIdx int
		description string
	}{
		{
			name:        "B-Tree leaf-lock 地址",
			shardID:     0x12345678,
			coreCount:   4,
			expectedIdx: 0x12345678 % 4,
			description: "使用 leaf-lock 地址作为 shardID，通过取模路由到对应 Core",
		},
		{
			name:        "WAL wal_id=5",
			shardID:     5,
			coreCount:   8,
			expectedIdx: 5 % 8,
			description: "wal_id=5 路由到 Core 5，确保同一 WAL 文件操作在同一 Core",
		},
		{
			name:        "AO ao_id=10",
			shardID:     10,
			coreCount:   16,
			expectedIdx: 10 % 16,
			description: "ao_id=10 路由到 Core 10，确保同一 AO 文件操作在同一 Core",
		},
		{
			name:        "负数 shardID",
			shardID:     -5,
			coreCount:   4,
			expectedIdx: 5 % 4, // abs(-5) % 4 = 1
			description: "负数 shardID 取绝对值后取模",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := NewMockShardItem(tt.shardID, 0)
			shardID := item.ShardID()

			var schedulerIndex int
			if shardID == 0 {
				// shardID=0: 动态选择（本测试不覆盖）
				t.Skip("shardID=0 使用动态负载均衡")
			} else if shardID > 0 {
				schedulerIndex = shardID % tt.coreCount
			} else {
				schedulerIndex = (-shardID) % tt.coreCount
			}

			assert.Equal(t, tt.expectedIdx, schedulerIndex, tt.description)
		})
	}
}
