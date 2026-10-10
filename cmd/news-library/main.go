package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/valpere/news-library/internal/library"
)

func run() error {
	c, err := readConfig(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(c.Bind)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("bind повинен бути явно заданою loopback IP-адресою")
	}
	path, err := filepath.Abs(c.Data)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(path, 0700); err != nil {
		return err
	}
	// Claim the configured port before migrating any database. A second process
	// must not upgrade the database underneath an older running binary.
	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	defer listener.Close()
	s, err := library.Open(filepath.Join(path, "library.sqlite"))
	if err != nil {
		return err
	}
	defer s.Close()
	rules, err := library.NewRuleSet(c.FilterThreshold, c.FilterRules)
	if err != nil {
		return err
	}
	if err = s.ConfigureRules(rules); err != nil {
		return err
	}
	var generator *library.Ollama
	if !c.NoDigests {
		generator, err = library.NewOllama(c.OllamaURL, c.OllamaKey, c.DigestTimeout)
		if err != nil {
			return err
		}
		prompt := library.DigestPromptVersion
		if strings.TrimSpace(c.Interests) != "" {
			prompt = library.DigestPredictionPromptVersion
		}
		if err = s.EnableDigests(library.DigestConfig{Model: c.Model, PromptVersion: prompt, MaxInputBytes: c.InputLimit, Interests: c.Interests, ChunkLimit: c.InputLimit, ChunkMaxParts: c.ChunkMaxParts}); err != nil {
			return err
		}
	}
	fetcher := library.NewFetcher(c.FetchTimeout, c.MaxBytes)
	cache := &library.HTTPCache{Store: s, Fetcher: fetcher, TTL: c.CacheTTL, MaxBytes: c.CacheMaxBytes, Retention: c.CacheRetention}
	fetcher.Cache = cache
	origin := "http://" + listener.Addr().String()
	web, err := library.NewWeb(s, origin, c.MaxBytes)
	if err != nil {
		return err
	}
	web.Cache = cache
	if _, err = cache.Prune(time.Now()); err != nil {
		log.Print("HTTP cache initial cleanup failed")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cleanupDone := make(chan struct{})
	go func() { defer close(cleanupDone); cache.RunCleanup(ctx, c.CacheCleanupInterval) }()
	done := make(chan struct{})
	if !c.NoWorker {
		worker := &library.Worker{Store: s, Source: fetcher, Generator: &library.ChunkGenerator{Store: s, Leaf: generator}, Timeout: c.FetchTimeout, DigestTimeout: c.DigestTimeout, Lease: c.Lease, Backoff: c.Backoff, MaxAttempts: c.Attempts}
		go func() { defer close(done); worker.Run(ctx) }()
	} else {
		close(done)
	}
	server := &http.Server{Handler: web.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: max(30*time.Second, c.FetchTimeout+5*time.Second), IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	fmt.Printf("news-library: %s\n", origin)
	err = server.Serve(listener)
	stop()
	<-shutdownDone
	<-done
	<-cleanupDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
