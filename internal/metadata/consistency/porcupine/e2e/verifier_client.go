// Package e2e 提供 E2E 测试框架的 Porcupine 验证集成
// 本文件实现 E2E 验证客户端
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	porcupineLib "github.com/anishathalye/porcupine"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

// ==================== 客户端配置 ====================

// E2EVerifierClientConfig E2E 验证客户端配置
type E2EVerifierClientConfig struct {
	// 节点地址映射：nodeID -> Debug API 地址
	Nodes map[string]string `json:"nodes"`

	// 请求配置
	Timeout    time.Duration `json:"timeout"`     // 请求超时
	AuthToken  string        `json:"auth_token"`  // 认证 Token
	MaxRetries int           `json:"max_retries"` // 最大重试次数

	// 验证配置
	ModelType porcupine.EnhancedOpType `json:"model_type"` // 验证模型类型
}

// DefaultE2EVerifierClientConfig 默认配置
func DefaultE2EVerifierClientConfig() E2EVerifierClientConfig {
	return E2EVerifierClientConfig{
		Nodes:      make(map[string]string),
		Timeout:    10 * time.Second,
		MaxRetries: 3,
	}
}

// ==================== 客户端实现 ====================

// E2EVerifierClient E2E 验证客户端
type E2EVerifierClient struct {
	config     E2EVerifierClientConfig
	serializer *porcupine.OperationSerializer
	httpClient *http.Client
	mu         sync.RWMutex
}

// NewE2EVerifierClient 创建 E2E 验证客户端
func NewE2EVerifierClient(config E2EVerifierClientConfig) *E2EVerifierClient {
	return &E2EVerifierClient{
		config:     config,
		serializer: porcupine.NewOperationSerializer(),
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}
}

// AddNode 添加节点
func (c *E2EVerifierClient) AddNode(nodeID, addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.Nodes[nodeID] = addr
}

// RemoveNode 移除节点
func (c *E2EVerifierClient) RemoveNode(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.config.Nodes, nodeID)
}

// ==================== 获取历史 ====================

// GetNodeHistory 获取指定节点的操作历史
func (c *E2EVerifierClient) GetNodeHistory(ctx context.Context, nodeID string) ([]porcupineLib.Operation, error) {
	c.mu.RLock()
	addr, exists := c.config.Nodes[nodeID]
	c.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}

	// 发送请求
	url := fmt.Sprintf("http://%s/debug/porcupine/history", addr)
	resp, err := c.doRequest(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// 解析响应
	var response struct {
		NodeID     string                             `json:"node_id"`
		Operations []*porcupine.SerializableOperation `json:"operations"`
		Count      int                                `json:"count"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	// 反序列化操作
	ops := make([]porcupineLib.Operation, 0, len(response.Operations))
	for _, ser := range response.Operations {
		op, err := c.serializer.DeserializeOperation(ser)
		if err != nil {
			return nil, fmt.Errorf("deserialize operation failed: %w", err)
		}
		ops = append(ops, op)
	}

	return ops, nil
}

// GetAllHistories 获取所有节点的操作历史
func (c *E2EVerifierClient) GetAllHistories(ctx context.Context) (map[string][]porcupineLib.Operation, error) {
	c.mu.RLock()
	nodes := make(map[string]string, len(c.config.Nodes))
	for k, v := range c.config.Nodes {
		nodes[k] = v
	}
	c.mu.RUnlock()

	result := make(map[string][]porcupineLib.Operation)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errs := make([]error, 0)
	var errsMu sync.Mutex

	for nodeID, addr := range nodes {
		wg.Add(1)
		go func(nodeID, addr string) {
			defer wg.Done()

			ops, err := c.GetNodeHistory(ctx, nodeID)
			if err != nil {
				errsMu.Lock()
				errs = append(errs, fmt.Errorf("node %s: %w", nodeID, err))
				errsMu.Unlock()
				return
			}

			mu.Lock()
			result[nodeID] = ops
			mu.Unlock()
		}(nodeID, addr)
	}

	wg.Wait()

	if len(errs) > 0 {
		return result, fmt.Errorf("some nodes failed: %v", errs)
	}

	return result, nil
}

// ==================== 集群验证 ====================

// ClusterVerificationResult 集群验证结果
type ClusterVerificationResult struct {
	TotalOps     int                         `json:"total_ops"`
	NodeResults  map[string]NodeVerifyResult `json:"node_results"`
	AllPassed    bool                        `json:"all_passed"`
	ErrorMessage string                      `json:"error_message,omitempty"`
	Duration     time.Duration               `json:"duration"`
}

// NodeVerifyResult 单节点验证结果
type NodeVerifyResult struct {
	OpsCount  int    `json:"ops_count"`
	Passed    bool   `json:"passed"`
	Error     string `json:"error,omitempty"`
	ModelType string `json:"model_type"`
}

// VerifyCluster 验证集群一致性
func (c *E2EVerifierClient) VerifyCluster(ctx context.Context, modelType porcupine.EnhancedOpType) (*ClusterVerificationResult, error) {
	start := time.Now()

	// 获取所有历史
	histories, err := c.GetAllHistories(ctx)
	if err != nil {
		return nil, fmt.Errorf("get histories failed: %w", err)
	}

	result := &ClusterVerificationResult{
		NodeResults: make(map[string]NodeVerifyResult),
		AllPassed:   true,
	}

	// 验证每个节点
	for nodeID, ops := range histories {
		result.TotalOps += len(ops)

		// 过滤指定模型类型的操作
		filteredOps := c.filterOpsByModel(ops, modelType)

		if len(filteredOps) == 0 {
			result.NodeResults[nodeID] = NodeVerifyResult{
				OpsCount:  0,
				Passed:    true,
				ModelType: modelType.String(),
				Error:     "no operations to verify",
			}
			continue
		}

		// 使用 Porcupine 验证
		passed, msg := c.verifyOps(filteredOps, modelType)

		result.NodeResults[nodeID] = NodeVerifyResult{
			OpsCount:  len(filteredOps),
			Passed:    passed,
			ModelType: modelType.String(),
			Error:     msg,
		}

		if !passed {
			result.AllPassed = false
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// VerifyClusterAll 验证所有模型类型
func (c *E2EVerifierClient) VerifyClusterAll(ctx context.Context) (map[porcupine.EnhancedOpType]*ClusterVerificationResult, error) {
	results := make(map[porcupine.EnhancedOpType]*ClusterVerificationResult)

	modelTypes := []porcupine.EnhancedOpType{
		porcupine.OpTypeTopology,
		porcupine.OpTypeFailureRecovery,
		porcupine.OpTypeLeaderHA,
	}

	for _, mt := range modelTypes {
		result, err := c.VerifyCluster(ctx, mt)
		if err != nil {
			results[mt] = &ClusterVerificationResult{
				AllPassed:    false,
				ErrorMessage: err.Error(),
			}
		} else {
			results[mt] = result
		}
	}

	return results, nil
}

// ==================== 辅助方法 ====================

// filterOpsByModel 过滤指定模型类型的操作
func (c *E2EVerifierClient) filterOpsByModel(ops []porcupineLib.Operation, modelType porcupine.EnhancedOpType) []porcupineLib.Operation {
	filtered := make([]porcupineLib.Operation, 0)
	for _, op := range ops {
		if input, ok := op.Input.(porcupine.EnhancedInput); ok {
			if input.Type == modelType {
				filtered = append(filtered, op)
			}
		}
	}
	return filtered
}

// verifyOps 使用 Porcupine 验证操作
func (c *E2EVerifierClient) verifyOps(ops []porcupineLib.Operation, modelType porcupine.EnhancedOpType) (bool, string) {
	switch modelType {
	case porcupine.OpTypeTopology:
		model := porcupine.TopologyAwareModel()
		result := porcupineLib.CheckOperations(model, ops)
		if result {
			return true, "topology model verification passed"
		}
		return false, "topology model verification failed"

	case porcupine.OpTypeFailureRecovery:
		model := porcupine.FailureRecoveryModel()
		result := porcupineLib.CheckOperations(model, ops)
		if result {
			return true, "failure recovery model verification passed"
		}
		return false, "failure recovery model verification failed"

	case porcupine.OpTypeLeaderHA:
		model := porcupine.LeaderHAModel()
		result := porcupineLib.CheckOperations(model, ops)
		if result {
			return true, "leader HA model verification passed"
		}
		return false, "leader HA model verification failed"

	default:
		return false, fmt.Sprintf("unknown model type: %d", modelType)
	}
}

// doRequest 发送 HTTP 请求
func (c *E2EVerifierClient) doRequest(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	// 添加认证头
	if c.config.AuthToken != "" {
		req.Header.Set("X-Debug-Token", c.config.AuthToken)
	}

	// 重试逻辑
	var lastErr error
	for i := 0; i <= c.config.MaxRetries; i++ {
		resp, err := c.httpClient.Do(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		// 等待后重试
		if i < c.config.MaxRetries {
			time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
		}
	}

	return nil, lastErr
}

// ==================== 健康检查 ====================

// HealthStatus 健康状态
type HealthStatus struct {
	NodeID       string `json:"node_id"`
	Status       string `json:"status"`
	TotalOps     int    `json:"total_ops"`
	PendingOps   int    `json:"pending_ops"`
	LastVerifyOk bool   `json:"last_verify_ok"`
	Error        string `json:"error,omitempty"`
}

// CheckNodeHealth 检查节点健康状态
func (c *E2EVerifierClient) CheckNodeHealth(ctx context.Context, nodeID string) (*HealthStatus, error) {
	c.mu.RLock()
	addr, exists := c.config.Nodes[nodeID]
	c.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}

	url := fmt.Sprintf("http://%s/debug/porcupine/health", addr)
	resp, err := c.doRequest(ctx, url)
	if err != nil {
		return &HealthStatus{
			NodeID: nodeID,
			Status: "unreachable",
			Error:  err.Error(),
		}, nil
	}
	defer resp.Body.Close()

	var health HealthStatus
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("decode health response failed: %w", err)
	}

	health.NodeID = nodeID
	return &health, nil
}

// CheckAllNodesHealth 检查所有节点健康状态
func (c *E2EVerifierClient) CheckAllNodesHealth(ctx context.Context) (map[string]*HealthStatus, error) {
	c.mu.RLock()
	nodes := make(map[string]string, len(c.config.Nodes))
	for k, v := range c.config.Nodes {
		nodes[k] = v
	}
	c.mu.RUnlock()

	result := make(map[string]*HealthStatus)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for nodeID := range nodes {
		wg.Add(1)
		go func(nodeID string) {
			defer wg.Done()

			health, _ := c.CheckNodeHealth(ctx, nodeID)

			mu.Lock()
			result[nodeID] = health
			mu.Unlock()
		}(nodeID)
	}

	wg.Wait()
	return result, nil
}
