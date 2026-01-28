// Package identity 标识符生成器单元测试
package identity

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateNodeIDFromPorts_Consistency 测试 NodeID 生成的一致性
func TestGenerateNodeIDFromPorts_Consistency(t *testing.T) {
	tests := []struct {
		name    string
		tcpPort int
		udpPort int
	}{
		{"仅 TCP", 9211, 0},
		{"仅 UDP", 0, 9212},
		{"TCP+UDP", 9211, 9212},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id1, err := GenerateNodeIDFromPorts(tt.tcpPort, tt.udpPort)
			if err != nil {
				t.Fatalf("GenerateNodeIDFromPorts(%d, %d) 失败: %v", tt.tcpPort, tt.udpPort, err)
			}

			id2, err := GenerateNodeIDFromPorts(tt.tcpPort, tt.udpPort)
			if err != nil {
				t.Fatalf("GenerateNodeIDFromPorts(%d, %d) 失败: %v", tt.tcpPort, tt.udpPort, err)
			}

			// 相同输入应生成相同 ID
			if id1 != id2 {
				t.Errorf("GenerateNodeIDFromPorts(%d, %d) 不一致: %d != %d", tt.tcpPort, tt.udpPort, id1, id2)
			}

			// ID 不应为 0
			if id1 == 0 {
				t.Errorf("GenerateNodeIDFromPorts(%d, %d) 返回 0", tt.tcpPort, tt.udpPort)
			}
		})
	}
}

// TestGenerateNodeIDFromPorts_Uniqueness 测试 NodeID 唯一性
func TestGenerateNodeIDFromPorts_Uniqueness(t *testing.T) {
	tests := []struct {
		tcpPort int
		udpPort int
	}{
		{9211, 0},
		{0, 9212},
		{9211, 9212},
	}

	ids := make(map[uint64]string)
	for _, tt := range tests {
		id, err := GenerateNodeIDFromPorts(tt.tcpPort, tt.udpPort)
		if err != nil {
			t.Fatalf("GenerateNodeIDFromPorts(%d, %d) 失败: %v", tt.tcpPort, tt.udpPort, err)
		}
		key := strconv.Itoa(tt.tcpPort) + ":" + strconv.Itoa(tt.udpPort)
		if existing, exists := ids[id]; exists {
			t.Errorf("NodeID 冲突: %q 和 %q 生成相同 ID %d", existing, key, id)
		}
		ids[id] = key
	}
}

// TestGenerateNodeIDFromPorts 测试从端口生成 NodeID
func TestGenerateNodeIDFromPorts(t *testing.T) {
	tests := []struct {
		name    string
		tcpPort int
		udpPort int
	}{
		{"仅 TCP", 9211, 0},
		{"仅 UDP", 0, 9212},
		{"TCP+UDP", 9211, 9212},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := GenerateNodeIDFromPorts(tt.tcpPort, tt.udpPort)
			if err != nil {
				t.Fatalf("GenerateNodeIDFromPorts(%d, %d) 失败: %v", tt.tcpPort, tt.udpPort, err)
			}

			// ID 不应为 0
			if id == 0 {
				t.Errorf("GenerateNodeIDFromPorts(%d, %d) 返回 0", tt.tcpPort, tt.udpPort)
			}
		})
	}
}

// TestNewMsgSeqGenerator 测试 MsgSeq 生成器初始化
func TestNewMsgSeqGenerator(t *testing.T) {
	gen1 := NewMsgSeqGenerator()

	// 短暂等待确保时间戳不同
	// time.Sleep(time.Microsecond)

	gen2 := NewMsgSeqGenerator()

	// gen2 初始值应该 >= gen1 初始值（时间单调递增）
	if gen2.Current() < gen1.Current() {
		t.Errorf("NewMsgSeqGenerator() 不单调: %d < %d", gen2.Current(), gen1.Current())
	}

	// 序列号不应为 0
	if gen1.Current() == 0 || gen2.Current() == 0 {
		t.Errorf("NewMsgSeqGenerator() 返回 0")
	}
}

// TestMsgSeqGeneratorNext 测试序列号递增
func TestMsgSeqGeneratorNext(t *testing.T) {
	gen := NewMsgSeqGenerator()
	initial := gen.Current()

	// 生成 1000 个序列号
	const count = 1000
	for i := 0; i < count; i++ {
		seq := gen.Next()
		expected := initial + uint64(i+1)

		if seq != expected {
			t.Errorf("Next() = %d, 期望 %d", seq, expected)
		}
	}

	// 验证最终值
	final := gen.Current()
	if final != initial+count {
		t.Errorf("Current() = %d, 期望 %d", final, initial+count)
	}
}

// TestMsgSeqGeneratorConcurrency 测试并发安全
func TestMsgSeqGeneratorConcurrency(t *testing.T) {
	gen := NewMsgSeqGenerator()
	const goroutines = 100
	const callsPerGoroutine = 100

	// 记录初始值
	initial := gen.Current()

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < callsPerGoroutine; j++ {
				gen.Next()
			}
		}()
	}

	wg.Wait()

	// 验证序列号递增了正确的次数
	expected := initial + uint64(goroutines*callsPerGoroutine)
	if gen.Current() != expected {
		t.Errorf("Current() = %d, 期望 %d", gen.Current(), expected)
	}
}

// BenchmarkGenerateNodeIDFromPorts 性能测试
func BenchmarkGenerateNodeIDFromPorts(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = GenerateNodeIDFromPorts(9211, 9212)
	}
}

// BenchmarkMsgSeqGeneratorNext 性能测试
func BenchmarkMsgSeqGeneratorNext(b *testing.B) {
	gen := NewMsgSeqGenerator()
	for i := 0; i < b.N; i++ {
		gen.Next()
	}
}

// TestGenerateNodeIDFromPorts_PortValidation 测试端口验证
func TestGenerateNodeIDFromPorts_PortValidation(t *testing.T) {
	tests := []struct {
		name        string
		tcpPort     int
		udpPort     int
		wantErr     bool
		errContains string
	}{
		{
			name:        "两个端口都为 0",
			tcpPort:     0,
			udpPort:     0,
			wantErr:     true,
			errContains: "至少需要启用一个端口",
		},
		{
			name:        "TCP 端口为负数",
			tcpPort:     -1,
			udpPort:     9212,
			wantErr:     true,
			errContains: "TCP 端口无效",
		},
		{
			name:        "UDP 端口为负数",
			tcpPort:     9211,
			udpPort:     -1,
			wantErr:     true,
			errContains: "UDP 端口无效",
		},
		{
			name:        "TCP 端口超出范围",
			tcpPort:     65536,
			udpPort:     9212,
			wantErr:     true,
			errContains: "TCP 端口无效",
		},
		{
			name:        "UDP 端口超出范围",
			tcpPort:     9211,
			udpPort:     65536,
			wantErr:     true,
			errContains: "UDP 端口无效",
		},
		{
			name:        "两个端口都超出范围",
			tcpPort:     65536,
			udpPort:     65536,
			wantErr:     true,
			errContains: "TCP 端口无效",
		},
		{
			name:    "仅 TCP 端口有效",
			tcpPort: 9211,
			udpPort: 0,
			wantErr: false,
		},
		{
			name:    "仅 UDP 端口有效",
			tcpPort: 0,
			udpPort: 9212,
			wantErr: false,
		},
		{
			name:    "TCP 和 UDP 都有效",
			tcpPort: 9211,
			udpPort: 9212,
			wantErr: false,
		},
		{
			name:    "端口边界值（最小有效端口）",
			tcpPort: 1,
			udpPort: 0,
			wantErr: false,
		},
		{
			name:    "端口边界值（最大有效端口）",
			tcpPort: 0,
			udpPort: 65535,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := GenerateNodeIDFromPorts(tt.tcpPort, tt.udpPort)

			if tt.wantErr {
				require.Error(t, err, "GenerateNodeIDFromPorts() 应该返回错误")
				assert.Contains(t, err.Error(), tt.errContains, "错误信息应包含指定内容")
				assert.Equal(t, uint64(0), id, "错误时应返回 0")
			} else {
				require.NoError(t, err, "GenerateNodeIDFromPorts() 不应该返回错误")
				assert.NotZero(t, id, "ID 不应为 0")
			}
		})
	}
}
