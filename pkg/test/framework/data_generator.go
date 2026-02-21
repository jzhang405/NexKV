package framework

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// KeyValue 键值对
type KeyValue struct {
	Key   string
	Value []byte
}

// DataGeneratorConfig 数据生成器配置
type DataGeneratorConfig struct {
	// KeyPrefix 键前缀
	KeyPrefix string
	// ValueSize 值大小（字节）
	ValueSize int
	// MinKey最小键序号
	MinKey int
	// MaxKey 最大键序号
	MaxKey int
	// RandomSeed 随机种子（0 表示使用当前时间）
	RandomSeed int64
	// Distribution 键分布类型
	Distribution KeyDistribution
}

// KeyDistribution 键分布类型
type KeyDistribution int

const (
	// DistributionUniform 均匀分布
	DistributionUniform KeyDistribution = iota
	// DistributionZipf Zipf 分布（热点键）
	DistributionZipf
	// DistributionSequential 顺序分布
	DistributionSequential
)

// DefaultDataGeneratorConfig 返回默认配置
func DefaultDataGeneratorConfig() *DataGeneratorConfig {
	return &DataGeneratorConfig{
		KeyPrefix:    "test-key-",
		ValueSize:    100,
		MinKey:       0,
		MaxKey:       10000,
		RandomSeed:   0,
		Distribution: DistributionUniform,
	}
}

// DataGenerator 数据生成器接口
type DataGenerator interface {
	// GenerateKV 生成单个键值对
	GenerateKV() *KeyValue

	// GenerateBatch 批量生成键值对
	GenerateBatch(count int) []*KeyValue

	// GenerateKeyRange 生成指定范围的键
	GenerateKeyRange(start, end int) []string

	// GenerateValue 生成指定大小的值
	GenerateValue(size int) []byte

	// Reset 重置生成器状态
	Reset()
}

// DefaultDataGenerator 默认数据生成器
type DefaultDataGenerator struct {
	config *DataGeneratorConfig
	rand   *rand.Rand
	zipf   *rand.Zipf
}

// NewDataGenerator 创建数据生成器
func NewDataGenerator(config *DataGeneratorConfig) *DefaultDataGenerator {
	if config == nil {
		config = DefaultDataGeneratorConfig()
	}

	seed := config.RandomSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}

	generator := &DefaultDataGenerator{
		config: config,
		rand:   rand.New(rand.NewSource(seed)),
	}

	// 初始化 Zipf 分布（如果需要）
	if config.Distribution == DistributionZipf {
		// Zipf 参数：s=1.5, v=2.0（典型的热点分布）
		keyRange := config.MaxKey - config.MinKey + 1
		generator.zipf = rand.NewZipf(generator.rand, 1.5, 2.0, uint64(keyRange-1))
	}

	return generator
}

// GenerateKV 生成单个键值对
func (g *DefaultDataGenerator) GenerateKV() *KeyValue {
	return &KeyValue{
		Key:   g.generateKey(),
		Value: g.GenerateValue(g.config.ValueSize),
	}
}

// GenerateBatch 批量生成键值对
func (g *DefaultDataGenerator) GenerateBatch(count int) []*KeyValue {
	kvs := make([]*KeyValue, 0, count)
	for i := 0; i < count; i++ {
		kvs = append(kvs, g.GenerateKV())
	}
	return kvs
}

// GenerateKeyRange 生成指定范围的键
func (g *DefaultDataGenerator) GenerateKeyRange(start, end int) []string {
	keys := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		keys = append(keys, fmt.Sprintf("%s%d", g.config.KeyPrefix, i))
	}
	return keys
}

// GenerateValue 生成指定大小的值
func (g *DefaultDataGenerator) GenerateValue(size int) []byte {
	// 生成有意义的随机值（可打印字符）
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 "

	value := make([]byte, size)
	for i := range value {
		value[i] = charset[g.rand.Intn(len(charset))]
	}
	return value
}

// generateKey 根据分布类型生成键
func (g *DefaultDataGenerator) generateKey() string {
	var keyNum int

	switch g.config.Distribution {
	case DistributionUniform:
		// 均匀分布
		keyNum = g.rand.Intn(g.config.MaxKey-g.config.MinKey+1) + g.config.MinKey

	case DistributionZipf:
		// Zipf 分布（热点键）
		if g.zipf != nil {
			keyNum = int(g.zipf.Uint64()) + g.config.MinKey
		} else {
			keyNum = g.rand.Intn(g.config.MaxKey-g.config.MinKey+1) + g.config.MinKey
		}

	case DistributionSequential:
		// 顺序分布（使用随机起点）
		keyNum = g.rand.Intn(g.config.MaxKey-g.config.MinKey+1) + g.config.MinKey

	default:
		keyNum = g.rand.Intn(g.config.MaxKey-g.config.MinKey+1) + g.config.MinKey
	}

	return fmt.Sprintf("%s%d", g.config.KeyPrefix, keyNum)
}

// Reset 重置生成器状态
func (g *DefaultDataGenerator) Reset() {
	seed := g.config.RandomSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	g.rand = rand.New(rand.NewSource(seed))

	if g.config.Distribution == DistributionZipf {
		keyRange := g.config.MaxKey - g.config.MinKey + 1
		g.zipf = rand.NewZipf(g.rand, 1.5, 2.0, uint64(keyRange-1))
	}
}

// SequentialDataGenerator 顺序数据生成器
// 生成连续的键值对，用于范围扫描测试
type SequentialDataGenerator struct {
	config     *DataGeneratorConfig
	currentKey int
}

// NewSequentialDataGenerator 创建顺序数据生成器
func NewSequentialDataGenerator(config *DataGeneratorConfig) *SequentialDataGenerator {
	if config == nil {
		config = DefaultDataGeneratorConfig()
	}
	return &SequentialDataGenerator{
		config:     config,
		currentKey: config.MinKey,
	}
}

// GenerateKV 生成下一个顺序键值对
func (g *SequentialDataGenerator) GenerateKV() *KeyValue {
	if g.currentKey > g.config.MaxKey {
		g.currentKey = g.config.MinKey
	}

	kv := &KeyValue{
		Key:   fmt.Sprintf("%s%d", g.config.KeyPrefix, g.currentKey),
		Value: make([]byte, g.config.ValueSize),
	}

	// 填充值
	for i := range kv.Value {
		kv.Value[i] = byte(g.currentKey % 256)
	}

	g.currentKey++
	return kv
}

// GenerateBatch 批量生成顺序键值对
func (g *SequentialDataGenerator) GenerateBatch(count int) []*KeyValue {
	kvs := make([]*KeyValue, 0, count)
	for i := 0; i < count; i++ {
		kvs = append(kvs, g.GenerateKV())
	}
	return kvs
}

// GenerateKeyRange 生成指定范围的键
func (g *SequentialDataGenerator) GenerateKeyRange(start, end int) []string {
	keys := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		keys = append(keys, fmt.Sprintf("%s%d", g.config.KeyPrefix, i))
	}
	return keys
}

// GenerateValue 生成指定大小的值
func (g *SequentialDataGenerator) GenerateValue(size int) []byte {
	value := make([]byte, size)
	for i := range value {
		value[i] = byte(g.currentKey % 256)
	}
	return value
}

// Reset 重置到起始位置
func (g *SequentialDataGenerator) Reset() {
	g.currentKey = g.config.MinKey
}

// HotspotDataGenerator 热点数据生成器
// 生成具有明显热点分布的数据，用于测试缓存效果
type HotspotDataGenerator struct {
	config      *DataGeneratorConfig
	rand        *rand.Rand
	hotKeys     []string
	hotKeyRatio float64 // 热点键访问比例
}

// NewHotspotDataGenerator 创建热点数据生成器
func NewHotspotDataGenerator(config *DataGeneratorConfig, hotKeyCount int, hotKeyRatio float64) *HotspotDataGenerator {
	if config == nil {
		config = DefaultDataGeneratorConfig()
	}

	seed := config.RandomSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}

	// 生成热点键列表
	r := rand.New(rand.NewSource(seed))
	hotKeys := make([]string, hotKeyCount)
	for i := 0; i < hotKeyCount; i++ {
		keyNum := r.Intn(config.MaxKey-config.MinKey+1) + config.MinKey
		hotKeys[i] = fmt.Sprintf("%s%d", config.KeyPrefix, keyNum)
	}

	return &HotspotDataGenerator{
		config:      config,
		rand:        r,
		hotKeys:     hotKeys,
		hotKeyRatio: hotKeyRatio,
	}
}

// GenerateKV 生成键值对（大概率命中热点）
func (g *HotspotDataGenerator) GenerateKV() *KeyValue {
	var key string

	// 按比例决定是否访问热点键
	if g.rand.Float64() < g.hotKeyRatio && len(g.hotKeys) > 0 {
		// 访问热点键
		key = g.hotKeys[g.rand.Intn(len(g.hotKeys))]
	} else {
		// 访问随机键
		keyNum := g.rand.Intn(g.config.MaxKey-g.config.MinKey+1) + g.config.MinKey
		key = fmt.Sprintf("%s%d", g.config.KeyPrefix, keyNum)
	}

	return &KeyValue{
		Key:   key,
		Value: make([]byte, g.config.ValueSize),
	}
}

// GenerateBatch 批量生成键值对
func (g *HotspotDataGenerator) GenerateBatch(count int) []*KeyValue {
	kvs := make([]*KeyValue, 0, count)
	for i := 0; i < count; i++ {
		kvs = append(kvs, g.GenerateKV())
	}
	return kvs
}

// GenerateKeyRange 生成指定范围的键
func (g *HotspotDataGenerator) GenerateKeyRange(start, end int) []string {
	keys := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		keys = append(keys, fmt.Sprintf("%s%d", g.config.KeyPrefix, i))
	}
	return keys
}

// GenerateValue 生成指定大小的值
func (g *HotspotDataGenerator) GenerateValue(size int) []byte {
	value := make([]byte, size)
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 "
	for i := range value {
		value[i] = charset[g.rand.Intn(len(charset))]
	}
	return value
}

// Reset 重置生成器状态
func (g *HotspotDataGenerator) Reset() {
	seed := g.config.RandomSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	g.rand = rand.New(rand.NewSource(seed))
}

// GetHotKeys 返回热点键列表
func (g *HotspotDataGenerator) GetHotKeys() []string {
	return g.hotKeys
}

// ParseKeyNumber 从键中提取序号
func ParseKeyNumber(key, prefix string) (int, error) {
	if !strings.HasPrefix(key, prefix) {
		return 0, fmt.Errorf("key %s does not have prefix %s", key, prefix)
	}

	var num int
	_, err := fmt.Sscanf(key, prefix+"%d", &num)
	if err != nil {
		return 0, fmt.Errorf("failed to parse key number: %w", err)
	}

	return num, nil
}
