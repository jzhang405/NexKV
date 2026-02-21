// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package scenarios 提供集成测试场景实现
package scenarios

import "time"

// 集成测试通用时间常量
// 消除魔法数字，提高代码可读性和可维护性
const (
	// ============================================================
	// 连接相关常量
	// ============================================================

	// ConnectionSetupWait 连接建立后的等待时间
	// 用于确保异步连接操作完成
	ConnectionSetupWait = 100 * time.Millisecond

	// ConnectionSetupWaitShort 短连接等待时间
	// 用于快速验证场景（如 IsBlocked 检查）
	ConnectionSetupWaitShort = 50 * time.Millisecond

	// MeshConnectionWait 全连接 Mesh 建立等待时间
	// 用于多节点互联场景
	MeshConnectionWait = 200 * time.Millisecond

	// ============================================================
	// 网络分区相关常量
	// ============================================================

	// PartitionStabilizeWait 分区生效等待时间
	// 用于等待网络分区操作完成并稳定
	PartitionStabilizeWait = 200 * time.Millisecond

	// ============================================================
	// mDNS 发现相关常量
	// ============================================================

	// MDNSDiscoveryTimeout mDNS 节点发现超时时间
	// mDNS 发现可能需要较长时间
	MDNSDiscoveryTimeout = 10 * time.Second

	// MDNSDiscoveryPollInterval mDNS 发现轮询间隔
	// 定期检查节点是否发现其他节点
	MDNSDiscoveryPollInterval = 500 * time.Millisecond

	// MDNSDirectDiscoveryWait mDNS 直接发现等待时间
	// 用于直接测试 mDNS 发现功能
	MDNSDirectDiscoveryWait = 5 * time.Second

	// RediscoverWait 重新发现等待时间
	// 用于网络分区恢复后的重新发现
	RediscoverWait = 2 * time.Second

	// ============================================================
	// 测试上下文超时常量
	// ============================================================

	// DefaultTestTimeout 默认测试超时时间
	// 适用于大多数集成测试场景
	DefaultTestTimeout = 30 * time.Second

	// LongTestTimeout 长时间测试超时时间
	// 适用于网络分区、重连等需要较长时间的场景
	LongTestTimeout = 60 * time.Second

	// NetworkOperationTimeout 网络操作超时时间
	// 用于带超时的网络操作上下文
	NetworkOperationTimeout = 5 * time.Second
)
