package library

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSharedCacheLifecycleAndBoundaries(t *testing.T) {
	s, w := testWeb(t)
	var calls atomic.Int32
	var bypass atomic.Bool
	f := &Fetcher{MaxBytes: 4096, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		control := "max-age=3600"
		if bypass.Load() {
			control = "no-store"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}, "Cache-Control": {control}}, Body: ioBody("<script>alert(1)</script> cached source"), Request: r}, nil
	})}}
	c := &HTTPCache{Store: s, Fetcher: f, TTL: time.Hour}
	f.Cache = c
	w.Cache = c
	raw := "https://example.com/cache"
	r, e := c.Get(context.Background(), raw, false, false)
	if e != nil || r.Status != "MISS" {
		t.Fatal(r, e)
	}
	got, e := c.Get(context.Background(), raw, false, false)
	if e != nil || got.Status != "HIT" || calls.Load() != 1 || got.FetchedAt != r.FetchedAt {
		t.Fatal("cache hit", e)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, e := c.Get(context.Background(), raw, false, false); e != nil || r.Status != "HIT" {
				t.Error(r, e)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("hit fetched network")
	}
	s.DB.Exec("UPDATE cache_entries SET expires_at=0")
	got, e = c.Get(context.Background(), raw, false, true)
	if e != nil || got.Status != "STALE" || calls.Load() != 1 {
		t.Fatal("explicit stale", e)
	}
	got, e = c.Get(context.Background(), raw, false, false)
	if e != nil || got.Status != "MISS" || calls.Load() != 2 {
		t.Fatal("expiry", e)
	}
	got, e = c.Get(context.Background(), raw, true, false)
	if e != nil || got.Status != "MISS" || calls.Load() != 3 {
		t.Fatal("refresh", e)
	}
	var path string
	s.DB.QueryRow("SELECT body_path FROM cache_entries WHERE url=?", raw).Scan(&path)
	os.WriteFile(filepath.Join(s.Files.Root, path), []byte("corrupt"), 0600)
	got, e = c.Get(context.Background(), raw, false, false)
	if e != nil || got.Status != "MISS" || calls.Load() != 4 {
		t.Fatal("cache corruption recovery", e)
	}
	rw := webRequest(w, "GET", "/cache?url=https%3A%2F%2Fexample.com%2Fcache", nil, nil)
	if rw.Code != 200 || rw.Header().Get("X-News-Library-Cache") != "HIT" || !strings.Contains(rw.Header().Get("Content-Security-Policy"), "sandbox") || !strings.Contains(rw.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("unsafe cache response", rw.Code, rw.Header())
	}
	rw = webRequest(w, "GET", "/cache?url=https%3A%2F%2Fexample.com%2Fcache", nil, map[string]string{"If-None-Match": rw.Header().Get("ETag")})
	if rw.Code != 304 || rw.Body.Len() != 0 {
		t.Fatal("cache validator")
	}
	rw = webRequest(w, "GET", "/cache?url=https%3A%2F%2Fexample.com%2Fcache", nil, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if rw.Code != 403 {
		t.Fatal("unsolicited browser fetch")
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM items").Scan(&count)
	if count != 0 {
		t.Fatal("cache created item")
	}
	s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&count)
	if count != 0 {
		t.Fatal("cache scheduled AI")
	}
	bypass.Store(true)
	for range 2 {
		r, e = c.Get(context.Background(), raw, true, false)
		if e != nil || r.Status != "BYPASS" {
			t.Fatal("no-store", e)
		}
	}
	s.DB.QueryRow("SELECT count(*) FROM cache_entries").Scan(&count)
	if count != 0 {
		t.Fatal("no-store left stale cache")
	}
	c.Fetcher = NewFetcher(time.Second, 4096)
	if _, e = c.Get(context.Background(), "http://127.0.0.1/private", false, false); e == nil {
		t.Fatal("cache SSRF")
	}
}
func TestConcurrentCacheMissSingleFetch(t *testing.T) {
	s := testStore(t)
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	f := &Fetcher{MaxBytes: 4096, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: ioBody("source"), Request: r}, nil
	})}}
	c := &HTTPCache{Store: s, Fetcher: f, TTL: time.Hour}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := c.Get(context.Background(), "https://example.com/concurrent", false, false); e != nil {
				t.Error(e)
			}
		}()
	}
	<-entered
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicate fetches", calls.Load())
	}
}
func TestCacheUpstreamPrivacyDirectives(t *testing.T) {
	for _, h := range []http.Header{{"Cache-Control": {"private"}}, {"Set-Cookie": {"session=x"}}, {"Cache-Control": {"no-cache"}}, {"Cache-Control": {"max-age=60", "no-store"}}, {"Vary": {"Accept", "Cookie"}}, {"Cache-Control": {"max-age=60"}, "Age": {"61"}}, {"Vary": {"*"}}} {
		if cacheLifetime(h, time.Hour, time.Now()) > 0 {
			t.Fatal("private response cached", fmt.Sprint(h))
		}
	}
}
