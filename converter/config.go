package main

import (
	"os"
	"strconv"
	"time"
)

// Tunables read once at startup from the environment — see .env.example.
var (
	mokuroRetries    = envInt("CONVERTER_MOKURO_RETRIES", 3)
	mokuroRetryDelay = envDuration("CONVERTER_MOKURO_RETRY_DELAY", 5*time.Second)
	progressEvery    = envInt("CONVERTER_PROGRESS_EVERY", 10)
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
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

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
