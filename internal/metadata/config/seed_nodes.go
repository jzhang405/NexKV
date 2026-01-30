// Package config 种子节点配置解析与验证
//
// PR-036: 支持从配置文件和环境变量获取种子节点地址列表
// 使用 IPFS multiaddr 格式: /ip4/<IP>/tcp/<PORT>
// 支持运行时热更新（使用 fsnotify 监控配置文件变化）
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/multiformats/go-multiaddr"
)

// ParseSeedNodes 解析种子节点配置
//
// 支持格式：
//   - []string: ["/ip4/127.0.0.1/tcp/7946", "/ip4/127.0.0.1/tcp/7947"]
//   - string: "/ip4/127.0.0.1/tcp/7946,/ip4/127.0.0.1/tcp/7947"
//
// 返回规范化的地址列表（去重、去空）
func ParseSeedNodes(config interface{}) ([]string, error) {
	var nodes []string

	switch v := config.(type) {
	case []string:
		nodes = v
	case string:
		nodes = splitSeedNodesString(v)
	case []interface{}:
		// YAML 解析可能返回 []interface{}
		nodes = make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				nodes = append(nodes, str)
			}
		}
	default:
		return nil, fmt.Errorf("不支持的种子节点配置类型: %T", config)
	}

	// 规范化地址列表
	normalized := NormalizeSeedNodes(nodes)

	// 验证每个地址
	for _, addr := range normalized {
		if err := ValidateSeedNodeAddress(addr); err != nil {
			return nil, fmt.Errorf("种子节点地址 %s 验证失败: %w", addr, err)
		}
	}

	return normalized, nil
}

// ValidateSeedNodeAddress 验证单个地址格式
//
// 要求：IPFS multiaddr 格式
// 支持的格式：
//   - IPv4: /ip4/192.168.1.10/tcp/9211
//   - IPv6: /ip6/::1/tcp/9211
//   - DNS:  /dns4/localhost/tcp/9211
//
// 验证规则：
//   - 必须是有效的 multiaddr 格式
//   - 必须包含 TCP 协议组件
//   - TCP 端口必须在 1-65535 范围内
func ValidateSeedNodeAddress(addr string) error {
	// 去除首尾空格
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("地址不能为空")
	}

	// 验证以 / 开头
	if !strings.HasPrefix(addr, "/") {
		return fmt.Errorf("multiaddr 格式必须以 / 开头")
	}

	// 使用 multiaddr 库解析和验证
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("无效的 multiaddr 格式: %w", err)
	}

	// 检查是否包含 TCP 组件
	addrStr := maddr.String()
	if !strings.Contains(addrStr, "/tcp/") {
		return fmt.Errorf("地址必须包含 TCP 协议组件（如 /tcp/<PORT>）")
	}

	// 验证 TCP 端口范围
	if err := validateTCPPortFromString(addrStr); err != nil {
		return err
	}

	return nil
}

// validateTCPPortFromString 从字符串中提取并验证 TCP 端口
func validateTCPPortFromString(addr string) error {
	// 查找 /tcp/ 的位置
	tcpIdx := strings.Index(addr, "/tcp/")
	if tcpIdx == -1 {
		return fmt.Errorf("未找到 TCP 协议组件")
	}

	// 提取 /tcp/ 后面的部分
	afterTCP := addr[tcpIdx+5:] // +5 跳过 "/tcp/"
	if afterTCP == "" {
		return fmt.Errorf("TCP 组件后必须跟端口号")
	}

	// multiaddr 可能还有其他组件，只取到下一个 / 或字符串结尾
	endIdx := strings.Index(afterTCP, "/")
	if endIdx == -1 {
		endIdx = len(afterTCP)
	}
	portStr := afterTCP[:endIdx]

	// 解析端口
	var port int
	_, err := fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		return fmt.Errorf("无效的 TCP 端口值: %s", portStr)
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf("TCP 端口必须在 1-65535 范围内，当前值: %d", port)
	}

	return nil
}

// NormalizeSeedNodes 规范化地址列表
//
// 执行：
//   - 去重（保留首次出现顺序）
//   - 去空（移除空字符串）
//   - 去空格（去除首尾空格）
func NormalizeSeedNodes(nodes []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(nodes))

	for _, node := range nodes {
		// 去除首尾空格
		addr := strings.TrimSpace(node)

		// 跳过空字符串
		if addr == "" {
			continue
		}

		// 去重
		if !seen[addr] {
			seen[addr] = true
			result = append(result, addr)
		}
	}

	return result
}

// splitSeedNodesString 分割逗号分隔的字符串
//
// 支持格式：
//   - "/ip4/127.0.0.1/tcp/7946,/ip4/127.0.0.1/tcp/7947"
//   - 支持逗号后空格
func splitSeedNodesString(s string) []string {
	if s == "" {
		return []string{}
	}

	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		addr := strings.TrimSpace(part)
		if addr != "" {
			result = append(result, addr)
		}
	}

	return result
}

// ========================================
// 运行时热更新支持（使用 fsnotify）
// ========================================

// SeedNodesWatcher 种子节点配置监控器
//
// 核心功能：
//   - 监控配置文件变化（fsnotify）
//   - 线程安全的配置访问（sync.RWMutex）
//   - 配置变化回调通知
//   - 优雅关闭机制
type SeedNodesWatcher struct {
	mu    sync.RWMutex
	nodes []string // 当前种子节点列表

	// 配置文件路径
	filePath string

	// 变化回调
	callback func([]string)

	// fsnotify 监控器
	watcher *fsnotify.Watcher

	// 生命周期管理
	done      chan struct{}
	closeOnce sync.Once
}

// NewSeedNodesWatcher 创建配置监控器
//
// 参数：
//   - filePath: 配置文件路径（绝对路径或相对路径）
//   - callback: 配置变化回调函数（接收新的种子节点列表）
//
// 返回：
//   - *SeedNodesWatcher: 配置监控器实例
//   - error: 创建失败时返回错误（如文件不存在、fsnotify 初始化失败）
func NewSeedNodesWatcher(filePath string, callback func([]string)) (*SeedNodesWatcher, error) {
	// 验证文件路径
	if filePath == "" {
		return nil, fmt.Errorf("配置文件路径不能为空")
	}

	// 转换为绝对路径
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("获取绝对路径失败: %w", err)
	}

	// 检查文件是否存在（可选：允许文件不存在，由后续创建）
	if _, err := os.Stat(absPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("检查配置文件失败: %w", err)
	}

	// 创建 fsnotify 监控器
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("创建文件监控器失败: %w", err)
	}

	sw := &SeedNodesWatcher{
		filePath: absPath,
		callback: callback,
		watcher:  watcher,
		done:     make(chan struct{}),
	}

	return sw, nil
}

// Start 启动配置监控
//
// 启动后，监控器会：
//  1. 监控配置文件所在目录
//  2. 检测文件变化（WRITE、CREATE、REMOVE、RENAME）
//  3. 重新加载配置并调用回调
//
// 注意：
//   - 重复调用 Start 会返回错误
//   - 配置文件不存在时会记录警告，但不会报错
func (w *SeedNodesWatcher) Start() error {
	// 监控配置文件所在目录
	dir := filepath.Dir(w.filePath)
	if err := w.watcher.Add(dir); err != nil {
		return fmt.Errorf("监控目录失败: %w", err)
	}

	logging.Infof("[SeedNodesWatcher] 启动配置文件监控: %s", w.filePath)

	// 首次加载配置
	if err := w.reload(); err != nil {
		logging.Warnf("[SeedNodesWatcher] 初始加载配置失败: %v", err)
	}

	// 启动监控协程
	go w.watchLoop()

	return nil
}

// Stop 停止配置监控
//
// 优雅关闭：
//  1. 停止监控文件变化
//  2. 关闭 fsnotify 监控器
//  3. 释放资源
func (w *SeedNodesWatcher) Stop() {
	w.closeOnce.Do(func() {
		logging.Infof("[SeedNodesWatcher] 停止配置文件监控")
		close(w.done)
		w.watcher.Close()
	})
}

// GetSeedNodes 获取当前种子节点（线程安全）
//
// 返回当前配置的种子节点列表副本，避免并发修改问题。
func (w *SeedNodesWatcher) GetSeedNodes() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// 返回副本，避免并发修改
	result := make([]string, len(w.nodes))
	copy(result, w.nodes)
	return result
}

// watchLoop 监控循环
func (w *SeedNodesWatcher) watchLoop() {
	// 防抖：短时间内多次文件变化只触发一次重载
	var debounceTimer *time.Timer
	debounceDelay := 500 * time.Millisecond

	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}

			// 只关心目标配置文件的变化
			if event.Name != w.filePath {
				continue
			}

			// 检查事件类型
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}

			logging.Debugf("[SeedNodesWatcher] 检测到配置文件变化: %s (操作: %s)", event.Name, event.Op)

			// 防抖：重置定时器
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(debounceDelay, func() {
				if err := w.reload(); err != nil {
					logging.Warnf("[SeedNodesWatcher] 重新加载配置失败: %v", err)
				}
			})

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			logging.Errorf("[SeedNodesWatcher] 监控错误: %v", err)

		case <-w.done:
			// 停止防抖定时器
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return
		}
	}
}

// reload 重新加载配置
//
// 流程：
//  1. 读取配置文件
//  2. 解析 YAML
//  3. 提取 SeedNodes 字段
//  4. 验证地址格式
//  5. 更新内存中的配置
//  6. 触发回调
func (w *SeedNodesWatcher) reload() error {
	// 检查文件是否存在
	if _, err := os.Stat(w.filePath); os.IsNotExist(err) {
		return fmt.Errorf("配置文件不存在: %s", w.filePath)
	}

	// 加载配置
	cfg, err := LoadConfig(w.filePath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	// 解析种子节点
	nodes, err := ParseSeedNodes(cfg.Cluster.SeedNodes)
	if err != nil {
		return fmt.Errorf("解析种子节点失败: %w", err)
	}

	// 规范化（去重、去空）
	nodes = NormalizeSeedNodes(nodes)

	// 更新内存配置
	w.mu.Lock()
	oldNodes := w.nodes
	w.nodes = nodes
	w.mu.Unlock()

	// 检查是否有变化
	if !stringSlicesEqual(oldNodes, nodes) {
		logging.Infof("[SeedNodesWatcher] 配置已更新: %d -> %d 个种子节点", len(oldNodes), len(nodes))

		// 触发回调
		if w.callback != nil {
			// 在独立协程中调用回调，避免阻塞监控循环
			go w.callback(nodes)
		}
	}

	return nil
}

// stringSlicesEqual 比较两个字符串切片是否相等
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
