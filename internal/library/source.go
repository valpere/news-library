package library

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
)

type SourceError struct {
	Message string
	Retry   bool
}

func (e *SourceError) Error() string { return e.Message }

type Fetcher struct {
	client   *http.Client
	MaxBytes int64
}

func NewFetcher(timeout time.Duration, maxBytes int64) *Fetcher {
	tr := &http.Transport{DialContext: safeDial, ResponseHeaderTimeout: timeout, TLSHandshakeTimeout: 10 * time.Second, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}
	client := &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("redirect limit")
		}
		_, err := CanonicalURL(req.URL.String())
		return err
	}}
	return &Fetcher{client: client, MaxBytes: maxBytes}
}

var deniedNetworks = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("2001::/32"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range deniedNetworks {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func safeDial(ctx context.Context, network, address string) (net.Conn, error) {
	return dialResolved(ctx, network, address, net.DefaultResolver.LookupNetIP, (&net.Dialer{Timeout: 10 * time.Second}).DialContext)
}
func dialResolved(ctx context.Context, network, address string, lookup func(context.Context, string, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := lookup(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("no resolved addresses")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, &SourceError{Message: "джерело вказує на заборонену мережеву адресу"}
		}
	}
	var last error
	for _, ip := range ips {
		conn, e := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
		last = e
	}
	return nil, last
}
func (f *Fetcher) get(ctx context.Context, raw string) ([]byte, *url.URL, string, bool, error) {
	if _, err := CanonicalURL(raw); err != nil {
		return nil, nil, "", false, &SourceError{Message: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, nil, "", false, err
	}
	req.Header.Set("User-Agent", "news-library/0.1 (personal text archive)")
	resp, err := f.client.Do(req)
	if err != nil {
		var se *SourceError
		if errors.As(err, &se) {
			return nil, nil, "", false, se
		}
		return nil, nil, "", false, &SourceError{Message: "отримання джерела не вдалося (мережа або redirect)", Retry: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, nil, "", false, &SourceError{Message: fmt.Sprintf("джерело повернуло HTTP %d", resp.StatusCode), Retry: resp.StatusCode == 429 || resp.StatusCode >= 500}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, f.MaxBytes+1))
	partial := resp.StatusCode == http.StatusPartialContent || int64(len(data)) > f.MaxBytes
	if int64(len(data)) > f.MaxBytes {
		data = data[:f.MaxBytes]
	}
	if err != nil {
		if len(data) == 0 {
			return nil, nil, "", false, &SourceError{Message: "відповідь джерела перервано", Retry: true}
		}
		partial = true
	}
	return data, resp.Request.URL, resp.Header.Get("Content-Type"), partial, nil
}

var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

func youtubeID(u *url.URL) (string, bool, error) {
	host := strings.ToLower(u.Hostname())
	var id string
	switch host {
	case "youtu.be":
		id = strings.TrimPrefix(u.Path, "/")
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		switch {
		case u.Path == "/watch":
			id = u.Query().Get("v")
		case strings.HasPrefix(u.Path, "/shorts/"):
			id = strings.TrimPrefix(u.Path, "/shorts/")
		case strings.HasPrefix(u.Path, "/embed/"):
			id = strings.TrimPrefix(u.Path, "/embed/")
		default:
			return "", true, &SourceError{Message: "потрібен URL конкретного YouTube-відео"}
		}
	default:
		return "", false, nil
	}
	if !videoIDPattern.MatchString(id) {
		return "", true, &SourceError{Message: "некоректний ID YouTube-відео"}
	}
	return id, true, nil
}
func (f *Fetcher) Fetch(ctx context.Context, raw string) (Snapshot, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Snapshot{}, err
	}
	id, isYT, err := youtubeID(u)
	if err != nil {
		return Snapshot{}, err
	}
	if isYT {
		return f.youtube(ctx, id)
	}
	data, final, kind, partial, err := f.get(ctx, raw)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Origin: "fetch", Extractor: "readability-v2.1.3", Completeness: "unknown", Warning: "Виділений текст HTML; повноту оригіналу не підтверджено"}
	if strings.Contains(kind, "text/plain") {
		snap.Text = string(data)
		snap.Title = final.Hostname()
		snap.Extractor = "plain-v1"
		snap.Completeness = "full"
		snap.Warning = ""
	} else if strings.Contains(kind, "text/html") || strings.Contains(kind, "application/xhtml+xml") || kind == "" {
		article, e := readability.FromReader(bytes.NewReader(data), final)
		if e != nil {
			return Snapshot{}, &SourceError{Message: "не вдалося виділити текст статті"}
		}
		var b bytes.Buffer
		if e = article.RenderText(&b); e != nil {
			return Snapshot{}, e
		}
		snap.Text = b.String()
		snap.Title = article.Title()
	} else {
		return Snapshot{}, &SourceError{Message: "непідтримуваний формат джерела: потрібен HTML або text/plain"}
	}
	snap.Text = strings.TrimSpace(snap.Text)
	if snap.Text == "" {
		return Snapshot{}, &SourceError{Message: "доступного тексту немає; можна вставити вручну"}
	}
	if partial {
		snap.Completeness = "partial"
		snap.Warning = "Відповідь часткова, обрізана або перервана; доступну частину збережено"
	}
	return snap, nil
}

type captionTrack struct {
	BaseURL      string `json:"baseUrl"`
	LanguageCode string `json:"languageCode"`
	Kind         string `json:"kind"`
}

func captionTracks(data []byte) ([]captionTrack, error) {
	marker := []byte(`"captionTracks":`)
	pos := bytes.Index(data, marker)
	if pos < 0 {
		return nil, errors.New("no captions")
	}
	var tracks []captionTrack
	err := json.NewDecoder(bytes.NewReader(data[pos+len(marker):])).Decode(&tracks)
	return tracks, err
}
func (f *Fetcher) youtube(ctx context.Context, id string) (Snapshot, error) {
	data, _, _, pagePartial, err := f.get(ctx, "https://www.youtube.com/watch?v="+id)
	if err != nil {
		return Snapshot{}, err
	}
	tracks, err := captionTracks(data)
	if err != nil || len(tracks) == 0 {
		return Snapshot{}, &SourceError{Message: "доступних YouTube-субтитрів немає або формат сторінки змінився; вставте текст вручну"}
	}
	// Prefer human captions, then Ukrainian/English when available.
	best := 0
	rank := func(t captionTrack) int {
		n := 0
		if t.Kind != "asr" {
			n += 4
		}
		if t.LanguageCode == "uk" {
			n += 2
		} else if t.LanguageCode == "en" {
			n++
		}
		return n
	}
	for k := range tracks {
		if rank(tracks[k]) > rank(tracks[best]) {
			best = k
		}
	}
	body, _, _, partial, err := f.get(ctx, tracks[best].BaseURL)
	if err != nil {
		return Snapshot{}, err
	}
	text, err := decodeCaptions(body)
	if strings.TrimSpace(text) == "" {
		return Snapshot{}, &SourceError{Message: "субтитри недоступні або не вдалося їх прочитати"}
	}
	if err != nil {
		partial = true
	}
	s := Snapshot{Text: text, Title: "YouTube: " + id, Origin: "fetch", Extractor: "youtube-timedtext-v1", Completeness: "full", Warning: "Субтитри: " + tracks[best].LanguageCode}
	if tracks[best].Kind == "asr" {
		s.Warning += "; автоматичні"
	}
	if partial || pagePartial {
		s.Completeness = "partial"
		s.Warning += "; відповідь часткова або не повністю прочитана"
	}
	return s, nil
}
func decodeCaptions(data []byte) (string, error) {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		var document struct {
			Events []struct {
				Segs []struct {
					Text string `json:"utf8"`
				} `json:"segs"`
			} `json:"events"`
		}
		if err := json.Unmarshal(data, &document); err != nil {
			return "", err
		}
		var b strings.Builder
		for _, e := range document.Events {
			for _, s := range e.Segs {
				b.WriteString(s.Text)
			}
			b.WriteByte('\n')
		}
		return strings.TrimSpace(b.String()), nil
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var b strings.Builder
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if b.Len() > 0 {
				return strings.TrimSpace(b.String()), err
			}
			return "", err
		}
		switch v := token.(type) {
		case xml.StartElement:
			if v.Name.Local == "text" || v.Name.Local == "p" {
				depth++
			}
		case xml.EndElement:
			if v.Name.Local == "text" || v.Name.Local == "p" {
				depth--
				b.WriteByte('\n')
			}
		case xml.CharData:
			if depth > 0 {
				b.Write(v)
			}
		}
	}
	return strings.TrimSpace(b.String()), nil
}
