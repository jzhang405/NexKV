// Package errors 提供统一的错误定义
// 采用 sentinel error + NexError 包装模式
package errors

import (
	stderrors "errors"
	"fmt"
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

	// 可暂停调度器错误
	ErrStepNotFound         = stderrors.New("step: operation not found")
	ErrStepNotPausable      = stderrors.New("step: step is not pausable")
	ErrStepMaxPausedReached = stderrors.New("step: max paused operations limit reached")
	ErrCheckpointNotFound   = stderrors.New("step: checkpoint not found")
	ErrMigrationNotFound    = stderrors.New("step: migration not found")

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

	// BTree 内联错误
	ErrBTreeClosed                       = stderrors.New("btree: is closed")
	ErrBTreeRetry                        = stderrors.New("btree: cas failed, retry operation")
	ErrBTreeInvalidPath                  = stderrors.New("btree: invalid path, node structure inconsistent")
	ErrBTreeKeyNotFound                  = stderrors.New("btree: key not found")
	ErrBTreePageStale                    = stderrors.New("btree: page state stale, retry")
	ErrBTreeCircularReference            = stderrors.New("btree: circular reference detected, retry with backoff")
	ErrBTreeNotImplemented               = stderrors.New("btree: not implemented")
	ErrBTreeInvalidParam                 = stderrors.New("btree: invalid parameter")
	ErrBTreePageNotLoaded                = stderrors.New("btree: page not loaded")
	ErrBTreeLeafPageNotLoaded            = stderrors.New("btree: leaf page not loaded")
	ErrBTreeRootPageInfoNil              = stderrors.New("btree: root page info is nil")
	ErrBTreeEmptyPath                    = stderrors.New("btree: empty path")
	ErrBTreePageLockNil                  = stderrors.New("btree: page lock is nil")
	ErrBTreeLeafPageInfoNil              = stderrors.New("btree: leaf page info is nil")
	ErrBTreeRootInfoNil                  = stderrors.New("btree: root info is nil")
	ErrBTreeParentInfoNil                = stderrors.New("btree: parent info is nil")
	ErrBTreeRootPageRefNil               = stderrors.New("btree: root PageRef is nil")
	ErrBTreeParentLockNil                = stderrors.New("btree: parent lock is nil")
	ErrBTreeChildNotFound                = stderrors.New("btree: child not found in parent")
	ErrBTreeMaxRetriesExceeded           = stderrors.New("btree: max retries exceeded")
	ErrBTreeChunkManagerNotInit          = stderrors.New("btree: chunk manager not initialized")
	ErrBTreePageInfoNil                  = stderrors.New("btree: pageInfo is nil")
	ErrBTreeUnknownPageType              = stderrors.New("btree: unknown page type")
	ErrBTreeCopiedPathTooShort           = stderrors.New("btree: copiedPath too short")
	ErrBTreeParentPageNotLoaded          = stderrors.New("btree: parent page not loaded in copiedPath")
	ErrBTreeLeftSiblingInsufficientKeys  = stderrors.New("btree: left sibling has insufficient keys")
	ErrBTreeLeftSiblingChildrenMismatch  = stderrors.New("btree: left sibling children count mismatch")
	ErrBTreeLeftSiblingIndexOutOfRange   = stderrors.New("btree: left sibling children index out of range")
	ErrBTreeRightSiblingInsufficientKeys = stderrors.New("btree: right sibling has insufficient keys")
	ErrBTreeRightSiblingChildrenMismatch = stderrors.New("btree: right sibling children count mismatch")
	ErrBTreeRightSiblingNoChildren       = stderrors.New("btree: right sibling has no children")
	ErrBTreeRootNotInit                  = stderrors.New("btree: root not initialized")
	ErrBTreePageTypeMismatch             = stderrors.New("btree: page type mismatch")
	ErrBTreeChildPageInfoNil             = stderrors.New("btree: child page info is nil")
	ErrBTreePathExceedsMaxLevels         = stderrors.New("btree: search path exceeds max levels")
	ErrBTreeEmptyRefs                    = stderrors.New("btree: empty refs")
	ErrBTreeOldChildStillExists          = stderrors.New("btree: old child still exists after split")
	ErrBTreeNewChildrenNotFound          = stderrors.New("btree: new children not found in parent")
	ErrBTreeChildCountMismatch           = stderrors.New("btree: child count mismatch")
	ErrBTreeLeftRefPageInfoNil           = stderrors.New("btree: leftRef page info is nil after split")
	ErrBTreeBatchFailed                  = stderrors.New("btree: batch operation failed")
	ErrBTreeCASRetryExhausted            = stderrors.New("btree: cas retry exhausted")
	ErrBTreeInvalidInsertIndex           = stderrors.New("btree: invalid insert index")
	ErrBTreeSplitFailed                  = stderrors.New("btree: split failed")
	ErrBTreeAllocPageFailed              = stderrors.New("btree: allocate page failed")
	ErrBTreeMaterializePageFailed        = stderrors.New("btree: materialize page failed")
	ErrBTreeCASFailed                    = stderrors.New("btree: cas failed")
	ErrBTreeFallbackInsertFailed         = stderrors.New("btree: fallback insert failed")
	ErrBTreeGrandparentInfoNil           = stderrors.New("btree: grandparent info is nil")
	ErrBTreeGrandparentLockNil           = stderrors.New("btree: grandparent lock is nil")
	ErrBTreeAllocGrandparentFailed       = stderrors.New("btree: allocate grandparent page failed")
	ErrBTreeMaterializeGrandparentFailed = stderrors.New("btree: materialize grandparent page failed")
	ErrBTreeUpdateGrandparentFailed      = stderrors.New("btree: update grandparent failed")
	ErrBTreePostSplitInsertFailed        = stderrors.New("btree: post-split insert failed")
	ErrBTreeParentInfoNilAfterCAS        = stderrors.New("btree: parent info nil after CAS")
	ErrBTreeParentLockNilAfterRetry      = stderrors.New("btree: parent lock nil during CAS retry")
	ErrBTreeParentPageIDChanged          = stderrors.New("btree: parent pageID changed during CAS retry")
	ErrBTreeAllocLeftIndexFailed         = stderrors.New("btree: allocate left index page failed")
	ErrBTreeAllocRightIndexFailed        = stderrors.New("btree: allocate right index page failed")
	ErrBTreeMaterializeLeftIndexFailed   = stderrors.New("btree: materialize left index page failed")
	ErrBTreeMaterializeRightIndexFailed  = stderrors.New("btree: materialize right index page failed")
	ErrBTreeMaterializationBug           = stderrors.New("btree: materialization bug detected")
	ErrBTreePersistInternalPageFailed    = stderrors.New("btree: persist internal page failed")
	ErrBTreePersistLeafPageFailed        = stderrors.New("btree: persist leaf page failed")
	ErrBTreeRootPersistFailed            = stderrors.New("btree: persist root failed")
	ErrBTreeAllocRootPageFailed          = stderrors.New("btree: allocate root page failed")
	ErrBTreeMaterializeRootPageFailed    = stderrors.New("btree: materialize root page failed")
	ErrBTreeChildrenLoss                 = stderrors.New("btree: children loss detected")
	ErrBTreeAllocFailed                  = stderrors.New("btree: allocate page failed")
	ErrBTreeMaterializeFailed            = stderrors.New("btree: materialize page failed")
	ErrBTreeUpdateFailed                 = stderrors.New("btree: update failed")

	// BTree CCOW/Snapshot 错误
	ErrBTreeSnapshotNotFound    = stderrors.New("btree: snapshot not found")
	ErrBTreeSnapshotRootRefNil  = stderrors.New("btree: snapshot root ref is nil")
	ErrBTreeSnapshotRootInfoNil = stderrors.New("btree: snapshot root info is nil")

	// BTree PageLock 错误
	ErrBTreeNotOwner         = stderrors.New("btree: not the lock owner")
	ErrBTreeLockInvalidState = stderrors.New("btree: invalid lock state")

	// BTree PagePersist 错误
	ErrBTreePagePersistNotFound  = stderrors.New("btree: page not found")
	ErrBTreePagePersistCorrupted = stderrors.New("btree: page corrupted")
	ErrBTreePageStoreClosed      = stderrors.New("btree: store closed")

	// BTree Position 错误
	ErrBTreeInvalidPosition = stderrors.New("btree: invalid page position")
	ErrBTreeInvalidChunkID  = stderrors.New("btree: invalid chunk id")
	ErrBTreeInvalidOffset   = stderrors.New("btree: invalid offset")
	ErrBTreeInvalidPageType = stderrors.New("btree: invalid page type")

	// BTree PageSerializer 错误
	ErrBTreeInvalidDataSize = stderrors.New("btree: invalid data size")
	ErrBTreeUnexpectedEOF   = stderrors.New("btree: unexpected EOF")

	// BTree ChunkManager 错误
	ErrBTreeChunkNotFound     = stderrors.New("btree: chunk not found")
	ErrBTreeDeserializeFailed = stderrors.New("btree: deserialize page failed")
	ErrBTreeDataDirError      = stderrors.New("btree: data directory error")

	// BTree 页面操作错误（internal_page / leaf_page）
	ErrBTreeChildIndexOutOfRange   = stderrors.New("btree: child index out of range")
	ErrBTreeInvariantViolated      = stderrors.New("btree: page invariant violated")
	ErrBTreeConcurrentModification = stderrors.New("btree: page modified concurrently during update")
	ErrBTreeKeyNotFoundInPage      = stderrors.New("btree: key not found in page")
	ErrBTreeKeyOrderViolation      = stderrors.New("btree: key ordering violation")
	ErrBTreeCannotSplitMinKeys     = stderrors.New("btree: cannot split page with minimum keys")
	ErrBTreeDeserializeReadFailed  = stderrors.New("btree: failed to read during deserialize")
	ErrBTreeIndexOutOfRange        = stderrors.New("btree: index out of range")

	// BTree PageRef 错误
	ErrBTreePageNotLoadedInvalidNodeRef = stderrors.New("btree: page not loaded (invalid NodeRef)")

	// BTree Ops 错误
	ErrBTreeCloneLeafPageFailed  = stderrors.New("btree: clone leaf page failed")
	ErrBTreeInsertIntoLeafFailed = stderrors.New("btree: insert into leaf failed")
	ErrBTreeTaskExecutionFailed  = stderrors.New("btree: task execution failed")

	// BTree GC 错误
	ErrBTreeOutOfMemory = stderrors.New("btree: out of memory")

	// BTree OffHeap Adapter 错误
	ErrBTreePageFull             = stderrors.New("btree: page is full")
	ErrBTreeStaleChildReference  = stderrors.New("btree: stale child reference")
	ErrBTreePageTooLarge         = stderrors.New("btree: page too large to split")
	ErrBTreeInvalidSplitIndex    = stderrors.New("btree: invalid split index")
	ErrBTreeDuplicatePageIDAlloc = stderrors.New("btree: allocator returned same pageID")
	ErrBTreeInvalidPageIDAlloc   = stderrors.New("btree: allocator returned invalid pageID")

	// BTree ReplaceChild TOCTOU 防御错误（热路径，直接返回 sentinel error）
	ErrBTreeParentPageRecycled = stderrors.New("btree: parent page recycled")
	ErrBTreeInvalidParentState = stderrors.New("btree: parent page invalid state")

	// BTree ParentSplit 错误
	ErrBTreeAsyncParentSplitFailed = stderrors.New("btree: async parent split failed")

	// BTree SetItem 错误
	ErrBTreeSetWithLeafRefFailed = stderrors.New("btree: set with leaf ref failed")

	// BTree CAS 相关
	ErrBTreeCASUpdateRootFailed = stderrors.New("btree: CAS update root failed")

	// BTree PagePersist 操作错误
	ErrBTreeOpenFile       = stderrors.New("btree: open file failed")
	ErrBTreeStatFile       = stderrors.New("btree: stat file failed")
	ErrBTreeSeekFile       = stderrors.New("btree: seek file failed")
	ErrBTreeWriteFile      = stderrors.New("btree: write file failed")
	ErrBTreeSyncFile       = stderrors.New("btree: sync file failed")
	ErrBTreeReadFile       = stderrors.New("btree: read file failed")
	ErrBTreeIncompleteRead = stderrors.New("btree: incomplete read")
	ErrBTreeCloseFile      = stderrors.New("btree: close file failed")
	ErrBTreeWritePageNil   = stderrors.New("btree: WritePage page is nil")

	// BTree Chunk 操作错误
	ErrBTreeChunkReadOnly           = stderrors.New("btree: chunk is read-only")
	ErrBTreeChunkFull               = stderrors.New("btree: chunk is full")
	ErrBTreeChunkPageSizeMismatch   = stderrors.New("btree: chunk page size mismatch")
	ErrBTreeChunkPositionOutOfRange = stderrors.New("btree: chunk position out of range")

	// BTree PageLock 操作错误
	ErrBTreeCannotUnlockUnlocked = stderrors.New("btree: cannot unlock unlocked lock")
	ErrBTreeUnlockStateChanged   = stderrors.New("btree: unlock failed: state changed")

	// OffHeap 错误
	ErrOffHeapAllocatorSizeInvalid = stderrors.New("offheap: allocator size must be positive")
	ErrOffHeapAllocExceedsSize     = stderrors.New("offheap: alloc size exceeds allocator size")
	ErrOffHeapPageFull             = stderrors.New("offheap: page full")
	ErrOffHeapMMapExceedsLimit     = stderrors.New("offheap: mmap size exceeds 32-bit PageID limit")
	ErrOffHeapOutOfMemory          = stderrors.New("offheap: out of memory")
	ErrOffHeapInvalidPageID        = stderrors.New("offheap: invalid page ID")
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
func Wrapf(err error, format string, args ...any) *NexError {
	// P0-2 修复：nil 错误返回 nil，避免 panic
	if err == nil {
		return nil
	}
	return mergeNexError(err, fmt.Sprintf(format, args...))
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

// SourceIDEmpty 创建 SourceID 为空错误
func SourceIDEmpty() error {
	return ErrSourceIDEmpty
}

// SourceIDInvalidFormat 创建 SourceID 格式错误
func SourceIDInvalidFormat() error {
	return ErrSourceIDInvalidFormat
}

// ModuleEmpty 创建模块为空错误
func ModuleEmpty() error {
	return ErrSourceIDModuleEmpty
}

// SubModuleEmpty 创建子模块为空错误
func SubModuleEmpty() error {
	return ErrSourceIDSubModuleEmpty
}

// ActionEmpty 创建动作字段为空错误
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

// TaskAlreadyRegistered 任务已注册错误
func TaskAlreadyRegistered(taskName string) error {
	return Wrapf(ErrTaskAlreadyRegistered, "task name: %s", taskName)
}

// TaskNotFound 任务未找到错误
func TaskNotFound(taskName string) error {
	return Wrapf(ErrTaskNotFound, "task name: %s", taskName)
}

// SchedulerNotStarted 调度器未启动错误
func SchedulerNotStarted() error {
	return ErrSchedulerNotStarted
}

// SchedulerAlreadyRunning 调度器已在运行错误
func SchedulerAlreadyRunning() error {
	return ErrSchedulerRunning
}

// ExecutionOrderConflict 执行顺序冲突错误
func ExecutionOrderConflict(order int, existingTask string) error {
	return Wrapf(ErrExecutionOrderConflict, "execution order %d already registered by task: %s", order, existingTask)
}

// CoreRegisterFailed 核心注册任务失败错误
func CoreRegisterFailed(coreID int, err error) error {
	return Wrapf(err, "register to core %d", coreID)
}

// CoreStartFailed 核心启动失败错误
func CoreStartFailed(coreID int, err error) error {
	return Wrapf(err, "start core %d", coreID)
}

// CorePanicDetected 核心检测到 panic 错误
func CorePanicDetected(coreID int) error {
	return Wrapf(ErrTaskPanic, "core %d has panic", coreID)
}

// CoreQueueTooLong 核心队列过长错误
func CoreQueueTooLong(coreID int, queueLen int64) error {
	return Wrapf(ErrQueueTooLong, "core %d queue too long: %d", coreID, queueLen)
}

// ===========================
// BTree 层错误函数
// ===========================

// BTree 通用错误
func BTreeShardItemInterface() error {
	return Wrapf(ErrBTreeInvalidParam, "item does not implement ShardItem interface")
}

// Open/Close 相关错误
func BTreeCreateDirectory(dir string, err error) error {
	return Wrapf(err, "create directory: %s", dir)
}

func BTreeOpenChunkManager(name string, err error) error {
	return Wrapf(err, "open chunk manager: %s", name)
}

func BTreeOpenWAL(name string, err error) error {
	return Wrapf(err, "open WAL: %s", name)
}

func BTreeCreateOffheapManager(err error) error {
	return Wrapf(err, "create offheap page manager")
}

func BTreeAllocRootPage(err error) error {
	return Wrapf(err, "alloc initial root leaf page")
}

func BTreeReplayWAL(err error) error {
	return Wrapf(err, "replay WAL")
}

func BTreeRegisterTask(name string, err error) error {
	return Wrapf(err, "register btree-%s task", name)
}

func BTreeCreateExecutor(err error) error {
	return Wrapf(err, "create per-core executor")
}

func BTreeStartScheduler(err error) error {
	return Wrapf(err, "start scheduler")
}

// Get/Set 操作错误
func BTreeLeafPageNotLoaded() error {
	return Wrapf(ErrBTreePageNotLoaded, "leaf page not loaded")
}

func BTreeOffheapGet(err error) error {
	return Wrapf(err, "offheap get")
}

func BTreeOffheapSet(err error) error {
	return Wrapf(err, "offheap set")
}

func BTreeOffheapInsert(err error) error {
	return err
}

func BTreeOffheapDelete(err error) error {
	return Wrapf(err, "offheap delete")
}

func BTreeCircularReferenceRetry(maxRetries int, err error) error {
	return Wrapf(err, "circular reference retry exhausted after %d attempts", maxRetries)
}

func BTreeMaxRetriesExceeded(maxRetries int) error {
	return Wrapf(ErrBTreeMaxRetriesExceeded, "max retries (%d) exceeded", maxRetries)
}

// 路径/页面相关错误
func BTreeEmptyPath() error {
	return Wrapf(ErrBTreeEmptyPath, "empty path")
}

func BTreePageLockNil() error {
	return Wrapf(ErrBTreePageLockNil, "page lock is nil")
}

func BTreeLeafPageInfoNil() error {
	return Wrapf(ErrBTreeLeafPageInfoNil, "leaf page info is nil")
}

func BTreeLeafPageNotLoaded2() error {
	return Wrapf(ErrBTreeLeafPageNotLoaded, "leaf page not loaded")
}

func BTreeRootInfoNil(op string) error {
	return Wrapf(ErrBTreeRootInfoNil, "root info is nil during %s", op)
}

func BTreeParentInfoNil(op string) error {
	return Wrapf(ErrBTreeParentInfoNil, "parent info is nil during %s", op)
}

func BTreeRootPageRefNil(op string) error {
	return Wrapf(ErrBTreeRootPageRefNil, "root PageRef is nil during %s", op)
}

func BTreeParentLockNil() error {
	return Wrapf(ErrBTreeParentLockNil, "parent lock is nil")
}

func BTreeParentInfoNilOp(op string) error {
	return ErrBTreeParentInfoNil
}

func BTreeParentLockNilOp(op string) error {
	return ErrBTreeParentLockNil
}

func BTreeChildNotFound(parentID, childID uint64) error {
	return Wrapf(ErrBTreeChildNotFound, "child %d not found in parent page %d", childID, parentID)
}

func BTreeUpdateParentChildIndex(err error) error {
	return Wrapf(err, "update parent child index")
}

func BTreePersistRoot(err error) error {
	return Wrapf(err, "persist root")
}

func BTreeFindLeafPage(err error) error {
	return Wrapf(err, "find leaf page")
}

func BTreeCopyPath(err error) error {
	return Wrapf(err, "copy path")
}

func BTreeDeleteFromLeaf(err error) error {
	return Wrapf(err, "delete from leaf")
}

func BTreeMergeLeaf(err error) error {
	return Wrapf(err, "merge leaf")
}

// Transaction/Snapshot 错误
func BTreeBeginTxNotImplemented() error {
	return Wrapf(ErrBTreeNotImplemented, "BeginTx: not implemented")
}

func BTreeCreateSnapshotNotImplemented() error {
	return Wrapf(ErrBTreeNotImplemented, "CreateSnapshot: not implemented")
}

func BTreeReleaseSnapshotNotImplemented() error {
	return Wrapf(ErrBTreeNotImplemented, "ReleaseSnapshot: not implemented")
}

// Close 相关错误
func BTreeTruncateWAL(err error) error {
	return Wrapf(err, "truncate WAL")
}

func BTreeCloseChunkManager(err error) error {
	return Wrapf(err, "close chunk manager")
}

func BTreeCloseWAL(err error) error {
	return Wrapf(err, "close WAL")
}

// Load/Dump 相关错误
func BTreeChunkManagerNotInit() error {
	return Wrapf(ErrBTreeChunkManagerNotInit, "chunk manager not initialized (in-memory mode)")
}

func BTreeLoadPageAt(pos int64, err error) error {
	return Wrapf(err, "load page at %d", pos)
}

func BTreePageInfoNil() error {
	return Wrapf(ErrBTreePageInfoNil, "pageInfo is nil")
}

func BTreePageNotLoadedNoPos() error {
	return Wrapf(ErrBTreePageNotLoaded, "page not loaded and no position (pos=0)")
}

func BTreePageNotLoaded() error {
	return ErrBTreePageNotLoaded
}

func BTreeLoadPage(err error) error {
	return Wrapf(err, "load page")
}

// Persist 相关错误
func BTreeSerializeLeafPage(err error) error {
	return Wrapf(err, "serialize leaf page")
}

func BTreeSerializeInternalPage(err error) error {
	return Wrapf(err, "serialize internal page")
}

func BTreeUnknownPageType(pageType string) error {
	return Wrapf(ErrBTreeUnknownPageType, "unknown page type: %s", pageType)
}

func BTreeAllocatePage(err error) error {
	return Wrapf(err, "allocate page")
}

func BTreeWritePageToChunk(err error) error {
	return Wrapf(err, "write page to chunk")
}

func BTreePersistChildPage(childID uint64, err error) error {
	return Wrapf(err, "persist child page %d", childID)
}

func BTreePersistInternalPage(err error) error {
	return Wrapf(err, "persist internal page")
}

func BTreePersistLeafPage(err error) error {
	return Wrapf(err, "persist leaf page")
}

func BTreeRootPageInfoNil() error {
	return Wrapf(ErrBTreeRootPageInfoNil, "root page info is nil")
}

func BTreeChildNotFoundInParent(parentID uint64) error {
	return Wrapf(ErrBTreeChildNotFound, "child not found in parent %d", parentID)
}

func BTreeLoadPageFromChunk(err error) error {
	return Wrapf(err, "load page from chunk")
}

// CopyPath 相关错误
func BTreeCopiedPathTooShort(expected, got int) error {
	return Wrapf(ErrBTreeCopiedPathTooShort, "copiedPath too short: expected at least %d, got %d", expected, got)
}

func BTreeParentPageNotLoaded() error {
	return Wrapf(ErrBTreeParentPageNotLoaded, "parent page not loaded in copiedPath")
}

func BTreeFindChildIndex(err error) error {
	return Wrapf(err, "find child index")
}

// Rebalance 相关错误
func BTreeLeftSiblingInsufficientKeys(numKeys int) error {
	return Wrapf(ErrBTreeLeftSiblingInsufficientKeys, "left sibling has insufficient keys to borrow: %d", numKeys)
}

func BTreeLeftSiblingChildrenMismatch(keys, children int) error {
	return Wrapf(ErrBTreeLeftSiblingChildrenMismatch, "left sibling children count mismatch: keys=%d, children=%d", keys, children)
}

func BTreeLeftSiblingIndexOutOfRange(lastIdx, childrenLen int) error {
	return Wrapf(ErrBTreeLeftSiblingIndexOutOfRange, "left sibling children index out of range: lastIdx=%d, children_len=%d", lastIdx, childrenLen)
}

func BTreeRightSiblingInsufficientKeys(numKeys int) error {
	return Wrapf(ErrBTreeRightSiblingInsufficientKeys, "right sibling has insufficient keys to borrow: %d", numKeys)
}

func BTreeRightSiblingChildrenMismatch(keys, children int) error {
	return Wrapf(ErrBTreeRightSiblingChildrenMismatch, "right sibling children count mismatch: keys=%d, children=%d", keys, children)
}

func BTreeRightSiblingNoChildren() error {
	return Wrapf(ErrBTreeRightSiblingNoChildren, "right sibling has no children")
}

// SearchPath 错误
func BTreePathRootNotInit() error {
	return Wrapf(ErrBTreeRootNotInit, "root not initialized")
}

func BTreePathRootPageInfoNil() error {
	return Wrapf(ErrBTreeRootPageInfoNil, "root page info is nil")
}

func BTreePathLoadPageAtDepth(depth int, err error) error {
	return Wrapf(err, "load page at depth %d", depth)
}

func BTreePathExpectedInternalAtDepth(depth int, pageType string) error {
	return Wrapf(ErrBTreePageTypeMismatch, "expected internal page at depth %d, got %s", depth, pageType)
}

func BTreePathChildPageInfoNil(depth int) error {
	return Wrapf(ErrBTreeChildPageInfoNil, "child page info is nil at depth %d", depth)
}

func BTreePathExceedsMaxLevels(maxLevels int) error {
	return Wrapf(ErrBTreePathExceedsMaxLevels, "search path exceeds max levels (%d)", maxLevels)
}

func BTreePathEmptyRefs() error {
	return Wrapf(ErrBTreeEmptyRefs, "empty refs")
}

func BTreePathOldChildStillExists(oldChild, parent uint64) error {
	return Wrapf(ErrBTreeOldChildStillExists, "old child %d still exists after split at parent %d", oldChild, parent)
}

func BTreePathNewChildrenNotFound(parent uint64, left, right uint64) error {
	return Wrapf(ErrBTreeNewChildrenNotFound, "new children not found in parent %d: left=%d, right=%d", parent, left, right)
}

func BTreePathChildCountMismatch(expected, got int) error {
	return Wrapf(ErrBTreeChildCountMismatch, "child count mismatch: expected %d, got %d", expected, got)
}

// LeafLockSet 错误
func BTreeReplaceChildInParent(err error) error {
	return err
}

func BTreeLeftRefPageInfoNil() error {
	return Wrapf(ErrBTreeLeftRefPageInfoNil, "leftRef page info is nil after split")
}

func BTreeRootPageInfoNilAfterPersist() error {
	return Wrapf(ErrBTreeRootPageInfoNil, "root page info is nil after persist")
}

func BTreeFinalizeDeepClone(err error) error {
	return Wrapf(err, "finalize deep clone")
}

func BTreeAllocFallbackPage(err error) error {
	return err
}

func BTreeFallbackInsert(err error) error {
	return err
}

func BTreeParentInfoNilInFallback(op string) error {
	return ErrBTreeParentInfoNil
}

func BTreeParentLockNilInFallback(op string) error {
	return ErrBTreeParentLockNil
}

func BTreeUpdateParentInFallback(err error) error {
	return err
}

func BTreeRootSplitPostSplitInsertFailed(err error) error {
	return err
}

func BTreeInvalidInsertIndex(insertIndex, count int) error {
	return ErrBTreeInvalidInsertIndex
}

func BTreeUpdateParentIndex(err error) error {
	return err
}

func BTreeParentInfoNilDuringCASRetry() error {
	return ErrBTreeParentInfoNilAfterCAS
}

func BTreeParentPageIDChangedDuringRetry(was, now uint64) error {
	return ErrBTreeParentPageIDChanged
}

func BTreeCASRetryExhausted(casAttempt int) error {
	return ErrBTreeCASRetryExhausted
}

func BTreeCircularReferenceAfterParentUpdate(pageID uint64) error {
	return ErrBTreeCircularReference
}

func BTreeParentSplitIntegrityCheck(err error) error {
	return err
}

func BTreeParentInfoNilAfterCASOp() error {
	return ErrBTreeParentInfoNilAfterCAS
}

func BTreeGrandparentInfoNilOp(op string) error {
	return ErrBTreeGrandparentInfoNil
}

func BTreeOldParentNotFoundInGrandparent(oldParent, grandParent uint64) error {
	return ErrBTreeOldChildStillExists
}

func BTreeUpdateGrandparent(err error) error {
	return err
}

func BTreeGrandparentLockNilOp(op string) error {
	return ErrBTreeGrandparentLockNil
}

func BTreeAllocGrandparentPage(err error) error {
	return err
}

func BTreeMaterializeGrandparentPage(err error) error {
	return err
}

func BTreeGrandparentLockNilAfterAlloc(op string) error {
	return ErrBTreeGrandparentLockNil
}

func BTreePostSplitInsert(err error) error {
	return err
}

func BTreeAllocIndexPage(err error) error {
	return err
}

func BTreeMaterializeRootIndexPage(err error) error {
	return err
}

func BTreeCASFailed(oldRootID, newRootPageID uint64, retry int) error {
	return Wrapf(ErrBTreeCASFailed, "CAS failed: oldRootID=%d, newRootPageID=%d, retry=%d", oldRootID, newRootPageID, retry)
}

func BTreeAllocLeftIndexPage(err error) error {
	return err
}

func BTreeAllocRightIndexPage(err error) error {
	return err
}

func BTreeMaterializeLeftIndexPage(err error) error {
	return err
}

func BTreeMaterializationBugLeft(pageID uint64, expected, got int) error {
	return Wrapf(ErrBTreeMaterializationBug, "MATERIALIZATION BUG: leftPageID=%d expected %d children but got %d", pageID, expected, got)
}

func BTreeMaterializeRightIndexPage(err error) error {
	return err
}

func BTreeMaterializationBugRight(pageID uint64, expected, got int) error {
	return Wrapf(ErrBTreeMaterializationBug, "MATERIALIZATION BUG: rightPageID=%d expected %d children but got %d", pageID, expected, got)
}

func BTreeBatchItemFailed(batchID, i int, err error) error {
	return Wrapf(err, "batch %d item %d", batchID, i)
}

func BTreePersistInternalPageErr(err error) error {
	return Wrapf(err, "persist internal page")
}

func BTreePersistLeafPageErr(err error) error {
	return Wrapf(err, "persist leaf page")
}

func BTreePersistRootErr(err error) error {
	return Wrapf(err, "persist root")
}

func BTreeAllocRootPageErr(err error) error {
	return Wrapf(err, "alloc root page")
}

func BTreeMaterializeRootPageErr(err error) error {
	return Wrapf(err, "materialize root page")
}

func BTreeParentPageIDChangedDuringRetryOp() error {
	return stderrors.New("btree: parent pageID changed during CAS retry")
}

func BTreeChildrenLossDetectedLeft(pageID uint64, expected, actual int) error {
	return Wrapf(ErrBTreeChildrenLoss, "CHILDREN LOSS DETECTED! leftPageID=%d: expected=%d children, actual=%d children", pageID, expected, actual)
}

func BTreeChildrenLossDetectedRight(pageID uint64, expected, actual int) error {
	return Wrapf(ErrBTreeChildrenLoss, "CHILDREN LOSS DETECTED! rightPageID=%d: expected=%d children, actual=%d children", pageID, expected, actual)
}

func BTreeCASFailedOp(oldRoot, newRoot uint64, retry int) error {
	return Wrapf(ErrBTreeCASFailed, "CAS failed: oldRoot=%d, newRoot=%d, retry=%d", oldRoot, newRoot, retry)
}

func BTreeCASSuccessButRootNil() error {
	return stderrors.New("btree: CAS successful but root is nil")
}

func BTreeAllocIndexPageForRoot(err error) error {
	return err
}

func BTreeMaterializeRootPage(err error) error {
	return err
}

func BTreeAllocNewInternalPage(err error) error {
	return err
}

func BTreeMaterializeNewInternalPage(err error) error {
	return err
}

func BTreeAllocNewRootPage(err error) error {
	return err
}

func BTreeMaterializeNewRootPage(err error) error {
	return err
}

func BTreeUpdateParentIndexEntry(err error) error {
	return err
}

// ===========================
// InternalPage / LeafPage 错误函数
// ===========================

// BTreeChildIndexOutOfRangeError 创建子节点索引越界错误
func BTreeChildIndexOutOfRangeError(idx, max int) error {
	return Wrapf(ErrBTreeChildIndexOutOfRange, "child index %d out of range [0, %d)", idx, max)
}

// BTreeInvariantViolatedError 创建页面不变量违反错误
func BTreeInvariantViolatedError(children, keys int) error {
	return Wrapf(ErrBTreeInvariantViolated, "InternalPage invariant violated: len(children)=%d, len(keys)=%d", children, keys)
}

// BTreeConcurrentModificationError 创建页面并发修改错误
func BTreeConcurrentModificationError() error {
	return ErrBTreeConcurrentModification
}

// BTreeKeyNotFoundInPageError 创建页面内键未找到错误
func BTreeKeyNotFoundInPageError() error {
	return ErrBTreeKeyNotFoundInPage
}

// BTreeKeyOrderViolationError 创建键排序违反错误
func BTreeKeyOrderViolationError() error {
	return ErrBTreeKeyOrderViolation
}

// BTreeCannotSplitMinKeysError 创建分裂键数不足错误
func BTreeCannotSplitMinKeysError(count int) error {
	return fmt.Errorf("btree: cannot split page with less than 2 keys, got %d", count)
}

// BTreeDeserializeReadPageID 创建反序列化读取 pageID 失败错误
func BTreeDeserializeReadPageID(err error) error {
	return Wrapf(ErrBTreeDeserializeReadFailed, "failed to read pageID: %v", err)
}

// BTreeDeserializeReadVersion 创建反序列化读取 version 失败错误
func BTreeDeserializeReadVersion(err error) error {
	return Wrapf(ErrBTreeDeserializeReadFailed, "failed to read version: %v", err)
}

// BTreeDeserializeReadNumKeys 创建反序列化读取 numKeys 失败错误
func BTreeDeserializeReadNumKeys(err error) error {
	return Wrapf(ErrBTreeDeserializeReadFailed, "failed to read numKeys: %v", err)
}

// BTreeDeserializeReadNumChildren 创建反序列化读取 numChildren 失败错误
func BTreeDeserializeReadNumChildren(err error) error {
	return Wrapf(ErrBTreeDeserializeReadFailed, "failed to read numChildren: %v", err)
}

// BTreeDeserializeReadKeyLen 创建反序列化读取键长度失败错误
func BTreeDeserializeReadKeyLen(err error) error {
	return Wrapf(ErrBTreeDeserializeReadFailed, "failed to read key len: %v", err)
}

// BTreeDeserializeReadKeyData 创建反序列化读取键数据失败错误
func BTreeDeserializeReadKeyData(err error) error {
	return Wrapf(ErrBTreeDeserializeReadFailed, "failed to read key data: %v", err)
}

// BTreeDeserializeReadChildID 创建反序列化读取 child ID 失败错误
func BTreeDeserializeReadChildID(err error) error {
	return Wrapf(ErrBTreeDeserializeReadFailed, "failed to read child ID: %v", err)
}

// BTreeDeserializeReadValueLen 创建反序列化读取值长度失败错误
func BTreeDeserializeReadValueLen(err error) error {
	return Wrapf(ErrBTreeDeserializeReadFailed, "failed to read value len: %v", err)
}

// BTreeDeserializeReadValueData 创建反序列化读取值数据失败错误
func BTreeDeserializeReadValueData(err error) error {
	return Wrapf(ErrBTreeDeserializeReadFailed, "failed to read value data: %v", err)
}

// BTreeIndexOutOfRangeError 创建索引越界错误
func BTreeIndexOutOfRangeError(startIdx, max int) error {
	return Wrapf(ErrBTreeIndexOutOfRange, "start index %d out of range [0, %d]", startIdx, max)
}

// ===========================
// ChunkManager 错误函数
// ===========================

func BTreeCreateDataDirectory(dataDir string, err error) error {
	return Wrapf(err, "create data directory: %s", dataDir)
}

func BTreeLoadExistingChunks(err error) error {
	return Wrapf(err, "load existing chunks")
}

func BTreeReadDataDirectory(err error) error {
	return Wrapf(ErrBTreeDataDirError, "read data directory: %v", err)
}

func BTreeOpenExistingChunk(id int, err error) error {
	return Wrapf(err, "open existing chunk %d", id)
}

func BTreeEnsureCurrentChunk(err error) error {
	return Wrapf(err, "ensure current chunk")
}

func BTreeRotateChunk(err error) error {
	return Wrapf(err, "rotate chunk")
}

func BTreeAllocPageFromNewChunk(err error) error {
	return Wrapf(err, "allocate page from new chunk")
}

func BTreeEncodePagePosition(err error) error {
	return Wrapf(err, "encode page position")
}

func BTreeChunkNotFound(chunkID int) error {
	return Wrapf(ErrBTreeChunkNotFound, "chunk %d not found", chunkID)
}

func BTreeReadPageFromChunk(chunkID, offset int, err error) error {
	return Wrapf(err, "read page from chunk %d at offset %d", chunkID, offset)
}

func BTreeDeserializeLeafPage(err error) error {
	return Wrapf(ErrBTreeDeserializeFailed, "deserialize leaf page: %v", err)
}

func BTreeDeserializeInternalPage(err error) error {
	return Wrapf(ErrBTreeDeserializeFailed, "deserialize internal page: %v", err)
}

func BTreeUnknownPageTypeLoad(pageType int) error {
	return Wrapf(ErrBTreeUnknownPageType, "unknown page type: %d", pageType)
}

func BTreeCreateNewChunk(newID int, err error) error {
	return Wrapf(err, "create new chunk %d", newID)
}

func BTreeSyncChunk(chunkID int, err error) error {
	return Wrapf(err, "sync chunk %d", chunkID)
}

// ===========================
// CCOW Manager 错误函数
// ===========================

func BTreeRootPageInfoNilSnapshot() error {
	return Wrapf(ErrBTreeRootPageInfoNil, "root page info is nil")
}

func BTreeEmptyPathCCOW() error {
	return Wrapf(ErrBTreeEmptyPath, "empty path")
}

func BTreeModifyFailed(err error) error {
	return Wrapf(err, "modify failed")
}

func BTreeUpdateChildRefFailed(err error) error {
	return Wrapf(err, "update child ref failed")
}

func BTreeCASUpdateRootFailed() error {
	return Wrapf(ErrBTreeCASUpdateRootFailed, "concurrent modification detected")
}

func BTreeCollectDirtyPagesFailed(err error) error {
	return Wrapf(err, "collect dirty pages failed")
}

func BTreeSnapshotNotFound() error {
	return Wrapf(ErrBTreeSnapshotNotFound, "snapshot not found")
}

func BTreeSnapshotRootRefNil() error {
	return Wrapf(ErrBTreeSnapshotRootRefNil, "snapshot root ref is nil")
}

func BTreeSnapshotRootInfoNil() error {
	return Wrapf(ErrBTreeSnapshotRootInfoNil, "snapshot root info is nil")
}

// ===========================
// PageLock 错误函数
// ===========================

func BTreeCannotUnlockUnlocked() error {
	return Wrapf(ErrBTreeCannotUnlockUnlocked, "cannot unlock unlocked lock")
}

func BTreeUnlockStateChanged() error {
	return Wrapf(ErrBTreeUnlockStateChanged, "unlock failed: state changed")
}

// ===========================
// PagePersist 错误函数
// ===========================

func BTreeOpenFileError(err error) error {
	return Wrapf(err, "open file")
}

func BTreeStatFileError(err error) error {
	return Wrapf(err, "stat file")
}

func BTreeSeekOffset(offset int64, err error) error {
	return Wrapf(err, "seek to offset %d", offset)
}

func BTreeWritePageFailed(pageID int, err error) error {
	return Wrapf(err, "write page %d", pageID)
}

func BTreeSyncPageFailed(pageID int, err error) error {
	return Wrapf(err, "sync page %d", pageID)
}

func BTreeReadPageFailed(pageID int, err error) error {
	return Wrapf(err, "read page %d", pageID)
}

func BTreeIncompleteRead(n, pageSize int) error {
	return Wrapf(ErrBTreeIncompleteRead, "incomplete read: %d < %d", n, pageSize)
}

func BTreeSyncFileError(err error) error {
	return Wrapf(err, "sync file")
}

func BTreeCloseFileError(err error) error {
	return Wrapf(err, "close file")
}

func BTreeWritePageIsNil() error {
	return Wrapf(ErrBTreeWritePageNil, "WritePage: page is nil")
}

// ===========================
// Chunk 错误函数
// ===========================

// BTreeOpenChunkFile 打开 chunk 文件失败
func BTreeOpenChunkFile(filePath string, err error) error {
	return Wrapf(ErrBTreeOpenFile, "open chunk file %s: %v", filePath, err)
}

// BTreeStatChunkFile stat chunk 文件失败
func BTreeStatChunkFile(filePath string, err error) error {
	return Wrapf(ErrBTreeStatFile, "stat chunk file %s: %v", filePath, err)
}

// BTreeChunkReadOnly chunk 只读错误
func BTreeChunkReadOnly(chunkID int) error {
	return Wrapf(ErrBTreeChunkReadOnly, "chunk %d is read-only", chunkID)
}

// BTreeChunkFull chunk 已满错误
func BTreeChunkFull(chunkID int, size, current int64) error {
	return Wrapf(ErrBTreeChunkFull, "chunk %d is full (size: %d, current: %d)", chunkID, size, current)
}

// BTreeChunkPageSizeMismatch chunk 页面大小不匹配错误
func BTreeChunkPageSizeMismatch(expected, got int) error {
	return Wrapf(ErrBTreeChunkPageSizeMismatch, "page size must be %d bytes, got %d", expected, got)
}

// BTreeChunkPositionOutOfRange chunk 位置越界错误
func BTreeChunkPositionOutOfRange(pos int64, chunkSize int) error {
	return Wrapf(ErrBTreeChunkPositionOutOfRange, "position %d out of chunk bounds [0, %d]", pos, chunkSize)
}

// BTreeChunkWritePageFailed chunk 写页面失败
func BTreeChunkWritePageFailed(pos int64, err error) error {
	return Wrapf(ErrBTreeWriteFile, "write page at pos %d: %v", pos, err)
}

// BTreeChunkReadPageFailed chunk 读页面失败
func BTreeChunkReadPageFailed(pos int64, err error) error {
	return Wrapf(ErrBTreeReadFile, "read page at pos %d: %v", pos, err)
}

// BTreeWritePageAt 写页面到指定位置失败
func BTreeWritePageAt(pos int64, err error) error {
	return Wrapf(ErrBTreeWriteFile, "write page at pos %d: %v", pos, err)
}

// BTreeReadPageAt 从指定位置读页面失败
func BTreeReadPageAt(pos int64, err error) error {
	return Wrapf(ErrBTreeReadFile, "read page at pos %d: %v", pos, err)
}

// ===========================
// PageSerializer 错误函数
// ===========================

func BTreeInvalidDataSize(expected, got int) error {
	return Wrapf(ErrBTreeInvalidDataSize, "invalid data size: expected %d bytes, got %d", expected, got)
}

func BTreeUnexpectedEOF(expected, remaining int) error {
	return Wrapf(ErrBTreeUnexpectedEOF, "unexpected EOF: expected %d bytes, got %d", expected, remaining)
}

// ===========================
// PageRef 错误函数
// ===========================

func BTreePageInfoNilRef() error {
	return Wrapf(ErrBTreePageInfoNil, "pageInfo is nil")
}

func BTreePageNotLoadedInvalidNodeRef() error {
	return Wrapf(ErrBTreePageNotLoadedInvalidNodeRef, "page not loaded (invalid NodeRef)")
}

// ===========================
// BTree Ops 错误函数
// ===========================

func BTreeCloneLeafPageFailed() error {
	return Wrapf(ErrBTreeCloneLeafPageFailed, "clone leaf page failed")
}

func BTreeInsertIntoLeafFailed(err error) error {
	return Wrapf(err, "insert into leaf")
}

func BTreeFinalizeDeepCloneErr(err error) error {
	return Wrapf(err, "finalize deep clone")
}

func BTreeTaskExecutionFailed(err error) error {
	return Wrapf(err, "task execution failed")
}

// ===========================
// BTree GC 错误函数
// ===========================

func BTreeOutOfMemory(used, requesting, limit int64) error {
	return Wrapf(ErrBTreeOutOfMemory, "out of memory: used=%d, requesting=%d, limit=%d", used, requesting, limit)
}

// ===========================
// ParentSplit 错误函数
// ===========================

func BTreeAsyncParentSplitFailed(pageID int, err error) error {
	return Wrapf(err, "async parent split pageID=%d", pageID)
}

// ===========================
// BTreeSetItem 错误函数
// ===========================

func BTreeSetWithLeafRefFailed(err error) error {
	return Wrapf(err, "btree set with leaf ref failed")
}

// ===========================
// Position 错误函数
// ===========================

func BTreeInvalidChunkIDRange(chunkID, maxChunks int) error {
	return Wrapf(ErrBTreeInvalidChunkID, "chunk ID %d out of range [0, %d)", chunkID, maxChunks)
}

func BTreeInvalidOffsetRange(offset, maxOffset int) error {
	return Wrapf(ErrBTreeInvalidOffset, "offset %d out of range [0, %d)", offset, maxOffset)
}

func BTreeInvalidPageTypeRange(pageType, maxPageType int) error {
	return Wrapf(ErrBTreeInvalidPageType, "page type %d out of range [0, %d)", pageType, maxPageType)
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

func OffHeapVirtualAllocFailed(err error) error {
	return Wrapf(err, "VirtualAlloc failed")
}

func OffHeapVirtualFreeFailed(err error) error {
	return Wrapf(err, "VirtualFree failed")
}

func OffHeapInsertEntry(index int, err error) error {
	return Wrapf(err, "insert entry %d", index)
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

// ===========================
// OffHeap Adapter 错误函数
// ===========================

// BTreeAllocPageAdapter 分配页面失败（AllocLeafPage/AllocIndexPage）
func BTreeAllocPageAdapter(err error) error {
	return err
}

// BTreeMaterializePageAdapter 物化页面失败（通用）
func BTreeMaterializePageAdapter(err error) error {
	return err
}

// BTreeAllocNewPageForSplit 分配新页面用于更新/分裂
func BTreeAllocNewPageForSplit(err error) error {
	return err
}

// BTreeMaterializePageForSplit 物化索引页面失败（UpdateIndexEntry/ReplaceChild）
func BTreeMaterializePageForSplit(err error) error {
	return err
}

// BTreeInvalidParamMsg 创建带消息的参数无效错误
func BTreeInvalidParamMsg(msg string) error {
	return Wrapf(ErrBTreeInvalidParam, "%s", msg)
}

// BTreeIndexPageFull 索引页面已满
func BTreeIndexPageFull(pageID uint64) error {
	return Wrapf(ErrBTreePageFull, "index page %d is full, cannot insert entry", pageID)
}

// BTreeKeyOrderViolationAt 键排序违反（带位置信息）
func BTreeKeyOrderViolationAt(i int, prev, curr []byte) error {
	return Wrapf(ErrBTreeKeyOrderViolation, "keys not sorted: [%d] %v >= [%d] %v", i-1, prev, i, curr)
}

// BTreeStaleChildRef 陈旧的子节点引用
func BTreeStaleChildRef(parent, child uint64, expected, actual uint64) error {
	return Wrapf(ErrBTreeStaleChildReference, "parent=%d child=%d expectedVersion=%d actualVersion=%d", parent, child, expected, actual)
}

// BTreeUpdateIndexEntryRightZero UpdateIndexEntry 中 rightPageID 为零
func BTreeUpdateIndexEntryRightZero() error {
	return Wrapf(ErrBTreeInvalidParam, "UpdateIndexEntry: rightPageID cannot be 0 (use ReplaceChild for single child replacement)")
}

// BTreeUpdateIndexEntryLeftZero UpdateIndexEntry 中 leftPageID 为零
func BTreeUpdateIndexEntryLeftZero() error {
	return Wrapf(ErrBTreeInvalidParam, "UpdateIndexEntry: leftPageID cannot be 0")
}

// BTreeParentFull 父节点已满
func BTreeParentFull(count, max int) error {
	return Wrapf(ErrBTreePageFull, "parent page full: count=%d, max=%d", count, max)
}

// BTreeInvalidChildIndexAt 无效的子节点索引
func BTreeInvalidChildIndexAt(index, count int) error {
	return Wrapf(ErrBTreeChildIndexOutOfRange, "invalid child index: %d (count=%d)", index, count)
}

// BTreeAllocNewPageForDelete 分配删除操作所需的新页面
func BTreeAllocNewPageForDelete(err error) error {
	return Wrapf(ErrBTreeAllocPageFailed, "alloc new page for delete: %v", err)
}

// BTreeMaterializePageAfterDelete 删除后物化页面失败
func BTreeMaterializePageAfterDelete(err error) error {
	return Wrapf(ErrBTreeMaterializePageFailed, "materialize page after delete: %v", err)
}

// BTreeAllocNewParentPage 分配新父页面失败
func BTreeAllocNewParentPage(err error) error {
	return err
}

// BTreeMaterializeParentPage 物化父页面失败
func BTreeMaterializeParentPage(err error) error {
	return err
}

// BTreeDuplicatePageIDAlloc 分配器返回了相同的 pageID
func BTreeDuplicatePageIDAlloc(pageID uint32) error {
	return ErrBTreeDuplicatePageIDAlloc
}

// BTreeInvalidPageIDAlloc 分配器返回了无效的 pageID
func BTreeInvalidPageIDAlloc(left, right uint32) error {
	return ErrBTreeInvalidPageIDAlloc
}

// BTreeSplitMinKeys 分裂时键数不足（需要 less than 2 keys）
func BTreeSplitMinKeys(count int) error {
	return fmt.Errorf("btree: cannot split page with less than 2 keys, got %d", count)
}

// BTreePageTooLargeToSplit 页面过大无法分裂
func BTreePageTooLargeToSplit(count int) error {
	return ErrBTreePageTooLarge
}

// BTreeInvalidSplitIdx 无效的分裂索引
func BTreeInvalidSplitIdx(splitIdx, keysLen int) error {
	return ErrBTreeInvalidSplitIndex
}

// BTreeMaterializeLeftPage 物化左页面失败
func BTreeMaterializeLeftPage(err error) error {
	return err
}

// BTreeMaterializeRightPage 物化右页面失败
func BTreeMaterializeRightPage(err error) error {
	return err
}

// BTreeAllocLeftPage 分配左页面失败
func BTreeAllocLeftPage(err error) error {
	return err
}

// BTreeAllocRightPage 分配右页面失败
func BTreeAllocRightPage(err error) error {
	return err
}

// BTreeBulkInitFailed BulkInit 页面拷贝失败
func BTreeBulkInitFailed(err error) error {
	return err
}
