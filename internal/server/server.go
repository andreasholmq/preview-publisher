package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/andreasholmqvist/preview-publisher/internal/archive"
	"github.com/andreasholmqvist/preview-publisher/internal/auth"
	"github.com/andreasholmqvist/preview-publisher/internal/config"
	"github.com/andreasholmqvist/preview-publisher/internal/password"
	"github.com/andreasholmqvist/preview-publisher/internal/slug"
	"github.com/andreasholmqvist/preview-publisher/internal/store"
)

type App struct {
	cfg    config.Config
	store  *store.DB
	signer *auth.Signer
	logger *slog.Logger
}

func New(cfg config.Config, db *store.DB, signer *auth.Signer, logger *slog.Logger) *App {
	if logger == nil {
		logger = slog.Default()
	}
	return &App{cfg: cfg, store: db, signer: signer, logger: logger}
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	case r.Method == http.MethodGet && r.URL.Path == "/robots.txt":
		a.handleRobots(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/"):
		a.handleAPI(w, r)
	case a.isPublicPath(r.URL.Path):
		a.handlePublic(w, r)
	default:
		a.handleDashboard(w, r)
	}
}

func (a *App) handleRobots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, "User-agent: *\nDisallow: %s/\n", a.cfg.PublicRoutePrefix)
}

func (a *App) handleAPI(w http.ResponseWriter, r *http.Request) {
	if !a.validBearer(r) {
		writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
		return
	}
	if r.URL.Path == "/api/previews" {
		switch r.Method {
		case http.MethodGet:
			a.apiListPreviews(w, r)
		case http.MethodPost:
			a.apiPublishPreview(w, r)
		default:
			methodNotAllowed(w)
		}
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/previews/") {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/previews/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	rawSlug := parts[0]
	if _, err := slug.Normalize(rawSlug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			a.apiGetPreview(w, r, rawSlug)
		case http.MethodDelete:
			a.apiDeletePreview(w, r, rawSlug)
		default:
			methodNotAllowed(w)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "password" {
		switch r.Method {
		case http.MethodPost:
			a.apiSetPassword(w, r, rawSlug)
		case http.MethodDelete:
			a.apiClearPassword(w, r, rawSlug)
		default:
			methodNotAllowed(w)
		}
		return
	}
	http.NotFound(w, r)
}

func (a *App) apiListPreviews(w http.ResponseWriter, r *http.Request) {
	previews, err := a.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list previews: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, previews)
}

func (a *App) apiGetPreview(w http.ResponseWriter, r *http.Request, previewSlug string) {
	preview, ok, err := a.store.Get(r.Context(), previewSlug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get preview: "+err.Error())
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (a *App) apiPublishPreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxUploadBytes+1024*1024)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "parse multipart form: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, fileHeader, err := r.FormFile("artifact")
	if err != nil {
		writeError(w, http.StatusBadRequest, "artifact is required")
		return
	}
	defer file.Close()
	if fileHeader.Size > a.cfg.MaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "artifact exceeds upload limit")
		return
	}

	previewSlug, err := a.publishSlug(r.Context(), r.FormValue("slug"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	mode := strings.TrimSpace(r.FormValue("password_mode"))
	if mode == "" {
		mode = "none"
	}
	passwordHash, returnedPassword, err := passwordForPublish(mode, r.FormValue("password"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	stageRoot, err := os.MkdirTemp(filepath.Join(a.cfg.DataDir, "tmp"), "preview-"+previewSlug+"-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create staging directory: "+err.Error())
		return
	}
	defer os.RemoveAll(stageRoot)
	filesDir := filepath.Join(stageRoot, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "create files directory: "+err.Error())
		return
	}
	stats, err := archive.ExtractTarGz(file, filesDir, a.cfg.MaxFileCount)
	if err != nil {
		writeError(w, http.StatusBadRequest, "extract artifact: "+err.Error())
		return
	}

	finalRoot := filepath.Join(a.cfg.DataDir, "previews", previewSlug)
	restore, commit, err := replaceDir(stageRoot, finalRoot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "replace preview files: "+err.Error())
		return
	}
	upsert, err := a.store.Upsert(r.Context(), store.UpsertPreview{
		Slug:         previewSlug,
		Title:        title,
		PublicURL:    config.PublicURL(a.cfg.PublicBaseURL, a.cfg.PublicRoutePrefix, previewSlug),
		PasswordHash: passwordHash,
		SizeBytes:    stats.SizeBytes,
		FileCount:    stats.FileCount,
	})
	if err != nil {
		restore()
		writeError(w, http.StatusInternalServerError, "save preview metadata: "+err.Error())
		return
	}
	commit()

	response := publishResponse{
		Slug:      upsert.Preview.Slug,
		PublicURL: upsert.Preview.PublicURL,
		Title:     upsert.Preview.Title,
		Protected: upsert.Preview.Protected,
		Created:   upsert.Created,
		Replaced:  upsert.Replaced,
		Password:  returnedPassword,
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) apiDeletePreview(w http.ResponseWriter, r *http.Request, previewSlug string) {
	deleted, err := a.store.Delete(r.Context(), previewSlug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete preview metadata: "+err.Error())
		return
	}
	if !deleted {
		http.NotFound(w, r)
		return
	}
	if err := os.RemoveAll(filepath.Join(a.cfg.DataDir, "previews", previewSlug)); err != nil {
		writeError(w, http.StatusInternalServerError, "delete preview files: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) apiSetPassword(w http.ResponseWriter, r *http.Request, previewSlug string) {
	var input struct {
		Password string `json:"password"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	returnedPassword := input.Password
	if strings.TrimSpace(returnedPassword) == "" {
		generated, err := password.Generate()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "generate password: "+err.Error())
			return
		}
		returnedPassword = generated
	}
	hash, err := password.Hash(returnedPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ok, err := a.store.SetPassword(r.Context(), previewSlug, hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "set password: "+err.Error())
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":      previewSlug,
		"protected": true,
		"password":  returnedPassword,
	})
}

func (a *App) apiClearPassword(w http.ResponseWriter, r *http.Request, previewSlug string) {
	ok, err := a.store.ClearPassword(r.Context(), previewSlug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "clear password: "+err.Error())
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":      previewSlug,
		"protected": false,
	})
}

func (a *App) handlePublic(w http.ResponseWriter, r *http.Request) {
	setPreviewHeaders(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	previewSlug, fileRel, hasTrailingSlash, ok := a.parsePublicPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !hasTrailingSlash && fileRel == "" {
		redirectPath := r.URL.Path + "/"
		if r.URL.RawQuery != "" {
			redirectPath += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, redirectPath, http.StatusMovedPermanently)
		return
	}
	preview, exists, err := a.store.Get(r.Context(), previewSlug)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.NotFound(w, r)
		return
	}
	if preview.Protected && !a.authorizedForPreview(r, previewSlug) {
		if r.Method == http.MethodPost {
			a.handlePreviewPasswordPost(w, r, preview)
			return
		}
		a.renderPasswordGate(w, http.StatusOK, r.URL.RequestURI())
		return
	}
	if r.Method == http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if fileRel == "" {
		fileRel = "index.html"
	}
	cleanRel, err := cleanPublicFilePath(fileRel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fullPath := filepath.Join(a.cfg.DataDir, "previews", previewSlug, "files", filepath.FromSlash(cleanRel))
	if err := ensureFileWithin(filepath.Join(a.cfg.DataDir, "previews", previewSlug, "files"), fullPath); err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if info.IsDir() {
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		fullPath = filepath.Join(fullPath, "index.html")
		if _, err := os.Stat(fullPath); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	http.ServeFile(w, r, fullPath)
}

func (a *App) handlePreviewPasswordPost(w http.ResponseWriter, r *http.Request, preview store.Preview) {
	if err := r.ParseForm(); err != nil {
		a.renderPasswordGate(w, http.StatusBadRequest, r.URL.RequestURI())
		return
	}
	if !password.Verify(r.FormValue("password"), preview.PasswordHash) {
		a.renderPasswordGate(w, http.StatusUnauthorized, r.URL.RequestURI())
		return
	}
	cookiePath := a.cfg.PublicRoutePrefix + "/" + preview.Slug + "/"
	a.signer.SetCookie(w, auth.PreviewCookieName(preview.Slug), "preview:"+preview.Slug, cookiePath, a.secureCookies(r), time.Now())
	next := r.FormValue("next")
	if next == "" || !strings.HasPrefix(next, cookiePath) {
		next = cookiePath
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *App) renderPasswordGate(w http.ResponseWriter, status int, next string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = passwordGateTemplate.Execute(w, map[string]string{"Next": next})
}

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		if !a.authorizedForAdmin(r) {
			a.renderLogin(w, http.StatusOK)
			return
		}
		a.renderDashboard(w, r)
	case r.URL.Path == "/login" && r.Method == http.MethodPost:
		a.handleLogin(w, r)
	case r.URL.Path == "/logout" && r.Method == http.MethodPost:
		a.clearAdminCookie(w, r)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case r.URL.Path == "/delete" && r.Method == http.MethodPost:
		if !a.authorizedForAdmin(r) {
			a.renderLogin(w, http.StatusUnauthorized)
			return
		}
		previewSlug := r.FormValue("slug")
		if _, err := slug.Normalize(previewSlug); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = a.store.Delete(r.Context(), previewSlug)
		_ = os.RemoveAll(filepath.Join(a.cfg.DataDir, "previews", previewSlug))
		http.Redirect(w, r, "/", http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderLogin(w, http.StatusBadRequest)
		return
	}
	token := r.FormValue("token")
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.cfg.AdminToken)) != 1 {
		a.renderLogin(w, http.StatusUnauthorized)
		return
	}
	a.signer.SetCookie(w, auth.AdminCookieName, "admin", "/", a.secureCookies(r), time.Now())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) clearAdminCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.AdminCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.secureCookies(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) renderLogin(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginTemplate.Execute(w, nil)
}

func (a *App) renderDashboard(w http.ResponseWriter, r *http.Request) {
	previews, err := a.store.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = dashboardTemplate.Execute(w, map[string]any{"Previews": previews})
}

func (a *App) publishSlug(ctx context.Context, raw string) (string, error) {
	if strings.TrimSpace(raw) != "" {
		return slug.Normalize(raw)
	}
	for range 20 {
		generated, err := slug.Generate()
		if err != nil {
			return "", err
		}
		if _, exists, err := a.store.Get(ctx, generated); err != nil {
			return "", err
		} else if !exists {
			return generated, nil
		}
	}
	return "", errors.New("could not generate unique slug")
}

func (a *App) isPublicPath(requestPath string) bool {
	return requestPath == a.cfg.PublicRoutePrefix || strings.HasPrefix(requestPath, a.cfg.PublicRoutePrefix+"/")
}

func (a *App) parsePublicPath(requestPath string) (string, string, bool, bool) {
	if !strings.HasPrefix(requestPath, a.cfg.PublicRoutePrefix+"/") {
		return "", "", false, false
	}
	rest := strings.TrimPrefix(requestPath, a.cfg.PublicRoutePrefix+"/")
	if rest == "" {
		return "", "", false, false
	}
	parts := strings.SplitN(rest, "/", 2)
	if parts[0] == "" {
		return "", "", false, false
	}
	previewSlug, err := slug.Normalize(parts[0])
	if err != nil {
		return "", "", false, false
	}
	if len(parts) == 1 {
		return previewSlug, "", false, true
	}
	return previewSlug, parts[1], true, true
}

func (a *App) validBearer(r *http.Request) bool {
	value := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(value, "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(a.cfg.AdminToken)) == 1
}

func (a *App) authorizedForPreview(r *http.Request, previewSlug string) bool {
	return a.signer.VerifyRequestCookie(r, auth.PreviewCookieName(previewSlug), "preview:"+previewSlug, time.Now())
}

func (a *App) authorizedForAdmin(r *http.Request) bool {
	return a.signer.VerifyRequestCookie(r, auth.AdminCookieName, "admin", time.Now())
}

func (a *App) secureCookies(r *http.Request) bool {
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	for _, raw := range []string{a.cfg.PublicBaseURL, a.cfg.AdminBaseURL} {
		u, err := url.Parse(raw)
		if err == nil && u.Scheme == "https" {
			return true
		}
	}
	return false
}

func passwordForPublish(mode, explicit string) (string, string, error) {
	switch mode {
	case "none":
		return "", "", nil
	case "generated":
		generated, err := password.Generate()
		if err != nil {
			return "", "", err
		}
		hash, err := password.Hash(generated)
		return hash, generated, err
	case "provided":
		if explicit == "" {
			return "", "", errors.New("password is required when password_mode=provided")
		}
		hash, err := password.Hash(explicit)
		return hash, explicit, err
	default:
		return "", "", errors.New("password_mode must be none, generated, or provided")
	}
}

type publishResponse struct {
	Slug      string `json:"slug"`
	PublicURL string `json:"public_url"`
	Title     string `json:"title,omitempty"`
	Protected bool   `json:"protected"`
	Created   bool   `json:"created"`
	Replaced  bool   `json:"replaced"`
	Password  string `json:"password,omitempty"`
}

func setPreviewHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func cleanPublicFilePath(fileRel string) (string, error) {
	fileRel = strings.ReplaceAll(fileRel, "\\", "/")
	if strings.HasPrefix(fileRel, "/") || path.IsAbs(fileRel) {
		return "", errors.New("absolute path")
	}
	cleaned := path.Clean(fileRel)
	if cleaned == "." {
		return "index.html", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("path traversal")
	}
	return cleaned, nil
}

func ensureFileWithin(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("path escapes root")
	}
	return nil
}

func replaceDir(staged, final string) (restore func(), commit func(), err error) {
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return nil, nil, err
	}
	suffix, err := randomHex(8)
	if err != nil {
		return nil, nil, err
	}
	backup := final + ".old-" + suffix
	existed := false
	if _, err := os.Stat(final); err == nil {
		existed = true
		if err := os.Rename(final, backup); err != nil {
			return nil, nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if err := os.Rename(staged, final); err != nil {
		if existed {
			_ = os.Rename(backup, final)
		}
		return nil, nil, err
	}
	restore = func() {
		_ = os.RemoveAll(final)
		if existed {
			_ = os.Rename(backup, final)
		}
	}
	commit = func() {
		if existed {
			_ = os.RemoveAll(backup)
		}
	}
	return restore, commit, nil
}

func randomHex(bytesLen int) (string, error) {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

var passwordGateTemplate = template.Must(template.New("password").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Password</title>
  <style>
    html, body { height: 100%; margin: 0; font-family: system-ui, sans-serif; }
    body { display: grid; place-items: center; background: #f7f7f8; }
    input { width: min(22rem, 80vw); padding: 0.85rem 1rem; border: 1px solid #b7bcc7; border-radius: 6px; font: inherit; }
  </style>
</head>
<body>
  <form method="post">
    <input name="password" type="password" autocomplete="current-password" autofocus>
    <input name="next" type="hidden" value="{{.Next}}">
  </form>
</body>
</html>`))

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Preview Publisher</title>
  <style>
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; font-family: system-ui, sans-serif; background: #f6f7f9; color: #151922; }
    form { width: min(24rem, calc(100vw - 2rem)); display: grid; gap: 0.75rem; }
    input, button { font: inherit; padding: 0.75rem 0.9rem; border-radius: 6px; }
    input { border: 1px solid #b8bfcc; }
    button { border: 0; background: #1b5e5a; color: white; cursor: pointer; }
  </style>
</head>
<body>
  <form method="post" action="/login">
    <input name="token" type="password" autocomplete="current-password" autofocus placeholder="Admin token">
    <button type="submit">Sign in</button>
  </form>
</body>
</html>`))

var dashboardTemplate = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Preview Publisher</title>
  <style>
    body { margin: 0; font-family: system-ui, sans-serif; color: #151922; background: #f6f7f9; }
    header { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 1rem 1.25rem; background: #ffffff; border-bottom: 1px solid #d9dde5; }
    main { padding: 1.25rem; overflow-x: auto; }
    h1 { margin: 0; font-size: 1.1rem; }
    table { width: 100%; border-collapse: collapse; background: #ffffff; }
    th, td { padding: 0.75rem; border-bottom: 1px solid #e2e5eb; text-align: left; vertical-align: top; white-space: nowrap; }
    th { font-size: 0.8rem; color: #596273; background: #f0f2f5; }
    button { font: inherit; padding: 0.45rem 0.65rem; border-radius: 6px; border: 1px solid #b8bfcc; background: #fff; cursor: pointer; }
    .danger { color: #9f1d1d; }
    .url { max-width: 32rem; overflow: hidden; text-overflow: ellipsis; }
  </style>
</head>
<body>
  <header>
    <h1>Preview Publisher</h1>
    <form method="post" action="/logout"><button type="submit">Sign out</button></form>
  </header>
  <main>
    <table>
      <thead>
        <tr>
          <th>Title</th>
          <th>Slug</th>
          <th>URL</th>
          <th>Created</th>
          <th>Updated</th>
          <th>Size</th>
          <th>Files</th>
          <th>Access</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
      {{range .Previews}}
        <tr>
          <td>{{.Title}}</td>
          <td>{{.Slug}}</td>
          <td class="url"><a href="{{.PublicURL}}">{{.PublicURL}}</a> <button type="button" onclick="navigator.clipboard.writeText('{{.PublicURL}}')">Copy</button></td>
          <td>{{.CreatedAt.Format "2006-01-02 15:04"}}</td>
          <td>{{.UpdatedAt.Format "2006-01-02 15:04"}}</td>
          <td>{{.SizeBytes}}</td>
          <td>{{.FileCount}}</td>
          <td>{{if .Protected}}protected{{else}}public{{end}}</td>
          <td>
            <form method="post" action="/delete">
              <input type="hidden" name="slug" value="{{.Slug}}">
              <button class="danger" type="submit">Delete</button>
            </form>
          </td>
        </tr>
      {{else}}
        <tr><td colspan="9">No previews</td></tr>
      {{end}}
      </tbody>
    </table>
  </main>
</body>
</html>`))
