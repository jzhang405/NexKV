package bftree

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPageCorruptError 测试页面损坏错误
func TestPageCorruptError(t *testing.T) {
	t.Run("Error message", func(t *testing.T) {
		err := &PageCorruptError{
			PageID: 123,
			Reason: "invalid checksum",
		}
		assert.Equal(t, "page 123 corrupted: invalid checksum", err.Error())
	})

	t.Run("Is method", func(t *testing.T) {
		err1 := &PageCorruptError{PageID: 1, Reason: "test"}
		err2 := &PageCorruptError{PageID: 2, Reason: "other"}

		assert.True(t, errors.Is(err1, &PageCorruptError{}))
		assert.True(t, errors.Is(err2, &PageCorruptError{}))
		assert.False(t, errors.Is(err1, errors.New("other")))
	})

	t.Run("NewPageCorruptError", func(t *testing.T) {
		err := NewPageCorruptError(456, "data corruption")
		pageErr, ok := err.(*PageCorruptError)
		require.True(t, ok)
		assert.Equal(t, uint64(456), pageErr.PageID)
		assert.Equal(t, "data corruption", pageErr.Reason)
	})
}

// TestInvalidPageLevelError 测试无效页面级别错误
func TestInvalidPageLevelError(t *testing.T) {
	t.Run("Error message", func(t *testing.T) {
		err := &InvalidPageLevelError{
			Current: L1,
			Target:  Full,
		}
		assert.Equal(t, "invalid page level: current=L1(64B), target=Full(4KB)", err.Error())
	})

	t.Run("Is method", func(t *testing.T) {
		err1 := &InvalidPageLevelError{Current: L1, Target: L2}
		err2 := &InvalidPageLevelError{Current: L3, Target: L4}

		assert.True(t, errors.Is(err1, &InvalidPageLevelError{}))
		assert.True(t, errors.Is(err2, &InvalidPageLevelError{}))
		assert.False(t, errors.Is(err1, errors.New("other")))
	})

	t.Run("NewInvalidPageLevelError", func(t *testing.T) {
		err := NewInvalidPageLevelError(L2, L5)
		levelErr, ok := err.(*InvalidPageLevelError)
		require.True(t, ok)
		assert.Equal(t, L2, levelErr.Current)
		assert.Equal(t, L5, levelErr.Target)
	})
}

// TestChecksumError 测试校验和错误
func TestChecksumError(t *testing.T) {
	t.Run("Error message", func(t *testing.T) {
		err := &ChecksumError{
			Expected: 12345,
			Actual:   54321,
		}
		assert.Equal(t, "checksum mismatch: expected=12345, actual=54321", err.Error())
	})

	t.Run("Is method", func(t *testing.T) {
		err1 := &ChecksumError{Expected: 100, Actual: 200}
		err2 := &ChecksumError{Expected: 300, Actual: 400}

		assert.True(t, errors.Is(err1, &ChecksumError{}))
		assert.True(t, errors.Is(err2, &ChecksumError{}))
		assert.False(t, errors.Is(err1, errors.New("other")))
	})

	t.Run("NewChecksumError", func(t *testing.T) {
		err := NewChecksumError(9999, 8888)
		sumErr, ok := err.(*ChecksumError)
		require.True(t, ok)
		assert.Equal(t, uint32(9999), sumErr.Expected)
		assert.Equal(t, uint32(8888), sumErr.Actual)
	})
}
