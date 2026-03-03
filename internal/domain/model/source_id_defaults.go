package model

// SourceID 默认值变量
// 用于 TaskExecutor 的 CPU 亲和性调度
var (
	// SourceBTree BTree 数据源
	SourceBTree = MustParseSourceID("btree:core:op")

	// SourceWAL WAL 数据源
	SourceWAL = MustParseSourceID("wal:writer:flush")

	// SourceNetwork 网络数据源
	SourceNetwork = MustParseSourceID("network:rpc:send")

	// SourceGC 垃圾回收数据源
	SourceGC = MustParseSourceID("gc:core:cleanup")

	// SourceReplication 复制数据源
	SourceReplication = MustParseSourceID("replication:sync:replicate")

	// SourceCompaction 压缩数据源
	SourceCompaction = MustParseSourceID("compaction:core:compact")

	// SourceDefault 默认数据源（用于不需要 CPU 亲和性的场景）
	SourceDefault = MustParseSourceID("default:general:task")
)
