// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

const (
	// splitThreshold 触发分裂的键数量阈值
	splitThreshold = 128

	// maxInternalKeys 内部节点最大键数量
	maxInternalKeys = 256

	// InitialLeafCapacity 叶子节点初始容量
	// ✅ Phase 3 优化：增大初始容量，减少扩容次数，降低 GC 压力
	// 从 16 → 128（与 splitThreshold 对齐），避免多次扩容
	InitialLeafCapacity = 128

	// InitialInternalCapacity 内部节点初始容量
	InitialInternalCapacity = 16

	// DefaultSliceCapacity 默认切片容量
	// ✅ Phase 3 优化：从 8 → 32，减少扩容次数
	DefaultSliceCapacity = 32

	// DefaultChildSlots 默认子节点槽数量
	DefaultChildSlots = 17
)
