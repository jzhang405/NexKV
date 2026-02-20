# Transport 层测试覆盖率分析

> **日期**: 2026-02-20
> **当前覆盖率**: 67.2% (单元测试)
> **目标覆盖率**: 80%

---

## 一、覆盖率现状

### 1.1 高覆盖率模块 (≥80%)

| 模块 | 覆盖率 | 状态 |
|------|--------|------|
| `middleware_chain.go` | 100% | ✅ |
| `logging_middleware.go` | 95%+ | ✅ |
| `metrics_middleware.go` | 95%+ | ✅ |
| `messagepack_codec.go` | 80-100% | ✅ |
| `async_common.go` | 90%+ | ✅ |

### 1.2 中等覆盖率模块 (50-79%)

| 模块 | 覆盖率 | 主要未覆盖 |
|------|--------|-----------|
| `libp2p_rpc.go` | 58.8% | `Call`, `WriteV`, `sendRequestAndWaitResponse` |
| `libp2p_transport.go` | 68% | `Connect`, `OpenStream` 错误路径 |
| `libp2p_async_channel.go` | 53% | `sendLoop`, `pushRecvError` |
| `libp2p_async_stream.go` | 50% | `pushReadResultNonBlocking` |

### 1.3 零覆盖率模块 (0%)

| 模块 | 方法数 | 原因 |
|------|--------|------|
| `libp2p_channel.go` | 5 | 需要真实 libp2p Channel |
| `libp2p_discovery.go` | 3 | 需要 mDNS 环境 |
| `libp2p_stream.go` | 8 | 需要真实网络流 |
| `libp2p_rpc.go:HandleIncomingStream` | 1 | 需要完整流处理链 |

---

## 二、提升方案分类

### 2.1 单元测试可提升 (预计 +5-8%)

这些可以通过添加更多测试用例来覆盖：

#### A. 错误路径测试

| 方法 | 当前 | 可达 | 测试内容 |
|------|------|------|----------|
| `Call` | 58.8% | 75% | 连接失败、编码失败、超时 |
| `sendRequestAndWaitResponse` | 26.3% | 60% | OpenStream 失败、编码失败 |
| `sendRequestNoResponse` | 16.7% | 50% | 编码失败、流关闭 |

**示例测试用例**:
```go
// TestLibp2pRPC_Call_EncodeFailure - 编码失败场景
// TestLibp2pRPC_Call_StreamOpenFailure - 流打开失败
// TestLibp2pRPC_WriteV_PartialFailure - 部分写入失败
```

#### B. 边界条件测试

| 方法 | 当前 | 可达 | 测试内容 |
|------|------|------|----------|
| `pushRecvError` | 50% | 80% | channel 满时丢弃 |
| `pushReadResultNonBlocking` | 50% | 80% | channel 满时丢弃 |

#### C. 配置验证测试

| 方法 | 当前 | 可达 | 测试内容 |
|------|------|------|----------|
| `WriteV` | 22.2% | 60% | 批量写入、部分失败、超时 |

### 2.2 需要集成测试 (预计 +8-12%)

这些需要真实的 libp2p 网络环境：

#### A. Stream/Channel 通信测试

```
测试环境要求:
- 2个 libp2p 节点
- 真实网络连接
- 协议协商完成

测试内容:
- Stream ID/Protocol/RemotePeer 方法
- Stream Read/Write 双向通信
- Channel Send/Recv 消息传递
- SetReadDeadline/SetDeadline 超时
```

#### B. mDNS 发现服务测试

```
测试环境要求:
- 本地 mDNS 可用
- 多个节点在同一网络

测试内容:
- NewDiscoveryService 创建服务
- HandlePeerFound 回调处理
- Close 清理资源
```

#### C. RPC 完整流程测试

```
测试环境要求:
- 2+ 节点集群
- RPC 服务端和客户端

测试内容:
- HandleIncomingStream 完整流处理
- 请求-响应往返
- 广播消息分发
```

---

## 三、集成测试框架设计

### 3.1 测试环境搭建

```go
// test/integration/transport/setup.go

type TestCluster struct {
    Nodes []*Libp2pTransport
    Hosts []host.Host
}

func NewTestCluster(t *testing.T, nodeCount int) *TestCluster {
    // 1. 创建 libp2p hosts
    // 2. 建立连接
    // 3. 返回集群
}

func (c *TestCluster) Close() {
    // 清理资源
}
```

### 3.2 测试用例设计

```go
// test/integration/transport/stream_test.go

func TestStreamCommunication(t *testing.T) {
    cluster := NewTestCluster(t, 2)
    defer cluster.Close()

    // 测试 Stream ID
    stream, _ := cluster.Nodes[0].OpenStream(ctx, cluster.Nodes[1].Self(), "/test/1.0")
    if stream.ID() == "" {
        t.Error("Stream ID should not be empty")
    }

    // 测试 Read/Write
    data := []byte("hello world")
    n, err := stream.Write(data)
    // ...
}
```

### 3.3 CI 集成

```yaml
# .github/workflows/integration-test.yml
name: Integration Tests
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - run: go test -v ./test/integration/... -timeout 5m
```

---

## 四、实施计划

### Phase 1: 单元测试补充 (1-2 天)

| 任务 | 预期提升 | 优先级 |
|------|----------|--------|
| 添加 `Call` 错误路径测试 | +5% | P0 |
| 添加 `WriteV` 测试 | +3% | P1 |
| 添加 `sendRequest*` 测试 | +3% | P1 |
| 添加 async 边界测试 | +2% | P2 |

### Phase 2: 集成测试框架 (2-3 天)

| 任务 | 预期提升 | 优先级 |
|------|----------|--------|
| 搭建 TestCluster 基础设施 | - | P0 |
| Stream/Channel 通信测试 | +8% | P0 |
| RPC 完整流程测试 | +4% | P1 |
| mDNS 发现测试 | +2% | P2 |

### Phase 3: 覆盖率验证 (1 天)

| 任务 | 内容 |
|------|------|
| 运行完整覆盖率报告 | 合并单元测试和集成测试覆盖率 |
| 更新文档 | M1 验收文档更新覆盖率数据 |
| CI 集成 | 添加覆盖率门禁 |

---

## 五、覆盖率目标

| 阶段 | 覆盖率 | 状态 |
|------|--------|------|
| 当前 (单元测试) | 67.2% | ✅ |
| Phase 1 完成后 | 72-75% | 🔄 |
| Phase 2 完成后 | 80-85% | 🎯 目标 |

---

## 六、结论

**单元测试覆盖率的合理上限约为 70-75%**，因为：
1. `libp2p_channel.go`、`libp2p_stream.go` 等是 libp2p 的薄包装层
2. 这些包装方法无法在隔离环境中测试
3. 需要真实网络环境才能验证行为

**建议**：
1. 优先完成单元测试补充（+5-8%）
2. 建立集成测试框架（+8-12%）
3. 最终达到 80%+ 的综合覆盖率

---

**文档版本**: v1.0
**创建日期**: 2026-02-20
**作者**: AI Agent
