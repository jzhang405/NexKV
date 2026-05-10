// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package checkpoint

// DirtyTracker is intentionally minimal for the COW BTree architecture.
//
// COW Dirty Page Semantics (§6.2 问题 1):
//   - NexKV BTree uses Copy-on-Write: every write allocates new pages.
//   - Old pages are never modified in-place; they remain as snapshots.
//   - "Dirty pages" in traditional sense don't exist — all active-path pages
//     need to be written during Checkpoint, and all are "clean" after COW.
//   - Checkpoint DFS traversal (§3.5) walks the active path from rootRef,
//     which is exactly the set of pages that need to be persisted.
//
// Implementation: CheckpointManager.enumeratePages() performs DFS from the
// COW root snapshot, collecting all reachable page IDs. No bitmap tracking
// is needed — COW semantics guarantee that pages not in the old rootRef
// subtree are either (a) already in Checkpoint data from previous cycles,
// or (b) covered by WAL replay during Recovery.
//
// Phase 4 note: If the BTree ever supports in-place page mutation (non-COW
// mode), a traditional dirty-page bitmap will be needed.
type DirtyTracker struct{}
