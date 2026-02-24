package model

// NetworkAddress 网络地址接口（领域抽象）
// 用于在领域层表示网络节点地址，不依赖具体的基础设施实现
// 基础设施层提供具体实现（如 libp2p address、TCP address 等）
type NetworkAddress interface {
	// String 返回地址字符串表示
	String() string

	// Protocol 返回协议类型（tcp、quic、ws 等）
	Protocol() string
}

// SimpleAddress 简单地址实现（领域层值对象）
// 用于测试和简单场景
type SimpleAddress struct {
	addr     string
	protocol string
}

// NewSimpleAddress 创建简单地址
func NewSimpleAddress(addr, protocol string) *SimpleAddress {
	return &SimpleAddress{
		addr:     addr,
		protocol: protocol,
	}
}

// String 实现 NetworkAddress 接口
func (a *SimpleAddress) String() string {
	return a.addr
}

// Protocol 实现 NetworkAddress 接口
func (a *SimpleAddress) Protocol() string {
	return a.protocol
}
