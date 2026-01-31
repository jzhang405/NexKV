// Package config 提供元数据层的配置管理
//
// PR-037: 三级配置重构（Cluster → Host → Node）
// - Cluster: 集群级别配置（名称、基础目录）
// - Host: 主机级别配置（每个物理机/容器一个 Host）
// - Node: 节点级别配置（角色由系统动态分配）
package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"gopkg.in/yaml.v3"
)

// Config 是元数据层的完整配置结构
type Config struct {
	Cluster  ClusterConfig  `yaml:"cluster" mapstructure:"cluster"`
	Metadata MetadataConfig `yaml:"metadata" mapstructure:"metadata"`
	Storage  StorageConfig  `yaml:"storage" mapstructure:"storage"`
	Network  NetworkConfig  `yaml:"network" mapstructure:"network"`
	Logging  LoggingConfig  `yaml:"logging" mapstructure:"logging"`
	Clock    ClockConfig    `yaml:"clock" mapstructure:"clock"`
}

// ClusterConfig 集群配置（PR-037: 三级配置结构）
type ClusterConfig struct {
	Name    string       `yaml:"name" mapstructure:"name"`
	BaseDir string       `yaml:"base_dir" mapstructure:"base_dir"` // 可被 NEXKV_BASE_DIR 环境变量覆盖
	Hosts   []HostConfig `yaml:"hosts" mapstructure:"hosts"`       // Host 级别配置列表
}

// HostConfig Host 级别配置（PR-037: 新增）
type HostConfig struct {
	HostID   string       `yaml:"host_id" mapstructure:"host_id"`     // Host 唯一标识
	SeedNode string       `yaml:"seed_node" mapstructure:"seed_node"` // 种子节点地址（multiaddr 格式）
	Nodes    []NodeConfig `yaml:"nodes" mapstructure:"nodes"`         // Node 级别配置列表
}

// NodeConfig Node 级别配置（PR-037: 新增）
type NodeConfig struct {
	NodeID      string `yaml:"node_id" mapstructure:"node_id"`             // Node 唯一标识
	NodeAddrTCP string `yaml:"node_addr_tcp" mapstructure:"node_addr_tcp"` // TCP 监听地址（multiaddr 格式）
	NodeAddrUDP string `yaml:"node_addr_udp" mapstructure:"node_addr_udp"` // UDP 监听地址（multiaddr 格式）
}

// TreeCoordConfig 树形协调器配置
type TreeCoordConfig struct {
	MaxChildren int `yaml:"max_children" mapstructure:"max_children"` // 每个父节点最多子节点数（5-10，可配置）
	GroupSize   int `yaml:"group_size" mapstructure:"group_size"`     // 组大小（5-10，可配置）
}

// MetadataConfig 元数据层配置
// PR-037: data_dir 现在由 {base_dir}/{host_id}/metadata 自动管理，此处仅配置行为参数
type MetadataConfig struct {
	// 注意：data_dir 已废弃，由 {base_dir}/{host_id}/metadata 自动管理
	// 保留此字段仅为兼容旧配置文件，新配置不应设置此字段
	DataDir        string        `yaml:"data_dir" mapstructure:"data_dir"` // 已废弃
	GossipInterval time.Duration `yaml:"gossip_interval" mapstructure:"gossip_interval"`
	QuorumTimeout  time.Duration `yaml:"quorum_timeout" mapstructure:"quorum_timeout"`
	ChangeLogSize  int           `yaml:"change_log_size" mapstructure:"change_log_size"`
}

// StorageConfig 存储层配置
// PR-037: 目录路径现在由 {base_dir}/{host_id}/{shards|wal|snapshots} 自动管理
type StorageConfig struct {
	// 注意：以下目录已废弃，由 {base_dir}/{host_id}/ 自动管理
	// 保留这些字段仅为兼容旧配置文件，新配置不应设置这些字段
	ShardDataDir  string        `yaml:"shard_data_dir" mapstructure:"shard_data_dir"` // 已废弃
	WALDir        string        `yaml:"wal_dir" mapstructure:"wal_dir"`               // 已废弃
	SnapshotDir   string        `yaml:"snapshot_dir" mapstructure:"snapshot_dir"`     // 已废弃
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
			Name:    "nexkv-cluster",
			BaseDir: "~/.nexkv", // 默认基础目录，可被 NEXKV_BASE_DIR 环境变量覆盖
			Hosts: []HostConfig{
				{
					HostID:   "host-1",
					SeedNode: "/ip4/127.0.0.1/tcp/9211",
					Nodes: []NodeConfig{
						{
							NodeID:      "node-1",
							NodeAddrTCP: "/ip4/127.0.0.1/tcp/9211",
							NodeAddrUDP: "/ip4/127.0.0.1/udp/9212",
						},
					},
				},
			},
		},
		Metadata: MetadataConfig{
			// DataDir 已废弃，由 {base_dir}/{host_id}/metadata 自动管理
			GossipInterval: 10 * time.Second,
			QuorumTimeout:  30 * time.Second,
			ChangeLogSize:  1000,
		},
		Storage: StorageConfig{
			// ShardDataDir, WALDir, SnapshotDir 已废弃，由 {base_dir}/{host_id}/ 自动管理
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

// LoadConfig 从文件加载配置
func LoadConfig(path string) (*Config, error) {
	// 检查文件是否存在
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// 文件不存在，返回默认配置
		cfg := DefaultConfig()
		// 应用环境变量覆盖
		applyEnvOverrides(cfg)
		return cfg, nil
	}

	// 读取配置文件
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, types.NewConfigReadFileError(err)
	}

	// 解析 YAML
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, types.NewConfigParseFileError(err)
	}

	// 应用环境变量覆盖
	applyEnvOverrides(cfg)

	// 验证配置
	if err := ValidateConfig(cfg); err != nil {
		return nil, types.NewConfigValidateFileError(err)
	}

	return cfg, nil
}

// applyEnvOverrides 应用环境变量覆盖
func applyEnvOverrides(cfg *Config) {
	// NEXKV_BASE_DIR 环境变量优先级最高
	if baseDir := os.Getenv("NEXKV_BASE_DIR"); baseDir != "" {
		// 展开波浪号
		if strings.HasPrefix(baseDir, "~/") {
			if homeDir, err := os.UserHomeDir(); err == nil {
				baseDir = filepath.Join(homeDir, baseDir[2:])
			}
		}
		cfg.Cluster.BaseDir = baseDir
	} else {
		// 展开配置中的波浪号
		if strings.HasPrefix(cfg.Cluster.BaseDir, "~/") {
			if homeDir, err := os.UserHomeDir(); err == nil {
				cfg.Cluster.BaseDir = filepath.Join(homeDir, cfg.Cluster.BaseDir[2:])
			}
		}
	}
}

// ValidateConfig 验证配置有效性
func ValidateConfig(cfg *Config) error {
	validators := []struct {
		name string
		fn   func(*Config) error
	}{
		{"集群配置", validateClusterConfigWrapper},
		{"元数据配置", validateMetadataConfigWrapper},
		{"存储配置", validateStorageConfigWrapper},
		{"网络配置", validateNetworkConfigWrapper},
		{"日志配置", validateLoggingConfigWrapper},
		{"时钟配置", validateClockConfigWrapper},
	}

	for _, v := range validators {
		if err := v.fn(cfg); err != nil {
			return err
		}
	}

	return nil
}

// validateClusterConfigWrapper 验证集群配置（PR-037: 三级配置结构）
// P1-4 修复：使用 early return 优化性能，减少不必要的验证
func validateClusterConfigWrapper(cfg *Config) error {
	if cfg.Cluster.Name == "" {
		return types.NewConfigClusterNameEmptyError()
	}
	if cfg.Cluster.BaseDir == "" {
		return types.NewConfigBaseDirEmptyError()
	}
	if len(cfg.Cluster.Hosts) == 0 {
		return types.NewConfigHostsEmptyError()
	}

	// 验证每个 Host 配置
	for i, host := range cfg.Cluster.Hosts {
		// Early return: 快速失败，避免继续验证无效配置
		if host.HostID == "" {
			return types.NewConfigHostIDEmptyError(i)
		}
		if host.SeedNode == "" {
			return types.NewConfigSeedNodeEmptyError(i)
		}
		if len(host.Nodes) == 0 {
			return types.NewConfigNodesEmptyError(i)
		}

		// 验证每个 Node 配置
		for j, node := range host.Nodes {
			// Early return: 快速失败
			if node.NodeID == "" {
				return types.NewConfigNodeIDEmptyError(i, j)
			}
			if node.NodeAddrTCP == "" {
				return types.NewConfigNodeAddrTCPEmptyError(i, j)
			}
			if node.NodeAddrUDP == "" {
				return types.NewConfigNodeAddrUDPEmptyError(i, j)
			}

			// 验证 multiaddr 格式（使用 HasPrefix 进行快速检查）
			// P1-4 优化：提前计算前缀检查结果
			tcpValid := strings.HasPrefix(node.NodeAddrTCP, "/ip4/") || strings.HasPrefix(node.NodeAddrTCP, "/ip6/")
			if !tcpValid {
				return types.NewConfigNodeAddrTCPInvalidFormatError(i, j)
			}
			udpValid := strings.HasPrefix(node.NodeAddrUDP, "/ip4/") || strings.HasPrefix(node.NodeAddrUDP, "/ip6/")
			if !udpValid {
				return types.NewConfigNodeAddrUDPInvalidFormatError(i, j)
			}
		}
	}

	return nil
}

// validateMetadataConfigWrapper 验证元数据配置（包装函数）
func validateMetadataConfigWrapper(cfg *Config) error {
	return validateMetadataConfig(cfg.Metadata)
}

// validateStorageConfigWrapper 验证存储配置（包装函数）
func validateStorageConfigWrapper(cfg *Config) error {
	return validateStorageConfig(cfg.Storage)
}

// validateNetworkConfigWrapper 验证网络配置（包装函数）
func validateNetworkConfigWrapper(cfg *Config) error {
	return validateNetworkConfig(cfg.Network)
}

// validateLoggingConfigWrapper 验证日志配置（包装函数）
func validateLoggingConfigWrapper(cfg *Config) error {
	return validateLoggingConfig(cfg.Logging)
}

// validateClockConfigWrapper 验证时钟配置（包装函数）
func validateClockConfigWrapper(cfg *Config) error {
	return validateClockConfig(cfg.Clock.HLC)
}

// validateMetadataConfig 验证元数据配置
func validateMetadataConfig(cfg MetadataConfig) error {
	if cfg.GossipInterval < time.Second {
		return types.NewConfigGossipIntervalInvalidError()
	}
	if cfg.QuorumTimeout < time.Second {
		return types.NewConfigQuorumTimeoutInvalidError()
	}
	if cfg.ChangeLogSize < 100 {
		return types.NewConfigChangeLogSizeInvalidError()
	}
	return nil
}

// validateStorageConfig 验证存储配置
func validateStorageConfig(cfg StorageConfig) error {
	if cfg.FlushInterval < time.Second {
		return types.NewConfigFlushIntervalInvalidError()
	}
	return nil
}

// validateNetworkConfig 验证网络配置
func validateNetworkConfig(cfg NetworkConfig) error {
	if cfg.ListenAddr == "" {
		return types.NewConfigListenAddrEmptyError()
	}
	if cfg.TransportType == "" {
		return types.NewConfigTransportTypeEmptyError()
	}
	if cfg.MessagePackType == "" {
		return types.NewConfigMessagePackTypeEmptyError()
	}
	if cfg.MaxMessageSize < 1024 {
		return types.NewConfigMaxMessageSizeInvalidError()
	}
	if cfg.ConnectTimeout < time.Second {
		return types.NewConfigConnectTimeoutInvalidError()
	}
	return nil
}

// validateLoggingConfig 验证日志配置
func validateLoggingConfig(cfg LoggingConfig) error {
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
		"fatal": true,
	}
	if !validLogLevels[cfg.Level] {
		return types.NewConfigInvalidLogLevelError(cfg.Level)
	}

	validLogFormats := map[string]bool{
		"json": true,
		"text": true,
	}
	if !validLogFormats[cfg.Format] {
		return types.NewConfigInvalidLogFormatError(cfg.Format)
	}

	validLogOutputs := map[string]bool{
		"stdout": true,
		"file":   true,
	}
	if !validLogOutputs[cfg.Output] {
		return types.NewConfigInvalidLogOutputError(cfg.Output)
	}

	if cfg.Output == "file" && cfg.File == "" {
		return types.NewConfigLogFileRequiredError()
	}

	return nil
}

// validateClockConfig 验证时钟配置
func validateClockConfig(cfg HLCConfig) error {
	if cfg.MaxDrift < 0 {
		return types.NewConfigMaxDriftInvalidError()
	}
	if cfg.SyncInterval < time.Second {
		return types.NewConfigSyncIntervalInvalidError()
	}
	return nil
}

// SaveConfig 保存配置到文件
func SaveConfig(cfg *Config, path string) error {
	// 验证配置
	if err := ValidateConfig(cfg); err != nil {
		return err
	}

	// 创建目录
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return types.NewConfigCreateDirError(err)
	}

	// 序列化为 YAML
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return types.NewConfigSerializeError(err)
	}

	// 写入文件
	if err := os.WriteFile(path, data, 0644); err != nil {
		return types.NewConfigWriteFileError(err)
	}

	return nil
}
