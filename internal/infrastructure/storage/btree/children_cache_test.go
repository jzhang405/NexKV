// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChildrenCacheSearch(t *testing.T) {
	tests := []struct {
		name        string
		separators  [][]byte
		numChildren int // Children count (len(Separators)+1)
		key         string
		want        int
	}{
		{
			name:        "empty_cache",
			separators:  nil,
			numChildren: 0,
			key:         "a",
			want:        0,
		},
		{
			name:        "single_child_no_separators",
			separators:  nil,
			numChildren: 1,
			key:         "z",
			want:        0,
		},
		{
			name:        "single_separator_key_less",
			separators:  [][]byte{[]byte("f")},
			numChildren: 2,
			key:         "a",
			want:        0, // a < f → C0
		},
		{
			name:        "single_separator_key_equal",
			separators:  [][]byte{[]byte("f")},
			numChildren: 2,
			key:         "f",
			want:        1, // f == f → C1 (right subtree)
		},
		{
			name:        "single_separator_key_greater",
			separators:  [][]byte{[]byte("f")},
			numChildren: 2,
			key:         "z",
			want:        1, // z > f → C1
		},
		{
			// Matches nodePageHandle.Search test: keys=[c,f,i], 4 children
			name:        "three_separators_key_before_all",
			separators:  [][]byte{[]byte("c"), []byte("f"), []byte("i")},
			numChildren: 4,
			key:         "a",
			want:        0, // a < c → C0
		},
		{
			name:        "three_separators_key_equals_first",
			separators:  [][]byte{[]byte("c"), []byte("f"), []byte("i")},
			numChildren: 4,
			key:         "c",
			want:        1, // c == c → C1 (right subtree)
		},
		{
			name:        "three_separators_key_between_first_second",
			separators:  [][]byte{[]byte("c"), []byte("f"), []byte("i")},
			numChildren: 4,
			key:         "d",
			want:        1, // c < d < f → C1
		},
		{
			name:        "three_separators_key_equals_second",
			separators:  [][]byte{[]byte("c"), []byte("f"), []byte("i")},
			numChildren: 4,
			key:         "f",
			want:        2, // f == f → C2 (right subtree)
		},
		{
			name:        "three_separators_key_between_second_third",
			separators:  [][]byte{[]byte("c"), []byte("f"), []byte("i")},
			numChildren: 4,
			key:         "g",
			want:        2, // f < g < i → C2
		},
		{
			name:        "three_separators_key_equals_third",
			separators:  [][]byte{[]byte("c"), []byte("f"), []byte("i")},
			numChildren: 4,
			key:         "i",
			want:        3, // i == i → C3 (right subtree)
		},
		{
			name:        "three_separators_key_after_all",
			separators:  [][]byte{[]byte("c"), []byte("f"), []byte("i")},
			numChildren: 4,
			key:         "z",
			want:        3, // z > i → C3
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &ChildrenCache{
				Children:   make([]*PageRef, tt.numChildren),
				Separators: tt.separators,
			}
			got := cache.Search([]byte(tt.key))
			assert.Equal(t, tt.want, got, "key=%q", tt.key)
		})
	}
}

func TestChildrenCacheSearchNil(t *testing.T) {
	// Nil cache should return 0 without panic
	var cache *ChildrenCache
	assert.Equal(t, 0, cache.Search([]byte("any")))
}

func TestChildrenCacheInvariant(t *testing.T) {
	// Verify Search return value is always in [0, len(Children))
	cache := &ChildrenCache{
		Children:   make([]*PageRef, 5),
		Separators: [][]byte{[]byte("b"), []byte("d"), []byte("f"), []byte("h")},
	}
	assert.Equal(t, 4, len(cache.Separators), "len(Separators) == len(Children) - 1")

	testKeys := []string{"", "a", "b", "c", "d", "e", "f", "g", "h", "i", "z", "\xff\xff"}
	for _, k := range testKeys {
		idx := cache.Search([]byte(k))
		assert.GreaterOrEqual(t, idx, 0, "key=%q: idx >= 0", k)
		assert.Less(t, idx, len(cache.Children), "key=%q: idx < len(Children)", k)
	}
}

func TestCopyKey(t *testing.T) {
	original := []byte("hello")

	copied := copyKey(original)

	// Same content
	assert.Equal(t, original, copied)

	// Different underlying array
	copied[0] = 'H'
	assert.Equal(t, []byte("hello"), original, "modifying copy should not affect original")
}

func TestCopyKeyEmpty(t *testing.T) {
	original := []byte{}
	copied := copyKey(original)
	assert.Equal(t, []byte{}, copied)
	assert.Equal(t, 0, len(copied))
}

func TestCopyKeyNil(t *testing.T) {
	copied := copyKey(nil)
	assert.Equal(t, 0, len(copied))
}
