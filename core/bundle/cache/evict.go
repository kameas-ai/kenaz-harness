package cache

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// EvictPolicy controls cache GC. Default is "indefinite" — the cache never
// auto-evicts, on the principle that a content-addressable store never has
// stale data, only unused data. Operators may opt in to TTL or maximum-
// size policies via this struct.
type EvictPolicy struct {
	// MaxAge evicts entries whose last-modified time is older than now-MaxAge.
	// Zero = no age limit.
	MaxAge time.Duration

	// MaxBytes evicts least-recently-used entries until total size drops
	// below this threshold. Zero = no size limit.
	MaxBytes int64
}

// GC walks the canonical sha256/ tree and evicts entries per policy.
// Returns the count of evicted entries and the bytes reclaimed.
func (c *fsCAS) GC(policy EvictPolicy) (count int, bytesEvicted int64, err error) {
	root := filepath.Join(c.root, "sha256")
	type entry struct {
		path    string
		size    int64
		modTime time.Time
	}
	var entries []entry
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		entries = append(entries, entry{path: p, size: info.Size(), modTime: info.ModTime()})
		return nil
	}); err != nil {
		// If the sha256/ tree doesn't exist yet, that's fine — nothing to GC.
		if !os.IsNotExist(err) {
			return 0, 0, err
		}
	}

	now := time.Now()
	// Phase 1: age-based eviction.
	if policy.MaxAge > 0 {
		kept := entries[:0]
		for _, e := range entries {
			if now.Sub(e.modTime) > policy.MaxAge {
				if rerr := os.Remove(e.path); rerr == nil {
					count++
					bytesEvicted += e.size
				}
				continue
			}
			kept = append(kept, e)
		}
		entries = kept
	}

	// Phase 2: size-based LRU.
	if policy.MaxBytes > 0 {
		var total int64
		for _, e := range entries {
			total += e.size
		}
		if total > policy.MaxBytes {
			// Sort oldest-first.
			sort.SliceStable(entries, func(i, j int) bool {
				return entries[i].modTime.Before(entries[j].modTime)
			})
			for _, e := range entries {
				if total <= policy.MaxBytes {
					break
				}
				if rerr := os.Remove(e.path); rerr == nil {
					count++
					bytesEvicted += e.size
					total -= e.size
				}
			}
		}
	}
	return count, bytesEvicted, nil
}
