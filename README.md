# Preview Publisher

Small self-hostable app for publishing static HTML previews.

## Binaries

- `preview-gateway`: server and admin dashboard.
- `preview`: CLI for publishing, listing, deleting, and password changes.

## Server

Required environment:

```sh
PREVIEW_PUBLIC_BASE_URL=https://example.com
PREVIEW_PUBLIC_ROUTE_PREFIX=/p
PREVIEW_ADMIN_TOKEN=change-me-change-me-change-me-change-me
```

Common local run:

```sh
PREVIEW_PUBLIC_BASE_URL=http://localhost:8080 \
PREVIEW_PUBLIC_ROUTE_PREFIX=/p \
PREVIEW_ADMIN_TOKEN=change-me-change-me-change-me-change-me \
PREVIEW_DATA_DIR=./data \
go run ./cmd/preview-gateway
```

The dashboard is served at `/`. The public preview route is configurable and must be a non-root path such as `/p`.

## CLI

```sh
export PREVIEW_GATEWAY_URL=http://localhost:8080
export PREVIEW_GATEWAY_TOKEN=change-me-change-me-change-me-change-me

preview publish ./demo.html
preview publish ./site-folder --password
preview publish ./demo.html --password client-demo --slug checkout-redesign
preview list
preview delete checkout-redesign
preview password set checkout-redesign
preview password remove checkout-redesign
```

Directories must contain root `index.html`. Single-file publishing accepts one `.html` file and uploads it as `index.html`.

## Docker

```sh
docker build -t preview-publisher .
docker run --rm -p 8080:8080 \
  -v "$PWD/data:/data" \
  -e PREVIEW_PUBLIC_BASE_URL=http://localhost:8080 \
  -e PREVIEW_PUBLIC_ROUTE_PREFIX=/p \
  -e PREVIEW_ADMIN_TOKEN=change-me-change-me-change-me-change-me \
  preview-publisher
```

See [docker-compose.example.yml](docker-compose.example.yml).

## Development

```sh
go test ./...
go build ./cmd/preview-gateway
go build ./cmd/preview
```

## Operational usage receipts

Publish, replacement, deletion and password metadata operations append a minimal
receipt in the same SQLite transaction as their metadata change. The outbox
contains a random event ID, UTC occurrence time and a bounded action only; it
contains no slug, title, URL, password or file content. Successful publish and
replacement receipts remain available after a preview is deleted. Homelab reads
the approved projection from the local database; it never derives publish usage
from current preview stock. Failed receipt writes roll back metadata changes.

Image publication uses GitHub OIDC after Go tests pass. This public repository
runs a publish-only workflow because it cannot invoke homelab's private reusable
workflow. GitHub has only non-secret identity/client IDs; scoped Zot publisher
credentials live in `release-preview-publisher` in Infisical. The OIDC identity is
bound to this repository, its production environment, main ref and release
workflow. Publication creates only the tested immutable SHA tag. Homelab pins
that tag in Compose and Dokploy deploys its inventory-owned configuration. The
publisher receives no Dokploy credential and does not advance a moving tag.
