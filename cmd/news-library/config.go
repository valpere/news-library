package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/valpere/news-library/internal/library"
)

type config struct {
	OpenRouterEnabled        bool
	OpenRouterKey            string
	OpenRouterModels         []string
	OpenRouterTimeout        time.Duration
	OpenRouterDataCollection string
	Chat                     bool
	ChatModel                string
	ChatTimeout              time.Duration
	Knowledge                bool
	YTDLP, YouTubeLanguages  string
	FetchUserAgent           string

	Embeddings                                            bool
	ResumeRestored                                        bool
	EmbedURL, ReadToken                                   string
	EmbedTimeout                                          time.Duration
	Data, Bind, Model, OllamaURL, OllamaKey               string
	Interests, FilterRules                                string
	FilterThreshold                                       float64
	FetchTimeout, DigestTimeout, Lease, Backoff, CacheTTL time.Duration
	Attempts, InputLimit, MinDigestWords                  int
	SourceAdapters                                        string
	MaxBytes                                              int64
	NoWorker, NoDigests                                   bool
	CacheMaxBytes                                         int64
	CacheRetention, CacheCleanupInterval                  time.Duration
	ChunkMaxParts                                         int
}

func readConfig(args []string) (config, error) {
	path := ".env"
	explicitFile := false
	for i, a := range args {
		if a == "-env-file" || a == "--env-file" {
			explicitFile = true
			if i+1 < len(args) {
				path = args[i+1]
			}
		} else if strings.HasPrefix(a, "--env-file=") {
			explicitFile = true
			path = strings.TrimPrefix(a, "--env-file=")
		} else if strings.HasPrefix(a, "-env-file=") {
			explicitFile = true
			path = strings.TrimPrefix(a, "-env-file=")
		}
	}
	values := map[string]string{}
	if path != "" {
		var err error
		values, err = godotenv.Read(path)
		if err != nil && (explicitFile || !errors.Is(err, os.ErrNotExist)) {
			return config{}, errors.New("не вдалося прочитати конфігурацію .env")
		}
	}
	// Resolve supplied values before parsing defaults: an invalid lower-priority
	// setting must not reject a valid command-line override.
	flagNames := map[string]string{
		"openrouter": "NEWS_LIBRARY_OPENROUTER_ENABLED", "openrouter-models": "NEWS_LIBRARY_OPENROUTER_MODELS", "openrouter-timeout": "NEWS_LIBRARY_OPENROUTER_TIMEOUT",
		"openrouter-data-collection": "NEWS_LIBRARY_OPENROUTER_DATA_COLLECTION",
		"knowledge":                  "NEWS_LIBRARY_KNOWLEDGE",
		"chat":                       "NEWS_LIBRARY_CHAT", "chat-model": "NEWS_LIBRARY_CHAT_MODEL", "chat-timeout": "NEWS_LIBRARY_CHAT_TIMEOUT",
		"fetch-user-agent": "NEWS_LIBRARY_FETCH_USER_AGENT", "yt-dlp": "NEWS_LIBRARY_YT_DLP", "youtube-languages": "NEWS_LIBRARY_YOUTUBE_LANGUAGES",
		"embeddings": "NEWS_LIBRARY_EMBEDDINGS", "embed-url": "NEWS_LIBRARY_EMBED_URL", "embed-timeout": "NEWS_LIBRARY_EMBED_TIMEOUT", "read-token": "NEWS_LIBRARY_READ_TOKEN",
		"chunk-max-parts": "NEWS_LIBRARY_CHUNK_MAX_PARTS", "cache-max-bytes": "NEWS_LIBRARY_CACHE_MAX_BYTES", "cache-retention": "NEWS_LIBRARY_CACHE_RETENTION", "cache-cleanup-interval": "NEWS_LIBRARY_CACHE_CLEANUP_INTERVAL",
		"interests": "NEWS_LIBRARY_INTERESTS", "filter-rules": "NEWS_LIBRARY_FILTER_RULES", "filter-threshold": "NEWS_LIBRARY_FILTER_THRESHOLD",
		"data": "NEWS_LIBRARY_DATA", "bind": "NEWS_LIBRARY_BIND", "model": "OLLAMA_MODEL", "ollama-url": "OLLAMA_API_URL",
		"fetch-timeout": "NEWS_LIBRARY_FETCH_TIMEOUT", "digest-timeout": "NEWS_LIBRARY_DIGEST_TIMEOUT", "lease": "NEWS_LIBRARY_LEASE",
		"backoff": "NEWS_LIBRARY_BACKOFF", "cache-ttl": "NEWS_LIBRARY_CACHE_TTL", "attempts": "NEWS_LIBRARY_ATTEMPTS",
		"max-bytes": "NEWS_LIBRARY_MAX_BYTES", "digest-max-bytes": "NEWS_LIBRARY_DIGEST_MAX_BYTES", "digest-min-words": "NEWS_LIBRARY_DIGEST_MIN_WORDS", "source-adapters": "NEWS_LIBRARY_SOURCE_ADAPTERS", "no-worker": "NEWS_LIBRARY_NO_WORKER", "no-digests": "NEWS_LIBRARY_NO_DIGESTS",
	}
	overrides := map[string]string{}
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		variable, known := flagNames[name]
		if !known {
			if name == "env-file" && !hasValue {
				i++
			}
			continue
		}
		if !hasValue {
			if name == "openrouter" || name == "chat" || name == "knowledge" || name == "no-worker" || name == "no-digests" || name == "embeddings" {
				value = "true"
			} else if i+1 < len(args) {
				i++
				value = args[i]
			} else {
				continue
			}
		}
		overrides[variable] = value
	}
	get := func(name, fallback string) string {
		if v, ok := overrides[name]; ok {
			return v
		}
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if v, ok := values[name]; ok {
			return v
		}
		return fallback
	}
	duration := func(name, fallback string) (time.Duration, error) {
		v, e := time.ParseDuration(get(name, fallback))
		if e != nil {
			return 0, fmt.Errorf("некоректне налаштування %s", name)
		}
		return v, nil
	}
	integer := func(name, fallback string) (int64, error) {
		v, e := strconv.ParseInt(get(name, fallback), 10, 64)
		if e != nil {
			return 0, fmt.Errorf("некоректне налаштування %s", name)
		}
		return v, nil
	}
	boolean := func(name, fallback string) (bool, error) {
		v, e := strconv.ParseBool(get(name, fallback))
		if e != nil {
			return false, fmt.Errorf("некоректне налаштування %s", name)
		}
		return v, nil
	}
	c := config{FetchUserAgent: get("NEWS_LIBRARY_FETCH_USER_AGENT", library.DefaultSourceUserAgent), YTDLP: get("NEWS_LIBRARY_YT_DLP", "yt-dlp"), YouTubeLanguages: get("NEWS_LIBRARY_YOUTUBE_LANGUAGES", "uk,en"), EmbedURL: get("NEWS_LIBRARY_EMBED_URL", "http://localhost:11434/api/embed"), ReadToken: get("NEWS_LIBRARY_READ_TOKEN", ""), Interests: get("NEWS_LIBRARY_INTERESTS", ""), FilterRules: get("NEWS_LIBRARY_FILTER_RULES", "[]"), Data: get("NEWS_LIBRARY_DATA", "data"), Bind: get("NEWS_LIBRARY_BIND", "127.0.0.1:8787"), Model: get("OLLAMA_MODEL", "gemma4:cloud"), OllamaURL: get("OLLAMA_API_URL", "http://localhost:11434/api/chat"), OllamaKey: get("OLLAMA_API_KEY", "")}
	c.ChatModel = get("NEWS_LIBRARY_CHAT_MODEL", c.Model)
	if strings.TrimSpace(c.ChatModel) == "" {
		c.ChatModel = c.Model
	}
	var thresholdErr error
	c.FilterThreshold, thresholdErr = strconv.ParseFloat(get("NEWS_LIBRARY_FILTER_THRESHOLD", "0.8"), 64)
	if thresholdErr != nil {
		return c, errors.New("некоректний NEWS_LIBRARY_FILTER_THRESHOLD")
	}
	for _, entry := range []struct {
		name, value string
		target      *time.Duration
	}{{"NEWS_LIBRARY_CHAT_TIMEOUT", "90s", &c.ChatTimeout}, {"NEWS_LIBRARY_EMBED_TIMEOUT", "30s", &c.EmbedTimeout}, {"NEWS_LIBRARY_CACHE_RETENTION", "168h", &c.CacheRetention}, {"NEWS_LIBRARY_CACHE_CLEANUP_INTERVAL", "1h", &c.CacheCleanupInterval}, {"NEWS_LIBRARY_FETCH_TIMEOUT", "30s", &c.FetchTimeout}, {"NEWS_LIBRARY_DIGEST_TIMEOUT", "300s", &c.DigestTimeout}, {"NEWS_LIBRARY_LEASE", "1m", &c.Lease}, {"NEWS_LIBRARY_BACKOFF", "10s", &c.Backoff}, {"NEWS_LIBRARY_CACHE_TTL", "24h", &c.CacheTTL}} {
		v, e := duration(entry.name, entry.value)
		if e != nil {
			return c, e
		}
		*entry.target = v
	}
	attempts, e := integer("NEWS_LIBRARY_ATTEMPTS", "3")
	if e != nil {
		return c, e
	}
	limit, e := integer("NEWS_LIBRARY_DIGEST_MAX_BYTES", "163840")
	if e != nil {
		return c, e
	}
	c.MaxBytes, e = integer("NEWS_LIBRARY_MAX_BYTES", "4194304")
	if e != nil {
		return c, e
	}
	if attempts < 1 || attempts > 100 || limit < 1024 || limit > 4<<20 {
		return c, errors.New("attempts має бути 1..100, digest-max-bytes — 1KiB..4MiB")
	}
	c.Attempts = int(attempts)
	minWords, e := integer("NEWS_LIBRARY_DIGEST_MIN_WORDS", "40")
	if e != nil || minWords < 0 || minWords > 1000 {
		return c, errors.New("digest-min-words має бути 0..1000 (0 вимикає відсічку)")
	}
	c.MinDigestWords = int(minWords)
	c.SourceAdapters = get("NEWS_LIBRARY_SOURCE_ADAPTERS", "all")
	if _, e = library.SelectAdapters(c.SourceAdapters); e != nil {
		return c, e
	}
	c.InputLimit = int(limit)
	c.Embeddings, e = boolean("NEWS_LIBRARY_EMBEDDINGS", "false")
	if e != nil {
		return c, e
	}
	c.OpenRouterEnabled, e = boolean("NEWS_LIBRARY_OPENROUTER_ENABLED", "false")
	if e != nil {
		return c, e
	}
	c.OpenRouterKey = get("OPENROUTER_API_KEY", "")
	c.OpenRouterDataCollection = get("NEWS_LIBRARY_OPENROUTER_DATA_COLLECTION", "deny")
	c.OpenRouterModels, e = library.ParseFreeModels(get("NEWS_LIBRARY_OPENROUTER_MODELS", "[]"))
	if e != nil {
		return c, e
	}
	c.OpenRouterTimeout, e = time.ParseDuration(get("NEWS_LIBRARY_OPENROUTER_TIMEOUT", "90s"))
	if e != nil || c.OpenRouterTimeout <= 0 {
		return c, errors.New("invalid OpenRouter timeout")
	}
	c.Knowledge, e = boolean("NEWS_LIBRARY_KNOWLEDGE", "true")
	if e != nil {
		return c, e
	}
	c.NoWorker, e = boolean("NEWS_LIBRARY_NO_WORKER", "false")
	if e != nil {
		return c, e
	}
	c.Chat, e = boolean("NEWS_LIBRARY_CHAT", "true")
	if e != nil {
		return c, e
	}
	c.NoDigests, e = boolean("NEWS_LIBRARY_NO_DIGESTS", "false")
	if e != nil {
		return c, e
	}
	c.CacheMaxBytes, e = integer("NEWS_LIBRARY_CACHE_MAX_BYTES", "536870912")
	if e != nil {
		return c, e
	}
	parts, e := integer("NEWS_LIBRARY_CHUNK_MAX_PARTS", "64")
	if e != nil {
		return c, e
	}
	if parts < 2 || parts > 512 {
		return c, errors.New("chunk-max-parts має бути 2..512")
	}
	c.ChunkMaxParts = int(parts)
	flags := flag.NewFlagSet("news-library", flag.ContinueOnError)
	flags.BoolVar(&c.Knowledge, "knowledge", c.Knowledge, "автоматичні метадані та нотатки-зв’язкові після дайджесту")
	flags.BoolVar(&c.OpenRouterEnabled, "openrouter", c.OpenRouterEnabled, "enable explicit free-only digest fallback")
	freeModelsJSON, _ := json.Marshal(c.OpenRouterModels)
	freeModelsFlag := string(freeModelsJSON)
	flags.StringVar(&freeModelsFlag, "openrouter-models", freeModelsFlag, "JSON array of concrete provider/model:free IDs")
	flags.DurationVar(&c.OpenRouterTimeout, "openrouter-timeout", c.OpenRouterTimeout, "maximum OpenRouter HTTP request time")
	flags.StringVar(&c.OpenRouterDataCollection, "openrouter-data-collection", c.OpenRouterDataCollection, "deny provider collection (default) or explicitly allow it")
	flags.StringVar(&c.FetchUserAgent, "fetch-user-agent", c.FetchUserAgent, "User-Agent for source HTTP requests")
	flags.StringVar(&c.YTDLP, "yt-dlp", c.YTDLP, "yt-dlp executable for YouTube metadata fallback; empty disables")
	flags.StringVar(&c.YouTubeLanguages, "youtube-languages", c.YouTubeLanguages, "preferred subtitle languages, comma-separated")
	flags.BoolVar(&c.ResumeRestored, "resume-restored", false, "явно відновити workers після restore")
	flags.BoolVar(&c.Embeddings, "embeddings", c.Embeddings, "локальна bge-m3 індексація")
	flags.StringVar(&c.EmbedURL, "embed-url", c.EmbedURL, "локальний Ollama /api/embed")
	flags.DurationVar(&c.EmbedTimeout, "embed-timeout", c.EmbedTimeout, "таймаут одного embedding запиту")
	flags.StringVar(&c.ReadToken, "read-token", c.ReadToken, "bearer token read API")
	// Do not expose the configured credential in -h / PrintDefaults.
	flags.Lookup("read-token").DefValue = ""
	flags.Int64Var(&c.CacheMaxBytes, "cache-max-bytes", c.CacheMaxBytes, "ліміт HTTP cache bodies")
	flags.DurationVar(&c.CacheRetention, "cache-retention", c.CacheRetention, "скільки тримати прострочений кеш для explicit stale")
	flags.DurationVar(&c.CacheCleanupInterval, "cache-cleanup-interval", c.CacheCleanupInterval, "інтервал очищення HTTP-кешу")
	flags.IntVar(&c.ChunkMaxParts, "chunk-max-parts", c.ChunkMaxParts, "максимальна кількість частин довгого тексту")
	flags.StringVar(&path, "env-file", path, "локальний файл конфігурації; порожньо = без файлу")
	flags.StringVar(&c.Interests, "interests", c.Interests, "поточні інтереси для прогнозу; порожньо = без прогнозу")
	flags.StringVar(&c.FilterRules, "filter-rules", c.FilterRules, "JSON-масив правил відсіву")
	flags.Float64Var(&c.FilterThreshold, "filter-threshold", c.FilterThreshold, "поріг відсіву R > T")
	flags.StringVar(&c.Data, "data", c.Data, "каталог SQLite, текстів, дайджестів та HTTP-кешу")
	flags.StringVar(&c.Bind, "bind", c.Bind, "loopback host:port")
	flags.BoolVar(&c.Chat, "chat", c.Chat, "увімкнути cloud розуміння діалогу")
	flags.StringVar(&c.ChatModel, "chat-model", c.ChatModel, "cloud модель діалогу")
	flags.DurationVar(&c.ChatTimeout, "chat-timeout", c.ChatTimeout, "час планування діалогу")
	flags.StringVar(&c.Model, "model", c.Model, "Ollama cloud модель дайджесту")
	flags.StringVar(&c.OllamaURL, "ollama-url", c.OllamaURL, "локальний Ollama /api/chat")
	flags.DurationVar(&c.FetchTimeout, "fetch-timeout", c.FetchTimeout, "час отримання одного джерела")
	flags.DurationVar(&c.DigestTimeout, "digest-timeout", c.DigestTimeout, "час cloud генерації")
	flags.DurationVar(&c.Lease, "lease", c.Lease, "lease черги з heartbeat")
	flags.DurationVar(&c.Backoff, "backoff", c.Backoff, "початкова затримка повтору")
	flags.DurationVar(&c.CacheTTL, "cache-ttl", c.CacheTTL, "максимальний TTL HTTP-кешу; 0 = bypass")
	flags.IntVar(&c.Attempts, "attempts", c.Attempts, "максимум спроб завдання")
	flags.Int64Var(&c.MaxBytes, "max-bytes", c.MaxBytes, "ліміт джерела/ручного тексту")
	flags.IntVar(&c.InputLimit, "digest-max-bytes", c.InputLimit, "ліміт тексту одного дайджесту")
	flags.StringVar(&c.SourceAdapters, "source-adapters", c.SourceAdapters, "адаптери джерел: all, none або список імен через кому")
	flags.IntVar(&c.MinDigestWords, "digest-min-words", c.MinDigestWords, "джерела коротші за це число слів без дайджесту (0 вимикає)")
	flags.BoolVar(&c.NoWorker, "no-worker", c.NoWorker, "лише UI, без виконання черги")
	flags.BoolVar(&c.NoDigests, "no-digests", c.NoDigests, "вимкнути генерацію, зберігаючи отримання тексту")
	if e = flags.Parse(args); e != nil {
		return c, e
	}
	if flags.NArg() != 0 {
		return c, errors.New("неочікувані аргументи")
	}
	if _, e = library.ParseYouTubeLanguages(c.YouTubeLanguages); e != nil {
		return c, e
	}
	if c.EmbedTimeout <= 0 || c.EmbedTimeout > 10*time.Minute {
		return c, errors.New("некоректний embed timeout")
	}
	if c.ReadToken != "" && (len(c.ReadToken) < 32 || len(c.ReadToken) > 256 || strings.ContainsAny(c.ReadToken, " \t\r\n")) {
		return c, errors.New("read token must have 32..256 non-whitespace bytes")
	}
	if c.Embeddings {
		if _, e = library.NewBGE(c.EmbedURL, c.EmbedTimeout); e != nil {
			return c, errors.New("некоректний embedding endpoint")
		}
	}
	if c.ChatTimeout <= 0 || c.ChatTimeout > 5*time.Minute {
		return c, errors.New("chat-timeout має бути більше нуля й не більше 5m")
	}
	if strings.TrimSpace(c.FetchUserAgent) == "" || len(c.FetchUserAgent) > 512 || strings.ContainsAny(c.FetchUserAgent, "\r\n") {
		return c, errors.New("invalid source User-Agent")
	}
	if c.OpenRouterDataCollection != "deny" && c.OpenRouterDataCollection != "allow" {
		return c, errors.New("OpenRouter data collection must be deny or allow")
	}
	if c.OpenRouterEnabled {
		if _, e := library.NewFreeOpenRouter(c.OpenRouterKey, c.OpenRouterModels, c.OpenRouterTimeout, c.OpenRouterDataCollection); e != nil {
			return c, e
		}
	}
	if c.FetchTimeout <= 0 || c.DigestTimeout <= 0 || c.Lease < time.Second || c.Backoff <= 0 || c.CacheTTL < 0 || c.Attempts < 1 || c.Attempts > 100 || c.MaxBytes < 1024 || c.MaxBytes > 64<<20 || c.InputLimit < 1024 || c.InputLimit > 4<<20 {
		return c, errors.New("некоректні таймаути/ліміти конфігурації")
	}
	if c.CacheMaxBytes < 1024 || c.CacheMaxBytes > 1<<40 || c.CacheRetention < 0 || c.CacheCleanupInterval < time.Second || c.ChunkMaxParts < 2 || c.ChunkMaxParts > 512 {
		return c, errors.New("некоректні ліміти cache/chunks")
	}
	if len(c.Interests) > 8000 {
		return c, errors.New("профіль інтересів завеликий")
	}
	if _, e = library.NewRuleSet(c.FilterThreshold, c.FilterRules); e != nil {
		return c, e
	}
	return c, nil
}
