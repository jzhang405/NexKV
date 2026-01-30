// Package config 种子节点配置解析与验证
//
// PR-036: 支持从配置文件和环境变量获取种子节点地址列表
// 使用 IPFS multiaddr 格式: /ip4/<IP>/tcp/<PORT>
package config

import (
	"fmt"
	"strings"

	"github.com/multiformats/go-multiaddr"
)

// ParseSeedNodes 解析种子节点配置
//
// 支持格式：
//   - []string: ["/ip4/127.0.0.1/tcp/7946", "/ip4/127.0.0.1/tcp/7947"]
//   - string: "/ip4/127.0.0.1/tcp/7946,/ip4/127.0.0.1/tcp/7947"
//
// 返回规范化的地址列表（去重、去空）
func ParseSeedNodes(config interface{}) ([]string, error) {
	var nodes []string

	switch v := config.(type) {
	case []string:
		nodes = v
	case string:
		nodes = splitSeedNodesString(v)
	case []interface{}:
		// YAML 解析可能返回 []interface{}
		nodes = make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				nodes = append(nodes, str)
			}
		}
	default:
		return nil, fmt.Errorf("不支持的种子节点配置类型: %T", config)
	}

	// 规范化地址列表
	normalized := NormalizeSeedNodes(nodes)

	// 验证每个地址
	for _, addr := range normalized {
		if err := ValidateSeedNodeAddress(addr); err != nil {
			return nil, fmt.Errorf("种子节点地址 %s 验证失败: %w", addr, err)
		}
	}

	return normalized, nil
}

// ValidateSeedNodeAddress 验证单个地址格式
//
// 要求：IPFS multiaddr 格式
// 支持的格式：
//   - IPv4: /ip4/192.168.1.10/tcp/9211
//   - IPv6: /ip6/::1/tcp/9211
//   - DNS:  /dns4/localhost/tcp/9211
//
// 验证规则：
//   - 必须是有效的 multiaddr 格式
//   - 必须包含 TCP 协议组件
//   - TCP 端口必须在 1-65535 范围内
func ValidateSeedNodeAddress(addr string) error {
	// 去除首尾空格
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("地址不能为空")
	}

	// 验证以 / 开头
	if !strings.HasPrefix(addr, "/") {
		return fmt.Errorf("multiaddr 格式必须以 / 开头")
	}

	// 使用 multiaddr 库解析和验证
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("无效的 multiaddr 格式: %w", err)
	}

	// 检查是否包含 TCP 组件
	addrStr := maddr.String()
	if !strings.Contains(addrStr, "/tcp/") {
		return fmt.Errorf("地址必须包含 TCP 协议组件（如 /tcp/<PORT>）")
	}

	// 验证 TCP 端口范围
	if err := validateTCPPortFromString(addrStr); err != nil {
		return err
	}

	return nil
}

// validateTCPPortFromString 从字符串中提取并验证 TCP 端口
func validateTCPPortFromString(addr string) error {
	// 查找 /tcp/ 的位置
	tcpIdx := strings.Index(addr, "/tcp/")
	if tcpIdx == -1 {
		return fmt.Errorf("未找到 TCP 协议组件")
	}

	// 提取 /tcp/ 后面的部分
	afterTCP := addr[tcpIdx+5:] // +5 跳过 "/tcp/"
	if afterTCP == "" {
		return fmt.Errorf("TCP 组件后必须跟端口号")
	}

	// multiaddr 可能还有其他组件，只取到下一个 / 或字符串结尾
	endIdx := strings.Index(afterTCP, "/")
	if endIdx == -1 {
		endIdx = len(afterTCP)
	}
	portStr := afterTCP[:endIdx]

	// 解析端口
	var port int
	_, err := fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		return fmt.Errorf("无效的 TCP 端口值: %s", portStr)
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf("TCP 端口必须在 1-65535 范围内，当前值: %d", port)
	}

	return nil
}

// NormalizeSeedNodes 规范化地址列表
//
// 执行：
//   - 去重（保留首次出现顺序）
//   - 去空（移除空字符串）
//   - 去空格（去除首尾空格）
func NormalizeSeedNodes(nodes []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(nodes))

	for _, node := range nodes {
		// 去除首尾空格
		addr := strings.TrimSpace(node)

		// 跳过空字符串
		if addr == "" {
			continue
		}

		// 去重
		if !seen[addr] {
			seen[addr] = true
			result = append(result, addr)
		}
	}

	return result
}

// splitSeedNodesString 分割逗号分隔的字符串
//
// 支持格式：
//   - "/ip4/127.0.0.1/tcp/7946,/ip4/127.0.0.1/tcp/7947"
//   - 支持逗号后空格
func splitSeedNodesString(s string) []string {
	if s == "" {
		return []string{}
	}

	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		addr := strings.TrimSpace(part)
		if addr != "" {
			result = append(result, addr)
		}
	}

	return result
}
