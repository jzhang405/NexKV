// Package transport 实现传输层基础设施
package transport

import (
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/compressor"
)

// MiddlewareBuilderConfig 中间件构建器配置
// 用于配置化启用/禁用中间件
type MiddlewareBuilderConfig struct {
	// RateLimit 限流配置
	RateLimit struct {
		Enabled           bool
		RequestsPerSecond int
		Burst             int
	}

	// CircuitBreaker 熔断配置
	CircuitBreaker struct {
		Enabled     bool
		Name        string
		MaxRequests uint32
		Timeout     time.Duration
	}

	// Retry 重试配置
	Retry struct {
		Enabled      bool
		MaxAttempts  uint
		InitialDelay time.Duration
		MaxDelay     time.Duration
		MaxTotalTime time.Duration
	}

	// Compression 压缩配置
	Compression struct {
		Enabled             bool
		Algorithm           compressor.CompressorType
		Threshold           int
		MaxDecompressedSize int
	}
}

// DefaultMiddlewareBuilderConfig 默认中间件构建器配置
func DefaultMiddlewareBuilderConfig() MiddlewareBuilderConfig {
	return MiddlewareBuilderConfig{
		RateLimit: struct {
			Enabled           bool
			RequestsPerSecond int
			Burst             int
		}{
			Enabled:           true,
			RequestsPerSecond: 1000,
			Burst:             100,
		},
		CircuitBreaker: struct {
			Enabled     bool
			Name        string
			MaxRequests uint32
			Timeout     time.Duration
		}{
			Enabled:     true,
			Name:        "rpc-circuit-breaker",
			MaxRequests: 3,
			Timeout:     30 * time.Second,
		},
		Retry: struct {
			Enabled      bool
			MaxAttempts  uint
			InitialDelay time.Duration
			MaxDelay     time.Duration
			MaxTotalTime time.Duration
		}{
			Enabled:      true,
			MaxAttempts:  3,
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     5 * time.Second,
			MaxTotalTime: 10 * time.Second,
		},
		Compression: struct {
			Enabled             bool
			Algorithm           compressor.CompressorType
			Threshold           int
			MaxDecompressedSize int
		}{
			Enabled:             true,
			Algorithm:           compressor.Snappy,
			Threshold:           1024,
			MaxDecompressedSize: 10 * 1024 * 1024,
		},
	}
}

// BuildMiddlewareChain 按配置构建中间件链
// 中间件顺序固定：RateLimit → CircuitBreaker → Compression → Retry
// 启用的中间件按顺序添加，禁用的跳过（无空中间件）
func BuildMiddlewareChain(chain service.MiddlewareChain, config MiddlewareBuilderConfig) error {
	// 1. RateLimit（最外层限流）
	if config.RateLimit.Enabled {
		err := chain.Use(NewRateLimitMiddleware(RateLimitConfig{
			RequestsPerSecond: config.RateLimit.RequestsPerSecond,
			Burst:             config.RateLimit.Burst,
		}))
		if err != nil {
			return err
		}
	}

	// 2. CircuitBreaker（熔断保护）
	if config.CircuitBreaker.Enabled {
		err := chain.Use(NewCircuitBreakerMiddleware(CircuitBreakerConfig{
			Name:        config.CircuitBreaker.Name,
			MaxRequests: config.CircuitBreaker.MaxRequests,
			Timeout:     config.CircuitBreaker.Timeout,
		}))
		if err != nil {
			return err
		}
	}

	// 3. Compression（压缩，在 Retry 之前避免重复压缩）
	if config.Compression.Enabled {
		err := chain.Use(NewCompressionMiddleware(CompressionConfig{
			Algorithm:           config.Compression.Algorithm,
			Threshold:           config.Compression.Threshold,
			MaxDecompressedSize: config.Compression.MaxDecompressedSize,
		}))
		if err != nil {
			return err
		}
	}

	// 4. Retry（最内层重试，仅重试网络错误）
	if config.Retry.Enabled {
		err := chain.Use(NewRetryMiddleware(RetryConfig{
			MaxAttempts:  config.Retry.MaxAttempts,
			InitialDelay: config.Retry.InitialDelay,
			MaxDelay:     config.Retry.MaxDelay,
			MaxTotalTime: config.Retry.MaxTotalTime,
		}))
		if err != nil {
			return err
		}
	}

	return nil
}

// BuildMiddlewareChainWithDefaults 使用默认配置构建中间件链
// 全部启用，适合快速开发
func BuildMiddlewareChainWithDefaults(chain service.MiddlewareChain) error {
	return BuildMiddlewareChain(chain, DefaultMiddlewareBuilderConfig())
}

// BuildMinimalChain 构建最小中间件链（仅熔断）
// 适合资源受限场景
func BuildMinimalChain(chain service.MiddlewareChain) error {
	return chain.Use(NewCircuitBreakerMiddleware(DefaultCircuitBreakerConfig()))
}
