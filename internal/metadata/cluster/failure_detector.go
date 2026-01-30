// Package cluster 提供故障检测器实现
//
// FailureDetector：心跳超时 + TCP/UDP 双重探测
//   - 心跳超时检测：检查 LastHeartbeat 字段
//   - TCP 探测：连接性 + 往返时延（RTT）
//   - UDP 探测：快速可达性检查
//   - 双重验证：心跳超时 + 主动探测，避免误判
//   - 防脑裂延迟：网络抖动场景的延迟确认机制
package cluster

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

const (
	defaultHeartbeatTimeout    = 30 * time.Second // 默认心跳超时
	defaultProbeTimeout        = 3 * time.Second  // 默认探测超时
	defaultMaxConsecutiveFails = 3                // 默认最大连续失败次数
	defaultDelayDuration       = 2 * time.Second  // 默认防脑裂延迟
	failureCountResetInterval  = 10 * time.Second // 失败计数重置间隔
)

// ProbeResult 探测结果
//
// P1-3 优化：移除 UDPReachable 字段（UDP 探测不可靠）
// - UDP 是无连接协议，Write 成功不代表服务可达
// - TCP 探测已经足够验证连接性和时延
type ProbeResult struct {
	TCPReachable bool          // TCP 可达
	RTT          time.Duration // 往返时延（TCP 连接耗时）
	ProbedAt     int64         // 探测时间戳（Unix 秒）
	Error        error         // 探测错误信息
}

// FailureDetectorConfig 故障检测器配置
type FailureDetectorConfig struct {
	HeartbeatTimeout    time.Duration // 心跳超时阈值
	ProbeTimeout        time.Duration // 探测超时时间
	MaxConsecutiveFails int           // 最大连续失败次数
	DelayDuration       time.Duration // 防脑裂延迟时长
}

// DefaultFailureDetectorConfig 默认故障检测器配置
var DefaultFailureDetectorConfig = FailureDetectorConfig{
	HeartbeatTimeout:    defaultHeartbeatTimeout,
	ProbeTimeout:        defaultProbeTimeout,
	MaxConsecutiveFails: defaultMaxConsecutiveFails,
	DelayDuration:       defaultDelayDuration,
}

// FailureDetector 故障检测器
type FailureDetector struct {
	config        FailureDetectorConfig
	hostManager   *HostManager
	portAllocator *PortAllocator // 端口分配器
	mu            sync.RWMutex
	failureCount  map[string]int          // hostID → 连续失败次数
	lastFailTime  map[string]int64        // hostID → 最后失败时间
	lastProbe     map[string]*ProbeResult // hostID → 最后探测结果
}

// NewFailureDetector 创建故障检测器
func NewFailureDetector(hostManager *HostManager, portAllocator *PortAllocator, config FailureDetectorConfig) *FailureDetector {
	if config.HeartbeatTimeout == 0 {
		config = DefaultFailureDetectorConfig
	}

	return &FailureDetector{
		config:        config,
		hostManager:   hostManager,
		portAllocator: portAllocator,
		failureCount:  make(map[string]int),
		lastFailTime:  make(map[string]int64),
		lastProbe:     make(map[string]*ProbeResult),
	}
}

// formatAddress 格式化网络地址，支持 IPv4 和 IPv6
// IPv4: "192.168.1.100:9000"
// IPv6: "[2001:db8::1]:9000"
func formatAddress(hostname string, port int) string {
	// 检查是否为 IPv6 地址（包含冒号）
	if strings.Contains(hostname, ":") {
		return fmt.Sprintf("[%s]:%d", hostname, port)
	}
	return fmt.Sprintf("%s:%d", hostname, port)
}

// DetectHeartbeatTimeout 检测心跳超时的 Host
//
// 返回心跳超时的 HostID 列表
func (fd *FailureDetector) DetectHeartbeatTimeout() ([]string, error) {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	hosts, err := fd.hostManager.ListAllHosts()
	if err != nil {
		return nil, types.NewClusterHostListFailedError(err)
	}

	now := time.Now()
	timeoutHosts := make([]string, 0)

	for _, host := range hosts {
		lastHeartbeat := time.Unix(host.LastHeartbeat, 0)
		if now.Sub(lastHeartbeat) > fd.config.HeartbeatTimeout {
			timeoutHosts = append(timeoutHosts, host.HostID)
		}
	}

	return timeoutHosts, nil
}

// ProbeHost 探测单个 Host（TCP 探测）
//
// 返回探测结果，包含 TCP 可达性、RTT、错误信息
//
// P1-3 优化：移除 UDP 探测（UDP 是无连接协议，Write 成功不代表服务可达）
// - TCP 探测已经足够验证连接性和时延
// - UDP 探测在当前架构下没有实际意义
func (fd *FailureDetector) ProbeHost(hostID string) (*ProbeResult, error) {
	// 步骤 1: 获取 Host 信息
	host, err := fd.hostManager.GetHost(hostID)
	if err != nil {
		return nil, types.NewClusterHostNotFoundError(hostID)
	}

	// 步骤 2: 从 PortAllocator 获取分配的端口
	allocation, err := fd.portAllocator.GetAllocation(hostID)
	if err != nil {
		return nil, types.NewClusterPortAllocationNotFoundError(hostID, err)
	}

	tcpPort := allocation.TCPPort

	// 步骤 3: 构建 TCP 地址（支持 IPv4 和 IPv6）
	hostname := host.Hostname
	if hostname == "" {
		return nil, types.NewClusterHostnameRequiredError()
	}

	tcpAddr := formatAddress(hostname, tcpPort)

	result := &ProbeResult{
		ProbedAt: time.Now().Unix(),
	}

	// 步骤 4: TCP 探测（连接性 + 时延）
	start := time.Now()
	conn, err := net.DialTimeout("tcp", tcpAddr, fd.config.ProbeTimeout)
	if err == nil {
		result.TCPReachable = true
		result.RTT = time.Since(start)
		conn.Close()
	} else {
		result.Error = types.NewClusterTCPProbeFailedError(err)
	}

	// 步骤 5: 缓存探测结果
	fd.mu.Lock()
	fd.lastProbe[hostID] = result
	fd.mu.Unlock()

	return result, nil
}

// IsHostFailed 判断 Host 是否故障（双重验证）
//
// 验证步骤：
//  1. 心跳超时检测
//  2. TCP 探测（连接性 + 时延）
//  3. 连续失败次数判断
//  4. 防脑裂延迟确认
//
// P0-3 优化：减少锁持有时间，避免在网络 I/O 期间持有锁
// P1-3 优化：移除 UDP 探测，只使用 TCP 探测（更可靠）
// P1-1 修复：使用 context 支持可取消的防脑裂延迟
func (fd *FailureDetector) IsHostFailed(hostID string) (bool, error) {
	// 使用 background context 调用新方法，保持向后兼容
	return fd.IsHostFailedWithContext(context.Background(), hostID)
}

// IsHostFailedWithContext 判断 Host 是否故障（支持 context 取消）
//
// P1-1 修复：添加 context 支持，使防脑裂延迟可被取消
// 这避免了 goroutine 泄漏，并允许快速响应系统关闭
func (fd *FailureDetector) IsHostFailedWithContext(ctx context.Context, hostID string) (bool, error) {
	// 步骤 1: 心跳超时检测（不持有锁，hostManager 有自己的锁）
	host, err := fd.hostManager.GetHost(hostID)
	if err != nil {
		return true, types.NewClusterHostNotFoundError(hostID)
	}

	lastHeartbeat := time.Unix(host.LastHeartbeat, 0)
	if time.Since(lastHeartbeat) < fd.config.HeartbeatTimeout {
		fd.resetFailureCount(hostID)
		return false, nil
	}

	// 步骤 2: 检查并增加连续失败次数
	currentFailCount := fd.incrementFailureCount(hostID)
	if currentFailCount < fd.config.MaxConsecutiveFails {
		return false, nil
	}

	// 步骤 3-4: 执行第一次探测并判断结果
	result, err := fd.ProbeHost(hostID)
	if err != nil {
		return true, err
	}

	if result.TCPReachable {
		fd.resetFailureCount(hostID)
		return false, nil
	}

	// 步骤 5: 防脑裂延迟（P1-1 修复：可取消的延迟）
	if err := fd.waitForDelay(ctx); err != nil {
		return false, err
	}

	// 步骤 6-7: 延迟后再次探测并判断结果
	result2, err2 := fd.ProbeHost(hostID)
	if err2 != nil {
		return true, err2
	}

	if result2.TCPReachable {
		fd.resetFailureCount(hostID)
		return false, nil
	}

	return true, nil
}

// resetFailureCount 重置失败计数（辅助方法）
func (fd *FailureDetector) resetFailureCount(hostID string) {
	fd.mu.Lock()
	fd.failureCount[hostID] = 0
	fd.mu.Unlock()
}

// incrementFailureCount 增加失败计数并返回当前值（辅助方法）
func (fd *FailureDetector) incrementFailureCount(hostID string) int {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	now := time.Now()
	lastFail, exists := fd.lastFailTime[hostID]
	if exists && now.Sub(time.Unix(lastFail, 0)) > failureCountResetInterval {
		fd.failureCount[hostID] = 0
	}

	fd.failureCount[hostID]++
	fd.lastFailTime[hostID] = now.Unix()
	return fd.failureCount[hostID]
}

// waitForDelay 等待防脑裂延迟（P1-1 修复：支持 context 取消）
func (fd *FailureDetector) waitForDelay(ctx context.Context) error {
	delayTimer := time.NewTimer(fd.config.DelayDuration)
	defer delayTimer.Stop()

	select {
	case <-delayTimer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GetLastProbe 获取 Host 的最后探测结果
func (fd *FailureDetector) GetLastProbe(hostID string) (*ProbeResult, error) {
	fd.mu.RLock()
	defer fd.mu.RUnlock()

	result, exists := fd.lastProbe[hostID]
	if !exists {
		return nil, types.NewClusterNoProbeResultError(hostID)
	}

	return result, nil
}

// GetFailureCount 获取 Host 的连续失败次数
func (fd *FailureDetector) GetFailureCount(hostID string) (int, error) {
	fd.mu.RLock()
	defer fd.mu.RUnlock()

	count, exists := fd.failureCount[hostID]
	if !exists {
		return 0, nil
	}

	return count, nil
}

// ResetFailureCount 重置 Host 的失败计数
func (fd *FailureDetector) ResetFailureCount(hostID string) {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	fd.failureCount[hostID] = 0
	delete(fd.lastFailTime, hostID)
}

// CheckAllHosts 检查所有 Host 的故障状态
//
// 返回故障 HostID 列表
//
// P0-1 修复：探测失败时将节点加入可疑列表，而不是静默忽略
func (fd *FailureDetector) CheckAllHosts() ([]string, error) {
	hosts, err := fd.hostManager.ListAllHosts()
	if err != nil {
		return nil, types.NewClusterHostListFailedError(err)
	}

	failedHosts := make([]string, 0)

	for _, host := range hosts {
		failed, err := fd.IsHostFailed(host.HostID)
		if err != nil {
			// P0-1 修复：探测失败时记录日志，并将节点加入可疑列表
			// 这样可以确保探测失败的节点不会被视为正常节点
			logging.WithFields(map[string]any{
				"host_id": host.HostID,
				"error":   err,
			}).Warn("探测失败，将节点加入可疑列表")

			// 将探测失败的节点加入故障列表，由上层决定如何处理
			// 这比静默忽略更安全，可以避免遗漏真正的故障
			failedHosts = append(failedHosts, host.HostID)
			continue
		}

		if failed {
			failedHosts = append(failedHosts, host.HostID)
		}
	}

	return failedHosts, nil
}
