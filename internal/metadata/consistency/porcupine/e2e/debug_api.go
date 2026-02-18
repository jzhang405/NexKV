// Package e2e 提供 E2E 测试框架的 Porcupine 验证集成
// 本文件实现安全的 Debug API 端点
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine/runtime"
)

// ==================== 配置定义 ====================

// DebugAPIConfig Debug API 配置
type DebugAPIConfig struct {
	// 基础配置
	Enabled  bool   `json:"enabled"`  // 是否启用
	Endpoint string `json:"endpoint"` // 监听地址（默认 127.0.0.1:0）

	// 安全配置
	AuthToken       string        `json:"auth_token"`        // 认证 Token（空则禁用认证）
	RequireLocal    bool          `json:"require_local"`     // 仅允许本地访问
	RequestTimeout  time.Duration `json:"request_timeout"`   // 请求超时
	RateLimit       int           `json:"rate_limit"`        // 每分钟请求限制
	MaxResponseSize int           `json:"max_response_size"` // 响应大小上限（字节）

	// 数据保护
	SanitizeData bool `json:"sanitize_data"` // 是否脱敏敏感数据
}

// DefaultDebugAPIConfig 默认配置（安全优先）
func DefaultDebugAPIConfig() DebugAPIConfig {
	return DebugAPIConfig{
		Enabled:         false,            // 默认禁用
		Endpoint:        "127.0.0.1:0",    // 仅绑定本地，随机端口
		RequireLocal:    true,             // 仅本地访问
		RequestTimeout:  10 * time.Second, // 10 秒超时
		RateLimit:       10,               // 每分钟 10 次
		MaxResponseSize: 10 * 1024 * 1024, // 10MB
		SanitizeData:    true,             // 默认脱敏
	}
}

// ==================== Debug API 服务器 ====================

// DebugAPIServer Debug API 服务器
type DebugAPIServer struct {
	config     DebugAPIConfig
	verifier   *runtime.RuntimeVerifier
	serializer *porcupine.OperationSerializer

	// 服务器状态
	server *http.Server
	mu     sync.RWMutex

	// 速率限制
	requestTimes []time.Time

	// 审计日志
	auditLog []AuditEntry
}

// AuditEntry 审计日志条目
type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	ClientIP  string    `json:"client_ip"`
	Action    string    `json:"action"`
	Result    string    `json:"result"` // "success" | "denied" | "error"
	Error     string    `json:"error,omitempty"`
}

// NewDebugAPIServer 创建 Debug API 服务器
func NewDebugAPIServer(config DebugAPIConfig, verifier *runtime.RuntimeVerifier) *DebugAPIServer {
	return &DebugAPIServer{
		config:       config,
		verifier:     verifier,
		serializer:   porcupine.NewOperationSerializer(),
		requestTimes: make([]time.Time, 0, 100),
		auditLog:     make([]AuditEntry, 0, 1000),
	}
}

// Start 启动 Debug API 服务器
func (s *DebugAPIServer) Start() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.config.Enabled {
		return "", fmt.Errorf("debug API is disabled")
	}

	// 创建监听器
	listener, err := net.Listen("tcp", s.config.Endpoint)
	if err != nil {
		return "", fmt.Errorf("failed to listen on %s: %w", s.config.Endpoint, err)
	}

	// 获取实际监听地址
	addr := listener.Addr().String()

	// 创建 HTTP 服务器
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/porcupine/history", s.handleGetHistory)
	mux.HandleFunc("/debug/porcupine/stats", s.handleGetStats)
	mux.HandleFunc("/debug/porcupine/health", s.handleHealth)

	s.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  s.config.RequestTimeout,
		WriteTimeout: s.config.RequestTimeout,
	}

	// 启动服务器（后台）
	go func() {
		_ = s.server.Serve(listener)
	}()

	return addr, nil
}

// Stop 停止 Debug API 服务器
func (s *DebugAPIServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(ctx)
	}
	return nil
}

// ==================== HTTP 处理器 ====================

// handleGetHistory 获取操作历史
func (s *DebugAPIServer) handleGetHistory(w http.ResponseWriter, r *http.Request) {
	// 1. 安全检查
	if !s.checkAccess(w, r, "GetHistory") {
		return
	}

	// 2. 获取操作历史
	ops := s.verifier.Recorder().GetOperations()

	// 3. 序列化操作
	serialized := make([]*porcupine.SerializableOperation, 0, len(ops))
	for _, op := range ops {
		ser, err := s.serializer.SerializeOperation(op)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "serialize failed: %v", err)
			s.audit(r, "GetHistory", "error", err.Error())
			return
		}

		// 可选：脱敏
		if s.config.SanitizeData {
			s.sanitizeOperation(ser)
		}

		serialized = append(serialized, ser)
	}

	// 4. 构建响应
	response := map[string]interface{}{
		"node_id":    "local", // TODO: 从配置获取
		"operations": serialized,
		"count":      len(serialized),
		"timestamp":  time.Now().Unix(),
	}

	// 5. 检查响应大小
	respBytes, err := json.Marshal(response)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "marshal failed: %v", err)
		return
	}

	if len(respBytes) > s.config.MaxResponseSize {
		s.writeError(w, http.StatusRequestEntityTooLarge,
			"response too large: %d > %d", len(respBytes), s.config.MaxResponseSize)
		s.audit(r, "GetHistory", "denied", "response too large")
		return
	}

	// 6. 返回响应
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(respBytes)
	s.audit(r, "GetHistory", "success", "")
}

// handleGetStats 获取统计信息
func (s *DebugAPIServer) handleGetStats(w http.ResponseWriter, r *http.Request) {
	if !s.checkAccess(w, r, "GetStats") {
		return
	}

	stats := s.verifier.Stats()
	response := map[string]interface{}{
		"stats":     stats,
		"timestamp": time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
	s.audit(r, "GetStats", "success", "")
}

// handleHealth 健康检查
func (s *DebugAPIServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	// 健康检查不需要认证（用于服务发现）

	stats := s.verifier.Stats()
	lastResult := s.verifier.GetLastResult()

	health := map[string]interface{}{
		"status":         "ok",
		"total_ops":      stats.TotalOps,
		"pending_ops":    stats.PendingOps,
		"last_verify_ok": lastResult != nil && lastResult.AllPassed(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(health)
}

// ==================== 安全检查 ====================

// checkAccess 检查访问权限
func (s *DebugAPIServer) checkAccess(w http.ResponseWriter, r *http.Request, action string) bool {
	// 1. 检查是否启用
	if !s.config.Enabled {
		s.writeError(w, http.StatusServiceUnavailable, "debug API is disabled")
		s.audit(r, action, "denied", "disabled")
		return false
	}

	// 2. 检查本地访问
	if s.config.RequireLocal {
		clientIP := s.getClientIP(r)
		if !s.isLocalIP(clientIP) {
			s.writeError(w, http.StatusForbidden, "remote access denied")
			s.audit(r, action, "denied", "remote access")
			return false
		}
	}

	// 3. 检查认证
	if s.config.AuthToken != "" {
		token := r.Header.Get("X-Debug-Token")
		if token != s.config.AuthToken {
			s.writeError(w, http.StatusUnauthorized, "invalid token")
			s.audit(r, action, "denied", "invalid token")
			return false
		}
	}

	// 4. 检查速率限制
	if s.config.RateLimit > 0 {
		if !s.checkRateLimit() {
			s.writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			s.audit(r, action, "denied", "rate limit")
			return false
		}
	}

	return true
}

// checkRateLimit 检查速率限制
func (s *DebugAPIServer) checkRateLimit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	minuteAgo := now.Add(-time.Minute)

	// 清理旧的请求记录
	valid := make([]time.Time, 0, len(s.requestTimes))
	for _, t := range s.requestTimes {
		if t.After(minuteAgo) {
			valid = append(valid, t)
		}
	}
	s.requestTimes = valid

	// 检查是否超过限制
	if len(s.requestTimes) >= s.config.RateLimit {
		return false
	}

	// 记录本次请求
	s.requestTimes = append(s.requestTimes, now)
	return true
}

// isLocalIP 检查是否是本地 IP
func (s *DebugAPIServer) isLocalIP(ip string) bool {
	// 检查常见本地 IP
	localIPs := []string{"127.0.0.1", "::1", "localhost"}
	for _, local := range localIPs {
		if ip == local {
			return true
		}
	}

	// 检查是否是本地回环地址
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	return parsedIP.IsLoopback()
}

// getClientIP 获取客户端 IP
func (s *DebugAPIServer) getClientIP(r *http.Request) string {
	// 优先检查 X-Forwarded-For
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}

	// 检查 X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// 使用 RemoteAddr
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

// ==================== 数据脱敏 ====================

// sanitizeOperation 脱敏操作数据
func (s *DebugAPIServer) sanitizeOperation(op *porcupine.SerializableOperation) {
	// 脱敏 Input 中的 Value 字段
	var input map[string]interface{}
	if err := json.Unmarshal(op.Input, &input); err == nil {
		if topologyOp, ok := input["topology_op"].(map[string]interface{}); ok {
			if value, ok := topologyOp["value"].(string); ok && len(value) > 100 {
				topologyOp["value"] = value[:50] + "...[REDACTED]"
			}
		}
		if frOp, ok := input["failure_recovery_op"].(map[string]interface{}); ok {
			if value, ok := frOp["value"].(string); ok && len(value) > 100 {
				frOp["value"] = value[:50] + "...[REDACTED]"
			}
		}
		if leaderOp, ok := input["leader_ha_op"].(map[string]interface{}); ok {
			if value, ok := leaderOp["value"].(string); ok && len(value) > 100 {
				leaderOp["value"] = value[:50] + "...[REDACTED]"
			}
		}
		if newData, err := json.Marshal(input); err == nil {
			op.Input = newData
		}
	}

	// 脱敏 Output 中的 Value 字段
	var output map[string]interface{}
	if err := json.Unmarshal(op.Output, &output); err == nil {
		if topologyOut, ok := output["topology_out"].(map[string]interface{}); ok {
			if value, ok := topologyOut["value"].(string); ok && len(value) > 100 {
				topologyOut["value"] = value[:50] + "...[REDACTED]"
			}
		}
		if frOut, ok := output["failure_recovery_out"].(map[string]interface{}); ok {
			if value, ok := frOut["value"].(string); ok && len(value) > 100 {
				frOut["value"] = value[:50] + "...[REDACTED]"
			}
		}
		if leaderOut, ok := output["leader_ha_out"].(map[string]interface{}); ok {
			if value, ok := leaderOut["value"].(string); ok && len(value) > 100 {
				leaderOut["value"] = value[:50] + "...[REDACTED]"
			}
		}
		if newData, err := json.Marshal(output); err == nil {
			op.Output = newData
		}
	}
}

// ==================== 审计日志 ====================

// audit 记录审计日志
func (s *DebugAPIServer) audit(r *http.Request, action, result, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := AuditEntry{
		Timestamp: time.Now(),
		ClientIP:  s.getClientIP(r),
		Action:    action,
		Result:    result,
		Error:     errMsg,
	}

	s.auditLog = append(s.auditLog, entry)

	// 保持审计日志大小
	if len(s.auditLog) > 1000 {
		s.auditLog = s.auditLog[100:]
	}
}

// GetAuditLog 获取审计日志
func (s *DebugAPIServer) GetAuditLog() []AuditEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]AuditEntry, len(s.auditLog))
	copy(result, s.auditLog)
	return result
}

// ==================== 辅助方法 ====================

// writeError 写入错误响应
func (s *DebugAPIServer) writeError(w http.ResponseWriter, code int, format string, args ...interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	errorResp := map[string]interface{}{
		"error":     fmt.Sprintf(format, args...),
		"code":      code,
		"timestamp": time.Now().Unix(),
	}
	_ = json.NewEncoder(w).Encode(errorResp)
}
