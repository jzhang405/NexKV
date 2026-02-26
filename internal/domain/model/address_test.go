package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewSimpleAddress(t *testing.T) {
	addr := "192.168.1.1:8080"
	protocol := "tcp"

	simpleAddr := NewSimpleAddress(addr, protocol)

	assert.NotNil(t, simpleAddr)
	assert.Equal(t, addr, simpleAddr.String())
	assert.Equal(t, protocol, simpleAddr.Protocol())
}

func TestSimpleAddress_String(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		protocol string
		expected string
	}{
		{
			name:     "tcp address",
			addr:     "192.168.1.1:8080",
			protocol: "tcp",
			expected: "192.168.1.1:8080",
		},
		{
			name:     "quic address",
			addr:     "10.0.0.1:443",
			protocol: "quic",
			expected: "10.0.0.1:443",
		},
		{
			name:     "empty address",
			addr:     "",
			protocol: "tcp",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			simpleAddr := NewSimpleAddress(tt.addr, tt.protocol)
			result := simpleAddr.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSimpleAddress_Protocol(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		protocol string
		expected string
	}{
		{
			name:     "tcp protocol",
			addr:     "192.168.1.1:8080",
			protocol: "tcp",
			expected: "tcp",
		},
		{
			name:     "quic protocol",
			addr:     "10.0.0.1:443",
			protocol: "quic",
			expected: "quic",
		},
		{
			name:     "websocket protocol",
			addr:     "example.com:80",
			protocol: "ws",
			expected: "ws",
		},
		{
			name:     "empty protocol",
			addr:     "localhost:3000",
			protocol: "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			simpleAddr := NewSimpleAddress(tt.addr, tt.protocol)
			result := simpleAddr.Protocol()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSimpleAddress_InterfaceCompliance(t *testing.T) {
	simpleAddr := NewSimpleAddress("192.168.1.1:8080", "tcp")

	// 验证 SimpleAddress 实现了 NetworkAddress 接口
	var _ NetworkAddress = simpleAddr
}

func TestSimpleAddress_Embedding(t *testing.T) {
	type CustomAddress struct {
		*SimpleAddress
		extra string
	}

	simpleAddr := NewSimpleAddress("192.168.1.1:8080", "tcp")
	customAddr := &CustomAddress{
		SimpleAddress: simpleAddr,
		extra:         "custom-data",
	}

	assert.Equal(t, "192.168.1.1:8080", customAddr.String())
	assert.Equal(t, "tcp", customAddr.Protocol())
	assert.Equal(t, "custom-data", customAddr.extra)
}
