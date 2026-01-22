// Package types 定义内部通用的数据类型
//
// 避免各层之间的循环依赖，提供统一的类型定义
package types

import (
	"fmt"
)

// ErrorCode 统一错误码（从 errcodes 迁移）
//
// 为了保持与现有代码的兼容性，使用 int 型枚举
type ErrorCode int

const (
	// ========================================
	// 存储层错误码
	// ========================================

	// P2-1 修复：添加占位符，使错误码从 1 开始（而非 0）
	// 原因：ErrorCode 类型的零值也是 0，无法区分未初始化的 ErrorCode 和有效的 ErrCodeNotFound
	_ ErrorCode = iota

	// ErrCodeNotFound 键不存在
	ErrCodeNotFound

	// ErrCodeAlreadyExists 键已存在
	ErrCodeAlreadyExists

	// ErrCodeVersionNotFound 版本不存在
	ErrCodeVersionNotFound

	// ErrCodeChecksum 校验和错误
	ErrCodeChecksum

	// ErrCodeClosed 存储已关闭
	ErrCodeClosed

	// ErrCodeInternal 内部错误
	ErrCodeInternal

	// ========================================
	// 传输层错误码
	// ========================================

	// ErrCodeTransport 传输错误
	ErrCodeTransport

	// ========================================
	// 帧错误码
	// ========================================

	// ErrCodeInvalidFrameMagic 魔数无效
	ErrCodeInvalidFrameMagic

	// ErrCodeFrameTooLarge 帧过大
	ErrCodeFrameTooLarge

	// ErrCodeFrameChecksum 帧校验和错误
	ErrCodeFrameChecksum

	// ErrCodeInvalidFrameSize 无效的帧大小
	ErrCodeInvalidFrameSize

	// ========================================
	// 编解码错误码
	// ========================================

	// ErrCodecInvalidCodec 无效的编解码器
	ErrCodecInvalidCodec

	// ErrCodecEncodeFailed 编码失败
	ErrCodecEncodeFailed

	// ErrCodecDecodeFailed 解码失败
	ErrCodecDecodeFailed

	// ErrCodecInvalidData 无效的数据
	ErrCodecInvalidData

	// ErrCodecInvalidMessage 无效的消息
	ErrCodecInvalidMessage

	// ErrCodecUnknownMessageType 未知消息类型
	ErrCodecUnknownMessageType

	// ErrCompressionDecompress 解压失败
	ErrCompressionDecompress

	// ErrCompressionCompress 压缩失败
	ErrCompressionCompress

	// ========================================
	// Store 模块错误码
	// ========================================

	// ErrStoreDirectoryCreation 目录创建失败
	ErrStoreDirectoryCreation

	// ErrStoreWALOperation WAL 操作失败
	ErrStoreWALOperation

	// ErrStoreSnapshotOperation 快照操作失败
	ErrStoreSnapshotOperation

	// ErrStoreKeyValidation Key 验证失败
	ErrStoreKeyValidation

	// ErrStoreInvalidParameter 无效参数
	ErrStoreInvalidParameter

	// ========================================
	// Consensus 模块错误码
	// ========================================

	// ErrConsensusNilParameter 参数为空
	ErrConsensusNilParameter

	// ErrConsensusServiceState 服务状态错误
	ErrConsensusServiceState

	// ErrConsensusTransaction 事务相关错误
	ErrConsensusTransaction

	// ErrConsensusOperation 协议操作失败
	ErrConsensusOperation

	// ErrConsensusTimeout 超时错误
	ErrConsensusTimeout

	// ErrConsensusUnknownOperation 未知操作类型
	ErrConsensusUnknownOperation

	// ========================================
	// Cluster 模块错误码
	// ========================================

	// ErrClusterNilParameter 参数为空
	ErrClusterNilParameter

	// ErrClusterServiceState 服务状态错误
	ErrClusterServiceState

	// ErrClusterNodeManagement 节点管理错误
	ErrClusterNodeManagement

	// ErrClusterCoordinator 协调器操作错误
	ErrClusterCoordinator

	// ErrClusterTreeManagement 树结构管理错误
	ErrClusterTreeManagement

	// ErrClusterElection 选举错误
	ErrClusterElection

	// ErrClusterFailureDetection 故障检测错误
	ErrClusterFailureDetection

	// ErrClusterNodeNotFound 节点不存在
	ErrClusterNodeNotFound

	// ========================================
	// Transport 模块错误码
	// ========================================

	// ErrTransportConnection 连接错误
	ErrTransportConnection

	// ErrTransportState 传输状态错误
	ErrTransportState

	// ErrTransportTimeout 传输超时
	ErrTransportTimeout

	// ErrTransportSend 发送失败
	ErrTransportSend

	// ErrTransportReceive 接收失败
	ErrTransportReceive

	// ErrTransportHopCountExpired Hop Count 过期（消息不再转发）
	ErrTransportHopCountExpired

	// ========================================
	// Config 模块错误码
	// ========================================

	// ErrConfigLoad 配置加载失败
	ErrConfigLoad

	// ErrConfigValidation 配置验证失败
	ErrConfigValidation

	// ========================================
	// UUID/Clock 模块错误码
	// ========================================

	// ErrUUIDFormat UUID 格式错误
	ErrUUIDFormat

	// ErrClockOperation 时钟操作错误
	ErrClockOperation
)

// ========================================
// 统一错误结构
// ========================================

// Error 统一错误结构（从 errcodes 迁移）
//
// 兼容原有 errcodes.Error 结构，包含 Code, Message, Op, Err 字段
type Error struct {
	Code    ErrorCode // 错误码
	Message string    // 错误消息
	Op      string    // 操作（可选）
	Err     error     // 底层错误（可选）
}

// Error 实现 error 接口
func (e *Error) Error() string {
	if e.Op != "" {
		if e.Err != nil {
			return e.Op + ": " + e.Message + ": " + e.Err.Error()
		}
		return e.Op + ": " + e.Message
	}
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// Unwrap 实现 errors.Unwrap 接口，支持错误链
func (e *Error) Unwrap() error {
	return e.Err
}

// Temporary 返回是否为临时错误（可重试）
func (e *Error) Temporary() bool {
	// 某些错误码默认为临时错误
	return e.Code == ErrCodeTransport
}

// ========================================
// 通用错误构造函数
// ========================================

// New 创建新错误
func New(code ErrorCode, msg string) *Error {
	return &Error{
		Code:    code,
		Message: msg,
	}
}

// NewWithErr 创建带底层错误的错误
func NewWithErr(code ErrorCode, msg string, err error) *Error {
	return &Error{
		Code:    code,
		Message: msg,
		Err:     err,
	}
}

// NewOp 创建带操作信息的错误
func NewOp(code ErrorCode, op, msg string) *Error {
	return &Error{
		Code:    code,
		Message: msg,
		Op:      op,
	}
}

// NewOpErr 创建带操作和底层错误的错误
func NewOpErr(code ErrorCode, op, msg string, err error) *Error {
	return &Error{
		Code:    code,
		Message: msg,
		Op:      op,
		Err:     err,
	}
}

// ========================================
// 存储层错误构造函数
// ========================================

// NewNotFoundError 创建"键不存在"错误
func NewNotFoundError(key string) *Error {
	return &Error{
		Code:    ErrCodeNotFound,
		Message: fmt.Sprintf("键不存在: %s", key),
	}
}

// NewAlreadyExistsError 创建"键已存在"错误
func NewAlreadyExistsError(key string) *Error {
	return &Error{
		Code:    ErrCodeAlreadyExists,
		Message: fmt.Sprintf("键已存在: %s", key),
	}
}

// NewVersionNotFoundError 创建"版本不存在"错误
func NewVersionNotFoundError(key string, version uint64) *Error {
	return &Error{
		Code:    ErrCodeVersionNotFound,
		Message: fmt.Sprintf("版本不存在: %s@%d", key, version),
	}
}

// NewChecksumError 创建校验和错误
func NewChecksumError(expected, actual uint32) *Error {
	return &Error{
		Code:    ErrCodeChecksum,
		Message: fmt.Sprintf("校验和不匹配: 期望 %d, 实际 %d", expected, actual),
	}
}

// NewClosedError 创建"已关闭"错误
func NewClosedError(resource string) *Error {
	return &Error{
		Code:    ErrCodeClosed,
		Message: fmt.Sprintf("%s 已关闭", resource),
	}
}

// NewInternalError 创建内部错误
func NewInternalError(msg string, err error) *Error {
	return &Error{
		Code:    ErrCodeInternal,
		Message: msg,
		Err:     err,
	}
}

// ========================================
// 传输层错误构造函数
// ========================================

// NewTransportError 创建传输错误
func NewTransportError(op, addr string, err error) *Error {
	return &Error{
		Code:    ErrCodeTransport,
		Message: "传输错误",
		Op:      fmt.Sprintf("%s %s", op, addr),
		Err:     err,
	}
}

// ========================================
// 帧错误构造函数
// ========================================

// NewFrameInvalidMagicError 创建魔数无效错误
func NewFrameInvalidMagicError() *Error {
	return &Error{
		Code:    ErrCodeInvalidFrameMagic,
		Message: "帧魔数无效",
	}
}

// NewFrameTooLargeError 创建帧过大错误
func NewFrameTooLargeError(size int) *Error {
	return &Error{
		Code:    ErrCodeFrameTooLarge,
		Message: fmt.Sprintf("帧过大: %d 字节", size),
	}
}

// NewFrameChecksumError 创建帧校验和错误
func NewFrameChecksumError() *Error {
	return &Error{
		Code:    ErrCodeFrameChecksum,
		Message: "帧校验和不匹配",
	}
}

// NewInvalidFrameSizeError 创建无效帧大小错误
func NewInvalidFrameSizeError(msg string) *Error {
	return &Error{
		Code:    ErrCodeInvalidFrameSize,
		Message: fmt.Sprintf("无效的帧大小: %s", msg),
	}
}

// ========================================
// 编解码错误构造函数
// ========================================

// NewCodecEncodeFailedError 创建编码失败错误
func NewCodecEncodeFailedError(op string, err error) *Error {
	return &Error{
		Code:    ErrCodecEncodeFailed,
		Message: "编码失败",
		Op:      op,
		Err:     err,
	}
}

// NewCodecDecodeFailedError 创建解码失败错误
func NewCodecDecodeFailedError(op string, err error) *Error {
	return &Error{
		Code:    ErrCodecDecodeFailed,
		Message: "解码失败",
		Op:      op,
		Err:     err,
	}
}

// NewCodecInvalidDataError 创建无效数据错误
func NewCodecInvalidDataError(op string, msg string) *Error {
	return &Error{
		Code:    ErrCodecInvalidData,
		Message: fmt.Sprintf("无效数据: %s", msg),
		Op:      op,
	}
}

// NewCodecInvalidMessageError 创建无效消息错误
func NewCodecInvalidMessageError(msg string) *Error {
	return &Error{
		Code:    ErrCodecInvalidMessage,
		Message: msg,
	}
}

// NewCodecUnknownMessageTypeError 创建未知消息类型错误
func NewCodecUnknownMessageTypeError(msgType int) *Error {
	return &Error{
		Code:    ErrCodecUnknownMessageType,
		Message: fmt.Sprintf("未知消息类型: %d", msgType),
	}
}

// ========================================
// Store 模块错误构造函数
// ========================================

// NewStoreDirectoryCreationError 创建目录创建失败错误
func NewStoreDirectoryCreationError(dir string, err error) *Error {
	return &Error{
		Code:    ErrStoreDirectoryCreation,
		Message: fmt.Sprintf("创建目录失败: %s", dir),
		Err:     err,
	}
}

// NewStoreWALError 创建 WAL 操作失败错误
func NewStoreWALError(op string, err error) *Error {
	return &Error{
		Code:    ErrStoreWALOperation,
		Message: fmt.Sprintf("WAL %s 失败", op),
		Err:     err,
	}
}

// NewStoreSnapshotError 创建快照操作失败错误
func NewStoreSnapshotError(op string, err error) *Error {
	return &Error{
		Code:    ErrStoreSnapshotOperation,
		Message: fmt.Sprintf("快照%s失败", op),
		Err:     err,
	}
}

// NewStoreKeyValidationError 创建 Key 验证失败错误
func NewStoreKeyValidationError(msg string) *Error {
	return &Error{
		Code:    ErrStoreKeyValidation,
		Message: msg,
	}
}

// NewStoreInvalidParameterError 创建无效参数错误
func NewStoreInvalidParameterError(param string) *Error {
	return &Error{
		Code:    ErrStoreInvalidParameter,
		Message: fmt.Sprintf("无效参数: %s", param),
	}
}

// ========================================
// Consensus 模块错误构造函数
// ========================================

// NewConsensusNilParameterError 创建参数为空错误
func NewConsensusNilParameterError(param string) *Error {
	return &Error{
		Code:    ErrConsensusNilParameter,
		Message: fmt.Sprintf("%s 不能为空", param),
	}
}

// NewConsensusServiceStateError 创建服务状态错误
func NewConsensusServiceStateError(service, state string) *Error {
	return &Error{
		Code:    ErrConsensusServiceState,
		Message: fmt.Sprintf("%s%s", service, state),
	}
}

// NewConsensusTransactionError 创建事务错误
func NewConsensusTransactionError(msg string, err error) *Error {
	return &Error{
		Code:    ErrConsensusTransaction,
		Message: msg,
		Err:     err,
	}
}

// NewConsensusOperationError 创建协议操作失败错误
func NewConsensusOperationError(op string, err error) *Error {
	return &Error{
		Code:    ErrConsensusOperation,
		Message: fmt.Sprintf("%s 失败", op),
		Err:     err,
	}
}

// NewConsensusTimeoutError 创建超时错误
func NewConsensusTimeoutError(op string) *Error {
	return &Error{
		Code:    ErrConsensusTimeout,
		Message: fmt.Sprintf("%s 超时", op),
	}
}

// NewConsensusUnknownOperationError 创建未知操作类型错误
func NewConsensusUnknownOperationError(opType string) *Error {
	return &Error{
		Code:    ErrConsensusUnknownOperation,
		Message: fmt.Sprintf("未知操作类型: %s", opType),
	}
}

// ========================================
// Cluster 模块错误构造函数
// ========================================

// NewClusterNilParameterError 创建参数为空错误
func NewClusterNilParameterError(param string) *Error {
	return &Error{
		Code:    ErrClusterNilParameter,
		Message: fmt.Sprintf("%s 不能为空", param),
	}
}

// NewClusterServiceStateError 创建服务状态错误
func NewClusterServiceStateError(service, state string) *Error {
	return &Error{
		Code:    ErrClusterServiceState,
		Message: fmt.Sprintf("%s%s", service, state),
	}
}

// NewClusterNodeManagementError 创建节点管理错误
func NewClusterNodeManagementError(op, nodeID string, err error) *Error {
	msg := fmt.Sprintf("%s节点 %s 失败", op, nodeID)
	if err != nil {
		msg += ": " + err.Error()
	}
	return &Error{
		Code:    ErrClusterNodeManagement,
		Message: msg,
		Err:     err,
	}
}

// NewClusterCoordinatorError 创建协调器操作错误
func NewClusterCoordinatorError(msg string, err error) *Error {
	return &Error{
		Code:    ErrClusterCoordinator,
		Message: msg,
		Err:     err,
	}
}

// NewClusterTreeManagementError 创建树结构管理错误
func NewClusterTreeManagementError(msg string) *Error {
	return &Error{
		Code:    ErrClusterTreeManagement,
		Message: msg,
	}
}

// NewClusterElectionError 创建选举错误
func NewClusterElectionError(msg string, err error) *Error {
	return &Error{
		Code:    ErrClusterElection,
		Message: msg,
		Err:     err,
	}
}

// NewClusterFailureDetectionError 创建故障检测错误
func NewClusterFailureDetectionError(msg string, err error) *Error {
	return &Error{
		Code:    ErrClusterFailureDetection,
		Message: msg,
		Err:     err,
	}
}

// NewClusterNodeNotFoundError 创建节点不存在错误
func NewClusterNodeNotFoundError(nodeID string) *Error {
	return &Error{
		Code:    ErrClusterNodeNotFound,
		Message: fmt.Sprintf("节点不存在: %s", nodeID),
	}
}

// ========================================
// Transport 模块错误构造函数
// ========================================

// NewTransportConnectionError 创建连接错误
func NewTransportConnectionError(op, addr string, err error) *Error {
	return &Error{
		Code:    ErrTransportConnection,
		Message: fmt.Sprintf("%s %s 失败", op, addr),
		Err:     err,
	}
}

// NewTransportStateError 创建传输状态错误
func NewTransportStateError(state string) *Error {
	return &Error{
		Code:    ErrTransportState,
		Message: fmt.Sprintf("传输层%s", state),
	}
}

// NewTransportTimeoutError 创建传输超时错误
func NewTransportTimeoutError(op string) *Error {
	return &Error{
		Code:    ErrTransportTimeout,
		Message: fmt.Sprintf("%s 超时", op),
	}
}

// NewTransportSendError 创建发送失败错误
func NewTransportSendError(err error) *Error {
	return &Error{
		Code:    ErrTransportSend,
		Message: "发送消息失败",
		Err:     err,
	}
}

// NewTransportReceiveError 创建接收失败错误
func NewTransportReceiveError(err error) *Error {
	return &Error{
		Code:    ErrTransportReceive,
		Message: "接收消息失败",
		Err:     err,
	}
}

// NewTransportHopCountExpiredError 创建 Hop Count 过期错误
func NewTransportHopCountExpiredError() *Error {
	return &Error{
		Code:    ErrTransportHopCountExpired,
		Message: "消息已过期（HopCount=0），不再转发",
	}
}

// ========================================
// Config 模块错误构造函数
// ========================================

// NewConfigLoadError 创建配置加载失败错误
func NewConfigLoadError(op string, err error) *Error {
	return &Error{
		Code:    ErrConfigLoad,
		Message: fmt.Sprintf("配置%s失败", op),
		Err:     err,
	}
}

// NewConfigValidationError 创建配置验证失败错误
func NewConfigValidationError(field, msg string) *Error {
	return &Error{
		Code:    ErrConfigValidation,
		Message: fmt.Sprintf("配置验证失败: %s %s", field, msg),
	}
}

// ========================================
// UUID/Clock 模块错误构造函数
// ========================================

// NewUUIDFormatError 创建 UUID 格式错误
func NewUUIDFormatError(msg string, err error) *Error {
	return &Error{
		Code:    ErrUUIDFormat,
		Message: fmt.Sprintf("UUID 格式错误: %s", msg),
		Err:     err,
	}
}

// NewClockOperationError 创建时钟操作错误
func NewClockOperationError(msg string) *Error {
	return &Error{
		Code:    ErrClockOperation,
		Message: msg,
	}
}

// ========================================
// Compression 模块错误构造函数
// ========================================

// NewCompressionDecompressError 创建解压失败错误
func NewCompressionDecompressError(op string, err error) *Error {
	return &Error{
		Code:    ErrCompressionDecompress,
		Message: "解压失败",
		Op:      op,
		Err:     err,
	}
}

// NewCompressionCompressError 创建压缩失败错误
func NewCompressionCompressError(op string, err error) *Error {
	return &Error{
		Code:    ErrCompressionCompress,
		Message: "压缩失败",
		Op:      op,
		Err:     err,
	}
}
