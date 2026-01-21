// Package identity 标识符生成器单元测试
package identity

import (
	"sync"
	"testing"
)

// TestGenerateNodeID 测试 NodeID 生成的一致性
func TestGenerateNodeID(t *testing.T) {
	tests := []struct {
		name       string
		listenAddr string
	}{
		{"仅 TCP", "127.0.0.1:9211:0"},
		{"仅 UDP", "127.0.0.1:0:9212"},
		{"TCP+UDP", "127.0.0.1:9211:9212"},
		{"不同主机", "192.168.1.100:9211:9212"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id1 := GenerateNodeID(tt.listenAddr)
			id2 := GenerateNodeID(tt.listenAddr)

			// 相同输入应生成相同 ID
			if id1 != id2 {
				t.Errorf("GenerateNodeID(%q) 不一致: %d != %d", tt.listenAddr, id1, id2)
			}

			// ID 不应为 0
			if id1 == 0 {
				t.Errorf("GenerateNodeID(%q) 返回 0", tt.listenAddr)
			}
		})
	}
}

// TestGenerateNodeIDUniqueness 测试 NodeID 唯一性
func TestGenerateNodeIDUniqueness(t *testing.T) {
	addrs := []string{
		"127.0.0.1:9211:0",
		"127.0.0.1:0:9212",
		"127.0.0.1:9211:9212",
		"192.168.1.1:9211:9212",
		"localhost:9211:9212",
	}

	ids := make(map[uint64]string)
	for _, addr := range addrs {
		id := GenerateNodeID(addr)
		if existing, exists := ids[id]; exists {
			t.Errorf("NodeID 冲突: %q 和 %q 生成相同 ID %d", existing, addr, id)
		}
		ids[id] = addr
	}
}

// TestGenerateNodeIDFromPorts 测试从端口生成 NodeID
func TestGenerateNodeIDFromPorts(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		tcpPort int
		udpPort int
	}{
		{"仅 TCP", "127.0.0.1", 9211, 0},
		{"仅 UDP", "127.0.0.1", 0, 9212},
		{"TCP+UDP", "127.0.0.1", 9211, 9212},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := GenerateNodeIDFromPorts(tt.host, tt.tcpPort, tt.udpPort)

			// ID 不应为 0
			if id == 0 {
				t.Errorf("GenerateNodeIDFromPorts(%s, %d, %d) 返回 0", tt.host, tt.tcpPort, tt.udpPort)
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

// BenchmarkGenerateNodeID 性能测试
func BenchmarkGenerateNodeID(b *testing.B) {
	addr := "127.0.0.1:9211:9212"
	for i := 0; i < b.N; i++ {
		GenerateNodeID(addr)
	}
}

// BenchmarkMsgSeqGeneratorNext 性能测试
func BenchmarkMsgSeqGeneratorNext(b *testing.B) {
	gen := NewMsgSeqGenerator()
	for i := 0; i < b.N; i++ {
		gen.Next()
	}
}
