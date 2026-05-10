// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package checkpoint

import "time"

// Config holds checkpoint configuration.
type Config struct {
	Interval              time.Duration // Fuzzy Checkpoint interval (default 30s)
	SegmentCountThreshold int           // trigger when WAL segments exceed this count
	WALSizeThreshold      int64         // trigger when WAL total size exceeds this (bytes)
}

// DefaultConfig returns the recommended configuration.
func DefaultConfig() *Config {
	return &Config{
		Interval:              30 * time.Second,
		SegmentCountThreshold: 4,
		WALSizeThreshold:      256 * 1024 * 1024, // 256MB
	}
}
