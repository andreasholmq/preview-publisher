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
