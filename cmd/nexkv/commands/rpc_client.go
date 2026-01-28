// Package commands RPC 客户端封装
//
// 提供 CLI 到 Daemon 的 RPC 调用封装
package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/jzhang405/NexKV/internal/rpc/client"
)

// RPCClient RPC 客户端封装
type RPCClient struct {
	client *client.RPCClient
}

// NewRPCClient 创建 RPC 客户端
func NewRPCClient(addr string) (*RPCClient, error) {
	rpcClient, err := client.NewRPCClient(&client.Config{
		ServerAddr: addr,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 RPC 客户端失败: %w", err)
	}

	return &RPCClient{
		client: rpcClient,
	}, nil
}

// Close 关闭客户端
func (c *RPCClient) Close() error {
	return c.client.Close()
}

// ========================================
// 节点管理
// ========================================

// AddNode 添加节点到集群
func (c *RPCClient) AddNode(ctx context.Context, nodeID, addr string) error {
	// 简化架构：直接使用 JSON 格式发送请求
	// 注意：正确的架构应该是 Transport 层负责解码，Handler 接收 types.Message
	// 当前为临时方案，避免在 Handler 中做 MessagePack 解码
	request := map[string]interface{}{
		"node_id": nodeID,
		"addr":    addr,
	}

	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("编码请求失败: %w", err)
	}

	// 封装成 Frame（使用 JSON Codec）
	frame := transport.NewFrame(
		0, // nodeID（占位）
		1, // msgSeq
		types.MessageTypeNodeJoin,
		uint16(types.CodecTypeJSON), // 使用 JSON 编码
		transport.FlagsIsRequest,
		data,
	).Finalize()

	// 序列化 Frame
	frameData, err := frame.Marshal()
	if err != nil {
		return fmt.Errorf("序列化 Frame 失败: %w", err)
	}

	// 发送 RPC 请求
	response, err := c.client.Call(ctx, frameData)
	if err != nil {
		return fmt.Errorf("RPC 调用失败: %w", err)
	}

	// 解析响应
	var result map[string]interface{}
	if err := json.Unmarshal(response, &result); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	// 检查响应
	if success, ok := result["success"].(bool); !ok || !success {
		return fmt.Errorf("添加节点失败: %v", result["message"])
	}

	return nil
}

// RemoveNode 从集群中删除节点
func (c *RPCClient) RemoveNode(ctx context.Context, nodeID string) error {
	// 简化架构：直接使用 JSON 格式发送请求
	request := map[string]interface{}{
		"node_id": nodeID,
	}

	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("编码请求失败: %w", err)
	}

	// 封装成 Frame（使用 JSON Codec）
	frame := transport.NewFrame(
		0, // nodeID（占位）
		1, // msgSeq
		types.MessageTypeNodeLeave,
		uint16(types.CodecTypeJSON), // 使用 JSON 编码
		transport.FlagsIsRequest,
		data,
	).Finalize()

	// 序列化 Frame
	frameData, err := frame.Marshal()
	if err != nil {
		return fmt.Errorf("序列化 Frame 失败: %w", err)
	}

	// 发送 RPC 请求
	response, err := c.client.Call(ctx, frameData)
	if err != nil {
		return fmt.Errorf("RPC 调用失败: %w", err)
	}

	// 解析响应
	var result map[string]interface{}
	if err := json.Unmarshal(response, &result); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	// 检查响应
	if success, ok := result["success"].(bool); !ok || !success {
		return fmt.Errorf("删除节点失败: %v", result["message"])
	}

	return nil
}

// ListNode 列出集群中的所有节点
func (c *RPCClient) ListNode(ctx context.Context, nodeID string) (*ClusterStatusResponse, error) {
	// 简化架构：直接使用 JSON 格式发送请求
	request := map[string]interface{}{
		"node_id": nodeID,
	}

	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("编码请求失败: %w", err)
	}

	// 封装成 Frame（使用 JSON Codec）
	frame := transport.NewFrame(
		0, // nodeID（占位）
		1, // msgSeq
		types.MessageTypeClusterStatus,
		uint16(types.CodecTypeJSON), // 使用 JSON 编码
		transport.FlagsIsRequest,
		data,
	).Finalize()

	// 序列化 Frame
	frameData, err := frame.Marshal()
	if err != nil {
		return nil, fmt.Errorf("序列化 Frame 失败: %w", err)
	}

	// 发送 RPC 请求
	response, err := c.client.Call(ctx, frameData)
	if err != nil {
		return nil, fmt.Errorf("RPC 调用失败: %w", err)
	}

	// 解析响应
	var result ClusterStatusResponse
	if err := json.Unmarshal(response, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("查询集群状态失败")
	}

	return &result, nil
}

// ========================================
// 集群管理
// ========================================

// ClusterStatusResponse 集群状态响应
type ClusterStatusResponse struct {
	Success bool                 `json:"success"`
	Nodes   []transport.NodeInfo `json:"nodes"`
	Count   int                  `json:"count"`
}

// GetClusterStatus 获取集群状态
func (c *RPCClient) GetClusterStatus(ctx context.Context, nodeID string) (*ClusterStatusResponse, error) {
	return c.ListNode(ctx, nodeID)
}

// PingNode Ping 节点
func (c *RPCClient) PingNode(ctx context.Context, nodeID string, sequence uint64) error {
	// 简化架构：直接使用 JSON 格式发送请求
	request := map[string]interface{}{
		"node_id": nodeID,
	}

	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("编码请求失败: %w", err)
	}

	// 封装成 Frame（使用 JSON Codec）
	frame := transport.NewFrame(
		0, // nodeID（占位）
		1, // msgSeq
		types.MessageTypeNodePing,
		uint16(types.CodecTypeJSON), // 使用 JSON 编码
		transport.FlagsIsRequest,
		data,
	).Finalize()

	// 序列化 Frame
	frameData, err := frame.Marshal()
	if err != nil {
		return fmt.Errorf("序列化 Frame 失败: %w", err)
	}

	// 发送 RPC 请求
	response, err := c.client.Call(ctx, frameData)
	if err != nil {
		return fmt.Errorf("RPC 调用失败: %w", err)
	}

	// 解析响应
	var result map[string]interface{}
	if err := json.Unmarshal(response, &result); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	// 检查响应状态
	if status, ok := result["status"].(string); !ok || status != "ready" {
		return fmt.Errorf("节点不可用: %v", result)
	}

	return nil
}

// FormatNodeList 格式化节点列表输出
func FormatNodeList(response *ClusterStatusResponse, verbose bool, outputFormat string) error {
	switch outputFormat {
	case "json":
		data, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	default: // table 格式
		if len(response.Nodes) == 0 {
			fmt.Println("集群中没有节点")
			return nil
		}

		// 表头
		fmt.Printf("%-20s %-25s %-15s %-10s\n", "Node ID", "Address", "Parent", "Status")
		fmt.Println("──────────────────── ──────────────────────── ────────────── ──────────")

		// 节点信息
		for _, node := range response.Nodes {
			parentID := node.ParentID
			if parentID == "" {
				parentID = "-"
			}
			fmt.Printf("%-20s %-25s %-15s %-10s\n",
				node.NodeID,
				node.Addr,
				parentID,
				node.Status,
			)

			// 详细信息
			if verbose {
				fmt.Printf("  Level: %d\n", node.Level)
			}
		}

		// 汇总信息
		fmt.Printf("\n总计: %d 个节点\n", response.Count)
	}

	return nil
}
