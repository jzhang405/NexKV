package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHLC(t *testing.T) {
	before := time.Now().UnixMilli()
	hlc := NewHLC()
	after := time.Now().UnixMilli()

	assert.NotNil(t, hlc)
	assert.GreaterOrEqual(t, hlc.PhysicalTime(), before)
	assert.LessOrEqual(t, hlc.PhysicalTime(), after)
	assert.Equal(t, uint16(0), hlc.LogicalCounter())
}

func TestNewHLCWithTime(t *testing.T) {
	pt := int64(1234567890)
	c := uint16(42)

	hlc := NewHLCWithTime(pt, c)

	assert.NotNil(t, hlc)
	assert.Equal(t, pt, hlc.PhysicalTime())
	assert.Equal(t, c, hlc.LogicalCounter())
}

func TestHLC_PhysicalTime(t *testing.T) {
	tests := []struct {
		name string
		hlc  *HLC
		want int64
	}{
		{
			name: "nil HLC returns 0",
			hlc:  nil,
			want: 0,
		},
		{
			name: "valid HLC returns physical time",
			hlc:  NewHLCWithTime(1234567890, 10),
			want: 1234567890,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.hlc.PhysicalTime()
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestHLC_LogicalCounter(t *testing.T) {
	tests := []struct {
		name string
		hlc  *HLC
		want uint16
	}{
		{
			name: "nil HLC returns 0",
			hlc:  nil,
			want: 0,
		},
		{
			name: "valid HLC returns logical counter",
			hlc:  NewHLCWithTime(1234567890, 42),
			want: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.hlc.LogicalCounter()
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestHLC_LessThan(t *testing.T) {
	tests := []struct {
		name     string
		h1       *HLC
		h2       *HLC
		expected bool
	}{
		{
			name:     "nil HLC returns false",
			h1:       nil,
			h2:       NewHLCWithTime(1000, 0),
			expected: false,
		},
		{
			name:     "other is nil returns false",
			h1:       NewHLCWithTime(1000, 0),
			h2:       nil,
			expected: false,
		},
		{
			name:     "both nil returns false",
			h1:       nil,
			h2:       nil,
			expected: false,
		},
		{
			name:     "smaller physical time",
			h1:       NewHLCWithTime(1000, 10),
			h2:       NewHLCWithTime(2000, 5),
			expected: true,
		},
		{
			name:     "same physical time, smaller counter",
			h1:       NewHLCWithTime(1000, 5),
			h2:       NewHLCWithTime(1000, 10),
			expected: true,
		},
		{
			name:     "same physical time, larger counter",
			h1:       NewHLCWithTime(1000, 10),
			h2:       NewHLCWithTime(1000, 5),
			expected: false,
		},
		{
			name:     "larger physical time",
			h1:       NewHLCWithTime(2000, 0),
			h2:       NewHLCWithTime(1000, 0),
			expected: false,
		},
		{
			name:     "equal HLC",
			h1:       NewHLCWithTime(1000, 10),
			h2:       NewHLCWithTime(1000, 10),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.h1.LessThan(tt.h2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHLC_Equal(t *testing.T) {
	tests := []struct {
		name     string
		h1       *HLC
		h2       *HLC
		expected bool
	}{
		{
			name:     "both nil returns true",
			h1:       nil,
			h2:       nil,
			expected: true,
		},
		{
			name:     "one nil returns false",
			h1:       NewHLCWithTime(1000, 0),
			h2:       nil,
			expected: false,
		},
		{
			name:     "equal values",
			h1:       NewHLCWithTime(1000, 10),
			h2:       NewHLCWithTime(1000, 10),
			expected: true,
		},
		{
			name:     "different physical time",
			h1:       NewHLCWithTime(1000, 10),
			h2:       NewHLCWithTime(2000, 10),
			expected: false,
		},
		{
			name:     "different logical counter",
			h1:       NewHLCWithTime(1000, 10),
			h2:       NewHLCWithTime(1000, 20),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.h1.Equal(tt.h2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHLC_GreaterThan(t *testing.T) {
	tests := []struct {
		name     string
		h1       *HLC
		h2       *HLC
		expected bool
	}{
		{
			name:     "greater physical time",
			h1:       NewHLCWithTime(2000, 0),
			h2:       NewHLCWithTime(1000, 0),
			expected: true,
		},
		{
			name:     "same physical time, greater counter",
			h1:       NewHLCWithTime(1000, 10),
			h2:       NewHLCWithTime(1000, 5),
			expected: true,
		},
		{
			name:     "smaller physical time",
			h1:       NewHLCWithTime(1000, 0),
			h2:       NewHLCWithTime(2000, 0),
			expected: false,
		},
		{
			name:     "equal HLC",
			h1:       NewHLCWithTime(1000, 10),
			h2:       NewHLCWithTime(1000, 10),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.h1.GreaterThan(tt.h2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHLC_Compare(t *testing.T) {
	tests := []struct {
		name     string
		h1       *HLC
		h2       *HLC
		expected int
	}{
		{
			name:     "less than",
			h1:       NewHLCWithTime(1000, 5),
			h2:       NewHLCWithTime(1000, 10),
			expected: -1,
		},
		{
			name:     "equal",
			h1:       NewHLCWithTime(1000, 10),
			h2:       NewHLCWithTime(1000, 10),
			expected: 0,
		},
		{
			name:     "greater than",
			h1:       NewHLCWithTime(2000, 0),
			h2:       NewHLCWithTime(1000, 0),
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.h1.Compare(tt.h2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHLC_String(t *testing.T) {
	tests := []struct {
		name     string
		hlc      *HLC
		expected string
	}{
		{
			name:     "nil HLC",
			hlc:      nil,
			expected: "HLC(nil)",
		},
		{
			name:     "valid HLC",
			hlc:      NewHLCWithTime(1234567890, 42),
			expected: "HLC(pt=1234567890, c=42)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.hlc.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHLC_ToTime(t *testing.T) {
	tests := []struct {
		name string
		hlc  *HLC
	}{
		{
			name: "nil HLC returns zero time",
			hlc:  nil,
		},
		{
			name: "valid HLC converts to time",
			hlc:  NewHLCWithTime(1234567890, 0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.hlc.ToTime()

			if tt.hlc == nil {
				assert.True(t, result.IsZero())
			} else {
				expectedTime := time.Unix(tt.hlc.PhysicalTime()/1000, (tt.hlc.PhysicalTime()%1000)*1e6)
				assert.Equal(t, expectedTime, result)
			}
		})
	}
}

func TestHLC_MarshalBinary(t *testing.T) {
	tests := []struct {
		name    string
		hlc     *HLC
		wantErr bool
	}{
		{
			name:    "nil HLC returns error",
			hlc:     nil,
			wantErr: true,
		},
		{
			name:    "valid HLC marshals successfully",
			hlc:     NewHLCWithTime(1234567890, 42),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.hlc.MarshalBinary()

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, data)
			} else {
				require.NoError(t, err)
				assert.Len(t, data, 10) // 8 bytes pt + 2 bytes c
			}
		})
	}
}

func TestHLC_UnmarshalBinary(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "invalid length",
			data:    []byte{1, 2, 3},
			wantErr: true,
		},
		{
			name:    "valid data",
			data:    make([]byte, 10),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hlc := &HLC{}
			err := hlc.UnmarshalBinary(tt.data)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHLC_SerializeRoundTrip(t *testing.T) {
	original := NewHLCWithTime(1234567890, 42)

	// Marshal
	data, err := original.MarshalBinary()
	require.NoError(t, err)

	// Unmarshal
	recovered := &HLC{}
	err = recovered.UnmarshalBinary(data)
	require.NoError(t, err)

	// Verify
	assert.True(t, original.Equal(recovered))
}

func TestHLC_Clone(t *testing.T) {
	tests := []struct {
		name string
		hlc  *HLC
	}{
		{
			name: "nil HLC returns nil",
			hlc:  nil,
		},
		{
			name: "valid HLC clones successfully",
			hlc:  NewHLCWithTime(1234567890, 42),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clone := tt.hlc.Clone()

			if tt.hlc == nil {
				assert.Nil(t, clone)
			} else {
				assert.NotNil(t, clone)
				assert.True(t, tt.hlc.Equal(clone))
				// Verify it's a different instance
				assert.NotSame(t, tt.hlc, clone)
			}
		})
	}
}

func TestHLC_IsAtMaxValue(t *testing.T) {
	tests := []struct {
		name     string
		hlc      *HLC
		expected bool
	}{
		{
			name:     "nil HLC returns false",
			hlc:      nil,
			expected: false,
		},
		{
			name:     "normal value",
			hlc:      NewHLCWithTime(1234567890, 42),
			expected: false,
		},
		{
			name:     "max value",
			hlc:      NewHLCWithTime(int64((1<<48)-1), 65535),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.hlc.IsAtMaxValue()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHLC_EdgeCases(t *testing.T) {
	t.Run("zero physical time and counter", func(t *testing.T) {
		hlc := NewHLCWithTime(0, 0)
		assert.Equal(t, int64(0), hlc.PhysicalTime())
		assert.Equal(t, uint16(0), hlc.LogicalCounter())
	})

	t.Run("max logical counter", func(t *testing.T) {
		hlc := NewHLCWithTime(1234567890, 65535)
		assert.Equal(t, uint16(65535), hlc.LogicalCounter())
	})

	t.Run("negative physical time", func(t *testing.T) {
		hlc := NewHLCWithTime(-1, 0)
		assert.Equal(t, int64(-1), hlc.PhysicalTime())
	})
}
