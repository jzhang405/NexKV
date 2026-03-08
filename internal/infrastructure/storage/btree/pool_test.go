// Package btree sync.Pool 性能基准测试
//
// Phase 0.5: 技术验证阶段 - Day 6-7
// 验证目标:
// 1. 对象池性能提升效果
// 2. 内存分配减少 ≥ 50%
// 3. pool 版本 ≥ new 版本性能的 80%
//
// 运行测试:
//   go test -bench=. -benchmem ./internal/infrastructure/storage/btree/ -run BenchmarkPool
package btree

import (
	"sync"
	"testing"
)

// ==========================================
// 测试数据结构
// ==========================================

const (
	// PageSize 模拟 BTree 页面大小 (4KB)
	PageSize = 4096

	// MaxKeys 模拟 BTree 节点最大键数
	MaxKeys = 128
)

// Page 模拟 BTree 页面
type Page struct {
	Data    [PageSize]byte
	RefCount int32
}

// Node 模拟 BTree 节点
type Node struct {
	Keys   [][]byte
	Values [][]byte
	RefCount int32
}

// ==========================================
// 对象池实现
// ==========================================

var (
	// PagePool 页面对象池
	pagePool = sync.Pool{
		New: func() any {
			return &Page{
				Data: [PageSize]byte{},
			}
		},
	}

	// NodePool 节点对象池
	nodePool = sync.Pool{
		New: func() any {
			return &Node{
				Keys:   make([][]byte, 0, MaxKeys),
				Values: make([][]byte, 0, MaxKeys),
			}
		},
	}
)

// AcquirePage 从池中获取页面
func AcquirePage() *Page {
	return pagePool.Get().(*Page)
}

// ReleasePage 将页面归还到池中
func ReleasePage(page *Page) {
	// 重置引用计数
	page.RefCount = 0
	// 清零数据（可选，取决于安全需求）
	// page.Data = [PageSize]byte{}
	pagePool.Put(page)
}

// AcquireNode 从池中获取节点
func AcquireNode() *Node {
	return nodePool.Get().(*Node)
}

// ReleaseNode 将节点归还到池中
func ReleaseNode(node *Node) {
	// 重置引用计数
	node.RefCount = 0
	// 清空切片（保留容量）
	node.Keys = node.Keys[:0]
	node.Values = node.Values[:0]
	nodePool.Put(node)
}

// ==========================================
// 非池化版本（用于对比）
// ==========================================

// NewPage 直接创建页面（不使用池）
func NewPage() *Page {
	return &Page{
		Data: [PageSize]byte{},
	}
}

// NewNode 直接创建节点（不使用池）
func NewNode() *Node {
	return &Node{
		Keys:   make([][]byte, 0, MaxKeys),
		Values: make([][]byte, 0, MaxKeys),
	}
}

// ==========================================
// 基准测试 - Page
// ==========================================

// BenchmarkPageWithPool 使用对象池分配页面
func BenchmarkPageWithPool(b *testing.B) {
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			page := AcquirePage()
			// 模拟使用
			page.RefCount = 1
			// 模拟释放
			ReleasePage(page)
		}
	})
}

// BenchmarkPageWithoutPool 不使用对象池分配页面
func BenchmarkPageWithoutPool(b *testing.B) {
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			page := NewPage()
			// 模拟使用
			page.RefCount = 1
			// 不释放，让 GC 回收
			_ = page
		}
	})
}

// BenchmarkPageWithPool_Sequential 顺序分配/释放页面
func BenchmarkPageWithPool_Sequential(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		page := AcquirePage()
		page.RefCount = 1
		ReleasePage(page)
	}
}

// BenchmarkPageWithoutPool_Sequential 顺序分配页面（不使用池）
func BenchmarkPageWithoutPool_Sequential(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		page := NewPage()
		page.RefCount = 1
		_ = page
	}
}

// ==========================================
// 基准测试 - Node
// ==========================================

// BenchmarkNodeWithPool 使用对象池分配节点
func BenchmarkNodeWithPool(b *testing.B) {
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			node := AcquireNode()
			// 模拟使用：添加一些键值对
			for i := 0; i < 10; i++ {
				key := []byte("test-key")
				value := []byte("test-value")
				node.Keys = append(node.Keys, key)
				node.Values = append(node.Values, value)
			}
			// 模拟释放
			ReleaseNode(node)
		}
	})
}

// BenchmarkNodeWithoutPool 不使用对象池分配节点
func BenchmarkNodeWithoutPool(b *testing.B) {
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			node := NewNode()
			// 模拟使用：添加一些键值对
			for i := 0; i < 10; i++ {
				key := []byte("test-key")
				value := []byte("test-value")
				node.Keys = append(node.Keys, key)
				node.Values = append(node.Values, value)
			}
			// 不释放，让 GC 回收
			_ = node
		}
	})
}

// BenchmarkNodeWithPool_Sequential 顺序分配/释放节点
func BenchmarkNodeWithPool_Sequential(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		node := AcquireNode()
		// 模拟使用
		for j := 0; j < 10; j++ {
			key := []byte("test-key")
			value := []byte("test-value")
			node.Keys = append(node.Keys, key)
			node.Values = append(node.Values, value)
		}
		ReleaseNode(node)
	}
}

// BenchmarkNodeWithoutPool_Sequential 顺序分配节点（不使用池）
func BenchmarkNodeWithoutPool_Sequential(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		node := NewNode()
		// 模拟使用
		for j := 0; j < 10; j++ {
			key := []byte("test-key")
			value := []byte("test-value")
			node.Keys = append(node.Keys, key)
			node.Values = append(node.Values, value)
		}
		_ = node
	}
}

// ==========================================
// 混合工作负载测试
// ==========================================

// BenchmarkMixedWorkload_Pool 使用对象池的混合负载
func BenchmarkMixedWorkload_Pool(b *testing.B) {
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// 70% 页面分配，30% 节点分配
			if b.N%10 < 7 {
				page := AcquirePage()
				page.RefCount = 1
				ReleasePage(page)
			} else {
				node := AcquireNode()
				node.Keys = append(node.Keys, []byte("key"))
				node.Values = append(node.Values, []byte("value"))
				ReleaseNode(node)
			}
		}
	})
}

// BenchmarkMixedWorkload_NoPool 不使用对象池的混合负载
func BenchmarkMixedWorkload_NoPool(b *testing.B) {
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// 70% 页面分配，30% 节点分配
			if b.N%10 < 7 {
				page := NewPage()
				page.RefCount = 1
				_ = page
			} else {
				node := NewNode()
				node.Keys = append(node.Keys, []byte("key"))
				node.Values = append(node.Values, []byte("value"))
				_ = node
			}
		}
	})
}

// ==========================================
// GC 压力测试
// ==========================================

// BenchmarkGCPressure_Pool 测试对象池下的 GC 压力
func BenchmarkGCPressure_Pool(b *testing.B) {
	b.ReportAllocs()

	var pages []*Page
	var nodes []*Node

	b.RunParallel(func(pb *testing.PB) {
		localPages := make([]*Page, 0, 100)
		localNodes := make([]*Node, 0, 100)

		for pb.Next() {
			// 分配
			page := AcquirePage()
			node := AcquireNode()

			localPages = append(localPages, page)
			localNodes = append(localNodes, node)

			// 定期释放
			if len(localPages) > 100 {
				for _, p := range localPages {
					ReleasePage(p)
				}
				localPages = localPages[:0]

				for _, n := range localNodes {
					ReleaseNode(n)
				}
				localNodes = localNodes[:0]
			}
		}

		pages = append(pages, localPages...)
		nodes = append(nodes, localNodes...)
	})

	// 清理
	for _, p := range pages {
		ReleasePage(p)
	}
	for _, n := range nodes {
		ReleaseNode(n)
	}
}

// BenchmarkGCPressure_NoPool 测试无对象池下的 GC 压力
func BenchmarkGCPressure_NoPool(b *testing.B) {
	b.ReportAllocs()

	var pages []*Page
	var nodes []*Node

	b.RunParallel(func(pb *testing.PB) {
		localPages := make([]*Page, 0, 100)
		localNodes := make([]*Node, 0, 100)

		for pb.Next() {
			// 分配
			page := NewPage()
			node := NewNode()

			localPages = append(localPages, page)
			localNodes = append(localNodes, node)

			// 定期丢弃
			if len(localPages) > 100 {
				localPages = localPages[:0]
				localNodes = localNodes[:0]
			}
		}

		pages = append(pages, localPages...)
		nodes = append(nodes, localNodes...)
	})

	// 不需要清理，GC 会处理
	_ = pages
	_ = nodes
}

// ==========================================
// 性能对比测试（辅助函数）
// ==========================================

// TestPoolPerformanceSummary 性能对比总结测试
func TestPoolPerformanceSummary(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过性能总结测试（使用 -short 标志）")
	}

	t.Log("=== sync.Pool 性能对比总结 ===")

	// 运行基准测试并收集结果
	results := runBenchmarks(t)

	// 分析结果
	analyzeResults(t, results)
}

// BenchmarkResult 基准测试结果
type BenchmarkResult struct {
	Name         string
	NsPerOp      int64
	AllocsPerOp  int64
	AllocBytesPerOp int64
}

// runBenchmarks 运行所有基准测试
func runBenchmarks(t *testing.T) map[string]BenchmarkResult {
	results := make(map[string]BenchmarkResult)

	// Page 基准测试
	t.Run("Page", func(t *testing.T) {
		result := runBenchmark(t, "BenchmarkPageWithPool", "BenchmarkPageWithoutPool")
		results["Page"] = result
	})

	// Node 基准测试
	t.Run("Node", func(t *testing.T) {
		result := runBenchmark(t, "BenchmarkNodeWithPool", "BenchmarkNodeWithoutPool")
		results["Node"] = result
	})

	return results
}

// runBenchmark 运行单个对比测试
func runBenchmark(t *testing.T, poolName, noPoolName string) BenchmarkResult {
	// 这里简化处理，实际应该使用 testing.Benchmark()
	// 返回模拟数据
	return BenchmarkResult{
		Name:         poolName,
		NsPerOp:      100,  // 占位
		AllocsPerOp:  1,    // 占位
		AllocBytesPerOp: 4096, // 占位
	}
}

// analyzeResults 分析基准测试结果
func analyzeResults(t *testing.T, results map[string]BenchmarkResult) {
	t.Log("性能分析结果:")
	t.Log("  注意: 这些数据需要通过实际基准测试获得")
	t.Log("  运行命令: go test -bench=. -benchmem ./internal/infrastructure/storage/btree/")
}
