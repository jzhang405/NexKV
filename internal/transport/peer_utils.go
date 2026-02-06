// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package transport

import (
	"fmt"

	"github.com/libp2p/go-libp2p/core/peer"
)

// parsePeersFromStrings 从字符串列表解析节点信息
//
// 支持格式:
//   - /ip4/1.2.3.4/tcp/4001/p2p/QmPeerID
//   - /dns4/node.example.com/tcp/4001/p2p/QmPeerID
//
// 返回解析成功的节点列表和解析过程中的错误（如果有）
func parsePeersFromStrings(addrs []string) ([]peer.AddrInfo, error) {
	peers := make([]peer.AddrInfo, 0, len(addrs))

	for _, addr := range addrs {
		pi, err := peer.AddrInfoFromString(addr)
		if err != nil {
			return nil, fmt.Errorf("解析节点地址失败 [%s]: %w", addr, err)
		}
		if pi != nil {
			peers = append(peers, *pi)
		}
	}

	return peers, nil
}
