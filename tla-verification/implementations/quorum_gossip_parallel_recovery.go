package implementations

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// RecoveryCoordinator 并行恢复协调器
// 核心功能：分析节点依赖关系，按批次并行恢复节点，避免死锁
type RecoveryCoordinator struct {
	cluster *Cluster
	batches [][]*Node // 恢复批次：每批节点可以并行恢复
}

// NewRecoveryCoordinator 创建恢复协调器
func NewRecoveryCoordinator(cluster *Cluster) *RecoveryCoordinator {
	return &RecoveryCoordinator{
		cluster: cluster,
		batches: make([][]*Node, 0),
	}
}

// AnalyzeDependencies 分析节点依赖关系，构建恢复批次
//
// 核心算法：
// 1. 识别活跃节点（未崩溃）和崩溃节点
// 2. 构建依赖图：崩溃节点需要从活跃节点同步数据
// 3. 按拓扑排序分配批次
//
// 批次分配规则：
// - Batch 0: 所有活跃节点（可并行恢复）
// - Batch 1+: 崩溃节点（按依赖关系分层）
func (rc *RecoveryCoordinator) AnalyzeDependencies() {
	// 1. 分类节点：活跃节点 vs 崩溃节点
	var activeNodes []*Node
	var crashedNodes []*Node

	for _, node := range rc.cluster.Nodes {
		node.mu.RLock()
		isCrashed := node.IsCrashed
		node.mu.RUnlock()

		if isCrashed {
			crashedNodes = append(crashedNodes, node)
		} else {
			activeNodes = append(activeNodes, node)
		}
	}

	// 2. 构建批次
	rc.batches = make([][]*Node, 0)

	// Batch 0: 活跃节点（如果有）
	if len(activeNodes) > 0 {
		rc.batches = append(rc.batches, activeNodes)
		log.Printf("[RecoveryCoordinator] Batch 0: %d active nodes", len(activeNodes))
	}

	// Batch 1+: 崩溃节点
	// 策略：所有崩溃节点放在同一批次，因为它们都依赖活跃节点
	// 活跃节点恢复完成后，所有崩溃节点可以并行恢复
	if len(crashedNodes) > 0 {
		rc.batches = append(rc.batches, crashedNodes)
		log.Printf("[RecoveryCoordinator] Batch 1: %d crashed nodes", len(crashedNodes))
	}

	log.Printf("[RecoveryCoordinator] Total batches: %d", len(rc.batches))
}

// ParallelRecover 并行恢复所有节点
//
// 执行流程：
// 1. 按批次顺序恢复
// 2. 同一批次内的节点并行恢复（使用 goroutine）
// 3. 批次间使用 WaitGroup 同步
// 4. 每个批次完成后，等待 100ms 让增量同步完成
//
// 时间复杂度：
// - 顺序恢复：O(N * t) 其中 N 是节点数，t 是单节点恢复时间
// - 并行恢复：O(B * t) 其中 B 是批次数，t 是单节点恢复时间
// - 加速比：N / B（理想情况下）
func (rc *RecoveryCoordinator) ParallelRecover() error {
	if len(rc.batches) == 0 {
		rc.AnalyzeDependencies()
	}

	if len(rc.batches) == 0 {
		return fmt.Errorf("no nodes to recover")
	}

	startTime := time.Now()

	// 按批次顺序恢复
	for batchIdx, batch := range rc.batches {
		batchStart := time.Now()
		log.Printf("[RecoveryCoordinator] Starting batch %d with %d nodes", batchIdx, len(batch))

		// 并行恢复当前批次的所有节点
		var wg sync.WaitGroup
		recoverErrors := make(chan error, len(batch))

		for _, node := range batch {
			wg.Add(1)
			go func(n *Node) {
				defer wg.Done()

				// 检查节点是否需要恢复
				n.mu.RLock()
				isCrashed := n.IsCrashed
				n.mu.RUnlock()

				if !isCrashed {
					log.Printf("[RecoveryCoordinator] Node %s is already active, skipping", n.ID)
					return
				}

				// 执行恢复
				log.Printf("[RecoveryCoordinator] Recovering node %s (batch %d)", n.ID, batchIdx)
				if err := n.Recover(rc.cluster); err != nil {
					log.Printf("[RecoveryCoordinator] ERROR: Failed to recover node %s: %v", n.ID, err)
					recoverErrors <- fmt.Errorf("node %s: %w", n.ID, err)
				} else {
					log.Printf("[RecoveryCoordinator] Successfully recovered node %s", n.ID)
				}
			}(node)
		}

		// 等待当前批次完成
		wg.Wait()
		close(recoverErrors)

		// 检查错误
		var errs []error
		for err := range recoverErrors {
			errs = append(errs, err)
		}

		if len(errs) > 0 {
			return fmt.Errorf("batch %d had %d errors: %v", batchIdx, len(errs), errs)
		}

		batchDuration := time.Since(batchStart)
		log.Printf("[RecoveryCoordinator] Batch %d completed in %v", batchIdx, batchDuration)

		// 如果不是最后一批，等待增量同步完成
		if batchIdx < len(rc.batches)-1 {
			log.Printf("[RecoveryCoordinator] Waiting for incremental sync to complete...")
			time.Sleep(100 * time.Millisecond)
		}
	}

	totalDuration := time.Since(startTime)
	log.Printf("[RecoveryCoordinator] All batches completed in %v (batches: %d)", totalDuration, len(rc.batches))

	return nil
}

// GetBatchCount 获取批次数
func (rc *RecoveryCoordinator) GetBatchCount() int {
	return len(rc.batches)
}

// GetBatchNodeCount 获取指定批次的节点数
func (rc *RecoveryCoordinator) GetBatchNodeCount(batchIdx int) int {
	if batchIdx < 0 || batchIdx >= len(rc.batches) {
		return 0
	}
	return len(rc.batches[batchIdx])
}
