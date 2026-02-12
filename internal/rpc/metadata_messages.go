// Package rpc 元数据同步 RPC 消息定义
//
// 架构优化（P2-3）：使用类型别名引用 internal/metadata/types 中的消息定义
// 修复依赖方向：rpc → types（符合分层架构）
package rpc

import (
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// 元数据同步消息类型别名
// ========================================
//
// 以下类型别名指向 internal/metadata/types 中的定义
// 保持向后兼容的同时，修正依赖方向

// MetadataSyncRequest 元数据同步请求
type MetadataSyncRequest = types.MetadataSyncRequest

// MetadataSyncResponse 元数据同步响应
type MetadataSyncResponse = types.MetadataSyncResponse

// MetadataChangeNotification 元数据变更通知
type MetadataChangeNotification = types.MetadataChangeNotification

// ========================================
// 辅助构造函数（保持向后兼容）
// ========================================

// NewMetadataSyncRequest 创建元数据同步请求
func NewMetadataSyncRequest(namespace string, keys []string, version uint64) *MetadataSyncRequest {
	return &types.MetadataSyncRequest{
		Namespace: namespace,
		Keys:      keys,
		Version:   version,
		Timestamp: time.Now().UnixNano(),
	}
}

// NewMetadataChangeNotification 创建元数据变更通知
func NewMetadataChangeNotification(namespace, key, operation string, version uint64) *MetadataChangeNotification {
	return &types.MetadataChangeNotification{
		Namespace: namespace,
		Key:       key,
		Operation: operation,
		Version:   version,
		Timestamp: time.Now().UnixNano(),
	}
}
