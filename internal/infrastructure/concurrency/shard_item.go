// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import "github.com/jzhang405/NexKV/internal/domain/model"

// ShardItem 带重试控制、按 CPU 核心分片的任务项接口
//
// ShardID 的核心目的是通过资源亲和性（Resource Affinity）减少锁竞争，提高并发性能。
//
// ShardID 值的语义：
//   - shardID > 0：固定路由到 (shardID % coreCount) 对应的 Core
//     例如：leaf-lock 地址 % coreCount 确保同一 leaf 在同一 Core
//   - shardID = 0：无资源亲和性要求，动态选择负载最小的 Core
//     适用于：纯计算任务、无共享状态的操作
//   - shardID < 0：取绝对值后路由（备用方案）
//
// ShardItem 通过接口组合（Interface Embedding）获得完整能力：
//   - 嵌入 model.TaskRunner：获得任务执行能力（Run, Priority, SourceID）
//   - 嵌入 model.TaskResult：获得结果查询能力（Done, Wait, Status, IsDone, GetError）
//   - 自定义方法：ShardID, MaxRetries, IncAttempts
type ShardItem interface {
	// ===== 接口组合：TaskRunner（调度能力）=====
	model.TaskRunner // 嵌入：Run, Priority, SourceID

	// ===== 接口组合：TaskResult（结果能力）=====
	model.TaskResult // 嵌入：Done, Wait, Status, IsDone, GetError

	// ===== ShardItem 特有能力（重试控制）=====

	// ShardID 返回分片 ID，用于路由到对应的 runLoop
	//
	// 使用场景：
	//   - B-Tree SET：返回 leaf-lock 地址，确保同一 leaf 的操作在同一 Core
	//   - WAL：返回 wal_id（wal_{wal_id}.WAL），确保同一 WAL 文件的 append 操作在同一 Core
	//   - AO 文件：返回 ao_id（ao_{ao_id}.XXX），确保同一 AO 文件的操作在同一 Core
	//   - 通用计算：返回用户定义的业务 ID，通过取模固定路由到特定 Core
	ShardID() int

	// MaxRetries 返回最大重试次数（0 表示不重试）
	MaxRetries() int

	// IncAttempts 增加尝试次数并返回当前次数
	// 返回值 > MaxRetries() 时表示已超过最大重试次数
	IncAttempts() int

	// TaskOrder 返回任务执行顺序（executionOrder）
	//
	// Deprecated: 路由已改用 taskMap[taskName] 查找（O(1) map lookup），
	// 此方法仅在调试/日志场景保留。新代码不应依赖此方法做路由决策。
	TaskOrder() int
}

// BatchShardItem 可批量处理的 ShardItem
//
// 用于 TaskScheduler 批量优化场景，允许任务声明其批量处理偏好。
// TaskScheduler 可以根据此接口动态调整批量大小，优化队列操作开销。
//
// 设计原则：
// - 每个 TaskQueue 通过 PreferredBatchSize() 决定自己的批量大小
// - 批量策略："有多少 batch 多少"（min(batchSize, queueLen)）
// - 至少 2 个才批量，否则使用单个处理
type BatchShardItem interface {
	ShardItem

	// BatchType 返回批量类型标识
	// 用于 TaskScheduler 识别可批量处理的任务类型
	// 例如："btree-set", "btree-get", "wal-append"
	BatchType() string

	// PreferredBatchSize 返回建议的批量大小
	// 每个任务类型可以有自己的最优批量大小
	// 返回值将作为 TaskScheduler 的批量上限
	// 实际批量大小 = min(PreferredBatchSize(), queueLen)
	PreferredBatchSize() int
}
