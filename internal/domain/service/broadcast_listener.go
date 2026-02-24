// Package service 定义领域服务接口
package service

import (
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ============================================================================
// BroadcastProgress Listener 机制（v1.5）
// ============================================================================

// BroadcastListener 广播进度监听接口
//
// 监听器执行顺序（针对每个响应）：
//  1. OnSuccess / OnFailure（每次响应）
//     ↓
//  2. OnMajority（达到多数派时，仅一次）
//     ↓
//  3. OnComplete（全部完成时，仅一次）
//
// 特殊场景：OnMajority 和 OnComplete 可能在同一次 RecordSuccess 中
// 顺序触发（如果 Majority 达成时恰好也是最后一个响应）
//
// 监听器实现注意事项：
// 1. 监听器在锁外执行，但应避免调用 BroadcastProgress 的方法（防止死锁）
// 2. 监听器应快速返回（< 10ms），长时间处理应启动 goroutine
// 3. 监听器可能被并发调用（OnSuccess/OnFailure），实现需线程安全
// 4. 不要依赖监听器的调用顺序（除文档明确保证的之外）
type BroadcastListener interface {
	// OnSuccess 每次收到成功响应时调用
	// 参数说明：
	//   - peer: 响应节点 ID
	//   - resp: 成功响应消息（不会为 nil）
	//   - stats: 当前统计信息
	OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats)

	// OnFailure 每次收到失败响应时调用
	// 参数说明：
	//   - peer: 失败节点 ID
	//   - err: 错误信息（不会为 nil，包含具体错误类型）
	//          - 超时错误：context.DeadlineExceeded
	//          - 网络错误：net.Error
	//          - 业务错误：业务逻辑返回的错误
	//   - stats: 当前统计信息
	OnFailure(peer model.PeerID, err error, stats BroadcastStats)

	// OnMajority 达到多数派时调用（仅调用一次）
	// 触发条件：
	//   - 成功响应数 >= majority（len(targets)/2 + 1）
	//   - 只在 RecordSuccess 时检查，RecordFailure 不会触发
	//   - 例如：3 个节点，2 个成功即触发（即使 1 个失败）
	// 参数说明：
	//   - stats: 达到多数派时的统计信息
	OnMajority(stats BroadcastStats)

	// OnComplete 全部完成时调用（仅调用一次）
	// 触发条件：
	//   - 成功数 + 失败数 == 总节点数
	// 参数说明：
	//   - stats: 全部完成时的统计信息
	OnComplete(stats BroadcastStats)
}

// BroadcastStats 广播统计信息
type BroadcastStats struct {
	TaskID            string        // 任务 ID
	Total             int           // 总节点数
	Success           int           // 成功数
	Failed            int           // 失败数
	Pending           int           // 待响应数
	SuccessRate       float64       // 成功率
	ElapsedTime       time.Duration // 已耗时（从任务开始到现在）
	FirstResponseTime time.Duration // 首个响应耗时（从任务开始到首个响应）
	MajorityReachTime time.Duration // 达到多数派耗时（从任务开始到多数派达成）
}

// NoOpListener 空实现的 BroadcastListener
// 可用于嵌入到自定义 Callback 中，只重写需要的方法
//
// 使用示例:
//
//	type MyCallback struct {
//	    NoOpListener // 嵌入所有空实现
//	}
//
//	// 只重写关心的方法
//	func (m *MyCallback) OnComplete(stats BroadcastStats) {
//	    fmt.Printf("广播完成！成功率: %.2f%%\n", stats.SuccessRate*100)
//	}
type NoOpListener struct{}

// OnSuccess 空实现
func (n NoOpListener) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {}

// OnFailure 空实现
func (n NoOpListener) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {}

// OnMajority 空实现
func (n NoOpListener) OnMajority(stats BroadcastStats) {}

// OnComplete 空实现
func (n NoOpListener) OnComplete(stats BroadcastStats) {}
