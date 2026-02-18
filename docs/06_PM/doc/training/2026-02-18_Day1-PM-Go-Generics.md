# Day 1 下午：Go 泛型编程培训

> **培训时间**: 3小时（13:30-16:30）
> **培训内容**: Go 1.18+ 泛型语法 + AsyncOperation[T] 接口设计

---

## 一、Go 泛型基础（45分钟）

### 1.1 泛型简介

**Go 泛型（Generics）** 是 Go 1.18 引入的重要特性，允许编写类型参数化的代码。

**为什么需要泛型？**

**问题示例**（没有泛型）:
```go
// ❌ 为每种类型写一个函数
func SumInts(nums []int) int {
    var sum int
    for _, num := range nums {
        sum += num
    }
    return sum
}

func SumFloats(nums []float64) float64 {
    var sum float64
    for _, num := range nums {
        sum += num
    }
    return sum
}
```

**使用泛型**:
```go
// ✅ 一个函数处理多种类型
func Sum[T int | float64](nums []T) T {
    var sum T
    for _, num := range nums {
        sum += num
    }
    return sum
}

// 使用
ints := []int{1, 2, 3}
floats := []float64{1.1, 2.2, 3.3}

sumInts := Sum(ints)       // 6
sumFloats := Sum(floats)   // 6.6
```

---

### 1.2 泛型语法

#### 类型参数（Type Parameters）

**基本语法**:
```go
func FunctionName[T constraint](param T) T {
    // 函数体
}
```

**示例**:
```go
// Print 打印任意类型的值
func Print[T any](value T) {
    fmt.Println(value)
}

Print(42)        // int
Print("hello")   // string
Print(3.14)      // float64
```

---

#### 类型约束（Type Constraints）

**约束定义类型参数的范围**。

**1. 内置约束**:
```go
// any: 任意类型
func PrintAny[T any](value T) {
    fmt.Println(value)
}

// comparable: 可比较类型（支持 == 和 !=）
func Index[T comparable](slice []T, target T) int {
    for i, v := range slice {
        if v == target {
            return i
        }
    }
    return -1
}
```

**2. 自定义约束**:
```go
// Number: 数值类型约束
type Number interface {
    int | int8 | int16 | int32 | int64 |
    uint | uint8 | uint16 | uint32 | uint64 |
    float32 | float64
}

// Sum 求和（支持所有数值类型）
func Sum[T Number](nums []T) T {
    var sum T
    for _, num := range nums {
        sum += num
    }
    return sum
}
```

**3. 类型集合（Type Sets）**:
```go
// SignedInteger: 有符号整数
type SignedInteger interface {
    ~int | ~int8 | ~int16 | ~int32 | ~int64
}

// UnsignedInteger: 无符号整数
type UnsignedInteger interface {
    ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// Integer: 所有整数
type Integer interface {
    SignedInteger | UnsignedInteger
}
```

---

### 1.3 泛型类型

**泛型结构体**:
```go
// Stack 泛型栈
type Stack[T any] struct {
    elements []T
}

// Push 压栈
func (s *Stack[T]) Push(value T) {
    s.elements = append(s.elements, value)
}

// Pop 弹栈
func (s *Stack[T]) Pop() (T, error) {
    if len(s.elements) == 0 {
        var zero T
        return zero, errors.New("stack is empty")
    }
    
    index := len(s.elements) - 1
    value := s.elements[index]
    s.elements = s.elements[:index]
    return value, nil
}

// 使用
intStack := &Stack[int]{}
intStack.Push(1)
intStack.Push(2)
value, _ := intStack.Pop()  // 2

stringStack := &Stack[string]{}
stringStack.Push("hello")
value, _ = stringStack.Pop()  // "hello"
```

---

### 1.4 泛型方法

**泛型方法约束**:
```go
// ✅ 正确：方法的接收者类型参数在类型定义时指定
func (s *Stack[T]) Push(value T) {
    s.elements = append(s.elements, value)
}

// ❌ 错误：不能在方法上定义额外的类型参数
// func (s *Stack[T]) Push[U any](value U) { }
```

---

## 二、AsyncOperation[T] 接口设计（60分钟）

### 2.1 AsyncOperation[T] 概述

**AsyncOperation[T]** 是 NexKV 的统一异步接口，支持三种模式：
1. **Future 模式**: 返回 Future 对象，调用方阻塞等待
2. **Callback 模式**: 接收回调函数，异步通知
3. **Channel 模式**: 返回 Channel，流式处理

**接口定义**:
```go
// AsyncOperation[T] 统一异步接口
type AsyncOperation[T any] interface {
    // Future 模式
    Get() (T, error)
    GetWithTimeout(timeout time.Duration) (T, error)
    
    // Callback 模式
    OnComplete(callback func(T, error))
    
    // Channel 模式
    Channel() <-chan Result[T]
    
    // 状态查询
    IsDone() bool
    Cancel() error
}

// Result[T] 异步结果
type Result[T any] struct {
    Value T
    Error error
}
```

---

### 2.2 Future 模式实现

**Future 模式**: 异步操作返回 Future 对象，调用方通过 `Get()` 阻塞等待结果。

**实现**:
```go
// FutureImpl[T] Future 实现
type FutureImpl[T any] struct {
    result  *Result[T]
    done    chan struct{}
    mu      sync.RWMutex
}

// NewFuture[T] 创建 Future
func NewFuture[T any]() *FutureImpl[T] {
    return &FutureImpl[T]{
        done: make(chan struct{}),
    }
}

// SetResult 设置结果（只能设置一次）
func (f *FutureImpl[T]) SetResult(value T, err error) {
    f.mu.Lock()
    defer f.mu.Unlock()
    
    if f.result != nil {
        return  // 已经设置过结果
    }
    
    f.result = &Result[T]{Value: value, Error: err}
    close(f.done)
}

// Get 阻塞等待结果
func (f *FutureImpl[T]) Get() (T, error) {
    <-f.done
    return f.result.Value, f.result.Error
}

// GetWithTimeout 带超时的等待
func (f *FutureImpl[T]) GetWithTimeout(timeout time.Duration) (T, error) {
    select {
    case <-f.done:
        return f.result.Value, f.result.Error
    case <-time.After(timeout):
        var zero T
        return zero, errors.New("timeout")
    }
}

// IsDone 检查是否完成
func (f *FutureImpl[T]) IsDone() bool {
    select {
    case <-f.done:
        return true
    default:
        return false
    }
}
```

**使用示例**:
```go
// AsyncPut 异步写入（Future 模式）
func (s *KVStoreImpl) AsyncPut(key string, value []byte) AsyncOperation[error] {
    future := NewFuture[error]()
    
    go func() {
        // 异步执行写入
        err := s.putSync(key, value)
        future.SetResult(err, nil)
    }()
    
    return future
}

// 调用示例
future := store.AsyncPut("key", []byte("value"))
err := future.Get()  // 阻塞等待
if err != nil {
    // 处理错误
}
```

---

### 2.3 Callback 模式实现

**Callback 模式**: 异步操作接收回调函数，操作完成后自动调用。

**实现**:
```go
// CallbackImpl[T] Callback 实现
type CallbackImpl[T any] struct {
    *FutureImpl[T]
    callbacks []func(T, error)
}

// NewCallback[T] 创建 Callback
func NewCallback[T any]() *CallbackImpl[T] {
    return &CallbackImpl[T]{
        FutureImpl: NewFuture[T](),
    }
}

// OnComplete 注册回调
func (c *CallbackImpl[T]) OnComplete(callback func(T, error)) {
    c.mu.Lock()
    c.callbacks = append(c.callbacks, callback)
    done := c.IsDone()
    c.mu.Unlock()
    
    // 如果已经完成，立即调用回调
    if done {
        result := c.result
        callback(result.Value, result.Error)
    }
}

// SetResult 设置结果并调用回调
func (c *CallbackImpl[T]) SetResult(value T, err error) {
    c.FutureImpl.SetResult(value, err)
    
    c.mu.RLock()
    callbacks := make([]func(T, error), len(c.callbacks))
    copy(callbacks, c.callbacks)
    c.mu.RUnlock()
    
    // 调用所有回调
    for _, callback := range callbacks {
        go callback(value, err)
    }
}
```

**使用示例**:
```go
// AsyncGet 异步读取（Callback 模式）
func (s *KVStoreImpl) AsyncGet(key string) AsyncOperation[[]byte] {
    callback := NewCallback[[]byte]()
    
    go func() {
        value, err := s.getSync(key)
        callback.SetResult(value, err)
    }()
    
    return callback
}

// 调用示例
store.AsyncGet("key").OnComplete(func(value []byte, err error) {
    if err != nil {
        // 处理错误
        return
    }
    // 处理结果
    fmt.Println("value:", string(value))
})
```

---

### 2.4 Channel 模式实现

**Channel 模式**: 异步操作返回 Channel，支持流式处理。

**实现**:
```go
// ChannelImpl[T] Channel 实现
type ChannelImpl[T any] struct {
    *FutureImpl[T]
    ch chan Result[T]
}

// NewChannel[T] 创建 Channel
func NewChannel[T any]() *ChannelImpl[T] {
    return &ChannelImpl[T]{
        FutureImpl: NewFuture[T](),
        ch:         make(chan Result[T], 1),
    }
}

// Channel 获取 Channel
func (c *ChannelImpl[T]) Channel() <-chan Result[T] {
    return c.ch
}

// SetResult 设置结果并发送到 Channel
func (c *ChannelImpl[T]) SetResult(value T, err error) {
    c.FutureImpl.SetResult(value, err)
    
    result := Result[T]{Value: value, Error: err}
    c.ch <- result
    close(c.ch)
}
```

**使用示例**:
```go
// AsyncScan 异步扫描（Channel 模式）
func (s *KVStoreImpl) AsyncScan(start, end string) AsyncOperation[[]KeyValue] {
    ch := NewChannel[[]KeyValue]()
    
    go func() {
        results, err := s.scanSync(start, end)
        ch.SetResult(results, err)
    }()
    
    return ch
}

// 调用示例
resultCh := store.AsyncScan("a", "z").Channel()
for result := range resultCh {
    if result.Error != nil {
        // 处理错误
        break
    }
    // 处理结果
    for _, kv := range result.Value {
        fmt.Println(kv.Key, string(kv.Value))
    }
}
```

---

## 三、类型推断和约束（30分钟）

### 3.1 类型推断

**Go 编译器可以自动推断类型参数**。

**示例**:
```go
func Sum[T Number](nums []T) T {
    var sum T
    for _, num := range nums {
        sum += num
    }
    return sum
}

// 调用时无需显式指定类型
ints := []int{1, 2, 3}
sum := Sum(ints)  // 编译器推断 T 为 int
```

---

### 3.2 类型约束最佳实践

**1. 使用内置约束**:
```go
// ✅ 推荐：使用 comparable
func Index[T comparable](slice []T, target T) int {
    for i, v := range slice {
        if v == target {
            return i
        }
    }
    return -1
}
```

**2. 定义语义化的约束**:
```go
// ✅ 推荐：语义化约束
type Ordered interface {
    int | int8 | int16 | int32 | int64 |
    uint | uint8 | uint16 | uint32 | uint64 |
    float32 | float64 | string
}

// Max 返回最大值
func Max[T Ordered](a, b T) T {
    if a > b {
        return a
    }
    return b
}
```

**3. 使用类型集合组合约束**:
```go
// ✅ 推荐：组合约束
type Number interface {
    SignedInteger | UnsignedInteger | Float
}

type SignedInteger interface {
    ~int | ~int8 | ~int16 | ~int32 | ~int64
}

type UnsignedInteger interface {
    ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

type Float interface {
    ~float32 | ~float64
}
```

---

## 四、性能优化和最佳实践（30分钟）

### 4.1 性能考虑

**1. 避免不必要的类型转换**:
```go
// ❌ 避免：频繁的类型转换
func Process[T any](value T) {
    if v, ok := value.(int); ok {
        // 处理 int
    }
}

// ✅ 推荐：使用类型约束
func Process[T Number](value T) {
    // 直接使用，无需转换
}
```

**2. 内联小型泛型函数**:
```go
// ✅ 推荐：小型函数可内联
func Max[T Ordered](a, b T) T {
    if a > b {
        return a
    }
    return b
}
```

---

### 4.2 最佳实践

**1. 优先使用接口**:
```go
// ✅ 如果只需要少数方法，使用接口
type Reader interface {
    Read() ([]byte, error)
}

func Process(r Reader) {
    data, _ := r.Read()
    // 处理数据
}

// ✅ 如果需要多种类型，使用泛型
func Process[T Reader](r T) {
    data, _ := r.Read()
    // 处理数据
}
```

**2. 命名约定**:
```go
// ✅ 单个类型参数：使用 T
func Sum[T Number](nums []T) T

// ✅ 多个类型参数：使用描述性名称
func Map[K comparable, V any](m map[K]V) []K

// ✅ 特定类型：使用有意义的名称
func Process[T Data](data T)
```

**3. 避免过度泛型化**:
```go
// ❌ 避免：过度泛型化
func Add[T any](a, b T) T {
    // 无法实现，因为 any 不支持 +
}

// ✅ 推荐：适当的约束
func Add[T Number](a, b T) T {
    return a + b
}
```

---

## 五、实践环节（30分钟）

### 5.1 实现 AsyncOperation[T]

**练习**: 实现一个支持取消的 AsyncOperation[T]

**要求**:
1. 实现 `Get()`、`GetWithTimeout()`、`IsDone()`、`Cancel()`
2. 支持取消操作
3. 支持注册多个回调
4. 线程安全

**提示**:
```go
type CancellableFuture[T any] struct {
    result   *Result[T]
    done     chan struct{}
    cancel   chan struct{}
    mu       sync.RWMutex
    canceled bool
}
```

---

### 5.2 实现泛型容器

**练习**: 实现一个泛型队列（Queue）

**要求**:
1. 支持 `Enqueue(value T)`
2. 支持 `Dequeue() (T, error)`
3. 支持 `Size() int`
4. 线程安全

---

## 六、总结和 Q&A（15分钟）

### 6.1 关键要点

1. ✅ **Go 泛型基础**:
   - 类型参数：`[T constraint]`
   - 类型约束：`any`、`comparable`、自定义约束
   - 类型推断：编译器自动推断类型参数

2. ✅ **AsyncOperation[T] 设计**:
   - Future 模式：阻塞等待
   - Callback 模式：异步回调
   - Channel 模式：流式处理

3. ✅ **最佳实践**:
   - 优先使用接口
   - 适当的类型约束
   - 避免过度泛型化

---

## 七、课后阅读

**推荐阅读**:
1. [Go Generics 官方教程](https://go.dev/doc/tutorial/generics)
2. [Go Generics 规范](https://go.googlesource.com/proposal/+/refs/heads/master/design/43651-type-parameters.md)
3. NexKV 接口定义 v18.0（`docs/07_spike/2026-02-18_spike-nexkv-ddd-interface.md`）

---

**培训师**: 架构师
**培训日期**: 2026-02-18
**文档版本**: v1.0
