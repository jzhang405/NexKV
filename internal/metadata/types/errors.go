// Package types 定义内部通用的数据类型
//
// 避免各层之间的循环依赖，提供统一的类型定义
package types

import (
	"context"
	"errors"
	"fmt"
	"time"
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
	// PR-033: Host 管理错误码
	// ========================================

	// ErrClusterHostIDRequired HostID 必填
	ErrClusterHostIDRequired

	// ErrClusterHostnameRequired Hostname 必填
	ErrClusterHostnameRequired

	// ErrClusterInvalidNodeIDConstraints 无效的 NodeID 约束
	ErrClusterInvalidNodeIDConstraints

	// ErrClusterHostMarshalFailed Host 序列化失败
	ErrClusterHostMarshalFailed

	// ErrClusterHostSaveFailed Host 保存失败
	ErrClusterHostSaveFailed

	// ErrClusterHostNotFound Host 不存在
	ErrClusterHostNotFound

	// ErrClusterHostUnmarshalFailed Host 反序列化失败
	ErrClusterHostUnmarshalFailed

	// ErrClusterHostDeleteFailed Host 删除失败
	ErrClusterHostDeleteFailed

	// ErrClusterHostListFailed Host 列表获取失败
	ErrClusterHostListFailed

	// ========================================
	// PR-033: Port Allocator 错误码
	// ========================================

	// ErrClusterPortAllocationNotFound 端口分配不存在
	ErrClusterPortAllocationNotFound

	// ErrClusterPortConflictCheckFailed 端口冲突检查失败
	ErrClusterPortConflictCheckFailed

	// ErrClusterPortAllocationSaveFailed 端口分配保存失败
	ErrClusterPortAllocationSaveFailed

	// ErrClusterPortAllocationUnmarshalFailed 端口分配反序列化失败
	ErrClusterPortAllocationUnmarshalFailed

	// ErrClusterPortAllocationListFailed 端口分配列表获取失败
	ErrClusterPortAllocationListFailed

	// ErrClusterPortAllocationMarshalFailed 端口分配序列化失败
	ErrClusterPortAllocationMarshalFailed

	// ErrClusterPortReleaseFailed 端口释放失败
	ErrClusterPortReleaseFailed

	// ========================================
	// PR-033: Failure Detector 错误码
	// ========================================

	// ErrClusterTCPProbeFailed TCP 探测失败
	ErrClusterTCPProbeFailed

	// ErrClusterNoProbeResult 无探测结果
	ErrClusterNoProbeResult

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

	// ErrTransportInvalidListenAddr 监听地址无效
	ErrTransportInvalidListenAddr

	// ErrTransportUnsupportedProtocol 不支持的协议类型
	ErrTransportUnsupportedProtocol

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

	// ========================================
	// Encryption 模块错误码
	// ========================================

	// ErrEncryptionKeySize 加密密钥大小错误
	ErrEncryptionKeySize

	// ErrEncryptionIVSize 加密IV大小错误
	ErrEncryptionIVSize

	// ErrEncryptionCiphertextSize 密文大小错误
	ErrEncryptionCiphertextSize

	// ErrEncryptionPadSize 填充大小错误
	ErrEncryptionPadSize

	// ErrEncryptionEmptyData 空数据错误
	ErrEncryptionEmptyData

	// ========================================
	// Frame IO 模块错误码
	// ========================================

	// ErrFrameIOWrite 帧写入失败
	ErrFrameIOWrite

	// ErrFrameIORead 帧读取失败
	ErrFrameIORead

	// ErrFrameConnectionTimeout 帧连接超时
	ErrFrameConnectionTimeout

	// ========================================
	// Frame Parsing 模块错误码
	// ========================================

	// ErrFrameParseFixedHeader 解析固定头失败
	ErrFrameParseFixedHeader

	// ErrFrameParseExtensionHeader 解析扩展头失败
	ErrFrameParseExtensionField

	// ErrFrameDefragmentation 分片反序列化失败
	ErrFrameDefragmentation
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

// ========================================
// 通用错误构造函数
// ========================================

// newBase 创建基础错误（内部辅助函数）
func newBase(code ErrorCode, format string, args ...any) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}

// newWithErr 创建带底层错误的错误（内部辅助函数）
func newWithErr(code ErrorCode, cause error, format string, args ...any) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
		Err:     cause,
	}
}

// newWithOp 创建带操作信息的错误（内部辅助函数）
func newWithOp(code ErrorCode, op string, format string, args ...any) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
		Op:      op,
	}
}

// newWithOpErr 创建带操作和底层错误的错误（内部辅助函数）
func newWithOpErr(code ErrorCode, op string, cause error, format string, args ...any) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
		Op:      op,
		Err:     cause,
	}
}

// New 创建新错误
func New(code ErrorCode, msg string) *Error {
	return newBase(code, msg)
}

// NewWithErr 创建带底层错误的错误
func NewWithErr(code ErrorCode, msg string, err error) *Error {
	return newWithErr(code, err, msg)
}

// NewOp 创建带操作信息的错误
func NewOp(code ErrorCode, op, msg string) *Error {
	return newWithOp(code, op, msg)
}

// NewOpErr 创建带操作和底层错误的错误
func NewOpErr(code ErrorCode, op, msg string, err error) *Error {
	return newWithOpErr(code, op, err, msg)
}

// ========================================
// 存储层错误构造函数
// ========================================

// NewNotFoundError 创建"键不存在"错误
func NewNotFoundError(key string) *Error {
	return newBase(ErrCodeNotFound, "键不存在: %s", key)
}

// NewAlreadyExistsError 创建"键已存在"错误
func NewAlreadyExistsError(key string) *Error {
	return newBase(ErrCodeAlreadyExists, "键已存在: %s", key)
}

// NewVersionNotFoundError 创建"版本不存在"错误
func NewVersionNotFoundError(key string, version uint64) *Error {
	return newBase(ErrCodeVersionNotFound, "版本不存在: %s@%d", key, version)
}

// NewChecksumError 创建校验和错误
func NewChecksumError(expected, actual uint32) *Error {
	return newBase(ErrCodeChecksum, "校验和不匹配: 期望 %d, 实际 %d", expected, actual)
}

// NewClosedError 创建"已关闭"错误
func NewClosedError(resource string) *Error {
	return newBase(ErrCodeClosed, "%s 已关闭", resource)
}

// NewInternalError 创建内部错误
func NewInternalError(msg string, err error) *Error {
	return newWithErr(ErrCodeInternal, err, msg)
}

// ========================================
// 传输层错误构造函数
// ========================================

// ========================================
// 帧错误构造函数
// ========================================

// NewFrameInvalidMagicError 创建魔数无效错误
func NewFrameInvalidMagicError() *Error {
	return newBase(ErrCodeInvalidFrameMagic, "帧魔数无效")
}

// NewFrameTooLargeError 创建帧过大错误
func NewFrameTooLargeError(size int) *Error {
	return newBase(ErrCodeFrameTooLarge, "帧过大: %d 字节", size)
}

// NewFrameChecksumError 创建帧校验和错误
func NewFrameChecksumError() *Error {
	return newBase(ErrCodeFrameChecksum, "帧校验和不匹配")
}

// NewInvalidFrameSizeError 创建无效帧大小错误
func NewInvalidFrameSizeError(msg string) *Error {
	return newBase(ErrCodeInvalidFrameSize, "无效的帧大小: %s", msg)
}

// NewFrameIOWriteError 创建帧写入失败错误
func NewFrameIOWriteError(op string, err error) *Error {
	return newWithErr(ErrFrameIOWrite, err, "帧写入失败: %s", op)
}

// NewFrameIOReadError 创建帧读取失败错误
func NewFrameIOReadError(op string, err error) *Error {
	return newWithErr(ErrFrameIORead, err, "帧读取失败: %s", op)
}

// NewFrameConnectionTimeoutError 创建帧连接超时错误
func NewFrameConnectionTimeoutError(timeout string) *Error {
	return newBase(ErrFrameConnectionTimeout, "连接超时 (%v)", timeout)
}

// NewFrameParseFixedHeaderError 创建解析固定头失败错误
func NewFrameParseFixedHeaderError(err error) *Error {
	return newWithErr(ErrFrameParseFixedHeader, err, "解析固定头失败")
}

// NewFrameParseExtensionFieldError 创建解析扩展字段失败错误
func NewFrameParseExtensionFieldError(err error) *Error {
	return newWithErr(ErrFrameParseExtensionField, err, "解析扩展字段失败")
}

// NewFrameDefragmentationError 创建分片反序列化失败错误
func NewFrameDefragmentationError(err error) *Error {
	return newWithErr(ErrFrameDefragmentation, err, "分片反序列化失败")
}

// ========================================
// 编解码错误构造函数
// ========================================

// NewCodecEncodeFailedError 创建编码失败错误
func NewCodecEncodeFailedError(op string, err error) *Error {
	return newWithOpErr(ErrCodecEncodeFailed, op, err, "编码失败")
}

// NewCodecDecodeFailedError 创建解码失败错误
func NewCodecDecodeFailedError(op string, err error) *Error {
	return newWithOpErr(ErrCodecDecodeFailed, op, err, "解码失败")
}

// NewCodecInvalidDataError 创建无效数据错误
func NewCodecInvalidDataError(op string, msg string) *Error {
	return newWithOp(ErrCodecInvalidData, op, "无效数据: %s", msg)
}

// NewCodecInvalidMessageError 创建无效消息错误
func NewCodecInvalidMessageError(msg string) *Error {
	return newBase(ErrCodecInvalidMessage, msg)
}

// NewCodecUnknownMessageTypeError 创建未知消息类型错误
func NewCodecUnknownMessageTypeError(msgType int) *Error {
	return newBase(ErrCodecUnknownMessageType, "未知消息类型: %d", msgType)
}

// ========================================
// Encryption 模块错误构造函数
// ========================================

// NewEncryptionKeySizeError 创建密钥大小错误
func NewEncryptionKeySizeError(expected int, actual int) *Error {
	return newBase(ErrEncryptionKeySize, "AES-256 密钥必须是%d字节，实际为%d字节", expected, actual)
}

// NewEncryptionCiphertextSizeError 创建密文大小错误
func NewEncryptionCiphertextSizeError(msg string) *Error {
	return newBase(ErrEncryptionCiphertextSize, msg)
}

// NewEncryptionPadSizeError 创建填充大小错误
func NewEncryptionPadSizeError(msg string) *Error {
	return newBase(ErrEncryptionPadSize, msg)
}

// NewEncryptionEmptyDataError 创建空数据错误
func NewEncryptionEmptyDataError(msg string) *Error {
	return newBase(ErrEncryptionEmptyData, msg)
}

// ========================================
// Store 模块错误构造函数
// ========================================

// NewStoreDirectoryCreationError 创建目录创建失败错误
func NewStoreDirectoryCreationError(dir string, err error) *Error {
	return newWithErr(ErrStoreDirectoryCreation, err, "创建目录失败: %s", dir)
}

// NewStoreWALError 创建 WAL 操作失败错误
func NewStoreWALError(op string, err error) *Error {
	return newWithErr(ErrStoreWALOperation, err, "WAL %s 失败", op)
}

// NewStoreSnapshotError 创建快照操作失败错误
func NewStoreSnapshotError(op string, err error) *Error {
	return newWithErr(ErrStoreSnapshotOperation, err, "快照%s失败", op)
}

// NewStoreKeyValidationError 创建 Key 验证失败错误
func NewStoreKeyValidationError(msg string) *Error {
	return newBase(ErrStoreKeyValidation, msg)
}

// NewStoreInvalidParameterError 创建无效参数错误
func NewStoreInvalidParameterError(param string) *Error {
	return newBase(ErrStoreInvalidParameter, "无效参数: %s", param)
}

// ========================================
// Consensus 模块错误构造函数
// ========================================

// NewConsensusNilParameterError 创建参数为空错误
func NewConsensusNilParameterError(param string) *Error {
	return newBase(ErrConsensusNilParameter, "%s 不能为空", param)
}

// NewConsensusServiceStateError 创建服务状态错误
func NewConsensusServiceStateError(service, state string) *Error {
	return newBase(ErrConsensusServiceState, "%s%s", service, state)
}

// NewConsensusTransactionError 创建事务错误
func NewConsensusTransactionError(msg string, err error) *Error {
	return newWithErr(ErrConsensusTransaction, err, msg)
}

// NewConsensusOperationError 创建协议操作失败错误
func NewConsensusOperationError(op string, err error) *Error {
	return newWithErr(ErrConsensusOperation, err, "%s 失败", op)
}

// NewConsensusTimeoutError 创建超时错误
func NewConsensusTimeoutError(op string) *Error {
	return newBase(ErrConsensusTimeout, "%s 超时", op)
}

// NewConsensusUnknownOperationError 创建未知操作类型错误
func NewConsensusUnknownOperationError(opType string) *Error {
	return newBase(ErrConsensusUnknownOperation, "未知操作类型: %s", opType)
}

// ========================================
// Cluster 模块错误构造函数
// ========================================

// NewClusterNilParameterError 创建参数为空错误
func NewClusterNilParameterError(param string) *Error {
	return newBase(ErrClusterNilParameter, "%s 不能为空", param)
}

// NewClusterServiceStateError 创建服务状态错误
func NewClusterServiceStateError(service, state string) *Error {
	return newBase(ErrClusterServiceState, "%s%s", service, state)
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
	return newWithErr(ErrClusterCoordinator, err, msg)
}

// NewClusterTreeManagementError 创建树结构管理错误
func NewClusterTreeManagementError(msg string) *Error {
	return newBase(ErrClusterTreeManagement, msg)
}

// NewClusterElectionError 创建选举错误
func NewClusterElectionError(msg string, err error) *Error {
	return newWithErr(ErrClusterElection, err, msg)
}

// NewClusterFailureDetectionError 创建故障检测错误
func NewClusterFailureDetectionError(msg string, err error) *Error {
	return newWithErr(ErrClusterFailureDetection, err, msg)
}

// NewClusterNodeNotFoundError 创建节点不存在错误
func NewClusterNodeNotFoundError(nodeID string) *Error {
	return newBase(ErrClusterNodeNotFound, "节点不存在: %s", nodeID)
}

// ========================================
// PR-033: Host 管理错误构造函数
// ========================================

// NewClusterHostIDRequiredError 创建 HostID 必填错误
func NewClusterHostIDRequiredError() *Error {
	return newBase(ErrClusterHostIDRequired, "HostID is required")
}

// NewClusterHostnameRequiredError 创建 Hostname 必填错误
func NewClusterHostnameRequiredError() *Error {
	return newBase(ErrClusterHostnameRequired, "Hostname is required")
}

// NewClusterInvalidNodeIDConstraintsError 创建无效的 NodeID 约束错误
func NewClusterInvalidNodeIDConstraintsError(err error) *Error {
	return newWithErr(ErrClusterInvalidNodeIDConstraints, err, "invalid NodeID constraints")
}

// NewClusterHostMarshalFailedError 创建 Host 序列化失败错误
func NewClusterHostMarshalFailedError(err error) *Error {
	return newWithErr(ErrClusterHostMarshalFailed, err, "failed to marshal host")
}

// NewClusterHostSaveFailedError 创建 Host 保存失败错误
func NewClusterHostSaveFailedError(err error) *Error {
	return newWithErr(ErrClusterHostSaveFailed, err, "failed to save host to MVStore")
}

// NewClusterHostNotFoundError 创建 Host 不存在错误
func NewClusterHostNotFoundError(hostID string) *Error {
	return newBase(ErrClusterHostNotFound, "host not found: %s", hostID)
}

// NewClusterHostUnmarshalFailedError 创建 Host 反序列化失败错误
func NewClusterHostUnmarshalFailedError(err error) *Error {
	return newWithErr(ErrClusterHostUnmarshalFailed, err, "failed to unmarshal host")
}

// NewClusterHostDeleteFailedError 创建 Host 删除失败错误
func NewClusterHostDeleteFailedError(err error) *Error {
	return newWithErr(ErrClusterHostDeleteFailed, err, "failed to delete host from MVStore")
}

// NewClusterHostListFailedError 创建 Host 列表获取失败错误
func NewClusterHostListFailedError(err error) *Error {
	return newWithErr(ErrClusterHostListFailed, err, "failed to list hosts")
}

// ========================================
// PR-033: Port Allocator 错误构造函数
// ========================================

// NewClusterPortAllocationNotFoundError 创建端口分配不存在错误
func NewClusterPortAllocationNotFoundError(hostID string, err error) *Error {
	return newWithErr(ErrClusterPortAllocationNotFound, err, "port allocation not found for host %s", hostID)
}

// NewClusterPortConflictCheckFailedError 创建端口冲突检查失败错误
func NewClusterPortConflictCheckFailedError(err error) *Error {
	return newWithErr(ErrClusterPortConflictCheckFailed, err, "failed to check port conflict")
}

// NewClusterPortAllocationSaveFailedError 创建端口分配保存失败错误
func NewClusterPortAllocationSaveFailedError(err error) *Error {
	return newWithErr(ErrClusterPortAllocationSaveFailed, err, "failed to save port allocation")
}

// NewClusterPortAllocationUnmarshalFailedError 创建端口分配反序列化失败错误
func NewClusterPortAllocationUnmarshalFailedError(err error) *Error {
	return newWithErr(ErrClusterPortAllocationUnmarshalFailed, err, "failed to unmarshal port allocation")
}

// NewClusterPortAllocationListFailedError 创建端口分配列表获取失败错误
func NewClusterPortAllocationListFailedError(err error) *Error {
	return newWithErr(ErrClusterPortAllocationListFailed, err, "failed to list port allocations")
}

// NewClusterPortAllocationMarshalFailedError 创建端口分配序列化失败错误
func NewClusterPortAllocationMarshalFailedError(err error) *Error {
	return newWithErr(ErrClusterPortAllocationMarshalFailed, err, "failed to marshal port allocation")
}

// NewClusterPortReleaseFailedError 创建端口释放失败错误
func NewClusterPortReleaseFailedError(err error) *Error {
	return newWithErr(ErrClusterPortReleaseFailed, err, "failed to release port")
}

// ========================================
// PR-033: Failure Detector 错误构造函数
// ========================================

// NewClusterTCPProbeFailedError 创建 TCP 探测失败错误
func NewClusterTCPProbeFailedError(err error) *Error {
	return newWithErr(ErrClusterTCPProbeFailed, err, "TCP probe failed")
}

// NewClusterNoProbeResultError 创建无探测结果错误
func NewClusterNoProbeResultError(hostID string) *Error {
	return newBase(ErrClusterNoProbeResult, "no probe result for host: %s", hostID)
}

// ========================================
// Transport 模块错误构造函数
// ========================================

// NewTransportConnectionError 创建连接错误
func NewTransportConnectionError(op, addr string, err error) *Error {
	return newWithErr(ErrTransportConnection, err, "%s %s 失败", op, addr)
}

// NewTransportStateError 创建传输状态错误
func NewTransportStateError(state string) *Error {
	return newBase(ErrTransportState, "传输层%s", state)
}

// NewTransportTimeoutError 创建传输超时错误
func NewTransportTimeoutError(op string) *Error {
	return newBase(ErrTransportTimeout, "%s 超时", op)
}

// NewTransportSendError 创建发送失败错误
func NewTransportSendError(err error) *Error {
	return newWithErr(ErrTransportSend, err, "发送消息失败")
}

// NewTransportHopCountExpiredError 创建 Hop Count 过期错误
func NewTransportHopCountExpiredError() *Error {
	return newBase(ErrTransportHopCountExpired, "消息已过期（HopCount=0），不再转发")
}

// NewTransportInvalidListenAddrError 创建监听地址无效错误
func NewTransportInvalidListenAddrError(addr, reason string, err error) *Error {
	msg := fmt.Sprintf("无效的监听地址 %q: %s", addr, reason)
	if err != nil {
		msg += ": " + err.Error()
	}
	return &Error{
		Code:    ErrTransportInvalidListenAddr,
		Message: msg,
		Err:     err,
	}
}

// NewTransportUnsupportedProtocolError 创建不支持的协议类型错误
func NewTransportUnsupportedProtocolError(protocol string) *Error {
	return newBase(ErrTransportUnsupportedProtocol, "不支持的协议类型: %s", protocol)
}

// ========================================
// Config 模块错误构造函数
// ========================================

// NewConfigLoadError 创建配置加载失败错误
func NewConfigLoadError(op string, err error) *Error {
	return newWithErr(ErrConfigLoad, err, "配置%s失败", op)
}

// NewConfigValidationError 创建配置验证失败错误
func NewConfigValidationError(field, msg string) *Error {
	return newBase(ErrConfigValidation, "配置验证失败: %s %s", field, msg)
}

// ========================================
// UUID/Clock 模块错误构造函数
// ========================================

// NewUUIDFormatError 创建 UUID 格式错误
func NewUUIDFormatError(msg string, err error) *Error {
	return newWithErr(ErrUUIDFormat, err, "UUID 格式错误: %s", msg)
}

// NewClockOperationError 创建时钟操作错误
func NewClockOperationError(msg string) *Error {
	return newBase(ErrClockOperation, msg)
}

// ========================================
// Compression 模块错误构造函数
// ========================================

// NewCompressionDecompressError 创建解压失败错误
func NewCompressionDecompressError(op string, err error) *Error {
	return newWithOpErr(ErrCompressionDecompress, op, err, "解压失败")
}

// NewCompressionCompressError 创建压缩失败错误
func NewCompressionCompressError(op string, err error) *Error {
	return newWithOpErr(ErrCompressionCompress, op, err, "压缩失败")
}

// ========================================
// 传输层错误分类（降级机制）
// ========================================

// ========================================
// 协议层错误（触发降级）
// ========================================

var (
	// ========================================
	// UDP 错误
	// ========================================

	// ErrUDPFragmentTimeout UDP 分片重组超时
	ErrUDPFragmentTimeout = errors.New("udp fragment reassembly timeout")

	// ErrUDPSendFailed UDP 发送系统调用失败
	ErrUDPSendFailed = errors.New("udp send system call failed")

	// ErrUDPReceiveFailed UDP 接收失败
	ErrUDPReceiveFailed = errors.New("udp receive failed")

	// ========================================
	// TCP 错误
	// ========================================

	// ErrTCPConnFailed TCP 连接失败
	ErrTCPConnFailed = errors.New("tcp connect failed")

	// ErrTCPSendTimeout TCP 发送超时
	ErrTCPSendTimeout = errors.New("tcp send timeout")

	// ========================================
	// 通用协议错误
	// ========================================
)

// ========================================
// 帧层错误（触发降级）
// ========================================

var (
	// ErrFrameTooLarge 帧过大
	ErrFrameTooLarge = errors.New("frame size exceeds maximum")

	// ErrInvalidFrameFormat 无效帧格式
	ErrInvalidFrameFormat = errors.New("invalid frame format")
)

// ========================================
// 业务层错误（不触发降级）
// ========================================

var (
	// ErrMsgTooLarge 消息大小超过限制
	ErrMsgTooLarge = errors.New("message size exceeds limit")

	// ErrInvalidAddr 地址格式无效
	ErrInvalidAddr = errors.New("invalid address format")

	// ErrCodecFailed 消息编解码失败
	ErrCodecFailed = errors.New("message codec failed")
)

// ========================================
// 错误分类工具函数
// ========================================

// IsProtocolError 判断是否为协议层错误
func IsProtocolError(err error) bool {
	if err == nil {
		return false
	}

	protocolErrors := []error{
		// UDP 错误
		ErrUDPFragmentTimeout,
		ErrUDPSendFailed,
		ErrUDPReceiveFailed,
		// TCP 错误
		ErrTCPConnFailed,
		ErrTCPSendTimeout,
		// 帧错误
		ErrFrameTooLarge,
		ErrInvalidFrameFormat,
	}

	for _, protoErr := range protocolErrors {
		if errors.Is(err, protoErr) {
			return true
		}
	}

	return false
}

// IsBusinessError 判断是否为业务层错误
func IsBusinessError(err error) bool {
	if err == nil {
		return false
	}

	businessErrors := []error{
		ErrMsgTooLarge,
		ErrInvalidAddr,
		ErrCodecFailed,
	}

	for _, bizErr := range businessErrors {
		if errors.Is(err, bizErr) {
			return true
		}
	}

	return false
}

// ========================================
// RPC 错误分类体系
// ========================================

// ========================================
// RPC 错误类型枚举
// ========================================

// RPCErrorType RPC 错误类型
type RPCErrorType int

const (
	// RPCErrorTypeTimeout 超时错误（可重试）
	RPCErrorTypeTimeout RPCErrorType = iota

	// RPCErrorTypeNetwork 网络错误（可重试）
	RPCErrorTypeNetwork

	// RPCErrorTypeCodec 编解码错误（不可重试）
	RPCErrorTypeCodec

	// RPCErrorTypeProtocol 协议错误（不可重试）
	RPCErrorTypeProtocol

	// RPCErrorTypeServer 服务端错误（部分可重试）
	RPCErrorTypeServer

	// RPCErrorTypeApplication 业务逻辑错误（不可重试）
	RPCErrorTypeApplication

	// RPCErrorTypeSystem 系统错误（不可重试）
	RPCErrorTypeSystem
)

// String 返回错误类型的字符串表示
func (t RPCErrorType) String() string {
	switch t {
	case RPCErrorTypeTimeout:
		return "TIMEOUT"
	case RPCErrorTypeNetwork:
		return "NETWORK"
	case RPCErrorTypeCodec:
		return "CODEC"
	case RPCErrorTypeProtocol:
		return "PROTOCOL"
	case RPCErrorTypeServer:
		return "SERVER"
	case RPCErrorTypeApplication:
		return "APPLICATION"
	case RPCErrorTypeSystem:
		return "SYSTEM"
	default:
		return "UNKNOWN"
	}
}

// ========================================
// RPCError 结构化错误定义
// ========================================

// RPCError RPC 结构化错误
type RPCError struct {
	Code       string       // 错误码（如 "RPC_REQUEST_TIMEOUT"）
	Message    string       // 错误消息（人类可读）
	Type       RPCErrorType // 错误类型
	Retryable  bool         // 是否可重试
	Cause      error        // 原始错误（可选）
	Timestamp  time.Time    // 错误发生时间
	RequestID  string       // 请求ID（CorrelationID，可选）
	TargetAddr string       // 目标地址（可选）
}

// Error 实现 error 接口
func (e *RPCError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap 返回原始错误（支持 errors.Unwrap）
func (e *RPCError) Unwrap() error {
	return e.Cause
}

// Is 实现 errors.Is 接口（支持错误类型匹配）
func (e *RPCError) Is(target error) bool {
	t, ok := target.(*RPCError)
	if !ok {
		return false
	}
	return e.Code == t.Code
}

// ========================================
// RPC 错误类型定义（集中管理）
// ========================================

var (
	// === 客户端错误 ===

	// ErrRPCRequestTimeout RPC 请求超时
	// 场景：Call() 方法在指定时间内未收到响应
	// 可重试：是（网络抖动或服务端繁忙）
	ErrRPCRequestTimeout = &RPCError{
		Code:      "RPC_REQUEST_TIMEOUT",
		Message:   "RPC 请求超时",
		Type:      RPCErrorTypeTimeout,
		Retryable: true,
	}

	// ErrRPCResponseTimeout RPC 响应超时（等待响应通道超时）
	// 场景：respCh/errCh 在指定时间内无数据
	// 可重试：是（可能服务端处理慢）
	ErrRPCResponseTimeout = &RPCError{
		Code:      "RPC_RESPONSE_TIMEOUT",
		Message:   "RPC 响应超时",
		Type:      RPCErrorTypeTimeout,
		Retryable: true,
	}

	// ErrRPCNetworkError RPC 网络错误
	// 场景：TCP/UDP 连接失败、Send() 失败、连接中断
	// 可重试：是（网络临时故障）
	ErrRPCNetworkError = &RPCError{
		Code:      "RPC_NETWORK_ERROR",
		Message:   "RPC 网络错误",
		Type:      RPCErrorTypeNetwork,
		Retryable: true,
	}

	// ErrRPCTransportClosed RPC Transport 已关闭
	// 场景：调用 Stop() 后继续调用 Call()
	// 可重试：否（需要重新创建 Transport）
	ErrRPCTransportClosed = &RPCError{
		Code:      "RPC_TRANSPORT_CLOSED",
		Message:   "RPC Transport 已关闭",
		Type:      RPCErrorTypeSystem,
		Retryable: false,
	}

	// === 编解码错误 ===

	// ErrRPCCodecError RPC 编解码错误
	// 场景：Message 序列化/反序列化失败
	// 可重试：否（数据格式错误，重试无意义）
	ErrRPCCodecError = &RPCError{
		Code:      "RPC_CODEC_ERROR",
		Message:   "RPC 编解码错误",
		Type:      RPCErrorTypeCodec,
		Retryable: false,
	}

	// ErrRPCInvalidMessage RPC 消息格式无效
	// 场景：响应消息类型不匹配、CorrelationID 不存在
	// 可重试：否（协议错误，重试无意义）
	ErrRPCInvalidMessage = &RPCError{
		Code:      "RPC_INVALID_MESSAGE",
		Message:   "RPC 消息格式无效",
		Type:      RPCErrorTypeProtocol,
		Retryable: false,
	}

	// === 服务端错误 ===

	// ErrRPCServerPanic RPC 服务端异常（panic）
	// 场景：服务端 handler 触发 panic
	// 可重试：是（服务端重启后可能恢复）
	ErrRPCServerPanic = &RPCError{
		Code:      "RPC_SERVER_PANIC",
		Message:   "RPC 服务端异常",
		Type:      RPCErrorTypeServer,
		Retryable: true,
	}

	// ErrRPCServerError RPC 服务端业务错误
	// 场景：服务端 handler 返回业务错误（如数据不存在、权限不足）
	// 可重试：否（业务逻辑错误，重试无意义）
	ErrRPCServerError = &RPCError{
		Code:      "RPC_SERVER_ERROR",
		Message:   "RPC 服务端业务错误",
		Type:      RPCErrorTypeApplication,
		Retryable: false,
	}

	// === 上下文错误 ===

	// ErrRPCContextCanceled RPC 请求被取消
	// 场景：context.WithCancel() 被调用
	// 可重试：否（用户主动取消）
	ErrRPCContextCanceled = &RPCError{
		Code:      "RPC_CONTEXT_CANCELED",
		Message:   "RPC 请求被取消",
		Type:      RPCErrorTypeSystem,
		Retryable: false,
	}

	// ErrRPCContextDeadlineExceeded RPC 请求超时（context）
	// 场景：context.WithTimeout() 超时
	// 可重试：是（超时后可重试）
	ErrRPCContextDeadlineExceeded = &RPCError{
		Code:      "RPC_CONTEXT_DEADLINE_EXCEEDED",
		Message:   "RPC 请求超时（context）",
		Type:      RPCErrorTypeTimeout,
		Retryable: true,
	}
)

// ========================================
// RPC 错误构造函数（便捷创建错误实例）
// ========================================

// newRPCBase 创建基础 RPC 错误（内部辅助函数）
func newRPCBase(code string, errType RPCErrorType, retryable bool, format string, args ...any) *RPCError {
	return &RPCError{
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Type:      errType,
		Retryable: retryable,
		Timestamp: time.Now(),
	}
}

// newRPCWithCause 创建带原因的 RPC 错误（内部辅助函数）
func newRPCWithCause(code string, errType RPCErrorType, retryable bool, cause error, format string, args ...any) *RPCError {
	return &RPCError{
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Type:      errType,
		Retryable: retryable,
		Cause:     cause,
		Timestamp: time.Now(),
	}
}

// NewRPCRequestTimeout 创建请求超时错误
func NewRPCRequestTimeout(timeout time.Duration, addr string) *RPCError {
	return &RPCError{
		Code:       "RPC_REQUEST_TIMEOUT",
		Message:    fmt.Sprintf("RPC 请求超时（超时时间：%v）", timeout),
		Type:       RPCErrorTypeTimeout,
		Retryable:  true,
		Timestamp:  time.Now(),
		TargetAddr: addr,
	}
}

// NewRPCNetworkError 创建网络错误
func NewRPCNetworkError(addr string, cause error) *RPCError {
	return &RPCError{
		Code:       "RPC_NETWORK_ERROR",
		Message:    fmt.Sprintf("RPC 网络错误（地址：%s）", addr),
		Type:       RPCErrorTypeNetwork,
		Retryable:  true,
		Cause:      cause,
		Timestamp:  time.Now(),
		TargetAddr: addr,
	}
}

// NewRPCCodecError 创建编解码错误
func NewRPCCodecError(msgType string, cause error) *RPCError {
	return newRPCWithCause("RPC_CODEC_ERROR", RPCErrorTypeCodec, false, cause, "RPC 编解码错误（消息类型：%s）", msgType)
}

// NewRPCServerError 创建服务端错误
func NewRPCServerError(addr string, cause error) *RPCError {
	return &RPCError{
		Code:       "RPC_SERVER_ERROR",
		Message:    fmt.Sprintf("RPC 服务端错误（地址：%s）", addr),
		Type:       RPCErrorTypeApplication,
		Retryable:  false,
		Cause:      cause,
		Timestamp:  time.Now(),
		TargetAddr: addr,
	}
}

// NewRPCInvalidMessage 创建无效消息错误
func NewRPCInvalidMessage(reason string) *RPCError {
	return newRPCBase("RPC_INVALID_MESSAGE", RPCErrorTypeProtocol, false, "RPC 消息格式无效：%s", reason)
}

// NewRPCContextCanceled 创建上下文取消错误
func NewRPCContextCanceled(cause error) *RPCError {
	return newRPCWithCause("RPC_CONTEXT_CANCELED", RPCErrorTypeSystem, false, cause, "RPC 请求被取消")
}

// ========================================
// RPC 错误判断工具函数
// ========================================

// IsRPCRetryable 判断错误是否可重试
//
// 使用场景：业务层决定是否重试 RPC 调用
//
// 可重试错误类型：
//   - 超时错误（网络抖动或服务端繁忙）
//   - 网络错误（连接失败、Send() 失败）
//   - 服务端 panic（服务端重启后可能恢复）
//
// 不可重试错误类型：
//   - 编解码错误（数据格式错误）
//   - 业务逻辑错误（数据不存在、权限不足）
//   - 系统错误（Transport 已关闭、用户主动取消）
func IsRPCRetryable(err error) bool {
	if err == nil {
		return false
	}

	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Retryable
	}

	// 兼容标准库 net.Error
	var netErr interface {
		Timeout() bool
		Temporary() bool
	}
	if errors.As(err, &netErr) {
		// 网络错误可重试（超时、连接拒绝）
		if netErr.Timeout() {
			return true
		}
		return true
	}

	return false
}

// IsRPCTimeout 判断是否是超时错误
func IsRPCTimeout(err error) bool {
	if err == nil {
		return false
	}

	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Type == RPCErrorTypeTimeout
	}

	// 兼容 context.DeadlineExceeded
	return errors.Is(err, context.DeadlineExceeded)
}

// IsRPCNetworkError 判断是否是网络错误
func IsRPCNetworkError(err error) bool {
	if err == nil {
		return false
	}

	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Type == RPCErrorTypeNetwork
	}

	// 兼容标准库 net.Error
	var netErr interface {
		Timeout() bool
		Temporary() bool
	}
	return errors.As(err, &netErr)
}

// IsRPCApplicationError 判断是否是业务逻辑错误
func IsRPCApplicationError(err error) bool {
	if err == nil {
		return false
	}

	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Type == RPCErrorTypeApplication
	}

	return false
}

// IsRPCSystemError 判断是否是系统错误
func IsRPCSystemError(err error) bool {
	if err == nil {
		return false
	}

	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Type == RPCErrorTypeSystem
	}

	return errors.Is(err, context.Canceled)
}

// GetRPCErrorCode 获取错误码
func GetRPCErrorCode(err error) string {
	if err == nil {
		return ""
	}

	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Code
	}

	return "UNKNOWN"
}

// GetRPCErrorType 获取错误类型
func GetRPCErrorType(err error) RPCErrorType {
	if err == nil {
		return RPCErrorTypeSystem
	}

	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Type
	}

	return RPCErrorTypeSystem
}

// ========================================
// RPC 重试策略定义
// ========================================

// RPCRetryPolicy 重试策略配置
type RPCRetryPolicy struct {
	MaxRetries     int           // 最大重试次数
	InitialDelay   time.Duration // 初始延迟（1s）
	MaxDelay       time.Duration // 最大延迟（30s）
	BackoffRate    float64       // 退避率（2.0 表示指数退避）
	MaxElapsedTime time.Duration // 最大重试总时长（0 表示不限制）
}

// DefaultRPCRetryPolicy 默认重试策略
var DefaultRPCRetryPolicy = &RPCRetryPolicy{
	MaxRetries:     3,
	InitialDelay:   1 * time.Second,
	MaxDelay:       30 * time.Second,
	BackoffRate:    2.0,
	MaxElapsedTime: 0, // 不限制总时长
}
