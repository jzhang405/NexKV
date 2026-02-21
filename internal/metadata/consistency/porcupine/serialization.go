// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件实现操作序列化，支持 JSON 导出/导入
package porcupine

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/anishathalye/porcupine"
)

// ==================== 可序列化操作定义 ====================

// SerializableOperation JSON 可序列化的操作记录
// 解决 porcupine.Operation 的 any 序列化问题
type SerializableOperation struct {
	ClientID  int             `json:"client_id"`
	Input     json.RawMessage `json:"input"`  // 序列化后的 EnhancedInput
	Output    json.RawMessage `json:"output"` // 序列化后的 EnhancedOutput
	Call      int64           `json:"call"`
	Return    int64           `json:"return"`
	ModelType EnhancedOpType  `json:"model_type"` // 标记属于哪种模型
}

// SerializableInput 可序列化的输入（用于 API 传输）
type SerializableInput struct {
	Type              EnhancedOpType                 `json:"type"`
	TopologyOp        *SerializableTopologyOp        `json:"topology_op,omitempty"`
	FailureRecoveryOp *SerializableFailureRecoveryOp `json:"failure_recovery_op,omitempty"`
	LeaderHAOp        *SerializableLeaderHAOp        `json:"leader_ha_op,omitempty"`
}

// SerializableOutput 可序列化的输出（用于 API 传输）
type SerializableOutput struct {
	Type               EnhancedOpType                  `json:"type"`
	TopologyOut        *SerializableTopologyOut        `json:"topology_out,omitempty"`
	FailureRecoveryOut *SerializableFailureRecoveryOut `json:"failure_recovery_out,omitempty"`
	LeaderHAOut        *SerializableLeaderHAOut        `json:"leader_ha_out,omitempty"`
}

// SerializableTopologyOp 可序列化的拓扑操作
type SerializableTopologyOp struct {
	Type         string      `json:"type"`
	NodeID       string      `json:"node_id"`
	Key          string      `json:"key"`
	Value        string      `json:"value"` // Base64 编码
	Version      uint64      `json:"version"`
	Term         uint64      `json:"term"`
	Participants []string    `json:"participants"`
	Nodes        []*NodeInfo `json:"nodes"`
}

// SerializableTopologyOut 可序列化的拓扑输出
type SerializableTopologyOut struct {
	Ok      bool   `json:"ok"`
	Value   string `json:"value"` // Base64 编码
	Version uint64 `json:"version"`
	Error   string `json:"error"`
}

// SerializableFailureRecoveryOp 可序列化的故障恢复操作
type SerializableFailureRecoveryOp struct {
	Type         string   `json:"type"`
	NodeID       string   `json:"node_id"`
	Key          string   `json:"key"`
	Value        string   `json:"value"` // Base64 编码
	Version      uint64   `json:"version"`
	Participants []string `json:"participants"`
	FailedNodes  []string `json:"failed_nodes"`
	AllNodes     []string `json:"all_nodes"`
}

// SerializableFailureRecoveryOut 可序列化的故障恢复输出
type SerializableFailureRecoveryOut struct {
	Ok      bool   `json:"ok"`
	Value   string `json:"value"` // Base64 编码
	Version uint64 `json:"version"`
	Error   string `json:"error"`
}

// SerializableLeaderHAOp 可序列化的 Leader HA 操作
type SerializableLeaderHAOp struct {
	Type      string      `json:"type"`
	NodeID    string      `json:"node_id"`
	Key       string      `json:"key"`
	Value     string      `json:"value"` // Base64 编码
	Version   uint64      `json:"version"`
	Term      uint64      `json:"term"`
	Nodes     []*NodeInfo `json:"nodes"`
	NewLeader string      `json:"new_leader"`
}

// SerializableLeaderHAOut 可序列化的 Leader HA 输出
type SerializableLeaderHAOut struct {
	Ok         bool   `json:"ok"`
	Value      string `json:"value"` // Base64 编码
	Version    uint64 `json:"version"`
	Term       uint64 `json:"term"`
	Error      string `json:"error"`
	NewLeader  string `json:"new_leader"`
	ActiveTerm uint64 `json:"active_term"`
}

// ==================== 序列化器 ====================

// OperationSerializer 操作序列化器
type OperationSerializer struct{}

// NewOperationSerializer 创建操作序列化器
func NewOperationSerializer() *OperationSerializer {
	return &OperationSerializer{}
}

// SerializeOperation 序列化单个操作
func (s *OperationSerializer) SerializeOperation(op porcupine.Operation) (*SerializableOperation, error) {
	input, ok := op.Input.(EnhancedInput)
	if !ok {
		return nil, fmt.Errorf("invalid input type: %T", op.Input)
	}

	output, ok := op.Output.(EnhancedOutput)
	if !ok {
		return nil, fmt.Errorf("invalid output type: %T", op.Output)
	}

	// 序列化输入
	serializableInput, err := s.serializeInput(input)
	if err != nil {
		return nil, fmt.Errorf("serialize input failed: %w", err)
	}

	inputJSON, err := json.Marshal(serializableInput)
	if err != nil {
		return nil, fmt.Errorf("marshal input failed: %w", err)
	}

	// 序列化输出
	serializableOutput, err := s.serializeOutput(output)
	if err != nil {
		return nil, fmt.Errorf("serialize output failed: %w", err)
	}

	outputJSON, err := json.Marshal(serializableOutput)
	if err != nil {
		return nil, fmt.Errorf("marshal output failed: %w", err)
	}

	return &SerializableOperation{
		ClientID:  op.ClientId,
		Input:     inputJSON,
		Output:    outputJSON,
		Call:      op.Call,
		Return:    op.Return,
		ModelType: input.Type,
	}, nil
}

// DeserializeOperation 反序列化单个操作
func (s *OperationSerializer) DeserializeOperation(serialized *SerializableOperation) (porcupine.Operation, error) {
	var input SerializableInput
	if err := json.Unmarshal(serialized.Input, &input); err != nil {
		return porcupine.Operation{}, fmt.Errorf("unmarshal input failed: %w", err)
	}

	var output SerializableOutput
	if err := json.Unmarshal(serialized.Output, &output); err != nil {
		return porcupine.Operation{}, fmt.Errorf("unmarshal output failed: %w", err)
	}

	// 反序列化输入
	enhancedInput, err := s.deserializeInput(input)
	if err != nil {
		return porcupine.Operation{}, fmt.Errorf("deserialize input failed: %w", err)
	}

	// 反序列化输出
	enhancedOutput, err := s.deserializeOutput(output)
	if err != nil {
		return porcupine.Operation{}, fmt.Errorf("deserialize output failed: %w", err)
	}

	return porcupine.Operation{
		ClientId: serialized.ClientID,
		Input:    enhancedInput,
		Output:   enhancedOutput,
		Call:     serialized.Call,
		Return:   serialized.Return,
	}, nil
}

// ==================== 输入序列化 ====================

func (s *OperationSerializer) serializeInput(input EnhancedInput) (*SerializableInput, error) {
	result := &SerializableInput{
		Type: input.Type,
	}

	switch input.Type {
	case OpTypeTopology:
		serializableOp, err := s.serializeTopologyOp(input.TopologyOp)
		if err != nil {
			return nil, err
		}
		result.TopologyOp = serializableOp

	case OpTypeFailureRecovery:
		serializableOp, err := s.serializeFailureRecoveryOp(input.FailureRecoveryOp)
		if err != nil {
			return nil, err
		}
		result.FailureRecoveryOp = serializableOp

	case OpTypeLeaderHA:
		serializableOp, err := s.serializeLeaderHAOp(input.LeaderHAOp)
		if err != nil {
			return nil, err
		}
		result.LeaderHAOp = serializableOp
	}

	return result, nil
}

func (s *OperationSerializer) deserializeInput(input SerializableInput) (EnhancedInput, error) {
	result := EnhancedInput{
		Type: input.Type,
	}

	switch input.Type {
	case OpTypeTopology:
		if input.TopologyOp != nil {
			op, err := s.deserializeTopologyOp(*input.TopologyOp)
			if err != nil {
				return EnhancedInput{}, err
			}
			result.TopologyOp = op
		}

	case OpTypeFailureRecovery:
		if input.FailureRecoveryOp != nil {
			op, err := s.deserializeFailureRecoveryOp(*input.FailureRecoveryOp)
			if err != nil {
				return EnhancedInput{}, err
			}
			result.FailureRecoveryOp = op
		}

	case OpTypeLeaderHA:
		if input.LeaderHAOp != nil {
			op, err := s.deserializeLeaderHAOp(*input.LeaderHAOp)
			if err != nil {
				return EnhancedInput{}, err
			}
			result.LeaderHAOp = op
		}
	}

	return result, nil
}

// ==================== 拓扑操作序列化 ====================

func (s *OperationSerializer) serializeTopologyOp(op TopologyOperation) (*SerializableTopologyOp, error) {
	return &SerializableTopologyOp{
		Type:         op.Type.String(),
		NodeID:       op.NodeID,
		Key:          op.Key,
		Value:        encodeBase64(op.Value),
		Version:      op.Version,
		Term:         op.Term,
		Participants: op.Participants,
		Nodes:        op.Nodes,
	}, nil
}

func (s *OperationSerializer) deserializeTopologyOp(op SerializableTopologyOp) (TopologyOperation, error) {
	opType, err := parseTopologyOpType(op.Type)
	if err != nil {
		return TopologyOperation{}, err
	}

	value, err := decodeBase64(op.Value)
	if err != nil {
		return TopologyOperation{}, fmt.Errorf("decode value failed: %w", err)
	}

	return TopologyOperation{
		Type:         opType,
		NodeID:       op.NodeID,
		Key:          op.Key,
		Value:        value,
		Version:      op.Version,
		Term:         op.Term,
		Participants: op.Participants,
		Nodes:        op.Nodes,
	}, nil
}

func (s *OperationSerializer) serializeTopologyOut(out TopologyOutput) (*SerializableTopologyOut, error) {
	return &SerializableTopologyOut{
		Ok:      out.Ok,
		Value:   encodeBase64(out.Value),
		Version: out.Version,
		Error:   out.Error,
	}, nil
}

func (s *OperationSerializer) deserializeTopologyOut(out SerializableTopologyOut) (TopologyOutput, error) {
	value, err := decodeBase64(out.Value)
	if err != nil {
		return TopologyOutput{}, fmt.Errorf("decode value failed: %w", err)
	}

	return TopologyOutput{
		Ok:      out.Ok,
		Value:   value,
		Version: out.Version,
		Error:   out.Error,
	}, nil
}

// ==================== 故障恢复操作序列化 ====================

func (s *OperationSerializer) serializeFailureRecoveryOp(op FailureRecoveryOperation) (*SerializableFailureRecoveryOp, error) {
	return &SerializableFailureRecoveryOp{
		Type:         op.Type.String(),
		NodeID:       op.NodeID,
		Key:          op.Key,
		Value:        encodeBase64(op.Value),
		Version:      op.Version,
		Participants: op.Participants,
		FailedNodes:  op.FailedNodes,
		AllNodes:     op.AllNodes,
	}, nil
}

func (s *OperationSerializer) deserializeFailureRecoveryOp(op SerializableFailureRecoveryOp) (FailureRecoveryOperation, error) {
	opType, err := parseFailureRecoveryOpType(op.Type)
	if err != nil {
		return FailureRecoveryOperation{}, err
	}

	value, err := decodeBase64(op.Value)
	if err != nil {
		return FailureRecoveryOperation{}, fmt.Errorf("decode value failed: %w", err)
	}

	return FailureRecoveryOperation{
		Type:         opType,
		NodeID:       op.NodeID,
		Key:          op.Key,
		Value:        value,
		Version:      op.Version,
		Participants: op.Participants,
		FailedNodes:  op.FailedNodes,
		AllNodes:     op.AllNodes,
	}, nil
}

func (s *OperationSerializer) serializeFailureRecoveryOut(out FailureRecoveryOutput) (*SerializableFailureRecoveryOut, error) {
	return &SerializableFailureRecoveryOut{
		Ok:      out.Ok,
		Value:   encodeBase64(out.Value),
		Version: out.Version,
		Error:   out.Error,
	}, nil
}

func (s *OperationSerializer) deserializeFailureRecoveryOut(out SerializableFailureRecoveryOut) (FailureRecoveryOutput, error) {
	value, err := decodeBase64(out.Value)
	if err != nil {
		return FailureRecoveryOutput{}, fmt.Errorf("decode value failed: %w", err)
	}

	return FailureRecoveryOutput{
		Ok:      out.Ok,
		Value:   value,
		Version: out.Version,
		Error:   out.Error,
	}, nil
}

// ==================== Leader HA 操作序列化 ====================

func (s *OperationSerializer) serializeLeaderHAOp(op LeaderHAOperation) (*SerializableLeaderHAOp, error) {
	return &SerializableLeaderHAOp{
		Type:      op.Type.String(),
		NodeID:    op.NodeID,
		Key:       op.Key,
		Value:     encodeBase64(op.Value),
		Version:   op.Version,
		Term:      op.Term,
		Nodes:     op.Nodes,
		NewLeader: op.NewLeader,
	}, nil
}

func (s *OperationSerializer) deserializeLeaderHAOp(op SerializableLeaderHAOp) (LeaderHAOperation, error) {
	opType, err := parseLeaderHAOpType(op.Type)
	if err != nil {
		return LeaderHAOperation{}, err
	}

	value, err := decodeBase64(op.Value)
	if err != nil {
		return LeaderHAOperation{}, fmt.Errorf("decode value failed: %w", err)
	}

	return LeaderHAOperation{
		Type:      opType,
		NodeID:    op.NodeID,
		Key:       op.Key,
		Value:     value,
		Version:   op.Version,
		Term:      op.Term,
		Nodes:     op.Nodes,
		NewLeader: op.NewLeader,
	}, nil
}

func (s *OperationSerializer) serializeLeaderHAOut(out LeaderHAOutput) (*SerializableLeaderHAOut, error) {
	return &SerializableLeaderHAOut{
		Ok:         out.Ok,
		Value:      encodeBase64(out.Value),
		Version:    out.Version,
		Term:       out.Term,
		Error:      out.Error,
		NewLeader:  out.NewLeader,
		ActiveTerm: out.ActiveTerm,
	}, nil
}

func (s *OperationSerializer) deserializeLeaderHAOut(out SerializableLeaderHAOut) (LeaderHAOutput, error) {
	value, err := decodeBase64(out.Value)
	if err != nil {
		return LeaderHAOutput{}, fmt.Errorf("decode value failed: %w", err)
	}

	return LeaderHAOutput{
		Ok:         out.Ok,
		Value:      value,
		Version:    out.Version,
		Term:       out.Term,
		Error:      out.Error,
		NewLeader:  out.NewLeader,
		ActiveTerm: out.ActiveTerm,
	}, nil
}

// ==================== 输出序列化 ====================

func (s *OperationSerializer) serializeOutput(output EnhancedOutput) (*SerializableOutput, error) {
	result := &SerializableOutput{
		Type: output.Type,
	}

	switch output.Type {
	case OpTypeTopology:
		serializableOut, err := s.serializeTopologyOut(output.TopologyOut)
		if err != nil {
			return nil, err
		}
		result.TopologyOut = serializableOut

	case OpTypeFailureRecovery:
		serializableOut, err := s.serializeFailureRecoveryOut(output.FailureRecoveryOut)
		if err != nil {
			return nil, err
		}
		result.FailureRecoveryOut = serializableOut

	case OpTypeLeaderHA:
		serializableOut, err := s.serializeLeaderHAOut(output.LeaderHAOut)
		if err != nil {
			return nil, err
		}
		result.LeaderHAOut = serializableOut
	}

	return result, nil
}

func (s *OperationSerializer) deserializeOutput(output SerializableOutput) (EnhancedOutput, error) {
	result := EnhancedOutput{
		Type: output.Type,
	}

	switch output.Type {
	case OpTypeTopology:
		if output.TopologyOut != nil {
			out, err := s.deserializeTopologyOut(*output.TopologyOut)
			if err != nil {
				return EnhancedOutput{}, err
			}
			result.TopologyOut = out
		}

	case OpTypeFailureRecovery:
		if output.FailureRecoveryOut != nil {
			out, err := s.deserializeFailureRecoveryOut(*output.FailureRecoveryOut)
			if err != nil {
				return EnhancedOutput{}, err
			}
			result.FailureRecoveryOut = out
		}

	case OpTypeLeaderHA:
		if output.LeaderHAOut != nil {
			out, err := s.deserializeLeaderHAOut(*output.LeaderHAOut)
			if err != nil {
				return EnhancedOutput{}, err
			}
			result.LeaderHAOut = out
		}
	}

	return result, nil
}

// ==================== 辅助函数 ====================

// encodeBase64 Base64 编码 []byte
func encodeBase64(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

// decodeBase64 Base64 解码到 []byte
func decodeBase64(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

// parseTopologyOpType 解析拓扑操作类型字符串
func parseTopologyOpType(s string) (TopologyOpType, error) {
	switch s {
	case "InitTopology":
		return TopologyOpInitTopology, nil
	case "Write2PC":
		return TopologyOpWrite2PC, nil
	case "WriteQuorum":
		return TopologyOpWriteQuorum, nil
	case "WriteGossip":
		return TopologyOpWriteGossip, nil
	case "Get":
		return TopologyOpGet, nil
	default:
		return 0, fmt.Errorf("unknown topology operation type: %s", s)
	}
}

// parseFailureRecoveryOpType 解析故障恢复操作类型字符串
func parseFailureRecoveryOpType(s string) (FailureRecoveryOpType, error) {
	switch s {
	case "Init":
		return FailureRecoveryOpInit, nil
	case "NodeFail":
		return FailureRecoveryOpNodeFail, nil
	case "NodeRecover":
		return FailureRecoveryOpNodeRecover, nil
	case "QuorumWrite":
		return FailureRecoveryOpQuorumWrite, nil
	case "Get":
		return FailureRecoveryOpGet, nil
	default:
		return 0, fmt.Errorf("unknown failure recovery operation type: %s", s)
	}
}

// parseLeaderHAOpType 解析 Leader HA 操作类型字符串
func parseLeaderHAOpType(s string) (LeaderHAOpType, error) {
	switch s {
	case "Init":
		return LeaderHAOpInit, nil
	case "LeaderChange":
		return LeaderHAOpLeaderChange, nil
	case "Write":
		return LeaderHAOpWrite, nil
	case "Get":
		return LeaderHAOpGet, nil
	default:
		return 0, fmt.Errorf("unknown leader HA operation type: %s", s)
	}
}
