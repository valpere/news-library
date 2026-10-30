package library

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFeedbackAndRegenerationPreserveExactContext(t *testing.T) {
	s, w := testWeb(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/feedback")
	manual(t, s, id, "Source text")
	completeFixtureDigest(t, s)
	snapshots, _ := s.Snapshots(id)
	sid := snapshots[0].ID
	d, _ := s.DigestForSnapshot(sid)
	if e := s.Rate(id, 3, nil); e != nil {
		t.Fatal(e)
	}
	post(t, w, "/items/1/feedback", url.Values{"digest": {strconv.FormatInt(d.ID, 10)}, "quality": {"inaccurate"}})
	if got, _ := s.DigestForSnapshot(sid); got.Feedback != "inaccurate" {
		t.Fatal("feedback flag not stored", got.Feedback)
	}
	i, _ := s.Item(id)
	if i.Score.Int64 != 3 || i.Read {
		t.Fatal("feedback changed usefulness")
	}
	jobs, _ := s.Jobs(id)
	if len(jobs) != 0 {
		t.Fatal("feedback scheduled AI")
	}
	if e := s.RegenerateDigest(id); e != nil {
		t.Fatal(e)
	}
	if e := s.RegenerateDigest(id); e == nil {
		t.Fatal("duplicate regeneration accepted")
	}
	j := claim(t, s)
	rw := webRequest(w, "GET", "/items/1", nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "Оновлюється дайджест") || !strings.Contains(rw.Body.String(), `name="digest" value="1"`) {
		t.Fatal("previous result disappeared while regenerating")
	}
	if e := s.CompleteDigest(j, Digest{Markdown: strings.Replace(fixtureDigest, "Один сервіс.", "Нова версія.", 1), ActualModel: j.Model, Provider: "fixture"}); e != nil {
		t.Fatal(e)
	}
	next, _ := s.DigestForSnapshot(sid)
	if next == nil || !strings.Contains(next.Markdown, "Нова версія.") {
		t.Fatal("new digest missing")
	}
	if next.Feedback != "" {
		t.Fatal("feedback of the replaced digest kept", next.Feedback)
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
	kept, e := s.DigestForSnapshot(sid)
	if e != nil || kept.ID != next.ID {
		t.Fatal("failed regeneration lost previous digest", e)
	}
	rw = webRequest(w, "GET", "/items/1", nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "Оновлення дайджесту не вдалося: fixture outage") {
		t.Fatal("failed regeneration invisible", rw.Code)
	}
	other := add(t, s, "https://example.com/foreign")
	if e = s.Feedback(other, d.ID, "good"); e == nil {
		t.Fatal("foreign feedback accepted")
	}
	if e = s.Feedback(id, d.ID, "unknown"); e == nil {
		t.Fatal("invalid quality accepted")
	}
	if e = s.RegenerateDigest(other); e == nil {
		t.Fatal("foreign source regeneration")
	}
}

func TestFeedbackCanBeClearedAndDiesWithTheText(t *testing.T) {
	s, w := testWeb(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/flag")
	manual(t, s, id, "Source text")
	completeFixtureDigest(t, s)
	if e := s.Feedback(id, id, "unclear"); e != nil {
		t.Fatal(e)
	}
	page := webRequest(w, "GET", "/items/1", nil, nil).Body.String()
	if !strings.Contains(page, `value="unclear" selected`) {
		t.Fatal("current flag not preselected")
	}
	if e := s.Feedback(id, id, ""); e != nil {
		t.Fatal(e)
	}
	if d, _ := s.DigestForSnapshot(id); d.Feedback != "" {
		t.Fatal("flag not cleared", d.Feedback)
	}
	if e := s.Feedback(id, id, "good"); e != nil {
		t.Fatal(e)
	}
	manual(t, s, id, "A different source text")
	if d, _ := s.DigestForSnapshot(id); d != nil {
		t.Fatal("digest and its flag survived the text change", d.Feedback)
	}
}
