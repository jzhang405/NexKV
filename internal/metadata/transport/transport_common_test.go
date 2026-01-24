// Package transport 传输层公共实现测试
package transport

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// validateTransportConfig 测试
// ========================================

// TestValidateTransportConfig_ValidConfig 测试有效配置
func TestValidateTransportConfig_ValidConfig(t *testing.T) {
	testCases := []struct {
		name   string
		config *TransportConfig
	}{
		{
			name:   "默认配置",
			config: DefaultTransportConfig(),
		},
		{
			name: "最小有效配置",
			config: &TransportConfig{
				ListenAddr:         "127.0.0.1:8080",
				MaxMessageSize:     1,
				ReadTimeout:        0,
				WriteTimeout:       0,
				KeepAliveInterval:  0,
				KeepAliveTimeout:   0,
				BufferSize:         1,
				ChannelSendTimeout: 0,
			},
		},
		{
			name: "最大有效配置",
			config: &TransportConfig{
				ListenAddr:         "0.0.0.0:9211",
				MaxMessageSize:     1024 * 1024 * 1024, // 1GB
				ReadTimeout:        1 * time.Hour,
				WriteTimeout:       1 * time.Hour,
				KeepAliveInterval:  1 * time.Hour,
				KeepAliveTimeout:   1 * time.Hour,
				BufferSize:         65536, // 64KB
				ChannelSendTimeout: 1 * time.Hour,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTransportConfig(tc.config)
			assert.NoError(t, err)
		})
	}
}

// TestValidateTransportConfig_EmptyListenAddr 测试空监听地址
func TestValidateTransportConfig_EmptyListenAddr(t *testing.T) {
	config := &TransportConfig{
		ListenAddr:         "",
		MaxMessageSize:     1024,
		ReadTimeout:        30 * time.Second,
		WriteTimeout:       30 * time.Second,
		KeepAliveInterval:  10 * time.Second,
		KeepAliveTimeout:   30 * time.Second,
		BufferSize:         4096,
		ChannelSendTimeout: 5 * time.Second,
	}

	err := validateTransportConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "监听地址不能为空")
}

// TestValidateTransportConfig_InvalidMaxMessageSize 测试无效的最大消息大小
func TestValidateTransportConfig_InvalidMaxMessageSize(t *testing.T) {
	testCases := []struct {
		name           string
		maxMessageSize int64
		expectedErr    string
	}{
		{"零大小", 0, "最大消息大小必须在"},
		{"负大小", -1, "最大消息大小必须在"},
		{"超过1GB", 1024*1024*1024 + 1, "最大消息大小必须在"},
		{"远超限制", 10 * 1024 * 1024 * 1024, "最大消息大小必须在"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &TransportConfig{
				ListenAddr:         "127.0.0.1:8080",
				MaxMessageSize:     tc.maxMessageSize,
				ReadTimeout:        30 * time.Second,
				WriteTimeout:       30 * time.Second,
				KeepAliveInterval:  10 * time.Second,
				KeepAliveTimeout:   30 * time.Second,
				BufferSize:         4096,
				ChannelSendTimeout: 5 * time.Second,
			}

			err := validateTransportConfig(config)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

// TestValidateTransportConfig_NegativeTimeout 测试负超时值
func TestValidateTransportConfig_NegativeTimeout(t *testing.T) {
	timeoutFields := []struct {
		name        string
		fieldSetter func(*TransportConfig, time.Duration)
		expectedErr string
	}{
		{"读超时", func(c *TransportConfig, d time.Duration) { c.ReadTimeout = d }, "读超时不能为负数"},
		{"写超时", func(c *TransportConfig, d time.Duration) { c.WriteTimeout = d }, "写超时不能为负数"},
		{"保活间隔", func(c *TransportConfig, d time.Duration) { c.KeepAliveInterval = d }, "保活间隔不能为负数"},
		{"保活超时", func(c *TransportConfig, d time.Duration) { c.KeepAliveTimeout = d }, "保活超时不能为负数"},
		{"通道发送超时", func(c *TransportConfig, d time.Duration) { c.ChannelSendTimeout = d }, "通道发送超时不能为负数"},
	}

	for _, tc := range timeoutFields {
		t.Run(tc.name, func(t *testing.T) {
			config := &TransportConfig{
				ListenAddr:         "127.0.0.1:8080",
				MaxMessageSize:     1024,
				ReadTimeout:        30 * time.Second,
				WriteTimeout:       30 * time.Second,
				KeepAliveInterval:  10 * time.Second,
				KeepAliveTimeout:   30 * time.Second,
				BufferSize:         4096,
				ChannelSendTimeout: 5 * time.Second,
			}

			tc.fieldSetter(config, -1*time.Second)

			err := validateTransportConfig(config)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

// TestValidateTransportConfig_InvalidBufferSize 测试无效的缓冲区大小
func TestValidateTransportConfig_InvalidBufferSize(t *testing.T) {
	testCases := []struct {
		name        string
		bufferSize  int
		expectedErr string
	}{
		{"零大小", 0, "缓冲区大小必须在"},
		{"负大小", -1, "缓冲区大小必须在"},
		{"超过64KB", 65536 + 1, "缓冲区大小必须在"},
		{"远超限制", 1000000, "缓冲区大小必须在"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &TransportConfig{
				ListenAddr:         "127.0.0.1:8080",
				MaxMessageSize:     1024,
				ReadTimeout:        30 * time.Second,
				WriteTimeout:       30 * time.Second,
				KeepAliveInterval:  10 * time.Second,
				KeepAliveTimeout:   30 * time.Second,
				BufferSize:         tc.bufferSize,
				ChannelSendTimeout: 5 * time.Second,
			}

			err := validateTransportConfig(config)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

// TestValidateTransportConfig_BoundaryValues 测试边界值
func TestValidateTransportConfig_BoundaryValues(t *testing.T) {
	testCases := []struct {
		name   string
		config *TransportConfig
		valid  bool
	}{
		{
			name: "MaxMessageSize 边界值 1",
			config: &TransportConfig{
				ListenAddr:     "127.0.0.1:8080",
				MaxMessageSize: 1,
				BufferSize:     4096,
			},
			valid: true,
		},
		{
			name: "MaxMessageSize 边界值 1GB",
			config: &TransportConfig{
				ListenAddr:     "127.0.0.1:8080",
				MaxMessageSize: 1024 * 1024 * 1024,
				BufferSize:     4096,
			},
			valid: true,
		},
		{
			name: "BufferSize 边界值 1",
			config: &TransportConfig{
				ListenAddr:     "127.0.0.1:8080",
				MaxMessageSize: 1024,
				BufferSize:     1,
			},
			valid: true,
		},
		{
			name: "BufferSize 边界值 64KB",
			config: &TransportConfig{
				ListenAddr:     "127.0.0.1:8080",
				MaxMessageSize: 1024,
				BufferSize:     65536,
			},
			valid: true,
		},
		{
			name: "超时零值（有效）",
			config: &TransportConfig{
				ListenAddr:         "127.0.0.1:8080",
				MaxMessageSize:     1024,
				BufferSize:         4096,
				ReadTimeout:        0,
				WriteTimeout:       0,
				KeepAliveInterval:  0,
				KeepAliveTimeout:   0,
				ChannelSendTimeout: 0,
			},
			valid: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTransportConfig(tc.config)
			if tc.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// ========================================
// createBatchForwardResult 测试
// ========================================

// TestCreateBatchForwardResult 测试创建批量转发结果
func TestCreateBatchForwardResult(t *testing.T) {
	addrs := []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	testErr := errors.New("test error")

	result := createBatchForwardResult(addrs, testErr)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 3, result.FailureCount)
	assert.Len(t, result.Results, 3)

	// 验证每个结果
	for i, addr := range addrs {
		assert.Equal(t, addr, result.Results[i].Addr)
		assert.Equal(t, uint64(0), result.Results[i].SeqID)
		assert.Equal(t, testErr, result.Results[i].Error)
	}
}

// TestCreateBatchForwardResult_EmptyAddrs 测试空地址列表
func TestCreateBatchForwardResult_EmptyAddrs(t *testing.T) {
	addrs := []string{}
	testErr := errors.New("test error")

	result := createBatchForwardResult(addrs, testErr)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Len(t, result.Results, 0)
}

// TestCreateBatchForwardResult_SingleAddr 测试单个地址
func TestCreateBatchForwardResult_SingleAddr(t *testing.T) {
	addrs := []string{"127.0.0.1:8080"}
	testErr := errors.New("test error")

	result := createBatchForwardResult(addrs, testErr)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 1, result.FailureCount)
	assert.Len(t, result.Results, 1)
	assert.Equal(t, "127.0.0.1:8080", result.Results[0].Addr)
}

// ========================================
// generateMsgSeq 测试
// ========================================

// TestGenerateMsgSeq_DefaultCounter 测试默认计数器
func TestGenerateMsgSeq_DefaultCounter(t *testing.T) {
	var counter atomic.Uint64

	for i := uint64(1); i <= 10; i++ {
		seq := generateMsgSeq(nil, &counter)
		assert.Equal(t, i, seq)
	}
}

// TestGenerateMsgSeq_CustomGenerator 测试自定义生成器
func TestGenerateMsgSeq_CustomGenerator(t *testing.T) {
	var counter atomic.Uint64
	customSeq := uint64(1000)

	generator := func() uint64 {
		customSeq++
		return customSeq
	}

	for i := uint64(1); i <= 10; i++ {
		seq := generateMsgSeq(generator, &counter)
		assert.Equal(t, uint64(1000+i), seq)
	}

	// 验证默认计数器未被使用
	assert.Equal(t, uint64(0), counter.Load())
}

// TestGenerateMsgSeq_InvalidGenerator 测试无效生成器
func TestGenerateMsgSeq_InvalidGenerator(t *testing.T) {
	var counter atomic.Uint64

	// 传入非函数类型
	invalidGenerator := "not a function"

	seq := generateMsgSeq(invalidGenerator, &counter)
	assert.Equal(t, uint64(1), seq)
	assert.Equal(t, uint64(1), counter.Load())
}

// TestGenerateMsgSeq_NilGeneratorFunction 测试 nil 函数
func TestGenerateMsgSeq_NilGeneratorFunction(t *testing.T) {
	var counter atomic.Uint64

	var nilFunc func() uint64

	seq := generateMsgSeq(nilFunc, &counter)
	assert.Equal(t, uint64(1), seq)
	assert.Equal(t, uint64(1), counter.Load())
}

// TestGenerateMsgSeq_Concurrent 测试并发安全性（降低并发数避免 OOM）
func TestGenerateMsgSeq_Concurrent(t *testing.T) {
	var counter atomic.Uint64

	done := make(chan bool, 50)
	sequences := make(chan uint64, 50)

	for i := 0; i < 50; i++ {
		go func() {
			seq := generateMsgSeq(nil, &counter)
			sequences <- seq
			done <- true
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 50; i++ {
		<-done
	}
	close(sequences)

	// 验证序列号唯一且连续
	seqMap := make(map[uint64]bool)
	for seq := range sequences {
		if seqMap[seq] {
			t.Errorf("重复的序列号: %d", seq)
		}
		seqMap[seq] = true
	}

	assert.Equal(t, 50, len(seqMap))
	assert.Equal(t, uint64(50), counter.Load())
}

// ========================================
// executeBatchForward 测试
// ========================================

// mockBatchForwarder 模拟批量转发器
type mockBatchForwarder struct {
	results map[string]forwardResult
	delay   time.Duration
}

type forwardResult struct {
	seqID uint64
	err   error
}

func (m *mockBatchForwarder) forward(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if result, ok := m.results[addr]; ok {
		return result.seqID, result.err
	}
	return 0, fmt.Errorf("unknown address: %s", addr)
}

// TestExecuteBatchForward_AllSuccess 测试全部成功
func TestExecuteBatchForward_AllSuccess(t *testing.T) {
	addrs := []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		results: map[string]forwardResult{
			"127.0.0.1:8080": {seqID: 1, err: nil},
			"127.0.0.1:8081": {seqID: 2, err: nil},
			"127.0.0.1:8082": {seqID: 3, err: nil},
		},
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	assert.Equal(t, 3, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Len(t, result.Results, 3)

	// 验证结果
	assert.Equal(t, "127.0.0.1:8080", result.Results[0].Addr)
	assert.Equal(t, uint64(1), result.Results[0].SeqID)
	assert.NoError(t, result.Results[0].Error)

	assert.Equal(t, "127.0.0.1:8081", result.Results[1].Addr)
	assert.Equal(t, uint64(2), result.Results[1].SeqID)
	assert.NoError(t, result.Results[1].Error)

	assert.Equal(t, "127.0.0.1:8082", result.Results[2].Addr)
	assert.Equal(t, uint64(3), result.Results[2].SeqID)
	assert.NoError(t, result.Results[2].Error)
}

// TestExecuteBatchForward_PartialFailure 测试部分失败
func TestExecuteBatchForward_PartialFailure(t *testing.T) {
	addrs := []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		results: map[string]forwardResult{
			"127.0.0.1:8080": {seqID: 1, err: nil},
			"127.0.0.1:8081": {seqID: 0, err: errors.New("connection refused")},
			"127.0.0.1:8082": {seqID: 3, err: nil},
		},
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	assert.Equal(t, 2, result.SuccessCount)
	assert.Equal(t, 1, result.FailureCount)
	assert.Len(t, result.Results, 3)

	// 验证失败地址
	assert.Equal(t, "127.0.0.1:8081", result.Results[1].Addr)
	assert.Error(t, result.Results[1].Error)
	assert.Contains(t, result.Results[1].Error.Error(), "connection refused")
}

// TestExecuteBatchForward_AllFailure 测试全部失败
func TestExecuteBatchForward_AllFailure(t *testing.T) {
	addrs := []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		results: map[string]forwardResult{
			"127.0.0.1:8080": {seqID: 0, err: errors.New("timeout")},
			"127.0.0.1:8081": {seqID: 0, err: errors.New("connection refused")},
			"127.0.0.1:8082": {seqID: 0, err: errors.New("network unreachable")},
		},
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 3, result.FailureCount)
	assert.Len(t, result.Results, 3)

	for _, r := range result.Results {
		assert.Error(t, r.Error)
	}
}

// TestExecuteBatchForward_MaxBatchSize 测试超过最大批量大小
func TestExecuteBatchForward_MaxBatchSize(t *testing.T) {
	// 创建超过 maxBatchSize 的地址列表
	addrs := make([]string, maxBatchSize+10)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("127.0.0.1:%d", 8080+i)
	}

	msgExt := MsgFrame{}
	mock := &mockBatchForwarder{
		results: make(map[string]forwardResult),
	}

	// 添加所有地址的成功结果
	for _, addr := range addrs {
		mock.results[addr] = forwardResult{seqID: 1, err: nil}
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	// 应该只处理前 maxBatchSize 个地址
	assert.Equal(t, maxBatchSize, result.SuccessCount)
	assert.Len(t, result.Results, maxBatchSize)
}

// TestExecuteBatchForward_EmptyAddrs 测试空地址列表
func TestExecuteBatchForward_EmptyAddrs(t *testing.T) {
	addrs := []string{}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Len(t, result.Results, 0)
}

// TestExecuteBatchForward_SingleAddr 测试单个地址
func TestExecuteBatchForward_SingleAddr(t *testing.T) {
	addrs := []string{"127.0.0.1:8080"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		results: map[string]forwardResult{
			"127.0.0.1:8080": {seqID: 42, err: nil},
		},
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	assert.Equal(t, 1, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Equal(t, uint64(42), result.Results[0].SeqID)
}

// TestExecuteBatchForward_ConcurrencyLimit 测试并发限制
func TestExecuteBatchForward_ConcurrencyLimit(t *testing.T) {
	// 创建超过 maxBatchConcurrency 的地址列表
	addrs := make([]string, maxBatchConcurrency*2)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("127.0.0.1:%d", 8080+i)
	}

	msgExt := MsgFrame{}
	mock := &mockBatchForwarder{
		delay:   100 * time.Millisecond, // 每个请求需要 100ms
		results: make(map[string]forwardResult),
	}

	// 添加所有地址的成功结果
	for _, addr := range addrs {
		mock.results[addr] = forwardResult{seqID: 1, err: nil}
	}

	start := time.Now()
	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)
	elapsed := time.Since(start)

	// 如果没有并发限制，20个请求串行需要 2000ms
	// 有并发限制（maxBatchConcurrency=10），最多需要 200ms（2批次）
	assert.Less(t, elapsed, 500*time.Millisecond, "并发限制应该加速执行")
	assert.Equal(t, len(addrs), result.SuccessCount)
}

// TestExecuteBatchForward_ContextCancellation 测试上下文取消
func TestExecuteBatchForward_ContextCancellation(t *testing.T) {
	addrs := []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		delay: 1 * time.Second, // 每个请求需要 1s
		results: map[string]forwardResult{
			"127.0.0.1:8080": {seqID: 1, err: nil},
			"127.0.0.1:8081": {seqID: 2, err: nil},
			"127.0.0.1:8082": {seqID: 3, err: nil},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := executeBatchForward(ctx, addrs, msgExt, mock.forward)

	// 由于上下文超时，部分请求可能失败
	assert.True(t, result.SuccessCount+result.FailureCount > 0, "应该有一些请求被处理")
}

// TestExecuteBatchForward_ResultOrder 测试结果顺序保持
func TestExecuteBatchForward_ResultOrder(t *testing.T) {
	addrs := []string{"addr1", "addr2", "addr3", "addr4", "addr5"}
	msgExt := MsgFrame{}

	mock := &mockBatchForwarder{
		results: map[string]forwardResult{
			"addr1": {seqID: 10, err: nil},
			"addr2": {seqID: 20, err: nil},
			"addr3": {seqID: 30, err: nil},
			"addr4": {seqID: 40, err: nil},
			"addr5": {seqID: 50, err: nil},
		},
	}

	result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

	// 验证结果顺序与输入地址顺序一致
	assert.Equal(t, "addr1", result.Results[0].Addr)
	assert.Equal(t, "addr2", result.Results[1].Addr)
	assert.Equal(t, "addr3", result.Results[2].Addr)
	assert.Equal(t, "addr4", result.Results[3].Addr)
	assert.Equal(t, "addr5", result.Results[4].Addr)

	// 验证 SeqID 顺序
	assert.Equal(t, uint64(10), result.Results[0].SeqID)
	assert.Equal(t, uint64(20), result.Results[1].SeqID)
	assert.Equal(t, uint64(30), result.Results[2].SeqID)
	assert.Equal(t, uint64(40), result.Results[3].SeqID)
	assert.Equal(t, uint64(50), result.Results[4].SeqID)
}

// ========================================
// 集成测试
// ========================================

// TestTransportCommon_Integration 测试公共函数集成场景
func TestTransportCommon_Integration(t *testing.T) {
	t.Run("场景1: 配置验证 + 批量转发", func(t *testing.T) {
		// 验证配置
		config := &TransportConfig{
			ListenAddr:         "127.0.0.1:8080",
			MaxMessageSize:     10 * 1024 * 1024, // 10MB
			ReadTimeout:        30 * time.Second,
			WriteTimeout:       30 * time.Second,
			KeepAliveInterval:  10 * time.Second,
			KeepAliveTimeout:   30 * time.Second,
			BufferSize:         8192,
			ChannelSendTimeout: 5 * time.Second,
		}

		err := validateTransportConfig(config)
		require.NoError(t, err)

		// 执行批量转发
		addrs := []string{"127.0.0.1:8081", "127.0.0.1:8082"}
		msgExt := MsgFrame{}

		mock := &mockBatchForwarder{
			results: map[string]forwardResult{
				"127.0.0.1:8081": {seqID: 1, err: nil},
				"127.0.0.1:8082": {seqID: 2, err: nil},
			},
		}

		result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

		assert.Equal(t, 2, result.SuccessCount)
		assert.Equal(t, 0, result.FailureCount)
	})

	t.Run("场景2: 序列号生成 + 批量转发", func(t *testing.T) {
		var counter atomic.Uint64
		currentSeq := uint64(0)

		// 使用自定义序列号生成器
		generator := func() uint64 {
			currentSeq += 100 // 每次增加 100
			return currentSeq
		}

		// 生成序列号
		seq1 := generateMsgSeq(generator, &counter)
		seq2 := generateMsgSeq(generator, &counter)

		assert.Equal(t, uint64(100), seq1)
		assert.Equal(t, uint64(200), seq2)

		// 执行批量转发（验证序列号未被重置）
		addrs := []string{"127.0.0.1:8081"}
		msgExt := MsgFrame{}

		mock := &mockBatchForwarder{
			results: map[string]forwardResult{
				"127.0.0.1:8081": {seqID: seq2, err: nil},
			},
		}

		result := executeBatchForward(context.Background(), addrs, msgExt, mock.forward)

		assert.Equal(t, 1, result.SuccessCount)
		assert.Equal(t, uint64(200), result.Results[0].SeqID)
		assert.Equal(t, uint64(0), counter.Load()) // 默认计数器未被使用
	})

	t.Run("场景3: 错误处理流程", func(t *testing.T) {
		// 无效配置
		invalidConfig := &TransportConfig{
			ListenAddr: "", // 空地址
		}

		err := validateTransportConfig(invalidConfig)
		assert.Error(t, err)

		// 创建批量转发失败结果
		addrs := []string{"127.0.0.1:8081", "127.0.0.1:8082"}
		result := createBatchForwardResult(addrs, err)

		assert.Equal(t, 0, result.SuccessCount)
		assert.Equal(t, 2, result.FailureCount)

		// 验证错误信息传递
		for _, r := range result.Results {
			assert.Same(t, err, r.Error)
		}
	})
}
