// Package wal 的单元测试
package wal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWALType_String(t *testing.T) {
	tests := []struct {
		name     string
		walType  WALType
		expected string
	}{
		{
			name:     "Insert",
			walType:  WALTypeInsert,
			expected: "Insert",
		},
		{
			name:     "Update",
			walType:  WALTypeUpdate,
			expected: "Update",
		},
		{
			name:     "Delete",
			walType:  WALTypeDelete,
			expected: "Delete",
		},
		{
			name:     "Commit",
			walType:  WALTypeCommit,
			expected: "Commit",
		},
		{
			name:     "Rollback",
			walType:  WALTypeRollback,
			expected: "Rollback",
		},
		{
			name:     "Checkpoint",
			walType:  WALTypeCheckpoint,
			expected: "Checkpoint",
		},
		{
			name:     "Split",
			walType:  WALTypeSplit,
			expected: "Split",
		},
		{
			name:     "Unknown",
			walType:  WALType(99),
			expected: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WALTypeString(tt.walType)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestNewWALEntry(t *testing.T) {
	key := []byte("test-key")
	value := []byte("test-value")
	entry := NewWALEntry(WALTypeInsert, 123, key, value, LSN(100))

	assert.Equal(t, WALTypeInsert, entry.Type)
	assert.Equal(t, uint64(123), entry.TxID)
	assert.Equal(t, key, entry.Key)
	assert.Equal(t, value, entry.Value)
	assert.Equal(t, LSN(100), entry.PrevLSN)
	assert.Greater(t, entry.Timestamp, int64(0))
}

func TestWALEntry_Marshal_Unmarshal(t *testing.T) {
	tests := []struct {
		name    string
		entry   *WALEntry
		wantErr bool
	}{
		{
			name: "valid entry with key and value",
			entry: &WALEntry{
				LSN:       LSN(1),
				TxID:      100,
				Timestamp: 1234567890,
				Type:      WALTypeInsert,
				Key:       []byte("test-key"),
				Value:     []byte("test-value"),
				PrevLSN:   LSNInvalid,
			},
			wantErr: false,
		},
		{
			name: "valid entry with key only",
			entry: &WALEntry{
				LSN:       LSN(2),
				TxID:      101,
				Timestamp: 1234567891,
				Type:      WALTypeDelete,
				Key:       []byte("delete-key"),
				Value:     nil,
				PrevLSN:   LSN(1),
			},
			wantErr: false,
		},
		{
			name: "valid entry with empty key and value",
			entry: &WALEntry{
				LSN:       LSN(3),
				TxID:      102,
				Timestamp: 1234567892,
				Type:      WALTypeCommit,
				Key:       nil,
				Value:     nil,
				PrevLSN:   LSN(2),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal
			data, err := MarshalWALEntry(tt.entry)
			if (err != nil) != tt.wantErr {
				t.Errorf("Marshal() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			// Unmarshal
			unentry := &WALEntry{}
			err = UnmarshalWALEntry(unentry, data)
			if err != nil {
				t.Errorf("Unmarshal() error = %v", err)
				return
			}

			// 验证字段
			assert.Equal(t, tt.entry.LSN, unentry.LSN)
			assert.Equal(t, tt.entry.TxID, unentry.TxID)
			assert.Equal(t, tt.entry.Timestamp, unentry.Timestamp)
			assert.Equal(t, tt.entry.Type, unentry.Type)
			assert.Equal(t, tt.entry.Key, unentry.Key)
			assert.Equal(t, tt.entry.Value, unentry.Value)
			assert.Equal(t, tt.entry.PrevLSN, unentry.PrevLSN)
		})
	}
}

func TestWALEntry_Unmarshal_InvalidChecksum(t *testing.T) {
	// 创建有效数据
	entry := &WALEntry{
		LSN:       LSN(1),
		TxID:      100,
		Timestamp: 1234567890,
		Type:      WALTypeInsert,
		Key:       []byte("test-key"),
		Value:     []byte("test-value"),
		PrevLSN:   LSNInvalid,
	}

	data, err := MarshalWALEntry(entry)
	assert.NoError(t, err)

	// 破坏 CRC
	if len(data) >= 4 {
		data[0] = ^data[0]
		data[1] = ^data[1]
		data[2] = ^data[2]
		data[3] = ^data[3]
	}

	// Unmarshal 应该返回校验和错误
	unentry := &WALEntry{}
	err = UnmarshalWALEntry(unentry, data)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrWALChecksumMismatch)
}

func TestWALEntry_Unmarshal_TruncatedData(t *testing.T) {
	// 数据太短，无法解析
	shortData := []byte{1, 2, 3}

	entry := &WALEntry{}
	err := UnmarshalWALEntry(entry, shortData)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrWALEntryCorrupted)
}

func TestWALConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *WALConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: &WALConfig{
				Dir:         "/tmp/wal",
				SegmentSize: 64 * 1024 * 1024,
				SyncPolicy:  SyncPolicyEveryWrite,
			},
			wantErr: false,
		},
		{
			name: "empty dir",
			config: &WALConfig{
				Dir:         "",
				SegmentSize: 64 * 1024 * 1024,
			},
			wantErr: true,
		},
		{
			name: "segment size too small",
			config: &WALConfig{
				Dir:         "/tmp/wal",
				SegmentSize: 512 * 1024, // 512KB
			},
			wantErr: true,
		},
		{
			name: "minimum valid segment size",
			config: &WALConfig{
				Dir:         "/tmp/wal",
				SegmentSize: 1024 * 1024, // 1MB
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidWALConfig)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDefaultWALConfig(t *testing.T) {
	config := DefaultWALConfig()

	assert.Equal(t, int64(64*1024*1024), config.SegmentSize)
	assert.Equal(t, SyncPolicyEveryWrite, config.SyncPolicy)
}

func TestLSN_Constants(t *testing.T) {
	assert.Equal(t, LSN(0), LSNInvalid)
}
