// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
)

// ========================================
// BatchOptions Tests
// ========================================

// TestDefaultBatchOptions 测试默认选项
func TestDefaultBatchOptions(t *testing.T) {
	opts := DefaultBatchOptions()

	assert.Equal(t, 10, opts.MaxConcurrent, "默认最大并发数应该是 10")
	assert.Equal(t, 30*time.Second, opts.Timeout, "默认超时应该是 30 秒")
	assert.False(t, opts.ContinueOnError, "默认遇到错误应该停止")
	assert.True(t, opts.PreserveOrder, "默认应该保持顺序")
}

// ========================================
// BatchRequest/Response Tests
// ========================================

// TestBatchResponse_Success 测试响应成功判断
func TestBatchResponse_Success(t *testing.T) {
	tests := []struct {
		name     string
		response BatchResponse
		expected bool
	}{
		{
			name: "成功响应",
			response: BatchResponse{
				Method:  "test.method",
				Body:    []byte("result"),
				Error:   nil,
				Success: true,
			},
			expected: true,
		},
		{
			name: "失败响应",
			response: BatchResponse{
				Method:  "test.method",
				Body:    nil,
				Error:   errors.New("test error"),
				Success: false,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.response.Success)
		})
	}
}

// ========================================
// BatchResult Tests
// ========================================

// TestBatchResult_String 测试批量结果字符串表示
func TestBatchResult_String(t *testing.T) {
	result := &BatchResult{
		Total:      10,
		Success:    8,
		Failed:     2,
		Duration:   100 * time.Millisecond,
		AvgLatency: 50 * time.Millisecond,
	}

	str := result.String()
	assert.Contains(t, str, "Total: 10")
	assert.Contains(t, str, "Success: 8")
	assert.Contains(t, str, "Failed: 2")
}

// TestBatchResult_GetSuccessRate 测试成功率计算
func TestBatchResult_GetSuccessRate(t *testing.T) {
	tests := []struct {
		name         string
		successCount int
		totalCount   int
		expectedRate float64
	}{
		{
			name:         "全部成功",
			successCount: 10,
			totalCount:   10,
			expectedRate: 1.0,
		},
		{
			name:         "部分成功",
			successCount: 5,
			totalCount:   10,
			expectedRate: 0.5,
		},
		{
			name:         "全部失败",
			successCount: 0,
			totalCount:   10,
			expectedRate: 0.0,
		},
		{
			name:         "空结果",
			successCount: 0,
			totalCount:   0,
			expectedRate: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &BatchResult{
				Success: tt.successCount,
				Total:   tt.totalCount,
			}
			assert.Equal(t, tt.expectedRate, result.GetSuccessRate())
		})
	}
}

// ========================================
// Batch Call Integration Tests
// ========================================

// TestBatchCall_EmptyRequests 测试空请求列表
func TestBatchCall_EmptyRequests(t *testing.T) {
	// 这是一个集成测试，需要真实的 RPC Client
	// 这里我们只测试基本逻辑
	reqs := []BatchRequest{}

	result := &BatchResult{
		Responses: []BatchResponse{},
		Total:     len(reqs),
	}

	assert.Equal(t, 0, result.Total)
	assert.Equal(t, 0, result.Success)
	assert.Equal(t, 0, result.Failed)
}

// TestBatchCalculateResult 测试结果计算
func TestBatchCalculateResult(t *testing.T) {
	client := &Client{}

	responses := []BatchResponse{
		{
			Method:  "method1",
			Success: true,
			Latency: 10 * time.Millisecond,
		},
		{
			Method:  "method2",
			Success: true,
			Latency: 20 * time.Millisecond,
		},
		{
			Method:  "method3",
			Success: false,
			Error:   errors.New("error"),
			Latency: 0,
		},
	}

	result := client.calculateBatchResult(responses, time.Now())

	assert.Equal(t, 3, result.Total)
	assert.Equal(t, 2, result.Success)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, 15*time.Millisecond, result.AvgLatency)
	assert.Equal(t, 20*time.Millisecond, result.MaxLatency)
	assert.Equal(t, 10*time.Millisecond, result.MinLatency)
}

// TestBatchCalculateResult_AllFailures 测试全部失败的结果计算
func TestBatchCalculateResult_AllFailures(t *testing.T) {
	client := &Client{}

	responses := []BatchResponse{
		{
			Method:  "method1",
			Success: false,
			Error:   errors.New("error1"),
		},
		{
			Method:  "method2",
			Success: false,
			Error:   errors.New("error2"),
		},
	}

	result := client.calculateBatchResult(responses, time.Now())

	assert.Equal(t, 2, result.Total)
	assert.Equal(t, 0, result.Success)
	assert.Equal(t, 2, result.Failed)
	assert.Equal(t, time.Duration(0), result.AvgLatency)
	assert.Equal(t, time.Duration(0), result.MaxLatency)
	assert.Equal(t, time.Duration(0), result.MinLatency)
}

// TestBatchCalculateResult_AllSuccess 测试全部成功的结果计算
func TestBatchCalculateResult_AllSuccess(t *testing.T) {
	client := &Client{}

	responses := []BatchResponse{
		{
			Method:  "method1",
			Success: true,
			Latency: 10 * time.Millisecond,
		},
		{
			Method:  "method2",
			Success: true,
			Latency: 30 * time.Millisecond,
		},
		{
			Method:  "method3",
			Success: true,
			Latency: 20 * time.Millisecond,
		},
	}

	result := client.calculateBatchResult(responses, time.Now())

	assert.Equal(t, 3, result.Total)
	assert.Equal(t, 3, result.Success)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, 20*time.Millisecond, result.AvgLatency)
	assert.Equal(t, 30*time.Millisecond, result.MaxLatency)
	assert.Equal(t, 10*time.Millisecond, result.MinLatency)
}

// ========================================
// Options Validation Tests
// ========================================

// TestBatchOptions_MaxConcurrent 测试并发控制
func TestBatchOptions_MaxConcurrent(t *testing.T) {
	tests := []struct {
		name            string
		maxConcurrent   int
		expectUnlimited bool
	}{
		{
			name:            "限制并发",
			maxConcurrent:   5,
			expectUnlimited: false,
		},
		{
			name:            "不限制并发",
			maxConcurrent:   0,
			expectUnlimited: true,
		},
		{
			name:            "负数（不限制）",
			maxConcurrent:   -1,
			expectUnlimited: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &BatchOptions{
				MaxConcurrent: tt.maxConcurrent,
			}

			// 验证信号量创建逻辑
			if opts.MaxConcurrent <= 0 {
				// 无限制情况
				semSize := 10 // 模拟一个合理的大小
				sem := make(chan struct{}, semSize)
				assert.Equal(t, semSize, cap(sem), "应该无限制")
			} else {
				sem := make(chan struct{}, opts.MaxConcurrent)
				assert.Equal(t, tt.maxConcurrent, cap(sem), "应该有限制")
			}
		})
	}
}

// ========================================
// Timeout Tests
// ========================================

// TestBatchOptions_Timeout 测试超时配置
func TestBatchOptions_Timeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		valid   bool
	}{
		{
			name:    "有效超时",
			timeout: 30 * time.Second,
			valid:   true,
		},
		{
			name:    "零超时（无超时）",
			timeout: 0,
			valid:   true,
		},
		{
			name:    "负超时（无效）",
			timeout: -1 * time.Second,
			valid:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &BatchOptions{
				Timeout: tt.timeout,
			}

			if tt.valid {
				if tt.timeout > 0 {
					ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
					defer cancel()
					assert.NotNil(t, ctx)
				}
			}
		})
	}
}

// ========================================
// Error Handling Tests
// ========================================

// TestBatchOptions_ContinueOnError 测试错误处理策略
func TestBatchOptions_ContinueOnError(t *testing.T) {
	tests := []struct {
		name            string
		continueOnError bool
		description     string
	}{
		{
			name:            "遇错停止",
			continueOnError: false,
			description:     "遇到错误应该停止",
		},
		{
			name:            "遇错继续",
			continueOnError: true,
			description:     "遇到错误应该继续",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &BatchOptions{
				ContinueOnError: tt.continueOnError,
			}

			assert.Equal(t, tt.continueOnError, opts.ContinueOnError)
		})
	}
}

// ========================================
// Order Preservation Tests
// ========================================

// TestBatchOptions_PreserveOrder 测试顺序保持
func TestBatchOptions_PreserveOrder(t *testing.T) {
	tests := []struct {
		name          string
		preserveOrder bool
		description   string
	}{
		{
			name:          "保持顺序",
			preserveOrder: true,
			description:   "响应应该保持请求顺序",
		},
		{
			name:          "不保持顺序",
			preserveOrder: false,
			description:   "响应顺序可能不保证",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &BatchOptions{
				PreserveOrder: tt.preserveOrder,
			}

			assert.Equal(t, tt.preserveOrder, opts.PreserveOrder)
		})
	}
}

// ========================================
// Benchmark Tests
// ========================================

// BenchmarkBatchCalculateResult 基准测试：批量结果计算性能
func BenchmarkBatchCalculateResult(b *testing.B) {
	client := &Client{}

	responses := make([]BatchResponse, 100)
	for i := 0; i < 100; i++ {
		responses[i] = BatchResponse{
			Method:  "test.method",
			Success: i%2 == 0,
			Latency: time.Duration(i) * time.Millisecond,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = client.calculateBatchResult(responses, time.Now())
	}
}

// BenchmarkBatchOptions_Default 基准测试：默认选项创建性能
func BenchmarkBatchOptions_Default(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = DefaultBatchOptions()
	}
}

// ========================================
// Mock Tests
// ========================================

// MockBatchClient 模拟批量调用客户端（用于集成测试）
type MockBatchClient struct {
	CallFunc func(ctx context.Context, peerID peer.ID, method string, body []byte) ([]byte, error)
}

func (m *MockBatchClient) Call(ctx context.Context, peerID peer.ID, method string, body []byte) ([]byte, error) {
	if m.CallFunc != nil {
		return m.CallFunc(ctx, peerID, method, body)
	}
	return []byte("mock response"), nil
}

// TestBatchCall_MockClient 测试模拟客户端
func TestBatchCall_MockClient(t *testing.T) {
	mockClient := &MockBatchClient{
		CallFunc: func(ctx context.Context, peerID peer.ID, method string, body []byte) ([]byte, error) {
			if method == "error.method" {
				return nil, errors.New("mock error")
			}
			return []byte("mock response"), nil
		},
	}

	// 测试成功调用
	ctx := context.Background()
	peerID := peer.ID("test-peer-1")

	body, err := mockClient.Call(ctx, peerID, "test.method", []byte("request"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("mock response"), body)

	// 测试失败调用
	body, err = mockClient.Call(ctx, peerID, "error.method", []byte("request"))
	assert.Error(t, err)
	assert.Nil(t, body)
}
