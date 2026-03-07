// Package benchmark 提供 BoltDB vs BfTree 性能基准测试对比
package benchmark

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"go.etcd.io/bbolt"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/bftree"
)

// BenchmarkConfig 基准测试配置
type BenchmarkConfig struct {
	DataSize      int    // 数据条数
	KeySize       int    // Key 大小
	ValueSize     int    // Value 大小
	NumReaders    int    // 并发读协程数
	NumWriters    int    // 并发写协程数
	UseBitmapLock bool   // BfTree 是否启用 BitmapLock
}

// DefaultBenchmarkConfig 默认配置
func DefaultBenchmarkConfig() BenchmarkConfig {
	return BenchmarkConfig{
		DataSize:      10000,
		KeySize:       16,
		ValueSize:     100,
		NumReaders:    10,
		NumWriters:    10,
		UseBitmapLock: false,
	}
}

// generateTestData 生成测试数据
func generateTestData(size, keySize, valueSize int) [][2][]byte {
	data := make([][2][]byte, size)
	for i := 0; i < size; i++ {
		key := fmt.Sprintf("key-%010d", i)
		value := fmt.Sprintf("value-%098d", i) // 补齐到 100 字节
		data[i] = [2][]byte{[]byte(key), []byte(value)}
	}
	return data
}

// setupBoltDB 创建 BoltDB 测试实例
func setupBoltDB(dataSize int) (*bbolt.DB, string, error) {
	tmpDir := os.TempDir()
	dbPath := filepath.Join(tmpDir, fmt.Sprintf("boltdb_bench_%d.db", dataSize))
	os.Remove(dbPath) // 清理旧数据

	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{
		NoSync: true, // 禁用 fsync 以提高测试速度
	})
	if err != nil {
		return nil, "", err
	}

	return db, dbPath, nil
}

// setupBfTree 创建 BfTree 测试实例
func setupBfTree(useBitmapLock bool, dataSize int) (*bftree.BfTree, string, error) {
	tmpDir := os.TempDir()
	dataDir := filepath.Join(tmpDir, fmt.Sprintf("bftree_bench_%d", dataSize))
	os.RemoveAll(dataDir) // 清理旧数据

	config := bftree.DefaultConfig()
	config.DataDir = dataDir
	config.EnableWAL = false // 禁用 WAL 以提高测试速度
	config.UseBitmapLock = useBitmapLock

	tree, err := bftree.NewBfTree(config)
	if err != nil {
		return nil, "", err
	}

	return tree, dataDir, nil
}

// cleanup 清理测试资源
func cleanup(dbPath string, dataDir string, boltDB *bbolt.DB, bftree *bftree.BfTree) {
	if boltDB != nil {
		boltDB.Close()
		os.Remove(dbPath)
	}
	if bftree != nil {
		bftree.Close()
		os.RemoveAll(dataDir)
	}
}

// ===========================
// 场景 1: 顺序写入 Set
// ===========================

func BenchmarkBoltDB_Set_Sequential(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	db, dbPath, err := setupBoltDB(config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BoltDB: %v", err)
	}
	defer cleanup(dbPath, "", db, nil)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := i % config.DataSize
		err := db.Update(func(tx *bbolt.Tx) error {
			bucket, err := tx.CreateBucketIfNotExists([]byte("test"))
			if err != nil {
				return err
			}
			return bucket.Put(data[idx][0], data[idx][1])
		})
		if err != nil {
			b.Fatalf("Failed to put: %v", err)
		}
	}
}

func BenchmarkBfTree_P0_Set_Sequential(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000
	config.UseBitmapLock = false

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	tree, dataDir, err := setupBfTree(config.UseBitmapLock, config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BfTree: %v", err)
	}
	defer cleanup("", dataDir, nil, tree)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := i % config.DataSize
		err := tree.Set(b.Context(), data[idx][0], data[idx][1])
		if err != nil {
			b.Fatalf("Failed to set: %v", err)
		}
	}
}

func BenchmarkBfTree_P1_Set_Sequential(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000
	config.UseBitmapLock = true

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	tree, dataDir, err := setupBfTree(config.UseBitmapLock, config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BfTree: %v", err)
	}
	defer cleanup("", dataDir, nil, tree)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := i % config.DataSize
		err := tree.Set(b.Context(), data[idx][0], data[idx][1])
		if err != nil {
			b.Fatalf("Failed to set: %v", err)
		}
	}
}

// ===========================
// 场景 2: 随机写入 Set
// ===========================

func BenchmarkBoltDB_Set_Random(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)
	shuffleData(data)

	db, dbPath, err := setupBoltDB(config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BoltDB: %v", err)
	}
	defer cleanup(dbPath, "", db, nil)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := i % config.DataSize
		err := db.Update(func(tx *bbolt.Tx) error {
			bucket, err := tx.CreateBucketIfNotExists([]byte("test"))
			if err != nil {
				return err
			}
			return bucket.Put(data[idx][0], data[idx][1])
		})
		if err != nil {
			b.Fatalf("Failed to put: %v", err)
		}
	}
}

func BenchmarkBfTree_P0_Set_Random(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000
	config.UseBitmapLock = false

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)
	shuffleData(data)

	tree, dataDir, err := setupBfTree(config.UseBitmapLock, config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BfTree: %v", err)
	}
	defer cleanup("", dataDir, nil, tree)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := i % config.DataSize
		err := tree.Set(b.Context(), data[idx][0], data[idx][1])
		if err != nil {
			b.Fatalf("Failed to set: %v", err)
		}
	}
}

func BenchmarkBfTree_P1_Set_Random(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000
	config.UseBitmapLock = true

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)
	shuffleData(data)

	tree, dataDir, err := setupBfTree(config.UseBitmapLock, config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BfTree: %v", err)
	}
	defer cleanup("", dataDir, nil, tree)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := i % config.DataSize
		err := tree.Set(b.Context(), data[idx][0], data[idx][1])
		if err != nil {
			b.Fatalf("Failed to set: %v", err)
		}
	}
}

// ===========================
// 场景 3: 点查询 Get
// ===========================

func BenchmarkBoltDB_Get(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	db, dbPath, err := setupBoltDB(config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BoltDB: %v", err)
	}
	defer cleanup(dbPath, "", db, nil)

	// 预填充数据
	err = db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("test"))
		if err != nil {
			return err
		}
		for _, kv := range data {
			if err := bucket.Put(kv[0], kv[1]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatalf("Failed to populate data: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := i % config.DataSize
		err := db.View(func(tx *bbolt.Tx) error {
			bucket := tx.Bucket([]byte("test"))
			if bucket == nil {
				return fmt.Errorf("bucket not found")
			}
			val := bucket.Get(data[idx][0])
			if val == nil {
				return fmt.Errorf("key not found")
			}
			return nil
		})
		if err != nil {
			b.Fatalf("Failed to get: %v", err)
		}
	}
}

func BenchmarkBfTree_P0_Get(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000
	config.UseBitmapLock = false

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	tree, dataDir, err := setupBfTree(config.UseBitmapLock, config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BfTree: %v", err)
	}
	defer cleanup("", dataDir, nil, tree)

	// 预填充数据
	for _, kv := range data {
		if err := tree.Set(b.Context(), kv[0], kv[1]); err != nil {
			b.Fatalf("Failed to populate data: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := i % config.DataSize
		_, err := tree.Get(b.Context(), data[idx][0])
		if err != nil {
			b.Fatalf("Failed to get: %v", err)
		}
	}
}

func BenchmarkBfTree_P1_Get(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000
	config.UseBitmapLock = true

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	tree, dataDir, err := setupBfTree(config.UseBitmapLock, config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BfTree: %v", err)
	}
	defer cleanup("", dataDir, nil, tree)

	// 预填充数据
	for _, kv := range data {
		if err := tree.Set(b.Context(), kv[0], kv[1]); err != nil {
			b.Fatalf("Failed to populate data: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := i % config.DataSize
		_, err := tree.Get(b.Context(), data[idx][0])
		if err != nil {
			b.Fatalf("Failed to get: %v", err)
		}
	}
}

// ===========================
// 场景 5: 并发读
// ===========================

func BenchmarkBoltDB_ConcurrentReads(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	db, dbPath, err := setupBoltDB(config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BoltDB: %v", err)
	}
	defer cleanup(dbPath, "", db, nil)

	// 预填充数据
	err = db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("test"))
		if err != nil {
			return err
		}
		for _, kv := range data {
			if err := bucket.Put(kv[0], kv[1]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatalf("Failed to populate data: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			idx := i % config.DataSize
			err := db.View(func(tx *bbolt.Tx) error {
				bucket := tx.Bucket([]byte("test"))
				if bucket == nil {
					return fmt.Errorf("bucket not found")
				}
				val := bucket.Get(data[idx][0])
				if val == nil {
					return fmt.Errorf("key not found")
				}
				return nil
			})
			if err != nil {
				b.Errorf("Failed to get: %v", err)
			}
			i++
		}
	})
}

func BenchmarkBfTree_P0_ConcurrentReads(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000
	config.UseBitmapLock = false

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	tree, dataDir, err := setupBfTree(config.UseBitmapLock, config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BfTree: %v", err)
	}
	defer cleanup("", dataDir, nil, tree)

	// 预填充数据
	for _, kv := range data {
		if err := tree.Set(b.Context(), kv[0], kv[1]); err != nil {
			b.Fatalf("Failed to populate data: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			idx := i % config.DataSize
			_, err := tree.Get(b.Context(), data[idx][0])
			if err != nil {
				b.Errorf("Failed to get: %v", err)
			}
			i++
		}
	})
}

func BenchmarkBfTree_P1_ConcurrentReads(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000
	config.UseBitmapLock = true

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	tree, dataDir, err := setupBfTree(config.UseBitmapLock, config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BfTree: %v", err)
	}
	defer cleanup("", dataDir, nil, tree)

	// 预填充数据
	for _, kv := range data {
		if err := tree.Set(b.Context(), kv[0], kv[1]); err != nil {
			b.Fatalf("Failed to populate data: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			idx := i % config.DataSize
			_, err := tree.Get(b.Context(), data[idx][0])
			if err != nil {
				b.Errorf("Failed to get: %v", err)
			}
			i++
		}
	})
}

// ===========================
// 场景 6: 并发写
// ===========================

func BenchmarkBoltDB_ConcurrentWrites(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	db, dbPath, err := setupBoltDB(config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BoltDB: %v", err)
	}
	defer cleanup(dbPath, "", db, nil)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			idx := i % config.DataSize
			err := db.Update(func(tx *bbolt.Tx) error {
				bucket, err := tx.CreateBucketIfNotExists([]byte("test"))
				if err != nil {
					return err
				}
				return bucket.Put(data[idx][0], data[idx][1])
			})
			if err != nil {
				b.Errorf("Failed to put: %v", err)
			}
			i++
		}
	})
}

func BenchmarkBfTree_P0_ConcurrentWrites(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000
	config.UseBitmapLock = false

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	tree, dataDir, err := setupBfTree(config.UseBitmapLock, config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BfTree: %v", err)
	}
	defer cleanup("", dataDir, nil, tree)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			idx := i % config.DataSize
			err := tree.Set(b.Context(), data[idx][0], data[idx][1])
			if err != nil {
				b.Errorf("Failed to set: %v", err)
			}
			i++
		}
	})
}

func BenchmarkBfTree_P1_ConcurrentWrites(b *testing.B) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000
	config.UseBitmapLock = true

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	tree, dataDir, err := setupBfTree(config.UseBitmapLock, config.DataSize)
	if err != nil {
		b.Fatalf("Failed to setup BfTree: %v", err)
	}
	defer cleanup("", dataDir, nil, tree)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			idx := i % config.DataSize
			err := tree.Set(b.Context(), data[idx][0], data[idx][1])
			if err != nil {
				b.Errorf("Failed to set: %v", err)
			}
			i++
		}
	})
}

// ===========================
// 工具函数
// ===========================

// shuffleData 打乱数据顺序
func shuffleData(data [][2][]byte) {
	for i := len(data) - 1; i > 0; i-- {
		j := i % (len(data) / 2)
		data[i], data[j] = data[j], data[i]
	}
}

// BenchmarkMixed 读写混合测试
func BenchmarkMixed_ReadWrite(b *testing.B) {
	tests := []struct {
		name       string
		dbType     string
		readRatio  float64
		numGorcs   int
		useBMPLock bool
	}{
		{"BoltDB_70Read_30Write", "boltdb", 0.7, 10, false},
		{"BfTree_P0_70Read_30Write", "bftree", 0.7, 10, false},
		{"BfTree_P1_70Read_30Write", "bftree", 0.7, 10, true},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			benchmarkMixed(b, tt.dbType, tt.readRatio, tt.numGorcs, tt.useBMPLock)
		})
	}
}

func benchmarkMixed(b *testing.B, dbType string, readRatio float64, numGorcs int, useBMPLock bool) {
	config := DefaultBenchmarkConfig()
	config.DataSize = 10000

	data := generateTestData(config.DataSize, config.KeySize, config.ValueSize)

	var db interface{}
	var closeFunc func()

	if dbType == "boltdb" {
		boltDB, dbPath, err := setupBoltDB(config.DataSize)
		if err != nil {
			b.Fatalf("Failed to setup BoltDB: %v", err)
		}
		db = boltDB
		closeFunc = func() {
			cleanup(dbPath, "", boltDB, nil)
		}
	} else {
		tree, dataDir, err := setupBfTree(useBMPLock, config.DataSize)
		if err != nil {
			b.Fatalf("Failed to setup BfTree: %v", err)
		}
		db = tree
		closeFunc = func() {
			cleanup("", dataDir, nil, tree)
		}
	}
	defer closeFunc()

	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	for g := 0; g < numGorcs; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < b.N/numGorcs; i++ {
				idx := (goroutineID*1000 + i) % config.DataSize
				if float64(i%100)/100 < readRatio {
					// 读操作
					if dbType == "boltdb" {
						boltDB := db.(*bbolt.DB)
						_ = boltDB.View(func(tx *bbolt.Tx) error {
							bucket := tx.Bucket([]byte("test"))
							if bucket != nil {
								_ = bucket.Get(data[idx][0])
							}
							return nil
						})
					} else {
						tree := db.(*bftree.BfTree)
						_, _ = tree.Get(b.Context(), data[idx][0])
					}
				} else {
					// 写操作
					if dbType == "boltdb" {
						boltDB := db.(*bbolt.DB)
						_ = boltDB.Update(func(tx *bbolt.Tx) error {
							bucket, _ := tx.CreateBucketIfNotExists([]byte("test"))
							return bucket.Put(data[idx][0], data[idx][1])
						})
					} else {
						tree := db.(*bftree.BfTree)
						_ = tree.Set(b.Context(), data[idx][0], data[idx][1])
					}
				}
			}
		}(g)
	}
	wg.Wait()
}
