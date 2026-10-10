package library

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheEvictionRetainsPrimaryAndSharedBodies(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/primary")
	manual(t, s, id, "Primary immutable text")
	enableDigests(t, s)
	completeFixtureDigest(t, s)
	snapshots, _ := s.Snapshots(id)
	digest, _ := s.DigestForSnapshot(snapshots[0].ID)
	f := &Fetcher{MaxBytes: 4096, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := "shared"
		if r.URL.Path == "/different" {
			body = "different"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: ioBody(body), Request: r}, nil
	})}}
	c := &HTTPCache{Store: s, Fetcher: f, TTL: time.Hour, MaxBytes: 20, Retention: time.Hour}
	for _, path := range []string{"/a", "/b", "/different"} {
		if _, e := c.Get(context.Background(), "https://example.com"+path, false, false); e != nil {
			t.Fatal(e)
		}
	}
	// Expiring one alias must not remove the body used by another URL.
	s.DB.Exec("UPDATE cache_entries SET expires_at=0 WHERE url='https://example.com/a'")
	result, e := c.Prune(time.Now())
	if e != nil || result.Bytes != 15 {
		t.Fatal("shared byte accounting", result, e)
	}
	if r, e := c.Get(context.Background(), "https://example.com/b", false, false); e != nil || r.Status != "HIT" {
		t.Fatal("shared body removed", e)
	}
	s.DB.Exec("UPDATE cache_entries SET last_accessed=1 WHERE url='https://example.com/different'")
	c.MaxBytes = 6
	result, e = c.Prune(time.Now())
	if e != nil || result.Bytes != 6 {
		t.Fatal("budget not enforced", result, e)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM cache_entries").Scan(&count)
	if count != 1 {
		t.Fatal("LRU references not evicted")
	}
	orphan, e := s.Files.Put("cache/http", []byte("orphan body"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Prune(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(s.Files.Root, orphan)); !os.IsNotExist(e) {
		t.Fatal("orphan not removed")
	}
	if snap, e := s.Snapshot(snapshots[0].ID); e != nil || snap.Text != "Primary immutable text" {
		t.Fatal("primary text evicted", e)
	}
	if kept, e := s.Digest(digest.ID, id); e != nil || kept.Markdown != fixtureDigest {
		t.Fatal("primary digest evicted", e)
	}
	s.DB.QueryRow("SELECT count(*) FROM rating_events").Scan(&count)
	if count != 0 {
		t.Fatal("cache changed ratings")
	}
}
func TestCacheWriteFailureStillReturnsAvailableSource(t *testing.T) {
	s := testStore(t)
	os.WriteFile(filepath.Join(s.Files.Root, "cache"), []byte("blocks cache directory"), 0600)
	f := &Fetcher{MaxBytes: 4096, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: ioBody("available source despite cache failure"), Request: r}, nil
	})}}
	c := &HTTPCache{Store: s, Fetcher: f, TTL: time.Hour, MaxBytes: 4096}
	f.Cache = c
	source, e := f.Fetch(context.Background(), "https://example.com/failure")
	if e != nil || !strings.Contains(source.Text, "available source") {
		t.Fatal("cache failure lost source", e)
	}
	id := add(t, s, "https://example.com/failure")
	if e = s.Complete(claim(t, s), source); e != nil {
		t.Fatal(e)
	}
	if v, e := s.Snapshots(id); e != nil || len(v) != 1 {
		t.Fatal("source not preserved", e)
	}
}

func TestCacheCleanupRejectsDirectorySymlinks(t *testing.T) {
	s := testStore(t)
	outside := t.TempDir()
	os.Mkdir(filepath.Join(s.Files.Root, "cache"), 0700)
	name := strings.Repeat("a", 64) + ".body"
	path := filepath.Join(outside, name)
	os.WriteFile(path, []byte("outside file"), 0600)
	if e := os.Symlink(outside, filepath.Join(s.Files.Root, "cache", "http")); e != nil {
		t.Fatal(e)
	}
	c := HTTPCache{Store: s, MaxBytes: 1}
	if _, e := c.Prune(time.Now()); e == nil {
		t.Fatal("symlinked directory followed")
	}
	if data, e := os.ReadFile(path); e != nil || string(data) != "outside file" {
		t.Fatal("outside file changed", e)
	}
}
