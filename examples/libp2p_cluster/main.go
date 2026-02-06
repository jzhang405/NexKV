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
	"os"
	"os/signal"
	"syscall"

	"github.com/jzhang405/NexKV/internal/config"
	"github.com/jzhang405/NexKV/internal/transport"
	"github.com/libp2p/go-libp2p/core/peer"
)

func main() {
	ctx := context.Background()

	// 加载配置
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}

	// 创建 P2P Service（使用 Transport 工厂）
	p2pService, err := transport.NewTransportFromConfig(&cfg.Transport)
	if err != nil {
		log.Fatalf("创建 P2P Service 失败: %v", err)
	}

	// 启动服务
	if err := p2pService.Start(ctx); err != nil {
		log.Fatalf("启动 P2P Service 失败: %v", err)
	}
	log.Printf("✅ P2P Service 启动成功，监听地址: %s", p2pService.PeerID())

	// 注册消息处理器（可选）
	p2pService.Protocol().RegisterHandler(
		transport.MessageTypeSync,
		transport.MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *transport.Message) error {
			log.Printf("收到消息 from %s: type=%d", from, msg.Type)
			return nil
		}),
	)

	// 等待信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("正在关闭 P2P Service...")
	if err := p2pService.Close(); err != nil {
		log.Printf("关闭 P2P Service 失败: %v", err)
	}

	log.Println("P2P Service 已关闭")
}
