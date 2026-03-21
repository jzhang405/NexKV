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
}
