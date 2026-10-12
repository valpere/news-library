package library

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRevalidationPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	calls := 0
	f := &Fetcher{MaxBytes: 4096, client: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		h := http.Header{}
		h.Set("ETag", `"first"`)
		h.Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		h.Set("Cache-Control", "max-age=120")
		h.Set("Content-Type", "text/plain")
		status, body := 200, "original source"
		if calls > 1 {
			if req.Header.Get("If-None-Match") != `"first"` || req.Header.Get("If-Modified-Since") != h.Get("Last-Modified") {
				t.Error("lost persisted validators", req.Header)
			}
			status, body = 304, ""
			h.Del("Cache-Control") // Inherit the original storage policy.
		}
		return &http.Response{StatusCode: status, Header: h, Body: ioBody(body), Request: req}, nil
	})}}
	c := &HTTPCache{Store: s, Fetcher: f, TTL: time.Hour}
	raw := "https://example.com/article"
	first, err := c.Get(context.Background(), raw, false, false)
	if err != nil {
		t.Fatal(err)
	}
	// Stable fixture provenance makes it clear that 304 does not re-fetch the body.
	first.FetchedAt = "2020-01-01T00:00:00Z"
	if _, err = s.DB.Exec("UPDATE cache_entries SET expires_at=0,fetched_at=?", first.FetchedAt); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Store = s
	checked, err := c.Get(context.Background(), raw, false, false)
	if err != nil || checked.Status != "REVALIDATED" || checked.Hash != first.Hash || string(checked.Body) != "original source" || checked.FetchedAt != first.FetchedAt || checked.CheckedAt == first.FetchedAt || checked.ExpiresAt <= time.Now().UnixMilli() {
		t.Fatal(checked, err)
	}
	hit, err := c.Get(context.Background(), raw, false, false)
	if err != nil || hit.Status != "HIT" || calls != 2 || hit.CheckedAt != checked.CheckedAt {
		t.Fatal(hit, err, calls)
	}
	w, err := NewWeb(s, "http://localhost:8787", 4096)
	if err != nil {
		t.Fatal(err)
	}
	w.Cache = c
	response := webRequest(w, "GET", "/cache?url=https%3A%2F%2Fexample.com%2Farticle", nil, nil)
	if response.Code != 200 || response.Header().Get("X-Source-Fetched-At") != first.FetchedAt || response.Header().Get("X-Source-Checked-At") != checked.CheckedAt {
		t.Fatal(response.Code, response.Header())
	}
	var count int
	for _, table := range []string{"items", "jobs", "rating_events"} {
		if err = s.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
}

func TestCacheRevalidationChangedBodyAndUnsafePolicy(t *testing.T) {
	for _, policy := range []string{"", "no-store", "private", "no-cache", "cookie", "vary"} {
		t.Run(policy, func(t *testing.T) {
			s := testStore(t)
			calls := 0
			f := &Fetcher{MaxBytes: 4096, client: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				h := http.Header{}
				h.Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
				h.Set("Content-Type", "text/plain")
				code, body := 200, "old body"
				if calls > 1 {
					if req.Header.Get("If-Modified-Since") == "" || req.Header.Get("If-None-Match") != "" {
						t.Error("last-modified validator missing")
					}
					if policy == "" {
						body = "changed body"
						h.Set("ETag", `"new"`)
					} else {
						code, body = 304, ""
						switch policy {
						case "cookie":
							h.Set("Set-Cookie", "session=x")
						case "vary":
							h.Set("Vary", "Cookie")
						default:
							h.Set("Cache-Control", policy)
						}
					}
				}
				return &http.Response{StatusCode: code, Header: h, Body: ioBody(body), Request: req}, nil
			})}}
			c := &HTTPCache{Store: s, Fetcher: f, TTL: time.Hour}
			raw := "https://example.com/policy"
			if _, err := c.Get(context.Background(), raw, false, false); err != nil {
				t.Fatal(err)
			}
			got, err := c.Get(context.Background(), raw, true, false)
			if err != nil {
				t.Fatal(err)
			}
			var count int
			s.DB.QueryRow("SELECT count(*) FROM cache_entries").Scan(&count)
			if policy == "" {
				if got.Status != "MISS" || string(got.Body) != "changed body" || count != 1 {
					t.Fatal(got, count)
				}
			} else {
				if got.Status != "BYPASS" || string(got.Body) != "old body" || count != 0 {
					t.Fatal(got, count)
				}
			}
		})
	}
}

func TestCacheRevalidationRefusesMissingBodyAndUnexpected304(t *testing.T) {
	s := testStore(t)
	calls := 0
	f := &Fetcher{MaxBytes: 4096, client: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		h := http.Header{}
		h.Set("ETag", `"one"`)
		code := 200
		if calls == 2 {
			if req.Header.Get("If-None-Match") != "" {
				t.Error("sent validator without verified body")
			}
			code = 304
		}
		return &http.Response{StatusCode: code, Header: h, Body: ioBody("source"), Request: req}, nil
	})}}
	c := &HTTPCache{Store: s, Fetcher: f, TTL: time.Hour}
	raw := "https://example.com/missing"
	if _, err := c.Get(context.Background(), raw, false, false); err != nil {
		t.Fatal(err)
	}
	var path string
	s.DB.QueryRow("SELECT body_path FROM cache_entries").Scan(&path)
	if err := os.Remove(filepath.Join(s.Files.Root, path)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), raw, false, false); err == nil {
		t.Fatal("unexpected 304 reused missing body")
	}
	recovered, err := c.Get(context.Background(), raw, false, false)
	if err != nil || recovered.Status != "MISS" || string(recovered.Body) != "source" {
		t.Fatal(recovered, err)
	}
	c.TTL = 0
	if _, err = c.Get(context.Background(), raw, false, false); err != nil {
		t.Fatal(err)
	}
}

func TestCacheValidatorsFollowOnlyExactSavedRedirectTarget(t *testing.T) {
	s := testStore(t)
	target := "https://example.com/final"
	calls := 0
	f := NewFetcher(time.Second, 4096)
	f.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		h := http.Header{}
		code, body := 200, "body"
		if req.URL.Path == "/start" {
			if req.Header.Get("If-None-Match") != "" {
				t.Error("validator leaked to redirect origin")
			}
			code, body = 302, ""
			h.Set("Location", target)
		} else {
			h.Set("ETag", `"one"`)
			if calls > 2 && req.URL.String() == "https://example.com/final" {
				if req.Header.Get("If-None-Match") != `"one"` {
					t.Error("missing target validator")
				}
				code, body = 304, ""
			} else if req.Header.Get("If-None-Match") != "" {
				t.Error("validator leaked to changed redirect target")
			}
		}
		return &http.Response{StatusCode: code, Header: h, Body: ioBody(body), Request: req}, nil
	})
	c := &HTTPCache{Store: s, Fetcher: f, TTL: time.Hour}
	raw := "https://example.com/start"
	for _, want := range []string{"MISS", "REVALIDATED", "MISS"} {
		got, err := c.Get(context.Background(), raw, true, false)
		if err != nil || got.Status != want {
			t.Fatal(got, err)
		}
		if want == "REVALIDATED" {
			target = "https://different.example/new"
		}
	}
}

func TestCacheMismatched304ValidatorRequiresFullResponse(t *testing.T) {
	s := testStore(t)
	calls := 0
	f := &Fetcher{MaxBytes: 4096, client: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		h := http.Header{}
		h.Set("ETag", `"first"`)
		code, body := 200, "old"
		if calls == 2 {
			code, body = 304, ""
			h.Set("ETag", `"different"`)
		}
		if calls == 3 {
			if req.Header.Get("If-None-Match") != "" {
				t.Error("fallback remained conditional")
			}
			body = "new"
			h.Set("ETag", `"different"`)
		}
		return &http.Response{StatusCode: code, Header: h, Body: ioBody(body), Request: req}, nil
	})}}
	c := &HTTPCache{Store: s, Fetcher: f, TTL: time.Hour}
	raw := "https://example.com/mismatch"
	if _, err := c.Get(context.Background(), raw, false, false); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(context.Background(), raw, true, false)
	if err != nil || got.Status != "MISS" || string(got.Body) != "new" || calls != 3 {
		t.Fatal(got, err, calls)
	}
}

func TestCacheRevalidationFailureKeepsExplicitStaleAndPruneCanRun(t *testing.T) {
	s := testStore(t)
	calls := 0
	var f *Fetcher
	f = &Fetcher{MaxBytes: 4096, client: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		h := http.Header{}
		h.Set("ETag", `"source"`)
		code, body := 200, "body"
		if calls == 2 {
			code, body = 503, ""
		}
		if calls == 3 {
			// Cleanup can remove a cache entry while validation is in flight. The
			// verified body held in memory must be republished before new metadata.
			if _, err := s.DB.Exec("UPDATE cache_entries SET expires_at=0"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.Cache.Prune(time.Now()); err != nil {
				t.Fatal(err)
			}
			code, body = 304, ""
		}
		return &http.Response{StatusCode: code, Header: h, Body: ioBody(body), Request: req}, nil
	})}}
	c := &HTTPCache{Store: s, Fetcher: f, TTL: time.Hour, MaxBytes: 4096}
	f.Cache = c
	raw := "https://example.com/failure"
	if _, err := c.Get(context.Background(), raw, false, false); err != nil {
		t.Fatal(err)
	}
	s.DB.Exec("UPDATE cache_entries SET expires_at=0")
	if _, err := c.Get(context.Background(), raw, false, false); err == nil {
		t.Fatal("upstream failure silently served stale")
	}
	got, err := c.Get(context.Background(), raw, false, true)
	if err != nil || got.Status != "STALE" || calls != 2 {
		t.Fatal(got, err, calls)
	}
	got, err = c.Get(context.Background(), raw, false, false)
	if err != nil || got.Status != "REVALIDATED" || string(got.Body) != "body" {
		t.Fatal(got, err)
	}
	hit, err := c.Get(context.Background(), raw, false, false)
	if err != nil || hit.Status != "HIT" || calls != 3 {
		t.Fatal(hit, err, calls)
	}
}

func TestCacheV5MigrationPreservesBodyWithoutInventingValidators(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	bodyPath, err := s.Files.Put("cache/http", []byte("old body"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP TABLE knowledge_batches",
		"DROP TABLE knowledge_failures",
		"DROP TABLE article_knowledge",
		"DROP TABLE knowledge_runs",
		"DROP TABLE job_failures",
		"DROP TABLE connection_members",
		"DROP TABLE connection_versions",
		"DROP TABLE connection_notes",
		"DROP TABLE delivery_parts",
		"DROP TABLE deliveries",
		"DROP TABLE embedding_chunks",
		"DROP TABLE embedding_runs",
		"ALTER TABLE cache_entries DROP COLUMN response_headers",
		"ALTER TABLE cache_entries DROP COLUMN checked_at",
		"PRAGMA user_version=5",
	} {
		if _, err = s.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	hash := filepath.Base(bodyPath)[:64]
	if _, err = s.DB.Exec(`INSERT INTO cache_entries(url,final_url,content_type,body_path,hash,fetched_at,expires_at,partial) VALUES(?,?,?,?,?,?,?,0)`, "https://example.com/legacy", "https://example.com/legacy", "text/plain", bodyPath, hash, "2020-01-01T00:00:00Z", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := &HTTPCache{Store: s, TTL: time.Hour}
	got, err := c.Get(context.Background(), "https://example.com/legacy", false, false)
	if err != nil || got.Status != "HIT" || string(got.Body) != "old body" || len(got.Headers) != 0 || got.CheckedAt != "" {
		t.Fatal(got, err)
	}
}
