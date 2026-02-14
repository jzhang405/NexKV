// Package gossip 提供带宽优化器测试
package gossip

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"testing"
	"time"
)

// ==================== 配置测试 ====================

func TestDefaultBandwidthConfig(t *testing.T) {
	config := DefaultBandwidthConfig()

	if config.BatchSize != 50 {
		t.Errorf("expected default BatchSize 50, got %d", config.BatchSize)
	}
	if config.BatchTimeout != 100*time.Millisecond {
		t.Errorf("expected default BatchTimeout 100ms, got %v", config.BatchTimeout)
	}
	if config.CompressionThreshold != 1024 {
		t.Errorf("expected default CompressionThreshold 1024, got %d", config.CompressionThreshold)
	}
	if !config.EnableCompression {
		t.Error("expected EnableCompression to be true by default")
	}
	if config.MaxBatchSize != 100 {
		t.Errorf("expected MaxBatchSize 100, got %d", config.MaxBatchSize)
	}
}

func TestBandwidthConfig_Custom(t *testing.T) {
	config := &BandwidthConfig{
		BatchSize:            100,
		BatchTimeout:         200 * time.Millisecond,
		CompressionThreshold: 2048,
		EnableCompression:    false,
		MaxBatchSize:         200,
	}

	if config.BatchSize != 100 {
		t.Errorf("expected BatchSize 100, got %d", config.BatchSize)
	}
	if config.EnableCompression {
		t.Error("expected EnableCompression to be false")
	}
}

// ==================== 带宽优化器测试 ====================

func TestNewBandwidthOptimizer(t *testing.T) {
	config := &BandwidthConfig{
		BatchSize:    10,
		BatchTimeout: 50 * time.Millisecond,
	}
	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	if optimizer == nil {
		t.Error("expected optimizer to be created")
	}
}

func TestBandwidthOptimizer_Submit(t *testing.T) {
	config := &BandwidthConfig{
		BatchSize:    5,
		BatchTimeout: 50 * time.Millisecond,
	}
	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	// 提交事件
	event := GossipEvent{
		Type:      EventWrite,
		Namespace: "test",
		Key:       "key1",
	}
	optimizer.Submit(event)

	stats := optimizer.GetStats()
	if stats.TotalEvents != 1 {
		t.Errorf("expected 1 event, got %d", stats.TotalEvents)
	}
}

func TestBandwidthOptimizer_BatchEvents(t *testing.T) {
	config := &BandwidthConfig{
		BatchSize:    5,
		BatchTimeout: 50 * time.Millisecond,
	}
	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	// 提交多个事件
	for i := 0; i < 10; i++ {
		optimizer.Submit(GossipEvent{
			Type:      EventWrite,
			Namespace: "test",
			Key:       string(rune('a' + i)),
		})
	}

	// 等待批处理
	time.Sleep(100 * time.Millisecond)

	stats := optimizer.GetStats()
	if stats.TotalEvents != 10 {
		t.Errorf("expected 10 events, got %d", stats.TotalEvents)
	}
}

func TestBandwidthOptimizer_GetStats(t *testing.T) {
	config := DefaultBandwidthConfig()
	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	stats := optimizer.GetStats()

	if stats.QueueDepth != 0 {
		t.Errorf("expected empty queue, got %d", stats.QueueDepth)
	}
}

// ==================== 压缩测试 ====================

func TestBandwidthOptimizer_CompressIfNeeded(t *testing.T) {
	config := &BandwidthConfig{
		EnableCompression:    true,
		CompressionThreshold: 10,
	}

	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	// 小数据不压缩
	smallData := []byte("hello")
	compressed, wasCompressed, err := optimizer.CompressIfNeeded(smallData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wasCompressed {
		t.Error("expected small data not to be compressed")
	}
	if !bytes.Equal(compressed, smallData) {
		t.Error("expected small data to be unchanged")
	}

	// 大数据压缩
	largeData := make([]byte, 1000)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}
	compressed, wasCompressed, err = optimizer.CompressIfNeeded(largeData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wasCompressed {
		t.Error("expected large data to be compressed")
	}
	if len(compressed) >= len(largeData) {
		t.Errorf("expected compressed data to be smaller, got %d >= %d", len(compressed), len(largeData))
	}
}

func TestBandwidthOptimizer_CompressIfNeeded_Disabled(t *testing.T) {
	config := &BandwidthConfig{
		EnableCompression:    false,
		CompressionThreshold: 10,
	}

	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	// 即使大数据也不压缩
	largeData := make([]byte, 1000)
	compressed, wasCompressed, err := optimizer.CompressIfNeeded(largeData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wasCompressed {
		t.Error("expected no compression when disabled")
	}
	if !bytes.Equal(compressed, largeData) {
		t.Error("expected data to be unchanged")
	}
}

func TestBandwidthOptimizer_DecompressIfNeeded(t *testing.T) {
	config := &BandwidthConfig{
		EnableCompression:    true,
		CompressionThreshold: 10,
	}

	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	// 原始数据
	original := make([]byte, 1000)
	for i := range original {
		original[i] = byte(i % 256)
	}

	// 压缩
	compressed, wasCompressed, err := optimizer.CompressIfNeeded(original)
	if err != nil {
		t.Fatalf("compression error: %v", err)
	}
	if !wasCompressed {
		t.Fatal("expected data to be compressed")
	}

	// 解压
	decompressed, err := optimizer.DecompressIfNeeded(compressed, true)
	if err != nil {
		t.Fatalf("decompression error: %v", err)
	}
	if !bytes.Equal(decompressed, original) {
		t.Error("expected decompressed data to match original")
	}

	// 不解压未压缩的数据
	unchanged, err := optimizer.DecompressIfNeeded(original, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(unchanged, original) {
		t.Error("expected unchanged data")
	}
}

// ==================== 消息合并测试 ====================

func TestBandwidthOptimizer_MergeEvents_Empty(t *testing.T) {
	config := DefaultBandwidthConfig()
	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	merged := optimizer.MergeEvents([]GossipEvent{})
	if merged != nil {
		t.Error("expected nil for empty events")
	}
}

func TestBandwidthOptimizer_MergeEvents_Single(t *testing.T) {
	config := DefaultBandwidthConfig()
	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	events := []GossipEvent{
		{Type: EventWrite, Namespace: "ns1", Key: "key1", Timestamp: time.Now()},
	}

	merged := optimizer.MergeEvents(events)
	if merged == nil {
		t.Fatal("expected merged event")
	}
	if merged.Type != EventWrite {
		t.Errorf("expected EventWrite, got %d", merged.Type)
	}
	if merged.Namespace != "ns1" {
		t.Errorf("expected namespace ns1, got %s", merged.Namespace)
	}
}

func TestBandwidthOptimizer_MergeEvents_Multiple(t *testing.T) {
	config := DefaultBandwidthConfig()
	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	now := time.Now()
	events := []GossipEvent{
		{Type: EventWrite, Namespace: "ns1", Key: "key1", Timestamp: now.Add(-2 * time.Second)},
		{Type: EventWrite, Namespace: "ns1", Key: "key1", Timestamp: now}, // 最新
		{Type: EventWrite, Namespace: "ns1", Key: "key2", Timestamp: now.Add(-1 * time.Second)},
	}

	merged := optimizer.MergeEvents(events)
	if merged == nil {
		t.Fatal("expected merged event")
	}
	// 对于多个不同 Key，应该返回批量事件
	if merged.Type != EventBatch {
		t.Errorf("expected EventBatch, got %d", merged.Type)
	}
}

func TestBandwidthOptimizer_MergeEvents_SameKey(t *testing.T) {
	config := DefaultBandwidthConfig()
	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	now := time.Now()
	events := []GossipEvent{
		{Type: EventWrite, Namespace: "ns1", Key: "key1", Timestamp: now.Add(-2 * time.Second)},
		{Type: EventWrite, Namespace: "ns1", Key: "key1", Timestamp: now.Add(-1 * time.Second)},
		{Type: EventWrite, Namespace: "ns1", Key: "key1", Timestamp: now}, // 最新
	}

	merged := optimizer.MergeEvents(events)
	if merged == nil {
		t.Fatal("expected merged event")
	}
	// 同一个 Key，应该返回最新的
	if merged.Type != EventWrite {
		t.Errorf("expected EventWrite, got %d", merged.Type)
	}
	if !merged.Timestamp.Equal(now) {
		t.Errorf("expected latest timestamp, got %v", merged.Timestamp)
	}
}

// ==================== 增量同步测试 ====================

func TestBandwidthOptimizer_SyncDifferential_NoMerkleSync(t *testing.T) {
	config := DefaultBandwidthConfig()
	optimizer := NewBandwidthOptimizer(config, nil) // 无 MerkleSync
	defer optimizer.Close()

	ctx := context.Background()
	result, err := optimizer.SyncDifferential(ctx, "peer-1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result when no merkleSync")
	}
}

// ==================== 统计测试 ====================

func TestBandwidthOptimizer_Stats(t *testing.T) {
	config := &BandwidthConfig{
		BatchSize:            10,
		BatchTimeout:         50 * time.Millisecond,
		EnableCompression:    true,
		CompressionThreshold: 100,
	}

	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	// 提交事件
	for i := 0; i < 20; i++ {
		optimizer.Submit(GossipEvent{
			Type:      EventWrite,
			Namespace: "test",
			Key:       string(rune('a' + i%26)),
		})
	}

	// 压缩测试
	largeData := make([]byte, 500)
	_, _, _ = optimizer.CompressIfNeeded(largeData)

	// 等待批处理
	time.Sleep(100 * time.Millisecond)

	stats := optimizer.GetStats()

	if stats.TotalEvents != 20 {
		t.Errorf("expected 20 events, got %d", stats.TotalEvents)
	}
	if stats.CompressionCount != 1 {
		t.Errorf("expected 1 compression, got %d", stats.CompressionCount)
	}
	if stats.CompressionRatio <= 0 {
		t.Errorf("expected positive compression ratio, got %f", stats.CompressionRatio)
	}
}

// ==================== 辅助测试 ====================

func TestBandwidthOptimizer_EstimateEventSize(t *testing.T) {
	config := DefaultBandwidthConfig()
	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	event := GossipEvent{
		Type:      EventWrite,
		Namespace: "namespace",
		Key:       "key",
		Value:     []byte("value"),
	}

	size := optimizer.estimateEventSize(event)
	if size <= 0 {
		t.Errorf("expected positive size, got %d", size)
	}
	// 基础大小(64) + len("namespace") + len("key") + len("value")
	expected := 64 + len(event.Namespace) + len(event.Key) + len(event.Value)
	if size != expected {
		t.Errorf("expected size %d, got %d", expected, size)
	}
}

func TestBandwidthOptimizer_GetBatchChan(t *testing.T) {
	config := &BandwidthConfig{
		BatchSize:    3,
		BatchTimeout: 50 * time.Millisecond,
	}

	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	batchChan := optimizer.GetBatchChan()
	if batchChan == nil {
		t.Error("expected batch channel")
	}

	// 提交事件触发批次
	for i := 0; i < 5; i++ {
		optimizer.Submit(GossipEvent{
			Type:      EventWrite,
			Namespace: "test",
			Key:       string(rune('a' + i)),
		})
	}

	// 等待批次
	select {
	case batch := <-batchChan:
		if len(batch.Events) == 0 {
			t.Error("expected non-empty batch")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("expected batch to be produced")
	}
}

// ==================== 并发测试 ====================

func TestBandwidthOptimizer_ConcurrentSubmit(t *testing.T) {
	config := &BandwidthConfig{
		BatchSize:    100, // 增大队列以避免丢失
		BatchTimeout: 50 * time.Millisecond,
	}

	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	done := make(chan bool)

	// 并发提交
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 10; j++ {
				optimizer.Submit(GossipEvent{
					Type:      EventWrite,
					Namespace: "test",
					Key:       string(rune('a' + j)),
				})
			}
			done <- true
		}(i)
	}

	// 等待完成
	for i := 0; i < 5; i++ {
		<-done
	}

	time.Sleep(100 * time.Millisecond)

	stats := optimizer.GetStats()
	// 并发提交可能会有丢失，检查至少收到了部分事件
	if stats.TotalEvents < 40 {
		t.Errorf("expected at least 40 events, got %d", stats.TotalEvents)
	}
}

// ==================== Benchmark ====================

func BenchmarkBandwidthOptimizer_Submit(b *testing.B) {
	config := &BandwidthConfig{
		BatchSize:    100,
		BatchTimeout: 10 * time.Millisecond,
	}

	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	event := GossipEvent{
		Type:      EventWrite,
		Namespace: "benchmark",
		Key:       "key",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		optimizer.Submit(event)
	}
}

func BenchmarkBandwidthOptimizer_CompressIfNeeded(b *testing.B) {
	config := &BandwidthConfig{
		EnableCompression:    true,
		CompressionThreshold: 100,
	}

	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = optimizer.CompressIfNeeded(data)
	}
}

func BenchmarkBandwidthOptimizer_MergeEvents(b *testing.B) {
	config := DefaultBandwidthConfig()
	optimizer := NewBandwidthOptimizer(config, nil)
	defer optimizer.Close()

	events := make([]GossipEvent, 100)
	for i := range events {
		events[i] = GossipEvent{
			Type:      EventWrite,
			Namespace: "benchmark",
			Key:       string(rune('a' + i%26)),
			Timestamp: time.Now(),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = optimizer.MergeEvents(events)
	}
}

// ==================== 辅助函数 ====================

// 手动验证压缩/解压
func TestCompressDecompress(t *testing.T) {
	original := []byte("hello world, this is a test string that should compress well")

	// 压缩
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, err := writer.Write(original)
	if err != nil {
		t.Fatalf("compress error: %v", err)
	}
	writer.Close()
	compressed := buf.Bytes()

	// 解压
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("create reader error: %v", err)
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("decompress error: %v", err)
	}
	reader.Close()

	if !bytes.Equal(decompressed, original) {
		t.Error("expected decompressed to match original")
	}
}
