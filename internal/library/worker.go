package library

import (
	"context"
	"errors"
	"log"
	"time"
)

type Source interface {
	Fetch(context.Context, string) (SourceText, error)
}
type Generator interface {
	Generate(context.Context, Job, SourceText) (Digest, error)
}
type Worker struct {
	Store                   *Store
	Source                  Source
	Generator               Generator
	DigestTimeout           time.Duration
	Timeout, Lease, Backoff time.Duration
	MaxAttempts             int
}

func (w *Worker) Step(ctx context.Context) (bool, error) {
	j, err := w.Store.Claim(ctx, time.Now(), w.Lease, w.MaxAttempts)
	if err != nil || j == nil {
		return false, err
	}
	timeout := w.Timeout
	if j.Stage == "digest" && w.DigestTimeout > 0 {
		timeout = w.DigestTimeout
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(w.Lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-fetchCtx.Done():
				return
			case <-ticker.C:
				if e := w.Store.Renew(*j, time.Now(), w.Lease); e != nil {
					cancel()
					return
				}
			}
		}
	}()
	var snap SourceText
	var digest Digest
	if j.Stage == "digest" {
		snap, err = w.Store.SourceText(j.ItemID)
		if err == nil {
			if w.Generator == nil {
				err = &SourceError{Message: "генерацію не налаштовано"}
			} else {
				digest, err = w.Generator.Generate(fetchCtx, *j, snap)
			}
		}
	} else if fresh, ok := w.Source.(interface {
		FetchFresh(context.Context, string) (SourceText, error)
	}); ok && j.Refresh {
		snap, err = fresh.FetchFresh(fetchCtx, j.URL)
	} else {
		snap, err = w.Source.Fetch(fetchCtx, j.URL)
	}
	close(done)
	<-heartbeatDone
	// On shutdown leave the persisted lease for recovery instead of consuming a failure.
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if err == nil {
		if j.Stage == "digest" {
			return true, w.Store.CompleteDigest(*j, digest)
		}
		return true, w.Store.Complete(*j, snap)
	}
	retry := true
	var sourceError *SourceError
	if errors.As(err, &sourceError) {
		retry = sourceError.Retry
	}
	return true, w.Store.FailError(*j, err, retry, w.MaxAttempts, time.Now(), w.Backoff*time.Duration(1<<min(j.Attempts-1, 8)))
}
func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := w.Step(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("background processing could not persist its result")
		}
		if worked && err == nil {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
