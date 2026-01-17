// Package config 提供配置加载功能
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Loader 配置加载器
type Loader struct {
	configPath string
	viper      *viper.Viper
}

// NewLoader 创建配置加载器
func NewLoader(configPath string) *Loader {
	return &Loader{
		configPath: configPath,
		viper:      viper.New(),
	}
}

// Load 加载配置文件
func (l *Loader) Load() (*Config, error) {
	// 设置配置文件路径
	if l.configPath != "" {
		// 使用指定的配置文件
		ext := filepath.Ext(l.configPath)
		l.viper.SetConfigFile(l.configPath)

		if ext == "" || ext == ".yaml" || ext == ".yml" {
			l.viper.SetConfigType("yaml")
		}
	} else {
		// 自动搜索配置文件
		l.viper.SetConfigName("config")
		l.viper.SetConfigType("yaml")
		l.viper.AddConfigPath("./configs")
		l.viper.AddConfigPath(".")
	}

	// 读取环境变量
	l.viper.AutomaticEnv()
	l.viper.SetEnvPrefix("NEXKV")

	// 读取配置文件
	if err := l.viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
		// 配置文件不存在，使用默认配置
	}

	// 解析配置到结构体
	cfg := DefaultConfig()
	if err := l.viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	// 验证配置
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	return cfg, nil
}

// MustLoad 加载配置，失败时 panic
func (l *Loader) MustLoad() *Config {
	cfg, err := l.Load()
	if err != nil {
		panic(fmt.Sprintf("加载配置失败: %v", err))
	}
	return cfg
}

// Validate 验证配置
func (c *Config) Validate() error {
	// 验证集群配置
	if c.Cluster.Name == "" {
		return fmt.Errorf("cluster.name 不能为空")
	}
	if c.Cluster.NodeID == "" {
		return fmt.Errorf("cluster.node_id 不能为空")
	}
	if c.Cluster.NodeAddr == "" {
		return fmt.Errorf("cluster.node_addr 不能为空")
	}

	// 验证树形分组配置
	if c.Cluster.TreeCoord.MaxChildren < 5 || c.Cluster.TreeCoord.MaxChildren > 10 {
		return fmt.Errorf("cluster.tree_coord.max_children 必须在 5-10 之间")
	}
	if c.Cluster.TreeCoord.GroupSize < 5 || c.Cluster.TreeCoord.GroupSize > 10 {
		return fmt.Errorf("cluster.tree_coord.group_size 必须在 5-10 之间")
	}

	// 验证元数据配置
	if c.Metadata.DataDir == "" {
		return fmt.Errorf("metadata.data_dir 不能为空")
	}

	// 验证存储配置
	if c.Storage.ShardDataDir == "" {
		return fmt.Errorf("storage.shard_data_dir 不能为空")
	}
	if c.Storage.WALDir == "" {
		return fmt.Errorf("storage.wal_dir 不能为空")
	}

	// 验证网络配置
	if c.Network.ListenAddr == "" {
		return fmt.Errorf("network.listen_addr 不能为空")
	}

	// 创建必要目录
	if err := c.CreateDirs(); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	return nil
}

// CreateDirs 创建必要的目录
func (c *Config) CreateDirs() error {
	dirs := []string{
		c.Metadata.DataDir,
		c.Storage.ShardDataDir,
		c.Storage.WALDir,
		c.Storage.SnapshotDir,
	}

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", dir, err)
		}
	}

	return nil
}
