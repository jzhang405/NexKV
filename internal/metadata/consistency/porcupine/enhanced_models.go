// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件提供增强模型的统一入口和便捷方法
package porcupine

import (
	"fmt"

	"github.com/anishathalye/porcupine"
)

// ==================== 增强模型注册表 ====================

// EnhancedModelType 增强模型类型
type EnhancedModelType string

const (
	// ModelTypeTopology 拓扑感知模型
	ModelTypeTopology EnhancedModelType = "topology"
	// ModelTypeFailureRecovery 失败恢复模型
	ModelTypeFailureRecovery EnhancedModelType = "failure_recovery"
	// ModelTypeLeaderHA Leader HA 模型
	ModelTypeLeaderHA EnhancedModelType = "leader_ha"
)

// String 返回模型类型的字符串表示
func (t EnhancedModelType) String() string {
	return string(t)
}

// EnhancedModelRegistry 增强模型注册表
type EnhancedModelRegistry struct {
	models map[EnhancedModelType]porcupine.Model
}

// NewEnhancedModelRegistry 创建增强模型注册表
func NewEnhancedModelRegistry() *EnhancedModelRegistry {
	return &EnhancedModelRegistry{
		models: map[EnhancedModelType]porcupine.Model{
			ModelTypeTopology:        TopologyAwareModel(),
			ModelTypeFailureRecovery: FailureRecoveryModel(),
			ModelTypeLeaderHA:        LeaderHAModel(),
		},
	}
}

// GetModel 获取指定类型的模型
func (r *EnhancedModelRegistry) GetModel(modelType EnhancedModelType) (porcupine.Model, error) {
	model, ok := r.models[modelType]
	if !ok {
		return porcupine.Model{}, fmt.Errorf("unknown model type: %s", modelType)
	}
	return model, nil
}

// ListModels 列出所有注册的模型类型
func (r *EnhancedModelRegistry) ListModels() []EnhancedModelType {
	types := make([]EnhancedModelType, 0, len(r.models))
	for t := range r.models {
		types = append(types, t)
	}
	return types
}

// ==================== 便捷验证函数 ====================

// VerifyResult 验证结果
type VerifyResult struct {
	ModelType EnhancedModelType
	Passed    bool
	Message   string
}

// VerifyOperationsWithModel 使用指定模型验证操作历史
// 注意：ops 应该是原始操作类型（TopologyOperation 等），不是 EnhancedInput 包装类型
// 对于 EnhancedHistoryRecorder 的操作，请使用 VerifyAllModels
func VerifyOperationsWithModel(modelType EnhancedModelType, ops []porcupine.Operation) VerifyResult {
	registry := NewEnhancedModelRegistry()
	model, err := registry.GetModel(modelType)
	if err != nil {
		return VerifyResult{
			ModelType: modelType,
			Passed:    false,
			Message:   err.Error(),
		}
	}

	// 检测并解包 EnhancedInput/Output
	unwrappedOps := make([]porcupine.Operation, len(ops))
	for i, op := range ops {
		// 尝试解包 EnhancedInput
		if enhancedInput, ok := op.Input.(EnhancedInput); ok {
			enhancedOutput, ok := op.Output.(EnhancedOutput)
			if !ok {
				return VerifyResult{
					ModelType: modelType,
					Passed:    false,
					Message:   "invalid output type: expected EnhancedOutput",
				}
			}
			// 根据模型类型解包
			switch modelType {
			case ModelTypeTopology:
				unwrappedOps[i] = porcupine.Operation{
					ClientId: op.ClientId,
					Input:    enhancedInput.TopologyOp,
					Output:   enhancedOutput.TopologyOut,
					Call:     op.Call,
					Return:   op.Return,
				}
			case ModelTypeFailureRecovery:
				unwrappedOps[i] = porcupine.Operation{
					ClientId: op.ClientId,
					Input:    enhancedInput.FailureRecoveryOp,
					Output:   enhancedOutput.FailureRecoveryOut,
					Call:     op.Call,
					Return:   op.Return,
				}
			case ModelTypeLeaderHA:
				unwrappedOps[i] = porcupine.Operation{
					ClientId: op.ClientId,
					Input:    enhancedInput.LeaderHAOp,
					Output:   enhancedOutput.LeaderHAOut,
					Call:     op.Call,
					Return:   op.Return,
				}
			}
		} else {
			// 不是 EnhancedInput，直接使用
			unwrappedOps[i] = op
		}
	}

	result := porcupine.CheckOperations(model, unwrappedOps)
	if result {
		return VerifyResult{
			ModelType: modelType,
			Passed:    true,
			Message:   "linearizability verification passed",
		}
	}
	return VerifyResult{
		ModelType: modelType,
		Passed:    false,
		Message:   "linearizability verification failed",
	}
}

// VerifyAllModels 使用所有增强模型验证操作历史
// 返回所有验证结果
func VerifyAllModels(recorder *EnhancedHistoryRecorder) []VerifyResult {
	results := make([]VerifyResult, 0)

	// 验证拓扑感知模型
	if topologyOps := recorder.GetTopologyOperations(); len(topologyOps) > 0 {
		results = append(results, VerifyOperationsWithModel(ModelTypeTopology, topologyOps))
	}

	// 验证失败恢复模型
	if frOps := recorder.GetFailureRecoveryOperations(); len(frOps) > 0 {
		results = append(results, VerifyOperationsWithModel(ModelTypeFailureRecovery, frOps))
	}

	// 验证 Leader HA 模型
	if leaderOps := recorder.GetLeaderHAOperations(); len(leaderOps) > 0 {
		results = append(results, VerifyOperationsWithModel(ModelTypeLeaderHA, leaderOps))
	}

	return results
}

// VerifyAllPassed 检查所有验证结果是否通过
func VerifyAllPassed(results []VerifyResult) bool {
	for _, r := range results {
		if !r.Passed {
			return false
		}
	}
	return true
}

// ==================== 模型创建工厂 ====================

// ModelFactory 模型创建工厂
type ModelFactory struct {
	registry *EnhancedModelRegistry
}

// NewModelFactory 创建模型工厂
func NewModelFactory() *ModelFactory {
	return &ModelFactory{
		registry: NewEnhancedModelRegistry(),
	}
}

// CreateModel 创建指定类型的模型
func (f *ModelFactory) CreateModel(modelType EnhancedModelType) (porcupine.Model, error) {
	return f.registry.GetModel(modelType)
}

// CreateAllModels 创建所有增强模型
func (f *ModelFactory) CreateAllModels() map[EnhancedModelType]porcupine.Model {
	result := make(map[EnhancedModelType]porcupine.Model)
	for _, t := range f.registry.ListModels() {
		model, _ := f.registry.GetModel(t)
		result[t] = model
	}
	return result
}

// ==================== 场景验证器 ====================

// ScenarioValidator 场景验证器
// 提供常见场景的预定义验证
type ScenarioValidator struct {
	recorder *EnhancedHistoryRecorder
	factory  *ModelFactory
}

// NewScenarioValidator 创建场景验证器
func NewScenarioValidator(clientID string, timestamp TimestampGenerator) *ScenarioValidator {
	return &ScenarioValidator{
		recorder: NewEnhancedHistoryRecorder(clientID, timestamp),
		factory:  NewModelFactory(),
	}
}

// Recorder 获取记录器
func (v *ScenarioValidator) Recorder() *EnhancedHistoryRecorder {
	return v.recorder
}

// ValidateTopologyScenario 验证拓扑感知场景
func (v *ScenarioValidator) ValidateTopologyScenario() VerifyResult {
	ops := v.recorder.GetTopologyOperations()
	if len(ops) == 0 {
		return VerifyResult{
			ModelType: ModelTypeTopology,
			Passed:    true,
			Message:   "no operations to verify",
		}
	}
	return VerifyOperationsWithModel(ModelTypeTopology, ops)
}

// ValidateFailureRecoveryScenario 验证失败恢复场景
func (v *ScenarioValidator) ValidateFailureRecoveryScenario() VerifyResult {
	ops := v.recorder.GetFailureRecoveryOperations()
	if len(ops) == 0 {
		return VerifyResult{
			ModelType: ModelTypeFailureRecovery,
			Passed:    true,
			Message:   "no operations to verify",
		}
	}
	return VerifyOperationsWithModel(ModelTypeFailureRecovery, ops)
}

// ValidateLeaderHAScenario 验证 Leader HA 场景
func (v *ScenarioValidator) ValidateLeaderHAScenario() VerifyResult {
	ops := v.recorder.GetLeaderHAOperations()
	if len(ops) == 0 {
		return VerifyResult{
			ModelType: ModelTypeLeaderHA,
			Passed:    true,
			Message:   "no operations to verify",
		}
	}
	return VerifyOperationsWithModel(ModelTypeLeaderHA, ops)
}

// ValidateAll 验证所有场景
func (v *ScenarioValidator) ValidateAll() []VerifyResult {
	return VerifyAllModels(v.recorder)
}

// Clear 清空记录器
func (v *ScenarioValidator) Clear() {
	v.recorder.Clear()
}

// ==================== 预定义场景模板 ====================

// RunTopologyInitScenario 运行拓扑初始化场景
func RunTopologyInitScenario(nodes []*NodeInfo) (bool, string) {
	validator := NewScenarioValidator("test", &testTimestampGenerator{})

	// 记录初始化操作
	initOp := TopologyOperation{
		Type:  TopologyOpInitTopology,
		Nodes: nodes,
	}
	opID := validator.Recorder().RecordTopologyCall(initOp)
	validator.Recorder().RecordTopologyReturn(opID, TopologyOutput{Ok: true})

	result := validator.ValidateTopologyScenario()
	return result.Passed, result.Message
}

// RunFailureRecoveryScenario 运行失败恢复场景
func RunFailureRecoveryScenario(allNodes, failedNodes []string) (bool, string) {
	validator := NewScenarioValidator("test", &testTimestampGenerator{})

	// 记录初始化
	initOp := FailureRecoveryOperation{
		Type:        FailureRecoveryOpInit,
		AllNodes:    allNodes,
		FailedNodes: failedNodes,
	}
	initID := validator.Recorder().RecordFailureRecoveryCall(initOp)
	validator.Recorder().RecordFailureRecoveryReturn(initID, FailureRecoveryOutput{Ok: true})

	// 记录节点故障
	for _, node := range failedNodes {
		failOp := FailureRecoveryOperation{
			Type:   FailureRecoveryOpNodeFail,
			NodeID: node,
		}
		failID := validator.Recorder().RecordFailureRecoveryCall(failOp)
		validator.Recorder().RecordFailureRecoveryReturn(failID, FailureRecoveryOutput{Ok: true})
	}

	result := validator.ValidateFailureRecoveryScenario()
	return result.Passed, result.Message
}

// RunLeaderHAScenario 运行 Leader HA 场景
func RunLeaderHAScenario(nodes []*NodeInfo, performSwitch bool) (bool, string) {
	validator := NewScenarioValidator("test", &testTimestampGenerator{})

	// 记录初始化
	initOp := LeaderHAOperation{
		Type:  LeaderHAOpInit,
		Nodes: nodes,
	}
	initID := validator.Recorder().RecordLeaderHACall(initOp)
	validator.Recorder().RecordLeaderHAReturn(initID, LeaderHAOutput{Ok: true, Term: 1})

	// 如果需要，执行 Leader 切换
	if performSwitch && len(nodes) > 1 {
		changeOp := LeaderHAOperation{
			Type:      LeaderHAOpLeaderChange,
			Term:      1,
			NewLeader: nodes[1].NodeID,
		}
		changeID := validator.Recorder().RecordLeaderHACall(changeOp)
		validator.Recorder().RecordLeaderHAReturn(changeID, LeaderHAOutput{
			Ok:         true,
			NewLeader:  nodes[1].NodeID,
			ActiveTerm: 2,
		})
	}

	result := validator.ValidateLeaderHAScenario()
	return result.Passed, result.Message
}

// ==================== 辅助类型 ====================

// testTimestampGenerator 测试用时间戳生成器（用于预定义场景）
type testTimestampGenerator struct {
	current int64
}

func (g *testTimestampGenerator) Now() int64 {
	g.current++
	return g.current
}
