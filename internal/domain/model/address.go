package model

// NetworkAddress 网络地址（领域值对象）
// 用于在领域层表示网络节点地址，不依赖具体的基础设施实现
type NetworkAddress struct {
	// Protocol 协议类型，如 "tcp", "udp", "ws" 等
	Protocol string `json:"protocol"`

	// IP IP 地址
	IP string `json:"ip"`

	// Port 端口号
	Port int `json:"port"`

	// Raw 原始地址字符串（用于存储无法解析的地址格式）
	Raw string `json:"raw,omitempty"`
}

// String 返回地址字符串表示
func (a NetworkAddress) String() string {
	if a.Raw != "" {
		return a.Raw
	}
	return a.Protocol + "://" + a.IP + ":" + string(rune('0'+a.Port))
}
