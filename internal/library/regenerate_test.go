package library

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRegenerationPreservesExactContext(t *testing.T) {
	s, w := testWeb(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/feedback")
	seedText(t, s, id, "Source text")
	completeFixtureDigest(t, s)
	snapshots, _ := s.Texts(id)
	sid := snapshots[0].ItemID
	if e := s.Rate(id, 3, nil); e != nil {
		t.Fatal(e)
	}
	if e := s.RegenerateDigest(id); e != nil {
		t.Fatal(e)
	}
	if e := s.RegenerateDigest(id); e == nil {
		t.Fatal("duplicate regeneration accepted")
	}
	j := claim(t, s)
	rw := webRequest(w, "GET", "/items/1", nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "Оновлюється дайджест") {
		t.Fatal("previous result disappeared while regenerating")
	}
	if e := s.CompleteDigest(j, Digest{Markdown: strings.Replace(fixtureDigest, "Один сервіс.", "Нова версія.", 1), ActualModel: j.Model, Provider: "fixture"}); e != nil {
		t.Fatal(e)
	}
	next, _ := s.DigestOf(sid)
	if next == nil || !strings.Contains(next.Markdown, "Нова версія.") {
		t.Fatal("new digest missing")
	}
	if ratings, _ := s.Ratings(id); len(ratings) != 1 {
		t.Fatal("regeneration erased the rating")
	}
	if e := s.RegenerateDigest(id); e != nil {
		t.Fatal(e)
	}
	failed := claim(t, s)
	if e := s.Fail(failed, "fixture outage", false, 3, time.Now(), time.Second); e != nil {
		t.Fatal(e)
	}
	kept, e := s.DigestOf(sid)
	if e != nil || kept == nil || kept.Markdown != next.Markdown {
		t.Fatal("failed regeneration lost previous digest", e)
	}
	rw = webRequest(w, "GET", "/items/1", nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "Оновлення дайджесту не вдалося: fixture outage") {
		t.Fatal("failed regeneration invisible", rw.Code)
	}
	other := add(t, s, "https://example.com/foreign")
	if e = s.RegenerateDigest(other); e == nil {
		t.Fatal("foreign source regeneration")
	}
}

func TestDigestFeedbackIsGone(t *testing.T) {
	s, w := testWeb(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/nofeedback")
	seedText(t, s, id, "Source text")
	completeFixtureDigest(t, s)
	body := webRequest(w, "GET", "/items/1", nil, nil).Body.String()
	for _, bad := range []string{"Якість дайджесту", "Зберегти відгук", "/feedback", `name="quality"`} {
		if strings.Contains(body, bad) {
			t.Errorf("page still offers digest feedback: %q", bad)
		}
	}
	rw := webRequest(w, "POST", "/items/1/feedback", url.Values{"csrf": {w.CSRF}, "quality": {"good"}}, nil)
	if rw.Code != 404 {
		t.Errorf("POST /feedback must be an unknown action (404), got %d", rw.Code)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM pragma_table_info('digests') WHERE name='feedback'").Scan(&n)
	if n != 0 {
		t.Error("digests.feedback column still exists")
	}
}
