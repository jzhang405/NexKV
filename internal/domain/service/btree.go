// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package service provides BTree-specific service interfaces.
package service

import (
	"context"
)

// BTree defines the BTree storage interface.
type BTree interface {
	KVStore

	// BTree-specific operations
	GetHeight(ctx context.Context) (int, error)
	GetPageCount(ctx context.Context) (int, error)

	// Debug and monitoring
	DumpTree(ctx context.Context) (string, error)
	Validate(ctx context.Context) error
}

// ==========================================
// BTreeSetItem BTree 异步 SET 任务
// ==========================================
// 注意：BTreeSetItem 的示例实现已移至 concurrency 包
// 详见：internal/infrastructure/concurrency/btree_set_item_example.go
//
// 原因：避免循环导入
