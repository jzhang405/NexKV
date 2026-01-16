package implementations

import (
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// BenchmarkFaultInjector_InjectRandomFault 基准测试：随机故障注入性能
// 目标：验证优化后的 FaultInjector 在并发场景下的性能提升
func BenchmarkFaultInjector_InjectRandomFault(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-fault-injector-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	// 创建故障注入器
	injector := NewFaultInjector(cluster, 0.10, 50*time.Millisecond)

	b.ResetTimer()

	// 并发运行多个 goroutine 模拟高并发场景
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			injector.injectRandomFault()
		}
	})

	// 清理
	injector.Stop()
}

// BenchmarkFaultInjector_NodeCrash 基准测试：节点崩溃性能
func BenchmarkFaultInjector_NodeCrash(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-node-crash-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	injector := NewFaultInjector(cluster, 0.10, 50*time.Millisecond)

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			injector.injectNodeCrash()
		}
	})

	injector.Stop()
}

// BenchmarkFaultInjector_NetworkPartition 基准测试：网络分区性能
func BenchmarkFaultInjector_NetworkPartition(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-network-partition-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	injector := NewFaultInjector(cluster, 0.10, 50*time.Millisecond)

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			injector.injectNetworkPartition()
		}
	})

	injector.Stop()
}

// BenchmarkFaultInjector_ConcurrentReads 基准测试：并发读取性能
// 验证：读写锁在多读场景下的性能优势
func BenchmarkFaultInjector_ConcurrentReads(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-concurrent-reads-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	injector := NewFaultInjector(cluster, 0.10, 50*time.Millisecond)

	// 模拟多个 goroutine 并发读取集群状态
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		for pb.Next() {
			// 使用读锁读取节点列表
			injector.mu.RLock()
			_ = len(injector.cluster.Nodes)
			injector.mu.RUnlock()

			// 模拟随机延迟
			if rng.Intn(100) < 10 {
				time.Sleep(1 * time.Microsecond)
			}
		}
	})

	injector.Stop()
}

// BenchmarkFaultInjector_MixedWorkload 基准测试：混合读写负载
// 验证：读写锁在读写混合场景下的性能
func BenchmarkFaultInjector_MixedWorkload(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "bench-mixed-workload-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	injector := NewFaultInjector(cluster, 0.10, 50*time.Millisecond)

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		for pb.Next() {
			// 70% 读操作，30% 写操作
			if rng.Intn(100) < 70 {
				// 读操作：使用读锁
				injector.mu.RLock()
				_ = len(injector.cluster.Nodes)
				injector.mu.RUnlock()
			} else {
				// 写操作：模拟故障注入
				injector.injectNodeCrash()
			}
		}
	})

	injector.Stop()
}

// BenchmarkFaultInjector_StopPerformance 基准测试：Stop() 方法性能
// 验证：原子操作优化后的停止性能
func BenchmarkFaultInjector_StopPerformance(b *testing.B) {
	for i := 0; i < b.N; i++ {
		func() {
			tempDir, err := os.MkdirTemp("", "bench-stop-*")
			if err != nil {
				b.Fatal(err)
			}
			defer os.RemoveAll(tempDir)

			cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
			defer cluster.Close()

			injector := NewFaultInjector(cluster, 0.10, 50*time.Millisecond)

			// 启动后台 goroutine
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					if atomic.LoadUint32(&injector.stopping) == 0 {
						injector.injectNodeCrash()
					}
					time.Sleep(1 * time.Millisecond)
				}
			}()

			// 测试 Stop() 性能
			b.StartTimer()
			injector.Stop()
			b.StopTimer()

			wg.Wait()
		}()
	}
}
