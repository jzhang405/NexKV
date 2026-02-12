# E2E 测试框架

> **NexKV 端到端测试框架**
> 用于验证 nexkvd daemon 进程和 nexkv CLI 的完整交互

---

## 概述

E2E 测试框架通过启动真实的 nexkvd 进程和执行 nexkv CLI 命令来验证系统的端到端行为。

### 测试策略

采用**渐进式验证**策略，从简单到复杂分为 5 个阶段：

| 阶段 | 描述 | 测试数量 | 预计耗时 |
|------|------|---------|---------|
| **Phase 1** | 单节点基础测试 | 8 | 5 min |
| **Phase 2** | 多节点集群测试 | 6 | 10 min |
| **Phase 3** | 故障注入测试 | 5 | 15 min |
| **Phase 4** | 并发场景测试 | 4 | 20 min |
| **Phase 5** | 性能测试 | 3 | 30 min |

---

## 快速开始

### 前置条件

1. 编译项目：
```bash
make build
```

2. 确保 `nexkvd` 和 `nexkv` 可执行文件在项目根目录：
```bash
ls -la nexkvd nexkv
```

### 运行测试

**运行所有 E2E 测试**：
```bash
make test-e2e
```

**运行特定阶段**：
```bash
make test-e2e-phase1  # 单节点基础测试
make test-e2e-phase2  # 多节点集群测试
make test-e2e-phase3  # 故障注入测试
make test-e2e-phase4  # 并发场景测试
make test-e2e-phase5  # 性能测试
```

---

## 框架结构

```
test/e2e/
├── framework/           # 测试框架核心
│   ├── daemon.go       # Daemon 进程管理
│   ├── cli.go          # CLI 命令执行
│   ├── cluster.go      # 集群编排
│   ├── config.go       # 配置生成
│   └── assert.go       # E2E 断言
├── phases/             # 分阶段测试
│   ├── phase1_single_node/
│   ├── phase2_cluster/
│   ├── phase3_fault_injection/
│   ├── phase4_concurrency/
│   └── phase5_performance/
└── README.md           # 本文件
```

---

## 核心组件

### Daemon 进程管理

```go
// 创建 daemon 进程
daemon := framework.NewDaemonProcess(
    "node-1",                    // node ID
    "127.0.0.1:7946",           // 监听地址
    "/tmp/config",              // 配置目录
    "/tmp/logs",                // 日志目录
)

// 启动 daemon
err := daemon.Start(ctx)

// 停止 daemon
err := daemon.Stop()

// 检查运行状态
running := daemon.IsRunning()

// 健康检查
err := daemon.healthCheck()
```

### CLI 命令执行

```go
// 创建 CLI 执行器
cli := framework.NewCLIExecutor("127.0.0.1:7946")

// 执行命令
result := cli.Execute(ctx, "cluster", "status")
if result.ExitCode == 0 {
    fmt.Println(result.Stdout)
}

// 便捷方法
status, err := cli.ClusterStatus(ctx)
nodes, err := cli.NodeList(ctx)
topology, err := cli.ClusterTopology(ctx)
```

### 集群编排

```go
// 创建 3 节点集群
cluster := framework.NewTestCluster(3)

// 启动集群
err := cluster.Start(ctx)
defer cluster.Stop()

// 等待集群稳定
err := cluster.WaitStable(ctx, 30*time.Second)

// 杀死节点（故障注入）
err := cluster.KillNode("node-2")

// 重启节点
err := cluster.RestartNode(ctx, "node-2")

// 获取节点 CLI
cli := cluster.CLI("node-1")
```

### E2E 断言

```go
assert := framework.NewE2EAssert(t)

// 断言 daemon 运行中
assert.DaemonRunning(daemon)

// 断言集群在线节点数
assert.ClusterOnlineNodes(cluster, 3)

// 断言 CLI 命令成功
assert.CLICommandSuccess(result)

// 等待条件满足
assert.Eventually(func() bool {
    return cluster.OnlineNodeCount() == 3
}, 30*time.Second, 1*time.Second)

// 等待集群稳定
assert.WaitForClusterStable(ctx, cluster, 30*time.Second)
```

---

## 编写测试

### 测试模板

```go
package phaseX

import (
    "context"
    "testing"
    "time"

    "github.com/jzhang405/NexKV/test/e2e/framework"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestFeature(t *testing.T) {
    ctx := context.Background()

    // 创建集群
    cluster := framework.NewTestCluster(3)
    require.NoError(t, cluster.Start(ctx))
    defer cluster.Stop()

    // 等待稳定
    cluster.WaitStable(ctx, 30*time.Second)

    // 执行测试
    cli := cluster.CLI("node-1")
    result, err := cli.ClusterStatus(ctx)
    require.NoError(t, err)

    // 验证结果
    assert.Equal(t, 3, result.OnlineNodeCount)
}
```

### 最佳实践

1. **使用 defer 清理**：
```go
cluster := framework.NewTestCluster(3)
require.NoError(t, cluster.Start(ctx))
defer cluster.Stop()  // 确保清理
```

2. **使用 require 处理关键错误**：
```go
require.NoError(t, err, "cluster should start")  // 失败则终止测试
```

3. **使用 assert 验证预期**：
```go
assert.Equal(t, expected, actual, "should match")  // 失败继续执行
```

4. **使用 E2E 断言**：
```go
assert := framework.NewE2EAssert(t)
assert.WaitForClusterStable(ctx, cluster, 30*time.Second)
```

---

## 故障排查

### 测试超时

**问题**：测试超时失败

**解决**：
1. 检查日志文件：`/tmp/nexkv-e2e/logs/*.log`
2. 增加超时时间
3. 检查端口冲突

### 进程残留

**问题**：测试后进程未清理

**解决**：
```bash
# 手动清理
pkill -f nexkvd
rm -rf /tmp/nexkv-e2e/
```

### 配置错误

**问题**：daemon 启动失败

**解决**：
1. 检查配置文件路径
2. 检查端口是否占用
3. 查看日志文件

---

## CI 集成

E2E 测试在 CI 中的执行策略：

1. **快速验证**：PR 触发 Phase 1 测试
2. **完整验证**：合并到 main 后执行所有阶段
3. **性能测试**： nightly 执行 Phase 5

详见 `.github/workflows/e2e-test.yml`

---

## 贡献指南

添加新测试时：

1. 确定测试所属阶段
2. 在对应 `phases/phaseX_*/` 目录下创建测试文件
3. 使用框架提供的方法和断言
4. 确保测试可重复执行
5. 添加必要的注释和文档

---

## 相关文档

- **架构设计**：`docs/04_test/07_E2E测试架构设计.md`
- **测试计划**：`docs/04_test/01_测试计划文档.md`
- **测试用例**：`docs/04_test/02_测试用例文档.md`

---

**维护者**: 🤖 AI 核心开发
**最后更新**: 2026-02-13
