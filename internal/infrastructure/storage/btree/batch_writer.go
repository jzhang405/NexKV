// Package btree BatchWriter — high-level batch write API with PageDispatcher.
package btree

import (
	"context"
	"fmt"
)

// BatchWriter 批量写入调度器（V1：纯非事务批量写入）。
type BatchWriter struct {
	dispatcher *PageDispatcher
}

// NewBatchWriter 创建 BatchWriter。
func NewBatchWriter(tree *BTree) *BatchWriter {
	return &BatchWriter{
		dispatcher: NewPageDispatcher(tree),
	}
}

// WriteBatch 批量写入（非事务）。
// 内部自动按 Page 分组、并发调度。全部成功返回 nil，部分失败返回 *BatchError。
func (bw *BatchWriter) WriteBatch(ctx context.Context, keys, values [][]byte) error {
	if len(keys) != len(values) {
		return fmt.Errorf("btree: keys and values length mismatch: %d != %d", len(keys), len(values))
	}

	results, err := bw.dispatcher.Dispatch(ctx, keys, values)
	if err != nil {
		return fmt.Errorf("btree: dispatch failed: %w", err)
	}

	var errs []WriteResult
	for _, r := range results {
		if r.Err != nil {
			errs = append(errs, r)
		}
	}
	if len(errs) > 0 {
		return &BatchError{Errors: errs}
	}
	return nil
}

// Shutdown 关闭调度器。
func (bw *BatchWriter) Shutdown() { bw.dispatcher.Shutdown() }

// Wait 等待所有已提交任务完成。在 Shutdown 之后调用。
func (bw *BatchWriter) Wait() { bw.dispatcher.Wait() }

// BatchError 聚合多个写入错误。
type BatchError struct {
	Errors []WriteResult
}

func (be *BatchError) Error() string {
	return fmt.Sprintf("%d write(s) failed", len(be.Errors))
}
