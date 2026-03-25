// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"fmt"
	"os"
)

// BTREE_DEBUG 环境变量控制调试输出
// 设置为 1 启用调试输出
var btreetDebugEnabled = os.Getenv("OFFHEAP_DEBUG") == "1"

// DebugPrintf 条件调试输出
func DebugPrintf(format string, args ...interface{}) {
	if btreetDebugEnabled {
		fmt.Printf(format, args...)
	}
}
