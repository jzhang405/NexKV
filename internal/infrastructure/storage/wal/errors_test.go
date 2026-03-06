// Package wal 的错误测试
package wal

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWALErrors(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		check func(t *testing.T, err error)
	}{
		{
			name: "ErrWALClosed",
			err:  ErrWALClosed,
			check: func(t *testing.T, err error) {
				assert.Error(t, err)
				assert.True(t, IsWALClosed(err))
				assert.False(t, IsWALCorrupted(err))
			},
		},
		{
			name: "ErrWALCorrupted",
			err:  ErrWALCorrupted,
			check: func(t *testing.T, err error) {
				assert.Error(t, err)
				assert.False(t, IsWALClosed(err))
				assert.True(t, IsWALCorrupted(err))
			},
		},
		{
			name: "ErrWALEntryCorrupted",
			err:  ErrWALEntryCorrupted,
			check: func(t *testing.T, err error) {
				assert.Error(t, err)
				assert.False(t, IsWALClosed(err))
				assert.True(t, IsWALCorrupted(err))
			},
		},
		{
			name: "ErrWALChecksumMismatch",
			err:  ErrWALChecksumMismatch,
			check: func(t *testing.T, err error) {
				assert.Error(t, err)
				assert.False(t, IsWALClosed(err))
				assert.True(t, IsWALCorrupted(err))
			},
		},
		{
			name: "ErrWALLSNGap",
			err:  ErrWALLSNGap,
			check: func(t *testing.T, err error) {
				assert.Error(t, err)
			},
		},
		{
			name: "ErrInvalidWALConfig",
			err:  ErrInvalidWALConfig,
			check: func(t *testing.T, err error) {
				assert.Error(t, err)
			},
		},
		{
			name: "ErrWALSegmentFull",
			err:  ErrWALSegmentFull,
			check: func(t *testing.T, err error) {
				assert.Error(t, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, tt.err)
		})
	}
}

func TestIsWALClosed(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "ErrWALClosed",
			err:      ErrWALClosed,
			expected: true,
		},
		{
			name:     "wrapped ErrWALClosed",
			err:      fmt.Errorf("wrapped: %w", ErrWALClosed),
			expected: true,
		},
		{
			name:     "other error",
			err:      ErrWALCorrupted,
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsWALClosed(tt.err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestIsWALCorrupted(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "ErrWALCorrupted",
			err:      ErrWALCorrupted,
			expected: true,
		},
		{
			name:     "ErrWALEntryCorrupted",
			err:      ErrWALEntryCorrupted,
			expected: true,
		},
		{
			name:     "ErrWALChecksumMismatch",
			err:      ErrWALChecksumMismatch,
			expected: true,
		},
		{
			name:     "wrapped corrupted error",
			err:      fmt.Errorf("wrapped: %w", ErrWALCorrupted),
			expected: true,
		},
		{
			name:     "other error",
			err:      ErrWALClosed,
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsWALCorrupted(tt.err)
			assert.Equal(t, tt.expected, got)
		})
	}
}
