// Package types 单元测试
package types

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestRPCErrorTypeString 测试错误类型字符串表示
func TestRPCErrorTypeString(t *testing.T) {
	tests := []struct {
		name     string
		errType  RPCErrorType
		expected string
	}{
		{"Timeout", RPCErrorTypeTimeout, "TIMEOUT"},
		{"Network", RPCErrorTypeNetwork, "NETWORK"},
		{"Codec", RPCErrorTypeCodec, "CODEC"},
		{"Protocol", RPCErrorTypeProtocol, "PROTOCOL"},
		{"Server", RPCErrorTypeServer, "SERVER"},
		{"Application", RPCErrorTypeApplication, "APPLICATION"},
		{"System", RPCErrorTypeSystem, "SYSTEM"},
		{"Unknown", RPCErrorType(999), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.errType.String(); got != tt.expected {
				t.Errorf("RPCErrorType.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestRPCError_Error 测试错误消息格式
func TestRPCError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *RPCError
		contains []string // 错误消息应该包含的字符串
	}{
		{
			name:     "带原因的错误",
			err:      NewRPCNetworkError("127.0.0.1:9211", errors.New("connection refused")),
			contains: []string{"[RPC_NETWORK_ERROR]", "RPC 网络错误（地址：", "connection refused"},
		},
		{
			name: "不带原因的错误",
			err: &RPCError{
				Code:      "RPC_CUSTOM_ERROR",
				Message:   "自定义错误",
				Type:      RPCErrorTypeSystem,
				Retryable: false,
			},
			contains: []string{"[RPC_CUSTOM_ERROR]", "自定义错误"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errMsg := tt.err.Error()
			for _, substr := range tt.contains {
				if !strings.Contains(errMsg, substr) {
					t.Errorf("Error() = %v, 应该包含 %v", errMsg, substr)
				}
			}
		})
	}
}

// TestRPCError_Is 测试错误匹配
func TestRPCError_Is(t *testing.T) {
	baseErr := NewRPCRequestTimeout(10*time.Second, "127.0.0.1:9211")

	tests := []struct {
		name     string
		err      error
		target   error
		expected bool
	}{
		{
			name:     "相同错误码",
			err:      baseErr,
			target:   NewRPCRequestTimeout(5*time.Second, "192.168.1.1:8080"),
			expected: true, // 错误码相同
		},
		{
			name:     "不同错误码",
			err:      baseErr,
			target:   NewRPCNetworkError("127.0.0.1:9211", nil),
			expected: false, // 错误码不同
		},
		{
			name:     "非 RPCError",
			err:      baseErr,
			target:   errors.New("other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errors.Is(tt.err, tt.target); got != tt.expected {
				t.Errorf("errors.Is() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestIsRPCRetryable 测试可重试错误判断
func TestIsRPCRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil 错误",
			err:      nil,
			expected: false,
		},
		{
			name:     "请求超时",
			err:      NewRPCRequestTimeout(10*time.Second, "127.0.0.1:9211"),
			expected: true,
		},
		{
			name:     "网络错误",
			err:      NewRPCNetworkError("127.0.0.1:9211", nil),
			expected: true,
		},
		{
			name:     "编解码错误",
			err:      NewRPCCodecError("GetRequest", nil),
			expected: false,
		},
		{
			name:     "系统错误",
			err:      ErrRPCTransportClosed,
			expected: false,
		},
		{
			name:     "上下文取消",
			err:      NewRPCContextCanceled(context.Canceled),
			expected: false,
		},
		{
			name:     "标准库网络错误（超时）",
			err:      &netErrorOp{timeout: true},
			expected: true,
		},
		{
			name:     "标准库网络错误（连接拒绝）",
			err:      &netErrorOp{},
			expected: true,
		},
		{
			name:     "普通错误",
			err:      errors.New("other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRPCRetryable(tt.err); got != tt.expected {
				t.Errorf("IsRPCRetryable() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestIsRPCTimeout 测试超时错误判断
func TestIsRPCTimeout(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil 错误",
			err:      nil,
			expected: false,
		},
		{
			name:     "请求超时",
			err:      NewRPCRequestTimeout(10*time.Second, "127.0.0.1:9211"),
			expected: true,
		},
		{
			name:     "网络错误",
			err:      NewRPCNetworkError("127.0.0.1:9211", nil),
			expected: false,
		},
		{
			name:     "context 超时",
			err:      context.DeadlineExceeded,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRPCTimeout(tt.err); got != tt.expected {
				t.Errorf("IsRPCTimeout() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestIsRPCNetworkError 测试网络错误判断
func TestIsRPCNetworkError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil 错误",
			err:      nil,
			expected: false,
		},
		{
			name:     "网络错误",
			err:      NewRPCNetworkError("127.0.0.1:9211", nil),
			expected: true,
		},
		{
			name:     "超时错误",
			err:      NewRPCRequestTimeout(10*time.Second, "127.0.0.1:9211"),
			expected: false,
		},
		{
			name:     "标准库 net.Error",
			err:      &netErrorOp{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRPCNetworkError(tt.err); got != tt.expected {
				t.Errorf("IsRPCNetworkError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestIsRPCApplicationError 测试业务错误判断
func TestIsRPCApplicationError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil 错误",
			err:      nil,
			expected: false,
		},
		{
			name:     "业务错误",
			err:      NewRPCServerError("127.0.0.1:9211", nil),
			expected: true,
		},
		{
			name:     "网络错误",
			err:      NewRPCNetworkError("127.0.0.1:9211", nil),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRPCApplicationError(tt.err); got != tt.expected {
				t.Errorf("IsRPCApplicationError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestIsRPCSystemError 测试系统错误判断
func TestIsRPCSystemError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil 错误",
			err:      nil,
			expected: false,
		},
		{
			name:     "Transport 关闭",
			err:      ErrRPCTransportClosed,
			expected: true,
		},
		{
			name:     "上下文取消",
			err:      NewRPCContextCanceled(context.Canceled),
			expected: true,
		},
		{
			name:     "context.Canceled",
			err:      context.Canceled,
			expected: true,
		},
		{
			name:     "网络错误",
			err:      NewRPCNetworkError("127.0.0.1:9211", nil),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRPCSystemError(tt.err); got != tt.expected {
				t.Errorf("IsRPCSystemError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestGetRPCErrorCode 测试获取错误码
func TestGetRPCErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil 错误",
			err:      nil,
			expected: "",
		},
		{
			name:     "RPC 错误",
			err:      NewRPCRequestTimeout(10*time.Second, "127.0.0.1:9211"),
			expected: "RPC_REQUEST_TIMEOUT",
		},
		{
			name:     "普通错误",
			err:      errors.New("other error"),
			expected: "UNKNOWN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetRPCErrorCode(tt.err); got != tt.expected {
				t.Errorf("GetRPCErrorCode() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestGetRPCErrorType 测试获取错误类型
func TestGetRPCErrorType(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected RPCErrorType
	}{
		{
			name:     "nil 错误",
			err:      nil,
			expected: RPCErrorTypeSystem,
		},
		{
			name:     "超时错误",
			err:      NewRPCRequestTimeout(10*time.Second, "127.0.0.1:9211"),
			expected: RPCErrorTypeTimeout,
		},
		{
			name:     "网络错误",
			err:      NewRPCNetworkError("127.0.0.1:9211", nil),
			expected: RPCErrorTypeNetwork,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetRPCErrorType(tt.err); got != tt.expected {
				t.Errorf("GetRPCErrorType() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestRPCErrorConstructor 测试错误构造函数
func TestRPCErrorConstructor(t *testing.T) {
	t.Run("NewRPCRequestTimeout", func(t *testing.T) {
		timeout := 5 * time.Second
		addr := "192.168.1.1:8080"
		err := NewRPCRequestTimeout(timeout, addr)

		if err.Code != "RPC_REQUEST_TIMEOUT" {
			t.Errorf("Code = %v, want RPC_REQUEST_TIMEOUT", err.Code)
		}
		if err.Type != RPCErrorTypeTimeout {
			t.Errorf("Type = %v, want RPCErrorTypeTimeout", err.Type)
		}
		if !err.Retryable {
			t.Errorf("Retryable = false, want true")
		}
		if err.TargetAddr != addr {
			t.Errorf("TargetAddr = %v, want %v", err.TargetAddr, addr)
		}
	})

	t.Run("NewRPCNetworkError", func(t *testing.T) {
		addr := "127.0.0.1:9211"
		cause := errors.New("connection refused")
		err := NewRPCNetworkError(addr, cause)

		if err.Code != "RPC_NETWORK_ERROR" {
			t.Errorf("Code = %v, want RPC_NETWORK_ERROR", err.Code)
		}
		if err.Type != RPCErrorTypeNetwork {
			t.Errorf("Type = %v, want RPCErrorTypeNetwork", err.Type)
		}
		if !err.Retryable {
			t.Errorf("Retryable = false, want true")
		}
		if err.Cause != cause {
			t.Errorf("Cause = %v, want %v", err.Cause, cause)
		}
	})

	t.Run("NewRPCCodecError", func(t *testing.T) {
		msgType := "GetRequest"
		cause := errors.New("invalid message format")
		err := NewRPCCodecError(msgType, cause)

		if err.Code != "RPC_CODEC_ERROR" {
			t.Errorf("Code = %v, want RPC_CODEC_ERROR", err.Code)
		}
		if err.Type != RPCErrorTypeCodec {
			t.Errorf("Type = %v, want RPCErrorTypeCodec", err.Type)
		}
		if err.Retryable {
			t.Errorf("Retryable = true, want false")
		}
	})
}

// ========================================
// 测试辅助类型
// ========================================

// netErrorOp 实现 net.Error 接口（用于测试）
type netErrorOp struct {
	timeout bool
}

func (e *netErrorOp) Error() string   { return "network error" }
func (e *netErrorOp) Timeout() bool   { return e.timeout }
func (e *netErrorOp) Temporary() bool { return true }
func (e *netErrorOp) Unwrap() error   { return nil }

// ========================================
// 基准测试（性能验证）
// ========================================

// BenchmarkIsRPCRetryable 性能基准测试：错误重试判断
func BenchmarkIsRPCRetryable(b *testing.B) {
	err := NewRPCRequestTimeout(10*time.Second, "127.0.0.1:9211")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		IsRPCRetryable(err)
	}
}

// BenchmarkIsRPCTimeout 性能基准测试：超时错误判断
func BenchmarkIsRPCTimeout(b *testing.B) {
	err := NewRPCRequestTimeout(10*time.Second, "127.0.0.1:9211")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		IsRPCTimeout(err)
	}
}

// BenchmarkNewRPCRequestTimeout 性能基准测试：错误构造
func BenchmarkNewRPCRequestTimeout(b *testing.B) {
	addr := "127.0.0.1:9211"
	timeout := 10 * time.Second
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewRPCRequestTimeout(timeout, addr)
	}
}
