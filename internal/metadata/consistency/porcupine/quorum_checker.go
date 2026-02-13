// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件实现 Quorum 线性一致性检查器
package porcupine

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anishathalye/porcupine"
)

// QuorumLinearizabilityChecker Quorum 线性一致性检查器
// 用于验证 Quorum 操作的线性一致性
type QuorumLinearizabilityChecker struct {
	model     porcupine.Model
	timestamp *HLCTimestamp
	nodeID    string
	timeout   time.Duration
}

// NewQuorumLinearizabilityChecker 创建 Quorum 线性一致性检查器
// model: Porcupine 状态模型
// nodeID: 节点标识
func NewQuorumLinearizabilityChecker(model porcupine.Model, nodeID string) *QuorumLinearizabilityChecker {
	return &QuorumLinearizabilityChecker{
		model:     model,
		timestamp: NewHLCTimestamp(),
		nodeID:    nodeID,
		timeout:   0, // 无超时限制
	}
}

// GetTimestamp 获取当前时间戳
func (c *QuorumLinearizabilityChecker) GetTimestamp() int64 {
	return c.timestamp.Now()
}

// CheckOperations 检查操作的线性一致性
func (c *QuorumLinearizabilityChecker) CheckOperations(history []porcupine.Operation) *CheckResult {
	if len(history) == 0 {
		return &CheckResult{Ok: true}
	}

	// porcupine.CheckOperations 返回 bool
	ok := porcupine.CheckOperations(c.model, history)
	if ok {
		return &CheckResult{Ok: true}
	}

	return &CheckResult{
		Ok:    false,
		Error: "linearizability check failed",
	}
}

// CheckWithVisualization 带可视化的一致性检查
// 返回: (是否通过, 可视化文件路径)
func (c *QuorumLinearizabilityChecker) CheckWithVisualization(history []porcupine.Operation) (bool, string) {
	if len(history) == 0 {
		return true, ""
	}

	// 使用 CheckOperationsVerbose 获取 LinearizationInfo
	result, info := porcupine.CheckOperationsVerbose(c.model, history, c.timeout)

	// porcupine.CheckResult 是 string 类型: Ok, Illegal, Unknown
	if result == porcupine.Ok {
		return true, ""
	}

	// 生成可视化 HTML 文件
	visPath := filepath.Join(os.TempDir(), fmt.Sprintf("porcupine-violation-%d.html", time.Now().Unix()))

	// 创建文件作为 io.Writer
	f, err := os.Create(visPath)
	if err != nil {
		return false, fmt.Sprintf("failed to create visualization file: %v", err)
	}
	defer f.Close()

	if err := porcupine.Visualize(c.model, info, f); err != nil {
		return false, fmt.Sprintf("visualization error: %v", err)
	}

	return false, visPath
}

// RecordingQuorumScenario 带记录功能的 Quorum 测试场景
type RecordingQuorumScenario struct {
	NodeID    string
	Recorder  *HistoryRecorder
	Client    *RecordingClient
	Checker   *QuorumLinearizabilityChecker
	timestamp *HLCTimestamp
}

// NewRecordingQuorumScenario 创建带记录功能的 Quorum 测试场景
func NewRecordingQuorumScenario(nodeID string, kv KVOperator) *RecordingQuorumScenario {
	timestamp := NewHLCTimestamp()
	recorder := NewHistoryRecorder(nodeID, timestamp)
	client := NewRecordingClient(kv, recorder)
	checker := NewQuorumLinearizabilityChecker(NexKVModel, nodeID)

	return &RecordingQuorumScenario{
		NodeID:    nodeID,
		Recorder:  recorder,
		Client:    client,
		Checker:   checker,
		timestamp: timestamp,
	}
}

// GetTimestamp 获取当前时间戳
func (s *RecordingQuorumScenario) GetTimestamp() int64 {
	return s.timestamp.Now()
}

// VerifyLinearizability 验证线性一致性
func (s *RecordingQuorumScenario) VerifyLinearizability() *CheckResult {
	ops := s.Recorder.GetOperations()
	if len(ops) == 0 {
		return &CheckResult{Ok: true}
	}
	return s.Checker.CheckOperations(ops)
}

// VerifyLinearizabilityWithVis 带可视化的线性化验证
// 返回: (检查结果, 可视化文件路径)
func (s *RecordingQuorumScenario) VerifyLinearizabilityWithVis() (*CheckResult, string) {
	ops := s.Recorder.GetOperations()
	if len(ops) == 0 {
		return &CheckResult{Ok: true}, ""
	}

	ok, visPath := s.Checker.CheckWithVisualization(ops)
	if ok {
		return &CheckResult{Ok: true}, ""
	}

	return &CheckResult{
		Ok:    false,
		Error: fmt.Sprintf("Linearizability violation. Visualization: %s", visPath),
	}, visPath
}

// Clear 清空记录
func (s *RecordingQuorumScenario) Clear() {
	s.Recorder.Clear()
}
