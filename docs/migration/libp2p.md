# libp2p 迁移指南

## 概述

本指南帮助您将 NexKV 集群迁移到 libp2p Transport。

## 迁移前准备

### 1. 备份现有配置

```bash
cp /etc/nexkv/config.yaml /etc/nexkv/config.yaml.backup
```

### 2. 备份数据目录

```bash
tar -czf nexkv-backup-$(date +%Y%m%d).tar.gz /var/lib/nexkv
```

### 3. 确认依赖

```bash
# 检查 libp2p 依赖
go list -m github.com/libp2p/go-libp2p
```

## 迁移步骤

### 步骤 1: 更新配置文件

编辑 `/etc/nexkv/config.yaml`：

```yaml
# 使用简化版配置
p2p:
  listen_addr: "/ip4/0.0.0.0/tcp/4001"
  private_key_path: "/var/lib/nexkv/libp2p_key"
```

或使用完整版配置：

```yaml
cluster:
  node_id: 1
  transport:
    type: libp2p
    libp2p:
      listen_port: 4001
      listen_addr: "0.0.0.0"
      private_key_path: "/var/lib/nexkv/libp2p_key"
      discovery:
        mdns_enabled: true
        dht_enabled: true
```

### 步骤 2: 生成密钥对

```bash
# 生成新的 libp2p 密钥对
go run github.com/jzhang405/NexKV/cmd/keygen/main.go \
  --output /var/lib/nexkv/libp2p_key
```

### 步骤 3: 验证配置

```bash
# 使用配置验证工具（如果实现）
./bin/config-validator -config /etc/nexkv/config.yaml
```

### 步骤 4: 重启节点

```bash
systemctl restart nexkd
```

### 步骤 5: 验证集群状态

```bash
# 检查节点状态
nexctl cluster status

# 查看连接的对等节点
nexctl cluster peers
```

## 地址格式说明

### multiaddr 格式

libp2p 使用 multiaddr 格式表示节点地址：

```
/ip4/192.168.1.1/tcp/4001/p2p/12D3KooW...
/dns4/node1.example.com/tcp/4001/p2p/12D3KooW...
```

### 从旧格式迁移

**旧格式**（TCP/UDP）：
```yaml
cluster:
  listen_addr: "192.168.1.1:5001"
  seed_nodes:
    - "192.168.1.2:5001"
```

**新格式**（libp2p）：
```yaml
p2p:
  listen_addr: "/ip4/192.168.1.1/tcp/5001"
  bootstrap_peers:
    - "/ip4/192.168.1.2/tcp/5001/p2p/QmPeerID..."
```

## 回滚方案

如果迁移失败：

```bash
# 1. 停止节点
systemctl stop nexkd

# 2. 恢复配置
cp /etc/nexkv/config.yaml.backup /etc/nexkv/config.yaml

# 3. 重启节点
systemctl start nexkd
```

## 常见问题

### Q1: 节点无法发现其他节点

**A**: 检查以下项：
1. 防火墙设置（mDNS 需要 UDP 5353，libp2p 需要配置的端口）
2. 确认 `discovery.mdns_enabled: true`
3. 检查节点是否在同一网络

### Q2: 节点 ID 发生变化

**A**: 节点 ID 从公钥派生。确保：
1. 使用相同的密钥文件
2. 密钥文件路径正确

### Q3: 配置验证失败

**A**: 常见错误：
- `invalid multiaddr format`：使用 `/ip4/x.x.x.x/tcp/port` 格式
- `private_key_path is required`：必须指定私钥路径

### Q4: 连接数超过限制

**A**: 调整 `connection_manager.high_water` 配置：
```yaml
connection_manager:
  high_water: 500  # 增加最大连接数
```

## 支持与反馈

如有问题，请：
1. 查看日志：`journalctl -u nexkd -f`
2. 启用调试日志：在配置中设置 `logging.level: debug`
3. 提交 Issue 到 GitHub 仓库
