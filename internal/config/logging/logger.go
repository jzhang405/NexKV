// Package logging 提供全局日志管理
package logging

import (
	"os"
	"sync"

	"github.com/sirupsen/logrus"
)

var (
	globalLogger *logrus.Logger
	once         sync.Once
	loggerOnce   sync.Once
)

// Init 初始化全局日志
func Init(logger *logrus.Logger) {
	once.Do(func() {
		globalLogger = logger
	})
}

// GetLogger 获取全局日志实例（线程安全）
func GetLogger() *logrus.Logger {
	loggerOnce.Do(func() {
		if globalLogger == nil {
			// 默认配置
			globalLogger = logrus.New()
			globalLogger.SetLevel(logrus.InfoLevel)
			globalLogger.SetFormatter(&logrus.JSONFormatter{
				TimestampFormat: "2006-01-02 15:04:05",
			})
			globalLogger.SetOutput(os.Stdout)
		}
	})
	return globalLogger
}

// WithField 创建带字段的日志条目
func WithField(key string, value any) *logrus.Entry {
	return GetLogger().WithField(key, value)
}

// WithFields 创建带多个字段的日志条目
func WithFields(fields logrus.Fields) *logrus.Entry {
	return GetLogger().WithFields(fields)
}

// Debug 记录调试日志
func Debug(args ...any) {
	GetLogger().Debug(args...)
}

// Debugf 记录格式化调试日志
func Debugf(format string, args ...any) {
	GetLogger().Debugf(format, args...)
}

// Info 记录信息日志
func Info(args ...any) {
	GetLogger().Info(args...)
}

// Infof 记录格式化信息日志
func Infof(format string, args ...any) {
	GetLogger().Infof(format, args...)
}

// Warn 记录警告日志
func Warn(args ...any) {
	GetLogger().Warn(args...)
}

// Warnf 记录格式化警告日志
func Warnf(format string, args ...any) {
	GetLogger().Warnf(format, args...)
}

// Error 记录错误日志
func Error(args ...any) {
	GetLogger().Error(args...)
}

// Errorf 记录格式化错误日志
func Errorf(format string, args ...any) {
	GetLogger().Errorf(format, args...)
}

// Fatal 记录致命日志并退出
func Fatal(args ...any) {
	GetLogger().Fatal(args...)
}

// Fatalf 记录格式化致命日志并退出
func Fatalf(format string, args ...any) {
	GetLogger().Fatalf(format, args...)
}
