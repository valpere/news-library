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
	Store            *Store
	Cache            *HTTPCache
	Semantic         *Semantic
	Telegram         TelegramConfig
	BackgroundPaused bool
	ReadToken        string
	Origin, CSRF     string
	MaxText          int64
	tmpl             *template.Template
	zone             *time.Location
}
type pageData struct {
	DigestRetryNext, DigestRetryAfter int64
	DigestRetryIncludeUnknown         bool

	DigestRetryCandidates                 []DigestRetryCandidate
	DigestRetryResults                    []DigestRetryResult
	DigestRetryTruncated                  bool
	DigestRetryQueued, DigestRetrySkipped int

	DigestFailureKind, DigestFailureLabel, DigestFailureAdvice, DigestRetryLabel, DigestRejectedResponse string

	BatchResults                           []BatchURLResult
	BatchInput, BatchError                 string
	BatchAdded, BatchExisting, BatchFailed int

	ConnectionHTML       template.HTML
	ConnectionVocabulary map[string][]string

	Connections                      []ConnectionSummary
	Connection                       *ConnectionNote
	ConnectionQuery, ConnectionError string
	ConnectionChoices                []Item
	ConnectionSlots                  []int

	Deliveries                []Delivery
	Delivery                  *Delivery
	DeliveryEnabled           bool
	DailyTime, DeliveryChat   string
	DeliverySize              int
	BackgroundPaused          bool
	CSRF                      string
	SearchMode, SearchWarning string
	EmbeddingStatus           string
	EmbeddingsEnabled         bool
	Indexed, Eligible         int
	Filters                   Filters
	Items                     []Item
	Item                      Item
	Snapshots                 []Snapshot
	Selected                  *Snapshot
	Ratings                   []RatingEvent
	Jobs                      []JobStatus
	Digest                    *Digest
	Sections                  []DigestSection
	DigestEnabled             bool
	CanRetryDigest            bool
	ExportScript              template.JS
	ExportCSP                 string
	FilterDecisions           []FilterDecision
	Feedback                  []DigestFeedback
	Digests                   []Digest
	CanRegenerate             bool
	DigestError               string
	Pending                   bool
	RateWithoutText           bool
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
		"retrySelection": retrySelection,
		"failureLabel":   func(kind string) string { label, _, _ := FailureAdvice(kind); return label },
		"probability":    func(p float64) string { return strconv.FormatFloat(p, 'g', -1, 64) },
		"percent":        func(p float64) string { return strconv.FormatFloat(p*100, 'f', 0, 64) + "%" },
		"confidence": func(p sql.NullFloat64) string {
			if !p.Valid {
				return "відсутній"
			}
			return strconv.FormatFloat(p.Float64*100, 'f', 0, 64) + "%"
		},
		"filterLabel": func(v string) string {
			switch v {
			case "passed":
				return "Пропущено до обробки"
			case "rejected":
				return "Відсіяно правилами"
			case "overridden":
				return "Повернуто користувачем"
			}
			return "Не перевірено"
		},
		"quality": func(v string) string {
			switch v {
			case "good":
				return "Вдалий"
			case "inaccurate":
				return "Є помилки"
			case "incomplete":
				return "Пропущено важливе"
			case "unclear":
				return "Незрозумілий"
			}
			return v
		},
		"state": func(s string) string {
			labels := map[string]string{"filtered": "Обробку пропущено за правилами", "queued": "У черзі", "fetching": "Отримання тексту", "ready": "Готово", "enriching": "Створення дайджесту", "digest_retry_wait": "Очікує повтору дайджесту", "digest_failed": "Дайджест не створено", "needs_input": "Потрібен текст", "retry_wait": "Очікує повтору", "failed": "Не вдалося", "running": "Виконується", "done": "Завершено", "cancelled": "Скасовано"}
			if label, ok := labels[s]; ok {
				return label
			}
			return s
		},
		"deliveryState": func(s string) string {
			return map[string]string{"preview": "Попередній перегляд", "queued": "У черзі", "sending": "Відправляється", "sent": "Доставлено", "failed": "Не відправлено", "unknown": "Результат невідомий", "missed": "Випуск пропущено"}[s]
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
			return []string{"queued", "fetching", "ready", "needs_input", "retry_wait", "failed", "enriching", "digest_retry_wait", "digest_failed", "filtered"}
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
	mux.HandleFunc("GET /digest-retries", w.digestRetryBatchPage)
	mux.HandleFunc("POST /digest-retries", w.digestRetryBatchSubmit)
	mux.HandleFunc("GET /connections", w.connectionListPage)
	mux.HandleFunc("GET /connections/new", w.connectionForm)
	mux.HandleFunc("GET /connections/{id}", w.connectionPage)
	mux.HandleFunc("GET /connections/{id}/edit", w.connectionForm)
	mux.HandleFunc("GET /connections/{id}/{format}", w.connectionPage)
	mux.HandleFunc("POST /items/batch", w.addBatch)
	mux.HandleFunc("POST /connections", w.saveConnection)
	mux.HandleFunc("POST /connections/{id}", w.saveConnection)
	mux.HandleFunc("GET /{$}", w.list)
	mux.HandleFunc("GET /cache", w.cache)
	mux.HandleFunc("GET /deliveries", w.deliveryList)
	mux.HandleFunc("GET /deliveries/{id}", w.deliveryDetail)
	mux.HandleFunc("POST /deliveries/preview", w.deliveryPreview)
	mux.HandleFunc("POST /deliveries/{id}/{action}", w.deliveryQueue)
	mux.HandleFunc("/api/read/", w.readAPI)
	mux.HandleFunc("POST /reindex", func(rw http.ResponseWriter, r *http.Request) {
		if w.Semantic == nil {
			http.Error(rw, "indexing is disabled", 503)
			return
		}
		if err := w.Store.RebuildEmbeddings(); err != nil {
			http.Error(rw, "index unavailable", 503)
			return
		}
		http.Redirect(rw, r, "/", http.StatusSeeOther)
	})
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
	w.renderStatus(rw, name, data, http.StatusOK)
}
func (w *Web) renderStatus(rw http.ResponseWriter, name string, data pageData, status int) {
	data.CSRF = w.CSRF
	data.BackgroundPaused = w.BackgroundPaused
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Buffer before writing so a template failure does not send a partial success.
	var b strings.Builder
	if err := w.tmpl.ExecuteTemplate(&b, name, data); err != nil {
		http.Error(rw, "не вдалося показати сторінку", http.StatusInternalServerError)
		return
	}
	rw.WriteHeader(status)
	rw.Write([]byte(b.String()))
}
func (w *Web) list(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := Filters{Query: q.Get("q"), Placement: q.Get("placement"), Rating: q.Get("rating"), Read: q.Get("read"), State: q.Get("state"), Sort: q.Get("sort"), Direction: q.Get("direction"), Filter: q.Get("filter"), ConfidenceMin: q.Get("confidence_min"), ConfidenceMax: q.Get("confidence_max")}
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
	result, err := w.search(r, f, q.Get("mode"))
	if err != nil {
		http.Error(rw, "некоректні параметри пошуку", 400)
		return
	}
	w.render(rw, "list", pageData{Items: result.Items, Filters: f, SearchMode: q.Get("mode"), SearchWarning: result.Warning, Indexed: result.Indexed, Eligible: result.Eligible, EmbeddingStatus: w.Store.EmbeddingStatus(), EmbeddingsEnabled: w.Semantic != nil})
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
	var digests []Digest
	var feedback []DigestFeedback
	if snap != nil {
		digest, err = w.Store.DigestForSnapshot(snap.ID)
		if raw := r.URL.Query().Get("digest"); raw != "" {
			did, e := strconv.ParseInt(raw, 10, 64)
			if e != nil {
				http.Error(rw, "некоректна версія дайджесту", 400)
				return
			}
			d, e := w.Store.Digest(did, id)
			if errors.Is(e, sql.ErrNoRows) || (e == nil && d.SnapshotID != snap.ID) {
				http.NotFound(rw, r)
				return
			}
			digest = &d
			err = e
		}
		if err == nil && digest != nil {
			sections, err = SplitDigest(digest.Markdown)
		}
		if err == nil {
			digests, err = w.Store.DigestsForSnapshot(snap.ID)
		}
		if err == nil && digest != nil {
			feedback, err = w.Store.FeedbackHistory(id, digest.ID)
		}
		if err != nil {
			http.Error(rw, "дайджест недоступний; перевірте сховище", 500)
			return
		}
	} else if r.URL.Query().Get("digest") != "" {
		http.NotFound(rw, r)
		return
	}
	decisions, err := w.Store.FilterDecisions(id)
	if err != nil {
		http.Error(rw, "історія правил недоступна", 500)
		return
	}
	pending := snap == nil && (i.State == "queued" || i.State == "fetching" || i.State == "retry_wait")
	digestError := ""
	var digestFailure *JobFailure
	if snap != nil && w.Store.DigestConfig != nil {
		for _, job := range jobs {
			if job.Stage != "digest" || job.SnapshotID.Int64 != snap.ID {
				continue
			}
			if job.State == "queued" || job.State == "running" || job.State == "retry_wait" {
				pending = true
			}
			if digestError == "" && job.State == "failed" {
				digestError = job.Error
				failures, e := w.Store.JobFailures(job.ID)
				if e != nil {
					http.Error(rw, "діагностика генерації недоступна", 503)
					return
				}
				if len(failures) > 0 {
					f := failures[0]
					digestFailure = &f
				} else {
					digestFailure = &JobFailure{Kind: legacyFailureKind(job.Error)}
				}
			}
			// The newest job determines whether an old regeneration error is relevant.
			if job.State == "done" || job.State == "failed" {
				break
			}
		}
	}
	connections, err := w.Store.Connections("", id, 0)
	if err != nil {
		http.Error(rw, "зв’язки недоступні", 503)
		return
	}
	failureKind, failureLabel, failureAdvice, retryLabel, rejected := "", "", "", "Повторити генерацію дайджесту", ""
	if digestFailure != nil {
		failureKind = digestFailure.Kind
		failureLabel, failureAdvice, retryLabel = FailureAdvice(failureKind)
		rejected = digestFailure.Response
		if digestFailure.DiagnosticError != "" {
			failureAdvice += " Збережена діагностична відповідь недоступна; перевірте каталог даних або backup."
		}
	}
	current := snap != nil && snap.ID == i.Selected.Int64
	w.render(rw, "detail", pageData{DigestFailureKind: failureKind, DigestFailureLabel: failureLabel, DigestFailureAdvice: failureAdvice, DigestRetryLabel: retryLabel, DigestRejectedResponse: rejected, Connections: connections, Item: i, Snapshots: snapshots, Selected: snap, Digest: digest, Sections: sections, Digests: digests, Feedback: feedback, FilterDecisions: decisions, DigestError: digestError, DigestEnabled: w.Store.DigestConfig != nil, CanRetryDigest: current && w.Store.DigestConfig != nil && i.FilterState != "rejected", CanRegenerate: current && w.Store.DigestConfig != nil && i.FilterState != "rejected" && !pending, Pending: pending, Ratings: ratings, Jobs: jobs, RateWithoutText: r.URL.Query().Get("rate_without_text") == "1"})
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
	case "filter-override":
		err = w.Store.OverrideFilter(id)
	case "feedback":
		did, e := strconv.ParseInt(form.Get("digest"), 10, 64)
		if e != nil || did <= 0 {
			err = errors.New("некоректний дайджест")
		} else {
			err = w.Store.Feedback(id, did, form.Get("quality"), form.Get("note"))
		}
	case "digest-regenerate":
		sid, e := strconv.ParseInt(form.Get("source"), 10, 64)
		if e != nil || sid <= 0 {
			err = errors.New("некоректна версія тексту")
		} else {
			err = w.Store.RegenerateDigest(id, sid)
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
	target := "/items/" + strconv.FormatInt(id, 10)
	if r.PathValue("action") == "feedback" {
		d, e := w.Store.Digest(func() int64 { v, _ := strconv.ParseInt(form.Get("digest"), 10, 64); return v }(), id)
		if e == nil {
			target += "?source=" + strconv.FormatInt(d.SnapshotID, 10) + "&digest=" + strconv.FormatInt(d.ID, 10)
		}
	}
	http.Redirect(rw, r, target, http.StatusSeeOther)
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
		fmt.Fprintf(rw, "# %s\n\nURL: %s\n\nМодель: %s · %s\n\nТекст #%d · %s · %s · частин: %d\n\n%s\n", item.Title, item.URL, d.ActualModel, d.CreatedAt, snapshot.ID, snapshot.Completeness, snapshot.Warning, d.ChunkCount, d.Markdown)
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
