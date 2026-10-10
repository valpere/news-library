package library

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
)

type cacheMetaKey struct{}
type cacheRefreshKey struct{}
type NetworkMeta struct {
	Headers                http.Header
	FetchedAt, CacheStatus string
}
type CacheResponse struct {
	Body                                      []byte
	URL, ContentType, Hash, FetchedAt, Status string
	Partial                                   bool
	ExpiresAt                                 int64
}
type HTTPCache struct {
	Store   *Store
	Fetcher *Fetcher
	TTL     time.Duration
	group   singleflight.Group
}

func (f *Fetcher) get(ctx context.Context, raw string) ([]byte, *url.URL, string, bool, error) {
	meta, _ := ctx.Value(cacheMetaKey{}).(*NetworkMeta)
	if f.Cache == nil {
		return f.getNetwork(ctx, raw, meta)
	}
	force, _ := ctx.Value(cacheRefreshKey{}).(bool)
	r, err := f.Cache.Get(ctx, raw, force, false)
	if err != nil {
		return nil, nil, "", false, err
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		return nil, nil, "", false, err
	}
	if meta != nil {
		meta.FetchedAt = r.FetchedAt
		meta.CacheStatus = r.Status
	}
	return r.Body, u, r.ContentType, r.Partial, nil
}
func (c *HTTPCache) lookup(raw string) (CacheResponse, error) {
	var r CacheResponse
	var path string
	err := c.Store.DB.QueryRow(`SELECT final_url,content_type,body_path,hash,fetched_at,expires_at,partial FROM cache_entries WHERE url=?`, raw).Scan(&r.URL, &r.ContentType, &path, &r.Hash, &r.FetchedAt, &r.ExpiresAt, &r.Partial)
	if err != nil {
		return r, err
	}
	r.Body, err = c.Store.Files.Read(path, r.Hash)
	return r, err
}
func cacheLifetime(headers http.Header, limit time.Duration, now time.Time) time.Duration {
	if len(headers.Values("Set-Cookie")) > 0 {
		return 0
	}
	for _, v := range strings.Split(strings.Join(headers.Values("Vary"), ","), ",") {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "accept-encoding", "user-agent", "accept":
		default:
			return 0
		}
	}
	lifetime := limit
	hasMaxAge := false
	for _, directive := range strings.Split(strings.Join(headers.Values("Cache-Control"), ","), ",") {
		parts := strings.SplitN(strings.TrimSpace(strings.ToLower(directive)), "=", 2)
		switch parts[0] {
		case "no-store", "no-cache", "private":
			return 0
		case "max-age":
			if len(parts) != 2 {
				return 0
			}
			seconds, err := strconv.ParseInt(strings.Trim(parts[1], `"`), 10, 32)
			if err != nil || seconds <= 0 {
				return 0
			}
			age, _ := strconv.ParseInt(headers.Get("Age"), 10, 32)
			duration := time.Duration(seconds-max(age, 0)) * time.Second
			if duration < lifetime {
				lifetime = duration
			}
			hasMaxAge = true
		}
	}
	if !hasMaxAge {
		if raw := headers.Get("Expires"); raw != "" {
			expires, err := http.ParseTime(raw)
			if err != nil {
				return 0
			}
			if duration := expires.Sub(now); duration < lifetime {
				lifetime = duration
			}
		}
	}
	return lifetime
}
func (c *HTTPCache) Get(ctx context.Context, raw string, force, allowStale bool) (CacheResponse, error) {
	canonical, err := CanonicalURL(raw)
	if err != nil {
		return CacheResponse{}, &SourceError{Message: err.Error()}
	}
	key := canonical
	if force {
		key += "|refresh"
	}
	if allowStale {
		key += "|stale"
	}
	result := c.group.DoChan(key, func() (any, error) {
		cached, cachedErr := c.lookup(canonical)
		if cachedErr == nil && !force {
			if cached.ExpiresAt > time.Now().UnixMilli() {
				cached.Status = "HIT"
				return cached, nil
			}
			if allowStale {
				cached.Status = "STALE"
				return cached, nil
			}
		}
		meta := &NetworkMeta{}
		body, final, kind, partial, err := c.Fetcher.getNetwork(ctx, canonical, meta)
		if err != nil {
			return CacheResponse{}, err
		}
		r := CacheResponse{Body: body, URL: final.String(), ContentType: kind, Partial: partial, Hash: fmt.Sprintf("%x", sha256.Sum256(body)), FetchedAt: meta.FetchedAt, Status: "MISS"}
		lifetime := cacheLifetime(meta.Headers, c.TTL, time.Now())
		if lifetime <= 0 || partial {
			r.Status = "BYPASS"
			if _, e := c.Store.DB.Exec("DELETE FROM cache_entries WHERE url=?", canonical); e != nil {
				return CacheResponse{}, e
			}
			return r, nil
		}
		r.ExpiresAt = time.Now().Add(lifetime).UnixMilli()
		path, err := c.Store.Files.Put("cache/http", body)
		if err != nil {
			return CacheResponse{}, err
		}
		_, err = c.Store.DB.Exec(`INSERT INTO cache_entries(url,final_url,content_type,body_path,hash,fetched_at,expires_at,partial) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(url) DO UPDATE SET final_url=excluded.final_url,content_type=excluded.content_type,body_path=excluded.body_path,hash=excluded.hash,fetched_at=excluded.fetched_at,expires_at=excluded.expires_at,partial=excluded.partial`, canonical, r.URL, r.ContentType, path, r.Hash, r.FetchedAt, r.ExpiresAt, partial)
		return r, err
	})
	select {
	case <-ctx.Done():
		return CacheResponse{}, ctx.Err()
	case response := <-result:
		if response.Err != nil {
			return CacheResponse{}, response.Err
		}
		return response.Val.(CacheResponse), nil
	}
}

// Browser requests cannot use the shared cache to run unsolicited fetches.
// Curl and local tools need no extra credentials on the loopback listener.
func (w *Web) cache(rw http.ResponseWriter, r *http.Request) {
	if w.Cache == nil {
		http.Error(rw, "HTTP-кеш не налаштовано", 503)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
		http.Error(rw, "сторонній Origin", 403)
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		http.Error(rw, "сторонній browser запит", 403)
		return
	}
	response, err := w.Cache.Get(r.Context(), r.URL.Query().Get("url"), r.URL.Query().Get("refresh") == "1", r.URL.Query().Get("stale") == "1")
	if err != nil {
		var source *SourceError
		if errors.As(err, &source) {
			http.Error(rw, source.Message, 502)
		} else {
			http.Error(rw, "кеш недоступний", 502)
		}
		return
	}
	kind := response.ContentType
	if kind == "" {
		kind = "application/octet-stream"
	}
	rw.Header().Set("Content-Type", kind)
	// A raw cached page is untrusted. It must never run as application-origin HTML.
	rw.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; frame-ancestors 'none'")
	rw.Header().Set("Content-Disposition", `attachment; filename="source"`)
	rw.Header().Set("X-News-Library-Cache", response.Status)
	rw.Header().Set("X-Source-URL", response.URL)
	rw.Header().Set("X-Source-Fetched-At", response.FetchedAt)
	if response.Partial {
		rw.Header().Set("X-Content-Completeness", "partial")
	} else {
		rw.Header().Set("X-Content-Completeness", "full")
	}
	rw.Header().Set("ETag", `"`+response.Hash+`"`)
	if r.Header.Get("If-None-Match") == `"`+response.Hash+`"` {
		rw.WriteHeader(http.StatusNotModified)
		return
	}
	rw.Write(response.Body)
}
