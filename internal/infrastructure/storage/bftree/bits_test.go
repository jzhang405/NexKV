package bftree

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetBitAndGetBit(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		offset   uint64
		setValue bool
		getValue bool
	}{
		{
			name:     "set bit 0 to true",
			data:     make([]byte, 8),
			offset:   0,
			setValue: true,
			getValue: true,
		},
		{
			name:     "set bit 7 to true",
			data:     make([]byte, 8),
			offset:   7,
			setValue: true,
			getValue: true,
		},
		{
			name:     "set bit 8 to true",
			data:     make([]byte, 8),
			offset:   8,
			setValue: true,
			getValue: true,
		},
		{
			name:     "set bit 63 to true",
			data:     make([]byte, 8),
			offset:   63,
			setValue: true,
			getValue: true,
		},
		{
			name:     "set bit to true then false",
			data:     make([]byte, 8),
			offset:   10,
			setValue: false,
			getValue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetBit(tt.data, tt.offset, tt.setValue)
			got := GetBit(tt.data, tt.offset)
			if got != tt.getValue {
				t.Errorf("GetBit() = %v, want %v", got, tt.getValue)
			}
		})
	}
}

func TestCountBits(t *testing.T) {
	tests := []struct {
		name   string
		bitmap uint64
		want   int
	}{
		{
			name:   "all zeros",
			bitmap: 0x0000000000000000,
			want:   0,
		},
		{
			name:   "all ones",
			bitmap: 0xffffffffffffffff,
			want:   64,
		},
		{
			name:   "single bit",
			bitmap: 0x0000000000000001,
			want:   1,
		},
		{
			name:   "alternating bits",
			bitmap: 0x5555555555555555,
			want:   32,
		},
		{
			name:   "random pattern",
			bitmap: 0xff00ff00ff00ff00,
			want:   32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountBits(tt.bitmap)
			if got != tt.want {
				t.Errorf("CountBits() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNextFreeSlot(t *testing.T) {
	tests := []struct {
		name   string
		bitmap uint64
		want   int
	}{
		{
			name:   "all free",
			bitmap: 0x0000000000000000,
			want:   0,
		},
		{
			name:   "bit 0 occupied",
			bitmap: 0x0000000000000001,
			want:   1,
		},
		{
			name:   "bit 0-7 occupied",
			bitmap: 0x00000000000000ff,
			want:   8,
		},
		{
			name:   "all occupied",
			bitmap: 0xffffffffffffffff,
			want:   -1,
		},
		{
			name:   "alternating occupied",
			bitmap: 0x5555555555555555,
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextFreeSlot(tt.bitmap)
			if got != tt.want {
				t.Errorf("NextFreeSlot() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindFirstSet(t *testing.T) {
	tests := []struct {
		name   string
		bitmap uint64
		want   int
	}{
		{
			name:   "no bits set",
			bitmap: 0x0000000000000000,
			want:   -1,
		},
		{
			name:   "bit 0 set",
			bitmap: 0x0000000000000001,
			want:   0,
		},
		{
			name:   "bit 7 set",
			bitmap: 0x0000000000000080,
			want:   7,
		},
		{
			name:   "bit 63 set",
			bitmap: 0x8000000000000000,
			want:   63,
		},
		{
			name:   "multiple bits set",
			bitmap: 0x00000000000000ff,
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindFirstSet(tt.bitmap)
			if got != tt.want {
				t.Errorf("FindFirstSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetBitRange(t *testing.T) {
	tests := []struct {
		name   string
		bitmap uint64
		start  uint
		end    uint
		value  bool
		want   uint64
	}{
		{
			name:   "set range 0-8 to true",
			bitmap: 0x0000000000000000,
			start:  0,
			end:    8,
			value:  true,
			want:   0x00000000000000ff,
		},
		{
			name:   "set range 8-16 to true",
			bitmap: 0x0000000000000000,
			start:  8,
			end:    16,
			value:  true,
			want:   0x000000000000ff00,
		},
		{
			name:   "set range 0-8 to false",
			bitmap: 0x00000000000000ff,
			start:  0,
			end:    8,
			value:  false,
			want:   0x0000000000000000,
		},
		{
			name:   "set range with odd boundaries",
			bitmap: 0x0000000000000000,
			start:  3,
			end:    10,
			value:  true,
			want:   0x00000000000003f8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.bitmap
			SetBitRange(&got, tt.start, tt.end, tt.value)
			if got != tt.want {
				t.Errorf("SetBitRange() = 0x%016x, want 0x%016x", got, tt.want)
			}
		})
	}
}

func TestIsBitRangeFull(t *testing.T) {
	tests := []struct {
		name   string
		bitmap uint64
		start  uint
		end    uint
		want   bool
	}{
		{
			name:   "range 0-8 full",
			bitmap: 0x00000000000000ff,
			start:  0,
			end:    8,
			want:   true,
		},
		{
			name:   "range 0-8 not full",
			bitmap: 0x00000000000000fe,
			start:  0,
			end:    8,
			want:   false,
		},
		{
			name:   "range 8-16 full",
			bitmap: 0x000000000000ff00,
			start:  8,
			end:    16,
			want:   true,
		},
		{
			name:   "all full",
			bitmap: 0xffffffffffffffff,
			start:  0,
			end:    64,
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsBitRangeFull(tt.bitmap, tt.start, tt.end)
			if got != tt.want {
				t.Errorf("IsBitRangeFull() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCountBitsInRange(t *testing.T) {
	tests := []struct {
		name   string
		bitmap uint64
		start  uint
		end    uint
		want   int
	}{
		{
			name:   "count in range 0-8",
			bitmap: 0x00000000000000ff,
			start:  0,
			end:    8,
			want:   8,
		},
		{
			name:   "count in range 8-16",
			bitmap: 0x00000000000000ff,
			start:  8,
			end:    16,
			want:   0,
		},
		{
			name:   "count in range 0-16",
			bitmap: 0x00000000000000ff,
			start:  0,
			end:    16,
			want:   8,
		},
		{
			name:   "count in alternating pattern",
			bitmap: 0x5555555555555555,
			start:  0,
			end:    16,
			want:   8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountBitsInRange(tt.bitmap, tt.start, tt.end)
			if got != tt.want {
				t.Errorf("CountBitsInRange() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBytesToUint64(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want uint64
	}{
		{
			name: "zero",
			data: []byte{0, 0, 0, 0, 0, 0, 0, 0},
			want: 0,
		},
		{
			name: "little-endian 1",
			data: []byte{1, 0, 0, 0, 0, 0, 0, 0},
			want: 1,
		},
		{
			name: "little-endian max",
			data: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			want: 0xffffffffffffffff,
		},
		{
			name: "mixed",
			data: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			want: 0x0807060504030201,
		},
		{
			name: "less than 8 bytes",
			data: []byte{0x01, 0x02, 0x03, 0x04},
			want: 0x04030201,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BytesToUint64(tt.data)
			if got != tt.want {
				t.Errorf("BytesToUint64() = 0x%016x, want 0x%016x", got, tt.want)
			}
		})
	}
}

func TestUint64ToBytes(t *testing.T) {
	tests := []struct {
		name  string
		value uint64
		want  []byte
	}{
		{
			name:  "zero",
			value: 0,
			want:  []byte{0, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name:  "one",
			value: 1,
			want:  []byte{1, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name:  "max",
			value: 0xffffffffffffffff,
			want:  []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		},
		{
			name:  "mixed",
			value: 0x0807060504030201,
			want:  []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Uint64ToBytes(tt.value)
			if len(got) != len(tt.want) {
				t.Fatalf("Uint64ToBytes() length = %v, want %v", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Uint64ToBytes()[%d] = 0x%02x, want 0x%02x", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Benchmark tests
func BenchmarkCountBits(b *testing.B) {
	bitmap := uint64(0x5555555555555555)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CountBits(bitmap)
	}
}

func BenchmarkNextFreeSlot(b *testing.B) {
	bitmap := uint64(0x00000000000000ff)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NextFreeSlot(bitmap)
	}
}

func BenchmarkFindFirstSet(b *testing.B) {
	bitmap := uint64(0x0000000000000080)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FindFirstSet(bitmap)
	}
}

// TestBits_RangeFunctions_Coverage 测试位范围函数覆盖率
func TestBits_RangeFunctions_Coverage(t *testing.T) {
	var bitmap uint64

	// 测试 IsBitRangeFull - 空范围
	assert.False(t, IsBitRangeFull(bitmap, 0, 8))

	// 测试 IsBitRangeFull - 部分填充
	bitmap = 0x0F
	assert.False(t, IsBitRangeFull(bitmap, 0, 8))

	// 测试 IsBitRangeFull - 完全填充
	bitmap = 0xFF
	assert.True(t, IsBitRangeFull(bitmap, 0, 8))

	// 测试 CountBitsInRange
	bitmap = 0b10101010
	count := CountBitsInRange(bitmap, 0, 4)
	assert.Equal(t, 2, count)

	// 测试 CountBitsInRange - 全零
	count = CountBitsInRange(0, 0, 8)
	assert.Equal(t, 0, count)

	// 测试 CountBitsInRange - 全一
	count = CountBitsInRange(0xFF, 0, 8)
	assert.Equal(t, 8, count)
}

// TestGetBit_BitOperations 测试位操作

// TestSetBit_ModifyBits 测试修改位
func TestSetBit_ModifyBits(t *testing.T) {
	data := []byte{0x00, 0x00}

	// 设置位
	SetBit(data, 0, true)
	assert.Equal(t, byte(0x01), data[0])

	SetBit(data, 7, true)
	assert.Equal(t, byte(0x81), data[0])

	SetBit(data, 8, true)
	assert.Equal(t, byte(0x81), data[0])
	assert.Equal(t, byte(0x01), data[1])

	// 清除位
	SetBit(data, 0, false)
	assert.Equal(t, byte(0x80), data[0])
}
