package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andreasholmqvist/preview-publisher/internal/auth"
	"github.com/andreasholmqvist/preview-publisher/internal/config"
	"github.com/andreasholmqvist/preview-publisher/internal/store"
)

func TestPublishProtectedPreviewAndServeAfterPassword(t *testing.T) {
	app, cleanup := newTestApp(t)
	defer cleanup()

	rr := publish(t, app, map[string]string{
		"slug":          "demo",
		"password_mode": "generated",
	}, archiveBytes(t, map[string]string{"index.html": "<h1>private</h1>"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("publish status %d: %s", rr.Code, rr.Body.String())
	}
	var response publishResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Protected || response.Password == "" || !response.Created {
		t.Fatalf("unexpected response: %+v", response)
	}

	rr = httptest.NewRecorder()
	app.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/p/demo/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("gate status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `type="password"`) {
		t.Fatal("expected password gate")
	}

	form := url.Values{"password": {response.Password}, "next": {"/p/demo/"}}
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/p/demo/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("password post status %d", rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected auth cookie")
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/p/demo/", nil)
	req.AddCookie(cookies[0])
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("preview status %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "private") {
		t.Fatal("expected preview content")
	}
	if rr.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Fatal("missing robots header")
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store header")
	}
}

func TestFailedReplacementKeepsExistingPreview(t *testing.T) {
	app, cleanup := newTestApp(t)
	defer cleanup()

	rr := publish(t, app, map[string]string{"slug": "demo"}, archiveBytes(t, map[string]string{"index.html": "old"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("initial publish status %d: %s", rr.Code, rr.Body.String())
	}
	rr = publish(t, app, map[string]string{"slug": "demo"}, archiveBytes(t, map[string]string{
		"index.html":  "new",
		"../evil.txt": "bad",
	}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad publish status %d: %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	app.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/p/demo/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("preview status %d", rr.Code)
	}
	if rr.Body.String() != "old" {
		t.Fatalf("got %q, want old", rr.Body.String())
	}
}

func newTestApp(t *testing.T) (*App, func()) {
	t.Helper()
	dataDir := t.TempDir()
	for _, dir := range []string{"previews", "secrets", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dataDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{
		ListenAddr:        "127.0.0.1:0",
		PublicBaseURL:     "http://example.com",
		PublicRoutePrefix: "/p",
		DataDir:           dataDir,
		AdminToken:        "abcdefghijklmnopqrstuvwxyz123456",
		MaxUploadMB:       200,
		MaxUploadBytes:    200 * 1024 * 1024,
		MaxFileCount:      20000,
		StorageDriver:     "filesystem",
		LogLevel:          "info",
	}
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := auth.NewSigner(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, db, signer, logger), func() {
		_ = db.Close()
	}
}

func publish(t *testing.T, app *App, fields map[string]string, artifact []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("artifact", "artifact.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(artifact); err != nil {
		t.Fatal(err)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/previews", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer abcdefghijklmnopqrstuvwxyz123456")
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	return rr
}

func archiveBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
