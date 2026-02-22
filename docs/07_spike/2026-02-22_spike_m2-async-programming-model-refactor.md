# 异步编程模型重构方案 v2.2

> **预研类型**: Spike
> **创建日期**: 2026-02-22
> **最后更新**: 2026-02-22
> **分支**: `spike/async-programming-model`
> **状态**: ✅ 已批准（作为 M2 存储引擎前置依赖）
> **文档版本**: v2.2
> **设计原则**: DDD 分层架构、方案B并行实现、生产级可用
> **参考文档**:
>   - [DDD架构 - AsyncOperation](./2026-02-18_spike_nexkv-ddd-interface.md#13-b3-asyncoperation)
>   - [DDD架构 - GoroutineProvider](./2026-02-18_spike_nexkv-ddd-interface.md#13-b4-goroutineprovider)
>   - [M2存储引擎 - 异步接口](./2026-02-21_spike_m2-storage-engine-interface.md#11-asyncoperation)

---

## 目录

1. [架构概述](#一架构概述)
2. [API 层](#二api-层)
3. [Control Plane 层](#三control-plane-层)
4. [Domain 层](#四domain-层)
5. [Storage Engine 层](#五storage-engine-层)
6. [Infrastructure 层](#六infrastructure-层)
7. [实施路线图](#七实施路线图)
8. [方案B：并行实现策略](#八方案b并行实现策略)
9. [总结](#九总结)

---

## 一、架构概述

### 1.1 NexKV 5层架构

```
┌─────────────────────────────────────────────────────────────┐
│                     API 层                                  │
│  REST API / gRPC / CLI                                      │
├─────────────────────────────────────────────────────────────┤
│                  Control Plane 层                           │
│  集群管理 / 路由 / 负载均衡 / 配置管理                        │
├─────────────────────────────────────────────────────────────┤
│                    Domain 层                                │
│  领域模型 / 领域服务 / 业务逻辑                               │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │  Async RPC   │  │   Broadcast  │  │    KV Ops    │      │
│  │   Service    │  │    Service   │  │   Service    │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
├─────────────────────────────────────────────────────────────┤
│                 Storage Engine 层                           │
│  KV存储 / 索引 / 事务 / 复制                                  │
├─────────────────────────────────────────────────────────────┤
│                  Infrastructure 层                          │
│  网络 / 存储 / 协程池 / 监控                                  │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │  Transport   │  │  Goroutine   │  │   AsyncOp    │      │
│  │   (RPC)      │  │   Provider   │  │   Impl       │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
└─────────────────────────────────────────────────────────────┘
```

### 1.2 异步编程模型在各层的分布

| 层级 | 组件 | 异步抽象 | 说明 |
|------|------|----------|------|
| **API** | REST/gRPC Handler | `AsyncOperation[T]` | 异步响应客户端请求 |
| **Control Plane** | Cluster Manager | `AsyncGroup[T]` | 批量节点操作 |
| **Domain** | RPC Service | `RPCAsync` 接口 | 领域级异步服务 |
| **Domain** | Broadcast Service | `BroadcastCallback` | 广播回调接口 |
| **Storage** | KV Engine | `AsyncOperation[KVResult]` | 异步存储操作 |
| **Infrastructure** | Transport | `AsyncOp[T]` 实现 | 底层异步实现 |
| **Infrastructure** | Goroutine Pool | `GoroutineProvider` | 协程池管理 |

---

## 二、API 层

### 2.1 职责
- 接收外部请求（REST/gRPC/CLI）
- 参数校验与转换
- 调用 Control Plane 或 Domain 层服务
- 异步响应客户端

### 2.2 异步接口设计

```go
// internal/api/http/handler.go

package http

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/async"
)

// KVHandler KV HTTP 处理器
type KVHandler struct {
	kvService service.KVService
}

// NewKVHandler 创建 KV 处理器
func NewKVHandler(kvService service.KVService) *KVHandler {
	return &KVHandler{kvService: kvService}
}

// SetAsync 异步设置键值
// POST /api/v1/kv/async
func (h *KVHandler) SetAsync(c *gin.Context) {
	var req SetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	ctx := c.Request.Context()
	
	// 调用 Domain 层服务，返回 AsyncOperation
	op := h.kvService.SetAsync(ctx, req.Key, req.Value)
	
	// 方式1: 阻塞等待（短超时）
	result, err := op.Get(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, SuccessResponse{Data: result})
}

// SetAsyncWithCallback 异步设置（回调风格）
// POST /api/v1/kv/async/callback
func (h *KVHandler) SetAsyncWithCallback(c *gin.Context) {
	var req SetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	ctx := c.Request.Context()
	op := h.kvService.SetAsync(ctx, req.Key, req.Value)
	
	// 方式2: 注册回调，立即返回操作ID
	cbID := op.OnComplete(func(result service.KVResult, err error) {
		// 异步处理结果（如推送 WebSocket、记录日志等）
		if err != nil {
			log.Printf("Async set failed: %v", err)
			return
		}
		log.Printf("Async set succeeded: LSN=%d", result.LSN)
	})
	
	c.JSON(http.StatusAccepted, AsyncResponse{
		OperationID: "op-xxx", // 实际应从 op 获取ID
		CallbackID:  cbID,
		Status:      "pending",
	})
}
```

### 2.3 API 层使用模式

```go
// 模式1: 阻塞等待（适合短操作）
func (h *KVHandler) Get(c *gin.Context) {
	op := h.kvService.GetAsync(ctx, key)
	result, err := op.Get(ctx) // 阻塞等待
	// ...
}

// 模式2: 回调风格（适合长操作）
func (h *KVHandler) BatchSet(c *gin.Context) {
	op := h.kvService.BatchSetAsync(ctx, items)
	op.OnComplete(func(result BatchResult, err error) {
		// 异步处理
	})
	c.JSON(http.StatusAccepted, AsyncResponse{...})
}

// 模式3: Channel 风格（适合流式处理）
func (h *KVHandler) Watch(c *gin.Context) {
	op := h.kvService.WatchAsync(ctx, key)
	for result := range op.ResultChan() {
		// 流式响应
		sse.Write(result)
	}
}
```

---

## 三、Control Plane 层

### 3.1 职责
- 集群状态管理
- 请求路由与负载均衡
- 节点发现与健康检查
- 批量操作协调

### 3.2 异步集群管理

```go
// internal/controlplane/cluster_manager.go

package controlplane

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/async"
)

// ClusterManager 集群管理器
type ClusterManager struct {
	mu          sync.RWMutex
	nodes       map[model.NodeID]*NodeInfo
	rpcService  service.RPCAsync
	gp          async.GoroutineProvider
}

// NodeInfo 节点信息
type NodeInfo struct {
	ID       model.NodeID
	Address  string
	Status   NodeStatus
	LastSeen time.Time
}

type NodeStatus int

const (
	NodeStatusHealthy NodeStatus = iota
	NodeStatusUnhealthy
	NodeStatusOffline
)

// CheckNodesHealthAsync 异步检查所有节点健康状态
// 返回 AsyncGroup，可以等待全部或多数节点响应
func (cm *ClusterManager) CheckNodesHealthAsync(ctx context.Context) *async.AsyncGroup[HealthResult] {
	cm.mu.RLock()
	nodes := make([]model.NodeID, 0, len(cm.nodes))
	for id := range cm.nodes {
		nodes = append(nodes, id)
	}
	cm.mu.RUnlock()

	// 使用 AsyncGroup 批量检查
	return async.NewGroup[HealthResult](ctx, nodes, func(ctx context.Context, nodeID model.NodeID) (HealthResult, error) {
		return cm.checkSingleNode(ctx, nodeID)
	})
}

// RebalanceAsync 异步数据重平衡
// 协调多个分片的数据迁移
func (cm *ClusterManager) RebalanceAsync(ctx context.Context, plan *RebalancePlan) async.AsyncOperation[RebalanceResult] {
	return async.NewOp[RebalanceResult](ctx, func(ctx context.Context) (RebalanceResult, error) {
		// 1. 获取需要迁移的分片
		shards := plan.GetShardsToMove()
		
		// 2. 为每个分片创建迁移操作
		migrations := make([]async.AsyncOperation[MigrationResult], 0, len(shards))
		for _, shard := range shards {
			op := cm.migrateShardAsync(ctx, shard)
			migrations = append(migrations, op)
		}
		
		// 3. 等待所有迁移完成
		var results []MigrationResult
		for _, op := range migrations {
			result, err := op.Get(ctx)
			if err != nil {
				// 记录错误但继续
				log.Printf("Migration failed: %v", err)
			}
			results = append(results, result)
		}
		
		return RebalanceResult{Migrations: results}, nil
	})
}

// checkSingleNode 检查单个节点健康
func (cm *ClusterManager) checkSingleNode(ctx context.Context, nodeID model.NodeID) (HealthResult, error) {
	cm.mu.RLock()
	node, exists := cm.nodes[nodeID]
	cm.mu.RUnlock()
	
	if !exists {
		return HealthResult{}, fmt.Errorf("node not found: %s", nodeID)
	}
	
	// 发送健康检查请求
	req := model.Message{Type: model.MsgTypeHealthCheck}
	op := cm.rpcService.CallAsync(ctx, nodeID, req)
	
	resp, err := op.Get(ctx)
	if err != nil {
		return HealthResult{NodeID: nodeID, Healthy: false}, err
	}
	
	return HealthResult{
		NodeID:  nodeID,
		Healthy: resp.Status == model.StatusOK,
	}, nil
}
```

### 3.3 Control Plane 批量操作模式

```go
// 等待所有节点响应
func (cm *ClusterManager) WaitAllNodes(ctx context.Context) {
	group := cm.CheckNodesHealthAsync(ctx)
	
	// 方式1: 等待全部
	results := group.WaitAll(ctx)
	
	// 方式2: 等待多数
	majorityResult := group.WaitMajority(ctx)
	
	// 方式3: 等待任意一个
	firstResult := group.WaitAny(ctx)
}
```

---

## 四、Domain 层

### 4.1 职责
- 领域模型定义
- 业务逻辑实现
- 领域服务编排
- 事务管理

### 4.2 领域服务接口

```go
// internal/domain/service/kv_service.go

package service

import (
	"context"

	"github.com/jzhang405/NexKV/pkg/async"
)

// KVService KV 领域服务接口
type KVService interface {
	// SetAsync 异步设置键值
	SetAsync(ctx context.Context, key, value []byte) async.AsyncOperation[KVResult]
	
	// GetAsync 异步获取键值
	GetAsync(ctx context.Context, key []byte) async.AsyncOperation[KVResult]
	
	// DeleteAsync 异步删除键
	DeleteAsync(ctx context.Context, key []byte) async.AsyncOperation[KVResult]
	
	// BatchSetAsync 批量异步设置
	BatchSetAsync(ctx context.Context, items []KVItem) async.AsyncOperation[BatchResult]
}

// KVResult KV 操作结果
type KVResult struct {
	Key   []byte
	Value []byte
	LSN   uint64
	Err   error
}

// KVItem KV 项
type KVItem struct {
	Key   []byte
	Value []byte
}

// BatchResult 批量操作结果
type BatchResult struct {
	SuccessCount int
	FailedCount  int
	Results      []KVResult
}
```

### 4.3 RPC 领域服务

```go
// internal/domain/service/rpc_async.go

package service

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/async"
)

// RPCAsync 异步 RPC 领域服务接口
// 位于 Domain 层，定义领域级别的异步通信契约
type RPCAsync interface {
	// CallAsync 单播异步调用
	// 对指定节点发送请求，返回 AsyncOperation 等待响应
	CallAsync(ctx context.Context, to model.PeerID, req model.Message) async.AsyncOperation[model.Message]
	
	// BroadcastAsync 广播异步调用
	// 向多个节点广播请求，返回 AsyncGroup 管理批量响应
	BroadcastAsync(
		ctx context.Context,
		to []model.PeerID,
		req model.Message,
		strategy ResponseStrategy,
	) *async.AsyncGroup[model.Message]
}

// rpcAsyncImpl RPCAsync 实现
type rpcAsyncImpl struct {
	transport transport.Transport  // Infrastructure 层依赖
	gp        async.GoroutineProvider
}

// NewRPCAsync 创建异步 RPC 服务
func NewRPCAsync(transport transport.Transport, gp async.GoroutineProvider) RPCAsync {
	return &rpcAsyncImpl{
		transport: transport,
		gp:        gp,
	}
}

// CallAsync 实现
func (r *rpcAsyncImpl) CallAsync(
	ctx context.Context,
	to model.PeerID,
	req model.Message,
) async.AsyncOperation[model.Message] {
	// 调用 Infrastructure 层的 Transport
	return async.NewOp[model.Message](ctx, func(ctx context.Context) (model.Message, error) {
		return r.transport.Call(ctx, to, req)
	}, async.WithGoroutineProvider(r.gp))
}

// BroadcastAsync 实现
func (r *rpcAsyncImpl) BroadcastAsync(
	ctx context.Context,
	to []model.PeerID,
	req model.Message,
	strategy ResponseStrategy,
) *async.AsyncGroup[model.Message] {
	return async.NewGroup[model.Message](ctx, to, func(ctx context.Context, target model.PeerID) (model.Message, error) {
		return r.transport.Call(ctx, target, req)
	})
}
```

### 4.4 Broadcast 领域服务

```go
// internal/domain/service/broadcast.go

package service

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/async"
)

// BroadcastCallback 广播回调接口
// 领域层定义的广播回调契约
type BroadcastCallback interface {
	OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats)
	OnFailure(peer model.PeerID, err error, stats BroadcastStats)
	OnMajorityReached(stats BroadcastStats)
	OnFullDone(stats BroadcastStats)
}

// BroadcastStats 广播统计
type BroadcastStats struct {
	TotalPeers        int
	SuccessCount      int
	FailureCount      int
	StartTime         time.Time
	FirstResponseTime time.Time
	MajorityReachTime time.Time
	EndTime           time.Time
}

// BroadcastService 广播服务
type BroadcastService struct {
	rpc RPCAsync
	gp  async.GoroutineProvider
}

// BroadcastWithCallback 带回调的广播
func (s *BroadcastService) BroadcastWithCallback(
	ctx context.Context,
	peers []model.PeerID,
	req model.Message,
	strategy ResponseStrategy,
	callback BroadcastCallback,
) {
	group := s.rpc.BroadcastAsync(ctx, peers, req, strategy)
	
	// 设置回调桥接
	group.SetCallback(callback)
}

// ResponseStrategy 响应策略
type ResponseStrategy int

const (
	StrategyWaitAll ResponseStrategy = iota
	StrategyWaitMajority
	StrategyWaitAny
	StrategyWaitQuorum
)
```

### 4.5 KV 服务实现

```go
// internal/domain/service/kv_service_impl.go

package service

import (
	"context"

	"github.com/jzhang405/NexKV/internal/storage"
	"github.com/jzhang405/NexKV/pkg/async"
)

// kvServiceImpl KVService 实现
type kvServiceImpl struct {
	storage storage.KVStorage  // Storage Engine 层依赖
	gp      async.GoroutineProvider
}

// NewKVService 创建 KV 服务
func NewKVService(storage storage.KVStorage, gp async.GoroutineProvider) KVService {
	return &kvServiceImpl{
		storage: storage,
		gp:      gp,
	}
}

// SetAsync 实现
func (s *kvServiceImpl) SetAsync(ctx context.Context, key, value []byte) async.AsyncOperation[KVResult] {
	return async.NewOp[KVResult](ctx, func(ctx context.Context) (KVResult, error) {
		// 调用 Storage Engine 层
		lsn, err := s.storage.Set(ctx, key, value)
		if err != nil {
			return KVResult{Key: key, Err: err}, err
		}
		
		return KVResult{
			Key:   key,
			Value: value,
			LSN:   lsn,
		}, nil
	}, async.WithGoroutineProvider(s.gp))
}

// GetAsync 实现
func (s *kvServiceImpl) GetAsync(ctx context.Context, key []byte) async.AsyncOperation[KVResult] {
	return async.NewOp[KVResult](ctx, func(ctx context.Context) (KVResult, error) {
		value, lsn, err := s.storage.Get(ctx, key)
		if err != nil {
			return KVResult{Key: key, Err: err}, err
		}
		
		return KVResult{
			Key:   key,
			Value: value,
			LSN:   lsn,
		}, nil
	}, async.WithGoroutineProvider(s.gp))
}

// BatchSetAsync 实现
func (s *kvServiceImpl) BatchSetAsync(ctx context.Context, items []KVItem) async.AsyncOperation[BatchResult] {
	return async.NewOp[BatchResult](ctx, func(ctx context.Context) (BatchResult, error) {
		var results []KVResult
		var successCount, failedCount int
		
		for _, item := range items {
			op := s.SetAsync(ctx, item.Key, item.Value)
			result, err := op.Get(ctx)
			
			if err != nil {
				failedCount++
			} else {
				successCount++
			}
			results = append(results, result)
		}
		
		return BatchResult{
			SuccessCount: successCount,
			FailedCount:  failedCount,
			Results:      results,
		}, nil
	}, async.WithGoroutineProvider(s.gp))
}
```

---

## 五、Storage Engine 层

### 5.1 职责
- KV 存储引擎实现
- WAL（Write-Ahead Log）
- MemTable / SSTable
- 复制与一致性

### 5.2 异步存储接口

```go
// internal/storage/kv_storage.go

package storage

import (
	"context"
)

// KVStorage KV 存储接口
// Storage Engine 层定义的存储契约
type KVStorage interface {
	// Set 设置键值（同步接口，内部可异步实现）
	Set(ctx context.Context, key, value []byte) (uint64, error)
	
	// Get 获取键值
	Get(ctx context.Context, key []byte) ([]byte, uint64, error)
	
	// Delete 删除键
	Delete(ctx context.Context, key []byte) (uint64, error)
	
	// Sync 刷盘
	Sync() error
}

// AsyncKVStorage 异步 KV 存储接口（可选扩展）
type AsyncKVStorage interface {
	KVStorage
	
	// SetAsync 异步设置（返回 LSN）
	SetAsync(ctx context.Context, key, value []byte) <-chan LSNResult
}

type LSNResult struct {
	LSN uint64
	Err error
}
```

### 5.3 存储引擎实现

```go
// internal/storage/engine.go

package storage

import (
	"context"
	"sync"

	"github.com/jzhang405/NexKV/pkg/async"
)

// StorageEngine 存储引擎实现
type StorageEngine struct {
	mu       sync.RWMutex
	wal      *WAL
	memTable *MemTable
	gp       async.GoroutineProvider
}

// NewStorageEngine 创建存储引擎
func NewStorageEngine(gp async.GoroutineProvider) *StorageEngine {
	return &StorageEngine{
		wal:      NewWAL(),
		memTable: NewMemTable(),
		gp:       gp,
	}
}

// Set 实现
func (e *StorageEngine) Set(ctx context.Context, key, value []byte) (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	// 1. 写入 WAL
	lsn, err := e.wal.Append(key, value)
	if err != nil {
		return 0, err
	}
	
	// 2. 写入 MemTable
	if err := e.memTable.Put(key, value); err != nil {
		return 0, err
	}
	
	return lsn, nil
}

// Get 实现
func (e *StorageEngine) Get(ctx context.Context, key []byte) ([]byte, uint64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	
	// 1. 先查 MemTable
	value, found := e.memTable.Get(key)
	if found {
		return value, 0, nil // MemTable 中的数据没有 LSN
	}
	
	// 2. 查 SSTable（简化处理）
	return nil, 0, ErrKeyNotFound
}
```

---

## 六、Infrastructure 层

### 6.1 职责
- 网络传输实现
- 协程池管理
- 异步操作实现
- 监控与日志

### 6.2 目录结构

```
internal/infrastructure/
├── transport/
│   ├── libp2p_rpc.go           # RPC 传输实现
│   ├── libp2p_rpc_adapter.go   # 旧接口适配器
│   └── async_lifecycle.go      # 异步生命周期管理
└── concurrency/
    ├── goroutine_provider.go   # GoroutineProvider 接口
    └── ants_provider.go        # ants 实现

pkg/async/
├── async_op.go                 # AsyncOp 实现
├── async_group.go              # AsyncGroup 实现
└── bridge.go                   # 桥接工具
```

### 6.3 GoroutineProvider 实现

```go
// internal/infrastructure/concurrency/goroutine_provider.go

package concurrency

import (
	"context"
	"time"
)

// GoroutineProvider 协程池提供者接口
type GoroutineProvider interface {
	Submit(task func()) error
	SubmitWithContext(ctx context.Context, task func(context.Context)) error
	SubmitWithResult[T any](task func() (T, error)) Result[T]
	SubmitWithPriority(priority Priority, task func()) error
	SubmitDelayed(delay time.Duration, task func()) error
	SubmitBatch(tasks []func()) error
	SubmitBatchAllErrors(tasks []func()) []error
	SubmitBatchWithResult[T any](tasks []func() (T, error)) []Result[T]
	Stats() PoolStats
	Health() HealthStatus
	SetCapacity(capacity int) error
	Close() error
	CloseWithTimeout(timeout time.Duration) error
}

// Priority 任务优先级
type Priority int

const (
	PriorityCritical Priority = iota
	PriorityHigh
	PriorityNormal
	PriorityLow
)

type Result[T any] struct {
	Value T
	Err   error
}

type PoolStats struct {
	Total      int
	ByPriority map[Priority]int
}

type HealthStatus int

const (
	HealthStatusHealthy HealthStatus = iota
	HealthStatusUnhealthy
)
```

### 6.4 AsyncOperation 实现

```go
// pkg/async/async_op.go

package async

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/infrastructure/transport"
)

// AsyncOperation 统一异步操作接口
type AsyncOperation[T any] interface {
	Get(ctx context.Context) (T, error)
	Status() OperationStatus
	Cancel() (canceled bool, err error)
	Discard() error
	IsStarted() bool
	OnComplete(callback func(T, error)) string
	OffComplete(cbID string) error
}

// OperationStatus 操作状态
type OperationStatus int

const (
	StatusPending OperationStatus = iota
	StatusRunning
	StatusCompleted
	StatusFailed
	StatusCanceled
	StatusDiscarded
	StatusTimeout
)

func (s OperationStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCanceled, StatusDiscarded, StatusTimeout:
		return true
	default:
		return false
	}
}

// AsyncOp 异步操作实现
type AsyncOp[T any] struct {
	lifecycle *transport.AsyncLifecycle
	resultCh  chan Result[T]
	done      chan struct{}
	value     T
	err       error
	callbacks map[string]func(T, error)
	cbMu      sync.RWMutex
	cbSeq     int64
	execFunc  func(ctx context.Context) (T, error)
	status    OperationStatus
	statusMu  sync.RWMutex
	started   bool
	cancel    context.CancelFunc
}

// Result 结果包装器
type Result[T any] struct {
	Value T
	Err   error
}

// OpOption 选项
type OpOption func(*opConfig)

type opConfig struct {
	timeout time.Duration
}

// WithTimeout 设置超时
func WithTimeout(timeout time.Duration) OpOption {
	return func(c *opConfig) {
		c.timeout = timeout
	}
}

// NewOp 创建异步操作
func NewOp[T any](
	ctx context.Context,
	execFunc func(ctx context.Context) (T, error),
	opts ...OpOption,
) AsyncOperation[T] {
	config := &opConfig{timeout: 30 * time.Second}
	for _, opt := range opts {
		opt(config)
	}

	var cancel context.CancelFunc
	if config.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, config.timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}

	lifecycle := transport.NewAsyncLifecycle()

	op := &AsyncOp[T]{
		lifecycle: lifecycle,
		resultCh:  make(chan Result[T], 1),
		done:      make(chan struct{}),
		callbacks: make(map[string]func(T, error)),
		execFunc:  execFunc,
		status:    StatusPending,
		cancel:    cancel,
	}

	lifecycle.Go(func() {
		defer close(op.done)

		op.statusMu.Lock()
		op.status = StatusRunning
		op.started = true
		op.statusMu.Unlock()

		value, err := execFunc(lifecycle.Context())

		op.statusMu.Lock()
		if ctx.Err() == context.DeadlineExceeded {
			op.status = StatusTimeout
			op.err = ctx.Err()
		} else if err != nil {
			op.status = StatusFailed
			op.err = err
			op.value = value
		} else {
			op.status = StatusCompleted
			op.value = value
		}
		op.statusMu.Unlock()

		select {
		case op.resultCh <- Result[T]{Value: op.value, Err: op.err}:
		default:
		}

		op.executeCallbacks(op.value, op.err)
	})

	return op
}

// Get 实现
func (op *AsyncOp[T]) Get(ctx context.Context) (T, error) {
	select {
	case result := <-op.resultCh:
		return result.Value, result.Err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Status 实现
func (op *AsyncOp[T]) Status() OperationStatus {
	op.statusMu.RLock()
	defer op.statusMu.RUnlock()
	return op.status
}

// Cancel 实现
func (op *AsyncOp[T]) Cancel() (bool, error) {
	op.statusMu.Lock()
	defer op.statusMu.Unlock()

	if op.status.IsTerminal() {
		return false, fmt.Errorf("operation already in terminal state: %v", op.status)
	}

	op.status = StatusCanceled
	op.lifecycle.Cancel()
	return true, nil
}

// Discard 实现
func (op *AsyncOp[T]) Discard() error {
	op.statusMu.Lock()
	defer op.statusMu.Unlock()

	if op.status.IsTerminal() {
		return fmt.Errorf("cannot discard operation in terminal state: %v", op.status)
	}

	op.status = StatusDiscarded
	op.lifecycle.Cancel()
	return nil
}

// IsStarted 实现
func (op *AsyncOp[T]) IsStarted() bool {
	op.statusMu.RLock()
	defer op.statusMu.RUnlock()
	return op.started
}

// OnComplete 实现
func (op *AsyncOp[T]) OnComplete(callback func(T, error)) string {
	op.cbMu.Lock()
	defer op.cbMu.Unlock()

	op.cbSeq++
	cbID := fmt.Sprintf("cb-%d", op.cbSeq)

	select {
	case <-op.done:
		go safeCallback(callback, op.value, op.err)
	default:
		op.callbacks[cbID] = callback
	}

	return cbID
}

// OffComplete 实现
func (op *AsyncOp[T]) OffComplete(cbID string) error {
	op.cbMu.Lock()
	defer op.cbMu.Unlock()

	if _, exists := op.callbacks[cbID]; !exists {
		return fmt.Errorf("callback not found: %s", cbID)
	}

	delete(op.callbacks, cbID)
	return nil
}

// ResultChan 返回结果通道（扩展方法）
func (op *AsyncOp[T]) ResultChan() <-chan Result[T] {
	return op.resultCh
}

// executeCallbacks 执行回调
func (op *AsyncOp[T]) executeCallbacks(value T, err error) {
	op.cbMu.RLock()
	callbacks := make([]func(T, error), 0, len(op.callbacks))
	for _, cb := range op.callbacks {
		callbacks = append(callbacks, cb)
	}
	op.cbMu.RUnlock()

	for _, cb := range callbacks {
		cb := cb
		go safeCallback(cb, value, err)
	}
}

// safeCallback 安全执行回调
func safeCallback[T any](callback func(T, error), value T, err error) {
	defer func() {
		if r := recover(); r != nil {
			// 记录 panic 但不影响主流程
		}
	}()
	callback(value, err)
}
```

### 6.5 AsyncGroup 实现

```go
// pkg/async/async_group.go

package async

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/transport"
)

// AsyncGroup 批量异步操作组
type AsyncGroup[T any] struct {
	lifecycle *transport.AsyncLifecycle
	targets   []model.PeerID
	ops       map[model.PeerID]AsyncOperation[T]
	results   map[model.PeerID]T
	errors    map[model.PeerID]error
	mu        sync.RWMutex
	anyDone       chan struct{}
	majorityDone  chan struct{}
	allDone       chan struct{}
	callback      service.BroadcastCallback
	startTime             time.Time
	firstResponseTime     time.Time
	majorityReachTime     time.Time
	firstResponseRecorded bool
}

// GroupResult 批量操作结果
type GroupResult[T any] struct {
	Values       map[model.PeerID]T
	Errors       map[model.PeerID]error
	SuccessPeers []model.PeerID
	FailedPeers  []model.PeerID
}

// NewGroup 创建批量异步操作组
func NewGroup[T any](
	ctx context.Context,
	targets []model.PeerID,
	execFunc func(ctx context.Context, target model.PeerID) (T, error),
) *AsyncGroup[T] {
	lifecycle := transport.NewAsyncLifecycle()

	targetsCopy := make([]model.PeerID, len(targets))
	copy(targetsCopy, targets)

	g := &AsyncGroup[T]{
		lifecycle:    lifecycle,
		targets:      targetsCopy,
		ops:          make(map[model.PeerID]AsyncOperation[T]),
		results:      make(map[model.PeerID]T),
		errors:       make(map[model.PeerID]error),
		anyDone:      make(chan struct{}),
		majorityDone: make(chan struct{}),
		allDone:      make(chan struct{}),
		startTime:    time.Now(),
	}

	for _, target := range targets {
		target := target
		op := NewOp[T](lifecycle.Context(), func(ctx context.Context) (T, error) {
			return execFunc(ctx, target)
		})
		g.ops[target] = op

		op.OnComplete(func(value T, err error) {
			g.handleResult(target, value, err)
		})
	}

	return g
}

// handleResult 处理单个结果
func (g *AsyncGroup[T]) handleResult(peer model.PeerID, value T, err error) {
	var callback service.BroadcastCallback
	var stats service.BroadcastStats
	var shouldTriggerMajority bool
	var shouldTriggerAllDone bool

	g.mu.Lock()

	if !g.firstResponseRecorded {
		g.firstResponseTime = time.Now()
		g.firstResponseRecorded = true
		close(g.anyDone)
	}

	if err != nil {
		g.errors[peer] = err
	} else {
		g.results[peer] = value
	}

	total := len(g.targets)
	success := len(g.results)
	failed := len(g.errors)
	completed := success + failed

	if success >= (total/2)+1 && g.majorityReachTime.IsZero() {
		g.majorityReachTime = time.Now()
		shouldTriggerMajority = true
		close(g.majorityDone)
	}

	if completed >= total {
		shouldTriggerAllDone = true
		close(g.allDone)
	}

	stats = service.BroadcastStats{
		TotalPeers:        total,
		SuccessCount:      success,
		FailureCount:      failed,
		StartTime:         g.startTime,
		FirstResponseTime: g.firstResponseTime,
		MajorityReachTime: g.majorityReachTime,
	}

	callback = g.callback
	g.mu.Unlock()

	if callback != nil {
		if err != nil {
			callback.OnFailure(peer, err, stats)
		} else {
			callback.OnSuccess(peer, value, stats)
		}
		if shouldTriggerMajority {
			callback.OnMajorityReached(stats)
		}
		if shouldTriggerAllDone {
			callback.OnFullDone(stats)
		}
	}
}

// SetCallback 设置回调
func (g *AsyncGroup[T]) SetCallback(callback service.BroadcastCallback) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.callback = callback
}

// WaitAll 等待全部完成
func (g *AsyncGroup[T]) WaitAll(ctx context.Context) GroupResult[T] {
	select {
	case <-g.allDone:
	case <-ctx.Done():
	}

	return g.getResult()
}

// WaitMajority 等待多数完成
func (g *AsyncGroup[T]) WaitMajority(ctx context.Context) GroupResult[T] {
	select {
	case <-g.majorityDone:
	case <-ctx.Done():
	}

	return g.getResult()
}

// WaitAny 等待任意一个完成
func (g *AsyncGroup[T]) WaitAny(ctx context.Context) (model.PeerID, T, error) {
	select {
	case <-g.anyDone:
	case <-ctx.Done():
		var zero T
		return "", zero, ctx.Err()
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	for peer, value := range g.results {
		return peer, value, nil
	}

	for peer, err := range g.errors {
		var zero T
		return peer, zero, err
	}

	var zero T
	return "", zero, fmt.Errorf("no result available")
}

// getResult 获取结果
func (g *AsyncGroup[T]) getResult() GroupResult[T] {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := GroupResult[T]{
		Values: make(map[model.PeerID]T),
		Errors: make(map[model.PeerID]error),
	}

	for peer, value := range g.results {
		result.Values[peer] = value
		result.SuccessPeers = append(result.SuccessPeers, peer)
	}

	for peer, err := range g.errors {
		result.Errors[peer] = err
		result.FailedPeers = append(result.FailedPeers, peer)
	}

	return result
}
```

---

## 七、实施路线图

### 7.1 分层实施顺序

```
Week 1: Infrastructure 层（基础）
├── 实现 GoroutineProvider
├── 实现 AsyncOp / AsyncGroup
└── 单元测试

Week 2: Domain 层（核心）
├── 实现 RPCAsync 接口
├── 实现 BroadcastService
├── 实现 KVService
└── 集成测试

Week 3: Storage Engine + Control Plane
├── Storage Engine 异步支持
├── Cluster Manager 批量操作
└── 端到端测试

Week 4: API 层 + 方案B适配
├── REST/gRPC 异步接口
├── 旧接口适配器
└── 全量回归测试
```

### 7.2 依赖关系

```
API 层
  ↓ 依赖
Control Plane 层
  ↓ 依赖
Domain 层
  ↓ 依赖
Storage Engine 层
  ↓ 依赖
Infrastructure 层（最基础）
```

---

## 八、方案B：并行实现策略

### 8.1 核心思想

**新旧实现并存，内部统一，逐步替换**

```
┌─────────────────────────────────────────┐
│           API 层（保持不变）              │
├─────────────────────────────────────────┤
│      Domain 层（适配器模式）              │
│  旧接口 ──→ 适配器 ──→ 新 AsyncOperation │
├─────────────────────────────────────────┤
│  ┌─────────────┐    ┌─────────────┐   │
│  │  旧实现      │    │  新实现      │   │
│  │  pendingCall│    │  AsyncOp[T] │   │
│  │  Channel    │    │  泛型接口    │   │
│  │  (冻结维护)  │    │  (活跃开发)  │   │
│  └─────────────┘    └─────────────┘   │
├─────────────────────────────────────────┤
│  Infrastructure 层（共享）               │
│  AsyncLifecycle / GoroutineProvider     │
└─────────────────────────────────────────┘
```

### 8.2 适配器实现

```go
// internal/domain/service/rpc_adapter.go

package service

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/async"
)

// RPCAdapter 为旧代码提供兼容层
type RPCAdapter struct {
	rpc RPCAsync
}

// NewRPCAdapter 创建适配器
func NewRPCAdapter(rpc RPCAsync) *RPCAdapter {
	return &RPCAdapter{rpc: rpc}
}

// CallAsyncOld 旧风格调用（回调式）
func (a *RPCAdapter) CallAsyncOld(
	ctx context.Context,
	to model.PeerID,
	req model.Message,
	cb func(model.Message, error),
) {
	op := a.rpc.CallAsync(ctx, to, req)
	op.OnComplete(cb)
}

// BroadcastAsyncOld 旧风格广播
func (a *RPCAdapter) BroadcastAsyncOld(
	ctx context.Context,
	to []model.PeerID,
	req model.Message,
	strategy ResponseStrategy,
	onSuccess func(peer model.PeerID, resp model.Message),
	onFailure func(peer model.PeerID, err error),
) {
	group := a.rpc.BroadcastAsync(ctx, to, req, strategy)
	
	callback := &legacyBroadcastCallback{
		onSuccess: onSuccess,
		onFailure: onFailure,
	}
	group.SetCallback(callback)
}

type legacyBroadcastCallback struct {
	onSuccess func(peer model.PeerID, resp model.Message)
	onFailure func(peer model.PeerID, err error)
}

func (c *legacyBroadcastCallback) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
	if c.onSuccess != nil {
		c.onSuccess(peer, resp)
	}
}

func (c *legacyBroadcastCallback) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
	if c.onFailure != nil {
		c.onFailure(peer, err)
	}
}

func (c *legacyBroadcastCallback) OnMajorityReached(stats BroadcastStats) {}
func (c *legacyBroadcastCallback) OnFullDone(stats BroadcastStats)       {}
```

### 8.3 迁移检查清单

- [ ] **Week 1**: Infrastructure 层实现
- [ ] **Week 2**: Domain 层新接口
- [ ] **Week 3**: 创建适配器
- [ ] **Week 3**: 标记旧接口为 `@Deprecated`
- [ ] **Week 4**: 逐个模块迁移
- [ ] **Week 4**: 删除适配层和旧实现

---

## 九、总结

### 9.1 DDD 分层架构优势

| 层级 | 职责清晰 | 可测试性 | 可替换性 |
|------|----------|----------|----------|
| API | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ |
| Control Plane | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐☆ |
| Domain | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐☆ |
| Storage Engine | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐☆ | ⭐⭐⭐⭐⭐ |
| Infrastructure | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ |

### 9.2 核心设计决策

1. **接口定义位置**
   - `AsyncOperation[T]` → `pkg/async`（跨层共享）
   - `GoroutineProvider` → `internal/infrastructure/concurrency`
   - `RPCAsync` / `KVService` → `internal/domain/service`

2. **依赖方向**
   - 上层依赖下层（Domain → Infrastructure）
   - 通过接口解耦（依赖倒置）

3. **异步抽象层级**
   - Infrastructure: 底层实现（AsyncOp）
   - Domain: 领域服务（RPCAsync）
   - API: 接口暴露（HTTP Handler）

### 9.3 参考文档

1. **DDD架构 - AsyncOperation**: [2026-02-18_spike_nexkv-ddd-interface.md](./2026-02-18_spike_nexkv-ddd-interface.md#13-b3-asyncoperation)
2. **DDD架构 - GoroutineProvider**: [2026-02-18_spike_nexkv-ddd-interface.md](./2026-02-18_spike_nexkv-ddd-interface.md#13-b4-goroutineprovider)
3. **M2存储引擎 - 异步接口**: [2026-02-21_spike_m2-storage-engine-interface.md](./2026-02-21_spike_m2-storage-engine-interface.md#11-asyncoperation)

---

**文档版本**: v2.2
**最后更新**: 2026-02-22
**变更说明**: 按 DDD 5层架构重新组织文档结构，合并原非 DDD 版本
