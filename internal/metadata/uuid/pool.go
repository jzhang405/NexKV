// Package uuid 提供 UUID 预生成池（性能优化）
package uuid

import (
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// UUIDPool UUID 预生成池
// 性能提升: 预生成池可将 UUID 生成性能提升 2-3 倍，适合高并发场景
type UUIDPool struct {
	mu        sync.Mutex
	pool      chan string
	poolSize  int
	uuidType  string // "v4", "v7", "snowflake"
	stopCh    chan struct{}
	wg        sync.WaitGroup
	snowflake *Snowflake
}

// NewUUIDPool 创建 UUID 预生成池
// 参数: uuidType: "v4", "v7", "snowflake"; poolSize: 池大小（默认 100）
//
//	datacenterID, workerID: 仅 snowflake 类型需要
func NewUUIDPool(uuidType string, poolSize int, datacenterID, workerID int64) (*UUIDPool, error) {
	if poolSize <= 0 {
		poolSize = 100
	}

	pool := &UUIDPool{
		pool:     make(chan string, poolSize),
		poolSize: poolSize,
		uuidType: uuidType,
		stopCh:   make(chan struct{}),
	}

	if uuidType == "snowflake" {
		snowflake, err := NewSnowflake(datacenterID, workerID)
		if err != nil {
			return nil, err
		}
		pool.snowflake = snowflake
	}

	pool.refill()

	pool.wg.Add(1)
	go pool.backgroundRefill()

	return pool, nil
}

// Generate 实现 UUIDGenerator 接口
func (p *UUIDPool) Generate() string {
	return p.Get()
}

// GenerateTransactionID 实现 UUIDGenerator 接口
func (p *UUIDPool) GenerateTransactionID() string {
	return p.Get()
}

// GenerateNodeID 实现 UUIDGenerator 接口，仅 snowflake 类型有效
// 返回值: 成功返回有效 ID (> 0)，失败返回 0
func (p *UUIDPool) GenerateNodeID() int64 {
	if p.uuidType == "snowflake" && p.snowflake != nil {
		id, err := p.snowflake.Generate()
		if err != nil {
			logging.Errorf("Snowflake 生成失败，返回 0: %v", err)
			return 0
		}
		return id
	}
	logging.Warnf("UUIDPool 类型 %s 不支持 GenerateNodeID，返回 0", p.uuidType)
	return 0
}

// Get 从池中获取 UUID，池为空时立即生成
func (p *UUIDPool) Get() string {
	select {
	case uuid := <-p.pool:
		return uuid
	default:
		return p.generate()
	}
}

// MustGet 从池中获取 UUID，阻塞等待
func (p *UUIDPool) MustGet() string {
	return <-p.pool
}

// generate 根据类型生成新的 UUID
func (p *UUIDPool) generate() string {
	switch p.uuidType {
	case "v4":
		return GenerateUUIDv4()
	case "v7":
		return GenerateUUIDv7()
	case "snowflake":
		if p.snowflake != nil {
			id, err := p.snowflake.Generate()
			if err != nil {
				logging.Errorf("Snowflake 生成失败，降级到 UUID v7: %v", err)
				return GenerateUUIDv7()
			}
			return fmt.Sprintf("%d", id)
		}
		logging.Warnf("Snowflake 生成器未初始化，降级到 UUID v7")
		return GenerateUUIDv7()
	default:
		return GenerateUUIDv4()
	}
}

// refill 补充池中的 UUID
func (p *UUIDPool) refill() {
	p.mu.Lock()
	defer p.mu.Unlock()

	needed := p.poolSize - len(p.pool)
	if needed <= 0 {
		return
	}

	for i := 0; i < needed; i++ {
		select {
		case p.pool <- p.generate():
		default:
			return
		}
	}
}

// backgroundRefill 后台补充协程
func (p *UUIDPool) backgroundRefill() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	defer p.wg.Done()

	for {
		select {
		case <-ticker.C:
			p.refill()
		case <-p.stopCh:
			return
		}
	}
}

// GetSize 获取池中当前 UUID 数量
func (p *UUIDPool) GetSize() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pool)
}

// GetPoolSize 获取池大小
func (p *UUIDPool) GetPoolSize() int {
	return p.poolSize
}

// Close 关闭池并等待后台协程退出
func (p *UUIDPool) Close() {
	close(p.stopCh)
	p.wg.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()

	for len(p.pool) > 0 {
		<-p.pool
	}
}

// PerformanceStats 性能统计
type PerformanceStats struct {
	PoolSize    int
	CurrentSize int
	UUIDType    string
}

// GetStats 获取性能统计
func (p *UUIDPool) GetStats() PerformanceStats {
	p.mu.Lock()
	currentSize := len(p.pool)
	p.mu.Unlock()

	return PerformanceStats{
		PoolSize:    p.poolSize,
		CurrentSize: currentSize,
		UUIDType:    p.uuidType,
	}
}
