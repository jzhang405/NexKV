// Package btree PageDispatcher — batch write dispatcher with per-page serialization.
package btree

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// WorkerPool
// ==========================================

// WorkerPool 常驻 worker goroutine 池，消费 per-page 写入任务。
type WorkerPool struct {
	taskCh chan *pageBatch
	done   chan struct{} // closed on Shutdown, prevents TOCTOU send on closed taskCh
	wg     sync.WaitGroup
	closed atomic.Bool
}

// NewWorkerPool 创建 n 个常驻 worker goroutine。
func NewWorkerPool(n int) *WorkerPool {
	wp := &WorkerPool{
		taskCh: make(chan *pageBatch, n*4),
		done:   make(chan struct{}),
	}
	for range n {
		go wp.runWorker()
	}
	return wp
}

func (wp *WorkerPool) runWorker() {
	for batch := range wp.taskCh {
		wp.executeBatch(batch)
	}
}

// executeBatch 串行执行同一 Page 的所有写入。
// 借鉴 Lealone：CAS 失败 3 次后重新入队而非自旋。
func (wp *WorkerPool) executeBatch(batch *pageBatch) {
	defer wp.wg.Done()

	var panicked any
	defer func() {
		if r := recover(); r != nil {
			panicked = r
		}
		if panicked != nil {
			for i := batch.nextIdx; i < len(batch.tasks); i++ {
				batch.results[i] = WriteResult{
					Index: batch.tasks[i].idx,
					Err:   errpkg.Wrap(ErrWorkerPoolPanic, fmt.Sprintf("worker panic: %v", panicked)),
				}
			}
		}
	}()

	for i := batch.nextIdx; i < len(batch.tasks); i++ {
		t := &batch.tasks[i]
		err := batch.tree.SetWithRetry(batch.ctx, t.key, t.value, maxCASFastAttempts)

		if err != nil && isCASRetryExhausted(err) && batch.retries < maxCASRequeue {
			batch.nextIdx = i
			batch.retries++
			if wp.Submit(batch) != nil {
				for j := i; j < len(batch.tasks); j++ {
					batch.results[j] = WriteResult{Index: batch.tasks[j].idx, Err: ErrWorkerPoolClosed}
				}
				return
			}
			return
		}

		batch.results[i] = WriteResult{Index: t.idx, Err: err}
	}
}

// Submit 提交 pageBatch。Shutdown 后返回 ErrWorkerPoolClosed。
// 使用 select+done channel 消除 TOCTOU：closed.Load 和 taskCh<-batch 之间的窗口。
func (wp *WorkerPool) Submit(batch *pageBatch) error {
	if wp.closed.Load() {
		return ErrWorkerPoolClosed
	}
	wp.wg.Add(1)
	select {
	case <-wp.done:
		wp.wg.Add(-1)
		return ErrWorkerPoolClosed
	case wp.taskCh <- batch:
		return nil
	}
}

// Wait 等待所有已提交任务完成。
func (wp *WorkerPool) Wait() { wp.wg.Wait() }

// Shutdown 优雅关闭：先标记 closed，再 close done channel（解除 Submit 阻塞），最后 close taskCh。
func (wp *WorkerPool) Shutdown() {
	wp.closed.Store(true)
	close(wp.done)
	close(wp.taskCh)
}

// ==========================================
// PageDispatcher
// ==========================================

const (
	numShards          = 64
	maxCASFastAttempts = 3
	maxCASRequeue      = 3
)

// Sentinel errors.
var (
	ErrWorkerPoolClosed = errors.New("btree: worker pool closed")
	ErrWorkerPoolPanic  = errors.New("btree: worker panic recovered")
)

// KeyToShard 使用 FNV-1a hash 将 key 映射到固定数量的分片。
func KeyToShard(key []byte) int {
	h := fnv.New32a()
	h.Write(key)
	return int(h.Sum32() % numShards)
}

type keyWithIndex struct {
	key []byte
	idx int
}

// pageBatch 单个 Page 的批量写入任务。
type pageBatch struct {
	ctx     context.Context
	tree    *BTree
	pageID  model.PageID
	tasks   []writeTask
	results []WriteResult
	nextIdx int
	retries int
}

type writeTask struct {
	idx   int
	key   []byte
	value []byte
}

// WriteResult 单个写入的结果。
type WriteResult struct {
	Index int
	Err   error
}

// PageDispatcher 按 Page 分片调度写入。
type PageDispatcher struct {
	tree *BTree
	pool *WorkerPool
}

// NewPageDispatcher 创建 PageDispatcher。
func NewPageDispatcher(tree *BTree) *PageDispatcher {
	workers := min(runtime.GOMAXPROCS(0), numShards/2)
	return &PageDispatcher{
		tree: tree,
		pool: NewWorkerPool(workers),
	}
}

// Shutdown 关闭调度器。
func (pd *PageDispatcher) Shutdown() { pd.pool.Shutdown() }

// Dispatch 批量写入主流程。
func (pd *PageDispatcher) Dispatch(ctx context.Context, keys, values [][]byte) ([]WriteResult, error) {
	n := len(keys)
	if n == 0 {
		return nil, nil
	}

	// Phase 1: Hash 分桶
	shards := make([][]keyWithIndex, numShards)
	for i, key := range keys {
		s := KeyToShard(key)
		shards[s] = append(shards[s], keyWithIndex{key: key, idx: i})
	}

	// Phase 2: 桶间并行 resolvePageIDs
	var wg sync.WaitGroup
	shardResults := make([]map[model.PageID][]writeTask, numShards)
	shardErr := make([]error, numShards)

	for s := range numShards {
		if len(shards[s]) == 0 {
			continue
		}
		wg.Add(1)
		go func(shardIdx int) {
			defer wg.Done()
			shardResults[shardIdx], shardErr[shardIdx] = pd.resolveShardPageIDs(ctx, shards[shardIdx], values)
		}(s)
	}
	wg.Wait()

	for _, err := range shardErr {
		if err != nil {
			return nil, err
		}
	}

	// Phase 3: 跨桶 merge
	pageGroups := make(map[model.PageID][]writeTask)
	for _, sr := range shardResults {
		for pid, tasks := range sr {
			pageGroups[pid] = append(pageGroups[pid], tasks...)
		}
	}

	if len(pageGroups) == 0 {
		return nil, nil
	}

	// Phase 4: 提交到 WorkerPool（保留 batch 引用用于收集结果）
	batches := make([]*pageBatch, 0, len(pageGroups))
	for pid, tasks := range pageGroups {
		batch := &pageBatch{
			ctx:     ctx,
			tree:    pd.tree,
			pageID:  pid,
			tasks:   tasks,
			results: make([]WriteResult, len(tasks)),
		}
		if err := pd.pool.Submit(batch); err != nil {
			for _, t := range tasks {
				batch.results[t.idx] = WriteResult{Index: t.idx, Err: err}
			}
		}
		batches = append(batches, batch)
	}

	// Phase 5: 等待完成
	pd.pool.Wait()

	// Phase 6: 收集结果
	results := make([]WriteResult, n)
	for _, batch := range batches {
		for i, t := range batch.tasks {
			results[t.idx] = batch.results[i]
		}
	}

	return results, nil
}

// resolveShardPageIDs 对单个 shard 内的 key 排序后批量解析 PageID。
func (pd *PageDispatcher) resolveShardPageIDs(ctx context.Context, keys []keyWithIndex, values [][]byte) (map[model.PageID][]writeTask, error) {
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i].key) < string(keys[j].key)
	})

	result := make(map[model.PageID][]writeTask)
	var lastPage model.PageID
	for _, k := range keys {
		var pid model.PageID
		if lastPage != 0 && pd.tree.inSamePage(lastPage, k.key) {
			pid = lastPage
		} else {
			var err error
			pid, err = pd.tree.ResolvePageID(ctx, k.key)
			if err != nil {
				return nil, err
			}
			lastPage = pid
		}
		result[pid] = append(result[pid], writeTask{idx: k.idx, key: k.key, value: values[k.idx]})
	}
	return result, nil
}

// isCASRetryExhausted checks if the error indicates CAS retries were exhausted.
func isCASRetryExhausted(err error) bool {
	return errors.Is(err, ErrCASRetryExhausted)
}
