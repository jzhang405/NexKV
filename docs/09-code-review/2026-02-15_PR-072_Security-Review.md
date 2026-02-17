# 安全审查报告 - PR-072 E2E 测试框架

**审查日期**: 2026-02-15
**审查人**: Security Reviewer Agent
**文档类型**: PR Pre 文档安全审查
**风险等级**: **🟡 MEDIUM**（中等风险）

---

## 执行摘要

本报告对 E2E 测试框架的 PR Pre 文档进行了全面的安全审查。该框架涉及**真实进程级别的测试**，引入了新的安全风险点。总体而言，文档设计考虑了部分安全问题，但在**进程管理、资源隔离、敏感信息保护**等方面存在改进空间。

### 关键发现

| 风险等级 | 数量 | 核心问题 |
|---------|------|---------|
| **CRITICAL** | 0 | 无 |
| **HIGH** | 3 | 进程组管理缺失细节、端口泄露风险、CI 环境权限隔离 |
| **MEDIUM** | 4 | 测试数据残留、日志敏感信息、并发测试资源竞争、配置文件安全 |
| **LOW** | 2 | 错误处理、资源清理的健壮性 |

### 审查结论

**建议**: **有条件批准**，需在下一次架构师评审前解决 HIGH 级别问题。

---

## 详细安全审查

### 1. 进程管理安全（HIGH）

#### 1.1 进程组管理策略不明确

**问题位置**: 第 3.2 节 - ProcessManager 设计

**安全风险**: HIGH

**问题描述**:

文档提到使用 `Setpgid` 进行进程组管理（第 185 行），但未提供具体实现细节：

1. **进程树清理风险**: 如果子进程启动了孙子进程，`Kill(-pgid)` 可能无法清理所有后代进程
2. **僵尸进程风险**: 未明确说明是否正确处理 `SIGCHLD` 信号和 `waitpid()`
3. **进程泄露场景**: 测试异常退出时，可能导致孤儿进程残留

**影响**:

- 测试机器资源耗尽（进程数、内存、端口）
- CI 环境中累积僵尸进程导致系统不稳定
- 后续测试因端口占用而失败

**现有代码审查发现**:

项目已有进程组管理经验（参考 `docs/06_PM/feature/2026-02-10_datadir-path-unification_post.md`），建议复用现有实践。

**修复建议**:

```go
// ❌ 缺失：进程启动时的安全配置
func (pm *ProcessManager) Start(config ProcessConfig) error {
    cmd := exec.Command(config.BinaryPath, config.Args...)

    // ✅ 必须添加：进程组管理
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setpgid: true,    // 创建新的进程组
        Pgid: 0,          // 子进程成为进程组leader
    }

    // ✅ 必须添加：输出重定向，避免子进程继承stdout/stderr
    cmd.Stdout = &safeBuffer{maxSize: 10 * 1024 * 1024} // 限制日志大小
    cmd.Stderr = &safeBuffer{maxSize: 10 * 1024 * 1024}
}

// ❌ 缺失：进程停止时的完整清理
func (pm *ProcessManager) Stop(ctx context.Context, id string) error {
    process := pm.getProcess(id)

    // ✅ 必须添加：优雅关闭超时机制
    // 1. 先发送 SIGTERM
    if err := process.Signal(syscall.SIGTERM); err != nil {
        log.Warn("Failed to send SIGTERM", "error", err)
    }

    // 2. 等待优雅关闭（带超时）
    done := make(chan error, 1)
    go func() {
        _, err := process.Wait()
        done <- err
    }()

    select {
    case <-done:
        // 进程正常退出
    case <-time.After(10 * time.Second):
        // 3. 强制杀死进程组
        log.Warn("Process didn't exit gracefully, force killing")
        if err := syscall.Kill(-process.Pid, syscall.SIGKILL); err != nil {
            return fmt.Errorf("failed to kill process group: %w", err)
        }
        <-done // 等待进程真正退出
    case <-ctx.Done():
        return ctx.Err()
    }

    return nil
}

// ✅ 必须添加：清理整个进程树
func (pm *ProcessManager) cleanupProcessTree(pgid int) error {
    // 1. 递归查找所有子进程
    processes, err := pm.findAllProcessesInGroup(pgid)
    if err != nil {
        return err
    }

    // 2. 按层级从深到浅排序（先杀子进程，再杀父进程）
    sort.Slice(processes, func(i, j int) bool {
        return processes[i].depth > processes[j].depth
    })

    // 3. 依次发送 SIGTERM
    for _, p := range processes {
        if err := p.Signal(syscall.SIGTERM); err != nil {
            log.Warn("Failed to signal process", "pid", p.Pid, "error", err)
        }
    }

    // 4. 等待所有进程退出（带超时）
    // ...
}
```

**优先级**: P0（必须在 Phase 1 实现前解决）

---

#### 1.2 进程启动命令注入风险

**问题位置**: 第 3.2 节 - ProcessManager.Start()

**安全风险**: MEDIUM

**问题描述**:

如果 `ProcessConfig.BinaryPath` 或 `ProcessConfig.Args` 来自不受信任的输入，可能导致命令注入。

**影响**:

- 在 CI 环境中可能执行恶意代码
- 测试数据污染导致的安全风险

**修复建议**:

```go
// ✅ 安全实现：输入验证
func (pm *ProcessManager) Start(config ProcessConfig) error {
    // 1. 验证二进制路径
    absPath, err := filepath.Abs(config.BinaryPath)
    if err != nil {
        return fmt.Errorf("invalid binary path: %w", err)
    }

    // 2. 检查路径是否在允许的目录内
    allowedDirs := []string{
        filepath.Join(pm.projectRoot, "bin"),
        filepath.Join(pm.projectRoot, "test", "binaries"),
    }

    isAllowed := false
    for _, dir := range allowedDirs {
        if strings.HasPrefix(absPath, dir) {
            isAllowed = true
            break
        }
    }
    if !isAllowed {
        return fmt.Errorf("binary path outside allowed directories: %s", absPath)
    }

    // 3. 验证文件存在且可执行
    info, err := os.Stat(absPath)
    if err != nil {
        return fmt.Errorf("binary not found: %w", err)
    }
    if info.Mode()&0111 == 0 {
        return fmt.Errorf("binary not executable: %s", absPath)
    }

    // 4. 验证参数（不允许 shell 元字符）
    for _, arg := range config.Args {
        if containsShellMetacharacters(arg) {
            return fmt.Errorf("arg contains shell metacharacters: %s", arg)
        }
    }

    // 5. 直接执行，不使用 shell
    cmd := exec.Command(absPath, config.Args...)
    // ...
}

func containsShellMetacharacters(s string) bool {
    // 检查常见的 shell 元字符
    metachars := []string{";", "|", "&", "$", "`", "(", ")", "<", ">", "\n", "\r"}
    for _, char := range metachars {
        if strings.Contains(s, char) {
            return true
        }
    }
    return false
}
```

**优先级**: P1（建议在 Phase 1 实现时包含）

---

### 2. 端口管理安全（HIGH）

#### 2.1 端口泄露风险

**问题位置**: 第 3.2 节 - PortAllocator

**安全风险**: HIGH

**问题描述**:

文档提到使用 OS 动态分配（`:0`），但存在以下风险：

1. **TOCTOU 竞争**: `net.Listen(":0")` 和获取实际端口之间存在时间窗口，其他进程可能占用
2. **测试崩溃泄露**: 测试异常退出时，端口可能未被正确释放
3. **长时间占用**: 测试超时未清理，端口被长时间占用

**影响**:

- CI 环境中端口耗尽导致后续测试失败
- 影响同一机器上的其他服务
- 测试结果不可预测

**现有代码审查发现**:

项目已有端口分配器（`internal/metadata/cluster/port_allocator.go`），但它是基于哈希的确定性分配（9000-32767），不适用于 E2E 测试的动态分配场景。

**修复建议**:

```go
// ✅ 推荐：双重绑定策略
type PortAllocator struct {
    mu        sync.RWMutex
    allocated map[int]*PortBinding // 端口 -> 绑定信息
    listeners map[int]net.Listener // 端口 -> Listener（持有以防止释放）
}

type PortBinding struct {
    TestID     string
    Port       int
    Listener   net.Listener // 保持监听，防止端口被其他进程占用
    AllocatedAt time.Time
    PID        int // 分配端口的进程ID
}

// ✅ 安全实现：持有 Listener 防止泄露
func (pa *PortAllocator) AllocatePort(testID string) (int, error) {
    pa.mu.Lock()
    defer pa.mu.Unlock()

    // 1. 监听 :0 让 OS 分配端口
    listener, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        return 0, fmt.Errorf("failed to allocate port: %w", err)
    }

    // 2. 获取实际分配的端口
    addr := listener.Addr().(*net.TCPAddr)
    port := addr.Port

    // 3. 记录绑定信息（持有 Listener）
    binding := &PortBinding{
        TestID:      testID,
        Port:        port,
        Listener:    listener, // 关键：持有 Listener 防止端口释放
        AllocatedAt: time.Now(),
        PID:         os.Getpid(),
    }

    pa.allocated[port] = binding
    pa.listeners[port] = listener

    // 4. 立即返回端口（Listener 会被传递给测试进程）
    return port, nil
}

// ✅ 安全实现：显式释放
func (pa *PortAllocator) ReleasePort(port int) error {
    pa.mu.Lock()
    defer pa.mu.Unlock()

    listener, exists := pa.listeners[port]
    if !exists {
        return fmt.Errorf("port %d not allocated", port)
    }

    // 1. 关闭 Listener
    if err := listener.Close(); err != nil {
        log.Warn("Failed to close listener", "port", port, "error", err)
    }

    // 2. 清理记录
    delete(pa.allocated, port)
    delete(pa.listeners, port)

    return nil
}

// ✅ 必须添加：定期清理过期绑定
func (pa *PortAllocator) startCleanupRoutine() {
    go func() {
        ticker := time.NewTicker(30 * time.Second)
        defer ticker.Stop()

        for range ticker.C {
            pa.cleanupStaleBindings()
        }
    }()
}

func (pa *PortAllocator) cleanupStaleBindings() {
    pa.mu.Lock()
    defer pa.mu.Unlock()

    now := time.Now()
    for port, binding := range pa.allocated {
        // 超过 10 分钟未释放的绑定，强制清理
        if now.Sub(binding.AllocatedAt) > 10*time.Minute {
            log.Warn("Cleaning up stale port binding",
                "port", port,
                "testID", binding.TestID,
                "age", now.Sub(binding.AllocatedAt))

            binding.Listener.Close()
            delete(pa.allocated, port)
            delete(pa.listeners, port)
        }
    }
}
```

**优先级**: P0（必须在 Phase 1 实现前解决）

---

#### 2.2 端口范围配置缺失

**问题位置**: 第 3.2 节 - PortAllocator

**安全风险**: MEDIUM

**问题描述**:

文档未明确端口分配的范围和策略，可能导致：

1. **系统端口冲突**: 可能占用 1024 以下的系统保留端口
2. **已知服务冲突**: 可能占用常用服务端口（如 MySQL 3306, Redis 6379）
3. **防火墙规则**: 某些端口可能被防火墙阻止

**修复建议**:

```go
// ✅ 推荐：可配置的端口范围
type PortAllocatorConfig struct {
    // 允许的端口范围（避免系统端口和常用服务端口）
    MinPort int // 默认 10000
    MaxPort int // 默认 30000

    // 黑名单端口（常用服务端口）
    BlacklistedPorts []int

    // 绑定接口（默认仅本地）
    BindInterface string // 默认 "127.0.0.1"
}

func NewPortAllocator(config *PortAllocatorConfig) *PortAllocator {
    if config == nil {
        config = &PortAllocatorConfig{
            MinPort:      10000,
            MaxPort:      30000,
            BindInterface: "127.0.0.1",
            BlacklistedPorts: []int{
                3306,  // MySQL
                5432,  // PostgreSQL
                6379,  // Redis
                27017, // MongoDB
                9211,  // NexKV 默认端口
            },
        }
    }
    // ...
}

// ✅ 安全实现：在范围内随机尝试
func (pa *PortAllocator) AllocatePortInRange(testID string) (int, error) {
    maxRetries := 100
    for i := 0; i < maxRetries; i++ {
        // 随机选择端口
        port := rand.Intn(pa.config.MaxPort-pa.config.MinPort) + pa.config.MinPort

        // 检查黑名单
        if pa.isBlacklisted(port) {
            continue
        }

        // 尝试绑定
        listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", pa.config.BindInterface, port))
        if err == nil {
            // 绑定成功
            return port, nil
        }
    }
    return 0, fmt.Errorf("failed to allocate port after %d retries", maxRetries)
}
```

**优先级**: P1（建议在 Phase 1 实现时包含）

---

### 3. 数据目录隔离安全（MEDIUM）

#### 3.1 测试数据残留风险

**问题位置**: 第 3.2 节 - DataDirManager

**安全风险**: MEDIUM

**问题描述**:

文档提到"每个测试独立目录，支持自动清理"，但缺少以下保障：

1. **清理失败场景**: 测试崩溃、被 kill -9、机器宕机时，清理代码可能未执行
2. **磁盘空间泄露**: 大量测试数据累积导致磁盘空间耗尽
3. **权限残留**: 测试创建的文件可能保留不安全的权限（如 777）

**影响**:

- CI 环境磁盘空间耗尽
- 测试数据污染影响后续测试
- 敏感信息泄露（如果测试数据包含敏感信息）

**现有代码审查发现**:

项目已有良好实践：使用 `t.TempDir()` 自动清理（参考 `port_allocator_test.go:25`），建议 E2E 测试框架采用类似机制。

**修复建议**:

```go
// ✅ 推荐：多重清理保障
type DataDirManager struct {
    baseDir     string
    testDirs    map[string]*TestDirInfo
    mu          sync.RWMutex
    cleanupOnce sync.Once
}

type TestDirInfo struct {
    Path       string
    TestID     string
    CreatedAt  time.Time
    PID        int
    Cleaned    bool
}

// ✅ 安全实现：使用 t.TempDir() 风格的自动清理
func (dm *DataDirManager) CreateTestDir(testID string) (string, error) {
    dm.mu.Lock()
    defer dm.mu.Unlock()

    // 1. 创建唯一目录（包含时间戳和PID）
    timestamp := time.Now().Format("20060102-150405")
    dirName := fmt.Sprintf("%s-%s-%d", testID, timestamp, os.Getpid())
    testDir := filepath.Join(dm.baseDir, dirName)

    // 2. 创建目录（使用安全权限）
    if err := os.MkdirAll(testDir, 0750); err != nil {
        return "", fmt.Errorf("failed to create test dir: %w", err)
    }

    // 3. 记录信息
    info := &TestDirInfo{
        Path:      testDir,
        TestID:    testID,
        CreatedAt: time.Now(),
        PID:       os.Getpid(),
        Cleaned:   false,
    }
    dm.testDirs[testID] = info

    // 4. 注册清理回调（即使测试崩溃也会执行）
    runtime.SetFinalizer(info, func(i *TestDirInfo) {
        if !i.Cleaned {
            log.Warn("Test dir not cleaned, auto-cleaning", "path", i.Path)
            dm.forceCleanupDir(i.Path)
        }
    })

    return testDir, nil
}

// ✅ 必须添加：定期清理过期目录
func (dm *DataDirManager) startPeriodicCleanup() {
    go func() {
        ticker := time.NewTicker(5 * time.Minute)
        defer ticker.Stop()

        for range ticker.C {
            dm.cleanupOldDirs()
        }
    }()
}

func (dm *DataDirManager) cleanupOldDirs() {
    dm.mu.Lock()
    defer dm.mu.Unlock()

    now := time.Now()
    for testID, info := range dm.testDirs {
        // 超过 1 小时的目录，强制清理
        if now.Sub(info.CreatedAt) > time.Hour {
            log.Warn("Cleaning up old test dir", "path", info.Path, "age", now.Sub(info.CreatedAt))
            dm.forceCleanupDir(info.Path)
            delete(dm.testDirs, testID)
        }
    }

    // 清理孤儿目录（PID 不存在的目录）
    dm.cleanupOrphanDirs()
}

func (dm *DataDirManager) cleanupOrphanDirs() {
    // 扫描 baseDir 下的所有目录
    entries, err := os.ReadDir(dm.baseDir)
    if err != nil {
        log.Error("Failed to scan base dir", "error", err)
        return
    }

    for _, entry := range entries {
        if !entry.IsDir() {
            continue
        }

        // 解析目录名中的 PID
        dirName := entry.Name()
        parts := strings.Split(dirName, "-")
        if len(parts) < 3 {
            continue
        }

        pidStr := parts[len(parts)-1]
        pid, err := strconv.Atoi(pidStr)
        if err != nil {
            continue
        }

        // 检查进程是否存在
        if !processExists(pid) {
            dirPath := filepath.Join(dm.baseDir, dirName)
            log.Warn("Cleaning up orphan dir", "path", dirPath, "pid", pid)
            dm.forceCleanupDir(dirPath)
        }
    }
}

// ✅ 必须添加：安全删除（避免删除敏感目录）
func (dm *DataDirManager) forceCleanupDir(path string) error {
    // 安全检查：确保是测试目录
    absPath, err := filepath.Abs(path)
    if err != nil {
        return err
    }

    absBase, err := filepath.Abs(dm.baseDir)
    if err != nil {
        return err
    }

    // 确保路径在 baseDir 下
    if !strings.HasPrefix(absPath, absBase) {
        return fmt.Errorf("refusing to delete path outside base dir: %s", absPath)
    }

    // 递归删除
    return os.RemoveAll(absPath)
}
```

**优先级**: P1（建议在 Phase 1 实现时包含）

---

#### 3.2 目录权限安全

**问题位置**: 第 3.2 节 - DataDirManager

**安全风险**: MEDIUM

**问题描述**:

文档未明确目录和文件的权限设置，可能导致：

1. **过度权限**: 使用 0777 允许所有用户访问
2. **敏感信息泄露**: 测试数据可能包含敏感信息（如密钥、密码）
3. **多租户冲突**: CI 环境中多个用户运行测试时权限冲突

**修复建议**:

```go
// ✅ 安全实现：最小权限原则
const (
    DirPermission  = 0750 // rwxr-x--- (所有者读写执行，组读执行)
    FilePermission = 0640 // rw-r----- (所有者读写，组读)
)

func (dm *DataDirManager) CreateTestDir(testID string) (string, error) {
    // ...

    // 创建目录（限制权限）
    if err := os.MkdirAll(testDir, DirPermission); err != nil {
        return "", err
    }

    // 创建子目录（数据、日志、配置）
    subdirs := []string{"data", "logs", "config"}
    for _, subdir := range subdirs {
        path := filepath.Join(testDir, subdir)
        if err := os.Mkdir(path, DirPermission); err != nil {
            return "", err
        }
    }

    // 创建敏感文件时进一步限制权限
    configFile := filepath.Join(testDir, "config", "config.yaml")
    if err := os.WriteFile(configFile, configData, FilePermission); err != nil {
        return "", err
    }

    // 如果包含密钥，使用更严格的权限
    keyFile := filepath.Join(testDir, "config", "node.key")
    if err := os.WriteFile(keyFile, keyData, 0600); err != nil { // rw-------
        return "", err
    }

    return testDir, nil
}
```

**优先级**: P1（建议在 Phase 1 实现时包含）

---

### 4. 测试数据安全（MEDIUM）

#### 4.1 测试数据中的敏感信息

**问题位置**: 测试配置和测试用例

**安全风险**: MEDIUM

**问题描述**:

E2E 测试可能使用包含敏感信息的测试数据：

1. **硬编码密钥**: 测试配置中可能硬编码测试密钥
2. **真实数据**: 使用真实用户数据进行测试
3. **日志泄露**: 测试日志可能包含敏感信息

**影响**:

- 敏感信息泄露到 Git 历史记录
- CI 日志中暴露敏感信息
- 违反数据保护法规（如 GDPR）

**修复建议**:

```go
// ✅ 推荐：使用测试专用密钥
type TestConfig struct {
    NodeID   string
    KeyPair  *TestKeyPair // 测试专用密钥对
    DataDir  string
}

type TestKeyPair struct {
    PrivateKey []byte
    PublicKey  []byte
}

// ✅ 安全实现：生成测试专用密钥（每次测试重新生成）
func GenerateTestKeyPair() (*TestKeyPair, error) {
    // 使用加密安全的随机数生成器
    privateKey := make([]byte, 32)
    if _, err := rand.Read(privateKey); err != nil {
        return nil, err
    }

    publicKey := make([]byte, 32)
    if _, err := rand.Read(publicKey); err != nil {
        return nil, err
    }

    return &TestKeyPair{
        PrivateKey: privateKey,
        PublicKey:  publicKey,
    }, nil
}

// ✅ 推荐：使用假数据生成器
type FakeDataGenerator struct {
    seed int64
}

func (g *FakeDataGenerator) GenerateUserData() *UserData {
    return &UserData{
        ID:      uuid.New().String(),
        Name:    fmt.Sprintf("test-user-%d", g.seed),
        Email:   fmt.Sprintf("test-%d@example.com", g.seed),
        Balance: rand.Intn(10000),
    }
}

// ❌ 禁止：使用真实用户数据
func TestWithRealData(t *testing.T) {
    // ❌ 危险：从生产环境导出的真实数据
    realData := loadRealUserData() // 不要这样做！
}

// ✅ 正确：使用脱敏数据
func TestWithFakeData(t *testing.T) {
    fakeData := generateFakeUserData()
}
```

**测试配置最佳实践**:

```yaml
# ✅ 推荐：test/e2e/fixtures/config.yaml
node:
  id: "test-node-${RANDOM}"  # 使用随机ID
  cluster_name: "test-cluster-${TIMESTAMP}"

# ✅ 使用占位符，不要硬编码敏感信息
auth:
  private_key: "${GENERATE_TEST_KEY}"  # 测试时动态生成
  cert_path: "${TEST_CERT_PATH}"       # 使用测试证书

# ❌ 禁止：硬编码真实密钥
# auth:
#   private_key: "MIIEvgIBADANBgkq..."  # 永远不要这样做！
```

**日志脱敏**:

```go
// ✅ 推荐：日志脱敏
type SanitizedLogger struct {
    logger *log.Logger
}

func (l *SanitizedLogger) Info(msg string, fields ...interface{}) {
    sanitized := l.sanitizeFields(fields...)
    l.logger.Info(msg, sanitized...)
}

func (l *SanitizedLogger) sanitizeFields(fields ...interface{}) []interface{} {
    // 自动脱敏敏感字段
    for i := 0; i < len(fields); i += 2 {
        key := fields[i].(string)
        if isSensitiveKey(key) {
            fields[i+1] = "***REDACTED***"
        }
    }
    return fields
}

func isSensitiveKey(key string) bool {
    sensitiveKeys := []string{"password", "secret", "key", "token", "auth"}
    lowerKey := strings.ToLower(key)
    for _, sk := range sensitiveKeys {
        if strings.Contains(lowerKey, sk) {
            return true
        }
    }
    return false
}
```

**优先级**: P1（建议在 Phase 1 实现时包含）

---

### 5. CI 环境安全（HIGH）

#### 5.1 CI 权限隔离

**问题位置**: 第 4 节 - 风险评估

**安全风险**: HIGH

**问题描述**:

文档提到"阶段 1-2 不需要特殊权限"，但未明确 CI 环境的安全隔离要求：

1. **容器逃逸风险**: 如果测试进程在容器中以 root 运行，可能导致容器逃逸
2. **网络隔离缺失**: 测试进程可能访问外部网络，导致数据泄露
3. **资源限制缺失**: 测试可能耗尽 CI 节点资源，影响其他任务

**影响**:

- CI 环境被攻击者利用
- 影响其他项目的 CI 任务
- CI 成本超支（资源滥用）

**修复建议**:

```yaml
# ✅ 推荐：GitHub Actions 安全配置
name: E2E Tests

on: [push, pull_request]

jobs:
  e2e-tests:
    runs-on: ubuntu-latest

    # ✅ 容器安全配置
    container:
      image: golang:1.21
      options: --user 1000:1000  # 非 root 用户运行

    # ✅ 资源限制（通过 timeout）
    timeout-minutes: 30

    steps:
      - uses: actions/checkout@v3

      # ✅ 网络隔离（仅允许本地访问）
      - name: Run E2E tests
        run: |
          # 限制网络访问（仅本地）
          export TEST_BIND_INTERFACE=127.0.0.1
          export TEST_EXTERNAL_ACCESS=false

          # 限制资源使用
          ulimit -u 256         # 最大进程数
          ulimit -n 1024        # 最大文件描述符数
          ulimit -v 4194304     # 最大虚拟内存（4GB）

          make test-e2e

    # ✅ 清理资源（即使测试失败）
    - name: Cleanup
      if: always()
      run: |
        # 杀死所有残留进程
        pkill -9 -f nexkvd || true
        # 清理临时文件
        rm -rf /tmp/nexkv-test-* || true
```

**Docker 安全配置**:

```dockerfile
# ✅ 推荐：Dockerfile 安全配置
FROM golang:1.21

# 创建非 root 用户
RUN useradd -m -u 1000 testuser

# 创建测试目录并设置权限
RUN mkdir -p /home/testuser/test && \
    chown -R testuser:testuser /home/testuser

# 切换到非 root 用户
USER testuser
WORKDIR /home/testuser/test

# 设置只读文件系统（可选）
# docker run --read-only --tmpfs /tmp ...
```

**优先级**: P0（必须在 Phase 2 CI 集成前解决）

---

#### 5.2 测试并发安全

**问题位置**: 第 2.2 节 - 可用性目标

**安全风险**: MEDIUM

**问题描述**:

文档提到"支持并行测试（可选）"，但并行测试存在以下风险：

1. **资源竞争**: 多个测试同时分配端口、创建目录可能导致冲突
2. **数据污染**: 共享测试数据可能导致测试结果不可预测
3. **死锁风险**: 进程间资源依赖可能导致死锁

**影响**:

- 测试结果不稳定（flaky tests）
- CI 构建失败率增加
- 难以复现和调试问题

**修复建议**:

```go
// ✅ 推荐：测试隔离策略
type TestIsolation struct {
    portAllocator    *PortAllocator
    dataDirManager   *DataDirManager
    processManager   *ProcessManager
}

func NewTestIsolation(testID string) *TestIsolation {
    return &TestIsolation{
        portAllocator:  globalPortAllocator,  // 全局单例（线程安全）
        dataDirManager: NewTestDataDirManager(testID), // 每个测试独立
        processManager: NewProcessManager(testID),     // 每个测试独立
    }
}

// ✅ 使用 t.Parallel() 时确保资源隔离
func TestParallelBasicKV(t *testing.T) {
    t.Parallel() // 声明并行测试

    // 每个并行测试使用独立的资源
    iso := NewTestIsolation(t.Name())

    // 分配独立端口
    port, err := iso.portAllocator.AllocatePort(t.Name())
    require.NoError(t, err)

    // 创建独立数据目录
    dataDir, err := iso.dataDirManager.CreateTestDir(t.Name())
    require.NoError(t, err)

    // 测试结束后自动清理
    t.Cleanup(func() {
        iso.portAllocator.ReleasePort(port)
        iso.dataDirManager.CleanupTestDir(t.Name())
        iso.processManager.StopAll(context.Background())
    })

    // 运行测试...
}

// ✅ 推荐：串行化关键资源访问
type GlobalTestLock struct {
    mu sync.Mutex
}

var globalLock = &GlobalTestLock{}

func TestWithGlobalLock(t *testing.T) {
    // 某些测试需要串行执行（如系统级配置修改）
    globalLock.mu.Lock()
    defer globalLock.mu.Unlock()

    // 测试代码...
}
```

**Makefile 配置**:

```makefile
# ✅ 推荐：并行测试配置
test-e2e-parallel:
	@echo "Running E2E tests in parallel..."
	# 限制并行度（避免资源耗尽）
	$(GO) test -race -timeout 30m -parallel 4 ./test/e2e/...

# ✅ 推荐：串行测试（更稳定）
test-e2e-serial:
	@echo "Running E2E tests serially..."
	$(GO) test -race -timeout 60m -parallel 1 ./test/e2e/...
```

**优先级**: P1（建议在 Phase 2 实现时包含）

---

### 6. 配置文件安全（MEDIUM）

#### 6.1 配置文件验证

**问题位置**: 第 3.3 节 - 目录结构

**安全风险**: MEDIUM

**问题描述**:

文档提到 `fixtures/config.yaml` 作为测试配置模板，但未说明配置安全要求：

1. **配置注入风险**: 恶意配置可能导致测试执行危险操作
2. **路径遍历**: 配置中的路径可能指向敏感目录
3. **命令注入**: 配置中的命令字段可能被利用

**修复建议**:

```go
// ✅ 推荐：配置验证器
type TestConfigValidator struct {
    allowedBaseDirs []string
    allowedCommands []string
}

func (v *TestConfigValidator) Validate(config *TestConfig) error {
    // 1. 验证数据目录
    absDataDir, err := filepath.Abs(config.DataDir)
    if err != nil {
        return fmt.Errorf("invalid data dir: %w", err)
    }

    isAllowed := false
    for _, baseDir := range v.allowedBaseDirs {
        if strings.HasPrefix(absDataDir, baseDir) {
            isAllowed = true
            break
        }
    }
    if !isAllowed {
        return fmt.Errorf("data dir outside allowed directories: %s", absDataDir)
    }

    // 2. 验证二进制路径
    if err := v.validateBinaryPath(config.BinaryPath); err != nil {
        return err
    }

    // 3. 验证启动参数
    for _, arg := range config.Args {
        if containsDangerousPattern(arg) {
            return fmt.Errorf("dangerous pattern in arg: %s", arg)
        }
    }

    return nil
}

func containsDangerousPattern(s string) bool {
    // 检查路径遍历
    if strings.Contains(s, "..") {
        return true
    }

    // 检查 shell 元字符
    if containsShellMetacharacters(s) {
        return true
    }

    // 检查环境变量注入
    if strings.Contains(s, "${") || strings.Contains(s, "$(") {
        return true
    }

    return false
}

// ✅ 使用 Zod 风格的配置 schema
type TestConfigSchema struct {
    NodeID    string `validate:"required,min=1,max=64,alphanum"`
    DataDir   string `validate:"required,dirpath,max=255"`
    LogLevel  string `validate:"required,oneof=debug info warn error"`
    BindAddr  string `validate:"required,ip"`
    BindPort  int    `validate:"required,min=1024,max=65535"`
}

func ValidateConfig(config *TestConfig) error {
    validator := validator.New()
    return validator.Struct(config)
}
```

**优先级**: P1（建议在 Phase 1 实现时包含）

---

### 7. 错误处理与日志（LOW）

#### 7.1 错误信息泄露

**问题位置**: 全局

**安全风险**: LOW

**问题描述**:

错误信息可能包含敏感信息（文件路径、配置内容、系统信息），在日志中暴露。

**修复建议**:

```go
// ✅ 推荐：安全错误处理
func (pm *ProcessManager) Start(config ProcessConfig) error {
    if err := validateConfig(&config); err != nil {
        // ❌ 危险：暴露内部细节
        // return fmt.Errorf("config validation failed: %v (path: %s, args: %v)", err, config.BinaryPath, config.Args)

        // ✅ 安全：通用错误消息 + 内部日志
        log.Error("Config validation failed",
            "error", err,
            "path", config.BinaryPath,
            "args", config.Args) // 仅内部日志
        return fmt.Errorf("invalid process configuration") // 返回给用户
    }

    // ...
}
```

**优先级**: P2（可在后续迭代中优化）

---

#### 7.2 资源清理的健壮性

**问题位置**: 第 3.2 节 - 所有管理器

**安全风险**: LOW

**问题描述**:

资源清理代码可能因异常而失败，导致资源泄露。

**修复建议**:

```go
// ✅ 推荐：防御性清理
func (pm *ProcessManager) StopAll(ctx context.Context) error {
    pm.mu.RLock()
    processes := make([]*ManagedProcess, 0, len(pm.processes))
    for _, p := range pm.processes {
        processes = append(processes, p)
    }
    pm.mu.RUnlock()

    var errs []error

    // 并行停止所有进程（加速清理）
    var wg sync.WaitGroup
    errCh := make(chan error, len(processes))

    for _, process := range processes {
        wg.Add(1)
        go func(p *ManagedProcess) {
            defer wg.Done()

            // 每个进程停止都有独立超时
            stopCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
            defer cancel()

            if err := pm.Stop(stopCtx, p.ID); err != nil {
                errCh <- fmt.Errorf("failed to stop process %s: %w", p.ID, err)
            }
        }(process)
    }

    wg.Wait()
    close(errCh)

    // 收集所有错误
    for err := range errCh {
        errs = append(errs, err)
    }

    if len(errs) > 0 {
        return fmt.Errorf("some processes failed to stop: %v", errs)
    }

    return nil
}

// ✅ 推荐：强制清理（最后手段）
func (pm *ProcessManager) ForceCleanup() error {
    // 1. 查找所有残留进程（通过进程组）
    // 2. 强制 kill -9
    // 3. 清理临时文件
    // ...
}
```

**优先级**: P2（可在后续迭代中优化）

---

## 安全检查清单

### 进程管理安全

- [ ] **CRITICAL**: 实现进程组管理（Setpgid）
- [ ] **CRITICAL**: 实现进程树清理（递归 kill）
- [ ] **HIGH**: 实现优雅关闭超时机制
- [ ] **HIGH**: 验证二进制路径白名单
- [ ] **HIGH**: 验证启动参数（防止命令注入）

### 端口管理安全

- [ ] **CRITICAL**: 实现 Listener 持有策略（防止端口泄露）
- [ ] **CRITICAL**: 实现端口自动清理（定期扫描）
- [ ] **HIGH**: 配置端口范围（避免系统端口）
- [ ] **HIGH**: 配置端口黑名单（常用服务）
- [ ] **MEDIUM**: 绑定到 127.0.0.1（默认禁止外部访问）

### 数据目录安全

- [ ] **CRITICAL**: 使用最小权限（0750/0640）
- [ ] **HIGH**: 实现定期清理过期目录
- [ ] **HIGH**: 实现孤儿目录清理（检测 PID）
- [ ] **HIGH**: 实现安全删除（路径验证）
- [ ] **MEDIUM**: 实现自动清理回调（runtime.SetFinalizer）

### 测试数据安全

- [ ] **HIGH**: 禁止使用真实用户数据
- [ ] **HIGH**: 使用测试专用密钥（动态生成）
- [ ] **HIGH**: 实现日志脱敏
- [ ] **MEDIUM**: 使用假数据生成器
- [ ] **MEDIUM**: 配置文件不包含敏感信息

### CI 环境安全

- [ ] **CRITICAL**: 非 root 用户运行容器
- [ ] **HIGH**: 配置网络隔离（仅本地）
- [ ] **HIGH**: 配置资源限制（ulimit）
- [ ] **HIGH**: 实现强制清理（即使测试失败）
- [ ] **MEDIUM**: 配置测试超时

### 并发测试安全

- [ ] **HIGH**: 实现端口分配器线程安全
- [ ] **HIGH**: 实现数据目录隔离
- [ ] **MEDIUM**: 限制并行度（-parallel 4）
- [ ] **MEDIUM**: 提供串行测试模式

---

## 风险等级定义

| 等级 | 定义 | 修复优先级 | 时间要求 |
|------|------|-----------|---------|
| **CRITICAL** | 可被直接利用，导致系统完全沦陷或数据泄露 | P0 | 立即修复（开发前） |
| **HIGH** | 可被利用，导致部分系统受损或资源泄露 | P0/P1 | 本次 PR 必须修复 |
| **MEDIUM** | 在特定条件下可被利用，影响有限 | P1/P2 | 建议本次 PR 修复 |
| **LOW** | 影响较小，但不符合最佳实践 | P2 | 后续迭代修复 |

---

## 优先级修复建议

### 必须在 Phase 1 实现前解决（P0）

1. **进程组管理**：实现完整的进程树清理
2. **端口泄露防护**：持有 Listener 防止端口泄露
3. **CI 安全配置**：非 root 用户、网络隔离、资源限制

### 建议在 Phase 1 实现时包含（P1）

4. **命令注入防护**：验证二进制路径和参数
5. **数据目录清理**：定期清理、孤儿目录清理
6. **测试数据安全**：禁止真实数据、使用测试密钥
7. **并发测试安全**：线程安全、资源隔离

### 可在后续迭代中优化（P2）

8. **错误处理**：错误信息脱敏
9. **资源清理健壮性**：防御性清理、强制清理

---

## 建议的安全测试用例

```go
// ✅ 推荐：安全测试用例
func TestProcessManager_Security(t *testing.T) {
    t.Run("CommandInjection", func(t *testing.T) {
        pm := NewProcessManager()

        // 测试命令注入防护
        maliciousConfig := ProcessConfig{
            BinaryPath: "/bin/sh",
            Args:       []string{"-c", "rm -rf /"},
        }

        err := pm.Start(maliciousConfig)
        assert.Error(t, err, "Should reject malicious command")
    })

    t.Run("PathTraversal", func(t *testing.T) {
        pm := NewProcessManager()

        // 测试路径遍历防护
        maliciousConfig := ProcessConfig{
            BinaryPath: "../../../bin/malicious",
        }

        err := pm.Start(maliciousConfig)
        assert.Error(t, err, "Should reject path traversal")
    })

    t.Run("ProcessCleanup", func(t *testing.T) {
        pm := NewProcessManager()

        // 启动进程
        config := ProcessConfig{
            BinaryPath: "./bin/nexkvd",
            Args:       []string{"--test-mode"},
        }
        require.NoError(t, pm.Start(config))

        // 模拟测试崩溃（不调用 Stop）
        // 验证 CleanupAll 是否能清理所有进程
        require.NoError(t, pm.CleanupAll())

        // 验证进程已清理
        assert.Equal(t, 0, pm.ProcessCount())
    })
}

func TestPortAllocator_Security(t *testing.T) {
    t.Run("PortLeak", func(t *testing.T) {
        pa := NewPortAllocator()

        // 分配端口但不释放
        port, err := pa.AllocatePort("test-1")
        require.NoError(t, err)

        // 模拟测试崩溃
        // 验证定期清理是否能回收端口
        time.Sleep(11 * time.Minute) // 超过清理阈值

        // 验证端口已被自动清理
        _, exists := pa.GetBinding(port)
        assert.False(t, exists, "Port should be auto-cleaned")
    })
}

func TestDataDirManager_Security(t *testing.T) {
    t.Run("PermissionCheck", func(t *testing.T) {
        dm := NewDataDirManager()

        dir, err := dm.CreateTestDir("test-1")
        require.NoError(t, err)

        // 验证目录权限
        info, err := os.Stat(dir)
        require.NoError(t, err)

        // 应该是 0750 或更严格
        assert.Equal(t, os.FileMode(0750), info.Mode().Perm(),
            "Dir should have restricted permissions")
    })

    t.Run("CleanupOnCrash", func(t *testing.T) {
        dm := NewDataDirManager()

        dir, err := dm.CreateTestDir("test-2")
        require.NoError(t, err)

        // 模拟测试崩溃（不调用 CleanupTestDir）
        runtime.GC() // 触发 finalizer

        // 验证目录被自动清理
        time.Sleep(100 * time.Millisecond)
        _, err = os.Stat(dir)
        assert.True(t, os.IsNotExist(err), "Dir should be auto-cleaned")
    })
}
```

---

## 总结

E2E 测试框架引入了**真实进程级别的测试能力**，同时也带来了新的安全风险。本审查发现了 **3 个 HIGH 级别**和 **4 个 MEDIUM 级别**的安全问题，主要集中在：

1. **进程管理**：缺少完整的进程树清理和优雅关闭机制
2. **端口管理**：端口泄露风险和端口范围配置缺失
3. **资源隔离**：数据目录清理不够健壮
4. **CI 环境**：缺少权限隔离和资源限制

### 建议行动

1. **立即**：解决 3 个 P0 级别问题（进程组管理、端口泄露防护、CI 安全）
2. **Phase 1**：解决 4 个 P1 级别问题（命令注入防护、数据目录清理、测试数据安全、并发测试安全）
3. **后续迭代**：优化 P2 级别问题（错误处理、资源清理健壮性）

### 审查结论

**建议**: **有条件批准**

**条件**: 在架构师评审前，至少解决 3 个 P0 级别问题，并在 Pre 文档中补充安全设计细节。

---

**审查人**: Security Reviewer Agent
**审查日期**: 2026-02-15
**文档版本**: v1.0
**下次审查**: 代码实现完成后（Phase 1 结束）
