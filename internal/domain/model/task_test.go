package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTaskPriority_String(t *testing.T) {
	tests := []struct {
		name     string
		priority TaskPriority
		expected string
	}{
		{
			name:     "critical priority",
			priority: TaskPriorityCritical,
			expected: "critical",
		},
		{
			name:     "high priority",
			priority: TaskPriorityHigh,
			expected: "high",
		},
		{
			name:     "normal priority",
			priority: TaskPriorityNormal,
			expected: "normal",
		},
		{
			name:     "low priority",
			priority: TaskPriorityLow,
			expected: "low",
		},
		{
			name:     "unknown priority",
			priority: TaskPriority(999),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.priority.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTaskHealthStatus_String(t *testing.T) {
	tests := []struct {
		name     string
		status   TaskHealthStatus
		expected string
	}{
		{
			name:     "healthy status",
			status:   TaskHealthStatusHealthy,
			expected: "healthy",
		},
		{
			name:     "unhealthy status",
			status:   TaskHealthStatusUnhealthy,
			expected: "unhealthy",
		},
		{
			name:     "unknown status",
			status:   TaskHealthStatus(999),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.status.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTaskPoolStats(t *testing.T) {
	stats := TaskPoolStats{
		Total: 100,
		ByPriority: map[TaskPriority]int{
			TaskPriorityCritical: 10,
			TaskPriorityHigh:     20,
			TaskPriorityNormal:   50,
			TaskPriorityLow:      20,
		},
		Running:  30,
		Waiting:  70,
		Capacity: 200,
	}

	assert.Equal(t, 100, stats.Total)
	assert.Equal(t, 30, stats.Running)
	assert.Equal(t, 70, stats.Waiting)
	assert.Equal(t, 200, stats.Capacity)
	assert.Equal(t, 10, stats.ByPriority[TaskPriorityCritical])
	assert.Equal(t, 20, stats.ByPriority[TaskPriorityHigh])
	assert.Equal(t, 50, stats.ByPriority[TaskPriorityNormal])
	assert.Equal(t, 20, stats.ByPriority[TaskPriorityLow])
}
