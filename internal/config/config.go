package config

import (
	"flag"
	"os"
)

type Config struct {
	DB     string
	Data   string
	Addr   string
	APIKey string

	ScanOnStart bool
}

func Load() Config {
	c := Config{}

	flag.StringVar(&c.DB, "db", env("YOMEKURO_DB", ""), "PostgreSQL DSN")
	flag.StringVar(&c.Data, "data", env("YOMEKURO_DATA", "/data"), "Data directory for covers etc.")
	flag.StringVar(&c.Addr, "addr", env("YOMEKURO_ADDR", ":8080"), "Listen address")
	flag.StringVar(&c.APIKey, "api-key", env("YOMEKURO_API_KEY", ""), "Optional API key (X-API-Key header)")
	flag.BoolVar(&c.ScanOnStart, "scan-on-start", false, "Run full library scan on startup")
	flag.Parse()

	return c
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
