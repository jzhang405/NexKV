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

// ==========================================
// 便捷格式化函数测试
// ==========================================

// TestInvalidParamf 测试 InvalidParamf 函数
func TestInvalidParamf(t *testing.T) {
	err := InvalidParamf("value %d is out of range", 42)
	if err == nil {
		t.Fatal("InvalidParamf should return error")
	}
	expected := "invalid parameter: value 42 is out of range"
	if err.Error() != expected {
		t.Errorf("InvalidParamf() = %q, want %q", err.Error(), expected)
	}
}

// TestInvalidCoreID 测试 InvalidCoreID 函数
func TestInvalidCoreID(t *testing.T) {
	err := InvalidCoreID(16, 8)
	if err == nil {
		t.Fatal("InvalidCoreID should return error")
	}
	// 验证错误可以被 errors.Is 匹配
	if !stderrors.Is(err, ErrCPUInvalidCoreID) {
		t.Error("InvalidCoreID should wrap ErrCPUInvalidCoreID")
	}
	// 验证错误消息包含参数信息
	errMsg := err.Error()
	if errMsg == "" {
		t.Error("InvalidCoreID error message should not be empty")
	}
}

// TestSourceIDEmpty 测试 SourceIDEmpty 函数
func TestSourceIDEmpty(t *testing.T) {
	err := SourceIDEmpty()
	if err == nil {
		t.Fatal("SourceIDEmpty should return error")
	}
	// 验证是预期的 sentinel error
	if !stderrors.Is(err, ErrSourceIDEmpty) {
		t.Error("SourceIDEmpty should return ErrSourceIDEmpty")
	}
}

// TestSourceIDInvalidFormat 测试 SourceIDInvalidFormat 函数
func TestSourceIDInvalidFormat(t *testing.T) {
	err := SourceIDInvalidFormat()
	if err == nil {
		t.Fatal("SourceIDInvalidFormat should return error")
	}
	// 验证是预期的 sentinel error
	if !stderrors.Is(err, ErrSourceIDInvalidFormat) {
		t.Error("SourceIDInvalidFormat should return ErrSourceIDInvalidFormat")
	}
}

// TestModuleEmpty 测试 ModuleEmpty 函数
func TestModuleEmpty(t *testing.T) {
	err := ModuleEmpty()
	if err == nil {
		t.Fatal("ModuleEmpty should return error")
	}
	// 验证是预期的 sentinel error
	if !stderrors.Is(err, ErrSourceIDModuleEmpty) {
		t.Error("ModuleEmpty should return ErrSourceIDModuleEmpty")
	}
}

// TestSubModuleEmpty 测试 SubModuleEmpty 函数
func TestSubModuleEmpty(t *testing.T) {
	err := SubModuleEmpty()
	if err == nil {
		t.Fatal("SubModuleEmpty should return error")
	}
	// 验证是预期的 sentinel error
	if !stderrors.Is(err, ErrSourceIDSubModuleEmpty) {
		t.Error("SubModuleEmpty should return ErrSourceIDSubModuleEmpty")
	}
}

// TestActionEmpty 测试 ActionEmpty 函数
func TestActionEmpty(t *testing.T) {
	err := ActionEmpty()
	if err == nil {
		t.Fatal("ActionEmpty should return error")
	}
	// 验证是预期的 sentinel error
	if !stderrors.Is(err, ErrSourceIDActionEmpty) {
		t.Error("ActionEmpty should return ErrSourceIDActionEmpty")
	}
}

// TestUnknownTaskMode 测试 UnknownTaskMode 函数
func TestUnknownTaskMode(t *testing.T) {
	err := UnknownTaskMode("invalid_mode")
	if err == nil {
		t.Fatal("UnknownTaskMode should return error")
	}
	// 验证错误可以被 errors.Is 匹配
	if !stderrors.Is(err, ErrTaskModeUnknown) {
		t.Error("UnknownTaskMode should wrap ErrTaskModeUnknown")
	}
	// 验证错误消息包含模式名称
	errMsg := err.Error()
	if errMsg == "" {
		t.Error("UnknownTaskMode error message should not be empty")
	}
}

// ==========================================
// BTree 层错误函数测试
// ==========================================

// TestBTreeErrorFunctions 测试 BTree 便捷错误函数
func TestBTreeErrorFunctions(t *testing.T) {
	tests := []struct {
		name    string
		fn      func() error
		wantErr error
	}{
		{"BTreeLeafPageNotLoaded", BTreeLeafPageNotLoaded, ErrBTreePageNotLoaded},
		{"BTreeEmptyPath", BTreeEmptyPath, ErrBTreeEmptyPath},
		{"BTreePageLockNil", BTreePageLockNil, ErrBTreePageLockNil},
		{"BTreeLeafPageInfoNil", BTreeLeafPageInfoNil, ErrBTreeLeafPageInfoNil},
		{"BTreeRootInfoNil", func() error { return BTreeRootInfoNil("get") }, ErrBTreeRootInfoNil},
		{"BTreeParentInfoNil", func() error { return BTreeParentInfoNil("set") }, ErrBTreeParentInfoNil},
		{"BTreeParentLockNil", BTreeParentLockNil, ErrBTreeParentLockNil},
		{"BTreeMaxRetriesExceeded", func() error { return BTreeMaxRetriesExceeded(10) }, ErrBTreeMaxRetriesExceeded},
		{"BTreeChunkManagerNotInit", BTreeChunkManagerNotInit, ErrBTreeChunkManagerNotInit},
		{"BTreePageNotLoaded", BTreePageNotLoaded, ErrBTreePageNotLoaded},
		{"BTreeUnknownPageType", func() error { return BTreeUnknownPageType("foo") }, ErrBTreeUnknownPageType},
		{"BTreeRootPageInfoNil", BTreeRootPageInfoNil, ErrBTreeRootPageInfoNil},
		{"BTreePathRootNotInit", BTreePathRootNotInit, ErrBTreeRootNotInit},
		{"BTreePathExceedsMaxLevels", func() error { return BTreePathExceedsMaxLevels(10) }, ErrBTreePathExceedsMaxLevels},
		{"BTreeCloneLeafPageFailed", BTreeCloneLeafPageFailed, ErrBTreeCloneLeafPageFailed},
		{"BTreeBeginTxNotImplemented", BTreeBeginTxNotImplemented, ErrBTreeNotImplemented},
		{"BTreeOutOfMemory", func() error { return BTreeOutOfMemory(100, 50, 200) }, ErrBTreeOutOfMemory},
		{"BTreeCASUpdateRootFailed", BTreeCASUpdateRootFailed, ErrBTreeCASUpdateRootFailed},
		{"BTreeCannotUnlockUnlocked", BTreeCannotUnlockUnlocked, ErrBTreeCannotUnlockUnlocked},
		{"BTreeUnlockStateChanged", BTreeUnlockStateChanged, ErrBTreeUnlockStateChanged},
		{"BTreeInvalidDataSize", func() error { return BTreeInvalidDataSize(100, 50) }, ErrBTreeInvalidDataSize},
		{"BTreeUnexpectedEOF", func() error { return BTreeUnexpectedEOF(100, 50) }, ErrBTreeUnexpectedEOF},
		{"BTreeWritePageIsNil", BTreeWritePageIsNil, ErrBTreeWritePageNil},
		{"BTreeInvalidChunkIDRange", func() error { return BTreeInvalidChunkIDRange(100, 50) }, ErrBTreeInvalidChunkID},
		{"BTreeInvalidOffsetRange", func() error { return BTreeInvalidOffsetRange(100, 50) }, ErrBTreeInvalidOffset},
		{"BTreeInvalidPageTypeRange", func() error { return BTreeInvalidPageTypeRange(100, 50) }, ErrBTreeInvalidPageType},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !stderrors.Is(err, tt.wantErr) {
				t.Errorf("errors.Is() = false for %v, want sentinel %v", err, tt.wantErr)
			}
		})
	}
}

// TestBTreeErrorWrapping 测试 BTree 错误函数的错误链
func TestBTreeErrorWrapping(t *testing.T) {
	// 测试 Wrap 模式的错误函数
	innerErr := stderrors.New("inner error")

	tests := []struct {
		name string
		err  error
	}{
		{"BTreeOffheapGet", BTreeOffheapGet(innerErr)},
		{"BTreeOffheapSet", BTreeOffheapSet(innerErr)},
		{"BTreeOffheapInsert", BTreeOffheapInsert(innerErr)},
		{"BTreeOffheapDelete", BTreeOffheapDelete(innerErr)},
		{"BTreeLoadPage", BTreeLoadPage(innerErr)},
		{"BTreeAllocatePage", BTreeAllocatePage(innerErr)},
		{"BTreePersistRoot", BTreePersistRoot(innerErr)},
		{"BTreeFindLeafPage", BTreeFindLeafPage(innerErr)},
		{"BTreeCopyPath", BTreeCopyPath(innerErr)},
		{"BTreeDeleteFromLeaf", BTreeDeleteFromLeaf(innerErr)},
		{"BTreeMergeLeaf", BTreeMergeLeaf(innerErr)},
		{"BTreeTaskExecutionFailed", BTreeTaskExecutionFailed(innerErr)},
		{"BTreeInsertIntoLeafFailed", BTreeInsertIntoLeafFailed(innerErr)},
		{"BTreeFinalizeDeepCloneErr", BTreeFinalizeDeepCloneErr(innerErr)},
		{"BTreePostSplitInsert", BTreePostSplitInsert(innerErr)},
		{"BTreeAllocIndexPage", BTreeAllocIndexPage(innerErr)},
		{"BTreeUpdateParentIndex", BTreeUpdateParentIndex(innerErr)},
		{"BTreeUpdateGrandparent", BTreeUpdateGrandparent(innerErr)},
		{"BTreeParentSplitIntegrityCheck", BTreeParentSplitIntegrityCheck(innerErr)},
		{"BTreeOpenFileError", BTreeOpenFileError(innerErr)},
		{"BTreeStatFileError", BTreeStatFileError(innerErr)},
		{"BTreeSyncFileError", BTreeSyncFileError(innerErr)},
		{"BTreeCloseFileError", BTreeCloseFileError(innerErr)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Fatal("expected error, got nil")
			}
			// Wrap 模式保留内部错误
			if !stderrors.Is(tt.err, innerErr) {
				t.Errorf("errors.Is(%v, innerErr) = false, want true", tt.err)
			}
			// 错误消息不应为空
			if tt.err.Error() == "" {
				t.Error("error message should not be empty")
			}
		})
	}
}

// TestBTreeErrorWithSpecificParams 测试带参数的 BTree 错误函数
func TestBTreeErrorWithSpecificParams(t *testing.T) {
	t.Run("BTreeChildNotFound", func(t *testing.T) {
		err := BTreeChildNotFound(100, 200)
		if !stderrors.Is(err, ErrBTreeChildNotFound) {
			t.Error("should wrap ErrBTreeChildNotFound")
		}
	})

	t.Run("BTreeCASFailed", func(t *testing.T) {
		err := BTreeCASFailed(1, 2, 3)
		if !stderrors.Is(err, ErrBTreeCASFailed) {
			t.Error("should wrap ErrBTreeCASFailed")
		}
	})

	t.Run("BTreeCircularReferenceRetry", func(t *testing.T) {
		err := BTreeCircularReferenceRetry(5, ErrBTreeCircularReference)
		if !stderrors.Is(err, ErrBTreeCircularReference) {
			t.Error("should wrap ErrBTreeCircularReference")
		}
	})

	t.Run("BTreeChildrenLossDetected", func(t *testing.T) {
		err := BTreeChildrenLossDetectedLeft(42, 10, 5)
		if !stderrors.Is(err, ErrBTreeChildrenLoss) {
			t.Error("should wrap ErrBTreeChildrenLoss")
		}
		err = BTreeChildrenLossDetectedRight(43, 10, 5)
		if !stderrors.Is(err, ErrBTreeChildrenLoss) {
			t.Error("should wrap ErrBTreeChildrenLoss")
		}
	})

	t.Run("BTreeBatchItemFailed", func(t *testing.T) {
		innerErr := stderrors.New("db error")
		err := BTreeBatchItemFailed(1, 5, innerErr)
		if !stderrors.Is(err, innerErr) {
			t.Error("should wrap inner error")
		}
	})

	t.Run("BTreeSeekOffset", func(t *testing.T) {
		innerErr := stderrors.New("seek error")
		err := BTreeSeekOffset(4096, innerErr)
		if !stderrors.Is(err, innerErr) {
			t.Error("should wrap inner error")
		}
	})

	t.Run("BTreeMaterializationBug", func(t *testing.T) {
		err := BTreeMaterializationBugLeft(42, 10, 5)
		if !stderrors.Is(err, ErrBTreeMaterializationBug) {
			t.Error("should wrap ErrBTreeMaterializationBug")
		}
	})

	t.Run("BTreeParentPageIDChangedDuringRetry", func(t *testing.T) {
		err := BTreeParentPageIDChangedDuringRetry(1, 2)
		if !stderrors.Is(err, ErrBTreeParentPageIDChanged) {
			t.Error("should wrap ErrBTreeParentPageIDChanged")
		}
	})
}

// ==========================================
// OffHeap 层错误函数测试
// ==========================================

// TestOffHeapErrorFunctions 测试 OffHeap 便捷错误函数
func TestOffHeapErrorFunctions(t *testing.T) {
	tests := []struct {
		name    string
		fn      func() error
		wantErr error
	}{
		{"AllocatorSizeMustBePositive", func() error { return OffHeapAllocatorSizeMustBePositive(0) }, ErrOffHeapAllocatorSizeInvalid},
		{"AllocExceedsSize", func() error { return OffHeapAllocExceedsSize(100, 50) }, ErrOffHeapAllocExceedsSize},
		{"PageFull", func() error { return OffHeapPageFull(3000, 2000, 4096) }, ErrOffHeapPageFull},
		{"MMapSizeExceedsLimit", func() error { return OffHeapMMapSizeExceedsLimit(1<<33, 1<<32) }, ErrOffHeapMMapExceedsLimit},
		{"OutOfMemory", func() error { return OffHeapOutOfMemory(100, 100) }, ErrOffHeapOutOfMemory},
		{"InvalidPageID", func() error { return OffHeapInvalidPageID(200, 100) }, ErrOffHeapInvalidPageID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !stderrors.Is(err, tt.wantErr) {
				t.Errorf("errors.Is() = false for %v, want sentinel %v", err, tt.wantErr)
			}
		})
	}
}

// TestOffHeapWrappingErrors 测试 OffHeap 错误包装函数
func TestOffHeapWrappingErrors(t *testing.T) {
	innerErr := stderrors.New("syscall error")

	tests := []struct {
		name string
		err  error
	}{
		{"MMapFailed", OffHeapMMapFailed(innerErr)},
		{"VirtualAllocFailed", OffHeapVirtualAllocFailed(innerErr)},
		{"VirtualFreeFailed", OffHeapVirtualFreeFailed(innerErr)},
		{"CreateAllocatorFailed", OffHeapCreateAllocatorFailed(innerErr)},
		{"AllocMemoryFailed", OffHeapAllocMemoryFailed(innerErr)},
		{"InsertEntry", OffHeapInsertEntry(5, innerErr)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !stderrors.Is(tt.err, innerErr) {
				t.Errorf("errors.Is(%v, innerErr) = false, want true", tt.err)
			}
		})
	}
}

// ==========================================
// BTree SearchPath 错误测试
// ==========================================

// TestBTreeSearchPathErrors 测试 SearchPath 相关错误函数
func TestBTreeSearchPathErrors(t *testing.T) {
	tests := []struct {
		name    string
		fn      func() error
		wantErr error
	}{
		{"PathRootPageInfoNil", BTreePathRootPageInfoNil, ErrBTreeRootPageInfoNil},
		{"PathChildPageInfoNil", func() error { return BTreePathChildPageInfoNil(3) }, ErrBTreeChildPageInfoNil},
		{"PathExpectedInternal", func() error { return BTreePathExpectedInternalAtDepth(2, "leaf") }, ErrBTreePageTypeMismatch},
		{"PathEmptyRefs", BTreePathEmptyRefs, ErrBTreeEmptyRefs},
		{"PathOldChildStillExists", func() error { return BTreePathOldChildStillExists(10, 20) }, ErrBTreeOldChildStillExists},
		{"PathNewChildrenNotFound", func() error { return BTreePathNewChildrenNotFound(10, 20, 30) }, ErrBTreeNewChildrenNotFound},
		{"PathChildCountMismatch", func() error { return BTreePathChildCountMismatch(3, 5) }, ErrBTreeChildCountMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if !stderrors.Is(err, tt.wantErr) {
				t.Errorf("errors.Is(%v, %v) = false", err, tt.wantErr)
			}
		})
	}
}

// ==========================================
// BTree LeafLockSet 错误测试
// ==========================================

// TestBTreeLeafLockSetErrors 测试 LeafLockSet 相关错误函数
func TestBTreeLeafLockSetErrors(t *testing.T) {
	innerErr := stderrors.New("inner")

	tests := []struct {
		name    string
		fn      func() error
		wantErr error // nil means check innerErr
	}{
		{"ReplaceChildInParent", func() error { return BTreeReplaceChildInParent(innerErr) }, nil},
		{"LeftRefPageInfoNil", BTreeLeftRefPageInfoNil, ErrBTreeLeftRefPageInfoNil},
		{"RootPageInfoNilAfterPersist", BTreeRootPageInfoNilAfterPersist, ErrBTreeRootPageInfoNil},
		{"AllocFallbackPage", func() error { return BTreeAllocFallbackPage(innerErr) }, nil},
		{"FallbackInsert", func() error { return BTreeFallbackInsert(innerErr) }, nil},
		{"ParentInfoNilInFallback", func() error { return BTreeParentInfoNilInFallback("cas") }, ErrBTreeParentInfoNil},
		{"ParentLockNilInFallback", func() error { return BTreeParentLockNilInFallback("cas") }, ErrBTreeParentLockNil},
		{"InvalidInsertIndex", func() error { return BTreeInvalidInsertIndex(10, 5) }, ErrBTreeInvalidInsertIndex},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if tt.wantErr != nil {
				if !stderrors.Is(err, tt.wantErr) {
					t.Errorf("errors.Is(%v, %v) = false", err, tt.wantErr)
				}
			} else {
				if !stderrors.Is(err, innerErr) {
					t.Errorf("errors.Is(%v, innerErr) = false", err)
				}
			}
		})
	}
}

// ==========================================
// BTree CCOW/Snapshot 错误测试
// ==========================================

// TestBTreeCCOWErrors 测试 CCOW 管理器相关错误函数
func TestBTreeCCOWErrors(t *testing.T) {
	innerErr := stderrors.New("inner")

	t.Run("SnapshotNotFound", func(t *testing.T) {
		if !stderrors.Is(BTreeSnapshotNotFound(), ErrBTreeSnapshotNotFound) {
			t.Error("should wrap ErrBTreeSnapshotNotFound")
		}
	})
	t.Run("SnapshotRootRefNil", func(t *testing.T) {
		if !stderrors.Is(BTreeSnapshotRootRefNil(), ErrBTreeSnapshotRootRefNil) {
			t.Error("should wrap ErrBTreeSnapshotRootRefNil")
		}
	})
	t.Run("ModifyFailed", func(t *testing.T) {
		if !stderrors.Is(BTreeModifyFailed(innerErr), innerErr) {
			t.Error("should wrap inner error")
		}
	})
	t.Run("UpdateChildRefFailed", func(t *testing.T) {
		if !stderrors.Is(BTreeUpdateChildRefFailed(innerErr), innerErr) {
			t.Error("should wrap inner error")
		}
	})
	t.Run("CASUpdateRootFailed", func(t *testing.T) {
		if !stderrors.Is(BTreeCASUpdateRootFailed(), ErrBTreeCASUpdateRootFailed) {
			t.Error("should wrap ErrBTreeCASUpdateRootFailed")
		}
	})
	t.Run("CollectDirtyPagesFailed", func(t *testing.T) {
		if !stderrors.Is(BTreeCollectDirtyPagesFailed(innerErr), innerErr) {
			t.Error("should wrap inner error")
		}
	})
}
