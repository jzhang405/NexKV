// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

//go:build linux || darwin || freebsd

package chunk

import (
	"os"
)

// preallocate pre-allocates disk space for a file.
// Uses ftruncate to extend the file to the desired size.
func preallocate(f *os.File, size int64) error {
	return f.Truncate(size)
}
