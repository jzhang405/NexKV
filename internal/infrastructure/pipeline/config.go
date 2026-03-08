package pipeline

// Config Pipeline 配置
type Config struct {
	// 队列配置
	QueueSize int // 默认: 1000

	// 背压配置
	EnableBackpressure bool // 默认: true
	MaxQueueLength     int  // 默认: 10000

	// Worker 配置
	WorkerCount int // 默认: 1（每个 Stage 一个 Worker）

	// 监控配置
	EnableMetrics bool // 默认: true
	EnableTracing bool // 默认: false
}

// DefaultConfig 默认配置
func DefaultConfig() *Config {
	return &Config{
		QueueSize:          1000,
		EnableBackpressure: true,
		MaxQueueLength:     10000,
		WorkerCount:        1,
		EnableMetrics:      true,
		EnableTracing:      false,
	}
}

// QueueSizeOption 设置队列大小
func QueueSizeOption(size int) func(*Config) {
	return func(c *Config) {
		c.QueueSize = size
	}
}

// EnableBackpressureOption 启用背压
func EnableBackpressureOption(enable bool) func(*Config) {
	return func(c *Config) {
		c.EnableBackpressure = enable
	}
}

// MaxQueueLengthOption 设置最大队列长度
func MaxQueueLengthOption(length int) func(*Config) {
	return func(c *Config) {
		c.MaxQueueLength = length
	}
}

// EnableMetricsOption 启用监控
func EnableMetricsOption(enable bool) func(*Config) {
	return func(c *Config) {
		c.EnableMetrics = enable
	}
}
