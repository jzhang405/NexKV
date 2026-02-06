// Package config 提供配置加载功能
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jzhang405/NexKV/internal/metadata/types"

	"github.com/spf13/viper"
)

// Loader 配置加载器
type Loader struct {
	configPath string
	viper      *viper.Viper
	hostID     string // PR-037: 指定当前 Host ID
}

// NewLoader 创建配置加载器
// PR-037: hostID 参数指定当前使用的 Host，用于获取对应的数据目录
func NewLoader(configPath string, hostID string) *Loader {
	return &Loader{
		configPath: configPath,
		viper:      viper.New(),
		hostID:     hostID,
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
			return nil, types.NewConfigLoadError("读取配置文件", err)
		}
		// 配置文件不存在，使用默认配置
	}

	// 解析配置到结构体
	cfg := DefaultConfig()
	if err := l.viper.Unmarshal(cfg); err != nil {
		return nil, types.NewConfigLoadError("解析配置", err)
	}

	// 应用环境变量覆盖
	applyEnvOverrides(cfg)

	// 验证配置
	if err := cfg.Validate(); err != nil {
		return nil, types.NewConfigValidationError("", "配置验证失败: "+err.Error())
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

// Validate 验证配置（PR-037: 适配三级配置结构）
func (c *Config) Validate() error {
	// 使用统一的 ValidateConfig 函数
	return ValidateConfig(c)
}

// GetHostDir 获取指定 Host 的数据目录（PR-037: 新增）
// 返回 {base_dir}/{host_id}
func (c *Config) GetHostDir(hostID string) string {
	baseDir := c.Cluster.BaseDir
	// 展开波浪号
	if strings.HasPrefix(baseDir, "~/") {
		if homeDir, err := os.UserHomeDir(); err == nil {
			baseDir = filepath.Join(homeDir, baseDir[2:])
		}
	}
	return filepath.Join(baseDir, hostID)
}

// GetMetadataDir 获取元数据目录（PR-037: 使用三级配置结构）
// 返回 {base_dir}/{host_id}/metadata
func (c *Config) GetMetadataDir(hostID string) string {
	return filepath.Join(c.GetHostDir(hostID), "metadata")
}

// GetShardsDir 获取分片数据目录（PR-037: 使用三级配置结构）
// 返回 {base_dir}/{host_id}/shards
func (c *Config) GetShardsDir(hostID string) string {
	return filepath.Join(c.GetHostDir(hostID), "shards")
}

// GetWALDir 获取 WAL 目录（PR-037: 使用三级配置结构）
// 返回 {base_dir}/{host_id}/wal
func (c *Config) GetWALDir(hostID string) string {
	return filepath.Join(c.GetHostDir(hostID), "wal")
}

// GetSnapshotsDir 获取快照目录（PR-037: 使用三级配置结构）
// 返回 {base_dir}/{host_id}/snapshots
func (c *Config) GetSnapshotsDir(hostID string) string {
	return filepath.Join(c.GetHostDir(hostID), "snapshots")
}

// CreateHostDirs 创建指定 Host 的必要目录（PR-037: 新增）
func (c *Config) CreateHostDirs(hostID string) error {
	// 定义需要创建的目录列表（基于三级配置结构）
	dirs := []string{
		c.GetMetadataDir(hostID),
		c.GetShardsDir(hostID),
		c.GetWALDir(hostID),
		c.GetSnapshotsDir(hostID),
	}

	// 并发创建目录以提高性能
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		// P1-6 修复：使用更严格的权限 0700（仅所有者可访问），避免安全风险
		// 元数据、WAL 日志和分片数据包含敏感信息，不应允许其他用户访问
		if err := os.MkdirAll(dir, 0700); err != nil {
			return types.NewConfigLoadError("创建目录 "+dir, err)
		}
	}

	return nil
}

// CreateDirs 创建必要的目录（PR-037: 使用三级配置结构，兼容旧接口）
func (c *Config) CreateDirs() error {
	// 如果没有指定 hostID，使用第一个 Host
	hostID := ""
	if len(c.Cluster.Hosts) > 0 {
		hostID = c.Cluster.Hosts[0].HostID
	}
	if hostID == "" {
		return types.NewConfigValidationError("cluster.hosts", "没有可用的 Host")
	}
	return c.CreateHostDirs(hostID)
}
