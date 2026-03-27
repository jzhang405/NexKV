package btree

import (
	"os"
	"sync"
	"sync/atomic"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

const (
	// ChunkSize Chunk 文件大小 (256MB)
	ChunkSize = 256 * 1024 * 1024

	// ChunkPagesPerChunk 每个 Chunk 包含的页面数量
	ChunkPagesPerChunk = ChunkSize / PageSize // 65536 pages
)

// Chunk Append-Only 文件
// 文件命名：btree_0000.ao, btree_0001.ao, ...
// 每个Chunk固定256MB，支持4KB固定页面
type Chunk struct {
	id         int          // Chunk ID
	file       *os.File     // 文件句柄
	writePos   int64        // 当前写入位置（字节）
	pageCount  atomic.Int64 // 页面计数
	pageIndex  atomic.Int64 // 页面索引（用于快速查找）
	isReadOnly bool         // 是否只读
	mu         sync.Mutex   // 保护文件操作
}

// NewChunk 创建新的 Chunk
func NewChunk(id int, filePath string, readOnly bool) (*Chunk, error) {
	flags := os.O_RDWR | os.O_CREATE
	if readOnly {
		flags = os.O_RDONLY
	}

	file, err := os.OpenFile(filePath, flags, 0644)
	if err != nil {
		return nil, errpkg.BTreeOpenChunkFile(filePath, err)
	}

	// 如果不是只读，定位到文件末尾
	writePos := int64(0)
	if !readOnly {
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, errpkg.BTreeStatChunkFile(filePath, err)
		}
		writePos = info.Size()
	}

	chunk := &Chunk{
		id:         id,
		file:       file,
		writePos:   writePos,
		isReadOnly: readOnly,
	}

	// 计算页面数量
	chunk.pageCount.Store(writePos / PageSize)
	chunk.pageIndex.Store(writePos / PageSize)

	return chunk, nil
}

// AllocatePage 分配新页面（追加写入）
// 返回：页面位置（在 Chunk 中的字节偏移）
func (c *Chunk) AllocatePage() (int64, error) {
	if c.isReadOnly {
		return 0, errpkg.BTreeChunkReadOnly(c.id)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 计算新页面位置
	pos := c.writePos

	// 检查是否超出 Chunk 大小
	chunkSize := int64(ChunkSize)
	if pos+PageSize > chunkSize {
		return 0, errpkg.BTreeChunkFull(c.id, chunkSize, pos)
	}

	// 更新写入位置
	c.writePos += PageSize
	c.pageCount.Add(1)
	c.pageIndex.Add(1)

	return pos, nil
}

// WritePage 写入页面到指定位置
func (c *Chunk) WritePage(pos int64, data []byte) error {
	if c.isReadOnly {
		return errpkg.BTreeChunkReadOnly(c.id)
	}

	if len(data) != PageSize {
		return errpkg.BTreeChunkPageSizeMismatch(PageSize, len(data))
	}

	// 边界检查
	if pos < 0 || pos+PageSize > int64(ChunkSize) {
		return errpkg.BTreeChunkPositionOutOfRange(pos, ChunkSize)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 写入文件
	_, err := c.file.WriteAt(data, pos)
	if err != nil {
		return errpkg.BTreeWritePageAt(pos, err)
	}

	return nil
}

// ReadPage 读取页面
func (c *Chunk) ReadPage(pos int64) ([]byte, error) {
	// 边界检查
	if pos < 0 || pos+PageSize > int64(ChunkSize) {
		return nil, errpkg.BTreeChunkPositionOutOfRange(pos, ChunkSize)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 读取页面
	data := make([]byte, PageSize)
	_, err := c.file.ReadAt(data, pos)
	if err != nil {
		return nil, errpkg.BTreeReadPageAt(pos, err)
	}

	return data, nil
}

// GetPageCount 获取页面数量
func (c *Chunk) GetPageCount() int64 {
	return c.pageCount.Load()
}

// GetPageIndex 获取页面索引（页面序号）
func (c *Chunk) GetPageIndex() int64 {
	return c.pageIndex.Load()
}

// GetWritePos 获取当前写入位置
func (c *Chunk) GetWritePos() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writePos
}

// IsFull 判断 Chunk 是否已满
func (c *Chunk) IsFull() bool {
	return c.GetWritePos()+PageSize > int64(ChunkSize)
}

// Sync 同步文件到磁盘
func (c *Chunk) Sync() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.file.Sync()
}

// Close 关闭 Chunk
func (c *Chunk) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.file.Close()
}

// GetID 获取 Chunk ID
func (c *Chunk) GetID() int {
	return c.id
}

// IsReadOnly 判断是否只读
func (c *Chunk) IsReadOnly() bool {
	return c.isReadOnly
}

// MarkReadOnly 标记为只读
func (c *Chunk) MarkReadOnly() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isReadOnly = true
}
