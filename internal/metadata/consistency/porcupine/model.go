// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件定义 NexKV 状态模型，用于线性一致性验证
package porcupine

import (
	"fmt"

	"github.com/anishathalye/porcupine"
)

// OpType 操作类型枚举
type OpType int

const (
	// OpGet 读取操作
	OpGet OpType = iota
	// OpPut 写入操作
	OpPut
	// OpDelete 删除操作
	OpDelete
	// OpQuorumGet Quorum 读取操作
	OpQuorumGet
	// OpQuorumPut Quorum 写入操作
	OpQuorumPut
)

// String 返回操作类型的字符串表示
func (op OpType) String() string {
	switch op {
	case OpGet:
		return "Get"
	case OpPut:
		return "Put"
	case OpDelete:
		return "Delete"
	case OpQuorumGet:
		return "QuorumGet"
	case OpQuorumPut:
		return "QuorumPut"
	default:
		return fmt.Sprintf("Unknown(%d)", op)
	}
}

// 错误常量
const (
	// ErrKeyNotFound key 不存在错误
	ErrKeyNotFound = "key not found"
)

// NexKVInput 输入类型
// 定义客户端请求操作的参数
type NexKVInput struct {
	Op        OpType // 操作类型
	Namespace string // 命名空间
	Key       string // 键
	Value     []byte // 值（仅 Put 操作使用）
}

// KeyWithNamespace 返回带命名空间的完整 key
// 格式: namespace:key，命名空间为空时返回 key
func (input NexKVInput) KeyWithNamespace() string {
	if input.Namespace == "" {
		return input.Key
	}
	return input.Namespace + ":" + input.Key
}

// NexKVOutput 输出类型
// 定义操作返回的结果
type NexKVOutput struct {
	Ok    bool   // 操作是否成功
	Value []byte // 读取的值（仅 Get 操作使用）
	Error string // 错误信息
}

// NexKVModel 顺序规范模型
// 定义 NexKV 的状态空间和状态转移函数
// 用于 Porcupine 线性一致性验证
// 使用 NondeterministicModel 以支持自定义状态比较
var NexKVModel = func() porcupine.Model {
	nm := newNondeterministicModel()
	return nm.ToModel()
}()

// newNondeterministicModel 创建非确定性模型
func newNondeterministicModel() *porcupine.NondeterministicModel {
	return &porcupine.NondeterministicModel{
		// Init 初始化状态
		// 返回初始状态列表（这里只有一个空状态）
		Init: func() []interface{} {
			return []interface{}{make(map[string]string)}
		},

		// Step 状态转移函数
		// 返回所有可能的下一状态
		Step: func(state, input, output interface{}) []interface{} {
			kvState, ok := state.(map[string]string)
			if !ok {
				return nil
			}

			kvInput, ok := input.(NexKVInput)
			if !ok {
				return nil
			}

			kvOutput, ok := output.(NexKVOutput)
			if !ok {
				return nil
			}

			// 获取完整 key（带命名空间）
			fullKey := kvInput.KeyWithNamespace()

			switch kvInput.Op {
			case OpGet, OpQuorumGet:
				// Get 操作：验证返回值是否正确
				value, exists := kvState[fullKey]
				if !exists {
					// key 不存在，output 应该表示失败
					if !kvOutput.Ok && kvOutput.Error == ErrKeyNotFound {
						return []interface{}{state}
					}
					return nil
				}
				// key 存在，验证返回值是否匹配
				if !kvOutput.Ok {
					return nil
				}
				if value != string(kvOutput.Value) {
					return nil
				}
				return []interface{}{state}

			case OpPut, OpQuorumPut:
				// Put 操作：更新状态
				if !kvOutput.Ok {
					return nil
				}
				// 复制状态
				newState := make(map[string]string)
				for k, v := range kvState {
					newState[k] = v
				}
				newState[fullKey] = string(kvInput.Value)
				return []interface{}{newState}

			case OpDelete:
				// Delete 操作：从状态中删除 key
				if !kvOutput.Ok {
					return nil
				}
				newState := make(map[string]string)
				for k, v := range kvState {
					if k != fullKey {
						newState[k] = v
					}
				}
				return []interface{}{newState}

			default:
				return nil
			}
		},

		// Equal 自定义状态比较函数
		// 用于比较两个 map 是否相等
		Equal: func(state1, state2 interface{}) bool {
			m1, ok1 := state1.(map[string]string)
			m2, ok2 := state2.(map[string]string)
			if !ok1 || !ok2 {
				return false
			}
			if len(m1) != len(m2) {
				return false
			}
			for k, v1 := range m1 {
				if v2, exists := m2[k]; !exists || v1 != v2 {
					return false
				}
			}
			return true
		},

		// DescribeOperation 描述操作（用于可视化）
		DescribeOperation: func(input, output interface{}) string {
			kvInput, _ := input.(NexKVInput)
			kvOutput, _ := output.(NexKVOutput)
			return fmt.Sprintf("%s(%s:%s) -> %v", kvInput.Op, kvInput.Namespace, kvInput.Key, kvOutput.Ok)
		},

		// DescribeState 描述状态（用于可视化）
		DescribeState: func(state interface{}) string {
			kvState, _ := state.(map[string]string)
			return fmt.Sprintf("%v", kvState)
		},
	}
}
