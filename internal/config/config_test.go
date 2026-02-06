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
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfig_LoadValidConfig RED: 加载有效配置
func TestConfig_LoadValidConfig(t *testing.T) {
	// 创建临时配置文件
	configContent := `
p2p:
  listen_addr: "/ip4/0.0.0.0/tcp/4001"
  bootstrap_peers:
    - "/ip4/192.168.1.1/tcp/4001/p2p/QmPeer1"
    - "/ip4/192.168.1.2/tcp/4001/p2p/QmPeer2"
  private_key_path: "/tmp/test-key.pem"
  discovery:
    mdns_enabled: true
    dht_enabled: true
`
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(configContent)
	require.NoError(t, err)
	tmpFile.Close()

	// GREEN: 实现配置加载
	cfg, err := LoadConfig(tmpFile.Name())
	require.NoError(t, err)

	// 验证配置值
	assert.Equal(t, "/ip4/0.0.0.0/tcp/4001", cfg.P2P.ListenAddr)
	assert.Len(t, cfg.P2P.BootstrapPeers, 2)
	assert.True(t, cfg.P2P.Discovery.MDNSEnabled)
	assert.True(t, cfg.P2P.Discovery.DHTEnabled)
}

// TestConfig_ValidationRequiredFields 测试必填字段验证
func TestConfig_ValidationRequiredFields(t *testing.T) {
	testCases := []struct {
		name          string
		configContent string
		expectError   bool
		errorMsg      string
	}{
		{
			name: "缺少listen_addr",
			configContent: `
p2p:
  private_key_path: "/tmp/key.pem"
`,
			expectError: true,
			errorMsg:    "listen_addr",
		},
		{
			name: "缺少private_key_path",
			configContent: `
p2p:
  listen_addr: "/ip4/0.0.0.0/tcp/4001"
`,
			expectError: true,
			errorMsg:    "private_key_path",
		},
		{
			name: "无效的multiaddr",
			configContent: `
p2p:
  listen_addr: "invalid-multiaddr"
  private_key_path: "/tmp/key.pem"
`,
			expectError: true,
			errorMsg:    "multiaddr",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpFile, err := os.CreateTemp("", "config-*.yaml")
			require.NoError(t, err)
			defer os.Remove(tmpFile.Name())

			_, err = tmpFile.WriteString(tc.configContent)
			require.NoError(t, err)
			tmpFile.Close()

			_, err = LoadConfig(tmpFile.Name())
			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestConfig_DefaultValues 测试默认值处理
func TestConfig_DefaultValues(t *testing.T) {
	configContent := `
p2p:
  listen_addr: "/ip4/0.0.0.0/tcp/4001"
  private_key_path: "/tmp/key.pem"
`
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(configContent)
	require.NoError(t, err)
	tmpFile.Close()

	cfg, err := LoadConfig(tmpFile.Name())
	require.NoError(t, err)

	// 验证默认值
	assert.Equal(t, true, cfg.P2P.Discovery.MDNSEnabled) // 默认启用mDNS
	assert.Equal(t, true, cfg.P2P.Discovery.DHTEnabled)  // 默认启用DHT
	assert.Equal(t, 10*time.Second, cfg.P2P.ConnTimeout) // 默认连接超时
}

// TestConfig_LoadFromEnv 测试从环境变量加载配置
func TestConfig_LoadFromEnv(t *testing.T) {
	// 设置环境变量
	os.Setenv("NEXKV_P2P_LISTEN_ADDR", "/ip4/0.0.0.0/tcp/5000")
	os.Setenv("NEXKV_P2P_PRIVATE_KEY_PATH", "/tmp/env-key.pem")
	defer os.Unsetenv("NEXKV_P2P_LISTEN_ADDR")
	defer os.Unsetenv("NEXKV_P2P_PRIVATE_KEY_PATH")

	cfg, err := LoadConfigFromEnv()
	require.NoError(t, err)

	assert.Equal(t, "/ip4/0.0.0.0/tcp/5000", cfg.P2P.ListenAddr)
	assert.Equal(t, "/tmp/env-key.pem", cfg.P2P.PrivateKeyPath)
}

// TestConfig_EnvOverridesYAML 测试环境变量覆盖YAML配置
func TestConfig_EnvOverridesYAML(t *testing.T) {
	configContent := `
p2p:
  listen_addr: "/ip4/0.0.0.0/tcp/4001"
  private_key_path: "/tmp/yaml-key.pem"
`
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.WriteString(configContent)
	require.NoError(t, err)
	tmpFile.Close()

	// 设置环境变量覆盖
	os.Setenv("NEXKV_P2P_LISTEN_ADDR", "/ip4/0.0.0.0/tcp/6000")
	defer os.Unsetenv("NEXKV_P2P_LISTEN_ADDR")

	cfg, err := LoadConfigWithEnvOverride(tmpFile.Name())
	require.NoError(t, err)

	// 验证环境变量覆盖了YAML配置
	assert.Equal(t, "/ip4/0.0.0.0/tcp/6000", cfg.P2P.ListenAddr)
	assert.Equal(t, "/tmp/yaml-key.pem", cfg.P2P.PrivateKeyPath) // 未覆盖的保持原值
}
