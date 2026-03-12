package btree

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// ChunkManager 管理 Append-Only 文件
// 支持多个 Chunk 文件的创建、分配和写入
type ChunkManager struct {
	// 基础配置
	chunkSize int    // 每个 Chunk 大小 (256MB)
	pageSize  int    // 页面大小 (4KB)
	dataDir   string // 数据目录

	// 文件管理
	activeChunks   []*Chunk     // 活跃的 Chunk（可写入）
	archivedChunks []*Chunk     // 已归档的 Chunk（只读）
	currentChunk   *Chunk       // 当前写入的 Chunk
	currentChunkID atomic.Int64 // 当前 Chunk ID

	// 并发控制
	mu sync.RWMutex // 保护 Chunk 列表

	// 内存池
	pagePool sync.Pool // *[]byte 复用
}

// NewChunkManager 创建新的 ChunkManager
func NewChunkManager(dataDir string) (*ChunkManager, error) {
	// 确保数据目录存在
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory %s: %w", dataDir, err)
	}

	cm := &ChunkManager{
		chunkSize:      ChunkSize,
		pageSize:       PageSize,
		dataDir:        dataDir,
		activeChunks:   make([]*Chunk, 0, 8),
		archivedChunks: make([]*Chunk, 0, 8),
	}

	// 初始化内存池
	cm.pagePool.New = func() interface{} {
		buf := make([]byte, PageSize)
		return &buf
	}

	// 加载现有的 Chunk 文件
	if err := cm.loadExistingChunks(); err != nil {
		return nil, fmt.Errorf("failed to load existing chunks: %w", err)
	}

	return cm, nil
}

// loadExistingChunks 加载现有的 Chunk 文件
func (cm *ChunkManager) loadExistingChunks() error {
	entries, err := os.ReadDir(cm.dataDir)
	if err != nil {
		return fmt.Errorf("failed to read data directory: %w", err)
	}

	maxID := int64(-1)

	for _, entry := range entries {
		// 解析文件名：btree_XXXX.ao
		var id int
		_, err := fmt.Sscanf(entry.Name(), "btree_%d.ao", &id)
		if err != nil {
			continue // 跳过非 Chunk 文件
		}

		filePath := filepath.Join(cm.dataDir, entry.Name())
		chunk, err := NewChunk(id, filePath, true) // 只读模式
		if err != nil {
			return fmt.Errorf("failed to open chunk %d: %w", id, err)
		}

		cm.mu.Lock()
		cm.archivedChunks = append(cm.archivedChunks, chunk)
		cm.mu.Unlock()

		if int64(id) > maxID {
			maxID = int64(id)
		}
	}

	// 设置当前 Chunk ID
	cm.currentChunkID.Store(maxID + 1)

	return nil
}

// AllocatePage 分配新页面（追加写入）
// 返回：64 位位置编码（ChunkID + Offset + PageType）
func (cm *ChunkManager) AllocatePage(pageType int) (int64, error) {
	// 确保有可写入的 Chunk
	if err := cm.ensureCurrentChunk(); err != nil {
		return 0, fmt.Errorf("failed to ensure current chunk: %w", err)
	}

	// 从当前 Chunk 分配页面
	chunk := cm.getCurrentChunk()
	pos, err := chunk.AllocatePage()
	if err != nil {
		// Chunk 已满，创建新的 Chunk
		if err := cm.rotateChunk(); err != nil {
			return 0, fmt.Errorf("failed to rotate chunk: %w", err)
		}

		// 从新的 Chunk 分配
		chunk = cm.getCurrentChunk()
		pos, err = chunk.AllocatePage()
		if err != nil {
			return 0, fmt.Errorf("failed to allocate page from new chunk: %w", err)
		}
	}

	// 编码位置
	chunkID := chunk.GetID()
	encodedPos, err := EncodePagePos(chunkID, int(pos), pageType)
	if err != nil {
		return 0, fmt.Errorf("failed to encode page position: %w", err)
	}

	return encodedPos, nil
}

// WritePage 写入页面到指定位置
func (cm *ChunkManager) WritePage(pos int64, data []byte) error {
	chunkID := GetChunkID(pos)
	offset := GetOffset(pos)

	chunk := cm.getChunkByID(int(chunkID))
	if chunk == nil {
		return fmt.Errorf("chunk %d not found", chunkID)
	}

	return chunk.WritePage(int64(offset), data)
}

// ReadPage 读取页面（返回原始字节数据）
func (cm *ChunkManager) ReadPage(pos int64) ([]byte, error) {
	chunkID := GetChunkID(pos)
	offset := GetOffset(pos)

	chunk := cm.getChunkByID(int(chunkID))
	if chunk == nil {
		return nil, fmt.Errorf("chunk %d not found", chunkID)
	}

	return chunk.ReadPage(int64(offset))
}

// LoadPage 加载并反序列化页面（懒加载核心）
// 从 Chunk 文件读取页面数据并反序列化为具体的 Page 类型（LeafPage 或 InternalPage）
//
// 参数：
//
//	pos - 64 位位置编码（包含 ChunkID、Offset、PageType）
//
// 返回：
//
//	interface{} - 反序列化后的页面对象（实际类型为 *LeafPage 或 *InternalPage）
//	error - 错误信息
//
// 懒加载流程：
// 1. 解码 pos，获取 ChunkID、Offset、PageType
// 2. 从对应的 Chunk 读取原始字节数据
// 3. 根据 PageType 反序列化为具体类型
// 4. 返回具体类型（需要类型断言使用）
func (cm *ChunkManager) LoadPage(pos int64) (interface{}, error) {
	// 1. 解码位置信息
	chunkID, offset, pageType := DecodePagePos(pos)

	// 2. 查找 Chunk
	chunk := cm.getChunkByID(int(chunkID))
	if chunk == nil {
		return nil, fmt.Errorf("chunk %d not found", chunkID)
	}

	// 3. 读取原始字节数据
	data, err := chunk.ReadPage(int64(offset))
	if err != nil {
		return nil, fmt.Errorf("read page from chunk %d at offset %d: %w", chunkID, offset, err)
	}

	// 4. 根据 PageType 反序列化
	switch pageType {
	case PageTypeLeaf:
		leafPage, err := DeserializeLeafPage(data)
		if err != nil {
			return nil, fmt.Errorf("deserialize leaf page: %w", err)
		}
		return leafPage, nil

	case PageTypeInternal:
		internalPage, err := DeserializeInternalPage(data)
		if err != nil {
			return nil, fmt.Errorf("deserialize internal page: %w", err)
		}
		return internalPage, nil

	default:
		return nil, fmt.Errorf("unknown page type: %d", pageType)
	}
}

// ensureCurrentChunk 确保有可写入的 Chunk
func (cm *ChunkManager) ensureCurrentChunk() error {
	chunk := cm.getCurrentChunk()
	if chunk != nil && !chunk.IsFull() {
		return nil
	}

	// 创建新的 Chunk
	return cm.rotateChunk()
}

// rotateChunk 轮换到新的 Chunk
func (cm *ChunkManager) rotateChunk() error {
	// 将当前 Chunk 标记为只读并归档
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.currentChunk != nil {
		cm.currentChunk.MarkReadOnly()
		cm.archivedChunks = append(cm.archivedChunks, cm.currentChunk)
		cm.currentChunk = nil
	}

	// 创建新的 Chunk
	newID := int(cm.currentChunkID.Load())
	filePath := filepath.Join(cm.dataDir, fmt.Sprintf("btree_%04d.ao", newID))

	chunk, err := NewChunk(newID, filePath, false)
	if err != nil {
		return fmt.Errorf("failed to create new chunk %d: %w", newID, err)
	}

	cm.currentChunk = chunk
	cm.activeChunks = append(cm.activeChunks, chunk)
	cm.currentChunkID.Add(1)

	return nil
}

// getCurrentChunk 获取当前可写入的 Chunk
func (cm *ChunkManager) getCurrentChunk() *Chunk {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.currentChunk
}

// getChunkByID 根据 ID 获取 Chunk
func (cm *ChunkManager) getChunkByID(id int) *Chunk {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// 先查找活跃 Chunk
	for _, chunk := range cm.activeChunks {
		if chunk.GetID() == id {
			return chunk
		}
	}

	// 再查找已归档 Chunk
	for _, chunk := range cm.archivedChunks {
		if chunk.GetID() == id {
			return chunk
		}
	}

	return nil
}

// AcquirePageBuffer 从内存池获取页面缓冲区
func (cm *ChunkManager) AcquirePageBuffer() *[]byte {
	return cm.pagePool.Get().(*[]byte)
}

// ReleasePageBuffer 归还页面缓冲区到内存池
func (cm *ChunkManager) ReleasePageBuffer(buf *[]byte) {
	cm.pagePool.Put(buf)
}

// Sync 同步所有 Chunk 到磁盘
func (cm *ChunkManager) Sync() error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// 同步活跃 Chunk
	for _, chunk := range cm.activeChunks {
		if err := chunk.Sync(); err != nil {
			return fmt.Errorf("failed to sync chunk %d: %w", chunk.GetID(), err)
		}
	}

	return nil
}

// Close 关闭所有 Chunk
func (cm *ChunkManager) Close() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	var lastErr error

	// 关闭活跃 Chunk
	for _, chunk := range cm.activeChunks {
		if err := chunk.Close(); err != nil {
			lastErr = err
		}
	}

	// 关闭已归档 Chunk
	for _, chunk := range cm.archivedChunks {
		if err := chunk.Close(); err != nil {
			lastErr = err
		}
	}

	cm.activeChunks = nil
	cm.archivedChunks = nil
	cm.currentChunk = nil

	return lastErr
}

// GetStats 获取 ChunkManager 统计信息
func (cm *ChunkManager) GetStats() ChunkManagerStats {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	stats := ChunkManagerStats{
		ActiveChunkCount:   len(cm.activeChunks),
		ArchivedChunkCount: len(cm.archivedChunks),
		CurrentChunkID:     int(cm.currentChunkID.Load()),
	}

	if cm.currentChunk != nil {
		stats.CurrentChunkWritePos = cm.currentChunk.GetWritePos()
		stats.CurrentChunkPageCount = cm.currentChunk.GetPageCount()
	}

	return stats
}

// ChunkManagerStats ChunkManager 统计信息
type ChunkManagerStats struct {
	ActiveChunkCount      int   // 活跃 Chunk 数量
	ArchivedChunkCount    int   // 已归档 Chunk 数量
	CurrentChunkID        int   // 当前 Chunk ID
	CurrentChunkWritePos  int64 // 当前 Chunk 写入位置
	CurrentChunkPageCount int64 // 当前 Chunk 页面数量
}
