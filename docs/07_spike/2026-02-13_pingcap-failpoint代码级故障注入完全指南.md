---
tags: ["NexKV/e2e-testing", "Go", "故障注入", "Failpoint", "分布式测试"]
aliases: ["pingcap failpoint", "Go 故障注入框架", "Failpoint 教程"]
date: 2026-02-13
status: active
---

# pingcap/failpoint: Go 代码级故障注入完全指南

> **GitHub**: [pingcap/failpoint](https://github.com/pingcap/failpoint)
> **文档版本**: v1.0
> **创建日期**: 2026-02-13
> **目标**: 深入理解 failpoint 原理与 NexKV 集成实践

---

## 📋 目录

1. [概述与背景](#1-概述与背景)
2. [设计原理](#2-设计原理)
3. [核心概念](#3-核心概念)
4. [安装与配置](#4-安装与配置)
5. [标记函数详解](#5-标记函数详解)
6. [环境变量语法](#6-环境变量语法)
7. [AST 重写机制](#7-ast-重写机制)
8. [高级用法](#8-高级用法)
9. [并行测试支持](#9-并行测试支持)
10. [NexKV 集成实践](#10-nexkv-集成实践)
11. [与 gofail 对比](#11-与-gofail-对比)
12. [最佳实践](#12-最佳实践)
13. [故障排查](#13-故障排查)
14. [参考资料](#14-参考资料)

---

## 1. 概述与背景

### 1.1 什么是 Failpoint？

**Failpoint（故障点）** 是一种代码级别的故障注入技术，起源于 FreeBSD 操作系统。它允许开发者在代码中预定义的"故障点"注入各种错误，用于测试系统在异常情况下的行为。

> **历史背景**: FreeBSD 在 2005 年引入 failpoint 机制，通过 SYSCTL 变量控制故障注入，用于内核测试。

### 1.2 为什么需要 Failpoint？

在分布式系统开发中，**测试异常路径** 是极其困难的：

```
┌─────────────────────────────────────────────────────────────────┐
│                    分布式系统测试挑战                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ❌ 网络分区 - 难以在测试环境中模拟                               │
│  ❌ 磁盘故障 - 需要特殊硬件或复杂配置                             │
│  ❌ 内存不足 - 可能导致系统崩溃而非优雅降级                       │
│  ❌ 超时场景 - 需要精确控制时间                                   │
│  ❌ 并发竞争 - 时序难以重现                                       │
│  ❌ 部分失败 - 多步骤操作中间状态难以测试                         │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Failpoint 解决方案**：在代码中预埋故障点，运行时动态激活。

### 1.3 PingCAP failpoint 的诞生

PingCAP 在开发 TiDB/TiKV 过程中，需要一个强大的故障注入框架。虽然 etcd 的 gofail 已经存在，但 PingCAP 选择了重新实现，原因如下：

| 需求 | gofail | pingcap/failpoint |
|------|--------|-------------------|
| **代码形式** | 注释 `// gofail:` | 合法 Go 代码 |
| **编译检查** | ❌ 无 | ✅ 编译时检查 |
| **并行测试** | ❌ 全局控制 | ✅ 支持 context.Context |
| **代码补全** | ❌ 注释不支持 | ✅ IDE 完全支持 |
| **可读性** | 中等 | 高 |

### 1.4 工业界应用

| 项目/公司 | 用途 |
|-----------|------|
| **TiDB** | 分布式 NewSQL 数据库测试 |
| **TiKV** | 分布式事务 KV 存储测试 |
| **PD (Placement Driver)** | 调度器测试 |
| **Chaos Mesh** | 混沌工程平台 |

---

## 2. 设计原理

### 2.1 核心设计原则

PingCAP failpoint 遵循以下六大设计原则：

```
┌─────────────────────────────────────────────────────────────────┐
│                   failpoint 设计原则                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. 合法 Go 代码                                                │
│     └─ 不是注释，不是魔法字符串，是真正的 Go 代码                  │
│                                                                 │
│  2. 零运行时开销                                                │
│     └─ 未激活时不影响性能，不出现在最终二进制中                    │
│                                                                 │
│  3. 编译时检查                                                  │
│     └─ failpoint 代码语法错误会在编译时被发现                     │
│                                                                 │
│  4. 可读性强                                                    │
│     └─ 生成的代码清晰易读                                        │
│                                                                 │
│  5. 保持行号                                                    │
│     └─ 重写后的代码与原始代码行号一致，便于调试                    │
│                                                                 │
│  6. 并行测试支持                                                │
│     └─ 通过 context.Context 隔离不同测试的 failpoint              │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 工作流程

```
┌────────────────────────────────────────────────────────────────────────┐
│                        failpoint 工作流程                               │
├────────────────────────────────────────────────────────────────────────┤
│                                                                        │
│  1. 编写代码                                                           │
│     ┌──────────────────────────────────────────────┐                  │
│     │ func criticalOp() error {                    │                  │
│     │     failpoint.Inject("network-error", func(){│                  │
│     │         failpoint.Return(errors.New("net"))  │                  │
│     │     })                                       │                  │
│     │     return nil                               │                  │
│     │ }                                            │                  │
│     └──────────────────────────────────────────────┘                  │
│                         │                                              │
│                         ▼                                              │
│  2. failpoint-ctl enable (AST 重写)                                    │
│     ┌──────────────────────────────────────────────┐                  │
│     │ func criticalOp() error {                    │                  │
│     │     if val, _err_ := failpoint.Eval(         │                  │
│     │         _curpkg_("network-error")); _err_==nil{                 │
│     │         return errors.New("net")             │                  │
│     │     }                                        │                  │
│     │     return nil                               │                  │
│     │ }                                            │                  │
│     └──────────────────────────────────────────────┘                  │
│                         │                                              │
│                         ▼                                              │
│  3. go build (编译)                                                    │
│                         │                                              │
│                         ▼                                              │
│  4. 运行时激活                                                         │
│     $ GO_FAILPOINTS="network-error=return(true)" ./myapp             │
│                                                                        │
└────────────────────────────────────────────────────────────────────────┘
```

### 2.3 与传统方法对比

| 方法 | 优点 | 缺点 |
|------|------|------|
| **Mock 框架** | 灵活 | 仅限接口，无法测试内部逻辑 |
| **条件编译** | 无运行时开销 | 需要维护多份代码 |
| **配置开关** | 简单 | 运行时有开销，不够灵活 |
| **Failpoint** | 灵活、零开销、编译检查 | 需要 AST 重写步骤 |

---

## 3. 核心概念

### 3.1 Failpoint（故障点）

**Failpoint** 是代码中的一个标记点，只有在被激活时才会执行关联的代码块。

```go
// 基本形式
failpoint.Inject("failpoint-name", func(val failpoint.Value) {
    // 故障代码 - 仅在 failpoint 激活时执行
    fmt.Println("Failpoint triggered!", val)
})

// 简化形式（不需要值）
failpoint.Inject("simple-fp", func() {
    panic("boom!")
})
```

**核心特性**：
- 闭包内的代码可以访问外部变量
- 闭包内的 `return`、`break`、`continue` 会被正确处理
- 未激活时，整个代码块被跳过，零开销

### 3.2 Marker Functions（标记函数）

标记函数是 failpoint 的核心 API，它们是**空函数**，仅作为 AST 重写的标记：

```go
// 所有标记函数都是空实现
func Inject(fpname string, fpblock func(val Value)) {}
func InjectContext(fpname string, ctx context.Context, fpblock func(val Value)) {}
func InjectCall(fpname string, args ...any) {}
func Break(label ...string) {}
func Continue(label ...string) {}
func Goto(label string) {}
func Fallthrough() {}
func Return(results ...interface{}) {}
func Label(label string) {}
```

**为什么是空函数？**
- 编译时内联，零运行时开销
- 提供 IDE 代码补全和类型检查
- 闭包语法允许访问外部作用域变量

### 3.3 failpoint.Value

`failpoint.Value` 是故障点激活时传递的值：

```go
failpoint.Inject("return-value", func(val failpoint.Value) {
    // val 是通过环境变量传递的值
    // GO_FAILPOINTS="return-value=return(42)"
    result := val.(int)  // result == 42
    fmt.Println(result)
})
```

### 3.4 _curpkg_ 宏

`_curpkg_` 是一个自动生成的宏，用于将 failpoint 名称转换为完整路径：

```go
// 原始代码（package: github.com/myproject/mypackage）
failpoint.Inject("my-fp", func() { ... })

// 重写后
if _, _err_ := failpoint.Eval(_curpkg_("my-fp")); _err_ == nil { ... }

// _curpkg_("my-fp") 展开为 "github.com/myproject/mypackage/my-fp"
```

**优点**：
- 避免不同包之间的 failpoint 名称冲突
- 便于通过完整路径精确控制

---

## 4. 安装与配置

### 4.1 安装方式

**方式一：作为依赖安装**

```bash
go get github.com/pingcap/failpoint
```

**方式二：从源码构建工具**

```bash
# 克隆仓库
git clone https://github.com/pingcap/failpoint.git
cd failpoint

# 构建所有工具
make

# 查看生成的工具
ls bin/
# failpoint-ctl      - 命令行工具
# failpoint-toolexec - go build 集成工具
```

### 4.2 两种使用方式

#### 方式 A：failpoint-ctl（推荐新手）

```bash
# 1. 编写包含 failpoint 的代码
cat > main.go << 'EOF'
package main

import "github.com/pingcap/failpoint"

func main() {
    failpoint.Inject("test-fp", func() {
        println("failpoint triggered!")
    })
    println("normal execution")
}
EOF

# 2. 启用 failpoint（重写代码）
failpoint-ctl enable

# 3. 编译
go build -o myapp

# 4. 运行（不带 failpoint）
./myapp
# 输出: normal execution

# 5. 运行（激活 failpoint）
GO_FAILPOINTS="main/test-fp=return(true)" ./myapp
# 输出: failpoint triggered!
#       normal execution

# 6. 清理（恢复原始代码）
failpoint-ctl disable
```

#### 方式 B：failpoint-toolexec（推荐 CI/CD）

```bash
# 1. 直接构建，无需手动 enable/disable
GOCACHE=/tmp/failpoint-cache \
go build -toolexec ./bin/failpoint-toolexec -o myapp

# 2. 运行测试
GOCACHE=/tmp/failpoint-cache \
GO_FAILPOINTS="main/test-fp=return(true)" \
go test -toolexec ./bin/failpoint-toolexec ./...
```

**方式对比**：

| 特性 | failpoint-ctl | failpoint-toolexec |
|------|---------------|---------------------|
| **工作流** | 手动 enable/disable | 自动集成到 go build |
| **代码变更** | 修改源文件 | 不修改源文件 |
| **CI 友好** | 需要额外步骤 | ✅ 完美集成 |
| **调试** | 可查看生成代码 | 透明 |
| **推荐场景** | 开发调试 | CI/CD 生产环境 |

### 4.3 Makefile 集成

```makefile
# Makefile
.PHONY: test test-failpoint build

# 常规测试
test:
	go test -v ./...

# 带 failpoint 的测试
test-failpoint:
	@echo "Running tests with failpoint injection..."
	GOCACHE=/tmp/failpoint-cache \
	go test -toolexec ./tools/failpoint-toolexec \
	-coverprofile=coverage.out \
	./...

# 构建生产版本（无 failpoint）
build:
	go build -ldflags="-s -w" -o bin/nexkvd ./cmd/nexkvd

# 开发构建（带 failpoint 支持）
build-dev:
	GOCACHE=/tmp/failpoint-cache \
	go build -toolexec ./tools/failpoint-toolexec \
	-o bin/nexkvd-debug ./cmd/nexkvd
```

---

## 5. 标记函数详解

### 5.1 Inject - 基础注入

`Inject` 是最常用的标记函数，用于在任意位置注入故障代码：

```go
// 形式 1：带值的闭包
failpoint.Inject("my-fp", func(val failpoint.Value) {
    fmt.Println("Value:", val)
})

// 形式 2：忽略值
failpoint.Inject("my-fp", func(_ failpoint.Value) {
    panic("boom!")
})

// 形式 3：无参数闭包
failpoint.Inject("my-fp", func() {
    fmt.Println("Triggered!")
})
```

**重写结果**：

```go
// 形式 1 重写后
if val, _err_ := failpoint.Eval(_curpkg_("my-fp")); _err_ == nil {
    fmt.Println("Value:", val)
}

// 形式 3 重写后
if _, _err_ := failpoint.Eval(_curpkg_("my-fp")); _err_ == nil {
    fmt.Println("Triggered!")
}
```

### 5.2 InjectContext - 并行测试支持

`InjectContext` 支持 `context.Context`，用于并行测试中的 failpoint 隔离：

```go
func processData(ctx context.Context, data []byte) error {
    failpoint.InjectContext(ctx, "process-error", func(val failpoint.Value) {
        // 只有当 ctx 中允许此 failpoint 时才触发
        err := val.(error)
        panic(err)
    })

    // 正常处理逻辑
    return nil
}
```

**使用场景**：

```go
func TestParallel(t *testing.T) {
    // 测试 A：只激活特定 failpoint
    ctxA := failpoint.WithHook(context.Background(), func(ctx context.Context, fpname string) bool {
        return fpname == "process-error"
    })

    // 测试 B：禁用所有 failpoint
    ctxB := failpoint.WithHook(context.Background(), func(ctx context.Context, fpname string) bool {
        return false
    })

    // 并行运行
    var wg sync.WaitGroup
    wg.Add(2)

    go func() {
        defer wg.Done()
        processData(ctxA, data) // 会触发 failpoint
    }()

    go func() {
        defer wg.Done()
        processData(ctxB, data) // 不会触发 failpoint
    }()

    wg.Wait()
}
```

### 5.3 InjectCall - 函数调用注入

`InjectCall` 允许动态注入函数调用，避免污染源代码：

```go
// 在代码中埋点
failpoint.InjectCall("before-save", record)

// 测试中动态注册回调
func TestWithCallback(t *testing.T) {
    failpoint.EnableCall("before-save", func(record *Record) {
        // 修改记录
        record.Timestamp = time.Now()
    })

    // 运行测试...
}
```

**注意**：`InjectCall` 不能通过环境变量激活，必须在同一进程中调用 `EnableCall`。

### 5.4 流程控制标记

这些标记用于在循环和条件语句中控制流程：

#### Break

```go
failpoint.Label("outer")
for i := 0; i < 10; i++ {
    for j := 0; j < 10; j++ {
        failpoint.Inject("break-inner", func() {
            failpoint.Break() // break 内层循环
        })
        failpoint.Inject("break-outer", func() {
            failpoint.Break("outer") // break 外层循环
        })
    }
}
```

#### Continue

```go
for i := 0; i < 10; i++ {
    failpoint.Inject("skip-even", func(val failpoint.Value) {
        if val.(int)%2 == 0 {
            failpoint.Continue() // 跳过偶数
        }
    })
    process(i)
}
```

#### Goto

```go
failpoint.Label("retry")
start:
    err := doSomething()
    failpoint.Inject("retry-on-error", func() {
        if err != nil {
            failpoint.Goto("retry") // 重试
        }
    })
```

#### Return

```go
func criticalOperation() (result int, err error) {
    failpoint.Inject("early-return", func(val failpoint.Value) {
        failpoint.Return(val.(int), errors.New("injected error"))
    })

    // 正常逻辑
    return 42, nil
}
```

#### Fallthrough

```go
switch value {
case 1:
    failpoint.Inject("fallthrough-case", func() {
        failpoint.Fallthrough() // 继续执行下一个 case
    })
    handleCase1()
case 2:
    handleCase2()
}
```

### 5.5 完整示例：循环中的 failpoint

```go
func ProcessBatch(records []Record) error {
    failpoint.Label("batch-loop")

    for i, record := range records {
        // 模拟处理超时
        failpoint.Inject("process-timeout", func(val failpoint.Value) {
            if i == val.(int) {
                time.Sleep(10 * time.Second)
                failpoint.Break("batch-loop")
            }
        })

        // 模拟处理错误
        failpoint.Inject("process-error", func(val failpoint.Value) {
            if i >= val.(int) {
                return fmt.Errorf("injected error at index %d", i)
            }
        })

        // 正常处理
        if err := processRecord(record); err != nil {
            return err
        }
    }

    return nil
}
```

---

## 6. 环境变量语法

### 6.1 基本语法

failpoint 通过 `GO_FAILPOINTS` 环境变量激活，语法类似 FreeBSD SYSCTL：

```
GO_FAILPOINTS="<package-path>/<failpoint-name>=<term>[;<more-terms>]"
```

### 6.2 Term 语法

```
[<percent>%][<count>*]<action>[(args...)][-><more-terms>]
```

| 组成部分 | 说明 | 示例 |
|----------|------|------|
| `<percent>%` | 触发概率 | `50%` = 50% 概率触发 |
| `<count>*` | 触发次数 | `5*` = 只触发 5 次 |
| `<action>` | 动作类型 | return, panic, sleep, etc. |
| `(args...)` | 动作参数 | `return(42)` |
| `-><more>` | 链式动作 | `return(1)->return(2)` |

### 6.3 动作类型

| 动作 | 说明 | 示例 |
|------|------|------|
| `off` | 不触发（默认） | `off` |
| `return` | 触发并返回值 | `return(true)` `return("error")` |
| `sleep` | 休眠指定毫秒 | `sleep(1000)` = 1秒 |
| `panic` | 触发 panic | `panic("boom!")` |
| `break` | 进入调试器 | `break` |
| `print` | 打印日志 | `print` |
| `pause` | 暂停直到禁用 | `pause` |

### 6.4 实际示例

```bash
# 简单返回
GO_FAILPOINTS="github.com/nexkv/storage/write-error=return(true)"

# 返回特定值
GO_FAILPOINTS="github.com/nexkv/storage/latency=return(100)"

# 50% 概率触发
GO_FAILPOINTS="github.com/nexkv/network/unstable=50%return(true)"

# 只触发 3 次
GO_FAILPOINTS="github.com/nexkv/storage/disk-full=3*return(true)"

# 链式：先 sleep 再 return
GO_FAILPOINTS="github.com/nexkv/network/timeout=sleep(5000)->return(true)"

# 多个 failpoint（分号分隔）
GO_FAILPOINTS="fp1=return(1);fp2=panic;fp3=50%sleep(100)"
```

### 6.5 GO_FAILPOINTS_OPTS

对于复杂配置，可以使用配置文件：

```bash
# 从文件加载
GO_FAILPOINTS_OPTS="-f /path/to/failpoints.conf"
```

配置文件格式：

```ini
# failpoints.conf
github.com/nexkv/storage/write-error=return(true)
github.com/nexkv/network/partition=50%sleep(1000)
github.com/nexkv/replication/failure=3*panic("replication failed")
```

---

## 7. AST 重写机制

### 7.1 重写原理

pingcap/failpoint 使用 Go 的 `go/ast` 包进行源代码重写：

```
┌─────────────────────────────────────────────────────────────────────┐
│                      AST 重写流程                                    │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  1. 解析源文件                                                       │
│     └─ go/parser.ParseFile() → *ast.File                           │
│                                                                     │
│  2. 检查是否导入 failpoint                                           │
│     └─ 未导入则跳过                                                  │
│                                                                     │
│  3. 遍历 AST 查找标记函数调用                                         │
│     └─ ast.Inspect() 递归遍历                                       │
│                                                                     │
│  4. 重写调用点                                                       │
│     └─ failpoint.Inject(...) → if val, err := failpoint.Eval(...){}│
│                                                                     │
│  5. 生成绑定文件                                                     │
│     └─ binding__failpoint_binding__.go                             │
│                                                                     │
│  6. 输出重写后的代码                                                 │
│     └─ 保持原始行号和格式                                            │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 7.2 重写示例详解

**原始代码**：

```go
package storage

import "github.com/pingcap/failpoint"

func Write(key string, value []byte) error {
    failpoint.Inject("disk-full", func() {
        failpoint.Return(errors.New("disk full"))
    })

    return writeToDisk(key, value)
}
```

**重写后**：

```go
// Code generated by failpoint-ctl. DO NOT EDIT.
// See: https://github.com/pingcap/failpoint

package storage

import (
    "errors"
    "github.com/pingcap/failpoint"
)

// 自动生成的包路径常量
const _pkg_ = "github.com/nexkv/storage"

func _curpkg_(fpname string) string {
    return _pkg_ + "/" + fpname
}

func Write(key string, value []byte) error {
    if _, _err_ := failpoint.Eval(_curpkg_("disk-full")); _err_ == nil {
        return errors.New("disk full")
    }

    return writeToDisk(key, value)
}
```

### 7.3 复杂场景重写

#### 闭包中的外部变量访问

```go
// 原始
func process(records []Record) {
    for _, r := range records {
        failpoint.Inject("skip-record", func() {
            fmt.Println("Skipping:", r.ID) // 访问外部变量 r
        })
    }
}

// 重写后
func process(records []Record) {
    for _, r := range records {
        if _, _err_ := failpoint.Eval(_curpkg_("skip-record")); _err_ == nil {
            fmt.Println("Skipping:", r.ID) // 仍然可以访问 r
        }
    }
}
```

#### 循环中的 break/continue

```go
// 原始
outer:
    for i := 0; i < 10; i++ {
        for j := 0; j < 10; j++ {
            failpoint.Inject("break-outer", func() {
                failpoint.Break("outer")
            })
        }
    }

// 重写后
outer:
    for i := 0; i < 10; i++ {
        for j := 0; j < 10; j++ {
            if _, _err_ := failpoint.Eval(_curpkg_("break-outer")); _err_ == nil {
                break outer
            }
        }
    }
```

### 7.4 行号保持

failpoint 重写器会保持原始代码的行号：

```go
// 原始文件 main.go
01: package main
02:
03: import "github.com/pingcap/failpoint"
04:
05: func main() {
06:     failpoint.Inject("test", func() {
07:         println("test")
08:     })
09:     println("done")
10: }

// 重写后（行号不变！）
01: package main
02:
03: import "github.com/pingcap/failpoint"
04:
05: func main() {
06:     if _, _err_ := failpoint.Eval(_curpkg_("test")); _err_ == nil {
07:         println("test")
08:     }
09:     println("done")
10: }
```

**好处**：调试时的堆栈跟踪仍然指向正确的源代码位置。

---

## 8. 高级用法

### 8.1 SELECT 语句中的 failpoint

控制 SELECT case 的行为：

```go
func (s *Server) handleRequest() {
    select {
    case <-func() chan Request {
        failpoint.Inject("block-priority", func() chan Request {
            return make(chan Request) // 返回空 channel，阻塞此 case
        })
        return s.priorityCh
    }():
        handlePriority()

    case req := <-s.normalCh:
        handleNormal(req)
    }
}
```

### 8.2 SWITCH 语句中的 failpoint

动态添加 switch case：

```go
func handleOp(op string) {
    switch op {
    case "read":
        handleRead()
    case "write":
        handleWrite()

    case func() bool {
        failpoint.Inject("custom-op", func(val failpoint.Value) bool {
            return val.(string) == op
        })
        return false
    }():
        handleCustomOp()

    default:
        panic("unknown op")
    }
}
```

### 8.3 IF 条件中的 failpoint

控制条件表达式：

```go
func shouldRetry(err error) bool {
    if err != nil && func() bool {
        failpoint.Inject("always-retry", func() bool {
            return true
        })
        return isTransient(err)
    }() {
        return true
    }
    return false
}
```

### 8.4 函数参数中的 failpoint

```go
func callExternal(api string, timeout time.Duration) {
    result := external.Call(
        api,
        func() time.Duration {
            failpoint.Inject("custom-timeout", func(val failpoint.Value) time.Duration {
                return time.Duration(val.(int)) * time.Millisecond
            })
            return timeout
        }(),
    )
    // ...
}
```

### 8.5 嵌套 failpoint

```go
func complexOperation() error {
    failpoint.Inject("outer-fp", func(val failpoint.Value) {
        // 外层 failpoint 激活时
        failpoint.Inject("inner-fp", func() {
            // 内层 failpoint 也可能激活
            panic("nested failpoint!")
        })

        fmt.Println("Outer triggered with value:", val)
    })

    return nil
}
```

---

## 9. 并行测试支持

### 9.1 问题背景

在并行测试中，不同测试可能需要激活不同的 failpoint，传统全局控制方式无法满足需求。

### 9.2 WithHook 解决方案

```go
func TestParallelFailpoints(t *testing.T) {
    // 测试 A：只激活读取相关的 failpoint
    ctxA := failpoint.WithHook(context.Background(), func(ctx context.Context, fpname string) bool {
        readFailpoints := map[string]bool{
            "read-timeout":    true,
            "read-corruption": true,
            "read-notfound":   true,
        }
        return readFailpoints[fpname]
    })

    // 测试 B：只激活写入相关的 failpoint
    ctxB := failpoint.WithHook(context.Background(), func(ctx context.Context, fpname string) bool {
        writeFailpoints := map[string]bool{
            "write-failure": true,
            "disk-full":     true,
        }
        return writeFailpoints[fpname]
    })

    // 并行运行
    t.Run("ReadTest", func(t *testing.T) {
        t.Parallel()
        testReadPath(ctxA)
    })

    t.Run("WriteTest", func(t *testing.T) {
        t.Parallel()
        testWritePath(ctxB)
    })
}
```

### 9.3 完整并行测试示例

```go
package storage_test

import (
    "context"
    "testing"

    "github.com/pingcap/failpoint"
    "github.com/stretchr/testify/require"
)

func TestStorageParallel(t *testing.T) {
    // 定义测试场景
    scenarios := []struct {
        name        string
        ctx         context.Context
        failpoints  map[string]bool
        expectError bool
    }{
        {
            name: "normal",
            ctx:  context.Background(),
            failpoints: map[string]bool{},
            expectError: false,
        },
        {
            name: "write-error",
            ctx: context.Background(),
            failpoints: map[string]bool{
                "storage/write-error": true,
            },
            expectError: true,
        },
        {
            name: "network-partition",
            ctx: context.Background(),
            failpoints: map[string]bool{
                "network/partition": true,
            },
            expectError: true,
        },
    }

    for _, sc := range scenarios {
        t.Run(sc.name, func(t *testing.T) {
            t.Parallel()

            // 创建带 hook 的 context
            ctx := failpoint.WithHook(sc.ctx, func(ctx context.Context, fpname string) bool {
                return sc.failpoints[fpname]
            })

            // 运行测试
            err := runStorageTest(ctx)
            if sc.expectError {
                require.Error(t, err)
            } else {
                require.NoError(t, err)
            }
        })
    }
}
```

---

## 10. NexKV 集成实践

### 10.1 NexKV Failpoint 规划

```
┌─────────────────────────────────────────────────────────────────────┐
│                    NexKV Failpoint 规划                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Layer 0: Storage (internal/storage)                               │
│  ├── disk-full              - 磁盘满                                │
│  ├── write-error            - 写入错误                              │
│  ├── read-corruption        - 读取数据损坏                          │
│  └── sync-error             - fsync 错误                            │
│                                                                     │
│  Layer 1: Consistency (internal/consistency)                       │
│  ├── 2pc-prepare-timeout    - 2PC prepare 超时                      │
│  ├── 2pc-commit-error       - 2PC commit 错误                       │
│  ├── quorum-not-reached     - Quorum 未达成                         │
│  └── merkle-tree-mismatch   - Merkle Tree 校验失败                  │
│                                                                     │
│  Layer 2: Coordination (internal/coordination)                     │
│  ├── gossip-message-drop    - Gossip 消息丢失                       │
│  ├── leader-election-timeout - 选举超时                             │
│  └── heartbeat-miss         - 心跳丢失                              │
│                                                                     │
│  Layer 3: Routing (internal/routing)                               │
│  ├── route-cache-stale      - 路由缓存过期                          │
│  └── shard-split-failure    - 分片分裂失败                          │
│                                                                     │
│  Layer 4: Client (internal/client)                                 │
│  ├── connection-refused     - 连接被拒绝                            │
│  ├── request-timeout        - 请求超时                              │
│  └── retry-exhausted        - 重试耗尽                              │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 10.2 Storage Layer 实现

```go
// internal/storage/disk.go
package storage

import (
    "errors"
    "github.com/pingcap/failpoint"
)

var (
    ErrDiskFull     = errors.New("disk full")
    ErrWriteError   = errors.New("write error")
    ErrReadCorrupt  = errors.New("read corruption")
)

// Write 写入数据到磁盘
func (s *Storage) Write(key string, value []byte) error {
    // 故障注入点：磁盘满
    failpoint.Inject("disk-full", func() {
        failpoint.Return(ErrDiskFull)
    })

    // 故障注入点：写入错误
    failpoint.Inject("write-error", func(val failpoint.Value) {
        if err, ok := val.(error); ok {
            failpoint.Return(err)
        }
        failpoint.Return(ErrWriteError)
    })

    // 正常写入逻辑
    return s.writeToDisk(key, value)
}

// Read 从磁盘读取数据
func (s *Storage) Read(key string) ([]byte, error) {
    var result []byte

    // 故障注入点：读取数据损坏
    failpoint.Inject("read-corruption", func(val failpoint.Value) {
        corruptRatio := val.(float64)
        result = corruptData(result, corruptRatio)
    })

    // 故障注入点：读取错误
    failpoint.Inject("read-error", func() {
        failpoint.Return(nil, ErrReadCorrupt)
    })

    data, err := s.readFromDisk(key)
    if err != nil {
        return nil, err
    }

    if result != nil {
        return result, nil // 返回损坏的数据
    }
    return data, nil
}

// Sync 强制同步到磁盘
func (s *Storage) Sync() error {
    failpoint.Inject("sync-error", func() {
        failpoint.Return(errors.New("fsync failed"))
    })

    return s.file.Sync()
}
```

### 10.3 Consistency Layer 实现

```go
// internal/consistency/2pc.go
package consistency

import (
    "context"
    "time"

    "github.com/pingcap/failpoint"
)

// TwoPhaseCommit 执行两阶段提交
func (c *Coordinator) TwoPhaseCommit(ctx context.Context, tx *Transaction) error {
    // Phase 1: Prepare
    failpoint.InjectContext(ctx, "2pc-prepare-timeout", func() {
        time.Sleep(30 * time.Second)
        failpoint.Return(ErrPrepareTimeout)
    })

    failpoint.InjectContext(ctx, "2pc-prepare-error", func(val failpoint.Value) {
        nodeID := val.(string)
        if tx.InvolvesNode(nodeID) {
            failpoint.Return(&PrepareError{NodeID: nodeID})
        }
    })

    if err := c.prepare(ctx, tx); err != nil {
        return c.rollback(ctx, tx)
    }

    // Phase 2: Commit
    failpoint.InjectContext(ctx, "2pc-commit-error", func() {
        failpoint.Return(ErrCommitFailed)
    })

    if err := c.commit(ctx, tx); err != nil {
        // 严重错误，需要人工干预
        return c.markAsIndoubt(tx, err)
    }

    return nil
}

// CheckQuorum 检查是否达成 Quorum
func (c *Coordinator) CheckQuorum(shard *Shard) (bool, error) {
    failpoint.Inject("quorum-not-reached", func() {
        failpoint.Return(false, ErrQuorumNotReached)
    })

    responses := c.broadcastCheck(shard)
    return len(responses) >= shard.Quorum, nil
}

// VerifyMerkleTree 验证 Merkle Tree
func (c *Coordinator) VerifyMerkleTree(shard *Shard) error {
    failpoint.Inject("merkle-tree-mismatch", func(val failpoint.Value) {
        mismatchNode := val.(string)
        failpoint.Return(&MerkleTreeError{
            ShardID:    shard.ID,
            MismatchAt: mismatchNode,
        })
    })

    rootHash := c.computeMerkleRoot(shard)
    if rootHash != shard.ExpectedRoot {
        return ErrMerkleTreeMismatch
    }
    return nil
}
```

### 10.4 Network Layer 实现

```go
// internal/network/transport.go
package network

import (
    "context"
    "time"

    "github.com/pingcap/failpoint"
)

// SendMessage 发送网络消息
func (t *Transport) SendMessage(ctx context.Context, msg *Message) error {
    // 故障注入：消息丢失
    failpoint.InjectContext(ctx, "network-drop", func(val failpoint.Value) {
        dropRate := val.(float64)
        if rand.Float64() < dropRate {
            // 静默丢弃，不返回错误
            failpoint.Return(nil)
        }
    })

    // 故障注入：网络延迟
    failpoint.InjectContext(ctx, "network-latency", func(val failpoint.Value) {
        latency := time.Duration(val.(int)) * time.Millisecond
        time.Sleep(latency)
    })

    // 故障注入：网络分区
    failpoint.InjectContext(ctx, "network-partition", func(val failpoint.Value) {
        partitionedNodes := val.([]string)
        for _, node := range partitionedNodes {
            if msg.Target == node {
                failpoint.Return(ErrNetworkPartition)
            }
        }
    })

    return t.doSend(ctx, msg)
}
```

### 10.5 测试用例

```go
// internal/storage/storage_test.go
package storage_test

import (
    "context"
    "testing"

    "github.com/pingcap/failpoint"
    "github.com/stretchr/testify/require"

    "nexkv/internal/storage"
)

func TestStorageWrite_DiskFull(t *testing.T) {
    s := storage.NewTestStorage(t)
    defer s.Close()

    // 激活 disk-full failpoint
    require.NoError(t, failpoint.Enable("nexkv/internal/storage/disk-full", "return(true)"))
    defer failpoint.Disable("nexkv/internal/storage/disk-full")

    err := s.Write("key1", []byte("value1"))
    require.ErrorIs(t, err, storage.ErrDiskFull)
}

func TestStorageRead_Corruption(t *testing.T) {
    s := storage.NewTestStorage(t)
    defer s.Close()

    // 先写入数据
    require.NoError(t, s.Write("key1", []byte("value1")))

    // 激活 50% 数据损坏
    require.NoError(t, failpoint.Enable("nexkv/internal/storage/read-corruption", "return(0.5)"))
    defer failpoint.Disable("nexkv/internal/storage/read-corruption")

    data, err := s.Read("key1")
    require.NoError(t, err)
    // 数据可能被损坏
    t.Logf("Read data: %s (may be corrupted)", string(data))
}

func Test2PC_PrepareFailure(t *testing.T) {
    ctx := context.Background()
    coord := NewTestCoordinator(t)

    // 只在 prepare 阶段注入故障
    ctx = failpoint.WithHook(ctx, func(ctx context.Context, fpname string) bool {
        return fpname == "2pc-prepare-error"
    })

    // 激活 failpoint
    require.NoError(t, failpoint.Enable(
        "nexkv/internal/consistency/2pc-prepare-error",
        `return("node-3")`,
    ))
    defer failpoint.Disable("nexkv/internal/consistency/2pc-prepare-error")

    tx := NewTestTransaction(t, []string{"node-1", "node-2", "node-3"})
    err := coord.TwoPhaseCommit(ctx, tx)

    // 应该触发 rollback
    require.Error(t, err)
    require.Contains(t, err.Error(), "node-3")
}

func TestNetworkPartition(t *testing.T) {
    tests := []struct {
        name       string
        failpoint  string
        expectErr  bool
    }{
        {
            name:      "normal",
            failpoint: "off",
            expectErr: false,
        },
        {
            name:      "partition",
            failpoint: `return(["node-2","node-3"])`,
            expectErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ctx := context.Background()
            transport := NewTestTransport(t)

            require.NoError(t, failpoint.Enable(
                "nexkv/internal/network/network-partition",
                tt.failpoint,
            ))
            defer failpoint.Disable("nexkv/internal/network/network-partition")

            msg := &Message{Target: "node-2", Payload: []byte("test")}
            err := transport.SendMessage(ctx, msg)

            if tt.expectErr {
                require.Error(t, err)
            } else {
                require.NoError(t, err)
            }
        })
    }
}
```

### 10.6 Makefile 集成

```makefile
# NexKV Makefile with failpoint support

FAILPOINT_BIN := $(shell pwd)/tools/failpoint-ctl
GOCACHE_FAILPOINT := /tmp/nexkv-failpoint-cache

# 初始化 failpoint 工具
failpoint-setup:
	@echo "Setting up failpoint tools..."
	@if [ ! -f $(FAILPOINT_BIN) ]; then \
		git clone https://github.com/pingcap/failpoint.git /tmp/failpoint && \
		cd /tmp/failpoint && make && \
		cp bin/failpoint-ctl $(FAILPOINT_BIN); \
	fi

# 运行所有测试（包含 failpoint）
test: failpoint-setup
	@echo "Running all tests with failpoint support..."
	GOCACHE=$(GOCACHE_FAILPOINT) \
	go test -toolexec $(FAILPOINT_BIN) -v -race -coverprofile=coverage.out ./...

# 运行特定 failpoint 测试
test-failpoint-%: failpoint-setup
	@echo "Running failpoint test: $*"
	GOCACHE=$(GOCACHE_FAILPOINT) \
	GO_FAILPOINTS="nexkv/internal/storage/$*=return(true)" \
	go test -toolexec $(FAILPOINT_BIN) -v -run TestStorage ./internal/storage/...

# 运行混沌测试（随机激活 failpoint）
test-chaos: failpoint-setup
	@echo "Running chaos tests..."
	@for fp in disk-full write-error read-corruption; do \
		echo "Testing failpoint: $$fp"; \
		GOCACHE=$(GOCACHE_FAILPOINT) \
		GO_FAILPOINTS="nexkv/internal/storage/$$fp=50%return(true)" \
		go test -toolexec $(FAILPOINT_BIN) -v -run TestStorage ./internal/storage/... || exit 1; \
	done

# 生成 failpoint 测试报告
test-report: test
	@echo "Generating test report..."
	go tool cover -html=coverage.out -o coverage.html
	@echo "Report generated: coverage.html"
```

---

## 11. 与 gofail 对比

### 11.1 技术对比

| 特性 | pingcap/failpoint | etcd-io/gofail |
|------|-------------------|----------------|
| **注入方式** | AST 重写合法 Go 代码 | 注释 `// gofail:` |
| **编译检查** | ✅ 编译时检查 | ❌ 运行时检查 |
| **IDE 支持** | ✅ 完整支持 | ⚠️ 注释无补全 |
| **并行测试** | ✅ context.Context | ❌ 仅全局控制 |
| **性能开销** | 零（编译时移除） | 零 |
| **学习曲线** | 中等 | 简单 |
| **维护状态** | 活跃 | 活跃 |

### 11.2 语法对比

**pingcap/failpoint**:

```go
// 合法 Go 代码，有 IDE 支持
failpoint.Inject("network-error", func() {
    failpoint.Return(errors.New("network failed"))
})
```

**etcd-io/gofail**:

```go
// 注释形式，需要特殊处理
// gofail: var networkError error
if networkError != nil {
    return networkError
}
```

### 11.3 选择建议

| 场景 | 推荐 |
|------|------|
| **新项目** | pingcap/failpoint |
| **已有 etcd 生态项目** | gofail |
| **需要并行测试** | pingcap/failpoint |
| **快速原型** | gofail |
| **生产级分布式系统** | pingcap/failpoint |

---

## 12. 最佳实践

### 12.1 命名规范

```go
// ✅ 好的命名
failpoint.Inject("storage/disk-full", ...)           // 包含模块前缀
failpoint.Inject("network/request-timeout", ...)     // 清晰描述故障类型
failpoint.Inject("replication/leader-election-failure", ...)

// ❌ 不好的命名
failpoint.Inject("error", ...)        // 太模糊
failpoint.Inject("fp1", ...)          // 无意义
failpoint.Inject("test", ...)         // 不描述故障类型
```

### 12.2 放置位置

```go
func Write(key string, value []byte) error {
    // ✅ 放在实际操作之前
    failpoint.Inject("disk-full", func() {
        failpoint.Return(ErrDiskFull)
    })

    // ❌ 不要放在无关位置
    // failpoint.Inject("random-fp", func() { ... })

    return s.writeToDisk(key, value)
}

// ✅ 在关键路径上放置
func (c *Coordinator) Replicate(shard *Shard) error {
    failpoint.Inject("replication/quorum-failure", ...)
    failpoint.Inject("replication/network-partition", ...)
    // ...
}
```

### 12.3 错误类型设计

```go
// 定义明确的错误类型
var (
    ErrDiskFull     = errors.New("storage: disk full")
    ErrQuorumFailed = errors.New("consistency: quorum not reached")
    ErrNetworkPartition = errors.New("network: partition detected")
)

// failpoint 返回明确的错误
failpoint.Inject("disk-full", func() {
    failpoint.Return(ErrDiskFull)
})
```

### 12.4 测试覆盖

```go
// 使用表格驱动测试覆盖多种 failpoint 场景
func TestWithFailpoints(t *testing.T) {
    tests := []struct {
        name       string
        failpoint  string
        value      string
        expectErr  error
    }{
        {"normal", "off", "", nil},
        {"disk-full", "return(true)", "", ErrDiskFull},
        {"write-error", "return(custom)", "custom error", errors.New("custom error")},
        {"timeout", "sleep(5000)", "", context.DeadlineExceeded},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

### 12.5 CI/CD 集成

```yaml
# .github/workflows/failpoint-test.yml
name: Failpoint Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Setup Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Setup failpoint
        run: |
          git clone https://github.com/pingcap/failpoint.git /tmp/failpoint
          cd /tmp/failpoint && make

      - name: Run failpoint tests
        run: |
          GOCACHE=/tmp/failpoint-cache \
          go test -toolexec /tmp/failpoint/bin/failpoint-toolexec \
            -v -race -coverprofile=coverage.out ./...

      - name: Run chaos tests
        run: |
          ./scripts/run-chaos-tests.sh

      - name: Upload coverage
        uses: codecov/codecov-action@v3
        with:
          file: ./coverage.out
```

---

## 13. 故障排查

### 13.1 常见问题

#### Q1: failpoint 没有触发

```bash
# 检查 failpoint 名称是否正确
go run -toolexec ./bin/failpoint-toolexec -tags=debug main.go

# 输出当前所有注册的 failpoint
GODEBUG=failpoint=1 ./myapp
```

#### Q2: 编译错误 "undefined: failpoint.Inject"

```bash
# 确保 failpoint-ctl 已经 enable
failpoint-ctl enable

# 或者使用 toolexec
go build -toolexec ./bin/failpoint-toolexec
```

#### Q3: 并行测试互相干扰

```go
// 使用 WithHook 隔离
ctx := failpoint.WithHook(context.Background(), func(ctx context.Context, fpname string) bool {
    // 只允许特定的 failpoint
    return fpname == "my-specific-fp"
})
```

### 13.2 调试技巧

```bash
# 打印所有 failpoint 路径
GO_FAILPOINTS="*=print" ./myapp

# 使用 break 进入调试器
GO_FAILPOINTS="mypackage/my-fp=break" gdb ./myapp

# 查看生成的代码
failpoint-ctl enable
cat binding__failpoint_binding__.go
```

---

## 14. 参考资料

### 14.1 官方资源

| 资源 | 链接 |
|------|------|
| **GitHub 仓库** | [github.com/pingcap/failpoint](https://github.com/pingcap/failpoint) |
| **设计文档** | [PingCAP Blog](https://pingcap.co.jp/blog/design-and-implementation-of-golang-failpoints/) |
| **Go Package 文档** | [pkg.go.dev](https://pkg.go.dev/github.com/pingcap/failpoint) |

### 14.2 相关工具

| 工具 | 链接 |
|------|------|
| **etcd gofail** | [github.com/etcd-io/gofail](https://github.com/etcd-io/gofail) |
| **FreeBSD failpoint** | [FreeBSD Handbook](https://docs.freebsd.org/en/books/developers-handbook/testing/) |

### 14.3 学习资源

| 资源 | 链接 |
|------|------|
| **TiDB 开发指南** | [pingcap.github.io/tidb-dev-guide](https://pingcap.github.io/tidb-dev-guide/) |
| **Testing Distributed Systems** | [asatarin.github.io](https://asatarin.github.io/testing-distributed-systems/) |
| **Chaos Engineering** | [chaos-mesh.org](https://chaos-mesh.org/) |

---

**文档版本**: v1.0
**创建日期**: 2026-02-13
**维护者**: NexKV 开发团队

---

## 附录 A: 快速参考卡

```go
// ===== 基本用法 =====
failpoint.Inject("name", func() { panic("boom") })
failpoint.Inject("name", func(v failpoint.Value) { fmt.Println(v) })
failpoint.Return(err)
failpoint.Break()
failpoint.Continue()

// ===== Context 支持 =====
failpoint.InjectContext(ctx, "name", func() { ... })
ctx := failpoint.WithHook(ctx, hook)

// ===== 环境变量 =====
GO_FAILPOINTS="pkg/name=return(true)"
GO_FAILPOINTS="pkg/name=50%return(1)"
GO_FAILPOINTS="pkg/name=3*panic"

// ===== 构建命令 =====
failpoint-ctl enable    # 启用
failpoint-ctl disable   # 禁用
go build -toolexec failpoint-toolexec  # CI 模式
```
