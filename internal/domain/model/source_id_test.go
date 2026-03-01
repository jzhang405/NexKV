// Package model 定义领域模型
package model

import (
	"testing"
)

func TestParseSourceID(t *testing.T) {
	tests := []struct {
		input       string
		expectError bool
	}{
		{"hlc:clock:tick", false},
		{"wal:writer:flush", false},
		{"rpc:client:send", false},
		{"", true},                    // 空字符串
		{"invalid", true},             // 缺少冒号
		{"only:two", true},            // 只有两部分
		{"too:many:parts:here", true}, // 超过三部分
		{":empty:module", true},       // 空模块名
		{"module::action", true},      // 空子模块名
		{"module:sub:", true},         // 空操作名
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := ParseSourceID(tt.input)
			if tt.expectError {
				if err == nil {
					t.Errorf("ParseSourceID(%s) expected error, got nil", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("ParseSourceID(%s) unexpected error: %v", tt.input, err)
				}
			}
		})
	}
}

func TestSourceID_Accessors(t *testing.T) {
	sourceID, _ := ParseSourceID("hlc:clock:tick")

	// 测试 Module()
	if got := sourceID.Module(); got != "hlc" {
		t.Errorf("Module() = %v, want hlc", got)
	}

	// 测试 SubModule()
	if got := sourceID.SubModule(); got != "clock" {
		t.Errorf("SubModule() = %v, want clock", got)
	}

	// 测试 Action()
	if got := sourceID.Action(); got != "tick" {
		t.Errorf("Action() = %v, want tick", got)
	}

	// 测试 String()
	if got := sourceID.String(); got != "hlc:clock:tick" {
		t.Errorf("String() = %v, want hlc:clock:tick", got)
	}
}

func TestSourceID_Equals(t *testing.T) {
	id1, _ := ParseSourceID("hlc:clock:tick")
	id2, _ := ParseSourceID("hlc:clock:tick")
	id3, _ := ParseSourceID("hlc:clock:tock")
	id4, _ := ParseSourceID("wal:clock:tick")

	tests := []struct {
		name     string
		a, b     SourceID
		expected bool
	}{
		{"same", id1, id2, true},
		{"different action", id1, id3, false},
		{"different module", id1, id4, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Equals(tt.b); got != tt.expected {
				t.Errorf("Equals() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestSourceID_Match(t *testing.T) {
	sourceID, _ := ParseSourceID("hlc:clock:tick")

	tests := []struct {
		pattern  string
		expected bool
	}{
		{"hlc:clock:tick", true},        // 完全匹配
		{"hlc:clock:*", true},           // 通配符匹配
		{"hlc:*:tick", true},            // 中间通配符
		{"*:clock:tick", true},          // 开头通配符
		{"hlc:*:*", true},               // 多个通配符
		{"*:*:*", true},                 // 全通配符
		{"wal:clock:tick", false},       // 模块不匹配
		{"hlc:writer:tick", false},      // 子模块不匹配
		{"hlc:clock:tock", false},       // 操作不匹配
		{"hlc:clock", false},            // 模式太短
		{"hlc:clock:tick:extra", false}, // 模式太长
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			if got := sourceID.Match(tt.pattern); got != tt.expected {
				t.Errorf("Match(%s) = %v, want %v", tt.pattern, got, tt.expected)
			}
		})
	}
}

func TestSourceID_RecommendedMode(t *testing.T) {
	tests := []struct {
		sourceID     string
		expectedMode TaskMode
	}{
		// Per-Core 模式
		{"hlc:clock:tick", ModePerCore},
		{"wal:writer:flush", ModePerCore},
		{"transaction:matrix:compute", ModePerCore},
		{"replication:sync:send", ModePerCore},

		// DefaultPool 模式（所有其他场景）
		{"rpc:client:send", ModeDefaultPool},
		{"rpc:server:handle", ModeDefaultPool},
		{"network:send:packet", ModeDefaultPool},
		{"network:recv:data", ModeDefaultPool},
		{"query:range:scan", ModeDefaultPool},
		{"query:point:get", ModeDefaultPool},
		{"shard:write:data", ModeDefaultPool},
		{"background:log:flush", ModeDefaultPool},
		{"background:metric:collect", ModeDefaultPool},
		{"log:write:entry", ModeDefaultPool},
		{"metric:collect:counter", ModeDefaultPool},
		{"cleanup:gc:run", ModeDefaultPool},

		// DefaultPool 模式
		{"test:temp:task", ModeDefaultPool},
		{"unknown:module:action", ModeDefaultPool},
	}

	for _, tt := range tests {
		t.Run(tt.sourceID, func(t *testing.T) {
			sourceID, err := ParseSourceID(tt.sourceID)
			if err != nil {
				t.Fatalf("ParseSourceID(%s) error: %v", tt.sourceID, err)
			}

			gotMode := sourceID.RecommendedMode()
			if gotMode != tt.expectedMode {
				t.Errorf("RecommendedMode() = %v, want %v", gotMode, tt.expectedMode)
			}
		})
	}
}

func TestSourceID_Validate(t *testing.T) {
	// 有效 SourceID
	validIDs := []string{
		"hlc:clock:tick",
		"wal:writer:flush",
		"rpc:client:send",
		"query:range:scan",
		"background:log:flush",
		"test:temp:task",
	}

	for _, id := range validIDs {
		t.Run("valid_"+id, func(t *testing.T) {
			sourceID, err := ParseSourceID(id)
			if err != nil {
				t.Fatalf("ParseSourceID(%s) error: %v", id, err)
			}
			if err := sourceID.Validate(); err != nil {
				t.Errorf("Validate() error: %v", err)
			}
		})
	}

	// 测试 MustParseSourceID
	t.Run("MustParseSourceID", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("MustParseSourceID should panic for invalid input")
			}
		}()
		_ = MustParseSourceID("invalid")
	})
}

func TestSourceID_IsHighPriority(t *testing.T) {
	tests := []struct {
		sourceID string
		isHigh   bool
	}{
		{"hlc:clock:tick", true},
		{"wal:writer:flush", true},
		{"replication:sync:send", true},
		{"rpc:client:send", false},
		{"query:range:scan", false},
		{"background:log:flush", false},
	}

	for _, tt := range tests {
		t.Run(tt.sourceID, func(t *testing.T) {
			sourceID, _ := ParseSourceID(tt.sourceID)
			if got := sourceID.IsHighPriority(); got != tt.isHigh {
				t.Errorf("IsHighPriority() = %v, want %v", got, tt.isHigh)
			}
		})
	}
}

func TestSourceID_IsBackground(t *testing.T) {
	tests := []struct {
		sourceID     string
		isBackground bool
	}{
		{"background:log:flush", true},
		{"background:metric:collect", true},
		{"log:write:entry", true},
		{"metric:collect:counter", true},
		{"cleanup:gc:run", true},
		{"hlc:clock:tick", false},
		{"wal:writer:flush", false},
		{"rpc:client:send", false},
	}

	for _, tt := range tests {
		t.Run(tt.sourceID, func(t *testing.T) {
			sourceID, _ := ParseSourceID(tt.sourceID)
			if got := sourceID.IsBackground(); got != tt.isBackground {
				t.Errorf("IsBackground() = %v, want %v", got, tt.isBackground)
			}
		})
	}
}

func TestSourceID_Hash(t *testing.T) {
	id1, _ := ParseSourceID("hlc:clock:tick")
	id2, _ := ParseSourceID("hlc:clock:tick")
	id3, _ := ParseSourceID("hlc:clock:tock")

	// 相同 SourceID 应该有相同 Hash
	if id1.Hash() != id2.Hash() {
		t.Error("Same SourceID should have same hash")
	}

	// 不同 SourceID 应该有不同 Hash
	if id1.Hash() == id3.Hash() {
		t.Error("Different SourceID should have different hash")
	}

	// Hash 应该等于 String()
	if id1.Hash() != id1.String() {
		t.Errorf("Hash() = %v, want %v", id1.Hash(), id1.String())
	}
}

// 基准测试
func BenchmarkParseSourceID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = ParseSourceID("hlc:clock:tick")
	}
}

func BenchmarkSourceID_Match(b *testing.B) {
	sourceID, _ := ParseSourceID("hlc:clock:tick")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sourceID.Match("hlc:*:tick")
	}
}

func BenchmarkSourceID_RecommendedMode(b *testing.B) {
	sourceID, _ := ParseSourceID("hlc:clock:tick")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sourceID.RecommendedMode()
	}
}
