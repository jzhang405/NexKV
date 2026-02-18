# Phase 0: libp2p POC 验证计划（局域网环境）

**文档版本**: v1.0
**创建日期**: 2026-02-18
**执行周期**: Week 0（1周，5个工作日）
**前置条件**: Go 1.21+ 环境已搭建

---

## 一、POC 目标

### 1.1 核心目标

验证 libp2p 在局域网环境下的基础通信能力，为 Phase 1 基础设施层开发提供技术基础。

### 1.2 验证范围

| 验证项 | 优先级 | 说明 |
|--------|--------|------|
| **局域网节点发现** | P0 | mDNS 或静态配置节点发现 |
| **节点间直连通信** | P0 | TCP 直连，无需 NAT 穿透 |
| **基础 RPC 调用** | P0 | 请求-响应模式 |
| **Stream 流式传输** | P1 | 双向流通信 |
| **性能基准测试** | P1 | 连接建立时间、吞吐量 |

---

## 二、每日任务分解（5个工作日）

### Day 1：环境搭建与基础框架（周一）

**上午（4小时）**：
1. [ ] 安装 libp2p Go SDK
   ```bash
   go get github.com/libp2p/go-libp2p@latest
   go get github.com/multiformats/go-multiaddr@latest
   ```

2. [ ] 创建 POC 项目结构
   ```
   poc/libp2p-lan/
   ├── node/
   │   ├── node.go         # 节点实现
   │   └── config.go       # 配置管理
   ├── discovery/
   │   ├── mdns.go         # mDNS 发现
   │   └── static.go       # 静态配置
   ├── rpc/
   │   ├── client.go       # RPC 客户端
   │   └── server.go       # RPC 服务端
   ├── benchmark/
   │   └── perf_test.go    # 性能测试
   └── main.go             # 入口
   ```

3. [ ] 编写基础节点代码
   ```go
   // node/node.go
   func NewNode(ctx context.Context, listenPort int) (host.Host, error) {
       // 创建 libp2p 主机（局域网模式，禁用中继）
       h, err := libp2p.New(
           libp2p.ListenAddrStrings(
               fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", listenPort),
           ),
           libp2p.NoSecurity,  // 局域网无加密（可选）
           libp2p.NATPortMap(), // 尝试 NAT 映射（局域网通常不需要）
       )
       return h, err
   }
   ```

**下午（4小时）**：
4. [ ] 实现静态配置节点发现
   ```go
   // discovery/static.go
   func DiscoverFromConfig(configPath string) ([]peer.AddrInfo, error) {
       // 从配置文件读取节点列表
       // 格式：/ip4/192.168.1.100/tcp/9000/p2p/QmPeerID
   }
   ```

5. [ ] 测试单节点启动
   ```bash
   go run main.go --port 9000 --config nodes.yaml
   ```

**验收标准**：
- [x] 节点成功启动，监听指定端口
- [x] 可以从配置文件读取节点列表
- [x] 日志输出正常（Zap 结构化日志）

---

### Day 2：mDNS 节点发现（周二）

**上午（4小时）**：
1. [ ] 实现 mDNS 节点发现
   ```go
   // discovery/mdns.go
   func SetupMDNS(ctx context.Context, host host.Host, serviceName string) error {
       // 创建 mDNS 服务
       service, err := mdns.NewMdnsService(
           host,
           serviceName,
           &discoveryNotifee{host: host},
       )
       if err != nil {
           return err
       }
       return service.Start()
   }

   // 发现新节点时的回调
   type discoveryNotifee struct {
       host host.Host
   }

   func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
       log.Info("发现新节点",
           zap.String("peer", pi.ID.Pretty()),
           zap.Strings("addrs", addrsToStrings(pi.Addrs)),
       )
       n.host.Connect(context.Background(), pi)
   }
   ```

2. [ ] 测试 mDNS 发现功能
   ```bash
   # 终端 1：启动节点 A
   go run main.go --port 9000 --discovery mdns

   # 终端 2：启动节点 B（自动发现节点 A）
   go run main.go --port 9001 --discovery mdns
   ```

**下午（4小时）**：
3. [ ] 实现混合发现策略（mDNS + 静态配置）
   ```go
   // discovery/hybrid.go
   func DiscoverPeers(ctx context.Context, host host.Host, cfg *Config) error {
       // 1. 尝试 mDNS 发现
       if cfg.EnableMDNS {
           if err := SetupMDNS(ctx, host, cfg.ServiceName); err != nil {
               log.Warn("mDNS 发现失败，使用静态配置", zap.Error(err))
           }
       }

       // 2. 静态配置节点
       if len(cfg.StaticPeers) > 0 {
           for _, peerAddr := range cfg.StaticPeers {
               // 连接到静态节点
           }
       }

       return nil
   }
   ```

4. [ ] 测试 3 节点集群发现
   ```bash
   # 启动 3 个节点
   for i in {0..2}; do
       go run main.go --port $((9000 + i)) --discovery hybrid &
   done
   ```

**验收标准**：
- [x] mDNS 自动发现局域网节点
- [x] 静态配置作为降级方案
- [x] 3 节点可以互相发现

---

### Day 3：基础 RPC 调用（周三）

**上午（4小时）**：
1. [ ] 实现 RPC 协议定义
   ```go
   // rpc/protocol.go
   const (
       ProtocolID    = "/nexkv/rpc/1.0.0"
       ProtocolEcho  = "/nexkv/echo/1.0.0"
       ProtocolPing  = "/nexkv/ping/1.0.0"
   )

   // RPC 消息格式
   type RPCRequest struct {
       ID      string
       Method  string
       Params  []byte
   }

   type RPCResponse struct {
       ID      string
       Result  []byte
       Error   string
   }
   ```

2. [ ] 实现 RPC 服务端
   ```go
   // rpc/server.go
   func (s *RPCServer) Start(ctx context.Context, host host.Host) error {
       // 注册 RPC 协议处理器
       host.SetStreamHandler(ProtocolID, s.handleRPCStream)

       // 注册 Echo 协议（测试用）
       host.SetStreamHandler(ProtocolEcho, s.handleEcho)

       return nil
   }

   func (s *RPCServer) handleRPCStream(stream network.Stream) {
       defer stream.Close()

       // 读取请求
       var req RPCRequest
       if err := json.NewDecoder(stream).Decode(&req); err != nil {
           log.Error("解析请求失败", zap.Error(err))
           return
       }

       // 处理请求
       resp := s.handleRequest(&req)

       // 发送响应
       json.NewEncoder(stream).Encode(resp)
   }
   ```

**下午（4小时）**：
3. [ ] 实现 RPC 客户端
   ```go
   // rpc/client.go
   func (c *RPCClient) Call(
       ctx context.Context,
       peerID peer.ID,
       method string,
       params []byte,
   ) ([]byte, error) {
       // 打开到目标节点的 Stream
       stream, err := c.host.NewStream(ctx, peerID, ProtocolID)
       if err != nil {
           return nil, err
       }
       defer stream.Close()

       // 发送请求
       req := &RPCRequest{
           ID:     uuid.New().String(),
           Method: method,
           Params: params,
       }
       if err := json.NewEncoder(stream).Encode(req); err != nil {
           return nil, err
       }

       // 读取响应
       var resp RPCResponse
       if err := json.NewDecoder(stream).Decode(&resp); err != nil {
           return nil, err
       }

       if resp.Error != "" {
           return nil, errors.New(resp.Error)
       }

       return resp.Result, nil
   }
   ```

4. [ ] 测试 RPC 调用
   ```bash
   # 节点 A（服务端）
   go run main.go --port 9000 --mode server

   # 节点 B（客户端）
   go run main.go --port 9001 --mode client --peer /ip4/192.168.1.100/tcp/9000/p2p/QmXXX
   ```

**验收标准**：
- [x] 客户端可以调用服务端 RPC
- [x] 请求-响应延迟 < 5ms
- [x] 错误处理正常

---

### Day 4：Stream 流式传输与性能测试（周四）

**上午（4小时）**：
1. [ ] 实现 Stream 流式传输
   ```go
   // rpc/stream.go
   func (s *StreamServer) HandleStream(stream network.Stream) {
       // 双向流通信
       go func() {
           // 读取流数据
           scanner := bufio.NewScanner(stream)
           for scanner.Scan() {
               data := scanner.Bytes()
               log.Info("收到流数据", zap.Int("bytes", len(data)))

               // 处理数据
               result := s.processData(data)

               // 发送响应
               stream.Write(result)
               stream.Write([]byte("\n"))
           }
       }()
   }
   ```

2. [ ] 测试流式传输
   ```bash
   # 发送 1MB 数据
   go run main.go --mode stream --size 1048576
   ```

**下午（4小时）**：
3. [ ] 实现性能基准测试
   ```go
   // benchmark/perf_test.go
   func BenchmarkRPCLatency(b *testing.B) {
       // 测试 RPC 调用延迟
       for i := 0; i < b.N; i++ {
           start := time.Now()
           _, err := client.Call(ctx, peerID, "echo", []byte("hello"))
           latency := time.Since(start)
           require.NoError(b, err)
           require.Less(b, latency.Milliseconds(), int64(5))
       }
   }

   func BenchmarkThroughput(b *testing.B) {
       // 测试吞吐量
       data := make([]byte, 1024) // 1KB
       for i := 0; i < b.N; i++ {
           _, err := client.Call(ctx, peerID, "echo", data)
           require.NoError(b, err)
       }
   }
   ```

4. [ ] 运行性能测试
   ```bash
   # 延迟测试
   go test -bench=Latency -benchtime=10s

   # 吞吐量测试
   go test -bench=Throughput -benchtime=30s
   ```

**验收标准**：
- [x] RPC 调用延迟 < 5ms（P99）
- [x] 吞吐量 ≥ 10,000 ops/sec
- [x] Stream 流式传输正常

---

### Day 5：集成测试与 POC 报告（周五）

**上午（4小时）**：
1. [ ] 3 节点集群集成测试
   ```bash
   # 启动 3 节点集群
   ./scripts/start-cluster.sh 3

   # 运行集成测试
   go test -v ./tests/integration/...
   ```

2. [ ] 故障恢复测试
   - [ ] 节点 A 宕机后重启
   - [ ] 节点 B、C 自动重连
   - [ ] 数据传输不丢失

3. [ ] 性能压力测试
   ```bash
   # 100 并发客户端
   go run main.go --mode stress --concurrency 100 --duration 60s
   ```

**下午（4小时）**：
4. [ ] 编写 POC 验收报告
   ```markdown
   # libp2p POC 验收报告

   ## 1. 测试环境
   - 操作系统：Linux/macOS
   - Go 版本：1.21+
   - 网络环境：局域网（千兆以太网）

   ## 2. 功能验证
   | 功能 | 状态 | 说明 |
   |------|------|------|
   | mDNS 节点发现 | ✅ 通过 | 3秒内发现所有节点 |
   | 静态配置发现 | ✅ 通过 | 配置文件加载正常 |
   | RPC 调用 | ✅ 通过 | 延迟 < 5ms |
   | Stream 流式 | ✅ 通过 | 吞吐量 15MB/s |

   ## 3. 性能测试
   | 指标 | 目标值 | 实际值 | 状态 |
   |------|--------|--------|------|
   | 节点发现时间 | < 5s | 3.2s | ✅ 达标 |
   | 连接建立时间 | < 2s | 1.1s | ✅ 达标 |
   | RPC 延迟 P99 | < 5ms | 3.8ms | ✅ 达标 |
   | 吞吐量 | ≥ 10K ops/s | 12.5K ops/s | ✅ 达标 |

   ## 4. 问题与解决
   - 问题 1：mDNS 在某些网络环境下不稳定
   - 解决：增加静态配置作为降级方案

   ## 5. 结论
   ✅ POC 验证通过，建议启动 Phase 1 开发
   ```

5. [ ] 代码审查与清理
   - [ ] 移除调试日志
   - [ ] 添加代码注释
   - [ ] 整理 POC 代码为参考实现

**验收标准**：
- [x] 3 节点集群稳定运行 1 小时
- [x] 所有性能指标达标
- [x] POC 验收报告完成

---

## 三、验收标准汇总

### 3.1 功能验收

| 验收项 | 验收标准 | 优先级 |
|--------|----------|--------|
| **节点发现** | mDNS 或静态配置，3秒内发现所有节点 | P0 |
| **节点直连** | TCP 直连成功，无需 NAT 穿透 | P0 |
| **RPC 调用** | 请求-响应模式正常，延迟 < 5ms | P0 |
| **Stream 流式** | 双向流通信正常 | P1 |
| **集群稳定** | 3 节点集群运行 1 小时无故障 | P1 |

### 3.2 性能验收

| 指标 | 目标值 | 验证方法 |
|------|--------|----------|
| **节点发现时间** | < 5秒 | 启动节点，测量发现时间 |
| **连接建立时间** | < 2秒 | 测量 Dial 耗时 |
| **RPC 延迟 P99** | < 5ms | 压测 1000 次调用 |
| **吞吐量** | ≥ 10,000 ops/sec | 压测 60 秒 |

### 3.3 文档验收

- [x] POC 验收报告（Markdown）
- [x] 性能测试报告（图表）
- [x] 代码注释完整
- [x] README 文档（使用说明）

---

## 四、风险与降级

### 4.1 潜在风险

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|----------|
| **mDNS 不稳定** | 中 | 中 | 静态配置作为降级方案 |
| **性能不达标** | 低 | 高 | 优化协议，减少序列化开销 |
| **连接不稳定** | 低 | 高 | 增加重连机制 |

### 4.2 降级方案

**触发条件**：
- mDNS 发现失败
- 性能不达标（延迟 > 10ms 或吞吐量 < 5K ops/s）

**降级操作**：
1. 切换到静态配置节点发现
2. 简化协议（去掉加密、压缩）
3. 如果仍不达标，考虑使用 gRPC

---

## 五、交付物

### 5.1 代码交付

```
poc/libp2p-lan/
├── node/             # 节点实现
├── discovery/        # 节点发现（mDNS + 静态）
├── rpc/              # RPC 实现
├── benchmark/        # 性能测试
├── tests/            # 集成测试
└── docs/             # 文档
    ├── POC验收报告.md
    ├── 性能测试报告.md
    └── README.md
```

### 5.2 文档交付

1. **POC 验收报告**（docs/POC验收报告.md）
2. **性能测试报告**（docs/性能测试报告.md）
3. **README 文档**（README.md）
4. **架构决策记录**（ADR-007-libp2p-lan-poc.md）

---

## 六、下一步行动

**POC 验收通过后**：

1. ✅ **启动 Phase 1**（Week 1-4）
   - 基础设施层开发
   - 基于 POC 代码实现 16 个接口

2. ✅ **并行启动 Bf-Tree 移植**
   - 参考 POC 的节点通信模式
   - Week 8 检查点

3. ✅ **技术债务清理**
   - POC 代码重构为生产级代码
   - 增加单元测试（80% 覆盖率）

---

**文档版本**: v1.0
**创建日期**: 2026-02-18
**维护者**: NexKV 开发团队
**状态**: 📋 待执行
