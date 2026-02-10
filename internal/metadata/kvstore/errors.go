// Package kvstore 元数据 KV 存储错误定义
package kvstore

import "errors"

// 错误变量定义
var (
	// ErrNamespaceNotFound 命名空间不存在
	// 当使用无效的命名空间时返回
	ErrNamespaceNotFound = errors.New("namespace not found")

	// ErrKeyNotFound 键不存在
	// 当查询的键在存储中不存在时返回
	ErrKeyNotFound = errors.New("key not found")

	// ErrVersionNotFound 版本不存在
	// 当查询指定版本的键值不存在时返回
	ErrVersionNotFound = errors.New("version not found")

	// ErrInvalidKeyFormat 无效的键格式
	// 当键格式不符合命名空间要求时返回
	ErrInvalidKeyFormat = errors.New("invalid key format")

	// ErrEncodingFailed 编码失败
	// 当 MessagePack 编码失败时返回
	ErrEncodingFailed = errors.New("encoding failed")

	// ErrDecodingFailed 解码失败
	// 当 MessagePack 解码失败时返回
	ErrDecodingFailed = errors.New("decoding failed")

	// ErrStoreClosed 存储已关闭
	// 当尝试访问已关闭的存储时返回
	ErrStoreClosed = errors.New("store closed")

	// ErrInvalidNamespace 无效的命名空间
	// 当命名空间不符合预定义列表时返回
	ErrInvalidNamespace = errors.New("invalid namespace")

	// ErrEmptyKey 空键
	// 当键为空字符串时返回
	ErrEmptyKey = errors.New("empty key")

	// ErrNilValue 空值
	// 当尝试写入 nil 值时返回
	ErrNilValue = errors.New("nil value")

	// ErrConcurrentWrite 并发写入冲突
	// 当检测到并发写入冲突时返回
	ErrConcurrentWrite = errors.New("concurrent write conflict")

	// ErrCompressionFailed 压缩失败
	// 当数据压缩失败时返回
	ErrCompressionFailed = errors.New("compression failed")

	// ErrDecompressionFailed 解压缩失败
	// 当数据解压缩失败时返回
	ErrDecompressionFailed = errors.New("decompression failed")

	// ErrStoreNotInitialized 存储未初始化
	// 当尝试使用未初始化的存储时返回
	ErrStoreNotInitialized = errors.New("store not initialized")
)

// MetadataError 元数据错误接口
type MetadataError interface {
	error
	// Namespace 返回错误发生的命名空间
	Namespace() string
	// Key 返回错误相关的键
	Key() string
	// Code 返回错误代码
	Code() string
}

// metadataError 元数据错误实现
type metadataError struct {
	namespace string
	key       string
	code      string
	message   string
	cause     error
}

// NewMetadataError 创建元数据错误
func NewMetadataError(namespace, key, code, message string, cause error) MetadataError {
	return &metadataError{
		namespace: namespace,
		key:       key,
		code:      code,
		message:   message,
		cause:     cause,
	}
}

// Error 实现 error 接口
func (e *metadataError) Error() string {
	if e.cause != nil {
		return e.message + ": " + e.cause.Error()
	}
	return e.message
}

// Namespace 返回错误发生的命名空间
func (e *metadataError) Namespace() string {
	return e.namespace
}

// Key 返回错误相关的键
func (e *metadataError) Key() string {
	return e.key
}

// Code 返回错误代码
func (e *metadataError) Code() string {
	return e.code
}

// Unwrap 返回底层错误
func (e *metadataError) Unwrap() error {
	return e.cause
}

// 错误代码常量
const (
	ErrCodeNamespaceNotFound  = "NAMESPACE_NOT_FOUND"
	ErrCodeKeyNotFound        = "KEY_NOT_FOUND"
	ErrCodeVersionNotFound    = "VERSION_NOT_FOUND"
	ErrCodeInvalidKeyFormat   = "INVALID_KEY_FORMAT"
	ErrCodeEncodingFailed     = "ENCODING_FAILED"
	ErrCodeDecodingFailed     = "DECODING_FAILED"
	ErrCodeStoreClosed        = "STORE_CLOSED"
	ErrCodeInvalidNamespace   = "INVALID_NAMESPACE"
	ErrCodeEmptyKey           = "EMPTY_KEY"
	ErrCodeNilValue           = "NIL_VALUE"
	ErrCodeConcurrentWrite    = "CONCURRENT_WRITE"
	ErrCodeCompressionFailed  = "COMPRESSION_FAILED"
	ErrCodeDecompressionFailed = "DECOMPRESSION_FAILED"
	ErrCodeStoreNotInitialized = "STORE_NOT_INITIALIZED"
)

// IsNamespaceNotFound 检查是否为命名空间未找到错误
func IsNamespaceNotFound(err error) bool {
	return errors.Is(err, ErrNamespaceNotFound)
}

// IsKeyNotFound 检查是否为键未找到错误
func IsKeyNotFound(err error) bool {
	return errors.Is(err, ErrKeyNotFound)
}

// IsVersionNotFound 检查是否为版本未找到错误
func IsVersionNotFound(err error) bool {
	return errors.Is(err, ErrVersionNotFound)
}

// IsStoreClosed 检查是否为存储已关闭错误
func IsStoreClosed(err error) bool {
	return errors.Is(err, ErrStoreClosed)
}

// IsInvalidKeyFormat 检查是否为无效键格式错误
func IsInvalidKeyFormat(err error) bool {
	return errors.Is(err, ErrInvalidKeyFormat)
}

// IsEncodingFailed 检查是否为编码失败错误
func IsEncodingFailed(err error) bool {
	return errors.Is(err, ErrEncodingFailed)
}

// IsDecodingFailed 检查是否为解码失败错误
func IsDecodingFailed(err error) bool {
	return errors.Is(err, ErrDecodingFailed)
}

// IsInvalidNamespace 检查是否为无效命名空间错误
func IsInvalidNamespace(err error) bool {
	return errors.Is(err, ErrInvalidNamespace)
}
