// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// FanoutRequest Tests
// ========================================

// TestNewFanoutRequest 测试 Fanout 请求创建
func TestNewFanoutRequest(t *testing.T) {
	peers := []peer.ID{"peer1", "peer2", "peer3"}
	method := "test.method"
	body := []byte("test body")

	req := NewFanoutRequest(method, body, peers)

	assert.Equal(t, method, req.Method)
	assert.Equal(t, body, req.Body)
	assert.Equal(t, peers, req.Peers)
	assert.NotNil(t, req.ForwardedPeers, "ForwardedPeers 应该被初始化")
	assert.Empty(t, req.ForwardedPeers, "初始 ForwardedPeers 应该为空")
	assert.Equal(t, uint8(1), req.Hops, "默认跳数应该是 1")
}

// TestFanoutResponse_IsSuccess 测试响应成功判断
func TestFanoutResponse_IsSuccess(t *testing.T) {
	tests := []struct {
		name     string
		response FanoutResponse
		expected bool
	}{
		{
			name: "成功响应",
			response: FanoutResponse{
				PeerID:  "peer1",
				Body:    []byte("result"),
				Error:   nil,
				Latency: time.Millisecond * 100,
			},
			expected: true,
		},
		{
			name: "失败响应",
			response: FanoutResponse{
				PeerID:  "peer2",
				Body:    nil,
				Error:   assert.AnError,
				Latency: 0,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.response.IsSuccess())
		})
	}
}

// ========================================
// FanoutOptions Validation Tests
// ========================================

// TestValidateAndNormalize 测试选项验证和规范化
func TestValidateAndNormalize(t *testing.T) {
	tests := []struct {
		name           string
		opts           *FanoutOptions
		peerCount      int
		expectedValid  bool
		expectedMode   ResponseMode
		expectedQuorum int
	}{
		{
			name:          "默认选项",
			opts:          nil,
			peerCount:     5,
			expectedValid: true,
			expectedMode:  WaitAll, // 默认模式
		},
		{
			name: "FireForget模式",
			opts: &FanoutOptions{
				Mode:    FireForget,
				Timeout: 30 * time.Millisecond,
			},
			peerCount:     5,
			expectedValid: true,
			expectedMode:  FireForget,
		},
		{
			name: "Quorum模式-自动计算",
			opts: &FanoutOptions{
				Mode:    Quorum,
				Quorum:  0, // 自动计算
				Timeout: 30 * time.Millisecond,
			},
			peerCount:      5,
			expectedValid:  true,
			expectedMode:   Quorum,
			expectedQuorum: 3, // 5/2 + 1 = 3
		},
		{
			name: "Quorum模式-手动指定",
			opts: &FanoutOptions{
				Mode:    Quorum,
				Quorum:  4,
				Timeout: 30 * time.Millisecond,
			},
			peerCount:      5,
			expectedValid:  true,
			expectedMode:   Quorum,
			expectedQuorum: 4,
		},
		{
			name: "WaitAll模式",
			opts: &FanoutOptions{
				Mode:    WaitAll,
				Timeout: 30 * time.Millisecond,
			},
			peerCount:     5,
			expectedValid: true,
			expectedMode:  WaitAll,
		},
		{
			name: "超时时间过短",
			opts: &FanoutOptions{
				Mode:    WaitAll,
				Timeout: 1 * time.Millisecond, // 太短
			},
			peerCount:     5,
			expectedValid: false,
		},
		{
			name: "Quorum超过peer数量",
			opts: &FanoutOptions{
				Mode:    Quorum,
				Quorum:  10, // 超过 peer 数量
				Timeout: 30 * time.Millisecond,
			},
			peerCount:     5,
			expectedValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, err := ValidateAndNormalize(tt.opts, tt.peerCount)

			if tt.expectedValid {
				assert.NoError(t, err)
				assert.NotNil(t, normalized)
				assert.Equal(t, tt.expectedMode, normalized.Mode)
				if tt.expectedQuorum > 0 {
					assert.Equal(t, tt.expectedQuorum, normalized.Quorum)
				}
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// ========================================
// Fanout Metrics Tests
// ========================================

// TestNewFanoutMetrics 测试 Fanout 指标创建
func TestNewFanoutMetrics(t *testing.T) {
	metrics := NewFanoutMetrics()

	assert.NotNil(t, metrics)
	// 验证所有指标都被初始化
	assert.NotNil(t, metrics.FanoutTotal)
	assert.NotNil(t, metrics.FanoutSuccess)
	assert.NotNil(t, metrics.FanoutFailed)
	assert.NotNil(t, metrics.FanoutTimeout)
	assert.NotNil(t, metrics.FanoutLatency)
	assert.NotNil(t, metrics.FireForgetCount)
	assert.NotNil(t, metrics.QuorumCount)
	assert.NotNil(t, metrics.WaitAllCount)
	assert.NotNil(t, metrics.PeerSuccess)
	assert.NotNil(t, metrics.PeerFailed)
	assert.NotNil(t, metrics.PeerTimeout)
	assert.NotNil(t, metrics.FanoutForwardTotal)
	assert.NotNil(t, metrics.FanoutForwardFailed)
	assert.NotNil(t, metrics.HopsDistribution)
	assert.NotNil(t, metrics.ForwardPerHop)
}

// ========================================
// Hops Control Tests
// ========================================

// TestCanForward 测试转发判断
func TestCanForward(t *testing.T) {
	tests := []struct {
		name     string
		hops     uint8
		maxHops  uint8
		expected bool
	}{
		{
			name:     "可以转发",
			hops:     3,
			maxHops:  5,
			expected: true,
		},
		{
			name:     "刚好达到最大跳数",
			hops:     5,
			maxHops:  5,
			expected: false,
		},
		{
			name:     "超过最大跳数",
			hops:     6,
			maxHops:  5,
			expected: false,
		},
		{
			name:     "跳数为0",
			hops:     0,
			maxHops:  5,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, CanForward(tt.hops, tt.maxHops))
		})
	}
}

// TestDecrementHops 测试跳数递减
func TestDecrementHops(t *testing.T) {
	tests := []struct {
		name     string
		hops     uint8
		expected uint8
	}{
		{
			name:     "正常递减",
			hops:     5,
			expected: 4,
		},
		{
			name:     "递减到0",
			hops:     1,
			expected: 0,
		},
		{
			name:     "已经是0",
			hops:     0,
			expected: 0, // 不会变成负数
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, DecrementHops(tt.hops))
		})
	}
}

// ========================================
// Fanout Mode Tests
// ========================================

// TestFanout_FireForget 测试 FireForget 模式
func TestFanout_FireForget(t *testing.T) {
	// 注意：这是单元测试，需要 mock RPC Client
	// 这里只测试选项验证和模式选择

	opts := &FanoutOptions{
		Mode:    FireForget,
		Timeout: 30 * time.Millisecond,
	}
	peers := []peer.ID{"peer1", "peer2", "peer3"}

	normalized, err := ValidateAndNormalize(opts, len(peers))
	require.NoError(t, err)
	assert.Equal(t, FireForget, normalized.Mode)
}

// TestFanout_Quorum 测试 Quorum 模式
func TestFanout_Quorum(t *testing.T) {
	opts := &FanoutOptions{
		Mode:    Quorum,
		Quorum:  0, // 自动计算
		Timeout: 30 * time.Millisecond,
	}
	peers := []peer.ID{"peer1", "peer2", "peer3", "peer4", "peer5"}

	normalized, err := ValidateAndNormalize(opts, len(peers))
	require.NoError(t, err)
	assert.Equal(t, Quorum, normalized.Mode)
	assert.Equal(t, 3, normalized.Quorum) // 5/2 + 1 = 3
}

// TestFanout_WaitAll 测试 WaitAll 模式
func TestFanout_WaitAll(t *testing.T) {
	opts := &FanoutOptions{
		Mode:    WaitAll,
		Timeout: 30 * time.Millisecond,
	}
	peers := []peer.ID{"peer1", "peer2", "peer3"}

	normalized, err := ValidateAndNormalize(opts, len(peers))
	require.NoError(t, err)
	assert.Equal(t, WaitAll, normalized.Mode)
}

// ========================================
// FanoutRequest Hops Tests
// ========================================

// TestFanoutRequest_HopsDecrement 测试请求递减跳数
func TestFanoutRequest_HopsDecrement(t *testing.T) {
	req := NewFanoutRequest("test.method", []byte("body"), []peer.ID{"peer1"})

	assert.Equal(t, uint8(1), req.Hops, "初始跳数应该是 1")

	// 模拟递减
	req.Hops = DecrementHops(req.Hops)
	assert.Equal(t, uint8(0), req.Hops, "递减后应该是 0")
}

// TestFanoutRequest_ForwardedPeers 测试转发peer集合
func TestFanoutRequest_ForwardedPeers(t *testing.T) {
	peers := []peer.ID{"peer1", "peer2", "peer3"}
	req := NewFanoutRequest("test.method", []byte("body"), peers)

	// 初始状态
	assert.Empty(t, req.ForwardedPeers)

	// 添加已转发peer
	req.ForwardedPeers["peer1"] = struct{}{}

	// 检查是否已转发
	_, exists := req.ForwardedPeers["peer1"]
	assert.True(t, exists, "peer1 应该在 ForwardedPeers 中")

	_, exists = req.ForwardedPeers["peer2"]
	assert.False(t, exists, "peer2 不应该在 ForwardedPeers 中")
}

// ========================================
// Error Handling Tests
// ========================================

// TestFanout_InvalidMode 测试无效响应模式
func TestFanout_InvalidMode(t *testing.T) {
	// 测试无效的模式值
	opts := &FanoutOptions{
		Mode:    ResponseMode(99), // 无效模式
		Timeout: 30 * time.Second,
	}
	peers := []peer.ID{"peer1", "peer2"}

	_, err := ValidateAndNormalize(opts, len(peers))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "无效的响应模式")
}

// TestFanout_ZeroPeers 测试空peer列表
func TestFanout_ZeroPeers(t *testing.T) {
	opts := &FanoutOptions{
		Mode:    WaitAll,
		Timeout: 30 * time.Second,
	}
	peers := []peer.ID{} // 空 peer 列表

	_, err := ValidateAndNormalize(opts, len(peers))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "peer 列表为空")
}

// TestFanout_QuorumExceedsPeers 测试Quorum超过peer数量
func TestFanout_QuorumExceedsPeers(t *testing.T) {
	opts := &FanoutOptions{
		Mode:    Quorum,
		Quorum:  10, // 超过 peer 数量
		Timeout: 30 * time.Second,
	}
	peers := []peer.ID{"peer1", "peer2", "peer3"}

	_, err := ValidateAndNormalize(opts, len(peers))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "quorum 阈值")
}

// ========================================
// Timeout Tests
// ========================================

// TestFanout_MinTimeout 测试最小超时验证
func TestFanout_MinTimeout(t *testing.T) {
	tests := []struct {
		name        string
		timeout     time.Duration
		expectError bool
	}{
		{
			name:        "超时过短",
			timeout:     1 * time.Millisecond,
			expectError: true,
		},
		{
			name:        "刚好等于最小值",
			timeout:     10 * time.Millisecond,
			expectError: false,
		},
		{
			name:        "正常超时",
			timeout:     30 * time.Second,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &FanoutOptions{
				Mode:    WaitAll,
				Timeout: tt.timeout,
			}
			peers := []peer.ID{"peer1", "peer2"}

			_, err := ValidateAndNormalize(opts, len(peers))

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ========================================
// Concurrent Fanout Tests
// ========================================

// TestFanout_ConcurrentOptionsValidation 测试并发选项验证
func TestFanout_ConcurrentOptionsValidation(t *testing.T) {
	opts := &FanoutOptions{
		Mode:    WaitAll,
		Timeout: 30 * time.Millisecond,
	}
	peers := []peer.ID{"peer1", "peer2", "peer3"}

	// 并发验证不应该有竞态条件
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := ValidateAndNormalize(opts, len(peers))
			assert.NoError(t, err)
			done <- true
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 10; i++ {
		<-done
	}
}

// ========================================
// IsTimeout Tests
// ========================================

// TestIsTimeout 测试超时错误判断
func TestIsTimeout(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expectError bool
	}{
		{
			name:        "context.DeadlineExceeded",
			err:         context.DeadlineExceeded,
			expectError: true,
		},
		{
			name:        "RPC超时错误",
			err:         NewRPCError(ErrCodeTimeout, "operation timeout"),
			expectError: true,
		},
		{
			name:        "其他错误",
			err:         assert.AnError,
			expectError: false,
		},
		{
			name:        "nil错误",
			err:         nil,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectError, IsTimeout(tt.err))
		})
	}
}

// ========================================
// FanoutResult Tests
// ========================================

// TestFanoutResult_QuorumSuccessRate 测试 Quorum 成功率计算
func TestFanoutResult_QuorumSuccessRate(t *testing.T) {
	tests := []struct {
		name         string
		successCount int
		totalPeers   int
		expectedRate float64
	}{
		{
			name:         "全部成功",
			successCount: 5,
			totalPeers:   5,
			expectedRate: 1.0,
		},
		{
			name:         "部分成功",
			successCount: 3,
			totalPeers:   5,
			expectedRate: 0.6,
		},
		{
			name:         "全部失败",
			successCount: 0,
			totalPeers:   5,
			expectedRate: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &QuorumResult{
				SuccessCount: tt.successCount,
				TotalPeers:   tt.totalPeers,
			}
			assert.Equal(t, tt.expectedRate, result.GetSuccessRate())
		})
	}
}

// ========================================
// Integration Mock Tests
// ========================================

// MockFanoutClient 模拟 Fanout Client（用于集成测试）
type MockFanoutClient struct {
	callFunc func(ctx context.Context, peerID peer.ID, method string, body []byte) ([]byte, error)
}

func (m *MockFanoutClient) Call(ctx context.Context, peerID peer.ID, method string, body []byte) ([]byte, error) {
	if m.callFunc != nil {
		return m.callFunc(ctx, peerID, method, body)
	}
	return []byte("mock response"), nil
}

// TestFanout_IntegrationWithQuorum 测试与 Quorum 集成
func TestFanout_IntegrationWithQuorum(t *testing.T) {
	manager := NewQuorumManager(&QuorumConfig{
		Enabled:       true,
		DefaultQuorum: 0, // 动态计算
		MinQuorum:     1,
	})

	peers := []peer.ID{"peer1", "peer2", "peer3", "peer4", "peer5"}

	// 测试 Quorum 阈值计算
	quorum := manager.GetQuorumThreshold(len(peers))
	assert.Equal(t, 3, quorum, "5个peers的Quorum应该是3")

	// 测试 Quorum 结果计算
	result := manager.CalculateQuorumResult(
		len(peers),
		3, // 成功3个
		nil,
		nil,
	)

	assert.True(t, result.Success, "3/5应该达到Quorum")
	assert.Equal(t, 3, result.Quorum)
}

// ========================================
// Benchmark Tests
// ========================================

// BenchmarkFanoutOptionsValidation 基准测试：选项验证性能
func BenchmarkFanoutOptionsValidation(b *testing.B) {
	opts := &FanoutOptions{
		Mode:    WaitAll,
		Timeout: 30 * time.Second,
	}
	peerCount := 10

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ValidateAndNormalize(opts, peerCount)
	}
}

// BenchmarkHopsControl 基准测试：跳数控制性能
func BenchmarkHopsControl(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CanForward(5, 10)
		DecrementHops(5)
	}
}

// ========================================
// Fanout Metrics Tests
// ========================================

// TestFanoutMetrics_GetMetrics 测试获取全局指标
func TestFanoutMetrics_GetMetrics(t *testing.T) {
	metrics := GetFanoutMetrics()

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.FanoutTotal)
	assert.NotNil(t, metrics.FanoutSuccess)
	assert.NotNil(t, metrics.FanoutFailed)
	assert.NotNil(t, metrics.FanoutLatency)
	assert.NotNil(t, metrics.PeerSuccess)
	assert.NotNil(t, metrics.PeerFailed)
}

// TestFanoutMetrics_RecordStart 测试记录开始指标
func TestFanoutMetrics_RecordStart(t *testing.T) {
	metrics := GetFanoutMetrics()

	// 验证指标操作不会 panic
	assert.NotPanics(t, func() {
		metrics.FanoutTotal.Inc()
	})
}

// TestFanoutMetrics_RecordModeDistribution 测试响应模式分布
func TestFanoutMetrics_RecordModeDistribution(t *testing.T) {
	metrics := GetFanoutMetrics()

	// 验证所有模式计数器可以正常递增
	assert.NotPanics(t, func() {
		metrics.FireForgetCount.Inc()
		metrics.QuorumCount.Inc()
		metrics.WaitAllCount.Inc()
	})
}

// TestFanoutMetrics_PeerLevelMetrics 测试 Peer 级别指标
func TestFanoutMetrics_PeerLevelMetrics(t *testing.T) {
	metrics := GetFanoutMetrics()
	peerID := "test-peer-123"

	// 验证 peer 级别指标操作不会 panic
	assert.NotPanics(t, func() {
		metrics.PeerSuccess.WithLabelValues(peerID).Inc()
		metrics.PeerFailed.WithLabelValues(peerID).Inc()
		metrics.PeerTimeout.WithLabelValues(peerID).Inc()
	})
}

// TestFanoutMetrics_LatencyHistogram 测试延迟直方图
func TestFanoutMetrics_LatencyHistogram(t *testing.T) {
	metrics := GetFanoutMetrics()

	// 记录不同延迟值
	metrics.FanoutLatency.Observe(0.001) // 1ms
	metrics.FanoutLatency.Observe(0.010) // 10ms
	metrics.FanoutLatency.Observe(0.100) // 100ms
	metrics.FanoutLatency.Observe(1.000) // 1s

	// 验证观察成功（没有断言错误即表示成功）
	assert.True(t, true)
}
