package framework

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ==========================================
// ComponentType 常量测试
// ==========================================

func TestComponentType_Constants(t *testing.T) {
	tests := []struct {
		name     string
		ctype    ComponentType
		expected string
	}{
		{"transport", ComponentTypeTransport, "transport"},
		{"storage", ComponentTypeStorage, "storage"},
		{"replication", ComponentTypeReplication, "replication"},
		{"cluster", ComponentTypeCluster, "cluster"},
		{"transaction", ComponentTypeTransaction, "transaction"},
		{"metadata", ComponentTypeMetadata, "metadata"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, ComponentType(tt.expected), tt.ctype)
		})
	}
}

// ==========================================
// EnvironmentStatus 测试
// ==========================================

func TestEnvironmentStatus_String(t *testing.T) {
	tests := []struct {
		status   EnvironmentStatus
		expected string
	}{
		{EnvironmentStatusUnknown, "unknown"},
		{EnvironmentStatusCreating, "creating"},
		{EnvironmentStatusCreated, "created"},
		{EnvironmentStatusStarting, "starting"},
		{EnvironmentStatusRunning, "running"},
		{EnvironmentStatusStopping, "stopping"},
		{EnvironmentStatusStopped, "stopped"},
		{EnvironmentStatusError, "error"},
		{EnvironmentStatus(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}

// ==========================================
// HealthCheckConfig 测试
// ==========================================

func TestDefaultHealthCheckConfig(t *testing.T) {
	config := DefaultHealthCheckConfig()

	assert.Equal(t, 5*time.Second, config.Timeout)
	assert.Equal(t, 10*time.Second, config.Interval)
	assert.Equal(t, 3, config.RetryCount)
	assert.Equal(t, 1*time.Second, config.RetryInterval)
	assert.True(t, config.Critical)
}

// ==========================================
// BaseComponent 测试
// ==========================================

func TestNewBaseComponent(t *testing.T) {
	deps := []ComponentType{ComponentTypeTransport}
	comp := NewBaseComponent("test-component", ComponentTypeStorage, deps)

	assert.Equal(t, "test-component", comp.Name())
	assert.Equal(t, ComponentTypeStorage, comp.Type())
	assert.Equal(t, deps, comp.GetDependencies())
	assert.NotNil(t, comp.GetHealthCheckConfig())
}

func TestBaseComponent_DefaultMethods(t *testing.T) {
	comp := NewBaseComponent("test", ComponentTypeTransport, nil)
	ctx := context.TODO()

	// 默认方法不应返回错误
	assert.NoError(t, comp.Init(ctx, nil))
	assert.NoError(t, comp.Start(ctx))
	assert.NoError(t, comp.Stop(ctx))
	assert.NoError(t, comp.HealthCheck(ctx))
}

func TestBaseComponent_SetHealthCheckConfig(t *testing.T) {
	comp := NewBaseComponent("test", ComponentTypeTransport, nil)

	newConfig := &HealthCheckConfig{
		Timeout:       10e9,
		Interval:      20e9,
		RetryCount:    5,
		RetryInterval: 2e9,
		Critical:      false,
	}

	comp.SetHealthCheckConfig(newConfig)
	assert.Equal(t, newConfig, comp.GetHealthCheckConfig())
}
