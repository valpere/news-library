package library

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestArticleFixtures(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/article":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>Fixture news</title></head><body><nav>ignore</nav><article><h1>Knowledge</h1><p>`+strings.Repeat("Useful information about Go libraries and persistent storage. ", 30)+`</p><script>alert('bad')</script></article></body></html>`)
		case "/redirect":
			http.Redirect(w, r, "/article", http.StatusFound)
		case "/plain":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, strings.Repeat("knowledge ", 500))
		case "/partial":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusPartialContent)
			fmt.Fprint(w, "available partial knowledge")
		case "/empty":
			w.Header().Set("Content-Type", "text/plain")
		case "/slow":
			select {
			case <-r.Context().Done():
				return
			case <-time.After(200 * time.Millisecond):
				fmt.Fprint(w, "late")
			}
		default:
			http.Error(w, "no source", 404)
		}
	}))
	defer fixture.Close()
	f := &Fetcher{client: fixture.Client(), MaxBytes: 10000}
	snap, e := f.Fetch(context.Background(), fixture.URL+"/redirect")
	if e != nil || !strings.Contains(snap.Text, "persistent storage") || strings.Contains(snap.Text, "alert") || snap.Title != "Fixture news" || snap.Completeness != "unknown" {
		t.Fatal("article extraction", snap, e)
	}
	snap, e = f.Fetch(context.Background(), fixture.URL+"/partial")
	if e != nil || snap.Completeness != "partial" || snap.Text != "available partial knowledge" {
		t.Fatal("HTTP206 text lost", e)
	}
	f.MaxBytes = 1024
	snap, e = f.Fetch(context.Background(), fixture.URL+"/plain")
	if e != nil || snap.Completeness != "partial" || len(snap.Text) > 1024 || snap.Warning == "" {
		t.Fatal("partial text", e)
	}
	for _, path := range []string{"/empty", "/missing"} {
		_, e = f.Fetch(context.Background(), fixture.URL+path)
		var se *SourceError
		if !errors.As(e, &se) || se.Retry {
			t.Fatal("permanent failure", path, e)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, e = f.Fetch(ctx, fixture.URL+"/slow")
	var se *SourceError
	if !errors.As(e, &se) || !se.Retry {
		t.Fatal("timeout not retryable", e)
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestYouTubeFixtures(t *testing.T) {
	cases := []struct {
		name, body, want string
		missing          bool
	}{
		{"xml", `<transcript><text start="0">Hello &amp; world</text><text start="2">Knowledge</text></transcript>`, "Hello & world", false},
		{"srv3", `<timedtext><body><p><s>Hello </s><s>world</s></p></body></timedtext>`, "Hello world", false},
		{"json3", `{"events":[{"segs":[{"utf8":"Hello world"}]}]}`, "Hello world", false},
		{"missing", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			f := &Fetcher{MaxBytes: 4096, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				body := tc.body
				if r.URL.Host == "www.youtube.com" {
					body = `<script>var ytInitialPlayerResponse={"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[{"baseUrl":"https://captions.example/track","languageCode":"en","kind":"asr"}]}}}};</script>`
					if tc.missing {
						body = "no subtitles"
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/xml"}}, Body: ioBody(body), Request: r}, nil
			})}}
			snap, e := f.Fetch(context.Background(), "https://youtu.be/abcdefghijk")
			if tc.missing {
				var se *SourceError
				if !errors.As(e, &se) || se.Retry || calls != 1 {
					t.Fatal("missing captions", e, calls)
				}
			} else if e != nil || !strings.Contains(snap.Text, tc.want) || snap.Extractor != "youtube-timedtext-v1" || calls != 2 {
				t.Fatal("caption fixture", snap, e, calls)
			}
		})
	}
	for _, raw := range []string{"https://youtube.com/watch", "https://youtu.be/bad", "https://youtube.com/channel/x"} {
		_, e := (&Fetcher{}).Fetch(context.Background(), raw)
		if e == nil {
			t.Fatal("bad video URL", raw)
		}
	}
	text, e := decodeCaptions([]byte(`<transcript><text>partial`))
	if text != "partial" || e == nil {
		t.Fatal("broken XML falsely full", text, e)
	}
}
func ioBody(s string) *bodyReader { return &bodyReader{Reader: strings.NewReader(s)} }

type bodyReader struct{ *strings.Reader }

func (*bodyReader) Close() error { return nil }
func TestFetchNetworkBoundaries(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "224.0.0.1", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "2001:db8::1", "64:ff9b::7f00:1", "2002:7f00:1::"} {
		if publicIP(netip.MustParseAddr(raw)) {
			t.Fatal("private/special allowed", raw)
		}
	}
	if !publicIP(netip.MustParseAddr("8.8.8.8")) || !publicIP(netip.MustParseAddr("2606:4700:4700::1111")) {
		t.Fatal("public denied")
	}
	calls := 0
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	dial := func(context.Context, string, string) (net.Conn, error) {
		calls++
		return nil, errors.New("unexpected dial")
	}
	if _, e := dialResolved(context.Background(), "tcp", "fixture.test:80", lookup, dial); e == nil || calls != 0 {
		t.Fatal("mixed DNS bypass", e, calls)
	}
	resolved := 0
	lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		resolved++
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	dial = func(_ context.Context, _ string, address string) (net.Conn, error) {
		calls++
		if address != "8.8.8.8:80" {
			t.Fatal("re-resolved hostname", address)
		}
		return nil, errors.New("fixture")
	}
	dialResolved(context.Background(), "tcp", "fixture.test:80", lookup, dial)
	if resolved != 1 || calls != 1 {
		t.Fatal("DNS pin failed")
	}
	f := NewFetcher(time.Second, 1024)
	for _, raw := range []string{"http://127.0.0.1/", "http://[::1]/", "http://169.254.169.254/"} {
		if _, e := f.Fetch(context.Background(), raw); e == nil {
			t.Fatal("private source fetched", raw)
		}
	}
	// Initial public hostname is routed to a fixture only inside this test;
	// every subsequent dial still applies production address validation.
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1/private", http.StatusFound)
	}))
	defer fixture.Close()
	transport := f.client.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialResolved(ctx, network, address, func(_ context.Context, _ string, host string) ([]netip.Addr, error) {
			if host == "public.example" {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			return []netip.Addr{netip.MustParseAddr(host)}, nil
		}, func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(fixture.URL, "http://"))
		})
	}
	_, e := f.Fetch(context.Background(), "http://public.example/")
	var se *SourceError
	if !errors.As(e, &se) || se.Retry {
		t.Fatal("private redirect not permanently rejected", e)
	}
	if transport.Proxy != nil {
		t.Fatal("environment proxy bypass")
	}
}
func TestWorkerFailureAndStoredText(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "searchable offline knowledge")
	}))
	defer fixture.Close()
	s := testStore(t)
	failed := add(t, s, fixture.URL+"/fail")
	ok := add(t, s, fixture.URL+"/ok")
	w := Worker{Store: s, Source: &Fetcher{client: fixture.Client(), MaxBytes: 1024}, Timeout: time.Second, Lease: time.Second, Backoff: time.Hour, MaxAttempts: 2}
	for range 2 {
		if worked, e := w.Step(context.Background()); !worked || e != nil {
			t.Fatal("worker", e)
		}
	}
	i, _ := s.Item(failed)
	if i.State != "retry_wait" {
		t.Fatal("no retry state")
	}
	i, _ = s.Item(ok)
	if i.State != "ready" {
		t.Fatal("failed item stalled next")
	}
	fixture.Close()
	text, _ := s.Texts(ok)
	if len(text) != 1 || text[0].Text != "searchable offline knowledge" {
		t.Fatal("available text lost")
	}
	got, e := s.Search(Filters{Query: "knowledge"})
	if e != nil || len(got) != 1 {
		t.Fatal("offline text not searchable", e)
	}
}
