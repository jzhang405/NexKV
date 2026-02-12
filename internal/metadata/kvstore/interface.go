// Package kvstore 定义元数据存储接口
//
// 遵循依赖倒置原则：接口由提供者（kvstore）定义，
// 而不是由使用者（cluster）定义。
package kvstore

import "context"

// Store 元数据存储接口
//
// 定义了元数据 KV 存储的核心操作，包括：
//   - 基础 CRUD 操作
//   - 批量操作
//   - 原始字节访问（用于网络传输）
type Store interface {
	// Put 写入键值对
	Put(ctx context.Context, ns, key string, value any) error

	// Get 获取键值
	Get(ctx context.Context, ns, key string, value any) error

	// Delete 删除键
	Delete(ctx context.Context, ns, key string) error

	// Exists 检查键是否存在
	Exists(ctx context.Context, ns, key string) (bool, error)

	// ListPrefix 列出指定前缀的键
	ListPrefix(ctx context.Context, ns, prefix string) ([]string, error)

	// Close 关闭存储
	Close() error

	// GetRaw 获取原始字节数据
	//
	// 用途：元数据网络传输优化，避免二次编解码开销
	// 调用层：RPC 层元数据同步
	// 设计决策：虽然职责不纯粹，但有实际性能价值（避免 Object→[]byte→Network→[]byte→Object）
	// 注意：返回的是存储层的原始字节（通常为 MessagePack 格式）
	GetRaw(ctx context.Context, ns, key string) ([]byte, error)

	// PutRaw 写入原始字节数据
	//
	// 用途：元数据网络传输优化，避免二次编解码开销
	// 调用层：RPC 层元数据同步
	// 设计决策：虽然职责不纯粹，但有实际性能价值（避免 Object→[]byte→Network→[]byte→Object）
	// 注意：直接存储字节，跳过对象编解码
	PutRaw(ctx context.Context, ns, key string, data []byte) error

	// BatchGetRaw 批量获取原始字节数据
	//
	// 用途：元数据网络传输优化，避免二次编解码开销
	// 调用层：RPC 层元数据同步
	// 设计决策：虽然职责不纯粹，但有实际性能价值（避免 Object→[]byte→Network→[]byte→Object）
	// 注意：返回 map[key]rawBytes，跳过对象反序列化
	BatchGetRaw(ctx context.Context, ns string, keys []string) (map[string][]byte, error)
}
