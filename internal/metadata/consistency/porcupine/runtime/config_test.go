// Package runtime 提供 Porcupine 运行时验证器
// 本文件测试验证器配置
package runtime

import (
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

func TestDefaultVerifierConfig(t *testing.T) {
	config := DefaultVerifierConfig()

	// 验证默认值
	if config.Enabled {
		t.Error("Expected Enabled=false by default")
	}
	if config.VerifyInterval != 5*time.Minute {
		t.Errorf("Expected VerifyInterval=5m, got %v", config.VerifyInterval)
	}
	if config.HistorySize != 100 {
		t.Errorf("Expected HistorySize=100, got %d", config.HistorySize)
	}
	if config.VerifyTimeout != time.Minute {
		t.Errorf("Expected VerifyTimeout=1m, got %v", config.VerifyTimeout)
	}
	if config.MaxOpsPerRecorder != 10000 {
		t.Errorf("Expected MaxOpsPerRecorder=10000, got %d", config.MaxOpsPerRecorder)
	}

	// 默认启用所有 Hook
	if !config.GossipEnabled || !config.QuorumEnabled || !config.FailureEnabled ||
		!config.DegradationEnabled || !config.LeaderEnabled {
		t.Error("Expected all hooks to be enabled by default")
	}
}

func TestVerifierConfig_Custom(t *testing.T) {
	config := VerifierConfig{
		Enabled:            true,
		VerifyInterval:     10 * time.Second,
		HistorySize:        50,
		VerifyTimeout:      30 * time.Second,
		MaxOpsPerRecorder:  5000,
		AsyncConfig:        porcupine.DefaultAsyncRecordConfig(),
		GossipEnabled:      false,
		QuorumEnabled:      true,
		FailureEnabled:     false,
		DegradationEnabled: true,
		LeaderEnabled:      false,
	}

	if !config.Enabled || !config.QuorumEnabled || !config.DegradationEnabled {
		t.Error("Expected Enabled, QuorumEnabled, DegradationEnabled=true")
	}
	if config.GossipEnabled || config.FailureEnabled || config.LeaderEnabled {
		t.Error("Expected GossipEnabled, FailureEnabled, LeaderEnabled=false")
	}
	if config.VerifyInterval != 10*time.Second || config.HistorySize != 50 {
		t.Errorf("Unexpected config values")
	}
}

// ==================== VerificationResult 测试 ====================

func TestVerificationResult_AllPassed(t *testing.T) {
	tests := []struct {
		name         string
		result       *VerificationResult
		expectedPass bool
	}{
		{"all pass", &VerificationResult{TopologyPass: true, FailurePass: true, LeaderHAPass: true}, true},
		{"topology fail", &VerificationResult{TopologyPass: false, FailurePass: true, LeaderHAPass: true}, false},
		{"failure fail", &VerificationResult{TopologyPass: true, FailurePass: false, LeaderHAPass: true}, false},
		{"leader fail", &VerificationResult{TopologyPass: true, FailurePass: true, LeaderHAPass: false}, false},
		{"all fail", &VerificationResult{TopologyPass: false, FailurePass: false, LeaderHAPass: false}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.AllPassed() != tt.expectedPass {
				t.Errorf("AllPassed() = %v, expected %v", tt.result.AllPassed(), tt.expectedPass)
			}
		})
	}
}

func TestVerificationResult_Summary(t *testing.T) {
	tests := []struct {
		name     string
		result   *VerificationResult
		expected string
	}{
		{"all pass", &VerificationResult{TopologyPass: true, FailurePass: true, LeaderHAPass: true}, "ALL PASSED"},
		{"topology fail", &VerificationResult{TopologyPass: false, FailurePass: true, LeaderHAPass: true}, "FAILED: Topology"},
		{"multiple fail", &VerificationResult{TopologyPass: false, FailurePass: false, LeaderHAPass: true}, "FAILED: Topology, FailureRecovery"},
		{"all fail", &VerificationResult{TopologyPass: false, FailurePass: false, LeaderHAPass: false}, "FAILED: Topology, FailureRecovery, LeaderHA"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if summary := tt.result.Summary(); summary != tt.expected {
				t.Errorf("Summary() = %q, expected %q", summary, tt.expected)
			}
		})
	}
}

func TestVerificationResult_Fields(t *testing.T) {
	now := time.Now()
	result := &VerificationResult{
		Timestamp:   now,
		Duration:    100 * time.Millisecond,
		TotalOps:    100,
		TopologyMsg: "topology message",
		FailureMsg:  "failure message",
		LeaderHAMsg: "leader HA message",
	}

	if !result.Timestamp.Equal(now) {
		t.Errorf("Timestamp mismatch")
	}
	if result.Duration != 100*time.Millisecond {
		t.Errorf("Duration mismatch")
	}
	if result.TotalOps != 100 {
		t.Errorf("TotalOps mismatch")
	}
	if result.TopologyMsg != "topology message" || result.FailureMsg != "failure message" || result.LeaderHAMsg != "leader HA message" {
		t.Errorf("Message fields mismatch")
	}
}
