// Package types 定义元数据同步消息类型
//
// 架构优化（P2-3）：将元数据同步消息类型从 internal/rpc 迁移到 types 包
// 修复依赖方向：cluster → rpc 改为 cluster → types, rpc → types
package types

import "time"

// ========================================
// 元数据同步消息
// ========================================

// MetadataSyncRequest 元数据同步请求
//
// 用于节点间同步元数据变更
type MetadataSyncRequest struct {
	// Namespace 命名空间（meta:node、meta:role 等）
	Namespace string `msgpack:"namespace"`

	// Keys 需要同步的键列表
	Keys []string `msgpack:"keys"`

	// Version 版本号
	Version uint64 `msgpack:"version"`

	// Timestamp 时间戳
	Timestamp int64 `msgpack:"timestamp"`
}

// MetadataSyncResponse 元数据同步响应
type MetadataSyncResponse struct {
	// Namespace 命名空间
	Namespace string `msgpack:"namespace"`

	// Metadata 元数据（键值对）
	Metadata map[string][]byte `msgpack:"metadata"`

	// Version 响应版本号
	Version uint64 `msgpack:"version"`

	// Timestamp 时间戳
	Timestamp int64 `msgpack:"timestamp"`
}

// MetadataChangeNotification 元数据变更通知
//
// 用于通知其他节点元数据发生变更
type MetadataChangeNotification struct {
	// Namespace 命名空间
	Namespace string `msgpack:"namespace"`

	// Key 变更的键
	Key string `msgpack:"key"`

	// Operation 操作类型（put、delete）
	Operation string `msgpack:"operation"`

	// Version 版本号
	Version uint64 `msgpack:"version"`

	// Timestamp 时间戳
	Timestamp int64 `msgpack:"timestamp"`
}

// ========================================
// 便捷构造函数
// ========================================

// NewMetadataSyncRequest 创建元数据同步请求
func NewMetadataSyncRequest(namespace string, keys []string, version uint64) *MetadataSyncRequest {
	return &MetadataSyncRequest{
		Namespace: namespace,
		Keys:      keys,
		Version:   version,
		Timestamp: time.Now().UnixNano(),
	}
}

// NewMetadataSyncResponse 创建元数据同步响应
func NewMetadataSyncResponse(namespace string, metadata map[string][]byte, version uint64) *MetadataSyncResponse {
	return &MetadataSyncResponse{
		Namespace: namespace,
		Metadata:  metadata,
		Version:   version,
		Timestamp: time.Now().UnixNano(),
	}
}

// NewMetadataChangeNotification 创建元数据变更通知
func NewMetadataChangeNotification(namespace, key, operation string, version uint64) *MetadataChangeNotification {
	return &MetadataChangeNotification{
		Namespace: namespace,
		Key:       key,
		Operation: operation,
		Version:   version,
		Timestamp: time.Now().UnixNano(),
	}
}
