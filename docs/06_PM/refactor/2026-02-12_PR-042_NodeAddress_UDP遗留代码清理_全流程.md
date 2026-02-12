# [PR-042] NodeAddress UDP 遗留代码清理 - Pre 文档

> **PR 编号**: PR-042
> **创建日期**: 2026-02-12
> **作者**: AI 开发团队
> **状态**: 🔄 草稿
> **标签**: `refactor` `types` `metadata`

---

## 需求

分析 `NodeAddress` 结构体中的 `UDPPort` 字段在 libp2p 架构下的合理性问题，评估清理 UDP 遗留代码的可行性和影响范围。

---

## 设计

### 方案选择：直接移除 UDP 支持

**理由**：
1. libp2p 不使用裸 UDP 传输（使用 TCP、QUIC、WebSocket）
2. 简化数据模型，只保留 TCP 端口
3. 统一使用 libp2p multiaddr 地址表示
4. 一次性清理，无向后兼容负担

---

## 风险

| 风险项 | 影响 | 缓解措施 |
|--------|------|---------|
| **配置文件兼容性** | 旧配置文件可能包含 UDP 端口 | 提供迁移脚本 + 文档说明 |
| **测试覆盖不足** | 遗留 UDP 引用导致运行时错误 | 全面代码搜索 + 测试验证 |

---

## 技术验证

### 代码引用确认

在实施清理前，使用以下命令确认所有 UDP 相关引用：

```bash
# 搜索 UDPPort 引用
rg "UDPPort" --type go -n

# 搜索 UDPAddr 方法调用
rg "UDPAddr\(\)" --type go -n

# 搜索 GetUDPAddr 方法调用
rg "GetUDPAddr\(\)" --type go -n

# 搜索 UDP 相关错误定义
rg "UDP" --type go -n | grep -i "error\|port"

# 搜索配置文件中的 UDP 端口配置
rg "udp.*port" --type go -i -n
```

### 验证清单

| 验证项 | 命令 | 预期结果 |
|--------|------|----------|
| **UDPPort 字段引用** | `rg "UDPPort" --type go -c` | 明确的引用列表 |
| **UDPAddr 方法调用** | `rg "UDPAddr\(\)" --type go` | 所有调用点 |
| **测试文件覆盖** | `rg "UDP" --type go -g "*test*"` | 相关测试用例 |
| **配置文件引用** | `rg "udp" --type go -i -g "*.yaml"` | 配置项清单 |
| **文档引用** | `rg "UDP" docs/ -i` | 文档更新清单 |

### 清理范围确认

根据 Spike 分析，需要清理的文件包括：

| 模块 | 文件 | 清理内容 |
|------|------|----------|
| **types** | `internal/metadata/types/node_info.go` | 移除 `UDPPort` 字段及相关方法 |
| **cluster** | `internal/metadata/cluster/*.go` | 移除 UDP 地址初始化和验证 |
| **config** | `cmd/server/config.go` | 移除 UDP 端口配置项 |
| **tests** | `**/*_test.go` | 更新测试用例 |

---

## 数据迁移风险评估

### 迁移策略：直接移除

**策略**：一次性移除所有 UDP 相关代码，不保留向后兼容性。

```go
// 最终结构：完全移除 UDPPort
type NodeAddress struct {
    Host    string `msgpack:"host"`
    TCPPort int    `msgpack:"tcp_port"`
}
```

### 配置迁移脚本

提供自动化迁移工具：

```bash
#!/bin/bash
# migrate-config.sh - 配置文件迁移脚本

set -e

CONFIG_FILE="${1:-config.yaml}"
BACKUP_FILE="${CONFIG_FILE}.bak.$(date +%Y%m%d_%H%M%S)"

echo "🔄 开始配置迁移..."
echo "📋 配置文件: $CONFIG_FILE"

# 备份原配置
cp "$CONFIG_FILE" "$BACKUP_FILE"
echo "✅ 已备份到: $BACKUP_FILE"

# 移除 UDP 端口配置
sed -i.tmp '/udp_port:/d' "$CONFIG_FILE"
rm -f "${CONFIG_FILE}.tmp"

echo "✅ UDP 端口配置已移除"
echo "⚠️  请手动检查并更新节点地址配置"
```

### 数据迁移检查清单

| 检查项 | 操作 | 验证方法 |
|--------|------|----------|
| **配置文件备份** | 移除前备份所有配置文件 | `ls config.yaml.bak.*` |
| **节点通信** | 验证移除后集群通信正常 | 集成测试覆盖 |
| **回滚方案** | 保留回滚脚本 | `rollback-migration.sh` |
| **监控告警** | 配置迁移期间监控 | Prometheus 指标 |

### 风险缓解措施

| 风险项 | 缓解措施 | 验证方法 |
|--------|----------|----------|
| **节点无法启动** | 提供配置迁移脚本 | 测试：移除 UDP 后启动 |
| **集群通信失败** | 全量测试覆盖 | 集成测试：集群通信 |
| **数据丢失** | 强制备份，提供回滚方案 | 检查：备份文件完整性 |

---

## 验收标准

1. ✅ **代码变更完成** - 所有 `UDPPort` 相关代码已直接移除
2. ✅ **测试全部通过** - 单元测试、集成测试覆盖
3. ✅ **文档同步更新** - Spike 分析、设计文档已更新
4. ✅ **迁移脚本验证** - 配置迁移脚本测试通过

---

**参考**:
- Spike 分析：`docs/07_spike/node-address-udp-cleanup_2026-02-12.md`
- 架构设计：`docs/02_design/protocols/01_一致性协议设计.md`

---

**文档状态**: 🔄 草稿
**下一步**: 等待架构师评审
