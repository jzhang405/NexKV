package btree

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodePagePos(t *testing.T) {
	tests := []struct {
		name     string
		chunkID  int
		offset   int
		pageType int
		wantErr  bool
	}{
		{
			name:     "有效参数（非零）",
			chunkID:  1,
			offset:  4096,
			pageType: 1,
			wantErr:  false,
		},
		{
			name:     "大有效值",
			chunkID:  1000000, // 1M
			offset:  256 * 1024 * 1024, // 256MB
			pageType: MaxPageType - 1,
			wantErr:  false,
		},
		{
			name:     "ChunkID 为负数",
			chunkID:  -1,
			offset:  0,
			pageType: 0,
			wantErr:  true,
		},
		{
			name:     "ChunkID 超出范围",
			chunkID:  MaxChunks,
			offset:  0,
			pageType: 0,
			wantErr:  true,
		},
		{
			name:     "Offset 为负数",
			chunkID:  0,
			offset:  -1,
			pageType: 0,
			wantErr:  true,
		},
		{
			name:     "Offset 超出范围",
			chunkID:  0,
			offset:  MaxOffset,
			pageType: 0,
			wantErr:  true,
		},
		{
			name:     "PageType 为负数",
			chunkID:  0,
			offset:  0,
			pageType: -1,
			wantErr:  true,
		},
		{
			name:     "PageType 超出范围",
			chunkID:  0,
			offset:  0,
			pageType: MaxPageType,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos, err := EncodePagePos(tt.chunkID, tt.offset, tt.pageType)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotZero(t, pos)
		})
	}
}

func TestDecodePagePos(t *testing.T) {
	tests := []struct {
		name      string
		chunkID   int
		offset    int
		pageType  int
	}{
		{
			name:     "最小值",
			chunkID:  0,
			offset:  0,
			pageType: 0,
		},
		{
			name:     "中等值",
			chunkID:  1000,
			offset:  4096,
			pageType: 1,
		},
		{
			name:     "大值",
			chunkID:  1000000,
			offset:  256 * 1024 * 1024,
			pageType: MaxPageType - 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos, err := EncodePagePos(tt.chunkID, tt.offset, tt.pageType)
			require.NoError(t, err)

			chunkID, offset, pageType := DecodePagePos(pos)
			assert.Equal(t, tt.chunkID, chunkID)
			assert.Equal(t, tt.offset, offset)
			assert.Equal(t, tt.pageType, pageType)
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	// 测试编码解码往返的正确性
	testCases := []struct {
		chunkID  int
		offset   int
		pageType int
	}{
		{0, 0, 0},
		{1, 4096, 1},
		{100, 8192, 2},
		{1000000, 256 * 1024 * 1024, MaxPageType - 1}, // 大值
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			pos, err := EncodePagePos(tc.chunkID, tc.offset, tc.pageType)
			require.NoError(t, err)

			chunkID, offset, pageType := DecodePagePos(pos)
			assert.Equal(t, tc.chunkID, chunkID)
			assert.Equal(t, tc.offset, offset)
			assert.Equal(t, tc.pageType, pageType)
		})
	}
}

func TestValidatePosition(t *testing.T) {
	tests := []struct {
		name string
		pos  int64
		want bool
	}{
		{
			name: "零位置",
			pos:  0,
			want: false,
		},
		{
			name: "有效位置",
			pos:  1 << 38, // ChunkID=1, Offset=0, PageType=0
			want: true,
		},
		{
			name: "大有效位置",
			pos:  (int64(1000000) << 38) | (int64(256*1024*1024) << 6) | (int64(MaxPageType-1) << 1),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidatePosition(tt.pos)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetChunkID(t *testing.T) {
	pos, err := EncodePagePos(100, 4096, 1)
	require.NoError(t, err)

	chunkID := GetChunkID(pos)
	assert.Equal(t, 100, chunkID)
}

func TestGetOffset(t *testing.T) {
	pos, err := EncodePagePos(100, 8192, 1)
	require.NoError(t, err)

	offset := GetOffset(pos)
	assert.Equal(t, 8192, offset)
}

func TestGetPageType(t *testing.T) {
	pos, err := EncodePagePos(100, 4096, 2)
	require.NoError(t, err)

	pageType := GetPageType(pos)
	assert.Equal(t, 2, pageType)
}

// Benchmark 编码解码性能
func BenchmarkEncodePagePos(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = EncodePagePos(1000, 4096, 1)
	}
}

func BenchmarkDecodePagePos(b *testing.B) {
	pos, _ := EncodePagePos(1000, 4096, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DecodePagePos(pos)
	}
}

func BenchmarkEncodeDecodeRoundTrip(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pos, _ := EncodePagePos(1000, 4096, 1)
		DecodePagePos(pos)
	}
}
