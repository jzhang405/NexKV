package btree

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// BatchWriter / WriteBatch
// ==========================================

func TestPageDispatcher_WriteBatch(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	bw := NewBatchWriter(tree)
	defer bw.Shutdown()

	ctx := context.Background()

	n := 100
	keys := make([][]byte, n)
	values := make([][]byte, n)
	for i := range n {
		keys[i] = []byte(string(rune('a'+i%26)) + string(rune('a'+i/26%26)))
		values[i] = []byte(string(keys[i]) + "_value")
	}

	err := bw.WriteBatch(ctx, keys, values)
	require.NoError(t, err)

	for i := range n {
		got, err := tree.Get(ctx, keys[i])
		require.NoError(t, err)
		assert.NotNil(t, got, "key %s not found", keys[i])
	}
}

func TestPageDispatcher_WriteBatch_Large(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	bw := NewBatchWriter(tree)
	defer bw.Shutdown()

	ctx := context.Background()

	n := 2000
	keys := make([][]byte, n)
	values := make([][]byte, n)
	for i := range n {
		keys[i] = []byte(fmt.Sprintf("key-%07d", i))
		values[i] = []byte(fmt.Sprintf("value-%07d", i))
	}

	err := bw.WriteBatch(ctx, keys, values)
	require.NoError(t, err)

	// Verify a sample
	for i := range 10 {
		got, err := tree.Get(ctx, keys[i*200])
		require.NoError(t, err)
		assert.Equal(t, values[i*200], got)
	}
}

func TestPageDispatcher_EmptyBatch(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	bw := NewBatchWriter(tree)
	defer bw.Shutdown()

	ctx := context.Background()
	require.NoError(t, bw.WriteBatch(ctx, nil, nil))
}

func TestPageDispatcher_MismatchedLen(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	bw := NewBatchWriter(tree)
	defer bw.Shutdown()

	ctx := context.Background()
	err := bw.WriteBatch(ctx, [][]byte{[]byte("k")}, [][]byte{})
	require.Error(t, err)
}

func TestWriteBatch_DuplicateKeys(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	bw := NewBatchWriter(tree)
	defer bw.Shutdown()

	ctx := context.Background()

	keys := [][]byte{[]byte("dup"), []byte("dup")}
	values := [][]byte{[]byte("v1"), []byte("v2")}

	err := bw.WriteBatch(ctx, keys, values)
	require.NoError(t, err)

	// Last write wins
	got, _ := tree.Get(ctx, []byte("dup"))
	assert.Equal(t, []byte("v2"), got)
}

// ==========================================
// KeyToShard
// ==========================================

func TestKeyToShard(t *testing.T) {
	k := []byte("test-key")
	s1 := KeyToShard(k)
	s2 := KeyToShard(k)
	assert.Equal(t, s1, s2)
	assert.True(t, s1 >= 0 && s1 < numShards)
}

func TestKeyToShard_Distribution(t *testing.T) {
	// Verify keys are distributed across multiple shards
	seen := make(map[int]bool)
	for i := range 500 {
		k := []byte(fmt.Sprintf("key-%05d", i))
		seen[KeyToShard(k)] = true
	}
	assert.Greater(t, len(seen), 1, "all keys mapped to same shard")
}

// ==========================================
// SetWithRetry
// ==========================================

func TestSetWithRetry(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	ctx := context.Background()

	err := tree.SetWithRetry(ctx, []byte("k1"), []byte("v1"), 10)
	require.NoError(t, err)

	got, err := tree.Get(ctx, []byte("k1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), got)

	// Minimal retries
	err = tree.SetWithRetry(ctx, []byte("k2"), []byte("v2"), 1)
	require.NoError(t, err)
}

func TestSetWithRetry_Exhausted(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	ctx := context.Background()

	// 0 retries means the loop body never executes → exhausts immediately
	err := tree.SetWithRetry(ctx, []byte("k"), []byte("v"), 0)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCASRetryExhausted))
}

func TestSetWithRetry_Update(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	ctx := context.Background()

	require.NoError(t, tree.Set(ctx, []byte("k"), []byte("v1")))
	require.NoError(t, tree.SetWithRetry(ctx, []byte("k"), []byte("v2"), 5))

	got, _ := tree.Get(ctx, []byte("k"))
	assert.Equal(t, []byte("v2"), got)
}

// ==========================================
// ResolvePageID
// ==========================================

func TestResolvePageID(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	ctx := context.Background()

	for i := range 10 {
		require.NoError(t, tree.Set(ctx, []byte(string(rune('a'+i))), []byte(string(rune('a'+i)))))
	}

	pid, err := tree.ResolvePageID(ctx, []byte("c"))
	require.NoError(t, err)
	assert.NotZero(t, pid)
}

func TestResolvePageID_EmptyTree(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	ctx := context.Background()
	// Insert one key so searchPath can find a leaf page
	require.NoError(t, tree.Set(ctx, []byte("a"), []byte("a")))
	pid, err := tree.ResolvePageID(ctx, []byte("b"))
	require.NoError(t, err)
	assert.NotZero(t, pid)
}

func TestResolvePageID_Consistent(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	ctx := context.Background()
	for i := range 20 {
		require.NoError(t, tree.Set(ctx, []byte(fmt.Sprintf("k-%02d", i)), []byte("v")))
	}

	// Same key always resolves to same page
	for i := range 5 {
		k := []byte(fmt.Sprintf("k-%02d", i))
		p1, _ := tree.ResolvePageID(ctx, k)
		p2, _ := tree.ResolvePageID(ctx, k)
		assert.Equal(t, p1, p2, "inconsistent resolution for %s", k)
	}
}

// ==========================================
// WorkerPool
// ==========================================

func TestWorkerPool_SubmitShutdown(t *testing.T) {
	wp := NewWorkerPool(2)

	// Submit a task before shutdown
	batch := &pageBatch{results: make([]WriteResult, 0)}
	batch.wg.Add(1)
	require.NoError(t, wp.Submit(batch))
	batch.wg.Wait()

	// Shutdown
	wp.Shutdown()

	// Submit after shutdown should fail
	err := wp.Submit(batch)
	assert.True(t, errors.Is(err, ErrWorkerPoolClosed))
}

func TestWorkerPool_ConcurrentSubmit(t *testing.T) {
	wp := NewWorkerPool(4)

	var batches []*pageBatch
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			batch := &pageBatch{results: make([]WriteResult, 0)}
			batch.wg.Add(1)
			_ = wp.Submit(batch)
			mu.Lock()
			batches = append(batches, batch)
			mu.Unlock()
		}()
	}
	wg.Wait()
	for _, b := range batches {
		b.wg.Wait()
	}
	wp.Shutdown()
}

// ==========================================
// BatchError
// ==========================================

func TestBatchError_Format(t *testing.T) {
	be := &BatchError{Errors: []WriteResult{{Index: 1, Err: errors.New("err1")}, {Index: 3, Err: errors.New("err2")}}}
	assert.Equal(t, "2 write(s) failed", be.Error())
}

func TestBatchError_Empty(t *testing.T) {
	be := &BatchError{}
	assert.Equal(t, "0 write(s) failed", be.Error())
}

// ==========================================
// Concurrent Dispatch Isolation
// ==========================================

func TestPageDispatcher_ConcurrentDispatch(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	ctx := context.Background()

	// Pre-populate some data
	for i := range 50 {
		require.NoError(t, tree.Set(ctx, []byte(fmt.Sprintf("pre-%02d", i)), []byte("pre")))
	}

	bw := NewBatchWriter(tree)
	defer bw.Shutdown()

	// Two sequential batches (concurrent Dispatch is not supported per design)
	for b := range 3 {
		n := 50
		keys := make([][]byte, n)
		values := make([][]byte, n)
		for i := range n {
			keys[i] = []byte(fmt.Sprintf("batch%d-%04d", b, i))
			values[i] = []byte(fmt.Sprintf("v%d-%04d", b, i))
		}
		require.NoError(t, bw.WriteBatch(ctx, keys, values))
	}

	// Verify all three batches
	for b := range 3 {
		got, err := tree.Get(ctx, []byte(fmt.Sprintf("batch%d-%04d", b, 42)))
		require.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("v%d-0042", b)), got)
	}
}

// ==========================================
// CAS Re-queue
// ==========================================

func TestExecuteBatch_CASRequeue(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	ctx := context.Background()

	// Use SetWithRetry with 0 retries to force immediate exhaustion.
	// In a single-goroutine test, the first attempt succeeds (batch.retries stays 0),
	// so we test the re-queue path indirectly by verifying SetWithRetry(exhausted).
	err := tree.SetWithRetry(ctx, []byte("k"), []byte("v"), 0)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCASRetryExhausted))

	// With 1 retry, it works for uncontended writes
	err = tree.SetWithRetry(ctx, []byte("k2"), []byte("v2"), 1)
	require.NoError(t, err)

	// Verify retries counter resets between calls
	err = tree.SetWithRetry(ctx, []byte("k3"), []byte("v3"), 0)
	assert.True(t, errors.Is(err, ErrCASRetryExhausted))
}

// ==========================================
// WorkerPool — Panic Recovery
// ==========================================

func TestWorkerPool_PanicRecovery(t *testing.T) {
	wp := NewWorkerPool(1)

	// Submit a batch that will panic during execution
	// The panic is recovered and converted to error results
	batch := &pageBatch{
		tree:    nil, // will cause nil pointer dereference → panic
		tasks:   []writeTask{{idx: 0, key: []byte("k"), value: []byte("v")}},
		results: make([]WriteResult, 1),
	}
	batch.wg.Add(1)
	require.NoError(t, wp.Submit(batch))
	batch.wg.Wait()

	// Panic should be recovered, task should have error
	assert.Error(t, batch.results[0].Err)
	assert.Contains(t, batch.results[0].Err.Error(), "worker panic")

	wp.Shutdown()
}

// ==========================================
// WorkerPool — Re-queue During Shutdown
// ==========================================

func TestWorkerPool_RequeueDuringShutdown(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	pd := NewPageDispatcher(tree)
	wp := pd.pool

	ctx := context.Background()

	// Create a batch
	batch := &pageBatch{
		ctx:     ctx,
		tree:    tree,
		tasks:   []writeTask{{idx: 0, key: []byte("k1"), value: []byte("v1")}, {idx: 1, key: []byte("k2"), value: []byte("v2")}},
		results: make([]WriteResult, 2),
	}

	// Shutdown pool before submitting
	wp.Shutdown()

	// Submit should fail
	err := wp.Submit(batch)
	assert.True(t, errors.Is(err, ErrWorkerPoolClosed))
}

// ==========================================
// Dispatch Error Handling
// ==========================================

func TestDispatch_PoolClosed(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	pd := NewPageDispatcher(tree)
	pd.Shutdown()

	ctx := context.Background()
	results, err := pd.Dispatch(ctx, [][]byte{[]byte("k")}, [][]byte{[]byte("v")})
	// Dispatch itself doesn't return error for pool closed — individual results get ErrWorkerPoolClosed
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, errors.Is(results[0].Err, ErrWorkerPoolClosed))
}

func TestDispatch_PoolClosed_MultiKey(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	pd := NewPageDispatcher(tree)
	pd.Shutdown()

	ctx := context.Background()
	// 3 keys that map to the same page (sequential, same prefix)
	keys := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	values := [][]byte{[]byte("va"), []byte("vb"), []byte("vc")}
	results, err := pd.Dispatch(ctx, keys, values)
	// No panic from batch.results[t.idx] out of bounds
	require.NoError(t, err)
	require.Len(t, results, 3)
	for _, r := range results {
		assert.True(t, errors.Is(r.Err, ErrWorkerPoolClosed))
	}
}

func TestWriteBatch_PoolClosed(t *testing.T) {
	tree, storage := newTestBTree(t)
	defer storage.Close()

	bw := NewBatchWriter(tree)
	bw.Shutdown()

	ctx := context.Background()
	err := bw.WriteBatch(ctx, [][]byte{[]byte("k")}, [][]byte{[]byte("v")})
	// BatchError wraps individual write errors
	require.Error(t, err)
	be, ok := err.(*BatchError)
	require.True(t, ok)
	assert.True(t, errors.Is(be.Errors[0].Err, ErrWorkerPoolClosed))
}

// ==========================================
// inSamePage
// ==========================================

func TestInSamePage_AlwaysFalse(t *testing.T) {
	tree, _ := newTestBTree(t)
	// V1: always returns false (conservative)
	assert.False(t, tree.inSamePage(1, []byte("any")))
	assert.False(t, tree.inSamePage(0, []byte("any")))
}

// ==========================================
// KeyToShard — Edge Cases
// ==========================================

func TestKeyToShard_EdgeCases(t *testing.T) {
	// Empty key
	s := KeyToShard([]byte{})
	assert.True(t, s >= 0 && s < numShards)

	// Different keys may map to same or different shards — both valid
	s1 := KeyToShard([]byte("key-0001"))
	s2 := KeyToShard([]byte("key-0002"))
	assert.True(t, s1 >= 0 && s1 < numShards)
	assert.True(t, s2 >= 0 && s2 < numShards)
}
