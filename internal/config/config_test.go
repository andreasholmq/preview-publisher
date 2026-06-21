package config

import "testing"

func TestValidateRejectsRootRoutePrefix(t *testing.T) {
	cfg := validConfig()
	cfg.PublicRoutePrefix = "/"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected root route prefix to fail")
	}
}

func TestValidateNormalizesRoutePrefix(t *testing.T) {
	cfg := validConfig()
	cfg.PublicRoutePrefix = "/p/"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.PublicRoutePrefix != "/p" {
		t.Fatalf("got %q", cfg.PublicRoutePrefix)
	}
}

func TestValidateRequiresLongAdminToken(t *testing.T) {
	cfg := validConfig()
	cfg.AdminToken = "short"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected short token to fail")
	}
}

func validConfig() Config {
	return Config{
		ListenAddr:        "127.0.0.1:0",
		PublicBaseURL:     "https://example.com",
		PublicRoutePrefix: "/p",
		DataDir:           "/tmp/preview-test",
		AdminToken:        "abcdefghijklmnopqrstuvwxyz123456",
		MaxUploadMB:       200,
		MaxUploadBytes:    200 * 1024 * 1024,
		MaxFileCount:      20000,
		StorageDriver:     "filesystem",
		LogLevel:          "info",
	}
}
