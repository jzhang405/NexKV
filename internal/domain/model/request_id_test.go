package model

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRequestID_String(t *testing.T) {
	id := RequestID("node-001-65d4a3f0-0001")
	assert.Equal(t, "node-001-65d4a3f0-0001", id.String())
}

func TestRequestID_IsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		id       RequestID
		expected bool
	}{
		{
			name:     "empty ID",
			id:       "",
			expected: true,
		},
		{
			name:     "non-empty ID",
			id:       "node-001-65d4a3f0-0001",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.id.IsEmpty()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRequestID_Validate(t *testing.T) {
	tests := []struct {
		name    string
		id      RequestID
		wantErr bool
	}{
		{
			name:    "empty ID",
			id:      "",
			wantErr: true,
		},
		{
			name:    "invalid format - too few parts (3 parts, simple nodeID)",
			id:      "node001-65d4a3f0-0001",
			wantErr: true, // Validate 期望 4 部分
		},
		{
			name:    "invalid format - too few parts (2 parts)",
			id:      "node001-65d4a3f0",
			wantErr: true,
		},
		{
			name:    "invalid format - too many parts (5 parts)",
			id:      "my-node-001-65d4a3f0-0001",
			wantErr: true,
		},
		{
			name:    "invalid timestamp",
			id:      "node-001-invalid-0001",
			wantErr: true,
		},
		{
			name:    "invalid sequence",
			id:      "node-001-65d4a3f0-invalid",
			wantErr: true,
		},
		{
			name:    "valid ID (4 parts - nodeID contains hyphen)",
			id:      "node-001-65d4a3f0-0001",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.id.Validate()

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRequestID_NodeID(t *testing.T) {
	tests := []struct {
		name     string
		id       RequestID
		expected string
	}{
		{
			name:     "simple node ID (4 parts)",
			id:       "node-001-65d4a3f0-0001",
			expected: "node-001", // 取最后两部分之前的所有部分
		},
		{
			name:     "invalid format - too few parts",
			id:       "invalid",
			expected: "",
		},
		{
			name:     "invalid format - 3 parts",
			id:       "node001-65d4a3f0-0001",
			expected: "node001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.id.NodeID()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRequestID_Timestamp(t *testing.T) {
	tests := []struct {
		name     string
		id       RequestID
		expected int64
	}{
		{
			name:     "valid timestamp",
			id:       "node-001-65d4a3f0-0001",
			expected: int64(0x65d4a3f0),
		},
		{
			name:     "zero timestamp",
			id:       "node-001-00000000-0001",
			expected: 0,
		},
		{
			name:     "invalid format",
			id:       "invalid",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.id.Timestamp()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRequestID_Sequence(t *testing.T) {
	tests := []struct {
		name     string
		id       RequestID
		expected uint32
	}{
		{
			name:     "valid sequence",
			id:       "node-001-65d4a3f0-0001",
			expected: 1,
		},
		{
			name:     "max sequence",
			id:       "node-001-65d4a3f0-ffff",
			expected: 65535,
		},
		{
			name:     "zero sequence",
			id:       "node-001-65d4a3f0-0000",
			expected: 0,
		},
		{
			name:     "invalid format",
			id:       "invalid",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.id.Sequence()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRequestID_Time(t *testing.T) {
	tests := []struct {
		name string
		id   RequestID
	}{
		{
			name: "valid timestamp",
			id:   "node-001-65d4a3f0-0001",
		},
		{
			name: "zero timestamp",
			id:   "node-001-00000000-0001",
		},
		{
			name: "invalid format",
			id:   "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.id.Time()
			ts := tt.id.Timestamp()

			if ts == 0 {
				assert.True(t, result.IsZero())
			} else {
				expectedTime := time.Unix(ts, 0)
				assert.Equal(t, expectedTime, result)
			}
		})
	}
}

func TestRequestID_Format(t *testing.T) {
	id := RequestID("test-node-65d4a3f0-0001")

	// Verify format: {NodeID}-{Timestamp:08x}-{Sequence:04x}
	parts := strings.Split(id.String(), "-")
	assert.GreaterOrEqual(t, len(parts), 4, "ID should have at least 4 parts")

	// Last two parts should be timestamp and sequence
	timestamp := parts[len(parts)-2]
	sequence := parts[len(parts)-1]

	// Timestamp should be 8 hex characters
	assert.Len(t, timestamp, 8, "Timestamp should be 8 characters")

	// Sequence should be 4 hex characters
	assert.Len(t, sequence, 4, "Sequence should be 4 characters")

	// Verify they are valid hex
	_, err := parseIntHex(timestamp)
	assert.NoError(t, err, "Timestamp should be valid hex")

	_, err = parseIntHex(sequence)
	assert.NoError(t, err, "Sequence should be valid hex")
}

// Helper function to parse hex string
func parseIntHex(s string) (int64, error) {
	var result int64
	for _, c := range s {
		result <<= 4
		switch {
		case c >= '0' && c <= '9':
			result |= int64(c - '0')
		case c >= 'a' && c <= 'f':
			result |= int64(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			result |= int64(c - 'A' + 10)
		default:
			return 0, assert.AnError
		}
	}
	return result, nil
}
