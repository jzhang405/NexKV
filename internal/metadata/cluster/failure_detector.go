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
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

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
type ProbeResult struct {
	TCPReachable bool          // TCP 可达
	UDPReachable bool          // UDP 可达
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

// ProbeHost 探测单个 Host（TCP + UDP 双重探测）
//
// 返回探测结果，包含 TCP/UDP 可达性、RTT、错误信息
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
	udpPort := allocation.UDPPort

	// 步骤 3: 构建 TCP 和 UDP 地址（支持 IPv4 和 IPv6）
	hostname := host.Hostname
	if hostname == "" {
		return nil, types.NewClusterHostnameRequiredError()
	}

	tcpAddr := formatAddress(hostname, tcpPort)
	udpAddr := formatAddress(hostname, udpPort)

	result := &ProbeResult{
		ProbedAt: time.Now().Unix(),
	}

	// 步骤 3: TCP 探测（连接性 + 时延）
	start := time.Now()
	conn, err := net.DialTimeout("tcp", tcpAddr, fd.config.ProbeTimeout)
	if err == nil {
		result.TCPReachable = true
		result.RTT = time.Since(start)
		conn.Close()
	} else {
		result.Error = types.NewClusterTCPProbeFailedError(err)
	}

	// 步骤 4: UDP 探测（快速可达性检查）
	// 注意：UDP 是无连接协议，DialTimeout 会立即成功
	// 需要发送实际数据包来检测可达性，或仅作为辅助参考
	udpConn, err := net.DialTimeout("udp", udpAddr, fd.config.ProbeTimeout)
	if err == nil {
		// 尝试发送一个小数据包
		_, err = udpConn.Write([]byte("ping"))
		if err == nil {
			result.UDPReachable = true
		}
		udpConn.Close()
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
//  2. TCP/UDP 双重探测
//  3. 连续失败次数判断
//  4. 防脑裂延迟确认
//
// P0-3 优化：减少锁持有时间，避免在网络 I/O 期间持有锁
func (fd *FailureDetector) IsHostFailed(hostID string) (bool, error) {
	// 步骤 1: 心跳超时检测（不持有锁，hostManager 有自己的锁）
	host, err := fd.hostManager.GetHost(hostID)
	if err != nil {
		return true, types.NewClusterHostNotFoundError(hostID)
	}

	lastHeartbeat := time.Unix(host.LastHeartbeat, 0)
	if time.Since(lastHeartbeat) < fd.config.HeartbeatTimeout {
		// 心跳正常，重置失败计数
		fd.mu.Lock()
		fd.failureCount[hostID] = 0
		fd.mu.Unlock()
		return false, nil
	}

	// 步骤 2: 检查并增加连续失败次数（需要锁）
	fd.mu.Lock()
	now := time.Now()
	lastFail, exists := fd.lastFailTime[hostID]
	if exists && now.Sub(time.Unix(lastFail, 0)) > failureCountResetInterval {
		// 距离上次失败超过重置间隔，重置计数
		fd.failureCount[hostID] = 0
	}

	fd.failureCount[hostID]++
	fd.lastFailTime[hostID] = now.Unix()
	currentFailCount := fd.failureCount[hostID]
	maxFails := fd.config.MaxConsecutiveFails
	fd.mu.Unlock()

	if currentFailCount < maxFails {
		// 未达到阈值，不判定为故障
		return false, nil
	}

	// 步骤 3: 执行第一次探测（释放锁，避免阻塞其他操作）
	result, err := fd.ProbeHost(hostID)
	if err != nil {
		// 探测失败，记录错误
		return true, err
	}

	// 步骤 4: 判断探测结果
	// 注意：优先使用 TCP 探测结果（更可靠）
	// UDP 是无连接协议，Write 成功不代表服务可达
	if result.TCPReachable {
		// TCP 可达，认为未故障，重置计数
		fd.mu.Lock()
		fd.failureCount[hostID] = 0
		fd.mu.Unlock()
		return false, nil
	}

	// 如果 TCP 不可达，无论 UDP 状态如何，都认为可能故障
	// 继续执行防脑裂延迟和再次探测

	// 步骤 5: 防脑裂延迟（关键！）
	// 等待 2 秒，确认不是网络抖动
	// 注意：这里在锁外等待，避免阻塞其他操作
	delayDuration := fd.config.DelayDuration
	time.Sleep(delayDuration)

	// 步骤 6: 延迟后再次探测
	result2, err2 := fd.ProbeHost(hostID)
	if err2 != nil {
		// 探测出错，认为故障
		return true, err2
	}

	// 步骤 7: 判断第二次探测结果（只看 TCP）
	if result2.TCPReachable {
		// 延迟后 TCP 恢复，重置计数
		fd.mu.Lock()
		fd.failureCount[hostID] = 0
		fd.mu.Unlock()
		return false, nil
	}

	// 确认故障（两次 TCP 探测都失败）
	return true, nil
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
func (fd *FailureDetector) CheckAllHosts() ([]string, error) {
	hosts, err := fd.hostManager.ListAllHosts()
	if err != nil {
		return nil, types.NewClusterHostListFailedError(err)
	}

	failedHosts := make([]string, 0)

	for _, host := range hosts {
		failed, err := fd.IsHostFailed(host.HostID)
		if err != nil {
			// 探测出错，记录日志但继续检查其他 Host
			continue
		}

		if failed {
			failedHosts = append(failedHosts, host.HostID)
		}
	}

	return failedHosts, nil
}
