package library

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

//go:embed pages.html
var pages string

//go:embed progress.js
var progressJS string

//go:embed digest.js
var digestJS string

type Web struct {
	Store        *Store
	Cache        *HTTPCache
	Origin, CSRF string
	MaxText      int64
	tmpl         *template.Template
	zone         *time.Location
}
type pageData struct {
	CSRF            string
	Filters         Filters
	Items           []Item
	Item            Item
	Snapshots       []Snapshot
	Selected        *Snapshot
	Ratings         []RatingEvent
	Jobs            []JobStatus
	Digest          *Digest
	Sections        []DigestSection
	DigestEnabled   bool
	CanRetryDigest  bool
	ExportScript    template.JS
	ExportCSP       string
	Pending         bool
	RateWithoutText bool
}

func NewWeb(s *Store, origin string, maxText int64) (*Web, error) {
	zone, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Scheme != "http" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return nil, errors.New("invalid local UI origin")
	}
	funcs := template.FuncMap{
		"state": func(s string) string {
			labels := map[string]string{"queued": "У черзі", "fetching": "Отримання тексту", "ready": "Готово", "enriching": "Створення дайджесту", "digest_retry_wait": "Очікує повтору дайджесту", "digest_failed": "Дайджест не створено", "needs_input": "Потрібен текст", "retry_wait": "Очікує повтору", "failed": "Не вдалося", "running": "Виконується", "done": "Завершено", "cancelled": "Скасовано"}
			if label, ok := labels[s]; ok {
				return label
			}
			return s
		},
		"placement": func(s string) string {
			switch s {
			case "inbox":
				return "Вхідні"
			case "library":
				return "Бібліотека"
			case "deferred":
				return "Відкладені"
			}
			return s
		}, "states": func() []string {
			return []string{"queued", "fetching", "ready", "needs_input", "retry_wait", "failed", "enriching", "digest_retry_wait", "digest_failed"}
		}, "quickScores": func() []int { return []int{0, 1, 2} }, "date": func(raw string) string {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return raw
			}
			return t.In(zone).Format("2006-01-02 15:04:05 MST")
		}, "score": func(n sql.NullInt64) string {
			if !n.Valid {
				return "неоцінено"
			}
			return strconv.FormatInt(n.Int64, 10)
		}}
	tmpl, err := template.New("pages").Funcs(funcs).Parse(pages)
	if err != nil {
		return nil, err
	}
	return &Web{Store: s, Origin: origin, CSRF: rand.Text(), MaxText: maxText, tmpl: tmpl, zone: zone}, nil
}
func (w *Web) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", w.list)
	mux.HandleFunc("GET /cache", w.cache)
	mux.HandleFunc("GET /static/progress.js", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		rw.Write([]byte(progressJS))
	})
	mux.HandleFunc("GET /static/digest.js", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		rw.Write([]byte(digestJS))
	})
	mux.HandleFunc("GET /items/{id}/digests/{digest}/{format}", w.exportDigest)
	mux.HandleFunc("GET /items/{id}", w.detail)
	mux.HandleFunc("POST /items", w.add)
	mux.HandleFunc("POST /items/{id}/{action}", w.mutate)
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		rw.Header().Set("X-Content-Type-Options", "nosniff")
		// Preserve the origin of same-origin form POSTs. no-referrer makes
		// Chromium send Origin: null for HTTP navigation forms. External links
		// still use rel=noreferrer and cross-origin referrers are suppressed.
		rw.Header().Set("Referrer-Policy", "same-origin")
		rw.Header().Set("Cache-Control", "no-store")
		u, _ := url.Parse(w.Origin)
		hostAllowed := r.Host == u.Host
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
			hostAllowed = hostAllowed || r.Host == net.JoinHostPort("localhost", u.Port())
		}
		if !hostAllowed {
			http.Error(rw, "невідомий Host", http.StatusForbidden)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if r.Header.Get("Authorization") != "" {
				http.Error(rw, "користувацькі мутації не приймають агентські облікові дані", http.StatusForbidden)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != u.Scheme+"://"+r.Host {
				http.Error(rw, "сторонній Origin", http.StatusForbidden)
				return
			}
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(rw, "сторонній browser запит", http.StatusForbidden)
				return
			}
			r.Body = http.MaxBytesReader(rw, r.Body, w.MaxText+65536)
			if err := r.ParseForm(); err != nil {
				http.Error(rw, "форма завелика або некоректна", http.StatusBadRequest)
				return
			}
			token := r.PostForm.Get("csrf")
			if subtle.ConstantTimeCompare([]byte(token), []byte(w.CSRF)) != 1 {
				http.Error(rw, "некоректний CSRF token; оновіть сторінку", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(rw, r)
	})
}
func (w *Web) render(rw http.ResponseWriter, name string, data pageData) {
	data.CSRF = w.CSRF
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Buffer before writing so a template failure does not send a partial success.
	var b strings.Builder
	if err := w.tmpl.ExecuteTemplate(&b, name, data); err != nil {
		http.Error(rw, "не вдалося показати сторінку", http.StatusInternalServerError)
		return
	}
	rw.Write([]byte(b.String()))
}
func (w *Web) list(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := Filters{Query: q.Get("q"), Placement: q.Get("placement"), Rating: q.Get("rating"), Read: q.Get("read"), State: q.Get("state"), Sort: q.Get("sort"), Direction: q.Get("direction")}
	for _, tag := range strings.Split(q.Get("tags"), ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			f.Tags = append(f.Tags, tag)
		}
	}
	if f.Rating != "" && f.Rating != "unrated" {
		n, e := strconv.ParseInt(f.Rating, 10, 64)
		if e != nil || n < 0 {
			http.Error(rw, "некоректний фільтр оцінки", 400)
			return
		}
	}
	items, err := w.Store.Search(f)
	if err != nil {
		http.Error(rw, "некоректні параметри пошуку", 400)
		return
	}
	w.render(rw, "list", pageData{Items: items, Filters: f})
}
func pathID(r *http.Request) (int64, error) {
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil || id <= 0 {
		return 0, errors.New("некоректний ID")
	}
	return id, nil
}
func (w *Web) detail(rw http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	i, err := w.Store.Item(id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(rw, r)
		return
	}
	if err != nil {
		http.Error(rw, "сховище недоступне", 500)
		return
	}
	snapshots, err := w.Store.Snapshots(id)
	if err != nil {
		http.Error(rw, "сховище недоступне", 500)
		return
	}
	selected := i.Selected.Int64
	if raw := r.URL.Query().Get("source"); raw != "" {
		selected, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.Error(rw, "некоректна версія", 400)
			return
		}
	}
	var snap *Snapshot
	for k := range snapshots {
		if snapshots[k].ID == selected {
			snap = &snapshots[k]
			break
		}
	}
	if r.URL.Query().Get("source") != "" && snap == nil {
		http.NotFound(rw, r)
		return
	}
	ratings, err := w.Store.Ratings(id)
	if err != nil {
		http.Error(rw, "сховище недоступне", 500)
		return
	}
	jobs, err := w.Store.Jobs(id)
	if err != nil {
		http.Error(rw, "сховище недоступне", 500)
		return
	}
	var digest *Digest
	var sections []DigestSection
	if snap != nil {
		digest, err = w.Store.DigestForSnapshot(snap.ID)
		if err == nil && digest != nil {
			sections, err = SplitDigest(digest.Markdown)
		}
		if err != nil {
			http.Error(rw, "дайджест недоступний; перевірте сховище", 500)
			return
		}
	}
	pending := snap == nil && (i.State == "queued" || i.State == "fetching" || i.State == "retry_wait")
	if snap != nil && digest == nil && w.Store.DigestConfig != nil {
		for _, job := range jobs {
			if job.Stage != "digest" {
				continue
			}
			if job.SnapshotID.Int64 == snap.ID && (job.State == "queued" || job.State == "running" || job.State == "retry_wait") {
				pending = true
			}
		}
	}
	w.render(rw, "detail", pageData{Item: i, Snapshots: snapshots, Selected: snap, Digest: digest, Sections: sections, DigestEnabled: w.Store.DigestConfig != nil, CanRetryDigest: snap != nil && snap.ID == i.Selected.Int64 && w.Store.DigestConfig != nil, Ratings: ratings, Jobs: jobs, Pending: pending, RateWithoutText: r.URL.Query().Get("rate_without_text") == "1"})
}
func (w *Web) add(rw http.ResponseWriter, r *http.Request) {
	id, err := w.Store.AddURL(r.PostForm.Get("url"))
	if err != nil {
		http.Error(rw, "URL не прийнято: "+err.Error(), 400)
		return
	}
	http.Redirect(rw, r, "/items/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
func (w *Web) mutate(rw http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	form := r.PostForm
	switch r.PathValue("action") {
	case "placement":
		err = w.Store.SetPlacement(id, form.Get("placement"))
	case "read":
		value := form.Get("read")
		if value != "0" && value != "1" {
			err = errors.New("потрібно 0 або 1")
		} else {
			err = w.Store.SetRead(id, value == "1")
		}
	case "tags":
		err = w.Store.SetTags(id, strings.Split(form.Get("tags"), ","))
	case "manual":
		text := form.Get("text")
		if int64(len(text)) > w.MaxText {
			err = errors.New("текст перевищує ліміт")
		} else {
			err = w.Store.Manual(id, text)
		}
	case "digest-retry":
		err = w.Store.RetryDigest(id)
	case "retry":
		err = w.Store.Retry(id)
	case "rating":
		var score int64
		score, err = strconv.ParseInt(form.Get("score"), 10, 64)
		if err != nil || score < 0 {
			err = errors.New("оцінка має бути цілим 0..9223372036854775807")
			break
		}
		var source *int64
		if raw := form.Get("source"); raw != "" {
			var sid int64
			sid, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || sid <= 0 {
				err = errors.New("некоректна версія тексту")
				break
			}
			source = &sid
		}
		var digest *int64
		if raw := form.Get("digest"); raw != "" {
			did, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || did <= 0 {
				err = errors.New("некоректна версія дайджесту")
				break
			}
			digest = &did
		}
		now := time.Now()
		var assessed *time.Time = &now
		if form.Get("unknown_date") == "1" {
			assessed = nil
		} else if raw := form.Get("assessed_at"); raw != "" {
			var t time.Time
			t, err = time.ParseInLocation("2006-01-02T15:04", raw, w.zone)
			if err != nil {
				err = errors.New("некоректна дата рішення")
				break
			}
			assessed = &t
		}
		err = w.Store.RateShown(id, score, source, digest, assessed, form.Get("reason"))
	default:
		http.NotFound(rw, r)
		return
	}
	if err != nil {
		http.Error(rw, "зміну не збережено: "+err.Error(), 400)
		return
	}
	http.Redirect(rw, r, "/items/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (w *Web) exportDigest(rw http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	did, e := strconv.ParseInt(r.PathValue("digest"), 10, 64)
	if err != nil || e != nil {
		http.NotFound(rw, r)
		return
	}
	d, err := w.Store.Digest(did, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(rw, r)
		return
	}
	if err != nil {
		http.Error(rw, "дайджест недоступний", 500)
		return
	}
	item, err := w.Store.Item(id)
	if err != nil {
		http.Error(rw, "сховище недоступне", 500)
		return
	}
	snapshot, err := w.Store.Snapshot(d.SnapshotID)
	if err != nil {
		http.Error(rw, "джерело дайджесту недоступне", 500)
		return
	}
	item.Title = snapshot.Title
	name := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		if r == '/' || r == '\\' {
			return '-'
		}
		return r
	}, snapshot.Title))
	if name == "" {
		name = fmt.Sprintf("digest-%d-%d", id, d.ID)
	}
	if runes := []rune(name); len(runes) > 180 {
		name = string(runes[:180])
	}
	format := r.PathValue("format")
	switch format {
	case "markdown":
		rw.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		rw.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".md"}))
		fmt.Fprintf(rw, "# %s\n\nURL: %s\n\nМодель: %s · %s\n\nТекст #%d · %s · %s\n\n%s\n", item.Title, item.URL, d.ActualModel, d.CreatedAt, snapshot.ID, snapshot.Completeness, snapshot.Warning, d.Markdown)
	case "html":
		sections, err := SplitDigest(d.Markdown)
		if err != nil {
			http.Error(rw, "дайджест пошкоджено", 500)
			return
		}
		rw.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".html"}))
		hash := sha256.Sum256([]byte(digestJS))
		policy := "default-src 'none'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(hash[:]) + "'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'"
		rw.Header().Set("Content-Security-Policy", policy)
		w.render(rw, "export", pageData{Item: item, Selected: &snapshot, Digest: &d, Sections: sections, ExportScript: template.JS(digestJS), ExportCSP: policy})
	default:
		http.NotFound(rw, r)
	}
}
