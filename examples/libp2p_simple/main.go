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

package main

import (
	"context"
	"log"
	"time"

	"github.com/jzhang405/NexKV/internal/transport"
)

func main() {
	ctx := context.Background()

	// 创建 P2P Service（使用默认配置）
	p2pService, err := transport.NewP2PService(transport.DefaultP2PServiceConfig("0.0.0.0:4001", "/tmp/libp2p_key"))
	if err != nil {
		log.Fatal(err)
	}

	// 启动服务
	if err := p2pService.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Printf("✅ P2P Service 启动成功，节点 ID: %s", p2pService.PeerID())

	// 等待连接建立
	time.Sleep(5 * time.Second)

	// 获取已连接的对等节点
	peers := p2pService.Host().Network().Peers()
	log.Printf("已连接的对等节点数量: %d", len(peers))
	for _, p := range peers {
		log.Printf("  - %s", p)
	}

	// 清理资源
	if err := p2pService.Close(); err != nil {
		log.Printf("关闭失败: %v", err)
	}
}
