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

func TestYouTubeFailureNamesYTDLPCause(t *testing.T) {
	f := NewFetcher(time.Second, 4096)
	missing := filepath.Join(t.TempDir(), "missing-yt-dlp")
	f.YTDLP = missing
	if _, err := f.youtubeMetadata(context.Background(), "abcdefghijk"); err == nil || !strings.Contains(err.Error(), "не знайдено") || !strings.Contains(err.Error(), missing) {
		t.Fatal("missing executable not named", err)
	}
	root := t.TempDir()
	for name, c := range map[string]struct {
		body string
		mode os.FileMode
		want string
	}{
		"not-executable": {"#!/bin/sh\n", 0600, "не вдалося запустити"},
		"no-interpreter": {"#!" + filepath.Join(root, "missing-python") + "\n", 0700, "не вдалося запустити"},
		"exit-code":      {"#!/bin/sh\nexit 3\n", 0700, "завершився з помилкою"},
		"bad-json":       {"#!/bin/sh\nprintf banner\n", 0700, "некоректні metadata"},
	} {
		script := filepath.Join(root, name)
		if err := os.WriteFile(script, []byte(c.body), c.mode); err != nil {
			t.Fatal(err)
		}
		f.YTDLP = script
		if _, err := f.youtubeMetadata(context.Background(), "abcdefghijk"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	noCaptions := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: ioBody("<html></html>"), Request: r}, nil
	})}
	f = &Fetcher{MaxBytes: 4096, client: noCaptions, metadataResolver: func(context.Context, string) (youtubeMetadata, error) {
		return youtubeMetadata{}, errors.New("yt-dlp не знайдено: fixture")
	}}
	_, err := f.Fetch(context.Background(), "https://youtu.be/abcdefghijk")
	if err == nil || !strings.Contains(err.Error(), "yt-dlp не знайдено: fixture") {
		t.Fatal("resolver cause dropped", err)
	}
	f = &Fetcher{MaxBytes: 4096, client: noCaptions}
	if _, err = f.Fetch(context.Background(), "https://youtu.be/abcdefghijk"); err == nil || !strings.Contains(err.Error(), "yt-dlp вимкнено") {
		t.Fatal("disabled fallback not stated", err)
	}
}

func TestSourceErrorsDoNotOfferManualText(t *testing.T) {
	for _, file := range []string{"source.go", "source_browser.go", "pages.html"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "текст вручну") || strings.Contains(string(data), "вставити вручну") {
			t.Errorf("%s still offers manual text", file)
		}
	}
}

func TestYouTubeMetadataSkipsMachineTranslations(t *testing.T) {
	tracks := metadataTracks(youtubeMetadata{Automatic: map[string][]youtubeFormat{
		"en":      {{URL: "https://captions.example/en?tlang=en", Ext: "json3"}, {URL: "https://captions.example/en", Ext: "json3"}},
		"en-orig": {{URL: "https://captions.example/orig", Ext: "json3"}},
		"uk":      {{URL: "https://captions.example/uk?tlang=uk", Ext: "json3"}},
	}})
	got := []string{}
	for _, track := range tracks {
		got = append(got, track.LanguageCode+"="+track.BaseURL)
	}
	if strings.Join(got, ",") != "en=https://captions.example/en,en-orig=https://captions.example/orig" {
		t.Fatal("machine translation offered as a track", got)
	}
}

func TestYouTubeCaptionRateLimitStopsFailover(t *testing.T) {
	captionCalls, resolverCalls := 0, 0
	f := &Fetcher{MaxBytes: 4096, YouTubeLanguages: []string{"uk", "en"}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "www.youtube.com" {
			body := `{"captionTracks":[{"baseUrl":"https://captions.example/uk","languageCode":"uk"},{"baseUrl":"https://captions.example/en","languageCode":"en"}]}`
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: ioBody(body), Request: r}, nil
		}
		captionCalls++
		return &http.Response{StatusCode: 429, Header: http.Header{}, Body: ioBody(""), Request: r}, nil
	})}, metadataResolver: func(context.Context, string) (youtubeMetadata, error) {
		resolverCalls++
		return youtubeMetadata{}, nil
	}}
	_, err := f.Fetch(context.Background(), "https://youtu.be/abcdefghijk")
	var se *SourceError
	if !errors.As(err, &se) || !se.Retry || !strings.Contains(se.Message, "429") || captionCalls != 1 || resolverCalls != 0 {
		t.Fatal("429 did not stop caption failover", err, captionCalls, resolverCalls)
	}
}

func TestYouTubeShortTrackIsPartial(t *testing.T) {
	caption := `{"events":[{"segs":[{"utf8":"лише кілька слів із довгого відео"}]}]}`
	page := `{"lengthSeconds":"600","captionTracks":[{"baseUrl":"https://captions.example/uk","languageCode":"uk"}]}`
	f := &Fetcher{MaxBytes: 4096, YouTubeLanguages: []string{"uk"}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := caption
		if r.URL.Host == "www.youtube.com" {
			body = page
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: ioBody(body), Request: r}, nil
	})}}
	s, err := f.Fetch(context.Background(), "https://youtu.be/abcdefghijk")
	if err != nil || s.Completeness != "partial" || !strings.Contains(s.Warning, "неповна") {
		t.Fatal("short page track not partial", s, err)
	}
	page = `{"captionTracks":[]}`
	f.metadataResolver = func(context.Context, string) (youtubeMetadata, error) {
		return youtubeMetadata{Duration: 600, Automatic: map[string][]youtubeFormat{"uk": {{URL: "https://captions.example/m", Ext: "json3"}}}}, nil
	}
	if s, err = f.Fetch(context.Background(), "https://youtu.be/abcdefghijk"); err != nil || s.Completeness != "partial" || !strings.Contains(s.Warning, "неповна") {
		t.Fatal("short metadata track not partial", s, err)
	}
	page = `{"lengthSeconds":"90","captionTracks":[{"baseUrl":"https://captions.example/uk","languageCode":"uk"}]}`
	if s, err = f.Fetch(context.Background(), "https://youtu.be/abcdefghijk"); err != nil || s.Completeness != "full" {
		t.Fatal("short video flagged", s, err)
	}
}
