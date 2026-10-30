// Explicit bounded recovery. Default mode is read-only and offline.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/joho/godotenv"
	"github.com/valpere/news-library/internal/library"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type result struct {
	ID                      int64
	State, Error, Knowledge string
	Snapshot, Digest        int64
}

func plan(s *library.Store, ids string, limit int) ([]int64, error) {
	if ids != "" {
		out := []int64{}
		seen := map[int64]bool{}
		for _, v := range strings.Split(ids, ",") {
			id, e := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if e != nil || id < 1 {
				return nil, errors.New("invalid item IDs")
			}
			if !seen[id] {
				out = append(out, id)
				seen[id] = true
			}
		}
		if len(out) > limit {
			return nil, errors.New("selection exceeds limit")
		}
		return out, nil
	}
	rows, e := s.DB.Query("SELECT id FROM active_items WHERE selected_snapshot IS NULL ORDER BY id LIMIT ?", limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func run() error {
	data := flag.String("data", "", "existing data directory (required)")
	envFile := flag.String("env-file", ".env", "private configuration file")
	ids := flag.String("items", "", "explicit comma-separated IDs; default: active items without source")
	limit := flag.Int("limit", 30, "maximum items, 1..500")
	apply := flag.Bool("apply", false, "allow database/file changes and live source fetches")
	cloud := flag.Bool("run-cloud", false, "also allow cloud digest and knowledge generation")
	output := flag.String("output", "", "required local JSON report for apply")
	flag.Parse()
	if *data == "" || flag.NArg() != 0 || *limit < 1 || *limit > 500 || (*cloud && !*apply) || (*apply && *output == "") {
		return errors.New("use -data; mutations require -apply and -output; generation also requires -run-cloud")
	}
	root, e := filepath.Abs(*data)
	if e != nil {
		return e
	}
	var s *library.Store
	if *apply {
		values, e := godotenv.Read(*envFile)
		if e != nil {
			return errors.New("cannot read private env file")
		}
		get := func(key, def string) string {
			if v, ok := os.LookupEnv(key); ok {
				return v
			}
			if v, ok := values[key]; ok {
				return v
			}
			return def
		}
		bind := get("NEWS_LIBRARY_BIND", "127.0.0.1:8787")
		host, port, e := net.SplitHostPort(bind)
		if e != nil || port == "0" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return errors.New("recovery lock requires loopback bind")
		}
		lock, e := net.Listen("tcp", bind)
		if e != nil {
			return errors.New("site must be stopped before recovery")
		}
		defer lock.Close()
		if library.RestoredPaused(root) {
			return errors.New("restored workers are paused")
		}
		info, e := os.Lstat(filepath.Join(root, "library.sqlite"))
		if e != nil || !info.Mode().IsRegular() {
			return errors.New("existing regular database file required")
		}
		s, e = library.Open(filepath.Join(root, "library.sqlite"))
		if e != nil {
			return e
		}
		defer s.Close()
		var active int
		if e = s.DB.QueryRow("SELECT (SELECT count(*) FROM jobs WHERE state IN ('queued','running','retry_wait'))+(SELECT count(*) FROM knowledge_runs WHERE state IN ('queued','running','retry_wait'))").Scan(&active); e != nil {
			return e
		}
		if active != 0 {
			return errors.New("existing processing queue must be resolved before a new recovery batch")
		}
		selection, e := plan(s, *ids, *limit)
		if e != nil {
			return e
		}
		threshold, e := strconv.ParseFloat(get("NEWS_LIBRARY_FILTER_THRESHOLD", "0.8"), 64)
		if e != nil {
			return errors.New("invalid filter threshold")
		}
		rules, e := library.NewRuleSet(threshold, get("NEWS_LIBRARY_FILTER_RULES", "[]"))
		if e != nil {
			return e
		}
		if e = s.ConfigureRules(rules); e != nil {
			return e
		}
		fetchTimeout, e := time.ParseDuration(get("NEWS_LIBRARY_FETCH_TIMEOUT", "30s"))
		if e != nil || fetchTimeout <= 0 {
			return errors.New("invalid fetch timeout")
		}
		digestTimeout, e := time.ParseDuration(get("NEWS_LIBRARY_DIGEST_TIMEOUT", "300s"))
		if e != nil || digestTimeout <= 0 {
			return errors.New("invalid digest timeout")
		}
		maxBytes, e := strconv.ParseInt(get("NEWS_LIBRARY_MAX_BYTES", "4194304"), 10, 64)
		if e != nil || maxBytes < 1024 || maxBytes > 64<<20 {
			return errors.New("invalid source limit")
		}
		f := library.NewFetcher(fetchTimeout, maxBytes)
		f.UserAgent = get("NEWS_LIBRARY_FETCH_USER_AGENT", library.DefaultSourceUserAgent)
		f.YTDLP = get("NEWS_LIBRARY_YT_DLP", "yt-dlp")
		f.YouTubeLanguages, e = library.ParseYouTubeLanguages(get("NEWS_LIBRARY_YOUTUBE_LANGUAGES", "uk,en"))
		if e != nil {
			return e
		}
		var g *library.Ollama
		if *cloud {
			input, e := strconv.Atoi(get("NEWS_LIBRARY_DIGEST_MAX_BYTES", "163840"))
			if e != nil || input < 1024 || input > 4<<20 {
				return errors.New("invalid digest limit")
			}
			parts, e := strconv.Atoi(get("NEWS_LIBRARY_CHUNK_MAX_PARTS", "64"))
			if e != nil || parts < 2 || parts > 512 {
				return errors.New("invalid chunk count")
			}
			profile := strings.TrimSpace(get("NEWS_LIBRARY_INTERESTS", ""))
			if len(profile) > 8000 {
				return errors.New("interest profile exceeds limit")
			}
			prompt := library.DigestPromptVersion
			if profile != "" {
				prompt = library.DigestPredictionPromptVersion
			}
			cfg := library.DigestConfig{Model: get("OLLAMA_MODEL", "gemma4:cloud"), PromptVersion: prompt, MaxInputBytes: input, Interests: profile, ChunkLimit: input, ChunkMaxParts: parts}
			// Validate without scheduling unrelated snapshots.
			if !strings.HasSuffix(cfg.Model, "-cloud") && !strings.HasSuffix(cfg.Model, ":cloud") {
				return errors.New("cloud model required")
			}
			s.DigestConfig = &cfg
			s.KnowledgeEnabled, e = strconv.ParseBool(get("NEWS_LIBRARY_KNOWLEDGE", "true"))
			if e != nil {
				return errors.New("invalid knowledge setting")
			}
			g, e = library.NewOllama(get("OLLAMA_API_URL", "http://localhost:11434/api/chat"), get("OLLAMA_API_KEY", ""), digestTimeout)
			if e != nil {
				return e
			}
		}
		before, e := humanState(s.DB)
		if e != nil {
			return e
		}
		report := []result{}
		save := func() error {
			b, e := json.MarshalIndent(report, "", "  ")
			if e != nil {
				return e
			}
			return os.WriteFile(*output, append(b, '\n'), 0600)
		}
		ctx := context.Background()
		w := &library.Worker{Store: s, Source: f, Timeout: fetchTimeout, DigestTimeout: digestTimeout, Lease: time.Minute, Backoff: time.Hour, MaxAttempts: 1}
		if g != nil {
			w.Generator = &library.ChunkGenerator{Store: s, Leaf: g}
		}
		for _, id := range selection {
			item, e := s.Item(id)
			if e != nil {
				return e
			}
			fetch := !item.Selected.Valid
			if item.Selected.Valid {
				snap, e := s.Snapshot(item.Selected.Int64)
				if e != nil {
					return e
				}
				fetch = library.ValidateSourceContent(snap) != nil
			}
			if fetch {
				e = s.Retry(id)
			} else if *cloud {
				d, er := s.DigestForSnapshot(item.Selected.Int64)
				if er != nil {
					return er
				}
				if d == nil {
					e = s.RetryDigest(id)
				}
			}
			entry := result{ID: id}
			if e != nil {
				entry.Error = e.Error()
			} else {
				// At most one fetch and one digest attempt, with persistent chunk checkpoints.
				for step := 0; step < 2; step++ {
					worked, er := w.Step(ctx)
					if er != nil {
						return er
					}
					if !worked {
						break
					}
				}
				if g != nil && s.KnowledgeEnabled {
					kw := &library.KnowledgeWorker{Store: s, Generator: g, Timeout: digestTimeout, ItemID: id}
					if _, er := kw.Step(ctx); er != nil {
						return er
					}
				}
			}
			if reason, er := s.TrashReason(id); er != nil {
				return er
			} else if reason != "" {
				entry.State = "trashed"
				entry.Error = reason
			} else {
				item, e = s.Item(id)
				if e != nil {
					return e
				}
				entry.State = item.State
				entry.Snapshot = item.Selected.Int64
				if entry.Error == "" {
					entry.Error = item.Error
				}
				if item.Selected.Valid {
					d, er := s.DigestForSnapshot(item.Selected.Int64)
					if er != nil {
						return er
					}
					if d != nil {
						entry.Digest = d.ID
					}
					k, er := s.KnowledgeStatus(id, item.Selected.Int64)
					if er != nil {
						return er
					}
					if k != nil {
						entry.Knowledge = k.State
						if k.State != "done" {
							entry.Error = k.Error
						}
					}
				}
			}
			report = append(report, entry)
			if e = save(); e != nil {
				return e
			}
			after, e := humanState(s.DB)
			if e != nil {
				return e
			}
			if before != after {
				return errors.New("human judgments or reading state changed")
			}
			fmt.Printf("item=%d state=%s digest=%d knowledge=%s\n", id, entry.State, entry.Digest, entry.Knowledge)
		}
		return nil
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(root, "library.sqlite")}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	s = &library.Store{DB: db, Files: library.Files{Root: root}}
	selection, e := plan(s, *ids, *limit)
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(selection)
}
func humanState(db *sql.DB) (string, error) {
	var a, b string
	if e := db.QueryRow(`SELECT coalesce(json_group_array(json_array(id,item_id,score,scale,recorded_at,assessed_at,source_version_id,digest_id,reason)),'[]') FROM (SELECT * FROM rating_events ORDER BY id)`).Scan(&a); e != nil {
		return "", e
	}
	if e := db.QueryRow(`SELECT coalesce(json_group_array(json_array(id,is_read)),'[]') FROM (SELECT id,is_read FROM items ORDER BY id)`).Scan(&b); e != nil {
		return "", e
	}
	return a + b, nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "source-recovery:", e)
		os.Exit(1)
	}
}
