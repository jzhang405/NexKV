package btree

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewChunk(t *testing.T) {
	// 使用临时目录
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "btree_0000.ao")

	// 创建新 Chunk
	chunk, err := NewChunk(0, filePath, false)
	require.NoError(t, err)
	assert.NotNil(t, chunk)
	assert.Equal(t, 0, chunk.GetID())
	assert.Equal(t, int64(0), chunk.GetWritePos())
	assert.Equal(t, int64(0), chunk.GetPageCount())
	assert.False(t, chunk.IsReadOnly())
}

func TestChunk_AllocatePage(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "btree_0000.ao")

	chunk, err := NewChunk(0, filePath, false)
	require.NoError(t, err)

	// 分配第一个页面
	pos1, err := chunk.AllocatePage()
	require.NoError(t, err)
	assert.Equal(t, int64(0), pos1)
	assert.Equal(t, int64(1), chunk.GetPageCount())

	// 分配第二个页面
	pos2, err := chunk.AllocatePage()
	require.NoError(t, err)
	assert.Equal(t, int64(PageSize), pos2)
	assert.Equal(t, int64(2), chunk.GetPageCount())

	// 验证页面顺序
	assert.Equal(t, PageSize, int(pos2-pos1))
}

func TestChunk_WriteReadPage(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "btree_0000.ao")

	chunk, err := NewChunk(0, filePath, false)
	require.NoError(t, err)

	// 写入页面
	data := []byte("test page data")
	padding := make([]byte, PageSize-len(data))
	pageData := append(data, padding...)

	err = chunk.WritePage(0, pageData)
	require.NoError(t, err)

	// 读取页面
	readData, err := chunk.ReadPage(0)
	require.NoError(t, err)
	assert.Equal(t, pageData, readData)
}

func TestChunk_WritePageInvalidSize(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "btree_0000.ao")

	chunk, err := NewChunk(0, filePath, false)
	require.NoError(t, err)

	// 错误的页面大小
	invalidData := []byte("too small")

	err = chunk.WritePage(0, invalidData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "page size must be")
}

func TestChunk_IsFull(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "btree_0000.ao")

	chunk, err := NewChunk(0, filePath, false)
	require.NoError(t, err)

	// 初始状态不满
	assert.False(t, chunk.IsFull())

	// 分配页面直到接近满
	for i := 0; i < ChunkPagesPerChunk-1; i++ {
		_, err := chunk.AllocatePage()
		require.NoError(t, err)
	}

	// 仍然不满
	assert.False(t, chunk.IsFull())

	// 分配最后一个页面
	_, err = chunk.AllocatePage()
	require.NoError(t, err)

	// 现在满了
	assert.True(t, chunk.IsFull())
}

func TestChunk_ReadOnly(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "btree_0000.ao")

	// 先创建文件
	file, err := os.Create(filePath)
	require.NoError(t, err)
	file.Close()

	// 创建只读 Chunk
	chunk, err := NewChunk(0, filePath, true)
	require.NoError(t, err)
	assert.True(t, chunk.IsReadOnly())

	// 只读 Chunk 不能分配页面
	_, err = chunk.AllocatePage()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read-only")

	// 只读 Chunk 不能写入
	data := make([]byte, PageSize)
	err = chunk.WritePage(0, data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read-only")
}

func TestNewChunkManager(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建新的 ChunkManager
	cm, err := NewChunkManager(tmpDir)
	require.NoError(t, err)
	assert.NotNil(t, cm)
	assert.NotNil(t, cm.activeChunks)
	assert.NotNil(t, cm.archivedChunks)

	// 验证数据目录已创建
	info, err := os.Stat(tmpDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestChunkManager_AllocatePage(t *testing.T) {
	tmpDir := t.TempDir()

	cm, err := NewChunkManager(tmpDir)
	require.NoError(t, err)

	// 分配页面
	pos1, err := cm.AllocatePage(1) // PageType = 1
	require.NoError(t, err)
	assert.NotZero(t, pos1)

	// 解码位置验证
	chunkID, offset, pageType := DecodePagePos(pos1)
	assert.Equal(t, 0, chunkID)  // 第一个 Chunk
	assert.Equal(t, 0, offset)   // 第一个页面
	assert.Equal(t, 1, pageType) // 页面类型

	// 分配第二个页面
	pos2, err := cm.AllocatePage(2)
	require.NoError(t, err)
	assert.NotZero(t, pos2)

	// 验证位置递增
	assert.Greater(t, pos2, pos1)
}

func TestChunkManager_WriteReadPage(t *testing.T) {
	tmpDir := t.TempDir()

	cm, err := NewChunkManager(tmpDir)
	require.NoError(t, err)

	// 分配页面
	pos, err := cm.AllocatePage(1)
	require.NoError(t, err)

	// 准备页面数据
	data := make([]byte, PageSize)
	copy(data, "test page content")

	// 写入页面
	err = cm.WritePage(pos, data)
	require.NoError(t, err)

	// 读取页面
	readData, err := cm.ReadPage(pos)
	require.NoError(t, err)
	assert.Equal(t, data, readData)
}

func TestChunkManager_AutoRotate(t *testing.T) {
	tmpDir := t.TempDir()

	cm, err := NewChunkManager(tmpDir)
	require.NoError(t, err)

	// 分配大量页面以触发 Chunk 轮换
	// 每个 Chunk 有 ChunkPagesPerChunk 个页面
	pagesToAllocate := ChunkPagesPerChunk + 1

	for i := 0; i < pagesToAllocate; i++ {
		_, err := cm.AllocatePage(1)
		require.NoError(t, err, "Allocation %d should succeed", i)
	}

	// 验证创建了至少 2 个 Chunk
	stats := cm.GetStats()
	assert.GreaterOrEqual(t, stats.ActiveChunkCount, 2)
}

func TestChunkManager_LoadExistingChunks(t *testing.T) {
	tmpDir := t.TempDir()

	// 手动创建 Chunk 文件
	filePath := filepath.Join(tmpDir, "btree_0000.ao")
	file, err := os.Create(filePath)
	require.NoError(t, err)

	// 写入一些数据
	data := make([]byte, PageSize)
	copy(data, "existing page")
	_, err = file.Write(data)
	require.NoError(t, err)

	file.Close()

	// 创建 ChunkManager，应该自动加载现有 Chunk
	cm, err := NewChunkManager(tmpDir)
	require.NoError(t, err)

	// 验证现有 Chunk 被加载
	stats := cm.GetStats()
	assert.Equal(t, 1, stats.ArchivedChunkCount)
}

func TestChunkManager_Sync(t *testing.T) {
	tmpDir := t.TempDir()

	cm, err := NewChunkManager(tmpDir)
	require.NoError(t, err)

	// 分配并写入页面
	pos, err := cm.AllocatePage(1)
	require.NoError(t, err)

	data := make([]byte, PageSize)
	copy(data, "sync test data")

	err = cm.WritePage(pos, data)
	require.NoError(t, err)

	// 同步
	err = cm.Sync()
	require.NoError(t, err)
}

func TestChunkManager_Close(t *testing.T) {
	tmpDir := t.TempDir()

	cm, err := NewChunkManager(tmpDir)
	require.NoError(t, err)

	// 分配一些页面
	for i := 0; i < 10; i++ {
		_, err := cm.AllocatePage(1)
		require.NoError(t, err)
	}

	// 关闭 ChunkManager
	err = cm.Close()
	require.NoError(t, err)

	// 关闭后再操作应该失败
	stats := cm.GetStats()
	assert.Equal(t, 0, stats.ActiveChunkCount)
	assert.Equal(t, 0, stats.ArchivedChunkCount)
}

func TestChunkManager_GetStats(t *testing.T) {
	tmpDir := t.TempDir()

	cm, err := NewChunkManager(tmpDir)
	require.NoError(t, err)

	// 分配一些页面
	for i := 0; i < 10; i++ {
		_, err := cm.AllocatePage(1)
		require.NoError(t, err)
	}

	// 获取统计信息
	stats := cm.GetStats()
	assert.Equal(t, 1, stats.ActiveChunkCount)
	assert.Equal(t, 0, stats.ArchivedChunkCount)
	assert.Equal(t, 1, stats.CurrentChunkID) // 从 ID 1 开始
	assert.Equal(t, int64(PageSize*10), stats.CurrentChunkWritePos)
	assert.Equal(t, int64(10), stats.CurrentChunkPageCount)
}

func TestChunkManager_PageBufferPool(t *testing.T) {
	tmpDir := t.TempDir()

	cm, err := NewChunkManager(tmpDir)
	require.NoError(t, err)

	// 获取缓冲区
	buf1 := cm.AcquirePageBuffer()
	assert.NotNil(t, buf1)
	assert.Equal(t, PageSize, cap(*buf1))

	// 归还缓冲区
	cm.ReleasePageBuffer(buf1)

	// 再次获取，应该是有效的缓冲区
	buf2 := cm.AcquirePageBuffer()
	assert.NotNil(t, buf2)
	assert.Equal(t, PageSize, cap(*buf2))
	// 注意：sync.Pool 不保证返回同一个对象，只保证返回有效对象
}

// Benchmark Chunk 操作性能
func BenchmarkChunk_AllocatePage(b *testing.B) {
	chunk := &Chunk{
		id:       0,
		writePos: 0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 模拟分配
		pos := chunk.writePos
		chunk.writePos += PageSize
		_ = pos
	}
}

func BenchmarkChunkManager_AllocatePage(b *testing.B) {
	tmpDir := b.TempDir()

	cm, err := NewChunkManager(tmpDir)
	require.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := cm.AllocatePage(1)
		if err != nil {
			b.Fatal(err)
		}
	}
}
