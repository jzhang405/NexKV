// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import "github.com/jzhang405/NexKV/internal/domain/model"

// PageInfo is an immutable snapshot of a page's metadata.
// Each COW mutation produces a new instance with incremented Version.
// Replaced atomically via PageRef.CAS.
type PageInfo struct {
	PageID    model.PageID
	Version   uint64
	Tombstone bool     // page has been split, no longer navigable (B3/B4 fix)
	Redirect  bool     // data structure changed (split), reader should re-navigate via NewRef
	NewRef    *PageRef // when Redirect=true, points to the left child of the split
}
