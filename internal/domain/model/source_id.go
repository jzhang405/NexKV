// Package model 定义领域模型
package model

import (
	"errors"
	"strings"
)

// SourceID 来源标识（值对象）
// 用于标识任务的来源，帮助路由到合适的执行器
// 格式: {module}:{sub-module}:{action}
type SourceID struct {
	module    string // 模块名（如 hlc, wal, rpc）
	subModule string // 子模块名（如 clock, writer, client）
	action    string // 操作名（如 tick, flush, send）
}

// ParseSourceID 从字符串解析 SourceID
// 格式必须为 {module}:{sub-module}:{action}
func ParseSourceID(s string) (SourceID, error) {
	if s == "" {
		return SourceID{}, errors.New("source ID cannot be empty")
	}

	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return SourceID{}, errors.New("source ID must be in format {module}:{sub-module}:{action}")
	}

	module := strings.TrimSpace(parts[0])
	subModule := strings.TrimSpace(parts[1])
	action := strings.TrimSpace(parts[2])

	if module == "" {
		return SourceID{}, errors.New("module cannot be empty")
	}
	if subModule == "" {
		return SourceID{}, errors.New("sub-module cannot be empty")
	}
	if action == "" {
		return SourceID{}, errors.New("action cannot be empty")
	}

	return SourceID{
		module:    module,
		subModule: subModule,
		action:    action,
	}, nil
}

// MustParseSourceID 从字符串解析 SourceID，解析失败时 panic
func MustParseSourceID(s string) SourceID {
	id, err := ParseSourceID(s)
	if err != nil {
		panic(err)
	}
	return id
}

// Module 返回模块名
func (s SourceID) Module() string {
	return s.module
}

// SubModule 返回子模块名
func (s SourceID) SubModule() string {
	return s.subModule
}

// Action 返回操作名
func (s SourceID) Action() string {
	return s.action
}

// String 返回完整字符串表示
func (s SourceID) String() string {
	return s.module + ":" + s.subModule + ":" + s.action
}

// Equals 比较两个 SourceID 是否相等
func (s SourceID) Equals(other SourceID) bool {
	return s.module == other.module &&
		s.subModule == other.subModule &&
		s.action == other.action
}

// Hash 返回 SourceID 的哈希值（用于作为 map 键）
func (s SourceID) Hash() string {
	return s.String()
}

// Match 检查 SourceID 是否匹配指定模式
// 模式支持通配符 "*"，例如：
//   - "hlc:clock:tick" 匹配 "hlc:clock:tick"
//   - "hlc:clock:*" 匹配所有 hlc:clock:xxx
//   - "hlc:*:*" 匹配所有 hlc:xxx:yyy
//   - "*:*:*" 匹配所有
func (s SourceID) Match(pattern string) bool {
	patternParts := strings.Split(pattern, ":")
	if len(patternParts) != 3 {
		return false
	}

	parts := []string{s.module, s.subModule, s.action}

	for i, patternPart := range patternParts {
		if patternPart != "*" && patternPart != parts[i] {
			return false
		}
	}

	return true
}

// Validate 验证 SourceID 有效性
func (s SourceID) Validate() error {
	if s.module == "" {
		return errors.New("module cannot be empty")
	}
	if s.subModule == "" {
		return errors.New("sub-module cannot be empty")
	}
	if s.action == "" {
		return errors.New("action cannot be empty")
	}
	return nil
}

// RecommendedMode 根据 SourceID 返回推荐的调度模式
func (s SourceID) RecommendedMode() TaskMode {
	// Per-Core 模式：延迟敏感的核心模块
	perCoreModules := map[string]bool{
		"hlc":        true, // HLC 时钟
		"wal":        true, // WAL 写入
		"transpose":  true, // 矩阵转置
		"replication": true, // 副本同步
	}

	// 函数池模式：高频重复任务
	funcPoolPatterns := []string{
		"rpc:client:",    // RPC 客户端
		"rpc:server:",    // RPC 服务端
		"network:send:",  // 网络发送
		"network:recv:",  // 网络接收
	}

	// 多池模式：分片场景
	multiPoolPatterns := []string{
		"query:range:",   // 范围查询
		"query:point:",   // 点查询
		"shard:",         // 分片操作
	}

	// 自定义池模式：后台任务
	customPoolPatterns := []string{
		"background:",    // 后台任务
		"log:",           // 日志
		"metric:",        // 指标
		"cleanup:",       // 清理
	}

	// 检查 Per-Core 模式
	if perCoreModules[s.module] {
		return ModePerCore
	}

	// 构建前缀
	prefix := s.module + ":" + s.subModule + ":"

	// 检查函数池模式
	for _, pattern := range funcPoolPatterns {
		if strings.HasPrefix(prefix, pattern) {
			return ModeFuncPool
		}
	}

	// 检查多池模式
	for _, pattern := range multiPoolPatterns {
		if strings.HasPrefix(s.module+":"+s.subModule+":", pattern) {
			return ModeMultiPool
		}
	}

	// 检查自定义池模式
	for _, pattern := range customPoolPatterns {
		if strings.HasPrefix(s.String(), pattern) {
			return ModeCustomPool
		}
	}

	// 默认使用默认池
	return ModeDefaultPool
}

// IsHighPriority 判断是否为高优先级任务
func (s SourceID) IsHighPriority() bool {
	highPriorityModules := map[string]bool{
		"hlc":        true,
		"wal":        true,
		"replication": true,
	}
	return highPriorityModules[s.module]
}

// IsBackground 判断是否为后台任务
func (s SourceID) IsBackground() bool {
	return s.module == "background" ||
		s.module == "log" ||
		s.module == "metric" ||
		s.module == "cleanup"
}
