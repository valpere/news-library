package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type youtubeMetadata struct {
	Title     string                     `json:"title"`
	Subtitles map[string][]youtubeFormat `json:"subtitles"`
	Automatic map[string][]youtubeFormat `json:"automatic_captions"`
}
type youtubeFormat struct {
	URL string `json:"url"`
	Ext string `json:"ext"`
}
type cappedOutput struct {
	bytes.Buffer
	limit int
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("metadata output too large")
	}
	return b.Buffer.Write(p)
}

// yt-dlp only discovers metadata for a validated YouTube ID. Its network
// connections pass through the same public-address guard as primary fetching.
func (f *Fetcher) youtubeMetadata(ctx context.Context, id string) (youtubeMetadata, error) {
	var info youtubeMetadata
	if f.YTDLP == "" {
		return info, errors.New("yt-dlp disabled")
	}
	if !videoIDPattern.MatchString(id) {
		return info, errors.New("invalid video ID")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	proxy, closeProxy, err := startMetadataProxy(ctx)
	if err != nil {
		return info, err
	}
	defer closeProxy()
	cmd := exec.CommandContext(ctx, f.YTDLP, "--ignore-config", "--no-plugin-dirs", "--no-cache-dir", "--no-playlist", "--skip-download", "--dump-single-json", "--no-warnings", "--socket-timeout", "8", "--retries", "0", "--extractor-retries", "0", "--proxy", proxy, "--", "https://www.youtube.com/watch?v="+id)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	stdout := &cappedOutput{limit: 4 << 20}
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err = cmd.Run(); err != nil {
		return info, errors.New("yt-dlp metadata unavailable")
	}
	if err = json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return info, errors.New("yt-dlp returned invalid metadata")
	}
	return info, nil
}
func metadataTracks(info youtubeMetadata) []captionTrack {
	out := []captionTrack{}
	for _, group := range []struct {
		values map[string][]youtubeFormat
		kind   string
	}{{info.Subtitles, ""}, {info.Automatic, "asr"}} {
		languages := make([]string, 0, len(group.values))
		for lang := range group.values {
			languages = append(languages, lang)
		}
		sort.Strings(languages)
		for _, lang := range languages {
			if lang == "live_chat" {
				continue
			}
			// One preferred format per language leaves room to try other languages.
			// JSON3 first, otherwise XML; never reinterpret VTT/SRT as XML.
		formats:
			for _, ext := range []string{"json3", "srv3", "srv1", "ttml"} {
				for _, format := range group.values[lang] {
					if format.Ext == ext && format.URL != "" {
						out = append(out, captionTrack{BaseURL: format.URL, LanguageCode: lang, Kind: group.kind})
						break formats
					}
				}
			}
		}
	}
	return out
}

// A short-lived, non-public HTTP proxy constrains the metadata helper too.
// CONNECT tunnels and ordinary HTTP redirects must resolve to public addresses.
func startMetadataProxy(ctx context.Context) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	closed := false
	track := func(c net.Conn) bool {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			c.Close()
			return false
		}
		connections[c] = true
		return true
	}
	untrack := func(c net.Conn) { mu.Lock(); delete(connections, c); mu.Unlock(); c.Close() }
	transport := &http.Transport{Proxy: nil, DialContext: safeDial, ResponseHeaderTimeout: 8 * time.Second, TLSHandshakeTimeout: 8 * time.Second}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			host, port, e := net.SplitHostPort(r.Host)
			if e != nil || host == "" || (port != "443" && port != "80") {
				http.Error(w, "invalid target", 403)
				return
			}
			upstream, e := safeDial(ctx, "tcp", r.Host)
			if e != nil {
				http.Error(w, "target unavailable", 403)
				return
			}
			if !track(upstream) {
				return
			}
			defer untrack(upstream)
			client, buf, e := w.(http.Hijacker).Hijack()
			if e != nil {
				return
			}
			if !track(client) {
				return
			}
			defer untrack(client)
			deadline := time.Now().Add(25 * time.Second)
			if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
				deadline = d
			}
			client.SetDeadline(deadline)
			upstream.SetDeadline(deadline)
			if _, e = buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); e != nil {
				return
			}
			if e = buf.Flush(); e != nil {
				return
			}
			done := make(chan struct{})
			go func() { io.Copy(upstream, buf); upstream.Close(); close(done) }()
			io.Copy(client, upstream)
			client.Close()
			<-done
			return
		}
		if r.Method != "GET" || r.URL.Scheme != "http" || r.URL.Host == "" {
			http.Error(w, "unsupported request", 403)
			return
		}
		if _, e := CanonicalURL(r.URL.String()); e != nil {
			http.Error(w, "invalid URL", 403)
			return
		}
		port := r.URL.Port()
		if port != "" && port != "80" {
			http.Error(w, "invalid port", 403)
			return
		}
		req := r.Clone(ctx)
		req.RequestURI = ""
		req.Header.Del("Proxy-Authorization")
		req.Header.Del("Proxy-Connection")
		resp, e := transport.RoundTrip(req)
		if e != nil {
			http.Error(w, "target unavailable", 403)
			return
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			w.Header()[key] = values
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, io.LimitReader(resp.Body, 4<<20))
	})
	go server.Serve(listener)
	closeProxy := func() {
		mu.Lock()
		closed = true
		for c := range connections {
			c.Close()
		}
		mu.Unlock()
		server.Close()
		transport.CloseIdleConnections()
	}
	return (&url.URL{Scheme: "http", Host: listener.Addr().String()}).String(), closeProxy, nil
}
func subtitleRank(t captionTrack, languages []string) int {
	lang := strings.ToLower(t.LanguageCode)
	rank := 0
	for i, preferred := range languages {
		preferred = strings.ToLower(strings.TrimSpace(preferred))
		if lang == preferred || strings.HasPrefix(lang, preferred+"-") {
			rank = (len(languages) - i) * 100
			break
		}
	}
	if t.Kind != "asr" {
		rank += 10
	}
	u, _ := url.Parse(t.BaseURL)
	if u != nil && u.Query().Get("tlang") != "" {
		rank -= 1
	}
	return rank
}

var subtitleLanguagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,34}$`)

func ParseYouTubeLanguages(raw string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range strings.Split(raw, ",") {
		value = strings.ToLower(strings.TrimSpace(value))
		if !subtitleLanguagePattern.MatchString(value) {
			return nil, errors.New("youtube languages must be comma-separated language codes")
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 || len(out) > 10 {
		return nil, errors.New("choose 1..10 YouTube languages")
	}
	return out, nil
}
