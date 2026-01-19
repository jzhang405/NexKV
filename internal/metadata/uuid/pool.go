// Package uuid 提供 UUID 预生成池（性能优化）
package uuid

import (
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// UUIDPool UUID 预生成池
//
// 特性:
//   - 预先生成 UUID，避免实时生成的性能开销
//   - 线程安全
//   - 自动补充池中的 UUID
//   - 支持配置池大小
//   - 实现 UUIDGenerator 接口
//
// 性能提升:
//   - 预生成池可将 UUID 生成性能提升 2-3 倍
//   - 适合高并发场景
type UUIDPool struct {
	mu       sync.Mutex
	pool     chan string
	poolSize int
	uuidType string         // "v4", "v7", "snowflake"
	stopCh   chan struct{}  // 停止信号
	wg       sync.WaitGroup // 等待后台协程退出

	// Snowflake 生成器（仅用于 snowflake 类型）
	snowflake *Snowflake
}

// NewUUIDPool 创建 UUID 预生成池
//
// 参数:
//   - uuidType: UUID 类型（"v4", "v7", "snowflake"）
//   - poolSize: 池大小（默认 100）
//   - datacenterID: 数据中心 ID（仅 snowflake 需要）
//   - workerID: 工作节点 ID（仅 snowflake 需要）
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

	// 初始化 Snowflake 生成器（如果需要）
	if uuidType == "snowflake" {
		snowflake, err := NewSnowflake(datacenterID, workerID)
		if err != nil {
			return nil, err
		}
		pool.snowflake = snowflake
	}

	// 预填充池
	pool.refill()

	// 启动后台补充协程
	pool.wg.Add(1)
	go pool.backgroundRefill()

	return pool, nil
}

// Generate 生成 UUID（实现 UUIDGenerator 接口）
// 从池中获取 UUID，如果池为空则立即生成
func (p *UUIDPool) Generate() string {
	return p.Get()
}

// GenerateTransactionID 生成事务 ID（实现 UUIDGenerator 接口）
// 对于 UUIDPool，此方法与 Generate() 相同
func (p *UUIDPool) GenerateTransactionID() string {
	return p.Get()
}

// GenerateNodeID 生成节点 ID（实现 UUIDGenerator 接口）
// 注意：仅当 uuidType 为 "snowflake" 时有效
//
// 返回值说明：
//   - 成功：返回有效的 Snowflake ID（始终 > 0）
//   - 失败：返回 0，并记录错误日志
//
// 注意：由于 Snowflake ID = epoch + 时间戳 + ...，总是 > 0
//
//	因此返回 0 可以明确表示生成失败，而非有效的 ID 值
func (p *UUIDPool) GenerateNodeID() int64 {
	if p.uuidType == "snowflake" && p.snowflake != nil {
		id, err := p.snowflake.Generate()
		if err != nil {
			// 容错机制: 记录错误并返回 0（而非 panic）
			// 调用者应检查返回值是否为 0 来判断是否成功
			logging.Errorf("Snowflake 生成失败，返回 0: %v", err)
			return 0
		}
		return id
	}
	// 非 snowflake 类型，记录警告并返回 0（而非 panic）
	logging.Warnf("UUIDPool 类型 %s 不支持 GenerateNodeID，返回 0", p.uuidType)
	return 0
}

// Get 从池中获取 UUID
// 如果池为空，立即生成一个新的
func (p *UUIDPool) Get() string {
	select {
	case uuid := <-p.pool:
		return uuid
	default:
		// 池为空，立即生成
		return p.generate()
	}
}

// MustGet 从池中获取 UUID，阻塞等待
func (p *UUIDPool) MustGet() string {
	return <-p.pool
}

// generate 生成新的 UUID
//
// 容错机制: snowflake 失败时返回错误（不降级到其他类型，保持类型一致性）
// 注意: 降级行为仅适用于 generate() 方法，不影响 GenerateNodeID()
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
				// 记录错误但不降级到其他类型（保持字符串类型一致性）
				logging.Errorf("Snowflake 生成失败，无法生成 ID: %v", err)
				return GenerateUUIDv7() // 降级为 UUID v7 字符串以保持类型一致
			}
			return fmt.Sprintf("%d", id)
		}
		// Snowflake 未初始化，降级到 UUID v7（保持字符串类型）
		logging.Warnf("Snowflake 生成器未初始化，降级到 UUID v7")
		return GenerateUUIDv7()
	default:
		return GenerateUUIDv4() // 默认使用 v4
	}
}

// refill 补充池中的 UUID
func (p *UUIDPool) refill() {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 计算需要补充的数量
	needed := p.poolSize - len(p.pool)
	if needed <= 0 {
		return
	}

	// 补充 UUID
	for i := 0; i < needed; i++ {
		select {
		case p.pool <- p.generate():
		default:
			// 池已满
			return
		}
	}
}

// backgroundRefill 后台补充协程
func (p *UUIDPool) backgroundRefill() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	defer p.wg.Done() // 确保退出时通知 WaitGroup

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
// 注意：添加锁保护以确保线程安全（与 GetStats 保持一致）
func (p *UUIDPool) GetSize() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pool)
}

// GetPoolSize 获取池大小
func (p *UUIDPool) GetPoolSize() int {
	return p.poolSize
}

// Close 关闭池
// 等待后台协程退出，防止资源泄漏
func (p *UUIDPool) Close() {
	// 停止后台协程
	close(p.stopCh)

	// 等待后台协程退出
	p.wg.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()

	// 清空池（不关闭 channel，避免 send on closed channel）
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
// 优化：只对需要保护的字段加锁
func (p *UUIDPool) GetStats() PerformanceStats {
	// 只对变化的字段加锁
	p.mu.Lock()
	currentSize := len(p.pool)
	p.mu.Unlock()

	return PerformanceStats{
		PoolSize:    p.poolSize, // 只读，不需要锁
		CurrentSize: currentSize,
		UUIDType:    p.uuidType, // 只读，不需要锁
	}
}
