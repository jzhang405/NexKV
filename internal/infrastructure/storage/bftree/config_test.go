package bftree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	tests := []struct {
		name  string
		check func(*Config) bool
	}{
		{
			name: "PageSize",
			check: func(c *Config) bool {
				return c.PageSize == DefaultPageSize
			},
		},
		{
			name: "MaxDepth",
			check: func(c *Config) bool {
				return c.MaxDepth == DefaultMaxDepth
			},
		},
		{
			name: "EnableWAL",
			check: func(c *Config) bool {
				return c.EnableWAL
			},
		},
		{
			name: "EnableDeltaChain",
			check: func(c *Config) bool {
				return c.EnableDeltaChain
			},
		},
		{
			name: "BitmapLockShards",
			check: func(c *Config) bool {
				return c.BitmapLockShards == DefaultBitmapLockShards
			},
		},
		{
			name: "SegmentSize",
			check: func(c *Config) bool {
				return c.SegmentSize == DefaultSegmentSize
			},
		},
		{
			name: "CacheSize",
			check: func(c *Config) bool {
				return c.CacheSize == 10000
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.check(config) {
				t.Errorf("DefaultConfig() %s check failed", tt.name)
			}
		})
	}
}
func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "valid default config",
			config: func() *Config {
				c := DefaultConfig()
				c.DataDir = "/tmp/test"
				return c
			}(),
			wantErr: false,
		},
		{
			name: "invalid page size too small",
			config: func() *Config {
				c := DefaultConfig()
				c.DataDir = "/tmp/test"
				c.PageSize = 512
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid page size too large",
			config: func() *Config {
				c := DefaultConfig()
				c.DataDir = "/tmp/test"
				c.PageSize = 128 * 1024
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid page size not power of 2",
			config: func() *Config {
				c := DefaultConfig()
				c.DataDir = "/tmp/test"
				c.PageSize = 3000
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid max depth too small",
			config: func() *Config {
				c := DefaultConfig()
				c.DataDir = "/tmp/test"
				c.MaxDepth = 2
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid max depth too large",
			config: func() *Config {
				c := DefaultConfig()
				c.DataDir = "/tmp/test"
				c.MaxDepth = 15
				return c
			}(),
			wantErr: true,
		},
		{
			name: "missing data dir",
			config: func() *Config {
				c := DefaultConfig()
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid bitmap lock shards too small",
			config: func() *Config {
				c := DefaultConfig()
				c.DataDir = "/tmp/test"
				c.BitmapLockShards = 0
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid bitmap lock shards too large",
			config: func() *Config {
				c := DefaultConfig()
				c.DataDir = "/tmp/test"
				c.BitmapLockShards = 128
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid bitmap lock shards not power of 2",
			config: func() *Config {
				c := DefaultConfig()
				c.DataDir = "/tmp/test"
				c.BitmapLockShards = 15
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid segment size too small",
			config: func() *Config {
				c := DefaultConfig()
				c.DataDir = "/tmp/test"
				c.EnableWAL = true
				c.SegmentSize = 512 * 1024
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid segment size too large",
			config: func() *Config {
				c := DefaultConfig()
				c.DataDir = "/tmp/test"
				c.EnableWAL = true
				c.SegmentSize = 2 * 1024 * 1024 * 1024
				return c
			}(),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
func TestPromotionConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  PromotionConfig
		wantErr bool
	}{
		{
			name:    "valid default config",
			config:  DefaultPromotionConfig(),
			wantErr: false,
		},
		{
			name: "invalid size threshold too small",
			config: func() PromotionConfig {
				c := DefaultPromotionConfig()
				c.SizeThresholdPct = 40
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid size threshold too large",
			config: func() PromotionConfig {
				c := DefaultPromotionConfig()
				c.SizeThresholdPct = 110
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid max delta chain length too small",
			config: func() PromotionConfig {
				c := DefaultPromotionConfig()
				c.MaxDeltaChainLen = 0
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid max delta chain length too large",
			config: func() PromotionConfig {
				c := DefaultPromotionConfig()
				c.MaxDeltaChainLen = 128
				return c
			}(),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("PromotionConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
func TestPageLevelString(t *testing.T) {
	tests := []struct {
		level PageLevel
		want  string
	}{
		{L1, "L1(64B)"},
		{L2, "L2(128B)"},
		{L3, "L3(256B)"},
		{L4, "L4(512B)"},
		{L5, "L5(1KB)"},
		{L6, "L6(2KB)"},
		{Full, "Full(4KB)"},
		{PageLevel(99), "Unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.level.String()
			if got != tt.want {
				t.Errorf("PageLevel.String() = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestPageLevelPageSize(t *testing.T) {
	tests := []struct {
		level PageLevel
		want  int
	}{
		{L1, 64},
		{L2, 128},
		{L3, 256},
		{L4, 512},
		{L5, 1024},
		{L6, 2048},
		{Full, 4096},
		{PageLevel(99), 64}, // 默认返回 64
	}
	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			got := tt.level.PageSize()
			if got != tt.want {
				t.Errorf("PageLevel.PageSize() = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestPageLevelValid(t *testing.T) {
	tests := []struct {
		level PageLevel
		want  bool
	}{
		{L1, true},
		{L2, true},
		{L3, true},
		{L4, true},
		{L5, true},
		{L6, true},
		{Full, true},
		{PageLevel(-1), false},
		{PageLevel(99), false},
	}
	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			got := tt.level.Valid()
			if got != tt.want {
				t.Errorf("PageLevel.Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestPageLevelNextLevel(t *testing.T) {
	tests := []struct {
		level PageLevel
		want  PageLevel
	}{
		{L1, L2},
		{L2, L3},
		{L3, L4},
		{L4, L5},
		{L5, L6},
		{L6, Full},
		{Full, Full}, // 已经是最大，返回自身
	}
	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			got := tt.level.NextLevel()
			if got != tt.want {
				t.Errorf("PageLevel.NextLevel() = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestPageTypeString(t *testing.T) {
	tests := []struct {
		ptype PageType
		want  string
	}{
		{PageTypeLeaf, "Leaf"},
		{PageTypeInner, "Inner"},
		{PageType(99), "Unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.ptype.String()
			if got != tt.want {
				t.Errorf("PageType.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConfig_EnsureDataDir 测试目录创建
func TestConfig_EnsureDataDir(t *testing.T) {
	t.Run("创建数据目录", func(t *testing.T) {
		tmpDir := t.TempDir()
		config := &Config{
			DataDir: tmpDir,
		}
		err := config.EnsureDataDir()
		require.NoError(t, err)
		// 验证目录已创建
		info, err := os.Stat(tmpDir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})
	t.Run("创建数据目录和WAL目录", func(t *testing.T) {
		tmpDir := t.TempDir()
		walDir := filepath.Join(tmpDir, "wal")
		config := &Config{
			DataDir:   tmpDir,
			EnableWAL: true,
			WALDir:    walDir,
		}
		err := config.EnsureDataDir()
		require.NoError(t, err)
		// 验证两个目录都已创建
		_, err = os.Stat(tmpDir)
		require.NoError(t, err)
		_, err = os.Stat(walDir)
		require.NoError(t, err)
	})
	t.Run("目录已存在", func(t *testing.T) {
		tmpDir := t.TempDir()
		config := &Config{
			DataDir: tmpDir,
		}
		// 第一次创建
		err := config.EnsureDataDir()
		require.NoError(t, err)
		// 第二次调用（目录已存在）应该成功
		err = config.EnsureDataDir()
		assert.NoError(t, err)
	})
}

// TestCoverage_SmallPages 小页面配置测试
func TestCoverage_SmallPages(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false
	config.PageSize = 1024 // 使用小页面

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入数据以触发分裂
	const numKeys = 500
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("small-%04d", i))
		value := []byte(fmt.Sprintf("value-%04d-padding", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 验证
	stats := tree.GetStats()
	t.Logf("Small pages: TotalPages=%d", stats.TotalPages)

	for i := 0; i < numKeys; i += 20 {
		key := []byte(fmt.Sprintf("small-%04d", i))
		_, err := tree.Get(ctx, key)
		assert.NoError(t, err)
	}
}
