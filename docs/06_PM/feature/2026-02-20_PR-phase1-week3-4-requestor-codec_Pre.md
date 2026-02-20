# 【PR全流程文档】Feature - Phase 1 Week 3-4 Requestor/Codec/Middleware 实现

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从需求对齐到开发完成的全流程，一个PR对应一份全流程文档，归档后作为项目追溯依据。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 功能开发（Feature） |
| PR编号 | PR-XXX（创建GitHub PR后补充完整） |
| 分支名称 | feature/phase1-week3-4-requestor-codec |
| 工作主题 | Phase 1 Week 3-4 Requestor/Codec/Middleware 实现 |
| 负责人 | 🤖 核心开发 A + B |
| 分支创建日期 | 2026-02-20 |
| 计划开工日期 | 2026-02-20 |
| 计划CI通过日期 | 2026-03-06 |
| 关联需求单号 | [NexKV DDD 架构实施 PR](../2026-02-18_PR-nexkv-ddd-architecture_Pre.md) |
| 架构师评审状态 | 🔄 待评审 |

### 2. 核心目标

**目标 1：实现 Requestor 接口**
- [ ] 定义 `Requestor` 接口（请求-响应模式）
- [ ] 实现 `Libp2pRequestor`

**目标 2：实现 Codec 接口**
- [ ] 定义 `Codec` 接口
- [ ] 实现 `MessagePackCodec`

**目标 3：实现 Middleware 接口**
- [ ] 定义 `Middleware` / `MiddlewareChain` 接口
- [ ] 实现 Logging/Metrics 中间件

**目标 4：扩展 POC 验证**
- [ ] 3 节点集群通信测试
- [ ] 性能基准测试（吞吐量 ≥ 10K ops/sec）

### 3. 接口设计

#### Requestor 接口
```go
type Requestor interface {
    Request(ctx context.Context, target model.PeerID, req model.Message) (model.Message, error)
    RequestWithTimeout(target model.PeerID, req model.Message, timeout time.Duration) (model.Message, error)
    Close() error
}
```

#### Codec 接口
```go
type Codec interface {
    Encode(msg model.Message) ([]byte, error)
    Decode(data []byte) (model.Message, error)
    Name() string
}
```

#### Middleware 接口
```go
type Middleware interface {
    Name() string
    InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next SendFunc) error
    InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next ReceiveFunc) error
}
```

### 4. 验收标准（M1 里程碑）

- [ ] 所有单元测试通过（覆盖率 ≥ 80%）
- [ ] 扩展 POC 验证通过
- [ ] 代码 lint 检查通过
- [ ] CI 流水线通过

---

## 第二部分：后置部分（开发完成后填写）

> 待补充

---

**文档状态**: 🔄 草稿（待架构师评审）
**最后更新**: 2026-02-20
