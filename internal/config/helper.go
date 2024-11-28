package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// loadDotenv loads the appropriate .env file based on the current environment
func loadDotenv(env string) {
	// Don't load .env files in production
	if strings.ToLower(env) == "production" {
		log.Printf("info: production environment detected, using system environment variables")
		return
	}

	files := []string{
		fmt.Sprintf(".env.%s.local", env),
		fmt.Sprintf(".env.%s", env),
		".env.local",
		".env",
	}

	loaded := false
	for _, file := range files {
		if path := findEnvFile(file); path != "" {
			if err := godotenv.Load(path); err != nil {
				log.Printf("warning: could not load %s: %v", path, err)
			} else {
				log.Printf("info: loaded environment from %s", path)
				loaded = true
			}
		}
	}

	if !loaded {
		log.Printf("info: no .env files found, using system environment variables")
	}
}

// getEnv retrieves an environment variable value or returns the fallback
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

// getEnvBool retrieves a boolean environment variable
func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return fallback
	}

	v = strings.ToLower(strings.TrimSpace(v))

	switch v {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}

	b, err := strconv.ParseBool(v)
	if err != nil {
		panic(fmt.Sprintf("environment variable %q must be a boolean, got %q", key, v))
	}

	return b
}

// getEnvDuration retrieves a duration environment variable
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return fallback
	}

	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		panic(fmt.Sprintf("environment variable %q must be a valid duration (e.g. 15s, 1m, 1h), got %q", key, v))
	}
	return d
}

// getEnvInt retrieves an integer environment variable
func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return fallback
	}

	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		panic(fmt.Sprintf("environment variable %q must be an integer, got %q", key, v))
	}
	return n
}

func getEnvSlice(key string, fallback []string) []string {
	raw := getEnv(key, "")
	if raw == "" {
		return fallback
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// findEnvFile searches for .env in current and parent directories
func findEnvFile(filename string) string {
	// Check if absolute path
	if filepath.IsAbs(filename) {
		if fileExists(filename) {
			return filename
		}
		return ""
	}

	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		path := filepath.Join(dir, filename)
		if fileExists(path) {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// fileExists checks if a file exists and is not a directory
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
