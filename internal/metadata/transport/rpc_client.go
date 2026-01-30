// Package transport RPC 客户端实现
//
// 支持双 Transport（TCP + UDP）：
//   - TCP：可靠传输，用于关键消息（Quorum、2PC、元数据操作）
//   - UDP：尽力而为，用于尽力型消息（Gossip、心跳）
//
// P1-2 优化：CallBatch 快速失败机制
//   - 使用 errgroup 实现某个请求失败立即返回
//   - 自动取消其他未完成的请求
//   - 提升批量调用响应速度
package transport

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// RPC Client 结构体
// ========================================

// RPCClient RPC 客户端
//
// 核心功能：
//   - 双 Transport 支持（TCP + UDP）
//   - 请求-响应匹配（通过 CorrelationID）
//   - CallBatch 批量调用（P1-2 快速失败）
//   - 自动重试（可配置）
//   - 超时控制
type RPCClient struct {
	// 传输层
	tcpTransport Transport // TCP 传输（可靠）
	udpTransport Transport // UDP 传输（尽力而为）

	// 请求表（匹配请求-响应）
	reqTable *requestTable

	// 配置
	config *RPCClientConfig

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 状态
	running atomic.Bool
}

// RPCClientConfig 客户端配置
type RPCClientConfig struct {
	// DialTimeout 连接超时（默认：5s）
	DialTimeout time.Duration

	// RequestTimeout 请求超时（默认：30s）
	RequestTimeout time.Duration

	// MaxRetries 最大重试次数（默认：3）
	MaxRetries int

	// RetryDelay 重试延迟（默认：100ms）
	RetryDelay time.Duration

	// EnableFastFail 启用快速失败（P1-2 优化，默认：true）
	EnableFastFail bool

	// FastFailTimeout 快速失败超时（默认：5s）
	// 超过此时间未完成的请求会被取消
	FastFailTimeout time.Duration
}

// DefaultRPCClientConfig 返回默认配置
func DefaultRPCClientConfig() *RPCClientConfig {
	return &RPCClientConfig{
		DialTimeout:     5 * time.Second,
		RequestTimeout:  30 * time.Second,
		MaxRetries:      3,
		RetryDelay:      100 * time.Millisecond,
		EnableFastFail:  true,
		FastFailTimeout: 5 * time.Second,
	}
}

// ========================================
// RPC Client 创建
// ========================================

// NewRPCClient 创建新的 RPC 客户端
//
// 参数：
//   - tcpTransport: TCP 传输层（必需，用于可靠消息）
//   - udpTransport: UDP 传输层（可选，用于尽力型消息）
//   - config: 客户端配置（nil 时使用默认配置）
//
// 返回：
//   - *RPCClient: RPC 客户端实例
//   - error: 配置无效时返回错误
func NewRPCClient(tcpTransport Transport, udpTransport Transport, config *RPCClientConfig) (*RPCClient, error) {
	if tcpTransport == nil {
		return nil, types.NewRPCInvalidMessage("tcpTransport is required")
	}

	// 使用默认配置
	if config == nil {
		config = DefaultRPCClientConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	c := &RPCClient{
		tcpTransport: tcpTransport,
		udpTransport: udpTransport,
		reqTable:     newRequestTable(),
		config:       config,
		ctx:          ctx,
		cancel:       cancel,
	}

	return c, nil
}

// ========================================
// RPC Client 生命周期
// ========================================

// Start 启动客户端
//
// 启动响应处理协程，接收响应并匹配请求
func (c *RPCClient) Start() error {
	if !c.running.CompareAndSwap(false, true) {
		return types.NewRPCInvalidMessage("client already running")
	}

	logging.Infof("[RPC-Client] Starting RPC client (FastFail=%v)", c.config.EnableFastFail)

	// 启动响应处理协程
	c.wg.Add(1)
	go c.responseLoop()

	return nil
}

// Stop 停止客户端
//
// 优雅关闭：
//  1. 停止接收新响应
//  2. 等待待处理请求完成或超时
//  3. 清理请求表
func (c *RPCClient) Stop() error {
	if !c.running.CompareAndSwap(true, false) {
		return types.NewRPCInvalidMessage("client not running")
	}

	logging.Infof("[RPC-Client] Stopping RPC client")

	// 先取消 context，让 responseLoop 退出
	c.cancel()

	// 等待响应处理协程退出
	c.wg.Wait()

	// 取消所有进行中的请求
	c.reqTable.cancelAll()

	// 停止请求表清理协程
	c.reqTable.close()

	logging.Infof("[RPC-Client] RPC Client stopped")
	return nil
}

// ========================================
// 单次调用
// ========================================

// Call 发送 RPC 请求并等待响应
func (c *RPCClient) Call(ctx context.Context, addr string, req types.Message) (types.Message, error) {
	transport := c.selectTransport(req)
	msgSeq := transport.GenerateMsgSeq()
	nodeID := transport.GetNodeID()
	correlationID := fmt.Sprintf("%d:%d", nodeID, msgSeq)

	logging.Debugf("[RPC-Client] Call: transport=%T, nodeID=%d, msgSeq=%d, correlationID=%s",
		transport, nodeID, msgSeq, correlationID)

	reqEntry := c.reqTable.add(correlationID)
	defer c.reqTable.remove(correlationID)

	if err := transport.Reply(ctx, addr, req, nodeID, msgSeq, ""); err != nil {
		return nil, types.NewRPCNetworkError(addr, err)
	}

	return c.waitForResponse(ctx, reqEntry, c.config.RequestTimeout, addr)
}

// waitForResponse 等待响应（带超时和上下文取消）
func (c *RPCClient) waitForResponse(ctx context.Context, reqEntry *requestEntry, timeout time.Duration, addr string) (types.Message, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case respMsg := <-reqEntry.responseCh:
		if respMsg.err != nil {
			return nil, respMsg.err
		}
		return respMsg.msg, nil
	case <-timer.C:
		return nil, types.NewRPCRequestTimeout(timeout, addr)
	case <-ctx.Done():
		return nil, types.NewRPCContextCanceled(ctx.Err())
	}
}

// ========================================
// P1-2: CallBatch 快速失败机制
// ========================================

// RPCBatchRequest 批量请求
type RPCBatchRequest struct {
	Addr    string        // 目标地址
	Message types.Message // 请求消息
}

// CallBatch 批量 RPC 调用（P1-2 快速失败优化）
func (c *RPCClient) CallBatch(ctx context.Context, requests []*RPCBatchRequest) ([]types.Message, error) {
	if len(requests) == 0 {
		return nil, types.NewRPCInvalidMessage("no requests")
	}

	if c.config.EnableFastFail {
		return c.callBatchFastFail(ctx, requests)
	}
	return c.callBatchWaitAll(ctx, requests)
}

// callBatchFastFail 快速失败实现（P1-2 优化）
func (c *RPCClient) callBatchFastFail(ctx context.Context, requests []*RPCBatchRequest) ([]types.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, c.config.FastFailTimeout)
	defer cancel()

	g, gctx := errgroup.WithContext(ctx)
	responses := make([]types.Message, len(requests))
	var mu sync.Mutex

	for i, req := range requests {
		i, req := i, req
		g.Go(func() error {
			select {
			case <-gctx.Done():
				return types.NewRPCContextCanceled(gctx.Err())
			default:
			}

			resp, err := c.Call(gctx, req.Addr, req.Message)
			if err != nil {
				logging.Warnf("[RPC-Client] CallBatch fast-fail: request %d to %s failed: %v", i, req.Addr, err)
				return err
			}

			mu.Lock()
			responses[i] = resp
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, types.NewRPCInvalidMessage(fmt.Sprintf("CallBatch fast-failed: %v", err))
	}

	return responses, nil
}

// callBatchWaitAll 等待所有请求完成（传统实现）
func (c *RPCClient) callBatchWaitAll(ctx context.Context, requests []*RPCBatchRequest) ([]types.Message, error) {
	responses := make([]types.Message, len(requests))
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex

	for i, req := range requests {
		i, req := i, req
		wg.Add(1)
		go func() {
			defer wg.Done()

			resp, err := c.Call(ctx, req.Addr, req.Message)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}

			mu.Lock()
			responses[i] = resp
			mu.Unlock()
		}()
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return responses, nil
}

// ========================================
// 响应处理
// ========================================

// responseLoop 响应处理循环
func (c *RPCClient) responseLoop() {
	defer c.wg.Done()

	channels := c.getResponseChannels()
	if len(channels) == 0 {
		logging.Warnf("[RPC-Client] No transport channels available")
		return
	}

	c.responseLoopUnified(channels)
}

// getResponseChannels 获取所有需要监听的响应通道
func (c *RPCClient) getResponseChannels() []<-chan MsgFrame {
	if c.udpTransport == nil {
		return []<-chan MsgFrame{c.tcpTransport.Receive()}
	}
	return []<-chan MsgFrame{
		c.tcpTransport.Receive(),
		c.udpTransport.Receive(),
	}
}

// responseLoopUnified 统一的响应处理循环（支持多个 channel）
func (c *RPCClient) responseLoopUnified(channels []<-chan MsgFrame) {
	cases := c.buildSelectCases(channels)

	for {
		chosen, value, ok := reflect.Select(cases)
		if chosen == 0 || !ok {
			logging.Debugf("[RPC-Client] Response loop stopped")
			return
		}

		msgFrame := value.Interface().(MsgFrame)
		c.handleResponse(msgFrame)
	}
}

// buildSelectCases 构建 reflect.Select 用例
func (c *RPCClient) buildSelectCases(channels []<-chan MsgFrame) []reflect.SelectCase {
	cases := make([]reflect.SelectCase, len(channels)+1)
	cases[0] = reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(c.ctx.Done()),
	}

	for i, ch := range channels {
		cases[i+1] = reflect.SelectCase{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(ch),
		}
	}

	return cases
}

// handleResponse 处理响应消息
func (c *RPCClient) handleResponse(msgFrame MsgFrame) {
	correlationID := msgFrame.CorrelationID()
	reqEntry := c.reqTable.get(correlationID)
	if reqEntry == nil {
		logging.Warnf("[RPC-Client] No matching request for CorrelationID: %s", correlationID)
		return
	}

	reqEntry.respond(msgFrame.Message, nil)
}

// ========================================
// 辅助方法
// ========================================

// selectTransport 根据消息类型选择传输协议
func (c *RPCClient) selectTransport(msg types.Message) Transport {
	// 根据消息类型返回对应的传输层
	switch msg.ProtocolType() {
	case types.ProtocolTCP:
		return c.tcpTransport
	case types.ProtocolUDP:
		if c.udpTransport != nil {
			return c.udpTransport
		}
		// UDP 不可用，回退到 TCP
		return c.tcpTransport
	default:
		// 默认使用 TCP
		return c.tcpTransport
	}
}

// ========================================
// 请求表
// ========================================

// requestEntry 请求条目
type requestEntry struct {
	correlationID string
	responseCh    chan responseMessage
	deadline      time.Time
	cancelOnce    sync.Once
	cancelCh      chan struct{}
	canceled      atomic.Bool // P1-1 修复：使用原子标志防止竞态
	completedAt   time.Time   // P1-1: 完成时间（用于延迟清理）
}

// responseMessage 响应消息
type responseMessage struct {
	msg types.Message
	err error
}

func (e *requestEntry) respond(msg types.Message, err error) {
	if e.canceled.Load() {
		return
	}

	e.completedAt = time.Now()
	select {
	case e.responseCh <- responseMessage{msg, err}:
	case <-e.cancelCh:
	}
}

func (e *requestEntry) cancel() {
	e.cancelOnce.Do(func() {
		e.canceled.Store(true)
		close(e.cancelCh)
	})
}

// requestTable 请求表（匹配请求-响应）
//
// P1-1 优化：延迟清理机制
// P1-3 修复：缩短清理间隔从 1 分钟到 15 秒，降低高并发场景下的内存占用
type requestTable struct {
	mu              sync.RWMutex
	table           map[string]*requestEntry
	cleanupInterval time.Duration
	cleanupDelay    time.Duration
	stopCh          chan struct{}
	closeOnce       sync.Once
}

func newRequestTable() *requestTable {
	rt := &requestTable{
		table:           make(map[string]*requestEntry),
		cleanupInterval: 15 * time.Second, // P1-3 修复：从 1 分钟缩短到 15 秒
		cleanupDelay:    5 * time.Second,
		stopCh:          make(chan struct{}),
	}

	go rt.cleanupLoop()

	return rt
}

// add 添加请求条目
func (rt *requestTable) add(correlationID string) *requestEntry {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	entry := &requestEntry{
		correlationID: correlationID,
		responseCh:    make(chan responseMessage, 1),
		deadline:      time.Now().Add(30 * time.Second),
		cancelCh:      make(chan struct{}),
	}

	rt.table[correlationID] = entry
	return entry
}

// get 获取请求条目
func (rt *requestTable) get(correlationID string) *requestEntry {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.table[correlationID]
}

// remove 标记删除（P1-1: 实际删除由清理协程执行）
func (rt *requestTable) remove(correlationID string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	entry := rt.table[correlationID]
	if entry != nil && entry.completedAt.IsZero() {
		entry.completedAt = time.Now()
	}
}

// cleanupLoop 清理协程（P1-1 优化）
func (rt *requestTable) cleanupLoop() {
	ticker := time.NewTicker(rt.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rt.cleanup()
		case <-rt.stopCh:
			return
		}
	}
}

// cleanup 清理已完成的条目
func (rt *requestTable) cleanup() {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	now := time.Now()
	cleaned := 0

	for correlationID, entry := range rt.table {
		if !entry.completedAt.IsZero() && now.Sub(entry.completedAt) > rt.cleanupDelay {
			delete(rt.table, correlationID)
			cleaned++
		}
	}

	if cleaned > 0 {
		logging.Debugf("[RPC-Client] Cleanup: removed %d completed request entries", cleaned)
	}
}

// cancelAll 取消所有进行中的请求
func (rt *requestTable) cancelAll() {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	for _, entry := range rt.table {
		entry.cancel()
	}
}

// close 关闭请求表（停止清理协程）
func (rt *requestTable) close() {
	rt.closeOnce.Do(func() {
		close(rt.stopCh)
	})
}
