// Package cluster 节点 ID 管理测试
package cluster

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewLocalNodeInfo_AutoGenerate 测试自动生成节点 ID
func TestNewLocalNodeInfo_AutoGenerate(t *testing.T) {
	tempDir := t.TempDir()

	info, err := NewLocalNodeInfo(tempDir, nil)
	require.NoError(t, err)

	nodeID := info.GetNodeID()
	assert.NotEmpty(t, nodeID)

	// 验证格式: env_hostname_service_port
	// hostname 和 service 使用 - 连接
	pattern := `^[a-z]+_[a-z0-9-]+(?:_[a-z0-9-]+)?_\d+$`
	assert.Regexp(t, regexp.MustCompile(pattern), nodeID)

	// 验证以环境标识开头
	assert.Contains(t, nodeID, "dev_")

	assert.Equal(t, tempDir, info.GetDataDir())
}

// TestNewLocalNodeInfo_EnvVariable 测试环境变量配置
func TestNewLocalNodeInfo_EnvVariable(t *testing.T) {
	tempDir := t.TempDir()

	// 设置环境变量（使用新格式）
	require.NoError(t, os.Setenv("NEXKV_NODE_ID", "prod_server01_shop-service_8080"))
	t.Cleanup(func() { require.NoError(t, os.Unsetenv("NEXKV_NODE_ID")) })

	info, err := NewLocalNodeInfo(tempDir, nil)
	require.NoError(t, err)

	assert.Equal(t, "prod_server01_shop-service_8080", info.GetNodeID())
}

// TestNewLocalNodeInfo_ConfigParam 测试配置参数
func TestNewLocalNodeInfo_ConfigParam(t *testing.T) {
	tempDir := t.TempDir()

	config := &NodeIDConfig{
		Env:      "prod",
		Hostname: "prod-shop-server-01",
		Service:  "shop-service",
		Port:     8080,
	}

	info, err := NewLocalNodeInfo(tempDir, config)
	require.NoError(t, err)

	assert.Equal(t, "prod_prod-shop-server-01_shop-service_8080", info.GetNodeID())
}

// TestNewLocalNodeInfo_ConfigWithoutService 测试无服务名的配置
func TestNewLocalNodeInfo_ConfigWithoutService(t *testing.T) {
	tempDir := t.TempDir()

	config := &NodeIDConfig{
		Env:      "test",
		Hostname: "test-server",
		Service:  "", // 无服务名
		Port:     9090,
	}

	info, err := NewLocalNodeInfo(tempDir, config)
	require.NoError(t, err)

	assert.Equal(t, "test_test-server_9090", info.GetNodeID())
}

// TestNewLocalNodeInfo_Persistence 测试持久化
func TestNewLocalNodeInfo_Persistence(t *testing.T) {
	tempDir := t.TempDir()

	config := &NodeIDConfig{
		Env:      "dev",
		Hostname: "dev-server-01",
		Service:  "nexkv",
		Port:     8080,
	}

	// 第一次创建：从配置生成
	info1, err := NewLocalNodeInfo(tempDir, config)
	require.NoError(t, err)
	nodeID1 := info1.GetNodeID()

	// 第二次创建：应该读取持久化的 ID
	info2, err := NewLocalNodeInfo(tempDir, nil)
	require.NoError(t, err)
	nodeID2 := info2.GetNodeID()

	assert.Equal(t, nodeID1, nodeID2, "节点 ID 应该保持一致")
}

// TestLocalNodeInfo_Paths 测试路径获取
func TestLocalNodeInfo_Paths(t *testing.T) {
	tempDir := t.TempDir()

	config := &NodeIDConfig{
		Env:      "prod",
		Hostname: "server01",
		Service:  "service",
		Port:     8080,
	}

	info, err := NewLocalNodeInfo(tempDir, config)
	require.NoError(t, err)

	expectedNodeID := "prod_server01_service_8080"
	assert.Equal(t, expectedNodeID, info.GetNodeID())
	assert.Equal(t, tempDir, info.GetDataDir())
	assert.Equal(t, tempDir+"/"+expectedNodeID+"/wal", info.GetWalPath())
	assert.Contains(t, info.GetSnapshotPath(), "/snapshots")
	assert.Contains(t, info.GetSSTPath(), "/sst")
}

// TestGenerateNodeID 测试 NodeID 生成函数
func TestGenerateNodeID(t *testing.T) {
	tests := []struct {
		name     string
		config   *NodeIDConfig
		expected string
	}{
		{
			name: "完整格式_带服务名",
			config: &NodeIDConfig{
				Env:      "prod",
				Hostname: "prod-shop-server-01",
				Service:  "shop-service",
				Port:     8080,
			},
			expected: "prod_prod-shop-server-01_shop-service_8080",
		},
		{
			name: "完整格式_不带服务名",
			config: &NodeIDConfig{
				Env:      "prod",
				Hostname: "prod-shop-server-01",
				Service:  "",
				Port:     8081,
			},
			expected: "prod_prod-shop-server-01_8081",
		},
		{
			name: "测试环境",
			config: &NodeIDConfig{
				Env:      "test",
				Hostname: "test-server-01",
				Service:  "api",
				Port:     9000,
			},
			expected: "test_test-server-01_api_9000",
		},
		{
			name: "特殊字符替换",
			config: &NodeIDConfig{
				Env:      "prod",
				Hostname: "prod.shop.server.01",
				Service:  "shop_service",
				Port:     8080,
			},
			expected: "prod_prod-shop-server-01_shop-service_8080",
		},
		{
			name: "大写转小写",
			config: &NodeIDConfig{
				Env:      "prod",
				Hostname: "ProdShopServer01",
				Service:  "ShopService",
				Port:     8080,
			},
			expected: "prod_prodshopserver01_shopservice_8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := generateNodeID(tt.config)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGenerateNodeID_AutoHostname 测试自动获取主机名
func TestGenerateNodeID_AutoHostname(t *testing.T) {
	config := &NodeIDConfig{
		Env:      "dev",
		Hostname: "", // 空主机名，自动获取
		Service:  "service",
		Port:     8080,
	}

	result, err := generateNodeID(config)
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "dev_")
	assert.Contains(t, result, "_service_8080")
}

// TestSanitizeName 测试名称清理函数
func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "纯字母",
			input:    "server01",
			expected: "server01",
		},
		{
			name:     "包含下划线",
			input:    "shop_service",
			expected: "shop-service",
		},
		{
			name:     "包含点号",
			input:    "prod.shop.server.01",
			expected: "prod-shop-server-01",
		},
		{
			name:     "包含空格",
			input:    "shop service",
			expected: "shop-service",
		},
		{
			name:     "混合特殊字符",
			input:    "prod.shop_service",
			expected: "prod-shop-service",
		},
		{
			name:     "大写字母",
			input:    "ShopService",
			expected: "shopservice",
		},
		{
			name:     "连续特殊字符",
			input:    "prod..shop",
			expected: "prod-shop",
		},
		{
			name:     "特殊字符开头",
			input:    "_shop",
			expected: "shop",
		},
		{
			name:     "特殊字符结尾",
			input:    "shop_",
			expected: "shop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeName(strings.ToLower(tt.input))
			assert.Equal(t, tt.expected, result)
		})
	}
}
