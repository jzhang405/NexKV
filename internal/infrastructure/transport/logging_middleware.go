// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/sirupsen/logrus"
)

// LoggingMiddleware 日志中间件
//
// 记录所有发送和接收的消息，包括：
// - 消息类型和大小
// - 源/目标节点
// - 处理耗时
// - 错误信息
type LoggingMiddleware struct {
	logger *logrus.Logger
	level  logrus.Level
}

// LoggingMiddlewareConfig 日志中间件配置
type LoggingMiddlewareConfig struct {
	// Logger 日志记录器（默认使用 logrus.StandardLogger()）
	Logger *logrus.Logger
	// Level 日志级别（默认 Info）
	Level logrus.Level
}

// NewLoggingMiddleware 创建日志中间件
func NewLoggingMiddleware(config *LoggingMiddlewareConfig) *LoggingMiddleware {
	if config == nil {
		config = &LoggingMiddlewareConfig{}
	}

	logger := config.Logger
	if logger == nil {
		logger = logrus.StandardLogger()
	}

	level := config.Level
	if level == logrus.PanicLevel { // 零值检查
		level = logrus.InfoLevel
	}

	return &LoggingMiddleware{
		logger: logger,
		level:  level,
	}
}

// Name 返回中间件名称
func (m *LoggingMiddleware) Name() string {
	return "logging"
}

// Priority 返回中间件优先级
func (m *LoggingMiddleware) Priority() int {
	return service.MiddlewarePriorityLogging
}

// InterceptSend 拦截发送消息
func (m *LoggingMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next service.SendFunc) error {
	// P1 修复：nil ctx 和 next 保护
	if ctx == nil {
		ctx = context.Background()
	}
	if next == nil {
		return service.ErrInvalidParam
	}

	start := time.Now()
	msgID, msgType, payloadLen := extractMsgInfo(msg)

	// P3 修复：先检查日志级别再记录
	if m.logger.IsLevelEnabled(m.level) {
		m.logger.WithFields(buildLogFields("send", peer, msgID, msgType, payloadLen, 0)).
			Log(m.level, "Sending message")
	}

	// 执行下一个中间件
	err := next(ctx, peer, msg)

	// 记录发送结果
	duration := time.Since(start).Milliseconds()
	fields := buildLogFields("send", peer, msgID, msgType, payloadLen, duration)

	if err != nil {
		fields["error"] = err.Error()
		m.logger.WithFields(fields).WithError(err).Error("Send failed")
	} else if m.logger.IsLevelEnabled(m.level) {
		m.logger.WithFields(fields).Log(m.level, "Send completed")
	}

	return err
}

// InterceptReceive 拦截接收消息
func (m *LoggingMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next service.ReceiveFunc) error {
	// P1 修复：nil ctx 和 next 保护
	if ctx == nil {
		ctx = context.Background()
	}
	if next == nil {
		return service.ErrInvalidParam
	}

	start := time.Now()
	msgID, msgType, payloadLen := extractMsgInfo(msg)

	// P3 修复：先检查日志级别再记录
	if m.logger.IsLevelEnabled(m.level) {
		m.logger.WithFields(buildLogFields("receive", peer, msgID, msgType, payloadLen, 0)).
			Log(m.level, "Receiving message")
	}

	// 执行下一个中间件
	err := next(ctx, peer, msg)

	// 记录接收结果
	duration := time.Since(start).Milliseconds()
	fields := buildLogFields("receive", peer, msgID, msgType, payloadLen, duration)

	if err != nil {
		fields["error"] = err.Error()
		m.logger.WithFields(fields).WithError(err).Error("Receive failed")
	} else if m.logger.IsLevelEnabled(m.level) {
		m.logger.WithFields(fields).Log(m.level, "Receive completed")
	}

	return err
}

// extractMsgInfo 提取消息基本信息（P1-4/P1-5 修复：nil 安全）
func extractMsgInfo(msg model.Message) (msgID, msgType string, payloadLen int) {
	if msg == nil {
		return "", "", 0
	}
	return msg.ID(), msg.Type().String(), len(msg.Payload())
}

// buildLogFields 构建日志字段
func buildLogFields(direction string, peer model.PeerID, msgID, msgType string, payloadLen int, durationMs int64) logrus.Fields {
	fields := logrus.Fields{
		"component":   "rpc",
		"middleware":  "logging",
		"direction":   direction,
		"peer":        peer,
		"msg_id":      msgID,
		"msg_type":    msgType,
		"payload_len": payloadLen,
	}
	if durationMs > 0 {
		fields["duration_ms"] = durationMs
	}
	return fields
}

// 确保实现 Middleware 接口
var _ service.Middleware = (*LoggingMiddleware)(nil)
