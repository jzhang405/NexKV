// Package config 提供元数据层的配置管理
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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

// LoadConfig 从文件加载配置
func LoadConfig(path string) (*Config, error) {
	// 检查文件是否存在
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// 文件不存在，返回默认配置
		return DefaultConfig(), nil
	}

	// 读取配置文件
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	// 解析 YAML
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 验证配置
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	return cfg, nil
}

// ValidateConfig 验证配置有效性
func ValidateConfig(cfg *Config) error {
	validators := []struct {
		name string
		fn   func(*Config) error
	}{
		{"集群配置", validateClusterConfigWrapper},
		{"树形协调器配置", validateTreeCoordConfigWrapper},
		{"元数据配置", validateMetadataConfigWrapper},
		{"存储配置", validateStorageConfigWrapper},
		{"网络配置", validateNetworkConfigWrapper},
		{"日志配置", validateLoggingConfigWrapper},
		{"时钟配置", validateClockConfigWrapper},
	}

	for _, v := range validators {
		if err := v.fn(cfg); err != nil {
			return fmt.Errorf("%s验证失败: %w", v.name, err)
		}
	}

	return nil
}

// validateClusterConfigWrapper 验证集群配置（包装函数）
func validateClusterConfigWrapper(cfg *Config) error {
	if cfg.Cluster.Name == "" {
		return fmt.Errorf("cluster.name 不能为空")
	}
	if cfg.Cluster.NodeID == "" {
		return fmt.Errorf("cluster.node_id 不能为空")
	}
	if cfg.Cluster.NodeAddr == "" {
		return fmt.Errorf("cluster.node_addr 不能为空")
	}
	return nil
}

// validateTreeCoordConfigWrapper 验证树形协调器配置（包装函数）
func validateTreeCoordConfigWrapper(cfg *Config) error {
	return validateTreeCoordConfig(cfg.Cluster.TreeCoord)
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

// validateTreeCoordConfig 验证树形协调器配置
func validateTreeCoordConfig(cfg TreeCoordConfig) error {
	if cfg.MaxChildren < 1 {
		return fmt.Errorf("max_children 必须 >= 1")
	}
	if cfg.MaxChildren > 50 {
		return fmt.Errorf("max_children 不能超过 50")
	}
	if cfg.GroupSize < 1 {
		return fmt.Errorf("group_size 必须 >= 1")
	}
	if cfg.GroupSize > 20 {
		return fmt.Errorf("group_size 不能超过 20")
	}
	return nil
}

// validateMetadataConfig 验证元数据配置
func validateMetadataConfig(cfg MetadataConfig) error {
	if cfg.DataDir == "" {
		return fmt.Errorf("data_dir 不能为空")
	}
	if cfg.GossipInterval < time.Second {
		return fmt.Errorf("gossip_interval 不能小于 1 秒")
	}
	if cfg.QuorumTimeout < time.Second {
		return fmt.Errorf("quorum_timeout 不能小于 1 秒")
	}
	if cfg.ChangeLogSize < 100 {
		return fmt.Errorf("change_log_size 不能小于 100")
	}
	return nil
}

// validateStorageConfig 验证存储配置
func validateStorageConfig(cfg StorageConfig) error {
	if cfg.ShardDataDir == "" {
		return fmt.Errorf("shard_data_dir 不能为空")
	}
	if cfg.WALDir == "" {
		return fmt.Errorf("wal_dir 不能为空")
	}
	if cfg.SnapshotDir == "" {
		return fmt.Errorf("snapshot_dir 不能为空")
	}
	if cfg.FlushInterval < time.Second {
		return fmt.Errorf("flush_interval 不能小于 1 秒")
	}
	return nil
}

// validateNetworkConfig 验证网络配置
func validateNetworkConfig(cfg NetworkConfig) error {
	if cfg.ListenAddr == "" {
		return fmt.Errorf("listen_addr 不能为空")
	}
	if cfg.TransportType == "" {
		return fmt.Errorf("transport_type 不能为空")
	}
	if cfg.MessagePackType == "" {
		return fmt.Errorf("message_pack_type 不能为空")
	}
	if cfg.MaxMessageSize < 1024 {
		return fmt.Errorf("max_message_size 不能小于 1024 字节")
	}
	if cfg.ConnectTimeout < time.Second {
		return fmt.Errorf("connect_timeout 不能小于 1 秒")
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
		return fmt.Errorf("无效的日志级别: %s（必须是 debug/info/warn/error/fatal）", cfg.Level)
	}

	validLogFormats := map[string]bool{
		"json": true,
		"text": true,
	}
	if !validLogFormats[cfg.Format] {
		return fmt.Errorf("无效的日志格式: %s（必须是 json/text）", cfg.Format)
	}

	validLogOutputs := map[string]bool{
		"stdout": true,
		"file":   true,
	}
	if !validLogOutputs[cfg.Output] {
		return fmt.Errorf("无效的日志输出: %s（必须是 stdout/file）", cfg.Output)
	}

	if cfg.Output == "file" && cfg.File == "" {
		return fmt.Errorf("日志输出为 file 时，必须指定 file 路径")
	}

	return nil
}

// validateClockConfig 验证时钟配置
func validateClockConfig(cfg HLCConfig) error {
	if cfg.MaxDrift < 0 {
		return fmt.Errorf("max_drift 不能为负数")
	}
	if cfg.SyncInterval < time.Second {
		return fmt.Errorf("sync_interval 不能小于 1 秒")
	}
	return nil
}

// SaveConfig 保存配置到文件
func SaveConfig(cfg *Config, path string) error {
	// 验证配置
	if err := ValidateConfig(cfg); err != nil {
		return fmt.Errorf("配置验证失败: %w", err)
	}

	// 创建目录
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	// 序列化为 YAML
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	return nil
}
