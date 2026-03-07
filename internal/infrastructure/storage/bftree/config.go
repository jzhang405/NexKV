// Package bftree 提供 Bf-Tree 存储引擎实现
//
// Bf-Tree 是 B+ 树的变体，通过以下优化提升性能：
//   - Mini-Page：小页面提升策略，减少内存占用
//   - Delta Chain：延迟写入，批量更新
//   - BitmapLock：细粒度锁，减少锁竞争
package bftree

import (
	"fmt"
	"os"
	"path/filepath"

	pkgerrors "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/pkg/compressor"
)

const (
	// DefaultPageSize 默认页面大小（4KB）
	DefaultPageSize = 4096
	// DefaultMaxDepth 默认最大深度（6 级）
	DefaultMaxDepth = 6
	// DefaultBitmapLockShards 默认 BitmapLock 分片数
	DefaultBitmapLockShards = 16
	// DefaultSegmentSize 默认 WAL 分段大小（64MB）
	DefaultSegmentSize = 64 * 1024 * 1024
)

// Config Bf-Tree 配置
type Config struct {
	// 基础配置
	PageSize  int    `json:"page_size"`  // 页面大小（字节）
	MaxDepth  int    `json:"max_depth"`  // 最大深度
	DataDir   string `json:"data_dir"`   // 数据目录
	EnableWAL bool   `json:"enable_wal"` // 是否启用 WAL

	// Mini-Page 配置
	EnableDeltaChain bool            `json:"enable_delta_chain"` // 是否启用 Delta Chain
	PromotionConfig  PromotionConfig `json:"promotion_config"`   // Mini-Page 提升配置

	// Delta Chain 配置（P2-1 新增）
	MaxDeltaChainLen  int    `json:"max_delta_chain_len"`  // Delta Chain 最大长度（默认 8）
	MaxDeltaChainSize uint16 `json:"max_delta_chain_size"` // Delta Chain 最大大小（字节，默认 Mini-Page 容量的 50%）

	// 并发控制配置
	BitmapLockShards int  `json:"bitmap_lock_shards"` // BitmapLock 分片数
	UseBitmapLock    bool `json:"use_bitmap_lock"`    // 是否启用 BitmapLock（细粒度锁）

	// WAL 配置
	WALDir      string `json:"wal_dir"`      // WAL 目录
	SegmentSize int64  `json:"segment_size"` // WAL 分段大小（字节）

	// 性能调优
	CacheSize int `json:"cache_size"` // 缓存大小（页面数）

	// 合并配置（Phase 2.3 新增）
	MergeThreshold float32 `json:"merge_threshold"` // 合并阈值，默认 0.25 (25%)
	MergeStrategy  string  `json:"merge_strategy"`  // 合并策略：默认 "merge"（rebalance 暂不支持）

	// 压缩配置（P2-2 新增）
	CompressionType      compressor.CompressorType `json:"compression_type"` // 压缩算法类型：none, snappy, lz4, zstd（默认 snappy）
	ZSTDCompressionLevel int                       `json:"zstd_level"`       // ZSTD 压缩级别（1-22，默认 3）
}

// PromotionConfig Mini-Page 提升配置
type PromotionConfig struct {
	// ReadPromotionThreshold 读取提升阈值（每个级别）
	// 格式：map[PageLevel]访问次数阈值
	// L1(64B): 1次, L2(128B): 2次, L3(256B): 4次, ...
	ReadThresholds map[PageLevel]uint32 `json:"read_thresholds"`

	// SizePromotionThresholdPct 大小提升阈值（百分比）
	// 当 Mini-Page 数据大小 >= PageSize * 阈值 时提升
	SizeThresholdPct uint8 `json:"size_threshold_pct"`

	// MaxDeltaChainLength Delta Chain 最大长度
	// 超过此长度时强制提升
	MaxDeltaChainLen uint16 `json:"max_delta_chain_len"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        true,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		UseBitmapLock:    false,
		BitmapLockShards: DefaultBitmapLockShards,
		SegmentSize:      DefaultSegmentSize,
		CacheSize:        10000,   // 10K 页面
		MergeThreshold:   0.25,    // 25% 利用率触发合并
		MergeStrategy:    "merge", // 优先合并策略
		// P2-1: Delta Chain 配置
		MaxDeltaChainLen:  8,    // 最大 8 个 Delta
		MaxDeltaChainSize: 2048, // 最大 2KB（50% of 4KB page）
		// P2-2: 压缩配置
		CompressionType:      compressor.Snappy, // Snappy 压缩（平衡速度和压缩率）
		ZSTDCompressionLevel: 3,                 // ZSTD 默认级别 3
	}
}

// DefaultPromotionConfig 返回默认 Mini-Page 提升配置
func DefaultPromotionConfig() PromotionConfig {
	return PromotionConfig{
		ReadThresholds: map[PageLevel]uint32{
			L1: 1,  // 64B: 1次读取
			L2: 2,  // 128B: 2次读取
			L3: 4,  // 256B: 4次读取
			L4: 8,  // 512B: 8次读取
			L5: 16, // 1KB: 16次读取
			L6: 32, // 2KB: 32次读取
		},
		SizeThresholdPct: 80, // 80% 填充率
		MaxDeltaChainLen: 8,  // 8 个 Delta
	}
}

// Validate 验证配置
func (c *Config) Validate() error {
	// 验证 PageSize
	if c.PageSize < 1024 || c.PageSize > 64*1024 {
		return fmt.Errorf("invalid page size %d: must be between 1KB and 64KB", c.PageSize)
	}

	// 验证 PageSize 是 2 的幂
	if (c.PageSize & (c.PageSize - 1)) != 0 {
		return fmt.Errorf("invalid page size %d: must be power of 2", c.PageSize)
	}

	// 验证 MaxDepth
	if c.MaxDepth < 3 || c.MaxDepth > 10 {
		return fmt.Errorf("invalid max depth %d: must be between 3 and 10", c.MaxDepth)
	}

	// 验证 DataDir
	if c.DataDir == "" {
		return pkgerrors.ErrBfTreeDataDirRequired
	}

	// 验证 BitmapLockShards
	if c.BitmapLockShards < 1 || c.BitmapLockShards > 64 {
		return fmt.Errorf("invalid bitmap lock shards %d: must be between 1 and 64", c.BitmapLockShards)
	}

	// 验证 BitmapLockShards 是 2 的幂
	if (c.BitmapLockShards & (c.BitmapLockShards - 1)) != 0 {
		return fmt.Errorf("invalid bitmap lock shards %d: must be power of 2", c.BitmapLockShards)
	}

	// 验证 WAL 配置
	if c.EnableWAL {
		if c.WALDir == "" {
			// 默认使用 DataDir/wal
			c.WALDir = filepath.Join(c.DataDir, "wal")
		}
		if c.SegmentSize < 1024*1024 || c.SegmentSize > 1024*1024*1024 {
			return fmt.Errorf("invalid segment size %d: must be between 1MB and 1GB", c.SegmentSize)
		}
	}

	// 验证 PromotionConfig
	if err := c.PromotionConfig.Validate(); err != nil {
		return fmt.Errorf("invalid promotion config: %w", err)
	}

	return nil
}

// EnsureDataDir 确保数据目录存在
func (c *Config) EnsureDataDir() error {
	if err := os.MkdirAll(c.DataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data dir %s: %w", c.DataDir, err)
	}

	if c.EnableWAL {
		if err := os.MkdirAll(c.WALDir, 0755); err != nil {
			return fmt.Errorf("failed to create WAL dir %s: %w", c.WALDir, err)
		}
	}

	return nil
}

// Validate 验证 Mini-Page 提升配置
func (p *PromotionConfig) Validate() error {
	// 验证 SizeThresholdPct
	if p.SizeThresholdPct < 50 || p.SizeThresholdPct > 100 {
		return fmt.Errorf("invalid size threshold %d: must be between 50 and 100", p.SizeThresholdPct)
	}

	// 验证 MaxDeltaChainLen
	if p.MaxDeltaChainLen < 1 || p.MaxDeltaChainLen > 64 {
		return fmt.Errorf("invalid max delta chain length %d: must be between 1 and 64", p.MaxDeltaChainLen)
	}

	return nil
}
