// Package concurrency 提供并发安全的数据结构包装器
package concurrency

import "sync"

// Locked[T] 泛型锁包装器
// 提供线程安全的访问控制，自动管理读写锁
type Locked[T any] struct {
	mu   sync.RWMutex
	core T
}

// NewLocked 创建锁包装器
func NewLocked[T any](initial T) *Locked[T] {
	return &Locked[T]{core: initial}
}

// View 读视图（自动加读锁）
// 用法：
//
//	locked := NewLocked(MyData{})
//	err := locked.View(func(data MyData) error {
//	    // 只读访问 data
//	    return nil
//	})
func (l *Locked[T]) View(fn func(core T) error) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return fn(l.core)
}

// Modify 写视图（自动加写锁）
// 用法:
//
//	locked := NewLocked(MyData{})
//	err := locked.Modify(func(data *MyData) error {
//	    // 修改 data
//	    return nil
//	})
func (l *Locked[T]) Modify(fn func(core *T) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return fn(&l.core)
}

// GetDirect 直接访问（无锁）
// 警告：仅在确保没有并发访问时使用，否则可能导致数据竞争
func (l *Locked[T]) GetDirect() T {
	return l.core
}

// Get 获取值（加读锁）
func (l *Locked[T]) Get() T {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.core
}

// Set 设置值（加写锁）
func (l *Locked[T]) Set(val T) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.core = val
}
