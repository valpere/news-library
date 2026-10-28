package library

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
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
type SourceAdapter interface {
	Name() string
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

// extractWithAdapter runs the first matching adapter. ok is false when none
// matches or the adapter failed; a failure is returned as a note for the
// generic extraction's warning, so it is never silent. A cancelled context is a
// real error, not a reason to fall back.
func (f *Fetcher) extractWithAdapter(ctx context.Context, u *url.URL) (snap Snapshot, note string, ok bool, err error) {
	for _, a := range f.Adapters {
		if !a.Match(u) {
			continue
		}
		snap, e := a.Extract(ctx, f, u)
		if e != nil {
			if ctx.Err() != nil || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				return Snapshot{}, "", false, e
			}
			return Snapshot{}, fmt.Sprintf("адаптер %s не спрацював (%s); взято загальний витяг сторінки", a.Name(), e.Error()), false, nil
		}
		snap.Origin = "fetch"
		snap.Extractor = a.Name()
		snap.RetrievedAt = nowUTC()
		return snap, "", true, nil
	}
	return Snapshot{}, "", false, nil
}
