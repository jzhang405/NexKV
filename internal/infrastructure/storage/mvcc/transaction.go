// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"context"
	"encoding/binary"
	"fmt"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ---------------------------------------------------------------------------
// StorageBackend — raw KV storage backend (no transaction, no MVCC)
// ---------------------------------------------------------------------------

// StorageBackend defines the minimal KV operations needed by the transaction engine.
// GetRaw must return a Go-heap independent copy (not mmap reference).
type StorageBackend interface {
	// GetRaw returns the full MVCC-encoded value ([Flag][beginTS][RealValue]) as a Go-heap copy.
	// Returns ErrKeyNotFound only if the key does not physically exist in the tree.
	GetRaw(ctx context.Context, key []byte) ([]byte, error)
	Set(ctx context.Context, key, value []byte) error
	Delete(ctx context.Context, key []byte) error

	// SetBatch applies multiple key-value pairs in a single COW+writeOperation pass.
	// All keys are applied to one COW copy of the leaf page, then CAS-published once.
	// Keys must already be sorted (caller responsibility) to avoid redundant binary searches.
	// Returns the count of operations applied and any error.
	SetBatch(ctx context.Context, pairs []KVPair) (int, error)

	// GetBatchRaw returns raw MVCC-encoded values for multiple keys in a single
	// searchPath+epoch window. Missing keys return nil at that index.
	// Each returned slice is a Go-heap independent copy.
	GetBatchRaw(ctx context.Context, keys [][]byte) ([][]byte, error)
}

// KVPair is a key-value pair for batched writes.
type KVPair struct {
	Key   []byte
	Value []byte
}

// ---------------------------------------------------------------------------
// Isolation levels
// ---------------------------------------------------------------------------

// IsolationLevel defines the transaction isolation level.
type IsolationLevel uint8

const (
	ReadCommitted     IsolationLevel = iota
	SnapshotIsolation                // default for NexKV
)

// ---------------------------------------------------------------------------
// Tx — single transaction session
// ---------------------------------------------------------------------------

// Tx is the per-transaction interface. Not thread-safe; caller must serialize calls.
type Tx interface {
	Get(ctx context.Context, key []byte) ([]byte, error)
	GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error)
	Put(key, value []byte) error
	Delete(key []byte) error
	Commit(ctx context.Context) error
	Rollback() error
	SnapshotTS() uint64
}

// ---------------------------------------------------------------------------
// TxManager — transaction manager
// ---------------------------------------------------------------------------

// TxManager creates and manages transactions.
type TxManager interface {
	BeginTx(ctx context.Context, level IsolationLevel) (Tx, error)
	// BeginPessimisticTx creates a transaction with pessimistic locking (Phase 1: Lealone RowLock equiv).
	// Put/Delete acquire per-key KeyLock eagerly; Commit skips PreCheck and GetRaw in commitKey.
	BeginPessimisticTx(ctx context.Context, level IsolationLevel) (Tx, error)
}

// NewTxManager creates a new transaction manager bound to the given storage and TSGenerator.
func NewTxManager(storage StorageBackend, tsGen TSGenerator) TxManager {
	return NewTxManagerWithGC(storage, tsGen, nil)
}

// NewTxManagerWithGC creates a new transaction manager with GC support.
// If gcCfg is nil, GC is disabled (Phase 2 compatibility).
func NewTxManagerWithGC(storage StorageBackend, tsGen TSGenerator, gcCfg *GCConfig) TxManager {
	return &txManager{
		storage:          storage,
		tsGen:            tsGen,
		activeTxRegistry: NewActiveTxRegistry(),
		gcCfg:            gcCfg,
	}
}

type txManager struct {
	storage          StorageBackend
	tsGen            TSGenerator
	activeTxRegistry *ActiveTxRegistry
	txIDCounter      atomic.Uint64
	siCount          atomic.Int32
	keyLocks         sync.Map // string → *KeyLock
	gcCfg            *GCConfig
	gcStats          GCStats
	wal              WALWriter // Phase 3: WAL for crash recovery (nil = no persistence)
	walMu            sync.Mutex
}

// WALWriter is the minimal WAL interface for the transaction engine.
type WALWriter interface {
	Append(entry *WALEntry) (service.LSN, error)
	AppendBatch(entries []*WALEntry) ([]service.LSN, error)
	Sync() error
}

// WALEntry is a lightweight WAL entry reference.
type WALEntry = service.WALEntry

// WALType constants.
const (
	WALInsert = service.WALTypeInsert
	WALUpdate = service.WALTypeUpdate
	WALDelete = service.WALTypeDelete
	WALCommit = service.WALTypeCommit
)

// SetWAL sets the WAL writer for crash recovery. If nil, WAL is disabled.
// Safe for concurrent use.
func (tm *txManager) SetWAL(w WALWriter) {
	tm.walMu.Lock()
	tm.wal = w
	tm.walMu.Unlock()
}

func (tm *txManager) BeginTx(ctx context.Context, level IsolationLevel) (Tx, error) {
	return tm.beginTx(ctx, level)
}

// BeginPessimisticTx is deprecated; use BeginTx (always pessimistic since Phase 1).
func (tm *txManager) BeginPessimisticTx(ctx context.Context, level IsolationLevel) (Tx, error) {
	return tm.beginTx(ctx, level)
}

func (tm *txManager) beginTx(ctx context.Context, level IsolationLevel) (Tx, error) {
	tm.activeTxRegistry.mu.Lock()
	snapshotTS := tm.tsGen.NextTS()
	txID := tm.txIDCounter.Add(1)
	tm.activeTxRegistry.txs[txID] = snapshotTS
	tm.activeTxRegistry.mu.Unlock()

	if level == SnapshotIsolation {
		tm.siCount.Add(1)
	}
	tx := &SnapshotTx{
		engine:         tm,
		snapshotTS:     snapshotTS,
		txID:           txID,
		isolationLevel: level,
		writeBuffer:    getWriteBuffer(),
		heldLocks:      make(map[string]*KeyLock),
		ctx:            ctx,
	}
	return tx, nil
}

// ---------------------------------------------------------------------------
// UndoEntry — for rollback after partial apply
// ---------------------------------------------------------------------------

// UndoEntry records state needed to roll back a single key after a partial commit.
type UndoEntry struct {
	Key        string
	OldRawVal  []byte // B+Tree value before apply (deepCopy); nil means key didn't exist
	CommitTS   uint64 // commitTS used for this key
	EncodedKey []byte // B+Tree key (for batch Set)
	EncodedVal []byte // B+Tree value (for batch Set)
}

// ---------------------------------------------------------------------------
// SnapshotTx — full transaction implementation
// ---------------------------------------------------------------------------

// SnapshotTx implements Tx with snapshot isolation (NexKV-SI).
type SnapshotTx struct {
	engine         *txManager
	snapshotTS     uint64
	txID           uint64 // Phase 3: unique transaction ID for GC tracking
	isolationLevel IsolationLevel
	writeBuffer    *WriteBuffer
	ctx            context.Context
	completed      atomic.Bool // true after Commit or Rollback

	// heldLocks holds per-key KeyLocks acquired eagerly in Put/Delete (Lealone RowLock equiv).
	// Always non-nil since Phase 1 unified to pessimistic-only path.
	heldLocks map[string]*KeyLock
}

// SnapshotTS returns the snapshot timestamp of this transaction.
func (tx *SnapshotTx) SnapshotTS() uint64 {
	return tx.snapshotTS
}

// ---------------------------------------------------------------------------
// Read path
// ---------------------------------------------------------------------------

// Get performs a snapshot read: WriteBuffer → B+Tree → VersionChain traversal.
func (tx *SnapshotTx) Get(ctx context.Context, key []byte) ([]byte, error) {
	if err := tx.checkActive(); err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return nil, errpkg.Wrap(errpkg.ErrMVCCGetError, "get: empty key")
	}
	keyStr := string(key)

	// Read-Your-Own-Writes: check WriteBuffer first
	if entry, ok := tx.writeBuffer.Get(keyStr); ok {
		switch entry.Op {
		case OpInsert, OpUpdate:
			return entry.Value, nil
		case OpDelete:
			return nil, ErrKeyNotFound
		}
	}

	return tx.snapshotGet(ctx, key)
}

// GetBatch performs batch snapshot reads: WriteBuffer → B+Tree (single searchPath+epoch).
// Keys not in WriteBuffer are read from BTree in one batch — one searchPath, one epoch,
// one GetLeafPage — then per-key ParseMVCC + snapshotTS check + copy.
//
// Missing keys return nil at that index. Callers MUST check results[i]==nil.
func (tx *SnapshotTx) GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error) {
	if err := tx.checkActive(); err != nil {
		return nil, err
	}

	n := len(keys)
	results := make([][]byte, n)

	// Phase 1: WriteBuffer filter (Read-Your-Own-Writes)
	var btreeKeys [][]byte
	btreeIndices := make([]int, 0, n)
	for i, key := range keys {
		if len(key) == 0 {
			return nil, errpkg.Wrap(errpkg.ErrMVCCGetError, "getbatch: empty key")
		}
		keyStr := string(key)
		if entry, ok := tx.writeBuffer.Get(keyStr); ok {
			switch entry.Op {
			case OpInsert, OpUpdate:
				results[i] = entry.Value
			case OpDelete:
				// results[i] stays nil
			}
			continue
		}
		btreeKeys = append(btreeKeys, key)
		btreeIndices = append(btreeIndices, i)
	}

	if len(btreeKeys) == 0 {
		return results, nil
	}

	// Phase 2: Batch BTree read — one searchPath + one epoch for all keys
	rawVals, err := tx.engine.storage.GetBatchRaw(ctx, btreeKeys)
	if err != nil {
		return nil, err
	}

	// Phase 3: Per-key MVCC snapshot check
	for j, raw := range rawVals {
		if raw == nil { // key not found
			continue
		}
		mv, parseErr := ParseMVCC(raw)
		if parseErr != nil {
			continue
		}

		// Path 1: current version visible
		if mv.BeginTS <= tx.snapshotTS {
			if mv.IsTombstone() {
				continue // nil = tombstone
			}
			// Safe: raw already heap-copied by epoch-protected batch read
			results[btreeIndices[j]] = mv.RealVal
			continue
		}

		// Path 2: prev version visible
		if mv.PrevBeginTS != 0 && mv.PrevBeginTS <= tx.snapshotTS {
			if mv.PrevFlag != FlagTombstone {
				results[btreeIndices[j]] = mv.PrevVal
			}
		}
		// Path 3: neither visible → results[i] stays nil
	}

	return results, nil
}

// snapshotGet implements snapshot read using inline prev version (Phase 3: version-inline).
// Eliminates VersionChain traversal entirely — prev version is embedded in BTree value.
func (tx *SnapshotTx) snapshotGet(ctx context.Context, key []byte) ([]byte, error) {
	raw, err := tx.engine.storage.GetRaw(ctx, key)
	if err != nil {
		return nil, err
	}

	mv, parseErr := ParseMVCC(raw)
	if parseErr != nil {
		return nil, parseErr
	}

	// Path 1: current version visible → return directly
	if mv.BeginTS <= tx.snapshotTS {
		if mv.IsTombstone() {
			return nil, ErrKeyNotFound
		}
		return mv.RealVal, nil // raw already heap-copied by epoch-protected getRawBytes
	}

	// Path 2: current too new → check embedded previous version
	// PrevBeginTS != 0 means a valid previous version exists
	if mv.PrevBeginTS != 0 && mv.PrevBeginTS <= tx.snapshotTS {
		if mv.PrevFlag == FlagTombstone {
			return nil, ErrKeyNotFound
		}
		return mv.PrevVal, nil // raw already heap-copied by epoch-protected getRawBytes
	}

	// Path 3: neither version visible → key didn't exist at snapshot time
	return nil, ErrKeyNotFound
}

// ---------------------------------------------------------------------------
// Write path
// ---------------------------------------------------------------------------

// Put writes to the WriteBuffer. Does not touch B+Tree until Commit.
//
// Phase 1 (pessimistic locking): if tx.heldLocks is non-nil, acquires per-key KeyLock
// eagerly (Lealone RowLock equiv). This allows Commit to skip PreCheck and commitKey
// to skip GetRaw — the conflict detection already happened here.
func (tx *SnapshotTx) Put(key, value []byte) error {
	if err := tx.checkActive(); err != nil {
		return err
	}
	if len(key) == 0 {
		return errpkg.Wrap(errpkg.ErrMVCCPutError, "put: empty key")
	}
	keyStr := string(key)

	// Acquire KeyLock eagerly (Lealone RowLock equiv)
	if _, exists := tx.heldLocks[keyStr]; !exists {
		lockVal, _ := tx.engine.keyLocks.LoadOrStore(keyStr, &KeyLock{})
		kl := lockVal.(*KeyLock)
		if err := kl.Lock(); err != nil {
			return errpkg.Wrap(err, fmt.Sprintf("pessimistic lock key %s: timeout", keyStr))
		}
		tx.heldLocks[keyStr] = kl
	}

	// Read B+Tree latest committed value (write path RC)
	var btreeOldValue []byte
	var btreeOldFlag byte
	var btreeOldBeginTS uint64
	var raw []byte

	if v, err := tx.engine.storage.GetRaw(tx.ctx, key); err == nil {
		raw = v
		mvccVal, parseErr := ParseMVCC(raw)
		if parseErr == nil {
			btreeOldFlag = mvccVal.Flag
			btreeOldBeginTS = mvccVal.BeginTS
			if mvccVal.Flag == FlagNormal {
				btreeOldValue = deepCopy(mvccVal.RealVal)
			}
		}
	}

	tx.writeBuffer.Put(keyStr, value, btreeOldValue, btreeOldFlag, btreeOldBeginTS)
	return nil
}

// Delete records a delete in the WriteBuffer.
func (tx *SnapshotTx) Delete(key []byte) error {
	if err := tx.checkActive(); err != nil {
		return err
	}
	if len(key) == 0 {
		return errpkg.Wrap(errpkg.ErrMVCCDeleteError, "delete: empty key")
	}
	keyStr := string(key)

	// Acquire KeyLock eagerly
	if _, exists := tx.heldLocks[keyStr]; !exists {
		lockVal, _ := tx.engine.keyLocks.LoadOrStore(keyStr, &KeyLock{})
		kl := lockVal.(*KeyLock)
		if err := kl.Lock(); err != nil {
			return errpkg.Wrap(err, fmt.Sprintf("pessimistic lock key %s: timeout", keyStr))
		}
		tx.heldLocks[keyStr] = kl
	}

	// Check WriteBuffer first (Read-Your-Own-Writes)
	if wbEntry, has := tx.writeBuffer.Get(keyStr); has {
		switch wbEntry.Op {
		case OpInsert:
			// Insert→Delete: cancel insert
			tx.writeBuffer.entries[keyStr] = WriteEntry{Op: OpInsert} // will be removed by Delete
			return tx.writeBuffer.Delete(keyStr, nil, 0, 0)
		case OpDelete:
			return nil // idempotent
		case OpUpdate:
			wbEntry.Op = OpDelete
			wbEntry.Value = nil
			tx.writeBuffer.entries[keyStr] = wbEntry
			return nil
		}
	}

	// Read B+Tree latest committed value (write path RC)
	var raw []byte
	if v, err := tx.engine.storage.GetRaw(tx.ctx, key); err == nil {
		raw = v
	}
	if raw == nil {
		return ErrKeyNotFound
	}
	mvccVal, parseErr := ParseMVCC(raw)
	if parseErr != nil {
		return parseErr
	}
	if mvccVal.IsTombstone() {
		return ErrKeyNotFound
	}

	btreeOldValue := deepCopy(mvccVal.RealVal)
	btreeOldFlag := mvccVal.Flag
	btreeOldBeginTS := mvccVal.BeginTS

	return tx.writeBuffer.Delete(keyStr, btreeOldValue, btreeOldFlag, btreeOldBeginTS)
}

// ---------------------------------------------------------------------------
// Commit path
// ---------------------------------------------------------------------------

// Commit executes PreCheck → allocate commitTS → applyWriteBuffer → cleanup.
func (tx *SnapshotTx) Commit(ctx context.Context) error {
	// Fast-fail: if already completed, return immediately.
	// cleanup() below also uses CAS to guard against double completion.
	if tx.completed.Load() {
		return nil
	}
	defer tx.engine.activeTxRegistry.Unregister(tx.txID)
	defer tx.releaseHeldLocks() // Phase 1: release pessimistic locks on all exit paths

	if err := tx.checkActive(); err != nil {
		tx.cleanup()
		return err
	}

	// Phase 2 (3.3): Prepare — WAL TxPrepare + Sync (includes oldValue snapshots for crash rollback)
	tx.engine.walMu.Lock()
	wal := tx.engine.wal
	tx.engine.walMu.Unlock()
	if wal != nil {
		prepare := TxPrepareEntry(tx.txID, tx.writeBuffer)
		if _, err := wal.Append(prepare); err != nil {
			tx.cleanup()
			return errpkg.Wrap(err, "wal txprepare")
		}
		if err := wal.Sync(); err != nil {
			tx.cleanup()
			return errpkg.Wrap(err, "wal txprepare sync")
		}
	}

	// Phase 3: Allocate commitTS — after Prepare Sync
	commitTS := tx.engine.tsGen.NextTS()

	// Phase 4: Apply WriteBuffer (Prepare durable, commitTS allocated)
	undoBuf, err := tx.applyWriteBuffer(ctx, commitTS)
	if err != nil {
		tx.cleanup()
		return err
	}

	// Phase 5: Commit — WAL TxCommit + Sync
	if wal != nil {
		txCommitKey := make([]byte, 16)
		binary.BigEndian.PutUint64(txCommitKey[0:8], tx.txID)
		binary.BigEndian.PutUint64(txCommitKey[8:16], commitTS)
		// Note: entryCount omitted for simplicity; total keys = len(undoBuf)
		if _, err := wal.Append(&service.WALEntry{
			Type: service.WALTypeTxCommit,
			Key:  txCommitKey,
		}); err != nil {
			_ = tx.rollbackApplied(undoBuf)
			tx.cleanup()
			return errpkg.Wrap(err, "wal txcommit")
		}
		if err := wal.Sync(); err != nil {
			_ = tx.rollbackApplied(undoBuf)
			tx.cleanup()
			return errpkg.Wrap(err, "wal txcommit sync")
		}
	}

	tx.cleanup()
	// Clear references after cleanup so checkActive can distinguish Committed from RolledBack.
	// cleanup sets completed=true (CAS), providing happens-before for these writes.
	putWriteBuffer(tx.writeBuffer)
	tx.writeBuffer = nil
	return nil
}

// applyWriteBuffer applies all WriteBuffer entries to B+Tree and VersionChain.
// Phase 2 optimization: batches BTree writes — all MVCC Prepend work is done per-key,
// but BTree.Set calls are grouped into ONE SetBatch → ONE COW + ONE CAS per transaction.
func (tx *SnapshotTx) applyWriteBuffer(ctx context.Context, commitTS uint64) ([]UndoEntry, error) {
	keys := tx.writeBuffer.OrderedKeys()
	sort.Strings(keys) // prevent deadlock

	// Phase 1: MVCC Prepend + collect BTree write pairs (always pessimistic)
	pairs := make([]KVPair, 0, len(keys))
	undoBuf := make([]UndoEntry, 0, len(keys))
	for _, key := range keys {
		entry, exists := tx.writeBuffer.entries[key]
		if !exists {
			continue
		}
		undoEntry, err := tx.engine.commitKey(ctx, key, entry, commitTS)
		if err != nil {
			if undoEntry != nil {
				undoBuf = append(undoBuf, *undoEntry)
			}
			if len(undoBuf) > 0 {
				if rollbackErr := tx.rollbackApplied(undoBuf); rollbackErr != nil {
					return nil, errpkg.Wrap(err, fmt.Sprintf("apply failed, rollback also failed: %v", rollbackErr))
				}
			}
			return nil, err
		}
		if undoEntry != nil {
			undoBuf = append(undoBuf, *undoEntry)
		}
		// Collect BTree write: commitFn already Prepend'd VersionChain; now batch the Set
		pairs = append(pairs, KVPair{Key: []byte(key), Value: undoEntry.EncodedVal})
	}

	// Phase 2: Batch BTree writes — one COW + one CAS per transaction
	// Keys already sorted (sort.Strings above), matching SetBatch contract.
	if len(pairs) > 0 {
		if _, err := tx.engine.storage.SetBatch(ctx, pairs); err != nil {
			// Batch Set failed — rollback all applied Prepends
			if rollbackErr := tx.rollbackApplied(undoBuf); rollbackErr != nil {
				return nil, errpkg.Wrap(err, fmt.Sprintf("batch set failed, rollback also failed: %v", rollbackErr))
			}
			return nil, err
		}
	}

	return undoBuf, nil
}

// commitKey commits a key whose KeyLock was already acquired in Put/Delete (pessimistic).
// Skips lock acquisition and GetRaw — the conflicted value was already validated at Put time.
func (tm *txManager) commitKey(ctx context.Context, key string, entry WriteEntry, commitTS uint64) (retUndo *UndoEntry, retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = errpkg.Wrap(errpkg.ErrMVCCPanicRecovered, fmt.Sprintf("commit key %s: panic: %v", key, r))
		}
	}()

	// Reconstruct old BTree value for rollback
	var oldRawVal []byte
	if entry.Op != OpInsert {
		oldRawVal, _ = BuildMVCC(entry.OldFlag, entry.OldBeginTS, entry.OldValue, 0, 0, nil)
	}

	// Determine write content
	flag := FlagNormal
	newVal := entry.Value
	if entry.Op == OpDelete {
		flag = FlagTombstone
		newVal = nil
	}

	// Encode with prev version inline (SET delegated to caller — applyWriteBuffer batch-Set)
	encoded, buildErr := BuildMVCC(flag, commitTS, newVal, entry.OldFlag, entry.OldBeginTS, entry.OldValue)
	if buildErr != nil {
		return &UndoEntry{Key: key, OldRawVal: oldRawVal, CommitTS: commitTS},
			errpkg.Wrap(buildErr, fmt.Sprintf("build mvcc for key %s", key))
	}

	return &UndoEntry{Key: key, OldRawVal: oldRawVal, CommitTS: commitTS,
		EncodedKey: []byte(key), EncodedVal: encoded}, nil
}

// ---------------------------------------------------------------------------
// Rollback path
// ---------------------------------------------------------------------------

// Rollback discards the WriteBuffer and cleans up.
func (tx *SnapshotTx) Rollback() error {
	if tx.completed.Load() {
		return nil
	}
	defer tx.engine.activeTxRegistry.Unregister(tx.txID)
	defer tx.releaseHeldLocks() // Phase 1: unlock all keys

	if err := tx.checkActive(); err != nil {
		return err
	}
	putWriteBuffer(tx.writeBuffer)
	tx.writeBuffer = nil
	tx.cleanup()
	return nil
}

// releaseHeldLocks unlocks all per-key KeyLocks acquired in Put/Delete (pessimistic mode).
func (tx *SnapshotTx) releaseHeldLocks() {
	if tx.heldLocks == nil {
		return
	}
	for _, kl := range tx.heldLocks {
		kl.Unlock()
	}
	tx.heldLocks = nil
}

// cleanup decrements siCount (CAS-guarded against double cleanup).
func (tx *SnapshotTx) cleanup() {
	if !tx.completed.CompareAndSwap(false, true) {
		return
	}
	if tx.isolationLevel == SnapshotIsolation {
		tx.engine.siCount.Add(-1)
	}
}

// CommitAndWait commits the transaction and waits for async BTree Apply to complete.
// In sync mode (default), this is equivalent to Commit().
// In async mode, this blocks until the BTreeApplyItem finishes.
func (tx *SnapshotTx) CommitAndWait(ctx context.Context) error {
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// In sync mode, Commit already applied — return immediately.
	return nil
}

// checkActive returns an error if the transaction is already completed.
func (tx *SnapshotTx) checkActive() error {
	if tx.completed.Load() {
		if tx.writeBuffer == nil {
			return ErrTxRolledBack
		}
		return ErrTxCommitted
	}
	return nil
}

// rollbackApplied reverses committed keys in reverse order.
func (tx *SnapshotTx) rollbackApplied(undoBuf []UndoEntry) (firstErr error) {
	for i := len(undoBuf) - 1; i >= 0; i-- {
		if err := tx.rollbackOneKey(undoBuf[i]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// rollbackOneKey rolls back a single key's B+Tree value.
func (tx *SnapshotTx) rollbackOneKey(entry UndoEntry) (retErr error) {
	var kl *KeyLock
	var alreadyHeld bool
	if tx.heldLocks != nil {
		_, alreadyHeld = tx.heldLocks[entry.Key]
	}
	if !alreadyHeld {
		lockVal, _ := tx.engine.keyLocks.LoadOrStore(entry.Key, &KeyLock{})
		kl = lockVal.(*KeyLock)
		if err := kl.Lock(); err != nil {
			return errpkg.Wrap(err, fmt.Sprintf("rollback key %s: lock timeout", entry.Key))
		}
		defer kl.Unlock()
	}

	defer func() {
		if r := recover(); r != nil {
			retErr = errpkg.Wrap(errpkg.ErrMVCCPanicRecovered, fmt.Sprintf("rollback key %s: panic: %v", entry.Key, r))
		}
	}()

	// commitTS validation — only roll back our own write
	rollbackCtx := context.Background()
	current, getErr := tx.engine.storage.GetRaw(rollbackCtx, []byte(entry.Key))
	if getErr != nil {
		if getErr == ErrKeyNotFound {
			return nil
		}
		return errpkg.Wrap(getErr, fmt.Sprintf("rollback key %s: GetRaw failed", entry.Key))
	}
	mvccVal, parseErr := ParseMVCC(current)
	if parseErr != nil {
		return errpkg.Wrap(parseErr, fmt.Sprintf("rollback key %s: parse failed", entry.Key))
	}
	if mvccVal.BeginTS != entry.CommitTS {
		return nil // not our write, skip
	}

	// Restore B+Tree to old value
	if entry.OldRawVal == nil {
		tombstone, buildErr := BuildMVCC(FlagTombstone, entry.CommitTS, nil, 0, 0, nil)
		if buildErr != nil {
			return errpkg.Wrap(buildErr, fmt.Sprintf("rollback key %s: build tombstone failed", entry.Key))
		}
		if opErr := tx.engine.storage.Set(rollbackCtx, []byte(entry.Key), tombstone); opErr != nil {
			return errpkg.Wrap(opErr, fmt.Sprintf("rollback key %s: set tombstone failed", entry.Key))
		}
	} else {
		if opErr := tx.engine.storage.Set(rollbackCtx, []byte(entry.Key), entry.OldRawVal); opErr != nil {
			return errpkg.Wrap(opErr, fmt.Sprintf("rollback key %s: restore failed", entry.Key))
		}
	}
	return nil
}
