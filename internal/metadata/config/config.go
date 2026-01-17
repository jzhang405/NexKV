// Package config 提供元数据层的配置管理
package config

import "time"

// Config 是元数据层的完整配置结构
type Config struct {
	Cluster  ClusterConfig  `yaml:"cluster" mapstructure:"cluster"`
	Metadata MetadataConfig `yaml:"metadata" mapstructure:"metadata"`
	Storage  StorageConfig  `yaml:"storage" mapstructure:"storage"`
	Network  NetworkConfig  `yaml:"network" mapstructure:"network"`
	Logging  LoggingConfig  `yaml:"logging" mapstructure:"logging"`
	Clock    ClockConfig    `yaml:"clock" mapstructure:"clock"`
}

// ClusterConfig 集群配置
type ClusterConfig struct {
	Name     string `yaml:"name" mapstructure:"name"`
	NodeID   string `yaml:"node_id" mapstructure:"node_id"`
	NodeAddr string `yaml:"node_addr" mapstructure:"node_addr"`

	// 树形分组配置
	TreeCoord TreeCoordConfig `yaml:"tree_coord" mapstructure:"tree_coord"`
}

// TreeCoordConfig 树形协调器配置
type TreeCoordConfig struct {
	MaxChildren int `yaml:"max_children" mapstructure:"max_children"` // 每个父节点最多子节点数（5-10，可配置）
	GroupSize   int `yaml:"group_size" mapstructure:"group_size"`     // 组大小（5-10，可配置）
}

// MetadataConfig 元数据层配置
type MetadataConfig struct {
	DataDir        string        `yaml:"data_dir" mapstructure:"data_dir"`
	GossipInterval time.Duration `yaml:"gossip_interval" mapstructure:"gossip_interval"`
	QuorumTimeout  time.Duration `yaml:"quorum_timeout" mapstructure:"quorum_timeout"`
	ChangeLogSize  int           `yaml:"change_log_size" mapstructure:"change_log_size"`
}

// StorageConfig 存储层配置
type StorageConfig struct {
	ShardDataDir  string        `yaml:"shard_data_dir" mapstructure:"shard_data_dir"`
	WALDir        string        `yaml:"wal_dir" mapstructure:"wal_dir"`
	SnapshotDir   string        `yaml:"snapshot_dir" mapstructure:"snapshot_dir"`
	FlushInterval time.Duration `yaml:"flush_interval" mapstructure:"flush_interval"`
}

// NetworkConfig 网络层配置
type NetworkConfig struct {
	ListenAddr      string        `yaml:"listen_addr" mapstructure:"listen_addr"`
	TransportType   string        `yaml:"transport_type" mapstructure:"transport_type"` // tcp, grpc, memory
	MessagePackType string        `yaml:"message_pack_type" mapstructure:"message_pack_type"`
	MaxMessageSize  int           `yaml:"max_message_size" mapstructure:"max_message_size"`
	ConnectTimeout  time.Duration `yaml:"connect_timeout" mapstructure:"connect_timeout"`
}

// LoggingConfig 日志配置
type LoggingConfig struct {
	Level  string `yaml:"level" mapstructure:"level"`
	Format string `yaml:"format" mapstructure:"format"` // json, text
	Output string `yaml:"output" mapstructure:"output"` // stdout, file
	File   string `yaml:"file" mapstructure:"file"`     // 日志文件路径
}

// ClockConfig 时钟服务配置
type ClockConfig struct {
	// HLC 配置
	HLC HLCConfig `yaml:"hlc" mapstructure:"hlc"`
}

// HLCConfig HLC 混合逻辑时钟配置
type HLCConfig struct {
	// 时间回拨检测阈值（毫秒）
	MaxDrift int64 `yaml:"max_drift" mapstructure:"max_drift"`
	// Gossip 时钟同步间隔
	SyncInterval time.Duration `yaml:"sync_interval" mapstructure:"sync_interval"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Cluster: ClusterConfig{
			Name:     "nexkv-cluster",
			NodeID:   "node-1",
			NodeAddr: "127.0.0.1:9211",
			TreeCoord: TreeCoordConfig{
				MaxChildren: 10,
				GroupSize:   5,
			},
		},
		Metadata: MetadataConfig{
			DataDir:        "./data/metadata",
			GossipInterval: 10 * time.Second,
			QuorumTimeout:  30 * time.Second,
			ChangeLogSize:  1000,
		},
		Storage: StorageConfig{
			ShardDataDir:  "./data/shards",
			WALDir:        "./data/wal",
			SnapshotDir:   "./data/snapshots",
			FlushInterval: 5 * time.Second,
		},
		Network: NetworkConfig{
			ListenAddr:      "0.0.0.0:9211",
			TransportType:   "tcp",
			MessagePackType: "msgpack",
			MaxMessageSize:  10 * 1024 * 1024, // 10MB
			ConnectTimeout:  10 * time.Second,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
			File:   "",
		},
		Clock: ClockConfig{
			HLC: HLCConfig{
				MaxDrift:     100, // 100ms
				SyncInterval: 10 * time.Second,
			},
		},
	}
}
