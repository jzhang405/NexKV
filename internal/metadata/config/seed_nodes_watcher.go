// Package config 种子节点配置热更新监控
//
// PR-036: 使用 fsnotify 监控配置文件变化，支持运行时热更新
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

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
//   1. 监控配置文件所在目录
//   2. 检测文件变化（WRITE、CREATE、REMOVE、RENAME）
//   3. 重新加载配置并调用回调
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
//   1. 停止监控文件变化
//   2. 关闭 fsnotify 监控器
//   3. 释放资源
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
//   1. 读取配置文件
//   2. 解析 YAML
//   3. 提取 SeedNodes 字段
//   4. 验证地址格式
//   5. 更新内存中的配置
//   6. 触发回调
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
