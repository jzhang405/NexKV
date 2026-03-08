// Package btree provides BTree storage engine implementation.
//
// ⚠️  MINI CCOW PROTOTYPE (Phase 0.5: 技术验证)
// 这是技术验证阶段的原型实现，用于验证核心 CCOW 机制可行性。
// 正式实现将在验证通过后开始。
//
// 验证目标:
// 1. 无锁读操作正确性
// 2. 路径复制算法正确性
// 3. 原子切换根指针正确性
// 4. 基本性能测试 (1000 ops/s)
//
// 文档: docs/07_spike/btree-porting/2026-03-09-day1-2-lealone-source-analysis.md
package btree

import (
	maps "maps"
	"sync"
	"sync/atomic"
	"time"
)

// ==========================================
// Mini CCOW 原型 - 最小可验证实现
// ==========================================

// RootNode 根节点（原型的简化版本）
type RootNode struct {
	Version int
	Data    map[string]string
}

// PageInfo 页面信息（对应 Java 的 PageInfo）
type PageInfo struct {
	pos        int64
	page       *RootNode
	lastTime   int64
	hits       int64
	isDirty    bool
	isSplitted bool
}

// copy 创建 PageInfo 副本（Copy-on-Write 关键）
func (p *PageInfo) copy() *PageInfo {
	return &PageInfo{
		pos:        p.pos,
		page:       p.page,
		lastTime:   p.lastTime,
		hits:       p.hits,
		isDirty:    p.isDirty,
		isSplitted: p.isSplitted,
	}
}

// MiniCCOW 最小可验证原型
// 基于 Lealone BTree 的 CCOW 机制简化实现
type MiniCCOW struct {
	root atomic.Value // *RootNode
	mu   sync.RWMutex  // 仅用于写操作保护（原型简化）
}

// NewMiniCCOW 创建新的 Mini CCOW
func NewMiniCCOW() *MiniCCOW {
	root := &RootNode{
		Version: 0,
		Data:    make(map[string]string),
	}
	ccow := &MiniCCOW{}
	ccow.root.Store(root)
	return ccow
}

// Read 无锁读操作（核心验证点 1）
func (m *MiniCCOW) Read(key string) (string, bool) {
	// ⭐ 关键：直接读取根节点，无需加锁
	root := m.root.Load().(*RootNode)
	if root == nil {
		return "", false
	}
	val, ok := root.Data[key]
	return val, ok
}

// Write 写入操作（带路径复制）
func (m *MiniCCOW) Write(key, value string) error {
	// ⭐ 原型简化：使用 mutex 保护写操作
	// 正式实现将使用 PerCoreExecutor 单写线程模式
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. 获取当前根
	oldRoot := m.root.Load().(*RootNode)

	// 2. Copy-on-Write：创建新根
	newRoot := &RootNode{
		Version: oldRoot.Version + 1,
		Data:    make(map[string]string),
	}

	// 3. 复制所有数据 + 写入新值
	maps.Copy(newRoot.Data, oldRoot.Data)
	newRoot.Data[key] = value

	// 4. ⭐ 原子切换根指针（核心验证点 3）
	m.root.Store(newRoot)

	return nil
}

// CopyOnWritePath 路径复制算法（核心验证点 2）
func (m *MiniCCOW) CopyOnWritePath(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. 获取当前版本
	oldRoot := m.root.Load().(*RootNode)

	// 2. 检查 key 是否存在（决定操作类型）
	_, exists := oldRoot.Data[key]

	// 3. Copy-on-Write：创建新版本
	newRoot := &RootNode{
		Version: oldRoot.Version + 1,
		Data:    make(map[string]string),
	}

	// 4. 复制所有数据
	maps.Copy(newRoot.Data, oldRoot.Data)

	// 5. 执行操作
	if !exists {
		// 插入新键值对
		newRoot.Data[key] = value
	} else {
		// 更新现有键值对
		newRoot.Data[key] = value
	}

	// 6. ⭐ 原子切换根指针
	m.root.Store(newRoot)

	return nil
}

// GetVersion 获取当前根版本（用于验证）
func (m *MiniCCOW) GetVersion() int {
	root := m.root.Load().(*RootNode)
	return root.Version
}

// Snapshot 获取快照（用于验证快照隔离）
func (m *MiniCCOW) Snapshot() *RootNode {
	return m.root.Load().(*RootNode)
}

// ==========================================
// 测试辅助函数
// ==========================================

// testConcurrentRead 测试并发读操作
func testConcurrentRead(ccow *MiniCCOW, goroutines int, iterations int) {
	// 先写入一些数据
	for i := range 100 {
		key := string(rune(i))
		value := "value-" + key
		ccow.Write(key, value)
	}

	var wg sync.WaitGroup
	start := time.Now()

	// 启动多个 goroutine 同时读
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range iterations {
				key := string(rune(j % 100))
				ccow.Read(key)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// 输出性能统计
	totalOps := goroutines * iterations
	opsPerSec := float64(totalOps) / elapsed.Seconds()
	println("并发读测试完成")
	println("  Goroutines:", goroutines)
	println("  每个goroutine迭代次数:", iterations)
	println("  总操作数:", totalOps)
	println("  耗时:", elapsed)
	println("  吞吐量:", opsPerSec, "ops/s")
}

// testConcurrentWrite 测试并发写操作
func testConcurrentWrite(ccow *MiniCCOW, goroutines int, iterations int) {
	var wg sync.WaitGroup
	start := time.Now()

	// 启动多个 goroutine 同时写
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range iterations {
				key := string(rune(id*1000 + j))
				value := "value-" + string(rune(id))
				ccow.Write(key, value)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// 输出性能统计
	totalOps := goroutines * iterations
	opsPerSec := float64(totalOps) / elapsed.Seconds()
	println("并发写测试完成")
	println("  Goroutines:", goroutines)
	println("  每个goroutine迭代次数:", iterations)
	println("  总操作数:", totalOps)
	println("  耗时:", elapsed)
	println("  吞吐量:", opsPerSec, "ops/s")
}

// testMixedWorkload 测试混合读写负载
func testMixedWorkload(ccow *MiniCCOW, goroutines int, iterations int) {
	// 先写入一些数据
	for i := range 10 {
		key := string(rune(i))
		value := "value-" + key
		ccow.Write(key, value)
	}

	var wg sync.WaitGroup
	start := time.Now()

	// 启动混合读写 goroutines
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range iterations {
				if id%2 == 0 {
					// 读操作
					key := string(rune(j % 10))
					ccow.Read(key)
				} else {
					// 写操作
					key := string(rune(id*1000 + j%10))
					value := "value-" + string(rune(id))
					ccow.Write(key, value)
				}
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	totalOps := goroutines * iterations
	opsPerSec := float64(totalOps) / elapsed.Seconds()
	println("混合负载测试完成")
	println("  Goroutines:", goroutines)
	println("  每个goroutine迭代次数:", iterations)
	println("  总操作数:", totalOps)
	println("  耗时:", elapsed)
	println("  吞吐量:", opsPerSec, "ops/s")
}
