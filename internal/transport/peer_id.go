package transport

import (
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// GeneratePeerID 从公钥生成 PeerID
func GeneratePeerID(pubKey crypto.PubKey) (peer.ID, error) {
	return peer.IDFromPublicKey(pubKey)
}

// PeerIDString 返回 PeerID 字符串表示
func PeerIDString(pid peer.ID) string {
	return pid.String()
}

// PeerIDShort 返回 PeerID 短格式表示（用于日志）
// 返回 PeerID 的前 12 个字符
func PeerIDShort(pid peer.ID) string {
	str := pid.String()
	if len(str) > 12 {
		return str[:12] + "..."
	}
	return str
}
