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

// TransportConfig Transport 配置
type TransportConfig struct {
	Type   string        `yaml:"type"` // libp2p
	Libp2p *Libp2pConfig `yaml:"libp2p"`
}

// TCPConfig TCP 配置（向后兼容）
type TCPConfig struct {
	ListenAddr string `yaml:"listen_addr"`
}

// UDPConfig UDP 配置（向后兼容）
type UDPConfig struct {
	ListenAddr string `yaml:"listen_addr"`
}

// Libp2pConfig libp2p 配置
type Libp2pConfig struct {
	ListenPort        int                  `yaml:"listen_port"`
	ListenAddr        string               `yaml:"listen_addr"`
	AnnounceAddrs     []string             `yaml:"announce_addrs"`
	PrivateKeyPath    string               `yaml:"private_key_path"`
	ConnectionManager *ConnectionMgrConfig `yaml:"connection_manager"`
	Discovery         *DiscoveryConfig     `yaml:"discovery"`
	Bootstrap         []string             `yaml:"bootstrap"`
}

// ConnectionMgrConfig 连接管理器配置
type ConnectionMgrConfig struct {
	LowWater  int `yaml:"low_water"`
	HighWater int `yaml:"high_water"`
}

// DiscoveryConfig 节点发现配置
type DiscoveryConfig struct {
	MDNSEnabled bool `yaml:"mdns_enabled"`
	DHTEnabled  bool `yaml:"dht_enabled"`
}
