// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
)

// ========================================
// QuorumConfig Tests
// ========================================

// TestDefaultQuorumConfig 测试默认配置
func TestDefaultQuorumConfig(t *testing.T) {
	config := DefaultQuorumConfig()

	assert.True(t, config.Enabled, "默认应该启用 Quorum")
	assert.Equal(t, 0, config.DefaultQuorum, "默认 Quorum 应该为 0（动态计算）")
	assert.Equal(t, 5000, config.Timeout, "默认超时应该是 5000ms")
	assert.Equal(t, 1, config.MinQuorum, "最小 Quorum 应该是 1")
}

// ========================================
// QuorumManager Tests
// ========================================

// TestQuorumManager_BasicFunctionality 测试基本功能
func TestQuorumManager_BasicFunctionality(t *testing.T) {
	manager := NewQuorumManager(nil)

	// 验证默认配置
	config := manager.GetConfig()
	assert.NotNil(t, config)
	assert.True(t, config.Enabled)

	// 设置新配置
	newConfig := &QuorumConfig{
		Enabled:       true,
		DefaultQuorum: 3,
		Timeout:       3000,
		MinQuorum:     2,
	}
	manager.SetConfig(newConfig)

	// 验证配置已更新
	updatedConfig := manager.GetConfig()
	assert.Equal(t, 3, updatedConfig.DefaultQuorum)
	assert.Equal(t, 3000, updatedConfig.Timeout)
	assert.Equal(t, 2, updatedConfig.MinQuorum)
}

// TestQuorumManager_GetQuorumThreshold 测试 Quorum 阈值计算
func TestQuorumManager_GetQuorumThreshold(t *testing.T) {
	tests := []struct {
		name           string
		defaultQuorum  int
		peerCount      int
		minQuorum      int
		expectedQuorum int
	}{
		{
			name:           "动态计算-5个peers",
			defaultQuorum:  0,
			peerCount:      5,
			minQuorum:      1,
			expectedQuorum: 3, // 5/2 + 1 = 3
		},
		{
			name:           "动态计算-10个peers",
			defaultQuorum:  0,
			peerCount:      10,
			minQuorum:      1,
			expectedQuorum: 6, // 10/2 + 1 = 6
		},
		{
			name:           "固定Quorum",
			defaultQuorum:  7,
			peerCount:      10,
			minQuorum:      1,
			expectedQuorum: 7, // 使用固定值
		},
		{
			name:           "最小Quorum限制",
			defaultQuorum:  0,
			peerCount:      2,
			minQuorum:      3,
			expectedQuorum: 3, // 受 MinQuorum 限制
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewQuorumManager(&QuorumConfig{
				DefaultQuorum: tt.defaultQuorum,
				MinQuorum:     tt.minQuorum,
			})

			quorum := manager.GetQuorumThreshold(tt.peerCount)
			assert.Equal(t, tt.expectedQuorum, quorum)
		})
	}
}

// TestQuorumManager_UpdatePeerCache 测试 peer 缓存更新
func TestQuorumManager_UpdatePeerCache(t *testing.T) {
	manager := NewQuorumManager(nil)

	// 初始状态：无缓存
	peers := manager.GetQuorumPeers()
	assert.Nil(t, peers)

	// 更新缓存
	peerList := []peer.ID{"peer1", "peer2", "peer3"}
	manager.UpdatePeerCache(peerList)

	// 验证缓存已更新
	cachedPeers := manager.GetQuorumPeers()
	assert.Equal(t, peerList, cachedPeers)

	// 验证返回的是副本（不是内部引用）
	cachedPeers[0] = "modified"
	originalPeers := manager.GetQuorumPeers()
	assert.NotEqual(t, cachedPeers[0], originalPeers[0], "应该返回副本")
}

// TestQuorumManager_CalculateQuorumResult 测试 Quorum 结果计算
func TestQuorumManager_CalculateQuorumResult(t *testing.T) {
	tests := []struct {
		name            string
		peerCount       int
		successCount    int
		quorum          int
		expectedSuccess bool
	}{
		{
			name:            "达到Quorum-3/5",
			peerCount:       5,
			successCount:    3,
			quorum:          3,
			expectedSuccess: true,
		},
		{
			name:            "刚好达到Quorum-3/3",
			peerCount:       3,
			successCount:    3,
			quorum:          3,
			expectedSuccess: true,
		},
		{
			name:            "未达到Quorum-2/5",
			peerCount:       5,
			successCount:    2,
			quorum:          3,
			expectedSuccess: false,
		},
		{
			name:            "全部失败-0/5",
			peerCount:       5,
			successCount:    0,
			quorum:          3,
			expectedSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建 Quorum 管理器并设置 Quorum
			manager := NewQuorumManager(&QuorumConfig{
				DefaultQuorum: tt.quorum,
			})

			result := manager.CalculateQuorumResult(
				tt.peerCount,
				tt.successCount,
				nil,
				nil,
			)

			assert.Equal(t, tt.expectedSuccess, result.Success)
			assert.Equal(t, tt.successCount, result.SuccessCount)
			assert.Equal(t, tt.peerCount, result.TotalPeers)

			// 验证成功率计算
			expectedRate := float64(tt.successCount) / float64(tt.peerCount)
			assert.Equal(t, expectedRate, result.GetSuccessRate())
		})
	}
}

// TestQuorumManager_Metrics 测试指标收集
func TestQuorumManager_Metrics(t *testing.T) {
	manager := NewQuorumManager(nil)

	// 执行一些 Quorum 操作
	manager.CalculateQuorumResult(5, 3, nil, nil) // 成功
	manager.CalculateQuorumResult(5, 2, nil, nil) // 失败
	manager.CalculateQuorumResult(5, 4, nil, nil) // 成功

	// 获取指标
	metrics := manager.GetMetrics()

	assert.Equal(t, 3, metrics.QuorumTotal, "总操作数应该是 3")
	assert.Equal(t, 2, metrics.QuorumSuccess, "成功数应该是 2")
	assert.Equal(t, 1, metrics.QuorumFailed, "失败数应该是 1")
	assert.Equal(t, 0, metrics.QuorumTimeout, "超时数应该是 0")

	// 重置指标
	manager.ResetMetrics()
	metricsAfter := manager.GetMetrics()

	assert.Equal(t, 0, metricsAfter.QuorumTotal, "重置后总操作数应该是 0")
	assert.Equal(t, 0, metricsAfter.QuorumSuccess, "重置后成功数应该是 0")
}

// TestQuorumManager_ValidateAndNormalizeWithQuorum 测试 FanoutOptions 验证集成
func TestQuorumManager_ValidateAndNormalizeWithQuorum(t *testing.T) {
	manager := NewQuorumManager(&QuorumConfig{
		Enabled:       true,
		DefaultQuorum: 0,
		MinQuorum:     1,
	})

	peers := []peer.ID{"peer1", "peer2", "peer3"}

	tests := []struct {
		name           string
		mode           ResponseMode
		quorum         int
		expectedQuorum int
		expectError    bool
	}{
		{
			name:           "Quorum模式-自动计算阈值",
			mode:           Quorum,
			quorum:         0, // 自动计算
			expectedQuorum: 2, // 3/2 + 1 = 2
			expectError:    false,
		},
		{
			name:           "Quorum模式-手动指定阈值",
			mode:           Quorum,
			quorum:         3,
			expectedQuorum: 3,
			expectError:    false,
		},
		{
			name:        "FireForget模式-不需要Quorum",
			mode:        FireForget,
			quorum:      0,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &FanoutOptions{
				Mode:    tt.mode,
				Quorum:  tt.quorum,
				Timeout: 30 * time.Millisecond,
			}

			normalized, err := manager.ValidateAndNormalizeWithQuorum(opts, peers)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedQuorum, normalized.Quorum)
			}
		})
	}
}

// ========================================
// QuorumResult Tests
// ========================================

// TestQuorumResult_IsQuorumReached 测试 Quorum 结果判断
func TestQuorumResult_IsQuorumReached(t *testing.T) {
	tests := []struct {
		name         string
		successCount int
		totalPeers   int
		quorum       int
		expected     bool
	}{
		{
			name:         "达到Quorum",
			successCount: 3,
			totalPeers:   5,
			quorum:       3,
			expected:     true,
		},
		{
			name:         "未达到Quorum",
			successCount: 2,
			totalPeers:   5,
			quorum:       3,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &QuorumResult{
				Success:      tt.successCount >= tt.quorum,
				SuccessCount: tt.successCount,
				TotalPeers:   tt.totalPeers,
				Quorum:       tt.quorum,
			}

			assert.Equal(t, tt.expected, result.IsQuorumReached())
		})
	}
}

// TestQuorumResult_GetSuccessRate 测试成功率计算
func TestQuorumResult_GetSuccessRate(t *testing.T) {
	tests := []struct {
		name         string
		successCount int
		totalPeers   int
		expectedRate float64
	}{
		{
			name:         "100%成功率",
			successCount: 5,
			totalPeers:   5,
			expectedRate: 1.0,
		},
		{
			name:         "60%成功率",
			successCount: 3,
			totalPeers:   5,
			expectedRate: 0.6,
		},
		{
			name:         "0%成功率",
			successCount: 0,
			totalPeers:   5,
			expectedRate: 0.0,
		},
		{
			name:         "空peer列表",
			successCount: 0,
			totalPeers:   0,
			expectedRate: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &QuorumResult{
				SuccessCount: tt.successCount,
				TotalPeers:   tt.totalPeers,
			}

			assert.Equal(t, tt.expectedRate, result.GetSuccessRate())
		})
	}
}

// ========================================
// ClusterService Integration Tests
// ========================================

// TestSetClusterService 测试集群服务设置
func TestSetClusterService(t *testing.T) {
	// 初始状态：无集群服务
	assert.Nil(t, GetClusterService())

	// 创建模拟集群服务
	mockService := &MockClusterService{
		peers: []peer.ID{"peer1", "peer2", "peer3"},
	}

	SetClusterService(mockService)

	// 验证服务已设置
	service := GetClusterService()
	assert.NotNil(t, service)
	assert.Same(t, mockService, service)
}

// TestQuorumManager_SyncFromCluster 测试从集群层同步
func TestQuorumManager_SyncFromCluster(t *testing.T) {
	// 创建模拟集群服务
	mockService := &MockClusterService{
		peers:  []peer.ID{"peer1", "peer2", "peer3"},
		config: &QuorumConfig{DefaultQuorum: 5, Timeout: 3000},
	}
	SetClusterService(mockService)

	// 创建 Quorum 管理器
	manager := NewQuorumManager(nil)

	// 同步配置
	err := manager.SyncQuorumConfigFromCluster()
	assert.NoError(t, err)
	assert.Equal(t, 5, manager.GetConfig().DefaultQuorum)

	// 同步 peers
	err = manager.SyncPeersFromCluster()
	assert.NoError(t, err)

	cachedPeers := manager.GetQuorumPeers()
	assert.Equal(t, mockService.peers, cachedPeers)
}

// ========================================
// Mock ClusterService
// ========================================

// MockClusterService 模拟集群服务（用于测试）
type MockClusterService struct {
	peers  []peer.ID
	config *QuorumConfig
}

// GetQuorumConfig 实现 ClusterService 接口
func (m *MockClusterService) GetQuorumConfig() *QuorumConfig {
	return m.config
}

// GetActivePeers 实现 ClusterService 接口
func (m *MockClusterService) GetActivePeers() []peer.ID {
	return m.peers
}

// IsPeerAvailable 实现 ClusterService 接口
func (m *MockClusterService) IsPeerAvailable(peerID peer.ID) bool {
	for _, p := range m.peers {
		if p == peerID {
			return true
		}
	}
	return false
}
