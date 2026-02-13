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

package transport

import "time"

// 连接管理常量
const (
	// DefaultLowWater 默认连接管理器低水位
	DefaultLowWater = 100
	// DefaultHighWater 默认连接管理器高水位
	DefaultHighWater = 400
	// GracePeriod 连接优雅关闭周期
	GracePeriod = time.Minute
)

// 超时时间常量
const (
	// DefaultConnectTimeout 默认连接超时时间
	DefaultConnectTimeout = 30 * time.Second
	// BootstrapConnectTimeout Bootstrap 连接超时时间
	BootstrapConnectTimeout = 10 * time.Second
	// StreamReadTimeout Stream 读取超时时间
	StreamReadTimeout = 30 * time.Second
	// StreamWriteTimeout Stream 写入超时时间
	StreamWriteTimeout = 10 * time.Second
	// DiscoveryConnectTimeout 发现服务连接超时时间
	DiscoveryConnectTimeout = 10 * time.Second
	// DefaultDiscoveryTag 默认发现服务标签
	DefaultDiscoveryTag = "nexkv-discovery"
)

// Bootstrap 等待常量
const (
	// BootstrapCheckInterval Bootstrap 状态检查间隔
	BootstrapCheckInterval = 100 * time.Millisecond
)

// 消息大小常量
const (
	// MaxMessageSize 最大消息大小（10MB）
	MaxMessageSize = 10 * 1024 * 1024
	// MaxConcurrentBroadcasts 最大并发广播数（防止 DoS）
	MaxConcurrentBroadcasts = 50
)

// 协议版本常量
const (
	// ProtocolVersion 协议版本号
	ProtocolVersion = "1.0.0"
)
