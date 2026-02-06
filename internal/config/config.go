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

package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config NexKV 配置
type Config struct {
	P2P       P2PConfig       `yaml:"p2p"`       // P2P 配置（简化版）
	Cluster   ClusterConfig   `yaml:"cluster"`   // 集群配置
	Transport TransportConfig `yaml:"transport"` // Transport 配置（完整版）
}

// ClusterConfig 集群配置
type ClusterConfig struct {
	NodeID uint64 `yaml:"node_id"`
}

// P2PConfig P2P 配置
type P2PConfig struct {
	ListenAddr     string          `yaml:"listen_addr"`
	PrivateKeyPath string          `yaml:"private_key_path"`
	BootstrapPeers []string        `yaml:"bootstrap_peers"`
	Discovery      DiscoveryConfig `yaml:"discovery"`
	MaxConnections int             `yaml:"max_connections"`
	ConnTimeout    time.Duration   `yaml:"conn_timeout"`
}

// DiscoveryConfig 节点发现配置
type DiscoveryConfig struct {
	MDNSEnabled bool `yaml:"mdns_enabled"`
	DHTEnabled  bool `yaml:"dht_enabled"`
}

// LoadConfig 从 YAML 文件加载配置
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 设置默认值
	setDefaults(&cfg)

	// 验证配置
	if err := ValidateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	return &cfg, nil
}

// setDefaults 设置默认值
func setDefaults(cfg *Config) {
	// 默认启用 mDNS 和 DHT
	if !cfg.P2P.Discovery.MDNSEnabled && !cfg.P2P.Discovery.DHTEnabled {
		cfg.P2P.Discovery.MDNSEnabled = true
		cfg.P2P.Discovery.DHTEnabled = true
	}

	// 默认连接超时 10 秒
	if cfg.P2P.ConnTimeout == 0 {
		cfg.P2P.ConnTimeout = 10 * time.Second
	}

	// 默认最大连接数
	if cfg.P2P.MaxConnections == 0 {
		cfg.P2P.MaxConnections = 200
	}
}

// ValidateConfig 验证配置
func ValidateConfig(cfg *Config) error {
	// 验证必填字段
	if cfg.P2P.ListenAddr == "" {
		return fmt.Errorf("p2p.listen_addr 是必填字段")
	}
	if cfg.P2P.PrivateKeyPath == "" {
		return fmt.Errorf("p2p.private_key_path 是必填字段")
	}

	// 验证 multiaddr 格式
	if !isValidMultiaddr(cfg.P2P.ListenAddr) {
		return fmt.Errorf("p2p.listen_addr 格式错误: %s (应为 multiaddr 格式，如 /ip4/0.0.0.0/tcp/4001)", cfg.P2P.ListenAddr)
	}

	// 验证 bootstrap peers
	for i, peer := range cfg.P2P.BootstrapPeers {
		if !isValidMultiaddr(peer) {
			return fmt.Errorf("p2p.bootstrap_peers[%d] 格式错误: %s", i, peer)
		}
	}

	return nil
}

// isValidMultiaddr 验证 multiaddr 格式
func isValidMultiaddr(addr string) bool {
	// 简单验证：检查是否以 / 开头（multiaddr 特征）
	if len(addr) == 0 {
		return false
	}
	return addr[0] == '/'
}

// LoadConfigFromEnv 从环境变量加载配置
func LoadConfigFromEnv() (*Config, error) {
	cfg := &Config{}

	// 从环境变量读取
	if listenAddr := os.Getenv("NEXKV_P2P_LISTEN_ADDR"); listenAddr != "" {
		cfg.P2P.ListenAddr = listenAddr
	}
	if keyPath := os.Getenv("NEXKV_P2P_PRIVATE_KEY_PATH"); keyPath != "" {
		cfg.P2P.PrivateKeyPath = keyPath
	}

	// 设置默认值
	setDefaults(cfg)

	// 验证配置
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// LoadConfigWithEnvOverride 从 YAML 加载配置，环境变量覆盖
func LoadConfigWithEnvOverride(path string) (*Config, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}

	// 环境变量覆盖
	if listenAddr := os.Getenv("NEXKV_P2P_LISTEN_ADDR"); listenAddr != "" {
		cfg.P2P.ListenAddr = listenAddr
	}

	return cfg, nil
}
