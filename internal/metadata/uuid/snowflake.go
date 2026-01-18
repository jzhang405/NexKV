// Package uuid 提供 Snowflake ID 生成实现（高性能短 ID）
package uuid

import (
	"errors"
	"fmt"
	"github.com/jzhang405/NexKV/internal/metadata/errcodes"
	"sync"
	"time"
)

// Snowflake 雪花算法 ID 生成器
//
// 结构: 64-bit 整数
//   - 1 bit: 符号位（始终为 0）
//   - 41 bits: 毫秒时间戳（可以使用 69 年）
//   - 10 bits: 机器 ID（5 bits 数据中心 + 5 bits 工作节点）
//   - 12 bits: 序列号（每毫秒内可生成 4096 个 ID）
//
// 优势:
//   - 高性能：单机每毫秒可生成 4096 个 ID
//   - 有序：按时间递增
//   - 短 ID：64-bit 整数，比 UUID 短
//   - 无需网络依赖：本地生成
//
// 适用场景: 节点 ID、高并发场景、需要短 ID 的场景
type Snowflake struct {
	mu           sync.Mutex
	lastTime     int64 // 上次生成时间戳（毫秒）
	sequence     int64 // 序列号
	machineID    int64 // 机器 ID（10 bits: 5 bits 数据中心 + 5 bits 工作节点）
	maxSequence  int64 // 最大序列号
	timeShift    uint8 // 时间戳偏移位数
	machineShift uint8 // 机器 ID 偏移位数
}

// Snowflake 配置常量
const (
	// SnowflakeEpoch 雪花算法纪元（起始时间）
	// 设定为: 2024-01-01 00:00:00 UTC
	SnowflakeEpoch = 1704067200000 // 2024-01-01 00:00:00 UTC in milliseconds

	// 各部分位数
	timeBits    = 41 // 时间戳位数
	machineBits = 10 // 机器 ID 位数
	seqBits     = 12 // 序列号位数

	// 最大值
	maxMachineID = (1 << machineBits) - 1 // 1023
	maxSequence  = (1 << seqBits) - 1     // 4095

	// 偏移位数
	machineShift = seqBits                    // 12
	timeShift    = machineShift + machineBits // 22
)

// NewSnowflake 创建 Snowflake ID 生成器
//
// 参数:
//   - datacenterID: 数据中心 ID（0-31）
//   - workerID: 工作节点 ID（0-31）
//
// 返回:
//   - *Snowflake: Snowflake ID 生成器
//   - error: 参数错误时返回错误
func NewSnowflake(datacenterID, workerID int64) (*Snowflake, error) {
	if datacenterID < 0 || datacenterID > 31 {
		return nil, errors.New("数据中心 ID 必须在 0-31 之间")
	}
	if workerID < 0 || workerID > 31 {
		return nil, errors.New("工作节点 ID 必须在 0-31 之间")
	}

	machineID := (datacenterID << 5) | workerID

	return &Snowflake{
		lastTime:     0,
		sequence:     0,
		machineID:    machineID,
		maxSequence:  maxSequence,
		timeShift:    timeShift,
		machineShift: machineShift,
	}, nil
}

// Generate 生成 Snowflake ID
func (s *Snowflake) Generate() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()

	// 处理时钟回拨
	if now < s.lastTime {
		return 0, errcodes.NewClockOperationError(fmt.Sprintf("时钟回拨检测: 当前时间=%d, 上次时间=%d", now, s.lastTime))
	}

	// 同一毫秒内，序列号递增
	if now == s.lastTime {
		s.sequence = (s.sequence + 1) & s.maxSequence
		// 序列号溢出，等待下一毫秒
		if s.sequence == 0 {
			// 等待下一毫秒
			for now <= s.lastTime {
				time.Sleep(time.Microsecond * 100)
				now = time.Now().UnixMilli()
			}
		}
	} else {
		// 新的毫秒，重置序列号
		s.sequence = 0
	}

	s.lastTime = now

	// 计算 Snowflake ID
	id := ((now - SnowflakeEpoch) << s.timeShift) |
		(s.machineID << s.machineShift) |
		s.sequence

	return id, nil
}

// GenerateString 生成 Snowflake ID 并返回字符串形式
func (s *Snowflake) GenerateString() (string, error) {
	id, err := s.Generate()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", id), nil
}

// MustGenerate 生成 Snowflake ID，失败时 panic
func (s *Snowflake) MustGenerate() int64 {
	id, err := s.Generate()
	if err != nil {
		panic(err)
	}
	return id
}

// ParseSnowflake 解析 Snowflake ID
func ParseSnowflake(id int64) map[string]any {
	timestamp := (id >> timeShift) + SnowflakeEpoch
	machineID := (id >> machineShift) & maxMachineID
	sequence := id & maxSequence
	datacenterID := (machineID >> 5) & 0x1F
	workerID := machineID & 0x1F

	return map[string]any{
		"id":           id,
		"timestamp":    timestamp,
		"datetime":     time.Unix(timestamp/1000, (timestamp%1000)*1e6),
		"datacenterID": datacenterID,
		"workerID":     workerID,
		"sequence":     sequence,
	}
}

// ExtractMachineID 从配置获取机器 ID
func ExtractMachineID(datacenterID, workerID int64) int64 {
	return (datacenterID << 5) | workerID
}

// DefaultSnowflake 创建默认 Snowflake ID 生成器
// 使用数据中心 ID=0，工作节点 ID=0
func DefaultSnowflake() (*Snowflake, error) {
	return NewSnowflake(0, 0)
}
