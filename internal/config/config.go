package config

import (
	"log/slog"
	"os"
	"time"
)

// Config holds all runtime configuration, loaded once at startup.
type Config struct {
	Port               string
	AlltrailsgpxBin    string
	HTTPRequestTimeout time.Duration
	ConversionTimeout  time.Duration
	CacheTTL           time.Duration
	CacheSweepInterval time.Duration
	LogLevel           slog.Level
}

// Load reads configuration from environment variables, applying defaults where
// variables are unset or empty.
func Load() Config {
	return Config{
		Port:               getEnv("PORT", "8080"),
		AlltrailsgpxBin:    getEnv("ALLTRAILSGPX_BIN", "alltrailsgpx"),
		HTTPRequestTimeout: getDuration("HTTP_REQUEST_TIMEOUT", 30*time.Second),
		ConversionTimeout:  getDuration("CONVERSION_TIMEOUT", 15*time.Second),
		CacheTTL:           getDuration("CACHE_TTL", 5*time.Minute),
		CacheSweepInterval: getDuration("CACHE_SWEEP_INTERVAL", time.Minute),
		LogLevel:           getLogLevel("LOG_LEVEL", slog.LevelInfo),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func getLogLevel(key string, fallback slog.Level) slog.Level {
	switch os.Getenv(key) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return fallback
	}
}
