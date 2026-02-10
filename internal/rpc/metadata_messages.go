// Package rpc 元数据同步 RPC 消息定义
//
// 定义元数据 Gossip 同步的消息类型
package rpc

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
// 辅助构造函数
// ========================================

// NewMetadataSyncRequest 创建元数据同步请求
func NewMetadataSyncRequest(namespace string, keys []string, version uint64) *MetadataSyncRequest {
	return &MetadataSyncRequest{
		Namespace: namespace,
		Keys:      keys,
		Version:   version,
		Timestamp: nowTimestamp(),
	}
}

// NewMetadataChangeNotification 创建元数据变更通知
func NewMetadataChangeNotification(namespace, key, operation string, version uint64) *MetadataChangeNotification {
	return &MetadataChangeNotification{
		Namespace: namespace,
		Key:       key,
		Operation: operation,
		Version:   version,
		Timestamp: nowTimestamp(),
	}
}
