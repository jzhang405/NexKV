// Package uuid 提供 Snowflake ID 生成实现（高性能短 ID）
package uuid

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// Snowflake 雪花算法 ID 生成器
// 64-bit 结构: 1 bit 符号 + 41 bits 时间戳 + 10 bits 机器 ID + 12 bits 序列号
// 优势: 高性能（每毫秒 4096 个 ID）、有序、短 ID、本地生成
type Snowflake struct {
	mu           sync.Mutex
	lastTime     int64
	sequence     int64
	machineID    int64
	maxSequence  int64
	timeShift    uint8
	machineShift uint8
}

// Snowflake 配置常量
const (
	// SnowflakeEpoch 雪花算法纪元（起始时间: 2024-01-01 00:00:00 UTC）
	SnowflakeEpoch = 1704067200000

	// 各部分位数
	timeBits    = 41
	machineBits = 10
	seqBits     = 12

	// 最大值
	maxMachineID = (1 << machineBits) - 1 // 1023
	maxSequence  = (1 << seqBits) - 1     // 4095

	// 偏移位数
	machineShift = seqBits                    // 12
	timeShift    = machineShift + machineBits // 22

	// 超时配置
	sequenceOverflowTimeout = 100 * time.Millisecond

	// ID 类型掩码
	maxIDPerType = 0x1F
	typeIDShift  = 5
)

// NewSnowflake 创建 Snowflake ID 生成器
// 参数: datacenterID, workerID: 0-31
func NewSnowflake(datacenterID, workerID int64) (*Snowflake, error) {
	if datacenterID < 0 || datacenterID > 31 {
		return nil, errors.New("数据中心 ID 必须在 0-31 之间")
	}
	if workerID < 0 || workerID > 31 {
		return nil, errors.New("工作节点 ID 必须在 0-31 之间")
	}

	machineID := (datacenterID << typeIDShift) | workerID

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

	if now < s.lastTime {
		return 0, types.NewClockOperationError(fmt.Sprintf("时钟回拨检测: 当前时间=%d, 上次时间=%d", now, s.lastTime))
	}

	if now == s.lastTime {
		s.sequence = (s.sequence + 1) & s.maxSequence
		if s.sequence == 0 {
			timeout := time.After(sequenceOverflowTimeout)
			ticker := time.NewTicker(time.Microsecond * 100)
			defer ticker.Stop()

			for now <= s.lastTime {
				select {
				case <-ticker.C:
					now = time.Now().UnixMilli()
				case <-timeout:
					return 0, types.NewClockOperationError("序列号溢出等待超时")
				}
			}
		}
	} else {
		s.sequence = 0
	}

	s.lastTime = now

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
// 使用场景: 仅用于初始化阶段或无法处理错误的场景
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
	datacenterID := (machineID >> typeIDShift) & maxIDPerType
	workerID := machineID & maxIDPerType

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
	return (datacenterID << typeIDShift) | workerID
}

// DefaultSnowflake 创建默认 Snowflake ID 生成器
func DefaultSnowflake() (*Snowflake, error) {
	return NewSnowflake(0, 0)
}
