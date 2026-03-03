// Package service 测试领域服务
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ==========================================
// OperationStatus 测试
// ==========================================

// TestOperationStatus_IsTerminal 测试终态判断
func TestOperationStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		name     string
		status   OperationStatus
		expected bool
	}{
		{"Pending is not terminal", StatusPending, false},
		{"Running is not terminal", StatusRunning, false},
		{"Completed is terminal", StatusCompleted, true},
		{"Failed is terminal", StatusFailed, true},
		{"Canceled is terminal", StatusCanceled, true},
		{"Discarded is terminal", StatusDiscarded, true},
		{"Timeout is terminal", StatusTimeout, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.status.IsTerminal()
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestOperationStatus_String 测试状态字符串表示
func TestOperationStatus_String(t *testing.T) {
	tests := []struct {
		name     string
		status   OperationStatus
		expected string
	}{
		{"Pending", StatusPending, "pending"},
		{"Running", StatusRunning, "running"},
		{"Completed", StatusCompleted, "completed"},
		{"Failed", StatusFailed, "failed"},
		{"Canceled", StatusCanceled, "canceled"},
		{"Discarded", StatusDiscarded, "discarded"},
		{"Timeout", StatusTimeout, "timeout"},
		{"Unknown status", OperationStatus(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.status.String()
			assert.Equal(t, tt.expected, got)
		})
	}
}
