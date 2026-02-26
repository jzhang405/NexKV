package framework

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// mockTestComponent 实现 TestComponent 接口
// ==========================================

type mockTestComponent struct {
	*BaseComponent
	initCalled  bool
	startCalled bool
	stopCalled  bool
}

func newMockTestComponent(name string, compType ComponentType, deps []ComponentType) *mockTestComponent {
	return &mockTestComponent{
		BaseComponent: NewBaseComponent(name, compType, deps),
	}
}

func (c *mockTestComponent) Init(ctx context.Context, env TestEnvironment) error {
	c.initCalled = true
	return c.BaseComponent.Init(ctx, env)
}

func (c *mockTestComponent) Start(ctx context.Context) error {
	c.startCalled = true
	return c.BaseComponent.Start(ctx)
}

func (c *mockTestComponent) Stop(ctx context.Context) error {
	c.stopCalled = true
	return c.BaseComponent.Stop(ctx)
}

// ==========================================
// ComponentRegistry 测试
// ==========================================

func TestNewComponentRegistry(t *testing.T) {
	registry := NewComponentRegistry()
	assert.NotNil(t, registry)
	assert.NotNil(t, registry.factories)
}

func TestComponentRegistry_Register(t *testing.T) {
	registry := NewComponentRegistry()

	factory := func() (TestComponent, error) {
		return newMockTestComponent("test", ComponentTypeTransport, nil), nil
	}

	err := registry.Register(ComponentTypeTransport, factory)
	assert.NoError(t, err)
}

func TestComponentRegistry_Register_Duplicate(t *testing.T) {
	registry := NewComponentRegistry()

	factory := func() (TestComponent, error) {
		return newMockTestComponent("test", ComponentTypeTransport, nil), nil
	}

	err := registry.Register(ComponentTypeTransport, factory)
	require.NoError(t, err)

	// 重复注册应该返回错误
	err = registry.Register(ComponentTypeTransport, factory)
	assert.Error(t, err)
}

func TestComponentRegistry_GetFactory(t *testing.T) {
	registry := NewComponentRegistry()

	factory := func() (TestComponent, error) {
		return newMockTestComponent("test", ComponentTypeTransport, nil), nil
	}

	_ = registry.Register(ComponentTypeTransport, factory)

	gotFactory, err := registry.GetFactory(ComponentTypeTransport)
	assert.NoError(t, err)
	assert.NotNil(t, gotFactory)
}

func TestComponentRegistry_GetFactory_NotFound(t *testing.T) {
	registry := NewComponentRegistry()

	_, err := registry.GetFactory(ComponentTypeTransport)
	assert.Error(t, err)
}

func TestComponentRegistry_ListRegistered(t *testing.T) {
	registry := NewComponentRegistry()

	factory1 := func() (TestComponent, error) {
		return newMockTestComponent("transport", ComponentTypeTransport, nil), nil
	}
	factory2 := func() (TestComponent, error) {
		return newMockTestComponent("storage", ComponentTypeStorage, nil), nil
	}

	_ = registry.Register(ComponentTypeTransport, factory1)
	_ = registry.Register(ComponentTypeStorage, factory2)

	types := registry.ListRegistered()
	assert.Len(t, types, 2)
	assert.Contains(t, types, ComponentTypeTransport)
	assert.Contains(t, types, ComponentTypeStorage)
}

func TestComponentRegistry_CreateComponent(t *testing.T) {
	registry := NewComponentRegistry()

	factory := func() (TestComponent, error) {
		return newMockTestComponent("test-transport", ComponentTypeTransport, nil), nil
	}

	_ = registry.Register(ComponentTypeTransport, factory)

	comp, err := registry.CreateComponent(ComponentTypeTransport)
	require.NoError(t, err)
	assert.Equal(t, "test-transport", comp.Name())
	assert.Equal(t, ComponentTypeTransport, comp.Type())
}

func TestComponentRegistry_CreateComponent_NotFound(t *testing.T) {
	registry := NewComponentRegistry()

	_, err := registry.CreateComponent(ComponentTypeTransport)
	assert.Error(t, err)
}

// ==========================================
// ResolveDependencies 测试
// ==========================================

func TestComponentRegistry_ResolveDependencies_NoDeps(t *testing.T) {
	registry := NewComponentRegistry()

	// 注册无依赖组件
	factory := func() (TestComponent, error) {
		return newMockTestComponent("transport", ComponentTypeTransport, nil), nil
	}
	_ = registry.Register(ComponentTypeTransport, factory)

	sorted, err := registry.ResolveDependencies([]ComponentType{ComponentTypeTransport})
	require.NoError(t, err)
	assert.Len(t, sorted, 1)
	assert.Equal(t, ComponentTypeTransport, sorted[0])
}

func TestComponentRegistry_ResolveDependencies_WithDeps(t *testing.T) {
	registry := NewComponentRegistry()

	// 注册有依赖关系的组件
	// Storage 依赖 Transport
	transportFactory := func() (TestComponent, error) {
		return newMockTestComponent("transport", ComponentTypeTransport, nil), nil
	}
	storageFactory := func() (TestComponent, error) {
		return newMockTestComponent("storage", ComponentTypeStorage, []ComponentType{ComponentTypeTransport}), nil
	}

	_ = registry.Register(ComponentTypeTransport, transportFactory)
	_ = registry.Register(ComponentTypeStorage, storageFactory)

	sorted, err := registry.ResolveDependencies([]ComponentType{ComponentTypeStorage, ComponentTypeTransport})
	require.NoError(t, err)
	assert.Len(t, sorted, 2)

	// Transport 应该在 Storage 之前
	assert.Equal(t, ComponentTypeTransport, sorted[0])
	assert.Equal(t, ComponentTypeStorage, sorted[1])
}

func TestComponentRegistry_ResolveDependencies_MissingDep(t *testing.T) {
	registry := NewComponentRegistry()

	// Storage 依赖 Transport，但 Transport 未注册
	storageFactory := func() (TestComponent, error) {
		return newMockTestComponent("storage", ComponentTypeStorage, []ComponentType{ComponentTypeTransport}), nil
	}
	_ = registry.Register(ComponentTypeStorage, storageFactory)

	_, err := registry.ResolveDependencies([]ComponentType{ComponentTypeStorage})
	assert.Error(t, err)
}

func TestComponentRegistry_ResolveDependencies_Circular(t *testing.T) {
	registry := NewComponentRegistry()

	// 创建循环依赖：A -> B -> A
	factoryA := func() (TestComponent, error) {
		return newMockTestComponent("a", ComponentTypeTransport, []ComponentType{ComponentTypeStorage}), nil
	}
	factoryB := func() (TestComponent, error) {
		return newMockTestComponent("b", ComponentTypeStorage, []ComponentType{ComponentTypeTransport}), nil
	}

	_ = registry.Register(ComponentTypeTransport, factoryA)
	_ = registry.Register(ComponentTypeStorage, factoryB)

	_, err := registry.ResolveDependencies([]ComponentType{ComponentTypeTransport, ComponentTypeStorage})
	assert.Error(t, err)
}

// ==========================================
// CreateAll 测试
// ==========================================

func TestComponentRegistry_CreateAll(t *testing.T) {
	registry := NewComponentRegistry()

	transportFactory := func() (TestComponent, error) {
		return newMockTestComponent("transport", ComponentTypeTransport, nil), nil
	}
	storageFactory := func() (TestComponent, error) {
		return newMockTestComponent("storage", ComponentTypeStorage, []ComponentType{ComponentTypeTransport}), nil
	}

	_ = registry.Register(ComponentTypeTransport, transportFactory)
	_ = registry.Register(ComponentTypeStorage, storageFactory)

	components, err := registry.CreateAll(context.Background(), []ComponentType{ComponentTypeStorage, ComponentTypeTransport})
	require.NoError(t, err)
	assert.Len(t, components, 2)

	// 验证组件创建
	_, ok := components[ComponentTypeTransport]
	assert.True(t, ok)
	_, ok = components[ComponentTypeStorage]
	assert.True(t, ok)
}

func TestComponentRegistry_CreateAll_ContextCancel(t *testing.T) {
	registry := NewComponentRegistry()

	transportFactory := func() (TestComponent, error) {
		return newMockTestComponent("transport", ComponentTypeTransport, nil), nil
	}
	_ = registry.Register(ComponentTypeTransport, transportFactory)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	_, err := registry.CreateAll(ctx, []ComponentType{ComponentTypeTransport})
	assert.Error(t, err)
}

func TestComponentRegistry_CreateAll_FailureCleansUp(t *testing.T) {
	registry := NewComponentRegistry()

	// 第一个组件成功
	transportFactory := func() (TestComponent, error) {
		return newMockTestComponent("transport", ComponentTypeTransport, nil), nil
	}
	// 第二个组件失败
	storageFactory := func() (TestComponent, error) {
		return nil, assert.AnError
	}

	_ = registry.Register(ComponentTypeTransport, transportFactory)
	_ = registry.Register(ComponentTypeStorage, storageFactory)

	_, err := registry.CreateAll(context.Background(), []ComponentType{ComponentTypeTransport, ComponentTypeStorage})
	assert.Error(t, err)
}

// ==========================================
// GetGlobalRegistry 测试
// ==========================================

func TestGetGlobalRegistry(t *testing.T) {
	registry1 := GetGlobalRegistry()
	registry2 := GetGlobalRegistry()

	// 应该返回同一个实例
	assert.Equal(t, registry1, registry2)
}

// ==========================================
// 并发测试
// ==========================================

func TestComponentRegistry_ConcurrentRegister(t *testing.T) {
	registry := NewComponentRegistry()

	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func(idx int) {
			// 每个协程注册不同的类型
			ctype := ComponentType("component-" + string(rune('A'+idx)))
			factory := func() (TestComponent, error) {
				return newMockTestComponent("test", ctype, nil), nil
			}
			_ = registry.Register(ctype, factory)
			done <- true
		}(i)
	}

	// 等待所有协程完成
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestComponentRegistry_ConcurrentRead(t *testing.T) {
	registry := NewComponentRegistry()

	factory := func() (TestComponent, error) {
		return newMockTestComponent("test", ComponentTypeTransport, nil), nil
	}
	_ = registry.Register(ComponentTypeTransport, factory)

	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func() {
			_, _ = registry.GetFactory(ComponentTypeTransport)
			_ = registry.ListRegistered()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
