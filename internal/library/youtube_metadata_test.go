package library

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestYouTubeLanguageAndFormatFailover(t *testing.T) {
	calls := []string{}
	f := &Fetcher{MaxBytes: 4096, YouTubeLanguages: []string{"uk", "en"}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Path)
		body := `{"events":[{"segs":[{"utf8":"Англійська резервна доріжка"}]}]}`
		if r.URL.Host == "www.youtube.com" {
			body = `{"captionTracks":[{"baseUrl":"https://captions.example/en","languageCode":"en","kind":"asr"},{"baseUrl":"https://captions.example/uk","languageCode":"uk","kind":"asr"}]}`
		} else if r.URL.Path == "/uk" {
			body = ""
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: ioBody(body), Request: r}, nil
	})}}
	s, err := f.Fetch(context.Background(), "https://youtu.be/abcdefghijk")
	if err != nil || !strings.Contains(s.Warning, "en; автоматичні") || strings.Join(calls, ",") != "/watch,/uk,/en" {
		t.Fatal(s, err, calls)
	}
}
func TestYouTubeMetadataFallbackPreservesUkrainianAndProvenance(t *testing.T) {
	tracks := metadataTracks(youtubeMetadata{Automatic: map[string][]youtubeFormat{"uk": {{URL: "https://captions.example/xml", Ext: "srv3"}, {URL: "https://captions.example/json", Ext: "json3"}}, "en": {{URL: "https://captions.example/en", Ext: "srv3"}}, "live_chat": {{URL: "https://captions.example/chat", Ext: "json3"}}}})
	if len(tracks) != 2 {
		t.Fatal("formats exhausted language attempts", tracks)
	}
	for _, track := range tracks {
		if track.LanguageCode == "uk" && track.BaseURL != "https://captions.example/json" {
			t.Fatal("JSON3 not preferred", track)
		}
	}
	metadataCalls := 0
	f := &Fetcher{MaxBytes: 4096, YouTubeLanguages: []string{"uk", "en"}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		if r.URL.Path == "/resolved" {
			body = `{"events":[{"segs":[{"utf8":"Українська доступна доріжка"}]}]}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: ioBody(body), Request: r}, nil
	})}, metadataResolver: func(ctx context.Context, id string) (youtubeMetadata, error) {
		metadataCalls++
		if id != "abcdefghijk" {
			t.Fatal(id)
		}
		return youtubeMetadata{Title: "Справжня назва", Automatic: map[string][]youtubeFormat{"uk": {{URL: "https://captions.example/resolved", Ext: "json3"}}, "live_chat": {{URL: "https://captions.example/chat", Ext: "json3"}}}}, nil
	}}
	s, err := f.Fetch(context.Background(), "https://youtu.be/abcdefghijk")
	if err != nil || metadataCalls != 1 || s.Extractor != "youtube-yt-dlp-metadata-v1" || s.Title != "Справжня назва" || s.Completeness != "full" || !strings.Contains(s.Warning, "uk; автоматичні") {
		t.Fatal(s, err, metadataCalls)
	}
	if _, err = f.Fetch(context.Background(), "https://example.com/page"); metadataCalls != 1 {
		t.Fatal("metadata fallback for non-YouTube", err)
	}
}
func TestMetadataProxyRejectsPrivateTargets(t *testing.T) {
	proxy, closeProxy, err := startMetadataProxy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeProxy()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	for _, target := range []string{"127.0.0.1:443", "[::1]:443", "localhost:443", "example.com:22"} {
		req, _ := http.NewRequest("CONNECT", proxy, nil)
		req.Host = target
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatal(target, response.StatusCode)
		}
	}
	// Use an actual HTTP proxy request for an absolute private URL.
	parsedProxy, _ := url.Parse(proxy)
	transport := &http.Transport{Proxy: http.ProxyURL(parsedProxy)}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Timeout: time.Second, Transport: transport}).Get("http://127.0.0.1/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
}
func TestMetadataCommandBoundsAndCancellation(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "fixture-helper")
	// Trusted fixture, no shell interpolation of source or user input.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '{\"title\":\"fixture\",\"subtitles\":{},\"automatic_captions\":{}}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f := NewFetcher(time.Second, 4096)
	f.YTDLP = script
	info, err := f.youtubeMetadata(context.Background(), "abcdefghijk")
	if err != nil || info.Title != "fixture" {
		t.Fatal(info, err)
	}
	if _, err = f.youtubeMetadata(context.Background(), "bad; id"); err == nil {
		t.Fatal("invalid ID reached executable")
	}
	out := &cappedOutput{limit: 3}
	if _, err = out.Write([]byte("1234")); err == nil || out.Len() != 0 {
		t.Fatal("unbounded output")
	}
	if err = os.WriteFile(script, []byte("#!/bin/sh\nsleep 20\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err = f.youtubeMetadata(ctx, "abcdefghijk"); err == nil || time.Since(start) > 2*time.Second {
		t.Fatal("helper did not terminate", err)
	}
	for _, raw := range []string{"uk,en", "ru,en-orig"} {
		if _, err := ParseYouTubeLanguages(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"", "uk,", "en; command"} {
		if _, err := ParseYouTubeLanguages(raw); err == nil {
			t.Fatal("bad languages accepted", raw)
		}
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal(ctx.Err())
	}
}
