package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
)

const MinAdminTokenLength = 32

type Config struct {
	ListenAddr        string
	PublicBaseURL     string
	PublicRoutePrefix string
	AdminBaseURL      string
	DataDir           string
	AdminToken        string
	MaxUploadMB       int64
	MaxUploadBytes    int64
	MaxFileCount      int
	StorageDriver     string
	LogLevel          string
	CookieSecret      string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:        env("PREVIEW_LISTEN_ADDR", "0.0.0.0:8080"),
		PublicBaseURL:     env("PREVIEW_PUBLIC_BASE_URL", ""),
		PublicRoutePrefix: env("PREVIEW_PUBLIC_ROUTE_PREFIX", "/p"),
		AdminBaseURL:      env("PREVIEW_ADMIN_BASE_URL", ""),
		DataDir:           env("PREVIEW_DATA_DIR", "/data"),
		AdminToken:        os.Getenv("PREVIEW_ADMIN_TOKEN"),
		MaxUploadMB:       envInt64("PREVIEW_MAX_UPLOAD_MB", 200),
		MaxFileCount:      envInt("PREVIEW_MAX_FILE_COUNT", 20000),
		StorageDriver:     env("PREVIEW_STORAGE_DRIVER", "filesystem"),
		LogLevel:          env("PREVIEW_LOG_LEVEL", "info"),
		CookieSecret:      os.Getenv("PREVIEW_COOKIE_SECRET"),
	}
	cfg.MaxUploadBytes = cfg.MaxUploadMB * 1024 * 1024
	return cfg, cfg.Validate()
}

func (c *Config) Validate() error {
	var errs []error
	if strings.TrimSpace(c.ListenAddr) == "" {
		errs = append(errs, errors.New("PREVIEW_LISTEN_ADDR is required"))
	}
	if err := validateAbsoluteURL("PREVIEW_PUBLIC_BASE_URL", c.PublicBaseURL); err != nil {
		errs = append(errs, err)
	}
	if c.AdminBaseURL != "" {
		if err := validateAbsoluteURL("PREVIEW_ADMIN_BASE_URL", c.AdminBaseURL); err != nil {
			errs = append(errs, err)
		}
	}
	prefix, err := normalizeRoutePrefix(c.PublicRoutePrefix)
	if err != nil {
		errs = append(errs, err)
	} else {
		c.PublicRoutePrefix = prefix
	}
	if strings.TrimSpace(c.DataDir) == "" {
		errs = append(errs, errors.New("PREVIEW_DATA_DIR is required"))
	}
	if len(c.AdminToken) < MinAdminTokenLength {
		errs = append(errs, fmt.Errorf("PREVIEW_ADMIN_TOKEN must be at least %d characters", MinAdminTokenLength))
	}
	if c.MaxUploadMB <= 0 {
		errs = append(errs, errors.New("PREVIEW_MAX_UPLOAD_MB must be greater than zero"))
	}
	if c.MaxFileCount <= 0 {
		errs = append(errs, errors.New("PREVIEW_MAX_FILE_COUNT must be greater than zero"))
	}
	if c.StorageDriver != "filesystem" {
		errs = append(errs, errors.New("PREVIEW_STORAGE_DRIVER must be filesystem for MVP"))
	}
	if c.MaxUploadBytes == 0 {
		c.MaxUploadBytes = c.MaxUploadMB * 1024 * 1024
	}
	return errors.Join(errs...)
}

func PublicURL(baseURL, routePrefix, slug string) string {
	return strings.TrimRight(baseURL, "/") + routePrefix + "/" + slug + "/"
}

func normalizeRoutePrefix(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", errors.New("PREVIEW_PUBLIC_ROUTE_PREFIX is required")
	}
	if !strings.HasPrefix(v, "/") {
		return "", errors.New("PREVIEW_PUBLIC_ROUTE_PREFIX must start with /")
	}
	clean := path.Clean(v)
	if clean == "/" {
		return "", errors.New("PREVIEW_PUBLIC_ROUTE_PREFIX must be a non-root path")
	}
	return clean, nil
}

func validateAbsoluteURL(name, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%s is required", name)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%s must be an absolute URL", name)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s must use http or https", name)
	}
	return nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	if v, ok := os.LookupEnv(key); ok {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		parsed, err := strconv.Atoi(v)
		if err == nil {
			return parsed
		}
	}
	return fallback
}
