package transport

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/jzhang405/NexKV/internal/metadata/cluster"
	"github.com/multiformats/go-multiaddr"
)

// NodeAddrToMultiaddr 将 NodeAddress 转换为 multiaddr
// proto: "tcp" 或 "udp"
func NodeAddrToMultiaddr(na *cluster.NodeAddress, proto string) (multiaddr.Multiaddr, error) {
	if na == nil {
		return nil, fmt.Errorf("NodeAddress 不能为空")
	}

	var components []string

	// 判断是 IP 还是 hostname
	if ip := net.ParseIP(na.Host); ip != nil {
		// IP 地址
		if ip.To4() != nil {
			// IPv4
			components = append(components, "ip4", na.Host)
		} else {
			// IPv6
			components = append(components, "ip6", na.Host)
		}
	} else {
		// hostname
		components = append(components, "dns4", na.Host)
	}

	// 协议和端口
	switch proto {
	case "tcp":
		if na.TCPPort < 0 || na.TCPPort > 65535 {
			return nil, fmt.Errorf("无效的 TCP 端口: %d", na.TCPPort)
		}
		components = append(components, "tcp", strconv.Itoa(na.TCPPort))
	case "udp":
		if na.UDPPort < 0 || na.UDPPort > 65535 {
			return nil, fmt.Errorf("无效的 UDP 端口: %d", na.UDPPort)
		}
		components = append(components, "udp", strconv.Itoa(na.UDPPort))
	default:
		return nil, fmt.Errorf("不支持的协议: %s", proto)
	}

	// 拼接成 multiaddr 字符串格式: /ip4/192.168.1.1/tcp/4001
	return multiaddr.NewMultiaddr("/" + strings.Join(components, "/"))
}

// MultiaddrToNodeAddr 将 multiaddr 转换为 NodeAddress
func MultiaddrToNodeAddr(ma multiaddr.Multiaddr) (*cluster.NodeAddress, error) {
	if ma == nil {
		return nil, fmt.Errorf("multiaddr 不能为空")
	}

	na := &cluster.NodeAddress{}

	// 获取 IP 地址
	if ip, err := ma.ValueForProtocol(multiaddr.P_IP4); err == nil {
		na.Host = ip
	} else if ip, err := ma.ValueForProtocol(multiaddr.P_IP6); err == nil {
		na.Host = ip
	}

	// 获取 hostname（优先使用，可能覆盖 IP）
	if hostname, err := ma.ValueForProtocol(multiaddr.P_DNS4); err == nil {
		na.Host = hostname
	} else if hostname, err := ma.ValueForProtocol(multiaddr.P_DNS6); err == nil {
		na.Host = hostname
	}

	// 如果既没有 IP 也没有 hostname，返回错误
	if na.Host == "" {
		return nil, fmt.Errorf("multiaddr 中没有有效的地址信息")
	}

	// 获取 TCP 端口
	if portStr, err := ma.ValueForProtocol(multiaddr.P_TCP); err == nil {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("无效的 TCP 端口: %s", portStr)
		}
		if port < 0 || port > 65535 {
			return nil, fmt.Errorf("TCP 端口超出范围: %d", port)
		}
		na.TCPPort = port
	}

	// 获取 UDP 端口
	if portStr, err := ma.ValueForProtocol(multiaddr.P_UDP); err == nil {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("无效的 UDP 端口: %s", portStr)
		}
		if port < 0 || port > 65535 {
			return nil, fmt.Errorf("UDP 端口超出范围: %d", port)
		}
		na.UDPPort = port
	}

	// 至少需要有一个端口
	if na.TCPPort == 0 && na.UDPPort == 0 {
		return nil, fmt.Errorf("multiaddr 中没有有效的端口信息")
	}

	return na, nil
}

// ExtractHostname 提取 hostname（如果有）
// 返回 hostname 和是否找到的标志
func ExtractHostname(ma multiaddr.Multiaddr) (string, bool) {
	if ma == nil {
		return "", false
	}

	// 优先查找 DNS4
	if hostname, err := ma.ValueForProtocol(multiaddr.P_DNS4); err == nil {
		return hostname, true
	}

	// 其次查找 DNS6
	if hostname, err := ma.ValueForProtocol(multiaddr.P_DNS6); err == nil {
		return hostname, true
	}

	return "", false
}

// ParseHostname 解析 hostname 为 multiaddr
func ParseHostname(hostname string, port int) (multiaddr.Multiaddr, error) {
	if hostname == "" {
		return nil, fmt.Errorf("hostname 不能为空")
	}

	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("无效的端口号: %d", port)
	}

	return multiaddr.NewMultiaddr(fmt.Sprintf("/dns4/%s/tcp/%d", hostname, port))
}
