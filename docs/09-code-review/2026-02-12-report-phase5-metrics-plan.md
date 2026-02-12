# 阶段 5.2：Metrics 补充计划

> NexKV 监控指标补充方案

**创建时间**：2026-02-12
**分析方法**：静态代码审查 + 现有实现分析

---

## Metrics 实现现状

### 现有 Metrics

| 指标 | 类型 | 状态 | 文件位置 |
|------|------|------|----------|
| QPS | Gauge | ✅ 已实现 | `internal/rpc/integration_monitoring.go` |
| 请求延迟 | Histogram | ✅ 已实现 | `internal/rpc/integration_monitoring.go` |
| 在线节点 | Gauge | ✅ 已实现 | `internal/rpc/integration_monitoring.go` |
| Leader 变更 | Counter | ✅ 已实现 | `internal/rpc/integration_monitoring.go` |
| 分片总数 | Gauge | ✅ 已实现 | `internal/rpc/integration_monitoring.go` |
| 写入字节数 | Counter | ✅ 已实现 | `internal/rpc/integration_monitoring.go` |

**代码证据**：
```go
// internal/rpc/integration_monitoring.go
metrics := GetRPCMetrics()
metrics.RecordConnectionOpened(peerID.String())
metrics.RecordStreamCreated(peerID.String())
metrics.RecordStreamClosed(peerID.String())
metrics.RecordCallStart(peerID.String(), "test.method")
metrics.RecordCallEnd(peerID.String())
metrics.RecordConnectionClosed(peerID.String())
```

---

## 缺失的关键 Metrics

### 集群健康指标

| 指标 | 类型 | 优先级 | 说明 |
|------|------|--------|----------|
| Gossip 收敛时间 | Histogram | P0 | 评估 Gossip 协议性能 |
| Gossip 消息吞吐 | Gauge | P0 | 监控 Gossip 消息队列深度 |
| Quorum 操作延迟 | Histogram | P1 | Quorum 请求响应时间 |
| Quorum 失败率 | Gauge | P1 | Quorum 失败请求占比 |
| 节点健康度 | Gauge | P2 | 综合节点健康评分 |

### 存储指标

| 指标 | 类型 | 优先级 | 说明 |
|------|------|--------|----------|
| MVStore 写入延迟 | Histogram | P0 | 元数据写入性能 |
| MVStore 操作吞吐 | Gauge | P0 | 每秒操作数 |
| WAL 追加延迟 | Histogram | P1 | WAL 写入性能 |
| WAL 文件大小 | Gauge | P1 | WAL 文件大小监控 |
| Checkpoint 间隔 | Gauge | P2 | Checkpoint 创建频率 |
| 快照大小 | Gauge | P2 | 快照文件大小监控 |

### 系统资源指标

| 指标 | 类型 | 优先级 | 说明 |
|------|------|--------|----------|
| Goroutine 数量 | Gauge | P0 | 当前运行的 goroutine 数 |
| 内存使用 | Gauge | P0 | 堆内存分配 |
| GC 暂停时间 | Histogram | P1 | GC 暂停统计 |
| 文件描述符 | Gauge | P2 | 打开文件描述符数量 |

---

## Prometheus 集成方案

### HTTP 端点配置

```go
// internal/metrics/http_handler.go（新建）
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/promhttp"
    "net/http"
)

var (
    requestDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
        Name: "nexkv_request_duration_seconds",
        Help: "RPC 请求延迟分布",
    Buckets: []float64{.005, .01, .025, .05, .1, .5, 1, 2.5, 5, 10},
    })

    qpsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "nexkv_qps",
        Help: "每秒请求数",
    })
)

func RecordRequest(duration time.Duration) {
    requestDuration.Observe(duration.Seconds())
}
```

---

## Metrics 补充计划

### 第一批（本周）- 集群健康

| 指标 | 实现方式 | 预估工作量 |
|------|----------|------------|
| Gossip 收敛时间 | Histogram | 1 天 |
| Gossip 消息吞吐 | Gauge | 0.5 天 |
| Quorum 操作延迟 | Histogram | 1 天 |
| 节点健康度 | Gauge | 2 天（需要定义健康度算法） |

### 第二批（2 周内）- 存储与系统

| 指标 | 实现方式 | 预估工作量 |
|------|----------|------------|
| MVStore 性能指标 | Histogram | 2 天 |
| WAL 性能指标 | Histogram | 2 天 |
| 系统资源指标 | Gauge | 3 天 |

---

## 代码实现示例

### Gossip 收敛时间指标

```go
// internal/metadata/gossip/metrics.go（新建）
package gossip

import (
    "time"
    "github.com/prometheus/client_golang/prometheus"
)

var (
    gossipSyncDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
        Name: "nexkv_gossip_sync_duration_seconds",
        Help: "Gossip 同步耗时",
        Buckets: prometheus.LinearBuckets(0, 300),
    })
)

func RecordGossipSync(duration time.Duration, success bool) {
    gossipSyncDuration.Observe(duration.Seconds())
    // 记录成功/失败
}
```

### Quorum 操作延迟指标

```go
// internal/metadata/quorum/metrics.go（新建）
package quorum

import (
    "time"
    "github.com/prometheus/client_golang/prometheus"
)

var (
    quorumOperationDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
        Name: "nexkv_quorum_operation_duration_seconds",
        Help: "Quorum 操作耗时",
        Buckets: prometheus.DefaltBuckets,
    })

    quorumFailureTotal = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "nexkv_quorum_failure_total",
        Help: "Quorum 失败总数",
    })
)

func RecordQuorumOperation(op string, duration time.Duration, success bool) {
    quorumOperationDuration.Observe(duration.Seconds())
    if !success {
        quorumFailureTotal.Inc()
    }
}
```

---

## 观察与发现

### ✅ 设计优点

1. **RPC Metrics 已实现**：
   - 连接管理
   - 调用统计
   - 性能监控

2. **使用标准 prometheus 库**：易于集成

### ⚠️ 需要补充的点

| 优先级 | 问题 | 说明 |
|--------|------|----------|
| **P0** | 缺少 Gossip 性能指标 | 影响 Gossip 协议调优 |
| **P0** | 缺少存储性能指标 | 影响 MVStore 性能诊断 |
| **P1** | 缺少 Quorum Metrics | 影响 Quorum 机制调优 |
| **P2** | 缺少系统资源监控 | 影响 集群容量规划 |

### 📌 建议实施

| 优先级 | 建议 | 预估工作量 |
|--------|--------|------------|
| P0 | 实现 Gossip Metrics | 1 天 |
| P0 | 实现存储性能监控 | 2 天 |
| P1 | 实现 Quorum Metrics | 1 天 |
| P2 | 添加系统资源监控 | 3 天 |

---

## 下一步

→ [阶段 5.3：补全关键文档](2026-02-12-report-phase5-documentation-update.md)
