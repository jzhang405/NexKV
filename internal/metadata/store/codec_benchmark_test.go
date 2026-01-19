// Package store 编解码器性能基准测试
//
// 对比 MessagePack、JSON 和 Protobuf 三种编解码器的性能差异
// 测试维度：
//   - 编码/解码速度（ns/op）
//   - 内存分配（B/op）
//   - 分配次数（allocs/op）
//   - 编码后数据大小（bytes）
//
// 使用方法：
//
//	go test -bench=. -benchmem -benchtime=10s ./internal/metadata/store/
package store

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// WAL Codec 性能基准测试
// ========================================

// BenchmarkWALCodec_MessagePack_Encode MessagePack 编码性能
func BenchmarkWALCodec_MessagePack_Encode(b *testing.B) {
	codec := NewMessagePackWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Encode(entry)
	}
}

// BenchmarkWALCodec_JSON_Encode JSON 编码性能
func BenchmarkWALCodec_JSON_Encode(b *testing.B) {
	codec := NewJSONWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Encode(entry)
	}
}

// BenchmarkWALCodec_Protobuf_Encode Protobuf 编码性能
func BenchmarkWALCodec_Protobuf_Encode(b *testing.B) {
	codec := NewProtobufWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Encode(entry)
	}
}

// BenchmarkWALCodec_MessagePack_Decode MessagePack 解码性能
func BenchmarkWALCodec_MessagePack_Decode(b *testing.B) {
	codec := NewMessagePackWALCodec()
	entry := createTestWALEntry(1024)
	data, _ := codec.Encode(entry)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Decode(data)
	}
}

// BenchmarkWALCodec_JSON_Decode JSON 解码性能
func BenchmarkWALCodec_JSON_Decode(b *testing.B) {
	codec := NewJSONWALCodec()
	entry := createTestWALEntry(1024)
	data, _ := codec.Encode(entry)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Decode(data)
	}
}

// BenchmarkWALCodec_Protobuf_Decode Protobuf 解码性能
func BenchmarkWALCodec_Protobuf_Decode(b *testing.B) {
	codec := NewProtobufWALCodec()
	entry := createTestWALEntry(1024)
	data, _ := codec.Encode(entry)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Decode(data)
	}
}

// BenchmarkWALCodec_MessagePack_RoundTrip MessagePack 编解码往返性能
func BenchmarkWALCodec_MessagePack_RoundTrip(b *testing.B) {
	codec := NewMessagePackWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := codec.Encode(entry)
		_, _ = codec.Decode(data)
	}
}

// BenchmarkWALCodec_JSON_RoundTrip JSON 编解码往返性能
func BenchmarkWALCodec_JSON_RoundTrip(b *testing.B) {
	codec := NewJSONWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := codec.Encode(entry)
		_, _ = codec.Decode(data)
	}
}

// BenchmarkWALCodec_Protobuf_RoundTrip Protobuf 编解码往返性能
func BenchmarkWALCodec_Protobuf_RoundTrip(b *testing.B) {
	codec := NewProtobufWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := codec.Encode(entry)
		_, _ = codec.Decode(data)
	}
}

// ========================================
// 不同数据大小性能对比
// ========================================

// BenchmarkWALCodec_DifferentSizes 不同数据大小编解码性能对比
func BenchmarkWALCodec_DifferentSizes(b *testing.B) {
	sizes := []int{64, 256, 1024, 4096, 16384}

	for _, size := range sizes {
		b.Run("MessagePack/Encode", func(b *testing.B) {
			codec := NewMessagePackWALCodec()
			entry := createTestWALEntry(size)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = codec.Encode(entry)
			}
		})

		b.Run("JSON/Encode", func(b *testing.B) {
			codec := NewJSONWALCodec()
			entry := createTestWALEntry(size)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = codec.Encode(entry)
			}
		})

		b.Run("Protobuf/Encode", func(b *testing.B) {
			codec := NewProtobufWALCodec()
			entry := createTestWALEntry(size)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = codec.Encode(entry)
			}
		})
	}
}

// ========================================
// 批量编解码性能对比
// ========================================

// BenchmarkWALCodec_BatchEncode_MessagePack MessagePack 批量编码性能
func BenchmarkWALCodec_BatchEncode_MessagePack(b *testing.B) {
	codec := NewMessagePackWALCodec()
	entries := createTestWALEntries(100, 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, entry := range entries {
			_, _ = codec.Encode(entry)
		}
	}
}

// BenchmarkWALCodec_BatchEncode_JSON JSON 批量编码性能
func BenchmarkWALCodec_BatchEncode_JSON(b *testing.B) {
	codec := NewJSONWALCodec()
	entries := createTestWALEntries(100, 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, entry := range entries {
			_, _ = codec.Encode(entry)
		}
	}
}

// BenchmarkWALCodec_BatchEncode_Protobuf Protobuf 批量编码性能
func BenchmarkWALCodec_BatchEncode_Protobuf(b *testing.B) {
	codec := NewProtobufWALCodec()
	entries := createTestWALEntries(100, 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, entry := range entries {
			_, _ = codec.Encode(entry)
		}
	}
}

// BenchmarkWALCodec_BatchDecode_MessagePack MessagePack 批量解码性能
func BenchmarkWALCodec_BatchDecode_MessagePack(b *testing.B) {
	codec := NewMessagePackWALCodec()
	entries := createTestWALEntries(100, 1024)
	datas := make([][]byte, len(entries))

	for i, entry := range entries {
		datas[i], _ = codec.Encode(entry)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, data := range datas {
			_, _ = codec.Decode(data)
		}
	}
}

// BenchmarkWALCodec_BatchDecode_JSON JSON 批量解码性能
func BenchmarkWALCodec_BatchDecode_JSON(b *testing.B) {
	codec := NewJSONWALCodec()
	entries := createTestWALEntries(100, 1024)
	datas := make([][]byte, len(entries))

	for i, entry := range entries {
		datas[i], _ = codec.Encode(entry)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, data := range datas {
			_, _ = codec.Decode(data)
		}
	}
}

// BenchmarkWALCodec_BatchDecode_Protobuf Protobuf 批量解码性能
func BenchmarkWALCodec_BatchDecode_Protobuf(b *testing.B) {
	codec := NewProtobufWALCodec()
	entries := createTestWALEntries(100, 1024)
	datas := make([][]byte, len(entries))

	for i, entry := range entries {
		datas[i], _ = codec.Encode(entry)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, data := range datas {
			_, _ = codec.Decode(data)
		}
	}
}

// ========================================
// 编码后数据大小对比
// ========================================

// BenchmarkWALCodec_EncodedSize_MessagePack MessagePack 编码后数据大小
func BenchmarkWALCodec_EncodedSize_MessagePack(b *testing.B) {
	codec := NewMessagePackWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := codec.Encode(entry)
		_ = data
	}

	// 报告编码后数据大小
	b.ReportMetric(float64(len(encodeWALEntry(codec, entry))), "bytes")
}

// BenchmarkWALCodec_EncodedSize_JSON JSON 编码后数据大小
func BenchmarkWALCodec_EncodedSize_JSON(b *testing.B) {
	codec := NewJSONWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := codec.Encode(entry)
		_ = data
	}

	// 报告编码后数据大小
	b.ReportMetric(float64(len(encodeWALEntry(codec, entry))), "bytes")
}

// BenchmarkWALCodec_EncodedSize_Protobuf Protobuf 编码后数据大小
func BenchmarkWALCodec_EncodedSize_Protobuf(b *testing.B) {
	codec := NewProtobufWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := codec.Encode(entry)
		_ = data
	}

	// 报告编码后数据大小
	b.ReportMetric(float64(len(encodeWALEntry(codec, entry))), "bytes")
}

// ========================================
// 不同 WAL 类型性能对比
// ========================================

// BenchmarkWALCodec_AllTypes_MessagePack MessagePack 所有类型编解码性能
func BenchmarkWALCodec_AllTypes_MessagePack(b *testing.B) {
	codec := NewMessagePackWALCodec()

	typeTest := []struct {
		name string
		typ  WALType
	}{
		{"Put", WALTypePut},
		{"Delete", WALTypeDelete},
		{"Checkpoint", WALTypeCheckpoint},
	}

	for _, tt := range typeTest {
		b.Run(tt.name, func(b *testing.B) {
			entry := createTestWALEntryByType(tt.typ, 1024)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				data, _ := codec.Encode(entry)
				_, _ = codec.Decode(data)
			}
		})
	}
}

// BenchmarkWALCodec_AllTypes_JSON JSON 所有类型编解码性能
func BenchmarkWALCodec_AllTypes_JSON(b *testing.B) {
	codec := NewJSONWALCodec()

	typeTest := []struct {
		name string
		typ  WALType
	}{
		{"Put", WALTypePut},
		{"Delete", WALTypeDelete},
		{"Checkpoint", WALTypeCheckpoint},
	}

	for _, tt := range typeTest {
		b.Run(tt.name, func(b *testing.B) {
			entry := createTestWALEntryByType(tt.typ, 1024)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				data, _ := codec.Encode(entry)
				_, _ = codec.Decode(data)
			}
		})
	}
}

// BenchmarkWALCodec_AllTypes_Protobuf Protobuf 所有类型编解码性能
func BenchmarkWALCodec_AllTypes_Protobuf(b *testing.B) {
	codec := NewProtobufWALCodec()

	typeTest := []struct {
		name string
		typ  WALType
	}{
		{"Put", WALTypePut},
		{"Delete", WALTypeDelete},
		{"Checkpoint", WALTypeCheckpoint},
	}

	for _, tt := range typeTest {
		b.Run(tt.name, func(b *testing.B) {
			entry := createTestWALEntryByType(tt.typ, 1024)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				data, _ := codec.Encode(entry)
				_, _ = codec.Decode(data)
			}
		})
	}
}

// ========================================
// 并发编解码性能对比
// ========================================

// BenchmarkWALCodec_ConcurrentEncode_MessagePack MessagePack 并发编码性能
func BenchmarkWALCodec_ConcurrentEncode_MessagePack(b *testing.B) {
	codec := NewMessagePackWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = codec.Encode(entry)
		}
	})
}

// BenchmarkWALCodec_ConcurrentEncode_JSON JSON 并发编码性能
func BenchmarkWALCodec_ConcurrentEncode_JSON(b *testing.B) {
	codec := NewJSONWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = codec.Encode(entry)
		}
	})
}

// BenchmarkWALCodec_ConcurrentEncode_Protobuf Protobuf 并发编码性能
func BenchmarkWALCodec_ConcurrentEncode_Protobuf(b *testing.B) {
	codec := NewProtobufWALCodec()
	entry := createTestWALEntry(1024)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = codec.Encode(entry)
		}
	})
}

// BenchmarkWALCodec_ConcurrentDecode_MessagePack MessagePack 并发解码性能
func BenchmarkWALCodec_ConcurrentDecode_MessagePack(b *testing.B) {
	codec := NewMessagePackWALCodec()
	entry := createTestWALEntry(1024)
	data, _ := codec.Encode(entry)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = codec.Decode(data)
		}
	})
}

// BenchmarkWALCodec_ConcurrentDecode_JSON JSON 并发解码性能
func BenchmarkWALCodec_ConcurrentDecode_JSON(b *testing.B) {
	codec := NewJSONWALCodec()
	entry := createTestWALEntry(1024)
	data, _ := codec.Encode(entry)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = codec.Decode(data)
		}
	})
}

// BenchmarkWALCodec_ConcurrentDecode_Protobuf Protobuf 并发解码性能
func BenchmarkWALCodec_ConcurrentDecode_Protobuf(b *testing.B) {
	codec := NewProtobufWALCodec()
	entry := createTestWALEntry(1024)
	data, _ := codec.Encode(entry)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = codec.Decode(data)
		}
	})
}

// ========================================
// 测试辅助函数
// ========================================

// createTestWALEntry 创建测试用 WAL 条目
func createTestWALEntry(valueSize int) *WALEntry {
	hlc := clock.NewHLC()

	return &WALEntry{
		Timestamp: hlc,
		Type:      WALTypePut,
		Key:       "test_key",
		Value:     make([]byte, valueSize),
		OldValue:  make([]byte, valueSize/2),
		Checksum:  12345,
	}
}

// createTestWALEntryByType 根据类型创建测试用 WAL 条目
func createTestWALEntryByType(walType WALType, valueSize int) *WALEntry {
	hlc := clock.NewHLC()

	entry := &WALEntry{
		Timestamp: hlc,
		Type:      walType,
		Key:       "test_key",
		Value:     make([]byte, valueSize),
		Checksum:  12345,
	}

	// 根据类型设置不同的字段
	switch walType {
	case WALTypePut:
		entry.OldValue = make([]byte, valueSize/2)
	case WALTypeDelete:
		entry.Value = nil
	case WALTypeCheckpoint:
		entry.Value = nil
		entry.OldValue = nil
	}

	return entry
}

// createTestWALEntries 创建批量测试用 WAL 条目
func createTestWALEntries(count, valueSize int) []*WALEntry {
	entries := make([]*WALEntry, count)
	for i := 0; i < count; i++ {
		entries[i] = createTestWALEntry(valueSize)
	}
	return entries
}

// encodeWALEntry 辅助函数：编码 WAL 条目并返回结果
func encodeWALEntry(codec WALCodec, entry *WALEntry) []byte {
	data, err := codec.Encode(entry)
	if err != nil {
		return nil
	}
	return data
}

// ========================================
// 性能对比总结测试（不参与 benchmark）
// ========================================

// TestCodecPerformanceSummary 编解码器性能对比总结
//
// 注意：此测试函数不会被 benchmark 工具执行
// 需要手动运行以查看性能对比数据
//
// 运行方式：
//
//	go test -v -run TestCodecPerformanceSummary ./internal/metadata/store/
func TestCodecPerformanceSummary(t *testing.T) {
	t.Log("========================================")
	t.Log("WAL Codec 性能对比总结")
	t.Log("========================================")

	valueSizes := []int{64, 256, 1024, 4096, 16384}

	for _, size := range valueSizes {
		t.Logf("\n数据大小: %d bytes", size)

		// 创建测试条目
		entry := createTestWALEntry(size)

		// MessagePack 测试
		mpCodec := NewMessagePackWALCodec()
		mpData, _ := mpCodec.Encode(entry)
		mpSize := len(mpData)

		// JSON 测试
		jsonCodec := NewJSONWALCodec()
		jsonData, _ := jsonCodec.Encode(entry)
		jsonSize := len(jsonData)

		// Protobuf 测试
		pbCodec := NewProtobufWALCodec()
		pbData, _ := pbCodec.Encode(entry)
		pbSize := len(pbData)

		// 计算压缩比和空间节省
		jsonVsMpRatio := float64(jsonSize) / float64(mpSize)
		jsonVsMpSaving := (1 - float64(mpSize)/float64(jsonSize)) * 100

		jsonVsPbRatio := float64(jsonSize) / float64(pbSize)
		jsonVsPbSaving := (1 - float64(pbSize)/float64(jsonSize)) * 100

		mpVsPbRatio := float64(mpSize) / float64(pbSize)
		mpVsPbSaving := (1 - float64(pbSize)/float64(mpSize)) * 100

		t.Logf("  MessagePack: %d bytes", mpSize)
		t.Logf("  JSON:        %d bytes", jsonSize)
		t.Logf("  Protobuf:    %d bytes", pbSize)
		t.Logf("  ----------------------------------------")
		t.Logf("  压缩比 (JSON/MessagePack):      %.2fx", jsonVsMpRatio)
		t.Logf("  空间节省 (MessagePack vs JSON): %.2f%%", jsonVsMpSaving)
		t.Logf("  ----------------------------------------")
		t.Logf("  压缩比 (JSON/Protobuf):        %.2fx", jsonVsPbRatio)
		t.Logf("  空间节省 (Protobuf vs JSON):    %.2f%%", jsonVsPbSaving)
		t.Logf("  ----------------------------------------")
		t.Logf("  压缩比 (MessagePack/Protobuf):  %.2fx", mpVsPbRatio)
		t.Logf("  空间节省 (Protobuf vs MP):      %.2f%%", mpVsPbSaving)
	}

	t.Log("\n========================================")
	t.Log("推荐使用场景：")
	t.Log("========================================")
	t.Log("MessagePack:")
	t.Log("  - 生产环境（高性能、低存储）")
	t.Log("  - 大数据量场景")
	t.Log("  - 高并发场景")
	t.Log("")
	t.Log("Protobuf:")
	t.Log("  - 生产环境（跨语言、高兼容性）")
	t.Log("  - 大规模分布式系统")
	t.Log("  - 需要强类型约束和 Schema 管理")
	t.Log("")
	t.Log("JSON:")
	t.Log("  - 开发调试（可读性好）")
	t.Log("  - 跨语言集成")
	t.Log("  - 人工检查 WAL 文件")
	t.Log("========================================")
}

// TestCodecTypeValidation 编解码器类型验证测试
func TestCodecTypeValidation(t *testing.T) {
	tests := []struct {
		name      string
		codecType types.CodecType
		wantName  string
	}{
		{
			name:      "MessagePack Codec",
			codecType: types.CodecTypeMessagePack,
			wantName:  "msgpack",
		},
		{
			name:      "JSON Codec",
			codecType: types.CodecTypeJSON,
			wantName:  "json",
		},
		{
			name:      "Protobuf Codec",
			codecType: types.CodecTypeProtobuf,
			wantName:  "protobuf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codec, err := NewWALCodec(tt.codecType)
			if err != nil {
				t.Fatalf("NewWALCodec() error = %v", err)
			}

			if codec.Type() != tt.codecType {
				t.Errorf("Type() = %v, want %v", codec.Type(), tt.codecType)
			}

			if codec.Name() != tt.wantName {
				t.Errorf("Name() = %v, want %v", codec.Name(), tt.wantName)
			}
		})
	}
}
