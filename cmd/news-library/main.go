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
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/valpere/news-library/internal/library"
)

func run() error {
	data := flag.String("data", "data", "каталог SQLite та runtime даних")
	bind := flag.String("bind", "127.0.0.1:8787", "loopback host:port")
	timeout := flag.Duration("fetch-timeout", 30*time.Second, "загальний час отримання тексту")
	lease := flag.Duration("lease", time.Minute, "термін lease; heartbeat поновлює його")
	backoff := flag.Duration("backoff", 10*time.Second, "початкова затримка повтору")
	attempts := flag.Int("attempts", 3, "максимальна кількість спроб fetch")
	maxBytes := flag.Int64("max-bytes", 4<<20, "ліміт байтів відповіді джерела та ручного тексту")
	noWorker := flag.Bool("no-worker", false, "лише UI, без обробки черги")
	flag.Parse()
	host, port, err := net.SplitHostPort(*bind)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("bind повинен бути явно заданою loopback IP-адресою")
	}
	if *timeout <= 0 || *lease < time.Second || *backoff <= 0 || *attempts < 1 || *maxBytes < 1024 || *maxBytes > 64<<20 {
		return errors.New("некоректні ліміти: timeout/backoff > 0, lease >= 1s, attempts >= 1, 1KiB <= max-bytes <= 64MiB")
	}
	path, err := filepath.Abs(*data)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(path, 0700); err != nil {
		return err
	}
	s, err := library.Open(filepath.Join(path, "library.sqlite"))
	if err != nil {
		return err
	}
	defer s.Close()
	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	defer listener.Close()
	origin := "http://" + listener.Addr().String()
	web, err := library.NewWeb(s, origin, *maxBytes)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	if !*noWorker {
		worker := &library.Worker{Store: s, Source: library.NewFetcher(*timeout, *maxBytes), Timeout: *timeout, Lease: *lease, Backoff: *backoff, MaxAttempts: *attempts}
		go func() { defer close(done); worker.Run(ctx) }()
	} else {
		close(done)
	}
	server := &http.Server{Handler: web.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10}
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
