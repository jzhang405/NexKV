// Package config 提供日志初始化功能
package config

import (
	"os"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/sirupsen/logrus"
)

// InitLogger 初始化日志系统
func InitLogger(cfg *Config) (*logrus.Logger, error) {
	logger := logrus.New()

	// 设置日志级别
	level, err := logrus.ParseLevel(cfg.Logging.Level)
	if err != nil {
		return nil, err
	}
	logger.SetLevel(level)

	// 设置日志格式
	switch cfg.Logging.Format {
	case "json":
		logger.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: "2006-01-02 15:04:05",
			FieldMap: logrus.FieldMap{
				logrus.FieldKeyTime:  "timestamp",
				logrus.FieldKeyLevel: "level",
				logrus.FieldKeyMsg:   "message",
				logrus.FieldKeyFunc:  "caller",
			},
		})
	case "text":
		logger.SetFormatter(&logrus.TextFormatter{
			TimestampFormat: "2006-01-02 15:04:05",
			FullTimestamp:   true,
		})
	default:
		logger.SetFormatter(&logrus.JSONFormatter{})
	}

	// 设置日志输出
	switch cfg.Logging.Output {
	case "stdout":
		logger.SetOutput(os.Stdout)
	case "file":
		if cfg.Logging.File == "" {
			logger.SetOutput(os.Stdout)
		} else {
			file, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
			if err != nil {
				return nil, err
			}
			logger.SetOutput(file)
		}
	default:
		logger.SetOutput(os.Stdout)
	}

	return logger, nil
}

// GetLogger 获取全局日志实例
func GetLogger() *logrus.Logger {
	return logging.GetLogger()
}
