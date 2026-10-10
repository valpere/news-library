package library

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type CacheCleanup struct {
	RemovedFiles int
	Bytes        int64
}

// Serialize lookup/read, publication, and eviction within this one service.
func (c *HTTPCache) Prune(now time.Time) (CacheCleanup, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pruneLocked(now)
}
func cacheBodyName(name string) bool {
	if len(name) != 69 || !strings.HasSuffix(name, ".body") {
		return false
	}
	for _, r := range name[:64] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func (c *HTTPCache) pruneLocked(now time.Time) (CacheCleanup, error) {
	var result CacheCleanup
	if err := c.checkCacheDirectory(); err != nil {
		return result, err
	}
	if _, err := c.Store.DB.Exec("DELETE FROM cache_entries WHERE expires_at<=?", now.Add(-c.Retention).UnixMilli()); err != nil {
		return result, err
	}
	rows, err := c.Store.DB.Query("SELECT body_path,max(last_accessed) FROM cache_entries GROUP BY body_path")
	if err != nil {
		return result, err
	}
	references := map[string]int64{}
	for rows.Next() {
		var path string
		var accessed int64
		if err = rows.Scan(&path, &accessed); err != nil {
			rows.Close()
			return result, err
		}
		references[path] = accessed
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	dir := filepath.Join(c.Store.Files.Root, "cache", "http")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	type bodyFile struct {
		path, relative string
		size, accessed int64
	}
	var files []bodyFile
	for _, entry := range entries {
		// Only our content-addressed regular cache bodies are eligible. Unknown
		// files, symlinks, directories and every primary namespace are left alone.
		if !cacheBodyName(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return result, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		relative := filepath.Join("cache", "http", entry.Name())
		path := filepath.Join(dir, entry.Name())
		accessed, referenced := references[relative]
		if !referenced {
			if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return result, err
			}
			result.RemovedFiles++
			continue
		}
		if accessed == 0 {
			accessed = info.ModTime().UnixMilli()
		}
		files = append(files, bodyFile{path, relative, info.Size(), accessed})
		result.Bytes += info.Size()
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].accessed == files[j].accessed {
			return files[i].relative < files[j].relative
		}
		return files[i].accessed < files[j].accessed
	})
	if c.MaxBytes > 0 {
		for _, file := range files {
			if result.Bytes <= c.MaxBytes {
				break
			}
			// Remove all references to a shared body before deleting it. A later
			// network fetch can repair a crash between metadata and file deletion.
			if _, err = c.Store.DB.Exec("DELETE FROM cache_entries WHERE body_path=?", file.relative); err != nil {
				return result, err
			}
			if err = os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return result, err
			}
			result.RemovedFiles++
			result.Bytes -= file.size
		}
	}
	return result, nil
}
func (c *HTTPCache) RunCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if _, err := c.Prune(now); err != nil {
				log.Print("HTTP cache cleanup failed")
			}
		}
	}
}

func (c *HTTPCache) checkCacheDirectory() error {
	for _, relative := range []string{"cache", filepath.Join("cache", "http")} {
		info, err := os.Lstat(filepath.Join(c.Store.Files.Root, relative))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return errors.New("cache directory is not a regular directory")
		}
	}
	return nil
}
