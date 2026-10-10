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
	w, e := NewWeb(s, "http://127.0.0.1:8080", 1<<20)
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
	vs, _ := s.Snapshots(id)
	sid := strconv.FormatInt(vs[0].ID, 10)
	post(t, w, base+"rating", url.Values{"score": {"3"}, "source": {sid}, "reason": {"useful"}})
	post(t, w, base+"placement", url.Values{"placement": {"library"}})
	post(t, w, base+"tags", url.Values{"tags": {"go, reading"}})
	rw = webRequest(w, "GET", "/?q=knowledge&placement=library&rating=3&tags=go&read=0&sort=relevance", nil, nil)
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
	if len(ratings) != 2 || ratings[1].SourceVersionID.Int64 != vs[0].ID || !ratings[1].AssessedAt.Valid || ratings[0].AssessedAt.Valid {
		t.Fatal("UI rating provenance")
	}
	post(t, w, base+"read", url.Values{"read": {"1"}})
	i, _ = s.Item(id)
	if !i.Read || i.Placement != "library" || i.Score.Int64 != 0 {
		t.Fatal("UI independent actions")
	}
	rw = webRequest(w, "GET", "/items/1?source="+sid, nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "offline useful knowledge") {
		t.Fatal("old text unavailable")
	}
	post(t, w, base+"retry", url.Values{})
	if e := s.Retry(id); e == nil {
		t.Fatal("UI duplicate retry")
	}
}
func TestWebRejectsInvalidScoresAndForeignContext(t *testing.T) {
	s, w := testWeb(t)
	a := add(t, s, "https://example.com/a")
	b := add(t, s, "https://example.com/b")
	manual(t, s, b, "foreign text")
	vs, _ := s.Snapshots(b)
	for _, score := range []string{"-1", "1.5", "9223372036854775808", "NaN", ""} {
		rw := webRequest(w, "POST", fmt.Sprintf("/items/%d/rating", a), url.Values{"csrf": {w.CSRF}, "score": {score}}, nil)
		if rw.Code != 400 {
			t.Fatal("score accepted", score, rw.Code)
		}
	}
	rw := webRequest(w, "POST", "/items/1/rating", url.Values{"csrf": {w.CSRF}, "score": {"3"}, "source": {strconv.FormatInt(vs[0].ID, 10)}}, nil)
	if rw.Code != 400 {
		t.Fatal("foreign snapshot accepted")
	}
	events, _ := s.Ratings(a)
	if len(events) != 0 {
		t.Fatal("invalid score inserted event")
	}
	rw = webRequest(w, "GET", "/items/1?source="+strconv.FormatInt(vs[0].ID, 10), nil, nil)
	if rw.Code != 404 {
		t.Fatal("foreign version shown")
	}
	post(t, w, "/items/1/rating", url.Values{"score": {"9223372036854775807"}})
	events, _ = s.Ratings(a)
	if len(events) != 1 || events[0].Score != 9223372036854775807 {
		t.Fatal("int64 maximum rejected")
	}
}
func TestWebMutationBoundaries(t *testing.T) {
	s, w := testWeb(t)
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
		{"rebound host", url.Values{"read": {"1"}, "csrf": {w.CSRF}}, map[string]string{"Host": "evil.example:8080"}},
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
