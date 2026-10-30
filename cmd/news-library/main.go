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
	if len(os.Args) > 1 && (os.Args[1] == "backup" || os.Args[1] == "restore" || os.Args[1] == "verify-backup") {
		return archiveCommand(os.Args[1], os.Args[2:])
	}
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
	if err = applyRestorePause(&c, path); err != nil {
		return err
	}
	if library.RestoredPaused(path) {
		log.Print("restored data: background execution paused; use -resume-restored to resume")
	}

	rules, err := library.NewRuleSet(c.FilterThreshold, c.FilterRules)
	if err != nil {
		return err
	}
	if err = s.ConfigureRules(rules); err != nil {
		return err
	}
	s.KnowledgeEnabled = c.Knowledge && !c.NoDigests
	var digestGenerator library.Generator
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
		if err = s.EnableDigests(library.DigestConfig{Model: c.Model, PromptVersion: prompt, MaxInputBytes: c.InputLimit, Interests: c.Interests, ChunkLimit: c.InputLimit, ChunkMaxParts: c.ChunkMaxParts, MinSourceWords: c.MinDigestWords}); err != nil {
			return err
		}
	}
	digestGenerator = generator
	if generator != nil && c.OpenRouterEnabled {
		secondary, e := library.NewFreeOpenRouter(c.OpenRouterKey, c.OpenRouterModels, c.OpenRouterTimeout, c.OpenRouterDataCollection)
		if e != nil {
			return e
		}
		secondary.Store = s
		digestGenerator = &library.DigestFallback{Primary: generator, Secondary: secondary, Store: s, PrimaryBudget: c.DigestTimeout / 2}
	}
	fetcher := library.NewFetcher(c.FetchTimeout, c.MaxBytes)
	fetcher.UserAgent = c.FetchUserAgent
	fetcher.YTDLP = c.YTDLP
	if fetcher.Adapters, err = library.SelectAdapters(c.SourceAdapters); err != nil {
		return err
	}
	fetcher.YouTubeLanguages, _ = library.ParseYouTubeLanguages(c.YouTubeLanguages)
	cache := &library.HTTPCache{Store: s, Fetcher: fetcher, TTL: c.CacheTTL, MaxBytes: c.CacheMaxBytes, Retention: c.CacheRetention}
	fetcher.Cache = cache
	origin := "http://" + listener.Addr().String()
	web, err := library.NewWeb(s, origin, c.MaxBytes)
	if err != nil {
		return err
	}
	web.ChatModel = c.ChatModel
	web.ChatTimeout = c.ChatTimeout
	if c.Chat {
		planner, e := library.NewOllama(c.OllamaURL, c.OllamaKey, c.ChatTimeout)
		if e != nil {
			return e
		}
		web.ChatPlanner = planner
	}
	web.Cache = cache
	web.BackgroundPaused = c.NoWorker
	web.ReadToken = c.ReadToken
	var semantic *library.Semantic
	if c.Embeddings {
		embedder, e := library.NewBGE(c.EmbedURL, c.EmbedTimeout)
		if e != nil {
			return e
		}
		semantic = &library.Semantic{Store: s, Embedder: embedder, ChunkBytes: 4096, Dimensions: 1024, Timeout: c.EmbedTimeout}
		web.Semantic = semantic
	}
	if _, err = cache.Prune(time.Now()); err != nil {
		log.Print("HTTP cache initial cleanup failed")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	knowledgeDone := make(chan struct{})
	if s.KnowledgeEnabled && !c.NoWorker {
		kw := &library.KnowledgeWorker{Store: s, Generator: generator, Timeout: c.DigestTimeout}
		go func() { defer close(knowledgeDone); kw.Run(ctx) }()
	} else {
		close(knowledgeDone)
	}
	embeddingDone := make(chan struct{})
	if semantic != nil && !c.NoWorker {
		go func() { defer close(embeddingDone); semantic.Run(ctx) }()
	} else {
		close(embeddingDone)
	}
	cleanupDone := make(chan struct{})
	go func() { defer close(cleanupDone); cache.RunCleanup(ctx, c.CacheCleanupInterval) }()
	done := make(chan struct{})
	if !c.NoWorker {
		worker := &library.Worker{Store: s, Source: fetcher, Generator: &library.ChunkGenerator{Store: s, Leaf: digestGenerator}, Timeout: c.FetchTimeout, DigestTimeout: c.DigestTimeout, Lease: c.Lease, Backoff: c.Backoff, MaxAttempts: c.Attempts}
		go func() { defer close(done); worker.Run(ctx) }()
	} else {
		close(done)
	}
	server := &http.Server{Handler: web.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: max(30*time.Second, c.FetchTimeout+5*time.Second, c.EmbedTimeout+5*time.Second, c.ChatTimeout+c.EmbedTimeout+10*time.Second), IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10}
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
	<-embeddingDone
	<-knowledgeDone
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

func applyRestorePause(c *config, path string) error {
	if c.ResumeRestored {
		return library.ResumeRestored(path)
	}
	if library.RestoredPaused(path) {
		c.NoWorker = true
	}
	return nil
}
