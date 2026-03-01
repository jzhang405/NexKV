// Package constants 提供跨包共享的常量定义
package constants

// ==========================================
// 网络消息大小限制
// ==========================================

const (
	// DefaultMaxMessageSize 默认最大消息大小 (10MB)
	DefaultMaxMessageSize = 10 * 1024 * 1024

	// MaxMessageSizeHardLimit 硬限制 (100MB)
	MaxMessageSizeHardLimit = 100 * 1024 * 1024

	// DefaultBufferSize 默认缓冲区大小 (4KB)
	DefaultBufferSize = 4096

	// LengthPrefixSize 长度前缀大小 (4字节)
	LengthPrefixSize = 4
)

// ==========================================
// 网络地址限制
// ==========================================

const (
	// MaxPeerIDLength PeerID 最大长度
	MaxPeerIDLength = 128

	// MaxAddrLength 地址最大长度
	MaxAddrLength = 1024
)

// ==========================================
// 压缩相关常量
// ==========================================

const (
	// DefaultMaxDecompressedSize 默认最大解压缩大小 (10MB)
	DefaultMaxDecompressedSize = 10 * 1024 * 1024
)
