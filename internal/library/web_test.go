package library

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func testWeb(t *testing.T) (*Store, *Web) {
	t.Helper()
	s := testStore(t)
	w, e := NewWeb(s, "http://127.0.0.1:8787", 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	return s, w
}
func webRequest(w *Web, method, path string, values url.Values, headers map[string]string) *httptest.ResponseRecorder {
	body := ""
	if values != nil {
		body = values.Encode()
	}
	r := httptest.NewRequest(method, w.Origin+path, strings.NewReader(body))
	if values != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		if k == "Host" {
			r.Host = v
		} else {
			r.Header.Set(k, v)
		}
	}
	rw := httptest.NewRecorder()
	w.Handler().ServeHTTP(rw, r)
	return rw
}
func post(t *testing.T, w *Web, path string, values url.Values) {
	t.Helper()
	values.Set("csrf", w.CSRF)
	rw := webRequest(w, "POST", path, values, nil)
	if rw.Code != http.StatusSeeOther {
		t.Fatalf("POST %s: %d %s", path, rw.Code, rw.Body.String())
	}
}
func TestWebWorkflowAndGETDoesNotRead(t *testing.T) {
	s, w := testWeb(t)
	post(t, w, "/items", url.Values{"url": {"https://example.com/news"}})
	id := int64(1)
	base := "/items/1/"
	rw := webRequest(w, "GET", "/items/1", nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "Текст ще не отримано") {
		t.Fatal("detail", rw.Code, rw.Body.String())
	}
	i, _ := s.Item(id)
	if i.Read || i.Score.Valid {
		t.Fatal("GET mutated item")
	}
	post(t, w, base+"manual", url.Values{"text": {"offline useful knowledge <script>alert(1)</script>"}})
	vs, _ := s.Texts(id)
	sid := strconv.FormatInt(vs[0].ItemID, 10)
	post(t, w, base+"rating", url.Values{"score": {"3"}, "source": {sid}, "reason": {"useful"}})
	post(t, w, base+"tags", url.Values{"tags": {"go, reading"}})
	rw = webRequest(w, "GET", "/?q=knowledge&usefulness=3&tags=go&read=0&sort=relevance", nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "/items/1") {
		t.Fatal("UI search", rw.Code, rw.Body.String())
	}
	rw = webRequest(w, "GET", "/items/1", nil, nil)
	if rw.Code != 200 || strings.Contains(rw.Body.String(), "<script>alert(1)</script>") || !strings.Contains(rw.Body.String(), "&lt;script&gt;") {
		t.Fatal("unsafe text rendering", rw.Body.String())
	}
	if rw.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("form origin would be suppressed")
	}
	if rw.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing CSP")
	}
	i, _ = s.Item(id)
	if i.Read {
		t.Fatal("GET marks read")
	}
	manual(t, s, id, "updated manual text")
	post(t, w, base+"rating", url.Values{"score": {"0"}, "unknown_date": {"1"}})
	ratings, _ := s.Ratings(id)
	if len(ratings) != 1 || ratings[0].Score != 0 || ratings[0].AssessedAt.Valid {
		t.Fatal("UI rating provenance")
	}
	post(t, w, base+"read", url.Values{"read": {"1"}})
	i, _ = s.Item(id)
	if !i.Read || i.Score.Int64 != 0 {
		t.Fatal("UI independent actions")
	}
	post(t, w, base+"retry", url.Values{})
	if e := s.Retry(id); e == nil {
		t.Fatal("UI duplicate retry")
	}
}
func TestWebRejectsInvalidScores(t *testing.T) {
	s, w := testWeb(t)
	a := add(t, s, "https://example.com/a")
	for _, score := range []string{"-1", "7", "1.5", "9223372036854775808", "NaN", ""} {
		rw := webRequest(w, "POST", fmt.Sprintf("/items/%d/rating", a), url.Values{"csrf": {w.CSRF}, "score": {score}}, nil)
		if rw.Code != 400 {
			t.Fatal("score accepted", score, rw.Code)
		}
	}
	events, _ := s.Ratings(a)
	if len(events) != 0 {
		t.Fatal("invalid score inserted event")
	}
	post(t, w, "/items/1/rating", url.Values{"score": {"6"}})
	events, _ = s.Ratings(a)
	if len(events) != 1 || events[0].Score != 6 {
		t.Fatal("top level rejected")
	}
}
func protectedTestWeb(t *testing.T) (*Store, *Web) {
	t.Helper()
	s := testStore(t)
	w, err := NewWeb(s, "http://192.0.2.1:8787", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return s, w
}
func TestWebMutationBoundaries(t *testing.T) {
	s, w := protectedTestWeb(t)
	id := add(t, s, "https://example.com/a")
	tests := []struct {
		name    string
		values  url.Values
		headers map[string]string
	}{
		{"missing token", url.Values{"read": {"1"}}, nil},
		{"wrong token", url.Values{"read": {"1"}, "csrf": {"wrong"}}, nil},
		{"foreign origin", url.Values{"read": {"1"}, "csrf": {w.CSRF}}, map[string]string{"Origin": "https://evil.example"}},
		{"cross site", url.Values{"read": {"1"}, "csrf": {w.CSRF}}, map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"rebound host", url.Values{"read": {"1"}, "csrf": {w.CSRF}}, map[string]string{"Host": "evil.example:8787"}},
		{"agent credentials", url.Values{"read": {"1"}, "csrf": {w.CSRF}}, map[string]string{"Authorization": "Bearer agent-token"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rw := webRequest(w, "POST", "/items/1/read", tc.values, tc.headers)
			if rw.Code != 403 {
				t.Fatal("mutation allowed", rw.Code)
			}
			i, _ := s.Item(id)
			if i.Read {
				t.Fatal("unauthorized write")
			}
		})
	}
	rw := webRequest(w, "GET", "/items/1/read?read=1&csrf="+w.CSRF, nil, nil)
	if rw.Code != 405 {
		t.Fatal("GET mutation allowed", rw.Code)
	}
	rw = webRequest(w, "POST", "/items/1/read?csrf="+w.CSRF, url.Values{"read": {"1"}}, nil)
	if rw.Code != 403 {
		t.Fatal("query CSRF accepted")
	}
	rw = webRequest(w, "POST", "/items/1/manual", url.Values{"csrf": {w.CSRF}, "text": {strings.Repeat("x", int(w.MaxText)+1)}}, nil)
	if rw.Code != 400 {
		t.Fatal("oversize manual accepted")
	}
	post(t, w, "/items/1/read", url.Values{"read": {"1"}})
}

func TestWebLocalhostWithoutCSRF(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/localhost")
	if w.CSRF != "" {
		t.Fatal("local CSRF enabled")
	}
	for _, path := range []string{"/", "/items/1"} {
		rw := webRequest(w, "GET", path, nil, map[string]string{"Host": "localhost:8787"})
		if rw.Code != 200 {
			t.Fatal(path, rw.Code)
		}
	}
	for _, headers := range []map[string]string{
		nil,
		{"Host": "localhost:8787", "Origin": "https://example.invalid", "Sec-Fetch-Site": "cross-site"},
		{"Host": "localhost:8787", "Origin": "null"},
		{"Host": "localhost:8787", "Origin": "http://localhost:9999"},
	} {
		for _, token := range []string{"", "previous-process-token"} {
			form := url.Values{"read": {"1"}}
			if token != "" {
				form.Set("csrf", token)
			}
			rw := webRequest(w, "POST", "/items/1/read", form, headers)
			if rw.Code != 303 {
				t.Fatal(headers, token, rw.Code, rw.Body.String())
			}
			item, _ := s.Item(id)
			if !item.Read {
				t.Fatal("POST did not persist")
			}
		}
	}
	for _, headers := range []map[string]string{
		{"Host": "localhost:9999"},
		{"Host": "evil.localhost:8787"},
		{"Authorization": "Bearer agent-token"},
	} {
		rw := webRequest(w, "POST", "/items/1/read", url.Values{"read": {"0"}}, headers)
		if rw.Code != 403 {
			t.Fatal(headers, rw.Code)
		}
		item, _ := s.Item(id)
		if !item.Read {
			t.Fatal("rejected request changed state")
		}
	}
}

func TestWebCSRFPolicyByOrigin(t *testing.T) {
	s := testStore(t)
	for _, tc := range []struct {
		origin  string
		enabled bool
	}{
		{"http://localhost:8787", false},
		{"http://127.0.0.1:8787", false},
		{"http://127.0.0.2:8787", false},
		{"http://[::1]:8787", false},
		{"http://192.0.2.1:8787", true},
		{"http://example.test:8787", true},
	} {
		w, err := NewWeb(s, tc.origin, 1<<20)
		if err != nil || (w.CSRF != "") != tc.enabled {
			t.Fatal(tc, err)
		}
	}
}

func TestWebShowsContentBeforeRating(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/unseen")
	for _, state := range []string{"queued", "fetching", "retry_wait", "needs_input"} {
		if _, err := s.DB.Exec("UPDATE items SET state=? WHERE id=?", state, id); err != nil {
			t.Fatal(err)
		}
		rw := webRequest(w, "GET", "/items/1", nil, nil)
		if rw.Code != 200 {
			t.Fatal("card failed", rw.Code)
		}
		html := rw.Body.String()
		if strings.Contains(html, `action="/items/1/rating"`) || strings.Contains(html, `name="score"`) {
			t.Fatal("unseen item prompts for rating", state)
		}
		if state != "needs_input" {
			if !strings.Contains(html, "Посилання збережено") || !strings.Contains(html, `data-pending="true"`) || !strings.Contains(html, `src="/static/progress.js"`) {
				t.Fatal("missing processing guidance", state)
			}
		} else if !strings.Contains(html, "Текст недоступний") || strings.Contains(html, `src="/static/progress.js"`) {
			t.Fatal("unavailable source still looks pending")
		}
	}
	// Someone who already read the source can explicitly choose the separate path.
	rw := webRequest(w, "GET", "/items/1?rate_without_text=1", nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), `action="/items/1/rating"`) || !strings.Contains(rw.Body.String(), "поза бібліотекою") {
		t.Fatal("explicit external assessment unavailable")
	}
	i, _ := s.Item(id)
	events, _ := s.Ratings(id)
	if i.Read || i.Score.Valid || len(events) != 0 {
		t.Fatal("opening assessment path made a judgment")
	}
	post(t, w, "/items/1/rating", url.Values{"score": {"3"}})
	events, _ = s.Ratings(id)
	if len(events) != 1 {
		t.Fatal("invented text provenance for external reading")
	}
	manual(t, s, id, "Read this available text first")
	rw = webRequest(w, "GET", "/items/1", nil, nil)
	html := rw.Body.String()
	textPos, ratePos := strings.Index(html, "Read this available text first"), strings.Index(html, `action="/items/1/rating"`)
	if rw.Code != 200 || textPos < 0 || ratePos < textPos || strings.Contains(html, `src="/static/progress.js"`) {
		t.Fatal("rating does not follow available text")
	}
}

func TestProgressPageReadsDoNotChangeState(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/pending")
	for range 5 {
		rw := webRequest(w, "GET", "/items/1", nil, nil)
		if rw.Code != 200 {
			t.Fatal(rw.Code)
		}
	}
	i, _ := s.Item(id)
	jobs, _ := s.Jobs(id)
	snapshots, _ := s.Texts(id)
	ratings, _ := s.Ratings(id)
	if i.Read || i.Score.Valid || i.State != "queued" || len(jobs) != 1 || jobs[0].Attempts != 0 || len(snapshots) != 0 || len(ratings) != 0 {
		t.Fatal("progress polling mutates library state")
	}
	rw := webRequest(w, "GET", "/static/progress.js", nil, nil)
	if rw.Code != 200 || !strings.HasPrefix(rw.Header().Get("Content-Type"), "text/javascript") || !strings.Contains(rw.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
		t.Fatal("progress script unavailable under CSP")
	}
}
