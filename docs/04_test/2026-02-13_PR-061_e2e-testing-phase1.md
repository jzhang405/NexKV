# PR-061 Phase 1: 单节点基础测试

> **父文档**: [PR-061 Pre 文档](../2026-02-13_PR-061_e2e-testing-framework_Pre.md)
> **阶段**: Phase 1
> **目标**: 验证 nexkvd daemon 进程的完整生命周期
> **预计耗时**: 5 min
> **依赖**: 无

---

## 1. 阶段目标

验证 nexkvd daemon 进程的完整生命周期和基础 CLI 功能：
- Daemon 进程启动/停止
- 优雅关闭
- 配置文件加载
- 健康检查
- CLI 基础命令交互

---

## 2. 测试用例清单

| 测试用例 | 验证目标 | 预计耗时 | 优先级 |
|---------|---------|---------|--------|
| TestDaemon_StartStop | Daemon 启动和停止 | 30s | P0 |
| TestDaemon_GracefulShutdown | 优雅关闭（SIGTERM） | 30s | P0 |
| TestDaemon_MultipleStart | 重复启动检测 | 20s | P1 |
| TestDaemon_MultipleStop | 重复停止检测 | 20s | P1 |
| TestDaemon_ConfigFileNotFound | 配置文件不存在时的处理 | 20s | P1 |
| TestCLI_ClusterStatus | CLI 查询集群状态 | 30s | P0 |
| TestCLI_NodeList | CLI 查询节点列表 | 30s | P0 |
| TestCLI_ClusterTopology | CLI 查询集群拓扑 | 30s | P0 |

**总计**: 8 个测试用例，预计 5 分钟

---

## 3. 框架组件需求

| 组件 | 接口 | 实现状态 |
|------|------|---------|
| **DaemonProcess** | `Start()`, `Stop()`, `IsRunning()`, `HealthCheck()` | ⚠️ 待实现 |
| **CLIExecutor** | `Execute()`, `ClusterStatus()`, `NodeList()` | ⚠️ 待实现 |
| **ConfigGenerator** | `GenerateDaemonConfig()` | ⚠️ 待实现 |

**DaemonProcess 核心接口**：
```go
type DaemonProcess struct {
    NodeID     string
    Addr       string
    ConfigPath string
    LogPath    string
    PidPath    string
}

func (d *DaemonProcess) Start(ctx context.Context) error
func (d *DaemonProcess) Stop() error
func (d *DaemonProcess) IsRunning() bool
func (d *DaemonProcess) HealthCheck() error
```

---

## 4. 验收标准

- [ ] 8/8 测试用例通过
- [ ] `make test-e2e-phase1` 可运行
- [ ] Daemon 进程能正常启动和停止
- [ ] CLI 能正确查询状态
- [ ] 优雅关闭无错误日志

---

## 5. 实施依赖

**前置条件**：
- [ ] nexkvd 可执行文件已编译
- [ ] 基础配置文件格式已定义

**后续阶段**：Phase 2（多节点集群）

---

## 6. 相关文档

- 主文档: [PR-061 Pre 文档](../2026-02-13_PR-061_e2e-testing-framework_Pre.md)
- 架构设计: [E2E 测试架构设计](../07_E2E测试架构设计.md)
