// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

const (
	// splitThreshold 触发分裂的键数量阈值
	// 优化：与 maxKeys 对齐，确保分裂检查一致性
	splitThreshold = 200

	// maxInternalKeys 内部节点最大键数量
	maxInternalKeys = 256

	// InitialLeafCapacity 叶子节点初始容量
	// 优化：增大初始容量，减少扩容次数，降低 GC 压力
	// 更新：从 128 → 200（与 splitThreshold 对齐）
	InitialLeafCapacity = 200

	// InitialInternalCapacity 内部节点初始容量
	InitialInternalCapacity = 16

	// DefaultSliceCapacity 默认切片容量
	// 优化：从 8 → 32 → 256，大幅减少扩容次数和 GC 压力
	// 分析显示 insertSlice 占 51% 内存分配，主要原因是频繁扩容
	DefaultSliceCapacity = 256

	// DefaultChildSlots 默认子节点槽数量
	DefaultChildSlots = 17
)
