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
	"sort"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
)

type SourceError struct {
	Challenge        bool
	FooterOnly       bool
	Kind, Diagnostic string

	Message string
	Retry   bool
}

func (e *SourceError) Error() string { return e.Message }

const DefaultSourceUserAgent = "Mozilla/5.0 (X11; Linux x86_64; rv:124.0) Gecko/20100101 Firefox/124.0"

type Fetcher struct {
	CURL             string
	browserResolver  func(context.Context, string) ([]byte, *url.URL, string, bool, error)
	UserAgent        string
	YTDLP            string
	YouTubeLanguages []string
	metadataResolver func(context.Context, string) (youtubeMetadata, error)

	client   *http.Client
	MaxBytes int64
	Cache    *HTTPCache
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
	return &Fetcher{CURL: "curl", UserAgent: DefaultSourceUserAgent, client: client, MaxBytes: maxBytes, YTDLP: "yt-dlp", YouTubeLanguages: []string{"uk", "en"}}
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
func (f *Fetcher) getNetwork(ctx context.Context, raw string, meta *NetworkMeta) ([]byte, *url.URL, string, bool, error) {
	if _, err := CanonicalURL(raw); err != nil {
		return nil, nil, "", false, &SourceError{Message: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, nil, "", false, err
	}
	agent := f.UserAgent
	if agent == "" {
		agent = DefaultSourceUserAgent
	}
	req.Header.Set("User-Agent", agent)
	client := *f.client
	applyValidators := func(r *http.Request) {
		r.Header.Del("If-None-Match")
		r.Header.Del("If-Modified-Since")
		if meta != nil && r.URL.String() == meta.ValidatorURL {
			for name, values := range meta.RequestHeaders {
				r.Header[name] = append([]string(nil), values...)
			}
		}
	}
	applyValidators(req)
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		applyValidators(r)
		if f.client.CheckRedirect != nil {
			return f.client.CheckRedirect(r, via)
		}
		if len(via) >= 10 {
			return errors.New("redirect limit")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		var se *SourceError
		if errors.As(err, &se) {
			return nil, nil, "", false, se
		}
		return nil, nil, "", false, &SourceError{Message: "отримання джерела не вдалося (мережа або redirect)", Retry: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && meta != nil && resp.Request.URL.String() == meta.ValidatorURL &&
		(resp.Request.Header.Get("If-None-Match") != "" || resp.Request.Header.Get("If-Modified-Since") != "") {
		meta.Headers = resp.Header.Clone()
		meta.FetchedAt = nowUTC()
		meta.NotModified = true
		return nil, resp.Request.URL, "", false, nil
	}
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
	if meta != nil {
		*meta = NetworkMeta{Headers: resp.Header.Clone(), FetchedAt: nowUTC()}
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
func (f *Fetcher) FetchFresh(ctx context.Context, raw string) (Snapshot, error) {
	return f.Fetch(context.WithValue(ctx, cacheRefreshKey{}, true), raw)
}
func (f *Fetcher) Fetch(ctx context.Context, raw string) (Snapshot, error) {
	meta := &NetworkMeta{}
	ctx = context.WithValue(ctx, cacheMetaKey{}, meta)
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
	snap, err := decodeArticle(data, final, kind, partial, meta)
	var se *SourceError
	if errors.As(err, &se) && se.Challenge && (f.CURL != "" || f.browserResolver != nil) {
		resolver := f.browserResolver
		if resolver == nil {
			resolver = f.browserGet
		}
		data, final, kind, partial, err = resolver(ctx, raw)
		if err != nil {
			return Snapshot{}, err
		}
		snap, err = decodeArticle(data, final, kind, partial, &NetworkMeta{FetchedAt: nowUTC()})
		if err == nil {
			snap.Extractor += "+curl-v1"
		}
	}
	return snap, err
}
func decodeArticle(data []byte, final *url.URL, kind string, partial bool, meta *NetworkMeta) (Snapshot, error) {
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
	if err := ValidateSourceContent(snap); err != nil {
		var se *SourceError
		if errors.As(err, &se) && se.Challenge {
			return Snapshot{}, err
		}
		// Keep extracted footer text. Store marks it needs_input before AI.
		snap.Warning = err.Error()
	}
	if snap.Completeness == "unknown" && snap.Origin == "fetch" && !strings.Contains(kind, "text/plain") && snap.Warning == "Виділений текст HTML; повноту оригіналу не підтверджено" {
		if why := partialSourceSuspicion(snap.Text); why != "" {
			snap.Warning = "Підозра на неповний витяг: " + why + "; повноту оригіналу не підтверджено"
		}
	}
	snap.RetrievedAt = meta.FetchedAt
	if meta.CacheStatus == "HIT" || meta.CacheStatus == "STALE" || meta.CacheStatus == "REVALIDATED" {
		snap.Warning += "; джерело з кешу, отримано " + meta.FetchedAt
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
	data, _, _, pagePartial, pageErr := f.get(ctx, "https://www.youtube.com/watch?v="+id)
	tracks, _ := captionTracks(data)
	title := "YouTube: " + id
	languages := f.YouTubeLanguages
	if len(languages) == 0 {
		languages = []string{"uk", "en"}
	}
	try := func(candidates []captionTrack, extractor string) (Snapshot, bool) {
		sort.SliceStable(candidates, func(i, j int) bool {
			return subtitleRank(candidates[i], languages) > subtitleRank(candidates[j], languages)
		})
		// Bounded failover, not a download of every translated language.
		for i, track := range candidates {
			if i >= 8 || ctx.Err() != nil {
				break
			}
			body, _, _, partial, e := f.get(ctx, track.BaseURL)
			if e != nil {
				continue
			}
			text, decodeErr := decodeCaptions(body)
			if strings.TrimSpace(text) == "" {
				continue
			}
			snap := Snapshot{Text: text, Title: title, Origin: "fetch", Extractor: extractor, Completeness: "full", Warning: "Субтитри: " + track.LanguageCode}
			if track.Kind == "asr" {
				snap.Warning += "; автоматичні"
			}
			if u, e := url.Parse(track.BaseURL); e == nil && u.Query().Get("tlang") != "" {
				snap.Warning += "; автоматичний переклад"
			}
			if partial || decodeErr != nil || (extractor == "youtube-timedtext-v1" && pagePartial) {
				snap.Completeness = "partial"
				snap.Warning += "; відповідь часткова або не повністю прочитана"
			}
			if meta, ok := ctx.Value(cacheMetaKey{}).(*NetworkMeta); ok {
				snap.RetrievedAt = meta.FetchedAt
				if meta.CacheStatus == "HIT" || meta.CacheStatus == "STALE" || meta.CacheStatus == "REVALIDATED" {
					snap.Warning += "; субтитри з кешу, отримано " + meta.FetchedAt
				}
			}
			return snap, true
		}
		return Snapshot{}, false
	}
	if snap, ok := try(tracks, "youtube-timedtext-v1"); ok {
		return snap, nil
	}
	if pageErr != nil {
		var se *SourceError
		if errors.As(pageErr, &se) && !se.Retry {
			return Snapshot{}, pageErr
		}
	}
	resolver := f.metadataResolver
	if resolver == nil && f.YTDLP != "" {
		resolver = f.youtubeMetadata
	}
	if resolver != nil {
		info, e := resolver(ctx, id)
		if e == nil {
			if strings.TrimSpace(info.Title) != "" {
				title = info.Title
			}
			if snap, ok := try(metadataTracks(info), "youtube-yt-dlp-metadata-v1"); ok {
				return snap, nil
			}
		}
	}
	if ctx.Err() != nil {
		return Snapshot{}, &SourceError{Message: "час отримання YouTube-субтитрів вичерпано; спробуйте повторити", Retry: true}
	}
	return Snapshot{}, &SourceError{Message: "не вдалося отримати доступні субтитри у вибраних чи інших мовах; перевірте yt-dlp або вставте текст вручну"}
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
