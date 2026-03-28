// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

// DebugPrintf no-op placeholder for debug logging
func DebugPrintf(format string, args ...any) {}

// IsDebugEnabled returns false
func IsDebugEnabled() bool { return false }
