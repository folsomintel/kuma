package api

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxCPUs      int32 = 8
	defaultMaxRamMB     int32 = 16384
	defaultMaxStorageGB int32 = 100
	maxJSONBodyBytes          = 64 << 10
)

// Config holds kuma-api runtime settings.
type Config struct {
	Addr                string
	APIToken            string
	RelayURL            string
	RelayAuthSecret     string
	FuseBaseURL         string
	FuseToken           string
	KumadDownloadURL    string
	KumadDownloadSHA256 string
	DefaultCPUs         int32
	DefaultRamMB        int32
	DefaultStorageGB    int32
	MaxCPUs             int32
	MaxRamMB            int32
	MaxStorageGB        int32
	MaxRuntimeSeconds   int64
}

// LoadConfig reads configuration from flags already resolved by main plus env defaults.
func LoadConfig(addr, apiToken, relayURL, relaySecret, fuseURL, fuseToken, kumadURL, kumadSHA string) (*Config, error) {
	cfg := &Config{
		Addr:                firstNonEmpty(addr, env("KUMA_API_ADDR", ":8090")),
		APIToken:            firstNonEmpty(apiToken, os.Getenv("KUMA_API_TOKEN")),
		RelayURL:            firstNonEmpty(relayURL, os.Getenv("KUMA_RELAY_URL")),
		RelayAuthSecret:     firstNonEmpty(relaySecret, os.Getenv("KUMA_RELAY_AUTH_SECRET")),
		FuseBaseURL:         firstNonEmpty(fuseURL, os.Getenv("FUSE_BASE_URL")),
		FuseToken:           firstNonEmpty(fuseToken, os.Getenv("FUSE_TOKEN")),
		KumadDownloadURL:    firstNonEmpty(kumadURL, os.Getenv("KUMAD_DOWNLOAD_URL")),
		KumadDownloadSHA256: firstNonEmpty(kumadSHA, os.Getenv("KUMAD_DOWNLOAD_SHA256")),
		DefaultCPUs:         int32Env("KUMA_CLOUD_CPUS", 2),
		DefaultRamMB:        int32Env("KUMA_CLOUD_RAM_MB", 2048),
		DefaultStorageGB:    int32Env("KUMA_CLOUD_STORAGE_GB", 10),
		MaxCPUs:             int32Env("KUMA_CLOUD_MAX_CPUS", defaultMaxCPUs),
		MaxRamMB:            int32Env("KUMA_CLOUD_MAX_RAM_MB", defaultMaxRamMB),
		MaxStorageGB:        int32Env("KUMA_CLOUD_MAX_STORAGE_GB", defaultMaxStorageGB),
		MaxRuntimeSeconds:   int64Env("KUMA_CLOUD_MAX_RUNTIME_SECONDS", 0),
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Addr == "" {
		return fmt.Errorf("addr is required")
	}
	if c.APIToken == "" {
		return fmt.Errorf("api token is required (KUMA_API_TOKEN)")
	}
	if c.RelayURL == "" {
		return fmt.Errorf("relay url is required (KUMA_RELAY_URL)")
	}
	if c.RelayAuthSecret == "" {
		return fmt.Errorf("relay auth secret is required (KUMA_RELAY_AUTH_SECRET)")
	}
	if c.FuseBaseURL == "" {
		return fmt.Errorf("fuse base url is required (FUSE_BASE_URL)")
	}
	if c.FuseToken == "" {
		return fmt.Errorf("fuse token is required (FUSE_TOKEN)")
	}
	if c.DefaultCPUs <= 0 {
		c.DefaultCPUs = 2
	}
	if c.DefaultRamMB <= 0 {
		c.DefaultRamMB = 2048
	}
	if c.DefaultStorageGB < 0 {
		c.DefaultStorageGB = 0
	}
	if c.MaxCPUs <= 0 {
		c.MaxCPUs = defaultMaxCPUs
	}
	if c.MaxRamMB <= 0 {
		c.MaxRamMB = defaultMaxRamMB
	}
	if c.MaxStorageGB <= 0 {
		c.MaxStorageGB = defaultMaxStorageGB
	}
	c.KumadDownloadURL = strings.TrimSpace(c.KumadDownloadURL)
	c.KumadDownloadSHA256 = strings.TrimSpace(strings.ToLower(c.KumadDownloadSHA256))
	if c.KumadDownloadURL != "" {
		u, err := url.Parse(c.KumadDownloadURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("KUMAD_DOWNLOAD_URL must be an https URL")
		}
		if c.KumadDownloadSHA256 == "" {
			return fmt.Errorf("KUMAD_DOWNLOAD_SHA256 is required when KUMAD_DOWNLOAD_URL is set")
		}
		if len(c.KumadDownloadSHA256) != 64 || !isHex(c.KumadDownloadSHA256) {
			return fmt.Errorf("KUMAD_DOWNLOAD_SHA256 must be a 64-char hex sha256")
		}
	}
	return nil
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func int32Env(key string, fallback int32) int32 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return fallback
	}
	return int32(n)
}

func int64Env(key string, fallback int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

// Now is overridable in tests.
var Now = time.Now
