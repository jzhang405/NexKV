// Package gossip 提供 Go 语言的密码学安全随机数生成工具
//
// 核心功能：
//   - 使用 crypto/rand 生成密码学安全的随机数
//   - 提供 cryptoRandInt 辅助函数供 gossip 包内部使用
package gossip

import (
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/jzhang405/NexKV/internal/config/logging"
)

// cryptoRandInt 生成 [0, max) 范围内的密码学安全随机数
//
// 参数：
//   - max: 上界（不包含）
//
// 返回：
//   - 生成的随机数
//   - 错误（如果 max <= 0）
func cryptoRandInt(max int64) (int64, error) {
	if max <= 0 {
		return 0, fmt.Errorf("max must be positive")
	}

	n, err := rand.Int(rand.Reader, big.NewInt(max))
	if err != nil {
		logging.WithField("error", err).Error("生成随机数失败，使用默认选择")
		return 0, err
	}

	return n.Int64(), nil
}
