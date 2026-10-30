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
	post(t, w, "/items/1/feedback", url.Values{"digest": {strconv.FormatInt(d.ID, 10)}, "quality": {"inaccurate"}, "note": {"A bad digest does not make the source uninteresting"}})
	i, _ := s.Item(id)
	if i.Score.Int64 != 3 || i.Read {
		t.Fatal("feedback changed usefulness")
	}
	jobs, _ := s.Jobs(id)
	if jobs[0].State != "done" {
		t.Fatal("feedback scheduled AI")
	}
	if e := s.RegenerateDigest(id, sid); e != nil {
		t.Fatal(e)
	}
	if e := s.RegenerateDigest(id, sid); e == nil {
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
	if next.ID == d.ID {
		t.Fatal("old artifact overwritten")
	}
	old, e := s.Digest(d.ID, id)
	if e != nil || old.Markdown != fixtureDigest {
		t.Fatal("old digest unavailable", e)
	}
	history, e := s.FeedbackHistory(id, d.ID)
	if e != nil || len(history) != 1 || history[0].Quality != "inaccurate" {
		t.Fatal("feedback moved", history, e)
	}
	ratings, _ := s.Ratings(id)
	if len(ratings) != 1 {
		t.Fatal("rating relinked")
	}
	rw = webRequest(w, "GET", "/items/1?source="+strconv.FormatInt(sid, 10)+"&digest="+strconv.FormatInt(d.ID, 10), nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "A bad digest") || !strings.Contains(rw.Body.String(), `name="digest" value="1"`) {
		t.Fatal("historical feedback unavailable", rw.Code)
	}
	if e = s.RegenerateDigest(id, sid); e != nil {
		t.Fatal(e)
	}
	failed := claim(t, s)
	if e = s.Fail(failed, "fixture outage", false, 3, time.Now(), time.Second); e != nil {
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
	if e = s.Feedback(other, d.ID, "good", ""); e == nil {
		t.Fatal("foreign feedback accepted")
	}
	if e = s.Feedback(id, d.ID, "unknown", ""); e == nil {
		t.Fatal("invalid quality accepted")
	}
	if _, e = s.DB.Exec("UPDATE digest_feedback SET quality='good'"); e == nil {
		t.Fatal("mutable feedback")
	}
	if e = s.RegenerateDigest(other, sid); e == nil {
		t.Fatal("foreign source regeneration")
	}
}
