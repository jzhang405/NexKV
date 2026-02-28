// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"log"
	"os"

	"go.uber.org/automaxprocs/maxprocs"
)

// init 自动初始化 GOMAXPROCS
// 使用 Uber automaxprocs 库自动设置 Go 程序的 GOMAXPROCS
// 确保 GOMAXPROCS 与实际可用的 CPU 核心数匹配
func init() {
	// 检查是否禁用自动设置
	if os.Getenv("AUTOMAXPROCS_DISABLE") == "true" {
		log.Println("[concurrency] automaxprocs disabled by environment variable")
		return
	}

	// 自动设置 GOMAXPROCS
	// automaxprocs 会根据 cgroup 配置（如 Kubernetes）自动调整
	undo, err := maxprocs.Set()
	if err != nil {
		log.Printf("[concurrency] automaxprocs set failed: %v", err)
		return
	}

	_ = undo // 可以在程序退出时调用 undo() 恢复原始设置

	log.Printf("[concurrency] automaxprocs enabled successfully")
}
