// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"
	"os"
)

const btreeDebug = "BTREE_DEBUG"

// initDebug 检查是否开启调试模式
var debugEnabled = false

func init() {
	// 通过环境变量 BTREE_DEBUG=1 开启调试
	debugEnabled = os.Getenv(btreeDebug) == "1"
}

// DebugPrintf 条件调试输出
// 只有在 BTREE_DEBUG=1 时才会输出
func DebugPrintf(format string, args ...any) {
	if debugEnabled {
		fmt.Printf(format, args...)
	}
}

// IsDebugEnabled 返回是否开启调试模式
func IsDebugEnabled() bool {
	return debugEnabled
}
