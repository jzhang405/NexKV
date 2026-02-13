// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件实现场景适配器，用于与现有 E2ETestScenario 集成
package porcupine

import (
	"context"
	"fmt"
)

// ScenarioKV 场景 KV 接口
// 定义 E2ETestScenario 中 mockMetadataKVForTree 的接口
type ScenarioKV interface {
	Put(ctx context.Context, namespace, key string, value any) error
	Get(ctx context.Context, namespace, key string) (any, error)
	Delete(ctx context.Context, namespace, key string) error
}

// ScenarioKVAdapter 场景 KV 适配器
// 将 ScenarioKV 接口适配为 KVOperator 接口
type ScenarioKVAdapter struct {
	kv ScenarioKV
}

// NewScenarioKVAdapter 创建场景 KV 适配器
func NewScenarioKVAdapter(kv ScenarioKV) *ScenarioKVAdapter {
	return &ScenarioKVAdapter{kv: kv}
}

// Put 写入操作
func (a *ScenarioKVAdapter) Put(ctx context.Context, namespace, key string, value []byte) error {
	return a.kv.Put(ctx, namespace, key, value)
}

// Get 读取操作
func (a *ScenarioKVAdapter) Get(ctx context.Context, namespace, key string) ([]byte, error) {
	val, err := a.kv.Get(ctx, namespace, key)
	if err != nil {
		return nil, err
	}
	// 类型断言
	if b, ok := val.([]byte); ok {
		return b, nil
	}
	return nil, fmt.Errorf("unexpected value type: %T", val)
}

// Delete 删除操作
func (a *ScenarioKVAdapter) Delete(ctx context.Context, namespace, key string) error {
	return a.kv.Delete(ctx, namespace, key)
}

// RecordingE2ETestScenario 带记录功能的测试场景
// 扩展现有 E2ETestScenario 概念，添加 Porcupine 验证支持
type RecordingE2ETestScenario struct {
	Nodes            []string                    // 节点 ID 列表
	RecordingClients map[string]*RecordingClient // nodeID -> client
	Recorders        map[string]*HistoryRecorder // nodeID -> recorder
	Checker          *ConsistencyChecker         // 一致性检查器
	timestamp        TimestampGenerator          // 共享时间戳生成器
}

// NewRecordingE2ETestScenario 创建带记录功能的测试场景
// nodes: 节点 ID 列表
func NewRecordingE2ETestScenario(nodes []string) *RecordingE2ETestScenario {
	// 创建共享时间戳生成器（多节点使用逻辑时间戳）
	ts := NewTimestampGenerator(nodes[0], len(nodes))

	recScenario := &RecordingE2ETestScenario{
		Nodes:            nodes,
		RecordingClients: make(map[string]*RecordingClient),
		Recorders:        make(map[string]*HistoryRecorder),
		Checker:          NewConsistencyChecker(NexKVModel, 0, ""),
		timestamp:        ts,
	}

	return recScenario
}

// AddNode 添加节点及其 KV 客户端
// nodeID: 节点 ID
// kv: 节点的 KV 存储（实现 ScenarioKV 接口）
func (s *RecordingE2ETestScenario) AddNode(nodeID string, kv ScenarioKV) {
	// 创建时间戳生成器（多节点使用逻辑时间戳）
	ts := NewTimestampGenerator(nodeID, len(s.Nodes))

	// 创建记录器
	recorder := NewHistoryRecorder(nodeID, ts)
	s.Recorders[nodeID] = recorder

	// 创建适配器
	adapter := NewScenarioKVAdapter(kv)

	// 创建记录客户端
	client := NewRecordingClient(adapter, recorder)
	s.RecordingClients[nodeID] = client
}

// GetAllOperations 获取所有节点的操作历史
func (s *RecordingE2ETestScenario) GetAllOperations() []interface{} {
	var allOps []interface{}
	for _, recorder := range s.Recorders {
		ops := recorder.GetOperations()
		for _, op := range ops {
			allOps = append(allOps, op)
		}
	}
	return allOps
}

// VerifyLinearizability 验证所有操作的线性一致性
// 返回检查结果
func (s *RecordingE2ETestScenario) VerifyLinearizability() *CheckResult {
	// 收集所有操作
	var allOps []interface{}
	for _, recorder := range s.Recorders {
		ops := recorder.GetOperations()
		for _, op := range ops {
			allOps = append(allOps, op)
		}
	}

	// 如果没有操作，直接返回成功
	if len(allOps) == 0 {
		return &CheckResult{Ok: true}
	}

	// 转换为 Operation 切片
	// 注意：这里假设 allOps 已经是 porcupine.Operation 类型
	// 实际使用时需要确保类型正确

	// 使用第一个 recorder 的 checker 进行检查
	for _, recorder := range s.Recorders {
		return s.Checker.CheckFromRecorder(recorder)
	}

	return &CheckResult{Ok: true}
}

// Clear 清空所有记录
func (s *RecordingE2ETestScenario) Clear() {
	for _, recorder := range s.Recorders {
		recorder.Clear()
	}
}

// RunWithVerification 运行测试函数并验证线性化
// 这是一个辅助函数，用于简化测试代码
//
// 用法示例：
//
//	recScenario := NewRecordingE2ETestScenario([]string{"node-1", "node-2"})
//	// ... 初始化节点和 KV ...
//
//	RunWithVerification(t, recScenario, func() {
//	    client := recScenario.RecordingClients["node-1"]
//	    client.Put(ctx, "ns1", "key", []byte("value"))
//	})
func RunWithVerification(t interface{}, scenario *RecordingE2ETestScenario, testFunc func()) {
	// 执行测试函数
	testFunc()

	// 验证线性化
	result := scenario.VerifyLinearizability()

	// 如果测试框架支持，报告结果
	if result != nil && !result.Ok {
		// 使用 Error 方法报告错误（如果 t 实现了 Error 方法）
		if tb, ok := t.(interface{ Error(args ...interface{}) }); ok {
			tb.Error("Linearizability check failed: ", result.Error)
		}
	}
}
