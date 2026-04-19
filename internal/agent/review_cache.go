/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const reviewCacheDir = ".kodrun/cache/review"

// ReviewCacheEntry stores a cached per-file review result.
type ReviewCacheEntry struct {
	Key       string    `json:"key"`
	FilePath  string    `json:"file_path"`
	ModTime   time.Time `json:"mod_time"`
	Findings  string    `json:"findings"`
	CreatedAt time.Time `json:"created_at"`
}

// ReviewCache persists per-file review results to disk so unchanged files
// are not re-reviewed on subsequent runs.
type ReviewCache struct {
	dir string
}

// NewReviewCache creates a review cache rooted at workDir/.kodrun/cache/review.
func NewReviewCache(workDir string) *ReviewCache {
	return &ReviewCache{dir: filepath.Join(workDir, reviewCacheDir)}
}

// Get returns cached findings for the given key.
func (c *ReviewCache) Get(key string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(c.dir, key+".json"))
	if err != nil {
		return "", false
	}
	var entry ReviewCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return "", false
	}
	return entry.Findings, true
}

// Put stores findings under the given key.
const (
	reviewCacheDirPerm  = 0o750
	reviewCacheFilePerm = 0o600
)

func (c *ReviewCache) Put(key string, entry *ReviewCacheEntry) error {
	if err := os.MkdirAll(c.dir, reviewCacheDirPerm); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.dir, key+".json"), data, reviewCacheFilePerm)
}

// ReviewCacheKey computes a deterministic cache key from file path, modification
// time, RAG context, and dependency signatures. Any change invalidates the cache.
func ReviewCacheKey(filePath string, modTime time.Time, ragBlock, depSigs string) string {
	h := sha256.New()
	h.Write([]byte(filePath))
	h.Write([]byte(modTime.Format(time.RFC3339Nano)))
	h.Write([]byte(ragBlock))
	h.Write([]byte(depSigs))
	return hex.EncodeToString(h.Sum(nil))[:16]
}
