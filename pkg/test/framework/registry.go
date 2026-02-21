package framework

import (
	"container/list"
	"context"
	"io"
	"sync"

	"github.com/jzhang405/NexKV/pkg/errors"
)

// ComponentFactory 组件工厂函数类型
type ComponentFactory func() (TestComponent, error)

// ComponentRegistry 组件注册表
// 管理组件工厂、依赖解析和拓扑排序
type ComponentRegistry struct {
	mu        sync.RWMutex
	factories map[ComponentType]ComponentFactory
}

// NewComponentRegistry 创建新的组件注册表
func NewComponentRegistry() *ComponentRegistry {
	return &ComponentRegistry{
		factories: make(map[ComponentType]ComponentFactory),
	}
}

// Register 注册组件工厂
func (r *ComponentRegistry) Register(componentType ComponentType, factory ComponentFactory) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[componentType]; exists {
		return errors.Wrapf(errors.ErrComponentExists, "component type %s already registered", componentType)
	}

	r.factories[componentType] = factory
	return nil
}

// GetFactory 获取组件工厂
func (r *ComponentRegistry) GetFactory(componentType ComponentType) (ComponentFactory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	factory, exists := r.factories[componentType]
	if !exists {
		return nil, errors.Wrapf(errors.ErrComponentNotFound, "component type %s not registered", componentType)
	}

	return factory, nil
}

// ListRegistered 列出所有已注册的组件类型
func (r *ComponentRegistry) ListRegistered() []ComponentType {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]ComponentType, 0, len(r.factories))
	for t := range r.factories {
		types = append(types, t)
	}
	return types
}

// CreateComponent 创建组件实例
func (r *ComponentRegistry) CreateComponent(componentType ComponentType) (TestComponent, error) {
	factory, err := r.GetFactory(componentType)
	if err != nil {
		return nil, err
	}

	return factory()
}

// ResolveDependencies 解析组件依赖并返回拓扑排序后的初始化顺序
// 使用 Kahn 算法：graph[A] = [B, C] 表示 B、C 依赖 A（A 必须先于 B、C 初始化）
func (r *ComponentRegistry) ResolveDependencies(componentTypes []ComponentType) ([]ComponentType, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 构建组件类型集合，用于快速查找
	typeSet := make(map[ComponentType]bool)
	for _, ct := range componentTypes {
		typeSet[ct] = true
	}

	// 初始化依赖图和入度表
	graph := make(map[ComponentType][]ComponentType)
	inDegree := make(map[ComponentType]int)
	for _, ct := range componentTypes {
		graph[ct] = []ComponentType{}
		inDegree[ct] = 0
	}

	// 构建依赖边（创建临时组件实例获取依赖关系）
	for _, ct := range componentTypes {
		factory, exists := r.factories[ct]
		if !exists {
			return nil, errors.Wrapf(errors.ErrComponentNotFound, "component type %s not registered", ct)
		}

		comp, err := factory()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create component %s", ct)
		}

		// 清理临时组件实例
		if closer, ok := comp.(io.Closer); ok {
			defer closer.Close()
		}

		for _, dep := range comp.GetDependencies() {
			if !typeSet[dep] {
				return nil, errors.Wrapf(errors.ErrDependencyNotMet, "component %s depends on %s which is not in the input set", ct, dep)
			}
			graph[dep] = append(graph[dep], ct)
			inDegree[ct]++
		}
	}

	// Kahn 算法：BFS 拓扑排序
	queue := list.New()
	result := make([]ComponentType, 0, len(componentTypes))

	// 将入度为 0 的节点加入队列
	for _, ct := range componentTypes {
		if inDegree[ct] == 0 {
			queue.PushBack(ct)
		}
	}

	for queue.Len() > 0 {
		elem := queue.Front()
		queue.Remove(elem)
		node := elem.Value.(ComponentType)
		result = append(result, node)

		for _, neighbor := range graph[node] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue.PushBack(neighbor)
			}
		}
	}

	// 检测循环依赖
	if len(result) != len(componentTypes) {
		return nil, errors.Wrap(errors.ErrCircularDependency, "circular dependency detected in component graph")
	}

	return result, nil
}

// CreateAll 按依赖顺序创建所有指定类型的组件实例
// 失败时自动清理已创建的组件
func (r *ComponentRegistry) CreateAll(ctx context.Context, componentTypes []ComponentType) (map[ComponentType]TestComponent, error) {
	sortedTypes, err := r.ResolveDependencies(componentTypes)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve dependencies")
	}

	components := make(map[ComponentType]TestComponent)
	success := false
	defer func() {
		if !success {
			cleanupComponents(components)
		}
	}()

	for _, ct := range sortedTypes {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		comp, err := r.CreateComponent(ct)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create component %s", ct)
		}
		components[ct] = comp
	}

	success = true
	return components, nil
}

// cleanupComponents 清理组件实例（内部辅助函数）
func cleanupComponents(components map[ComponentType]TestComponent) {
	for _, comp := range components {
		if closer, ok := comp.(io.Closer); ok {
			_ = closer.Close()
		}
	}
}

// Registry 全局注册表（已废弃，请使用 TestContext.Registry 实现完全隔离）
//
// 废弃原因：
// 1. 全局状态导致测试间耦合
// 2. 并行测试时难以调试
// 3. 无法为不同测试配置不同组件工厂
var (
	globalRegistry     *ComponentRegistry
	globalRegistryOnce sync.Once
)

// GetGlobalRegistry 获取全局组件注册表（已废弃）
// Deprecated: Use NewComponentRegistry() for test isolation.
func GetGlobalRegistry() *ComponentRegistry {
	globalRegistryOnce.Do(func() {
		globalRegistry = NewComponentRegistry()
	})
	return globalRegistry
}
