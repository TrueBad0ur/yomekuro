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

	ScanOnStart bool
}

func Load() Config {
	c := Config{}

	flag.StringVar(&c.DB, "db", env("YOMEKURO_DB", ""), "PostgreSQL DSN")
	flag.StringVar(&c.Data, "data", env("YOMEKURO_DATA", "/data"), "Data directory for covers etc.")
	flag.StringVar(&c.Addr, "addr", env("YOMEKURO_ADDR", ":8080"), "Listen address")
	flag.StringVar(&c.AdminUser, "admin-user", env("YOMEKURO_ADMIN_USER", "admin"), "Admin username (created on first run)")
	flag.StringVar(&c.AdminPassword, "admin-password", env("YOMEKURO_ADMIN_PASSWORD", ""), "Admin password (created on first run)")
	flag.BoolVar(&c.ScanOnStart, "scan-on-start", boolEnv("YOMEKURO_SCAN_ON_START", true), "Run full library scan on startup")
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
