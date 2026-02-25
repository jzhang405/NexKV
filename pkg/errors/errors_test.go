package errors

import (
	stderrors "errors"
	"testing"
)

// TestSentinelErrors 测试 sentinel errors 定义
func TestSentinelErrors(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		want  string
		layer string
	}{
		// 通用错误
		{"ErrCanceled", ErrCanceled, "operation canceled", "common"},
		{"ErrTimeout", ErrTimeout, "operation timeout", "common"},
		{"ErrCompleted", ErrCompleted, "operation already completed", "common"},
		{"ErrAlreadyCanceled", ErrAlreadyCanceled, "operation already canceled", "common"},
		{"ErrInvalidParam", ErrInvalidParam, "invalid parameter", "common"},

		// Transport 层错误
		{"ErrTransportClosed", ErrTransportClosed, "transport: is closed", "transport"},
		{"ErrAlreadyConnected", ErrAlreadyConnected, "transport: already connected", "transport"},
		{"ErrConnectionFailed", ErrConnectionFailed, "transport: connection failed", "transport"},
		{"ErrNotConnected", ErrNotConnected, "transport: not connected", "transport"},
		{"ErrChannelClosed", ErrChannelClosed, "transport: channel is closed", "transport"},
		{"ErrMessageTooLarge", ErrMessageTooLarge, "transport: message size exceeds limit", "transport"},
		{"ErrInvalidMessage", ErrInvalidMessage, "transport: invalid message format", "transport"},
		{"ErrNodeNotFound", ErrNodeNotFound, "transport: node not found", "transport"},
		{"ErrPeerIDInvalid", ErrPeerIDInvalid, "transport: invalid peer ID format", "transport"},
		{"ErrAddrInvalid", ErrAddrInvalid, "transport: invalid address format", "transport"},
		{"ErrAddrTooLong", ErrAddrTooLong, "transport: address too long", "transport"},

		// 异步模块错误
		{"ErrAsyncExecFailed", ErrAsyncExecFailed, "async: operation failed", "async"},
		{"ErrCallbackPanic", ErrCallbackPanic, "async: callback panic recovered", "async"},

		// RPC 层错误
		{"ErrMajorityFailed", ErrMajorityFailed, "rpc: majority quorum not reached", "rpc"},
		{"ErrAllFailed", ErrAllFailed, "rpc: all nodes failed", "rpc"},
		{"ErrPeerUnreachable", ErrPeerUnreachable, "rpc: peer unreachable", "rpc"},
		{"ErrNoHandler", ErrNoHandler, "rpc: no handler registered", "rpc"},
		{"ErrCodecFailure", ErrCodecFailure, "rpc: codec failure", "rpc"},
		{"ErrStrategyNotMajority", ErrStrategyNotMajority, "rpc: strategy satisfied but not majority", "rpc"},
		{"ErrInvalidStrategy", ErrInvalidStrategy, "rpc: invalid response strategy", "rpc"},

		// Middleware 层错误
		{"ErrChainFrozen", ErrChainFrozen, "middleware: chain is frozen", "middleware"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("%s.Error() = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// TestNexError_Error 测试 NexError.Error() 方法
func TestNexError_Error(t *testing.T) {
	tests := []struct {
		name    string
		nexErr  *NexError
		wantMsg string
	}{
		{
			name:    "with details",
			nexErr:  &NexError{Err: ErrTimeout, Details: "after 30s"},
			wantMsg: "operation timeout: after 30s",
		},
		{
			name:    "empty details",
			nexErr:  &NexError{Err: ErrCanceled, Details: ""},
			wantMsg: "operation canceled",
		},
		{
			name:    "no details",
			nexErr:  &NexError{Err: ErrTimeout},
			wantMsg: "operation timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.nexErr.Error(); got != tt.wantMsg {
				t.Errorf("NexError.Error() = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

// TestNexError_Unwrap 测试 NexError.Unwrap() 方法
func TestNexError_Unwrap(t *testing.T) {
	inner := ErrTimeout
	nexErr := &NexError{Err: inner, Details: "test"}

	unwrapped := nexErr.Unwrap()
	if unwrapped != inner {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, inner)
	}
}

// TestNexError_Is 测试 NexError.Is() 方法（支持 errors.Is）
func TestNexError_Is(t *testing.T) {
	// 创建包装错误
	wrapped := Wrap(ErrTimeout, "after 30s")

	// 测试 errors.Is 匹配
	if !stderrors.Is(wrapped, ErrTimeout) {
		t.Error("errors.Is(wrapped, ErrTimeout) = false, want true")
	}

	// 测试 errors.Is 不匹配
	if stderrors.Is(wrapped, ErrCanceled) {
		t.Error("errors.Is(wrapped, ErrCanceled) = true, want false")
	}

	// 测试 Is 方法直接调用
	if !wrapped.Is(ErrTimeout) {
		t.Error("wrapped.Is(ErrTimeout) = false, want true")
	}
}

// TestWrap 测试 Wrap 函数
func TestWrap(t *testing.T) {
	// 基本包装
	err := Wrap(ErrTimeout, "after 30s")
	if err.Err != ErrTimeout {
		t.Errorf("Wrap().Err = %v, want %v", err.Err, ErrTimeout)
	}
	if err.Details != "after 30s" {
		t.Errorf("Wrap().Details = %q, want %q", err.Details, "after 30s")
	}

	// 空 details
	err2 := Wrap(ErrCanceled, "")
	if err2.Details != "" {
		t.Errorf("Wrap().Details = %q, want empty", err2.Details)
	}
}

// TestWrapf 测试 Wrapf 函数
func TestWrapf(t *testing.T) {
	err := Wrapf(ErrTimeout, "after %d seconds", 30)
	if err.Err != ErrTimeout {
		t.Errorf("Wrapf().Err = %v, want %v", err.Err, ErrTimeout)
	}
	if err.Details != "after 30 seconds" {
		t.Errorf("Wrapf().Details = %q, want %q", err.Details, "after 30 seconds")
	}
}

// TestWrap_NestedPrevention 测试 Wrap 嵌套保护（P1-3 修复）
func TestWrap_NestedPrevention(t *testing.T) {
	// 第一次包装
	first := Wrap(ErrTimeout, "first detail")

	// 第二次包装（应该自动解包并合并）
	second := Wrap(first, "second detail")

	// 验证内部错误是原始 sentinel error
	if second.Err != ErrTimeout {
		t.Errorf("nested Wrap().Err = %v, want %v", second.Err, ErrTimeout)
	}

	// 验证 details 被合并
	expectedDetails := "first detail; second detail"
	if second.Details != expectedDetails {
		t.Errorf("nested Wrap().Details = %q, want %q", second.Details, expectedDetails)
	}

	// 验证 errors.Is 仍然工作
	if !stderrors.Is(second, ErrTimeout) {
		t.Error("errors.Is(nested wrap, ErrTimeout) = false, want true")
	}
}

// TestWrapf_NestedPrevention 测试 Wrapf 嵌套保护（P1-3 修复）
func TestWrapf_NestedPrevention(t *testing.T) {
	// 第一次包装
	first := Wrap(ErrTimeout, "first detail")

	// 第二次包装（格式化）
	second := Wrapf(first, "second detail: %s", "extra info")

	// 验证内部错误是原始 sentinel error
	if second.Err != ErrTimeout {
		t.Errorf("nested Wrapf().Err = %v, want %v", second.Err, ErrTimeout)
	}

	// 验证 details 被合并
	expectedDetails := "first detail; second detail: extra info"
	if second.Details != expectedDetails {
		t.Errorf("nested Wrapf().Details = %q, want %q", second.Details, expectedDetails)
	}
}

// TestWrap_NestedWithEmptyDetails 测试嵌套时空 details 处理
func TestWrap_NestedWithEmptyDetails(t *testing.T) {
	// 第一层包装带 details
	first := Wrap(ErrTimeout, "first detail")

	// 第二层包装空 details
	second := Wrap(first, "")

	// details 应该保持不变
	if second.Details != "first detail" {
		t.Errorf("Wrap(first, \"\").Details = %q, want %q", second.Details, "first detail")
	}

	// 反过来：第一层空，第二层有
	firstEmpty := Wrap(ErrTimeout, "")
	secondWithDetails := Wrap(firstEmpty, "second detail")

	if secondWithDetails.Details != "second detail" {
		t.Errorf("Wrap(firstEmpty, \"second\").Details = %q, want %q", secondWithDetails.Details, "second detail")
	}
}

// TestErrorPrefixConsistency 测试错误消息前缀一致性（P2-2 验证）
func TestErrorPrefixConsistency(t *testing.T) {
	// Transport 层错误应该有 "transport:" 前缀
	transportErrors := []error{
		ErrTransportClosed, ErrAlreadyConnected, ErrConnectionFailed,
		ErrNotConnected, ErrChannelClosed, ErrMessageTooLarge,
		ErrInvalidMessage, ErrNodeNotFound, ErrPeerIDInvalid,
		ErrAddrInvalid, ErrAddrTooLong,
	}

	for _, err := range transportErrors {
		if !hasPrefix(err.Error(), "transport:") {
			t.Errorf("Transport error %q should have 'transport:' prefix", err.Error())
		}
	}

	// RPC 层错误应该有 "rpc:" 前缀
	rpcErrors := []error{
		ErrMajorityFailed, ErrAllFailed, ErrPeerUnreachable,
		ErrNoHandler, ErrCodecFailure, ErrStrategyNotMajority,
		ErrInvalidStrategy,
	}

	for _, err := range rpcErrors {
		if !hasPrefix(err.Error(), "rpc:") {
			t.Errorf("RPC error %q should have 'rpc:' prefix", err.Error())
		}
	}

	// Middleware 层错误应该有 "middleware:" 前缀
	if !hasPrefix(ErrChainFrozen.Error(), "middleware:") {
		t.Errorf("Middleware error %q should have 'middleware:' prefix", ErrChainFrozen.Error())
	}

	// Async 模块错误应该有 "async:" 前缀
	asyncErrors := []error{ErrAsyncExecFailed, ErrCallbackPanic}
	for _, err := range asyncErrors {
		if !hasPrefix(err.Error(), "async:") {
			t.Errorf("Async error %q should have 'async:' prefix", err.Error())
		}
	}
}

// hasPrefix 检查字符串是否有指定前缀
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// TestErrorsIsCompatibility 测试与标准库 errors.Is 的兼容性
func TestErrorsIsCompatibility(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		target error
		wantIs bool
	}{
		{"direct match", ErrTimeout, ErrTimeout, true},
		{"no match", ErrTimeout, ErrCanceled, false},
		{"wrapped match", Wrap(ErrTimeout, "detail"), ErrTimeout, true},
		{"double wrapped match", Wrap(Wrap(ErrTimeout, "first"), "second"), ErrTimeout, true},
		{"wrapped no match", Wrap(ErrTimeout, "detail"), ErrCanceled, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stderrors.Is(tt.err, tt.target)
			if got != tt.wantIs {
				t.Errorf("errors.Is(%v, %v) = %v, want %v", tt.err, tt.target, got, tt.wantIs)
			}
		})
	}
}

// TestErrorsAsCompatibility 测试与标准库 errors.As 的兼容性
func TestErrorsAsCompatibility(t *testing.T) {
	// 创建包装错误
	wrapped := Wrap(ErrTimeout, "test detail")

	// 使用 errors.As 提取 NexError
	var nexErr *NexError
	if !stderrors.As(wrapped, &nexErr) {
		t.Fatal("errors.As failed to extract NexError")
	}

	if nexErr.Err != ErrTimeout {
		t.Errorf("extracted NexError.Err = %v, want %v", nexErr.Err, ErrTimeout)
	}
	if nexErr.Details != "test detail" {
		t.Errorf("extracted NexError.Details = %q, want %q", nexErr.Details, "test detail")
	}
}

// TestNexError_Unwrap_Nil 测试 nil receiver 的 Unwrap
func TestNexError_Unwrap_Nil(t *testing.T) {
	var nexErr *NexError
	unwrapped := nexErr.Unwrap()
	if unwrapped != nil {
		t.Errorf("nil NexError.Unwrap() = %v, want nil", unwrapped)
	}
}

// TestNexError_Is_Nil 测试 nil receiver 的 Is
func TestNexError_Is_Nil(t *testing.T) {
	var nexErr *NexError
	if nexErr.Is(ErrTimeout) {
		t.Error("nil NexError.Is() = true, want false")
	}
}

// TestNexError_Error_NilReceiver 测试 nil receiver 的 Error
func TestNexError_Error_NilReceiver(t *testing.T) {
	var nexErr *NexError
	got := nexErr.Error()
	if got != "error: nil" {
		t.Errorf("nil NexError.Error() = %q, want %q", got, "error: nil")
	}
}

// TestNexError_Error_NilErr 测试 nil Err 的 Error
func TestNexError_Error_NilErr(t *testing.T) {
	nexErr := &NexError{Err: nil, Details: ""}
	got := nexErr.Error()
	if got != "error: nil" {
		t.Errorf("NexError{nil, \"\"}.Error() = %q, want %q", got, "error: nil")
	}
}

// TestNexError_Error_NilErrWithDetails 测试 nil Err 但有 Details 的 Error
func TestNexError_Error_NilErrWithDetails(t *testing.T) {
	nexErr := &NexError{Err: nil, Details: "something went wrong"}
	got := nexErr.Error()
	if got != "something went wrong" {
		t.Errorf("NexError{nil, details}.Error() = %q, want %q", got, "something went wrong")
	}
}

// TestWrap_Nil 测试 Wrap nil 错误
func TestWrap_Nil(t *testing.T) {
	got := Wrap(nil, "details")
	if got != nil {
		t.Errorf("Wrap(nil, details) = %v, want nil", got)
	}
}

// TestWrapf_Nil 测试 Wrapf nil 错误
func TestWrapf_Nil(t *testing.T) {
	got := Wrapf(nil, "format %s", "arg")
	if got != nil {
		t.Errorf("Wrapf(nil, format) = %v, want nil", got)
	}
}

// TestErrorsIs_WithNilNexError 测试 errors.Is 与 nil NexError
func TestErrorsIs_WithNilNexError(t *testing.T) {
	var nexErr *NexError
	// 这不应该 panic
	if stderrors.Is(nexErr, ErrTimeout) {
		t.Error("errors.Is(nil NexError, ErrTimeout) = true, want false")
	}
}

// TestErrorsAs_WithNilNexError 测试 errors.As 与 nil NexError
func TestErrorsAs_WithNilNexError(t *testing.T) {
	var nexErr *NexError
	var target *NexError
	// nil NexError 的 errors.As 行为由标准库决定
	// 这里主要确保不会 panic
	_ = stderrors.As(nexErr, &target)
}

// TestNexError_Is_TargetNil 测试 Is 与 nil target
func TestNexError_Is_TargetNil(t *testing.T) {
	nexErr := Wrap(ErrTimeout, "details")
	// Is 与 nil target 应该返回 false
	if nexErr.Is(nil) {
		t.Error("NexError.Is(nil) = true, want false")
	}
}

// TestConcurrentErrors 测试并发场景下的错误处理
func TestConcurrentErrors(t *testing.T) {
	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func(id int) {
			err := Wrapf(ErrTimeout, "goroutine %d", id)
			if !stderrors.Is(err, ErrTimeout) {
				t.Errorf("goroutine %d: errors.Is failed", id)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
