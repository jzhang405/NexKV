// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件实现一致性检查器，调用 Porcupine 进行验证
package porcupine

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anishathalye/porcupine"
)

// CheckResult 一致性检查结果
type CheckResult struct {
	Ok         bool                        // 是否通过线性化验证
	Error      string                      // 错误信息（失败时）
	Result     porcupine.CheckResult       // Porcupine 原始结果
	Info       porcupine.LinearizationInfo // 线性化信息（用于可视化）
	ReportPath string                      // 可视化报告路径（失败时生成）
}

// IsOk 返回是否通过验证
func (r *CheckResult) IsOk() bool {
	return r.Ok
}

// String 返回结果的字符串表示
func (r *CheckResult) String() string {
	if r.Ok {
		return "LINEARIZABLE"
	}
	if r.Error != "" {
		return fmt.Sprintf("NOT_LINEARIZABLE: %s", r.Error)
	}
	return string(r.Result)
}

// ConsistencyChecker 一致性检查器
// 封装 Porcupine 验证逻辑，提供可视化报告生成功能
type ConsistencyChecker struct {
	model     porcupine.Model // 状态模型
	timeout   time.Duration   // 验证超时时间
	reportDir string          // 报告输出目录
}

// NewConsistencyChecker 创建一致性检查器
// model: 状态模型（如 NexKVModel）
// timeout: 验证超时时间（0 表示无限制）
// reportDir: 可视化报告输出目录（空字符串表示不生成报告）
func NewConsistencyChecker(model porcupine.Model, timeout time.Duration, reportDir string) *ConsistencyChecker {
	return &ConsistencyChecker{
		model:     model,
		timeout:   timeout,
		reportDir: reportDir,
	}
}

// Model 返回关联的状态模型
func (c *ConsistencyChecker) Model() porcupine.Model {
	return c.model
}

// CheckOperations 检查操作历史的线性一致性
// ops: 操作历史（porcupine.Operation 列表）
// 返回检查结果
func (c *ConsistencyChecker) CheckOperations(ops []porcupine.Operation) *CheckResult {
	// 使用 Porcupine 验证
	result, info := porcupine.CheckOperationsVerbose(c.model, ops, c.timeout)

	// 构建结果
	checkResult := &CheckResult{
		Result: result,
		Info:   info,
	}

	// 判断是否通过
	switch result {
	case porcupine.Ok:
		checkResult.Ok = true
	case porcupine.Illegal:
		checkResult.Ok = false
		checkResult.Error = "Illegal history: operations violate sequential specification"
	case porcupine.Unknown:
		checkResult.Ok = false
		checkResult.Error = "Timeout: could not determine linearizability within time limit"
	default:
		checkResult.Ok = false
		checkResult.Error = fmt.Sprintf("Unknown result: %s", result)
	}

	// 失败时生成可视化报告
	if !checkResult.Ok && c.reportDir != "" {
		reportPath := c.generateReport(info)
		checkResult.ReportPath = reportPath
	}

	return checkResult
}

// CheckEvents 检查事件历史的线性一致性
// events: 事件历史（porcupine.Event 列表）
// 返回检查结果
func (c *ConsistencyChecker) CheckEvents(events []porcupine.Event) *CheckResult {
	// 使用 Porcupine 验证
	result, info := porcupine.CheckEventsVerbose(c.model, events, c.timeout)

	// 构建结果
	checkResult := &CheckResult{
		Result: result,
		Info:   info,
	}

	// 判断是否通过
	switch result {
	case porcupine.Ok:
		checkResult.Ok = true
	case porcupine.Illegal:
		checkResult.Ok = false
		checkResult.Error = "Illegal history: operations violate sequential specification"
	case porcupine.Unknown:
		checkResult.Ok = false
		checkResult.Error = "Timeout: could not determine linearizability within time limit"
	default:
		checkResult.Ok = false
		checkResult.Error = fmt.Sprintf("Unknown result: %s", result)
	}

	// 失败时生成可视化报告
	if !checkResult.Ok && c.reportDir != "" {
		reportPath := c.generateReport(info)
		checkResult.ReportPath = reportPath
	}

	return checkResult
}

// generateReport 生成可视化报告
func (c *ConsistencyChecker) generateReport(info porcupine.LinearizationInfo) string {
	if c.reportDir == "" {
		return ""
	}

	// 确保目录存在
	if err := os.MkdirAll(c.reportDir, 0755); err != nil {
		return fmt.Sprintf("failed to create report directory: %v", err)
	}

	// 生成文件名
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("linearizability-report-%s.html", timestamp)
	reportPath := filepath.Join(c.reportDir, filename)

	// 创建文件
	f, err := os.Create(reportPath)
	if err != nil {
		return fmt.Sprintf("failed to create report file: %v", err)
	}
	defer f.Close()

	// 生成可视化
	if err := porcupine.Visualize(c.model, info, f); err != nil {
		return fmt.Sprintf("failed to generate visualization: %v", err)
	}

	return reportPath
}

// CheckFromRecorder 从 HistoryRecorder 检查
// recorder: 历史记录器
// 返回检查结果
func (c *ConsistencyChecker) CheckFromRecorder(recorder *HistoryRecorder) *CheckResult {
	ops := recorder.GetOperations()
	return c.CheckOperations(ops)
}
