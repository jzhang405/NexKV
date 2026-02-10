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

	// 原始字节访问接口（用于元数据同步）
	GetRaw(ctx context.Context, ns, key string) ([]byte, error)
	PutRaw(ctx context.Context, ns, key string, data []byte) error
	BatchGetRaw(ctx context.Context, ns string, keys []string) (map[string][]byte, error)
}
