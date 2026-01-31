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

	// ErrClusterPortExhausted 端口耗尽（PR-034 新增）
	ErrClusterPortExhausted

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

// NewClusterPortExhaustedError 创建端口耗尽错误（PR-034 新增）
func NewClusterPortExhaustedError() *Error {
	// P1-1 修复：硬编码端口范围，避免跨包访问常量
	// 端口范围：9000-32767（与 cluster/port_allocator.go 中的定义一致）
	return newBase(ErrClusterPortExhausted, "端口资源耗尽（范围: 9000-32767）")
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

// ========================================
// 新增错误码（从 fmt.Errorf 迁移）
// ========================================

const (
	// ========================================
	// Snapshot 错误码（新增详细错误）
	// ========================================

	// ErrSnapshotOpenFile 打开 Snapshot 文件失败
	ErrSnapshotOpenFile ErrorCode = iota + 3200

	// ErrSnapshotReadHeader 读取文件头失败
	ErrSnapshotReadHeader

	// ErrSnapshotParseHeader 解析文件头失败
	ErrSnapshotParseHeader

	// ErrSnapshotInvalidMagic 无效的魔术字
	ErrSnapshotInvalidMagic

	// ErrSnapshotUnsupportedVersion 不支持的版本号
	ErrSnapshotUnsupportedVersion

	// ErrSnapshotInvalidCompression 无效的压缩算法类型
	ErrSnapshotInvalidCompression

	// ErrSnapshotCreateCompressor 创建压缩器失败
	ErrSnapshotCreateCompressor

	// ErrSnapshotLocateMetadata 定位元数据段失败
	ErrSnapshotLocateMetadata

	// ErrSnapshotReadMetadata 读取元数据段失败
	ErrSnapshotReadMetadata

	// ErrSnapshotDecompressMetadata 解压缩元数据失败
	ErrSnapshotDecompressMetadata

	// ErrSnapshotDeserializeMetadata 反序列化元数据失败
	ErrSnapshotDeserializeMetadata

	// ErrSnapshotLocateData 定位数据段失败
	ErrSnapshotLocateData

	// ErrSnapshotReadData 读取数据段失败
	ErrSnapshotReadData

	// ErrSnapshotDecompressData 解压缩数据失败
	ErrSnapshotDecompressData

	// ErrSnapshotDeserializeData 反序列化数据失败
	ErrSnapshotDeserializeData

	// ErrSnapshotGetFileInfo 获取文件信息失败
	ErrSnapshotGetFileInfo

	// ErrSnapshotLocateChecksum 定位校验和失败
	ErrSnapshotLocateChecksum

	// ErrSnapshotReadChecksum 读取校验和失败
	ErrSnapshotReadChecksum

	// ErrSnapshotResetFile 重置文件指针失败
	ErrSnapshotResetFile

	// ErrSnapshotReadDataForChecksum 读取数据用于校验和失败
	ErrSnapshotReadDataForChecksum

	// ErrSnapshotCalculateChecksum 计算校验和失败
	ErrSnapshotCalculateChecksum

	// ErrSnapshotChecksumMismatch 校验和不匹配
	ErrSnapshotChecksumMismatch

	// ErrSnapshotCreateDir 创建 Snapshot 目录失败
	ErrSnapshotCreateDir

	// ErrSnapshotCreateWriter 创建 Snapshot 写入器失败
	ErrSnapshotCreateWriter

	// ErrSnapshotSerializeMetadata 序列化元数据失败
	ErrSnapshotSerializeMetadata

	// ErrSnapshotCompressMetadata 压缩元数据失败
	ErrSnapshotCompressMetadata

	// ErrSnapshotCacheMetadata 缓存元数据段失败
	ErrSnapshotCacheMetadata

	// ErrSnapshotSerializeData 序列化数据失败
	ErrSnapshotSerializeData

	// ErrSnapshotCompressData 压缩数据失败
	ErrSnapshotCompressData

	// ErrSnapshotCacheData 缓存数据段失败
	ErrSnapshotCacheData

	// ErrSnapshotFinalizeAlreadyCalled Finalize 已调用，不能重复调用
	ErrSnapshotFinalizeAlreadyCalled

	// ErrSnapshotWriteHeader 写入文件头失败
	ErrSnapshotWriteHeader

	// ErrSnapshotWriteMetadata 写入元数据段失败
	ErrSnapshotWriteMetadata

	// ErrSnapshotWriteData 写入数据段失败
	ErrSnapshotWriteData

	// ErrSnapshotSerializeHeader 序列化文件头失败
	ErrSnapshotSerializeHeader

	// ErrSnapshotCalculateHeaderHash 计算文件头哈希失败
	ErrSnapshotCalculateHeaderHash

	// ErrSnapshotCalculateMetadataHash 计算元数据段哈希失败
	ErrSnapshotCalculateMetadataHash

	// ErrSnapshotCalculateDataHash 计算数据段哈希失败
	ErrSnapshotCalculateDataHash

	// ErrSnapshotSyncData 同步数据失败
	ErrSnapshotSyncData

	// ErrSnapshotWriteChecksum 写入校验和失败
	ErrSnapshotWriteChecksum

	// ErrSnapshotFinalSync 最终同步失败
	ErrSnapshotFinalSync

	// ErrSnapshotCloseFile 关闭文件失败
	ErrSnapshotCloseFile

	// ErrSnapshotRenameFile 重命名 Snapshot 文件失败
	ErrSnapshotRenameFile

	// ErrSnapshotCreateReader 创建 Snapshot 读取器失败
	ErrSnapshotCreateReader

	// ErrSnapshotVerifyChecksum 验证校验和失败
	ErrSnapshotVerifyChecksum

	// ErrSnapshotReadMetadataSection 读取元数据段失败
	ErrSnapshotReadMetadataSection

	// ErrSnapshotReadDataSection 读取数据段失败
	ErrSnapshotReadDataSection

	// ErrSnapshotReadDir 读取 Snapshot 目录失败
	ErrSnapshotReadDir

	// ErrSnapshotNoSnapshotFile 没有找到 Snapshot 文件
	ErrSnapshotNoSnapshotFile

	// ErrSnapshotDeleteFile 删除 Snapshot 文件失败
	ErrSnapshotDeleteFile

	// ErrSnapshotInvalidFileName 无效的 Snapshot 文件名
	ErrSnapshotInvalidFileName

	// ErrSnapshotDirNotExist Snapshot 目录不存在
	ErrSnapshotDirNotExist

	// ErrSnapshotDirNotAccessible 无法访问 snapshot 目录
	ErrSnapshotDirNotAccessible

	// ErrSnapshotDirNotDirectory Snapshot 路径不是目录
	ErrSnapshotDirNotDirectory

	// ErrSnapshotDirNotWritable Snapshot 目录没有写权限
	ErrSnapshotDirNotWritable

	// ErrSnapshotGetSnapshotData 获取快照数据失败
	ErrSnapshotGetSnapshotData

	// ErrSnapshotCreateSnapshot 创建 Snapshot 失败
	ErrSnapshotCreateSnapshot

	// ErrSnapshotLoadSnapshot 加载 Snapshot 失败
	ErrSnapshotLoadSnapshot

	// ErrSnapshotMissingSnapshotData 缺少 snapshot_data 字段
	ErrSnapshotMissingSnapshotData

	// ========================================
	// Encryption 错误码（新增）
	// ========================================

	// ErrEncryptCreateGCMMode 创建 GCM 模式失败
	ErrEncryptCreateGCMMode

	// ErrEncryptDecryptOrAuthFailed 解密失败或认证标签验证失败
	ErrEncryptDecryptOrAuthFailed

	// ErrEncryptGenerateNonce 生成 Nonce 失败
	ErrEncryptGenerateNonce

	// ErrEncryptCreateCipher 创建 cipher 失败
	ErrEncryptCreateCipher

	// ErrEncryptCreateGCM 创建 GCM 失败
	ErrEncryptCreateGCM

	// ========================================
	// Daemon/Application 错误码（新增）
	// ========================================

	// ErrDaemonInitializeLogging 初始化日志失败
	ErrDaemonInitializeLogging

	// ErrDaemonLoadConfig 加载配置失败
	ErrDaemonLoadConfig

	// ErrDaemonCreateAppContext 创建应用上下文失败
	ErrDaemonCreateAppContext

	// ErrDaemonInitialize 初始化失败
	ErrDaemonInitialize

	// ErrDaemonStop 停止守护进程失败
	ErrDaemonStop

	// ErrDaemonShutdown 停止守护进程失败
	ErrDaemonShutdown

	// ErrDaemonConfigEmpty 配置不能为空
	ErrDaemonConfigEmpty

	// ErrDaemonConfigNil 配置不能为空
	ErrDaemonConfigNil

	// ErrDaemonParseListenAddr 解析监听地址失败
	ErrDaemonParseListenAddr

	// ErrDaemonGenerateNodeID 生成节点 ID 失败
	ErrDaemonGenerateNodeID

	// ErrDaemonCreateTCPTransport 创建 TCP 传输失败
	ErrDaemonCreateTCPTransport

	// ErrDaemonCreateUDPTransport 创建 UDP 传输失败
	ErrDaemonCreateUDPTransport

	// ErrDaemonStartTCPTransport 启动 TCP 传输失败
	ErrDaemonStartTCPTransport

	// ErrDaemonStartUDPTransport 启动 UDP 传输失败
	ErrDaemonStartUDPTransport

	// ErrDaemonCreateRPCClient 创建 RPC Client 失败
	ErrDaemonCreateRPCClient

	// ErrDaemonCreateRPCServer 创建 RPC Server 失败
	ErrDaemonCreateRPCServer

	// ErrDaemonStartRPCServer 启动 RPC Server 失败
	ErrDaemonStartRPCServer

	// ErrDaemonStartRPCClient 启动 RPC Client 失败
	ErrDaemonStartRPCClient

	// ErrDaemonCreateTreeCoordinator 创建 TreeCoordinator 失败
	ErrDaemonCreateTreeCoordinator

	// ErrDaemonStartTreeCoordinator 启动 TreeCoordinator 失败
	ErrDaemonStartTreeCoordinator

	// ErrDaemonStopTreeCoordinator 停止 TreeCoordinator 失败
	ErrDaemonStopTreeCoordinator

	// ErrDaemonStopRPCClient 停止 RPC Client 失败
	ErrDaemonStopRPCClient

	// ErrDaemonStopRPCServer 停止 RPC Server 失败
	ErrDaemonStopRPCServer

	// ErrDaemonStopTCPTransport 停止 TCP 传输层失败
	ErrDaemonStopTCPTransport

	// ErrDaemonStopUDPTransport 停止 UDP 传输层失败
	ErrDaemonStopUDPTransport

	// ErrDaemonStopWithErrors 停止守护进程时发生错误
	ErrDaemonStopWithErrors

	// ErrDaemonShutdownMultipleErrors 停止守护进程时发生多个错误
	ErrDaemonShutdownMultipleErrors

	// ErrDaemonListenAddrEmpty 监听地址不能为空
	ErrDaemonListenAddrEmpty

	// ErrDaemonParseAddr 解析地址失败
	ErrDaemonParseAddr

	// ErrDaemonSplitHostPort 分离主机端口失败
	ErrDaemonSplitHostPort

	// ErrDaemonInvalidPort 无效的端口号
	ErrDaemonInvalidPort

	// ErrDaemonMultipleHosts 配置中有多个 Host
	ErrDaemonMultipleHosts

	// ErrDaemonHostNotFound 未找到主机 ID
	ErrDaemonHostNotFound

	// ErrDaemonHostIDNotFound 未找到主机 ID
	ErrDaemonHostIDNotFound

	// ErrDaemonNoHosts 配置中没有 Host
	ErrDaemonNoHosts

	// ErrDaemonNodeIDNotFound 主机中未找到节点 ID
	ErrDaemonNodeIDNotFound

	// ErrDaemonNoNodesInHost 主机中没有 Node
	ErrDaemonNoNodesInHost

	// ErrDaemonNoNodes 主机中没有 Node
	ErrDaemonNoNodes

	// ========================================
	// CLI Command 错误码（新增）
	// ========================================

	// ErrCLIParameterEmpty 参数不能为空
	ErrCLIParameterEmpty

	// ErrCLIConnectDaemon 连接 Daemon 失败
	ErrCLIConnectDaemon

	// ErrCLIAddNode 添加节点失败
	ErrCLIAddNode

	// ErrCLINeedNodeID 需要指定节点 ID
	ErrCLINeedNodeID

	// ErrCLIDeleteNode 删除节点失败
	ErrCLIDeleteNode

	// ErrCLIQueryNodeList 查询节点列表失败
	ErrCLIQueryNodeList

	// ErrCLIResponseTypeError 响应类型错误
	ErrCLIResponseTypeError

	// ErrCLIFormatOutput 格式化输出失败
	ErrCLIFormatOutput

	// ErrCLIUnsupportedOutputFormat 不支持的输出格式
	ErrCLIUnsupportedOutputFormat

	// ErrCLIQueryClusterStatus 查询集群状态失败
	ErrCLIQueryClusterStatus

	// ErrCLIQueryClusterTopology 查询集群拓扑失败
	ErrCLIQueryClusterTopology

	// ErrCLIQueryClusterInfo 查询集群信息失败
	ErrCLIQueryClusterInfo

	// ErrCLIHealthCheck 健康检查失败
	ErrCLIHealthCheck

	// ErrCLISerialize JSON 序列化失败
	ErrCLISerialize

	// ErrCLINotImplemented YAML 格式暂未实现
	ErrCLINotImplemented

	// ========================================
	// Network Utility 错误码（新增）
	// ========================================

	// ErrNetInvalidEnvType 无效的环境类型
	ErrNetInvalidEnvType

	// ErrNetInvalidIP 无效的 IP 地址
	ErrNetInvalidIP

	// ErrNetGetPrivateIP 获取私网 IP 失败
	ErrNetGetPrivateIP

	// ErrNetNoPrivateIPFound 未找到可用的私网 IP
	ErrNetNoPrivateIPFound

	// ErrNetGetInterfaces 获取网络接口失败
	ErrNetGetInterfaces

	// ErrNetNoAvailablePort 未找到可用端口
	ErrNetNoAvailablePort

	// ErrNetIPMismatch 用户指定 IP 与自动绑定 IP 不匹配
	ErrNetIPMismatch

	// ========================================
	// Tree Coordinator 错误码（新增）
	// ========================================

	// ErrTreeCoordinatorAddrEmpty 地址字符串不能为空
	ErrTreeCoordinatorAddrEmpty

	// ErrTreeCoordinatorInvalidIPFSAddr 无效的 IPFS 地址格式
	ErrTreeCoordinatorInvalidIPFSAddr

	// ErrTreeCoordinatorInvalidProtocol 无效的协议格式
	ErrTreeCoordinatorInvalidProtocol

	// ErrTreeCoordinatorInvalidPort 无效的端口号
	ErrTreeCoordinatorInvalidPort

	// ErrTreeCoordinatorUnsupportedProtocol 不支持的协议类型
	ErrTreeCoordinatorUnsupportedProtocol

	// ErrTreeCoordinatorInvalidAddr 无效的地址格式
	ErrTreeCoordinatorInvalidAddr

	// ErrTreeCoordinatorSendJoinRequest 发送加入请求失败
	ErrTreeCoordinatorSendJoinRequest

	// ErrTreeCoordinatorNoSuitableParent 没有找到合适的父节点
	ErrTreeCoordinatorNoSuitableParent

	// ErrTreeCoordinatorCoordinatorNotInitialized coordinator not initialized
	ErrTreeCoordinatorCoordinatorNotInitialized

	// ErrTreeCoordinatorUnsupportedMessageType unsupported message type
	ErrTreeCoordinatorUnsupportedMessageType

	// ErrTreeCoordinatorFailedToAddChild failed to add child
	ErrTreeCoordinatorFailedToAddChild

	// ErrTreeCoordinatorFailedToRemoveChild failed to remove child
	ErrTreeCoordinatorFailedToRemoveChild

	// ErrTreeCoordinatorNodeNotFound 节点不存在
	ErrTreeCoordinatorNodeNotFound

	// ErrTreeCoordinatorTCPPortOutOfRange TCP端口范围错误
	ErrTreeCoordinatorTCPPortOutOfRange

	// ErrTreeCoordinatorUDPPortOutOfRange UDP端口范围错误
	ErrTreeCoordinatorUDPPortOutOfRange

	// ErrTreeCoordinatorUDPPortMustBeTCPPlusOne UDP端口必须等于TCP端口+1
	ErrTreeCoordinatorUDPPortMustBeTCPPlusOne

	// ErrTreeCoordinatorAtLeastOnePortRequired 至少需要一个端口
	ErrTreeCoordinatorAtLeastOnePortRequired

	// ErrTreeCoordinatorNodeIDRequired NodeID必填
	ErrTreeCoordinatorNodeIDRequired

	// ErrTreeCoordinatorHostIDRequired HostID必填
	ErrTreeCoordinatorHostIDRequired

	// ErrTreeCoordinatorInvalidNodeAddr 无效的节点地址
	ErrTreeCoordinatorInvalidNodeAddr

	// ErrTreeCoordinatorInvalidNodeRole 无效的节点角色
	ErrTreeCoordinatorInvalidNodeRole

	// ErrTreeCoordinatorLeafNodeIDRequired LeafNodeID必填
	ErrTreeCoordinatorLeafNodeIDRequired

	// ErrTreeCoordinatorParentNodeIDRequired ParentNodeID必填
	ErrTreeCoordinatorParentNodeIDRequired

	// ErrTreeCoordinatorLeafOnlyHostShouldNotHaveParentNodeID LeafOnly Host不应有ParentNodeID
	ErrTreeCoordinatorLeafOnlyHostShouldNotHaveParentNodeID

	// ErrTreeCoordinatorLeafOnlyHostShouldNotHaveParentStandbyNodeID LeafOnly Host不应有ParentStandbyNodeID
	ErrTreeCoordinatorLeafOnlyHostShouldNotHaveParentStandbyNodeID

	// ErrTreeCoordinatorParentStandbyNodeIDRequired ParentStandbyNodeID必填
	ErrTreeCoordinatorParentStandbyNodeIDRequired

	// ErrTreeCoordinatorParentStandbyNodeIDMustBeDifferent ParentStandbyNodeID必须与ParentNodeID不同
	ErrTreeCoordinatorParentStandbyNodeIDMustBeDifferent

	// ErrTreeCoordinatorInvalidHostRole 无效的主机角色
	ErrTreeCoordinatorInvalidHostRole

	// ErrTreeCoordinatorSendGossipMessageError 发送Gossip消息失败
	ErrTreeCoordinatorSendGossipMessageError

	// ========================================
	// Identity/NodeID 错误码（新增）
	// ========================================

	// ErrIdentityNoEnabledPort 至少需要启用一个端口
	ErrIdentityNoEnabledPort

	// ErrIdentityInvalidTCPPort TCP 端口无效
	ErrIdentityInvalidTCPPort

	// ErrIdentityInvalidUDPPort UDP 端口无效
	ErrIdentityInvalidUDPPort

	// ========================================
	// Config 详细验证错误码（新增）
	// ========================================

	// ErrConfigReadFile 读取配置文件失败
	ErrConfigReadFile

	// ErrConfigParseFile 解析配置文件失败
	ErrConfigParseFile

	// ErrConfigValidateFile 配置验证失败
	ErrConfigValidateFile

	// ErrConfigClusterNameEmpty cluster.name 不能为空
	ErrConfigClusterNameEmpty

	// ErrConfigBaseDirEmpty cluster.base_dir 不能为空
	ErrConfigBaseDirEmpty

	// ErrConfigHostsEmpty cluster.hosts 不能为空
	ErrConfigHostsEmpty

	// ErrConfigHostIDEmpty cluster.hosts[i].host_id 不能为空
	ErrConfigHostIDEmpty

	// ErrConfigSeedNodeEmpty cluster.hosts[i].seed_node 不能为空
	ErrConfigSeedNodeEmpty

	// ErrConfigNodesEmpty cluster.hosts[i].nodes 不能为空
	ErrConfigNodesEmpty

	// ErrConfigNodeIDEmpty cluster.hosts[i].nodes[j].node_id 不能为空
	ErrConfigNodeIDEmpty

	// ErrConfigNodeAddrTCPEmpty cluster.hosts[i].nodes[j].node_addr_tcp 不能为空
	ErrConfigNodeAddrTCPEmpty

	// ErrConfigNodeAddrUDPEmpty cluster.hosts[i].nodes[j].node_addr_udp 不能为空
	ErrConfigNodeAddrUDPEmpty

	// ErrConfigNodeAddrTCPInvalidFormat cluster.hosts[i].nodes[j].node_addr_tcp 格式错误
	ErrConfigNodeAddrTCPInvalidFormat

	// ErrConfigNodeAddrUDPInvalidFormat cluster.hosts[i].nodes[j].node_addr_udp 格式错误
	ErrConfigNodeAddrUDPInvalidFormat

	// ErrConfigGossipIntervalInvalid gossip_interval 不能小于 1 秒
	ErrConfigGossipIntervalInvalid

	// ErrConfigQuorumTimeoutInvalid quorum_timeout 不能小于 1 秒
	ErrConfigQuorumTimeoutInvalid

	// ErrConfigChangeLogSizeInvalid change_log_size 不能小于 100
	ErrConfigChangeLogSizeInvalid

	// ErrConfigFlushIntervalInvalid flush_interval 不能小于 1 秒
	ErrConfigFlushIntervalInvalid

	// ErrConfigListenAddrEmpty listen_addr 不能为空
	ErrConfigListenAddrEmpty

	// ErrConfigTransportTypeEmpty transport_type 不能为空
	ErrConfigTransportTypeEmpty

	// ErrConfigMessagePackTypeEmpty message_pack_type 不能为空
	ErrConfigMessagePackTypeEmpty

	// ErrConfigMaxMessageSizeInvalid max_message_size 不能小于 1024 字节
	ErrConfigMaxMessageSizeInvalid

	// ErrConfigConnectTimeoutInvalid connect_timeout 不能小于 1 秒
	ErrConfigConnectTimeoutInvalid

	// ErrConfigInvalidLogLevel 无效的日志级别
	ErrConfigInvalidLogLevel

	// ErrConfigInvalidLogFormat 无效的日志格式
	ErrConfigInvalidLogFormat

	// ErrConfigInvalidLogOutput 无效的日志输出
	ErrConfigInvalidLogOutput

	// ErrConfigLogFileRequired 日志输出为 file 时，必须指定 file 路径
	ErrConfigLogFileRequired

	// ErrConfigMaxDriftInvalid max_drift 不能为负数
	ErrConfigMaxDriftInvalid

	// ErrConfigSyncIntervalInvalid sync_interval 不能小于 1 秒
	ErrConfigSyncIntervalInvalid

	// ErrConfigCreateDir 创建配置目录失败
	ErrConfigCreateDir

	// ErrConfigSerialize 序列化配置失败
	ErrConfigSerialize

	// ErrConfigWriteFile 写入配置文件失败
	ErrConfigWriteFile

	// ========================================
	// E2E 测试错误码（PR-037: 新增）
	// ========================================

	// ErrE2EConfigFileNotFound 找不到配置文件
	ErrE2EConfigFileNotFound

	// ErrE2EConfigFileReadError 读取配置文件失败
	ErrE2EConfigFileReadError

	// ErrE2EConfigFileParseError 解析配置文件失败
	ErrE2EConfigFileParseError

	// ErrE2EInvalidMultiAddrFormat 无效的 multiaddr 格式
	ErrE2EInvalidMultiAddrFormat

	// ErrE2ENilResponse 响应为 nil
	ErrE2ENilResponse

	// ErrE2EWrongResponseType 响应类型错误
	ErrE2EWrongResponseType
)

// ========================================
// Snapshot 错误构造函数（新增详细错误）
// ========================================

// NewSnapshotOpenFileError 创建打开 Snapshot 文件失败错误
func NewSnapshotOpenFileError(err error) *Error {
	return newWithErr(ErrSnapshotOpenFile, err, "打开 Snapshot 文件失败")
}

// NewSnapshotReadHeaderError 创建读取文件头失败错误
func NewSnapshotReadHeaderError(err error) *Error {
	return newWithErr(ErrSnapshotReadHeader, err, "读取文件头失败")
}

// NewSnapshotParseHeaderError 创建解析文件头失败错误
func NewSnapshotParseHeaderError(err error) *Error {
	return newWithErr(ErrSnapshotParseHeader, err, "解析文件头失败")
}

// NewSnapshotInvalidMagicError 创建无效的魔术字错误
func NewSnapshotInvalidMagicError(expected, actual string) *Error {
	return newBase(ErrSnapshotInvalidMagic, "无效的魔术字: 期望 %s, 实际 %s", expected, actual)
}

// NewSnapshotUnsupportedVersionError 创建不支持的版本号错误
func NewSnapshotUnsupportedVersionError(version, supported uint32) *Error {
	return newBase(ErrSnapshotUnsupportedVersion, "不支持的版本号: %d (当前支持: %d)", version, supported)
}

// NewSnapshotInvalidCompressionError 创建无效的压缩算法类型错误
func NewSnapshotInvalidCompressionError(err error) *Error {
	return newWithErr(ErrSnapshotInvalidCompression, err, "无效的压缩算法类型")
}

// NewSnapshotCreateCompressorError 创建创建压缩器失败错误
func NewSnapshotCreateCompressorError(err error) *Error {
	return newWithErr(ErrSnapshotCreateCompressor, err, "创建压缩器失败")
}

// NewSnapshotLocateMetadataError 创建定位元数据段失败错误
func NewSnapshotLocateMetadataError(err error) *Error {
	return newWithErr(ErrSnapshotLocateMetadata, err, "定位元数据段失败")
}

// NewSnapshotReadMetadataError 创建读取元数据段失败错误
func NewSnapshotReadMetadataError(err error) *Error {
	return newWithErr(ErrSnapshotReadMetadata, err, "读取元数据段失败")
}

// NewSnapshotDecompressMetadataError 创建解压缩元数据失败错误
func NewSnapshotDecompressMetadataError(err error) *Error {
	return newWithErr(ErrSnapshotDecompressMetadata, err, "解压缩元数据失败")
}

// NewSnapshotDeserializeMetadataError 创建反序列化元数据失败错误
func NewSnapshotDeserializeMetadataError(err error) *Error {
	return newWithErr(ErrSnapshotDeserializeMetadata, err, "反序列化元数据失败")
}

// NewSnapshotLocateDataError 创建定位数据段失败错误
func NewSnapshotLocateDataError(err error) *Error {
	return newWithErr(ErrSnapshotLocateData, err, "定位数据段失败")
}

// NewSnapshotReadDataError 创建读取数据段失败错误
func NewSnapshotReadDataError(err error) *Error {
	return newWithErr(ErrSnapshotReadData, err, "读取数据段失败")
}

// NewSnapshotDecompressDataError 创建解压缩数据失败错误
func NewSnapshotDecompressDataError(err error) *Error {
	return newWithErr(ErrSnapshotDecompressData, err, "解压缩数据失败")
}

// NewSnapshotDeserializeDataError 创建反序列化数据失败错误
func NewSnapshotDeserializeDataError(err error) *Error {
	return newWithErr(ErrSnapshotDeserializeData, err, "反序列化数据失败")
}

// NewSnapshotGetFileInfoError 创建获取文件信息失败错误
func NewSnapshotGetFileInfoError(err error) *Error {
	return newWithErr(ErrSnapshotGetFileInfo, err, "获取文件信息失败")
}

// NewSnapshotLocateChecksumError 创建定位校验和失败错误
func NewSnapshotLocateChecksumError(err error) *Error {
	return newWithErr(ErrSnapshotLocateChecksum, err, "定位校验和失败")
}

// NewSnapshotReadChecksumError 创建读取校验和失败错误
func NewSnapshotReadChecksumError(err error) *Error {
	return newWithErr(ErrSnapshotReadChecksum, err, "读取校验和失败")
}

// NewSnapshotResetFileError 创建重置文件指针失败错误
func NewSnapshotResetFileError(err error) *Error {
	return newWithErr(ErrSnapshotResetFile, err, "重置文件指针失败")
}

// NewSnapshotReadDataForChecksumError 创建读取数据用于校验和失败错误
func NewSnapshotReadDataForChecksumError(err error) *Error {
	return newWithErr(ErrSnapshotReadDataForChecksum, err, "读取数据失败")
}

// NewSnapshotCalculateChecksumError 创建计算校验和失败错误
func NewSnapshotCalculateChecksumError(err error) *Error {
	return newWithErr(ErrSnapshotCalculateChecksum, err, "计算校验和失败")
}

// NewSnapshotChecksumMismatchError 创建校验和不匹配错误
func NewSnapshotChecksumMismatchError() *Error {
	return newBase(ErrSnapshotChecksumMismatch, "SHA256 校验和不匹配")
}

// NewSnapshotCreateDirError 创建创建 Snapshot 目录失败错误
func NewSnapshotCreateDirError(err error) *Error {
	return newWithErr(ErrSnapshotCreateDir, err, "创建 Snapshot 目录失败")
}

// NewSnapshotCreateWriterError 创建创建 Snapshot 写入器失败错误
func NewSnapshotCreateWriterError(err error) *Error {
	return newWithErr(ErrSnapshotCreateWriter, err, "创建 Snapshot 写入器失败")
}

// NewSnapshotSerializeMetadataError 创建序列化元数据失败错误
func NewSnapshotSerializeMetadataError(err error) *Error {
	return newWithErr(ErrSnapshotSerializeMetadata, err, "序列化元数据失败")
}

// NewSnapshotCompressMetadataError 创建压缩元数据失败错误
func NewSnapshotCompressMetadataError(err error) *Error {
	return newWithErr(ErrSnapshotCompressMetadata, err, "压缩元数据失败")
}

// NewSnapshotCacheMetadataError 创建缓存元数据段失败错误
func NewSnapshotCacheMetadataError(err error) *Error {
	return newWithErr(ErrSnapshotCacheMetadata, err, "缓存元数据段失败")
}

// NewSnapshotSerializeDataError 创建序列化数据失败错误
func NewSnapshotSerializeDataError(err error) *Error {
	return newWithErr(ErrSnapshotSerializeData, err, "序列化数据失败")
}

// NewSnapshotCompressDataError 创建压缩数据失败错误
func NewSnapshotCompressDataError(err error) *Error {
	return newWithErr(ErrSnapshotCompressData, err, "压缩数据失败")
}

// NewSnapshotCacheDataError 创建缓存数据段失败错误
func NewSnapshotCacheDataError(err error) *Error {
	return newWithErr(ErrSnapshotCacheData, err, "缓存数据段失败")
}

// NewSnapshotFinalizeAlreadyCalledError 创建 Finalize 已调用错误
func NewSnapshotFinalizeAlreadyCalledError() *Error {
	return newBase(ErrSnapshotFinalizeAlreadyCalled, "Finalize 已调用，不能重复调用")
}

// NewSnapshotWriteHeaderError 创建写入文件头失败错误
func NewSnapshotWriteHeaderError(err error) *Error {
	return newWithErr(ErrSnapshotWriteHeader, err, "写入文件头失败")
}

// NewSnapshotWriteMetadataError 创建写入元数据段失败错误
func NewSnapshotWriteMetadataError(err error) *Error {
	return newWithErr(ErrSnapshotWriteMetadata, err, "写入元数据段失败")
}

// NewSnapshotWriteDataError 创建写入数据段失败错误
func NewSnapshotWriteDataError(err error) *Error {
	return newWithErr(ErrSnapshotWriteData, err, "写入数据段失败")
}

// NewSnapshotSerializeHeaderError 创建序列化文件头失败错误
func NewSnapshotSerializeHeaderError(err error) *Error {
	return newWithErr(ErrSnapshotSerializeHeader, err, "序列化文件头失败")
}

// NewSnapshotCalculateHeaderHashError 创建计算文件头哈希失败错误
func NewSnapshotCalculateHeaderHashError(err error) *Error {
	return newWithErr(ErrSnapshotCalculateHeaderHash, err, "计算文件头哈希失败")
}

// NewSnapshotCalculateMetadataHashError 创建计算元数据段哈希失败错误
func NewSnapshotCalculateMetadataHashError(err error) *Error {
	return newWithErr(ErrSnapshotCalculateMetadataHash, err, "计算元数据段哈希失败")
}

// NewSnapshotCalculateDataHashError 创建计算数据段哈希失败错误
func NewSnapshotCalculateDataHashError(err error) *Error {
	return newWithErr(ErrSnapshotCalculateDataHash, err, "计算数据段哈希失败")
}

// NewSnapshotSyncDataError 创建同步数据失败错误
func NewSnapshotSyncDataError(err error) *Error {
	return newWithErr(ErrSnapshotSyncData, err, "同步数据失败")
}

// NewSnapshotWriteChecksumError 创建写入校验和失败错误
func NewSnapshotWriteChecksumError(err error) *Error {
	return newWithErr(ErrSnapshotWriteChecksum, err, "写入校验和失败")
}

// NewSnapshotFinalSyncError 创建最终同步失败错误
func NewSnapshotFinalSyncError(err error) *Error {
	return newWithErr(ErrSnapshotFinalSync, err, "最终同步失败")
}

// NewSnapshotCloseFileError 创建关闭文件失败错误
func NewSnapshotCloseFileError(err error) *Error {
	return newWithErr(ErrSnapshotCloseFile, err, "关闭文件失败")
}

// NewSnapshotRenameFileError 创建重命名 Snapshot 文件失败错误
func NewSnapshotRenameFileError(err error) *Error {
	return newWithErr(ErrSnapshotRenameFile, err, "重命名 Snapshot 文件失败")
}

// NewSnapshotCreateReaderError 创建创建 Snapshot 读取器失败错误
func NewSnapshotCreateReaderError(err error) *Error {
	return newWithErr(ErrSnapshotCreateReader, err, "创建 Snapshot 读取器失败")
}

// NewSnapshotVerifyChecksumError 创建验证校验和失败错误
func NewSnapshotVerifyChecksumError(err error) *Error {
	return newWithErr(ErrSnapshotVerifyChecksum, err, "验证校验和失败")
}

// NewSnapshotReadMetadataSectionError 创建读取元数据段失败错误
func NewSnapshotReadMetadataSectionError(err error) *Error {
	return newWithErr(ErrSnapshotReadMetadataSection, err, "读取元数据段失败")
}

// NewSnapshotReadDataSectionError 创建读取数据段失败错误
func NewSnapshotReadDataSectionError(err error) *Error {
	return newWithErr(ErrSnapshotReadDataSection, err, "读取数据段失败")
}

// NewSnapshotReadDirError 创建读取 Snapshot 目录失败错误
func NewSnapshotReadDirError(err error) *Error {
	return newWithErr(ErrSnapshotReadDir, err, "读取 Snapshot 目录失败")
}

// NewSnapshotNoSnapshotFileError 创建没有找到 Snapshot 文件错误
func NewSnapshotNoSnapshotFileError() *Error {
	return newBase(ErrSnapshotNoSnapshotFile, "没有找到 Snapshot 文件")
}

// NewSnapshotDeleteFileError 创建删除 Snapshot 文件失败错误
func NewSnapshotDeleteFileError(err error) *Error {
	return newWithErr(ErrSnapshotDeleteFile, err, "删除 Snapshot 文件失败")
}

// NewSnapshotInvalidFileNameError 创建无效的 Snapshot 文件名错误
func NewSnapshotInvalidFileNameError(fileName string) *Error {
	return newBase(ErrSnapshotInvalidFileName, "无效的 Snapshot 文件名: %s", fileName)
}

// NewSnapshotDirNotExistError 创建 Snapshot 目录不存在错误
func NewSnapshotDirNotExistError(dir string) *Error {
	return newBase(ErrSnapshotDirNotExist, "snapshot 目录不存在: %s", dir)
}

// NewSnapshotDirNotAccessibleError 创建无法访问 snapshot 目录错误
func NewSnapshotDirNotAccessibleError(err error, dir string) *Error {
	return newWithErr(ErrSnapshotDirNotAccessible, err, "无法访问 snapshot 目录: %s", dir)
}

// NewSnapshotDirNotDirectoryError 创建 snapshot 路径不是目录错误
func NewSnapshotDirNotDirectoryError(dir string) *Error {
	return newBase(ErrSnapshotDirNotDirectory, "snapshot 路径不是目录: %s", dir)
}

// NewSnapshotDirNotWritableError 创建 snapshot 目录没有写权限错误
func NewSnapshotDirNotWritableError(err error) *Error {
	return newWithErr(ErrSnapshotDirNotWritable, err, "snapshot 目录没有写权限")
}

// NewSnapshotGetSnapshotDataError 创建获取快照数据失败错误
func NewSnapshotGetSnapshotDataError(err error) *Error {
	return newWithErr(ErrSnapshotGetSnapshotData, err, "获取快照数据失败")
}

// NewSnapshotCreateSnapshotError 创建创建 Snapshot 失败错误
func NewSnapshotCreateSnapshotError(err error) *Error {
	return newWithErr(ErrSnapshotCreateSnapshot, err, "创建 Snapshot 失败")
}

// NewSnapshotLoadSnapshotError 创建加载 Snapshot 失败错误
func NewSnapshotLoadSnapshotError(err error) *Error {
	return newWithErr(ErrSnapshotLoadSnapshot, err, "加载 Snapshot 失败")
}

// NewSnapshotMissingSnapshotDataError 创建缺少 snapshot_data 字段错误
func NewSnapshotMissingSnapshotDataError() *Error {
	return newBase(ErrSnapshotMissingSnapshotData, "缺少 snapshot_data 字段")
}

// ========================================
// Encryption 错误构造函数（新增）
// ========================================

// NewEncryptCreateGCMModeError 创建创建 GCM 模式失败错误
func NewEncryptCreateGCMModeError(err error) *Error {
	return newWithErr(ErrEncryptCreateGCMMode, err, "创建 GCM 模式失败")
}

// NewEncryptDecryptOrAuthFailedError 创建解密失败或认证标签验证失败错误
func NewEncryptDecryptOrAuthFailedError(err error) *Error {
	return newWithErr(ErrEncryptDecryptOrAuthFailed, err, "解密失败或认证标签验证失败")
}

// NewEncryptGenerateNonceError 创建生成 Nonce 失败错误
func NewEncryptGenerateNonceError(err error) *Error {
	return newWithErr(ErrEncryptGenerateNonce, err, "生成 Nonce 失败")
}

// NewEncryptCreateCipherError 创建创建 cipher 失败错误
func NewEncryptCreateCipherError(err error) *Error {
	return newWithErr(ErrEncryptCreateCipher, err, "创建 cipher 失败")
}

// NewEncryptCreateGCMError 创建创建 GCM 失败错误
func NewEncryptCreateGCMError(err error) *Error {
	return newWithErr(ErrEncryptCreateGCM, err, "创建 GCM 失败")
}

// ========================================
// Daemon/Application 错误构造函数（新增）
// ========================================

// NewDaemonInitializeLoggingError 创建初始化日志失败错误
func NewDaemonInitializeLoggingError(err error) *Error {
	return newWithErr(ErrDaemonInitializeLogging, err, "初始化日志失败")
}

// NewDaemonLoadConfigError 创建加载配置失败错误
func NewDaemonLoadConfigError(err error) *Error {
	return newWithErr(ErrDaemonLoadConfig, err, "加载配置失败")
}

// NewDaemonCreateAppContextError 创建创建应用上下文失败错误
func NewDaemonCreateAppContextError(err error) *Error {
	return newWithErr(ErrDaemonCreateAppContext, err, "创建应用上下文失败")
}

// NewDaemonInitializeError 创建初始化失败错误
func NewDaemonInitializeError(err error) *Error {
	return newWithErr(ErrDaemonInitialize, err, "初始化失败")
}

// NewDaemonStopError 创建停止守护进程失败错误
func NewDaemonStopError(err error) *Error {
	return newWithErr(ErrDaemonStop, err, "停止守护进程失败")
}

// NewDaemonConfigEmptyError 创建配置不能为空错误
func NewDaemonConfigEmptyError() *Error {
	return newBase(ErrDaemonConfigEmpty, "配置不能为空")
}

// NewDaemonParseListenAddrError 创建解析监听地址失败错误
func NewDaemonParseListenAddrError(err error) *Error {
	return newWithErr(ErrDaemonParseListenAddr, err, "解析监听地址失败")
}

// NewDaemonGenerateNodeIDError 创建生成节点 ID 失败错误
func NewDaemonGenerateNodeIDError(err error) *Error {
	return newWithErr(ErrDaemonGenerateNodeID, err, "生成节点 ID 失败")
}

// NewDaemonCreateTCPTransportError 创建创建 TCP 传输失败错误
func NewDaemonCreateTCPTransportError(err error) *Error {
	return newWithErr(ErrDaemonCreateTCPTransport, err, "创建 TCP 传输失败")
}

// NewDaemonCreateUDPTransportError 创建创建 UDP 传输失败错误
func NewDaemonCreateUDPTransportError(err error) *Error {
	return newWithErr(ErrDaemonCreateUDPTransport, err, "创建 UDP 传输失败")
}

// NewDaemonStartTCPTransportError 创建启动 TCP 传输失败错误
func NewDaemonStartTCPTransportError(err error) *Error {
	return newWithErr(ErrDaemonStartTCPTransport, err, "启动 TCP 传输失败")
}

// NewDaemonStartUDPTransportError 创建启动 UDP 传输失败错误
func NewDaemonStartUDPTransportError(err error) *Error {
	return newWithErr(ErrDaemonStartUDPTransport, err, "启动 UDP 传输失败")
}

// NewDaemonCreateRPCClientError 创建创建 RPC Client 失败错误
func NewDaemonCreateRPCClientError(err error) *Error {
	return newWithErr(ErrDaemonCreateRPCClient, err, "创建 RPC Client 失败")
}

// NewDaemonCreateRPCServerError 创建创建 RPC Server 失败错误
func NewDaemonCreateRPCServerError(err error) *Error {
	return newWithErr(ErrDaemonCreateRPCServer, err, "创建 RPC Server 失败")
}

// NewDaemonStartRPCServerError 创建启动 RPC Server 失败错误
func NewDaemonStartRPCServerError(err error) *Error {
	return newWithErr(ErrDaemonStartRPCServer, err, "启动 RPC Server 失败")
}

// NewDaemonStartRPCClientError 创建启动 RPC Client 失败错误
func NewDaemonStartRPCClientError(err error) *Error {
	return newWithErr(ErrDaemonStartRPCClient, err, "启动 RPC Client 失败")
}

// NewDaemonCreateTreeCoordinatorError 创建创建 TreeCoordinator 失败错误
func NewDaemonCreateTreeCoordinatorError(err error) *Error {
	return newWithErr(ErrDaemonCreateTreeCoordinator, err, "创建 TreeCoordinator 失败")
}

// NewDaemonStartTreeCoordinatorError 创建启动 TreeCoordinator 失败错误
func NewDaemonStartTreeCoordinatorError(err error) *Error {
	return newWithErr(ErrDaemonStartTreeCoordinator, err, "启动 TreeCoordinator 失败")
}

// NewDaemonStopTreeCoordinatorError 创建停止 TreeCoordinator 失败错误
func NewDaemonStopTreeCoordinatorError(err error) *Error {
	return newWithErr(ErrDaemonStopTreeCoordinator, err, "停止 TreeCoordinator 失败")
}

// NewDaemonStopRPCClientError 创建停止 RPC Client 失败错误
func NewDaemonStopRPCClientError(err error) *Error {
	return newWithErr(ErrDaemonStopRPCClient, err, "停止 RPC Client 失败")
}

// NewDaemonStopRPCServerError 创建停止 RPC Server 失败错误
func NewDaemonStopRPCServerError(err error) *Error {
	return newWithErr(ErrDaemonStopRPCServer, err, "停止 RPC Server 失败")
}

// NewDaemonStopTCPTransportError 创建停止 TCP 传输层失败错误
func NewDaemonStopTCPTransportError(err error) *Error {
	return newWithErr(ErrDaemonStopTCPTransport, err, "停止 TCP 传输层失败")
}

// NewDaemonStopUDPTransportError 创建停止 UDP 传输层失败错误
func NewDaemonStopUDPTransportError(err error) *Error {
	return newWithErr(ErrDaemonStopUDPTransport, err, "停止 UDP 传输层失败")
}

// NewDaemonStopWithErrorsError 创建停止守护进程时发生错误错误
func NewDaemonStopWithErrorsError(errs []error) *Error {
	return newBase(ErrDaemonStopWithErrors, "停止守护进程时发生错误: %v", errs)
}

// NewDaemonShutdownError 创建停止守护进程失败错误
func NewDaemonShutdownError(err error) *Error {
	return newWithErr(ErrDaemonShutdown, err, "停止守护进程失败")
}

// NewDaemonConfigNilError 创建配置不能为空错误
func NewDaemonConfigNilError() *Error {
	return newBase(ErrDaemonConfigNil, "配置不能为空")
}

// NewDaemonShutdownMultipleErrorsError 创建停止守护进程时发生多个错误错误
func NewDaemonShutdownMultipleErrorsError(count int) *Error {
	return newBase(ErrDaemonShutdownMultipleErrors, "停止守护进程时发生 %d 个错误", count)
}

// NewDaemonSplitHostPortError 创建分离主机端口失败错误
func NewDaemonSplitHostPortError(addr string, err error) *Error {
	return newWithErr(ErrDaemonSplitHostPort, err, "解析地址 %s 失败", addr)
}

// NewDaemonHostIDNotFoundError 创建未找到主机 ID 错误
func NewDaemonHostIDNotFoundError(hostID string) *Error {
	return newBase(ErrDaemonHostIDNotFound, "未找到主机 ID: %s", hostID)
}

// NewDaemonNoNodesError 创建主机中没有 Node 错误
func NewDaemonNoNodesError(hostID string) *Error {
	return newBase(ErrDaemonNoNodes, "主机 %s 中没有 Node", hostID)
}

// NewDaemonListenAddrEmptyError 创建监听地址不能为空错误
func NewDaemonListenAddrEmptyError() *Error {
	return newBase(ErrDaemonListenAddrEmpty, "监听地址不能为空")
}

// NewDaemonParseAddrError 创建解析地址失败错误
func NewDaemonParseAddrError(err error) *Error {
	return newWithErr(ErrDaemonParseAddr, err, "解析地址失败")
}

// NewDaemonInvalidPortError 创建无效的端口号错误
func NewDaemonInvalidPortError(portStr string) *Error {
	return newBase(ErrDaemonInvalidPort, "无效的端口号: %s", portStr)
}

// NewDaemonMultipleHostsError 创建配置中有多个 Host 错误
func NewDaemonMultipleHostsError(count int) *Error {
	return newBase(ErrDaemonMultipleHosts, "配置中有多个 Host（%d 个），必须使用 --host-id 明确指定", count)
}

// NewDaemonHostNotFoundError 创建未找到主机 ID 错误
func NewDaemonHostNotFoundError(hostID string) *Error {
	return newBase(ErrDaemonHostNotFound, "未找到主机 ID: %s", hostID)
}

// NewDaemonNoHostsError 创建配置中没有 Host 错误
func NewDaemonNoHostsError() *Error {
	return newBase(ErrDaemonNoHosts, "配置中没有 Host")
}

// NewDaemonNodeIDNotFoundError 创建主机中未找到节点 ID 错误
func NewDaemonNodeIDNotFoundError(hostID, nodeID string) *Error {
	return newBase(ErrDaemonNodeIDNotFound, "主机 %s 中未找到节点 ID: %s", hostID, nodeID)
}

// NewDaemonNoNodesInHostError 创建主机中没有 Node 错误
func NewDaemonNoNodesInHostError(hostID string) *Error {
	return newBase(ErrDaemonNoNodesInHost, "主机 %s 中没有 Node", hostID)
}

// ========================================
// CLI Command 错误构造函数（新增）
// ========================================

// NewCLIParameterEmptyError 创建参数不能为空错误
func NewCLIParameterEmptyError(param string) *Error {
	return newBase(ErrCLIParameterEmpty, "--%s 参数不能为空", param)
}

// NewCLIConnectDaemonError 创建连接 Daemon 失败错误
func NewCLIConnectDaemonError(err error) *Error {
	return newWithErr(ErrCLIConnectDaemon, err, "连接 Daemon 失败")
}

// NewCLIAddNodeError 创建添加节点失败错误
func NewCLIAddNodeError(err error) *Error {
	return newWithErr(ErrCLIAddNode, err, "添加节点失败")
}

// NewCLINeedNodeIDError 创建需要指定节点 ID 错误
func NewCLINeedNodeIDError() *Error {
	return newBase(ErrCLINeedNodeID, "需要指定节点 ID")
}

// NewCLIDeleteNodeError 创建删除节点失败错误
func NewCLIDeleteNodeError(err error) *Error {
	return newWithErr(ErrCLIDeleteNode, err, "删除节点失败")
}

// NewCLIQueryNodeListError 创建查询节点列表失败错误
func NewCLIQueryNodeListError(err error) *Error {
	return newWithErr(ErrCLIQueryNodeList, err, "查询节点列表失败")
}

// NewCLIResponseTypeError 创建响应类型错误错误
func NewCLIResponseTypeError(expected string) *Error {
	return newBase(ErrCLIResponseTypeError, "响应类型错误: 期望 %s", expected)
}

// NewCLIFormatOutputError 创建格式化输出失败错误
func NewCLIFormatOutputError(err error) *Error {
	return newWithErr(ErrCLIFormatOutput, err, "格式化输出失败")
}

// NewCLIUnsupportedOutputFormatError 创建不支持的输出格式错误
func NewCLIUnsupportedOutputFormatError(format string) *Error {
	return newBase(ErrCLIUnsupportedOutputFormat, "不支持的输出格式: %s", format)
}

// NewCLIQueryClusterStatusError 创建查询集群状态失败错误
func NewCLIQueryClusterStatusError(err error) *Error {
	return newWithErr(ErrCLIQueryClusterStatus, err, "查询集群状态失败")
}

// NewCLIQueryClusterTopologyError 创建查询集群拓扑失败错误
func NewCLIQueryClusterTopologyError(err error) *Error {
	return newWithErr(ErrCLIQueryClusterTopology, err, "查询集群拓扑失败")
}

// NewCLIQueryClusterInfoError 创建查询集群信息失败错误
func NewCLIQueryClusterInfoError(err error) *Error {
	return newWithErr(ErrCLIQueryClusterInfo, err, "查询集群信息失败")
}

// NewCLIHealthCheckError 创建健康检查失败错误
func NewCLIHealthCheckError(err error) *Error {
	return newWithErr(ErrCLIHealthCheck, err, "健康检查失败")
}

// NewCLISerializeError 创建 JSON 序列化失败错误
func NewCLISerializeError(err error) *Error {
	return newWithErr(ErrCLISerialize, err, "JSON 序列化失败")
}

// NewCLINotImplementedError 创建 YAML 格式暂未实现错误
func NewCLINotImplementedError() *Error {
	return newBase(ErrCLINotImplemented, "YAML 格式暂未实现")
}

// ========================================
// Network Utility 错误构造函数（新增）
// ========================================

// NewNetInvalidEnvTypeError 创建无效的环境类型错误
func NewNetInvalidEnvTypeError(s string) *Error {
	return newBase(ErrNetInvalidEnvType, "无效的环境类型: %s（必须是 dev 或 cluster）", s)
}

// NewNetInvalidIPError 创建无效的 IP 地址错误
func NewNetInvalidIPError(ip string) *Error {
	return newBase(ErrNetInvalidIP, "无效的 IP 地址: %s", ip)
}

// NewNetGetPrivateIPError 创建获取私网 IP 失败错误
func NewNetGetPrivateIPError(err error) *Error {
	return newWithErr(ErrNetGetPrivateIP, err, "获取私网 IP 失败")
}

// NewNetNoPrivateIPFoundError 创建未找到可用的私网 IP 错误
func NewNetNoPrivateIPFoundError() *Error {
	return newBase(ErrNetNoPrivateIPFound, "未找到可用的私网 IP，请手动指定 --bind-ip 参数")
}

// NewNetGetInterfacesError 创建获取网络接口失败错误
func NewNetGetInterfacesError(err error) *Error {
	return newWithErr(ErrNetGetInterfaces, err, "获取网络接口失败")
}

// NewNetNoAvailablePortError 创建未找到可用端口错误
func NewNetNoAvailablePortError(startPort int) *Error {
	return newBase(ErrNetNoAvailablePort, "未找到可用端口（起始端口: %d）", startPort)
}

// NewNetIPMismatchError 创建用户指定 IP 与自动绑定 IP 不匹配错误
func NewNetIPMismatchError(userIP, autoIP string) *Error {
	return newBase(ErrNetIPMismatch, "用户指定 IP (%s) 与自动绑定 IP (%s) 不匹配，请确保一致", userIP, autoIP)
}

// ========================================
// Tree Coordinator 错误构造函数（新增）
// ========================================

// NewTreeCoordinatorAddrEmptyError 创建地址字符串不能为空错误
func NewTreeCoordinatorAddrEmptyError() *Error {
	return newBase(ErrTreeCoordinatorAddrEmpty, "地址字符串不能为空")
}

// NewTreeCoordinatorInvalidIPFSAddrError 创建无效的 IPFS 地址格式错误
func NewTreeCoordinatorInvalidIPFSAddrError(addrStr string) *Error {
	return newBase(ErrTreeCoordinatorInvalidIPFSAddr, "无效的 IPFS 地址格式: %s", addrStr)
}

// NewTreeCoordinatorInvalidProtocolError 创建无效的协议格式错误
func NewTreeCoordinatorInvalidProtocolError(addrStr string) *Error {
	return newBase(ErrTreeCoordinatorInvalidProtocol, "无效的协议格式: %s", addrStr)
}

// NewTreeCoordinatorInvalidPortError 创建无效的端口号错误
func NewTreeCoordinatorInvalidPortError(portStr string) *Error {
	return newBase(ErrTreeCoordinatorInvalidPort, "无效的端口号: %s", portStr)
}

// NewTreeCoordinatorUnsupportedProtocolError 创建不支持的协议类型错误
func NewTreeCoordinatorUnsupportedProtocolError(protocol string) *Error {
	return newBase(ErrTreeCoordinatorUnsupportedProtocol, "不支持的协议类型: %s", protocol)
}

// NewTreeCoordinatorInvalidAddrError 创建无效的地址格式错误
func NewTreeCoordinatorInvalidAddrError(addrStr string) *Error {
	return newBase(ErrTreeCoordinatorInvalidAddr, "无效的地址格式: %s", addrStr)
}

// NewTreeCoordinatorSendJoinRequestError 创建发送加入请求失败错误
func NewTreeCoordinatorSendJoinRequestError(err error) *Error {
	return newWithErr(ErrTreeCoordinatorSendJoinRequest, err, "发送加入请求失败")
}

// NewTreeCoordinatorNoSuitableParentError 创建没有找到合适的父节点错误
func NewTreeCoordinatorNoSuitableParentError() *Error {
	return newBase(ErrTreeCoordinatorNoSuitableParent, "没有找到合适的父节点")
}

// NewTreeCoordinatorNotInitializedError 创建 coordinator not initialized 错误
func NewTreeCoordinatorNotInitializedError() *Error {
	return newBase(ErrTreeCoordinatorCoordinatorNotInitialized, "coordinator not initialized")
}

// NewTreeCoordinatorUnsupportedMessageTypeError 创建 unsupported message type 错误
func NewTreeCoordinatorUnsupportedMessageTypeError(msg any) *Error {
	return newBase(ErrTreeCoordinatorUnsupportedMessageType, "unsupported message type: %T", msg)
}

// NewTreeCoordinatorFailedToAddChildError 创建 failed to add child 错误
func NewTreeCoordinatorFailedToAddChildError(err error) *Error {
	return newWithErr(ErrTreeCoordinatorFailedToAddChild, err, "failed to add child")
}

// NewTreeCoordinatorFailedToRemoveChildError 创建 failed to remove child 错误
func NewTreeCoordinatorFailedToRemoveChildError(err error) *Error {
	return newWithErr(ErrTreeCoordinatorFailedToRemoveChild, err, "failed to remove child")
}

// NewTreeCoordinatorNodeNotFoundError 创建节点不存在错误
func NewTreeCoordinatorNodeNotFoundError(nodeID string) *Error {
	return newBase(ErrTreeCoordinatorNodeNotFound, "节点不存在: %s", nodeID)
}

// NewTreeCoordinatorTCPPortOutOfRangeError 创建 TCP 端口范围错误
func NewTreeCoordinatorTCPPortOutOfRangeError(minPort, maxPort, port int) *Error {
	return newBase(ErrTreeCoordinatorTCPPortOutOfRange, "TCPPort must be in range [%d, %d], got %d", minPort, maxPort, port)
}

// NewTreeCoordinatorUDPPortOutOfRangeError 创建 UDP 端口范围错误
func NewTreeCoordinatorUDPPortOutOfRangeError(minPort, maxPort, port int) *Error {
	return newBase(ErrTreeCoordinatorUDPPortOutOfRange, "UDPPort must be in range [%d, %d], got %d", minPort, maxPort, port)
}

// NewTreeCoordinatorUDPPortMustBeTCPPlusOneError 创建 UDP端口必须等于TCP端口+1错误
func NewTreeCoordinatorUDPPortMustBeTCPPlusOneError(tcpPort, udpPort int) *Error {
	return newBase(ErrTreeCoordinatorUDPPortMustBeTCPPlusOne, "UDPPort must equal TCPPort + 1, got TCP=%d UDP=%d", tcpPort, udpPort)
}

// NewTreeCoordinatorAtLeastOnePortRequiredError 创建至少需要一个端口错误
func NewTreeCoordinatorAtLeastOnePortRequiredError() *Error {
	return newBase(ErrTreeCoordinatorAtLeastOnePortRequired, "at least one port (TCP or UDP) must be set")
}

// NewTreeCoordinatorNodeIDRequiredError 创建 NodeID 必填错误
func NewTreeCoordinatorNodeIDRequiredError() *Error {
	return newBase(ErrTreeCoordinatorNodeIDRequired, "NodeID is required")
}

// NewTreeCoordinatorHostIDRequiredError 创建 HostID 必填错误
func NewTreeCoordinatorHostIDRequiredError() *Error {
	return newBase(ErrTreeCoordinatorHostIDRequired, "HostID is required")
}

// NewTreeCoordinatorInvalidNodeAddrError 创建无效的节点地址错误
func NewTreeCoordinatorInvalidNodeAddrError(err error) *Error {
	return newWithErr(ErrTreeCoordinatorInvalidNodeAddr, err, "invalid Addr")
}

// NewTreeCoordinatorInvalidNodeRoleError 创建无效的节点角色错误
func NewTreeCoordinatorInvalidNodeRoleError(role int) *Error {
	return newBase(ErrTreeCoordinatorInvalidNodeRole, "invalid NodeRole: %d", role)
}

// NewTreeCoordinatorLeafNodeIDRequiredError 创建 LeafNodeID 必填错误
func NewTreeCoordinatorLeafNodeIDRequiredError() *Error {
	return newBase(ErrTreeCoordinatorLeafNodeIDRequired, "LeafNodeID is required")
}

// NewTreeCoordinatorParentNodeIDRequiredError 创建 ParentNodeID 必填错误
func NewTreeCoordinatorParentNodeIDRequiredError() *Error {
	return newBase(ErrTreeCoordinatorParentNodeIDRequired, "ParentNodeID is required")
}

// NewTreeCoordinatorLeafOnlyHostShouldNotHaveParentNodeIDError 创建 LeafOnly Host 不应有 ParentNodeID 错误
func NewTreeCoordinatorLeafOnlyHostShouldNotHaveParentNodeIDError() *Error {
	return newBase(ErrTreeCoordinatorLeafOnlyHostShouldNotHaveParentNodeID, "LeafOnly Host should not have ParentNodeID")
}

// NewTreeCoordinatorLeafOnlyHostShouldNotHaveParentStandbyNodeIDError 创建 LeafOnly Host 不应有 ParentStandbyNodeID 错误
func NewTreeCoordinatorLeafOnlyHostShouldNotHaveParentStandbyNodeIDError() *Error {
	return newBase(ErrTreeCoordinatorLeafOnlyHostShouldNotHaveParentStandbyNodeID, "LeafOnly Host should not have ParentStandbyNodeID")
}

// NewTreeCoordinatorParentStandbyNodeIDRequiredError 创建 ParentStandbyNodeID 必填错误
func NewTreeCoordinatorParentStandbyNodeIDRequiredError() *Error {
	return newBase(ErrTreeCoordinatorParentStandbyNodeIDRequired, "ParentStandbyNodeID is required")
}

// NewTreeCoordinatorParentStandbyNodeIDMustBeDifferentError 创建 ParentStandbyNodeID 必须与 ParentNodeID 不同错误
func NewTreeCoordinatorParentStandbyNodeIDMustBeDifferentError() *Error {
	return newBase(ErrTreeCoordinatorParentStandbyNodeIDMustBeDifferent, "ParentStandbyNodeID must be different from ParentNodeID")
}

// NewTreeCoordinatorInvalidHostRoleError 创建无效的主机角色错误
func NewTreeCoordinatorInvalidHostRoleError(role int) *Error {
	return newBase(ErrTreeCoordinatorInvalidHostRole, "invalid HostRole: %d", role)
}

// NewTreeCoordinatorSendGossipMessageError 创建发送 Gossip 消息失败错误
func NewTreeCoordinatorSendGossipMessageError(err error) *Error {
	return newWithErr(ErrTreeCoordinatorSendGossipMessageError, err, "发送 Gossip 消息失败")
}

// ========================================
// Identity/NodeID 错误构造函数（新增）
// ========================================

// NewIdentityNoEnabledPortError 创建至少需要启用一个端口错误
func NewIdentityNoEnabledPortError() *Error {
	return newBase(ErrIdentityNoEnabledPort, "至少需要启用一个端口（TCP 或 UDP）")
}

// NewIdentityInvalidTCPPortError 创建 TCP 端口无效错误
func NewIdentityInvalidTCPPortError(port int) *Error {
	return newBase(ErrIdentityInvalidTCPPort, "TCP 端口无效: %d（有效范围: 1-65535）", port)
}

// NewIdentityInvalidUDPPortError 创建 UDP 端口无效错误
func NewIdentityInvalidUDPPortError(port int) *Error {
	return newBase(ErrIdentityInvalidUDPPort, "UDP 端口无效: %d（有效范围: 1-65535）", port)
}

// ========================================
// Config 详细验证错误构造函数（新增）
// ========================================

// NewConfigReadFileError 创建读取配置文件失败错误
func NewConfigReadFileError(err error) *Error {
	return newWithErr(ErrConfigReadFile, err, "读取配置文件失败")
}

// NewConfigParseFileError 创建解析配置文件失败错误
func NewConfigParseFileError(err error) *Error {
	return newWithErr(ErrConfigParseFile, err, "解析配置文件失败")
}

// NewConfigValidateFileError 创建配置验证失败错误
func NewConfigValidateFileError(err error) *Error {
	return newWithErr(ErrConfigValidateFile, err, "配置验证失败")
}

// NewConfigClusterNameEmptyError 创建 cluster.name 不能为空错误
func NewConfigClusterNameEmptyError() *Error {
	return newBase(ErrConfigClusterNameEmpty, "cluster.name 不能为空")
}

// NewConfigBaseDirEmptyError 创建 cluster.base_dir 不能为空错误
func NewConfigBaseDirEmptyError() *Error {
	return newBase(ErrConfigBaseDirEmpty, "cluster.base_dir 不能为空")
}

// NewConfigHostsEmptyError 创建 cluster.hosts 不能为空错误
func NewConfigHostsEmptyError() *Error {
	return newBase(ErrConfigHostsEmpty, "cluster.hosts 不能为空，至少需要一个 Host 配置")
}

// NewConfigHostIDEmptyError 创建 cluster.hosts[i].host_id 不能为空错误
func NewConfigHostIDEmptyError(i int) *Error {
	return newBase(ErrConfigHostIDEmpty, "cluster.hosts[%d].host_id 不能为空", i)
}

// NewConfigSeedNodeEmptyError 创建 cluster.hosts[i].seed_node 不能为空错误
func NewConfigSeedNodeEmptyError(i int) *Error {
	return newBase(ErrConfigSeedNodeEmpty, "cluster.hosts[%d].seed_node 不能为空", i)
}

// NewConfigNodesEmptyError 创建 cluster.hosts[i].nodes 不能为空错误
func NewConfigNodesEmptyError(i int) *Error {
	return newBase(ErrConfigNodesEmpty, "cluster.hosts[%d].nodes 不能为空，至少需要一个 Node 配置", i)
}

// NewConfigNodeIDEmptyError 创建 cluster.hosts[i].nodes[j].node_id 不能为空错误
func NewConfigNodeIDEmptyError(i, j int) *Error {
	return newBase(ErrConfigNodeIDEmpty, "cluster.hosts[%d].nodes[%d].node_id 不能为空", i, j)
}

// NewConfigNodeAddrTCPEmptyError 创建 cluster.hosts[i].nodes[j].node_addr_tcp 不能为空错误
func NewConfigNodeAddrTCPEmptyError(i, j int) *Error {
	return newBase(ErrConfigNodeAddrTCPEmpty, "cluster.hosts[%d].nodes[%d].node_addr_tcp 不能为空", i, j)
}

// NewConfigNodeAddrUDPEmptyError 创建 cluster.hosts[i].nodes[j].node_addr_udp 不能为空错误
func NewConfigNodeAddrUDPEmptyError(i, j int) *Error {
	return newBase(ErrConfigNodeAddrUDPEmpty, "cluster.hosts[%d].nodes[%d].node_addr_udp 不能为空", i, j)
}

// NewConfigNodeAddrTCPInvalidFormatError 创建 cluster.hosts[i].nodes[j].node_addr_tcp 格式错误错误
func NewConfigNodeAddrTCPInvalidFormatError(i, j int) *Error {
	return newBase(ErrConfigNodeAddrTCPInvalidFormat, "cluster.hosts[%d].nodes[%d].node_addr_tcp 格式错误，应为 multiaddr 格式（如 /ip4/127.0.0.1/tcp/9211）", i, j)
}

// NewConfigNodeAddrUDPInvalidFormatError 创建 cluster.hosts[i].nodes[j].node_addr_udp 格式错误错误
func NewConfigNodeAddrUDPInvalidFormatError(i, j int) *Error {
	return newBase(ErrConfigNodeAddrUDPInvalidFormat, "cluster.hosts[%d].nodes[%d].node_addr_udp 格式错误，应为 multiaddr 格式（如 /ip4/127.0.0.1/udp/9212）", i, j)
}

// NewConfigGossipIntervalInvalidError 创建 gossip_interval 不能小于 1 秒错误
func NewConfigGossipIntervalInvalidError() *Error {
	return newBase(ErrConfigGossipIntervalInvalid, "gossip_interval 不能小于 1 秒")
}

// NewConfigQuorumTimeoutInvalidError 创建 quorum_timeout 不能小于 1 秒错误
func NewConfigQuorumTimeoutInvalidError() *Error {
	return newBase(ErrConfigQuorumTimeoutInvalid, "quorum_timeout 不能小于 1 秒")
}

// NewConfigChangeLogSizeInvalidError 创建 change_log_size 不能小于 100 错误
func NewConfigChangeLogSizeInvalidError() *Error {
	return newBase(ErrConfigChangeLogSizeInvalid, "change_log_size 不能小于 100")
}

// NewConfigFlushIntervalInvalidError 创建 flush_interval 不能小于 1 秒错误
func NewConfigFlushIntervalInvalidError() *Error {
	return newBase(ErrConfigFlushIntervalInvalid, "flush_interval 不能小于 1 秒")
}

// NewConfigListenAddrEmptyError 创建 listen_addr 不能为空错误
func NewConfigListenAddrEmptyError() *Error {
	return newBase(ErrConfigListenAddrEmpty, "listen_addr 不能为空")
}

// NewConfigTransportTypeEmptyError 创建 transport_type 不能为空错误
func NewConfigTransportTypeEmptyError() *Error {
	return newBase(ErrConfigTransportTypeEmpty, "transport_type 不能为空")
}

// NewConfigMessagePackTypeEmptyError 创建 message_pack_type 不能为空错误
func NewConfigMessagePackTypeEmptyError() *Error {
	return newBase(ErrConfigMessagePackTypeEmpty, "message_pack_type 不能为空")
}

// NewConfigMaxMessageSizeInvalidError 创建 max_message_size 不能小于 1024 字节错误
func NewConfigMaxMessageSizeInvalidError() *Error {
	return newBase(ErrConfigMaxMessageSizeInvalid, "max_message_size 不能小于 1024 字节")
}

// NewConfigConnectTimeoutInvalidError 创建 connect_timeout 不能小于 1 秒错误
func NewConfigConnectTimeoutInvalidError() *Error {
	return newBase(ErrConfigConnectTimeoutInvalid, "connect_timeout 不能小于 1 秒")
}

// NewConfigInvalidLogLevelError 创建无效的日志级别错误
func NewConfigInvalidLogLevelError(level string) *Error {
	return newBase(ErrConfigInvalidLogLevel, "无效的日志级别: %s（必须是 debug/info/warn/error/fatal）", level)
}

// NewConfigInvalidLogFormatError 创建无效的日志格式错误
func NewConfigInvalidLogFormatError(format string) *Error {
	return newBase(ErrConfigInvalidLogFormat, "无效的日志格式: %s（必须是 json/text）", format)
}

// NewConfigInvalidLogOutputError 创建无效的日志输出错误
func NewConfigInvalidLogOutputError(output string) *Error {
	return newBase(ErrConfigInvalidLogOutput, "无效的日志输出: %s（必须是 stdout/file）", output)
}

// NewConfigLogFileRequiredError 创建日志输出为 file 时，必须指定 file 路径错误
func NewConfigLogFileRequiredError() *Error {
	return newBase(ErrConfigLogFileRequired, "日志输出为 file 时，必须指定 file 路径")
}

// NewConfigMaxDriftInvalidError 创建 max_drift 不能为负数错误
func NewConfigMaxDriftInvalidError() *Error {
	return newBase(ErrConfigMaxDriftInvalid, "max_drift 不能为负数")
}

// NewConfigSyncIntervalInvalidError 创建 sync_interval 不能小于 1 秒错误
func NewConfigSyncIntervalInvalidError() *Error {
	return newBase(ErrConfigSyncIntervalInvalid, "sync_interval 不能小于 1 秒")
}

// NewConfigCreateDirError 创建创建配置目录失败错误
func NewConfigCreateDirError(err error) *Error {
	return newWithErr(ErrConfigCreateDir, err, "创建配置目录失败")
}

// NewConfigSerializeError 创建序列化配置失败错误
func NewConfigSerializeError(err error) *Error {
	return newWithErr(ErrConfigSerialize, err, "序列化配置失败")
}

// NewConfigWriteFileError 创建写入配置文件失败错误
func NewConfigWriteFileError(err error) *Error {
	return newWithErr(ErrConfigWriteFile, err, "写入配置文件失败")
}

// ========================================
// E2E 测试错误构造函数（PR-037: 新增）
// ========================================

// NewE2EConfigFileNotFoundError 创建找不到配置文件错误
func NewE2EConfigFileNotFoundError(attemptedPaths []string, rootDir string) *Error {
	return newBase(ErrE2EConfigFileNotFound, "找不到配置文件，尝试过的路径: %v (项目根目录: %s)", attemptedPaths, rootDir)
}

// NewE2EConfigFileReadError 创建读取配置文件失败错误
func NewE2EConfigFileReadError(configPath string, err error) *Error {
	return newWithOpErr(ErrE2EConfigFileReadError, "读取配置文件失败", err, "path: %s", configPath)
}

// NewE2EConfigFileParseError 创建解析配置文件失败错误
func NewE2EConfigFileParseError(configPath string, err error) *Error {
	return newWithOpErr(ErrE2EConfigFileParseError, "解析配置文件失败", err, "path: %s", configPath)
}

// NewE2EInvalidMultiAddrFormatError 创建无效的 multiaddr 格式错误
func NewE2EInvalidMultiAddrFormatError(multiaddr string) *Error {
	return newBase(ErrE2EInvalidMultiAddrFormat, "无效的 multiaddr 格式: %s", multiaddr)
}

// NewE2ENilResponseError 创建响应为 nil 错误
func NewE2ENilResponseError(seq int) *Error {
	return newBase(ErrE2ENilResponse, "seq %d: nil response", seq)
}

// NewE2EWrongResponseTypeError 创建响应类型错误错误
func NewE2EWrongResponseTypeError(seq int) *Error {
	return newBase(ErrE2EWrongResponseType, "seq %d: wrong response type", seq)
}
