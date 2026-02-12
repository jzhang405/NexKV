package framework

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfigGenerator 配置生成器
type ConfigGenerator struct {
	// BaseDir 基础目录
	BaseDir string
	// PortRange 端口范围
	PortRange *PortRange
}

// PortRange 端口范围
type PortRange struct {
	// Start 起始端口
	Start int
	// End 结束端口
	End int
	// current 当前端口
	current int
}

// NewPortRange 创建端口范围
func NewPortRange(start, end int) *PortRange {
	return &PortRange{
		Start:   start,
		End:     end,
		current: start,
	}
}

// Next 获取下一个端口
func (r *PortRange) Next() int {
	port := r.current
	r.current++
	if r.current > r.End {
		r.current = r.Start
	}
	return port
}

// NewConfigGenerator 创建配置生成器
func NewConfigGenerator(baseDir string) *ConfigGenerator {
	return &ConfigGenerator{
		BaseDir:   baseDir,
		PortRange: NewPortRange(19000, 19999),
	}
}

// DaemonConfig daemon 配置
type DaemonConfig struct {
	// NodeID 节点 ID
	NodeID string
	// Addr 监听地址
	Addr string
	// DataDir 数据目录
	DataDir string
	// LogDir 日志目录
	LogDir string
	// ClusterName 集群名称
	ClusterName string
	// BootstrapNodes 引导节点
	BootstrapNodes []string
	// Role 节点角色
	Role int
}

// GenerateDaemonConfig 生成 daemon 配置
func (g *ConfigGenerator) GenerateDaemonConfig(nodeID, addr string) (*DaemonConfig, error) {
	dataDir := filepath.Join(g.BaseDir, "data", nodeID)
	logDir := filepath.Join(g.BaseDir, "logs")

	// 创建目录
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log dir: %w", err)
	}

	return &DaemonConfig{
		NodeID:        nodeID,
		Addr:          addr,
		DataDir:       dataDir,
		LogDir:        logDir,
		ClusterName:   "nexkv-e2e-cluster",
		BootstrapNodes: []string{},
		Role:          1,
	}, nil
}

// GenerateClusterConfigs 生成集群配置
func (g *ConfigGenerator) GenerateClusterConfigs(nodeCount int) ([]*DaemonConfig, error) {
	configs := make([]*DaemonConfig, nodeCount)
	basePort := 19000

	for i := 0; i < nodeCount; i++ {
		nodeID := fmt.Sprintf("node-%d", i+1)
		addr := fmt.Sprintf("127.0.0.1:%d", basePort+i*100)

		config, err := g.GenerateDaemonConfig(nodeID, addr)
		if err != nil {
			return nil, err
		}

		// 第一个节点作为引导节点
		if i > 0 {
			config.BootstrapNodes = []string{fmt.Sprintf("127.0.0.1:%d", basePort)}
		}

		configs[i] = config
	}

	return configs, nil
}

// WriteConfig 写入配置文件
func (g *ConfigGenerator) WriteConfig(config *DaemonConfig) error {
	configPath := filepath.Join(g.BaseDir, fmt.Sprintf("%s.yaml", config.NodeID))

	// TODO: 生成 YAML 格式的配置文件
	// 这里需要根据 NexKV 的配置格式生成

	return os.WriteFile(configPath, []byte(fmt.Sprintf("# Config for %s", config.NodeID)), 0644)
}

// Cleanup 清理测试文件
func (g *ConfigGenerator) Cleanup() error {
	return os.RemoveAll(g.BaseDir)
}
