// Package service 定义 NexKV 分布式系统的领域服务接口
//
// 本包采用领域驱动设计(DDD)原则：
//   - domain/service: 定义接口（契约）
//   - infrastructure: 提供具体实现
//
// # 核心接口概览
//
//	RPCSync      → 同步 RPC 调用（阻塞等待结果）
//	RPCAsync     → 异步 RPC 调用（返回 AsyncOperation，支持回调）
//	BroadcastProgress  → 广播进度追踪
//	BroadcastListener → 广播事件监听
//	DiscoveryService  → 节点发现服务
//
// # RPC 调用模式
//
// NexKV 提供两种 RPC 调用模式：同步(RPCSync)和异步(RPCAsync)。
//
// ## 1. RPCSync - 同步调用
//
// 阻塞式调用，直接返回结果。适用于简单场景:
//
//	// 单播调用
//	resp, err := rpc.Call(ctx, peerID, request)
//
//	// 广播调用（等待所有节点响应）
//	result, err := rpc.BroadcastCall(ctx, peers, request, strategy, nil)
//
//	// 批量写入（单向，不等待响应）
//	err := rpc.WriteV(ctx, targets, messages, nil)
//
// ## 2. RPCAsync - 异步调用
//
// 返回 AsyncOperation[T]，支持链式回调和超时设置:
//
//	// 基础用法
//	op := rpc.CallAsync(ctx, peerID, request)
//	result, err := op.Await(ctx)
//
//	// 链式回调
//	op.OnSuccess(func(resp ResponseMsg) {
//	    fmt.Println("成功:", resp)
//	}).OnError(func(err error) {
//	    fmt.Println("失败:", err)
//	})
//
//	// 链式超时
//	result, err := rpc.CallAsync(ctx, peerID, request).
//	    WithTimeout(5 * time.Second).
//	    Await(ctx)
//
// # BroadcastProgress - 广播进度追踪
//
// BroadcastProgress 用于追踪广播操作的进度，支持同步和异步两种使用方式:
//
//	// 方式1: 同步等待（与 RPCSync 配合）
//	tracker := rpc.NewBroadcastProgress("task-001", peers)
//	rpc.BroadcastCall(ctx, peers, req, ResponseMajority, tracker)
//	stats, _ := tracker.WaitFull(ctx) // 等待全部完成
//
//	// 方式2: 回调监听（与 RPCAsync 配合）
//	tracker := rpc.NewBroadcastProgress("task-001", peers).
//	    OnSuccess(func(peer model.PeerID, resp model.Message, stats BroadcastStats) {
//	        fmt.Printf("节点 %s 成功响应\n", peer)
//	    }).
//	    OnMajority(func(stats BroadcastStats) {
//	        fmt.Println("已达成多数派")
//	    }).
//	    OnComplete(func(stats BroadcastStats) {
//	        fmt.Println("全部完成:", stats)
//	    }).
//	    Build()
//
//	rpc.BroadcastAsync(ctx, peers, req, WithBroadcastProgress(tracker))
//
// # BroadcastListener - 广播事件监听
//
// BroadcastListener 接口用于监听广播进度事件:
//
//	type MyListener struct{}
//
//	func (l *MyListener) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
//	    // 每次成功响应时调用
//	}
//
//	func (l *MyListener) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
//	    // 每次失败响应时调用
//	}
//
//	func (l *MyListener) OnMajority(stats BroadcastStats) {
//	    // 达成多数派时调用（仅一次）
//	}
//
//	func (l *MyListener) OnComplete(stats BroadcastStats) {
//	    // 全部完成时调用（仅一次）
//	}
//
//	// 使用 NoOpListener 作为基类可以只实现感兴趣的方法
//	type PartialListener struct {
//	    *service.NoOpListener
//	}
//
//	func (l *PartialListener) OnMajority(stats BroadcastStats) {
//	    // 只关注多数派达成事件
//	}
//
// # DiscoveryService - 节点发现
//
// DiscoveryService 负责发现和管理集群节点:
//
//	// 创建发现服务（具体实现由 infrastructure 提供）
//	discovery := NewMDNSDiscovery() // 或 NewDHTDiscovery()
//
//	// 设置节点事件回调
//	discovery.SetNotifee(&MyNotifee{})
//
//	// 启动发现服务
//	ctx := context.Background()
//	if err := discovery.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer discovery.Stop()
//
//	// MyNotifee 实现 DiscoveryNotifee 接口
//	type MyNotifee struct{}
//
//	func (n *MyNotifee) HandlePeerFound(peerID model.PeerID, addrs []model.NetworkAddress) {
//	    fmt.Printf("发现新节点: %s, 地址: %v\n", peerID, addrs)
//	}
//
//	func (n *MyNotifee) HandlePeerUpdated(peerID model.PeerID, addrs []model.NetworkAddress) {
//	    fmt.Printf("节点地址更新: %s, 新地址: %v\n", peerID, addrs)
//	}
//
//	func (n *MyNotifee) HandlePeerSuspected(peerID model.PeerID, reason string) {
//	    fmt.Printf("节点疑似失效: %s, 原因: %s\n", peerID, reason)
//	}
//
//	func (n *MyNotifee) HandlePeerLost(peerID model.PeerID) {
//	    fmt.Printf("节点丢失: %s\n", peerID)
//	}
//
// # TaskPoolProvider - 任务池管理
//
// TaskPoolProvider 用于统一管理异步任务的执行:
//
//	// 创建任务池
//	pool, err := ants.NewPool(1000)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	provider := NewAntsTaskPoolProvider(pool)
//	defer provider.Close()
//
//	// 设置到 RPCAsync 适配器
//	adapter.SetTaskPoolProvider(provider)
//
// # 完整示例
//
//	package main
//
//	import (
//	    "context"
//	    "fmt"
//	    "time"
//
//	    "github.com/jzhang405/NexKV/internal/domain/model"
//	    "github.com/jzhang405/NexKV/internal/domain/service"
//	    "github.com/jzhang405/NexKV/internal/infrastructure/rpc"
//	)
//
//	func main() {
//	    // 1. 创建同步 RPC 实例（实际由 infrastructure 提供）
//	    rpcSync := createRPCSync()
//
//	    // 2. 创建异步适配器
//	    config := &service.RPCAsyncConfig{
//	        DefaultTimeoutMs: 30000,
//	    }
//	    rpcAsync := rpc.NewRPCAsyncAdapter(rpcSync, config)
//
//	    // 3. 使用广播进度追踪
//	    peers := []model.PeerID{"peer-1", "peer-2", "peer-3"}
//	    tracker := rpc.NewProgress("broadcast-001", peers).
//	        OnSuccess(func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
//	            fmt.Printf("✓ %s 成功\n", peer)
//	        }).
//	        OnFailure(func(peer model.PeerID, err error, stats service.BroadcastStats) {
//	            fmt.Printf("✗ %s 失败: %v\n", peer, err)
//	        }).
//	        OnMajority(func(stats service.BroadcastStats) {
//	            fmt.Printf("多数派达成! (%d/%d)\n", stats.SuccessCount, stats.Total)
//	        }).
//	        OnComplete(func(stats service.BroadcastStats) {
//	            fmt.Printf("广播完成: 成功 %d, 失败 %d, 待处理 %d\n",
//	                stats.SuccessCount, stats.FailedCount, stats.PendingCount)
//	        }).
//	        Build()
//
//	    // 4. 执行异步广播
//	    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
//	    defer cancel()
//
//	    req := model.NewMessage("test-payload")
//	    op := rpcAsync.BroadcastAsync(ctx, peers, req)
//	    result, err := op.Await(ctx)
//
//	    if err != nil {
//	        fmt.Printf("广播失败: %v\n", err)
//	        return
//	    }
//
//	    fmt.Printf("结果: %+v\n", result)
//	}
package service
