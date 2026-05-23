// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"context"
	"fmt"
	"runtime"
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
		versionStore:     &VersionStore{},
		activeTxRegistry: NewActiveTxRegistry(),
		gcCfg:            gcCfg,
	}
}

type txManager struct {
	storage          StorageBackend
	tsGen            TSGenerator
	versionStore     *VersionStore
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
	// Allocate snapshotTS and register txID under the same Mutex to eliminate
	// the GC window where a transaction is visible but its snapshotTS isn't tracked.
	tm.activeTxRegistry.mu.Lock()
	snapshotTS := tm.tsGen.NextTS()
	txID := tm.txIDCounter.Add(1)
	tm.activeTxRegistry.txs[txID] = snapshotTS
	tm.activeTxRegistry.mu.Unlock()

	if level == SnapshotIsolation {
		tm.siCount.Add(1)
	}
	return &SnapshotTx{
		engine:         tm,
		snapshotTS:     snapshotTS,
		txID:           txID,
		isolationLevel: level,
		writeBuffer:    NewWriteBuffer(),
		readSet:        make(map[string]ReadFingerprint),
		ctx:            ctx,
	}, nil
}

// ---------------------------------------------------------------------------
// UndoEntry — for rollback after partial apply
// ---------------------------------------------------------------------------

// UndoEntry records state needed to roll back a single key after a partial commit.
type UndoEntry struct {
	Key              string
	OldRawVal        []byte       // B+Tree value before apply (deepCopy); nil means key didn't exist
	CommitTS         uint64       // commitTS used for this key
	PrePrependHead   *VersionNode // VersionChain head before Prepend
	PrependSucceeded bool         // whether Prepend succeeded
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
	readSet        map[string]ReadFingerprint
	ctx            context.Context
	completed      atomic.Bool // true after Commit or Rollback
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
		return nil, fmt.Errorf("get: empty key")
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

// snapshotGet implements the core snapshot read algorithm using optimistic retry.
//
// The read path is lock-free: it reads B+Tree then traverses VersionChain without
// acquiring KeyLock. To handle the Set-before-Prepend window (where B+Tree is updated
// but VersionChain hasn't been prepended yet) and rollback concurrency, we use an
// optimistic consistency check: record the chain's Generation() before traversal,
// and re-check after. If generation changed (a concurrent commit or rollback modified
// the chain), we retry the entire read.
//
// This avoids read-path locking overhead while guaranteeing snapshot consistency.
const snapshotGetMaxRetries = 3

func (tx *SnapshotTx) snapshotGet(ctx context.Context, key []byte) ([]byte, error) {
	keyStr := string(key)

	for range snapshotGetMaxRetries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Step 1: Read B+Tree (atomic single read)
		raw, err := tx.engine.storage.GetRaw(ctx, key)
		if err != nil {
			if err == ErrKeyNotFound {
				return nil, ErrKeyNotFound
			}
			return nil, fmt.Errorf("snapshot get: storage read failed for key %s: %w", keyStr, err)
		}

		mvccVal, parseErr := ParseMVCC(raw)
		if parseErr != nil {
			return nil, parseErr
		}

		// Step 2: B+Tree version visible → return directly (no chain involvement)
		if mvccVal.BeginTS <= tx.snapshotTS {
			if mvccVal.IsTombstone() {
				return nil, ErrKeyNotFound
			}
			return deepCopy(mvccVal.RealVal), nil
		}

		// Step 3: B+Tree version too new → traverse VersionChain
		chainVal := tx.engine.versionStore.Load(keyStr)
		if chainVal == nil {
			// Chain not yet created — the B+Tree was updated but commitKey's
			// LoadOrStore hasn't executed yet. This should be extremely rare since
			// LoadOrStore runs before KeyLock acquisition in commitKey.
			runtime.Gosched()
			continue
		}

		// Record generation before traversal for optimistic consistency check.
		// If a concurrent commit (Prepend) or rollback (CAS revert) changes the chain
		// during our traversal, generation will differ and we retry.
		genBefore := chainVal.Generation()

		// Step 4: Traverse chain, find bestNode (min commitTS > snapshotTS, not rolledBack)
		var bestNode *VersionNode
		node := chainVal.Load()
		for node != nil {
			if node.commitTS > tx.snapshotTS && !node.rolledBack.Load() && !node.reclaimed.Load() {
				if bestNode == nil || node.commitTS < bestNode.commitTS {
					bestNode = node
				}
			}
			node = node.next.Load()
		}

		// Optimistic validation: if generation changed, the chain was modified
		// during our traversal (Set-before-Prepend window or rollback). Retry.
		if chainVal.Generation() != genBefore {
			runtime.Gosched()
			continue
		}

		if bestNode != nil {
			if bestNode.flag == FlagTombstone {
				return nil, ErrKeyNotFound
			}
			return deepCopy(bestNode.value), nil
		}

		// bestNode == nil: no visible version in chain.
		// This can happen for Insert (Insert doesn't Prepend, so chain has no nodes
		// for the snapshot's key). The key didn't exist at snapshot time.
		return nil, ErrKeyNotFound
	}

	// Exhausted retries — highly unlikely under normal operation.
	// This indicates extreme contention on this key. Return a contention error
	// rather than ErrKeyNotFound to avoid confusing the caller about key existence.
	return nil, fmt.Errorf("snapshot read contention for key %s after %d retries: %w",
		keyStr, snapshotGetMaxRetries, ErrVersionChainConflict)
}

// ---------------------------------------------------------------------------
// Write path
// ---------------------------------------------------------------------------

// Put writes to the WriteBuffer. Does not touch B+Tree until Commit.
func (tx *SnapshotTx) Put(key, value []byte) error {
	if err := tx.checkActive(); err != nil {
		return err
	}
	if len(key) == 0 {
		return fmt.Errorf("put: empty key")
	}
	keyStr := string(key)

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

	// Record ReadFingerprint (prevent blind write Lost Update)
	if raw != nil && btreeOldFlag == FlagNormal {
		tx.readSet[keyStr] = NewReadFingerprint(raw)
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
		return fmt.Errorf("delete: empty key")
	}
	keyStr := string(key)

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

	// Record ReadFingerprint
	tx.readSet[keyStr] = NewReadFingerprint(raw)

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

	if err := tx.checkActive(); err != nil {
		tx.cleanup()
		return err
	}

	// Phase 1: PreCheck — verify read set
	if err := tx.preCheck(ctx); err != nil {
		tx.cleanup()
		return err
	}

	// Phase 2: WAL Append (write entries) + Sync — before commitTS allocation
	// Phase 3.2: commitTS is allocated AFTER WAL sync to guarantee strict monotonicity.
	tx.engine.walMu.Lock()
	wal := tx.engine.wal
	tx.engine.walMu.Unlock()
	if wal != nil {
		entries := tx.writeBuffer.WriteEntries()
		if _, err := wal.AppendBatch(entries); err != nil {
			tx.cleanup()
			return fmt.Errorf("wal append: %w", err)
		}
		if err := wal.Sync(); err != nil {
			tx.cleanup()
			return fmt.Errorf("wal sync: %w", err)
		}
	}

	// Phase 3: Allocate commitTS — after WAL sync (Phase 3.2)
	commitTS := tx.engine.tsGen.NextTS()

	// Phase 4: Apply WriteBuffer (WAL already durable, commitTS allocated)
	if err := tx.applyWriteBuffer(ctx, commitTS); err != nil {
		tx.cleanup()
		// Best-effort rollback (Phase 3.2: rollback WAL entries already written)
		return err
	}

	// Phase 5: WAL Commit marker (after Apply success)
	if wal != nil {
		if _, err := wal.Append(CommitEntry(commitTS)); err != nil {
			tx.cleanup()
			return fmt.Errorf("wal commit marker: %w", err)
		}
		if err := wal.Sync(); err != nil {
			tx.cleanup()
			return fmt.Errorf("wal commit sync: %w", err)
		}
	}

	tx.cleanup()
	// Clear references after cleanup so checkActive can distinguish Committed from RolledBack.
	// cleanup sets completed=true (CAS), providing happens-before for these writes.
	tx.writeBuffer = nil
	tx.readSet = nil
	return nil
}

// preCheck verifies that all keys in the read set have unchanged ValueHash.
// NOTE: PreCheck is a best-effort fast-fail optimization. The definitive conflict
// detection happens in commitKey under KeyLock (beginTS validation).
func (tx *SnapshotTx) preCheck(ctx context.Context) error {
	for keyStr, fp := range tx.readSet {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		current, err := tx.engine.storage.GetRaw(ctx, []byte(keyStr))
		if err != nil {
			return fmt.Errorf("precheck: key %s not found: %w", keyStr, ErrConflict)
		}
		currentFP := NewReadFingerprint(current)
		if currentFP.ValueHash != fp.ValueHash {
			return ErrConflict
		}
	}
	return nil
}

// applyWriteBuffer applies all WriteBuffer entries to B+Tree and VersionChain.
func (tx *SnapshotTx) applyWriteBuffer(ctx context.Context, commitTS uint64) error {
	keys := tx.writeBuffer.OrderedKeys()
	sort.Strings(keys) // prevent deadlock

	undoBuf := make([]UndoEntry, 0, len(keys))

	for _, key := range keys {
		entry, exists := tx.writeBuffer.entries[key]
		if !exists {
			continue // Insert→Delete removed from entries
		}

		undoEntry, err := tx.engine.commitKey(ctx, key, entry, commitTS)
		if err != nil {
			// commitKey may have returned a partial UndoEntry (Prepend failed after Set)
			if undoEntry != nil {
				undoBuf = append(undoBuf, *undoEntry)
			}
			if len(undoBuf) > 0 {
				if rollbackErr := tx.rollbackApplied(undoBuf); rollbackErr != nil {
					return fmt.Errorf("apply failed: %w, rollback also failed: %v", err, rollbackErr)
				}
			}
			return err
		}
		undoBuf = append(undoBuf, *undoEntry)
	}
	return nil
}

// commitKey atomically commits a single key under KeyLock protection.
// Executes GetRaw → validate → Prepend → Set within the lock.
//
// Phase 3 (NEW-2 CRITICAL): Prepend-before-Set order eliminates half-Apply OldValue loss.
// In the old order (Set-before-Prepend), a crash after Set but before Prepend overwrites
// the BTree old value, making Recovery unable to derive Prepend's OldValue. The new order
// (Prepend-before-Set) ensures that a crash after Prepend but before Set leaves the BTree
// with the old value intact — Recovery simply redoes Set (idempotent).
func (tm *txManager) commitKey(ctx context.Context, key string, entry WriteEntry, commitTS uint64) (retUndo *UndoEntry, retErr error) {
	// Ensure VersionChain exists before acquiring KeyLock (avoid sync.Map mutex in critical section)
	tm.versionStore.LoadOrStore(key)

	// Acquire per-key KeyLock
	lockVal, _ := tm.keyLocks.LoadOrStore(key, &KeyLock{})
	kl := lockVal.(*KeyLock)
	if err := kl.Lock(); err != nil {
		return nil, fmt.Errorf("key %s lock timeout: %w", key, err)
	}
	defer kl.Unlock()

	// Recover from panic in critical section to prevent B+Tree/VersionChain inconsistency
	// from propagating. The caller (applyWriteBuffer) will attempt rollback via undoBuf.
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("commit key %s: panic: %v", key, r)
		}
	}()

	// ===== Critical section: strictly serialized per-key =====

	// Step 1: Read current B+Tree value (no TOCTOU inside lock)
	var oldRawVal []byte
	if current, err := tm.storage.GetRaw(ctx, []byte(key)); err == nil {
		oldRawVal = current // already a heap copy from GetRaw
	}

	// Step 2: Validate
	switch entry.Op {
	case OpInsert:
		if oldRawVal != nil {
			mvccVal, parseErr := ParseMVCC(oldRawVal)
			if parseErr != nil {
				return nil, fmt.Errorf("parse old value for key %s: %w", key, parseErr)
			}
			if mvccVal.Flag != FlagTombstone {
				return nil, ErrConflict
			}
		}
	case OpUpdate, OpDelete:
		if oldRawVal == nil {
			return nil, ErrConflict
		}
		mvccVal, parseErr := ParseMVCC(oldRawVal)
		if parseErr != nil {
			return nil, fmt.Errorf("parse old value for key %s: %w", key, parseErr)
		}
		if mvccVal.BeginTS != entry.OldBeginTS {
			return nil, ErrConflict
		}
	}

	// Step 3: Determine write content
	flag := FlagNormal
	newVal := entry.Value
	if entry.Op == OpDelete {
		flag = FlagTombstone
		newVal = nil
	}

	// Step 4: Prepend to VersionChain FIRST (NEW-2: Prepend-before-Set)
	var prePrependHead *VersionNode
	chain := tm.versionStore.Load(key)
	if chain != nil {
		prePrependHead = chain.Load()
	}

	switch entry.Op {
	case OpInsert:
		if err := tm.versionStore.Prepend(key, commitTS, nil, FlagTombstone); err != nil {
			return &UndoEntry{Key: key, OldRawVal: oldRawVal, CommitTS: commitTS,
					PrePrependHead: prePrependHead, PrependSucceeded: false},
				fmt.Errorf("version chain prepend (insert marker) failed for key %s: %w", key, err)
		}
	case OpUpdate, OpDelete:
		if err := tm.versionStore.Prepend(key, commitTS, entry.OldValue, entry.OldFlag); err != nil {
			return &UndoEntry{Key: key, OldRawVal: oldRawVal, CommitTS: commitTS,
					PrePrependHead: prePrependHead, PrependSucceeded: false},
				fmt.Errorf("version chain prepend failed for key %s: %w", key, err)
		}
	}

	// Step 5: Write to B+Tree (AFTER Prepend — NEW-2)
	encoded, buildErr := BuildMVCC(flag, commitTS, newVal)
	if buildErr != nil {
		return &UndoEntry{Key: key, OldRawVal: oldRawVal, CommitTS: commitTS,
				PrePrependHead: prePrependHead, PrependSucceeded: true},
			fmt.Errorf("build mvcc for key %s: %w", key, buildErr)
	}
	if err := tm.storage.Set(ctx, []byte(key), encoded); err != nil {
		return &UndoEntry{Key: key, OldRawVal: oldRawVal, CommitTS: commitTS,
				PrePrependHead: prePrependHead, PrependSucceeded: true},
			fmt.Errorf("btree set failed for key %s: %w", key, err)
	}

	// ===== Critical section end =====

	return &UndoEntry{Key: key, OldRawVal: oldRawVal, CommitTS: commitTS,
		PrePrependHead: prePrependHead, PrependSucceeded: true}, nil
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

	if err := tx.checkActive(); err != nil {
		return err
	}
	tx.writeBuffer = nil
	tx.readSet = nil
	tx.cleanup()
	return nil
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

// rollbackOneKey rolls back a single key's B+Tree value and VersionChain head.
// Runs as an independent sub-function so defer Unlock fires immediately per key.
func (tx *SnapshotTx) rollbackOneKey(entry UndoEntry) (retErr error) {
	lockVal, _ := tx.engine.keyLocks.LoadOrStore(entry.Key, &KeyLock{})
	kl := lockVal.(*KeyLock)
	if err := kl.Lock(); err != nil {
		return fmt.Errorf("rollback key %s: lock timeout: %w", entry.Key, err)
	}
	defer kl.Unlock()

	// Recover from panic in critical section
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("rollback key %s: panic: %v", entry.Key, r)
		}
	}()

	// Step A: commitTS validation — only roll back our own write
	// Rollback uses context.Background() — internal recovery must not be affected by client context cancellation
	rollbackCtx := context.Background()
	current, getErr := tx.engine.storage.GetRaw(rollbackCtx, []byte(entry.Key))
	if getErr != nil {
		if getErr == ErrKeyNotFound {
			return nil // key already cleaned up, nothing to roll back
		}
		return fmt.Errorf("rollback key %s: GetRaw failed: %w", entry.Key, getErr)
	}
	mvccVal, parseErr := ParseMVCC(current)
	if parseErr != nil {
		return fmt.Errorf("rollback key %s: parse failed: %w", entry.Key, parseErr)
	}
	if mvccVal.BeginTS != entry.CommitTS {
		return nil // current value already updated by another transaction, skip
	}

	// Step B: Restore B+Tree
	if entry.OldRawVal == nil {
		// Original key didn't exist → write Tombstone (not physical delete)
		tombstone, buildErr := BuildMVCC(FlagTombstone, entry.CommitTS, nil)
		if buildErr != nil {
			return fmt.Errorf("rollback key %s: build tombstone failed: %w", entry.Key, buildErr)
		}
		if opErr := tx.engine.storage.Set(rollbackCtx, []byte(entry.Key), tombstone); opErr != nil {
			return fmt.Errorf("rollback key %s: set tombstone failed: %w", entry.Key, opErr)
		}
	} else {
		if opErr := tx.engine.storage.Set(rollbackCtx, []byte(entry.Key), entry.OldRawVal); opErr != nil {
			return fmt.Errorf("rollback key %s: restore failed: %w", entry.Key, opErr)
		}
	}

	// Step C: Rollback VersionChain
	if !entry.PrependSucceeded {
		return nil // Prepend never happened, chain unchanged
	}

	chain := tx.engine.versionStore.Load(entry.Key)
	if chain == nil {
		return nil
	}

	// Path 1: head still our node → CAS revert via chainHead
	old := chain.head.Load()
	if old != nil && old.node != nil && old.node.commitTS == entry.CommitTS {
		newHead := &chainHead{
			node:       entry.PrePrependHead,
			generation: old.generation + 1,
		}
		if chain.head.CompareAndSwap(old, newHead) {
			return nil
		}
		// CAS failed: another Prepend raced us, fall through to Path 2
	}

	// Path 2: head changed by later commit or CAS lost → mark our node rolledBack
	// Must bump generation so concurrent snapshotGet detects the chain modification.
	node := chain.Load() // re-load head after potential CAS failure
	for node != nil {
		if node.commitTS == entry.CommitTS {
			node.rolledBack.Store(true)
			chain.bumpGeneration()
			return nil
		}
		node = node.next.Load()
	}
	return nil
}
