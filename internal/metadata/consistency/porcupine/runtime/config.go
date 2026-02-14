// Package runtime 提供 Porcupine 运行时验证器
// 本文件定义验证器配置
package runtime

import (
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

// ==================== 验证器配置 ====================

// VerifierConfig 验证器配置
type VerifierConfig struct {
	// Enabled 是否启用验证
	Enabled bool

	// VerifyInterval 验证间隔（0 表示禁用周期验证，默认 5 分钟）
	VerifyInterval time.Duration

	// HistorySize 历史记录保留数量（默认 100）
	HistorySize int

	// VerifyTimeout 验证超时时间（默认 1 分钟）
	VerifyTimeout time.Duration

	// MaxOpsPerRecorder 每个 recorder 最大操作数（默认 10000）
	MaxOpsPerRecorder int

	// AsyncConfig 异步记录配置
	AsyncConfig porcupine.AsyncRecordConfig

	// 各模块 Hook 开关
	GossipEnabled      bool
	QuorumEnabled      bool
	FailureEnabled     bool
	DegradationEnabled bool
	LeaderEnabled      bool
}

// DefaultVerifierConfig 默认验证器配置
func DefaultVerifierConfig() VerifierConfig {
	return VerifierConfig{
		Enabled:            false,           // 默认禁用
		VerifyInterval:     5 * time.Minute, // 5 分钟周期验证
		HistorySize:        100,             // 保留 100 次验证结果
		VerifyTimeout:      time.Minute,     // 1 分钟超时
		MaxOpsPerRecorder:  10000,           // 最多 1 万操作
		AsyncConfig:        porcupine.DefaultAsyncRecordConfig(),
		GossipEnabled:      true,
		QuorumEnabled:      true,
		FailureEnabled:     true,
		DegradationEnabled: true,
		LeaderEnabled:      true,
	}
}

// ==================== 验证结果 ====================

// VerificationResult 验证结果
type VerificationResult struct {
	Timestamp    time.Time
	TopologyPass bool
	FailurePass  bool
	LeaderHAPass bool
	TopologyMsg  string
	FailureMsg   string
	LeaderHAMsg  string
	TotalOps     int
	Duration     time.Duration
}

// AllPassed 返回所有验证是否通过
func (r *VerificationResult) AllPassed() bool {
	return r.TopologyPass && r.FailurePass && r.LeaderHAPass
}

// Summary 返回结果摘要
func (r *VerificationResult) Summary() string {
	if r.AllPassed() {
		return "ALL PASSED"
	}

	var failed []string
	if !r.TopologyPass {
		failed = append(failed, "Topology")
	}
	if !r.FailurePass {
		failed = append(failed, "FailureRecovery")
	}
	if !r.LeaderHAPass {
		failed = append(failed, "LeaderHA")
	}

	result := "FAILED: "
	for i, f := range failed {
		if i > 0 {
			result += ", "
		}
		result += f
	}
	return result
}
