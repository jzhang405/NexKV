// Package degradation 提供降级日志的 WAL 持久化实现
package degradation

import (
	"context"
	"testing"
	"time"
)

// ==================== WALDemotionLog Tests ====================

func TestNewWALDemotionLog_DefaultConfig(t *testing.T) {
	store := NewMemoryWALStore()

	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if log == nil {
		t.Fatal("expected log to be created")
	}
	if log.Count() != 0 {
		t.Errorf("expected 0 entries, got %d", log.Count())
	}
}

func TestNewWALDemotionLog_NilStore(t *testing.T) {
	_, err := NewWALDemotionLog(nil)
	if err == nil {
		t.Error("expected error with nil config")
	}

	_, err = NewWALDemotionLog(&WALDemotionLogConfig{})
	if err == nil {
		t.Error("expected error with nil store")
	}
}

func TestWALDemotionLog_Append(t *testing.T) {
	store := NewMemoryWALStore()
	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	entry, err := log.Append("ns1", "key1", []byte("value1"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if entry == nil {
		t.Fatal("expected entry to be created")
	}
	if entry.Namespace != "ns1" {
		t.Errorf("expected namespace ns1, got %s", entry.Namespace)
	}
	if entry.Key != "key1" {
		t.Errorf("expected key key1, got %s", entry.Key)
	}
	if string(entry.Value) != "value1" {
		t.Errorf("expected value value1, got %s", entry.Value)
	}
	if entry.Synced {
		t.Error("expected Synced to be false")
	}
	if entry.ID == "" {
		t.Error("expected ID to be set")
	}
	if entry.HLC == nil {
		t.Error("expected HLC to be set")
	}

	// 验证持久化
	if store.Size() != 1 {
		t.Errorf("expected 1 entry in store, got %d", store.Size())
	}

	// 验证内存缓存
	if log.Count() != 1 {
		t.Errorf("expected 1 entry in memory, got %d", log.Count())
	}
	if log.UnsyncedCount() != 1 {
		t.Errorf("expected 1 unsynced, got %d", log.UnsyncedCount())
	}
}

func TestWALDemotionLog_GetUnsynced(t *testing.T) {
	store := NewMemoryWALStore()
	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// 添加多个条目
	_, _ = log.Append("ns1", "key1", []byte("value1"))
	_, _ = log.Append("ns1", "key2", []byte("value2"))
	_, _ = log.Append("ns1", "key3", []byte("value3"))

	unsynced := log.GetUnsynced()
	if len(unsynced) != 3 {
		t.Errorf("expected 3 unsynced, got %d", len(unsynced))
	}

	// 标记一个为已同步
	_ = log.MarkSynced(unsynced[0].ID)

	unsynced = log.GetUnsynced()
	if len(unsynced) != 2 {
		t.Errorf("expected 2 unsynced after mark, got %d", len(unsynced))
	}
}

func TestWALDemotionLog_MarkSynced(t *testing.T) {
	store := NewMemoryWALStore()
	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	entry, _ := log.Append("ns1", "key1", []byte("value1"))

	// 标记为已同步
	err = log.MarkSynced(entry.ID)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// 验证状态更新
	updated, exists := log.GetEntry(entry.ID)
	if !exists {
		t.Fatal("expected entry to exist")
	}
	if !updated.Synced {
		t.Error("expected Synced to be true")
	}
	if updated.SyncedAt == "" {
		t.Error("expected SyncedAt to be set")
	}

	// 验证未同步计数减少
	if log.UnsyncedCount() != 0 {
		t.Errorf("expected 0 unsynced, got %d", log.UnsyncedCount())
	}
}

func TestWALDemotionLog_MarkSynced_NotFound(t *testing.T) {
	store := NewMemoryWALStore()
	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	err = log.MarkSynced("non-existent")
	if err == nil {
		t.Error("expected error for non-existent entry")
	}
}

func TestWALDemotionLog_MarkSyncedBatch(t *testing.T) {
	store := NewMemoryWALStore()
	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	e1, _ := log.Append("ns1", "key1", []byte("value1"))
	e2, _ := log.Append("ns1", "key2", []byte("value2"))
	e3, _ := log.Append("ns1", "key3", []byte("value3"))

	ids := []string{e1.ID, e2.ID, e3.ID}
	err = log.MarkSyncedBatch(ids)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if log.UnsyncedCount() != 0 {
		t.Errorf("expected 0 unsynced, got %d", log.UnsyncedCount())
	}
}

func TestWALDemotionLog_ClearSynced(t *testing.T) {
	store := NewMemoryWALStore()
	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	e1, _ := log.Append("ns1", "key1", []byte("value1"))
	_, _ = log.Append("ns1", "key2", []byte("value2"))

	// 标记一个为已同步
	_ = log.MarkSynced(e1.ID)

	// 清除已同步的条目
	err = log.ClearSynced()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// 验证只剩一个条目
	if log.Count() != 1 {
		t.Errorf("expected 1 entry, got %d", log.Count())
	}

	// 验证存储也减少了（1 unsynced remaining, synced was deleted）
	if store.Size() != 1 {
		t.Errorf("expected 1 entry in store, got %d", store.Size())
	}
}

func TestWALDemotionLog_Compaction(t *testing.T) {
	store := NewMemoryWALStore()
	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store:              store,
		CompactionInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// 添加多个条目
	e1, _ := log.Append("ns1", "key1", []byte("value1"))
	e2, _ := log.Append("ns1", "key2", []byte("value2"))
	_, _ = log.Append("ns1", "key3", []byte("value3"))

	// 标记两个为已同步
	_ = log.MarkSynced(e1.ID)
	_ = log.MarkSynced(e2.ID)

	// 等待 Compaction 间隔
	time.Sleep(150 * time.Millisecond)

	// 执行 Compaction
	err = log.Compaction()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// 验证只保留未同步的条目
	if log.Count() != 1 {
		t.Errorf("expected 1 entry after compaction, got %d", log.Count())
	}
}

func TestWALDemotionLog_Recovery(t *testing.T) {
	store := NewMemoryWALStore()

	// 第一个实例：添加数据
	log1, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	e1, _ := log1.Append("ns1", "key1", []byte("value1"))
	_, _ = log1.Append("ns1", "key2", []byte("value2"))
	_ = log1.MarkSynced(e1.ID)

	// 模拟重启：创建新实例从同一个存储恢复
	log2, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error on recovery, got %v", err)
	}

	// 验证恢复的数据
	if log2.Count() != 2 {
		t.Errorf("expected 2 entries after recovery, got %d", log2.Count())
	}
	if log2.UnsyncedCount() != 1 {
		t.Errorf("expected 1 unsynced after recovery, got %d", log2.UnsyncedCount())
	}

	// 验证已同步状态也被恢复
	entries := log2.GetAllEntries()
	for _, e := range entries {
		if e.ID == e1.ID && !e.Synced {
			t.Error("expected entry 1 to be synced after recovery")
		}
	}
}

func TestWALDemotionLog_GetEntry(t *testing.T) {
	store := NewMemoryWALStore()
	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	entry, _ := log.Append("ns1", "key1", []byte("value1"))

	// 获取存在的条目
	found, exists := log.GetEntry(entry.ID)
	if !exists {
		t.Fatal("expected entry to exist")
	}
	if found.Key != "key1" {
		t.Errorf("expected key key1, got %s", found.Key)
	}

	// 获取不存在的条目
	_, exists = log.GetEntry("non-existent")
	if exists {
		t.Error("expected entry to not exist")
	}
}

func TestWALDemotionLog_GetAllEntries(t *testing.T) {
	store := NewMemoryWALStore()
	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, _ = log.Append("ns1", "key1", []byte("value1"))
	_, _ = log.Append("ns1", "key2", []byte("value2"))
	_, _ = log.Append("ns1", "key3", []byte("value3"))

	entries := log.GetAllEntries()
	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
}

func TestWALDemotionLog_Reset(t *testing.T) {
	store := NewMemoryWALStore()
	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, _ = log.Append("ns1", "key1", []byte("value1"))
	_, _ = log.Append("ns1", "key2", []byte("value2"))

	// 重置
	err = log.Reset()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if log.Count() != 0 {
		t.Errorf("expected 0 entries after reset, got %d", log.Count())
	}
	if store.Size() != 0 {
		t.Errorf("expected 0 entries in store after reset, got %d", store.Size())
	}
}

// ==================== MemoryWALStore Tests ====================

func TestMemoryWALStore_BasicOperations(t *testing.T) {
	store := NewMemoryWALStore()
	ctx := context.Background()

	// Put
	err := store.Put(ctx, "key1", []byte("value1"))
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Get
	value, err := store.Get(ctx, "key1")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if string(value) != "value1" {
		t.Errorf("expected value1, got %s", value)
	}

	// Delete
	err = store.Delete(ctx, "key1")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Get after delete
	value, _ = store.Get(ctx, "key1")
	if value != nil {
		t.Error("expected nil after delete")
	}
}

func TestMemoryWALStore_Scan(t *testing.T) {
	store := NewMemoryWALStore()
	ctx := context.Background()

	// 添加多个条目
	_ = store.Put(ctx, "demotion:entry1", []byte("value1"))
	_ = store.Put(ctx, "demotion:entry2", []byte("value2"))
	_ = store.Put(ctx, "other:entry3", []byte("value3"))

	// 扫描 demotion: 前缀
	result, err := store.Scan(ctx, "demotion:")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 entries, got %d", len(result))
	}
}

func TestMemoryWALStore_Size(t *testing.T) {
	store := NewMemoryWALStore()

	if store.Size() != 0 {
		t.Errorf("expected 0, got %d", store.Size())
	}

	_ = store.Put(context.Background(), "key1", []byte("value1"))
	if store.Size() != 1 {
		t.Errorf("expected 1, got %d", store.Size())
	}
}

// ==================== 辅助函数 Tests ====================

func TestGenerateWALEntryID(t *testing.T) {
	now := time.Now()
	id := generateWALEntryID(1, now)

	if id == "" {
		t.Error("expected ID to be generated")
	}

	// 验证格式
	if len(id) < 10 {
		t.Errorf("ID too short: %s", id)
	}
}

func TestExtractSeqFromID(t *testing.T) {
	id := "dem-20060102-150405-0001"
	seq := extractSeqFromID(id)

	if seq != 1 {
		t.Errorf("expected seq 1, got %d", seq)
	}
}

func TestGenerateCheckpointID(t *testing.T) {
	id := generateCheckpointID()

	if id == "" {
		t.Error("expected checkpoint ID to be generated")
	}

	// 验证前缀
	if len(id) < 3 || id[:3] != "cp-" {
		t.Errorf("expected cp- prefix, got %s", id)
	}
}

// ==================== Concurrent Tests ====================

func TestWALDemotionLog_ConcurrentAccess(t *testing.T) {
	store := NewMemoryWALStore()
	log, err := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	done := make(chan bool)

	// 并发写入
	go func() {
		for i := 0; i < 50; i++ {
			_, _ = log.Append("ns1", string(rune('a'+i%26)), []byte("value"))
		}
		done <- true
	}()

	// 并发标记同步
	go func() {
		for i := 0; i < 50; i++ {
			unsynced := log.GetUnsynced()
			for _, e := range unsynced {
				_ = log.MarkSynced(e.ID)
			}
			time.Sleep(time.Millisecond)
		}
		done <- true
	}()

	// 并发读取
	go func() {
		for i := 0; i < 50; i++ {
			_ = log.Count()
			_ = log.UnsyncedCount()
			_ = log.GetAllEntries()
		}
		done <- true
	}()

	// 等待完成
	for i := 0; i < 3; i++ {
		<-done
	}
}

// ==================== Benchmark ====================

func BenchmarkWALDemotionLog_Append(b *testing.B) {
	store := NewMemoryWALStore()
	log, _ := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = log.Append("ns1", "key", []byte("value"))
	}
}

func BenchmarkWALDemotionLog_MarkSynced(b *testing.B) {
	store := NewMemoryWALStore()
	log, _ := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})

	// 预先添加条目
	var ids []string
	for i := 0; i < b.N; i++ {
		entry, _ := log.Append("ns1", "key", []byte("value"))
		ids = append(ids, entry.ID)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = log.MarkSynced(ids[i])
	}
}

func BenchmarkWALDemotionLog_GetUnsynced(b *testing.B) {
	store := NewMemoryWALStore()
	log, _ := NewWALDemotionLog(&WALDemotionLogConfig{
		Store: store,
	})

	// 预先添加条目
	for i := 0; i < 100; i++ {
		_, _ = log.Append("ns1", "key", []byte("value"))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = log.GetUnsynced()
	}
}
