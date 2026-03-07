// Package bftree 提供 BitmapLock 细粒度锁实现
//
// BitmapLock 使用位图来表示页面锁状态，支持读写锁和分片策略，
// 相比全局 RWMutex 可以显著减少锁竞争。
package bftree

import (
	"sync"
	"sync/atomic"
)

const (
	// Bits per atomic pointer (64-bit)
	bitsPerWord = 64
)

// lockShard 是一个锁分片，包含多个页面的锁状态
type lockShard struct {
	// bitmap 使用位图表示 64 个页面的锁状态
	// 每个 pageID % 64 对应一个 bit
	bitmap atomic.Uint64

	// readers 记录每个页面的读锁计数
	// 使用数组存储，每个页面一个 uint32
	readers [bitsPerWord]atomic.Uint32

	// writer 标记是否有写锁
	writer atomic.Uint32

	// mutex 保护 shard 内部操作
	mu sync.Mutex

	// cond 用于条件等待（避免 busy waiting）
	cond *sync.Cond
}

// BitmapLock 细粒度锁实现
//
// 设计目标：
// - 支持分片策略（默认 16 分片），减少竞争
// - 读写锁语义
// - TryLock 支持（非阻塞）
// - 低开销的 Lock/Unlock 操作
type BitmapLock struct {
	shards    []*lockShard
	shardMask uint64
	shardCount int
}

// NewBitmapLock 创建新的 BitmapLock
//
// shardCount 必须是 2 的幂（1, 2, 4, 8, 16, 32, 64）
// 推荐值：16（默认配置）
func NewBitmapLock(shardCount int) *BitmapLock {
	// 验证 shardCount 是 2 的幂
	if shardCount < 1 || shardCount > 64 {
		panic("shard count must be between 1 and 64")
	}
	if (shardCount & (shardCount - 1)) != 0 {
		panic("shard count must be power of 2")
	}

	bl := &BitmapLock{
		shards:    make([]*lockShard, shardCount),
		shardMask: uint64(shardCount - 1),
		shardCount: shardCount,
	}

	// 初始化分片
	for i := range bl.shards {
		shard := &lockShard{}
		shard.cond = sync.NewCond(&shard.mu)  // 初始化条件变量
		bl.shards[i] = shard
	}

	return bl
}

// getShard 根据 pageID 获取对应的分片
func (bl *BitmapLock) getShard(pageID uint64) *lockShard {
	// 使用 pageID 的低位进行分片
	shardIndex := int(pageID & bl.shardMask)
	return bl.shards[shardIndex]
}

// getBitInShard 获取页面在分片中的 bit 位置
func (bl *BitmapLock) getBitInShard(pageID uint64) uint {
	// 统一使用 pageID % bitsPerWord 作为 bit 位置
	// 这样与 readers 数组的索引保持一致
	return uint(pageID % bitsPerWord)
}

// Lock 获取写锁（阻塞）
//
// 使用写锁时，页面将被完全锁定，不允许其他读写操作
// Lock 获取写锁（阻塞）
//
// 使用写锁时，页面将被完全锁定，不允许其他读写操作
// 使用 sync.Cond 避免忙等待，减少 CPU 占用
func (bl *BitmapLock) Lock(pageID uint64) {
	shard := bl.getShard(pageID)
	bit := bl.getBitInShard(pageID)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	// 等待读锁释放（使用 cond.Wait 阻塞等待，不占用 CPU）
	for shard.readers[bit].Load() != 0 {
		shard.cond.Wait()  // 自动释放/获取 mu，等待 Broadcast 信号
	}

	// 设置写锁标志
	shard.writer.Store(1)

	// 更新 bitmap
	oldBitmap := shard.bitmap.Load()
	newBitmap := oldBitmap | (1 << bit)
	shard.bitmap.Store(newBitmap)
}

// Unlock 释放写锁
//
// 释放写锁并唤醒等待的读锁
func (bl *BitmapLock) Unlock(pageID uint64) {
	shard := bl.getShard(pageID)
	bit := bl.getBitInShard(pageID)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	// 清除写锁标志
	shard.writer.Store(0)

	// 更新 bitmap
	oldBitmap := shard.bitmap.Load()
	newBitmap := oldBitmap &^ (1 << bit)
	shard.bitmap.Store(newBitmap)

	// 唤醒等待的读锁
	shard.cond.Broadcast()
}

// RLock 获取读锁（阻塞）
//
// 多个读锁可以同时持有，但与写锁互斥
// 使用 sync.Cond 避免忙等待
func (bl *BitmapLock) RLock(pageID uint64) {
	shard := bl.getShard(pageID)
	bit := bl.getBitInShard(pageID)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	// 等待写锁释放（使用 cond.Wait 阻塞等待）
	for shard.writer.Load() != 0 {
		shard.cond.Wait()  // 自动释放/获取 mu，等待 Broadcast 信号
	}

	// 增加读锁计数
	shard.readers[bit].Add(1)

	// 更新 bitmap（标记为已读）
	oldBitmap := shard.bitmap.Load()
	newBitmap := oldBitmap | (1 << bit)
	shard.bitmap.Store(newBitmap)
}

// RUnlock 释放读锁
//
// 释放读锁，如果是最后一个读锁，唤醒等待的写锁
func (bl *BitmapLock) RUnlock(pageID uint64) {
	shard := bl.getShard(pageID)
	bit := bl.getBitInShard(pageID)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	// 减少读锁计数
	newCount := shard.readers[bit].Add(^uint32(0)) // -1

	// 如果是最后一个读锁，清除 bitmap 标记并唤醒等待的写锁
	if newCount == 0 {
		oldBitmap := shard.bitmap.Load()
		newBitmap := oldBitmap &^ (1 << bit)
		shard.bitmap.Store(newBitmap)

		// 唤醒等待的写锁
		shard.cond.Broadcast()
	}
}

// TryLock 尝试获取写锁（非阻塞）
//
// 返回 true 表示成功获取锁，false 表示锁已被占用
func (bl *BitmapLock) TryLock(pageID uint64) bool {
	shard := bl.getShard(pageID)

	if !shard.mu.TryLock() {
		return false
	}
	defer shard.mu.Unlock()

	// 检查是否有读锁或写锁
	bit := bl.getBitInShard(pageID)
	if shard.readers[bit].Load() > 0 {
		return false
	}
	if shard.writer.Load() != 0 {
		return false
	}

	// 设置写锁
	shard.writer.Store(1)

	// 更新 bitmap
	oldBitmap := shard.bitmap.Load()
	newBitmap := oldBitmap | (1 << bit)
	shard.bitmap.Store(newBitmap)

	return true
}

// TryRLock 尝试获取读锁（非阻塞）
//
// 返回 true 表示成功获取锁，false 表示写锁已存在
func (bl *BitmapLock) TryRLock(pageID uint64) bool {
	shard := bl.getShard(pageID)

	if !shard.mu.TryLock() {
		return false
	}
	defer shard.mu.Unlock()

	// 检查是否有写锁
	if shard.writer.Load() != 0 {
		return false
	}

	// 增加读锁计数
	bit := bl.getBitInShard(pageID)
	shard.readers[bit].Add(1)

	// 更新 bitmap
	oldBitmap := shard.bitmap.Load()
	newBitmap := oldBitmap | (1 << bit)
	shard.bitmap.Store(newBitmap)

	return true
}

// IsLocked 检查页面是否被锁定（读或写）
func (bl *BitmapLock) IsLocked(pageID uint64) bool {
	shard := bl.getShard(pageID)
	bit := bl.getBitInShard(pageID)
	bitmap := shard.bitmap.Load()
	return (bitmap & (1 << bit)) != 0
}

// IsWriteLocked 检查页面是否被写锁锁定
func (bl *BitmapLock) IsWriteLocked(pageID uint64) bool {
	shard := bl.getShard(pageID)
	return shard.writer.Load() != 0
}

// GetReadLockCount 获取页面的读锁计数
func (bl *BitmapLock) GetReadLockCount(pageID uint64) uint32 {
	shard := bl.getShard(pageID)
	bit := bl.getBitInShard(pageID)
	return shard.readers[bit].Load()
}

// Stats 返回锁的统计信息
type LockStats struct {
	ShardCount  int // 分片数量
	LockedPages int // 被锁定的页面数
}

// Stats 获取锁统计信息
func (bl *BitmapLock) Stats() LockStats {
	stats := LockStats{
		ShardCount: bl.shardCount,
	}

	for _, shard := range bl.shards {
		bitmap := shard.bitmap.Load()
		for i := range bitsPerWord {
			if (bitmap & (1 << i)) != 0 {
				stats.LockedPages++
			}
		}
	}

	return stats
}
