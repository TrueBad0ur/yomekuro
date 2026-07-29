package config

import (
	"flag"
	"os"
	"strconv"
)

type Config struct {
	DB   string
	Data string
	Addr string

	AdminUser     string
	AdminPassword string

	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	ScanOnStart bool

	// How often the Settings page re-fetches the job list. Served to the frontend
	// via GET /api/config, since JS can't read container env.
	JobsPollIntervalMS int
	// How many open EPUB zips the LRU cache holds, so concurrent chapter reads
	// reuse a handle instead of reopening the archive.
	ZipCacheSize int
}

func Load() Config {
	c := Config{}

	flag.StringVar(&c.DB, "db", env("YOMEKURO_DB", ""), "PostgreSQL DSN")
	flag.StringVar(&c.Data, "data", env("YOMEKURO_DATA", "/data"), "Data directory for covers etc.")
	flag.StringVar(&c.Addr, "addr", env("YOMEKURO_ADDR", ":8080"), "Listen address")
	flag.StringVar(&c.AdminUser, "admin-user", env("YOMEKURO_ADMIN_USER", "admin"), "Admin username (created on first run)")
	flag.StringVar(&c.AdminPassword, "admin-password", env("YOMEKURO_ADMIN_PASSWORD", ""), "Admin password (created on first run)")
	flag.StringVar(&c.GoogleClientID, "google-client-id", env("YOMEKURO_GOOGLE_CLIENT_ID", ""), "Google OAuth client ID")
	flag.StringVar(&c.GoogleClientSecret, "google-client-secret", env("YOMEKURO_GOOGLE_CLIENT_SECRET", ""), "Google OAuth client secret")
	flag.StringVar(&c.GoogleRedirectURL, "google-redirect-url", env("YOMEKURO_GOOGLE_REDIRECT_URL", ""), "Google OAuth callback URL")
	flag.BoolVar(&c.ScanOnStart, "scan-on-start", boolEnv("YOMEKURO_SCAN_ON_START", true), "Run full library scan on startup")
	flag.IntVar(&c.JobsPollIntervalMS, "jobs-poll-interval-ms", intEnv("YOMEKURO_JOBS_POLL_INTERVAL_MS", 20000), "How often (ms) the Settings page polls for conversion job updates")
	flag.IntVar(&c.ZipCacheSize, "zip-cache-size", intEnv("YOMEKURO_ZIP_CACHE_SIZE", 20), "Number of open EPUB archives kept cached")
	flag.Parse()

	return c
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func boolEnv(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func intEnv(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
