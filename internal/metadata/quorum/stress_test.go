// Package quorum 提供 Quorum 一致性服务集成
//
// 压力测试：验证高并发场景下的正确性和性能
package quorum

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stressTestThreshold 获取压力测试时间阈值（CI 环境使用更宽松的阈值）
func stressTestThreshold(defaultThreshold time.Duration) time.Duration {
	if os.Getenv("CI") != "" {
		return defaultThreshold * 4 // CI 环境使用 4 倍阈值
	}
	return defaultThreshold
}

// TestQuorumAckCollector_HighConcurrency 测试高并发 ACK 收集
// 目标：500 并发
func TestQuorumAckCollector_HighConcurrency(t *testing.T) {
	const goroutines = 500
	const expectedPerCollector = 3

	var wg sync.WaitGroup
	var successCount int32
	var failCount int32

	start := time.Now()

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			collector := NewQuorumAckCollector(expectedPerCollector, 1*time.Second)

			// 模拟接收 ACK
			for j := 0; j < expectedPerCollector; j++ {
				collector.ReceiveACK(fmt.Sprintf("node-%d-%d", id, j), true)
			}

			success, _, ok := collector.WaitAll()
			if ok && success == expectedPerCollector {
				atomic.AddInt32(&successCount, 1)
			} else {
				atomic.AddInt32(&failCount, 1)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	require.Equal(t, int32(goroutines), successCount, "所有收集器应该成功")
	require.Equal(t, int32(0), failCount, "不应该有失败的收集器")
	t.Logf("500 并发 ACK 收集器测试完成: %v", elapsed)
	require.Less(t, elapsed, stressTestThreshold(500*time.Millisecond), "应该在阈值时间内完成")
}

// TestGetResponseCollector_HighConcurrency 测试高并发 GET 响应收集
// 目标：500 并发
func TestGetResponseCollector_HighConcurrency(t *testing.T) {
	const goroutines = 500
	const expectedPerCollector = 3

	var wg sync.WaitGroup
	var successCount int32

	start := time.Now()

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			collector := newGetResponseCollector(expectedPerCollector)
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			// 模拟接收响应
			for j := 0; j < expectedPerCollector; j++ {
				go func(idx int) {
					time.Sleep(time.Microsecond * time.Duration(100+idx))
					collector.AddResponse(&QuorumGetResponsePayload{
						RequestID: fmt.Sprintf("req-%d", id),
						Value:     []byte(fmt.Sprintf("value-%d", idx)),
						Version:   uint64(idx),
						Found:     true,
						NodeID:    fmt.Sprintf("node-%d", idx),
					})
				}(j)
			}

			_, _, err := collector.Wait(ctx)
			if err == nil {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	require.Equal(t, int32(goroutines), successCount, "所有收集器应该成功")
	t.Logf("500 并发 GET 响应收集器测试完成: %v", elapsed)
	require.Less(t, elapsed, stressTestThreshold(500*time.Millisecond), "应该在阈值时间内完成")
}

// TestMessageEncoding_HighThroughput 测试消息编码高吞吐量
// 目标：5000 msg/s
func TestMessageEncoding_HighThroughput(t *testing.T) {
	const targetMsgPerSec = 5000
	const totalMessages = targetMsgPerSec * 2

	var encodedCount int32
	var decodedCount int32
	var wg sync.WaitGroup

	// 预生成 payload
	payload := &QuorumPutPayload{
		TxID:      "tx-benchmark",
		NS:        "benchmark",
		Key:       "test-key",
		Value:     make([]byte, 100), // 100 bytes payload
		Timestamp: time.Now().UnixMilli(),
	}

	start := time.Now()

	// 编码 goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < totalMessages/10; j++ {
				_, err := newQuorumMessageAndEncode(MessageTypeQuorumPut, payload)
				if err == nil {
					atomic.AddInt32(&encodedCount, 1)
				}
			}
		}()
	}

	// 解码 goroutines (使用预编码的消息)
	msgBytes, err := newQuorumMessageAndEncode(MessageTypeQuorumPut, payload)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < totalMessages/10; j++ {
				_, err := decodeMessage(msgBytes)
				if err == nil {
					atomic.AddInt32(&decodedCount, 1)
				}
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	totalOps := encodedCount + decodedCount
	msgPerSec := float64(totalOps) / elapsed.Seconds()

	t.Logf("消息编码吞吐量: %d ops in %v (%.0f ops/s)", totalOps, elapsed, msgPerSec)
	t.Logf("  - 编码: %d msg", encodedCount)
	t.Logf("  - 解码: %d msg", decodedCount)

	require.GreaterOrEqual(t, msgPerSec, float64(targetMsgPerSec*2), // 编码+解码各5000
		"应该达到 5000 msg/s 的编码和解码能力")
}

// TestNetworkIntegrator_ConcurrentOperations 测试并发操作
// 目标：1000 tx/s
func TestNetworkIntegrator_ConcurrentOperations(t *testing.T) {
	const targetTxPerSec = 1000
	const totalTx = targetTxPerSec * 2

	ni := NewNetworkIntegrator(nil)
	var txCount int32
	var wg sync.WaitGroup

	start := time.Now()

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < totalTx/20; j++ {
				txID := generateTxID()

				// 模拟事务操作
				collector := NewQuorumAckCollector(1, 100*time.Millisecond)
				ni.ackCollectors.Store(txID, collector)
				collector.ReceiveACK("node-local", true)
				collector.WaitAll()
				ni.ackCollectors.Delete(txID)

				atomic.AddInt32(&txCount, 1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	txPerSec := float64(txCount) / elapsed.Seconds()
	t.Logf("事务处理吞吐量: %d tx in %v (%.0f tx/s)", txCount, elapsed, txPerSec)

	require.GreaterOrEqual(t, txPerSec, float64(targetTxPerSec),
		"应该达到 1000 tx/s 的处理能力")
}

// TestIDGeneration_HighConcurrency 测试 ID 生成高并发唯一性
func TestIDGeneration_HighConcurrency(t *testing.T) {
	const goroutines = 100
	const idsPerGoroutine = 1000

	ids := sync.Map{}
	var duplicateCount int32
	var wg sync.WaitGroup

	start := time.Now()

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < idsPerGoroutine; j++ {
				id := generateTxID()
				if _, exists := ids.LoadOrStore(id, true); exists {
					atomic.AddInt32(&duplicateCount, 1)
				}

				reqID := generateRequestID()
				if _, exists := ids.LoadOrStore(reqID, true); exists {
					atomic.AddInt32(&duplicateCount, 1)
				}
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	totalIDs := goroutines * idsPerGoroutine * 2 // txID + requestID
	idPerSec := float64(totalIDs) / elapsed.Seconds()

	t.Logf("ID 生成吞吐量: %d IDs in %v (%.0f IDs/s)", totalIDs, elapsed, idPerSec)
	t.Logf("重复 ID 数量: %d", duplicateCount)

	require.Equal(t, int32(0), duplicateCount, "不应该有重复的 ID")
	require.GreaterOrEqual(t, idPerSec, float64(100000), "应该达到 100K IDs/s")
}

// TestQuorumCoordinator_ConcurrentQuorum 测试并发 Quorum 计算
func TestQuorumCoordinator_ConcurrentQuorum(t *testing.T) {
	const goroutines = 500

	var wg sync.WaitGroup
	results := make([]int, goroutines)

	start := time.Now()

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// 不同数量的参与者
			participants := make([]string, idx%100+1)
			for j := range participants {
				participants[j] = fmt.Sprintf("node-%d", j)
			}

			coordinator := NewQuorumCoordinator(participants, nil)
			results[idx] = coordinator.GetQuorum()
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// 验证 Quorum 计算正确性
	for i := 0; i < goroutines; i++ {
		participantCount := i%100 + 1
		expectedQuorum := calculateQuorum(participantCount)
		require.Equal(t, expectedQuorum, results[i],
			"Quorum 计算应该正确 (participants=%d)", participantCount)
	}

	t.Logf("500 并发 Quorum 计算测试完成: %v", elapsed)
	require.Less(t, elapsed, stressTestThreshold(100*time.Millisecond), "应该在阈值时间内完成")
}

// TestAckCollector_MemoryPressure 测试内存压力
func TestAckCollector_MemoryPressure(t *testing.T) {
	const collectors = 10000

	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < collectors; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			collector := NewQuorumAckCollector(100, 1*time.Second)
			for j := 0; j < 50; j++ {
				collector.ReceiveACK(fmt.Sprintf("node-%d", j), true)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	t.Logf("10000 ACK 收集器创建测试完成: %v", elapsed)
	require.Less(t, elapsed, stressTestThreshold(1*time.Second), "应该在阈值时间内完成")
}

// BenchmarkQuorumAckCollector 基准测试 ACK 收集器
func BenchmarkQuorumAckCollector(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			collector := NewQuorumAckCollector(3, 1*time.Second)
			collector.ReceiveACK("node-1", true)
			collector.ReceiveACK("node-2", true)
			collector.ReceiveACK("node-3", true)
			collector.WaitAll()
		}
	})
}

// BenchmarkMessageEncoding 基准测试消息编码
func BenchmarkMessageEncoding(b *testing.B) {
	payload := &QuorumPutPayload{
		TxID:      "tx-bench",
		NS:        "bench",
		Key:       "key",
		Value:     make([]byte, 100),
		Timestamp: time.Now().UnixMilli(),
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := newQuorumMessageAndEncode(MessageTypeQuorumPut, payload)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkMessageDecoding 基准测试消息解码
func BenchmarkMessageDecoding(b *testing.B) {
	payload := &QuorumPutPayload{
		TxID:      "tx-bench",
		NS:        "bench",
		Key:       "key",
		Value:     make([]byte, 100),
		Timestamp: time.Now().UnixMilli(),
	}

	msgBytes, err := newQuorumMessageAndEncode(MessageTypeQuorumPut, payload)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := decodeMessage(msgBytes)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkIDGeneration 基准测试 ID 生成
func BenchmarkIDGeneration(b *testing.B) {
	b.Run("TxID", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = generateTxID()
			}
		})
	})

	b.Run("RequestID", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = generateRequestID()
			}
		})
	})
}
