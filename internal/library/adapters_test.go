package library

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type fakeAdapter struct {
	name, path string
	err        error
	calls      *int
}

func (a fakeAdapter) Name() string          { return a.name }
func (a fakeAdapter) Match(u *url.URL) bool { return u.Path == a.path }
func (a fakeAdapter) Extract(context.Context, *Fetcher, *url.URL) (Snapshot, error) {
	if a.calls != nil {
		*a.calls++
	}
	if a.err != nil {
		return Snapshot{}, a.err
	}
	return Snapshot{Text: "Повний текст від адаптера " + a.name, Title: "T", Origin: "fetch", Completeness: "unknown", Warning: "adapter"}, nil
}

func adapterFixture(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Generic</title></head><body><article><p>`+strings.Repeat("Загальний витяг сторінки. ", 20)+`</p></article></body></html>`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAdapterWinsAndIsRecordedAsTheExtractor(t *testing.T) {
	srv := adapterFixture(t)
	f := &Fetcher{client: srv.Client(), MaxBytes: 1 << 20, Adapters: []SourceAdapter{fakeAdapter{name: "fake-v1", path: "/paper"}}}
	snap, err := f.Fetch(context.Background(), srv.URL+"/paper")
	if err != nil || !strings.Contains(snap.Text, "від адаптера fake-v1") || snap.Extractor != "fake-v1" || snap.Completeness != "unknown" {
		t.Fatalf("%+v %v", snap, err)
	}
	other, err := f.Fetch(context.Background(), srv.URL+"/other")
	if err != nil || other.Extractor == "fake-v1" || !strings.Contains(other.Text, "Загальний витяг") {
		t.Fatalf("a non-matching URL must use the generic extractor: %+v %v", other, err)
	}
}

func TestFirstMatchingAdapterWins(t *testing.T) {
	srv := adapterFixture(t)
	first, second := 0, 0
	f := &Fetcher{client: srv.Client(), MaxBytes: 1 << 20, Adapters: []SourceAdapter{
		fakeAdapter{name: "first-v1", path: "/p", calls: &first}, fakeAdapter{name: "second-v1", path: "/p", calls: &second}}}
	snap, _ := f.Fetch(context.Background(), srv.URL+"/p")
	if snap.Extractor != "first-v1" || first != 1 || second != 0 {
		t.Fatalf("%q first=%d second=%d", snap.Extractor, first, second)
	}
}

func TestFailingAdapterFallsBackToGenericWithAWarning(t *testing.T) {
	srv := adapterFixture(t)
	f := &Fetcher{client: srv.Client(), MaxBytes: 1 << 20, Adapters: []SourceAdapter{fakeAdapter{name: "broken-v1", path: "/p", err: errors.New("формат змінився")}}}
	snap, err := f.Fetch(context.Background(), srv.URL+"/p")
	if err != nil || !strings.Contains(snap.Text, "Загальний витяг") {
		t.Fatalf("the generic extraction must still be saved: %+v %v", snap, err)
	}
	if !strings.Contains(snap.Warning, "broken-v1") || !strings.Contains(snap.Warning, "формат змінився") || strings.Contains(snap.Extractor, "broken") {
		t.Fatalf("the adapter failure must be visible: %q / %q", snap.Warning, snap.Extractor)
	}
}

func TestACancelledContextIsNotHiddenByTheFallback(t *testing.T) {
	srv := adapterFixture(t)
	f := &Fetcher{client: srv.Client(), MaxBytes: 1 << 20, Adapters: []SourceAdapter{fakeAdapter{name: "x-v1", path: "/p", err: context.Canceled}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Fetch(ctx, srv.URL+"/p"); err == nil {
		t.Fatal("a cancelled fetch must fail, not silently fall back")
	}
}

func TestSelectAdapters(t *testing.T) {
	all := DefaultAdapters()
	if len(all) == 0 {
		t.Fatal("there must be built-in adapters")
	}
	if got, err := SelectAdapters("all"); err != nil || len(got) != len(all) {
		t.Fatalf("all: %v %v", got, err)
	}
	if got, err := SelectAdapters(""); err != nil || len(got) != len(all) {
		t.Fatalf("empty means all: %v %v", got, err)
	}
	if got, err := SelectAdapters("none"); err != nil || len(got) != 0 {
		t.Fatalf("none: %v %v", got, err)
	}
	name := all[0].Name()
	if got, err := SelectAdapters(name); err != nil || len(got) != 1 || got[0].Name() != name {
		t.Fatalf("one by name: %v %v", got, err)
	}
	if _, err := SelectAdapters("neurips-typo-v9"); err == nil || !strings.Contains(err.Error(), "neurips-typo-v9") {
		t.Fatalf("a typo must be rejected, not silently ignored: %v", err)
	}
}

func TestAdapterNamesAreUniqueAndVersioned(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range DefaultAdapters() {
		n := a.Name()
		if seen[n] || !strings.Contains(n, "-v") {
			t.Errorf("adapter name %q must be unique and end with a version (-vN)", n)
		}
		seen[n] = true
	}
}
