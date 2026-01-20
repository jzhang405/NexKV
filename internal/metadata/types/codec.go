// Package types 定义内部通用的数据类型
//
// 避免各层之间的循环依赖，提供统一的类型定义
package types

// CodecType 编解码器类型
//
// 用于标识数据序列化/反序列化格式
// 应用场景：WAL 持久化、网络传输、快照存储
type CodecType uint16

const (
	// CodecTypeMessagePack MessagePack 编解码
	//
	// 特点：二进制格式，高效紧凑，支持丰富数据类型
	// 性能：编码/解码速度约为 JSON 的 2-3 倍
	// 数据大小：约为 JSON 的 30-50%
	CodecTypeMessagePack CodecType = 1

	// CodecTypeJSON JSON 编解码
	//
	// 特点：文本格式，可读性好，支持跨语言
	// 性能：编码/解码速度相对较慢
	// 数据大小：约为 MessagePack 的 2-3 倍
	CodecTypeJSON CodecType = 2

	// CodecTypeProtobuf Protobuf 编解码（预留）
	//
	// 特点：二进制格式，极致性能，强类型 Schema
	// 性能：编码/解码速度约为 JSON 的 3-5 倍
	// 数据大小：约为 JSON 的 40-60%
	// 状态：PR-002 实现
	CodecTypeProtobuf CodecType = 3
)

// String 返回 CodecType 的字符串表示
func (c CodecType) String() string {
	switch c {
	case CodecTypeMessagePack:
		return "msgpack"
	case CodecTypeJSON:
		return "json"
	case CodecTypeProtobuf:
		return "protobuf"
	default:
		return "unknown"
	}
}

// Validate 验证 CodecType 是否有效
func (c CodecType) Validate() error {
	switch c {
	case CodecTypeMessagePack, CodecTypeJSON, CodecTypeProtobuf:
		return nil
	default:
		return NewStoreInvalidParameterError("CodecType")
	}
}

// Codec 基础编解码器接口（可选，未来扩展）
//
// 定义统一的序列化抽象，用于 types 包内部使用
type Codec interface {
	// Encode 编码数据
	Encode(v any) ([]byte, error)

	// Decode 解码数据
	Decode(data []byte, v any) error

	// Type 返回编解码器类型
	Type() CodecType

	// Name 返回编解码器名称
	Name() string
}

// CompressionType 压缩算法类型
//
// 用于标识数据压缩/解压缩算法
// 应用场景：Snapshot 文件压缩、WAL 压缩（可选）、网络传输压缩
type CompressionType uint16

const (
	// CompressionTypeNone 无压缩
	//
	// 特点：不进行压缩，数据直接存储
	// 适用场景：调试、小数据量、性能敏感场景
	CompressionTypeNone CompressionType = 0

	// CompressionTypeSnappy Snappy 压缩
	//
	// 特点：Google 开发，极快速度，中等压缩率
	// 性能：压缩/解压速度极快（~500MB/s per core）
	// 压缩率：约为原始数据的 40-60%
	// 适用场景：对性能要求高、压缩率要求一般的场景
	// CGO：纯 Go 实现，无 CGO 依赖
	CompressionTypeSnappy CompressionType = 1

	// CompressionTypeZSTD Zstandard 压缩
	//
	// 特点：Facebook 开发，可配置压缩级别，高压缩率
	// 性能：压缩速度中等（~100-300MB/s per core），解压速度极快（~500MB/s per core）
	// 压缩率：约为原始数据的 30-50%（优于 Snappy）
	// 适用场景：对压缩率要求高、存储空间受限的场景
	// CGO：纯 Go 实现，无 CGO 依赖
	// 压缩级别：1-9（默认 3，级别越高压缩率越好但速度越慢）
	CompressionTypeZSTD CompressionType = 2

	// CompressionTypeLZ4 LZ4 压缩
	//
	// 特点：极高速度，低压缩率
	// 性能：压缩/解压速度极快（~400-700MB/s per core）
	// 压缩率：约为原始数据的 50-70%（低于 ZSTD）
	// 适用场景：对速度要求极高、压缩率要求一般的场景
	// CGO：纯 Go 实现，无 CGO 依赖
	CompressionTypeLZ4 CompressionType = 3
)

// String 返回 CompressionType 的字符串表示
func (c CompressionType) String() string {
	switch c {
	case CompressionTypeNone:
		return "none"
	case CompressionTypeSnappy:
		return "snappy"
	case CompressionTypeZSTD:
		return "zstd"
	case CompressionTypeLZ4:
		return "lz4"
	default:
		return "unknown"
	}
}

// Validate 验证 CompressionType 是否有效
func (c CompressionType) Validate() error {
	switch c {
	case CompressionTypeNone, CompressionTypeSnappy, CompressionTypeZSTD, CompressionTypeLZ4:
		return nil
	default:
		return NewStoreInvalidParameterError("CompressionType")
	}
}

// DefaultCompressionLevel 返回压缩算法的默认压缩级别
//
// 返回值：
// - CompressionTypeNone: 0（无压缩级别）
// - CompressionTypeSnappy: 0（Snappy 不支持压缩级别）
// - CompressionTypeZSTD: 3（ZSTD 默认级别，范围 1-9）
// - CompressionTypeLZ4: 0（LZ4 默认级别，范围 0-9，其中 0=默认，1=最快，9=最好压缩）
func (c CompressionType) DefaultCompressionLevel() int {
	switch c {
	case CompressionTypeNone:
		return 0
	case CompressionTypeSnappy:
		return 0 // Snappy 不支持压缩级别
	case CompressionTypeZSTD:
		return 3 // ZSTD 默认级别
	case CompressionTypeLZ4:
		return 0 // LZ4 默认级别（HC 模式）
	default:
		return 0
	}
}

// Compressor 压缩器接口
//
// 定义统一的压缩抽象，用于 types 包内部使用
type Compressor interface {
	// Compress 压缩数据
	// 返回压缩后的数据和错误信息
	Compress(data []byte) ([]byte, error)

	// Decompress 解压数据
	// 返回解压后的数据和错误信息
	Decompress(data []byte) ([]byte, error)

	// Type 返回压缩算法类型
	Type() CompressionType

	// Name 返回压缩算法名称
	Name() string
}

// Priority 消息优先级
//
// 用于标识消息处理优先级，支持 QoS（服务质量）控制
// 应用场景：关键消息优先处理、流量控制、负载均衡
type Priority uint8

const (
	// PriorityLow 低优先级
	//
	// 适用场景：后台同步、非关键元数据更新
	// 处理策略：在网络拥塞时可能被延迟或丢弃
	// 示例：Gossip 摘要同步、统计信息上报
	PriorityLow Priority = 0

	// PriorityNormal 普通优先级（默认）
	//
	// 适用场景：常规元数据操作、节点心跳
	// 处理策略：按先进先出（FIFO）顺序处理
	// 示例：Get/Put/Delete 请求、NodePing/NodePong
	PriorityNormal Priority = 1

	// PriorityHigh 高优先级
	//
	// 适用场景：重要但非阻塞的操作
	// 处理策略：优先于普通/低优先级消息处理
	// 示例：Quorum 提案、Gossip 同步、节点状态变更
	PriorityHigh Priority = 2

	// PriorityCritical 关键优先级
	//
	// 适用场景：系统关键操作、阻塞式协调
	// 处理策略：最高优先级，立即处理，不丢弃
	// 示例：2PC 协议消息、Leader 选举、故障恢复
	PriorityCritical Priority = 3
)

// String 返回 Priority 的字符串表示
func (p Priority) String() string {
	switch p {
	case PriorityLow:
		return "low"
	case PriorityNormal:
		return "normal"
	case PriorityHigh:
		return "high"
	case PriorityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// Validate 验证 Priority 是否有效
func (p Priority) Validate() error {
	switch p {
	case PriorityLow, PriorityNormal, PriorityHigh, PriorityCritical:
		return nil
	default:
		return NewStoreInvalidParameterError("Priority")
	}
}
