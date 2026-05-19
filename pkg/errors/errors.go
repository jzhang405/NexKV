// Package errors 提供统一的错误定义
// 采用 sentinel error + NexError 包装模式
package errors

import (
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"
)

// ===========================
// 标准 Sentinel Errors
// ===========================

var (
	// 通用错误
	ErrCanceled        = stderrors.New("operation canceled")
	ErrTimeout         = stderrors.New("operation timeout")
	ErrCompleted       = stderrors.New("operation already completed")
	ErrAlreadyCanceled = stderrors.New("operation already canceled")
	ErrInvalidParam    = stderrors.New("invalid parameter")

	// Transport 层错误
	ErrTransportClosed  = stderrors.New("transport: is closed")
	ErrAlreadyConnected = stderrors.New("transport: already connected")
	ErrConnectionFailed = stderrors.New("transport: connection failed")
	ErrNotConnected     = stderrors.New("transport: not connected")
	ErrChannelClosed    = stderrors.New("transport: channel is closed")
	ErrMessageTooLarge  = stderrors.New("transport: message size exceeds limit")
	ErrInvalidMessage   = stderrors.New("transport: invalid message format")
	ErrNodeNotFound     = stderrors.New("transport: node not found")
	ErrPeerIDInvalid    = stderrors.New("transport: invalid peer ID format")
	ErrAddrInvalid      = stderrors.New("transport: invalid address format")
	ErrAddrTooLong      = stderrors.New("transport: address too long")
	// Async Operation 错误
	ErrAsyncExecFailed           = stderrors.New("async: operation failed")
	ErrCallbackPanic             = stderrors.New("async: callback panic recovered")
	ErrOperationAlreadyCompleted = stderrors.New("async: operation already completed")
	ErrOperationCanceled         = stderrors.New("async: operation canceled")
	ErrCallbackIDEmpty           = stderrors.New("async: callback ID cannot be empty")
	ErrCallbackNotFound          = stderrors.New("async: callback not found")
	ErrCancelNotSupported        = stderrors.New("async: cancel not supported")
	ErrDiscardNotSupported       = stderrors.New("async: discard not supported")
	// GoroutinePool 层错误
	ErrPoolClosed            = stderrors.New("concurrency: goroutine pool is closed")
	ErrPoolFull              = stderrors.New("concurrency: goroutine pool is full")
	ErrTaskArgLengthMismatch = stderrors.New("concurrency: task and argument length mismatch")
	ErrTaskCanceled          = stderrors.New("concurrency: task was canceled")
	ErrTaskTimeout           = stderrors.New("concurrency: task timeout")
	ErrAllWorkersBusy        = stderrors.New("concurrency: all workers are bound to different source IDs")
	ErrTooManyDelayedTasks   = stderrors.New("concurrency: too many delayed tasks") // P1-01
	// TaskExecutor 层错误
	ErrExecutorClosed    = stderrors.New("concurrency: executor is closed")
	ErrExecutorNotFound  = stderrors.New("concurrency: executor not found for requested mode")
	ErrSelectorClosed    = stderrors.New("concurrency: selector is closed")
	ErrDuplicateExecutor = stderrors.New("concurrency: executor already registered for this mode")
	ErrInvalidConfig     = stderrors.New("concurrency: invalid configuration")
	ErrQueueFull         = stderrors.New("concurrency: queue full")
	ErrTaskPanic         = stderrors.New("concurrency: task panic recovered")
	// RPC 层错误
	ErrMajorityFailed      = stderrors.New("rpc: majority quorum not reached")
	ErrAllFailed           = stderrors.New("rpc: all nodes failed")
	ErrPeerUnreachable     = stderrors.New("rpc: peer unreachable")
	ErrNoHandler           = stderrors.New("rpc: no handler registered")
	ErrCodecFailure        = stderrors.New("rpc: codec failure")
	ErrStrategyNotMajority = stderrors.New("rpc: strategy satisfied but not majority")
	ErrInvalidStrategy     = stderrors.New("rpc: invalid response strategy")
	ErrTargetsMsgsMismatch = stderrors.New("rpc: targets and msgs length mismatch")
	ErrInvalidQuorum       = stderrors.New("rpc: invalid quorum value")
	ErrInvalidTimeout      = stderrors.New("rpc: invalid timeout value")
	ErrEmptyPeers          = stderrors.New("rpc: peers slice is empty")
	ErrNilConfig           = stderrors.New("rpc: config is nil")
	ErrNilRPC              = stderrors.New("rpc: rpc is nil")
	// Middleware 层错误
	ErrChainFrozen        = stderrors.New("middleware: chain is frozen")
	ErrRateLimitExceeded  = stderrors.New("middleware: rate limit exceeded")
	ErrCircuitBreakerOpen = stderrors.New("middleware: circuit breaker is open")
	ErrInvalidCompression = stderrors.New("middleware: invalid or unsupported compression")
	// Compressor 层错误
	ErrCompressionFailed   = stderrors.New("compressor: compression failed")
	ErrDecompressionFailed = stderrors.New("compressor: decompression failed")
	ErrUnsupportedType     = stderrors.New("compressor: unsupported compression type")
	ErrDecompressionTooBig = stderrors.New("compressor: decompressed data exceeds size limit")
	// Test Framework 层错误
	ErrTestSetupFailed    = stderrors.New("test: setup failed")
	ErrTestExecuteFailed  = stderrors.New("test: execute failed")
	ErrTestVerifyFailed   = stderrors.New("test: verify failed")
	ErrTestTeardownFailed = stderrors.New("test: teardown failed")
	ErrComponentNotFound  = stderrors.New("test: component not found")
	ErrComponentExists    = stderrors.New("test: component already exists")
	ErrDependencyNotMet   = stderrors.New("test: dependency not met")
	ErrCircularDependency = stderrors.New("test: circular dependency detected")
	ErrClusterNotRunning  = stderrors.New("test: cluster not running")
	ErrTestNodeNotFound   = stderrors.New("test: node not found")
	ErrInvalidState       = stderrors.New("test: invalid cluster state")
	ErrNotImplemented     = stderrors.New("test: not implemented")
	ErrNotInitialized     = stderrors.New("test: not initialized")
	// Clock 层错误
	ErrClockInvalidData = stderrors.New("clock: invalid data")
	ErrClockNilHLC      = stderrors.New("clock: nil HLC")
	ErrClockMarshalNil  = stderrors.New("clock: cannot marshal nil HLC")
	ErrClockInvalidSize = stderrors.New("clock: invalid HLC data size")
	// Per-Core 执行器错误
	ErrPerCoreExecutorClosed  = stderrors.New("percore: executor is closed")
	ErrPerCoreQueueFull       = stderrors.New("percore: task queue is full")
	ErrPerCoreInvalidCore     = stderrors.New("percore: invalid core ID")
	ErrPerCoreShutdownTimeout = stderrors.New("percore: shutdown timeout")
	ErrPerCoreNotSupported    = stderrors.New("percore: operation not supported")

	// Request ID 错误
	ErrRequestIDInvalidFormat = stderrors.New("request: invalid request id format")
	ErrRequestIDEmpty         = stderrors.New("request: request id cannot be empty")
	// CPU 亲和性错误
	ErrCPUInvalidCoreID     = stderrors.New("affinity: invalid core ID")
	ErrCPUSetAffinityFailed = stderrors.New("affinity: sched_setaffinity failed")
	ErrCPUGetCurrentThread  = stderrors.New("affinity: GetCurrentThread failed")
	ErrCPUSetAffinityMask   = stderrors.New("affinity: SetThreadAffinityMask failed")
	// SourceID 错误
	ErrSourceIDEmpty          = stderrors.New("source_id: cannot be empty")
	ErrSourceIDInvalidFormat  = stderrors.New("source_id: must be in format {module}:{sub-module}:{action}")
	ErrSourceIDModuleEmpty    = stderrors.New("source_id: module cannot be empty")
	ErrSourceIDSubModuleEmpty = stderrors.New("source_id: sub-module cannot be empty")
	ErrSourceIDActionEmpty    = stderrors.New("source_id: action cannot be empty")
	// TaskMode 错误
	ErrTaskModeUnknown = stderrors.New("task_mode: unknown task mode")
	// TaskScheduler 错误
	ErrTaskAlreadyRegistered  = stderrors.New("scheduler: task already registered")
	ErrTaskNotFound           = stderrors.New("scheduler: task not found")
	ErrSchedulerNotStarted    = stderrors.New("scheduler: not started")
	ErrSchedulerRunning       = stderrors.New("scheduler: already running")
	ErrExecutionOrderConflict = stderrors.New("scheduler: execution order already registered")
	ErrQueueTooLong           = stderrors.New("scheduler: queue too long")
	// ===========================
	// Storage 层错误
	// ===========================
	// Batch 层错误
	ErrBatchSubmitterClosed = stderrors.New("batch: submitter is closed")
	ErrBatchTimeout         = stderrors.New("batch: submit timeout")
	ErrBatchTooLarge        = stderrors.New("batch: submit too large")
	// Pipeline 层错误
	ErrPipelineClosed          = stderrors.New("pipeline: closed")
	ErrPipelineShutdownTimeout = stderrors.New("pipeline: graceful shutdown timeout")
	// BTree 核心错误（新版)
	ErrBTreeCASConflict       = stderrors.New("btree: cas conflict after max retries")
	ErrBTreeRetry             = stderrors.New("btree: retry operation")
	ErrBTreePageFreed         = stderrors.New("btree: page already freed")
	ErrBTreeKeyNotFound       = stderrors.New("btree: key not found")
	ErrBTreeClosed            = stderrors.New("btree: tree closed")
	ErrBTreeInvalidPage       = stderrors.New("btree: invalid page")
	ErrBTreePageFull          = stderrors.New("btree: page full")
	ErrBTreePageEmpty         = stderrors.New("btree: page empty")
	ErrBTreeDuplicateKey      = stderrors.New("btree: duplicate key")
	ErrBTreeNotImplemented    = stderrors.New("btree: not implemented")
	ErrBTreeBorrowSourceEmpty = stderrors.New("btree: borrow source page is empty")
	ErrBTreeMergeNoSibling    = stderrors.New("btree: merge requires sibling page")
	// OffHeap 错误
	ErrOffHeapAllocatorSizeInvalid  = stderrors.New("offheap: allocator size must be positive")
	ErrOffHeapAllocExceedsSize      = stderrors.New("offheap: alloc size exceeds allocator size")
	ErrOffHeapPageFull              = stderrors.New("offheap: page full")
	ErrOffHeapMMapFailed            = stderrors.New("offheap: mmap failed")
	ErrOffHeapMMapExceedsLimit      = stderrors.New("offheap: mmap size exceeds 32-bit PageID limit")
	ErrOffHeapOutOfMemory           = stderrors.New("offheap: out of memory")
	ErrOffHeapInvalidPageID         = stderrors.New("offheap: invalid page ID")
	ErrOffHeapConstraintViolation   = stderrors.New("offheap: constraint violation")
	ErrOffHeapCreateAllocatorFailed = stderrors.New("offheap: create allocator failed")
	ErrOffHeapAllocMemoryFailed     = stderrors.New("offheap: allocate memory failed")
)

// ===========================
// 增强错误结构（携带上下文信息）
// ===========================
// NexError 增强错误（携带上下文信息）
type NexError struct {
	Err     error  // 原始 sentinel error（必须）
	Details string // 错误详情
}

// Error 实现 error 接口
func (e *NexError) Error() string {
	// Guard against nil receiver
	if e == nil {
		return "error: nil"
	}
	// Guard against nil Err
	if e.Err == nil {
		if e.Details != "" {
			return e.Details
		}
		return "error: nil"
	}
	if e.Details != "" {
		return fmt.Sprintf("%s: %s", e.Err.Error(), e.Details)
	}
	return e.Err.Error()
}

// Unwrap 支持错误链
func (e *NexError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is 支持 errors.Is() 比较（基于 sentinel error）
// 注意：NexError.Err 必须是 sentinel error，不能是另一个 *NexError
func (e *NexError) Is(target error) bool {
	if e == nil {
		return false
	}
	return stderrors.Is(e.Err, target)
}

// ===========================
// 便捷包装函数（返回增强错误）
// ===========================
// Wrap 包装标准错误，携带详情
// 如果 err 本身是 *NexError，会自动解包并合并详情，防止嵌套
func Wrap(err error, details string) *NexError {
	// P0-2 修复：nil 错误返回 nil，避免 panic
	if err == nil {
		return nil
	}
	return mergeNexError(err, details)
}

// Wrapf 包装标准错误，格式化详情
// 如果 err 本身是 *NexError，会自动解包并合并详情，防止嵌套
//
// Deprecated: 在热路径上使用会导致 fmt.Sprintf 分配，建议使用 Wrap* 系列专用函数
func Wrapf(err error, format string, args ...any) *NexError {
	// P0-2 修复：nil 错误返回 nil，避免 panic
	if err == nil {
		return nil
	}
	return mergeNexError(err, fmt.Sprintf(format, args...))
}

// ===========================
// 热路径优化包装函数（避免 fmt.Sprintf 分配）
// ===========================

// WrapInt 包装错误并附带单个 int 值（零分配）
func WrapInt(err error, prefix string, val int) *NexError {
	if err == nil {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(": ")
	sb.WriteString(strconv.Itoa(val))
	return mergeNexError(err, sb.String())
}

// WrapInt2 包装错误并附带两个 int 值（零分配）
func WrapInt2(err error, prefix string, v1, v2 int) *NexError {
	if err == nil {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(": ")
	sb.WriteString(strconv.Itoa(v1))
	sb.WriteString(", ")
	sb.WriteString(strconv.Itoa(v2))
	return mergeNexError(err, sb.String())
}

// WrapUint32 包装错误并附带单个 uint32 值（零分配）
func WrapUint32(err error, prefix string, val uint32) *NexError {
	if err == nil {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(": ")
	sb.WriteString(strconv.FormatUint(uint64(val), 10))
	return mergeNexError(err, sb.String())
}

// WrapUint64 包装错误并附带单个 uint64 值（零分配）
func WrapUint64(err error, prefix string, val uint64) *NexError {
	if err == nil {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(": ")
	sb.WriteString(strconv.FormatUint(val, 10))
	return mergeNexError(err, sb.String())
}

// WrapBytes 包装错误并附带单个 []byte 值（零分配）
func WrapBytes(err error, prefix string, val []byte) *NexError {
	if err == nil {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(": ")
	sb.Write(val)
	return mergeNexError(err, sb.String())
}

// WrapString 包装错误并附带单个 string 值（零分配）
func WrapString(err error, prefix string, val string) *NexError {
	if err == nil {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(": ")
	sb.WriteString(val)
	return mergeNexError(err, sb.String())
}

// WrapUint32Idx 包装错误并附带 pageID 和 idx（BTree 热路径专用）
func WrapUint32Idx(err error, prefix string, pageID uint32, idx int) *NexError {
	if err == nil {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(": pageID=")
	sb.WriteString(strconv.FormatUint(uint64(pageID), 10))
	sb.WriteString(", idx=")
	sb.WriteString(strconv.Itoa(idx))
	return mergeNexError(err, sb.String())
}

// WrapIntRange 包装错误并附带索引范围（BTree 热路径专用）
func WrapIntRange(err error, prefix string, idx, count int) *NexError {
	if err == nil {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(": index ")
	sb.WriteString(strconv.Itoa(idx))
	sb.WriteString(" out of range [0, ")
	sb.WriteString(strconv.Itoa(count))
	sb.WriteString(")")
	return mergeNexError(err, sb.String())
}

// mergeNexError 合并 NexError 详情（内部方法）
func mergeNexError(err error, details string) *NexError {
	// P1-3 修复：防止嵌套 NexError
	if nexErr, ok := err.(*NexError); ok {
		mergedDetails := nexErr.Details
		if mergedDetails != "" && details != "" {
			mergedDetails += "; " + details
		} else if details != "" {
			mergedDetails = details
		}
		return &NexError{
			Err:     nexErr.Err,
			Details: mergedDetails,
		}
	}
	return &NexError{
		Err:     err,
		Details: details,
	}
}

// ===========================
// 便捷格式化函数（返回错误）
// ===========================
// InvalidParamf 创建参数无效错误
func InvalidParamf(format string, args ...any) error {
	return stderrors.New("invalid parameter: " + fmt.Sprintf(format, args...))
}

// InvalidCoreID 创建无效核心ID错误
func InvalidCoreID(coreID, maxCoreID int) error {
	return Wrapf(ErrCPUInvalidCoreID, "core ID %d out of range [0, %d]", coreID, maxCoreID)
}

// SourceID 相关便捷函数
func SourceIDEmpty() error {
	return ErrSourceIDEmpty
}
func SourceIDInvalidFormat() error {
	return ErrSourceIDInvalidFormat
}
func ModuleEmpty() error {
	return ErrSourceIDModuleEmpty
}
func SubModuleEmpty() error {
	return ErrSourceIDSubModuleEmpty
}
func ActionEmpty() error {
	return ErrSourceIDActionEmpty
}

// UnknownTaskMode 创建未知任务模式错误
func UnknownTaskMode(mode string) error {
	return Wrapf(ErrTaskModeUnknown, "mode: %s", mode)
}

// ===========================
// TaskScheduler 便捷错误函数
// ===========================
func TaskAlreadyRegistered(taskName string) error {
	return Wrapf(ErrTaskAlreadyRegistered, "task name: %s", taskName)
}
func TaskNotFound(taskName string) error {
	return Wrapf(ErrTaskNotFound, "task name: %s", taskName)
}
func SchedulerNotStarted() error {
	return ErrSchedulerNotStarted
}
func SchedulerAlreadyRunning() error {
	return ErrSchedulerRunning
}
func ExecutionOrderConflict(order int, existingTask string) error {
	return Wrapf(ErrExecutionOrderConflict, "execution order %d already registered by task: %s", order, existingTask)
}
func CoreRegisterFailed(coreID int, err error) error {
	return Wrapf(err, "register to core %d", coreID)
}
func CoreStartFailed(coreID int, err error) error {
	return Wrapf(err, "start core %d", coreID)
}
func CorePanicDetected(coreID int) error {
	return Wrapf(ErrTaskPanic, "core %d has panic", coreID)
}
func CoreQueueTooLong(coreID int, queueLen int64) error {
	return Wrapf(ErrQueueTooLong, "core %d queue too long: %d", coreID, queueLen)
}

// ===========================
// OffHeap 错误函数
// ===========================

func OffHeapAllocatorSizeMustBePositive(size int) error {
	return Wrapf(ErrOffHeapAllocatorSizeInvalid, "allocator size must be positive: %d", size)
}

func OffHeapMMapFailed(err error) error {
	return Wrapf(err, "mmap failed")
}

func OffHeapAllocExceedsSize(size, total int64) error {
	return Wrapf(ErrOffHeapAllocExceedsSize, "alloc size %d exceeds allocator size %d", size, total)
}

func OffHeapPageFull(used, required, total int) error {
	return Wrapf(ErrOffHeapPageFull, "page full: used=%d, required=%d, total=%d", used, required, total)
}

func OffHeapMMapSizeExceedsLimit(size, limit int64) error {
	return Wrapf(ErrOffHeapMMapExceedsLimit, "mmap size %d exceeds 32-bit PageID limit (%d pages)", size, limit)
}

func OffHeapCreateAllocatorFailed(err error) error {
	return Wrapf(err, "failed to create allocator")
}

func OffHeapAllocMemoryFailed(err error) error {
	return Wrapf(err, "failed to allocate memory")
}

func OffHeapOutOfMemory(total, used int) error {
	return Wrapf(ErrOffHeapOutOfMemory, "out of memory: no free pages available (total: %d, used: %d)", total, used)
}

func OffHeapInvalidPageID(pageID, total int) error {
	return Wrapf(ErrOffHeapInvalidPageID, "invalid pageID %d (total: %d)", pageID, total)
}

func OffHeapConstraintViolation(msg string) error {
	return Wrapf(ErrOffHeapConstraintViolation, "%s", msg)
}

// ===========================
// BTree 错误包装函数
// ===========================

// BTree Storage 错误
func BTreeCreatePageManager(err error) error {
	return Wrapf(err, "create page manager")
}

func BTreeAllocLeafPage(err error) error {
	return Wrapf(err, "alloc leaf page")
}

func BTreeAllocNodePage(err error) error {
	return Wrapf(err, "alloc node page")
}

func BTreeAllocForCOW(err error) error {
	return Wrapf(err, "alloc for copy-on-write")
}

func BTreePageIDExceedsMax(id uint64) error {
	return WrapUint64(ErrBTreeInvalidPage, "pageID exceeds uint32 max", id)
}

func BTreePageNotLeafPage(pageID uint64) error {
	return WrapUint64(ErrBTreeInvalidPage, "page is not a leaf page", pageID)
}

func BTreePageNotNodePage(pageID uint64) error {
	return WrapUint64(ErrBTreeInvalidPage, "page is not a node page", pageID)
}

func BTreePageNotFound(pageID uint64) error {
	return WrapUint64(ErrBTreeInvalidPage, "page not found in pageLocs (not yet loaded from AO)", pageID)
}

// BTree Leaf Page 错误
func BTreeLeafInsertAlloc(err error) error {
	return Wrap(err, "leaf insert alloc")
}

func BTreeLeafInsertEntry(err error) error {
	return Wrap(err, "leaf insert entry")
}

func BTreeLeafUpdateIndexOutOfRange(idx, count int) error {
	return WrapIntRange(ErrBTreeKeyNotFound, "leaf update", idx, count)
}

func BTreeLeafUpdateAlloc(err error) error {
	return Wrap(err, "leaf update alloc")
}

func BTreeLeafUpdateRebuildAlloc(err error) error {
	return Wrap(err, "leaf update rebuild alloc")
}

func BTreeLeafUpdateRebuild(err error) error {
	return Wrap(err, "leaf update rebuild")
}

func BTreeLeafUpdateReinsert(err error) error {
	return Wrap(err, "leaf update reinsert")
}

func BTreeLeafDeleteIndexOutOfRange(idx, count int) error {
	return WrapIntRange(ErrBTreeKeyNotFound, "leaf delete", idx, count)
}

func BTreeLeafDeleteAlloc(err error) error {
	return Wrap(err, "leaf delete alloc")
}

func BTreeLeafDeleteRebuild(err error) error {
	return Wrap(err, "leaf delete rebuild")
}

func BTreeLeafSplitMinKeys(count int) error {
	return WrapInt(ErrBTreeNotImplemented, "leaf split: page has entries", count)
}

func BTreeLeafSplitAllocLeft(err error) error {
	return Wrap(err, "leaf split alloc left")
}

func BTreeLeafSplitAllocRight(err error) error {
	return Wrap(err, "leaf split alloc right")
}

func BTreeLeafSplitLeftBulkInit(err error) error {
	return Wrap(err, "leaf split left bulk init")
}

func BTreeLeafSplitRightBulkInit(err error) error {
	return Wrap(err, "leaf split right bulk init")
}

func BTreeLeafValidateNegativeCount(count int) error {
	return WrapInt(ErrBTreeInvalidPage, "leaf validate: negative count", count)
}

func BTreeLeafValidateKeyOrderingViolation(idx int, prev, curr []byte) error {
	if prev == nil || curr == nil {
		return WrapInt(ErrBTreeInvalidPage, "leaf validate: key ordering violation at idx", idx)
	}
	// 热路径优化：使用 strings.Builder 避免 fmt.Sprintf
	var sb strings.Builder
	sb.WriteString("leaf validate: key ordering violation at idx ")
	sb.WriteString(strconv.Itoa(idx))
	sb.WriteString(": ")
	sb.Write(prev)
	sb.WriteString(" >= ")
	sb.Write(curr)
	return mergeNexError(ErrBTreeInvalidPage, sb.String())
}

// BTree 错误
func BTreeInitRootLeaf(err error) error {
	return Wrap(err, "init root leaf")
}

func BTreeGetOrCreateChildren(err error) error {
	return Wrap(err, "GetOrCreateChildren")
}

// BTree Search 错误
func BTreeSearchPathNilPageInfo(pageID uint64) error {
	return WrapUint64(ErrBTreeInvalidPage, "searchPath: nil PageInfo on page", pageID)
}

func BTreeSearchPathError(err error) error {
	return Wrap(err, "searchPath")
}

func BTreeSearchPathChildNotFound(idx int, pageID uint64) error {
	// 热路径优化
	var sb strings.Builder
	sb.WriteString("searchPath: child[")
	sb.WriteString(strconv.Itoa(idx))
	sb.WriteString("] not found on page ")
	sb.WriteString(strconv.FormatUint(pageID, 10))
	return mergeNexError(ErrBTreeInvalidPage, sb.String())
}

// BTree Operations 错误
func BTreeWriteOpSearch(err error) error {
	return Wrap(err, "write operation search")
}

func BTreeWriteOpGetLeaf(err error) error {
	return Wrap(err, "write operation get leaf")
}

// BTree Node Page 错误
func BTreeNodeReplaceChildIndexOutOfRange(idx, count int) error {
	return WrapIntRange(ErrBTreeInvalidPage, "node replace child", idx, count)
}

func BTreeNodeReplaceChildAlloc(err error) error {
	return Wrap(err, "node replace child alloc")
}

func BTreeNodeInsertChildIndexOutOfRange(idx, count int) error {
	return WrapIntRange(ErrBTreeInvalidPage, "node insert child", idx, count)
}

func BTreeNodeInsertChildAlloc(err error) error {
	return Wrap(err, "node insert child alloc")
}

func BTreeNodeInsertChildEntry(err error) error {
	return Wrap(err, "node insert child entry")
}

func BTreeNodeInsertChildAtEnd(err error) error {
	return Wrap(err, "node insert child at end")
}

func BTreeNodeRemoveChildNotImplemented() error {
	return Wrap(ErrBTreeNotImplemented, "NodePage.RemoveChild not implemented")
}

func BTreeNodeSplitMinKeys(count int) error {
	return WrapInt(ErrBTreeNotImplemented, "node split: page has entries", count)
}

func BTreeNodeSplitAllocLeft(err error) error {
	return Wrap(err, "node split alloc left")
}

func BTreeNodeSplitAllocRight(err error) error {
	return Wrap(err, "node split alloc right")
}

func BTreeNodeSplitLeftBulkInit(err error) error {
	return Wrap(err, "node split left bulk init")
}

func BTreeNodeSplitRightBulkInit(err error) error {
	return Wrap(err, "node split right bulk init")
}

func BTreeNodeValidateNegativeCount(count int) error {
	return WrapInt(ErrBTreeInvalidPage, "node validate: negative count", count)
}

func BTreeNodeValidateKeyOrderingViolation(idx int, prev, curr []byte) error {
	if prev == nil || curr == nil {
		return WrapInt(ErrBTreeInvalidPage, "node validate: key ordering violation at idx", idx)
	}
	// 热路径优化：使用 strings.Builder 避免 fmt.Sprintf
	var sb strings.Builder
	sb.WriteString("node validate: key ordering violation at idx ")
	sb.WriteString(strconv.Itoa(idx))
	sb.WriteString(": ")
	sb.Write(prev)
	sb.WriteString(" >= ")
	sb.Write(curr)
	return mergeNexError(ErrBTreeInvalidPage, sb.String())
}

func BTreeNodeValidateChildCountMismatch(childCount, keyCount int) error {
	return WrapInt2(ErrBTreeInvalidPage, "node validate: child count != key count + 1", childCount, keyCount)
}

// ===========================
// OffHeap Page Layout 错误包装函数
// ===========================

func OffHeapPageHasChildAtZero(pageID uint32, idx int) error {
	return WrapUint32Idx(ErrOffHeapConstraintViolation, "page has child=0 at index", pageID, idx)
}

func OffHeapPageHasExtraChildAtZero(pageID uint32, count uint16) error {
	return WrapUint32(ErrOffHeapConstraintViolation, "page has extraChild=0 with count", pageID)
}

func OffHeapPageKeysNotSortedViolation(pageID uint32, idx int) error {
	return WrapUint32Idx(ErrOffHeapConstraintViolation, "page invariant violated: keys not sorted at index", pageID, idx)
}

func OffHeapPageChildAtZeroViolation(pageID uint32, idx int) error {
	return WrapUint32Idx(ErrOffHeapConstraintViolation, "page invariant violated: child=0 at index", pageID, idx)
}

func OffHeapPageExtraChildAtZeroViolation(pageID uint32, count uint16) error {
	return WrapUint32(ErrOffHeapConstraintViolation, "page invariant violated: extraChild=0 with count", pageID)
}

func OffHeapIndexOutOfRange(index int, count uint16) error {
	// 热路径优化：使用 strings.Builder 避免 fmt.Sprintf
	var sb strings.Builder
	sb.WriteString("index ")
	sb.WriteString(strconv.Itoa(index))
	sb.WriteString(" out of range (count: ")
	sb.WriteString(strconv.FormatUint(uint64(count), 10))
	sb.WriteString(")")
	return mergeNexError(ErrOffHeapInvalidPageID, sb.String())
}

func OffHeapInvalidRange(startIdx, endIdx, totalCount int) error {
	// 热路径优化：使用 strings.Builder 避免 fmt.Sprintf
	var sb strings.Builder
	sb.WriteString("invalid range [")
	sb.WriteString(strconv.Itoa(startIdx))
	sb.WriteString(", ")
	sb.WriteString(strconv.Itoa(endIdx))
	sb.WriteString(") (count: ")
	sb.WriteString(strconv.Itoa(totalCount))
	sb.WriteString(")")
	return mergeNexError(ErrOffHeapInvalidPageID, sb.String())
}

func OffHeapSourcePageRecycled(err error) error {
	return Wrap(err, "source page recycled during bulk init")
}

func OffHeapSelfLoopDetected(srcPageID uint32, idx int, child uint32) error {
	// 热路径优化：使用 strings.Builder 避免 fmt.Sprintf
	var sb strings.Builder
	sb.WriteString("self-loop (or zero) detected in source page ")
	sb.WriteString(strconv.FormatUint(uint64(srcPageID), 10))
	sb.WriteString(" at index ")
	sb.WriteString(strconv.Itoa(idx))
	sb.WriteString(", child=")
	sb.WriteString(strconv.FormatUint(uint64(child), 10))
	return mergeNexError(ErrOffHeapConstraintViolation, sb.String())
}

func OffHeapSelfLoopInExtraChild(srcPageID uint32) error {
	return WrapUint32(ErrOffHeapConstraintViolation, "self-loop detected in source page extraChild", srcPageID)
}
