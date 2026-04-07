package btree

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagMissingRanges(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(256 * 1024 * 1024)
	require.NoError(t, err)

	tree, err := NewBTree(storage)
	require.NoError(t, err)
	defer tree.Close()

	// ★ Enable tracer to capture operation sequence
	tracer := NewTestTracer(t, 100000, 200000)
	GlobalTracer = tracer
	defer func() {
		GlobalTracer = &nilTracer{}
		tracer.Close()
	}()

	ctx := context.Background()

	const goroutines = 2
	const keysPerG = 500
	var missing []string

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := range keysPerG {
				key := fmt.Appendf(nil, "g%d-k%04d", gid, j)
				value := fmt.Appendf(nil, "v%d-%04d", gid, j)
				for range 100 {
					if err := tree.Set(ctx, key, value); err == nil {
						break
					}
				}
			}
		}(g)
	}
	wg.Wait()

	// === Logical view: root children cache ===
	rootInfo := tree.rootRef.GetPageInfo()
	t.Logf("Root PageInfo: pageID=%d ver=%d isLeaf=%v", rootInfo.PageID, rootInfo.Version, rootInfo.IsLeaf)

	var rootCache *ChildrenCache
	if !rootInfo.IsLeaf {
		rootCache = tree.rootRef.children.Load()
		if rootCache != nil {
			t.Logf("Root children cache: %d children, %d separators", len(rootCache.Children), len(rootCache.Separators))
			for i, child := range rootCache.Children {
				if child == nil {
					t.Logf("  cache.Children[%d]: nil", i)
					continue
				}
				ci := child.GetPageInfo()
				if ci.IsLeaf && !ci.Redirect {
					leafPage, lerr := storage.GetLeafPage(ci.PageID)
					if lerr == nil && leafPage.Count() > 0 {
						t.Logf("  cache.Children[%d]: refPageID=%d dataPageID=%d count=%d range=[%s..%s]",
							i, child.pageID, ci.PageID, leafPage.Count(),
							string(leafPage.GetKey(0)), string(leafPage.GetKey(leafPage.Count()-1)))
					} else {
						t.Logf("  cache.Children[%d]: refPageID=%d dataPageID=%d isLeaf=%v redirect=%v",
							i, child.pageID, ci.PageID, ci.IsLeaf, ci.Redirect)
					}
				} else {
					t.Logf("  cache.Children[%d]: refPageID=%d dataPageID=%d isLeaf=%v redirect=%v",
						i, child.pageID, ci.PageID, ci.IsLeaf, ci.Redirect)
				}
			}
			for i, sep := range rootCache.Separators {
				t.Logf("  cache.Separators[%d]=%s", i, string(sep))
			}
		}
	}

	// === Physical view: dump all pages ===
	pa := storage.GetPageAccessor()
	pages := pa.DumpAllPages()
	t.Logf("=== Physical pages: %d ===", len(pages))

	var leafKeys [][]string
	var leafCount, indexCount int
	for i := range pages {
		p := &pages[i]
		if p.Deleted {
			continue
		}
		if p.IsLeaf {
			leafCount++
			var ks []string
			for _, k := range p.Keys {
				ks = append(ks, string(k))
			}
			leafKeys = append(leafKeys, ks)
		} else {
			indexCount++
			t.Logf("Index page %d: count=%d ver=%d children=%v keys=%v",
				p.PageID, p.Count, p.Version, p.Children, keyBytesToStrs(p.Keys))
		}
	}
	t.Logf("Active pages: %d leaf, %d index", leafCount, indexCount)

	// Collect all keys from active leaf pages
	var allPhysicalKeys []string
	for _, ks := range leafKeys {
		allPhysicalKeys = append(allPhysicalKeys, ks...)
	}
	sort.Strings(allPhysicalKeys)
	t.Logf("Total keys in leaf pages: %d", len(allPhysicalKeys))

	// Check which keys are missing from Get()
	for g := range goroutines {
		for j := range keysPerG {
			key := fmt.Appendf(nil, "g%d-k%04d", g, j)
			_, err := tree.Get(ctx, key)
			if err != nil {
				missing = append(missing, string(key))
			}
		}
	}

	t.Logf("Missing from Get(): %d", len(missing))

	if len(missing) > 0 {
		t.Logf("=== Missing keys analysis ===")
		physicalSet := make(map[string]bool, len(allPhysicalKeys))
		for _, k := range allPhysicalKeys {
			physicalSet[k] = true
		}
		var inPhysical, notInPhysical int
		for _, k := range missing {
			if physicalSet[k] {
				inPhysical++
			} else {
				notInPhysical++
			}
		}
		t.Logf("  Missing keys found in physical pages: %d (tree navigation broken)", inPhysical)
		t.Logf("  Missing keys NOT in physical pages: %d (data lost)", notInPhysical)

		for i, k := range missing {
			if i >= 30 {
				break
			}
			t.Logf("  missing[%d]=%s inPhysical=%v", i, k, physicalSet[k])
		}
	}

	// Dump tracer log sample
	logs := tracer.DumpLogs()
	t.Logf("Tracer log entries: %d", len(logs))
	for i, l := range logs {
		if i >= 50 {
			break
		}
		t.Logf("  TRACE[%d]: %s", i, l)
	}

	assert.Equal(t, 0, len(missing), "no keys should be missing")
}

func keyBytesToStrs(keys [][]byte) []string {
	if len(keys) == 0 {
		return nil
	}
	result := make([]string, len(keys))
	for i, k := range keys {
		result[i] = string(k)
	}
	return result
}
