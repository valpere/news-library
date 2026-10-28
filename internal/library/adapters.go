package library

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// SourceAdapter extracts the text of one family of pages that the generic HTML
// extractor cannot read (for example articles embedded in a third-party
// reader). Adapters are selected by URL alone, never by a network probe, so
// choosing one costs nothing. They fetch through Fetcher.get, which keeps the
// private-address guard, size limit, user agent and HTTP cache.
//
// Name is a stable id with a version suffix (-vN); it is recorded as the
// snapshot's extractor. Bump it whenever the output format changes, so old and
// new texts stay distinguishable. Extract returns a Snapshot with Text, Title,
// Warning (saying where the text came from) and Completeness; Origin and
// Extractor are set by the caller.
//
// Hosts lists the lower-case hostnames whose URLs the adapter may match. The
// registry indexes adapters by host, so a URL is only compared against the few
// candidates for its host instead of every adapter; an empty list means "any
// host" (Match alone decides).
type SourceAdapter interface {
	Name() string
	Hosts() []string
	Match(u *url.URL) bool
	Extract(ctx context.Context, f *Fetcher, u *url.URL) (Snapshot, error)
}

// DefaultAdapters lists the built-in adapters in priority order: the first
// matching one wins, so put the most specific first.
func DefaultAdapters() []SourceAdapter {
	return []SourceAdapter{newNeuripsAdapter()}
}

// SelectAdapters resolves the NEWS_LIBRARY_SOURCE_ADAPTERS setting: empty or
// "all" enables every built-in adapter, "none" disables them, otherwise a
// comma-separated list of names. An unknown name is an error, so a typo cannot
// silently turn an adapter off.
func SelectAdapters(setting string) ([]SourceAdapter, error) {
	all := DefaultAdapters()
	setting = strings.TrimSpace(setting)
	switch strings.ToLower(setting) {
	case "", "all":
		return all, nil
	case "none":
		return nil, nil
	}
	var picked []SourceAdapter
	for _, name := range strings.Split(setting, ",") {
		name = strings.TrimSpace(name)
		found := false
		for _, a := range all {
			if a.Name() == name {
				picked = append(picked, a)
				found = true
				break
			}
		}
		if !found {
			names := make([]string, len(all))
			for i, a := range all {
				names[i] = a.Name()
			}
			return nil, fmt.Errorf("невідомий адаптер джерел %q; доступні: %s", name, strings.Join(names, ", "))
		}
	}
	return picked, nil
}

const (
	adapterFailureLimit = 3                // consecutive failures before an adapter rests
	adapterRest         = 30 * time.Minute // how long it rests
)

type adapterEntry struct {
	pos int // priority: lower wins
	a   SourceAdapter
}

type adapterHealth struct {
	failures int
	until    time.Time
}

// adapterState is the per-Fetcher registry: the host index and the failure
// counters. It is built lazily, so a zero Fetcher (as in tests) works.
type adapterState struct {
	mu     sync.Mutex
	built  bool
	byHost map[string][]adapterEntry
	anyOf  []adapterEntry
	health map[string]*adapterHealth
}

func (f *Fetcher) now() time.Time {
	if f.clock != nil {
		return f.clock()
	}
	return time.Now()
}

// candidates returns the adapters that may match host, in priority order.
func (f *Fetcher) candidates(host string) []SourceAdapter {
	st := &f.adapters
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.built {
		st.byHost = map[string][]adapterEntry{}
		st.health = map[string]*adapterHealth{}
		for pos, a := range f.Adapters {
			e := adapterEntry{pos, a}
			hosts := a.Hosts()
			if len(hosts) == 0 {
				st.anyOf = append(st.anyOf, e)
			}
			for _, h := range hosts {
				h = strings.ToLower(h)
				st.byHost[h] = append(st.byHost[h], e)
			}
		}
		st.built = true
	}
	list := append(append([]adapterEntry(nil), st.byHost[strings.ToLower(host)]...), st.anyOf...)
	sort.Slice(list, func(i, j int) bool { return list[i].pos < list[j].pos })
	out := make([]SourceAdapter, len(list))
	for i, e := range list {
		out[i] = e.a
	}
	return out
}

// resting reports whether the adapter is cooling down, and until when.
func (f *Fetcher) resting(name string) (time.Time, bool) {
	st := &f.adapters
	st.mu.Lock()
	defer st.mu.Unlock()
	h := st.health[name]
	if h == nil || h.until.IsZero() || !f.now().Before(h.until) {
		return time.Time{}, false
	}
	return h.until, true
}

// record updates the failure counter; the adapter rests after adapterFailureLimit
// failures in a row, and any success clears the count.
func (f *Fetcher) record(name string, failed bool) {
	st := &f.adapters
	st.mu.Lock()
	defer st.mu.Unlock()
	h := st.health[name]
	if h == nil {
		h = &adapterHealth{}
		st.health[name] = h
	}
	if !failed {
		*h = adapterHealth{}
		return
	}
	h.failures++
	if h.failures >= adapterFailureLimit {
		h.until = f.now().Add(adapterRest)
		h.failures = 0
	}
}

// extractWithAdapter runs the first matching adapter. ok is false when none
// matches or the adapter failed or is resting; the reason comes back as a note
// for the generic extraction's warning, so it is never silent. A cancelled
// context is a real error, not a reason to fall back.
func (f *Fetcher) extractWithAdapter(ctx context.Context, u *url.URL) (snap Snapshot, note string, ok bool, err error) {
	for _, a := range f.candidates(u.Hostname()) {
		if !a.Match(u) {
			continue
		}
		if until, rest := f.resting(a.Name()); rest {
			return Snapshot{}, fmt.Sprintf("адаптер %s тимчасово вимкнено після %d збоїв поспіль (до %s); взято загальний витяг сторінки", a.Name(), adapterFailureLimit, until.UTC().Format("15:04 UTC")), false, nil
		}
		snap, e := a.Extract(ctx, f, u)
		if e != nil {
			if ctx.Err() != nil || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				return Snapshot{}, "", false, e
			}
			f.record(a.Name(), true)
			return Snapshot{}, fmt.Sprintf("адаптер %s не спрацював (%s); взято загальний витяг сторінки", a.Name(), e.Error()), false, nil
		}
		f.record(a.Name(), false)
		snap.Origin = "fetch"
		snap.Extractor = a.Name()
		snap.RetrievedAt = nowUTC()
		return snap, "", true, nil
	}
	return Snapshot{}, "", false, nil
}
