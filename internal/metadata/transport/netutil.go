// Package transport 传输层网络工具模块
//
// 提供 IP 地址选择、网卡绑定等网络相关功能
package transport

import (
	"fmt"
	"net"
	"strings"
)

// EnvType 环境类型
type EnvType int

const (
	// EnvDev 开发环境（强制使用 127.0.0.1）
	EnvDev EnvType = iota
	// EnvCluster 生产环境（自动选择私网 IP）
	EnvCluster
)

// String 返回环境类型的字符串表示
func (e EnvType) String() string {
	switch e {
	case EnvDev:
		return "dev"
	case EnvCluster:
		return "cluster"
	default:
		return "unknown"
	}
}

// ParseEnvType 解析环境类型字符串
func ParseEnvType(s string) (EnvType, error) {
	switch strings.ToLower(s) {
	case "dev":
		return EnvDev, nil
	case "cluster":
		return EnvCluster, nil
	default:
		return EnvDev, fmt.Errorf("无效的环境类型: %s（必须是 dev 或 cluster）", s)
	}
}

// NetworkConfig 网络配置
type NetworkConfig struct {
	Env      EnvType // 环境类型
	BindIP   string  // 绑定 IP
	BindPort int     // 绑定端口
	PublicIP string  // 公网 IP（可选）
	AutoIP   bool    // 是否自动选择 IP
}

// GetBindAddress 获取绑定地址
func (c *NetworkConfig) GetBindAddress() string {
	if c.BindIP != "" {
		return fmt.Sprintf("%s:%d", c.BindIP, c.BindPort)
	}
	return fmt.Sprintf(":%d", c.BindPort)
}

// SelectBindIP 选择绑定 IP
func SelectBindIP(env EnvType, userSpecifyIP string) (string, error) {
	// 1. 用户指定 IP 优先
	if userSpecifyIP != "" {
		// 验证用户指定的 IP 是否合法
		if net.ParseIP(userSpecifyIP) == nil {
			return "", fmt.Errorf("无效的 IP 地址: %s", userSpecifyIP)
		}
		return userSpecifyIP, nil
	}

	// 2. Dev 环境强制使用 127.0.0.1
	if env == EnvDev {
		return "127.0.0.1", nil
	}

	// 3. Cluster 环境自动选择第一个私网 IP
	privateIPs, err := GetPrivateIPs()
	if err != nil {
		return "", fmt.Errorf("获取私网 IP 失败: %w", err)
	}

	if len(privateIPs) == 0 {
		return "", fmt.Errorf("未找到可用的私网 IP，请手动指定 --bind-ip 参数")
	}

	return privateIPs[0], nil
}

// GetPrivateIPs 获取所有私网 IP 地址
func GetPrivateIPs() ([]string, error) {
	var privateIPs []string

	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("获取网络接口失败: %w", err)
	}

	for _, iface := range interfaces {
		// 跳过回环接口和未启用的接口
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addresses {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			ip := ipNet.IP

			// 跳过 IPv6
			if ip.To4() == nil {
				continue
			}

			// 检查是否为私网 IP
			if isPrivateIP(ip) {
				privateIPs = append(privateIPs, ip.String())
			}
		}
	}

	return privateIPs, nil
}

// isPrivateIP 检查是否为私网 IP
func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() {
		return false
	}

	// 私网 IP 范围（使用预编译的 CIDR 列表）
	privateRanges := getPrivateIPRanges()

	for _, ipNet := range privateRanges {
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// getPrivateIPRanges 返回私网 IP 范围列表（包级缓存）
func getPrivateIPRanges() []*net.IPNet {
	// 私网 IP 范围：
	// 10.0.0.0/8
	// 172.16.0.0/12
	// 192.168.0.0/16
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}

	ranges := make([]*net.IPNet, 0, len(privateRanges))
	for _, cidr := range privateRanges {
		_, ipNet, _ := net.ParseCIDR(cidr)
		ranges = append(ranges, ipNet)
	}

	return ranges
}

// FindAvailablePort 查找可用端口
func FindAvailablePort(startPort int) (int, error) {
	for port := startPort; port <= startPort+1000; port++ {
		addr := fmt.Sprintf(":%d", port)
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			listener.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("未找到可用端口（起始端口: %d）", startPort)
}

// ValidateIPMismatch 验证用户指定 IP 与自动绑定 IP 是否匹配
func ValidateIPMismatch(userIP, autoIP string) error {
	if userIP == "" || autoIP == "" {
		return nil
	}

	// 如果是本地开发环境，允许不匹配
	if userIP == "127.0.0.1" || autoIP == "127.0.0.1" {
		return nil
	}

	if userIP != autoIP {
		return fmt.Errorf("用户指定 IP (%s) 与自动绑定 IP (%s) 不匹配，请确保一致", userIP, autoIP)
	}

	return nil
}
