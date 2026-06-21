# Preview Publisher MVP Plan

## Purpose

Build a small open-source app for publishing static HTML previews.

The app should make this workflow easy:

```bash
preview publish ./demo.html
preview publish ./site-folder --password
```

It should return a public share URL, and optionally a password. The app is
generic and self-hostable. The runtime must not bake in deployment-platform,
private-network, or personal-domain assumptions.

## Product Boundary

This repository owns:

- Go server and CLI source code.
- Dockerfile and example Docker Compose file.
- Tests.
- Documentation.
- GitHub Actions that build and push the image to Zot.

Deployment configuration for a specific server lives outside this repo. The
app should only need generic environment variables.

## MVP Scope

MVP includes:

- Go server binary named `preview-gateway`.
- Go CLI command named `preview`.
- Static preview upload from a directory or a single HTML file.
- Public preview serving under a configurable non-root route prefix.
- Optional per-preview password protection.
- Thin admin dashboard.
- Token-protected admin API.
- SQLite metadata database.
- Filesystem storage.
- Docker image build.
- GitHub Actions workflow that pushes `:main` to Zot.

MVP excludes:

- Vite, Node, or framework build detection.
- SPA fallback.
- TTL and automatic cleanup.
- Soft deletes.
- Public image publishing to GHCR.
- S3/R2 storage drivers.
- Basic Auth.
- Rate limiting.
- Config files for the CLI.
- Root-path preview serving.

## Runtime Design

Use Go 1.24 or newer.

The server should be one binary:

```txt
preview-gateway
```

Use the standard library where practical. A small router such as `chi` is fine
if it keeps routing simpler.

Use Go-rendered HTML for the admin dashboard and password gate. Prefer either:

- `html/template` for maximum simplicity, or
- `templ` if typed components materially improve maintainability.

Use only tiny htmx-style interactivity if needed. Do not add a TypeScript,
Vite, React, Vue, or Svelte frontend for MVP.

Use SQLite for metadata with the pure-Go driver:

```txt
modernc.org/sqlite
```

Use filesystem storage for preview files.

## Configuration

Environment variables:

```txt
PREVIEW_LISTEN_ADDR=0.0.0.0:8080
PREVIEW_PUBLIC_BASE_URL=https://example.com
PREVIEW_PUBLIC_ROUTE_PREFIX=/p
PREVIEW_ADMIN_BASE_URL=https://previews.example.com
PREVIEW_DATA_DIR=/data
PREVIEW_ADMIN_TOKEN=<at-least-32-chars>
PREVIEW_MAX_UPLOAD_MB=200
PREVIEW_MAX_FILE_COUNT=20000
PREVIEW_STORAGE_DRIVER=filesystem
PREVIEW_LOG_LEVEL=info
```

Rules:

- `PREVIEW_PUBLIC_ROUTE_PREFIX` must be a non-root path such as `/p`.
- Root preview serving with `/` is out of scope for MVP.
- `PREVIEW_ADMIN_TOKEN` is required.
- The server refuses to start if `PREVIEW_ADMIN_TOKEN` is missing or too short.
- No `AUTH_MODE=none` in MVP.
- Cookie signing secret is generated and persisted under `PREVIEW_DATA_DIR` on
  first start.
- `PREVIEW_COOKIE_SECRET` may exist as an optional advanced override, but it is
  not required for normal deployments.

## Storage Layout

```txt
/data/
  preview-publisher.db
  secrets/
    cookie-secret
  previews/
    <slug>/
      files/
        index.html
        ...
  tmp/
```

SQLite schema should be created and migrated automatically on startup.

Initial metadata fields:

```sql
create table previews (
  slug text primary key,
  title text,
  public_url text not null,
  password_hash text,
  created_at text not null,
  updated_at text not null,
  size_bytes integer not null,
  file_count integer not null
);
```

Notes:

- `password_hash` is null for public previews.
- Password hashes use salted Argon2id.
- No expiry fields in MVP.
- No deleted-at field in MVP.

## Public URL Model

Generated public URL:

```txt
{PREVIEW_PUBLIC_BASE_URL}{PREVIEW_PUBLIC_ROUTE_PREFIX}/{slug}/
```

Example:

```txt
PREVIEW_PUBLIC_BASE_URL=https://andreasholmqvist.se
PREVIEW_PUBLIC_ROUTE_PREFIX=/p

https://andreasholmqvist.se/p/k7m4q9z2v6txa8fb/
```

Rules:

- `GET /p/<slug>` redirects to `/p/<slug>/`.
- `GET /p/<slug>/...` serves files for that preview.
- Unknown, deleted, or missing paths return `404`.
- No SPA fallback in MVP.
- All preview responses include `X-Robots-Tag: noindex, nofollow`.
- Preview responses use `Cache-Control: no-store`.
- Preview responses include `X-Content-Type-Options: nosniff`.
- Do not add a strict CSP for preview content in MVP.

Serve a generated `robots.txt`:

```txt
User-agent: *
Disallow: /p/
```

The disallow path is generated from `PREVIEW_PUBLIC_ROUTE_PREFIX`. The
`robots.txt` response should also use `Cache-Control: no-store`.

## Slugs

Default slugs are opaque random IDs.

User-provided slugs are allowed and are validated as:

- Lowercase `a-z`, `0-9`, and `-`.
- Length 3 to 80.
- No leading hyphen.
- No trailing hyphen.
- No repeated `--`.
- Normalize to lowercase.
- Reject reserved words such as `api`, `healthz`, `admin`, `assets`, and
  `static`.

Publishing with an existing explicit slug replaces the preview atomically.

Publishing without `--slug` generates a random slug. If a generated slug
collides, the server retries internally.

## Publishing Model

Supported MVP inputs:

- Directory containing `index.html`.
- Single `.html` file.

Directory publishing:

- Recursively include regular files.
- Preserve directory structure.
- Require `index.html` at the directory root.
- Exclude symlinks.
- Do not implement include/exclude ignore rules in MVP.

Single-file publishing:

- Accept one `.html` file.
- Upload it internally as `index.html`.
- Include only that HTML file.
- Do not discover or upload nearby assets.

The CLI should warn, but not fail, when HTML contains root-absolute references
such as:

```html
<link rel="stylesheet" href="/assets/app.css">
<script src="/assets/app.js"></script>
```

These often break when served under `/p/<slug>/`.

## Artifact Format

CLI uploads a `tar.gz` artifact as multipart form data.

Server extraction must reject:

- Absolute paths.
- `..` traversal.
- Symlinks.
- Hardlinks.
- Device files.
- Uploads over `PREVIEW_MAX_UPLOAD_MB`.
- Uploads over `PREVIEW_MAX_FILE_COUNT`.

Publish flow:

1. Receive uploaded `tar.gz`.
2. Validate archive entries while extracting to a staging directory.
3. Count files and bytes.
4. Upsert metadata in SQLite.
5. Atomically replace the final preview directory.
6. Remove temporary files.

Same-slug replacement must not corrupt the currently served preview if upload
or validation fails.

## Password-Protected Previews

MVP uses server-enforced password-only protection, not Basic Auth.

User experience:

- Publisher can create a public preview.
- Publisher can create a protected preview.
- Protected preview visitors see a minimal centered password input page.
- The password page has only the password input box centered on the page.
- After successful entry, the user can browse the preview files.

Password behavior:

- Password protection is optional per preview.
- The server can generate a password.
- The publisher can provide a password.
- Store only salted Argon2id hashes.
- Return generated passwords once.
- Never reveal existing passwords later.

Cookie behavior:

- On successful password entry, set a signed HTTP-only cookie scoped to the
  preview path, for example `/p/<slug>/`.
- Use `Secure` when served over HTTPS.
- Use `SameSite=Lax`.
- Cookie lifetime is fixed at 30 days.
- Do not make cookie lifetime configurable in MVP.

No rate limiting in MVP.

## Admin API

All admin API routes require:

```txt
Authorization: Bearer <PREVIEW_ADMIN_TOKEN>
```

Routes:

```txt
GET    /healthz
GET    /api/previews
POST   /api/previews
GET    /api/previews/{slug}
DELETE /api/previews/{slug}
POST   /api/previews/{slug}/password
DELETE /api/previews/{slug}/password
```

No public upload endpoint exists. Publishing always requires the admin token.

### Publish Request

`POST /api/previews`

Multipart form:

```txt
artifact       tar.gz
slug           optional
title          optional
password_mode  none | generated | provided
password       required only when password_mode=provided
```

Response:

```json
{
  "slug": "k7m4q9z2v6txa8fb",
  "public_url": "https://example.com/p/k7m4q9z2v6txa8fb/",
  "title": "presentation",
  "protected": true,
  "created": true,
  "replaced": false,
  "password": "shown-once-if-generated"
}
```

Rules:

- Include `password` only when a password was generated or newly set.
- Include `created` and `replaced` so clients know what happened.
- Omit `ttl_hours`, `expires_at`, and source reference fields from MVP.

### Password API

`POST /api/previews/{slug}/password`

Request:

```json
{
  "password": "optional-explicit-password"
}
```

If `password` is omitted or empty, the server generates a new password.

Response:

```json
{
  "slug": "k7m4q9z2v6txa8fb",
  "protected": true,
  "password": "shown-once"
}
```

`DELETE /api/previews/{slug}/password` removes password protection.

## Admin Dashboard

The dashboard is secondary for MVP.

Serve it at `/` on the admin origin. API routes live under `/api/*`.

Dashboard access:

- Simple token login form.
- Successful login sets a signed admin session cookie.
- Admin session cookie lifetime is fixed at 30 days.
- API bearer token use is unaffected.

MVP dashboard features:

- List previews.
- Show title, slug, public URL, created/updated time, size, file count, and
  protected/public state.
- Copy public URL.
- Delete preview.

Do not include dashboard publish, upload, TTL extension, password reveal, or
password rotation UI in MVP.

## CLI

Ship a one-word CLI:

```txt
preview
```

Primary workflow:

```bash
preview publish [path]
```

`[path]` defaults to the current directory.

Examples:

```bash
preview publish
preview publish dist/
preview publish presentation.html
preview publish presentation.html --password
preview publish presentation.html --password "client-demo"
preview publish presentation.html --slug checkout-redesign
preview publish presentation.html --title "Checkout redesign"
```

Environment defaults:

```txt
PREVIEW_GATEWAY_URL=https://previews.example.com
PREVIEW_GATEWAY_TOKEN=<token>
```

Flags override environment variables:

```bash
preview publish --server https://previews.example.com --token "$TOKEN"
```

MVP commands:

```bash
preview publish [path]
preview list
preview delete <slug>
preview password set <slug> [password]
preview password remove <slug>
```

Password CLI behavior:

- `preview publish --password` asks the server to generate a password.
- `preview publish --password <value>` uses the provided password.
- `preview password set <slug>` asks the server to generate a new password.
- `preview password set <slug> <value>` uses the provided password.
- Generated passwords are printed once.

No CLI config file in MVP. Use env vars and flags only.

CLI help text is part of the MVP. Each command and flag should have clear,
discoverable help output.

## Docker

Provide:

- `Dockerfile`
- `docker-compose.example.yml`

Dockerfile shape:

```dockerfile
FROM golang:<pinned-version> AS build
FROM gcr.io/distroless/static-debian12:<pinned-version>
```

Runtime:

- Non-root user.
- Persistent data mounted at `/data`.
- Server listens on `0.0.0.0:8080` by default.

The example Compose file should show:

- Image reference placeholder.
- Port mapping.
- Persistent data volume.
- Required `PREVIEW_ADMIN_TOKEN`.
- `PREVIEW_PUBLIC_BASE_URL`.
- `PREVIEW_PUBLIC_ROUTE_PREFIX`.
- `PREVIEW_ADMIN_BASE_URL`.
- Upload limit configuration.

## GitHub Actions

Add a workflow that:

1. Checks out the repo.
2. Runs Go tests.
3. Builds the Linux amd64 Docker image.
4. Joins Tailscale so the runner can reach Zot.
5. Logs in to Zot.
6. Pushes:

```txt
zot.polecat-polaris.ts.net/preview-publisher:main
zot.polecat-polaris.ts.net/preview-publisher:<git-sha>
```

The app repo CI stops after pushing the image to Zot. It only needs credentials
required to push that image.

Use existing Zot-compatible Buildx settings:

```txt
oci-mediatypes=false
provenance=false
sbom=false
```

## Tests

Unit and integration tests should cover:

- Slug validation.
- Random slug collision retry.
- Admin token required for API routes.
- Server refuses to start without a valid admin token.
- Directory publishing requires root `index.html`.
- Single `.html` publishing maps to `index.html`.
- Archive safety rejects traversal, absolute paths, symlinks, hardlinks, and
  device files.
- Upload size and file-count limits.
- Atomic replacement keeps old preview when new upload fails.
- Public preview serving.
- Unknown preview returns `404`.
- Missing path returns `404`.
- Password-protected preview shows password page.
- Correct password sets scoped signed cookie.
- Incorrect password does not set cookie.
- Password hashes are not reversible and use unique salts.
- Password set and remove APIs.
- CLI packaging for directory and single-file inputs.
- CLI root-absolute asset warning.
- CLI help output exists for all MVP commands.

## Acceptance Criteria

MVP is complete when:

- `preview-gateway` starts with SQLite and filesystem storage.
- `preview publish presentation.html` returns a working public URL.
- `preview publish ./site-folder` uploads a directory with assets.
- `preview publish demo.html --password` returns URL and password.
- Visiting a protected preview shows a centered password input page.
- Entering the correct password unlocks the preview for 30 days.
- `preview list` shows published previews.
- `preview delete <slug>` removes a preview and files immediately.
- `preview password set <slug>` generates and returns a new password.
- `preview password remove <slug>` makes a preview public.
- Admin dashboard lists previews and can delete them.
- Docker image builds.
- Example Compose can run the server.
- GitHub Actions pushes the `:main` image to Zot.
