package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultDriftCacheTTL is the default time-to-live for cached drift scan results.
const DefaultDriftCacheTTL = 24 * time.Hour

// driftCacheData is the on-disk format for cached drift scan results.
type driftCacheData struct {
	LastScan time.Time         `json:"last_scan"`
	Results  *DriftScanResults `json:"results"`
}

// DriftCache persists drift scan results to disk with a timestamp,
// enabling time-gated re-scans and offline result access.
type DriftCache struct {
	dir              string
	contextName      string
	mu               sync.RWMutex
	ttl              time.Duration
	lastInvalidation time.Time
	scanning         atomic.Bool // true while a scan is in progress
}

// NewDriftCache creates a new DriftCache for a given Kubernetes context.
// Cache files are stored as dir/drift-<sanitized-context>.json.
func NewDriftCache(dir, contextName string) *DriftCache {
	return &DriftCache{
		dir:         dir,
		contextName: contextName,
		ttl:         DefaultDriftCacheTTL,
	}
}

// Generation returns a token that changes every time Invalidate() is called.
// Capture this before starting an asynchronous scan and pass it to
// SaveIfCurrent to skip the save when a mutation invalidated the cache while
// the scan was in flight.
func (c *DriftCache) Generation() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastInvalidation
}

// filePath returns the cache file path, sanitizing the context name
// for filesystem safety.
func (c *DriftCache) filePath() string {
	name := strings.ReplaceAll(c.contextName, "/", "-")
	return filepath.Join(c.dir, "drift-"+name+".json")
}

// Load returns cached drift scan results and the time of the last scan.
// The bool is false if no cache exists or the file is unreadable.
func (c *DriftCache) Load() (*DriftScanResults, time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loadLocked()
}

func (c *DriftCache) loadLocked() (*DriftScanResults, time.Time, bool) {
	data, err := os.ReadFile(c.filePath())
	if err != nil {
		return nil, time.Time{}, false
	}
	var cached driftCacheData
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, time.Time{}, false
	}
	if cached.Results == nil {
		return nil, time.Time{}, false
	}
	return cached.Results, cached.LastScan, true
}

// IsFresh returns true if valid cached results exist and are within the TTL.
func (c *DriftCache) IsFresh() bool {
	_, lastScan, ok := c.Load()
	if !ok {
		return false
	}
	return time.Since(lastScan) < c.ttl
}

// LoadFresh returns cached results only if they exist and are within the TTL.
// Single Load(); avoids the double file read of Load()+IsFresh().
func (c *DriftCache) LoadFresh() (*DriftScanResults, time.Time, bool) {
	results, lastScan, ok := c.Load()
	if !ok || results == nil {
		return nil, time.Time{}, false
	}
	if time.Since(lastScan) >= c.ttl {
		return nil, time.Time{}, false
	}
	return results, lastScan, true
}

// Age returns the duration since the last scan, or -1 if no cache exists.
func (c *DriftCache) Age() time.Duration {
	_, lastScan, ok := c.Load()
	if !ok {
		return -1
	}
	return time.Since(lastScan)
}

// Save persists drift scan results to disk with the current timestamp.
func (c *DriftCache) Save(results *DriftScanResults) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveLocked(results)
}

// SaveIfCurrent saves results only when the generation token matches the
// cache's current generation. Returns (false, nil) when a concurrent
// Invalidate() bumped the generation while the caller's scan was in flight —
// in that case the in-flight results are stale relative to a mutation and
// must not overwrite the invalidation.
func (c *DriftCache) SaveIfCurrent(results *DriftScanResults, gen time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.lastInvalidation.Equal(gen) {
		return false, nil
	}
	if err := c.saveLocked(results); err != nil {
		return false, err
	}
	return true, nil
}

func (c *DriftCache) saveLocked(results *DriftScanResults) error {
	if results == nil {
		return fmt.Errorf("cannot save nil results")
	}

	data := driftCacheData{
		LastScan: time.Now(),
		Results:  results,
	}
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling drift cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.filePath()), 0755); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}
	if err := os.WriteFile(c.filePath(), jsonData, 0644); err != nil {
		return fmt.Errorf("writing drift cache: %w", err)
	}
	return nil
}

// Invalidate removes the cache file for this context and bumps the generation
// token, so any concurrent in-flight scan's SaveIfCurrent will skip writing.
func (c *DriftCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastInvalidation = time.Now()
	_ = os.Remove(c.filePath())
}

// StartScan attempts to acquire the scanner lock. Returns true if the caller
// should proceed with the scan. Returns false if a scan is already in progress
// (caller should skip or return a "scan in progress" message).
func (c *DriftCache) StartScan() bool {
	return c.scanning.CompareAndSwap(false, true)
}

// EndScan releases the scanner lock. Must be called after a successful
// StartScan(), even if the scan fails.
func (c *DriftCache) EndScan() {
	c.scanning.Store(false)
}
