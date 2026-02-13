// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件实现记录客户端，包装 KV 操作并记录事件
package porcupine

import (
	"context"
	"errors"
)

// KVOperator KV 操作接口
// 定义线性化验证需要的最小操作集
type KVOperator interface {
	// Put 写入操作
	Put(ctx context.Context, namespace, key string, value []byte) error
	// Get 读取操作
	Get(ctx context.Context, namespace, key string) ([]byte, error)
	// Delete 删除操作
	Delete(ctx context.Context, namespace, key string) error
}

// RecordingClient 记录客户端
// 包装 KVOperator，记录所有操作到 HistoryRecorder
type RecordingClient struct {
	kv       KVOperator       // 原始 KV 客户端
	recorder *HistoryRecorder // 历史记录器
}

// NewRecordingClient 创建记录客户端
// kv: KV 操作接口
// recorder: 历史记录器
func NewRecordingClient(kv KVOperator, recorder *HistoryRecorder) *RecordingClient {
	return &RecordingClient{
		kv:       kv,
		recorder: recorder,
	}
}

// GetRecorder 获取历史记录器
func (c *RecordingClient) GetRecorder() *HistoryRecorder {
	return c.recorder
}

// Put 写入操作
// 记录操作并调用底层 KV
func (c *RecordingClient) Put(ctx context.Context, namespace, key string, value []byte) error {
	// 记录 Call
	opID := c.recorder.RecordCall(OpPut, namespace, key, value)

	// 执行操作
	err := c.kv.Put(ctx, namespace, key, value)

	// 记录 Return
	if err != nil {
		c.recorder.RecordReturn(opID, false, nil, err.Error())
	} else {
		c.recorder.RecordReturn(opID, true, nil, "")
	}

	return err
}

// Get 读取操作
// 记录操作并调用底层 KV
func (c *RecordingClient) Get(ctx context.Context, namespace, key string) ([]byte, error) {
	// 记录 Call
	opID := c.recorder.RecordCall(OpGet, namespace, key, nil)

	// 执行操作
	value, err := c.kv.Get(ctx, namespace, key)

	// 记录 Return
	if err != nil {
		errStr := err.Error()
		if errors.Is(err, errors.New(ErrKeyNotFound)) || errStr == ErrKeyNotFound {
			c.recorder.RecordReturn(opID, false, nil, ErrKeyNotFound)
		} else {
			c.recorder.RecordReturn(opID, false, nil, errStr)
		}
	} else {
		c.recorder.RecordReturn(opID, true, value, "")
	}

	return value, err
}

// Delete 删除操作
// 记录操作并调用底层 KV
func (c *RecordingClient) Delete(ctx context.Context, namespace, key string) error {
	// 记录 Call
	opID := c.recorder.RecordCall(OpDelete, namespace, key, nil)

	// 执行操作
	err := c.kv.Delete(ctx, namespace, key)

	// 记录 Return
	if err != nil {
		c.recorder.RecordReturn(opID, false, nil, err.Error())
	} else {
		c.recorder.RecordReturn(opID, true, nil, "")
	}

	return err
}

// QuorumPut Quorum 写入操作
// 记录操作并调用底层 Put（假设底层支持 Quorum 语义）
func (c *RecordingClient) QuorumPut(ctx context.Context, namespace, key string, value []byte) error {
	// 记录 Call
	opID := c.recorder.RecordCall(OpQuorumPut, namespace, key, value)

	// 执行操作（使用底层 Put）
	err := c.kv.Put(ctx, namespace, key, value)

	// 记录 Return
	if err != nil {
		c.recorder.RecordReturn(opID, false, nil, err.Error())
	} else {
		c.recorder.RecordReturn(opID, true, nil, "")
	}

	return err
}

// QuorumGet Quorum 读取操作
// 记录操作并调用底层 Get（假设底层支持 Quorum 语义）
func (c *RecordingClient) QuorumGet(ctx context.Context, namespace, key string) ([]byte, error) {
	// 记录 Call
	opID := c.recorder.RecordCall(OpQuorumGet, namespace, key, nil)

	// 执行操作（使用底层 Get）
	value, err := c.kv.Get(ctx, namespace, key)

	// 记录 Return
	if err != nil {
		errStr := err.Error()
		if errors.Is(err, errors.New(ErrKeyNotFound)) || errStr == ErrKeyNotFound {
			c.recorder.RecordReturn(opID, false, nil, ErrKeyNotFound)
		} else {
			c.recorder.RecordReturn(opID, false, nil, errStr)
		}
	} else {
		c.recorder.RecordReturn(opID, true, value, "")
	}

	return value, err
}
