package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCronJobStatus_String(t *testing.T) {
	tests := []struct {
		name     string
		status   CronJobStatus
		expected string
	}{
		{
			name:     "scheduled status",
			status:   CronJobStatusScheduled,
			expected: "scheduled",
		},
		{
			name:     "running status",
			status:   CronJobStatusRunning,
			expected: "running",
		},
		{
			name:     "paused status",
			status:   CronJobStatusPaused,
			expected: "paused",
		},
		{
			name:     "stopped status",
			status:   CronJobStatusStopped,
			expected: "stopped",
		},
		{
			name:     "unknown status",
			status:   CronJobStatus(999),
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

func TestCronSpec(t *testing.T) {
	tests := []struct {
		name string
		spec CronSpec
	}{
		{
			name: "every minute",
			spec: "* * * * *",
		},
		{
			name: "every hour",
			spec: "0 * * * *",
		},
		{
			name: "daily at midnight",
			spec: "0 0 * * *",
		},
		{
			name: "custom schedule",
			spec: "*/5 * * * *",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CronSpec 是 string 的别名，直接测试值
			assert.NotEmpty(t, string(tt.spec))
		})
	}
}

func TestCronJobInfo(t *testing.T) {
	now := time.Now()
	lastRun := now.Add(-1 * time.Hour)

	info := CronJobInfo{
		ID:        "job-001",
		Name:      "test-job",
		Spec:      "*/5 * * * *",
		Status:    CronJobStatusRunning,
		NextRun:   now.Add(5 * time.Minute),
		LastRun:   &lastRun,
		CreatedAt: now.Add(-24 * time.Hour),
	}

	assert.Equal(t, "job-001", info.ID)
	assert.Equal(t, "test-job", info.Name)
	assert.Equal(t, CronSpec("*/5 * * * *"), info.Spec)
	assert.Equal(t, CronJobStatusRunning, info.Status)
	assert.NotNil(t, info.LastRun)
	assert.Equal(t, lastRun, *info.LastRun)
}

func TestCronJobInfo_NilLastRun(t *testing.T) {
	info := CronJobInfo{
		ID:        "job-002",
		Name:      "new-job",
		Spec:      "0 * * * *",
		Status:    CronJobStatusScheduled,
		NextRun:   time.Now().Add(1 * time.Hour),
		LastRun:   nil,
		CreatedAt: time.Now(),
	}

	assert.Nil(t, info.LastRun)
}

func TestCronJobInfo_StatusTransitions(t *testing.T) {
	info := CronJobInfo{
		ID:     "job-003",
		Name:   "status-test",
		Spec:   "* * * * *",
		Status: CronJobStatusScheduled,
	}

	// Scheduled -> Running
	info.Status = CronJobStatusRunning
	assert.Equal(t, "running", info.Status.String())

	// Running -> Paused
	info.Status = CronJobStatusPaused
	assert.Equal(t, "paused", info.Status.String())

	// Paused -> Running
	info.Status = CronJobStatusRunning
	assert.Equal(t, "running", info.Status.String())

	// Running -> Stopped
	info.Status = CronJobStatusStopped
	assert.Equal(t, "stopped", info.Status.String())
}
