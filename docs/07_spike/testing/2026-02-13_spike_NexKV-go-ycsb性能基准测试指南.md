---
tags: ["NexKV/e2e-testing", "性能测试", "YCSB", "基准测试", "go-ycsb"]
aliases: ["NexKV YCSB基准测试", "go-ycsb集成"]
date: 2026-02-13
status: active
---

# go-ycsb: NexKV 性能基准测试指南

> **GitHub**: [pingcap/go-ycsb](https://github.com/pingcap/go-ycsb)
> **文档版本**: v1.0
> **创建日期**: 2026-02-13
> **目标**: 为 NexKV 集成 YCSB 基准测试

---

## 📋 目录

1. [go-ycsb 概述](#1-go-ycsb-概述)
2. [核心功能与特点](#2-核心功能与特点)
3. [安装与配置](#3-安装与配置)
4. [内置 Workload 详解](#4-内置-workload-详解)
5. [为 NexKV 实现驱动](#5-为-nexkv-实现驱动)
6. [NexKV 驱动完整实现](#6-nexkv-驱动完整实现)
7. [性能测试实战](#7-性能测试实战)
8. [结果分析与解读](#8-结果分析与解读)
9. [高级配置](#9-高级配置)
10. [最佳实践](#10-最佳实践)

---

## 1. go-ycsb 概述

### 1.1 什么是 YCSB?

**YCSB (Yahoo! Cloud Serving Benchmark)** 是 Yahoo! 开发的云服务基准测试工具，用于评估各种数据库系统的性能。它提供了：

- **标准化的工作负载**：模拟真实应用场景
- **可扩展的架构**：易于添加新的数据库驱动
- **全面的指标**：吞吐量、延迟分布等

### 1.2 为什么选择 go-ycsb?

| 特性 | go-ycsb | 原版 YCSB (Java) |
|------|---------|------------------|
| **语言** | Go | Java |
| **性能** | 更低开销 | JVM 开销 |
| **部署** | 单二进制 | 需要 JVM |
| **扩展性** | Go 接口 | Java 接口 |
| **社区** | PingCAP 维护 | Yahoo! 维护 |

**PingCAP 开发 go-ycsb 的原因**：
- 构建标准的 Go 基准测试工具
- 团队更熟悉 Go 语言
- 更好的性能和更低的资源消耗

### 1.3 支持的数据库

go-ycsb 原生支持 **20+ 种数据库**：

| 类别 | 数据库 |
|------|--------|
| **NewSQL** | TiDB, CockroachDB, Yugabyte, Spanner |
| **KV 存储** | TiKV, etcd, Redis, Badger, BoltDB, RocksDB |
| **关系型** | MySQL, PostgreSQL, SQLite |
| **NoSQL** | MongoDB, Cassandra, DynamoDB, Aerospike |
| **对象存储** | S3 (Amazon / S3-compatible) |

---

## 2. 核心功能与特点

### 2.1 核心组件

```
┌─────────────────────────────────────────────────────────────┐
│                      go-ycsb 架构                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐     │
│  │ Workload    │    │ Generator   │    │ Measurement │     │
│  │ (工作负载)   │───▶│ (数据生成器) │───▶│ (指标收集)   │     │
│  └─────────────┘    └─────────────┘    └─────────────┘     │
│         │                                     │             │
│         ▼                                     ▼             │
│  ┌─────────────────────────────────────────────────┐       │
│  │              DB Interface (数据库接口)            │       │
│  │  - Init()  - Close()                            │       │
│  │  - Read()  - Scan()                             │       │
│  │  - Update() - Insert() - Delete()               │       │
│  └─────────────────────────────────────────────────┘       │
│                           │                                │
│                           ▼                                │
│  ┌─────────────────────────────────────────────────┐       │
│  │         Database Driver (数据库驱动)              │       │
│  │  MySQL / Redis / TiKV / NexKV / ...             │       │
│  └─────────────────────────────────────────────────┘       │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 数据生成器

| 生成器 | 用途 | 特点 |
|--------|------|------|
| **CounterGenerator** | 顺序生成 | 生成连续整数 |
| **SequentialGenerator** | 顺序生成 | 可配置步长 |
| **UniformGenerator** | 均匀分布 | 随机均匀选择 |
| **ZipfianGenerator** | Zipf 分布 | 模拟热点数据 |
| **ScrambledZipfianGenerator** | 打乱的 Zipf | 热点但不连续 |
| **SkewedLatestGenerator** | 偏斜最新 | 新数据更热 |
| **HistogramGenerator** | 直方图 | 自定义分布 |

### 2.3 指标收集

| 类型 | 说明 |
|------|------|
| **histogram** | 直方图统计（默认） |
| **raw** | 原始数据输出 |
| **csv** | CSV 格式输出 |

**输出指标**：
- **OPS** (Operations Per Second): 每秒操作数
- **Latency**: 延迟分布（P50, P90, P95, P99）
- **Total Operations**: 总操作数
- **Runtime**: 运行时间

---

## 3. 安装与配置

### 3.1 下载预编译版本

**Linux**:
```bash
wget -c https://github.com/pingcap/go-ycsb/releases/latest/download/go-ycsb-linux-amd64.tar.gz -O - | tar -xz
./go-ycsb --help
```

**macOS**:
```bash
wget -c https://github.com/pingcap/go-ycsb/releases/latest/download/go-ycsb-darwin-amd64.tar.gz -O - | tar -xz
./go-ycsb --help
```

### 3.2 从源码构建

```bash
# 克隆仓库
git clone https://github.com/pingcap/go-ycsb.git
cd go-ycsb

# 构建
make

# 验证
./bin/go-ycsb --help
```

**要求**：
- Go 1.16+
- 如需 FoundationDB：安装 6.2.11 客户端库
- 如需 RocksDB：按 INSTALL 说明安装

### 3.3 目录结构

```
go-ycsb/
├── bin/
│   └── go-ycsb           # 编译后的可执行文件
├── workloads/            # 预定义工作负载
│   ├── workloada         # Workload A: 更新密集
│   ├── workloadb         # Workload B: 读取密集
│   ├── workloadc         # Workload C: 只读
│   ├── workloadd         # Workload D: 插入密集
│   ├── workloade         # Workload E: 小范围扫描
│   └── workloadf         # Workload F: 读取-修改-写入
├── pkg/
│   ├── db/               # 数据库驱动实现
│   ├── generator/        # 数据生成器
│   ├── measurement/      # 指标收集
│   ├── prop/             # 配置属性
│   ├── util/             # 工具函数
│   └── workload/         # 工作负载实现
└── cmd/
    └── go-ycsb/          # 主程序入口
```

---

## 4. 内置 Workload 详解

### 4.1 Workload A: 更新密集型

**场景**：记录写入后频繁更新（如用户会话存储）

```properties
# workloada
recordcount=1000
operationcount=1000
workload=core
readallfields=true
readproportion=0.5
updateproportion=0.5
scanproportion=0
insertproportion=0
```

| 操作 | 比例 |
|------|------|
| Read | 50% |
| Update | 50% |

### 4.2 Workload B: 读取密集型

**场景**：照片标签、用户资料缓存

```properties
# workloadb
readproportion=0.95
updateproportion=0.05
scanproportion=0
insertproportion=0
```

| 操作 | 比例 |
|------|------|
| Read | 95% |
| Update | 5% |

### 4.3 Workload C: 只读

**场景**：用户配置文件只读缓存

```properties
# workloadc
readproportion=1
updateproportion=0
scanproportion=0
insertproportion=0
```

| 操作 | 比例 |
|------|------|
| Read | 100% |

### 4.4 Workload D: 插入密集型

**场景**：用户状态更新、事件日志

```properties
# workloadd
readproportion=0
updateproportion=0
scanproportion=0
insertproportion=1
```

| 操作 | 比例 |
|------|------|
| Insert | 100% |

### 4.5 Workload E: 小范围扫描

**场景**：用户好友列表、关注者列表

```properties
# workloade
readproportion=0
updateproportion=0
scanproportion=0.95
insertproportion=0.05
scanlength=100
```

| 操作 | 比例 |
|------|------|
| Scan | 95% |
| Insert | 5% |

### 4.6 Workload F: 读取-修改-写入

**场景**：用户数据库、文档编辑

```properties
# workloadf
readproportion=0.5
updateproportion=0
scanproportion=0
insertproportion=0
readmodifywriteproportion=0.5
```

| 操作 | 比例 |
|------|------|
| Read | 50% |
| Read-Modify-Write | 50% |

---

## 5. 为 NexKV 实现驱动

### 5.1 DB 接口定义

go-ycsb 使用 `Database` 接口，需要实现以下方法：

```go
// pkg/db/db.go
type Database interface {
    // 初始化数据库连接
    InitThread(ctx context.Context, threadID int, threadCount int) context.Context

    // 清理线程资源
    CleanupThread(ctx context.Context)

    // 关闭数据库连接
    Close() error

    // 读取记录
    Read(ctx context.Context, table string, key string, fields []string) (map[string][]byte, error)

    // 扫描记录
    Scan(ctx context.Context, table string, startKey string, count int64, fields []string) ([]map[string][]byte, error)

    // 更新记录
    Update(ctx context.Context, table string, key string, values map[string][]byte) error

    // 插入记录
    Insert(ctx context.Context, table string, key string, values map[string][]byte) error

    // 删除记录
    Delete(ctx context.Context, table string, key string) error
}
```

### 5.2 NexKV 驱动目录结构

```
go-ycsb/
└── pkg/
    └── db/
        └── nexkv/
            ├── nexkv.go        # 主驱动实现
            ├── nexkv_test.go   # 单元测试
            └── README.md       # 驱动文档
```

### 5.3 配置参数设计

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `nexkv.endpoints` | `127.0.0.1:7946` | NexKV 节点地址，逗号分隔 |
| `nexkv.conns_per_endpoint` | `4` | 每个端点的连接数 |
| `nexkv.timeout` | `5s` | 操作超时时间 |
| `nexkv.namespace` | `ycsb` | 命名空间 |
| `nexkv.consistency` | `strong` | 一致性级别：strong, eventual |
| `nexkv.retry_count` | `3` | 重试次数 |
| `nexkv.retry_interval` | `100ms` | 重试间隔 |

---

## 6. NexKV 驱动完整实现

### 6.1 主驱动文件

```go
// pkg/db/nexkv/nexkv.go
package nexkv

import (
    "context"
    "fmt"
    "strings"
    "sync"
    "time"

    "github.com/magiconair/properties"
    "github.com/pingcap/go-ycsb/pkg/db"
    "github.com/pingcap/go-ycsb/pkg/util"

    "nexkv/client"  // NexKV Go 客户端
)

const (
    // 配置参数名
    endpointsKey         = "nexkv.endpoints"
    connsPerEndpointKey  = "nexkv.conns_per_endpoint"
    timeoutKey           = "nexkv.timeout"
    namespaceKey         = "nexkv.namespace"
    consistencyKey       = "nexkv.consistency"
    retryCountKey        = "nexkv.retry_count"
    retryIntervalKey     = "nexkv.retry_interval"

    // 默认值
    defaultEndpoints         = "127.0.0.1:7946"
    defaultConnsPerEndpoint  = 4
    defaultTimeout           = "5s"
    defaultNamespace         = "ycsb"
    defaultConsistency       = "strong"
    defaultRetryCount        = 3
    defaultRetryInterval     = "100ms"
)

func init() {
    // 注册驱动
    db.Register("nexkv", newNexKVDB)
}

// NexKVDB 实现 db.Database 接口
type NexKVDB struct {
    client     *client.Client
    pool       *connPool
    namespace  string
    timeout    time.Duration
    retryCount int
    retryInt   time.Duration
}

// 配置
type config struct {
    endpoints        []string
    connsPerEndpoint int
    timeout          time.Duration
    namespace        string
    consistency      string
    retryCount       int
    retryInterval    time.Duration
}

// 连接池（简化实现）
type connPool struct {
    mu       sync.Mutex
    conns    []*client.Client
    endpoint string
}

// 创建 NexKV 数据库实例
func newNexKVDB(p *properties.Properties) (db.Database, error) {
    cfg := parseConfig(p)

    // 创建客户端配置
    clientCfg := &client.Config{
        Endpoints:   cfg.endpoints,
        DialTimeout: cfg.timeout,
        Consistency: cfg.consistency,
    }

    // 创建客户端
    c, err := client.New(clientCfg)
    if err != nil {
        return nil, fmt.Errorf("failed to create nexkv client: %w", err)
    }

    // 创建连接池
    pool := &connPool{
        endpoint: cfg.endpoints[0],
    }
    for i := 0; i < cfg.connsPerEndpoint; i++ {
        conn, err := client.New(clientCfg)
        if err != nil {
            return nil, fmt.Errorf("failed to create connection %d: %w", i, err)
        }
        pool.conns = append(pool.conns, conn)
    }

    return &NexKVDB{
        client:     c,
        pool:       pool,
        namespace:  cfg.namespace,
        timeout:    cfg.timeout,
        retryCount: cfg.retryCount,
        retryInt:   cfg.retryInterval,
    }, nil
}

// 解析配置
func parseConfig(p *properties.Properties) *config {
    cfg := &config{
        endpoints:        strings.Split(p.GetString(endpointsKey, defaultEndpoints), ","),
        connsPerEndpoint: p.GetInt(connsPerEndpointKey, defaultConnsPerEndpoint),
        namespace:        p.GetString(namespaceKey, defaultNamespace),
        consistency:      p.GetString(consistencyKey, defaultConsistency),
        retryCount:       p.GetInt(retryCountKey, defaultRetryCount),
    }

    // 解析超时
    timeoutStr := p.GetString(timeoutKey, defaultTimeout)
    cfg.timeout, _ = time.ParseDuration(timeoutStr)

    retryStr := p.GetString(retryIntervalKey, defaultRetryInterval)
    cfg.retryInterval, _ = time.ParseDuration(retryStr)

    return cfg
}

// 获取连接
func (db *NexKVDB) getConn() *client.Client {
    db.pool.mu.Lock()
    defer db.pool.mu.Unlock()

    if len(db.pool.conns) == 0 {
        return db.client
    }

    conn := db.pool.conns[0]
    db.pool.conns = db.pool.conns[1:]
    db.pool.conns = append(db.pool.conns, conn)
    return conn
}

// 构建完整 key
func (db *NexKVDB) buildKey(table, key string) string {
    return fmt.Sprintf("%s:%s:%s", db.namespace, table, key)
}

// 带重试的执行
func (db *NexKVDB) withRetry(fn func() error) error {
    var lastErr error
    for i := 0; i < db.retryCount; i++ {
        if err := fn(); err != nil {
            lastErr = err
            time.Sleep(db.retryInt)
            continue
        }
        return nil
    }
    return lastErr
}

// InitThread 初始化线程上下文
func (db *NexKVDB) InitThread(ctx context.Context, threadID int, threadCount int) context.Context {
    return ctx
}

// CleanupThread 清理线程资源
func (db *NexKVDB) CleanupThread(ctx context.Context) {
    // 无需清理
}

// Close 关闭数据库连接
func (db *NexKVDB) Close() error {
    if db.client != nil {
        db.client.Close()
    }
    for _, conn := range db.pool.conns {
        conn.Close()
    }
    return nil
}

// Read 读取记录
func (db *NexKVDB) Read(ctx context.Context, table string, key string, fields []string) (map[string][]byte, error) {
    conn := db.getConn()
    fullKey := db.buildKey(table, key)

    var result map[string][]byte
    var readErr error

    err := db.withRetry(func() error {
        ctx, cancel := context.WithTimeout(ctx, db.timeout)
        defer cancel()

        value, err := conn.Get(ctx, fullKey)
        if err != nil {
            readErr = err
            return err
        }

        // 解析返回值
        result = map[string][]byte{
            "value": value,
        }
        return nil
    })

    if err != nil {
        return nil, fmt.Errorf("read failed: %w", readErr)
    }

    return result, nil
}

// Scan 扫描记录
func (db *NexKVDB) Scan(ctx context.Context, table string, startKey string, count int64, fields []string) ([]map[string][]byte, error) {
    conn := db.getConn()
    fullKey := db.buildKey(table, startKey)

    ctx, cancel := context.WithTimeout(ctx, db.timeout)
    defer cancel()

    // NexKV 范围扫描
    pairs, err := conn.Scan(ctx, fullKey, int(count))
    if err != nil {
        return nil, fmt.Errorf("scan failed: %w", err)
    }

    // 转换结果
    results := make([]map[string][]byte, 0, len(pairs))
    for _, pair := range pairs {
        results = append(results, map[string][]byte{
            "key":   []byte(pair.Key),
            "value": pair.Value,
        })
    }

    return results, nil
}

// Update 更新记录
func (db *NexKVDB) Update(ctx context.Context, table string, key string, values map[string][]byte) error {
    conn := db.getConn()
    fullKey := db.buildKey(table, key)

    return db.withRetry(func() error {
        ctx, cancel := context.WithTimeout(ctx, db.timeout)
        defer cancel()

        // 先读取当前值
        currentValue, err := conn.Get(ctx, fullKey)
        if err != nil {
            return fmt.Errorf("read for update failed: %w", err)
        }

        // 合并更新
        _ = currentValue // 简化：直接覆盖

        // 获取新值
        var newValue []byte
        for _, v := range values {
            newValue = v
            break
        }

        return conn.Put(ctx, fullKey, newValue)
    })
}

// Insert 插入记录
func (db *NexKVDB) Insert(ctx context.Context, table string, key string, values map[string][]byte) error {
    conn := db.getConn()
    fullKey := db.buildKey(table, key)

    return db.withRetry(func() error {
        ctx, cancel := context.WithTimeout(ctx, db.timeout)
        defer cancel()

        // 获取值
        var value []byte
        for _, v := range values {
            value = v
            break
        }

        return conn.Put(ctx, fullKey, value)
    })
}

// Delete 删除记录
func (db *NexKVDB) Delete(ctx context.Context, table string, key string) error {
    conn := db.getConn()
    fullKey := db.buildKey(table, key)

    return db.withRetry(func() error {
        ctx, cancel := context.WithTimeout(ctx, db.timeout)
        defer cancel()

        return conn.Delete(ctx, fullKey)
    })
}
```

### 6.2 单元测试

```go
// pkg/db/nexkv/nexkv_test.go
package nexkv

import (
    "context"
    "testing"

    "github.com/magiconair/properties"
    "github.com/pingcap/go-ycsb/pkg/db"
    "github.com/stretchr/testify/require"
)

func TestNexKVDB(t *testing.T) {
    // 跳过如果没有运行中的 NexKV
    if testing.Short() {
        t.Skip("skipping in short mode")
    }

    p := &properties.Properties{}
    p.Set(endpointsKey, "127.0.0.1:7946")
    p.Set(namespaceKey, "test")

    // 创建数据库实例
    database, err := newNexKVDB(p)
    require.NoError(t, err)
    defer database.Close()

    ctx := context.Background()
    ctx = database.InitThread(ctx, 0, 1)
    defer database.CleanupThread(ctx)

    // 测试 Insert
    values := map[string][]byte{
        "field0": []byte("test_value"),
    }
    err = database.Insert(ctx, "usertable", "user0", values)
    require.NoError(t, err)

    // 测试 Read
    result, err := database.Read(ctx, "usertable", "user0", nil)
    require.NoError(t, err)
    require.Equal(t, []byte("test_value"), result["value"])

    // 测试 Update
    updateValues := map[string][]byte{
        "field0": []byte("updated_value"),
    }
    err = database.Update(ctx, "usertable", "user0", updateValues)
    require.NoError(t, err)

    // 验证更新
    result, err = database.Read(ctx, "usertable", "user0", nil)
    require.NoError(t, err)
    require.Equal(t, []byte("updated_value"), result["value"])

    // 测试 Delete
    err = database.Delete(ctx, "usertable", "user0")
    require.NoError(t, err)

    // 验证删除
    _, err = database.Read(ctx, "usertable", "user0", nil)
    require.Error(t, err)
}

func TestNexKVDBRegistration(t *testing.T) {
    // 验证驱动已注册
    factories := db.GetDBNames()
    found := false
    for _, name := range factories {
        if name == "nexkv" {
            found = true
            break
        }
    }
    require.True(t, found, "nexkv driver should be registered")
}
```

### 6.3 注册驱动

在 `pkg/db/db.go` 中添加导入：

```go
import (
    // ... 其他导入
    _ "github.com/pingcap/go-ycsb/pkg/db/nexkv"  // 自动注册
)
```

或在 `cmd/go-ycsb/main.go` 中：

```go
import (
    // ... 其他导入
    _ "github.com/pingcap/go-ycsb/pkg/db/nexkv"
)
```

---

## 7. 性能测试实战

### 7.1 准备测试环境

```bash
# 1. 启动 NexKV 集群
./nexkvd --config config1.yaml &
./nexkvd --config config2.yaml &
./nexkvd --config config3.yaml &

# 2. 等待集群就绪
sleep 5

# 3. 检查集群状态
./nexkv cluster status
```

### 7.2 加载测试数据

```bash
# Workload A: 加载 100 万条记录
./bin/go-ycsb load nexkv -P workloads/workloada \
    -p nexkv.endpoints="127.0.0.1:7946,127.0.0.1:7947,127.0.0.1:7948" \
    -p nexkv.namespace="benchmark" \
    -p recordcount=1000000 \
    -p threadcount=32
```

### 7.3 运行基准测试

**Workload A (更新密集)**:
```bash
./bin/go-ycsb run nexkv -P workloads/workloada \
    -p nexkv.endpoints="127.0.0.1:7946,127.0.0.1:7947,127.0.0.1:7948" \
    -p nexkv.namespace="benchmark" \
    -p operationcount=10000000 \
    -p threadcount=64 \
    -p measurementtype=histogram
```

**Workload B (读取密集)**:
```bash
./bin/go-ycsb run nexkv -P workloads/workloadb \
    -p nexkv.endpoints="127.0.0.1:7946,127.0.0.1:7947,127.0.0.1:7948" \
    -p operationcount=10000000 \
    -p threadcount=64
```

**Workload C (只读)**:
```bash
./bin/go-ycsb run nexkv -P workloads/workloadc \
    -p nexkv.endpoints="127.0.0.1:7946,127.0.0.1:7947,127.0.0.1:7948" \
    -p operationcount=10000000 \
    -p threadcount=128  # 只读可以用更多线程
```

### 7.4 自定义工作负载

创建 `workload_nexkv` 文件：

```properties
# NexKV 自定义工作负载
recordcount=10000000
operationcount=50000000
workload=core

# 字段配置
fieldcount=10
fieldlength=100

# 数据分布
requestdistribution=zipfian

# 操作比例（模拟典型 KV 场景）
readproportion=0.8
updateproportion=0.1
insertproportion=0.05
scanproportion=0.05

# 扫描配置
scanlength=100

# 热点配置
hotspotdatafraction=0.2
```

运行自定义工作负载：

```bash
./bin/go-ycsb load nexkv -P workload_nexkv \
    -p nexkv.endpoints="127.0.0.1:7946" \
    -p threadcount=32

./bin/go-ycsb run nexkv -P workload_nexkv \
    -p nexkv.endpoints="127.0.0.1:7946" \
    -p threadcount=64
```

### 7.5 交互式 Shell

```bash
# 启动交互式 shell
./bin/go-ycsb shell nexkv \
    -p nexkv.endpoints="127.0.0.1:7946"

# 交互命令
» table usertable           # 设置表名
» insert user1 field0=value1 field1=value2  # 插入
» read user1                # 读取
» update user1 field0=newvalue              # 更新
» scan user0 10             # 扫描 10 条
» delete user1              # 删除
» help                      # 帮助
» exit                      # 退出
```

---

## 8. 结果分析与解读

### 8.1 输出示例

```
***************** properties *****************
"nexkv.endpoints"="127.0.0.1:7946"
"nexkv.namespace"="benchmark"
"operationcount"="10000000"
"recordcount"="1000000"
"threadcount"="64"
"workload"="core"
**********************************************

Run finished, takes 12.345678s
READ   - Takes(s): 12.3, Count: 8000000, OPS: 650406.5, Avg(us): 98, Min(us): 12, Max(us): 12345, 50th(us): 85, 90th(us): 156, 95th(us): 234, 99th(us): 567, 99.9th(us): 2345
UPDATE - Takes(s): 12.3, Count: 1000000, OPS: 81300.8, Avg(us): 789, Min(us): 234, Max(us): 45678, 50th(us): 654, 90th(us): 1234, 95th(us): 2345, 99th(us): 5678, 99.9th(us): 23456
INSERT - Takes(s): 12.3, Count: 500000, OPS: 40650.4, Avg(us): 876, Min(us): 345, Max(us): 56789, 50th(us): 765, 90th(us): 1567, 95th(us): 2876, 99th(us): 6789, 99.9th(us): 34567
SCAN   - Takes(s): 12.3, Count: 500000, OPS: 40650.4, Avg(us): 1234, Min(us): 456, Max(us): 67890, 50th(us): 1098, 90th(us): 2345, 95th(us): 4567, 99th(us): 9876, 99.9th(us): 45678
```

### 8.2 关键指标解读

| 指标 | 说明 | 优化方向 |
|------|------|----------|
| **OPS** | 每秒操作数 | 增加线程、优化网络 |
| **Avg(us)** | 平均延迟 | 减少锁竞争、批处理 |
| **50th** | 中位数延迟 | 通常代表正常情况 |
| **99th** | P99 延迟 | 关注尾部延迟 |
| **99.9th** | P99.9 延迟 | 极端情况，排查异常 |

### 8.3 性能基线参考

**NexKV 预期性能（3 节点集群）**：

| 操作 | 目标 OPS | 目标 P99 延迟 |
|------|----------|---------------|
| Read | > 500,000 | < 1ms |
| Update | > 100,000 | < 5ms |
| Insert | > 100,000 | < 5ms |
| Scan | > 50,000 | < 10ms |

### 8.4 对比测试脚本

```bash
#!/bin/bash
# benchmark_compare.sh

WORKLOADS=("workloada" "workloadb" "workloadc")
THREADS=(16 32 64 128)
RECORDCOUNT=1000000
OPERATIONCOUNT=10000000

for workload in "${WORKLOADS[@]}"; do
    echo "========== $workload =========="
    for threads in "${THREADS[@]}"; do
        echo "--- Threads: $threads ---"
        ./bin/go-ycsb run nexkv -P "workloads/$workload" \
            -p nexkv.endpoints="127.0.0.1:7946" \
            -p recordcount=$RECORDCOUNT \
            -p operationcount=$OPERATIONCOUNT \
            -p threadcount=$threads \
            -p measurement.output_file="results/${workload}_${threads}.txt"
    done
done
```

---

## 9. 高级配置

### 9.1 连接池优化

```bash
# 增加连接数
-p nexkv.conns_per_endpoint=8

# 更长的超时（适合高延迟网络）
-p nexkv.timeout=10s
```

### 9.2 重试策略

```bash
# 更激进的重试
-p nexkv.retry_count=5
-p nexkv.retry_interval=50ms
```

### 9.3 一致性级别

```bash
# 强一致性（默认）
-p nexkv.consistency=strong

# 最终一致性（更高吞吐）
-p nexkv.consistency=eventual
```

### 9.4 输出配置

```bash
# 输出到文件
-p measurement.output_file="results.txt"

# 使用 CSV 格式
-p measurementtype=csv

# 使用原始数据
-p measurementtype=raw
```

### 9.5 数据分布

```bash
# 均匀分布
-p requestdistribution=uniform

# Zipfian 分布（热点数据）
-p requestdistribution=zipfian

# 最新数据热点
-p requestdistribution=latest
```

---

## 10. 最佳实践

### 10.1 测试前准备

```bash
# 1. 清理旧数据
-p dropdata=true

# 2. 预热集群
./bin/go-ycsb load nexkv -P workloads/workloada -p recordcount=10000

# 3. 多轮测试取平均值
```

### 10.2 避免测试干扰

```bash
# 1. 绑定 CPU 核心
taskset -c 0-15 ./bin/go-ycsb run nexkv ...

# 2. 关闭性能调频
cpupower frequency-set -g performance

# 3. 禁用 NUMA 平衡
echo 0 > /proc/sys/kernel/numa_balancing
```

### 10.3 监控系统资源

```bash
# 监控 CPU、内存、网络
sar -u -r -n DEV 1 > system_stats.log &

# 监控磁盘 I/O
iostat -x 1 > io_stats.log &

# 监控 NexKV 内部指标
curl http://localhost:9090/metrics > nexkv_metrics.log &
```

### 10.4 结果归档

```bash
# 目录结构
results/
├── 2026-02-13/
│   ├── workloada_32threads.txt
│   ├── workloada_64threads.txt
│   ├── workloadb_32threads.txt
│   ├── system_stats.log
│   └── summary.md
```

### 10.5 CI 集成

```yaml
# .github/workflows/benchmark.yml
name: Performance Benchmark

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  benchmark:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Setup Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Build go-ycsb
        run: |
          git clone https://github.com/pingcap/go-ycsb.git
          cd go-ycsb
          make

      - name: Start NexKV
        run: |
          ./scripts/start_cluster.sh
          sleep 10

      - name: Run Benchmark
        run: |
          ./go-ycsb/bin/go-ycsb load nexkv -P workloads/workloada -p recordcount=100000
          ./go-ycsb/bin/go-ycsb run nexkv -P workloads/workloada -p operationcount=1000000

      - name: Upload Results
        uses: actions/upload-artifact@v3
        with:
          name: benchmark-results
          path: results/
```

---

## 附录 A: 常见问题

### Q1: 连接超时

```
Error: failed to create nexkv client: dial tcp 127.0.0.1:7946: connect: connection refused
```

**解决**: 确保 NexKV 集群正在运行，端口正确。

### Q2: OPS 过低

**可能原因**:
- 线程数不足
- 网络延迟高
- 磁盘 I/O 瓶颈

**解决**: 增加线程、优化网络、使用更快的存储。

### Q3: P99 延迟过高

**可能原因**:
- GC 停顿
- 锁竞争
- 网络抖动

**解决**: 调整 GC 参数、优化锁粒度、检查网络。

---

## 附录 B: 参考资源

| 资源 | 链接 |
|------|------|
| go-ycsb GitHub | [github.com/pingcap/go-ycsb](https://github.com/pingcap/go-ycsb) |
| YCSB 官方文档 | [github.com/brianfrankcooper/YCSB](https://github.com/brianfrankcooper/YCSB) |
| TiKV 基准测试 | [tikv.org/docs](https://tikv.org/docs/) |
| 性能测试方法论 | [YCSB Wiki](https://github.com/brianfrankcooper/YCSB/wiki) |

---

**文档版本**: v1.0
**创建日期**: 2026-02-13
**维护者**: NexKV 开发团队
